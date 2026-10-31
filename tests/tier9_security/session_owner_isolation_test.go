// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 session owner isolation. A session is readable by its owner and by
// a principal whose role grants "Read other users' sessions (same tenant)",
// and actionable by its owner alone. The test drives the session API as an
// outside caller would, over HTTP, behind the auth middleware's dev-header
// transport. The custom-role caller is authenticated by a principal layer
// in front of the middleware, because the dev-header transport admits only
// built-in role names and a custom role reaches a principal only through a
// bearer token's role claim.

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

const (
	isoAlice = "alice@acme.com"
	isoBob   = "bob@acme.com"
	// isoCustomRole is the tenant custom role holding the read bypass
	// alongside the session permissions a user holds.
	isoCustomRole = "session-reader"
	// isoCustomHeader marks a request the principal layer authenticates as
	// bob holding isoCustomRole.
	isoCustomHeader = "X-Test-Custom-Role-Bob"
)

// ownerIsolationGateway serves the session API with acme's session-reader
// role registered. A request carrying isoCustomHeader runs as bob holding
// that role; every other request authenticates through the dev-header
// transport with built-in roles honored.
func ownerIsolationGateway(t *testing.T) http.Handler {
	t.Helper()
	roles := customrolestore.NewMemory()
	if err := roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: isoCustomRole,
		Permissions: []pkgauth.Permission{
			pkgauth.PermReadTenantSessions, pkgauth.PermReadOwnSessions, pkgauth.PermManageOwnSessions,
		},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, CustomRoles: roles})
	t.Cleanup(srv.Close)
	api := srv.Handler()
	dev := authmw.Wrap(api, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true, AllowDevRoles: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(isoCustomHeader) == "" {
			dev.ServeHTTP(w, r)
			return
		}
		p := authmw.Principal{Subject: isoBob, TenantID: "acme", Roles: []pkgauth.Role{isoCustomRole}}
		api.ServeHTTP(w, r.WithContext(authmw.WithPrincipal(r.Context(), p)))
	})
}

// isoCaller is one outside caller: a dev-header user with a built-in role,
// or the custom-role bob.
type isoCaller struct {
	user, role string
	custom     bool
}

func (c isoCaller) do(t *testing.T, h http.Handler, method, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if c.custom {
		req.Header.Set(isoCustomHeader, "1")
	} else {
		req.Header.Set("X-Lenny-User-ID", c.user)
		req.Header.Set("X-Lenny-Roles", c.role)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	raw, _ := io.ReadAll(rr.Result().Body)
	return rr.Code, raw
}

// createSession creates a session as c with a body naming bob as userId and
// returns its id.
func (c isoCaller) createSession(t *testing.T, h http.Handler) string {
	t.Helper()
	code, raw := c.do(t, h, http.MethodPost, "/v1/sessions", `{"runtimeRef":"claude-code","userId":"bob@acme.com"}`)
	if code != http.StatusCreated {
		t.Fatalf("create as %s = %d %s, want 201", c.user, code, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		t.Fatalf("decode create %s: %v", raw, err)
	}
	return out.ID
}

// sessionOwner reads the userId c sees on session id.
func sessionOwner(t *testing.T, h http.Handler, c isoCaller, id string) string {
	t.Helper()
	code, raw := c.do(t, h, http.MethodGet, "/v1/sessions/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s as %s = %d %s, want 200", id, c.user, code, raw)
	}
	var out struct {
		UserID string `json:"userId"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.UserID
}

// listedIDs returns the session ids c's GET /v1/sessions lists.
func listedIDs(t *testing.T, h http.Handler, c isoCaller) map[string]bool {
	t.Helper()
	code, raw := c.do(t, h, http.MethodGet, "/v1/sessions", "")
	if code != http.StatusOK {
		t.Fatalf("list as %s = %d %s", c.user, code, raw)
	}
	var env struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode list %s: %v", raw, err)
	}
	out := map[string]bool{}
	for _, it := range env.Items {
		out[it.ID] = true
	}
	return out
}

// diagnosis: a user reached a session it does not own. Either bob, a
// `user`, read, messaged, terminated, or listed alice's session; or bob
// holding a custom role with the tenant read bypass messaged or terminated
// it, which the bypass never grants; or a create body's userId chose the
// owner. The ownership check is requireSessionAccess and SessionAccessible
// in pkg/gateway/sessionserver/rbac_gate.go and the list filter in
// session_read.go. Every refusal must be the missing-session 404, so it
// does not reveal that the session exists.
// spec: §10.2 (Session ownership) rules 1, 4, 5, 6, and 7
func TestSessionOwnerIsolation_spec_10_2(t *testing.T) {
	h := ownerIsolationGateway(t)
	alice := isoCaller{user: isoAlice, role: "user"}
	bob := isoCaller{user: isoBob, role: "user"}
	reader := isoCaller{user: isoBob, custom: true}

	id := alice.createSession(t, h)
	if owner := sessionOwner(t, h, alice, id); owner != isoAlice {
		t.Fatalf("owner of alice's session = %q, want %s; the body userId set it", owner, isoAlice)
	}
	missCode, missBody := bob.do(t, h, http.MethodGet, "/v1/sessions/sess_missing", "")
	if missCode != http.StatusNotFound {
		t.Fatalf("missing session = %d %s, want 404", missCode, missBody)
	}
	refusals := []struct {
		caller       isoCaller
		method, path string
	}{
		{bob, http.MethodGet, "/v1/sessions/" + id},
		{bob, http.MethodGet, "/v1/sessions/" + id + "/messages"},
		{bob, http.MethodPost, "/v1/sessions/" + id + "/messages"},
		{bob, http.MethodPost, "/v1/sessions/" + id + "/terminate"},
		{reader, http.MethodPost, "/v1/sessions/" + id + "/messages"},
		{reader, http.MethodPost, "/v1/sessions/" + id + "/terminate"},
	}
	for _, r := range refusals {
		code, raw := r.caller.do(t, h, r.method, r.path, `{"input":[{"type":"text","inline":"hi"}]}`)
		if code != http.StatusNotFound || !bytes.Equal(raw, missBody) {
			t.Errorf("%s %s as %s (custom %v) = %d %s, want the missing-session 404 %s",
				r.method, r.path, r.caller.user, r.caller.custom, code, raw, missBody)
		}
	}
	if listedIDs(t, h, bob)[id] {
		t.Error("bob/user lists alice's session")
	}
	if owner := sessionOwner(t, h, reader, id); owner != isoAlice {
		t.Errorf("custom-role bob reads owner %q, want %s", owner, isoAlice)
	}
	if !listedIDs(t, h, reader)[id] {
		t.Error("custom-role bob does not list alice's session")
	}
	own := reader.createSession(t, h)
	if owner := sessionOwner(t, h, reader, own); owner != isoBob {
		t.Errorf("custom-role bob's own session owner = %q, want %s", owner, isoBob)
	}
	if owner := sessionOwner(t, h, alice, id); owner != isoAlice {
		t.Errorf("alice's session owner after the refusals = %q", owner)
	}
}
