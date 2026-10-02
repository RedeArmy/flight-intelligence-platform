package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pgReadyLines returns the lines of text that run pg_isready.
func pgReadyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "pg_isready") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// TestPostgresHealthchecksProbeOverTCP guards a first-start race. On a new data volume the PostgreSQL image runs a
// temporary server that listens only on a Unix socket while it executes the init scripts. A pg_isready over the socket
// answers "accepting connections" during that phase, so Compose can start the migration job, which connects over TCP
// and is refused. Probing 127.0.0.1 only succeeds once the real server is up. The race is rare (one failure in eight
// first starts when it was measured) which is exactly why it needs a test instead of luck.
func TestPostgresHealthchecksProbeOverTCP(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "deployments", "local", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := pgReadyLines(string(b))
	if len(lines) < 2 {
		t.Fatalf("found %d pg_isready healthchecks, expected at least the two PostgreSQL services; the test is not checking anything", len(lines))
	}
	for _, l := range lines {
		if !strings.Contains(l, "pg_isready -h 127.0.0.1") {
			t.Errorf("healthcheck %q must probe over TCP (pg_isready -h 127.0.0.1 ...): over the socket it passes while a new volume is still initialising", l)
		}
	}
}

func TestPgReadyLinesFindsWhatItShould(t *testing.T) {
	text := "a: 1\n      test: [\"CMD-SHELL\", \"pg_isready -U postgres -d fip\"]\n  b: 2\n      test: [\"CMD-SHELL\", \"pg_isready -h 127.0.0.1 -U postgres\"]\n"
	got := pgReadyLines(text)
	if len(got) != 2 || !strings.Contains(got[0], "-U postgres -d fip") || !strings.Contains(got[1], "-h 127.0.0.1") {
		t.Fatalf("got %v", got)
	}
}
