package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubLoader struct {
	n atomic.Int64
}

func (s *stubLoader) LoadSnapshot(context.Context) (*PolicySnapshot, error) {
	i := s.n.Add(1)
	return &PolicySnapshot{
		LoadedAt: time.Now().UTC(),
		Namespaces: []CompiledNamespace{{
			Namespace: Namespace{ID: "x", Label: "n", Priority: int(i), MatchMode: MatchModeAll},
		}},
	}, nil
}

func TestPolicyCacheAtomicSwap(t *testing.T) {
	loader := &stubLoader{}
	cache := newPolicyCache(loader, time.Hour)
	require.NoError(t, cache.LoadOnce(context.Background()))

	first := cache.Snapshot()
	require.NotNil(t, first)
	require.Equal(t, 1, first.Namespaces[0].Namespace.Priority)

	// Concurrent readers during swap must always see a complete snapshot.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s := cache.Snapshot()
			require.NotNil(t, s)
			require.Len(t, s.Namespaces, 1)
		}
	}()

	require.NoError(t, cache.LoadOnce(context.Background()))
	second := cache.Snapshot()
	require.Equal(t, 2, second.Namespaces[0].Namespace.Priority)
	<-done
}
