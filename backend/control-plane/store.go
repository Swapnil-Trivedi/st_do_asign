package main

import "context"

// Store is the control-plane persistence contract (Mongo in prod, memory in tests).
type Store interface {
	ListAlgorithms(ctx context.Context) ([]Algorithm, error)

	ListConfigs(ctx context.Context) ([]AlgorithmConfig, error)
	GetConfig(ctx context.Context, id string) (AlgorithmConfig, error)
	CreateConfig(ctx context.Context, c AlgorithmConfig) (AlgorithmConfig, error)
	UpdateConfig(ctx context.Context, c AlgorithmConfig) (AlgorithmConfig, error)
	DeleteConfig(ctx context.Context, id string) error

	ListNamespaces(ctx context.Context) ([]Namespace, error)
	GetNamespace(ctx context.Context, id string) (Namespace, error)
	CreateNamespace(ctx context.Context, n Namespace) (Namespace, error)
	UpdateNamespace(ctx context.Context, n Namespace) (Namespace, error)
	DeleteNamespace(ctx context.Context, id string) error

	ListRules(ctx context.Context, namespaceID string) ([]Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	CreateRule(ctx context.Context, r Rule) (Rule, error)
	UpdateRule(ctx context.Context, r Rule) (Rule, error)
	DeleteRule(ctx context.Context, id string) error

	// CountNamespacesByConfig returns how many namespaces reference a config.
	CountNamespacesByConfig(ctx context.Context, configID string) (int, error)
}
