package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Predefined E2E functional scenarios (API-key / tenant / path rate-limit runs).
// Appended to the e2e suite so the Testing UI can execute them with the stack up.
func (a *api) e2eScenarioCases() []testCase {
	prefix := "e2e-scen-" + uuid.NewString()[:8]
	h := chi.NewRouter()
	a.routes(h)
	rlBase := rateLimiterBaseURL()
	tenant := "9101" // dedicated numeric tenant — avoids colliding with demo seed 1001

	return []testCase{
		{
			id: "e2e-scenario-api-key-dual-config", suite: "e2e",
			name: "API-key blocking: two keys, two configs (same tenant)",
			description: "Scenario: on one tenant path, provision two namespaces — each bound to a different US-API-Key " +
				"and a different fixed_window limit (2 vs 5). Traffic with key-A must 429 after 2 allows; key-B must still " +
				"allow and only 429 after its own higher quota.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				path := "/" + tenant + "/deviceList"
				keyA := "ak_" + prefix + "_a"
				keyB := "ak_" + prefix + "_b"
				var steps []TestStep
				var cleanups []func()
				cleanupAll := func() {
					for i := len(cleanups) - 1; i >= 0; i-- {
						cleanups[i]()
					}
				}

				// Key A — tight quota
				sA, _, _, nsA, cA, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-keyA",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 2, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   10,
					Rules: []map[string]any{
						{"label": "key-a", "match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": keyA}},
						{"label": "path", "match": map[string]any{"kind": "path", "op": "eq", "value": path}},
					},
				})
				steps = append(steps, sA...)
				cleanups = append(cleanups, cA)
				if errOut != nil {
					cleanupAll()
					return *errOut
				}

				// Key B — looser quota
				sB, _, _, nsB, cB, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-keyB",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 5, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   10,
					Rules: []map[string]any{
						{"label": "key-b", "match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": keyB}},
						{"label": "path", "match": map[string]any{"kind": "path", "op": "eq", "value": path}},
					},
				})
				steps = append(steps, sB...)
				cleanups = append(cleanups, cB)
				if errOut != nil {
					cleanupAll()
					return *errOut
				}
				defer cleanupAll()

				if !waitQuotaOrSkip(ctx, &steps, nsA, url.Values{"apiKey": {keyA}}) {
					return skip("policy not visible for key-A namespace (shared Mongo required)", "quota visible")
				}
				if !waitQuotaOrSkip(ctx, &steps, nsB, url.Values{"apiKey": {keyB}}) {
					return skip("policy not visible for key-B namespace", "quota visible")
				}

				// Exhaust key A (limit 2)
				allowA, denyA, burstSteps := burstProxy(rlBase, path, map[string]string{"US-API-Key": keyA}, 4)
				steps = append(steps, burstSteps...)
				if allowA < 2 || denyA < 1 {
					return testOutcome{
						err: fmt.Errorf("key-A: want ≥2 allows and ≥1 429, got allow=%d deny=%d", allowA, denyA),
						steps: steps, assertions: []string{"key-A limit=2 → allow×2 then 429"},
						meta: map[string]any{"keyA": keyA, "nsA": nsA, "allowA": allowA, "denyA": denyA},
					}
				}

				// Key B must still be independent (not exhausted by key A)
				code, raw, hdrs, err := outboundJSON(http.MethodGet, rlBase+path, map[string]string{"US-API-Key": keyB}, nil)
				steps = append(steps, TestStep{
					Name: "key-B first request after key-A exhausted",
					Request: &HTTPExchange{Method: "GET", Path: rlBase + path, Headers: map[string]string{"US-API-Key": keyB}},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
					OK: err == nil && code != http.StatusTooManyRequests && code >= 200 && code < 500,
				})
				if err != nil {
					return testOutcome{err: err, steps: steps}
				}
				if code == http.StatusTooManyRequests {
					return testOutcome{
						err: fmt.Errorf("key-B incorrectly 429'd after key-A traffic — quotas must be per-key"),
						steps: steps, assertions: []string{"key-B independent of key-A Redis subject"},
					}
				}

				allowB, denyB, burstB := burstProxy(rlBase, path, map[string]string{"US-API-Key": keyB}, 6)
				steps = append(steps, burstB...)
				// Already used 1 allow above
				totalAllowB := allowB + 1
				if totalAllowB < 5 || denyB < 1 {
					return testOutcome{
						err: fmt.Errorf("key-B: want ≥5 allows and ≥1 429, got allow≈%d deny=%d", totalAllowB, denyB),
						steps: steps, assertions: []string{"key-B limit=5 → allow×5 then 429"},
						meta: map[string]any{"keyB": keyB, "nsB": nsB, "allowB": totalAllowB, "denyB": denyB},
					}
				}

				return testOutcome{
					message: "two API keys enforced at different configs on same tenant path",
					assertions: []string{
						"key-A fixed_window limit=2 → 429",
						"key-B still allowed after key-A exhausted",
						"key-B fixed_window limit=5 → 429",
					},
					steps: steps,
					meta: map[string]any{
						"tenant": tenant, "path": path, "nsA": nsA, "nsB": nsB,
						"allowA": allowA, "denyA": denyA, "allowB": totalAllowB, "denyB": denyB,
					},
				}
			},
		},
		{
			id: "e2e-scenario-tenant-single-key", suite: "e2e",
			name: "Tenant-id blocking: single API key",
			description: "Scenario: rate-limit by path_attr tenantId (subject tenant:{id}) with a single API key present. " +
				"Quota is keyed by tenant, not header — fixed_window limit=2 must 429 the 3rd request.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				path := "/" + tenant + "/deviceList"
				key := "ak_" + prefix + "_tenant_single"
				steps, _, _, nsLabel, cleanup, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-tenant-single",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 2, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   20,
					Rules: []map[string]any{
						{
							"label": "tenant-attr",
							"match": map[string]any{
								"kind": "path_attr", "pattern": `^/(?P<tenantId>[0-9]+)/deviceList$`,
								"attr": "tenantId", "op": "eq", "value": tenant,
							},
						},
					},
				})
				if errOut != nil {
					cleanup()
					return *errOut
				}
				defer cleanup()

				if !waitQuotaOrSkip(ctx, &steps, nsLabel, url.Values{"tenant": {tenant}}) {
					return skip("tenant policy not visible on proxy", "quota visible")
				}

				allow, deny, burstSteps := burstProxy(rlBase, path, map[string]string{"US-API-Key": key}, 4)
				steps = append(steps, burstSteps...)
				if allow < 2 || deny < 1 {
					return testOutcome{
						err: fmt.Errorf("tenant single-key: want ≥2 allows + ≥1 429, got allow=%d deny=%d", allow, deny),
						steps: steps, assertions: []string{"subject tenant:" + tenant, "limit=2 → 429"},
					}
				}
				return testOutcome{
					message:    "tenant subject enforced with single key",
					assertions: []string{"path_attr → subject tenant:" + tenant, "limit=2 → ≥1 429"},
					steps:      steps,
					meta:       map[string]any{"tenant": tenant, "namespace": nsLabel, "allow": allow, "deny": deny},
				}
			},
		},
		{
			id: "e2e-scenario-tenant-multi-key", suite: "e2e",
			name: "Tenant-id blocking: multi API key shares tenant quota",
			description: "Scenario: same tenant path_attr policy (no header matcher). Two different US-API-Keys hit the same " +
				"tenant subject and share one fixed_window limit=3 — interleaved traffic must 429 after 3 allows total.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				path := "/" + tenant + "/deviceList"
				key1 := "ak_" + prefix + "_mk1"
				key2 := "ak_" + prefix + "_mk2"
				steps, _, _, nsLabel, cleanup, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-tenant-multi",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 3, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   21,
					Rules: []map[string]any{
						{
							"label": "tenant-attr",
							"match": map[string]any{
								"kind": "path_attr", "pattern": `^/(?P<tenantId>[0-9]+)/deviceList$`,
								"attr": "tenantId", "op": "eq", "value": tenant,
							},
						},
					},
				})
				if errOut != nil {
					cleanup()
					return *errOut
				}
				defer cleanup()

				if !waitQuotaOrSkip(ctx, &steps, nsLabel, url.Values{"tenant": {tenant}}) {
					return skip("tenant multi-key policy not visible on proxy", "quota visible")
				}

				keys := []string{key1, key2, key1, key2, key1}
				var allow, deny int
				for i, key := range keys {
					code, raw, hdrs, err := outboundJSON(http.MethodGet, rlBase+path, map[string]string{"US-API-Key": key}, nil)
					ok := err == nil && (code == http.StatusTooManyRequests || (code >= 200 && code < 300))
					steps = append(steps, TestStep{
						Name: fmt.Sprintf("interleaved #%d key=%s", i+1, key[len(key)-3:]),
						Request: &HTTPExchange{Method: "GET", Path: rlBase + path, Headers: map[string]string{"US-API-Key": key}},
						Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
						OK: ok, Detail: fmt.Sprintf("status=%d", code),
					})
					if err != nil {
						return testOutcome{err: err, steps: steps}
					}
					switch {
					case code == http.StatusTooManyRequests:
						deny++
					case code >= 200 && code < 300:
						allow++
					}
				}
				if allow != 3 || deny < 1 {
					return testOutcome{
						err: fmt.Errorf("multi-key tenant: want exactly 3 allows then 429, got allow=%d deny=%d", allow, deny),
						steps: steps,
						assertions: []string{
							"shared subject tenant:" + tenant,
							"two API keys share one quota",
							"limit=3 → allow×3 then 429",
						},
					}
				}
				return testOutcome{
					message: "multi-key traffic shares tenant quota",
					assertions: []string{
						"no header matcher → subject tenant:" + tenant,
						"key1+key2 share limit=3",
						"≥1 status == 429",
					},
					steps: steps,
					meta:  map[string]any{"tenant": tenant, "namespace": nsLabel, "allow": allow, "deny": deny, "key1": key1, "key2": key2},
				}
			},
		},
		{
			id: "e2e-scenario-http-path", suite: "e2e",
			name: "HTTP path blocking: per-path rate limits",
			description: "Scenario: apply a tight fixed_window (limit=2) only to /{tenant}/deviceList and a separate looser " +
				"limit (10) to /{tenant}/registerDevice. Exhausting deviceList must not block registerDevice.",
			fn: func(ctx context.Context) testOutcome {
				if ok, detail := probeRateLimiter(); !ok {
					return skip(detail, "rate-limiter reachable")
				}

				pathList := "/" + tenant + "/deviceList"
				pathReg := "/" + tenant + "/registerDevice"
				var steps []TestStep
				var cleanups []func()
				cleanupAll := func() {
					for i := len(cleanups) - 1; i >= 0; i-- {
						cleanups[i]()
					}
				}

				sList, _, _, nsList, cList, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-path-list",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 2, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   30,
					Rules: []map[string]any{
						{"label": "deviceList", "match": map[string]any{"kind": "path", "op": "eq", "value": pathList}},
						{"label": "GET", "match": map[string]any{"kind": "method", "op": "eq", "value": "GET"}},
					},
				})
				steps = append(steps, sList...)
				cleanups = append(cleanups, cList)
				if errOut != nil {
					cleanupAll()
					return *errOut
				}

				sReg, _, _, nsReg, cReg, errOut := a.provisionPolicy(h, policySpec{
					NamePrefix: prefix + "-path-reg",
					Algorithm:  AlgoFixedWindow,
					Params:     map[string]any{"limit": 10, "windowSeconds": 60},
					MatchMode:  MatchModeAll,
					Priority:   30,
					Rules: []map[string]any{
						{"label": "registerDevice", "match": map[string]any{"kind": "path", "op": "eq", "value": pathReg}},
						{"label": "POST", "match": map[string]any{"kind": "method", "op": "eq", "value": "POST"}},
					},
				})
				steps = append(steps, sReg...)
				cleanups = append(cleanups, cReg)
				if errOut != nil {
					cleanupAll()
					return *errOut
				}
				defer cleanupAll()

				if !waitQuotaOrSkip(ctx, &steps, nsList, url.Values{"path": {pathList}}) {
					return skip("deviceList path policy not visible", "quota visible")
				}
				if !waitQuotaOrSkip(ctx, &steps, nsReg, url.Values{"path": {pathReg}}) {
					return skip("registerDevice path policy not visible", "quota visible")
				}

				allowL, denyL, burstL := burstProxy(rlBase, pathList, nil, 4)
				steps = append(steps, burstL...)
				if allowL < 2 || denyL < 1 {
					return testOutcome{
						err: fmt.Errorf("deviceList path: want ≥2 allows + ≥1 429, got allow=%d deny=%d", allowL, denyL),
						steps: steps, assertions: []string{"path " + pathList + " limit=2 → 429"},
					}
				}

				// registerDevice must remain independently allowed
				code, raw, hdrs, err := outboundJSON(http.MethodPost, rlBase+pathReg, map[string]string{
					"Content-Type": "application/json",
					"US-API-Key":   "ak_" + prefix + "_path",
				}, map[string]any{"deviceId": "dev-" + prefix})
				steps = append(steps, TestStep{
					Name: "registerDevice after deviceList exhausted",
					Request: &HTTPExchange{
						Method: "POST", Path: rlBase + pathReg,
						Headers: map[string]string{"US-API-Key": "ak_" + prefix + "_path"},
						Body:    mustJSON(map[string]any{"deviceId": "dev-" + prefix}),
					},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
					OK: err == nil && code != http.StatusTooManyRequests && code >= 200 && code < 500,
				})
				if err != nil {
					return testOutcome{err: err, steps: steps}
				}
				if code == http.StatusTooManyRequests {
					return testOutcome{
						err: fmt.Errorf("registerDevice incorrectly 429'd — path quotas must be independent"),
						steps: steps, assertions: []string{"path " + pathReg + " independent of deviceList"},
					}
				}
				if code < 200 || code >= 300 {
					return testOutcome{
						err: fmt.Errorf("registerDevice want 2xx got %d: %s", code, truncate(raw, 160)),
						steps: steps, assertions: []string{"registerDevice forwarded (2xx)"},
					}
				}

				return testOutcome{
					message: "per-path rate limits enforced independently",
					assertions: []string{
						"GET " + pathList + " limit=2 → 429",
						"POST " + pathReg + " still allowed",
						"subjects path:/… are independent",
					},
					steps: steps,
					meta: map[string]any{
						"nsList": nsList, "nsReg": nsReg,
						"allowList": allowL, "denyList": denyL, "registerStatus": code,
					},
				}
			},
		},
	}
}

