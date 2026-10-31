// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 session owner isolation. A session is readable by its owner and by
// a principal whose role grants "Read other users' sessions (same tenant)",
// and actionable by its owner alone. The test runs the real cmd/lenny-gateway
// binary over Postgres through the live-gateway helper of
// erasure_isolation_test.go, and every caller authenticates with a signed
// bearer token through the gateway's own verifier. acme's session-reader
// custom role is registered through the tenant RBAC admin API, and bob
// holds it through the OIDC role claim of his bearer token, one of the two
// §10.2 role assignment sources. bob has no platform-managed assignment, so
// the claim is authoritative. The platform-managed source is not used here
// because the Postgres user store refuses a custom role name (F-10.2.33).

package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
)

const (
	isoAlice = "alice@acme.com"
	isoBob   = "bob@acme.com"
	isoCarol = "carol@acme.com"
	// isoCustomRole is the tenant custom role holding the read bypass
	// alongside the session permissions a user holds.
	isoCustomRole pkgauth.Role = "session-reader"
)

// isoCaller is one outside caller: a subject and the role its bearer token
// claims.
type isoCaller struct {
	sub  string
	role pkgauth.Role
}

// do sends one request to the live gateway as c and returns the status and
// the raw body, so a refusal compares byte for byte with a missing
// session's answer.
func (c isoCaller) do(t *testing.T, g *liveGateway, method, path, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, g.base+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.bearer(liveTenant, c.sub, c.role))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, raw
}

// createSession creates a session as c with a body naming bob as userId and
// returns its id.
func (c isoCaller) createSession(t *testing.T, g *liveGateway) string {
	t.Helper()
	code, raw := c.do(t, g, http.MethodPost, "/v1/sessions", `{"runtimeRef":"echo","userId":"bob@acme.com"}`)
	if code != http.StatusCreated {
		t.Fatalf("create as %s = %d %s, want 201", c.sub, code, raw)
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
func sessionOwner(t *testing.T, g *liveGateway, c isoCaller, id string) string {
	t.Helper()
	code, raw := c.do(t, g, http.MethodGet, "/v1/sessions/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s as %s/%s = %d %s, want 200", id, c.sub, c.role, code, raw)
	}
	var out struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode session %s: %v", raw, err)
	}
	return out.UserID
}

// listedIDs returns the session ids c's GET /v1/sessions lists.
func listedIDs(t *testing.T, g *liveGateway, c isoCaller) map[string]bool {
	t.Helper()
	code, raw := c.do(t, g, http.MethodGet, "/v1/sessions", "")
	if code != http.StatusOK {
		t.Fatalf("list as %s/%s = %d %s", c.sub, c.role, code, raw)
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

// registerSessionReader creates acme's session-reader custom role through
// the tenant RBAC admin API as carol, a tenant-admin.
func registerSessionReader(t *testing.T, g *liveGateway) {
	t.Helper()
	body := `{"name":"` + string(isoCustomRole) + `","permissions":["read_tenant_sessions","read_own_sessions","manage_own_sessions"]}`
	carol := isoCaller{sub: isoCarol, role: pkgauth.RoleTenantAdmin}
	if code, raw := carol.do(t, g, http.MethodPost, "/v1/admin/tenants/"+liveTenant+"/roles", body); code != http.StatusCreated {
		t.Fatalf("register custom role = %d %s, want 201", code, raw)
	}
}

// assertMissingAnswer checks that each request as c answers exactly the
// missing-session 404 body.
func assertMissingAnswer(t *testing.T, g *liveGateway, c isoCaller, id string, missing []byte) {
	t.Helper()
	requests := []struct{ method, path string }{
		{http.MethodGet, "/v1/sessions/" + id},
		{http.MethodGet, "/v1/sessions/" + id + "/messages"},
		{http.MethodPost, "/v1/sessions/" + id + "/messages"},
		{http.MethodPost, "/v1/sessions/" + id + "/terminate"},
	}
	for _, r := range requests {
		if c.role == isoCustomRole && r.method == http.MethodGet {
			continue
		}
		code, raw := c.do(t, g, r.method, r.path, `{"input":[{"type":"text","inline":"hi"}]}`)
		if code != http.StatusNotFound || !bytes.Equal(raw, missing) {
			t.Errorf("%s %s as %s/%s = %d %s, want the missing-session 404 %s",
				r.method, r.path, c.sub, c.role, code, raw, missing)
		}
	}
}

// diagnosis: a user reached a session it does not own on the real gateway.
// Either bob, a `user`, read, messaged, terminated, or listed alice's
// session; or bob holding a custom role with the tenant read bypass
// messaged or terminated it, which the bypass never grants, or was refused
// the read the bypass grants; or a create body's userId chose the owner.
// The ownership check is requireSessionAccess and SessionAccessible in
// pkg/gateway/sessionserver/rbac_gate.go and the list filter in
// session_read.go; the custom role reaches the principal through the
// platform role resolver and the custom-role store cmd/lenny-gateway wires
// into the session server. Every refusal must be the missing-session 404,
// so it does not reveal that the session exists.
// spec: §10.2 (Session ownership) rules 1, 4, 5, 6, and 7
func TestSessionOwnerIsolation_spec_10_2(t *testing.T) {
	g := startLiveGateway(t)
	registerSessionReader(t, g)
	alice := isoCaller{sub: isoAlice, role: pkgauth.RoleUser}
	bob := isoCaller{sub: isoBob, role: pkgauth.RoleUser}
	reader := isoCaller{sub: isoBob, role: isoCustomRole}

	id := alice.createSession(t, g)
	if owner := sessionOwner(t, g, alice, id); owner != isoAlice {
		t.Fatalf("owner of alice's session = %q, want %s; the body userId set it", owner, isoAlice)
	}
	missCode, missing := bob.do(t, g, http.MethodGet, "/v1/sessions/sess_missing", "")
	if missCode != http.StatusNotFound {
		t.Fatalf("missing session = %d %s, want 404", missCode, missing)
	}
	assertMissingAnswer(t, g, bob, id, missing)
	if listedIDs(t, g, bob)[id] {
		t.Error("bob/user lists alice's session")
	}

	assertMissingAnswer(t, g, reader, id, missing)
	if owner := sessionOwner(t, g, reader, id); owner != isoAlice {
		t.Errorf("custom-role bob reads owner %q, want %s", owner, isoAlice)
	}
	if !listedIDs(t, g, reader)[id] {
		t.Error("custom-role bob does not list alice's session")
	}
	own := reader.createSession(t, g)
	if owner := sessionOwner(t, g, reader, own); owner != isoBob {
		t.Errorf("custom-role bob's own session owner = %q, want %s", owner, isoBob)
	}
	if owner := sessionOwner(t, g, alice, id); owner != isoAlice {
		t.Errorf("alice's session owner after the refusals = %q", owner)
	}
	if code, raw := alice.do(t, g, http.MethodGet, "/v1/sessions/"+own, ""); code != http.StatusNotFound || !bytes.Equal(raw, missing) {
		t.Errorf("alice/user on bob's session = %d %s, want the missing-session 404", code, raw)
	}
}
