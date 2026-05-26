// SPDX-License-Identifier: MIT

package inputwait_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
)

func TestRegisterAndResolve(t *testing.T) {
	r := inputwait.NewRegistry()
	ch, err := r.Register("sess-1", "req-1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Pending("sess-1", "req-1") {
		t.Error("Pending = false after Register, want true")
	}
	if err := r.Resolve("sess-1", "req-1", "the answer"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case got := <-ch:
		if got != "the answer" {
			t.Errorf("answer = %q, want %q", got, "the answer")
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve did not deliver the answer to the waiter")
	}
	if r.Pending("sess-1", "req-1") {
		t.Error("Pending = true after Resolve, want false")
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	r := inputwait.NewRegistry()
	if _, err := r.Register("sess-1", "req-1"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register("sess-1", "req-1"); !errors.Is(err, inputwait.ErrDuplicate) {
		t.Errorf("duplicate Register err = %v, want ErrDuplicate", err)
	}
}

func TestResolveUnknownReturnsNotFound(t *testing.T) {
	r := inputwait.NewRegistry()
	if err := r.Resolve("sess-1", "req-absent", "x"); !errors.Is(err, inputwait.ErrNotFound) {
		t.Errorf("Resolve of an unknown request err = %v, want ErrNotFound", err)
	}
}

func TestCancelRemovesPending(t *testing.T) {
	r := inputwait.NewRegistry()
	if _, err := r.Register("sess-1", "req-1"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Cancel("sess-1", "req-1")
	if r.Pending("sess-1", "req-1") {
		t.Error("Pending = true after Cancel, want false")
	}
	// A resolve after cancel finds nothing.
	if err := r.Resolve("sess-1", "req-1", "x"); !errors.Is(err, inputwait.ErrNotFound) {
		t.Errorf("Resolve after Cancel err = %v, want ErrNotFound", err)
	}
	// Cancel of an unknown request is a no-op.
	r.Cancel("sess-1", "req-absent")
}

func TestRequestsAreKeyedBySessionAndID(t *testing.T) {
	r := inputwait.NewRegistry()
	// The same request id in two sessions is two distinct requests.
	chA, _ := r.Register("sess-a", "req-1")
	chB, _ := r.Register("sess-b", "req-1")

	if err := r.Resolve("sess-a", "req-1", "for-a"); err != nil {
		t.Fatalf("Resolve sess-a: %v", err)
	}
	if got := <-chA; got != "for-a" {
		t.Errorf("sess-a answer = %q, want for-a", got)
	}
	if !r.Pending("sess-b", "req-1") {
		t.Error("resolving sess-a also cleared sess-b's request")
	}
	if err := r.Resolve("sess-b", "req-1", "for-b"); err != nil {
		t.Fatalf("Resolve sess-b: %v", err)
	}
	if got := <-chB; got != "for-b" {
		t.Errorf("sess-b answer = %q, want for-b", got)
	}
}

// TestPendingForSessionListsRegistrations covers the lookup used by
// the §7.2 children_reattached emitter to surface a child's pending
// request id back to the resumed parent.
// spec: §7.2 line 153; F-7.2.16.
func TestPendingForSessionListsRegistrations(t *testing.T) {
	r := inputwait.NewRegistry()
	if got := r.PendingForSession("sess-empty"); len(got) != 0 {
		t.Errorf("PendingForSession on empty registry = %v, want nil", got)
	}
	_, _ = r.Register("sess-1", "req-a")
	_, _ = r.Register("sess-1", "req-b")
	_, _ = r.Register("sess-2", "req-x")

	got := r.PendingForSession("sess-1")
	if len(got) != 2 {
		t.Fatalf("sess-1 pending count = %d, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen["req-a"] || !seen["req-b"] {
		t.Errorf("sess-1 pending = %v, want both req-a and req-b", got)
	}
	// Session id is matched exactly (the NUL separator prevents
	// session-id prefix collisions).
	if other := r.PendingForSession("sess"); len(other) != 0 {
		t.Errorf("sess (prefix) returned %v, want nil — session ids are exact-match", other)
	}
}

func TestResolveNeverBlocksWhenWaiterLeft(t *testing.T) {
	r := inputwait.NewRegistry()
	// Register but never read the channel — Resolve must not block on
	// the cap-1 buffer.
	if _, err := r.Register("sess-1", "req-1"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Resolve("sess-1", "req-1", "x") }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Resolve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve blocked when the waiter was not reading")
	}
}
