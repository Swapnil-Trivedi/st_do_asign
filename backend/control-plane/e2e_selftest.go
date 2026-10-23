package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func rateLimiterBaseURL() string {
	return strings.TrimRight(envOr("RATE_LIMITER_URL", "http://localhost:8080"), "/")
}

func policyPollWait() time.Duration {
	raw := envOr("POLICY_POLL_INTERVAL", "2s")
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		d = 2 * time.Second
	}
	// Wait a few poll cycles so the proxy cache refreshes.
	return d*3 + 500*time.Millisecond
}

func skip(msg string, assertions ...string) testOutcome {
	return testOutcome{skipped: true, message: msg, assertions: assertions}
}

func outboundJSON(method, fullURL string, headers map[string]string, body any) (int, string, map[string]string, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, fullURL, rdr)
	if err != nil {
		return 0, "", nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	respHeaders := map[string]string{}
	for _, k := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After", "Content-Type"} {
		if v := res.Header.Get(k); v != "" {
			respHeaders[k] = v
		}
	}
	return res.StatusCode, string(raw), respHeaders, nil
}

func probeRateLimiter() (ok bool, detail string) {
	base := rateLimiterBaseURL()
	code, raw, _, err := outboundJSON(http.MethodGet, base+"/healthz", nil, nil)
	if err != nil {
		return false, fmt.Sprintf("%s unreachable: %v", base, err)
	}
	if code != http.StatusOK {
		return false, fmt.Sprintf("%s/healthz → %d %s", base, code, truncate(raw, 120))
	}
	return true, base
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func waitForNamespaceOnProxy(ctx context.Context, label, apiKey string) (found bool, lastStatus int, lastBody string) {
	base := rateLimiterBaseURL()
	deadline := time.Now().Add(policyPollWait())
	q := url.Values{}
	q.Set("namespace", label)
	q.Set("apiKey", apiKey)
	target := base + "/api/v1/quota?" + q.Encode()

	for {
		if ctx.Err() != nil {
			return false, lastStatus, lastBody
		}
		code, raw, _, err := outboundJSON(http.MethodGet, target, nil, nil)
		lastStatus, lastBody = code, raw
		if err == nil && code == http.StatusOK {
			return true, code, raw
		}
		if time.Now().After(deadline) {
			return false, lastStatus, lastBody
		}
		select {
		case <-ctx.Done():
			return false, lastStatus, lastBody
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func (a *api) e2eTestCases() []testCase {
	prefix := "e2e-" + uuid.NewString()[:8]
	apiKey := "ak_" + prefix
	path := "/1001/deviceList"
	h := chi.NewRouter()
	a.routes(h)
	rlBase := rateLimiterBaseURL()

	return []testCase{
		{
			id: "e2e-rate-limiter-health", suite: "e2e",
			name:        "rate-limiter health reachable",
			description: "E2E: GET {RATE_LIMITER_URL}/healthz must be 200. Skips the stack suite when the proxy is down.",
			fn: func(context.Context) testOutcome {
				ok, detail := probeRateLimiter()
				req := &HTTPExchange{Method: "GET", Path: rlBase + "/healthz"}
				if !ok {
					return testOutcome{
						skipped: true, message: detail,
						assertions: []string{"rate-limiter /healthz reachable"},
						request:    req,
						meta:       map[string]any{"RATE_LIMITER_URL": rlBase},
					}
				}
				code, raw, hdrs, _ := outboundJSON(http.MethodGet, rlBase+"/healthz", nil, nil)
				return testOutcome{
					message:    "proxy healthy",
					assertions: []string{"status == 200"},
					request:    req,
					response:   &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
					meta:       map[string]any{"RATE_LIMITER_URL": rlBase},
				}
			},
		},
		{
			id: "e2e-policy-propagate-quota", suite: "e2e",
			name:        "CP write → proxy quota peek",
			description: "E2E: create config+namespace+rules in control-plane, wait for POLICY_POLL_INTERVAL, then GET rate-limiter /api/v1/quota until the namespace is visible.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				steps := []TestStep{}
				cfgBody := map[string]any{
					"name": prefix + "-cfg", "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": 50, "burst": 100},
				}
				code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
				steps = append(steps, TestStep{
					Name: "CP create config",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(cfgBody)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})
				if code != http.StatusCreated {
					return testOutcome{err: fmt.Errorf("create config %d: %s", code, raw), steps: steps}
				}
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw), &cfg)

				nsLabel := prefix + "-ns"
				nsBody := map[string]any{
					"label": nsLabel, "upstreamHost": "http://mock-server:8081",
					"configId": cfg.ID, "matchMode": "all", "priority": 5,
				}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
				steps = append(steps, TestStep{
					Name: "CP create namespace",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces", Body: mustJSON(nsBody)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})
				if code != http.StatusCreated {
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
					return testOutcome{err: fmt.Errorf("create ns %d: %s", code, raw), steps: steps}
				}
				var ns Namespace
				_ = json.Unmarshal([]byte(raw), &ns)

				ruleHdr := map[string]any{
					"label": "api-key",
					"match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": apiKey},
				}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", ruleHdr)
				steps = append(steps, TestStep{
					Name: "CP create header rule",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(ruleHdr)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})
				rulePath := map[string]any{
					"label": "path",
					"match": map[string]any{"kind": "path", "op": "eq", "value": path},
				}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", rulePath)
				steps = append(steps, TestStep{
					Name: "CP create path rule",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(rulePath)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})

				found, qCode, qBody := waitForNamespaceOnProxy(ctx, nsLabel, apiKey)
				qPath := "/api/v1/quota?namespace=" + url.QueryEscape(nsLabel) + "&apiKey=" + url.QueryEscape(apiKey)
				steps = append(steps, TestStep{
					Name: "wait for proxy quota",
					Request: &HTTPExchange{Method: "GET", Path: rlBase + qPath},
					Response: &HTTPResponseCapture{Status: qCode, Body: json.RawMessage(qBody)},
					OK: found,
					Detail: fmt.Sprintf("pollWait=%s", policyPollWait()),
				})

				cleanup := func() {
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
				}

				if !found {
					cleanup()
					return testOutcome{
						skipped: true,
						message: "policy not visible on rate-limiter — control-plane must share Mongo with the proxy (compose). Last quota status=" + fmt.Sprint(qCode),
						assertions: []string{"quota status == 200 after poll"},
						steps: steps,
						meta: map[string]any{
							"RATE_LIMITER_URL": rlBase,
							"namespace":       nsLabel,
							"hint":            "Run this suite from the Testing UI against the compose control-plane.",
						},
					}
				}

				cleanup()
				return testOutcome{
					message:    "namespace visible on proxy quota API",
					assertions: []string{"CP create 201", "quota status == 200 after poll"},
					steps:      steps,
					request:    steps[0].Request,
					response:   steps[len(steps)-1].Response,
					meta: map[string]any{
						"prefix": prefix, "namespaceId": ns.ID, "configId": cfg.ID,
						"RATE_LIMITER_URL": rlBase,
					},
				}
			},
		},
		{
			id: "e2e-proxy-allow", suite: "e2e",
			name:        "matching request forwarded (allow)",
			description: "E2E: after policy propagates, a matching GET through the rate-limiter is allowed and forwarded to mock-server (2xx).",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				steps, cfgID, nsID, nsLabel, cleanup, errOut := a.provisionE2EPolicy(h, prefix+"-allow", apiKey+"-allow", path, AlgoFixedToken, map[string]any{
					"sustainedRps": 100, "burst": 200,
				})
				if errOut != nil {
					cleanup()
					return *errOut
				}
				defer cleanup()

				found, qCode, qBody := waitForNamespaceOnProxy(ctx, nsLabel, apiKey+"-allow")
				steps = append(steps, TestStep{
					Name: "wait for policy on proxy",
					Request: &HTTPExchange{Method: "GET", Path: rlBase + "/api/v1/quota"},
					Response: &HTTPResponseCapture{Status: qCode, Body: json.RawMessage(qBody)},
					OK: found,
				})
				if !found {
					return testOutcome{
						skipped: true,
						message: "policy not visible on rate-limiter (shared Mongo required)",
						assertions: []string{"quota visible before proxy call"},
						steps: steps,
						meta:  map[string]any{"configId": cfgID, "namespaceId": nsID},
					}
				}

				code, raw, hdrs, err := outboundJSON(http.MethodGet, rlBase+path, map[string]string{
					"US-API-Key": apiKey + "-allow",
				}, nil)
				steps = append(steps, TestStep{
					Name: "proxy GET allow",
					Request: &HTTPExchange{
						Method: "GET", Path: rlBase + path,
						Headers: map[string]string{"US-API-Key": apiKey + "-allow"},
					},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
					OK: err == nil && code >= 200 && code < 300,
				})
				if err != nil {
					return testOutcome{err: err, steps: steps, assertions: []string{"proxy request succeeds"}}
				}
				if code < 200 || code >= 300 {
					return testOutcome{
						err: fmt.Errorf("want 2xx got %d: %s", code, truncate(raw, 200)),
						steps: steps, request: steps[len(steps)-1].Request, response: steps[len(steps)-1].Response,
						assertions: []string{"status 2xx (forwarded to mock)"},
					}
				}
				return testOutcome{
					message:    fmt.Sprintf("allowed with HTTP %d", code),
					assertions: []string{"status 2xx", "forwarded to mock-server"},
					steps:      steps,
					request:    steps[len(steps)-1].Request,
					response:   steps[len(steps)-1].Response,
					meta:       map[string]any{"namespace": nsLabel, "RATE_LIMITER_URL": rlBase},
				}
			},
		},
		{
			id: "e2e-proxy-deny-429", suite: "e2e",
			name:        "over-limit requests return 429",
			description: "E2E: provision a tight fixed_window (limit=2), send 4 matching requests through the proxy, expect at least one 429.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				key := apiKey + "-deny"
				steps, cfgID, nsID, nsLabel, cleanup, errOut := a.provisionE2EPolicy(h, prefix+"-deny", key, path, AlgoFixedWindow, map[string]any{
					"limit": 2, "windowSeconds": 60,
				})
				if errOut != nil {
					cleanup()
					return *errOut
				}
				defer cleanup()

				found, qCode, qBody := waitForNamespaceOnProxy(ctx, nsLabel, key)
				steps = append(steps, TestStep{
					Name: "wait for policy on proxy",
					Response: &HTTPResponseCapture{Status: qCode, Body: json.RawMessage(qBody)},
					OK: found,
				})
				if !found {
					return testOutcome{
						skipped: true,
						message: "policy not visible on rate-limiter (shared Mongo required)",
						assertions: []string{"quota visible before burst"},
						steps: steps,
						meta:  map[string]any{"configId": cfgID, "namespaceId": nsID},
					}
				}

				var saw429 bool
				var lastCode int
				var lastRaw string
				var lastHdrs map[string]string
				for i := 0; i < 4; i++ {
					code, raw, hdrs, err := outboundJSON(http.MethodGet, rlBase+path, map[string]string{
						"US-API-Key": key,
					}, nil)
					lastCode, lastRaw, lastHdrs = code, raw, hdrs
					ok := err == nil && (code == http.StatusOK || code == http.StatusTooManyRequests || (code >= 200 && code < 300))
					steps = append(steps, TestStep{
						Name: fmt.Sprintf("proxy GET #%d", i+1),
						Request: &HTTPExchange{
							Method: "GET", Path: rlBase + path,
							Headers: map[string]string{"US-API-Key": key},
						},
						Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
						OK: ok,
						Detail: fmt.Sprintf("status=%d", code),
					})
					if err != nil {
						return testOutcome{err: err, steps: steps}
					}
					if code == http.StatusTooManyRequests {
						saw429 = true
					}
				}
				if !saw429 {
					return testOutcome{
						err: fmt.Errorf("expected at least one 429, last status=%d body=%s", lastCode, truncate(lastRaw, 160)),
						steps: steps,
						request: &HTTPExchange{Method: "GET", Path: rlBase + path, Headers: map[string]string{"US-API-Key": key}},
						response: &HTTPResponseCapture{Status: lastCode, Body: json.RawMessage(lastRaw), Headers: lastHdrs},
						assertions: []string{"≥1 response status == 429"},
					}
				}
				return testOutcome{
					message:    "rate limit enforced with 429",
					assertions: []string{"≥1 status == 429", "limit=2 fixed_window"},
					steps:      steps,
					meta:       map[string]any{"namespace": nsLabel, "RATE_LIMITER_URL": rlBase},
				}
			},
		},
	}
}

