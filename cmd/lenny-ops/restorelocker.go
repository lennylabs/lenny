// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// restoreLockScope is the §25.11 / §25.4 remediation-lock scope a restore
// holds so a competing restore cannot start against partially-restored
// state.
const restoreLockScope = "restore:platform"

// restoreLockTTLSeconds is the lock lease the restore acquires. §25.4 caps
// the TTL at one hour, which exceeds the §17.3 restore RTO targets (<30s
// Postgres, <5min MinIO); a pathologically long restore that outlives the
// lease is recoverable through the operator §25.4 lock API
// (/v1/admin/remediation-locks), which surfaces the held scope.
const restoreLockTTLSeconds = 3600

// restoreLocker adapts the §25.4 remediation-lock service to the §25.11
// backup.RestoreLocker contract, so the restore:platform lock is durable
// and coordinates across lenny-ops replicas instead of living in a single
// replica's memory (F-17.3.4). The lock is acquired on restore kickoff
// (any replica) and released by the leader-only restore-completion
// reconciler on success, so Release resolves the holder by scope rather
// than assuming the releasing replica acquired it.
type restoreLocker struct {
	locks coordination.RemediationLockService
}

// newRestoreLocker wraps a §25.4 lock service as a §25.11 RestoreLocker.
func newRestoreLocker(locks coordination.RemediationLockService) *restoreLocker {
	return &restoreLocker{locks: locks}
}

var _ backup.RestoreLocker = (*restoreLocker)(nil)

// Acquire takes the restore:platform lock for owner. A conflict held by a
// different owner maps to backup.ErrLockConflict; a conflict already held
// by owner is an idempotent success (the MemLocker semantics the
// orchestrator relies on for a retried acquire).
func (l *restoreLocker) Acquire(ctx context.Context, owner string) error {
	_, err := l.locks.Acquire(ctx, coordination.LockRequest{
		Scope:      restoreLockScope,
		Operation:  "restore",
		AcquiredBy: owner,
		TTLSeconds: restoreLockTTLSeconds,
	})
	if err == nil {
		return nil
	}
	if coordination.CodeOf(err) == coordination.ErrCodeConflict {
		if held, ok, herr := l.holder(ctx); herr == nil && ok && held == owner {
			return nil
		}
		return backup.ErrLockConflict
	}
	return err
}

// Holder returns the current holder of the restore:platform lock.
func (l *restoreLocker) Holder(ctx context.Context) (string, bool, error) {
	return l.holder(ctx)
}

// Release drops the restore:platform lock, resolving the holder by scope
// and releasing as that holder (the §25.4 service authorizes Release
// against the acquiredBy identity). A scope that is not held is an
// idempotent no-op.
func (l *restoreLocker) Release(ctx context.Context) error {
	lock, ok, err := l.lockForScope(ctx)
	if err != nil || !ok {
		return err
	}
	return l.locks.Release(coordination.WithCaller(ctx, lock.AcquiredBy), lock.ID)
}

// holder returns the acquiredBy of the restore:platform lock and whether
// it is held.
func (l *restoreLocker) holder(ctx context.Context) (string, bool, error) {
	lock, ok, err := l.lockForScope(ctx)
	if err != nil || !ok {
		return "", false, err
	}
	return lock.AcquiredBy, true, nil
}

// lockForScope returns the active lock on restore:platform, if any.
func (l *restoreLocker) lockForScope(ctx context.Context) (coordination.Lock, bool, error) {
	locks, err := l.locks.List(ctx)
	if err != nil {
		return coordination.Lock{}, false, err
	}
	for _, lock := range locks {
		if lock.Scope == restoreLockScope {
			return lock, true, nil
		}
	}
	return coordination.Lock{}, false, nil
}
