package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (a *api) e2eRoutes(r chi.Router) {
	r.Get("/api/v1/e2e/catalog", a.e2eCatalog)
	r.Post("/api/v1/e2e/run", a.e2eRun)
	r.Get("/api/v1/e2e/runs", a.e2eListRuns)
	r.Get("/api/v1/e2e/runs/{id}", a.e2eGetRun)
	r.Get("/api/v1/e2e/runs/{id}/report.html", a.e2eGetRunHTML)
}

func (a *api) e2eCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.e2e.catalog())
}

func (a *api) e2eRun(w http.ResponseWriter, r *http.Request) {
	var req e2eRunRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	// Flush headers early so proxies/browsers keep the long sync run alive.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	rep := a.e2e.run(req)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rep)
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

func (a *api) e2eGetRunHTML(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, ok := a.e2e.getReport(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	body := renderE2EReportHTML(rep)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="e2e-report-%s.html"`, id))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
