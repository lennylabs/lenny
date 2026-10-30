// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DeletionStep names one tenant-deletion Phase 3 step or one Phase 4 entry.
// A Phase 4 entry carries the production entry's name.
type DeletionStep string

// The Phase 3 steps, in the order tenantCredentialRevoker runs them.
const (
	// StepCommitDeleting commits TenantState deleting on the tenant row.
	StepCommitDeleting DeletionStep = "commit_deleting"
	// StepListSessions lists the tenant's sessions from the primary.
	StepListSessions DeletionStep = "list_sessions"
	// StepReturnLeases is the §11.4 lease step over every listed session.
	// It reports no error, so FailDeletionStep does not reach it: a
	// lease-store failure is modelled by Leases.FailSilently.
	StepReturnLeases DeletionStep = "return_leases"
	// StepDeleteTokens deletes the tenant's TokenStore rows.
	StepDeleteTokens DeletionStep = "delete_tokens"
)

// Phase3Steps lists the Phase 3 steps that can return an error, in order.
var Phase3Steps = []DeletionStep{StepCommitDeleting, StepListSessions, StepDeleteTokens}

// Phase4Entries lists the harness's Phase 4 entries in rank order. They are
// the entries of the stores a World holds, under the production names and in
// the production order.
var Phase4Entries = []DeletionStep{
	"artifacts", "checkpoint_manifests", "transcripts", "sessions", "tokens", "credential_pool", "user_roles",
}

// deletionProgress is how far the tenant's deletion has got, as the
// phase-exit invariants observed it.
type deletionProgress struct {
	phase3Done, phase4Done bool
	// steps counts each Phase 3 step and Phase 4 entry that completed.
	steps map[DeletionStep]int
}

// FailDeletionStep makes the next n runs of step s fail with ErrInjected
// before the step acts.
func (f *Faults) FailDeletionStep(s DeletionStep, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stepFaults == nil {
		f.stepFaults = map[DeletionStep]int{}
	}
	f.stepFaults[s] = n
}

// AfterDeletionStep runs fn after every Phase 3 step and Phase 4 entry that
// completes, such as to stop the replica between two entries. A nil fn
// disarms the hook.
func (f *Faults) AfterDeletionStep(fn func(DeletionStep)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afterStep = fn
}

// takeStepFault consumes one armed fault of step s and reports whether it
// fired.
func (f *Faults) takeStepFault(s DeletionStep) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stepFaults[s] <= 0 {
		return false
	}
	f.stepFaults[s]--
	return true
}

func (f *Faults) afterStepHook() func(DeletionStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.afterStep
}

// deletionStep runs fn as step s: it returns ctx's error when ctx has
// ended, as a replica that has stopped runs nothing, then an armed fault,
// and otherwise fn's error. A completed step is counted and then the
// after-step hook runs.
func (w *World) deletionStep(ctx context.Context, s DeletionStep, fn func() (int, error)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if w.Faults.takeStepFault(s) {
		return 0, fmt.Errorf("%s: %w", s, ErrInjected)
	}
	n, err := fn()
	if err != nil {
		return n, err
	}
	w.mu.Lock()
	if w.deletion.steps == nil {
		w.deletion.steps = map[DeletionStep]int{}
	}
	w.deletion.steps[s]++
	w.mu.Unlock()
	if hook := w.Faults.afterStepHook(); hook != nil {
		hook(s)
	}
	return n, nil
}

// DeletionSteps returns how many times each Phase 3 step and Phase 4 entry
// completed across every replica.
func (w *World) DeletionSteps() map[DeletionStep]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[DeletionStep]int, len(w.deletion.steps))
	for k, v := range w.deletion.steps {
		out[k] = v
	}
	return out
}

