package main

import "net/http"

// Scenario is a preconfigured load test for one-click validation (UI + API).
type Scenario struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Intent       string           `json:"intent"`
	Tags         []string         `json:"tags"`
	DurationHint string           `json:"durationHint"`
	Request      CreateRunRequest `json:"request"`
}

func builtInScenarios(defaultBaseURL string) []Scenario {
	hdr := func(key string) map[string]string {
		return map[string]string{"US-API-Key": key}
	}
	target := func(method, path, key string) TargetSpec {
		return TargetSpec{
			BaseURL: defaultBaseURL,
			Method:  method,
			Path:    path,
			Headers: hdr(key),
		}
	}

	return []Scenario{
		{
			ID:           "smoke-steady",
			Name:         "Smoke — steady trickle",
			Description:  "Lowest-cost check that the path is reachable. ~10s at 2 RPS.",
			Intent:       "Prove the mock upstream (or rate limiter) accepts traffic end-to-end with minimal load. Use first after bringing the stack up.",
			Tags:         []string{"smoke", "steady", "quick"},
			DurationHint: "~10s",
			Request: CreateRunRequest{
				Profile: ProfileSteady,
				Target:  target(http.MethodGet, "/1001/deviceList", "ak_demo_1001_alpha"),
				Steady:  &SteadyConfig{RPS: 2, Duration: "10s"},
			},
		},
		{
			ID:           "steady-validate-list",
			Name:         "Steady — deviceList validation",
			Description:  "Sustained GET /deviceList at 15 RPS for 20s. Good baseline before limiter tuning.",
			Intent:       "Establish a baseline success ratio and latency under constant load. Compare later runs after rate-limit rules change.",
			Tags:         []string{"steady", "deviceList", "baseline"},
			DurationHint: "~20s",
			Request: CreateRunRequest{
				Profile: ProfileSteady,
				Target:  target(http.MethodGet, "/1001/deviceList", "ak_demo_1001_alpha"),
				Steady:  &SteadyConfig{RPS: 15, Duration: "20s"},
			},
		},
		{
			ID:           "steady-alt-key",
			Name:         "Steady — alternate API key",
			Description:  "Same tenant 1001 using mobile key. Confirms multi-key logging / match headers.",
			Intent:       "Validate multi-key-per-tenant mapping: US-API-Key-Match should be true for ak_demo_1001_mobile and mock logs should show the alternate key.",
			Tags:         []string{"steady", "api-key", "tenant-1001"},
			DurationHint: "~15s",
			Request: CreateRunRequest{
				Profile: ProfileSteady,
				Target:  target(http.MethodGet, "/1001/deviceList", "ak_demo_1001_mobile"),
				Steady:  &SteadyConfig{RPS: 10, Duration: "15s"},
			},
		},
		{
			ID:           "ramp-warmup",
			Name:         "Ramp — warm-up",
			Description:  "Linear climb 2→25 RPS over 5 steps × 3s. Watch latency climb with mock 500ms delay.",
			Intent:       "Observe how p50/p95 degrade as RPS rises against the 500ms mock delay. Good warm-up before stress scenarios.",
			Tags:         []string{"ramp", "warmup"},
			DurationHint: "~15s",
			Request: CreateRunRequest{
				Profile: ProfileRamp,
				Target:  target(http.MethodGet, "/1001/deviceList", "ak_demo_1001_partner"),
				Ramp: &RampConfig{
					FromRPS: 2, ToRPS: 25, Steps: 5, StepDuration: "3s",
				},
			},
		},
		{
			ID:           "ramp-aggressive",
			Name:         "Ramp — aggressive",
			Description:  "Steeper ramp 5→60 RPS over 6×4s steps. Useful once rate limiter is in path.",
			Intent:       "Push load high enough to exercise deny/allow boundaries on the future rate limiter. Expect increasing errors or 429s when limits are enforced.",
			Tags:         []string{"ramp", "stress", "limiter"},
			DurationHint: "~24s",
			Request: CreateRunRequest{
				Profile: ProfileRamp,
				Target:  target(http.MethodGet, "/1002/deviceList", "ak_demo_1002_beta"),
				Ramp: &RampConfig{
					FromRPS: 5, ToRPS: 60, Steps: 6, StepDuration: "4s",
				},
			},
		},
		{
			ID:           "burst-quick-spike",
			Name:         "Burst — quick spikes",
			Description:  "Base 5 RPS with 40 RPS bursts every 6s for 1.5s. Total 18s. Immediate traffic spikes.",
			Intent:       "Simulate bursty clients. Confirms the system survives short spikes without hanging; watch mock logs for clustered requests.",
			Tags:         []string{"burst", "spike", "quick"},
			DurationHint: "~18s",
			Request: CreateRunRequest{
				Profile: ProfileBurst,
				Target:  target(http.MethodGet, "/1001/deviceList", "ak_demo_1001_alpha"),
				Burst: &BurstConfig{
					BaseRPS: 5, BurstRPS: 40,
					BurstEvery: "6s", BurstDuration: "1.5s", Duration: "18s",
				},
			},
		},
		{
			ID:           "burst-heavy",
			Name:         "Burst — heavy hammer",
			Description:  "Base 10 RPS, spikes to 100 RPS for 2s every 8s over 32s. Stress / deny-path rehearsal.",
			Intent:       "Rehearse rate-limit deny path and recovery between spikes. Primary scenario for demoing limiter effectiveness.",
			Tags:         []string{"burst", "stress", "limiter"},
			DurationHint: "~32s",
			Request: CreateRunRequest{
				Profile: ProfileBurst,
				Target:  target(http.MethodGet, "/1002/deviceList", "ak_demo_1002_ci"),
				Burst: &BurstConfig{
					BaseRPS: 10, BurstRPS: 100,
					BurstEvery: "8s", BurstDuration: "2s", Duration: "32s",
				},
			},
		},
		{
			ID:           "post-register-steady",
			Name:         "Steady — registerDevice POST",
			Description:  "POST registerDevice at 8 RPS for 12s. Validates write path + body forwarding.",
			Intent:       "Confirm POST body reaches the upstream and 201 responses are counted. Ensures Vegeta targets are not GET-only.",
			Tags:         []string{"steady", "POST", "registerDevice"},
			DurationHint: "~12s",
			Request: CreateRunRequest{
				Profile: ProfileSteady,
				Target: TargetSpec{
					BaseURL: defaultBaseURL,
					Method:  http.MethodPost,
					Path:    "/1001/registerDevice",
					Headers: map[string]string{
						"US-API-Key":   "ak_demo_1001_alpha",
						"Content-Type": "application/json",
					},
					Body: `{"id":"load-dev","name":"vegeta-scenario"}`,
				},
				Steady: &SteadyConfig{RPS: 8, Duration: "12s"},
			},
		},
		{
			ID:           "tenant-1003-smoke",
			Name:         "Smoke — tenant 1003",
			Description:  "Short steady against tenant 1003 single key. Cross-tenant sanity check.",
			Intent:       "Sanity-check a different tenant/key pair so demos are not stuck on tenant 1001 only.",
			Tags:         []string{"smoke", "tenant-1003", "quick"},
			DurationHint: "~8s",
			Request: CreateRunRequest{
				Profile: ProfileSteady,
				Target:  target(http.MethodGet, "/1003/deviceList", "ak_demo_1003_gamma"),
				Steady:  &SteadyConfig{RPS: 3, Duration: "8s"},
			},
		},
	}
}

func scenarioByID(id, defaultBaseURL string) (*Scenario, bool) {
	for _, s := range builtInScenarios(defaultBaseURL) {
		if s.ID == id {
			cp := s
			return &cp, true
		}
	}
	return nil, false
}
