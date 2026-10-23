package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memoryStore struct {
	mu          sync.RWMutex
	algorithms  []Algorithm
	configs     map[string]AlgorithmConfig
	namespaces  map[string]Namespace
	rules       map[string]Rule
	configNames map[string]string // name → id
	nsLabels    map[string]string // label → id
}

func newMemoryStore() *memoryStore {
	s := &memoryStore{
		configs:     map[string]AlgorithmConfig{},
		namespaces:  map[string]Namespace{},
		rules:       map[string]Rule{},
		configNames: map[string]string{},
		nsLabels:    map[string]string{},
		algorithms:  defaultAlgorithms(),
	}
	return s
}

func defaultAlgorithms() []Algorithm {
	return []Algorithm{
		{
			ID: AlgoSlidingWindow, Label: "Sliding window", Enabled: true,
			Description: "Rolling window of request timestamps.",
			ParamsSchema: map[string]any{
				"limit":         map[string]any{"type": "int", "min": 1, "required": true},
				"windowSeconds": map[string]any{"type": "int", "min": 1, "required": true},
			},
		},
		{
			ID: AlgoFixedToken, Label: "Fixed token (token bucket)", Enabled: true,
			Description: "Refill at sustained RPS; burst capacity for spikes.",
			ParamsSchema: map[string]any{
				"sustainedRps": map[string]any{"type": "int", "min": 1, "required": true},
				"burst":        map[string]any{"type": "int", "min": 1, "required": true},
			},
		},
		{
			ID: AlgoFixedWindow, Label: "Fixed window", Enabled: true,
			Description: "Hard count per discrete time window.",
			ParamsSchema: map[string]any{
				"limit":         map[string]any{"type": "int", "min": 1, "required": true},
				"windowSeconds": map[string]any{"type": "int", "min": 1, "required": true},
			},
		},
	}
}

func (s *memoryStore) ListAlgorithms(_ context.Context) ([]Algorithm, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Algorithm, len(s.algorithms))
	copy(out, s.algorithms)
	return out, nil
}

func (s *memoryStore) ListConfigs(_ context.Context) ([]AlgorithmConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AlgorithmConfig, 0, len(s.configs))
	for _, c := range s.configs {
		out = append(out, c)
	}
	return out, nil
}

func (s *memoryStore) GetConfig(_ context.Context, id string) (AlgorithmConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.configs[id]
	if !ok {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	return c, nil
}

func (s *memoryStore) CreateConfig(_ context.Context, c AlgorithmConfig) (AlgorithmConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.configNames[c.Name]; exists {
		return AlgorithmConfig{}, fmt.Errorf("config name already exists")
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	s.configs[c.ID] = c
	s.configNames[c.Name] = c.ID
	return c, nil
}

func (s *memoryStore) UpdateConfig(_ context.Context, c AlgorithmConfig) (AlgorithmConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.configs[c.ID]
	if !ok {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	if old.Name != c.Name {
		if other, exists := s.configNames[c.Name]; exists && other != c.ID {
			return AlgorithmConfig{}, fmt.Errorf("config name already exists")
		}
		delete(s.configNames, old.Name)
		s.configNames[c.Name] = c.ID
	}
	c.CreatedAt = old.CreatedAt
	c.UpdatedAt = time.Now().UTC()
	s.configs[c.ID] = c
	return c, nil
}

func (s *memoryStore) DeleteConfig(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.configs[id]
	if !ok {
		return fmt.Errorf("config not found")
	}
	delete(s.configs, id)
	delete(s.configNames, c.Name)
	return nil
}

func (s *memoryStore) ListNamespaces(_ context.Context) ([]Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Namespace, 0, len(s.namespaces))
	for _, n := range s.namespaces {
		out = append(out, n)
	}
	return out, nil
}

func (s *memoryStore) GetNamespace(_ context.Context, id string) (Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.namespaces[id]
	if !ok {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	return n, nil
}

func (s *memoryStore) CreateNamespace(_ context.Context, n Namespace) (Namespace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.nsLabels[n.Label]; exists {
		return Namespace{}, fmt.Errorf("namespace label already exists")
	}
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	n.CreatedAt = now
	n.UpdatedAt = now
	s.namespaces[n.ID] = n
	s.nsLabels[n.Label] = n.ID
	return n, nil
}

func (s *memoryStore) UpdateNamespace(_ context.Context, n Namespace) (Namespace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.namespaces[n.ID]
	if !ok {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	if old.Label != n.Label {
		if other, exists := s.nsLabels[n.Label]; exists && other != n.ID {
			return Namespace{}, fmt.Errorf("namespace label already exists")
		}
		delete(s.nsLabels, old.Label)
		s.nsLabels[n.Label] = n.ID
	}
	n.CreatedAt = old.CreatedAt
	n.UpdatedAt = time.Now().UTC()
	s.namespaces[n.ID] = n
	return n, nil
}

func (s *memoryStore) DeleteNamespace(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.namespaces[id]
	if !ok {
		return fmt.Errorf("namespace not found")
	}
	delete(s.namespaces, id)
	delete(s.nsLabels, n.Label)
	for rid, r := range s.rules {
		if r.NamespaceID == id {
			delete(s.rules, rid)
		}
	}
	return nil
}

func (s *memoryStore) ListRules(_ context.Context, namespaceID string) ([]Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, 0)
	for _, r := range s.rules {
		if r.NamespaceID == namespaceID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *memoryStore) GetRule(_ context.Context, id string) (Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[id]
	if !ok {
		return Rule{}, fmt.Errorf("rule not found")
	}
	return r, nil
}

func (s *memoryStore) CreateRule(_ context.Context, r Rule) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.namespaces[r.NamespaceID]; !ok {
		return Rule{}, fmt.Errorf("namespace not found")
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	r.CreatedAt = now
	r.UpdatedAt = now
	s.rules[r.ID] = r
	return r, nil
}

func (s *memoryStore) UpdateRule(_ context.Context, r Rule) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.rules[r.ID]
	if !ok {
		return Rule{}, fmt.Errorf("rule not found")
	}
	r.NamespaceID = old.NamespaceID
	r.CreatedAt = old.CreatedAt
	r.UpdatedAt = time.Now().UTC()
	s.rules[r.ID] = r
	return r, nil
}

func (s *memoryStore) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("rule not found")
	}
	delete(s.rules, id)
	return nil
}

func (s *memoryStore) CountNamespacesByConfig(_ context.Context, configID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, ns := range s.namespaces {
		if ns.ConfigID == configID {
			n++
		}
	}
	return n, nil
}
