package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

type policyLoader interface {
	LoadSnapshot(ctx context.Context) (*PolicySnapshot, error)
}

// PolicyCache holds an atomic snapshot refreshed by polling Mongo.
type PolicyCache struct {
	loader   policyLoader
	interval time.Duration
	snap     atomic.Pointer[PolicySnapshot]
}

func newPolicyCache(loader policyLoader, interval time.Duration) *PolicyCache {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &PolicyCache{loader: loader, interval: interval}
}

func (c *PolicyCache) Snapshot() *PolicySnapshot {
	return c.snap.Load()
}

// LoadOnce fetches and stores a snapshot. Required before serving traffic.
func (c *PolicyCache) LoadOnce(ctx context.Context) error {
	s, err := c.loader.LoadSnapshot(ctx)
	if err != nil {
		return err
	}
	c.snap.Store(s)
	log.Printf("policy cache loaded: %d namespaces (at %s)", len(s.Namespaces), s.LoadedAt.Format(time.RFC3339))
	return nil
}

// Start polls until ctx is cancelled. Failed reloads keep the last good snapshot.
func (c *PolicyCache) Start(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s, err := c.loader.LoadSnapshot(ctx)
			if err != nil {
				log.Printf("policy cache reload failed (keeping last snapshot): %v", err)
				continue
			}
			c.snap.Store(s)
		}
	}
}
