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

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// memoryServer wires a sessionserver bound to a fresh in-memory
// store + memorystore so the §9.4 REST surface tests run hermetic.
func memoryServer(t *testing.T, seed ...sessionstore.Session) (http.Handler, memorystore.Store) {
	t.Helper()
	store := memstore.New()
	for _, s := range seed {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	mem := memorystore.NewInMemory(0, nil)
	srv := sessionserver.New(store, sessionserver.Options{Memory: mem})
	return srv.Handler(), mem
}

func memorySession(id, user string) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "default", UserID: user, State: session.StateRunning}
}

// spec: §9.4 (POST /v1/sessions/{id}/memory writes records under
// the session's (tenant, user) scope and returns the stored shape)
func TestMemoryWriteIngestsRecords(t *testing.T) {
	h, store := memoryServer(t, memorySession("sess_1", "alice"))
	body, _ := json.Marshal(sessionserver.MemoryRequest{
		Memories: []sessionserver.MemoryItem{
			{Content: "kubectl is the cluster CLI"},
			{Content: "lenny up brings up the embedded stack", AgentType: "claude-code"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/memory", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("memory write: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MemoryQueryResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Memories) != 2 {
		t.Errorf("returned %d memories, want 2", len(resp.Memories))
	}
	// The store sees the same records under the (tenant, user, session) scope.
	stored, err := store.List(context.Background(), memorystore.MemoryScope{
		TenantID: "default", UserID: "alice", SessionID: "sess_1",
	}, memorystore.MemoryFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 {
		t.Errorf("store has %d memories, want 2", len(stored))
	}
	for _, m := range stored {
		if m.TenantID != "default" || m.UserID != "alice" || m.SessionID != "sess_1" {
			t.Errorf("stored memory has wrong scope: %+v", m)
		}
	}
}

// spec: §9.4 (empty body, no-content, missing user, missing session
// are all rejected with the documented envelopes)
func TestMemoryWriteRejectsInvalid(t *testing.T) {
	h, _ := memoryServer(t,
		memorySession("sess_user", "alice"),
		sessionstore.Session{ID: "sess_no_user", TenantID: "default", State: session.StateRunning},
	)
	cases := []struct {
		name string
		path string
		body []byte
		want int
		code string
	}{
		{"empty body", "/v1/sessions/sess_user/memory", []byte(`{}`),
			http.StatusBadRequest, "VALIDATION_ERROR"},
		{"no content", "/v1/sessions/sess_user/memory",
			[]byte(`{"memories":[{"content":""}]}`),
			http.StatusBadRequest, "VALIDATION_ERROR"},
		{"unknown session", "/v1/sessions/missing/memory",
			[]byte(`{"memories":[{"content":"x"}]}`),
			http.StatusNotFound, "RESOURCE_NOT_FOUND"},
		{"no user", "/v1/sessions/sess_no_user/memory",
			[]byte(`{"memories":[{"content":"x"}]}`),
			http.StatusUnprocessableEntity, "SESSION_NOT_USER_SCOPED"},
		{"malformed JSON", "/v1/sessions/sess_user/memory",
			[]byte(`{not-json}`),
			http.StatusBadRequest, "INVALID_REQUEST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, c.path, bytes.NewReader(c.body))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != c.want {
				t.Errorf("status = %d, want %d (body %s)", rr.Code, c.want, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), c.code) {
				t.Errorf("body missing %q:\n%s", c.code, rr.Body.String())
			}
		})
	}
}

// spec: §9.4 (GET /v1/sessions/{id}/memory returns recorded
// memories, with q narrowing by content and limit capping)
func TestMemoryQueryFiltersAndLimits(t *testing.T) {
	h, store := memoryServer(t, memorySession("sess_q", "alice"))
	scope := memorystore.MemoryScope{TenantID: "default", UserID: "alice", SessionID: "sess_q"}
	if err := store.Write(context.Background(), scope, []memorystore.Memory{
		{Content: "kubectl reaches the cluster"},
		{Content: "docker hosts the local stack"},
		{Content: "kubectl can drain nodes"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// q narrows by substring
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_q/memory?q=kubectl", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MemoryQueryResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Memories) != 2 {
		t.Errorf("q=kubectl returned %d, want 2", len(resp.Memories))
	}

	// limit caps
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_q/memory?limit=1", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Memories) != 1 {
		t.Errorf("limit=1 returned %d, want 1", len(resp.Memories))
	}

	// negative limit rejected
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_q/memory?limit=-1", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("limit=-1 status = %d, want 400", rr.Code)
	}
}

// spec: §9.4 (DELETE /v1/sessions/{id}/memory/{memoryId} removes
// the named record within the session's scope)
func TestMemoryDeleteRemovesRecord(t *testing.T) {
	h, store := memoryServer(t, memorySession("sess_d", "alice"))
	scope := memorystore.MemoryScope{TenantID: "default", UserID: "alice", SessionID: "sess_d"}
	if err := store.Write(context.Background(), scope, []memorystore.Memory{
		{ID: "m_remove", Content: "to be removed"},
		{ID: "m_keep", Content: "stays"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess_d/memory/m_remove", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	remaining, _ := store.List(context.Background(), scope, memorystore.MemoryFilter{})
	if len(remaining) != 1 || remaining[0].ID != "m_keep" {
		t.Errorf("remaining = %v, want only m_keep", remaining)
	}
}

// spec: §9.4 (the endpoints fail closed with MEMORY_UNAVAILABLE when
// the server is built without a memory store)
func TestMemoryEndpointsUnavailableWhenStoreNil(t *testing.T) {
	store := memstore.New()
	_ = store.Create(context.Background(), memorySession("sess_x", "alice"))
	srv := sessionserver.New(store, sessionserver.Options{}) // no Memory wired
	h := srv.Handler()

	cases := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPost, "/v1/sessions/sess_x/memory", []byte(`{"memories":[{"content":"x"}]}`)},
		{http.MethodGet, "/v1/sessions/sess_x/memory", nil},
		{http.MethodDelete, "/v1/sessions/sess_x/memory/m_1", nil},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var rdr *bytes.Reader
			if c.body != nil {
				rdr = bytes.NewReader(c.body)
			}
			var req *http.Request
			if rdr != nil {
				req = httptest.NewRequest(c.method, c.path, rdr)
			} else {
				req = httptest.NewRequest(c.method, c.path, nil)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "MEMORY_UNAVAILABLE") {
				t.Errorf("body missing MEMORY_UNAVAILABLE:\n%s", rr.Body.String())
			}
		})
	}
}
