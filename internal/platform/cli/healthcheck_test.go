package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serveOn starts a server answering every request with status on a free port and returns the port.
func serveOn(t *testing.T, status int, delay time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(delay)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	return port
}

func TestHealthcheckURL(t *testing.T) {
	cases := map[string]struct {
		addr string
		set  bool
		want string
		bad  bool
	}{
		"default":           {set: false, want: "http://127.0.0.1:8080/healthz"},
		"empty":             {addr: "", set: true, want: "http://127.0.0.1:8080/healthz"},
		"port only":         {addr: ":9000", set: true, want: "http://127.0.0.1:9000/healthz"},
		"wildcard host":     {addr: "0.0.0.0:9000", set: true, want: "http://127.0.0.1:9000/healthz"},
		"specific host":     {addr: "10.1.2.3:9000", set: true, want: "http://127.0.0.1:9000/healthz"},
		"no port":           {addr: "localhost", set: true, bad: true},
		"port zero":         {addr: ":0", set: true, bad: true},
		"port out of range": {addr: ":70000", set: true, bad: true},
		"port text":         {addr: ":http", set: true, bad: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := func(k string) (string, bool) { return c.addr, c.set && k == "SERVICE_ADDR" }
			got, err := HealthcheckURL(lookup, "SERVICE_ADDR", ":8080")
			if c.bad {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestHealthcheckExitCodes(t *testing.T) {
	ctx := context.Background()
	lookupPort := func(port string) func(string) (string, bool) {
		return func(string) (string, bool) { return "127.0.0.1:" + port, true }
	}
	t.Run("healthy", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := Healthcheck(ctx, lookupPort(serveOn(t, http.StatusOK, 0)), "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("code %d stderr %q", code, stderr.String())
		}
	})
	t.Run("server error", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := Healthcheck(ctx, lookupPort(serveOn(t, http.StatusServiceUnavailable, 0)), "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr); code != 1 || !strings.Contains(stderr.String(), "503") {
			t.Fatalf("code %d stderr %q", code, stderr.String())
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(l.Addr().String())
		_ = l.Close()
		var stderr bytes.Buffer
		if code := Healthcheck(ctx, lookupPort(port), "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr); code != 1 || stderr.Len() == 0 {
			t.Fatalf("code %d stderr %q", code, stderr.String())
		}
	})
	t.Run("bad address", func(t *testing.T) {
		var stderr bytes.Buffer
		lookup := func(string) (string, bool) { return "nonsense", true }
		if code := Healthcheck(ctx, lookup, "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr); code != 1 {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		var stderr bytes.Buffer
		if code := Healthcheck(cancelled, lookupPort(serveOn(t, http.StatusOK, 0)), "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr); code != 1 {
			t.Fatalf("code %d: a cancelled probe must fail", code)
		}
	})
}

func TestHealthcheckIsBoundedInTime(t *testing.T) {
	port := serveOn(t, http.StatusOK, 5*time.Second) // slower than the probe timeout
	var stderr bytes.Buffer
	start := time.Now()
	code := Healthcheck(context.Background(), func(string) (string, bool) { return "127.0.0.1:" + port, true }, "SERVICE_ADDR", ":8080", http.DefaultClient, &stderr)
	if code != 1 || time.Since(start) > healthcheckTimeout+time.Second {
		t.Fatalf("code %d after %v: a hung server must fail the probe within its timeout", code, time.Since(start))
	}
}
