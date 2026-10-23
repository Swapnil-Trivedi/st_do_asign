package main

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func setupLimiter(t *testing.T) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newLimiter(rdb), mr
}

func TestFixedTokenAllowThenDeny(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	// Low refill so back-to-back calls cannot earn a new token.
	params := map[string]any{"sustainedRps": int64(1), "burst": int64(2)}

	r1, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)

	r2, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj", params)
	require.NoError(t, err)
	require.True(t, r2.Allowed)

	r3, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "subj", params)
	require.NoError(t, err)
	require.False(t, r3.Allowed)
	require.Equal(t, int64(2), r3.Limit)
}

func TestFixedWindow(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(2), "windowSeconds": int64(60)}

	r1, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)

	r2, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw", params)
	require.NoError(t, err)
	require.True(t, r2.Allowed)

	r3, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "fw", params)
	require.NoError(t, err)
	require.False(t, r3.Allowed)
	require.Equal(t, int64(0), r3.Remaining)
}

func TestSlidingWindow(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"limit": int64(2), "windowSeconds": int64(60)}

	r1, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "sw", params)
	require.NoError(t, err)
	require.True(t, r1.Allowed)

	r2, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "sw", params)
	require.NoError(t, err)
	require.True(t, r2.Allowed)

	r3, err := lim.Evaluate(ctx, AlgoSlidingWindow, "ns", "sw", params)
	require.NoError(t, err)
	require.False(t, r3.Allowed)
}

func TestFloatParamsFromBSON(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	// Mongo often decodes numbers as float64.
	params := map[string]any{"limit": float64(1), "windowSeconds": float64(10)}
	r, err := lim.Evaluate(ctx, AlgoFixedWindow, "ns", "bson", params)
	require.NoError(t, err)
	require.True(t, r.Allowed)
}

func TestPeekDoesNotConsume(t *testing.T) {
	lim, _ := setupLimiter(t)
	ctx := context.Background()
	params := map[string]any{"sustainedRps": int64(1), "burst": int64(3)}

	_, err := lim.Evaluate(ctx, AlgoFixedToken, "ns", "peek", params)
	require.NoError(t, err)

	before, err := lim.Peek(ctx, AlgoFixedToken, "ns", "peek", params)
	require.NoError(t, err)
	require.Equal(t, int64(2), before.Remaining)
	require.Equal(t, int64(1), before.Consumed)

	after, err := lim.Peek(ctx, AlgoFixedToken, "ns", "peek", params)
	require.NoError(t, err)
	require.Equal(t, before.Remaining, after.Remaining)
	require.Equal(t, before.Consumed, after.Consumed)
}
