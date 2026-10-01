package secret

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const value = "s3cr3t-value-123"

func TestSecretNeverRevealsValue(t *testing.T) {
	s := Secret(value)
	outputs := map[string]string{
		"String":   s.String(),
		"%v":       fmt.Sprintf("%v", s),
		"%+v":      fmt.Sprintf("%+v", s),
		"%s":       fmt.Sprintf("<%s>", s),
		"%q":       fmt.Sprintf("%q", s),
		"%x":       fmt.Sprintf("%x", s),
		"%#v":      fmt.Sprintf("%#v", s),
		"struct":   fmt.Sprintf("%+v", struct{ Key Secret }{s}),
		"slice":    fmt.Sprintf("%v", []Secret{s}),
		"LogValue": s.LogValue().String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, value) {
			t.Errorf("%s leaked the value: %q", name, out)
		}
	}
}

func TestSecretMarshalling(t *testing.T) {
	b, err := json.Marshal(struct {
		Pepper Secret `json:"pepper"`
	}{Secret(value)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), value) || !strings.Contains(string(b), redacted) {
		t.Fatalf("json output not redacted: %s", b)
	}
	txt, err := Secret(value).MarshalText()
	if err != nil || string(txt) != redacted {
		t.Fatalf("MarshalText = %q, %v", txt, err)
	}
}

func TestSecretRevealAndEmpty(t *testing.T) {
	if got := Secret(value).Reveal(); got != value {
		t.Fatalf("Reveal = %q", got)
	}
	if !Secret("").IsEmpty() || Secret(value).IsEmpty() {
		t.Fatal("IsEmpty misreports")
	}
}

func TestSecretIsResolvedByLogValuer(t *testing.T) {
	v := slog.AnyValue(Secret(value)).Resolve()
	if v.String() != redacted {
		t.Fatalf("resolved log value = %q", v.String())
	}
}
