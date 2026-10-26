// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// budgetTerminateTimeout bounds the §11.2 mid-session budget termination's
// session lookup and terminal write. The teardown runs on a fresh background
// context off the proxy response path, so this is its own deadline rather
// than the request's. It does not bound the terminal pipeline:
// AfterTerminalWrite re-bounds the hook by the §7.1 terminal pipeline budget,
// which is longer than this deadline. spec: §11.2; §7.1 (Terminal pipeline
// context).
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
// teardown runs. spec: §11.2; §7.1.
type budgetSessionTerminator struct {
	store sessionstore.Store
	// onTerminal runs the sessionserver terminal-side-effects pipeline. It
	// is set after the session server is constructed (the same deferred
	// wiring sessionAdminAdapter uses for its terminal hook). fromState is the
	// session's pre-terminal state so the terminal pod-release path can
	// distinguish a pre-running claimed session from a running one (§4.6).
	onTerminal func(ctx context.Context, fromState session.State, sess sessionstore.Session)
	// beginTerminal opens the session server's terminal-writer claim before
	// the budget write; set with onTerminal, nil opens none. spec: §7.2
	// (Interactive Session Model).
	beginTerminal func(sessionID string) (end func())
}

// TerminateSession transitions sessionID to `expired` with the given
// §8.8 FailureReason and releases its pod. Idempotent: a session already
// terminal is a no-op. The teardown runs asynchronously on a fresh
// background context so the §4.9 proxy response path is never blocked.
//
// The terminal claim opens here, synchronously, before the goroutine starts,
// so a shutdown that races the goroutine already sees the claim and waits
// for it. The goroutine ends the claim after the terminal pipeline, or at
// once on a lookup error, a refusal, or a write error.
// spec: §11.2; §7.1 (Terminal pipeline context); §7.2 (Interactive Session
// Model).
func (t *budgetSessionTerminator) TerminateSession(sessionID, reason string) {
	if t == nil || t.store == nil || sessionID == "" {
		return
	}
	end := func() {}
	if t.beginTerminal != nil {
		end = t.beginTerminal(sessionID)
	}
	go func() {
		defer end()
		ctx, cancel := context.WithTimeout(context.Background(), budgetTerminateTimeout)
		defer cancel()
		t.terminate(ctx, sessionID, reason)
	}()
}

// terminate is the synchronous core of TerminateSession: it resolves the
// session globally, force-transitions a non-terminal row to `expired`
// under the tenant row lock, and runs the terminal-side-effects pipeline.
// The caller holds the session's terminal claim across the call.
func (t *budgetSessionTerminator) terminate(ctx context.Context, sessionID, reason string) {
	sess, err := t.store.GetByID(ctx, sessionID)
	if err != nil {
		log.Printf("lenny-gateway: budget-terminate lookup session=%s: %v", sessionID, err)
		return
	}
	var fromState session.State
	now := time.Now()
	updated, err := t.store.Update(ctx, sess.TenantID, sess.ID, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return sessionstore.ErrSuperseded
		}
		// spec: §7.1 — running → expired (budget exhausted). The
		// store does not validate the transition, matching the watchdog /
		// force-terminate force paths; the §8.8 MCP adapter surfaces this
		// as `failed` with error code `expired:budget`.
		fromState = s.State
		s.State = session.StateExpired
		s.FailureReason = reason
		sessionstore.MarkTerminated(s, fromState, now)
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		return
	}
	if err != nil {
		log.Printf("lenny-gateway: budget-terminate session=%s: %v", sessionID, err)
		return
	}
	if t.onTerminal != nil {
		// spec: §4.6 — a budget terminate runs against a running session, so
		// fromState (running) routes its teardown through the §6.2 executor
		// recycle path rather than the pre-running by-name reclaim.
		t.onTerminal(ctx, fromState, updated)
	}
}
