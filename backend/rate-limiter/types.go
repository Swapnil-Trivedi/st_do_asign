package main

import "time"

const (
	AlgoSlidingWindow = "sliding_window"
	AlgoFixedToken    = "fixed_token"
	AlgoFixedWindow   = "fixed_window"

	MatchHeader   = "header"
	MatchPath     = "path"
	MatchPathAttr = "path_attr"
	MatchMethod   = "method"
	MatchRegex    = "regex"

	MatchModeAll = "all"
	MatchModeAny = "any"
)

type AlgorithmConfig struct {
	ID        string
	Name      string
	Algorithm string
	Params    map[string]any
	Enabled   bool
}

type Namespace struct {
	ID           string
	Label        string
	UpstreamHost string
	Path         *string
	Algorithm    string
	ConfigID     string
	MatchMode    string
	Priority     int
	Enabled      bool
}

type RuleMatch struct {
	Kind    string
	Name    string
	Op      string
	Value   string
	Values  []string
	Pattern string
	Attr    string
	Field   string
}

type Rule struct {
	ID          string
	NamespaceID string
	Label       string
	Enabled     bool
	Match       RuleMatch
}

// CompiledNamespace is a namespace ready for request matching.
type CompiledNamespace struct {
	Namespace Namespace
	Rules     []Rule
	Config    AlgorithmConfig
}

// PolicySnapshot is an immutable view of enabled policy used on the request path.
type PolicySnapshot struct {
	LoadedAt   time.Time
	Namespaces []CompiledNamespace
}

type LimitResult struct {
	Allowed   bool
	Remaining int64
	Limit     int64
	ResetMs   int64
}

// QuotaState is a non-consuming view of current Redis quota for a subject.
type QuotaState struct {
	NamespaceID    string    `json:"namespaceId"`
	NamespaceLabel string    `json:"namespace"`
	Algorithm      string    `json:"algorithm"`
	Subject        string    `json:"subject"`
	RedisKey       string    `json:"redisKey"`
	Limit          int64     `json:"limit"`
	Remaining      int64     `json:"remaining"`
	Consumed       int64     `json:"consumed"`
	ResetMs        int64     `json:"resetMs"`
	ResetAt        time.Time `json:"resetAt"`
	ObservedAt     time.Time `json:"observedAt"`
	Params         map[string]any `json:"params,omitempty"`
}
