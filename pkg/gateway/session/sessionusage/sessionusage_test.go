// SPDX-License-Identifier: MIT

package sessionusage_test

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
)

// TestMemoryAddAccumulates_spec_8_8_897 confirms the §8.8 per-session
// accumulator sums repeated proxy-extracted counts into the session's
// running totals.
func TestMemoryAddAccumulates_spec_8_8_897(t *testing.T) {
	m := sessionusage.NewMemory()
	ctx := context.Background()
	if err := m.Add(ctx, "acme", "sess1", 1000, 400); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Add(ctx, "acme", "sess1", 500, 100); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := m.Get(ctx, "acme", "sess1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Input != 1500 || got.Output != 500 {
		t.Fatalf("got %+v, want {Input:1500 Output:500}", got)
	}
}

// TestMemoryGetUnknownIsZero confirms a session with no recorded usage
// returns the zero Tokens and a nil error (not an error).
func TestMemoryGetUnknownIsZero(t *testing.T) {
	m := sessionusage.NewMemory()
	got, err := m.Get(context.Background(), "acme", "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != (sessionusage.Tokens{}) {
		t.Fatalf("got %+v, want zero", got)
	}
}

// TestMemoryTenantIsolation confirms two tenants' counts under the same
// session id never commingle.
func TestMemoryTenantIsolation(t *testing.T) {
	m := sessionusage.NewMemory()
	ctx := context.Background()
	_ = m.Add(ctx, "acme", "s", 100, 10)
	_ = m.Add(ctx, "globex", "s", 7, 3)
	a, _ := m.Get(ctx, "acme", "s")
	g, _ := m.Get(ctx, "globex", "s")
	if a.Input != 100 || a.Output != 10 {
		t.Fatalf("acme got %+v", a)
	}
	if g.Input != 7 || g.Output != 3 {
		t.Fatalf("globex got %+v", g)
	}
}

// TestMemoryAddIgnoresNonPositiveAndEmpty confirms negative deltas clamp
// to zero, a zero/zero call is a no-op, and an empty tenant or session id
// is dropped rather than keyed on the empty string.
func TestMemoryAddIgnoresNonPositiveAndEmpty(t *testing.T) {
	m := sessionusage.NewMemory()
	ctx := context.Background()
	if err := m.Add(ctx, "acme", "s", -50, -3); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Add(ctx, "acme", "s", 0, 0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Add(ctx, "", "s", 10, 10); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Add(ctx, "acme", "", 10, 10); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, _ := m.Get(ctx, "acme", "s")
	if got != (sessionusage.Tokens{}) {
		t.Fatalf("got %+v, want zero (all writes dropped)", got)
	}
	// A mixed call records only the non-negative dimension.
	_ = m.Add(ctx, "acme", "s", 20, -5)
	got, _ = m.Get(ctx, "acme", "s")
	if got.Input != 20 || got.Output != 0 {
		t.Fatalf("got %+v, want {Input:20 Output:0}", got)
	}
}

// TestMemoryGetMany returns only the sessions that have recorded usage,
// keyed by session id; sessions with no usage are absent.
func TestMemoryGetMany(t *testing.T) {
	m := sessionusage.NewMemory()
	ctx := context.Background()
	_ = m.Add(ctx, "acme", "a", 10, 5)
	_ = m.Add(ctx, "acme", "b", 20, 8)
	out, err := m.GetMany(ctx, "acme", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (c has no usage)", len(out))
	}
	if out["a"].Input != 10 || out["b"].Output != 8 {
		t.Fatalf("got %+v", out)
	}
	if _, ok := out["c"]; ok {
		t.Fatalf("c should be absent")
	}
}

// TestMemoryAddConcurrent confirms concurrent adds do not lose updates —
// the proxy records from many request goroutines at once.
func TestMemoryAddConcurrent(t *testing.T) {
	m := sessionusage.NewMemory()
	ctx := context.Background()
	const goroutines = 16
	const per = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				_ = m.Add(ctx, "acme", "s", 1, 1)
			}
		}()
	}
	wg.Wait()
	got, _ := m.Get(ctx, "acme", "s")
	want := int64(goroutines * per)
	if got.Input != want || got.Output != want {
		t.Fatalf("got %+v, want {Input:%d Output:%d}", got, want, want)
	}
}
