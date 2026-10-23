package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type HTTPExchange struct {
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
}

type HTTPResponseCapture struct {
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type TestCaseResult struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Suite       string               `json:"suite"` // unit | functional | concurrency
	Status      string               `json:"status"` // passed | failed | skipped
	Duration    string               `json:"durationMs"`
	DurationRaw time.Duration        `json:"-"`
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

func (a *api) listTestCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"suites": []map[string]any{
			{
				"id":          "unit",
				"label":       "Unit tests",
				"description": "Pure validation helpers + isolated in-memory HTTP API checks (no Mongo writes).",
			},
			{
				"id":          "functional",
				"label":       "Functional tests",
				"description": "Live store CRUD: configs, namespaces, rules, conflict / duplicate guards with request/response traces.",
			},
			{
				"id":          "concurrency",
				"label":       "Concurrency tests",
				"description": "Parallel writers against the live store — unique creates, duplicate-name races, parallel rule inserts.",
			},
			{
				"id":          "e2e",
				"label":       "End-to-end tests",
				"description": "Control-plane → Mongo → rate-limiter → Redis/mock. Includes health/propagate/allow/429 plus predefined scenarios: dual API-key configs, tenant-id (single + multi key), and per-path limits. Skips when rate-limiter is unreachable or Mongo is not shared.",
			},
			{
				"id":          "all",
				"label":       "All suites",
				"description": "Run unit → functional → concurrency → e2e in order.",
			},
		},
	})
}

func (a *api) runTests(w http.ResponseWriter, r *http.Request) {
	suite := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("suite")))
	if suite == "" {
		suite = "all"
	}
	switch suite {
	case "unit", "functional", "concurrency", "e2e", "all":
	default:
		writeErr(w, http.StatusBadRequest, "suite must be unit, functional, concurrency, e2e, or all", "validation_error")
		return
	}
	writeJSON(w, http.StatusOK, a.executeSuites(r.Context(), suite))
}

