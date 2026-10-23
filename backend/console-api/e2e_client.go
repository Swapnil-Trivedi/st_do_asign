package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type e2eURLs struct {
	ControlPlane string
	RateLimiter  string
	Mock         string
	Vegeta       string
	PolicyWait   time.Duration
	VegetaPoll   time.Duration
}

func loadE2EURLs() e2eURLs {
	wait, err := time.ParseDuration(envOr("E2E_POLICY_WAIT", "7s"))
	if err != nil || wait <= 0 {
		wait = 7 * time.Second
	}
	poll, err := time.ParseDuration(envOr("E2E_VEGETA_POLL", "500ms"))
	if err != nil || poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return e2eURLs{
		// Defaults assume host-side console-api; compose overrides with st_net DNS names.
		ControlPlane: strings.TrimRight(envOr("CONTROL_PLANE_URL", "http://localhost:8091"), "/"),
		RateLimiter:  strings.TrimRight(envOr("RATE_LIMITER_URL", "http://localhost:8080"), "/"),
		Mock:         strings.TrimRight(envOr("MOCK_SERVER_URL", "http://localhost:8081"), "/"),
		Vegeta:       strings.TrimRight(envOr("VEGETA_URL", "http://localhost:8090"), "/"),
		PolicyWait:   wait,
		VegetaPoll:   poll,
	}
}

type httpClient struct {
	cli *http.Client
	u   e2eURLs
}

func newHTTPClient(u e2eURLs) *httpClient {
	return &httpClient{
		cli: &http.Client{Timeout: 30 * time.Second},
		u:   u,
	}
}

func (c *httpClient) doJSON(method, url string, headers map[string]string, body any) (int, []byte, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.cli.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, raw, res.Header.Clone(), nil
}

func (c *httpClient) probeStack() (ok bool, detail string) {
	code, raw, _, err := c.doJSON(http.MethodGet, c.u.RateLimiter+"/healthz", nil, nil)
	if err != nil {
		return false, "rate-limiter: " + err.Error()
	}
	if code != 200 {
		return false, fmt.Sprintf("rate-limiter healthz %d %s", code, truncateStr(string(raw), 80))
	}
	code, _, _, err = c.doJSON(http.MethodGet, c.u.ControlPlane+"/healthz", nil, nil)
	if err != nil {
		return false, "control-plane: " + err.Error()
	}
	if code != 200 {
		return false, fmt.Sprintf("control-plane healthz %d", code)
	}
	return true, "ok"
}

type cpConfig struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Algorithm string         `json:"algorithm"`
	Params    map[string]any `json:"params"`
	Enabled   bool           `json:"enabled"`
}

type cpNamespace struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	UpstreamHost string  `json:"upstreamHost"`
	Path         *string `json:"path"`
	ConfigID     string  `json:"configId"`
	Algorithm    string  `json:"algorithm"`
	MatchMode    string  `json:"matchMode"`
	Priority     int     `json:"priority"`
	Enabled      bool    `json:"enabled"`
}

type cpRule struct {
	ID    string `json:"id"`
	Match struct {
		Kind  string `json:"kind"`
		Name  string `json:"name,omitempty"`
		Op    string `json:"op,omitempty"`
		Value string `json:"value,omitempty"`
	} `json:"match"`
}

