package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

// healthcheckCommand is the argument that turns the binary into a container health probe: the runtime image has no
// shell, curl or wget, so the binary checks itself.
const healthcheckCommand = "healthcheck"

// healthcheckTimeout bounds the probe so a hung process fails the check instead of hanging it.
const healthcheckTimeout = 3 * time.Second

// healthcheck asks the running API on this machine whether it is alive (GET /healthz, which has no dependencies) and
// returns the process exit code: 0 healthy, 1 not. It reads only HTTP_ADDR, the same key the server uses, so a
// non-default port needs no second setting.
func healthcheck(ctx context.Context, lookup config.Lookup, client *http.Client, stderr io.Writer) int {
	target, err := healthcheckURL(lookup)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: unexpected status", resp.StatusCode)
		return 1
	}
	return 0
}

// healthcheckURL builds the probe URL from HTTP_ADDR. The probe always uses the loopback address: the check runs inside
// the same container as the server, whatever interface the server listens on.
func healthcheckURL(lookup config.Lookup) (string, error) {
	addr := ":8080"
	if v, ok := lookup("HTTP_ADDR"); ok && v != "" {
		addr = v
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR %q is not host:port", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("HTTP_ADDR %q has no usable port", addr)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz", nil
}
