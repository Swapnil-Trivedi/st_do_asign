package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixedTokenRefillAfterTime(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"sustainedRps": int64(1), "burst": int64(1)}
	t0 := int64(1_700_000_000_000)

	restore := setUnixMilli(t0)
	defer restore()

	r1, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "refill", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)

	r2, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "refill", params)
	require.NoError(t, err)
	require.False(t, r2.Allowed)
	require.Greater(t, r2.ResetMs, int64(0))

	// 1s later at 1 rps → one token refilled.
	restore2 := setUnixMilli(t0 + 1000)
	defer restore2()
	r3, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "refill", params)
	require.NoError(t, err)
	require.True(t, r3.Allowed, "expected refill after 1s")
}

func TestFixedWindowRollsOver(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(1), "windowSeconds": int64(1)}
	// Align to start of a 1s window.
	t0 := int64(1_700_000_000_000)
	restore := setUnixMilli(t0)
	defer restore()

	r1, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "roll", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)
	require.Greater(t, r1.ResetMs, int64(0))

	r2, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "roll", params)
	require.NoError(t, err)
	require.False(t, r2.Allowed)

	restore2 := setUnixMilli(t0 + 1000)
	defer restore2()
	r3, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "roll", params)
	require.NoError(t, err)
	require.True(t, r3.Allowed, "new window should allow again")
}

func TestSlidingWindowExpiresOldMembers(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(1), "windowSeconds": int64(1)}
	t0 := int64(1_700_000_000_000)
	restore := setUnixMilli(t0)
	defer restore()

	r1, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "slide-exp", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)

	r2, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "slide-exp", params)
	require.NoError(t, err)
	require.False(t, r2.Allowed)
	require.Greater(t, r2.ResetMs, int64(0))

	restore2 := setUnixMilli(t0 + 1001)
	defer restore2()
	r3, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "slide-exp", params)
	require.NoError(t, err)
	require.True(t, r3.Allowed, "member outside window should be trimmed")
}

func TestSubjectIsolation(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"sustainedRps": int64(1), "burst": int64(1)}

	a1, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj-a", params)
	require.NoError(t, err)
	require.True(t, a1.Allowed)
	a2, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj-a", params)
	require.NoError(t, err)
	require.False(t, a2.Allowed)

	b1, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj-b", params)
	require.NoError(t, err)
	require.True(t, b1.Allowed, "different subject must have its own bucket")
}

func TestNamespaceIsolation(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(1), "windowSeconds": int64(60)}

	r1, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns-1", "same-subj", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)
	r2, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns-1", "same-subj", params)
	require.NoError(t, err)
	require.False(t, r2.Allowed)

	r3, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns-2", "same-subj", params)
	require.NoError(t, err)
	require.True(t, r3.Allowed, "different namespaceId must isolate quota")
}

func TestPeekAllAlgorithms(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()

	t.Run("fixed_token", func(t *testing.T) {
		params := map[string]any{"sustainedRps": int64(1), "burst": int64(5)}
		_, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "peek-ft", params)
		require.NoError(t, err)
		st, err := lim.Peek(ctx, AlgoFixedToken, "ns", "peek-ft", params)
		require.NoError(t, err)
		require.Equal(t, int64(5), st.Limit)
		require.Equal(t, int64(4), st.Remaining)
		require.Equal(t, int64(1), st.Consumed)
		again, err := lim.Peek(ctx, AlgoFixedToken, "ns", "peek-ft", params)
		require.NoError(t, err)
		require.Equal(t, st.Remaining, again.Remaining)
	})

	t.Run("fixed_window", func(t *testing.T) {
		params := map[string]any{"limit": int64(3), "windowSeconds": int64(60)}
		_, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "peek-fw", params)
		require.NoError(t, err)
		_, err = lim.Evaluate(ctx, AlgoFixedWindow, "ns", "peek-fw", params)
		require.NoError(t, err)
		st, err := lim.Peek(ctx, AlgoFixedWindow, "ns", "peek-fw", params)
		require.NoError(t, err)
		require.Equal(t, int64(3), st.Limit)
		require.Equal(t, int64(1), st.Remaining)
		require.Equal(t, int64(2), st.Consumed)
		require.Greater(t, st.ResetMs, int64(0))
	})

	t.Run("sliding_window", func(t *testing.T) {
		params := map[string]any{"limit": int64(2), "windowSeconds": int64(60)}
		_, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "peek-sw", params)
		require.NoError(t, err)
		st, err := lim.Peek(ctx, AlgoSlidingWindow, "ns", "peek-sw", params)
		require.NoError(t, err)
		require.Equal(t, int64(2), st.Limit)
		require.Equal(t, int64(1), st.Remaining)
		require.Equal(t, int64(1), st.Consumed)
	})
}

func TestResetClearsQuota(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"sustainedRps": int64(1), "burst": int64(1)}

	r1, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "reset-me", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)
	r2, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "reset-me", params)
	require.NoError(t, err)
	require.False(t, r2.Allowed)

	n, err := lim.Reset(ctx, AlgoFixedToken, "ns", "reset-me")
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))

	r3, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "reset-me", params)
	require.NoError(t, err)
	require.True(t, r3.Allowed, "after Reset bucket should be full again")
}

func TestResetFixedWindowPattern(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(1), "windowSeconds": int64(60)}

	_, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw-reset", params)
	require.NoError(t, err)
	denied, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw-reset", params)
	require.NoError(t, err)
	require.False(t, denied.Allowed)

	n, err := lim.Reset(ctx, AlgoFixedWindow, "ns", "fw-reset")
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))

	again, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw-reset", params)
	require.NoError(t, err)
	require.True(t, again.Allowed)
}

func TestConcurrentEvaluatesRespectLimit(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(20), "windowSeconds": int64(60)}
	const workers = 50

	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			r, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "race", params)
			if err == nil && r.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(20), allowed.Load(), "Lua must serialize to exact limit under concurrency")
}

func TestInvalidParamsAndUnknownAlgo(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()

	_, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "x", map[string]any{"burst": int64(1)})
	require.Error(t, err)

	_, err = lim.Evaluate(ctx, AlgoFixedWindow, "ns", "x", map[string]any{"limit": int64(0), "windowSeconds": int64(1)})
	require.Error(t, err)

	_, err = lim.Evaluate(ctx, "nope", "ns", "x", map[string]any{"limit": int64(1), "windowSeconds": int64(1)})
	require.Error(t, err)
}
