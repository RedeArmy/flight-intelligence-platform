//go:build integration

package ratelimit

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisClient connects to TEST_REDIS_ADDR. When the variable is set the tests FAIL if Redis is unreachable, so a
// missing test Redis can never look like a green run; when it is unset they skip.
func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("TEST_REDIS_ADDR is set but Redis is unreachable: %v", err)
	}
	return rdb
}

// uniqueKey keeps parallel tests and repeated runs apart without flushing a shared Redis.
func uniqueKey(t *testing.T) string { return t.Name() + ":" + rand.Text() }

func TestRedisBucketAllowsABurstThenDenies(t *testing.T) {
	t.Parallel()
	l := NewRedis(redisClient(t))
	key := uniqueKey(t)
	slow := Rule{Limit: 5, Window: time.Hour} // refill is negligible during the test
	for i := range 5 {
		d, err := l.Allow(context.Background(), key, slow, 1)
		if err != nil || !d.Allowed || d.Remaining != 4-i || d.Limit != 5 {
			t.Fatalf("call %d: %+v, %v", i, d, err)
		}
	}
	d, err := l.Allow(context.Background(), key, slow, 1)
	if err != nil || d.Allowed || d.Remaining != 0 {
		t.Fatalf("the sixth call must be denied: %+v, %v", d, err)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > time.Hour/5+time.Second || d.ResetAfter < d.RetryAfter {
		t.Fatalf("retry %v reset %v are inconsistent with one token per 12 minutes", d.RetryAfter, d.ResetAfter)
	}
}

func TestRedisBucketRefills(t *testing.T) {
	t.Parallel()
	l := NewRedis(redisClient(t))
	key := uniqueKey(t)
	fast := Rule{Limit: 2, Window: 400 * time.Millisecond} // one token per 200 ms
	for range 2 {
		if d, _ := l.Allow(context.Background(), key, fast, 1); !d.Allowed {
			t.Fatal("the burst must pass")
		}
	}
	if d, _ := l.Allow(context.Background(), key, fast, 1); d.Allowed {
		t.Fatal("the bucket is empty")
	}
	time.Sleep(450 * time.Millisecond)
	if d, err := l.Allow(context.Background(), key, fast, 1); err != nil || !d.Allowed {
		t.Fatalf("the bucket must have refilled: %+v, %v", d, err)
	}
}

func TestRedisZeroCostPeeks(t *testing.T) {
	t.Parallel()
	l := NewRedis(redisClient(t))
	key := uniqueKey(t)
	rule := Rule{Limit: 1, Window: time.Hour}
	for range 3 {
		if d, err := l.Allow(context.Background(), key, rule, 0); err != nil || !d.Allowed || d.Remaining != 1 {
			t.Fatalf("a peek must not consume: %+v, %v", d, err)
		}
	}
	if d, _ := l.Allow(context.Background(), key, rule, 1); !d.Allowed {
		t.Fatal("the token must still be there")
	}
	if d, _ := l.Allow(context.Background(), key, rule, 0); d.Allowed {
		t.Fatal("a peek must report the empty bucket")
	}
}

// TestRedisBucketIsAtomicUnderConcurrency proves that a burst of parallel callers, as from several API instances,
// can never take more tokens than the bucket holds.
func TestRedisBucketIsAtomicUnderConcurrency(t *testing.T) {
	t.Parallel()
	l := NewRedis(redisClient(t))
	key := uniqueKey(t)
	rule := Rule{Limit: 10, Window: time.Hour}
	const callers = 50
	results := make(chan bool, callers)
	for range callers {
		go func() {
			d, err := l.Allow(context.Background(), key, rule, 1)
			results <- err == nil && d.Allowed
		}()
	}
	allowed := 0
	for range callers {
		if <-results {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("%d of %d concurrent calls were allowed, want exactly 10", allowed, callers)
	}
}

func TestRedisRejectsAnInvalidRuleAndReportsOutages(t *testing.T) {
	t.Parallel()
	l := NewRedis(redisClient(t))
	if _, err := l.Allow(context.Background(), "k", Rule{}, 1); err == nil {
		t.Fatal("an invalid rule must be an error")
	}
	dead := NewRedis(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1}))
	if _, err := dead.Allow(context.Background(), "k", Rule{Limit: 1, Window: time.Second}, 1); err == nil {
		t.Fatal("an unreachable Redis must be an error so the fallback can take over")
	}
}
