// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// budgetTerminateTimeout bounds the §11.2 mid-session budget termination
// store update and terminal pipeline. The teardown runs on a fresh
// background context off the proxy response path, so this is its own
// deadline rather than the request's.
const budgetTerminateTimeout = 30 * time.Second

// budgetSessionTerminator implements sessionbudget.Terminator. It
// transitions an over-budget session to the §7.1 `expired` terminal
// state and runs the terminal-side-effects pipeline (pod release, audit,
// billing, SSE) so a budget-exhausted session is torn down exactly like
// any other terminal. The store update runs on a fresh background
// context in its own goroutine so the §4.9 proxy response path that
// triggers the termination is never blocked on the teardown — the
// enforcer has already flipped the session's pre-flight gate closed
// synchronously, so further requests are rejected while the async
// teardown runs. spec: §11.2 line 44; §7.1 line 175.
type budgetSessionTerminator struct {
	store sessionstore.Store
	// onTerminal runs the sessionserver terminal-side-effects pipeline. It
	// is set after the session server is constructed (the same deferred
	// wiring sessionAdminAdapter uses for its terminal hook).
	onTerminal func(context.Context, sessionstore.Session)
}

// TerminateSession transitions sessionID to `expired` with the given
// §8.8 FailureReason and releases its pod. Idempotent: a session already
// terminal is a no-op. The teardown runs asynchronously on a fresh
// background context so the §4.9 proxy response path is never blocked.
func (t *budgetSessionTerminator) TerminateSession(sessionID, reason string) {
	if t == nil || t.store == nil || sessionID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), budgetTerminateTimeout)
		defer cancel()
		t.terminate(ctx, sessionID, reason)
	}()
}

// terminate is the synchronous core of TerminateSession: it resolves the
// session globally, force-transitions a non-terminal row to `expired`
// under the tenant row lock, and runs the terminal-side-effects pipeline.
func (t *budgetSessionTerminator) terminate(ctx context.Context, sessionID, reason string) {
	sess, err := t.store.GetByID(ctx, sessionID)
	if err != nil {
		log.Printf("lenny-gateway: budget-terminate lookup session=%s: %v", sessionID, err)
		return
	}
	var transitioned bool
	updated, err := t.store.Update(ctx, sess.TenantID, sess.ID, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return nil
		}
		// spec: §7.1 line 175 — running → expired (budget exhausted). The
		// store does not validate the transition, matching the watchdog /
		// force-terminate force paths; the §8.8 MCP adapter surfaces this
		// as `failed` with error code `expired:budget`.
		s.State = session.StateExpired
		s.FailureReason = reason
		transitioned = true
		return nil
	})
	if err != nil {
		log.Printf("lenny-gateway: budget-terminate session=%s: %v", sessionID, err)
		return
	}
	if transitioned && t.onTerminal != nil {
		t.onTerminal(ctx, updated)
	}
}
