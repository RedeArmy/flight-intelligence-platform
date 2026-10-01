package cache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

func TestOpenNeedsAnAddress(t *testing.T) {
	if _, err := Open(config.Redis{Timeout: time.Second}, ""); err == nil {
		t.Fatal("an empty address must be refused")
	}
}

func TestOpenRejectsAMalformedAddressWhenTLSIsOn(t *testing.T) {
	if _, err := Open(config.Redis{Addr: "no-port", TLS: true, Timeout: time.Second}, ""); err == nil {
		t.Fatal("TLS needs a host to verify, so a malformed address must be refused")
	}
}

func TestAnUnreachableRedisFailsTheCheckQuickly(t *testing.T) {
	c, err := Open(config.Redis{Addr: "127.0.0.1:1", Timeout: 100 * time.Millisecond}, secret.Secret("irrelevant"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	err = c.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the check took %v: a dead Redis must cost one short attempt, not retries", time.Since(start))
	}
	if c.Scripter() == nil {
		t.Fatal("the scripter must be available for the rate limiter")
	}
}

func TestOpenWithTLSConfiguresVerification(t *testing.T) {
	c, err := Open(config.Redis{Addr: "cache.internal:6380", TLS: true, Timeout: time.Second}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.rdb.Options().TLSConfig == nil || c.rdb.Options().TLSConfig.ServerName != "cache.internal" || c.rdb.Options().TLSConfig.InsecureSkipVerify {
		t.Fatalf("TLS config = %+v", c.rdb.Options().TLSConfig)
	}
}
