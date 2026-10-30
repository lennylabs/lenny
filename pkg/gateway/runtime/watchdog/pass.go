// SPDX-License-Identifier: MIT

package watchdog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// dispatchTerminal hands an applied terminal write's open claim to a
// goroutine that runs the write's hooks and then ends the claim. The hooks
// receive a context that keeps ctx's values and drops its cancellation, so
// the end of the sweep pass, or of the watchdog's Run, does not cut a terminal
// pipeline that has started. The open claim is what the gateway's shutdown
// wait observes, so the claim stays open until the hooks return. The caller
// does not end the claim itself after dispatch.
//
// spec: §7.1 (Terminal pipeline context:); §10.1.1 (Primary:).
func (w *Watchdog) dispatchTerminal(ctx context.Context, end func(), hooks func(context.Context)) {
	hctx := context.WithoutCancel(ctx)
	w.dispatched.Add(1)
	go func() {
		defer w.dispatched.Done()
		defer end()
		hooks(hctx)
	}()
}

// superseded reports whether a sweep write's error means the write did not
// apply because the locked row no longer qualifies: the sweep's own sentinel,
// or a row that no longer exists. Neither is a failure of the pass.
// spec: §10.1.1 (Primary:).
func superseded(err, sentinel error) bool {
	return errors.Is(err, sentinel) || errors.Is(err, sessionstore.ErrNotFound)
}

// rowError logs a failed write of one session row with its tenant and session
// IDs and returns the wrapped error the pass collects. The pass goes on to the
// next row, and the next pass evaluates this row again.
// spec: §10.1.1 (Primary:).
func rowError(tenant, sessionID, op string, err error) error {
	slog.Warn("watchdog: "+op, "tenant_id", tenant, "session_id", sessionID, "error", err)
	return fmt.Errorf("watchdog: %s: tenant %s session %s: %w", op, tenant, sessionID, err)
}

// listError logs a failed listing of one tenant's rows, in state st or in
// every state when st is empty, and returns the wrapped error the pass
// collects. The sub-sweep skips the listing and the pass goes on.
// spec: §10.1.1 (Primary:).
func listError(tenant string, st session.State, err error) error {
	slog.Warn("watchdog: list a tenant's sessions", "tenant_id", tenant, "state", string(st), "error", err)
	return fmt.Errorf("watchdog: list sessions: tenant %s state %q: %w", tenant, st, err)
}
