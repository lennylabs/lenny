// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §10.6 Environment resource end-to-end
// through the real cmd/lenny-gateway binary. It exercises the admin
// /v1/admin/environments CRUD surface and the OIDC-group-aware
// environment-resolver middleware that drives §10.6 transparent
// filtering on the §9.1 GET /v1/runtimes discovery endpoint.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// envClient issues dev-mode requests against the live gateway. roles
// is the comma-separated X-Lenny-Roles value and groups the
// comma-separated X-Lenny-Groups value, so a test can drive both the
// platform-admin admin surface and a group-bearing end-user.
type envClient struct {
	t    *testing.T
	base string
}

func (c envClient) do(method, path, tenant, user, roles, groups string, body any) (int, map[string]any) {
	return c.doIfMatch(method, path, tenant, user, roles, groups, "", body)
}

// doIfMatch is do() plus the §15.1 If-Match precondition header. The
// environments resource enforces ETag optimistic concurrency, so an admin
// PUT carries the current entity tag. spec: §15.1 lines 1207-1211.
func (c envClient) doIfMatch(method, path, tenant, user, roles, groups, ifMatch string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	if user != "" {
		req.Header.Set("X-Lenny-User-ID", user)
	}
	if roles != "" {
		req.Header.Set("X-Lenny-Roles", roles)
	}
	if groups != "" {
		req.Header.Set("X-Lenny-Groups", groups)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// spec: 10.6 (Environment admin CRUD + OIDC-group environment resolver)
// diagnosis: a §10.6 environment admin endpoint or the
//
//	environment-resolver middleware failed through the real
//	cmd/lenny-gateway binary. The /v1/admin/environments CRUD
//	surface, the manage_environments permission gate, or the
//	OIDC-group membership resolution did not behave as §10.6
//	specifies when driven through one process.
func TestEnvironmentResource(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := envClient{t: t, base: gw.BaseURL()}

	// ---- create: a §10.6 environment scoped to an OIDC group ----
	create := map[string]any{
		"name":        "security-team",
		"tenantId":    "acme",
		"description": "security engineering workspace",
		"members": []map[string]any{{
			"identity": map[string]any{"type": "oidc-group", "value": "security-engineers"},
			"role":     "creator",
		}},
		"runtimeSelector": map[string]any{"matchLabels": map[string]string{"team": "security"}},
	}
	code, created := c.do(http.MethodPost, "/v1/admin/environments", "platform", "ops@acme.com", "platform-admin", "", create)
	if code != http.StatusCreated {
		t.Fatalf("create environment: status %d (%v)", code, created)
	}
	if created["name"] != "security-team" {
		t.Errorf("created environment name = %v", created["name"])
	}
	if created["tenantId"] != "acme" {
		t.Errorf("created environment tenantId = %v, want acme", created["tenantId"])
	}

	// ---- create requires the §10.2 manage_environments permission ----
	code, _ = c.do(http.MethodPost, "/v1/admin/environments", "acme", "alice@acme.com", "", "", create)
	if code != http.StatusForbidden {
		t.Errorf("create without manage_environments: want 403, got %d", code)
	}

	// ---- get: the stored environment round-trips ----
	code, got := c.do(http.MethodGet, "/v1/admin/environments/security-team?tenantId=acme",
		"platform", "ops@acme.com", "platform-admin", "", nil)
	if code != http.StatusOK {
		t.Fatalf("get environment: status %d", code)
	}
	members, _ := got["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("environment members = %v", got["members"])
	}
	m0, _ := members[0].(map[string]any)
	identity, _ := m0["identity"].(map[string]any)
	if identity["value"] != "security-engineers" {
		t.Errorf("member identity = %v", identity)
	}

	// ---- list: the environment is enumerated for the tenant ----
	code, list := c.do(http.MethodGet, "/v1/admin/environments?tenantId=acme",
		"platform", "ops@acme.com", "platform-admin", "", nil)
	if code != http.StatusOK {
		t.Fatalf("list environments: status %d", code)
	}
	if envs, _ := list["environments"].([]any); len(envs) != 1 {
		t.Errorf("listed environments = %v", list["environments"])
	}

	// ---- update: the member role is reassigned ----
	update := map[string]any{
		"name":        "security-team",
		"tenantId":    "acme",
		"description": "security engineering workspace",
		"members": []map[string]any{{
			"identity": map[string]any{"type": "oidc-group", "value": "security-engineers"},
			"role":     "admin",
		}},
		"runtimeSelector": map[string]any{"matchLabels": map[string]string{"team": "security"}},
	}
	// spec: §15.1 lines 1207-1211 — the PUT requires If-Match; the entity
	// tag rides the GET body's etag field (it is also on the ETag header).
	ifMatch, _ := got["etag"].(string)
	code, updated := c.doIfMatch(http.MethodPut, "/v1/admin/environments/security-team",
		"platform", "ops@acme.com", "platform-admin", "", ifMatch, update)
	if code != http.StatusOK {
		t.Fatalf("update environment: status %d (%v)", code, updated)
	}
	upMembers, _ := updated["members"].([]any)
	upM0, _ := upMembers[0].(map[string]any)
	if upM0["role"] != "admin" {
		t.Errorf("updated member role = %v, want admin", upM0["role"])
	}

	// ---- delete: the environment is removed ----
	code, _ = c.do(http.MethodDelete, "/v1/admin/environments/security-team?tenantId=acme",
		"platform", "ops@acme.com", "platform-admin", "", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete environment: want 204, got %d", code)
	}
	code, _ = c.do(http.MethodGet, "/v1/admin/environments/security-team?tenantId=acme",
		"platform", "ops@acme.com", "platform-admin", "", nil)
	if code != http.StatusNotFound {
		t.Errorf("get after delete: want 404, got %d", code)
	}
}

// spec: 10.6 (transparent filtering: a caller sees only the runtimes their environments authorize)
// diagnosis: the §10.6 environment-resolver middleware did not narrow
// GET /v1/runtimes to the union of runtimes the caller's environments
// authorize. The environment middleware, the envaccess runtimeSelector
// match, or the OIDC-group membership resolution diverged from §10.6
// transparent filtering when driven through the real gateway binary.
func TestEnvironmentFiltering(t *testing.T) {
	// deny-all is the §10.6 platform default: a caller with no
	// environment membership sees no runtimes. The transparent filter
	// is only observable against deny-all.
	gw := gateway.StartWith(t, "--dev-mode", "--no-environment-policy", "deny-all")
	c := envClient{t: t, base: gw.BaseURL()}

	// ---- bootstrap two runtimes with distinct §5.1 labels ----
	code, _ := c.do(http.MethodPost, "/v1/admin/bootstrap", "acme", "ops@acme.com", "platform-admin", "", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{
			{"name": "security-scanner", "image": "lenny/sec@sha256:aaa", "labels": map[string]string{"team": "security"}},
			{"name": "billing-agent", "image": "lenny/bill@sha256:bbb", "labels": map[string]string{"team": "finance"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap runtimes: status %d", code)
	}

	// ---- an environment authorizing only the team:security runtime ----
	code, _ = c.do(http.MethodPost, "/v1/admin/environments", "platform", "ops@acme.com", "platform-admin", "", map[string]any{
		"name":     "security-team",
		"tenantId": "acme",
		"members": []map[string]any{{
			"identity": map[string]any{"type": "oidc-group", "value": "security-engineers"},
			"role":     "creator",
		}},
		"runtimeSelector": map[string]any{"matchLabels": map[string]string{"team": "security"}},
	})
	if code != http.StatusCreated {
		t.Fatalf("create environment: status %d", code)
	}

	// ---- a caller in the security-engineers group sees only the
	//      runtime that environment's runtimeSelector authorizes ----
	code, member := c.do(http.MethodGet, "/v1/runtimes", "acme", "alice@acme.com", "", "security-engineers", nil)
	if code != http.StatusOK {
		t.Fatalf("member runtime discovery: status %d", code)
	}
	memberNames := runtimeNames(member)
	if len(memberNames) != 1 || memberNames[0] != "security-scanner" {
		t.Errorf("member sees runtimes %v, want only [security-scanner]", memberNames)
	}

	// ---- a caller with no environment membership sees nothing under
	//      the §10.6 deny-all default ----
	code, outsider := c.do(http.MethodGet, "/v1/runtimes", "acme", "bob@acme.com", "", "", nil)
	if code != http.StatusOK {
		t.Fatalf("outsider runtime discovery: status %d", code)
	}
	if names := runtimeNames(outsider); len(names) != 0 {
		t.Errorf("outsider with no environment membership sees runtimes %v, want none", names)
	}
}

// runtimeNames extracts the runtime names from a GET /v1/runtimes
// discovery response body.
func runtimeNames(body map[string]any) []string {
	rows, _ := body["runtimes"].([]any)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if name, _ := m["name"].(string); name != "" {
			out = append(out, name)
		}
	}
	return out
}
