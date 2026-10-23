package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const (
	defaultAddr      = "0.0.0.0:8081"
	apiKeyHeader     = "US-API-Key"
	businessDelay    = 500 * time.Millisecond
	latencyHeader    = "US-Upstream-Latency-Ms"
	upstreamHeader   = "US-Upstream-Response"
	tenantHeader     = "US-Tenant"
	mappedKeysHeader = "US-Mapped-API-Keys"
	keyMatchHeader   = "US-API-Key-Match"
)

// Mock tenant (int32) → one or more API keys (logging / observability demos).
// Keys are not validated; mismatches are logged but still served.
var tenantAPIKeys = map[int32][]string{
	1001: {
		"ak_demo_1001_alpha",
		"ak_demo_1001_mobile",
		"ak_demo_1001_partner",
	},
	1002: {
		"ak_demo_1002_beta",
		"ak_demo_1002_ci",
	},
	1003: {
		"ak_demo_1003_gamma",
	},
}

func main() {
	addr := envOr("MOCK_SERVER_ADDR", defaultAddr)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", apiKeyHeader},
		MaxAge:         300,
	}))
	r.Use(apiKeyAccessLogger)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/openapi.json", serveOpenAPI)
	r.Get("/docs", serveDocs)
	r.Get("/api/v1/logs", serveLogs)

	r.Route("/{tenantId}", func(r chi.Router) {
		r.Get("/deviceList", deviceList)
		r.Post("/registerDevice", registerDevice)
		r.Put("/registerDevice", registerDevice)
	})

	boot := fmt.Sprintf("mock-server listening on http://%s", addr)
	log.Print(boot)
	accessLogs.add("info", boot)
	accessLogs.add("info", fmt.Sprintf("auth header: %s (not validated; echoed + logged)", apiKeyHeader))
	for id, keys := range tenantAPIKeys {
		accessLogs.add("info", fmt.Sprintf("tenant=%d keys=[%s]", id, strings.Join(keys, ", ")))
	}

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func serveLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": accessLogs.snapshot(limit),
	})
}

func apiKeyAccessLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		apiKey := r.Header.Get(apiKeyHeader)
		tenantRaw := chi.URLParam(r, "tenantId")
		reqID := middleware.GetReqID(r.Context())

		msg := fmt.Sprintf(
			"access method=%s path=%s status=%d latency_ms=%d request_id=%s tenant=%s api_key=%s",
			r.Method,
			r.URL.Path,
			ww.Status(),
			time.Since(start).Milliseconds(),
			reqID,
			tenantRaw,
			displayKey(apiKey),
		)
		log.Print(msg)
		accessLogs.add("access", msg)
	})
}

func deviceList(w http.ResponseWriter, r *http.Request) {
	ctx, ok := beginUpstream(w, r)
	if !ok {
		return
	}

	time.Sleep(businessDelay)
	writeUpstreamHeaders(w, ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"tenantId": ctx.tenantID,
		"apiKey":   ctx.apiKey,
		"devices": []map[string]string{
			{"id": "dev-001", "name": "sensor-a", "status": "online"},
			{"id": "dev-002", "name": "sensor-b", "status": "offline"},
		},
	})
	logUpstreamDone(ctx)
}

func registerDevice(w http.ResponseWriter, r *http.Request) {
	ctx, ok := beginUpstream(w, r)
	if !ok {
		return
	}

	time.Sleep(businessDelay)

	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	status := http.StatusCreated
	if r.Method == http.MethodPut {
		status = http.StatusOK
	}

	writeUpstreamHeaders(w, ctx)
	writeJSON(w, status, map[string]any{
		"tenantId": ctx.tenantID,
		"apiKey":   ctx.apiKey,
		"method":   r.Method,
		"accepted": true,
		"device":   body,
	})
	logUpstreamDone(ctx)
}

type upstreamCtx struct {
	tenantID   int32
	apiKey     string
	mappedKeys []string
	keyMatch   bool
	started    time.Time
}

func beginUpstream(w http.ResponseWriter, r *http.Request) (*upstreamCtx, bool) {
	tenantRaw := chi.URLParam(r, "tenantId")
	tenantID64, err := strconv.ParseInt(tenantRaw, 10, 32)
	if err != nil {
		msg := fmt.Sprintf("reject invalid tenant_id=%q", tenantRaw)
		log.Print(msg)
		accessLogs.add("warn", msg)
		http.Error(w, `{"error":"tenantId must be an int32"}`, http.StatusBadRequest)
		return nil, false
	}
	tenantID := int32(tenantID64)

	apiKey := r.Header.Get(apiKeyHeader)
	mappedKeys, knownTenant := tenantAPIKeys[tenantID]
	keyMatch := knownTenant && apiKey != "" && slices.Contains(mappedKeys, apiKey)

	mappedKeysLog := "<none>"
	if knownTenant {
		mappedKeysLog = strings.Join(mappedKeys, ",")
	}

	msg := fmt.Sprintf(
		"upstream_start method=%s path=%s tenant=%d api_key=%s mapped_keys=[%s] key_matches_map=%t",
		r.Method, r.URL.Path, tenantID, displayKey(apiKey), mappedKeysLog, keyMatch,
	)
	log.Print(msg)
	accessLogs.add("upstream", msg)

	return &upstreamCtx{
		tenantID:   tenantID,
		apiKey:     apiKey,
		mappedKeys: mappedKeys,
		keyMatch:   keyMatch,
		started:    time.Now(),
	}, true
}

func writeUpstreamHeaders(w http.ResponseWriter, ctx *upstreamCtx) {
	latencyMs := time.Since(ctx.started).Milliseconds()
	w.Header().Set(apiKeyHeader, ctx.apiKey)
	w.Header().Set(tenantHeader, strconv.FormatInt(int64(ctx.tenantID), 10))
	w.Header().Set(latencyHeader, strconv.FormatInt(latencyMs, 10))
	w.Header().Set(upstreamHeader, "mock-server")
	if len(ctx.mappedKeys) > 0 {
		w.Header().Set(mappedKeysHeader, strings.Join(ctx.mappedKeys, ","))
	}
	w.Header().Set(keyMatchHeader, strconv.FormatBool(ctx.keyMatch))
}

func logUpstreamDone(ctx *upstreamCtx) {
	latencyMs := time.Since(ctx.started).Milliseconds()
	msg := fmt.Sprintf(
		"upstream_done tenant=%d api_key=%s latency_ms=%d key_matches_map=%t",
		ctx.tenantID, displayKey(ctx.apiKey), latencyMs, ctx.keyMatch,
	)
	log.Print(msg)
	accessLogs.add("upstream", msg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func displayKey(key string) string {
	if key == "" {
		return "<missing>"
	}
	return key
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
