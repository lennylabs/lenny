// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// closeRecordingExecutor records the session ids Close was called for.
// When order is set, Close also appends "close" to it so a test can
// assert the seal/close ordering.
type closeRecordingExecutor struct {
	closed []string
	order  *[]string
}

func (e *closeRecordingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *closeRecordingExecutor) Close(_ context.Context, sessionID string) error {
	e.closed = append(e.closed, sessionID)
	if e.order != nil {
		*e.order = append(*e.order, "close")
	}
	return nil
}

// releaseRecordingExecutor implements both Executor and SessionReleaser so a
// test can assert the §6.2 terminal disposition the gateway records at
// session teardown (attached → completed/failed/cancelled/expired).
type releaseRecordingExecutor struct {
	released []executor.Disposition
}

func (e *releaseRecordingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *releaseRecordingExecutor) Close(context.Context, string) error {
	e.released = append(e.released, "")
	return nil
}

func (e *releaseRecordingExecutor) Release(_ context.Context, _ string, d executor.Disposition) error {
	e.released = append(e.released, d)
	return nil
}

// TestTerminalTransitionReleasesWithDisposition_spec_6_2 asserts that when the
// executor is a §6.2 Sandbox-backed pod executor (SessionReleaser), the
// gateway records the session's terminal disposition at teardown. A DELETE
// transitions running → cancelled, so the disposition is `cancelled`.
func TestTerminalTransitionReleasesWithDisposition_spec_6_2(t *testing.T) {
	store := memstore.New()
	exec := &releaseRecordingExecutor{}
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec})
	seedRunning(t, store, "sess-disp")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess-disp", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rr.Code)
	}

	if len(exec.released) != 1 || exec.released[0] != executor.DispositionCancelled {
		t.Errorf("Release dispositions = %v, want [cancelled]", exec.released)
	}
}

// recordingSealer records the seal-and-export calls. When order is set,
// Seal also appends "seal" to it.
type recordingSealer struct {
	sealed []string
	order  *[]string
}

func (s *recordingSealer) Seal(_ context.Context, tenantID, sessionID string) error {
	s.sealed = append(s.sealed, tenantID+"/"+sessionID)
	if s.order != nil {
		*s.order = append(*s.order, "seal")
	}
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

func TestTerminalTransitionSealsTheWorkspaceBeforeClosing(t *testing.T) {
	store := memstore.New()
	order := []string{}
	exec := &closeRecordingExecutor{order: &order}
	sealer := &recordingSealer{order: &order}
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec, Sealer: sealer})
	seedRunning(t, store, "sess-seal")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess-seal", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rr.Code)
	}

	if len(sealer.sealed) != 1 || sealer.sealed[0] != "acme/sess-seal" {
		t.Errorf("Seal calls = %v, want [acme/sess-seal]", sealer.sealed)
	}
	// §7.1: the final workspace is sealed before the pod is released.
	if len(order) != 2 || order[0] != "seal" || order[1] != "close" {
		t.Errorf("completion order = %v, want [seal close]", order)
	}
}

func TestNonTerminalTransitionDoesNotSeal(t *testing.T) {
	store := memstore.New()
	sealer := &recordingSealer{}
	srv := sessionserver.New(store, sessionserver.Options{Sealer: sealer})
	seedRunning(t, store, "sess-keep")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-keep/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("interrupt status = %d, want 200", rr.Code)
	}

	if len(sealer.sealed) != 0 {
		t.Errorf("Seal calls = %v, want none for a non-terminal transition", sealer.sealed)
	}
}
