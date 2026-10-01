package security

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

func testHasher(t *testing.T) *KeyHasher {
	t.Helper()
	h, err := NewKeyHasher(secret.Secret(strings.Repeat("pepper-", 6)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNewKeyHasherRejectsAShortPepper(t *testing.T) {
	if _, err := NewKeyHasher(secret.Secret("short")); err == nil {
		t.Fatal("a short pepper must be refused")
	}
}

func TestGeneratedKeyHasTheDocumentedShapeAndVerifies(t *testing.T) {
	h := testHasher(t)
	k, err := h.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix, keySecret, err := ParseKey(k.Token.Reveal())
	if err != nil {
		t.Fatalf("a generated key must parse: %v", err)
	}
	if prefix != k.Prefix || len(prefix) != 8 || len(keySecret) != 43 {
		t.Fatalf("prefix=%q (%d) secret length=%d", prefix, len(prefix), len(keySecret))
	}
	if !h.Matches(k.Hash, keySecret) {
		t.Fatal("the stored hash must verify the secret")
	}
	if h.Matches(k.Hash, keySecret[:42]+"x") && keySecret[42] != 'x' {
		t.Fatal("a different secret must not verify")
	}
	if strings.Contains(k.Token.String(), keySecret) {
		t.Fatal("the key must not print its value")
	}
}

// A base64url secret can contain "_" and "-"; parsing must not be confused by that.
func TestParseKeyAcceptsUnderscoresInTheSecret(t *testing.T) {
	tok := token("fip", "Ab3dE6gH", strings.Repeat("_", 43))
	prefix, keySecret, err := ParseKey(tok)
	if err != nil || prefix != "Ab3dE6gH" || keySecret != strings.Repeat("_", 43) {
		t.Fatalf("got %q %q %v", prefix, keySecret, err)
	}
}

// token assembles a key-shaped string from its parts, so no complete key literal appears in the source.
func token(scheme, prefix, body string) string {
	return strings.Join([]string{scheme, prefix, body}, "_")
}

func TestParseKeyRejectsMalformedTokens(t *testing.T) {
	body := strings.Repeat("A", 43)
	good := token("fip", "Ab3dE6gH", body)
	cases := map[string]string{
		"empty":         "",
		"wrong scheme":  token("xyz", "Ab3dE6gH", body),
		"short prefix":  token("fip", "Ab3dE6g", body),
		"bad prefix":    token("fip", "Ab3dE6g!", body),
		"short body":    token("fip", "Ab3dE6gH", body[:42]),
		"long body":     good + "A",
		"bad body":      token("fip", "Ab3dE6gH", body[:42]+"+"),
		"no separators": "fipAb3dE6gH" + body,
	}
	if _, _, err := ParseKey(good); err != nil {
		t.Fatalf("control token must parse: %v", err)
	}
	for name, tok := range cases {
		if _, _, err := ParseKey(tok); !errors.Is(err, ErrMalformedKey) {
			t.Errorf("%s: err = %v, want ErrMalformedKey", name, err)
		}
	}
}

func TestPepperChangesTheHash(t *testing.T) {
	a := testHasher(t)
	b, _ := NewKeyHasher(secret.Secret(strings.Repeat("other-pepper-", 4)))
	if bytes.Equal(a.Hash("same"), b.Hash("same")) {
		t.Fatal("the pepper must change the hash")
	}
}

func TestGenerateReportsEntropyFailures(t *testing.T) {
	h := testHasher(t)
	if _, err := h.Generate(iotest.ErrReader(errors.New("boom"))); err == nil {
		t.Error("prefix randomness failure must be reported")
	}
	// Enough for the prefix (16 bytes) but not for the secret.
	if _, err := h.Generate(bytes.NewReader(bytes.Repeat([]byte{1}, 16))); err == nil {
		t.Error("secret randomness failure must be reported")
	}
}

func TestPrefixIsUnbiasedByRejectingHighBytes(t *testing.T) {
	// Bytes of 248 and above are discarded, so only the following valid bytes contribute.
	src := append(bytes.Repeat([]byte{255}, 16), bytes.Repeat([]byte{0}, 16)...)
	got, err := randomPrefix(bytes.NewReader(src))
	if err != nil || got != "AAAAAAAA" {
		t.Fatalf("got %q, %v", got, err)
	}
}