// phase3Revoker is the harness Phase 3 CredentialRevoker. It follows
// cmd/lenny-gateway's tenantCredentialRevoker step for step: it commits
// deleting unless the row is already deleting or deleted, lists the
// tenant's sessions from the primary with terminal ones included, returns
// every lease they hold through the lease step, and deletes the tenant's
// TokenStore rows. It returns the first error, which pauses the job in
// Phase 3.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3), §11.4 (User Invalidation)
type phase3Revoker struct{ w *World }

// RevokeTenantCredentials implements tenantdeletion.CredentialRevoker.
func (r phase3Revoker) RevokeTenantCredentials(ctx context.Context, tenantID string) error {
	w := r.w
	if tenantID != w.Tenant {
		return fmt.Errorf("phase 3 of foreign tenant %s", tenantID)
	}
	if _, err := w.deletionStep(ctx, StepCommitDeleting, func() (int, error) {
		return 0, commitDeleting(ctx, w.S.Tenants, tenantID)
	}); err != nil {
		return fmt.Errorf("tenant deletion phase 3: commit deleting: %w", err)
	}
	var ids []string
	if _, err := w.deletionStep(ctx, StepListSessions, func() (int, error) {
		rows, err := w.S.Sessions.List(ctx, tenantID, sessionstore.ListFilter{Primary: true})
		for _, s := range rows {
			ids = append(ids, s.ID)
		}
		return len(rows), err
	}); err != nil {
		return fmt.Errorf("tenant deletion phase 3: list sessions: %w", err)
	}
	if _, err := w.deletionStep(ctx, StepReturnLeases, func() (int, error) {
		if len(ids) == 0 {
			return 0, nil
		}
		return w.Leases.RevokeUserLeases(tenantID, "", ids), nil
	}); err != nil {
		return fmt.Errorf("tenant deletion phase 3: return leases: %w", err)
	}
	if _, err := w.deletionStep(ctx, StepDeleteTokens, func() (int, error) {
		return w.Tokens.DeleteByTenant(ctx, tenantID)
	}); err != nil {
		return fmt.Errorf("tenant deletion phase 3: delete tokens: %w", err)
	}
	return nil
}

// commitDeleting moves the tenant row to deleting unless it is already
// deleting or deleted.
func commitDeleting(ctx context.Context, tenants tenantstore.Store, tenantID string) error {
	_, err := tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		if t.State != tenantstore.TenantStateDeleting && t.State != tenantstore.TenantStateDeleted {
			t.State = tenantstore.TenantStateDeleting
		}
		return nil
	})
	return err
}

// phase4Eraser is the harness Phase 4 TenantEraser: the Phase4Entries in
// rank order over the World's stores, stopping at the first error, as
// cmd/lenny-gateway's tenantEraser runs its list.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle), §12.8 (Compliance Interfaces: Idempotency and resumption)
type phase4Eraser struct{ w *World }

// DeleteByTenant implements tenantdeletion.TenantEraser.
func (e phase4Eraser) DeleteByTenant(ctx context.Context, tenantID string) (map[string]int, error) {
	w := e.w
	if tenantID != w.Tenant {
		return nil, fmt.Errorf("phase 4 of foreign tenant %s", tenantID)
	}
	fns := e.entries()
	out := make(map[string]int, len(Phase4Entries))
	for _, name := range Phase4Entries {
		fn := fns[name]
		n, err := w.deletionStep(ctx, name, func() (int, error) { return fn(ctx, tenantID) })
		if err != nil {
			return out, fmt.Errorf("tenant erase %s: %w", name, err)
		}
		out[string(name)] = n
	}
	return out, nil
}

// entries returns each Phase 4 entry's function by name.
func (e phase4Eraser) entries() map[DeletionStep]func(context.Context, string) (int, error) {
	w := e.w
	return map[DeletionStep]func(context.Context, string) (int, error){
		"artifacts":            w.Objects.DeleteByTenant,
		"checkpoint_manifests": w.Manifests.EraseTenant,
		"transcripts":          w.S.Transcripts.DeleteByTenant,
		"sessions":             w.S.Sessions.DeleteByTenant,
		"tokens":               w.Tokens.DeleteByTenant,
		"credential_pool":      w.Leases.DeleteByTenant,
		"user_roles":           w.S.Users.DeleteByTenant,
	}
}

