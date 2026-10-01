// Command devsecrets creates the local development secrets under ./secrets (git-ignored).
//
// Existing files are never overwritten and secret values are never printed. The same files feed Docker Compose
// (as Docker secrets) and the local secret store the API reads (SECRETS_DIR), see ADR-018 and ADR-031.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// secretNames are the local secrets, one file each.
var secretNames = []string{
	"postgres_superuser_password",
	"postgres_migrator_password",
	"postgres_password",
	"postgres_admin_password",
	"postgres_readonly_password",
}

const secretBytes = 24 // 48 hex characters

func main() {
	dir := flag.String("dir", "secrets", "directory that holds the secret files")
	flag.Parse()

	created, kept, err := ensure(*dir, secretNames, rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devsecrets:", err)
		os.Exit(1)
	}
	for _, n := range created {
		fmt.Println("created", filepath.Join(*dir, n))
	}
	for _, n := range kept {
		fmt.Println("kept   ", filepath.Join(*dir, n))
	}
}

// ensure creates every missing secret file with a random value and reports which were created or kept.
func ensure(dir string, names []string, src io.Reader) (created, kept []string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", dir, err)
	}
	for _, name := range names {
		ok, err := writeIfMissing(filepath.Join(dir, name), src)
		if err != nil {
			return created, kept, err
		}
		if ok {
			created = append(created, name)
		} else {
			kept = append(kept, name)
		}
	}
	return created, kept, nil
}

// writeIfMissing writes a new random secret to path unless the file already exists.
func writeIfMissing(path string, src io.Reader) (bool, error) {
	buf := make([]byte, secretBytes)
	if _, err := io.ReadFull(src, buf); err != nil {
		return false, fmt.Errorf("read randomness: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path is built from a fixed list of secret names
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(hex.EncodeToString(buf)); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
