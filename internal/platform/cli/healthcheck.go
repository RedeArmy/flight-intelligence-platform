package cli

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

// HealthcheckCommand is the argument that turns a service binary into a container health probe: the runtime image has
// no shell, curl or wget, so the binary checks itself.
const HealthcheckCommand = "healthcheck"

// healthcheckTimeout bounds the probe so a hung process fails the check instead of hanging it.
const healthcheckTimeout = 3 * time.Second

// Healthcheck asks the running service on this machine whether it is alive (GET /healthz, which has no dependencies)
// and returns the process exit code: 0 healthy, 1 not. It reads the service's own listen-address key (addrKey, for
// example HTTP_ADDR), the same key the server uses, so a non-default port needs no second setting.
func Healthcheck(ctx context.Context, lookup config.Lookup, addrKey, defaultAddr string, client *http.Client, stderr io.Writer) int {
	target, err := HealthcheckURL(lookup, addrKey, defaultAddr)
	if err != nil {
		return probeFailed(stderr, err)
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return probeFailed(stderr, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return probeFailed(stderr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return probeFailed(stderr, "unexpected status", resp.StatusCode)
	}
	return 0
}

// probeFailed reports why the probe failed on stderr, where the container runtime records it, and returns the exit code.
func probeFailed(stderr io.Writer, reason ...any) int {
	fmt.Fprintln(stderr, append([]any{"healthcheck:"}, reason...)...)
	return 1
}

// HealthcheckURL builds the probe URL from the listen address in addrKey (or defaultAddr when it is unset). The probe
// always uses the loopback address: the check runs inside the same container as the server, whatever interface the
// server listens on.
func HealthcheckURL(lookup config.Lookup, addrKey, defaultAddr string) (string, error) {
	addr := defaultAddr
	if v, ok := lookup(addrKey); ok && v != "" {
		addr = v
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s %q is not host:port", addrKey, addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s %q has no usable port", addrKey, addr)
	}
	// Plain HTTP on purpose: this is a probe of the server's own loopback listener inside the same container, and the
	// services serve plain HTTP and leave TLS to the edge in front of them. Nothing leaves the machine.
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz", nil // NOSONAR
}
