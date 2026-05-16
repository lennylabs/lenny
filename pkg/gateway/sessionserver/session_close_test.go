// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// closeRecordingExecutor records the session ids Close was called for.
type closeRecordingExecutor struct {
	closed []string
}

func (e *closeRecordingExecutor) Send(context.Context, string, []executor.Message) ([]executor.OutputPart, error) {
	return nil, nil
}

func (e *closeRecordingExecutor) Close(_ context.Context, sessionID string) error {
	e.closed = append(e.closed, sessionID)
	return nil
}

func seedRunning(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestTerminalTransitionClosesTheExecutor(t *testing.T) {
	store := memstore.New()
	exec := &closeRecordingExecutor{}
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec})
	seedRunning(t, store, "sess-term")

	// DELETE transitions the running session to cancelled (terminal).
	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess-term", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rr.Code)
	}

	if len(exec.closed) != 1 || exec.closed[0] != "sess-term" {
		t.Errorf("executor Close calls = %v, want [sess-term]", exec.closed)
	}
}

func TestNonTerminalTransitionLeavesTheExecutorOpen(t *testing.T) {
	store := memstore.New()
	exec := &closeRecordingExecutor{}
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec})
	seedRunning(t, store, "sess-int")

	// interrupt transitions running → suspended, a non-terminal state.
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-int/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("interrupt status = %d, want 200", rr.Code)
	}

	if len(exec.closed) != 0 {
		t.Errorf("executor Close calls = %v, want none for a non-terminal transition", exec.closed)
	}
}
