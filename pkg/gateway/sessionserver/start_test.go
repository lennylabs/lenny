// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §15.1 POST /v1/sessions/start convenience endpoint.

func TestSessionStartCreatesRunningSession(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_start_1" },
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state: got %q, want running", resp.State)
	}
	if resp.UploadToken == "" {
		t.Error("uploadToken missing")
	}

	row, err := store.Get(context.Background(), "acme", "sess_start_1")
	if err != nil {
		t.Fatalf("not stored: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("stored state: %q", row.State)
	}
}

func TestSessionStartRecordsEnvironment(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_start_env" },
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef:  "echo",
		Environment: "security-team",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Environment != "security-team" {
		t.Errorf("response environment: got %q, want security-team", resp.Environment)
	}
	row, err := store.Get(context.Background(), "acme", "sess_start_env")
	if err != nil {
		t.Fatalf("not stored: %v", err)
	}
	if row.Environment != "security-team" {
		t.Errorf("stored environment: got %q, want security-team", row.Environment)
	}
}

// spec: §4.4 (proposal: /start is launch-only), §15.1 (/start precondition,
// ready → running).
// TestTwoStepStartLaunchesReadySession exercises the two-step
// `POST /v1/sessions/{id}/start` on the minimal gateway (no pod binder): a
// `ready` session reaching `running` is the pure ready → running launch
// transition, with no claim, materialization, setup, or credential work. The
// preparation barrier already ran at /finalize, so /start is launch-only even
// on the minimal gateway, where the launch is a plain state transition.
func TestTwoStepStartLaunchesReadySession(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:       "sess_ready_1",
		TenantID: "acme",
		UserID:   "alice@acme.com",
		State:    session.StateReady,
	}); err != nil {
		t.Fatalf("seed ready session: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_ready_1/start", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running", resp.State)
	}
	row, err := store.Get(context.Background(), "acme", "sess_ready_1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("stored state = %q, want running", row.State)
	}
}

// spec: §15.1 (/start precondition: only a `ready` session may start).
// A two-step /start against a session that has not finalized (still
// `created`) is rejected by the precondition gate, so /start cannot run the
// launch against a pod that was never prepared.
func TestTwoStepStartRejectsNonReadyState(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:       "sess_created_1",
		TenantID: "acme",
		State:    session.StateCreated,
	}); err != nil {
		t.Fatalf("seed created session: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_created_1/start", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("start on a created session: status %d, want 409 (precondition)", rr.Code)
	}
}

func TestSessionStartRejectsMissingRuntime(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing runtime: got %d, want 400", rr.Code)
	}
}

func TestSessionStartValidatesWorkspacePlan(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_start_wp" },
	})
	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef: "echo",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"a","content":"b","mode":"04755"}]
		}`),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// Setuid mode → WORKSPACE_PLAN_INVALID
	if rr.Code != http.StatusBadRequest {
		t.Errorf("setuid mode: got %d, want 400", rr.Code)
	}
}
