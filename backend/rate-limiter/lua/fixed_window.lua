-- Fixed window counter.
-- KEYS[1] = base rl key (window id appended)
-- ARGV[1] = limit
-- ARGV[2] = windowSeconds
-- ARGV[3] = now_ms
-- Returns: {allowed, remaining, limit, reset_ms}

local base = KEYS[1]
local limit = tonumber(ARGV[1])
local window_sec = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])

local window_ms = window_sec * 1000
local window_id = math.floor(now_ms / window_ms)
local key = base .. ':' .. tostring(window_id)

local count = redis.call('INCR', key)
if count == 1 then
  redis.call('PEXPIRE', key, window_ms)
end

local allowed = 0
if count <= limit then
  allowed = 1
end

local remaining = limit - count
if remaining < 0 then
  remaining = 0
end

local window_end = (window_id + 1) * window_ms
local reset_ms = window_end - now_ms
if reset_ms < 0 then
  reset_ms = 0
end

return {allowed, remaining, limit, reset_ms}
