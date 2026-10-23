-- Peek fixed window without consuming.
-- KEYS[1] = base rl key
-- ARGV[1] = limit
-- ARGV[2] = windowSeconds
-- ARGV[3] = now_ms
-- Returns: {remaining, limit, reset_ms, consumed}

local base = KEYS[1]
local limit = tonumber(ARGV[1])
local window_sec = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])

local window_ms = window_sec * 1000
local window_id = math.floor(now_ms / window_ms)
local key = base .. ':' .. tostring(window_id)

local count = tonumber(redis.call('GET', key)) or 0
local remaining = limit - count
if remaining < 0 then
  remaining = 0
end

local window_end = (window_id + 1) * window_ms
local reset_ms = window_end - now_ms
if reset_ms < 0 then
  reset_ms = 0
end

return {remaining, limit, reset_ms, count}
