package id

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"testing/iotest"
	"time"
)

var v7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewV7HasVersionVariantAndTimestamp(t *testing.T) {
	now := time.UnixMilli(0x0123456789ab)
	got, err := NewV7(now, bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if !v7.MatchString(got) {
		t.Fatalf("%q is not a UUIDv7", got)
	}
	if got[:13] != "01234567-89ab" {
		t.Fatalf("timestamp prefix = %q", got[:13])
	}
}

func TestNewV7SortsByTime(t *testing.T) {
	zero := bytes.NewReader(make([]byte, 20))
	a, _ := NewV7(time.UnixMilli(1000), zero)
	b, _ := NewV7(time.UnixMilli(2000), zero)
	if a >= b {
		t.Fatalf("%s should sort before %s", a, b)
	}
}

func TestNewV7ReportsEntropyFailure(t *testing.T) {
	boom := errors.New("boom")
	if _, err := NewV7(time.Now(), iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the reader error", err)
	}
}
