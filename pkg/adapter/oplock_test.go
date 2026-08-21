// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// waitQueued blocks until an operation has entered the lock's pending
// set, so a test can act while exactly one operation is running and one
// is waiting.
func waitQueued(t *testing.T, l *opLock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		q := l.interruptPending || len(l.checkpoints) > 0
		l.mu.Unlock()
		if q {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not enter the pending set")
}

// waitPendingCheckpoints blocks until at least n distinct-slot
// checkpoints are pending, so an ordering test can queue several waiters
// before releasing the running operation.
func waitPendingCheckpoints(t *testing.T, l *opLock, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		got := len(l.checkpoints)
		l.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fewer than %d checkpoints entered the pending set", n)
}

// waitPendingSession blocks until a checkpoint for sessionID is pending.
func waitPendingSession(t *testing.T, l *opLock, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		_, ok := l.checkpoints[sessionID]
		l.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("checkpoint for session %q did not enter the pending set", sessionID)
}

func TestOpKindString(t *testing.T) {
	if opCheckpoint.String() != "checkpoint" {
		t.Errorf("opCheckpoint.String() = %q, want checkpoint", opCheckpoint.String())
	}
	if opInterrupt.String() != "interrupt" {
		t.Errorf("opInterrupt.String() = %q, want interrupt", opInterrupt.String())
	}
}

func TestOpLockIdleAcquireImmediate(t *testing.T) {
	var l opLock
	rel, err := l.Begin(context.Background(), opCheckpoint, "")
	if err != nil {
		t.Fatalf("Begin on idle lock: %v", err)
	}
	rel()
	// After release the lock is idle and acquirable again.
	rel2, err := l.Begin(context.Background(), opInterrupt, "")
	if err != nil {
		t.Fatalf("Begin after release: %v", err)
	}
	rel2()
}

func TestOpLockSerializesAndPromotes(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "")
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}

	ran := make(chan opKind, 1)
	go func() {
		rel2, err := l.Begin(context.Background(), opInterrupt, "")
		if err != nil {
			t.Errorf("queued Begin interrupt: %v", err)
			return
		}
		ran <- opInterrupt
		rel2()
	}()

	waitQueued(t, &l)
	// The queued interrupt must not run while the checkpoint holds the lock.
	select {
	case <-ran:
		t.Fatal("queued interrupt ran before the checkpoint released")
	case <-time.After(50 * time.Millisecond):
	}

	rel1()
	select {
	case k := <-ran:
		if k != opInterrupt {
			t.Errorf("promoted operation = %v, want interrupt", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued interrupt did not run after release")
	}
}

// promotionOrder queues one checkpoint per session identifier in queued
// order behind a running checkpoint, releases it, and returns the order
// the lock promoted the queued checkpoints in.
func promotionOrder(t *testing.T, queued []string) []string {
	t.Helper()
	var l opLock
	rel, err := l.Begin(context.Background(), opCheckpoint, "1f0a2c8e-running")
	if err != nil {
		t.Fatalf("Begin running checkpoint: %v", err)
	}
	promoted := make(chan string, len(queued))
	for _, session := range queued {
		session := session
		go func() {
			rel2, err := l.Begin(context.Background(), opCheckpoint, session)
			if err != nil {
				t.Errorf("queued Begin for %q: %v", session, err)
				return
			}
			promoted <- session
			rel2()
		}()
	}
	waitPendingCheckpoints(t, &l, len(queued))

	// Release the running checkpoint; promotions cascade one at a time.
	rel()

	var order []string
	for range queued {
		select {
		case session := <-promoted:
			order = append(order, session)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d checkpoints promoted", len(order), len(queued))
		}
	}
	return order
}

// TestOpLockPromotesCheckpointsInSessionIdentifierOrder pins the
// promotion rule: the lock holds one pending checkpoint per distinct
// session identifier and promotes the lowest identifier next. Session
// identifiers are opaque, so the order is a lexicographic tie-break
// chosen so the promotion pick is a pure function of the pending set
// rather than of the pending map's iteration order or of the order the
// checkpoints arrived in. The test asserts that property directly: three
// different arrival orders over the same opaque identifiers all promote
// in the same lexicographic order, and none of the identifiers carries an
// ordinal the pick could be reading instead.
//
// spec: 5.2 (one session's checkpoint upload at a time, in the
// lexicographic tie-break over session identifiers), 4.7
// (Checkpoint/Interrupt mutual exclusion, one pending checkpoint per
// distinct session identifier)
func TestOpLockPromotesCheckpointsInSessionIdentifierOrder(t *testing.T) {
	want := []string{"3b7d19f4-carol", "9c02ae55-alice", "e41fbb70-bob"}
	arrivals := [][]string{
		{"9c02ae55-alice", "e41fbb70-bob", "3b7d19f4-carol"},
		{"e41fbb70-bob", "3b7d19f4-carol", "9c02ae55-alice"},
		{"3b7d19f4-carol", "9c02ae55-alice", "e41fbb70-bob"},
	}
	for _, queued := range arrivals {
		order := promotionOrder(t, queued)
		if !sort.StringsAreSorted(order) {
			t.Errorf("arrival %v: promotion order = %v, want lexicographically ascending", queued, order)
		}
		for i := range want {
			if order[i] != want[i] {
				t.Errorf("arrival %v: promotion order = %v, want %v", queued, order, want)
				break
			}
		}
	}
}

// TestOpLockCoalescesSameSlotAdmitsDistinctSlot pins the per-session
// dedup: a re-fire for an already-pending session identifier coalesces,
// while a distinct session identifier is admitted into the pending set.
//
// spec: 5.2 (per-session checkpoint serialization), 4.7 (one pending
// checkpoint per distinct session identifier).
func TestOpLockCoalescesSameSlotAdmitsDistinctSlot(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "slot-a")
	if err != nil {
		t.Fatalf("Begin running checkpoint: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-b") }()
	waitPendingSession(t, &l, "slot-b")

	// A re-fire for the already-pending slot coalesces.
	if _, err := l.Begin(context.Background(), opCheckpoint, "slot-b"); !errors.Is(err, errOpCoalesced) {
		t.Fatalf("same-slot re-fire err = %v, want errOpCoalesced", err)
	}

	// A distinct slot is admitted into the pending set.
	go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-c") }()
	waitPendingSession(t, &l, "slot-c")
}

