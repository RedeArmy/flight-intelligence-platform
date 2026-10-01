package migrate

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

func testConfig() database.Config {
	return database.Config{
		Host: "db.internal", Port: 5432, Name: "fip", User: "fip_migrator", Password: secret.Secret("p@ss:w/rd#?&=x"),
		SSLMode: database.SSLVerifyFull, MaxConns: 1, ConnectTimeout: time.Second,
	}
}

func TestDSNEscapesCredentialsAndCarriesTheTLSMode(t *testing.T) {
	r := New(testConfig(), fstest.MapFS{}, nil)
	dsn, err := r.dsn()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("dsn is not a valid URL: %v", err)
	}
	pw, _ := u.User.Password()
	if u.Scheme != "pgx5" || u.User.Username() != "fip_migrator" || pw != "p@ss:w/rd#?&=x" {
		t.Errorf("credentials did not round-trip: %s %s %q", u.Scheme, u.User.Username(), pw)
	}
	if u.Host != "db.internal:5432" || u.Path != "/fip" || u.Query().Get("sslmode") != "verify-full" {
		t.Errorf("target = %s %s ?%s", u.Host, u.Path, u.RawQuery)
	}
}

func TestDSNRejectsAnInvalidConfig(t *testing.T) {
	c := testConfig()
	c.Host = ""
	if _, err := New(c, fstest.MapFS{}, nil).dsn(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSanitizeRemovesTheConnectionStringFromErrors(t *testing.T) {
	const dsn = "pgx5://fip_migrator:s3cret@db:5432/fip?sslmode=disable"
	err := sanitize(errors.New("dial failed for "+dsn+": refused"), dsn)
	if strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "<dsn redacted>") {
		t.Fatalf("sanitize = %q", err)
	}
	plain := errors.New("no connection string in here")
	if !errors.Is(sanitize(plain, dsn), plain) {
		t.Error("an error without the DSN must pass through unchanged")
	}
	if sanitize(nil, dsn) != nil {
		t.Error("nil must stay nil")
	}
}

func TestDownRequiresAtLeastOneStep(t *testing.T) {
	for _, steps := range []int{0, -1} {
		if err := New(testConfig(), fstest.MapFS{}, nil).Down(context.Background(), steps); err == nil {
			t.Errorf("Down(%d) must be rejected", steps)
		}
	}
}

func TestStepsRejectsZero(t *testing.T) {
	if err := New(testConfig(), fstest.MapFS{}, nil).Steps(context.Background(), 0); err == nil {
		t.Fatal("Steps(0) must be rejected")
	}
}

func TestCancelledContextStopsBeforeConnecting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := New(testConfig(), fstest.MapFS{}, nil)
	if err := r.Up(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Up = %v", err)
	}
	if _, _, err := r.Version(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Version = %v", err)
	}
}

func TestInvalidConfigFailsBeforeAnyConnection(t *testing.T) {
	c := testConfig()
	c.Port = 0
	if err := New(c, fstest.MapFS{}, nil).Up(context.Background()); err == nil {
		t.Fatal("expected a configuration error")
	}
}
