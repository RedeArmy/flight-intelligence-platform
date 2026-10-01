package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// API key format (ADR-027): fip_<prefix>_<secret>. The prefix is public (8 characters, the lookup handle); the
// secret is 32 random bytes in unpadded base64url (256 bits). Only HMAC-SHA-256(pepper, secret) is stored.
const (
	keyScheme       = "fip"
	prefixLength    = 8
	keySecretBytes  = 32
	keySecretLength = 43 // base64.RawURLEncoding of 32 bytes
	prefixAlphabet  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	// MinPepperBytes is the shortest pepper accepted.
	MinPepperBytes = 32
)

// GeneratedKey is a newly issued key. Token is shown to its owner once and never stored.
type GeneratedKey struct {
	Prefix string
	Token  secret.Secret
	Hash   []byte
}

// KeyHasher computes and checks key hashes with a server-side pepper that is kept in the secret store, not in
// the database, so a database leak alone does not allow verifying guesses.
type KeyHasher struct {
	pepper []byte
}

// NewKeyHasher returns a hasher. The pepper must have at least MinPepperBytes bytes.
func NewKeyHasher(pepper secret.Secret) (*KeyHasher, error) {
	if len(pepper.Reveal()) < MinPepperBytes {
		return nil, fmt.Errorf("security: the API key pepper must have at least %d bytes", MinPepperBytes)
	}
	return &KeyHasher{pepper: []byte(pepper.Reveal())}, nil
}

// Hash returns HMAC-SHA-256(pepper, keySecret).
func (h *KeyHasher) Hash(keySecret string) []byte {
	m := hmac.New(sha256.New, h.pepper)
	m.Write([]byte(keySecret))
	return m.Sum(nil)
}

// Matches reports whether keySecret hashes to stored, in constant time.
func (h *KeyHasher) Matches(stored []byte, keySecret string) bool {
	return hmac.Equal(stored, h.Hash(keySecret))
}

// Generate creates a new key reading randomness from entropy (crypto/rand.Reader in production).
func (h *KeyHasher) Generate(entropy io.Reader) (GeneratedKey, error) {
	prefix, err := randomPrefix(entropy)
	if err != nil {
		return GeneratedKey{}, err
	}
	raw := make([]byte, keySecretBytes)
	if _, err := io.ReadFull(entropy, raw); err != nil {
		return GeneratedKey{}, fmt.Errorf("security: read randomness: %w", err)
	}
	keySecret := base64.RawURLEncoding.EncodeToString(raw)
	return GeneratedKey{
		Prefix: prefix,
		Token:  secret.Secret(keyScheme + "_" + prefix + "_" + keySecret),
		Hash:   h.Hash(keySecret),
	}, nil
}

// GenerateKey is Generate using the operating system's randomness.
func (h *KeyHasher) GenerateKey() (GeneratedKey, error) { return h.Generate(rand.Reader) }

// randomPrefix draws prefixLength characters uniformly: bytes at or above 248 are discarded (62*4) so the
// modulo does not favour any character.
func randomPrefix(entropy io.Reader) (string, error) {
	const limit = 256 - 256%len(prefixAlphabet)
	out := make([]byte, 0, prefixLength)
	buf := make([]byte, prefixLength*2)
	for len(out) < prefixLength {
		if _, err := io.ReadFull(entropy, buf); err != nil {
			return "", fmt.Errorf("security: read randomness: %w", err)
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < prefixLength {
				out = append(out, prefixAlphabet[int(b)%len(prefixAlphabet)])
			}
		}
	}
	return string(out), nil
}

// ErrMalformedKey is returned when a token does not have the API key shape. Callers must not tell the client why.
var ErrMalformedKey = errors.New("security: malformed API key")

// ParseKey splits a token into its prefix and secret, validating the shape only (not whether the key exists).
func ParseKey(token string) (prefix, keySecret string, err error) {
	// The secret is base64url and may itself contain "_", so only the first two separators count.
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != keyScheme || len(parts[1]) != prefixLength || len(parts[2]) != keySecretLength {
		return "", "", ErrMalformedKey
	}
	for _, c := range parts[1] {
		if !strings.ContainsRune(prefixAlphabet, c) {
			return "", "", ErrMalformedKey
		}
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil {
		return "", "", ErrMalformedKey
	}
	return parts[1], parts[2], nil
}
