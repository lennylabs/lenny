// SPDX-License-Identifier: MIT

//go:build contract

package rest_sessions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// ownershipWireServer serves the session surface behind the auth
// middleware's dev-header transport, with roles honored, over a store
// holding alice's running session sess_alice in tenant acme.
func ownershipWireServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := memstore.New()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_alice", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	ts := httptest.NewServer(authmw.Wrap(srv.Handler(), authmw.Options{
		AllowDevHeaders: true, DevHeaderNamesTenant: true, AllowDevRoles: true,
	}))
	t.Cleanup(ts.Close)
	return ts
}

// ownershipCall sends one request as user holding roles and returns the
// status and the raw body.
func ownershipCall(t *testing.T, ts *httptest.Server, method, path, user, roles string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", user)
	if roles != "" {
		req.Header.Set("X-Lenny-Roles", roles)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// spec: §10.2 (Session ownership) rules 3, 4, 5, 6, and 7; §15.1 (RESOURCE_NOT_FOUND, UNAUTHORIZED)
// diagnosis: the session surface's wire answer to a caller other than the
// owner is wrong. A user that does not own the session must receive a 404
// body byte-identical to a missing session's on a read and on an action,
// and must not see the session in GET /v1/sessions; a tenant-viewer reads
// it but receives 403 on an action; a dev-header request with no
// X-Lenny-User-ID receives 401. Before the ownership check every caller the
// permission matrix admitted read and terminated every session in its
// tenant.
func TestSessionOwnershipOnTheWire_spec_10_2(t *testing.T) {
	ts := ownershipWireServer(t)
	missCode, missBody := ownershipCall(t, ts, http.MethodGet, "/v1/sessions/sess_missing", "bob@acme.com", "user")
	if missCode != http.StatusNotFound {
		t.Fatalf("missing session = %d %s, want 404", missCode, missBody)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/sessions/sess_alice"},
		{http.MethodGet, "/v1/sessions/sess_alice/transcript"},
		{http.MethodPost, "/v1/sessions/sess_alice/terminate"},
		{http.MethodPost, "/v1/sessions/sess_alice/elicitations/el_1/respond"},
	} {
		code, body := ownershipCall(t, ts, c.method, c.path, "bob@acme.com", "user")
		if code != http.StatusNotFound || body != missBody {
			t.Errorf("bob/user %s %s = %d %s, want the missing-session 404 %s", c.method, c.path, code, body, missBody)
		}
	}
	if code, body := ownershipCall(t, ts, http.MethodGet, "/v1/sessions/sess_alice", "bob@acme.com", "tenant-viewer"); code != http.StatusOK {
		t.Errorf("bob/tenant-viewer GET = %d %s, want 200", code, body)
	}
	if code, body := ownershipCall(t, ts, http.MethodPost, "/v1/sessions/sess_alice/terminate", "bob@acme.com", "tenant-viewer"); code != http.StatusForbidden {
		t.Errorf("bob/tenant-viewer terminate = %d %s, want 403", code, body)
	}
	if code, body := ownershipCall(t, ts, http.MethodGet, "/v1/sessions/sess_alice", "", "user"); code != http.StatusUnauthorized {
		t.Errorf("no X-Lenny-User-ID GET = %d %s, want 401", code, body)
	}
	_, listBody := ownershipCall(t, ts, http.MethodGet, "/v1/sessions", "bob@acme.com", "user")
	var env struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(listBody), &env); err != nil {
		t.Fatalf("decode list %s: %v", listBody, err)
	}
	if len(env.Items) != 0 {
		t.Errorf("bob/user list = %v, want none of alice's sessions", env.Items)
	}
	code, body := ownershipCall(t, ts, http.MethodGet, "/v1/sessions/sess_alice", "alice@acme.com", "user")
	if code != http.StatusOK {
		t.Errorf("alice GET her session = %d %s, want 200", code, body)
	}
}
