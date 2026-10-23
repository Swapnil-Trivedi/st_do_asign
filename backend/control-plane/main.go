package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const defaultAddr = "0.0.0.0:8091"

func main() {
	addr := envOr("CONTROL_PLANE_ADDR", defaultAddr)
	mongoURI := envOr("MONGO_URI", "mongodb://root:stroot@mongo:27017/?authSource=admin")
	dbName := envOr("MONGO_DB", "rate_limiter")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var store Store
	ms, err := newMongoStore(ctx, mongoURI, dbName)
	if err != nil {
		log.Printf("mongo unavailable (%v) — falling back to in-memory store", err)
		store = newMemoryStore()
	} else {
		store = ms
		log.Printf("connected to mongo db=%s", dbName)
	}

	a := &api{store: store}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	a.routes(r)

	log.Printf("control-plane listening on http://%s", addr)
	log.Printf("  GET/POST /api/v1/configs")
	log.Printf("  GET/POST /api/v1/namespaces")
	log.Printf("  GET/POST /api/v1/namespaces/{id}/rules")
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
