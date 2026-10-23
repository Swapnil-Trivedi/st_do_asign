package main

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const defaultAddr = "0.0.0.0:8095"

func main() {
	addr := envOr("CONSOLE_API_ADDR", defaultAddr)
	orch := newOrchestrator()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type"},
		MaxAge:         300,
	}))

	api := &api{orch: orch, e2e: newE2ERunner()}
	api.routes(r)

	log.Printf("console-api listening on http://%s", addr)
	log.Printf("project_root=%s (Docker Engine API via DOCKER_HOST/socket)", orch.projectRoot)
	log.Printf("managed services: %v", managedServices)
	log.Printf("  POST /api/v1/pipeline/up|down|build")
	log.Printf("  POST /api/v1/services/{name}/up|down|build")
	log.Printf("  GET  /api/v1/pipeline/status")
	log.Printf("  GET  /api/v1/jobs/{id}")
	log.Printf("  GET  /api/v1/e2e/catalog")
	log.Printf("  POST /api/v1/e2e/run")

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