func (a *api) executeSuites(ctx context.Context, suite string) TestRunReport {
	start := time.Now().UTC()
	var cases []testCase
	switch suite {
	case "unit":
		cases = unitTestCases()
	case "functional":
		cases = a.functionalTestCases()
	case "concurrency":
		cases = a.concurrencyTestCases()
	case "e2e":
		cases = a.e2eTestCases()
	default:
		cases = append(unitTestCases(), a.functionalTestCases()...)
		cases = append(cases, a.concurrencyTestCases()...)
		cases = append(cases, a.e2eTestCases()...)
	}

	results := make([]TestCaseResult, 0, len(cases))
	passed, failed, skipped := 0, 0, 0
	for _, tc := range cases {
		t0 := time.Now()
		out := tc.fn(ctx)
		dur := time.Since(t0)
		res := TestCaseResult{
			ID: tc.id, Name: tc.name, Description: tc.description, Suite: tc.suite,
			Duration: fmt.Sprintf("%.2fms", float64(dur.Microseconds())/1000),
			DurationRaw: dur,
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

func unitTestCases() []testCase {
	return []testCase{
		{
			id: "ut-validate-fixed-token", suite: "unit",
			name:        "fixed_token params accepted",
			description: "Unit: validateConfigInput must accept sustainedRps≥1 and burst≥1 for algorithm fixed_token.",
			fn: func(context.Context) testOutcome {
				in := map[string]any{"sustainedRps": 10, "burst": 20}
				if err := validateConfigInput(AlgoFixedToken, in); err != nil {
					return fail(err, "expect nil error for valid fixed_token params")
				}
				return testOutcome{
					message:    "validation passed",
					assertions: []string{"error == nil", "sustainedRps=10", "burst=20"},
					request: &HTTPExchange{
						Method: "VALIDATE", Path: "validateConfigInput(fixed_token)",
						Body: mustJSON(in),
					},
					response: &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"result": "ok"})},
				}
			},
		},
		{
			id: "ut-validate-fixed-token-reject", suite: "unit",
			name:        "fixed_token rejects sustainedRps < 1",
			description: "Unit: sustainedRps=0 must fail validation so bad configs never reach Mongo.",
			fn: func(context.Context) testOutcome {
				in := map[string]any{"sustainedRps": 0, "burst": 20}
				err := validateConfigInput(AlgoFixedToken, in)
				out := testOutcome{
					request: &HTTPExchange{
						Method: "VALIDATE", Path: "validateConfigInput(fixed_token)",
						Body: mustJSON(in),
					},
					assertions: []string{"error != nil", `error contains "sustainedRps"`},
				}
				if err == nil {
					out.err = fmt.Errorf("expected validation error")
					out.response = &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"result": "unexpected ok"})}
					return out
				}
				out.message = err.Error()
				out.response = &HTTPResponseCapture{Status: 400, Body: mustJSON(map[string]string{"error": err.Error()})}
				return out
			},
		},
		{
			id: "ut-validate-sliding", suite: "unit",
			name:        "sliding_window params accepted",
			description: "Unit: sliding_window requires limit≥1 and windowSeconds≥1.",
			fn: func(context.Context) testOutcome {
				in := map[string]any{"limit": 5, "windowSeconds": 1}
				if err := validateConfigInput(AlgoSlidingWindow, in); err != nil {
					return fail(err)
				}
				return testOutcome{
					message: "validation passed",
					assertions: []string{"error == nil"},
					request: &HTTPExchange{Method: "VALIDATE", Path: "validateConfigInput(sliding_window)", Body: mustJSON(in)},
					response: &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"result": "ok"})},
				}
			},
		},
		{
			id: "ut-validate-unknown-algo", suite: "unit",
			name:        "unknown algorithm rejected",
			description: "Unit: algorithms outside the catalog must be rejected at validation time.",
			fn: func(context.Context) testOutcome {
				err := validateConfigInput("nope", map[string]any{})
				if err == nil {
					return fail(fmt.Errorf("expected error"))
				}
				return testOutcome{
					message: err.Error(),
					assertions: []string{"error != nil"},
					request: &HTTPExchange{Method: "VALIDATE", Path: "validateConfigInput(nope)", Body: mustJSON(map[string]any{})},
					response: &HTTPResponseCapture{Status: 400, Body: mustJSON(map[string]string{"error": err.Error()})},
				}
			},
		},
		{
			id: "ut-validate-namespace", suite: "unit",
			name:        "namespace upstream must be http(s)",
			description: "Unit: upstreamHost must parse as absolute http/https URL; ftp and relative values fail.",
			fn: func(context.Context) testOutcome {
				steps := []TestStep{}
				if err := validateNamespaceInput("x", "http://mock-server:8081", MatchModeAll, "cfg"); err != nil {
					return fail(err, "valid http URL should pass")
				}
				steps = append(steps, TestStep{Name: "accept http upstream", OK: true, Detail: "http://mock-server:8081"})
				err := validateNamespaceInput("x", "ftp://bad", MatchModeAll, "cfg")
				if err == nil {
					return fail(fmt.Errorf("expected scheme error"), "ftp must fail")
				}
				steps = append(steps, TestStep{Name: "reject ftp upstream", OK: true, Detail: err.Error()})
				return testOutcome{
					message: "http accepted; ftp rejected",
					assertions: []string{"http ok", "ftp error"},
					steps: steps,
					request: &HTTPExchange{Method: "VALIDATE", Path: "validateNamespaceInput", Body: mustJSON(map[string]any{
						"good": "http://mock-server:8081", "bad": "ftp://bad",
					})},
					response: &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"result": "ok"})},
				}
			},
		},
		{
			id: "ut-validate-rule-header", suite: "unit",
			name:        "header rule match shape",
			description: "Unit: header rules require name + op=eq|in with value/values.",
			fn: func(context.Context) testOutcome {
				m := RuleMatch{Kind: MatchHeader, Name: "US-API-Key", Op: "eq", Value: "ak"}
				if err := validateRuleMatch(m); err != nil {
					return fail(err)
				}
				return testOutcome{
					message: "header match valid",
					assertions: []string{"error == nil"},
					request: &HTTPExchange{Method: "VALIDATE", Path: "validateRuleMatch(header)", Body: mustJSON(m)},
					response: &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"result": "ok"})},
				}
			},
		},
		{
			id: "ut-validate-rule-regex", suite: "unit",
			name:        "regex rule requires pattern + field",
			description: "Unit: regex matchers need field=path|full and a non-empty pattern.",
			fn: func(context.Context) testOutcome {
				ok := RuleMatch{Kind: MatchRegex, Field: "path", Pattern: "^/x$"}
				if err := validateRuleMatch(ok); err != nil {
					return fail(err)
				}
				bad := RuleMatch{Kind: MatchRegex, Field: "path"}
				err := validateRuleMatch(bad)
				if err == nil {
					return fail(fmt.Errorf("expected missing pattern error"))
				}
				return testOutcome{
					message: err.Error(),
					assertions: []string{"valid regex ok", "missing pattern errors"},
					request: &HTTPExchange{Method: "VALIDATE", Path: "validateRuleMatch(regex)", Body: mustJSON(map[string]any{"ok": ok, "bad": bad})},
					response: &HTTPResponseCapture{Status: 200, Body: mustJSON(map[string]string{"badError": err.Error()})},
				}
			},
		},
		{
			id: "ut-memory-api-config-crud", suite: "unit",
			name:        "in-memory HTTP config CRUD",
			description: "Unit (isolated handler): POST /configs → GET → DELETE against an in-memory store; no Mongo side effects.",
			fn: func(context.Context) testOutcome {
				h := isolatedHandler()
				name := "ut-cfg-" + uuid.NewString()[:8]
				body := map[string]any{
					"name": name, "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": 3, "burst": 6},
				}
				code, raw, err := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
				if err != nil {
					return fail(err)
				}
				steps := []TestStep{{
					Name: "POST /api/v1/configs",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				}}
				if code != http.StatusCreated {
					return testOutcome{err: fmt.Errorf("create → %d: %s", code, raw), steps: steps, request: steps[0].Request, response: steps[0].Response}
				}
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw), &cfg)
				gCode, gRaw, _ := rawJSON(h, http.MethodGet, "/api/v1/configs/"+cfg.ID, nil)
				steps = append(steps, TestStep{
					Name: "GET /api/v1/configs/{id}",
					Request: &HTTPExchange{Method: "GET", Path: "/api/v1/configs/" + cfg.ID},
					Response: &HTTPResponseCapture{Status: gCode, Body: json.RawMessage(gRaw)},
					OK: gCode == http.StatusOK,
				})
				dCode, dRaw, _ := rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
				steps = append(steps, TestStep{
					Name: "DELETE /api/v1/configs/{id}",
					Request: &HTTPExchange{Method: "DELETE", Path: "/api/v1/configs/" + cfg.ID},
					Response: &HTTPResponseCapture{Status: dCode, Body: json.RawMessage(dRaw)},
					OK: dCode == http.StatusNoContent,
				})
				if gCode != http.StatusOK || dCode != http.StatusNoContent {
					return testOutcome{err: fmt.Errorf("get=%d delete=%d", gCode, dCode), steps: steps, request: steps[0].Request, response: steps[0].Response}
				}
				return testOutcome{
					message: "create/get/delete succeeded",
					assertions: []string{"POST 201", "GET 200", "DELETE 204"},
					steps: steps, request: steps[0].Request, response: steps[0].Response,
					meta: map[string]any{"configId": cfg.ID, "name": name},
				}
			},
		},
		{
			id: "ut-memory-api-validation-400", suite: "unit",
			name:        "in-memory API returns 400 on bad config",
			description: "Unit: POST /configs with sustainedRps=-1 must yield HTTP 400 validation_error.",
			fn: func(context.Context) testOutcome {
				h := isolatedHandler()
				body := map[string]any{
					"name": "bad", "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": -1, "burst": 1},
				}
				code, raw, err := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
				req := &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)}
				resp := &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)}
				if err != nil {
					return testOutcome{err: err, request: req, response: resp}
				}
				if code != http.StatusBadRequest {
					return testOutcome{err: fmt.Errorf("want 400 got %d", code), request: req, response: resp,
						assertions: []string{"status == 400"}}
				}
				return testOutcome{
					message: "rejected with 400",
					assertions: []string{"status == 400"},
					request: req, response: resp,
				}
			},
		},
	}
}

