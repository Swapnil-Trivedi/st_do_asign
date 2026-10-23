package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type api struct {
	orch *orchestrator
}

func (a *api) routes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Get("/api/v1/catalog", a.catalog)
	r.Get("/api/v1/pipeline/status", a.pipelineStatus)
	r.Get("/api/v1/infra/health", a.infraHealth)

	r.Post("/api/v1/pipeline/up", a.pipelineUp)
	r.Post("/api/v1/pipeline/down", a.pipelineDown)
	r.Post("/api/v1/pipeline/build", a.pipelineBuild)

	r.Post("/api/v1/services/{name}/up", a.serviceUp)
	r.Post("/api/v1/services/{name}/down", a.serviceDown)
	r.Post("/api/v1/services/{name}/build", a.serviceBuild)

	r.Get("/api/v1/jobs", a.listJobs)
	r.Get("/api/v1/jobs/{id}", a.getJob)
}

func (a *api) catalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"managedServices": managedServices,
		"projectRoot":     a.orch.projectRoot,
		"runtime":         "scratch + Docker Engine API (docker.sock)",
		"actions": map[string]string{
			"pipeline.up":    "Pull/build (optional) and start mongo, redis, mock-server, rate-limiter-a/b, rate-limiter (LB), vegeta-client, control-plane",
			"pipeline.down":  "Stop all managed services (infra stop keeps volumes)",
			"pipeline.build": "Build scratch images / pull infra images",
			"service.up":     "Start one service (rebuild or pull optional)",
			"service.down":   "Stop one service",
			"service.build":  "Build scratch image or pull infra image",
		},
	})
}

func (a *api) pipelineStatus(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	services, raw, err := a.orch.status(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       false,
			"error":    err.Error(),
			"raw":      raw,
			"services": services,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"services": services,
		"raw":      raw,
	})
}

func (a *api) infraHealth(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"services": a.orch.infraHealth(ctx),
	})
}

type upBody struct {
	Rebuild bool `json:"rebuild"`
}

func (a *api) pipelineUp(w http.ResponseWriter, r *http.Request) {
	var body upBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	job := a.orch.pipelineUp(body.Rebuild)
	writeJSON(w, http.StatusAccepted, job)
}

func (a *api) pipelineDown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, a.orch.pipelineDown())
}

func (a *api) pipelineBuild(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, a.orch.pipelineBuild())
}

func (a *api) serviceUp(w http.ResponseWriter, r *http.Request) {
	var body upBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	job, err := a.orch.serviceUp(chi.URLParam(r, "name"), body.Rebuild)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *api) serviceDown(w http.ResponseWriter, r *http.Request) {
	job, err := a.orch.serviceDown(chi.URLParam(r, "name"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *api) serviceBuild(w http.ResponseWriter, r *http.Request) {
	job, err := a.orch.serviceBuild(chi.URLParam(r, "name"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *api) listJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": a.orch.listJobs()})
}

func (a *api) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := a.orch.getJob(chi.URLParam(r, "id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
