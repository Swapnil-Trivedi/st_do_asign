package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type proxyHandler struct {
	cache   *PolicyCache
	limiter *Limiter
	obs     *trafficObs

	mu      sync.Mutex
	proxies map[string]*httputil.ReverseProxy
}

func newProxyHandler(cache *PolicyCache, limiter *Limiter) *proxyHandler {
	return &proxyHandler{
		cache:   cache,
		limiter: limiter,
		obs:     newTrafficObs(500),
		proxies: map[string]*httputil.ReverseProxy{},
	}
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		inst := envOr("INSTANCE_ID", "1")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "instance": inst})
		return
	}
	if r.URL.Path == "/api/v1/quota" {
		h.serveQuota(w, r)
		return
	}
	if r.URL.Path == "/api/v1/traffic" || r.URL.Path == "/api/v1/traffic/reset" {
		return
	}

	start := time.Now()
	reqID := middleware.GetReqID(r.Context())

	snap := h.cache.Snapshot()
	if snap == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "policy not loaded"})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "error", Status: http.StatusServiceUnavailable,
			LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	if len(snap.Namespaces) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "open",
			"message": "no rate-limit namespaces configured",
		})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "open", Status: http.StatusOK,
			LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	matched, ok := matchRequest(snap, r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no matching namespace"})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "no_match", Status: http.StatusNotFound,
			LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	ns := matched.Namespace
	res, err := h.limiter.Evaluate(r.Context(), ns.Namespace.Algorithm, ns.Namespace.ID, matched.Subject, ns.Config.Params)
	if err != nil {
		log.Printf("rate limit evaluate error (fail-closed): ns=%s algo=%s subject=%s err=%v",
			ns.Namespace.ID, ns.Namespace.Algorithm, matched.Subject, err)
		w.Header().Set("X-RateLimit-Error", "redis")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit unavailable"})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "error",
			Namespace: ns.Namespace.Label, Algorithm: ns.Namespace.Algorithm, Subject: matched.Subject,
			Status: http.StatusTooManyRequests, LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	setQuotaHeaders(w, res)
	w.Header().Set("X-Proxy-Instance", envOr("INSTANCE_ID", "1"))
	if !res.Allowed {
		if res.ResetMs > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt((res.ResetMs+999)/1000, 10))
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":     "rate limit exceeded",
			"namespace": ns.Namespace.Label,
			"algorithm": ns.Namespace.Algorithm,
			"limit":     res.Limit,
			"remaining": res.Remaining,
			"resetMs":   res.ResetMs,
		})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "deny",
			Namespace: ns.Namespace.Label, Algorithm: ns.Namespace.Algorithm, Subject: matched.Subject,
			Status: http.StatusTooManyRequests, Limit: res.Limit, Remaining: res.Remaining, ResetMs: res.ResetMs,
			LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	upstream, err := url.Parse(ns.Namespace.UpstreamHost)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid upstreamHost"})
		h.obs.record(TrafficEvent{
			Method: r.Method, Path: r.URL.Path, Decision: "error",
			Namespace: ns.Namespace.Label, Algorithm: ns.Namespace.Algorithm, Subject: matched.Subject,
			Status: http.StatusBadGateway, LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
		})
		return
	}

	proxy := h.reverseProxy(upstream)
	origPath := r.URL.Path
	if ns.Namespace.Path != nil && *ns.Namespace.Path != "" {
		r.URL.Path = *ns.Namespace.Path
	}
	capW := &statusCapture{ResponseWriter: w, status: http.StatusOK}
	proxy.ServeHTTP(capW, r)
	r.URL.Path = origPath
	h.obs.record(TrafficEvent{
		Method: r.Method, Path: origPath, Decision: "allow",
		Namespace: ns.Namespace.Label, Algorithm: ns.Namespace.Algorithm, Subject: matched.Subject,
		Status: capW.status, Limit: res.Limit, Remaining: res.Remaining, ResetMs: res.ResetMs,
		LatencyMs: time.Since(start).Milliseconds(), RequestID: reqID,
	})
}

func (h *proxyHandler) reverseProxy(target *url.URL) *httputil.ReverseProxy {
	key := target.String()
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.proxies[key]; ok {
		return p
	}
	p := httputil.NewSingleHostReverseProxy(target)
	origDirector := p.Director
	p.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = target.Host
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		req.Header.Set("X-Forwarded-Proto", "http")
	}
	p.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("upstream error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream unavailable"})
	}
	h.proxies[key] = p
	return p
}

func setQuotaHeaders(w http.ResponseWriter, res LimitResult) {
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(res.Limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(res.Remaining, 10))
	if res.ResetMs > 0 {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(res.ResetMs, 10))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
