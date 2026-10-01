package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
)

var errSecretCause = stderrors.New("dial tcp: password=hunter2 rejected")

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindInternal: "internal", KindInvalid: "invalid", KindUnauthenticated: "unauthenticated",
		KindForbidden: "forbidden", KindNotFound: "not_found", KindConflict: "conflict",
		KindRateLimited: "rate_limited", KindUnavailable: "unavailable", KindTimeout: "timeout",
		Kind(200): "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestZeroKindIsInternal(t *testing.T) {
	if (&Error{}).Kind != KindInternal {
		t.Fatal("zero Kind must be KindInternal")
	}
}

func TestValidCode(t *testing.T) {
	valid := []string{"INTERNAL", "OFFER_NOT_FOUND", "A1", "RATE_LIMITED"}
	invalid := []string{"", "a", "lower_case", "X", "1ABC", "HAS SPACE", "WITH-DASH", strings.Repeat("A", 65)}
	for _, c := range valid {
		if !ValidCode(c) {
			t.Errorf("ValidCode(%q) = false, want true", c)
		}
	}
	for _, c := range invalid {
		if ValidCode(c) {
			t.Errorf("ValidCode(%q) = true, want false", c)
		}
	}
}

func TestGenericCodesAreValid(t *testing.T) {
	for _, c := range []string{
		CodeInternal, CodeInvalidRequest, CodeUnauthenticated, CodeForbidden, CodeNotFound,
		CodeConflict, CodeRateLimited, CodeUnavailable, CodeTimeout, CodeCanceled,
	} {
		if !ValidCode(c) {
			t.Errorf("generic code %q is not valid", c)
		}
	}
}

func TestNewPanicsOnInvalidCode(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an invalid code")
		}
	}()
	_ = New(KindInvalid, "not valid", "x")
}

func TestConstructorsSetKind(t *testing.T) {
	cases := map[Kind]*Error{
		KindInternal:        Internal("E_INTERNAL", "m"),
		KindInvalid:         Invalid("E_INVALID", "m"),
		KindUnauthenticated: Unauthenticated("E_UNAUTH", "m"),
		KindForbidden:       Forbidden("E_FORBIDDEN", "m"),
		KindNotFound:        NotFound("E_NOTFOUND", "m"),
		KindConflict:        Conflict("E_CONFLICT", "m"),
		KindRateLimited:     RateLimited("E_RATE", "m"),
		KindUnavailable:     Unavailable("E_UNAVAIL", "m"),
		KindTimeout:         Timeout("E_TIMEOUT", "m"),
	}
	for want, e := range cases {
		if e.Kind != want {
			t.Errorf("%s: Kind = %v, want %v", e.Code, e.Kind, want)
		}
	}
}

func TestErrorStringIncludesCauseButPublicMessageDoesNot(t *testing.T) {
	e := Wrap(KindUnavailable, "DB_DOWN", "service unavailable", errSecretCause)
	if !strings.Contains(e.Error(), "hunter2") {
		t.Fatalf("Error() should include the cause for logs, got %q", e.Error())
	}
	if got := PublicMessage(e); got != "service unavailable" || strings.Contains(got, "hunter2") {
		t.Fatalf("PublicMessage leaked or changed: %q", got)
	}
}

func TestUnwrapAndIs(t *testing.T) {
	sentinel := NotFound("OFFER_NOT_FOUND", "offer not found")
	derived := sentinel.WithDetails(map[string]any{"offerId": "off_1"}).WithCause(errSecretCause)

	if !stderrors.Is(derived, sentinel) {
		t.Error("derived error must match its sentinel with errors.Is")
	}
	if !stderrors.Is(derived, errSecretCause) {
		t.Error("errors.Is must reach the cause")
	}
	other := NotFound("ROUTE_NOT_FOUND", "route not found")
	if stderrors.Is(derived, other) {
		t.Error("different codes must not match")
	}
	if stderrors.Is(derived, Conflict("OFFER_NOT_FOUND", "x")) {
		t.Error("same code with a different kind must not match")
	}
	wrapped := fmt.Errorf("handler: %w", derived)
	if KindOf(wrapped) != KindNotFound || CodeOf(wrapped) != "OFFER_NOT_FOUND" {
		t.Errorf("classification lost through fmt.Errorf wrapping: %v %v", KindOf(wrapped), CodeOf(wrapped))
	}
}

func TestWithMethodsDoNotMutateOriginal(t *testing.T) {
	base := Invalid("BAD_INPUT", "bad input").WithDetails(map[string]any{"a": 1})
	derived := base.WithDetails(map[string]any{"b": 2}).WithMessage("changed")
	if len(base.Details) != 1 || base.Message != "bad input" || base.Cause != nil {
		t.Fatalf("original mutated: %+v", base)
	}
	if len(derived.Details) != 2 || derived.Message != "changed" {
		t.Fatalf("derived wrong: %+v", derived)
	}
	derived.Details["c"] = 3
	if _, ok := base.Details["c"]; ok {
		t.Fatal("details map is shared between copies")
	}
}

func TestClassificationOfPlainAndContextErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind Kind
		code string
		msg  string
	}{
		{"nil", nil, KindInternal, CodeInternal, "internal error"},
		{"plain", errSecretCause, KindInternal, CodeInternal, "internal error"},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), KindTimeout, CodeTimeout, "request timed out"},
		{"canceled", context.Canceled, KindUnavailable, CodeCanceled, "request canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KindOf(tc.err); got != tc.kind {
				t.Errorf("KindOf = %v, want %v", got, tc.kind)
			}
			if got := CodeOf(tc.err); got != tc.code {
				t.Errorf("CodeOf = %q, want %q", got, tc.code)
			}
			if got := PublicMessage(tc.err); got != tc.msg {
				t.Errorf("PublicMessage = %q, want %q", got, tc.msg)
			}
		})
	}
}

func TestInternalKindNeverLeaksMessageOrDetails(t *testing.T) {
	e := Internal("DB_FAILURE", "connection string postgres://u:p@h/db").
		WithDetails(map[string]any{"dsn": "postgres://u:p@h/db"})
	if got := PublicMessage(e); got != "internal error" {
		t.Errorf("PublicMessage = %q", got)
	}
	if DetailsOf(e) != nil {
		t.Error("DetailsOf must hide details of internal errors")
	}
	if CodeOf(e) != "DB_FAILURE" {
		t.Errorf("code should still be available for logs and metrics, got %q", CodeOf(e))
	}
}

func TestDetailsOfReturnsACopy(t *testing.T) {
	e := Invalid("BAD", "bad").WithDetails(map[string]any{"field": "origin"})
	d := DetailsOf(e)
	d["field"] = "tampered"
	if e.Details["field"] != "origin" {
		t.Fatal("DetailsOf must return a copy")
	}
}

func TestNilReceiverSafety(t *testing.T) {
	var e *Error
	if e.Error() != "<nil>" || e.Unwrap() != nil {
		t.Fatal("nil *Error must be safe")
	}
	if e.Is(Invalid("BAD", "x")) {
		t.Fatal("nil receiver must not match")
	}
}
