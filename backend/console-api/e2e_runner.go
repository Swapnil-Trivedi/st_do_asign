package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type e2eRunner struct {
	client *httpClient
	mu     sync.Mutex
	history []E2EReport
}

func newE2ERunner() *e2eRunner {
	return &e2eRunner{client: newHTTPClient(loadE2EURLs()), history: nil}
}

func (r *e2eRunner) catalog() e2eCatalogResponse {
	var infos []e2eSetupInfo
	for _, s := range allE2ESetups() {
		infos = append(infos, e2eSetupInfo{
			ID: s.ID, Group: s.Group, Name: s.Name, Description: s.Description, Driver: s.Driver,
		})
	}
	return e2eCatalogResponse{Groups: e2eGroups(), Setups: infos}
}

func (r *e2eRunner) listReports() []E2EReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]E2EReport, len(r.history))
	copy(out, r.history)
	return out
}

func (r *e2eRunner) getReport(id string) (E2EReport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rep := range r.history {
		if rep.ID == id {
			return rep, true
		}
	}
	return E2EReport{}, false
}

func (r *e2eRunner) storeReport(rep E2EReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = append([]E2EReport{rep}, r.history...)
	if len(r.history) > 20 {
		r.history = r.history[:20]
	}
}

func (r *e2eRunner) run(req e2eRunRequest) E2EReport {
	started := time.Now().UTC()
	rep := E2EReport{
		ID: uuid.NewString(), StartedAt: started,
		Summary: E2ESummary{ByGroup: map[string]int{}},
	}

	ok, detail := r.client.probeStack()
	if !ok {
		rep.EndedAt = time.Now().UTC()
		rep.Duration = rep.EndedAt.Sub(started).String()
		rep.OK = false
		rep.Setups = []E2ESetupResult{{
			SetupID: "stack", Name: "Stack probe", Status: "failed", Message: detail, Driver: "probe",
		}}
		rep.Summary.Failed = 1
		rep.Summary.TotalSetups = 1
		r.storeReport(rep)
		return rep
	}

	setups := setupsForRequest(req)
	for _, s := range setups {
		rep.Setups = append(rep.Setups, r.runOne(s))
	}

	rep.EndedAt = time.Now().UTC()
	rep.Duration = rep.EndedAt.Sub(started).String()
	for _, s := range rep.Setups {
		rep.Summary.TotalSetups++
		switch s.Status {
		case "passed":
			rep.Summary.Passed++
			rep.Summary.ByGroup[s.Group]++
		case "failed":
			rep.Summary.Failed++
		case "skipped":
			rep.Summary.Skipped++
		}
		rep.Summary.Allowed += s.Traffic.Allowed
		rep.Summary.RateLimited += s.Traffic.RateLimited
		rep.Summary.Other += s.Traffic.Other
		rep.Summary.VegetaJobs += len(s.VegetaRuns)
	}
	rep.OK = rep.Summary.Failed == 0 && rep.Summary.Passed > 0
	r.storeReport(rep)
	return rep
}

