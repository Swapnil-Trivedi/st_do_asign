package main

import (
	"fmt"
	"time"
)

// ProfileName is a load shape the Data Plane UI can select.
type ProfileName string

const (
	ProfileSteady ProfileName = "steady"
	ProfileRamp   ProfileName = "ramp"
	ProfileBurst  ProfileName = "burst"
)

// TargetSpec is the HTTP target hit through the rate limiter (or mock upstream in early demos).
type TargetSpec struct {
	BaseURL string            `json:"baseUrl"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

// SteadyConfig — constant RPS for a fixed duration.
// Duration is a Go duration string, e.g. "30s", "2m".
type SteadyConfig struct {
	RPS      int    `json:"rps"`
	Duration string `json:"duration"`
}

// RampConfig — linear RPS increase from FromRPS → ToRPS across Steps.
type RampConfig struct {
	FromRPS      int    `json:"fromRps"`
	ToRPS        int    `json:"toRps"`
	Steps        int    `json:"steps"`
	StepDuration string `json:"stepDuration"`
}

// BurstConfig — base RPS with periodic high-RPS bursts.
type BurstConfig struct {
	BaseRPS       int    `json:"baseRps"`
	BurstRPS      int    `json:"burstRps"`
	BurstEvery    string `json:"burstEvery"`
	BurstDuration string `json:"burstDuration"`
	Duration      string `json:"duration"`
}

// CreateRunRequest is the UI / API payload to start a load run.
type CreateRunRequest struct {
	Profile ProfileName   `json:"profile"`
	Target  TargetSpec    `json:"target"`
	Steady  *SteadyConfig `json:"steady,omitempty"`
	Ramp    *RampConfig   `json:"ramp,omitempty"`
	Burst   *BurstConfig  `json:"burst,omitempty"`
}

// RunMetrics is a snapshot suitable for the Data Plane UI.
type RunMetrics struct {
	Requests    uint64         `json:"requests"`
	Success     float64        `json:"successRatio"`
	LatencyP50  string         `json:"latencyP50"`
	LatencyP95  string         `json:"latencyP95"`
	LatencyP99  string         `json:"latencyP99"`
	LatencyMax  string         `json:"latencyMax"`
	BytesIn     uint64         `json:"bytesIn"`
	BytesOut    uint64         `json:"bytesOut"`
	Throughput  float64        `json:"throughput"`
	StatusCodes map[string]int `json:"statusCodes"`
	Errors      []string       `json:"errors,omitempty"`
}

// RunStatus for UI polling.
type RunStatus string

const (
	StatusQueued    RunStatus = "queued"
	StatusRunning   RunStatus = "running"
	StatusCompleted RunStatus = "completed"
	StatusStopped   RunStatus = "stopped"
	StatusFailed    RunStatus = "failed"
)

// RunView is returned by the control API.
type RunView struct {
	ID        string           `json:"id"`
	Profile   ProfileName      `json:"profile"`
	Status    RunStatus        `json:"status"`
	Target    TargetSpec       `json:"target"`
	Config    CreateRunRequest `json:"config"`
	StartedAt *time.Time       `json:"startedAt,omitempty"`
	EndedAt   *time.Time       `json:"endedAt,omitempty"`
	Error     string           `json:"error,omitempty"`
	Metrics   *RunMetrics      `json:"metrics,omitempty"`
}

// ProfileDescriptor documents knobs for the UI form.
type ProfileDescriptor struct {
	Name        ProfileName    `json:"name"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Defaults    map[string]any `json:"defaults"`
}

func parseDuration(label, raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", label, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be > 0", label)
	}
	return d, nil
}
