package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter implements a fixed-window rate limiter backed by Redis.
//
// Algorithm (atomic Lua script):
//  1. INCR <key>      — atomically increment the request counter
//  2. If counter == 1 (first request in this window): EXPIRE <key> window
//  3. If counter > limit: deny
//
// Key format: "rl:<zone>:<ip>:<window_bucket>"
// The window bucket is Unix-seconds / window.Seconds() so each window is a
// distinct Redis key that expires naturally.
//
// Why fixed-window over sliding?
//   - O(1) Redis ops regardless of request volume (vs. sorted-set sliding)
//   - Simple to reason about for an interview; the burst-at-boundary problem
//     is mitigated by the per-zone limits being generous enough that a 2x
//     spike is tolerable.
type RedisLimiter struct {
	client *redis.Client
	zone   string        // label used in the Redis key, e.g. "auth", "upload", "api"
	limit  int           // max requests per window
	window time.Duration // window duration, e.g. time.Minute
}

// NewRedisLimiter creates a RedisLimiter for the given zone, limit, and window.
func NewRedisLimiter(client *redis.Client, zone string, limit int, window time.Duration) *RedisLimiter {
	return &RedisLimiter{client: client, zone: zone, limit: limit, window: window}
}

// incrScript atomically increments the counter and sets the TTL on the first
// request of a new window. Returns [count, ttlSeconds].
var incrScript = redis.NewScript(`
local key     = KEYS[1]
local limit   = tonumber(ARGV[1])
local window  = tonumber(ARGV[2])
local count   = redis.call("INCR", key)
if count == 1 then
    redis.call("EXPIRE", key, window)
end
local ttl = redis.call("TTL", key)
return {count, ttl}
`)

// Allow checks whether key (typically an IP address) may proceed.
func (l *RedisLimiter) Allow(ctx context.Context, key string) Decision {
	bucket := l.windowBucket()
	redisKey := fmt.Sprintf("rl:%s:%s:%d", l.zone, key, bucket)
	windowSec := int(l.window.Seconds())

	result, err := incrScript.Run(ctx, l.client, []string{redisKey},
		l.limit, windowSec).Slice()
	if err != nil {
		// Redis error → fail open (allow the request) to avoid taking down
		// the service when Redis is temporarily unavailable.
		return Decision{
			Allowed:   true,
			Limit:     l.limit,
			Remaining: l.limit,
			ResetAt:   time.Now().Add(l.window),
		}
	}

	count := int(result[0].(int64))
	ttl := int(result[1].(int64))
	if ttl < 0 {
		ttl = windowSec
	}

	remaining := l.limit - count
	if remaining < 0 {
		remaining = 0
	}
	resetAt := time.Now().Add(time.Duration(ttl) * time.Second)

	return Decision{
		Allowed:   count <= l.limit,
		Limit:     l.limit,
		Remaining: remaining,
		ResetAt:   resetAt,
	}
}

// windowBucket returns the current fixed-window bucket index (Unix seconds
// divided by the window duration). Each unique value maps to one Redis key.
func (l *RedisLimiter) windowBucket() int64 {
	return time.Now().Unix() / int64(l.window.Seconds())
}
