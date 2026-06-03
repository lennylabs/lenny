// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// TestRestoreLockerAcquireConflictRelease asserts the §25.11 restore lock
// semantics over the §25.4 remediation-lock service: a second owner is
// rejected with a conflict, the same owner re-acquiring is idempotent, the
// holder is observable, and Release frees the scope regardless of which
// replica acquired it (the leader-only completion reconciler releases a
// lock another replica took). F-17.3.4.
//
// spec: §25.11 lines 4148-4149.
func TestRestoreLockerAcquireConflictRelease_spec_25_11(t *testing.T) {
	ctx := context.Background()
	locks := coordination.NewMemStore()
	l := newRestoreLocker(locks)

	if err := l.Acquire(ctx, "ops-a"); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// The same owner re-acquiring is idempotent (the MemLocker semantics).
	if err := l.Acquire(ctx, "ops-a"); err != nil {
		t.Fatalf("idempotent re-Acquire by same owner: %v", err)
	}

	// A different owner conflicts.
	if err := l.Acquire(ctx, "ops-b"); err != backup.ErrLockConflict {
		t.Fatalf("Acquire by second owner = %v, want ErrLockConflict", err)
	}

	// The holder is observable.
	holder, held, err := l.Holder(ctx)
	if err != nil {
		t.Fatalf("Holder: %v", err)
	}
	if !held || holder != "ops-a" {
		t.Fatalf("Holder = (%q, %v), want (ops-a, true)", holder, held)
	}

	// A different replica's locker releases the scope by resolving the
	// holder identity, not by assuming it acquired the lock.
	releaser := newRestoreLocker(locks)
	if err := releaser.Release(ctx); err != nil {
		t.Fatalf("Release from a different locker: %v", err)
	}

	if _, held, _ := l.Holder(ctx); held {
		t.Fatalf("lock still held after Release")
	}

	// Release on an unheld scope is an idempotent no-op.
	if err := l.Release(ctx); err != nil {
		t.Errorf("Release(unheld) = %v, want nil", err)
	}

	// After release the scope is free again.
	if err := l.Acquire(ctx, "ops-b"); err != nil {
		t.Errorf("Acquire after Release: %v", err)
	}
}