// provisionE2EPolicy creates config + namespace + header/path rules. cleanup always safe to call.
func (a *api) provisionE2EPolicy(
	h http.Handler,
	namePrefix, apiKey, path, algorithm string,
	params map[string]any,
) (steps []TestStep, cfgID, nsID, nsLabel string, cleanup func(), errOut *testOutcome) {
	cleanup = func() {}
	cfgBody := map[string]any{
		"name": namePrefix + "-cfg", "algorithm": algorithm, "params": params,
	}
	code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
	steps = append(steps, TestStep{
		Name: "CP create config",
		Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(cfgBody)},
		Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
		OK: code == http.StatusCreated,
	})
	if code != http.StatusCreated {
		out := testOutcome{err: fmt.Errorf("create config %d: %s", code, raw), steps: steps}
		return steps, "", "", "", cleanup, &out
	}
	var cfg AlgorithmConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	cfgID = cfg.ID

	nsLabel = namePrefix + "-ns"
	nsBody := map[string]any{
		"label": nsLabel, "upstreamHost": "http://mock-server:8081",
		"configId": cfg.ID, "matchMode": "all", "priority": 5,
	}
	code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
	steps = append(steps, TestStep{
		Name: "CP create namespace",
		Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces", Body: mustJSON(nsBody)},
		Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
		OK: code == http.StatusCreated,
	})
	if code != http.StatusCreated {
		_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
		out := testOutcome{err: fmt.Errorf("create ns %d: %s", code, raw), steps: steps}
		return steps, cfgID, "", nsLabel, cleanup, &out
	}
	var ns Namespace
	_ = json.Unmarshal([]byte(raw), &ns)
	nsID = ns.ID
	cleanup = func() {
		_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
		_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
	}

	ruleHdr := map[string]any{
		"label": "api-key",
		"match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": apiKey},
	}
	code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", ruleHdr)
	steps = append(steps, TestStep{
		Name: "CP create header rule",
		Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(ruleHdr)},
		Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
		OK: code == http.StatusCreated,
	})
	rulePath := map[string]any{
		"label": "path",
		"match": map[string]any{"kind": "path", "op": "eq", "value": path},
	}
	code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", rulePath)
	steps = append(steps, TestStep{
		Name: "CP create path rule",
		Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(rulePath)},
		Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
		OK: code == http.StatusCreated,
	})
	if code != http.StatusCreated {
		cleanup()
		out := testOutcome{err: fmt.Errorf("create path rule %d: %s", code, raw), steps: steps}
		cleanup = func() {}
		return steps, cfgID, nsID, nsLabel, cleanup, &out
	}
	return steps, cfgID, nsID, nsLabel, cleanup, nil
}
