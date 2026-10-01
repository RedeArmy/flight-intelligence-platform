// Package secret provides a string type that never reveals its value by accident.
//
// A Secret prints, marshals and logs as "[REDACTED]". The only way to read it is Reveal, which makes
// every use of the real value greppable and reviewable (SR-05, SR-09, ADR-018).
package secret

import "log/slog"

const redacted = "[REDACTED]"

// Secret is a sensitive string such as a credential, token or pepper.
type Secret string

// Redacted is the text that stands in for any secret value.
func Redacted() string { return redacted }

// String implements fmt.Stringer; it never returns the value.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer for %#v.
func (Secret) GoString() string { return "secret.Secret(" + redacted + ")" }

// MarshalText implements encoding.TextMarshaler.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// LogValue implements slog.LogValuer so structured logs never contain the value.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// Reveal returns the underlying value. Call it only at the point of use.
func (s Secret) Reveal() string { return string(s) }

// IsEmpty reports whether no value is set.
func (s Secret) IsEmpty() bool { return s == "" }
