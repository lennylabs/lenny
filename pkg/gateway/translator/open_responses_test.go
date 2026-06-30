// SPDX-License-Identifier: MIT

package translator_test

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
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
)

func newResponsesHandler(t *testing.T) (*translator.OpenResponsesHandler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	h := translator.NewOpenResponsesHandler(store, executor.NewEchoExecutor(), translator.OpenResponsesOptions{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "resp_1" },
	})
	return h, store
}

func respPost(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestOpenResponsesHappyPath(t *testing.T) {
	h, store := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model": "echo",
		"input": "hello",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "resp_1" || resp.Object != "response" {
		t.Errorf("envelope: %+v", resp)
	}
	if resp.Status != "completed" {
		t.Errorf("status: %q", resp.Status)
	}
	if len(resp.Output) != 1 || len(resp.Output[0].Content) != 1 {
		t.Fatalf("output: %+v", resp.Output)
	}
	if !strings.Contains(resp.Output[0].Content[0].Text, "hello") {
		t.Errorf("content: %q", resp.Output[0].Content[0].Text)
	}
	row, _ := store.Get(context.Background(), "acme", "resp_1")
	if row.State != session.StateCompleted {
		t.Errorf("session state: %q", row.State)
	}
}

func TestOpenResponsesAcceptsArrayInput(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model": "echo",
		"input": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestOpenResponsesRejectsMissingInput(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{"model": "echo"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing input: got %d", rr.Code)
	}
}

func TestOpenResponsesStreamEmitsTypedEvents(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model":  "echo",
		"input":  "hello stream",
		"stream": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("stream status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		"event: response.completed",
		"hello",
		"stream",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream body missing %q; body:\n%s", want, body)
		}
	}
}

func TestOpenResponsesGet(t *testing.T) {
	h, store := newResponsesHandler(t)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "resp_1", TenantID: "acme", State: session.StateCompleted,
		RuntimeRef: "echo",
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_1", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Status != "completed" || resp.Model != "echo" {
		t.Errorf("resp: %+v", resp)
	}
}

func TestOpenResponsesGetMissing(t *testing.T) {
	h, _ := newResponsesHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/missing", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rr.Code)
	}
}

func TestOpenResponsesDelete(t *testing.T) {
	h, store := newResponsesHandler(t)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "resp_1", TenantID: "acme",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	req := httptest.NewRequest(http.MethodDelete, "/v1/responses/resp_1", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rr.Code)
	}
	if _, err := store.Get(context.Background(), "acme", "resp_1"); err == nil {
		t.Error("response should be deleted")
	}
}

// TestOpenResponsesAdapterCapabilities pins §9.1 line 35 + §15.1
// line 575: the Open Responses adapter publishes its own capability
// block. previous_response_id threads a multi-turn conversation
// through the same Lenny session, so the adapter reports session
// continuity; delegation, elicitation, and interrupt are not part
// of the Open Responses envelope. F-9.1.6 / F-9.1.8.
func TestOpenResponsesAdapterCapabilities_spec_9_1_35(t *testing.T) {
	caps := translator.OpenResponsesAdapterCapabilities()
	if caps.PathPrefix != "/v1/responses" {
		t.Errorf("PathPrefix: %q", caps.PathPrefix)
	}
	if caps.Protocol != "open-responses" {
		t.Errorf("Protocol: %q", caps.Protocol)
	}
	if !caps.SupportsSessionContinuity {
		t.Error("Open Responses threads previous_response_id and must report continuity")
	}
	if caps.SupportsDelegation || caps.SupportsElicitation || caps.SupportsInterrupt {
		t.Errorf("Open Responses adapter exposes no Lenny surfaces: %+v", caps)
	}
}

func TestOpenResponsesPreservesPreviousResponseID(t *testing.T) {
	h, store := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model":                "echo",
		"input":                "hi",
		"previous_response_id": "resp_prev",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.PreviousResponseID != "resp_prev" {
		t.Errorf("previous_response_id: %q", resp.PreviousResponseID)
	}
	row, _ := store.Get(context.Background(), "acme", "resp_1")
	if row.ParentSessionID != "resp_prev" {
		t.Errorf("ParentSessionID: %q", row.ParentSessionID)
	}
}
