package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type api struct {
	store Store
}

func (a *api) routes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/tests/catalog", a.listTestCatalog)
		r.Post("/tests/run", a.runTests)

		r.Get("/algorithms", a.listAlgorithms)

		r.Get("/configs", a.listConfigs)
		r.Post("/configs", a.createConfig)
		r.Get("/configs/{id}", a.getConfig)
		r.Put("/configs/{id}", a.updateConfig)
		r.Delete("/configs/{id}", a.deleteConfig)

		r.Get("/namespaces", a.listNamespaces)
		r.Post("/namespaces", a.createNamespace)
		r.Get("/namespaces/{id}", a.getNamespace)
		r.Put("/namespaces/{id}", a.updateNamespace)
		r.Delete("/namespaces/{id}", a.deleteNamespace)

		r.Get("/namespaces/{id}/rules", a.listRules)
		r.Post("/namespaces/{id}/rules", a.createRule)
		r.Put("/rules/{id}", a.updateRule)
		r.Delete("/rules/{id}", a.deleteRule)
	})
}

func (a *api) listAlgorithms(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAlgorithms(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"algorithms": items})
}

func (a *api) listConfigs(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListConfigs(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": items})
}

func (a *api) getConfig(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.GetConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *api) createConfig(w http.ResponseWriter, r *http.Request) {
	var req CreateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required", "validation_error")
		return
	}
	if err := validateConfigInput(req.Algorithm, req.Params); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	c, err := a.store.CreateConfig(r.Context(), AlgorithmConfig{
		Name: req.Name, Algorithm: req.Algorithm, Params: req.Params, Enabled: enabled,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error(), "conflict")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *api) updateConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := a.store.GetConfig(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	var req UpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
	}
	if req.Algorithm != nil {
		existing.Algorithm = *req.Algorithm
	}
	if req.Params != nil {
		existing.Params = *req.Params
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if err := validateConfigInput(existing.Algorithm, existing.Params); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}
	updated, err := a.store.UpdateConfig(r.Context(), existing)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error(), "update_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	n, err := a.store.CountNamespacesByConfig(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "config is referenced by one or more namespaces", "in_use")
		return
	}
	if err := a.store.DeleteConfig(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listNamespaces(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListNamespaces(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespaces": items})
}

func (a *api) getNamespace(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.GetNamespace(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (a *api) createNamespace(w http.ResponseWriter, r *http.Request) {
	var req CreateNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	if req.MatchMode == "" {
		req.MatchMode = MatchModeAll
	}
	if err := validateNamespaceInput(req.Label, req.UpstreamHost, req.MatchMode, req.ConfigID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}
	cfg, err := a.store.GetConfig(r.Context(), req.ConfigID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "configId not found", "validation_error")
		return
	}
	if !cfg.Enabled {
		writeErr(w, http.StatusBadRequest, "config is disabled", "validation_error")
		return
	}
	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	n, err := a.store.CreateNamespace(r.Context(), Namespace{
		Label: strings.TrimSpace(req.Label), UpstreamHost: strings.TrimSpace(req.UpstreamHost),
		Path: req.Path, Algorithm: cfg.Algorithm, ConfigID: cfg.ID,
		MatchMode: req.MatchMode, Priority: priority, Enabled: enabled,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error(), "create_failed")
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (a *api) updateNamespace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := a.store.GetNamespace(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	var req UpdateNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	if req.Label != nil {
		existing.Label = strings.TrimSpace(*req.Label)
	}
	if req.UpstreamHost != nil {
		existing.UpstreamHost = strings.TrimSpace(*req.UpstreamHost)
	}
	if req.Path != nil {
		existing.Path = req.Path
	}
	if req.ConfigID != nil {
		cfg, err := a.store.GetConfig(r.Context(), *req.ConfigID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "configId not found", "validation_error")
			return
		}
		existing.ConfigID = cfg.ID
		existing.Algorithm = cfg.Algorithm
	}
	if req.MatchMode != nil {
		existing.MatchMode = *req.MatchMode
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if err := validateNamespaceInput(existing.Label, existing.UpstreamHost, existing.MatchMode, existing.ConfigID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}
	updated, err := a.store.UpdateNamespace(r.Context(), existing)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error(), "update_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteNamespace(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteNamespace(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listRules(w http.ResponseWriter, r *http.Request) {
	nsID := chi.URLParam(r, "id")
	if _, err := a.store.GetNamespace(r.Context(), nsID); err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	items, err := a.store.ListRules(r.Context(), nsID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error(), "store_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": items})
}

func (a *api) createRule(w http.ResponseWriter, r *http.Request) {
	nsID := chi.URLParam(r, "id")
	if _, err := a.store.GetNamespace(r.Context(), nsID); err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	var req CreateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	if err := validateRuleMatch(req.Match); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rule, err := a.store.CreateRule(r.Context(), Rule{
		NamespaceID: nsID, Label: req.Label, Enabled: enabled, Match: req.Match,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "create_failed")
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (a *api) updateRule(w http.ResponseWriter, r *http.Request) {
	existing, err := a.store.GetRule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	var req UpdateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "bad_request")
		return
	}
	if req.Label != nil {
		existing.Label = *req.Label
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Match != nil {
		if err := validateRuleMatch(*req.Match); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error(), "validation_error")
			return
		}
		existing.Match = *req.Match
	}
	updated, err := a.store.UpdateRule(r.Context(), existing)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "update_failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error(), "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, APIError{Error: msg, Code: code})
}