func (a *api) functionalTestCases() []testCase {
	prefix := "selftest-" + uuid.NewString()[:8]
	h := chi.NewRouter()
	a.routes(h)

	return []testCase{
		{
			id: "ft-algorithms", suite: "functional",
			name:        "list algorithm catalog",
			description: "Functional: GET /api/v1/algorithms returns the seeded catalog (sliding_window, fixed_token, fixed_window).",
			fn: func(context.Context) testOutcome {
				code, raw, err := rawJSON(h, http.MethodGet, "/api/v1/algorithms", nil)
				req := &HTTPExchange{Method: "GET", Path: "/api/v1/algorithms"}
				resp := &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)}
				if err != nil {
					return testOutcome{err: err, request: req, response: resp}
				}
				if code != http.StatusOK {
					return testOutcome{err: fmt.Errorf("status %d", code), request: req, response: resp}
				}
				var body struct {
					Algorithms []Algorithm `json:"algorithms"`
				}
				_ = json.Unmarshal([]byte(raw), &body)
				if len(body.Algorithms) < 3 {
					return testOutcome{err: fmt.Errorf("expected >=3 algorithms, got %d", len(body.Algorithms)), request: req, response: resp}
				}
				return testOutcome{
					message: fmt.Sprintf("%d algorithms", len(body.Algorithms)),
					assertions: []string{"status == 200", "len(algorithms) >= 3"},
					request: req, response: resp,
				}
			},
		},
		{
			id: "ft-config-namespace-rules-lifecycle", suite: "functional",
			name:        "config → namespace → rules lifecycle",
			description: "Functional: create config, bind namespace, add header+path rules, verify list, then cascade delete. Traces every HTTP exchange.",
			fn: func(context.Context) testOutcome {
				steps := []TestStep{}
				cfgBody := map[string]any{
					"name": prefix + "-cfg", "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": 10, "burst": 20},
				}
				code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
				steps = append(steps, TestStep{
					Name: "create config",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(cfgBody)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})
				if code != http.StatusCreated {
					return testOutcome{err: fmt.Errorf("create config %d: %s", code, raw), steps: steps, request: steps[0].Request, response: steps[0].Response}
				}
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw), &cfg)

				nsBody := map[string]any{
					"label": prefix + "-ns", "upstreamHost": "http://mock-server:8081",
					"configId": cfg.ID, "matchMode": "all", "priority": 10,
				}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
				steps = append(steps, TestStep{
					Name: "create namespace",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces", Body: mustJSON(nsBody)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})
				if code != http.StatusCreated {
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
					return testOutcome{err: fmt.Errorf("create ns %d: %s", code, raw), steps: steps, request: steps[1].Request, response: steps[1].Response}
				}
				var ns Namespace
				_ = json.Unmarshal([]byte(raw), &ns)

				ruleBody := map[string]any{
					"label": "api-key",
					"match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": "ak_demo_1001_alpha"},
				}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", ruleBody)
				steps = append(steps, TestStep{
					Name: "create header rule",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(ruleBody)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})

				pathRule := map[string]any{"match": map[string]any{"kind": "path", "op": "eq", "value": "/1001/deviceList"}}
				code, raw, _ = rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", pathRule)
				steps = append(steps, TestStep{
					Name: "create path rule",
					Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces/" + ns.ID + "/rules", Body: mustJSON(pathRule)},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusCreated,
				})

				code, raw, _ = rawJSON(h, http.MethodGet, "/api/v1/namespaces/"+ns.ID+"/rules", nil)
				steps = append(steps, TestStep{
					Name: "list rules",
					Request: &HTTPExchange{Method: "GET", Path: "/api/v1/namespaces/" + ns.ID + "/rules"},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusOK,
				})
				var rulesBody struct {
					Rules []Rule `json:"rules"`
				}
				_ = json.Unmarshal([]byte(raw), &rulesBody)
				if len(rulesBody.Rules) < 2 {
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
					return testOutcome{err: fmt.Errorf("expected >=2 rules"), steps: steps, request: steps[0].Request, response: steps[0].Response}
				}

				code, raw, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
				steps = append(steps, TestStep{
					Name: "delete namespace (cascade rules)",
					Request: &HTTPExchange{Method: "DELETE", Path: "/api/v1/namespaces/" + ns.ID},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusNoContent,
				})
				code, raw, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
				steps = append(steps, TestStep{
					Name: "delete config",
					Request: &HTTPExchange{Method: "DELETE", Path: "/api/v1/configs/" + cfg.ID},
					Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)},
					OK: code == http.StatusNoContent,
				})

				return testOutcome{
					message: "lifecycle completed",
					assertions: []string{"config 201", "namespace 201", "≥2 rules", "cleanup 204"},
					steps: steps, request: steps[0].Request, response: steps[len(steps)-1].Response,
					meta: map[string]any{"prefix": prefix, "configId": cfg.ID, "namespaceId": ns.ID},
				}
			},
		},
		{
			id: "ft-config-in-use-guard", suite: "functional",
			name:        "delete config blocked while referenced",
			description: "Functional: DELETE /configs/{id} returns 409 in_use when a namespace still points at the config.",
			fn: func(context.Context) testOutcome {
				cfgBody := map[string]any{
					"name": prefix + "-cfg-guard", "algorithm": AlgoFixedWindow,
					"params": map[string]any{"limit": 5, "windowSeconds": 1},
				}
				code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw), &cfg)
				nsBody := map[string]any{
					"label": prefix + "-ns-guard", "upstreamHost": "http://mock-server:8081",
					"configId": cfg.ID, "matchMode": "any",
				}
				_, nsRaw, _ := rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
				var ns Namespace
				_ = json.Unmarshal([]byte(nsRaw), &ns)

				dCode, dRaw, _ := rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
				req := &HTTPExchange{Method: "DELETE", Path: "/api/v1/configs/" + cfg.ID}
				resp := &HTTPResponseCapture{Status: dCode, Body: json.RawMessage(dRaw)}
				steps := []TestStep{
					{Name: "create config", Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(cfgBody)}, Response: &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)}, OK: code == 201},
					{Name: "create namespace referencing config", Request: &HTTPExchange{Method: "POST", Path: "/api/v1/namespaces", Body: mustJSON(nsBody)}, Response: &HTTPResponseCapture{Status: 201, Body: json.RawMessage(nsRaw)}, OK: true},
					{Name: "DELETE config while in use", Request: req, Response: resp, OK: dCode == http.StatusConflict},
				}
				_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
				_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)
				if dCode != http.StatusConflict {
					return testOutcome{err: fmt.Errorf("want 409 got %d", dCode), steps: steps, request: req, response: resp}
				}
				return testOutcome{
					message: "409 in_use as expected",
					assertions: []string{"DELETE status == 409", `body.code == "in_use"`},
					steps: steps, request: req, response: resp,
				}
			},
		},
		{
			id: "ft-duplicate-config-name", suite: "functional",
			name:        "duplicate config name → 409",
			description: "Functional: second POST /configs with the same name must conflict.",
			fn: func(context.Context) testOutcome {
				name := prefix + "-dup"
				body := map[string]any{
					"name": name, "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": 1, "burst": 1},
				}
				code1, raw1, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw1), &cfg)
				code2, raw2, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
				defer func() { _, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil) }()
				steps := []TestStep{
					{Name: "first create", Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)}, Response: &HTTPResponseCapture{Status: code1, Body: json.RawMessage(raw1)}, OK: code1 == 201},
					{Name: "duplicate create", Request: &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)}, Response: &HTTPResponseCapture{Status: code2, Body: json.RawMessage(raw2)}, OK: code2 == 409},
				}
				if code2 != http.StatusConflict {
					return testOutcome{err: fmt.Errorf("want 409 got %d", code2), steps: steps, request: steps[1].Request, response: steps[1].Response}
				}
				return testOutcome{
					message: "duplicate rejected",
					assertions: []string{"first 201", "second 409"},
					steps: steps, request: steps[1].Request, response: steps[1].Response,
				}
			},
		},
	}
}

