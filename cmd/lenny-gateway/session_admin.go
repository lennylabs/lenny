// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// sessionAdminAdapter backs the §24.11 platform-admin session
// investigation endpoints (admin.SessionAdmin). GetByID resolves a
// session by its global id; ForceTerminate drives the §24.11 line 136
// forced terminal transition and releases the assigned pod via the
// session server's terminal-side-effects hook. spec: §24.11 lines
// 135-136.
type sessionAdminAdapter struct {
	store sessionstore.Store
	// onTerminal runs the full terminal-side-effects pipeline (workspace
	// seal, executor release / pod reclaim, audit, billing). It is the
	// sessionserver's OnSessionTerminal hook — the same pipeline a
	// watchdog-forced termination runs, so a force-terminate releases the
	// pod exactly as a natural terminal does. spec: §5.2 line 519.
	onTerminal func(context.Context, sessionstore.Session)
}

func (a sessionAdminAdapter) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return a.store.GetByID(ctx, id)
}

// ForceTerminate transitions a non-terminal session to `failed` and
// releases its pod. It resolves the session's tenant from a global
// lookup (the operator supplies only the id), then performs the
// transition under the tenant's row lock so a concurrent natural
// transition is serialized: if the row has already reached a terminal
// state the mutate is a no-op and transitioned is false (idempotent).
// The pre-force state is captured inside the mutate so it reflects the
// state at commit time. spec: §24.11 line 136.
func (a sessionAdminAdapter) ForceTerminate(ctx context.Context, id string) (sessionstore.Session, session.State, bool, error) {
	sess, err := a.store.GetByID(ctx, id)
	if err != nil {
		return sessionstore.Session{}, "", false, err
	}
	var prev session.State
	var transitioned bool
	updated, err := a.store.Update(ctx, sess.TenantID, sess.ID, func(s *sessionstore.Session) error {
		prev = s.State
		if session.IsTerminal(s.State) {
			return nil
		}
		// §24.11 line 136: the session transitions immediately to failed.
		// FailureClass uses the §7.1 runtime_failure bucket (the operator
		// forcibly stopped a stuck or unresponsive runtime); the specific
		// cause rides FailureReason. The store does not validate the
		// transition, so this deliberately bypasses the interactive-state
		// guard the normal lifecycle enforces.
		s.State = session.StateFailed
		s.FailureClass = session.FailureClassRuntime
		s.FailureReason = "FORCE_TERMINATED"
		transitioned = true
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, "", false, err
	}
	if transitioned && a.onTerminal != nil {
		a.onTerminal(ctx, updated)
	}
	return updated, prev, transitioned, nil
}
