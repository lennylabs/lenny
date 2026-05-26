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
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §7.1 line 77 (default retention on create), §7.2 line 137
// (status_change on transitions).

func TestCreateStampsDefaultRetention_spec_7_1_5(t *testing.T) {
	store := memstore.New()
	created := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:  func() time.Time { return created },
		IDFunc: func() string { return "sess_ret" },
	})

	buf, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(buf))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	row, err := store.Get(context.Background(), "acme", "sess_ret")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	want := created.Add(sessionserver.DefaultArtifactRetention)
	if !row.RetentionExpiresAt.Equal(want) {
		t.Errorf("RetentionExpiresAt = %v, want created+7d = %v", row.RetentionExpiresAt, want)
	}
}

func TestInterruptEmitsStatusChangeSuspended_spec_7_2_2(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_i", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := sessionevents.NewBus(64)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_i/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("interrupt status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var foundSuspended bool
	for _, e := range bus.History("sess_i", 0) {
		if e.Type != "status_change" {
			continue
		}
		var p struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(e.Data), &p); err == nil && p.State == string(session.StateSuspended) {
			foundSuspended = true
		}
	}
	if !foundSuspended {
		t.Error("interrupt must emit status_change(suspended) on the SSE stream")
	}
	// A non-terminal transition must not emit session_complete.
	for _, e := range bus.History("sess_i", 0) {
		if e.Type == "session_complete" {
			t.Error("interrupt (non-terminal) must not emit session_complete")
		}
	}
}