// holdEnumerator is the Phase 3.5 LegalHoldEnumerator: one session hold
// while the test holds the deletion with Hold, and none otherwise. No escrow
// migrator is wired, so a hold pauses the job at Phase 3.5.
type holdEnumerator struct{ d *Deletion }

// ActiveTenantHolds implements tenantdeletion.LegalHoldEnumerator.
func (h holdEnumerator) ActiveTenantHolds(context.Context, string) ([]tenantdeletion.HeldResource, error) {
	h.d.mu.Lock()
	defer h.d.mu.Unlock()
	if !h.d.held {
		return nil, nil
	}
	return []tenantdeletion.HeldResource{{ResourceType: "session", ResourceID: "held"}}, nil
}

// Hold sets or clears the legal hold that pauses this replica's job at
// Phase 3.5.
func (d *Deletion) Hold(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held = on
}

// TakeOverDeletion builds the reconciler of a replica that takes over the
// deletion tick: its fresh job store holds the job rebuilt from the tenant
// row, as the gateway runner's ensureJob does. A tenant in deleting resumes
// at Phase 3, and any other state at Phase 1.
func (w *World) TakeOverDeletion(ctx context.Context) (*Deletion, error) {
	t, err := w.S.Tenants.Get(ctx, w.Tenant)
	if err != nil {
		return nil, fmt.Errorf("read tenant %s: %w", w.Tenant, err)
	}
	d := w.NewDeletion()
	phase, state := tenantdeletion.PhaseSoftDisable, tenantdeletion.TenantDisabling
	if t.State == tenantstore.TenantStateDeleting {
		phase, state = tenantdeletion.PhaseRevokeCredentials, tenantdeletion.TenantDeleting
	}
	now := time.Now().UTC()
	if err := d.Jobs.Create(ctx, tenantdeletion.Job{
		TenantID: w.Tenant, WorkspaceTier: "T3", State: state, Phase: phase,
		StartedAt: now, UpdatedAt: now,
		PhaseLog: map[tenantdeletion.Phase]time.Time{}, DeletedCounts: map[string]int{},
	}); err != nil {
		return nil, fmt.Errorf("rebuild deletion job: %w", err)
	}
	return d, nil
}

// ThroughPhase4 ticks until the job has left Phase 4, at most maxTicks
// times, and returns the job's phase.
func (d *Deletion) ThroughPhase4(ctx context.Context, maxTicks int) (tenantdeletion.Phase, error) {
	return d.until(ctx, maxTicks, tenantdeletion.PhaseDestroyKMSKey)
}

// Complete ticks until the job completes, at most maxTicks times, and then
// tombstones the tenant row, as the gateway runner's syncBack does.
func (d *Deletion) Complete(ctx context.Context, maxTicks int) error {
	if _, err := d.until(ctx, maxTicks, tenantdeletion.PhaseCompleted); err != nil {
		return err
	}
	if err := d.w.S.Tenants.SoftDelete(ctx, d.w.Tenant, time.Now().UTC()); err != nil {
		return fmt.Errorf("tombstone tenant %s: %w", d.w.Tenant, err)
	}
	return nil
}

// until ticks until the job reaches target or a later phase, at most
// maxTicks times. A failed job is an error.
func (d *Deletion) until(ctx context.Context, maxTicks int, target tenantdeletion.Phase) (tenantdeletion.Phase, error) {
	var phase tenantdeletion.Phase
	for i := 0; i < maxTicks; i++ {
		p, err := d.Tick(ctx)
		if err != nil {
			return p, err
		}
		phase = p
		if p == tenantdeletion.PhaseFailed {
			return p, fmt.Errorf("deletion job failed")
		}
		if phaseIndex(p) >= phaseIndex(target) {
			return p, nil
		}
	}
	return phase, fmt.Errorf("deletion job still at %s after %d ticks, want %s", phase, maxTicks, target)
}

