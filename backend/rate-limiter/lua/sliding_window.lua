-- Sliding window via sorted set of request timestamps.
-- KEYS[1] = rl key
-- ARGV[1] = limit
-- ARGV[2] = windowSeconds
-- ARGV[3] = now_ms
-- ARGV[4] = unique member id
-- Returns: {allowed, remaining, limit, reset_ms}

local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_sec = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local member = ARGV[4]

local window_ms = window_sec * 1000
local cutoff = now_ms - window_ms

redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)

local allowed = 0
local remaining = limit - count
if count < limit then
  redis.call('ZADD', key, now_ms, member)
  allowed = 1
  remaining = limit - count - 1
end

if remaining < 0 then
  remaining = 0
end

redis.call('PEXPIRE', key, window_ms)

local reset_ms = 0
if allowed == 0 then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  if #oldest >= 2 then
    reset_ms = tonumber(oldest[2]) + window_ms - now_ms
    if reset_ms < 0 then
      reset_ms = 0
    end
  else
    reset_ms = window_ms
  end
end

return {allowed, remaining, limit, reset_ms}
