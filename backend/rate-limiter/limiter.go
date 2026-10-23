package main

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/fixed_token.lua
var luaFixedToken string

//go:embed lua/fixed_window.lua
var luaFixedWindow string

//go:embed lua/sliding_window.lua
var luaSlidingWindow string

//go:embed lua/fixed_token_peek.lua
var luaFixedTokenPeek string

//go:embed lua/fixed_window_peek.lua
var luaFixedWindowPeek string

//go:embed lua/sliding_window_peek.lua
var luaSlidingWindowPeek string

type Limiter struct {
	rdb         *redis.Client
	fixed       *redis.Script
	window      *redis.Script
	slide       *redis.Script
	fixedPeek   *redis.Script
	windowPeek  *redis.Script
	slidePeek   *redis.Script
}

func newLimiter(rdb *redis.Client) *Limiter {
	return &Limiter{
		rdb:        rdb,
		fixed:      redis.NewScript(luaFixedToken),
		window:     redis.NewScript(luaFixedWindow),
		slide:      redis.NewScript(luaSlidingWindow),
		fixedPeek:  redis.NewScript(luaFixedTokenPeek),
		windowPeek: redis.NewScript(luaFixedWindowPeek),
		slidePeek:  redis.NewScript(luaSlidingWindowPeek),
	}
}

func (l *Limiter) Ping(ctx context.Context) error {
	return l.rdb.Ping(ctx).Err()
}

func redisKey(algorithm, namespaceID, subject string) string {
	return fmt.Sprintf("rl:%s:%s:%s", algorithm, namespaceID, subject)
}

func (l *Limiter) Evaluate(ctx context.Context, algorithm, namespaceID, subject string, params map[string]any) (LimitResult, error) {
	key := redisKey(algorithm, namespaceID, subject)
	nowMs := time.Now().UnixMilli()

	switch algorithm {
	case AlgoFixedToken:
		rate, err := intParam(params, "sustainedRps")
		if err != nil {
			return LimitResult{}, err
		}
		burst, err := intParam(params, "burst")
		if err != nil {
			return LimitResult{}, err
		}
		return l.runEval(ctx, l.fixed, []string{key}, rate, burst, nowMs)
	case AlgoFixedWindow:
		limit, err := intParam(params, "limit")
		if err != nil {
			return LimitResult{}, err
		}
		win, err := intParam(params, "windowSeconds")
		if err != nil {
			return LimitResult{}, err
		}
		return l.runEval(ctx, l.window, []string{key}, limit, win, nowMs)
	case AlgoSlidingWindow:
		limit, err := intParam(params, "limit")
		if err != nil {
			return LimitResult{}, err
		}
		win, err := intParam(params, "windowSeconds")
		if err != nil {
			return LimitResult{}, err
		}
		member := uuid.NewString()
		return l.runEval(ctx, l.slide, []string{key}, limit, win, nowMs, member)
	default:
		return LimitResult{}, fmt.Errorf("unknown algorithm %q", algorithm)
	}
}

// Peek returns current quota state without consuming a unit.
func (l *Limiter) Peek(ctx context.Context, algorithm, namespaceID, subject string, params map[string]any) (QuotaState, error) {
	key := redisKey(algorithm, namespaceID, subject)
	nowMs := time.Now().UnixMilli()

	var remaining, limit, resetMs, consumed int64
	var err error

	switch algorithm {
	case AlgoFixedToken:
		rate, e := intParam(params, "sustainedRps")
		if e != nil {
			return QuotaState{}, e
		}
		burst, e := intParam(params, "burst")
		if e != nil {
			return QuotaState{}, e
		}
		remaining, limit, resetMs, consumed, err = l.runPeek(ctx, l.fixedPeek, []string{key}, rate, burst, nowMs)
	case AlgoFixedWindow:
		lim, e := intParam(params, "limit")
		if e != nil {
			return QuotaState{}, e
		}
		win, e := intParam(params, "windowSeconds")
		if e != nil {
			return QuotaState{}, e
		}
		remaining, limit, resetMs, consumed, err = l.runPeek(ctx, l.windowPeek, []string{key}, lim, win, nowMs)
	case AlgoSlidingWindow:
		lim, e := intParam(params, "limit")
		if e != nil {
			return QuotaState{}, e
		}
		win, e := intParam(params, "windowSeconds")
		if e != nil {
			return QuotaState{}, e
		}
		remaining, limit, resetMs, consumed, err = l.runPeek(ctx, l.slidePeek, []string{key}, lim, win, nowMs)
	default:
		return QuotaState{}, fmt.Errorf("unknown algorithm %q", algorithm)
	}
	if err != nil {
		return QuotaState{}, err
	}

	now := time.Now().UTC()
	return QuotaState{
		Algorithm: algorithm,
		RedisKey:  key,
		Subject:   subject,
		Limit:     limit,
		Remaining: remaining,
		Consumed:  consumed,
		ResetMs:   resetMs,
		ResetAt:   now.Add(time.Duration(resetMs) * time.Millisecond),
		ObservedAt: now,
	}, nil
}

func (l *Limiter) runEval(ctx context.Context, script *redis.Script, keys []string, args ...any) (LimitResult, error) {
	raw, err := script.Run(ctx, l.rdb, keys, args...).Result()
	if err != nil {
		return LimitResult{}, err
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) < 4 {
		return LimitResult{}, fmt.Errorf("unexpected lua result: %T %v", raw, raw)
	}
	allowed, err := toInt64(arr[0])
	if err != nil {
		return LimitResult{}, err
	}
	remaining, err := toInt64(arr[1])
	if err != nil {
		return LimitResult{}, err
	}
	limit, err := toInt64(arr[2])
	if err != nil {
		return LimitResult{}, err
	}
	resetMs, err := toInt64(arr[3])
	if err != nil {
		return LimitResult{}, err
	}
	return LimitResult{
		Allowed:   allowed == 1,
		Remaining: remaining,
		Limit:     limit,
		ResetMs:   resetMs,
	}, nil
}

func (l *Limiter) runPeek(ctx context.Context, script *redis.Script, keys []string, args ...any) (remaining, limit, resetMs, consumed int64, err error) {
	raw, err := script.Run(ctx, l.rdb, keys, args...).Result()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) < 4 {
		return 0, 0, 0, 0, fmt.Errorf("unexpected peek result: %T %v", raw, raw)
	}
	remaining, err = toInt64(arr[0])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	limit, err = toInt64(arr[1])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	resetMs, err = toInt64(arr[2])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	consumed, err = toInt64(arr[3])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return remaining, limit, resetMs, consumed, nil
}

func intParam(params map[string]any, key string) (int64, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("missing param %q", key)
	}
	n, err := toInt64(v)
	if err != nil {
		return 0, fmt.Errorf("param %q: %w", key, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("param %q must be >= 1", key)
	}
	return n, nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case int32:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case float32:
		return int64(n), nil
	case string:
		return strconv.ParseInt(n, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}
