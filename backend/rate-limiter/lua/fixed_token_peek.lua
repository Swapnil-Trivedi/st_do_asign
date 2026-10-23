-- Peek token bucket without consuming.
-- KEYS[1] = rl key
-- ARGV[1] = sustainedRps
-- ARGV[2] = burst
-- ARGV[3] = now_ms
-- Returns: {remaining, limit, reset_ms, consumed}

local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])

if tokens == nil then
  tokens = burst
  ts = now
end

local elapsed = math.max(0, now - ts) / 1000.0
tokens = math.min(burst, tokens + elapsed * rate)

local remaining = math.floor(tokens)
local consumed = burst - remaining
if consumed < 0 then
  consumed = 0
end

local reset_ms = 0
if remaining < 1 then
  reset_ms = math.ceil((1 - tokens) / math.max(rate, 0.001) * 1000)
  if reset_ms < 0 then
    reset_ms = 0
  end
elseif remaining < burst then
  reset_ms = math.ceil((1 / math.max(rate, 0.001)) * 1000)
end

return {remaining, burst, reset_ms, consumed}