// TestOpLockRejectsSecondQueuedInterrupt pins that an interrupt returns
// errOpBusy while any operation is pending, whether a second interrupt
// holds the whole-pod queue or a per-slot checkpoint is pending.
//
// spec: 4.7 (a pending interrupt holds the whole-pod queue; an interrupt
// never displaces a pending checkpoint).
func TestOpLockRejectsSecondQueuedInterrupt(t *testing.T) {
	t.Run("behind a pending interrupt", func(t *testing.T) {
		var l opLock
		rel1, err := l.Begin(context.Background(), opCheckpoint, "")
		if err != nil {
			t.Fatalf("Begin checkpoint: %v", err)
		}
		defer rel1()

		go func() { _, _ = l.Begin(context.Background(), opInterrupt, "") }()
		waitQueued(t, &l)

		// The whole-pod queue is taken by the pending interrupt; a second
		// interrupt is dropped with BUSY.
		if _, err := l.Begin(context.Background(), opInterrupt, ""); !errors.Is(err, errOpBusy) {
			t.Fatalf("second queued interrupt err = %v, want errOpBusy", err)
		}
	})

	t.Run("behind a pending checkpoint", func(t *testing.T) {
		var l opLock
		rel1, err := l.Begin(context.Background(), opCheckpoint, "slot-a")
		if err != nil {
			t.Fatalf("Begin checkpoint: %v", err)
		}
		defer rel1()

		go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-b") }()
		waitPendingSession(t, &l, "slot-b")

		// An interrupt never displaces a pending checkpoint.
		if _, err := l.Begin(context.Background(), opInterrupt, ""); !errors.Is(err, errOpBusy) {
			t.Fatalf("interrupt while a checkpoint is pending err = %v, want errOpBusy", err)
		}
	})
}

func TestOpLockBusyWhenSlotFullDifferentKind(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "")
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opInterrupt, "") }()
	waitQueued(t, &l)

	// A checkpoint arriving behind a pending interrupt is rejected: the
	// pending interrupt holds the whole-pod queue.
	if _, err := l.Begin(context.Background(), opCheckpoint, "slot-a"); !errors.Is(err, errOpBusy) {
		t.Fatalf("checkpoint behind pending interrupt err = %v, want errOpBusy", err)
	}
}

