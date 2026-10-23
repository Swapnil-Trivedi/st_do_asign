package main

func e2eGroups() []e2eGroupInfo {
	return []e2eGroupInfo{
		{ID: e2eGroupFixtures, Label: "Fixtures", Description: "Direct HTTP algorithm / hot-reload checks"},
		{ID: e2eGroupVegeta, Label: "Vegeta", Description: "Single Vegeta profile runs through the LB"},
		{ID: e2eGroupConcurrency, Label: "Concurrency", Description: "Multi-Vegeta + hybrid load on shared Redis"},
	}
}

func allE2ESetups() []e2eSetup {
	sharedKey := "ak_e2e_shared_token"
	sharedPath := "/1001/deviceList"
	sharedCfg := "e2e-shared-fixed-token-10-20"
	sharedNS := "e2e-shared-deviceList"
	ftParams := map[string]any{"sustainedRps": 10, "burst": 20}

	burstJob := e2eVegetaJob{
		Profile: "burst",
		Burst:   &e2eBurstSpec{BaseRPS: 5, BurstRPS: 40, BurstEvery: "3s", BurstDuration: "2s", Duration: "7s"},
	}

	return []e2eSetup{
		{
			ID: "fixed-token-burst", Group: e2eGroupFixtures, Name: "Fixed token burst",
			Description: "40 concurrent requests against burst=20 → ~20 allow / ~20×429",
			Driver: e2eDriverDirect, ConfigName: "e2e-ft-burst-10-20", NamespaceLabel: "e2e-ft-burst",
			Algorithm: "fixed_token", Params: map[string]any{"sustainedRps": 10, "burst": 20},
			APIKey: "ak_e2e_ft_burst", Path: "/1001/deviceList", Priority: 5,
			DirectCount: 40, DirectConcurrent: true,
			MinAllow: 18, MaxAllow: 22, MinDeny: 18,
		},
		{
			ID: "fixed-window-tight", Group: e2eGroupFixtures, Name: "Fixed window tight",
			Description: "12 sequential against limit=5/10s → 5 allow / 7 deny",
			Driver: e2eDriverDirect, ConfigName: "e2e-fw-5-10", NamespaceLabel: "e2e-fw-tight",
			Algorithm: "fixed_window", Params: map[string]any{"limit": 5, "windowSeconds": 10},
			APIKey: "ak_e2e_fw", Path: "/1002/deviceList", Priority: 5,
			DirectCount: 12, DirectConcurrent: false,
			MinAllow: 5, MaxAllow: 5, MinDeny: 7,
		},
		{
			ID: "sliding-window-roll", Group: e2eGroupFixtures, Name: "Sliding window",
			Description: "15 concurrent against sliding limit=8 → ≤8 allow",
			Driver: e2eDriverDirect, ConfigName: "e2e-sw-8-5", NamespaceLabel: "e2e-sw-roll",
			Algorithm: "sliding_window", Params: map[string]any{"limit": 8, "windowSeconds": 5},
			APIKey: "ak_e2e_sw", Path: "/1003/deviceList", Priority: 5,
			DirectCount: 15, DirectConcurrent: true,
			MinAllow: 7, MaxAllow: 8, MinDeny: 7,
		},
		{
			ID: "demo-seed-smoke", Group: e2eGroupFixtures, Name: "Demo seed smoke",
			Description: "8 sequential under seed demo-deviceList (burst 20) — all should allow",
			Driver: e2eDriverDirect, ReuseNamespaceLabel: "demo-deviceList",
			APIKey: "ak_demo_1001_alpha", Path: "/1001/deviceList",
			DirectCount: 8, DirectConcurrent: false,
			MinAllow: 8, MaxAllow: 8,
		},
		{
			ID: "multi-instance-mix", Group: e2eGroupFixtures, Name: "Multi-instance mix",
			Description: "60 concurrent; both proxy replicas a and b should appear",
			Driver: e2eDriverDirect, ConfigName: "e2e-mi-30", NamespaceLabel: "e2e-multi-instance",
			Algorithm: "fixed_token", Params: map[string]any{"sustainedRps": 50, "burst": 30},
			APIKey: "ak_e2e_mi", Path: "/1001/deviceList", Priority: 5,
			DirectCount: 60, DirectConcurrent: true,
			MinAllow: 25, MaxAllow: 35, RequireBothProxyInstances: true,
		},
		{
			ID: "hot-reload-disable", Group: e2eGroupFixtures, Name: "Hot reload disable",
			Description: "Allow → disable namespace → poll → no match → re-enable",
			Driver: e2eDriverHotReload, ConfigName: "e2e-hot-10-20", NamespaceLabel: "e2e-hot-reload",
			Algorithm: "fixed_token", Params: map[string]any{"sustainedRps": 10, "burst": 20},
			APIKey: "ak_e2e_hot", Path: "/1002/deviceList", Priority: 5,
			HotReload: true,
		},
		{
			ID: "vegeta-steady-under", Group: e2eGroupVegeta, Name: "Vegeta steady under limit",
			Description: "Steady 5 RPS / 6s on shared burst=20 namespace — few/no 429s",
			Driver: e2eDriverVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			VegetaJobs: []e2eVegetaJob{{Profile: "steady", Steady: &e2eSteadySpec{RPS: 5, Duration: "6s"}}},
		},
		{
			ID: "vegeta-burst-over", Group: e2eGroupVegeta, Name: "Vegeta burst over limit",
			Description: "Burst spike 40 RPS — expect 429s",
			Driver: e2eDriverVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			VegetaJobs: []e2eVegetaJob{{
				Profile: "burst",
				Burst:   &e2eBurstSpec{BaseRPS: 5, BurstRPS: 40, BurstEvery: "3s", BurstDuration: "2s", Duration: "8s"},
			}},
			RequireVegeta429: true, MaxCombinedAllow: 45,
		},
		{
			ID: "vegeta-ramp-smoke", Group: e2eGroupVegeta, Name: "Vegeta ramp smoke",
			Description: "Ramp 2→12 over 3 steps — completes with metrics",
			Driver: e2eDriverVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			VegetaJobs: []e2eVegetaJob{{
				Profile: "ramp",
				Ramp:    &e2eRampSpec{FromRPS: 2, ToRPS: 12, Steps: 3, StepDuration: "2s"},
			}},
		},
		{
			ID: "concurrency-dual-vegeta-burst", Group: e2eGroupConcurrency, Name: "Dual Vegeta burst",
			Description: "2 parallel Vegeta bursts on same subject — combined allows ≤ shared burst",
			Driver: e2eDriverMultiVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			VegetaJobs:       []e2eVegetaJob{burstJob, burstJob},
			RequireVegeta429: true, MaxCombinedAllow: 50,
		},
		{
			ID: "concurrency-triple-steady", Group: e2eGroupConcurrency, Name: "Triple Vegeta steady",
			Description: "3 parallel steady 3 RPS × 5s — aggregate allows bounded by capacity",
			Driver: e2eDriverMultiVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			VegetaJobs: []e2eVegetaJob{
				{Profile: "steady", Steady: &e2eSteadySpec{RPS: 3, Duration: "5s"}},
				{Profile: "steady", Steady: &e2eSteadySpec{RPS: 3, Duration: "5s"}},
				{Profile: "steady", Steady: &e2eSteadySpec{RPS: 3, Duration: "5s"}},
			},
			MaxCombinedAllow: 55,
		},
		{
			ID: "concurrency-direct-plus-vegeta", Group: e2eGroupConcurrency, Name: "Direct + Vegeta hybrid",
			Description: "20 concurrent direct + 1 Vegeta burst overlapping shared Redis",
			Driver: e2eDriverHybrid, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			HybridDirectCount: 20,
			VegetaJobs: []e2eVegetaJob{{
				Profile: "burst",
				Burst:   &e2eBurstSpec{BaseRPS: 5, BurstRPS: 40, BurstEvery: "3s", BurstDuration: "2s", Duration: "6s"},
			}},
			RequireDeny: true, MaxCombinedAllow: 55,
		},
		{
			ID: "concurrency-multi-instance-vegeta", Group: e2eGroupConcurrency, Name: "Multi-instance + dual Vegeta",
			Description: "2 Vegeta bursts + direct probes for X-Proxy-Instance a/b; single Redis bucket",
			Driver: e2eDriverMultiVegeta, ConfigName: sharedCfg, NamespaceLabel: sharedNS,
			Algorithm: "fixed_token", Params: ftParams, APIKey: sharedKey, Path: sharedPath, Priority: 5,
			HybridDirectCount: 30,
			VegetaJobs: []e2eVegetaJob{
				{Profile: "burst", Burst: &e2eBurstSpec{BaseRPS: 8, BurstRPS: 35, BurstEvery: "3s", BurstDuration: "2s", Duration: "6s"}},
				{Profile: "burst", Burst: &e2eBurstSpec{BaseRPS: 8, BurstRPS: 35, BurstEvery: "3s", BurstDuration: "2s", Duration: "6s"}},
			},
			RequireBothProxyInstances: true, MaxCombinedAllow: 60,
		},
	}
}

func setupByID(id string) (e2eSetup, bool) {
	for _, s := range allE2ESetups() {
		if s.ID == id {
			return s, true
		}
	}
	return e2eSetup{}, false
}

func setupsForRequest(req e2eRunRequest) []e2eSetup {
	all := allE2ESetups()
	if len(req.Setups) > 0 {
		if len(req.Setups) == 1 && req.Setups[0] == "all" {
			return all
		}
		var out []e2eSetup
		seen := map[string]bool{}
		for _, id := range req.Setups {
			if s, ok := setupByID(id); ok && !seen[s.ID] {
				out = append(out, s)
				seen[s.ID] = true
			}
		}
		return out
	}
	groups := req.Groups
	if len(groups) == 0 || (len(groups) == 1 && groups[0] == "all") {
		return all
	}
	want := map[string]bool{}
	for _, g := range groups {
		want[g] = true
	}
	var out []e2eSetup
	for _, s := range all {
		if want[s.Group] {
			out = append(out, s)
		}
	}
	return out
}
