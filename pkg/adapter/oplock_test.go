// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitQueued blocks until an operation has entered the lock's queue
// slot, so a test can act while exactly one operation is running and
// one is waiting.
func waitQueued(t *testing.T, l *opLock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		q := l.queued
		l.mu.Unlock()
		if q {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not enter the queue")
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
	rel, err := l.Begin(context.Background(), opCheckpoint)
	if err != nil {
		t.Fatalf("Begin on idle lock: %v", err)
	}
	rel()
	// After release the lock is idle and acquirable again.
	rel2, err := l.Begin(context.Background(), opInterrupt)
	if err != nil {
		t.Fatalf("Begin after release: %v", err)
	}
	rel2()
}

func TestOpLockSerializesAndPromotes(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint)
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}

	ran := make(chan opKind, 1)
	go func() {
		rel2, err := l.Begin(context.Background(), opInterrupt)
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

func TestOpLockRejectsSecondQueuedInterrupt(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint)
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opInterrupt) }()
	waitQueued(t, &l)

	// The queue slot is taken; a second interrupt is dropped with BUSY.
	if _, err := l.Begin(context.Background(), opInterrupt); !errors.Is(err, errOpBusy) {
		t.Fatalf("second queued interrupt err = %v, want errOpBusy", err)
	}
}

func TestOpLockCoalescesSecondQueuedCheckpoint(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opInterrupt)
	if err != nil {
		t.Fatalf("Begin interrupt: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opCheckpoint) }()
	waitQueued(t, &l)

	// A second checkpoint behind a queued checkpoint is coalesced.
	if _, err := l.Begin(context.Background(), opCheckpoint); !errors.Is(err, errOpCoalesced) {
		t.Fatalf("second queued checkpoint err = %v, want errOpCoalesced", err)
	}
}

func TestOpLockBusyWhenSlotFullDifferentKind(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint)
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}
	defer rel1()

	go func() { _, _ = l.Begin(context.Background(), opInterrupt) }()
	waitQueued(t, &l)

	// A checkpoint cannot coalesce behind a queued interrupt; the slot
	// is full, so it is rejected.
	if _, err := l.Begin(context.Background(), opCheckpoint); !errors.Is(err, errOpBusy) {
		t.Fatalf("checkpoint behind queued interrupt err = %v, want errOpBusy", err)
	}
}

func TestOpLockContextCancelWhileQueued(t *testing.T) {
	var l opLock
	rel1, err := l.Begin(context.Background(), opCheckpoint)
	if err != nil {
		t.Fatalf("Begin checkpoint: %v", err)
	}
	defer rel1()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := l.Begin(ctx, opInterrupt); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued Begin err = %v, want context.DeadlineExceeded", err)
	}

	// Cancellation freed the queue slot; a new operation can queue.
	go func() { _, _ = l.Begin(context.Background(), opInterrupt) }()
	waitQueued(t, &l)
}
