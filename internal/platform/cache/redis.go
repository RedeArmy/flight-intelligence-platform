// Package cache wraps the Redis client (ADR-004). Redis holds ephemeral state only: losing it must degrade
// performance and limits, never correctness, so callers treat every error as "Redis is unavailable".
package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	"github.com/redis/go-redis/v9"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// Client is a Redis connection pool.
type Client struct {
	rdb *redis.Client
}

// Open creates the client. It does not connect eagerly: the process can start while Redis is still coming up.
// Timeouts are short and retries are off, so a slow or dead Redis costs one bounded attempt, not a pile-up.
func Open(c config.Redis, password secret.Secret) (*Client, error) {
	if c.Addr == "" {
		return nil, errors.New("cache: no Redis address configured")
	}
	opts := &redis.Options{
		Addr:         c.Addr,
		Password:     password.Reveal(),
		DialTimeout:  c.Timeout,
		ReadTimeout:  c.Timeout,
		WriteTimeout: c.Timeout,
		MaxRetries:   -1,
	}
	if c.TLS {
		host, _, err := net.SplitHostPort(c.Addr)
		if err != nil {
			return nil, fmt.Errorf("cache: invalid Redis address: %w", err)
		}
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return &Client{rdb: redis.NewClient(opts)}, nil
}

// Scripter exposes the client to components that run Lua scripts, such as the rate limiter.
func (c *Client) Scripter() redis.Scripter { return c.rdb }

// Check verifies that Redis answers PING. Use it as an optional readiness check.
func (c *Client) Check(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	return nil
}

// Close releases every connection.
func (c *Client) Close() error { return c.rdb.Close() }
