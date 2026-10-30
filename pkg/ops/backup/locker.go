// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"sync"
)

// restoreLockScope is the §25.11 / §25.4 remediation-lock scope a
// restore takes so a competing restore cannot start against
// partially-restored state.
const restoreLockScope = "restore:platform"

// RestoreLocker is the §25.11 hook into the §25.4 remediation-lock
// subsystem. A restore takes a lock on scope restore:platform before it
// runs; ExecuteRestore fails with REMEDIATION_LOCK_CONFLICT when
// another restore already holds it, and ResumeRestore requires the
// caller to be the current holder.
//
// The §25.4 remediation-lock manager is a separate subsystem. This
// interface is the seam: the BackupService orchestration enforces the
// §25.11 lock semantics against it, MemLocker is the in-memory
// implementation for tests and a single-replica deployment, and a
// production deployment supplies an adapter over the §25.4 manager.
type RestoreLocker interface {
	// Acquire takes the restore:platform lock for owner. It returns
	// ErrLockConflict when the lock is already held by a different
	// owner.
	Acquire(ctx context.Context, owner string) error
	// Holder returns the current holder of the restore:platform lock and
	// whether it is held at all.
	Holder(ctx context.Context) (owner string, held bool, err error)
	// Release drops the restore:platform lock. §25.11 releases it
	// automatically only on a successful restore; a failed restore keeps
	// it held for the operator to release explicitly.
	Release(ctx context.Context) error
}

// ErrLockConflict is returned by a RestoreLocker.Acquire when the
// restore:platform lock is already held by another owner. The Service
// maps it to the §25.11 REMEDIATION_LOCK_CONFLICT error.
var ErrLockConflict = &Error{Code: ErrCodeRemediationLockConflict, Message: "another restore holds the restore:platform lock"}

// MemLocker is the in-memory §25.11 RestoreLocker. It backs the unit
// tests and a single-replica deployment without the §25.4 distributed
// lock manager. It is goroutine-safe.
type MemLocker struct {
	mu    sync.Mutex
	owner string
	held  bool
}

var _ RestoreLocker = (*MemLocker)(nil)

// NewMemLocker returns an unheld MemLocker.
func NewMemLocker() *MemLocker { return &MemLocker{} }

// Acquire implements RestoreLocker.
func (m *MemLocker) Acquire(_ context.Context, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held && m.owner != owner {
		return ErrLockConflict
	}
	m.held = true
	m.owner = owner
	return nil
}

// Holder implements RestoreLocker.
func (m *MemLocker) Holder(context.Context) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owner, m.held, nil
}

// Release implements RestoreLocker.
func (m *MemLocker) Release(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.held = false
	m.owner = ""
	return nil
}
