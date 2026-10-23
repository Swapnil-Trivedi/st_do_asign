package main

import "time"

const (
	e2eGroupFixtures     = "fixtures"
	e2eGroupVegeta       = "vegeta"
	e2eGroupConcurrency  = "concurrency"
	e2eDriverDirect      = "direct"
	e2eDriverVegeta      = "vegeta"
	e2eDriverMultiVegeta = "multi-vegeta"
	e2eDriverHybrid      = "hybrid"
	e2eDriverHotReload   = "hot-reload"
)

type e2eCatalogResponse struct {
	Groups []e2eGroupInfo `json:"groups"`
	Setups []e2eSetupInfo `json:"setups"`
}

type e2eGroupInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type e2eSetupInfo struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Driver      string `json:"driver"`
}

type e2eRunRequest struct {
	Setups []string `json:"setups"`
	Groups []string `json:"groups"`
}

type E2EReport struct {
	ID        string           `json:"id"`
	StartedAt time.Time        `json:"startedAt"`
	EndedAt   time.Time        `json:"endedAt"`
	Duration  string           `json:"duration"`
	OK        bool             `json:"ok"`
	Setups    []E2ESetupResult `json:"setups"`
	Summary   E2ESummary       `json:"summary"`
}

type E2ESummary struct {
	TotalSetups   int            `json:"totalSetups"`
	Passed        int            `json:"passed"`
	Failed        int            `json:"failed"`
	Skipped       int            `json:"skipped"`
	Allowed       int            `json:"allowed"`
	RateLimited   int            `json:"rateLimited"`
	Other         int            `json:"other"`
	ByGroup       map[string]int `json:"byGroupPassed"`
	VegetaJobs    int            `json:"vegetaJobs"`
}

type E2ESetupResult struct {
	SetupID     string             `json:"setupId"`
	Group       string             `json:"group"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Status      string             `json:"status"` // passed|failed|skipped
	Message     string             `json:"message,omitempty"`
	Driver      string             `json:"driver"`
	DurationMs  int64              `json:"durationMs"`
	Traffic     TrafficStats       `json:"traffic"`
	VegetaRuns  []VegetaRunSlice   `json:"vegetaRuns,omitempty"`
	Quota       *QuotaSnapshot     `json:"quota,omitempty"`
	Analytics   ServiceAnalytics   `json:"analytics"`
	Steps       []E2EStep          `json:"steps,omitempty"`
}

type TrafficStats struct {
	Total        int            `json:"total"`
	Allowed      int            `json:"allowed"`
	RateLimited  int            `json:"rateLimited"`
	Other        int            `json:"other"`
	StatusCodes  map[string]int `json:"statusCodes"`
}

type VegetaRunSlice struct {
	RunID     string             `json:"runId"`
	Profile   string             `json:"profile"`
	Status    string             `json:"status"`
	Metrics   *VegetaMetricsView `json:"metrics,omitempty"`
}

type VegetaMetricsView struct {
	Requests     uint64         `json:"requests"`
	SuccessRatio float64        `json:"successRatio"`
	LatencyP95   string         `json:"latencyP95,omitempty"`
	LatencyMean  string         `json:"latencyMean,omitempty"`
	Throughput   float64        `json:"throughput,omitempty"`
	StatusCodes  map[string]int `json:"statusCodes,omitempty"`
}

type QuotaSnapshot struct {
	Namespace string `json:"namespace"`
	Subject   string `json:"subject"`
	Limit     int64  `json:"limit"`
	Remaining int64  `json:"remaining"`
	Consumed  int64  `json:"consumed"`
	ResetMs   int64  `json:"resetMs"`
	Algorithm string `json:"algorithm"`
}

type SampleHeaders struct {
	LastAllow map[string]string `json:"lastAllow,omitempty"`
	LastDeny  map[string]string `json:"lastDeny,omitempty"`
}

type MockLogStats struct {
	Access   int `json:"access"`
	Upstream int `json:"upstream"`
	Warn     int `json:"warn"`
	Info     int `json:"info"`
	Total    int `json:"total"`
}

type ServiceAnalytics struct {
	ProxyInstances       map[string]int `json:"proxyInstances"`
	RateLimitHeaders     SampleHeaders  `json:"rateLimitHeaders"`
	UpstreamForwards     int            `json:"upstreamForwards"`
	UpstreamLatencyMs    []int          `json:"upstreamLatencyMs,omitempty"`
	MockLogDelta         MockLogStats   `json:"mockLogDelta"`
	KeyMatchTrue         int            `json:"keyMatchTrue"`
	ConcurrentVegetaJobs int            `json:"concurrentVegetaJobs,omitempty"`
}

type E2EStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Internal fixture definition.
type e2eSetup struct {
	ID          string
	Group       string
	Name        string
	Description string
	Driver      string

	// Policy (skipped for demo-seed-smoke / reuse).
	ReuseNamespaceLabel string // if set, skip create; use existing
	ConfigName          string
	NamespaceLabel      string
	Algorithm           string
	Params              map[string]any
	APIKey              string
	Path                string
	Priority            int

	// Direct load
	DirectCount      int
	DirectConcurrent bool

	// Vegeta
	VegetaJobs []e2eVegetaJob

	// Hybrid: also run direct alongside vegeta
	HybridDirectCount int

	// Hot-reload special
	HotReload bool

	// Expectations
	MinAllow int
	MaxAllow int // 0 = no max
	MinDeny  int
	RequireBothProxyInstances bool
	RequireDeny               bool
	RequireVegeta429          bool
	MaxCombinedAllow          int // multi-vegeta shared capacity check; 0 = skip
}

type e2eVegetaJob struct {
	Profile string
	Steady  *e2eSteadySpec
	Ramp    *e2eRampSpec
	Burst   *e2eBurstSpec
}

type e2eSteadySpec struct {
	RPS      int
	Duration string
}

type e2eRampSpec struct {
	FromRPS      int
	ToRPS        int
	Steps        int
	StepDuration string
}

type e2eBurstSpec struct {
	BaseRPS       int
	BurstRPS      int
	BurstEvery    string
	BurstDuration string
	Duration      string
}
