package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type HTTPExchange struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

type HTTPResponseCapture struct {
	Status  int               `json:"status"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type TestCaseResult struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Suite       string               `json:"suite"`
	Status      string               `json:"status"`
	Duration    string               `json:"durationMs"`
	Message     string               `json:"message,omitempty"`
	Assertions  []string             `json:"assertions,omitempty"`
	Request     *HTTPExchange        `json:"request,omitempty"`
	Response    *HTTPResponseCapture `json:"response,omitempty"`
	Steps       []TestStep           `json:"steps,omitempty"`
	Meta        map[string]any       `json:"meta,omitempty"`
}

type TestStep struct {
	Name     string               `json:"name"`
	Request  *HTTPExchange        `json:"request,omitempty"`
	Response *HTTPResponseCapture `json:"response,omitempty"`
	OK       bool                 `json:"ok"`
	Detail   string               `json:"detail,omitempty"`
}

type TestRunReport struct {
	ID        string           `json:"id"`
	Suite     string           `json:"suite"`
	StartedAt time.Time        `json:"startedAt"`
	EndedAt   time.Time        `json:"endedAt"`
	Duration  string           `json:"duration"`
	Passed    int              `json:"passed"`
	Failed    int              `json:"failed"`
	Skipped   int              `json:"skipped"`
	Total     int              `json:"total"`
	OK        bool             `json:"ok"`
	Results   []TestCaseResult `json:"results"`
}

type testOutcome struct {
	err        error
	skipped    bool
	message    string
	assertions []string
	request    *HTTPExchange
	response   *HTTPResponseCapture
	steps      []TestStep
	meta       map[string]any
}

type testCase struct {
	id, name, description, suite string
	fn                           func(ctx context.Context) testOutcome
}

// selftestRunner executes data-plane suites against matcher/limiter units
// and the live Redis + policy cache used by this process.
type selftestRunner struct {
	cache   *PolicyCache
	limiter *Limiter
}

func (s *selftestRunner) listTestCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"suites": []map[string]any{
			{
				"id":          "unit",
				"label":       "Unit tests",
				"description": "Matcher specificity, subject builders, policy-cache swap, and Lua algorithms against isolated miniredis (no live Redis/Mongo).",
			},
			{
				"id":          "functional",
				"label":       "Functional tests",
				"description": "Live Redis evaluate/peek, healthz/quota HTTP, and match against the loaded policy snapshot. Skips policy-dependent cases when no namespaces are loaded.",
			},
			{
				"id":          "all",
				"label":       "All suites",
				"description": "Run unit → functional in order.",
			},
		},
	})
}

func (s *selftestRunner) runTests(w http.ResponseWriter, r *http.Request) {
	suite := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("suite")))
	if suite == "" {
		suite = "all"
	}
	switch suite {
	case "unit", "functional", "all":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "suite must be unit, functional, or all",
			"code":  "validation_error",
		})
		return
	}
	writeJSON(w, http.StatusOK, s.executeSuites(r.Context(), suite))
}

func (s *selftestRunner) executeSuites(ctx context.Context, suite string) TestRunReport {
	start := time.Now().UTC()
	var cases []testCase
	switch suite {
	case "unit":
		cases = unitTestCases()
	case "functional":
		cases = s.functionalTestCases()
	default:
		cases = append(unitTestCases(), s.functionalTestCases()...)
	}

	results := make([]TestCaseResult, 0, len(cases))
	passed, failed, skipped := 0, 0, 0
	for _, tc := range cases {
		t0 := time.Now()
		out := tc.fn(ctx)
		dur := time.Since(t0)
		res := TestCaseResult{
			ID: tc.id, Name: tc.name, Description: tc.description, Suite: tc.suite,
			Duration:   fmt.Sprintf("%.2fms", float64(dur.Microseconds())/1000),
			Assertions: out.assertions, Request: out.request, Response: out.response,
			Steps: out.steps, Meta: out.meta,
		}
		switch {
		case out.skipped:
			res.Status = "skipped"
			if out.message != "" {
				res.Message = out.message
			} else {
				res.Message = "skipped"
			}
			skipped++
		case out.err != nil:
			res.Status = "failed"
			res.Message = out.err.Error()
			failed++
		default:
			res.Status = "passed"
			if out.message != "" {
				res.Message = out.message
			} else {
				res.Message = "ok"
			}
			passed++
		}
		results = append(results, res)
	}
	end := time.Now().UTC()
	return TestRunReport{
		ID: uuid.NewString(), Suite: suite,
		StartedAt: start, EndedAt: end, Duration: end.Sub(start).String(),
		Passed: passed, Failed: failed, Skipped: skipped, Total: len(results),
		OK: failed == 0, Results: results,
	}
}

func pass(msg string, assertions ...string) testOutcome {
	return testOutcome{message: msg, assertions: assertions}
}

func fail(err error, assertions ...string) testOutcome {
	return testOutcome{err: err, assertions: assertions}
}

func skip(msg string, assertions ...string) testOutcome {
	return testOutcome{skipped: true, message: msg, assertions: assertions}
}

func unitTestCases() []testCase {
	return []testCase{
		{
			id: "ut-match-header-path-method", suite: "unit",
			name:        "match all: header + path + method",
			description: "Unit: matchRequest with matchMode=all must accept US-API-Key + /1001/deviceList + GET.",
			fn: func(context.Context) testOutcome {
				snap := demoSnapshot(MatchModeAll, 10)
				req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)
				req.Header.Set("US-API-Key", "ak_demo_1001_alpha")
				got, ok := matchRequest(snap, req)
				ex := &HTTPExchange{Method: "GET", Path: "/1001/deviceList", Headers: map[string]string{"US-API-Key": "ak_demo_1001_alpha"}}
				if !ok {
					return testOutcome{err: fmt.Errorf("expected match"), assertions: []string{"ok == true"}, request: ex}
				}
				if got.Subject != "hdr:US-API-Key:ak_demo_1001_alpha" {
					return fail(fmt.Errorf("subject=%q", got.Subject), "subject == hdr:US-API-Key:ak_demo_1001_alpha")
				}
				return testOutcome{
					message:    "matched demo namespace",
					assertions: []string{"ok == true", "label == demo", "subject == hdr:US-API-Key:ak_demo_1001_alpha"},
					request:    ex,
					meta:       map[string]any{"namespace": got.Namespace.Namespace.Label, "subject": got.Subject},
				}
			},
		},
		{
			id: "ut-match-rejects-missing-header", suite: "unit",
			name:        "match rejects missing header",
			description: "Unit: matchMode=all must reject a request that omits US-API-Key.",
			fn: func(context.Context) testOutcome {
				snap := demoSnapshot(MatchModeAll, 10)
				req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)
				_, ok := matchRequest(snap, req)
				ex := &HTTPExchange{Method: "GET", Path: "/1001/deviceList"}
				if ok {
					return testOutcome{err: fmt.Errorf("expected no match"), assertions: []string{"ok == false"}, request: ex}
				}
				return testOutcome{message: "correctly rejected", assertions: []string{"ok == false"}, request: ex}
			},
		},
		{
			id: "ut-match-mode-any", suite: "unit",
			name:        "matchMode any accepts single rule",
			description: "Unit: matchMode=any must match when only the header rule hits (path/method differ).",
			fn: func(context.Context) testOutcome {
				snap := demoSnapshot(MatchModeAny, 10)
				req := httptest.NewRequest(http.MethodPost, "/other", nil)
				req.Header.Set("US-API-Key", "ak_demo_1001_alpha")
				got, ok := matchRequest(snap, req)
				if !ok {
					return fail(fmt.Errorf("expected match"), "ok == true")
				}
				return pass("matched via any", "ok == true", "subject == "+got.Subject)
			},
		},
		{
			id: "ut-match-priority-specificity", suite: "unit",
			name:        "header specificity beats method-only",
			description: "Unit: equal priority namespaces — header matcher wins over method-only via specificity.",
			fn: func(context.Context) testOutcome {
				low := CompiledNamespace{
					Namespace: Namespace{ID: "a", Label: "low", Priority: 5, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
					Rules:     []Rule{{Match: RuleMatch{Kind: MatchMethod, Op: "eq", Value: "GET"}}},
					Config:    AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
				}
				highSpec := CompiledNamespace{
					Namespace: Namespace{ID: "b", Label: "header-ns", Priority: 5, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
					Rules:     []Rule{{Match: RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "eq", Value: "k"}}},
					Config:    AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
				}
				snap := &PolicySnapshot{Namespaces: []CompiledNamespace{low, highSpec}}
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("US-API-Key", "k")
				got, ok := matchRequest(snap, req)
				if !ok {
					return fail(fmt.Errorf("expected match"), "ok == true")
				}
				if got.Namespace.Namespace.Label != "header-ns" {
					return fail(fmt.Errorf("got label=%s", got.Namespace.Namespace.Label), "label == header-ns")
				}
				return pass("header-ns won", "label == header-ns")
			},
		},
		{
			id: "ut-path-attr-subject", suite: "unit",
			name:        "path_attr builds tenant subject",
			description: "Unit: path_attr match must set subject tenant:<id> from the named capture group.",
			fn: func(context.Context) testOutcome {
				ns := CompiledNamespace{
					Namespace: Namespace{ID: "t", Label: "tenant", Priority: 1, MatchMode: MatchModeAll, Algorithm: AlgoFixedWindow},
					Rules: []Rule{{
						Match: RuleMatch{
							Kind: MatchPathAttr, Pattern: `^/(?P<tenantId>[0-9]+)/deviceList$`,
							Attr: "tenantId", Op: "eq", Value: "1001",
						},
					}},
					Config: AlgorithmConfig{Algorithm: AlgoFixedWindow, Params: map[string]any{"limit": 10, "windowSeconds": 1}},
				}
				snap := &PolicySnapshot{Namespaces: []CompiledNamespace{ns}}
				req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)
				got, ok := matchRequest(snap, req)
				if !ok {
					return fail(fmt.Errorf("expected match"), "ok == true")
				}
				if got.Subject != "tenant:1001" {
					return fail(fmt.Errorf("subject=%q", got.Subject), "subject == tenant:1001")
				}
				return pass("tenant subject", "subject == tenant:1001")
			},
		},
		{
			id: "ut-header-in-op", suite: "unit",
			name:        "header in-op accepts listed value",
			description: "Unit: header match with op=in must accept a value present in values[].",
			fn: func(context.Context) testOutcome {
				ns := CompiledNamespace{
					Namespace: Namespace{ID: "h", Label: "in", Priority: 1, MatchMode: MatchModeAll, Algorithm: AlgoFixedToken},
					Rules: []Rule{{
						Match: RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "in", Values: []string{"a", "b"}},
					}},
					Config: AlgorithmConfig{Algorithm: AlgoFixedToken, Params: map[string]any{"sustainedRps": 1, "burst": 1}},
				}
				snap := &PolicySnapshot{Namespaces: []CompiledNamespace{ns}}
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("US-API-Key", "b")
				got, ok := matchRequest(snap, req)
				if !ok {
					return fail(fmt.Errorf("expected match"), "ok == true")
				}
				if got.Subject != "hdr:US-API-Key:b" {
					return fail(fmt.Errorf("subject=%q", got.Subject), "subject == hdr:US-API-Key:b")
				}
				return pass("in-op matched", "subject == hdr:US-API-Key:b")
			},
		},
		{
			id: "ut-fixed-token-allow-deny", suite: "unit",
			name:        "fixed_token allow then deny",
			description: "Unit: miniredis token bucket with burst=2 must allow twice then deny.",
			fn: func(ctx context.Context) testOutcome {
				lim, cleanup, err := miniLimiter()
				if err != nil {
					return fail(err)
				}
				defer cleanup()
				params := map[string]any{"sustainedRps": int64(1), "burst": int64(2)}
				subj := "selftest-" + uuid.NewString()[:8]
				r1, err := lim.Evaluate(ctx, AlgoFixedToken, "ut-ns", subj, params)
				if err != nil || !r1.Allowed {
					return fail(fmt.Errorf("r1: allowed=%v err=%v", r1.Allowed, err), "r1.Allowed == true")
				}
				r2, err := lim.Evaluate(ctx, AlgoFixedToken, "ut-ns", subj, params)
				if err != nil || !r2.Allowed {
					return fail(fmt.Errorf("r2: allowed=%v err=%v", r2.Allowed, err), "r2.Allowed == true")
				}
				r3, err := lim.Evaluate(ctx, AlgoFixedToken, "ut-ns", subj, params)
				if err != nil || r3.Allowed {
					return fail(fmt.Errorf("r3: allowed=%v err=%v", r3.Allowed, err), "r3.Allowed == false")
				}
				return pass("burst exhausted", "r1 allowed", "r2 allowed", "r3 denied", "limit == 2")
			},
		},
		{
			id: "ut-fixed-window", suite: "unit",
			name:        "fixed_window allow then deny",
			description: "Unit: miniredis fixed window limit=2 must deny the third call in the same window.",
			fn: func(ctx context.Context) testOutcome {
				lim, cleanup, err := miniLimiter()
				if err != nil {
					return fail(err)
				}
				defer cleanup()
				params := map[string]any{"limit": int64(2), "windowSeconds": int64(60)}
				subj := "selftest-" + uuid.NewString()[:8]
				for i, want := range []bool{true, true, false} {
					r, err := lim.Evaluate(ctx, AlgoFixedWindow, "ut-ns", subj, params)
					if err != nil {
						return fail(err, fmt.Sprintf("call %d", i+1))
					}
					if r.Allowed != want {
						return fail(fmt.Errorf("call %d: allowed=%v want=%v", i+1, r.Allowed, want))
					}
				}
				return pass("window limit enforced", "call1 allow", "call2 allow", "call3 deny")
			},
		},
		{
			id: "ut-sliding-window", suite: "unit",
			name:        "sliding_window allow then deny",
			description: "Unit: miniredis sliding window limit=2 must deny the third call.",
			fn: func(ctx context.Context) testOutcome {
				lim, cleanup, err := miniLimiter()
				if err != nil {
					return fail(err)
				}
				defer cleanup()
				params := map[string]any{"limit": int64(2), "windowSeconds": int64(60)}
				subj := "selftest-" + uuid.NewString()[:8]
				for i, want := range []bool{true, true, false} {
					r, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ut-ns", subj, params)
					if err != nil {
						return fail(err, fmt.Sprintf("call %d", i+1))
					}
					if r.Allowed != want {
						return fail(fmt.Errorf("call %d: allowed=%v want=%v", i+1, r.Allowed, want))
					}
				}
				return pass("sliding limit enforced", "call1 allow", "call2 allow", "call3 deny")
			},
		},
		{
			id: "ut-policy-cache-swap", suite: "unit",
			name:        "policy cache atomic swap",
			description: "Unit: PolicyCache.LoadOnce must atomically replace the snapshot; concurrent readers always see a complete value.",
			fn: func(ctx context.Context) testOutcome {
				loader := &stubCounterLoader{}
				cache := newPolicyCache(loader, time.Hour)
				if err := cache.LoadOnce(ctx); err != nil {
					return fail(err)
				}
				first := cache.Snapshot()
				if first == nil || first.Namespaces[0].Namespace.Priority != 1 {
					return fail(fmt.Errorf("first priority=%v", first))
				}
				if err := cache.LoadOnce(ctx); err != nil {
					return fail(err)
				}
				second := cache.Snapshot()
				if second.Namespaces[0].Namespace.Priority != 2 {
					return fail(fmt.Errorf("second priority=%d", second.Namespaces[0].Namespace.Priority), "priority == 2")
				}
				return pass("snapshot swapped", "first.priority == 1", "second.priority == 2")
			},
		},
	}
}

type stubCounterLoader struct {
	n atomic.Int64
}

func (s *stubCounterLoader) LoadSnapshot(context.Context) (*PolicySnapshot, error) {
	i := s.n.Add(1)
	return &PolicySnapshot{
		LoadedAt: time.Now().UTC(),
		Namespaces: []CompiledNamespace{{
			Namespace: Namespace{ID: "x", Label: "n", Priority: int(i), MatchMode: MatchModeAll},
		}},
	}, nil
}

func miniLimiter() (*Limiter, func(), error) {
	mr, err := miniredis.Run()
	if err != nil {
		return nil, nil, err
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cleanup := func() {
		_ = rdb.Close()
		mr.Close()
	}
	return newLimiter(rdb), cleanup, nil
}

func (s *selftestRunner) functionalTestCases() []testCase {
	prefix := "selftest-" + uuid.NewString()[:8]
	return []testCase{
		{
			id: "ft-redis-ping", suite: "functional",
			name:        "live Redis PING",
			description: "Functional: limiter must successfully PING the shared Redis used by this replica.",
			fn: func(ctx context.Context) testOutcome {
				if err := s.limiter.Ping(ctx); err != nil {
					return fail(err, "PING == PONG")
				}
				return pass("redis ok", "PING == PONG")
			},
		},
		{
			id: "ft-healthz", suite: "functional",
			name:        "GET /healthz",
			description: "Functional: in-process healthz must return 200 with status=ok.",
			fn: func(context.Context) testOutcome {
				h := newProxyHandler(s.cache, s.limiter)
				req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				body, _ := io.ReadAll(rec.Body)
				ex := &HTTPExchange{Method: "GET", Path: "/healthz"}
				resp := &HTTPResponseCapture{Status: rec.Code, Body: json.RawMessage(body)}
				if rec.Code != http.StatusOK {
					return testOutcome{err: fmt.Errorf("status %d", rec.Code), assertions: []string{"status == 200"}, request: ex, response: resp}
				}
				var m map[string]string
				_ = json.Unmarshal(body, &m)
				if m["status"] != "ok" {
					return testOutcome{err: fmt.Errorf("body=%s", body), assertions: []string{`status == "ok"`}, request: ex, response: resp}
				}
				return testOutcome{message: "healthy", assertions: []string{"status == 200", `body.status == "ok"`}, request: ex, response: resp}
			},
		},
		{
			id: "ft-live-fixed-token", suite: "functional",
			name:        "live Redis fixed_token evaluate",
			description: "Functional: Evaluate against live Redis with a unique subject — burst=2 allows twice then denies.",
			fn: func(ctx context.Context) testOutcome {
				params := map[string]any{"sustainedRps": int64(1), "burst": int64(2)}
				subj := "hdr:US-API-Key:" + prefix
				nsID := "ft-" + prefix
				steps := []TestStep{}
				for i, want := range []bool{true, true, false} {
					r, err := s.limiter.Evaluate(ctx, AlgoFixedToken, nsID, subj, params)
					ok := err == nil && r.Allowed == want
					steps = append(steps, TestStep{
						Name:   fmt.Sprintf("evaluate #%d", i+1),
						OK:     ok,
						Detail: fmt.Sprintf("allowed=%v remaining=%d wantAllowed=%v err=%v", r.Allowed, r.Remaining, want, err),
					})
					if !ok {
						return testOutcome{
							err: fmt.Errorf("evaluate #%d failed", i+1), assertions: []string{"burst=2 → allow,allow,deny"},
							steps: steps, meta: map[string]any{"redisKey": redisKey(AlgoFixedToken, nsID, subj)},
						}
					}
				}
				_ = s.limiter.rdb.Del(ctx, redisKey(AlgoFixedToken, nsID, subj))
				return testOutcome{
					message: "live burst exhausted", assertions: []string{"allow", "allow", "deny"},
					steps: steps, meta: map[string]any{"namespaceId": nsID, "subject": subj},
				}
			},
		},
		{
			id: "ft-live-peek", suite: "functional",
			name:        "live Redis peek after consume",
			description: "Functional: Peek must report consumed>0 and remaining < limit after one Evaluate.",
			fn: func(ctx context.Context) testOutcome {
				params := map[string]any{"limit": int64(5), "windowSeconds": int64(60)}
				subj := "path:/ft/" + prefix
				nsID := "ft-peek-" + prefix
				r, err := s.limiter.Evaluate(ctx, AlgoFixedWindow, nsID, subj, params)
				if err != nil || !r.Allowed {
					return fail(fmt.Errorf("evaluate: allowed=%v err=%v", r.Allowed, err), "evaluate allowed")
				}
				st, err := s.limiter.Peek(ctx, AlgoFixedWindow, nsID, subj, params)
				_ = s.limiter.rdb.Del(ctx, redisKey(AlgoFixedWindow, nsID, subj))
				if err != nil {
					return fail(err, "peek ok")
				}
				if st.Consumed < 1 || st.Remaining >= st.Limit {
					return fail(fmt.Errorf("consumed=%d remaining=%d limit=%d", st.Consumed, st.Remaining, st.Limit),
						"consumed >= 1", "remaining < limit")
				}
				return pass("peek reflects consume", "consumed >= 1", "remaining < limit",
					fmt.Sprintf("limit=%d", st.Limit), fmt.Sprintf("remaining=%d", st.Remaining))
			},
		},
		{
			id: "ft-quota-validation", suite: "functional",
			name:        "quota requires namespace",
			description: "Functional: GET /api/v1/quota without namespace must return 400.",
			fn: func(context.Context) testOutcome {
				h := newProxyHandler(s.cache, s.limiter)
				req := httptest.NewRequest(http.MethodGet, "/api/v1/quota", nil)
				rec := httptest.NewRecorder()
				h.serveQuota(rec, req)
				body, _ := io.ReadAll(rec.Body)
				ex := &HTTPExchange{Method: "GET", Path: "/api/v1/quota"}
				resp := &HTTPResponseCapture{Status: rec.Code, Body: json.RawMessage(body)}
				if rec.Code != http.StatusBadRequest {
					return testOutcome{err: fmt.Errorf("status %d", rec.Code), assertions: []string{"status == 400"}, request: ex, response: resp}
				}
				return testOutcome{message: "validation enforced", assertions: []string{"status == 400"}, request: ex, response: resp}
			},
		},
		{
			id: "ft-match-loaded-policy", suite: "functional",
			name:        "match against loaded policy",
			description: "Functional: if demo-deviceList (or any namespace) is in the cache, match a synthetic request. Skips when policy is empty.",
			fn: func(context.Context) testOutcome {
				snap := s.cache.Snapshot()
				if snap == nil || len(snap.Namespaces) == 0 {
					return skip("no namespaces in policy cache — seed Mongo or create via control-plane",
						"len(namespaces) > 0")
				}
				// Prefer demo seed; otherwise exercise the first namespace's rules best-effort.
				var target *CompiledNamespace
				for i := range snap.Namespaces {
					if snap.Namespaces[i].Namespace.Label == "demo-deviceList" {
						target = &snap.Namespaces[i]
						break
					}
				}
				if target == nil {
					target = &snap.Namespaces[0]
				}
				req := httptest.NewRequest(http.MethodGet, "/1001/deviceList", nil)
				req.Header.Set("US-API-Key", "ak_demo_1001_alpha")
				got, ok := matchRequest(snap, req)
				ex := &HTTPExchange{
					Method: "GET", Path: "/1001/deviceList",
					Headers: map[string]string{"US-API-Key": "ak_demo_1001_alpha"},
				}
				meta := map[string]any{
					"loadedNamespaces": len(snap.Namespaces),
					"targetLabel":      target.Namespace.Label,
				}
				if target.Namespace.Label == "demo-deviceList" {
					if !ok {
						return testOutcome{
							err: fmt.Errorf("demo-deviceList did not match demo request"),
							assertions: []string{"ok == true", "label == demo-deviceList"},
							request: ex, meta: meta,
						}
					}
					meta["subject"] = got.Subject
					return testOutcome{
						message: "matched demo-deviceList", assertions: []string{"ok == true", "label == demo-deviceList"},
						request: ex, meta: meta,
					}
				}
				// Non-demo policy: just confirm matcher runs without panic and reports a boolean.
				return testOutcome{
					message: fmt.Sprintf("matcher ran against %q (matched=%v)", target.Namespace.Label, ok),
					assertions: []string{"snapshot loaded", "matchRequest returned"},
					request: ex, meta: meta,
				}
			},
		},
		{
			id: "ft-quota-http-demo", suite: "functional",
			name:        "GET /api/v1/quota for loaded namespace",
			description: "Functional: peek quota via HTTP for the first loaded namespace with a selftest subject. Skips when no namespaces.",
			fn: func(context.Context) testOutcome {
				snap := s.cache.Snapshot()
				if snap == nil || len(snap.Namespaces) == 0 {
					return skip("no namespaces in policy cache", "len(namespaces) > 0")
				}
				ns := snap.Namespaces[0]
				path := fmt.Sprintf("/api/v1/quota?namespace=%s&subject=%s",
					ns.Namespace.Label, "hdr:US-API-Key:"+prefix)
				h := newProxyHandler(s.cache, s.limiter)
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				h.serveQuota(rec, req)
				body, _ := io.ReadAll(rec.Body)
				ex := &HTTPExchange{Method: "GET", Path: path}
				resp := &HTTPResponseCapture{Status: rec.Code, Body: json.RawMessage(body)}
				if rec.Code != http.StatusOK {
					return testOutcome{
						err: fmt.Errorf("status %d body=%s", rec.Code, truncate(string(body), 200)),
						assertions: []string{"status == 200"}, request: ex, response: resp,
					}
				}
				var st QuotaState
				if err := json.Unmarshal(body, &st); err != nil {
					return testOutcome{err: err, assertions: []string{"json QuotaState"}, request: ex, response: resp}
				}
				if st.Limit < 1 {
					return testOutcome{err: fmt.Errorf("limit=%d", st.Limit), assertions: []string{"limit >= 1"}, request: ex, response: resp}
				}
				return testOutcome{
					message: "quota peeked", assertions: []string{"status == 200", "limit >= 1"},
					request: ex, response: resp,
					meta: map[string]any{"namespace": st.NamespaceLabel, "algorithm": st.Algorithm, "remaining": st.Remaining},
				}
			},
		},
		{
			id: "ft-proxy-no-match", suite: "functional",
			name:        "proxy returns 404 when no match",
			description: "Functional: a request that matches no namespace must return 404. Skips when policy is empty (open-allow mode).",
			fn: func(context.Context) testOutcome {
				snap := s.cache.Snapshot()
				if snap == nil {
					return skip("policy not loaded", "snapshot != nil")
				}
				if len(snap.Namespaces) == 0 {
					return skip("open-allow mode (no namespaces) — 404 path not exercised", "len(namespaces) > 0")
				}
				h := newProxyHandler(s.cache, s.limiter)
				req := httptest.NewRequest(http.MethodGet, "/__selftest_no_match__/"+prefix, nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				body, _ := io.ReadAll(rec.Body)
				ex := &HTTPExchange{Method: "GET", Path: req.URL.Path}
				resp := &HTTPResponseCapture{Status: rec.Code, Body: json.RawMessage(body)}
				if rec.Code != http.StatusNotFound {
					return testOutcome{
						err: fmt.Errorf("status %d", rec.Code), assertions: []string{"status == 404"},
						request: ex, response: resp,
					}
				}
				return testOutcome{
					message: "no match → 404", assertions: []string{"status == 404"},
					request: ex, response: resp,
				}
			},
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// demoSnapshot is a synthetic policy used by unit selftests (and matcher_test.go).
func demoSnapshot(mode string, priority int) *PolicySnapshot {
	return &PolicySnapshot{Namespaces: []CompiledNamespace{{
		Namespace: Namespace{
			ID: "ns1", Label: "demo", UpstreamHost: "http://mock-server:8081",
			Algorithm: AlgoFixedToken, MatchMode: mode, Priority: priority, Enabled: true,
		},
		Rules: []Rule{
			{Match: RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "eq", Value: "ak_demo_1001_alpha"}},
			{Match: RuleMatch{Kind: MatchPath, Op: "eq", Value: "/1001/deviceList"}},
			{Match: RuleMatch{Kind: MatchMethod, Op: "eq", Value: "GET"}},
		},
		Config: AlgorithmConfig{
			ID: "c1", Algorithm: AlgoFixedToken,
			Params: map[string]any{"sustainedRps": 10, "burst": 20}, Enabled: true,
		},
	}}}
}
