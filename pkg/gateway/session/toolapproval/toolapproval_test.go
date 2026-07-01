// SPDX-License-Identifier: MIT

package toolapproval

import (
	"errors"
	"testing"
)

// TestRegistryRegisterResolve_spec_7_2 covers the happy path: a
// registered waiter receives the verdict delivered by Resolve.
func TestRegistryRegisterResolve_spec_7_2(t *testing.T) {
	r := NewRegistry()
	ch, err := r.Register("sess-1", "tc-1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Pending("sess-1", "tc-1") {
		t.Fatal("Pending = false after Register, want true")
	}
	if err := r.Resolve("sess-1", "tc-1", Decision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	d, ok := <-ch
	if !ok {
		t.Fatal("channel closed, want a delivered decision")
	}
	if !d.Approved {
		t.Errorf("decision Approved = false, want true")
	}
	if r.Pending("sess-1", "tc-1") {
		t.Error("Pending = true after Resolve, want false")
	}
}

// TestRegistryResolveDeny_spec_7_2 covers the §7.2 line 125 deny verdict
// carrying a reason.
func TestRegistryResolveDeny_spec_7_2(t *testing.T) {
	r := NewRegistry()
	ch, _ := r.Register("sess-1", "tc-1")
	if err := r.Resolve("sess-1", "tc-1", Decision{Approved: false, Reason: "policy"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	d := <-ch
	if d.Approved {
		t.Error("decision Approved = true, want false (deny)")
	}
	if d.Reason != "policy" {
		t.Errorf("decision Reason = %q, want %q", d.Reason, "policy")
	}
}

// TestRegistryResolveBufferedBeforeReceive_spec_7_2 verifies a verdict
// that races ahead of the waiter's receive is buffered, not lost — the
// approval-gate ordering (Register before the interaction becomes
// resolvable) relies on the cap-1 channel.
func TestRegistryResolveBufferedBeforeReceive_spec_7_2(t *testing.T) {
	r := NewRegistry()
	ch, _ := r.Register("sess-1", "tc-1")
	// Resolve before anyone is selecting on ch.
	if err := r.Resolve("sess-1", "tc-1", Decision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	d := <-ch
	if !d.Approved {
		t.Error("buffered decision lost; Approved = false")
	}
}

// TestRegistryDuplicateRegister_spec_7_2 rejects a second waiter for the
// same (session, tool call).
func TestRegistryDuplicateRegister_spec_7_2(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Register("sess-1", "tc-1"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register("sess-1", "tc-1"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("second Register err = %v, want ErrDuplicate", err)
	}
}

// TestRegistryResolveUnknown_spec_7_2 returns ErrNotFound when no waiter
// is registered — the resolution endpoint ignores it (the interaction
// phase is the authoritative record).
func TestRegistryResolveUnknown_spec_7_2(t *testing.T) {
	r := NewRegistry()
	if err := r.Resolve("sess-1", "absent", Decision{Approved: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve unknown err = %v, want ErrNotFound", err)
	}
}

// TestRegistryCancelClosesChannel_spec_7_2 covers a Cancel: the waiter
// unblocks with ok=false so the executor can treat it as an implicit
// denial.
func TestRegistryCancelClosesChannel_spec_7_2(t *testing.T) {
	r := NewRegistry()
	ch, _ := r.Register("sess-1", "tc-1")
	r.Cancel("sess-1", "tc-1")
	if _, ok := <-ch; ok {
		t.Error("channel delivered a value after Cancel, want closed (ok=false)")
	}
	if r.Pending("sess-1", "tc-1") {
		t.Error("Pending = true after Cancel, want false")
	}
	// Idempotent: a second Cancel is a no-op.
	r.Cancel("sess-1", "tc-1")
}

// TestRegistryDistinctKeys_spec_7_2 verifies sessions and tool-call ids
// are namespaced so a resolve for one does not wake another.
func TestRegistryDistinctKeys_spec_7_2(t *testing.T) {
	r := NewRegistry()
	chA, _ := r.Register("sess-1", "tc-1")
	chB, _ := r.Register("sess-2", "tc-1")
	r.Resolve("sess-2", "tc-1", Decision{Approved: true})
	select {
	case <-chA:
		t.Error("waiter for sess-1 woke on a sess-2 resolve")
	default:
	}
	if d := <-chB; !d.Approved {
		t.Error("sess-2 waiter did not receive its verdict")
	}
}
