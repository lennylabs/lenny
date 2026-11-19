// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// SessionTerminator backs the §24.11 platform-admin session
// investigation endpoints (SessionAdmin) and the lifecycle drains
// (DrainTerminator). GetByID resolves a session by its global id;
// ForceTerminate drives the §24.11 forced terminal transition and runs the
// session core's terminal pipeline through GWSEAM-OnSessionTerminal, which
// releases the assigned pod. spec: §24.11.
type SessionTerminator struct {
	// Store is the session store the forced write locks and updates.
	Store sessionstore.Store
	// Terminals is the session core's terminal pipeline: BeginTerminal
	// opens the terminal-writer claim before the forced write, and
	// OnSessionTerminal runs the full terminal side effects (workspace
	// seal, executor release or pod reclaim, audit, billing) on the row
	// the write produced, the same pipeline a watchdog-forced termination
	// runs, with the pre-force state so the pod release can tell a
	// pre-running claimed session from a running one (§4.6). Nil runs no
	// pipeline. spec: §5.2; §4.6; §7.2 (Interactive Session Model).
	Terminals seam.Terminals
}

var (
	_ SessionAdmin    = SessionTerminator{}
	_ DrainTerminator = SessionTerminator{}
)

// GetByID returns the session row whose id equals id, across every tenant.
func (a SessionTerminator) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return a.Store.GetByID(ctx, id)
}

// ForceTerminate transitions a non-terminal session of tenantID to
// `failed` and releases its pod. The caller supplies the tenant: the
// admin endpoint reads it from the row it has already resolved, and a
// lifecycle drain passes the tenant it lists. The adapter performs no
// lookup of its own, because a global lookup reads the §12.3 read pool,
// which can omit a row the drain listed from the primary; the locked
// Update below reads the primary and returns sessionstore.ErrNotFound
// when no row of that id exists in tenantID. The transition runs under
// the row lock so a concurrent natural transition is serialized: if the
// row has already reached a terminal state the mutate refuses
// (sessionstore.ErrSuperseded) and ForceTerminate returns the locked row,
// its state, false, and nil (idempotent). The pre-force state is captured
// inside the mutate so it reflects the state at commit time, and the
// forced write records the Terminated fact. It opens the terminal claim
// before the write and ends it when the core's OnSessionTerminal returns, or at once on a refusal or error.
// OnSessionTerminal starts the terminal pipeline detached under a claim of
// its own and returns without waiting for it, so a drain moves to its next
// session while this one's pipeline runs. The forced write is
// gateway-decided, so it runs
// detached from the caller's cancellation: an admin request that ends
// before the write commits does not strand the session, and the post-terminal
// hook bounds its own context. spec: §24.11; §7.2 (Interactive Session
// Model); §12.8 (Lifecycle fence and drain); §7.1 (Gateway-owned terminal
// writes).
func (a SessionTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	ctx = context.WithoutCancel(ctx)
	end := func() {}
	if a.Terminals != nil {
		end = a.Terminals.BeginTerminal(id)
	}
	defer end()
	var prev session.State
	var locked sessionstore.Session
	now := time.Now()
	updated, err := a.Store.Update(ctx, tenantID, id, func(s *sessionstore.Session) error {
		prev = s.State
		locked = *s
		if session.IsTerminal(s.State) {
			return sessionstore.ErrSuperseded
		}
		// §24.11: the session transitions immediately to failed.
		// FailureClass uses the §7.1 runtime_failure bucket (the operator
		// forcibly stopped a stuck or unresponsive runtime); the specific
		// cause rides FailureReason. The store does not validate the
		// transition, so this deliberately bypasses the interactive-state
		// guard the normal lifecycle enforces.
		s.State = session.StateFailed
		s.FailureClass = session.FailureClassRuntime
		s.FailureReason = "FORCE_TERMINATED"
		sessionstore.MarkTerminated(s, prev, now)
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		return locked, locked.State, false, nil
	}
	if err != nil {
		return sessionstore.Session{}, "", false, err
	}
	if a.Terminals != nil {
		// spec: §4.6 — prev is the pre-force state, so a force-terminate of a
		// created/finalizing/ready session reclaims its claimed pod by name
		// while a running session is released through the §6.2 executor path.
		a.Terminals.OnSessionTerminal(ctx, state.State(prev), seamRecord(updated))
	}
	return updated, prev, true, nil
}

// seamRecord is the session record of a stored row, carrying the row so
// that the session core runs its logic on the row the API front wrote.
func seamRecord(row sessionstore.Session) seam.Session {
	return seam.Session{
		ID:              row.ID,
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		ParentSessionID: row.ParentSessionID,
		RootSessionID:   row.RootSessionID,
		RuntimeRef:      row.RuntimeRef,
		Pool:            row.PoolRef,
		State:           state.State(row.State),
		Stored:          row,
		PodAssignment:   row.PodAssignment,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		TerminatedAt:    row.TerminatedAt,
	}
}
