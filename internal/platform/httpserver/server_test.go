package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServer runs a Server on random loopback ports and returns its addresses plus a result channel.
func startServer(t *testing.T, opts Options) (public, operator string, cancel context.CancelFunc, result <-chan error) {
	t.Helper()
	tl := newTestLog(t)
	if opts.Logger == nil {
		opts.Logger = tl.Logger
	}
	if opts.Operator == nil {
		opts.Operator = NewOperatorHandler(OperatorDeps{Logger: opts.Logger})
	}
	opts.PublicAddr, opts.OperatorAddr = "127.0.0.1:0", "127.0.0.1:0"
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = 5 * time.Second
	}
	opts.ReadHeaderTimeout = time.Second

	addrs := make(chan [2]string, 1)
	opts.OnListening = func(p, o net.Addr) { addrs <- [2]string{p.String(), o.String()} }

	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- srv.Run(ctx) }()

	select {
	case a := <-addrs:
		return a[0], a[1], cancelFn, res
	case err := <-res:
		cancelFn()
		t.Fatalf("server stopped before listening: %v", err)
	case <-time.After(5 * time.Second):
		cancelFn()
		t.Fatal("server did not start listening")
	}
	return "", "", cancelFn, res
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func waitResult(t *testing.T, res <-chan error) error {
	t.Helper()
	select {
	case err := <-res:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestServerServesBothListenersAndStopsCleanly(t *testing.T) {
	tl := newTestLog(t)
	health := NewHealth(time.Second)
	pub := NewPublicHandler(PublicDeps{Logger: tl.Logger, Health: health, MaxBodyBytes: 1024})
	op := NewOperatorHandler(OperatorDeps{Logger: tl.Logger})
	pubAddr, opAddr, cancel, res := startServer(t, Options{Logger: tl.Logger, Public: pub, Operator: op, Health: health})

	if code, body := get(t, "http://"+pubAddr+"/healthz"); code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Errorf("public healthz: %d %s", code, body)
	}
	if code, _ := get(t, "http://"+opAddr+"/healthz"); code != http.StatusNotFound {
		t.Errorf("operator listener must not serve public routes, got %d", code)
	}

	cancel()
	if err := waitResult(t, res); err != nil {
		t.Fatalf("clean shutdown returned %v", err)
	}
	if !health.Draining() {
		t.Error("readiness must report draining after shutdown")
	}
}

func TestServerFinishesInFlightRequestsBeforeStopping(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	health := NewHealth(time.Second)
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "finished")
	})
	pubAddr, _, cancel, res := startServer(t, Options{Public: slow, Health: health})

	type outcome struct {
		code int
		body string
	}
	done := make(chan outcome, 1)
	go func() {
		code, body := get(t, "http://"+pubAddr+"/")
		done <- outcome{code, body}
	}()
	<-started

	cancel()
	eventually(t, "readiness to flip to draining", health.Draining)
	select {
	case <-done:
		t.Fatal("the in-flight request was cut off by shutdown")
	case err := <-res:
		t.Fatalf("Run returned %v while a request was still in flight", err)
	default:
	}

	close(release)
	got := <-done
	if got.code != http.StatusOK || got.body != "finished" {
		t.Errorf("in-flight request: %d %q", got.code, got.body)
	}
	if err := waitResult(t, res); err != nil {
		t.Fatalf("shutdown after draining returned %v", err)
	}
}

func TestServerForceClosesWhenTheGracePeriodExpires(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stuck := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		once.Do(func() { close(started) })
		<-release
	})
	pubAddr, _, cancel, res := startServer(t, Options{Public: stuck, ShutdownTimeout: 50 * time.Millisecond})
	defer close(release)

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+pubAddr+"/", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	cancel()
	err := waitResult(t, res)
	if err == nil || !strings.Contains(err.Error(), "graceful shutdown did not finish") {
		t.Fatalf("expected a forced-close error, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the error should wrap the deadline: %v", err)
	}
}

func TestServerFailsFastWhenAPortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	tl := newTestLog(t)
	h := http.NotFoundHandler()

	for name, opts := range map[string]Options{
		"public":   {PublicAddr: taken.Addr().String(), OperatorAddr: "127.0.0.1:0"},
		"operator": {PublicAddr: "127.0.0.1:0", OperatorAddr: taken.Addr().String()},
	} {
		t.Run(name, func(t *testing.T) {
			opts.Logger, opts.Public, opts.Operator, opts.ShutdownTimeout = tl.Logger, h, h, time.Second
			srv, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			runErr := srv.Run(context.Background())
			if runErr == nil || !strings.Contains(runErr.Error(), "listen on "+name+" address") {
				t.Fatalf("Run = %v", runErr)
			}
		})
	}
}

func TestNewValidatesOptions(t *testing.T) {
	tl := newTestLog(t)
	h := http.NotFoundHandler()
	cases := map[string]Options{
		"no logger":           {Public: h, Operator: h, ShutdownTimeout: time.Second},
		"no public handler":   {Logger: tl.Logger, Operator: h, ShutdownTimeout: time.Second},
		"no shutdown timeout": {Logger: tl.Logger, Public: h, Operator: h},
	}
	for name, opts := range cases {
		if _, err := New(opts); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := New(Options{Logger: tl.Logger, Public: h, Operator: h, ShutdownTimeout: time.Second}); err != nil {
		t.Errorf("valid options rejected: %v", err)
	}
	if _, err := New(Options{Logger: tl.Logger, Public: h, ShutdownTimeout: time.Second}); err != nil {
		t.Errorf("a single listener (no operator handler) must be accepted: %v", err)
	}
}

func TestServerWorksWithoutAHealthObject(t *testing.T) {
	pubAddr, _, cancel, res := startServer(t, Options{Public: http.NotFoundHandler()})
	if code, _ := get(t, "http://"+pubAddr+"/x"); code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
	cancel()
	if err := waitResult(t, res); err != nil {
		t.Fatal(err)
	}
}