// phaseIndex returns the position of p in the §12.8 phase sequence, with
// completed after the last phase.
func phaseIndex(p tenantdeletion.Phase) int {
	i, cur := 0, tenantdeletion.PhaseSoftDisable
	for cur != p {
		next, ok := tenantdeletion.NextPhase(cur)
		if !ok {
			return -1
		}
		cur = next
		i++
	}
	return i
}

// onDeletionAdvance runs the phase-exit invariants of a deletion job's
// committed move from one phase to the next.
//
//   - Leaving Phase 2, the tenant holds no non-terminal session.
//   - Leaving Phase 3, the tenant row is deleting and therefore not
//     registered, so the registry refuses the tenant's bearers on every
//     replica; no session row of the tenant holds a lease, unless a silent
//     lease-store fault swallowed a lease step; and the tenant holds no
//     TokenStore row.
//   - Leaving Phase 4, no row, object, key, lease, or token of the tenant
//     remains in any store the World holds.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle), §10.2 (Authentication)
func (w *World) onDeletionAdvance(ctx context.Context, from, to tenantdeletion.Phase) {
	switch from {
	case tenantdeletion.PhaseTerminateSessions:
		w.assertScopeDrained(ctx, "", fmt.Sprintf("when the deletion job left Phase 2 for %s", to))
	case tenantdeletion.PhaseRevokeCredentials:
		w.assertPhase3Done(ctx)
	case tenantdeletion.PhaseDeleteData:
		w.assertErased(ctx, "when the deletion job left Phase 4")
		w.mu.Lock()
		w.deletion.phase4Done = true
		w.mu.Unlock()
	}
}

// assertPhase3Done records a violation for each Phase 3 effect that does
// not hold.
func (w *World) assertPhase3Done(ctx context.Context) {
	w.mu.Lock()
	w.deletion.phase3Done = true
	w.mu.Unlock()
	t, err := w.S.Tenants.Get(ctx, w.Tenant)
	switch {
	case err != nil:
		w.Violatef("leaving Phase 3: read tenant: %v", err)
	case t.State != tenantstore.TenantStateDeleting && t.State != tenantstore.TenantStateDeleted:
		w.Violatef("leaving Phase 3: tenant state %q, want deleting", t.State)
	case t.Registered():
		w.Violatef("leaving Phase 3: the tenant is still registered, so its bearers are admitted")
	}
	if held := w.reachableLeases(ctx, ""); len(held) > 0 && w.Leases.SilentFired() == 0 {
		w.Violatef("leaving Phase 3: the tenant still holds leases %v", held)
	}
	if n := w.Tokens.Count(w.Tenant); n > 0 {
		w.Violatef("leaving Phase 3: the tenant still holds %d TokenStore rows", n)
	}
}

// reachableLeases returns the held leases of the tenant (of user when user
// is non-empty) whose session still has a row. The lease step reaches a
// lease only through a listed session, so a lease whose session row a user
// erasure deleted is out of its reach; the Phase 4 credential_pool entry
// deletes its record.
func (w *World) reachableLeases(ctx context.Context, user string) []string {
	var out []string
	for id, sid := range w.Leases.HeldBySessions(w.Tenant, user) {
		if _, err := w.S.Sessions.Get(ctx, w.Tenant, sid); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// assertErased records a violation when anything of the tenant remains.
func (w *World) assertErased(ctx context.Context, when string) {
	r, err := w.Residue(ctx)
	if err != nil {
		w.Violatef("%s: %v", when, err)
		return
	}
	if !r.Erased() {
		w.Violatef("%s: the tenant still holds %+v", when, r)
	}
}

// Phase4Done reports whether the deletion job has left Phase 4.
func (w *World) Phase4Done() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deletion.phase4Done
}

// Phase3Done reports whether the deletion job has left Phase 3.
func (w *World) Phase3Done() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deletion.phase3Done
}
