package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// bucketScript is an atomic token bucket. It uses the Redis server clock (TIME), so instances with different clocks
// share one consistent bucket. KEYS[1] is the bucket; ARGV is limit, window in ms, cost.
// It returns {allowed, remaining, retry_after_ms, reset_after_ms}.
const bucketScript = `
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local t = redis.call('TIME')
local now = t[1] * 1000 + math.floor(t[2] / 1000)
local state = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(state[1])
local ts = tonumber(state[2])
if tokens == nil or ts == nil then
  tokens = limit
  ts = now
end
tokens = math.min(limit, tokens + math.max(0, now - ts) * limit / window)
local need = math.max(cost, 1)
local allowed = 0
local retry = 0
if tokens >= need then
  allowed = 1
  tokens = tokens - cost
else
  retry = math.ceil((need - tokens) * window / limit)
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', now)
redis.call('PEXPIRE', KEYS[1], window + 1000)
return {allowed, math.floor(tokens), retry, math.ceil((limit - tokens) * window / limit)}
`

// keyPrefix namespaces the buckets so they cannot collide with other uses of the same Redis.
const keyPrefix = "fip:rl:"

// Redis is a token-bucket limiter shared by every instance through Redis.
type Redis struct {
	rdb    redis.Scripter
	script *redis.Script
}

var _ Limiter = (*Redis)(nil)

// NewRedis returns a limiter on the given client.
func NewRedis(rdb redis.Scripter) *Redis {
	return &Redis{rdb: rdb, script: redis.NewScript(bucketScript)}
}

// Allow implements Limiter. Any Redis failure is returned so a Fallback can take over.
func (r *Redis) Allow(ctx context.Context, key string, rule Rule, cost int) (Decision, error) {
	if !rule.Valid() {
		return Decision{}, errors.New("ratelimit: invalid rule")
	}
	res, err := r.script.Run(ctx, r.rdb, []string{keyPrefix + key}, rule.Limit, rule.Window.Milliseconds(), cost).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("ratelimit: redis: %w", err)
	}
	if len(res) != 4 {
		return Decision{}, fmt.Errorf("ratelimit: unexpected script reply of %d values", len(res))
	}
	return Decision{
		Allowed:    res[0] == 1,
		Limit:      rule.Limit,
		Remaining:  int(res[1]),
		RetryAfter: time.Duration(res[2]) * time.Millisecond,
		ResetAfter: time.Duration(res[3]) * time.Millisecond,
	}, nil
}
