// SPDX-License-Identifier: MIT

package statusdedup

import (
	"testing"
	"time"
)

// spec: §4.6.1 statusUpdateDeduplicationWindow — a write within the
// window of the previous write for the same resource is deferred; the
// first write and any write after the window are permitted.
func TestGateAllowsFirstWriteThenDefersWithinWindow(t *testing.T) {
	base := time.Unix(1000, 0)
	clock := base
	g := New(500 * time.Millisecond)
	g.now = func() time.Time { return clock }

	if ok, rem := g.Allow("Sandbox/ns/a"); !ok || rem != 0 {
		t.Fatalf("first write: Allow = (%v, %v), want (true, 0)", ok, rem)
	}

	// 200ms later: still within the 500ms window, so the write is deferred
	// with the remaining 300ms.
	clock = base.Add(200 * time.Millisecond)
	ok, rem := g.Allow("Sandbox/ns/a")
	if ok {
		t.Fatalf("write within window: Allow ok = true, want deferred")
	}
	if rem != 300*time.Millisecond {
		t.Errorf("remaining = %v, want 300ms", rem)
	}
}

// spec: §4.6.1 — once the window expires the trailing write is permitted.
func TestGateAllowsAfterWindowExpires(t *testing.T) {
	base := time.Unix(1000, 0)
	clock := base
	g := New(500 * time.Millisecond)
	g.now = func() time.Time { return clock }

	g.Allow("Sandbox/ns/a")
	clock = base.Add(500 * time.Millisecond)
	if ok, rem := g.Allow("Sandbox/ns/a"); !ok || rem != 0 {
		t.Errorf("write at window boundary: Allow = (%v, %v), want (true, 0)", ok, rem)
	}
}

// Distinct resources have independent windows.
func TestGateKeysAreIndependent(t *testing.T) {
	g := New(500 * time.Millisecond)
	g.Allow("Sandbox/ns/a")
	if ok, _ := g.Allow("Sandbox/ns/b"); !ok {
		t.Error("a second resource must not be gated by the first resource's write")
	}
}

// A nil gate and a non-positive window both disable deduplication.
func TestNilGateAndZeroWindowAlwaysAllow(t *testing.T) {
	var nilGate *Gate
	if ok, rem := nilGate.Allow("k"); !ok || rem != 0 {
		t.Errorf("nil gate: Allow = (%v, %v), want (true, 0)", ok, rem)
	}
	zero := New(0)
	if ok, _ := zero.Allow("k"); !ok {
		t.Error("zero-window gate must always allow")
	}
	zero.Allow("k")
	if ok, _ := zero.Allow("k"); !ok {
		t.Error("zero-window gate must never defer")
	}
}

// Forget drops the last-write record so a recreated resource is not gated
// by its prior incarnation.
func TestForgetResetsWindow(t *testing.T) {
	base := time.Unix(1000, 0)
	clock := base
	g := New(500 * time.Millisecond)
	g.now = func() time.Time { return clock }

	g.Allow("Sandbox/ns/a")
	g.Forget("Sandbox/ns/a")
	clock = base.Add(10 * time.Millisecond)
	if ok, _ := g.Allow("Sandbox/ns/a"); !ok {
		t.Error("after Forget the next write must be permitted immediately")
	}
}
