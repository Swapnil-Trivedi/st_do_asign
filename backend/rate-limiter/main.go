package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/redis/go-redis/v9"
)

const defaultAddr = "0.0.0.0:8080"

func main() {
	addr := envOr("RATE_LIMITER_ADDR", defaultAddr)
	mongoURI := envOr("MONGO_URI", "mongodb://root:stroot@mongo:27017/?authSource=admin")
	dbName := envOr("MONGO_DB", "rate_limiter")
	redisAddr := envOr("REDIS_ADDR", "redis:6379")
	pollInterval := envDuration("POLICY_POLL_INTERVAL", 2*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bootCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	policy, err := newMongoPolicy(bootCtx, mongoURI, dbName)
	if err != nil {
		log.Fatalf("mongo: %v", err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = policy.Close(c)
	}()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(bootCtx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	limiter := newLimiter(rdb)

	cache := newPolicyCache(policy, pollInterval)
	if err := cache.LoadOnce(bootCtx); err != nil {
		log.Fatalf("policy load: %v", err)
	}
	go cache.Start(ctx)

	handler := newProxyHandler(cache, limiter)
	tests := &selftestRunner{cache: cache, limiter: limiter}
	instanceID := envOr("INSTANCE_ID", "1")
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "US-API-Key"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Get("/healthz", handler.ServeHTTP)
	r.Get("/api/v1/quota", handler.serveQuota)
	r.Delete("/api/v1/quota", handler.serveQuota)
	r.Post("/api/v1/quota/reset", handler.resetQuota)
	r.Get("/api/v1/traffic", handler.serveTraffic)
	r.Post("/api/v1/traffic/reset", handler.resetTraffic)
	r.Get("/api/v1/tests/catalog", tests.listTestCatalog)
	r.Post("/api/v1/tests/run", tests.runTests)
	r.HandleFunc("/*", handler.ServeHTTP)

	srv := &http.Server{Addr: addr, Handler: r}
	go func() {
		log.Printf("rate-limiter[%s] listening on http://%s", instanceID, addr)
		log.Printf("  redis=%s mongo_db=%s poll=%s", redisAddr, dbName, pollInterval)
		log.Printf("  GET|DELETE /api/v1/quota?namespace=<id|label>&apiKey=…")
		log.Printf("  POST /api/v1/quota/reset?apiKey=…  (all namespaces)")
		log.Printf("  GET /api/v1/traffic  POST /api/v1/traffic/reset")
		log.Printf("  GET /api/v1/tests/catalog  POST /api/v1/tests/run?suite=unit|functional|all")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
	_ = rdb.Close()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("invalid %s=%q — using %s", key, v, fallback)
		return fallback
	}
	return d
}
