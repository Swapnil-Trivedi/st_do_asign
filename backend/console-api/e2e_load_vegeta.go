package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type vegetaRunView struct {
	ID      string `json:"id"`
	Profile string `json:"profile"`
	Status  string `json:"status"`
	Metrics *struct {
		Requests     uint64         `json:"requests"`
		SuccessRatio float64        `json:"successRatio"`
		LatencyP95   string         `json:"latencyP95"`
		LatencyMean  string         `json:"latencyMean"`
		Throughput   float64        `json:"throughput"`
		StatusCodes  map[string]int `json:"statusCodes"`
	} `json:"metrics"`
}

func (c *httpClient) startVegetaJob(job e2eVegetaJob, path, apiKey string) (string, error) {
	body := map[string]any{
		"profile": job.Profile,
		"target": map[string]any{
			"baseUrl": c.u.RateLimiter,
			"method":  "GET",
			"path":    path,
			"headers": map[string]string{"US-API-Key": apiKey},
		},
	}
	switch job.Profile {
	case "steady":
		if job.Steady == nil {
			return "", fmt.Errorf("missing steady config")
		}
		body["steady"] = map[string]any{"rps": job.Steady.RPS, "duration": job.Steady.Duration}
	case "ramp":
		if job.Ramp == nil {
			return "", fmt.Errorf("missing ramp config")
		}
		body["ramp"] = map[string]any{
			"fromRps": job.Ramp.FromRPS, "toRps": job.Ramp.ToRPS,
			"steps": job.Ramp.Steps, "stepDuration": job.Ramp.StepDuration,
		}
	case "burst":
		if job.Burst == nil {
			return "", fmt.Errorf("missing burst config")
		}
		body["burst"] = map[string]any{
			"baseRps": job.Burst.BaseRPS, "burstRps": job.Burst.BurstRPS,
			"burstEvery": job.Burst.BurstEvery, "burstDuration": job.Burst.BurstDuration,
			"duration": job.Burst.Duration,
		}
	default:
		return "", fmt.Errorf("unknown vegeta profile %q", job.Profile)
	}

	code, raw, _, err := c.doJSON(http.MethodPost, c.u.Vegeta+"/api/v1/runs/", nil, body)
	if err != nil {
		return "", err
	}
	if code != http.StatusAccepted && code != http.StatusOK {
		return "", fmt.Errorf("start vegeta %d: %s", code, truncateStr(string(raw), 160))
	}
	var view vegetaRunView
	if err := json.Unmarshal(raw, &view); err != nil {
		return "", err
	}
	if view.ID == "" {
		return "", fmt.Errorf("vegeta run missing id: %s", truncateStr(string(raw), 120))
	}
	return view.ID, nil
}

func (c *httpClient) waitVegeta(id string, timeout time.Duration) (vegetaRunView, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, raw, _, err := c.doJSON(http.MethodGet, c.u.Vegeta+"/api/v1/runs/"+id, nil, nil)
		if err != nil {
			return vegetaRunView{}, err
		}
		if code != 200 {
			return vegetaRunView{}, fmt.Errorf("get run %d: %s", code, truncateStr(string(raw), 100))
		}
		var view vegetaRunView
		if err := json.Unmarshal(raw, &view); err != nil {
			return vegetaRunView{}, err
		}
		switch view.Status {
		case "completed", "stopped", "failed":
			return view, nil
		}
		time.Sleep(c.u.VegetaPoll)
	}
	return vegetaRunView{}, fmt.Errorf("vegeta run %s timed out", id)
}

func (c *httpClient) runVegetaJobs(jobs []e2eVegetaJob, path, apiKey string) (TrafficStats, []VegetaRunSlice, error) {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		id, err := c.startVegetaJob(job, path, apiKey)
		if err != nil {
			return TrafficStats{}, nil, err
		}
		ids = append(ids, id)
	}

	traffic := TrafficStats{StatusCodes: map[string]int{}}
	slices := make([]VegetaRunSlice, 0, len(ids))
	for i, id := range ids {
		view, err := c.waitVegeta(id, 3*time.Minute)
		if err != nil {
			return traffic, slices, err
		}
		sl := VegetaRunSlice{RunID: view.ID, Profile: view.Profile, Status: view.Status}
		if view.Metrics != nil {
			sl.Metrics = &VegetaMetricsView{
				Requests: view.Metrics.Requests, SuccessRatio: view.Metrics.SuccessRatio,
				LatencyP95: view.Metrics.LatencyP95, LatencyMean: view.Metrics.LatencyMean,
				Throughput: view.Metrics.Throughput, StatusCodes: view.Metrics.StatusCodes,
			}
			for code, n := range view.Metrics.StatusCodes {
				traffic.StatusCodes[code] += n
				traffic.Total += n
				switch code {
				case "200", "201", "204":
					traffic.Allowed += n
				case "429":
					traffic.RateLimited += n
				default:
					traffic.Other += n
				}
			}
		}
		slices = append(slices, sl)
		_ = i
	}
	return traffic, slices, nil
}
