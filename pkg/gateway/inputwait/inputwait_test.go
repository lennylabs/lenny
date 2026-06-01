// SPDX-License-Identifier: MIT

package inputwait_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
)

func TestRegisterAndResolve(t *testing.T) {
	r := inputwait.NewRegistry()
	ch, err := r.Register("sess-1", "req-1", nil)
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
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register("sess-1", "req-1", nil); !errors.Is(err, inputwait.ErrDuplicate) {
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
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
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

// TestCancelClosesChannelToUnblockWaiter verifies the Registry's
// cancellation contract: an external Cancel closes the channel the
// waiter is selecting on so the receive returns with ok=false. The
// handler treats that as a structured "cancelled" outcome rather than
// waiting for the §11.3 maxRequestInputWaitSeconds timeout.
func TestCancelClosesChannelToUnblockWaiter(t *testing.T) {
	r := inputwait.NewRegistry()
	ch, err := r.Register("sess-1", "req-1", nil)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Cancel("sess-1", "req-1")
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Cancel delivered an answer; want a closed channel (ok=false)")
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not close the channel; waiter is still blocked")
	}
}

// TestCancelIsIdempotent verifies a double Cancel does not panic on
// the close of an already-closed channel.
func TestCancelIsIdempotent(t *testing.T) {
	r := inputwait.NewRegistry()
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Cancel("sess-1", "req-1")
	// Must not panic.
	r.Cancel("sess-1", "req-1")
}

func TestRequestsAreKeyedBySessionAndID(t *testing.T) {
	r := inputwait.NewRegistry()
	// The same request id in two sessions is two distinct requests.
	chA, _ := r.Register("sess-a", "req-1", nil)
	chB, _ := r.Register("sess-b", "req-1", nil)

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
	_, _ = r.Register("sess-1", "req-a", nil)
	_, _ = r.Register("sess-1", "req-b", nil)
	_, _ = r.Register("sess-2", "req-x", nil)

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

// TestConsumedTracksRegisterAcrossResolveAndCancel exercises the §8.8
// line 869 per-session lifetime counter. Register bumps it; Resolve
// and Cancel do not decrement it. spec: §8.8 line 869. F-8.8.10.
func TestConsumedTracksRegisterAcrossResolveAndCancel_spec_8_8_869(t *testing.T) {
	r := inputwait.NewRegistry()
	if got := r.Consumed("sess-1"); got != 0 {
		t.Errorf("Consumed on empty registry = %d, want 0", got)
	}
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := r.Consumed("sess-1"); got != 1 {
		t.Errorf("Consumed after first Register = %d, want 1", got)
	}
	if err := r.Resolve("sess-1", "req-1", "x"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := r.Consumed("sess-1"); got != 1 {
		t.Errorf("Consumed after Resolve = %d, want 1 (lifetime counter does not decrement)", got)
	}
	if _, err := r.Register("sess-1", "req-2", nil); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if got := r.Consumed("sess-1"); got != 2 {
		t.Errorf("Consumed after second Register = %d, want 2", got)
	}
	r.Cancel("sess-1", "req-2")
	if got := r.Consumed("sess-1"); got != 2 {
		t.Errorf("Consumed after Cancel = %d, want 2 (lifetime counter does not decrement)", got)
	}
}

// TestConsumedIsPerSession verifies the §8.8 counter is partitioned by
// session id — one session's request_input rounds do not bleed into
// another's count. spec: §8.8 line 869. F-8.8.10.
func TestConsumedIsPerSession_spec_8_8_869(t *testing.T) {
	r := inputwait.NewRegistry()
	_, _ = r.Register("sess-a", "req-1", nil)
	_, _ = r.Register("sess-a", "req-2", nil)
	_, _ = r.Register("sess-b", "req-1", nil)
	if got := r.Consumed("sess-a"); got != 2 {
		t.Errorf("sess-a Consumed = %d, want 2", got)
	}
	if got := r.Consumed("sess-b"); got != 1 {
		t.Errorf("sess-b Consumed = %d, want 1", got)
	}
	if got := r.Consumed("sess-c"); got != 0 {
		t.Errorf("sess-c Consumed = %d, want 0", got)
	}
}

// TestForgetSessionClearsCounter is the §8.8 line 869 reclaim path the
// gateway calls on terminal transition. spec: §8.8 line 869. F-8.8.10.
func TestForgetSessionClearsCounter_spec_8_8_869(t *testing.T) {
	r := inputwait.NewRegistry()
	_, _ = r.Register("sess-1", "req-1", nil)
	r.ForgetSession("sess-1")
	if got := r.Consumed("sess-1"); got != 0 {
		t.Errorf("Consumed after ForgetSession = %d, want 0", got)
	}
	// A no-op for a session never tracked.
	r.ForgetSession("sess-unknown")
}

// TestRegisterDuplicateDoesNotBumpConsumed verifies that a duplicate
// Register (rejected with ErrDuplicate) does not double-count against
// the §8.8 lifetime counter. spec: §8.8 line 869. F-8.8.10.
func TestRegisterDuplicateDoesNotBumpConsumed_spec_8_8_869(t *testing.T) {
	r := inputwait.NewRegistry()
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := r.Register("sess-1", "req-1", nil); !errors.Is(err, inputwait.ErrDuplicate) {
		t.Fatalf("duplicate Register err = %v, want ErrDuplicate", err)
	}
	if got := r.Consumed("sess-1"); got != 1 {
		t.Errorf("Consumed after duplicate Register = %d, want 1", got)
	}
}

func TestResolveNeverBlocksWhenWaiterLeft(t *testing.T) {
	r := inputwait.NewRegistry()
	// Register but never read the channel — Resolve must not block on
	// the cap-1 buffer.
	if _, err := r.Register("sess-1", "req-1", nil); err != nil {
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

// TestPendingDetailsForSession verifies the §8.8 line 951
// lenny/await_children input_required surface: pending requests are
// returned with their question parts, sorted by request id, scoped to
// the session, and pruned on Resolve/Cancel. spec: §8.8 line 951.
// F-8.8.5.
func TestPendingDetailsForSession_spec_8_8_951(t *testing.T) {
	r := inputwait.NewRegistry()
	if got := r.PendingDetailsForSession("sess-1"); got != nil {
		t.Fatalf("PendingDetailsForSession on empty registry = %v, want nil", got)
	}
	partsB := []json.RawMessage{json.RawMessage(`{"type":"text","text":"q-b"}`)}
	// Register out of sorted order to prove the deterministic sort.
	if _, err := r.Register("sess-1", "req-b", partsB); err != nil {
		t.Fatalf("Register req-b: %v", err)
	}
	if _, err := r.Register("sess-1", "req-a", nil); err != nil {
		t.Fatalf("Register req-a: %v", err)
	}
	if _, err := r.Register("sess-2", "req-z", nil); err != nil {
		t.Fatalf("Register req-z: %v", err)
	}
	got := r.PendingDetailsForSession("sess-1")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (scoped to sess-1)", len(got))
	}
	if got[0].RequestID != "req-a" || got[1].RequestID != "req-b" {
		t.Errorf("order = %q,%q; want req-a,req-b (sorted by request id)", got[0].RequestID, got[1].RequestID)
	}
	if len(got[1].Parts) != 1 || string(got[1].Parts[0]) != `{"type":"text","text":"q-b"}` {
		t.Errorf("req-b parts = %v, want the registered question payload", got[1].Parts)
	}
	// Resolve prunes the entry from the partial surface.
	if err := r.Resolve("sess-1", "req-a", "ans"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := r.PendingDetailsForSession("sess-1"); len(got) != 1 || got[0].RequestID != "req-b" {
		t.Errorf("after Resolve = %v, want only req-b", got)
	}
	// Cancel prunes the remaining entry.
	r.Cancel("sess-1", "req-b")
	if got := r.PendingDetailsForSession("sess-1"); got != nil {
		t.Errorf("after Cancel = %v, want nil", got)
	}
}
