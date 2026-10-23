package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type api struct {
	store           *runStore
	defaultBaseURL  string
}

func (a *api) routes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// UI discovery — profile knobs + defaults for the Data Plane tab.
	r.Get("/api/v1/profiles", a.listProfiles)
	r.Get("/api/v1/defaults", a.defaults)
	r.Get("/api/v1/scenarios", a.listScenarios)
	r.Post("/api/v1/scenarios/{id}/run", a.runScenario)

	r.Route("/api/v1/runs", func(r chi.Router) {
		r.Get("/", a.listRuns)
		r.Post("/", a.createRun)
		r.Get("/{id}", a.getRun)
		r.Post("/{id}/stop", a.stopRun)
	})
}

func (a *api) listProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"profiles": []ProfileDescriptor{
			{
				Name:        ProfileSteady,
				Label:       "Steady",
				Description: "Constant RPS for a fixed duration",
				Defaults: map[string]any{
					"rps": 20, "duration": "30s",
				},
			},
			{
				Name:        ProfileRamp,
				Label:       "Ramp up",
				Description: "Linear RPS increase across steps",
				Defaults: map[string]any{
					"fromRps": 5, "toRps": 50, "steps": 5, "stepDuration": "5s",
				},
			},
			{
				Name:        ProfileBurst,
				Label:       "Burst",
				Description: "Base RPS with periodic high-RPS spikes",
				Defaults: map[string]any{
					"baseRps": 10, "burstRps": 80, "burstEvery": "10s",
					"burstDuration": "2s", "duration": "60s",
				},
			},
		},
	})
}

func (a *api) defaults(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"target": TargetSpec{
			BaseURL: a.defaultBaseURL,
			Method:  http.MethodGet,
			Path:    "/1001/deviceList",
			Headers: map[string]string{
				"US-API-Key": "ak_demo_1001_alpha",
			},
		},
		"note": "Traffic should target the rate-limiter service name on the compose network; mock-server is the upstream behind it.",
	})
}

func (a *api) listScenarios(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"scenarios": builtInScenarios(a.defaultBaseURL),
		"note":      "POST /api/v1/scenarios/{id}/run to start immediately. Base URL uses RATE_LIMITER_URL.",
	})
}

func (a *api) runScenario(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sc, ok := scenarioByID(id, a.defaultBaseURL)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown scenario id"})
		return
	}
	view, err := a.store.start(sc.Request, a.defaultBaseURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"scenarioId": sc.ID,
		"scenario":   sc.Name,
		"run":        view,
	})
}

func (a *api) listRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"runs": a.store.list()})
}

func (a *api) createRun(w http.ResponseWriter, r *http.Request) {
	var req CreateRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	view, err := a.store.start(req, a.defaultBaseURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	view, err := a.store.get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) stopRun(w http.ResponseWriter, r *http.Request) {
	view, err := a.store.stop(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
