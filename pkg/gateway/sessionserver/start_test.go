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
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
