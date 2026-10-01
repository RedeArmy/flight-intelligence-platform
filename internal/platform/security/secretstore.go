// Package security holds platform security building blocks. The SecretStore port is how every secret reaches the
// process (ADR-018, ADR-031): code never reads credentials from configuration or the environment directly.
package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// SecretGetter returns the secret stored under name. It is the SecretStore port of ADR-018.
type SecretGetter interface {
	Get(ctx context.Context, name string) (secret.Secret, error)
}

// Error codes returned by secret stores.
const (
	CodeSecretNotFound    = "SECRET_NOT_FOUND"
	CodeSecretNameInvalid = "SECRET_NAME_INVALID"
)

// ErrSecretNotFound is returned when no value exists for a secret name.
var ErrSecretNotFound = sharederrors.NotFound(CodeSecretNotFound, "secret not found")

// ErrLocalStoreForbidden is returned when the local store is requested in a production-like environment (SR-19).
var ErrLocalStoreForbidden = errors.New("the local secret store is not available in production-like environments: configure a managed secret store")

var secretName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

const (
	maxSecretBytes = 4096
	envPrefix      = "SECRET_"
)

// LocalStore reads secrets for local development and tests: first from the environment variable SECRET_<NAME>
// (upper case), then from a file named <name> inside Dir. It refuses to exist in production-like environments.
type LocalStore struct {
	lookup config.Lookup
	dir    string
}

var _ SecretGetter = (*LocalStore)(nil)

// NewLocalStore returns a local store. dir may be empty to disable file lookup. It fails when env is production-like.
func NewLocalStore(env config.Env, lookup config.Lookup, dir string) (*LocalStore, error) {
	if !env.AllowsLocalFeatures() {
		return nil, ErrLocalStoreForbidden
	}
	return &LocalStore{lookup: lookup, dir: dir}, nil
}

// Get returns the named secret. An empty value counts as missing.
func (s *LocalStore) Get(ctx context.Context, name string) (secret.Secret, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !secretName.MatchString(name) {
		return "", sharederrors.Invalid(CodeSecretNameInvalid, "secret names are lower_snake_case, 2 to 64 characters")
	}
	if v, ok := s.lookup(envPrefix + strings.ToUpper(name)); ok && strings.TrimSpace(v) != "" {
		return secret.Secret(strings.TrimSpace(v)), nil
	}
	if s.dir == "" {
		return "", ErrSecretNotFound.WithMessage("secret " + name + " not found")
	}
	value, err := readSecretFile(s.dir, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", ErrSecretNotFound.WithMessage("secret " + name + " not found")
	}
	return secret.Secret(value), nil
}

// readSecretFile reads dir/name without letting name escape dir (os.OpenRoot), capped at maxSecretBytes.
// A missing file is not an error: it returns "".
func readSecretFile(dir, name string) (string, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open secrets directory: %w", err)
	}
	defer root.Close()

	f, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open secret file %q: %w", name, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", name, err)
	}
	if len(data) > maxSecretBytes {
		return "", fmt.Errorf("secret file %q exceeds %d bytes", name, maxSecretBytes)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}
