package clock

import (
	"sync"
	"testing"
	"time"
)

func TestSystemReturnsUTC(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := System{}.Now()
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
	if got.Before(before) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("System.Now() = %v is not close to the wall clock", got)
	}
}

func TestFakeAdvanceAndSet(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("X", 3600))
	f := NewFake(start)
	if !f.Now().Equal(start) || f.Now().Location() != time.UTC {
		t.Fatalf("NewFake did not normalise to UTC: %v", f.Now())
	}
	f.Advance(90 * time.Minute)
	if want := start.Add(90 * time.Minute); !f.Now().Equal(want) {
		t.Fatalf("after Advance: %v, want %v", f.Now(), want)
	}
	f.Advance(-30 * time.Minute)
	if want := start.Add(time.Hour); !f.Now().Equal(want) {
		t.Fatalf("after negative Advance: %v, want %v", f.Now(), want)
	}
	f.Set(start)
	if !f.Now().Equal(start) {
		t.Fatalf("after Set: %v", f.Now())
	}
}

func TestFakeIsSafeForConcurrentUse(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Advance(time.Second)
			_ = f.Now()
		}()
	}
	wg.Wait()
	if want := time.Unix(50, 0).UTC(); !f.Now().Equal(want) {
		t.Fatalf("final time %v, want %v", f.Now(), want)
	}
}

var _ Clock = System{}
var _ Clock = (*Fake)(nil)