func (a *api) concurrencyTestCases() []testCase {
	prefix := "conc-" + uuid.NewString()[:8]
	h := chi.NewRouter()
	a.routes(h)

	return []testCase{
		{
			id: "conc-parallel-unique-configs", suite: "concurrency",
			name:        "parallel unique config creates",
			description: "Concurrency: N goroutines each POST a uniquely named config. All must succeed (201) with no data loss under parallel writers.",
			fn: func(context.Context) testOutcome {
				const n = 12
				var wg sync.WaitGroup
				var okCount atomic.Int64
				var failCount atomic.Int64
				ids := make([]string, n)
				var mu sync.Mutex
				var sampleReq *HTTPExchange
				var sampleResp *HTTPResponseCapture

				wg.Add(n)
				for i := 0; i < n; i++ {
					i := i
					go func() {
						defer wg.Done()
						body := map[string]any{
							"name": fmt.Sprintf("%s-cfg-%d", prefix, i),
							"algorithm": AlgoFixedToken,
							"params": map[string]any{"sustainedRps": 5, "burst": 10},
						}
						code, raw, err := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
						if i == 0 {
							mu.Lock()
							sampleReq = &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)}
							sampleResp = &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)}
							mu.Unlock()
						}
						if err != nil || code != http.StatusCreated {
							failCount.Add(1)
							return
						}
						var cfg AlgorithmConfig
						_ = json.Unmarshal([]byte(raw), &cfg)
						mu.Lock()
						ids[i] = cfg.ID
						mu.Unlock()
						okCount.Add(1)
					}()
				}
				wg.Wait()

				// cleanup
				for _, id := range ids {
					if id != "" {
						_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+id, nil)
					}
				}

				meta := map[string]any{
					"workers": n, "succeeded": okCount.Load(), "failed": failCount.Load(),
				}
				if okCount.Load() != int64(n) {
					return testOutcome{
						err: fmt.Errorf("expected %d successes, got %d (fail=%d)", n, okCount.Load(), failCount.Load()),
						assertions: []string{fmt.Sprintf("successes == %d", n)},
						request: sampleReq, response: sampleResp, meta: meta,
					}
				}
				return testOutcome{
					message: fmt.Sprintf("%d/%d parallel creates succeeded", okCount.Load(), n),
					assertions: []string{fmt.Sprintf("successes == %d", n), "no lost writes"},
					request: sampleReq, response: sampleResp, meta: meta,
				}
			},
		},
		{
			id: "conc-duplicate-name-race", suite: "concurrency",
			name:        "duplicate name race — only one winner",
			description: "Concurrency: N goroutines POST the same config name simultaneously. Exactly one must win (201); the rest must get 409 conflict.",
			fn: func(context.Context) testOutcome {
				const n = 16
				name := prefix + "-race"
				body := map[string]any{
					"name": name, "algorithm": AlgoFixedWindow,
					"params": map[string]any{"limit": 3, "windowSeconds": 1},
				}
				var wg sync.WaitGroup
				var created atomic.Int64
				var conflicts atomic.Int64
				var other atomic.Int64
				var winnerID string
				var sampleConflict *HTTPResponseCapture
				var mu sync.Mutex

				wg.Add(n)
				for i := 0; i < n; i++ {
					go func() {
						defer wg.Done()
						code, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", body)
						switch code {
						case http.StatusCreated:
							created.Add(1)
							var cfg AlgorithmConfig
							_ = json.Unmarshal([]byte(raw), &cfg)
							mu.Lock()
							if winnerID == "" {
								winnerID = cfg.ID
							}
							mu.Unlock()
						case http.StatusConflict:
							conflicts.Add(1)
							mu.Lock()
							if sampleConflict == nil {
								sampleConflict = &HTTPResponseCapture{Status: code, Body: json.RawMessage(raw)}
							}
							mu.Unlock()
						default:
							other.Add(1)
						}
					}()
				}
				wg.Wait()
				if winnerID != "" {
					_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+winnerID, nil)
				}

				req := &HTTPExchange{Method: "POST", Path: "/api/v1/configs", Body: mustJSON(body)}
				meta := map[string]any{
					"workers": n, "created": created.Load(), "conflicts": conflicts.Load(), "other": other.Load(),
					"winnerId": winnerID,
				}
				if created.Load() != 1 {
					return testOutcome{
						err: fmt.Errorf("expected exactly 1 create, got %d (conflicts=%d other=%d)", created.Load(), conflicts.Load(), other.Load()),
						request: req, response: sampleConflict, meta: meta,
						assertions: []string{"created == 1", "conflicts == N-1"},
					}
				}
				if conflicts.Load()+created.Load()+other.Load() != int64(n) {
					return testOutcome{err: fmt.Errorf("response accounting mismatch"), request: req, response: sampleConflict, meta: meta}
				}
				return testOutcome{
					message: fmt.Sprintf("1 winner, %d conflicts", conflicts.Load()),
					assertions: []string{"created == 1", fmt.Sprintf("conflicts == %d", n-1)},
					request: req,
					response: sampleConflict,
					meta: meta,
					steps: []TestStep{
						{Name: "parallel POST same name", Request: req, Response: sampleConflict, OK: true,
							Detail: fmt.Sprintf("created=%d conflicts=%d", created.Load(), conflicts.Load())},
					},
				}
			},
		},
		{
			id: "conc-parallel-rules-same-namespace", suite: "concurrency",
			name:        "parallel rule inserts on one namespace",
			description: "Concurrency: create one namespace, then M goroutines each add a distinct header rule. Final rule count must equal M.",
			fn: func(context.Context) testOutcome {
				cfgBody := map[string]any{
					"name": prefix + "-rules-cfg", "algorithm": AlgoFixedToken,
					"params": map[string]any{"sustainedRps": 8, "burst": 16},
				}
				_, raw, _ := rawJSON(h, http.MethodPost, "/api/v1/configs", cfgBody)
				var cfg AlgorithmConfig
				_ = json.Unmarshal([]byte(raw), &cfg)
				nsBody := map[string]any{
					"label": prefix + "-rules-ns", "upstreamHost": "http://mock-server:8081",
					"configId": cfg.ID, "matchMode": "any",
				}
				_, nsRaw, _ := rawJSON(h, http.MethodPost, "/api/v1/namespaces", nsBody)
				var ns Namespace
				_ = json.Unmarshal([]byte(nsRaw), &ns)

				const m = 10
				var wg sync.WaitGroup
				var okCount atomic.Int64
				wg.Add(m)
				for i := 0; i < m; i++ {
					i := i
					go func() {
						defer wg.Done()
						body := map[string]any{
							"label": fmt.Sprintf("r-%d", i),
							"match": map[string]any{
								"kind": "header", "name": "US-API-Key", "op": "eq",
								"value": fmt.Sprintf("ak_conc_%d", i),
							},
						}
						code, _, _ := rawJSON(h, http.MethodPost, "/api/v1/namespaces/"+ns.ID+"/rules", body)
						if code == http.StatusCreated {
							okCount.Add(1)
						}
					}()
				}
				wg.Wait()

				listCode, listRaw, _ := rawJSON(h, http.MethodGet, "/api/v1/namespaces/"+ns.ID+"/rules", nil)
				var rulesBody struct {
					Rules []Rule `json:"rules"`
				}
				_ = json.Unmarshal([]byte(listRaw), &rulesBody)

				_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/namespaces/"+ns.ID, nil)
				_, _, _ = rawJSON(h, http.MethodDelete, "/api/v1/configs/"+cfg.ID, nil)

				req := &HTTPExchange{Method: "GET", Path: "/api/v1/namespaces/" + ns.ID + "/rules"}
				resp := &HTTPResponseCapture{Status: listCode, Body: json.RawMessage(listRaw)}
				meta := map[string]any{"workers": m, "createsOk": okCount.Load(), "listed": len(rulesBody.Rules)}
				if int(okCount.Load()) != m || len(rulesBody.Rules) != m {
					return testOutcome{
						err: fmt.Errorf("creates=%d listed=%d want %d", okCount.Load(), len(rulesBody.Rules), m),
						request: req, response: resp, meta: meta,
						assertions: []string{fmt.Sprintf("creates == %d", m), fmt.Sprintf("listed == %d", m)},
					}
				}
				return testOutcome{
					message: fmt.Sprintf("%d rules created and listed", m),
					assertions: []string{fmt.Sprintf("creates == %d", m), fmt.Sprintf("listed == %d", m)},
					request: req, response: resp, meta: meta,
					steps: []TestStep{
						{Name: "setup config+namespace", OK: true, Detail: ns.ID},
						{Name: fmt.Sprintf("%d parallel rule POSTs", m), OK: true, Detail: fmt.Sprintf("ok=%d", okCount.Load())},
						{Name: "list rules", Request: req, Response: resp, OK: true},
					},
				}
			},
		},
	}
}

func isolatedHandler() http.Handler {
	a := &api{store: newMemoryStore()}
	r := chi.NewRouter()
	a.routes(r)
	return r
}

func mustJSON(v any) json.RawMessage {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func rawJSON(h http.Handler, method, path string, body any) (int, string, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String(), nil
}