func (r *e2eRunner) runOne(s e2eSetup) E2ESetupResult {
	t0 := time.Now()
	out := E2ESetupResult{
		SetupID: s.ID, Group: s.Group, Name: s.Name, Description: s.Description, Driver: s.Driver,
		Traffic: TrafficStats{StatusCodes: map[string]int{}},
		Analytics: ServiceAnalytics{ProxyInstances: map[string]int{}},
	}

	nsLabel, steps, err := r.client.ensurePolicy(s)
	out.Steps = append(out.Steps, steps...)
	if err != nil {
		out.Status = "failed"
		out.Message = "ensure policy: " + err.Error()
		out.DurationMs = time.Since(t0).Milliseconds()
		return out
	}
	out.Steps = append(out.Steps, E2EStep{Name: "wait policy poll", OK: true, Detail: r.client.u.PolicyWait.String()})
	time.Sleep(r.client.u.PolicyWait)

	logsBefore, _ := r.client.mockLogs()

	switch s.Driver {
	case e2eDriverHotReload:
		out = r.runHotReload(s, nsLabel, out)
	case e2eDriverDirect:
		tr, an := r.client.runDirect(s.Path, s.APIKey, s.DirectCount, s.DirectConcurrent)
		out.Traffic = tr
		out.Analytics = an
		out.Steps = append(out.Steps, E2EStep{Name: "direct load", OK: true, Detail: fmt.Sprintf("%d requests", s.DirectCount)})
	case e2eDriverVegeta, e2eDriverMultiVegeta:
		tr, runs, err := r.client.runVegetaJobs(s.VegetaJobs, s.Path, s.APIKey)
		if err != nil {
			out.Status = "failed"
			out.Message = "vegeta: " + err.Error()
			out.DurationMs = time.Since(t0).Milliseconds()
			return out
		}
		out.Traffic = tr
		out.VegetaRuns = runs
		out.Analytics.ConcurrentVegetaJobs = len(runs)
		out.Steps = append(out.Steps, E2EStep{Name: "vegeta load", OK: true, Detail: fmt.Sprintf("%d jobs", len(runs))})
		if s.HybridDirectCount > 0 {
			tr2, an2 := r.client.runDirect(s.Path, s.APIKey, s.HybridDirectCount, true)
			out.Traffic = mergeTraffic(out.Traffic, tr2)
			out.Analytics = mergeAnalytics(out.Analytics, an2)
			out.Steps = append(out.Steps, E2EStep{Name: "direct probes", OK: true, Detail: fmt.Sprintf("%d", s.HybridDirectCount)})
		}
	case e2eDriverHybrid:
		var wg sync.WaitGroup
		var trV TrafficStats
		var runs []VegetaRunSlice
		var vegErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			trV, runs, vegErr = r.client.runVegetaJobs(s.VegetaJobs, s.Path, s.APIKey)
		}()
		// slight delay then direct storm
		time.Sleep(300 * time.Millisecond)
		trD, anD := r.client.runDirect(s.Path, s.APIKey, s.HybridDirectCount, true)
		wg.Wait()
		if vegErr != nil {
			out.Status = "failed"
			out.Message = "vegeta: " + vegErr.Error()
			out.DurationMs = time.Since(t0).Milliseconds()
			return out
		}
		out.Traffic = mergeTraffic(trD, trV)
		out.VegetaRuns = runs
		out.Analytics = anD
		out.Analytics.ConcurrentVegetaJobs = len(runs)
		out.Steps = append(out.Steps,
			E2EStep{Name: "hybrid vegeta", OK: true, Detail: fmt.Sprintf("%d jobs", len(runs))},
			E2EStep{Name: "hybrid direct", OK: true, Detail: fmt.Sprintf("%d", s.HybridDirectCount)},
		)
	default:
		out.Status = "failed"
		out.Message = "unknown driver " + s.Driver
		out.DurationMs = time.Since(t0).Milliseconds()
		return out
	}

	logsAfter, _ := r.client.mockLogs()
	out.Analytics.MockLogDelta = mockLogDelta(logsBefore, logsAfter)

	if q, err := r.client.peekQuota(nsLabel, s.APIKey); err == nil {
		out.Quota = q
		out.Steps = append(out.Steps, E2EStep{Name: "quota peek", OK: true, Detail: fmt.Sprintf("remaining=%d consumed=%d", q.Remaining, q.Consumed)})
	} else {
		out.Steps = append(out.Steps, E2EStep{Name: "quota peek", OK: false, Detail: err.Error()})
	}

	out.Status, out.Message = scoreSetup(s, out)
	out.DurationMs = time.Since(t0).Milliseconds()
	return out
}