func (c *httpClient) listConfigs() ([]cpConfig, error) {
	code, raw, _, err := c.doJSON(http.MethodGet, c.u.ControlPlane+"/api/v1/configs", nil, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("list configs %d: %s", code, truncateStr(string(raw), 120))
	}
	var wrap struct {
		Configs []cpConfig `json:"configs"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	return wrap.Configs, nil
}

func (c *httpClient) listNamespaces() ([]cpNamespace, error) {
	code, raw, _, err := c.doJSON(http.MethodGet, c.u.ControlPlane+"/api/v1/namespaces", nil, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("list namespaces %d: %s", code, truncateStr(string(raw), 120))
	}
	var wrap struct {
		Namespaces []cpNamespace `json:"namespaces"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	return wrap.Namespaces, nil
}

func (c *httpClient) ensurePolicy(s e2eSetup) (nsLabel string, steps []E2EStep, err error) {
	if s.ReuseNamespaceLabel != "" {
		nss, e := c.listNamespaces()
		if e != nil {
			return "", nil, e
		}
		for _, n := range nss {
			if n.Label == s.ReuseNamespaceLabel && n.Enabled {
				return n.Label, []E2EStep{{Name: "reuse namespace", OK: true, Detail: n.Label}}, nil
			}
		}
		return "", nil, fmt.Errorf("namespace %q not found or disabled", s.ReuseNamespaceLabel)
	}

	configs, err := c.listConfigs()
	if err != nil {
		return "", nil, err
	}
	var cfgID string
	for _, cfg := range configs {
		if cfg.Name == s.ConfigName {
			cfgID = cfg.ID
			steps = append(steps, E2EStep{Name: "reuse config", OK: true, Detail: cfg.Name})
			break
		}
	}
	if cfgID == "" {
		code, raw, _, e := c.doJSON(http.MethodPost, c.u.ControlPlane+"/api/v1/configs", nil, map[string]any{
			"name": s.ConfigName, "algorithm": s.Algorithm, "params": s.Params, "enabled": true,
		})
		if e != nil {
			return "", steps, e
		}
		if code != 201 {
			return "", steps, fmt.Errorf("create config %d: %s", code, truncateStr(string(raw), 160))
		}
		var created cpConfig
		_ = json.Unmarshal(raw, &created)
		cfgID = created.ID
		steps = append(steps, E2EStep{Name: "create config", OK: true, Detail: s.ConfigName})
	}

	nss, err := c.listNamespaces()
	if err != nil {
		return "", steps, err
	}
	var nsID string
	for _, n := range nss {
		if n.Label == s.NamespaceLabel {
			nsID = n.ID
			// ensure enabled
			if !n.Enabled {
				_, _, _, _ = c.doJSON(http.MethodPut, c.u.ControlPlane+"/api/v1/namespaces/"+n.ID, nil, map[string]any{"enabled": true})
			}
			steps = append(steps, E2EStep{Name: "reuse namespace", OK: true, Detail: n.Label})
			break
		}
	}
	if nsID == "" {
		pri := s.Priority
		if pri == 0 {
			pri = 5
		}
		code, raw, _, e := c.doJSON(http.MethodPost, c.u.ControlPlane+"/api/v1/namespaces", nil, map[string]any{
			"label": s.NamespaceLabel, "upstreamHost": c.u.Mock, "path": nil,
			"configId": cfgID, "matchMode": "all", "priority": pri, "enabled": true,
		})
		if e != nil {
			return "", steps, e
		}
		if code != 201 {
			return "", steps, fmt.Errorf("create namespace %d: %s", code, truncateStr(string(raw), 160))
		}
		var created cpNamespace
		_ = json.Unmarshal(raw, &created)
		nsID = created.ID
		steps = append(steps, E2EStep{Name: "create namespace", OK: true, Detail: s.NamespaceLabel})
	}

	code, raw, _, err := c.doJSON(http.MethodGet, c.u.ControlPlane+"/api/v1/namespaces/"+nsID+"/rules", nil, nil)
	if err != nil {
		return "", steps, err
	}
	var rulesWrap struct {
		Rules []cpRule `json:"rules"`
	}
	_ = json.Unmarshal(raw, &rulesWrap)
	if code == 200 && len(rulesWrap.Rules) == 0 {
		for _, body := range []map[string]any{
			{"label": "e2e-header", "enabled": true, "match": map[string]any{"kind": "header", "name": "US-API-Key", "op": "eq", "value": s.APIKey}},
			{"label": "e2e-path", "enabled": true, "match": map[string]any{"kind": "path", "op": "eq", "value": s.Path}},
			{"label": "e2e-method", "enabled": true, "match": map[string]any{"kind": "method", "op": "eq", "value": "GET"}},
		} {
			rc, rraw, _, e := c.doJSON(http.MethodPost, c.u.ControlPlane+"/api/v1/namespaces/"+nsID+"/rules", nil, body)
			if e != nil || (rc != 201 && rc != 200) {
				return "", steps, fmt.Errorf("create rule: %v %s", e, truncateStr(string(rraw), 100))
			}
		}
		steps = append(steps, E2EStep{Name: "create rules", OK: true, Detail: "header+path+method"})
	} else {
		steps = append(steps, E2EStep{Name: "rules ready", OK: true, Detail: fmt.Sprintf("%d rules", len(rulesWrap.Rules))})
	}
	return s.NamespaceLabel, steps, nil
}

func (c *httpClient) setNamespaceEnabled(label string, enabled bool) error {
	nss, err := c.listNamespaces()
	if err != nil {
		return err
	}
	for _, n := range nss {
		if n.Label == label {
			code, raw, _, e := c.doJSON(http.MethodPut, c.u.ControlPlane+"/api/v1/namespaces/"+n.ID, nil, map[string]any{"enabled": enabled})
			if e != nil {
				return e
			}
			if code != 200 {
				return fmt.Errorf("update namespace %d: %s", code, truncateStr(string(raw), 100))
			}
			return nil
		}
	}
	return fmt.Errorf("namespace %q not found", label)
}

func (c *httpClient) peekQuota(nsLabel, apiKey string) (*QuotaSnapshot, error) {
	url := fmt.Sprintf("%s/api/v1/quota?namespace=%s&apiKey=%s", c.u.RateLimiter, nsLabel, apiKey)
	code, raw, _, err := c.doJSON(http.MethodGet, url, nil, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("quota %d: %s", code, truncateStr(string(raw), 120))
	}
	var q struct {
		Namespace string `json:"namespace"`
		Subject   string `json:"subject"`
		Limit     int64  `json:"limit"`
		Remaining int64  `json:"remaining"`
		Consumed  int64  `json:"consumed"`
		ResetMs   int64  `json:"resetMs"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &QuotaSnapshot{
		Namespace: q.Namespace, Subject: q.Subject, Limit: q.Limit,
		Remaining: q.Remaining, Consumed: q.Consumed, ResetMs: q.ResetMs, Algorithm: q.Algorithm,
	}, nil
}

func (c *httpClient) mockLogs() ([]map[string]any, error) {
	code, raw, _, err := c.doJSON(http.MethodGet, c.u.Mock+"/api/v1/logs?limit=200", nil, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("logs %d", code)
	}
	var wrap struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	return wrap.Logs, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func headerMap(h http.Header, keys ...string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func parseLatencyMs(h http.Header) (int, bool) {
	v := h.Get("US-Upstream-Latency-Ms")
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
