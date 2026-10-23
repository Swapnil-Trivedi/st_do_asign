package main

import (
	"time"
)

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

type Algorithm struct {
	ID           string         `json:"id" bson:"_id"`
	Label        string         `json:"label" bson:"label"`
	Description  string         `json:"description,omitempty" bson:"description,omitempty"`
	ParamsSchema map[string]any `json:"paramsSchema" bson:"paramsSchema"`
	Enabled      bool           `json:"enabled" bson:"enabled"`
}

type AlgorithmConfig struct {
	ID        string         `json:"id" bson:"_id"`
	Name      string         `json:"name" bson:"name"`
	Algorithm string         `json:"algorithm" bson:"algorithm"`
	Params    map[string]any `json:"params" bson:"params"`
	Enabled   bool           `json:"enabled" bson:"enabled"`
	CreatedAt time.Time      `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt" bson:"updatedAt"`
}

type Namespace struct {
	ID           string    `json:"id" bson:"_id"`
	Label        string    `json:"label" bson:"label"`
	UpstreamHost string    `json:"upstreamHost" bson:"upstreamHost"`
	Path         *string   `json:"path" bson:"path"`
	Algorithm    string    `json:"algorithm" bson:"algorithm"`
	ConfigID     string    `json:"configId" bson:"configId"`
	MatchMode    string    `json:"matchMode" bson:"matchMode"`
	Priority     int       `json:"priority" bson:"priority"`
	Enabled      bool      `json:"enabled" bson:"enabled"`
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt" bson:"updatedAt"`
}

type RuleMatch struct {
	Kind    string   `json:"kind" bson:"kind"`
	Name    string   `json:"name,omitempty" bson:"name,omitempty"`
	Op      string   `json:"op,omitempty" bson:"op,omitempty"`
	Value   string   `json:"value,omitempty" bson:"value,omitempty"`
	Values  []string `json:"values,omitempty" bson:"values,omitempty"`
	Pattern string   `json:"pattern,omitempty" bson:"pattern,omitempty"`
	Attr    string   `json:"attr,omitempty" bson:"attr,omitempty"`
	Field   string   `json:"field,omitempty" bson:"field,omitempty"`
}

type Rule struct {
	ID          string    `json:"id" bson:"_id"`
	NamespaceID string    `json:"namespaceId" bson:"namespaceId"`
	Label       string    `json:"label,omitempty" bson:"label,omitempty"`
	Enabled     bool      `json:"enabled" bson:"enabled"`
	Match       RuleMatch `json:"match" bson:"match"`
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" bson:"updatedAt"`
}

type CreateConfigRequest struct {
	Name      string         `json:"name"`
	Algorithm string         `json:"algorithm"`
	Params    map[string]any `json:"params"`
	Enabled   *bool          `json:"enabled,omitempty"`
}

type UpdateConfigRequest struct {
	Name      *string         `json:"name,omitempty"`
	Algorithm *string         `json:"algorithm,omitempty"`
	Params    *map[string]any `json:"params,omitempty"`
	Enabled   *bool           `json:"enabled,omitempty"`
}

type CreateNamespaceRequest struct {
	Label        string  `json:"label"`
	UpstreamHost string  `json:"upstreamHost"`
	Path         *string `json:"path"`
	ConfigID     string  `json:"configId"`
	MatchMode    string  `json:"matchMode"`
	Priority     *int    `json:"priority,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

type UpdateNamespaceRequest struct {
	Label        *string `json:"label,omitempty"`
	UpstreamHost *string `json:"upstreamHost,omitempty"`
	Path         *string `json:"path"`
	ConfigID     *string `json:"configId,omitempty"`
	MatchMode    *string `json:"matchMode,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

type CreateRuleRequest struct {
	Label   string    `json:"label"`
	Enabled *bool     `json:"enabled,omitempty"`
	Match   RuleMatch `json:"match"`
}

type UpdateRuleRequest struct {
	Label   *string    `json:"label,omitempty"`
	Enabled *bool      `json:"enabled,omitempty"`
	Match   *RuleMatch `json:"match,omitempty"`
}

type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}