func (r *e2eRunner) runHotReload(s e2eSetup, nsLabel string, out E2ESetupResult) E2ESetupResult {
	tr1, an1 := r.client.runDirect(s.Path, s.APIKey, 2, false)
	out.Traffic = tr1
	out.Analytics = an1
	if tr1.Allowed < 1 {
		out.Status = "failed"
		out.Message = "hot-reload: expected initial allow"
		out.Steps = append(out.Steps, E2EStep{Name: "initial allow", OK: false, Detail: fmt.Sprintf("allowed=%d", tr1.Allowed)})
		return out
	}
	out.Steps = append(out.Steps, E2EStep{Name: "initial allow", OK: true})

	if err := r.client.setNamespaceEnabled(nsLabel, false); err != nil {
		out.Status = "failed"
		out.Message = err.Error()
		return out
	}
	out.Steps = append(out.Steps, E2EStep{Name: "disable namespace", OK: true})
	time.Sleep(r.client.u.PolicyWait)

	// After disable, matching this namespace must not forward to mock.
	tr2, an2 := r.client.runDirect(s.Path, s.APIKey, 2, false)
	out.Traffic = mergeTraffic(out.Traffic, tr2)
	noForward := an2.UpstreamForwards == 0
	out.Steps = append(out.Steps, E2EStep{
		Name: "after disable", OK: noForward,
		Detail: fmt.Sprintf("forwards=%d codes=%v", an2.UpstreamForwards, tr2.StatusCodes),
	})

	_ = r.client.setNamespaceEnabled(nsLabel, true)
	time.Sleep(r.client.u.PolicyWait)
	tr3, an3 := r.client.runDirect(s.Path, s.APIKey, 2, false)
	out.Traffic = mergeTraffic(out.Traffic, tr3)
	out.Analytics = mergeAnalytics(out.Analytics, an3)
	restored := tr3.Allowed >= 1 && an3.UpstreamForwards >= 1
	out.Steps = append(out.Steps, E2EStep{
		Name: "re-enable allow", OK: restored,
		Detail: fmt.Sprintf("allowed=%d forwards=%d", tr3.Allowed, an3.UpstreamForwards),
	})

	if noForward && restored {
		out.Status = "passed"
		out.Message = "hot reload disable/enable observed"
	} else {
		out.Status = "failed"
		out.Message = fmt.Sprintf("hot-reload failed (noForward=%v restored=%v)", noForward, restored)
	}
	return out
}

func scoreSetup(s e2eSetup, out E2ESetupResult) (status, message string) {
	if s.HotReload {
		return out.Status, out.Message
	}
	var problems []string
	if s.MinAllow > 0 && out.Traffic.Allowed < s.MinAllow {
		problems = append(problems, fmt.Sprintf("allowed %d < min %d", out.Traffic.Allowed, s.MinAllow))
	}
	if s.MaxAllow > 0 && out.Traffic.Allowed > s.MaxAllow {
		problems = append(problems, fmt.Sprintf("allowed %d > max %d", out.Traffic.Allowed, s.MaxAllow))
	}
	if s.MinDeny > 0 && out.Traffic.RateLimited < s.MinDeny {
		problems = append(problems, fmt.Sprintf("429s %d < min %d", out.Traffic.RateLimited, s.MinDeny))
	}
	if s.RequireDeny && out.Traffic.RateLimited < 1 {
		problems = append(problems, "expected some 429s")
	}
	if s.RequireVegeta429 && out.Traffic.RateLimited < 1 {
		problems = append(problems, "expected vegeta 429s")
	}
	if s.MaxCombinedAllow > 0 && out.Traffic.Allowed > s.MaxCombinedAllow {
		problems = append(problems, fmt.Sprintf("combined allows %d > capacity band %d (possible double-quota)", out.Traffic.Allowed, s.MaxCombinedAllow))
	}
	if s.RequireBothProxyInstances {
		_, a := out.Analytics.ProxyInstances["a"]
		_, b := out.Analytics.ProxyInstances["b"]
		if !a || !b {
			problems = append(problems, fmt.Sprintf("need proxy instances a and b, got %v", out.Analytics.ProxyInstances))
		}
	}
	// vegeta jobs should complete
	for _, vr := range out.VegetaRuns {
		if vr.Status == "failed" {
			problems = append(problems, "vegeta run failed: "+vr.RunID)
		}
	}
	if len(problems) > 0 {
		return "failed", strings.Join(problems, "; ")
	}
	return "passed", fmt.Sprintf("allowed=%d rateLimited=%d", out.Traffic.Allowed, out.Traffic.RateLimited)
}
