// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"sync"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// ErrInjected is the store error Faults injects.
var ErrInjected = errors.New("lifecyclefence: injected store error")

// KeyedSet counts the entries written under each (tenant, session) key. It
// stands for a store keyed by session ID, such as the blob store or the
// checkpoint manifest.
type KeyedSet struct {
	mu sync.Mutex
	n  map[[2]string]int
	// deleteFaults is the number of upcoming DeleteBySession calls that
	// fail with ErrInjected and delete nothing.
	deleteFaults int
}

// NewKeyedSet returns an empty KeyedSet.
func NewKeyedSet() *KeyedSet { return &KeyedSet{n: map[[2]string]int{}} }

// Put writes one entry under the session.
func (k *KeyedSet) Put(tenantID, sessionID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.n[[2]string{tenantID, sessionID}]++
}

// FailDeletes makes the next n DeleteBySession calls fail with ErrInjected
// and delete nothing.
func (k *KeyedSet) FailDeletes(n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deleteFaults = n
}

// DeleteBySession removes the session's entries and returns how many it
// removed. Its signature is the erasure orchestrator's session-scoped
// eraser.
func (k *KeyedSet) DeleteBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.deleteFaults > 0 {
		k.deleteFaults--
		return 0, ErrInjected
	}
	key := [2]string{tenantID, sessionID}
	n := k.n[key]
	delete(k.n, key)
	return n, nil
}

// Count returns the session's entries.
func (k *KeyedSet) Count(tenantID, sessionID string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.n[[2]string{tenantID, sessionID}]
}

// Faults arms store errors on the drain's seams. Each armed fault fires on
// the next n calls of its seam and then disarms.
type Faults struct {
	mu            sync.Mutex
	listLeft      int
	terminateLeft int
	beforeList    func()
	beforeTerm    func()
	beforeInsert  func()
	// beforeTenantList is the hook of the next tenant-scope primary
	// listing, which only a suspend's drain and a Phase 2 drain issue.
	beforeTenantList func()
}

// BeforeTenantDrainList runs fn once, before the next primary listing of
// the whole tenant, such as to resume the tenant while a suspend's drain
// runs. A user erasure's drain lists one user and never fires it. fn may
// arm the hook again, so a test can reach the drain's verifying listing.
// A nil fn disarms the hook.
func (f *Faults) BeforeTenantDrainList(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeTenantList = fn
}

// takeBeforeTenantList returns and disarms the tenant-scope listing hook.
func (f *Faults) takeBeforeTenantList() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn := f.beforeTenantList
	f.beforeTenantList = nil
	return fn
}

// BeforeDrainList runs fn once, before the next primary listing, such as
// to cancel a request's context while its drain runs.
func (f *Faults) BeforeDrainList(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeList = fn
}

// takeBeforeList returns and disarms the before-listing hook.
func (f *Faults) takeBeforeList() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn := f.beforeList
	f.beforeList = nil
	return fn
}

// BeforeForceTerminate runs fn once, before the next forced termination,
// such as to hold one replica's drain between its listing and its
// terminations while another replica drains the same scope.
func (f *Faults) BeforeForceTerminate(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeTerm = fn
}

// takeBeforeTerminate returns and disarms the before-termination hook.
func (f *Faults) takeBeforeTerminate() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn := f.beforeTerm
	f.beforeTerm = nil
	return fn
}

// BeforeReservedInsert runs fn once, after the next reserved path's
// reservation and pre-insert writes and before its insert, such as to set a
// marker the insert's fence then refuses.
func (f *Faults) BeforeReservedInsert(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeInsert = fn
}

// takeBeforeInsert returns and disarms the before-insert hook.
func (f *Faults) takeBeforeInsert() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn := f.beforeInsert
	f.beforeInsert = nil
	return fn
}

// FailDrainListings makes the next n primary listings fail.
func (f *Faults) FailDrainListings(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listLeft = n
}

// FailForceTerminates makes the next n forced terminations fail.
func (f *Faults) FailForceTerminates(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminateLeft = n
}

// take consumes one armed fault from *left and reports whether it fired.
func (f *Faults) take(left *int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if *left <= 0 {
		return false
	}
	*left--
	return true
}

// FaultySessions is the session store with Faults applied to its primary
// listings, which only the drain issues.
type FaultySessions struct {
	sessionstore.Store
	faults *Faults
}

// List implements sessionstore.Store.
func (f *FaultySessions) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if !filter.Primary {
		return f.Store.List(ctx, tenantID, filter)
	}
	if fn := f.faults.takeBeforeList(); fn != nil {
		fn()
	}
	if filter.UserID == "" {
		if fn := f.faults.takeBeforeTenantList(); fn != nil {
			fn()
		}
	}
	if f.faults.take(&f.faults.listLeft) {
		return nil, ErrInjected
	}
	return f.Store.List(ctx, tenantID, filter)
}

// Terminator is the §24.11 forced terminal transition over the session
// store alone, with Faults applied. It satisfies admin.SessionAdmin.
type Terminator struct {
	store  sessionstore.Store
	faults *Faults
}

var _ admin.SessionAdmin = (*Terminator)(nil)

// GetByID implements admin.SessionAdmin.
func (t *Terminator) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return t.store.GetByID(ctx, id)
}

// ForceTerminate writes failed, FailureClassRuntime, and FORCE_TERMINATED
// under the row lock and reports whether this call made the transition.
func (t *Terminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	if fn := t.faults.takeBeforeTerminate(); fn != nil {
		fn()
	}
	if t.faults.take(&t.faults.terminateLeft) {
		return sessionstore.Session{}, "", false, ErrInjected
	}
	var prev session.State
	transitioned := false
	row, err := t.store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		prev = r.State
		if session.IsTerminal(r.State) {
			return nil
		}
		r.State, r.FailureClass, r.FailureReason = session.StateFailed, session.FailureClassRuntime, "FORCE_TERMINATED"
		transitioned = true
		return nil
	})
	return row, prev, transitioned, err
}

// AuditSink records the admin router's audit events.
type AuditSink struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

// EmitAdminEvent implements the admin router's audit seam.
func (a *AuditSink) EmitAdminEvent(_ context.Context, e admin.AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

// Events returns a copy of the recorded events of type typ.
func (a *AuditSink) Events(typ string) []admin.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []admin.AuditEvent
	for _, e := range a.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}
