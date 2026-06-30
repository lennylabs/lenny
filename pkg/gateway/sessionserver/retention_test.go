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

// spec: §7.1 artifact retention, §15.1 extend-retention.

func seedSessionForRetention(t *testing.T, store sessionstore.Store) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID:        "sess_R",
		TenantID:  "acme",
		State:     session.StateCompleted,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return row
}

func extendRetentionRequest(t *testing.T, h http.Handler, body sessionserver.ExtendRetentionRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_R/extend-retention", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestExtendRetentionSetsDeadlineFromClock(t *testing.T) {
	store := memstore.New()
	seedSessionForRetention(t, store)
	fixedNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock: func() time.Time { return fixedNow },
	})

	rr := extendRetentionRequest(t, srv.Handler(),
		sessionserver.ExtendRetentionRequest{TTLSeconds: 7 * 24 * 3600})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.ExtendRetentionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantExpiry := fixedNow.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if resp.RetentionExpiresAt != wantExpiry {
		t.Errorf("retentionExpiresAt: got %q, want %q", resp.RetentionExpiresAt, wantExpiry)
	}

	row, _ := store.Get(context.Background(), "acme", "sess_R")
	if !row.RetentionExpiresAt.Equal(fixedNow.Add(7 * 24 * time.Hour)) {
		t.Errorf("row.RetentionExpiresAt: got %v, want %v", row.RetentionExpiresAt, fixedNow.Add(7*24*time.Hour))
	}
}

func TestExtendRetentionRejectsZeroAndNegative(t *testing.T) {
	store := memstore.New()
	seedSessionForRetention(t, store)
	srv := sessionserver.New(store, sessionserver.Options{})

	for _, ttl := range []int64{0, -5} {
		rr := extendRetentionRequest(t, srv.Handler(),
			sessionserver.ExtendRetentionRequest{TTLSeconds: ttl})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("ttl=%d: status got %d, want 400; body=%s", ttl, rr.Code, rr.Body.String())
		}
	}
}

func TestExtendRetentionRejectsMalformedJSON(t *testing.T) {
	store := memstore.New()
	seedSessionForRetention(t, store)
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_R/extend-retention",
		bytes.NewReader([]byte("not-json")))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON: got %d, want 400", rr.Code)
	}
}

func TestExtendRetentionMissingSessionReturns404(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	rr := extendRetentionRequest(t, srv.Handler(),
		sessionserver.ExtendRetentionRequest{TTLSeconds: 3600})
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing session: got %d, want 404", rr.Code)
	}
}

func TestExtendRetentionAcceptsTerminalSession(t *testing.T) {
	// §7.1 retention is typically extended on terminal sessions; verify
	// no precondition-state rejection.
	store := memstore.New()
	row := seedSessionForRetention(t, store)
	if row.State != session.StateCompleted {
		t.Fatalf("seed should be completed, got %q", row.State)
	}
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := extendRetentionRequest(t, srv.Handler(),
		sessionserver.ExtendRetentionRequest{TTLSeconds: 60})
	if rr.Code != http.StatusOK {
		t.Errorf("terminal-session extend: got %d, want 200", rr.Code)
	}
}
