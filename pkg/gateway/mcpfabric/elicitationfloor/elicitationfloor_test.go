// SPDX-License-Identifier: MIT

package elicitationfloor

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestProviderSeedAndFloor(t *testing.T) {
	// spec: §17.2 line 86 — the startup seed (flag value) is the floor
	// until a ConfigMap reconcile supplies a value.
	if got := NewProvider("enforce").Floor(); got != "enforce" {
		t.Fatalf("Floor() = %q, want enforce", got)
	}
	if got := NewProvider("").Floor(); got != "" {
		t.Fatalf("empty seed Floor() = %q, want empty", got)
	}
}

func TestProviderSetValidChange(t *testing.T) {
	// spec: §17.2 line 86 — a valid floor change is adopted.
	p := NewProvider("off")
	prev, changed := p.Set("enforce")
	if !changed || prev != "off" {
		t.Fatalf("Set(enforce) = (%q, %v), want (off, true)", prev, changed)
	}
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q, want enforce", p.Floor())
	}
}

func TestProviderSetSameValueNoChange(t *testing.T) {
	p := NewProvider("detect-only")
	if _, changed := p.Set("detect-only"); changed {
		t.Fatalf("Set(detect-only) reported a change for an unchanged value")
	}
}

func TestProviderSetInvalidIgnored(t *testing.T) {
	// spec: §17.2 line 86 — a malformed ConfigMap value must never
	// weaken or corrupt the in-force floor.
	p := NewProvider("enforce")
	prev, changed := p.Set("bogus")
	if changed {
		t.Fatalf("Set(bogus) reported a change")
	}
	if prev != "enforce" || p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q after invalid Set, want enforce", p.Floor())
	}
}

func TestProviderConcurrentAccess(t *testing.T) {
	// The Provider is read by the per-request resolver while the
	// reconciler writes; the race detector must stay quiet.
	p := NewProvider("off")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); p.Set("enforce") }()
		go func() { defer wg.Done(); _ = p.Floor() }()
	}
	wg.Wait()
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q, want enforce", p.Floor())
	}
}

// stubReader is a scripted FloorReader.
type stubReader struct {
	value   string
	present bool
	err     error
	calls   int
}

func (s *stubReader) ReadFloor(context.Context) (string, bool, error) {
	s.calls++
	return s.value, s.present, s.err
}

func TestReconcileOnceAdoptsPresentValue(t *testing.T) {
	// spec: §17.2 line 86 — startup read seeds the floor from the
	// ConfigMap, raising the flag-default off to enforce.
	p := NewProvider("off")
	var changes [][2]string
	r := &Reconciler{
		Reader:   &stubReader{value: "enforce", present: true},
		Provider: p,
		OnChange: func(prev, cur string) { changes = append(changes, [2]string{prev, cur}) },
	}
	r.reconcileOnce(context.Background())
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q, want enforce", p.Floor())
	}
	if len(changes) != 1 || changes[0] != [2]string{"off", "enforce"} {
		t.Fatalf("OnChange = %v, want one (off,enforce)", changes)
	}
}

func TestReconcileOnceRetainsOnError(t *testing.T) {
	// A transient read error retains the last-known floor (no weakening).
	p := NewProvider("enforce")
	var changed bool
	r := &Reconciler{
		Reader:   &stubReader{err: errors.New("apiserver down")},
		Provider: p,
		OnChange: func(string, string) { changed = true },
	}
	r.reconcileOnce(context.Background())
	if p.Floor() != "enforce" || changed {
		t.Fatalf("Floor() = %q changed=%v after read error, want enforce/false", p.Floor(), changed)
	}
}

func TestReconcileOnceRetainsOnAbsentKey(t *testing.T) {
	// spec: §17.2 line 86 — an absent/empty floor key (legacy or
	// hand-edited ConfigMap) retains the last-known floor rather than
	// weakening to a default.
	p := NewProvider("enforce")
	r := &Reconciler{
		Reader:   &stubReader{value: "", present: false},
		Provider: p,
	}
	r.reconcileOnce(context.Background())
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q after absent key, want enforce", p.Floor())
	}
}

func TestReconcileOnceIgnoresInvalidValue(t *testing.T) {
	// A present-but-invalid floor value is ignored (retain + log).
	p := NewProvider("enforce")
	r := &Reconciler{
		Reader:   &stubReader{value: "loud", present: true},
		Provider: p,
	}
	r.reconcileOnce(context.Background())
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q after invalid value, want enforce", p.Floor())
	}
}

func TestReconcileOnceLowersFloor(t *testing.T) {
	// spec: §17.2 line 86 — lowering the floor is a legitimate operator
	// posture change (no downgrade guard on the floor key).
	p := NewProvider("enforce")
	r := &Reconciler{
		Reader:   &stubReader{value: "detect-only", present: true},
		Provider: p,
	}
	r.reconcileOnce(context.Background())
	if p.Floor() != "detect-only" {
		t.Fatalf("Floor() = %q, want detect-only", p.Floor())
	}
}

func TestRunNoOpWithoutSeams(t *testing.T) {
	// A reconciler missing a required seam returns immediately.
	(&Reconciler{}).Run(context.Background())
	(&Reconciler{Reader: &stubReader{}}).Run(context.Background())
}

func TestRunFirstReadImmediate(t *testing.T) {
	// Run fires the first reconcile immediately then exits on ctx cancel
	// before the ticker elapses, so the cold-start floor is in force at
	// once.
	p := NewProvider("off")
	rd := &stubReader{value: "enforce", present: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run so only the immediate read fires
	(&Reconciler{Reader: rd, Provider: p}).Run(ctx)
	if rd.calls != 1 {
		t.Fatalf("ReadFloor calls = %d, want 1 (immediate)", rd.calls)
	}
	if p.Floor() != "enforce" {
		t.Fatalf("Floor() = %q, want enforce", p.Floor())
	}
}
