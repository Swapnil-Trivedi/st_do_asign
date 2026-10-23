package main

import (
	"net/http"
	"strings"
)

// GET /api/v1/quota
//
// Query (one of):
//   - namespace=<id|label>&subject=<redis subject>
//   - namespace=<id|label>&apiKey=<value>           → subject hdr:US-API-Key:<value>
//   - namespace=<id|label>&header=<name>&value=<v> → subject hdr:<name>:<v>
//   - namespace=<id|label>&tenant=<id>             → subject tenant:<id>
//   - namespace=<id|label>&path=<path>             → subject path:<path>
func (h *proxyHandler) serveQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

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
