package database

import (
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

func TestFromConfigMapsEveryFieldAndTakesTheRoleAndPasswordFromTheCaller(t *testing.T) {
	p := config.Postgres{
		Host: "db.internal", Port: 6543, Name: "fipdb", User: "ignored", MigratorUser: "ignored-too", SSLMode: "verify-full",
		MaxConns: 12, MinConns: 3, ConnectTimeout: 7 * time.Second, StatementTimeout: 21 * time.Second, MaxConnLifetime: 45 * time.Minute,
	}
	got := FromConfig(p, "fip_app", secret.Secret("from-the-secret-store"), "api")

	want := Config{
		Host: "db.internal", Port: 6543, Name: "fipdb", User: "fip_app", Password: secret.Secret("from-the-secret-store"),
		SSLMode: "verify-full", AppName: "api", MaxConns: 12, MinConns: 3,
		ConnectTimeout: 7 * time.Second, StatementTimeout: 21 * time.Second, MaxConnLifetime: 45 * time.Minute,
	}
	if got != want {
		t.Fatalf("FromConfig =\n%+v\nwant\n%+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("a configuration built from valid settings must validate: %v", err)
	}
}

func TestFromConfigUsesTheCallersUserNotTheConfiguredOnes(t *testing.T) {
	p := config.Postgres{Host: "h", Port: 5432, Name: "d", User: "runtime", MigratorUser: "migrator", SSLMode: "disable", MaxConns: 1, ConnectTimeout: time.Second}
	if got := FromConfig(p, p.MigratorUser, "pw", "migrate"); got.User != "migrator" || got.AppName != "migrate" {
		t.Fatalf("user=%q app=%q", got.User, got.AppName)
	}
}
