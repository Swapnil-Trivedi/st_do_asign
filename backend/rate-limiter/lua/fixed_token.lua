-- Token bucket: refill at sustainedRps, capacity burst.
-- KEYS[1] = rl key
-- ARGV[1] = sustainedRps
-- ARGV[2] = burst
-- ARGV[3] = now_ms
-- Returns: {allowed, remaining, limit, reset_ms}

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

local allowed = 0
local remaining = tokens
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
  remaining = tokens
end

redis.call('HSET', key, 'tokens', tostring(tokens), 'ts', tostring(now))

local ttl_ms = math.ceil((burst / math.max(rate, 0.001)) * 1000) * 2
if ttl_ms < 60000 then
  ttl_ms = 60000
end
redis.call('PEXPIRE', key, ttl_ms)

local reset_ms = 0
if allowed == 0 then
  reset_ms = math.ceil((1 - tokens) / math.max(rate, 0.001) * 1000)
  if reset_ms < 0 then
    reset_ms = 0
  end
end

return {allowed, math.floor(remaining), burst, reset_ms}
