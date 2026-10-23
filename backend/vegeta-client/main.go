package main

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const (
	defaultAddr    = "0.0.0.0:8090"
	defaultTarget  = "http://rate-limiter:8080" // compose DNS name (proxy); override via env
)

func main() {
	addr := envOr("VEGETA_CLIENT_ADDR", defaultAddr)
	// Prefer rate-limiter hostname on the compose network; fall back for bare-metal demos.
	targetBase := envOr("RATE_LIMITER_URL", envOr("TARGET_BASE_URL", defaultTarget))

	store := newRunStore()
	api := &api{store: store, defaultBaseURL: targetBase}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	api.routes(r)

	log.Printf("vegeta-client listening on http://%s", addr)
	log.Printf("default target base (via rate limiter): %s", targetBase)
	log.Printf("profiles: steady | ramp | burst")
	log.Printf("  GET  /api/v1/profiles")
	log.Printf("  POST /api/v1/runs")
	log.Printf("  GET  /api/v1/runs/{id}")
	log.Printf("  POST /api/v1/runs/{id}/stop")

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
