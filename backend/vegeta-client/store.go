package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type runStore struct {
	mu   sync.RWMutex
	runs map[string]*runRecord
}

func newRunStore() *runStore {
	return &runStore{runs: map[string]*runRecord{}}
}

func (s *runStore) start(req CreateRunRequest, defaultBaseURL string) (RunView, error) {
	if req.Target.BaseURL == "" {
		req.Target.BaseURL = defaultBaseURL
	}
	if _, err := buildPhases(req); err != nil {
		return RunView{}, err
	}
	if _, err := makeTargeter(req.Target); err != nil {
		return RunView{}, err
	}

	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	rec := &runRecord{
		view: RunView{
			ID:      id,
			Profile: req.Profile,
			Status:  StatusQueued,
			Target:  req.Target,
			Config:  req,
		},
		cancel: cancel,
	}

	s.mu.Lock()
	s.runs[id] = rec
	s.mu.Unlock()

	go executeRun(ctx, rec, req)
	return rec.snapshot(), nil
}

func (s *runStore) get(id string) (RunView, error) {
	s.mu.RLock()
	rec, ok := s.runs[id]
	s.mu.RUnlock()
	if !ok {
		return RunView{}, fmt.Errorf("run not found")
	}
	return rec.snapshot(), nil
}

func (s *runStore) list() []RunView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RunView, 0, len(s.runs))
	for _, rec := range s.runs {
		out = append(out, rec.snapshot())
	}
	return out
}

func (s *runStore) stop(id string) (RunView, error) {
	s.mu.RLock()
	rec, ok := s.runs[id]
	s.mu.RUnlock()
	if !ok {
		return RunView{}, fmt.Errorf("run not found")
	}
	rec.cancel()
	rec.mu.Lock()
	if rec.attacker != nil {
		rec.attacker.Stop()
	}
	rec.mu.Unlock()
	return rec.snapshot(), nil
}