// TestOpLockSecondInterruptDuringRunningInterruptBusy covers the §4.7
// edge from F-4.7.18: while an interrupt runs and a second interrupt
// holds the whole-pod queue, a third interrupt is dropped with BUSY.
func TestOpLockSecondInterruptDuringRunningInterruptBusy(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opInterrupt, "")
	if err != nil {
		t.Fatalf("Begin interrupt: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opInterrupt, "") }()
	waitQueued(t, &l)

	if _, err := l.Begin(context.Background(), opInterrupt, ""); !errors.Is(err, errOpBusy) {
		t.Fatalf("third interrupt err = %v, want errOpBusy", err)
	}
}

// TestOpLockInterruptDuringCheckpointDeliversAfterComplete covers the
// §4.7 case from F-4.7.18: an interrupt arriving during a running
// checkpoint with an empty pending set is queued and delivered once the
// checkpoint releases the lock.
func TestOpLockInterruptDuringCheckpointDeliversAfterComplete(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "")
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}

	delivered := make(chan struct{})
	go func() {
		rel2, err := l.Begin(context.Background(), opInterrupt, "")
		if err != nil {
			t.Errorf("queued interrupt Begin: %v", err)
			return
		}
		close(delivered)
		rel2()
	}()

	waitQueued(t, &l)
	select {
	case <-delivered:
		t.Fatal("interrupt ran before the checkpoint released the lock")
	case <-time.After(50 * time.Millisecond):
	}

	rel1()
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("queued interrupt was not delivered after checkpoint completed")
	}
}

func TestOpLockContextCancelWhileQueued(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "")
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}
	defer rel1()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := l.Begin(ctx, opInterrupt, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued Begin err = %v, want context.DeadlineExceeded", err)
	}

	// Cancellation freed the pending slot; a new operation can queue.
	go func() { _, _ = l.Begin(context.Background(), opInterrupt, "") }()
	waitQueued(t, &l)
}

// TestOpLockContextCancelWithdrawsPendingCheckpoint pins that a cancelled
// per-slot checkpoint is withdrawn from the pending set, so a subsequent
// checkpoint for the same slot is admitted rather than coalesced.
//
// spec: 4.7 (per-slot pending set), 5.2 (per-slot checkpoint
// serialization).
func TestOpLockContextCancelWithdrawsPendingCheckpoint(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "slot-a")
	if err != nil {
		t.Fatalf("Begin running checkpoint: %v", err)
	}
	defer rel1()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := l.Begin(ctx, opCheckpoint, "slot-b"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled checkpoint err = %v, want context.DeadlineExceeded", err)
	}

	l.mu.Lock()
	_, stillPending := l.checkpoints["slot-b"]
	l.mu.Unlock()
	if stillPending {
		t.Fatal("cancelled checkpoint was not withdrawn from the pending set")
	}

	// The withdrawn slot can be re-admitted.
	go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-b") }()
	waitPendingSession(t, &l, "slot-b")
}

// TestOpLockPendingInterruptOccupiesNoCheckpointKey pins that a waiting
// interrupt is recorded outside the pending checkpoint set, so no key of
// that map, empty or otherwise, ever stands for the pod-scoped
// interrupt. The promotion rule is a lexicographic tie-break over
// session identifiers, and an interrupt carries none.
// spec: §4.7 (Checkpoint/Interrupt mutual exclusion), §4.10 (a session
// is addressed by its session identifier on every pod)
func TestOpLockPendingInterruptOccupiesNoCheckpointKey(t *testing.T) {
	var l opLock
	rel, err := l.Begin(context.Background(), opCheckpoint, "session-a")
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, berr := l.Begin(ctx, opInterrupt, "")
		done <- berr
	}()
	waitQueued(t, &l)

	l.mu.Lock()
	pending := l.interruptPending
	keys := len(l.checkpoints)
	l.mu.Unlock()
	if !pending {
		t.Fatal("queued interrupt is not recorded in interruptPending")
	}
	if keys != 0 {
		t.Fatalf("pending checkpoint keys = %d, want 0 while only an interrupt waits", keys)
	}

	// Withdrawing the interrupt clears the same two fields and still
	// leaves the checkpoint set untouched.
	cancel()
	if werr := <-done; !errors.Is(werr, context.Canceled) {
		t.Fatalf("cancelled interrupt Begin err = %v, want context.Canceled", werr)
	}
	l.mu.Lock()
	pending = l.interruptPending
	keys = len(l.checkpoints)
	l.mu.Unlock()
	if pending {
		t.Fatal("withdrawn interrupt still recorded in interruptPending")
	}
	if keys != 0 {
		t.Fatalf("pending checkpoint keys = %d after interrupt withdrawal, want 0", keys)
	}
	rel()
}
