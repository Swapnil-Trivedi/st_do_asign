-- Peek sliding window without consuming (trims expired members only).
-- KEYS[1] = rl key
-- ARGV[1] = limit
-- ARGV[2] = windowSeconds
-- ARGV[3] = now_ms
-- Returns: {remaining, limit, reset_ms, consumed}

local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_sec = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])

local window_ms = window_sec * 1000
local cutoff = now_ms - window_ms

redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)

local remaining = limit - count
if remaining < 0 then
  remaining = 0
end

local reset_ms = 0
if count > 0 then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  if #oldest >= 2 then
    reset_ms = tonumber(oldest[2]) + window_ms - now_ms
    if reset_ms < 0 then
      reset_ms = 0
    end
  end
end

return {remaining, limit, reset_ms, count}
