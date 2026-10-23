package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *api) e2eRoutes(r chi.Router) {
	r.Get("/api/v1/e2e/catalog", a.e2eCatalog)
	r.Post("/api/v1/e2e/run", a.e2eRun)
	r.Get("/api/v1/e2e/runs", a.e2eListRuns)
	r.Get("/api/v1/e2e/runs/{id}", a.e2eGetRun)
}

func (a *api) e2eCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.e2e.catalog())
}

func (a *api) e2eRun(w http.ResponseWriter, r *http.Request) {
	var req e2eRunRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	rep := a.e2e.run(req)
	writeJSON(w, http.StatusOK, rep)
}

func (a *api) e2eListRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"runs": a.e2e.listReports()})
}

func (a *api) e2eGetRun(w http.ResponseWriter, r *http.Request) {
	rep, ok := a.e2e.getReport(chi.URLParam(r, "id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
