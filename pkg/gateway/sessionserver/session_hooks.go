// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
)

// Sealer takes the §7.1 final workspace snapshot of a session that has
// reached a terminal state. The gateway invokes it as the
// seal-and-export step of session completion.
type Sealer interface {
	// Seal snapshots the session's final workspace. An implementation
	// is expected to no-op for a session that never ran on a pod.
	Seal(ctx context.Context, tenantID, sessionID string) error
}

// SessionLogHook is the §4.4 close-hook the gateway invokes
// on every transition to a terminal state. Implementations capture
// the buffered runtime stderr bytes and persist them best-effort.
// The default production wiring lives in pkg/gateway/sessionlogstore
// (CloseHook.OnSessionTerminal).
//
// spec: §4.4 — "Session logs and runtime stderr".
type SessionLogHook interface {
	// OnSessionTerminal records the session log for (tenant, session).
	// Implementations are best-effort: a failure must not be
	// propagated as a fatal error to the caller.
	OnSessionTerminal(ctx context.Context, tenantID, sessionID string, body []byte, truncated bool) error
}

// ActivityStamper records §6.2 qualifying agent activity for
// a session so the §11.3 idle watchdog does not reap it as idle. The
// gateway wires *sessionidle.Stamper here. Implementations coalesce the
// durable write (≤1/s per session) and are non-blocking. F-11.3.7.
type ActivityStamper interface {
	Stamp(tenantID, sessionID string)
}

// QuotaFinalCheckpointer writes the §11.2
// token-usage checkpoint for a (tenant, user) when a session reaches a
// terminal state: the final cumulative window total is persisted to
// Postgres as the authoritative value so a subsequent Redis-recovery
// reconstruction has an accurate baseline. The default production wiring
// is quotacheckpoint.Service.CheckpointSubject. Best-effort: a failure
// must not abort the terminal-state transition.
//
// spec: §11.2.
type QuotaFinalCheckpointer interface {
	CheckpointSubject(ctx context.Context, tenantID, userID string) error
}
