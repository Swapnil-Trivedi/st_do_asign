package main

import (
	"net/http"
	"strings"
)

// GET /api/v1/quota — peek (no consume)
// DELETE /api/v1/quota — clear Redis keys for one namespace+subject
// POST /api/v1/quota/reset — clear Redis keys for subject across all loaded namespaces
//
// Query (one of):
//   - namespace=<id|label>&subject=<redis subject>
//   - namespace=<id|label>&apiKey=<value>           → subject hdr:US-API-Key:<value>
//   - namespace=<id|label>&header=<name>&value=<v> → subject hdr:<name>:<v>
//   - namespace=<id|label>&tenant=<id>             → subject tenant:<id>
//   - namespace=<id|label>&path=<path>             → subject path:<path>
// For POST /api/v1/quota/reset, namespace is optional (omit = all namespaces).
func (h *proxyHandler) serveQuota(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.peekQuota(w, r)
	case http.MethodDelete:
		h.clearQuota(w, r, false)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *proxyHandler) resetQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	h.clearQuota(w, r, true)
}

func (h *proxyHandler) peekQuota(w http.ResponseWriter, r *http.Request) {
	snap := h.cache.Snapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "policy not loaded"})
		return
	}

	nsKey := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if nsKey == "" {
		nsKey = strings.TrimSpace(r.URL.Query().Get("namespaceId"))
	}
	if nsKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "namespace query param required (id or label)",
		})
		return
	}

	ns, ok := findNamespace(snap, nsKey)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "namespace not found or disabled"})
		return
	}

	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	if subject == "" {
		subject = buildSubjectFromQuery(r)
	}
	if subject == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "subject required (subject=… or apiKey=… / header+value / tenant=… / path=…)",
		})
		return
	}

	state, err := h.limiter.Peek(r.Context(), ns.Namespace.Algorithm, ns.Namespace.ID, subject, ns.Config.Params)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "quota peek failed", "detail": err.Error()})
		return
	}
	state.NamespaceID = ns.Namespace.ID
	state.NamespaceLabel = ns.Namespace.Label
	state.Params = ns.Config.Params
	writeJSON(w, http.StatusOK, state)
}

func (h *proxyHandler) clearQuota(w http.ResponseWriter, r *http.Request, allNamespaces bool) {
	snap := h.cache.Snapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "policy not loaded"})
		return
	}

	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	if subject == "" {
		subject = buildSubjectFromQuery(r)
	}
	if subject == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "subject required (subject=… or apiKey=… / header+value / tenant=… / path=…)",
		})
		return
	}

	var targets []CompiledNamespace
	if allNamespaces {
		nsKey := strings.TrimSpace(r.URL.Query().Get("namespace"))
		if nsKey == "" {
			nsKey = strings.TrimSpace(r.URL.Query().Get("namespaceId"))
		}
		if nsKey != "" {
			ns, ok := findNamespace(snap, nsKey)
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "namespace not found or disabled"})
				return
			}
			targets = []CompiledNamespace{ns}
		} else {
			targets = append(targets, snap.Namespaces...)
		}
	} else {
		nsKey := strings.TrimSpace(r.URL.Query().Get("namespace"))
		if nsKey == "" {
			nsKey = strings.TrimSpace(r.URL.Query().Get("namespaceId"))
		}
		if nsKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "namespace query param required (id or label)",
			})
			return
		}
		ns, ok := findNamespace(snap, nsKey)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "namespace not found or disabled"})
			return
		}
		targets = []CompiledNamespace{ns}
	}

	type cleared struct {
		Namespace string `json:"namespace"`
		Algorithm string `json:"algorithm"`
		Subject   string `json:"subject"`
		RedisKey  string `json:"redisKey"`
		Deleted   int64  `json:"deleted"`
	}
	out := make([]cleared, 0, len(targets))
	var total int64
	for _, ns := range targets {
		n, err := h.limiter.Reset(r.Context(), ns.Namespace.Algorithm, ns.Namespace.ID, subject)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  "quota reset failed",
				"detail": err.Error(),
			})
			return
		}
		total += n
		out = append(out, cleared{
			Namespace: ns.Namespace.Label,
			Algorithm: ns.Namespace.Algorithm,
			Subject:   subject,
			RedisKey:  redisKey(ns.Namespace.Algorithm, ns.Namespace.ID, subject),
			Deleted:   n,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"subject": subject,
		"deleted": total,
		"cleared": out,
	})
}

func findNamespace(snap *PolicySnapshot, key string) (CompiledNamespace, bool) {
	for _, ns := range snap.Namespaces {
		if ns.Namespace.ID == key || ns.Namespace.Label == key {
			return ns, true
		}
	}
	return CompiledNamespace{}, false
}

func buildSubjectFromQuery(r *http.Request) string {
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("apiKey")); v != "" {
		return "hdr:US-API-Key:" + v
	}
	if name := strings.TrimSpace(q.Get("header")); name != "" {
		if v := strings.TrimSpace(q.Get("value")); v != "" {
			return "hdr:" + name + ":" + v
		}
	}
	if t := strings.TrimSpace(q.Get("tenant")); t != "" {
		return "tenant:" + t
	}
	if p := strings.TrimSpace(q.Get("path")); p != "" {
		return "path:" + p
	}
	return ""
}
