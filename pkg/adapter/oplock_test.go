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

// waitPendingSlot blocks until a checkpoint for slotID is pending.
func waitPendingSlot(t *testing.T, l *opLock, slotID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		_, ok := l.checkpoints[slotID]
		l.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("checkpoint for slot %q did not enter the pending set", slotID)
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

// TestOpLockPromotesCheckpointsInSlotIDOrder pins the slot-ID-order
// promotion rule: distinct-slot checkpoints queued in non-ascending order
// are promoted in ascending slotID order.
//
// spec: 5.2 (one slot's checkpoint upload at a time, in slot-ID order),
// 4.7 (Checkpoint/Interrupt mutual exclusion, slot-ID-order promotion).
func TestOpLockPromotesCheckpointsInSlotIDOrder(t *testing.T) {
	var l opLock
	rel, err := l.Begin(context.Background(), opCheckpoint, "slot-0")
	if err != nil {
		t.Fatalf("Begin running checkpoint: %v", err)
	}

	// Queue distinct slots in non-ascending order. Each waiter records its
	// slot when promoted, then releases so the next lowest slot promotes.
	queued := []string{"slot-c", "slot-a", "slot-b"}
	promoted := make(chan string, len(queued))
	for _, slot := range queued {
		slot := slot
		go func() {
			rel2, err := l.Begin(context.Background(), opCheckpoint, slot)
			if err != nil {
				t.Errorf("queued Begin for %q: %v", slot, err)
				return
			}
			promoted <- slot
			rel2()
		}()
	}
	waitPendingCheckpoints(t, &l, len(queued))

	// Release the running checkpoint; promotions cascade one at a time.
	rel()

	var order []string
	for range queued {
		select {
		case slot := <-promoted:
			order = append(order, slot)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d checkpoints promoted", len(order), len(queued))
		}
	}
	want := []string{"slot-a", "slot-b", "slot-c"}
	if !sort.StringsAreSorted(order) {
		t.Errorf("promotion order = %v, want ascending slot-ID order", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("promotion order = %v, want %v", order, want)
			break
		}
	}
}

// TestOpLockCoalescesSameSlotAdmitsDistinctSlot pins the per-slot dedup:
// a re-fire for an already-pending slotID coalesces, while a distinct
// slotID is admitted into the pending set.
//
// spec: 5.2 (per-slot checkpoint serialization), 4.7 (one pending
// checkpoint per distinct slotId).
func TestOpLockCoalescesSameSlotAdmitsDistinctSlot(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint, "slot-a")
	if err != nil {
		t.Fatalf("Begin running checkpoint: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-b") }()
	waitPendingSlot(t, &l, "slot-b")

	// A re-fire for the already-pending slot coalesces.
	if _, err := l.Begin(context.Background(), opCheckpoint, "slot-b"); !errors.Is(err, errOpCoalesced) {
		t.Fatalf("same-slot re-fire err = %v, want errOpCoalesced", err)
	}

	// A distinct slot is admitted into the pending set.
	go func() { _, _ = l.Begin(context.Background(), opCheckpoint, "slot-c") }()
	waitPendingSlot(t, &l, "slot-c")
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
		waitPendingSlot(t, &l, "slot-b")

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
	waitPendingSlot(t, &l, "slot-b")
}