type policySpec struct {
	NamePrefix string
	Algorithm  string
	Params     map[string]any
	MatchMode  string
	Priority   int
	Rules      []map[string]any
}

// provisionPolicy creates config + namespace + arbitrary rules. cleanup deletes ns then config.
func (a *api) provisionPolicy(
	h http.Handler,
	spec policySpec,
) (steps []TestStep, cfgID, nsID, nsLabel string, cleanup func(), errOut *testOutcome) {
	cleanup = func() {}
	cfgBody := map[string]any{
		"name": spec.NamePrefix + "-cfg", "algorithm": spec.Algorithm, "params": spec.Params,
	}
	code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
	steps = append(steps, TestStep{
		Name: "CP create config (" + spec.NamePrefix + ")",
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

	prio := spec.Priority
	if prio == 0 {
		prio = 50
	}
	mode := spec.MatchMode
	if mode == "" {
		mode = MatchModeAll
	}
	nsLabel = spec.NamePrefix + "-ns"
	nsBody := map[string]any{
		"label": nsLabel, "upstreamHost": "http://mock-server:8081",
		"configId": cfg.ID, "matchMode": mode, "priority": prio,
	}
	code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
	steps = append(steps, TestStep{
		Name: "CP create namespace (" + nsLabel + ")",
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

	for i, rule := range spec.Rules {
		code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", rule)
		label, _ := rule["label"].(string)
		if label == "" {
			label = fmt.Sprintf("rule-%d", i+1)
		}
		steps = append(steps, TestStep{
			Name: "CP create rule (" + label + ")",
			Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(rule)},
			Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
			OK: code == http.StatusCreated,
		})
		if code != http.StatusCreated {
			cleanup()
			out := testOutcome{err: fmt.Errorf("create rule %s %d: %s", label, code, raw), steps: steps}
			cleanup = func() {}
			return steps, cfgID, nsID, nsLabel, cleanup, &out
		}
	}
	return steps, cfgID, nsID, nsLabel, cleanup, nil
}

func waitForQuota(ctx context.Context, nsLabel string, extra url.Values) (found bool, lastStatus int, lastBody string) {
	base := rateLimiterBaseURL()
	deadline := time.Now().Add(policyPollWait())
	q := url.Values{}
	q.Set("namespace", nsLabel)
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
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

func waitQuotaOrSkip(ctx context.Context, steps *[]TestStep, nsLabel string, extra url.Values) bool {
	found, qCode, qBody := waitForQuota(ctx, nsLabel, extra)
	*steps = append(*steps, TestStep{
		Name: "wait for proxy quota (" + nsLabel + ")",
		Request: &HTTPExchange{Method: "GET", Path: rateLimiterBaseURL() + "/api/v1/quota?namespace=" + url.QueryEscape(nsLabel)},
		Response: &HTTPResponseCapture{Status: qCode, Body: json.RawMessage(qBody)},
		OK: found, Detail: fmt.Sprintf("pollWait=%s", policyPollWait()),
	})
	return found
}

// burstProxy sends n GETs and counts 2xx vs 429.
func burstProxy(rlBase, path string, headers map[string]string, n int) (allow, deny int, steps []TestStep) {
	for i := 0; i < n; i++ {
		code, raw, hdrs, err := outboundJSON(http.MethodGet, rlBase+path, headers, nil)
		ok := err == nil && (code == http.StatusTooManyRequests || (code >= 200 && code < 300))
		steps = append(steps, TestStep{
			Name: fmt.Sprintf("proxy GET #%d %s", i+1, path),
			Request: &HTTPExchange{Method: "GET", Path: rlBase + path, Headers: headers},
			Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw), Headers: hdrs},
			OK: ok, Detail: fmt.Sprintf("status=%d", code),
		})
		if err != nil {
			continue
		}
		switch {
		case code == http.StatusTooManyRequests:
			deny++
		case code >= 200 && code < 300:
			allow++
		}
	}
	return allow, deny, steps
}
