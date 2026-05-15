// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §7.2 message injection; §15.1 POST /v1/sessions/{id}/messages.

func newMessagesServer(t *testing.T) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor: executor.NewEchoExecutor(),
	})
	return srv, store
}

func seedRunningSession(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func sendMessageRequest(t *testing.T, h http.Handler, id string, body sessionserver.MessageRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/messages", bytes.NewReader(buf))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMessagesEchoExecutor(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_m1")

	rr := sendMessageRequest(t, srv.Handler(), "sess_m1", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{
			{Role: "user", Content: "hello"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryStatus != "delivered" {
		t.Errorf("DeliveryStatus: %q", resp.DeliveryStatus)
	}
	if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Text, "hello") {
		t.Errorf("output: %+v", resp.Output)
	}
}

func TestMessagesRejectsEmptyBatch(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_m2")
	rr := sendMessageRequest(t, srv.Handler(), "sess_m2", sessionserver.MessageRequest{
		Messages: nil,
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty batch: got %d, want 400", rr.Code)
	}
}

func TestMessagesRejectsTerminalSession(t *testing.T) {
	srv, store := newMessagesServer(t)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_done", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "sess_done", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusConflict {
		t.Errorf("terminal session: got %d, want 409", rr.Code)
	}
}

func TestMessagesRejectsMissingSession(t *testing.T) {
	srv, _ := newMessagesServer(t)
	rr := sendMessageRequest(t, srv.Handler(), "sess_missing", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing: got %d, want 404", rr.Code)
	}
}

func TestMessagesRejectsWhenExecutorUnwired(t *testing.T) {
	store := memstore.New()
	seedRunningSession(t, store, "sess_no_exec")
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := sendMessageRequest(t, srv.Handler(), "sess_no_exec", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no executor: got %d, want 503", rr.Code)
	}
}

func TestMessagesAcceptsCreatedSessionPerPreconditionTable(t *testing.T) {
	srv, store := newMessagesServer(t)
	// Created is non-terminal, so the §15.1 precondition table admits it.
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_c", TenantID: "acme", State: session.StateCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "sess_c", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "early"}},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("created state: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
