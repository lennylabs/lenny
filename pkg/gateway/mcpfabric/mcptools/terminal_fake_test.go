// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// hookTerminal is a post-terminal hook fake standing in for the session
// server: it records each hook call with its fromState, runs the writer's
// signal tail, and releases the session through the executor with the
// disposition of the row's state, as the session server's hook does.
type hookTerminal struct {
	exec executor.Executor

	mu    sync.Mutex
	open  map[string]int
	calls []hookCall
}

type hookCall struct {
	id   string
	from session.State
	to   session.State
}

func newHookTerminal(exec executor.Executor) *hookTerminal {
	return &hookTerminal{exec: exec, open: map[string]int{}}
}

func (h *hookTerminal) BeginTerminal(id string) func() {
	h.mu.Lock()
	h.open[id]++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.open[id]--
			h.mu.Unlock()
		})
	}
}

func (h *hookTerminal) AfterTerminalWrite(ctx context.Context, from session.State, sess sessionstore.Session,
	signals func(context.Context, sessionstore.Session),
) sessionstore.Session {
	if signals != nil {
		signals(ctx, sess)
	}
	if h.exec != nil {
		_ = executor.ReleaseSession(ctx, h.exec, sess.ID, executor.DispositionCancelled)
	}
	h.mu.Lock()
	h.calls = append(h.calls, hookCall{id: sess.ID, from: from, to: sess.State})
	h.mu.Unlock()
	return sess
}

// waitSettled waits until n hook calls ran and every claim ended.
func (h *hookTerminal) waitSettled(t *testing.T, n int) []hookCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		openClaims := 0
		for _, c := range h.open {
			openClaims += c
		}
		calls := append([]hookCall(nil), h.calls...)
		h.mu.Unlock()
		if len(calls) >= n && openClaims == 0 {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("hook calls = %+v with %d open claims, want %d calls and every claim ended", calls, openClaims, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
