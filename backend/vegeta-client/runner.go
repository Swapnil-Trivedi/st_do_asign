package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
)

type phase struct {
	rps      int
	duration time.Duration
}

func buildPhases(req CreateRunRequest) ([]phase, error) {
	switch req.Profile {
	case ProfileSteady:
		cfg := req.Steady
		if cfg == nil {
			return nil, fmt.Errorf("steady config required")
		}
		if cfg.RPS <= 0 {
			return nil, fmt.Errorf("steady.rps must be > 0")
		}
		dur, err := parseDuration("steady.duration", cfg.Duration, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return []phase{{rps: cfg.RPS, duration: dur}}, nil

	case ProfileRamp:
		cfg := req.Ramp
		if cfg == nil {
			return nil, fmt.Errorf("ramp config required")
		}
		if cfg.FromRPS < 0 || cfg.ToRPS <= 0 || cfg.ToRPS < cfg.FromRPS {
			return nil, fmt.Errorf("ramp.fromRps/toRps invalid")
		}
		steps := cfg.Steps
		if steps <= 0 {
			steps = 5
		}
		stepDur, err := parseDuration("ramp.stepDuration", cfg.StepDuration, 5*time.Second)
		if err != nil {
			return nil, err
		}
		phases := make([]phase, 0, steps)
		if steps == 1 {
			return []phase{{rps: cfg.ToRPS, duration: stepDur}}, nil
		}
		for i := 0; i < steps; i++ {
			rps := cfg.FromRPS + int(float64(cfg.ToRPS-cfg.FromRPS)*float64(i)/float64(steps-1))
			if rps < 1 {
				rps = 1
			}
			phases = append(phases, phase{rps: rps, duration: stepDur})
		}
		return phases, nil

	case ProfileBurst:
		cfg := req.Burst
		if cfg == nil {
			return nil, fmt.Errorf("burst config required")
		}
		if cfg.BaseRPS <= 0 || cfg.BurstRPS <= 0 {
			return nil, fmt.Errorf("burst.baseRps and burst.burstRps must be > 0")
		}
		total, err := parseDuration("burst.duration", cfg.Duration, 60*time.Second)
		if err != nil {
			return nil, err
		}
		every, err := parseDuration("burst.burstEvery", cfg.BurstEvery, 10*time.Second)
		if err != nil {
			return nil, err
		}
		burstFor, err := parseDuration("burst.burstDuration", cfg.BurstDuration, 2*time.Second)
		if err != nil {
			return nil, err
		}
		if burstFor >= every {
			return nil, fmt.Errorf("burst.burstDuration must be < burst.burstEvery")
		}

		var phases []phase
		elapsed := time.Duration(0)
		for elapsed < total {
			baseFor := every - burstFor
			if elapsed+baseFor > total {
				baseFor = total - elapsed
			}
			if baseFor > 0 {
				phases = append(phases, phase{rps: cfg.BaseRPS, duration: baseFor})
				elapsed += baseFor
			}
			if elapsed >= total {
				break
			}
			b := burstFor
			if elapsed+b > total {
				b = total - elapsed
			}
			phases = append(phases, phase{rps: cfg.BurstRPS, duration: b})
			elapsed += b
		}
		return phases, nil

	default:
		return nil, fmt.Errorf("unknown profile %q (steady|ramp|burst)", req.Profile)
	}
}

func makeTargeter(t TargetSpec) (vegeta.Targeter, error) {
	if t.BaseURL == "" {
		return nil, fmt.Errorf("target.baseUrl is required")
	}
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := strings.TrimRight(t.BaseURL, "/") + path

	hdr := http.Header{}
	for k, v := range t.Headers {
		hdr.Set(k, v)
	}

	var body []byte
	if t.Body != "" {
		body = []byte(t.Body)
	}

	return vegeta.NewStaticTargeter(vegeta.Target{
		Method: method,
		URL:    url,
		Body:   body,
		Header: hdr,
	}), nil
}

func metricsFrom(m *vegeta.Metrics) *RunMetrics {
	codes := map[string]int{}
	for code, n := range m.StatusCodes {
		codes[code] = int(n)
	}
	errs := append([]string(nil), m.Errors...)
	if len(errs) > 8 {
		errs = errs[:8]
	}
	return &RunMetrics{
		Requests:    m.Requests,
		Success:     m.Success,
		LatencyMin:  m.Latencies.Min.String(),
		LatencyMean: m.Latencies.Mean.String(),
		LatencyP50:  m.Latencies.P50.String(),
		LatencyP95:  m.Latencies.P95.String(),
		LatencyP99:  m.Latencies.P99.String(),
		LatencyMax:  m.Latencies.Max.String(),
		BytesIn:     m.BytesIn.Total,
		BytesOut:    m.BytesOut.Total,
		Throughput:  m.Throughput,
		StatusCodes: codes,
		Errors:      errs,
	}
}

type runRecord struct {
	mu        sync.RWMutex
	view      RunView
	cancel    context.CancelFunc
	metrics   vegeta.Metrics
	attacker  *vegeta.Attacker
}

func (r *runRecord) snapshot() RunView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := r.view
	out.Metrics = metricsFrom(&r.metrics)
	return out
}

func (r *runRecord) setStatus(s RunStatus, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.view.Status = s
	if errMsg != "" {
		r.view.Error = errMsg
	}
	if s == StatusRunning && r.view.StartedAt == nil {
		now := time.Now().UTC()
		r.view.StartedAt = &now
	}
	if s == StatusCompleted || s == StatusStopped || s == StatusFailed {
		now := time.Now().UTC()
		r.view.EndedAt = &now
	}
}

func executeRun(ctx context.Context, rec *runRecord, req CreateRunRequest) {
	phases, err := buildPhases(req)
	if err != nil {
		rec.setStatus(StatusFailed, err.Error())
		return
	}
	targeter, err := makeTargeter(req.Target)
	if err != nil {
		rec.setStatus(StatusFailed, err.Error())
		return
	}

	rec.setStatus(StatusRunning, "")
	attacker := vegeta.NewAttacker(vegeta.Timeout(10 * time.Second))
	rec.mu.Lock()
	rec.attacker = attacker
	rec.mu.Unlock()

	finalize := func(status RunStatus, errMsg string) {
		rec.mu.Lock()
		rec.metrics.Close()
		rec.mu.Unlock()
		rec.setStatus(status, errMsg)
	}

	for i, p := range phases {
		select {
		case <-ctx.Done():
			finalize(StatusStopped, "stopped by client")
			return
		default:
		}

		rate := vegeta.Rate{Freq: p.rps, Per: time.Second}
		name := fmt.Sprintf("%s-phase-%d@%drps", req.Profile, i+1, p.rps)
		for res := range attacker.Attack(targeter, rate, p.duration, name) {
			select {
			case <-ctx.Done():
				attacker.Stop()
				rec.mu.Lock()
				rec.metrics.Add(res)
				rec.mu.Unlock()
				finalize(StatusStopped, "stopped by client")
				return
			default:
			}
			rec.mu.Lock()
			rec.metrics.Add(res)
			rec.mu.Unlock()
		}
	}

	finalize(StatusCompleted, "")
}
