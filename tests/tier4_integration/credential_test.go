// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 / §15.1 end-user credential
// lifecycle end-to-end through the real cmd/lenny-gateway binary. It
// exercises the /v1/credentials surface — register, list, rotate,
// revoke, delete — and asserts the §15.1 secret-free wire contract
// and the per-caller scoping hold through one process.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// credClient drives the live gateway's §15.1 /v1/credentials surface.
type credClient struct {
	t    *testing.T
	base string
}

// do issues a credential request as the given tenant + user and
// returns the status and decoded body.
func (c credClient) do(method, path, tenant, user string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-User-ID", user)
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

// spec: 4.9 (end-user credential lifecycle through the gateway)
// diagnosis: the §4.9 / §15.1 /v1/credentials lifecycle diverged
//
//	through the real cmd/lenny-gateway binary. Register, list,
//	rotate, revoke, or delete on the end-user credential
//	registry did not behave as §15.1 specifies, or the §15.1
//	secret-free wire contract was violated, when driven through
//	one process. The §4.9 runtime-side AssignCredentials fan-out
//	is exercised separately — it needs a pod the integration
//	harness does not provide.
func TestCredentialLifecycle(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := credClient{t: t, base: gw.BaseURL()}

	// ---- register a §4.9 credential ----
	code, registered := c.do(http.MethodPost, "/v1/credentials", "acme", "alice@acme.com", map[string]any{
		"provider": "anthropic_direct",
		"secret":   "sk-ant-secret-value",
	})
	if code != http.StatusCreated {
		t.Fatalf("register credential: status %d (%v)", code, registered)
	}
	ref, _ := registered["ref"].(string)
	if ref == "" {
		t.Fatal("register returned no credential ref")
	}
	if registered["status"] != "active" {
		t.Errorf("registered credential status = %v, want active", registered["status"])
	}
	// §15.1: the credential wire payload never carries secret material.
	if _, leaked := registered["secret"]; leaked {
		t.Error("the credential payload leaked the secret value")
	}

	// ---- list: the credential is enumerated for the caller ----
	code, list := c.do(http.MethodGet, "/v1/credentials", "acme", "alice@acme.com", nil)
	if code != http.StatusOK {
		t.Fatalf("list credentials: status %d", code)
	}
	creds, _ := list["credentials"].([]any)
	if len(creds) != 1 {
		t.Fatalf("listed credentials = %v, want exactly 1", list["credentials"])
	}
	c0, _ := creds[0].(map[string]any)
	if c0["ref"] != ref {
		t.Errorf("listed credential ref = %v, want %v", c0["ref"], ref)
	}
	if _, leaked := c0["secret"]; leaked {
		t.Error("the listed credential leaked the secret value")
	}

	// ---- rotate: a new secret is installed; status stays active ----
	code, rotated := c.do(http.MethodPut, "/v1/credentials/"+ref, "acme", "alice@acme.com", map[string]any{
		"secret": "sk-ant-rotated-value",
	})
	if code != http.StatusOK {
		t.Fatalf("rotate credential: status %d (%v)", code, rotated)
	}
	if rotated["status"] != "active" {
		t.Errorf("rotated credential status = %v, want active", rotated["status"])
	}
	if rotated["rotatedAt"] == "" || rotated["rotatedAt"] == nil {
		t.Error("rotate did not stamp rotatedAt")
	}

	// ---- revoke: the credential transitions to revoked ----
	code, revoked := c.do(http.MethodPost, "/v1/credentials/"+ref+"/revoke", "acme", "alice@acme.com", nil)
	if code != http.StatusOK {
		t.Fatalf("revoke credential: status %d (%v)", code, revoked)
	}
	if revoked["status"] != "revoked" {
		t.Errorf("revoked credential status = %v, want revoked", revoked["status"])
	}
	if revoked["revokedAt"] == "" || revoked["revokedAt"] == nil {
		t.Error("revoke did not stamp revokedAt")
	}

	// ---- delete: the credential is removed from the registry ----
	code, _ = c.do(http.MethodDelete, "/v1/credentials/"+ref, "acme", "alice@acme.com", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete credential: want 204, got %d", code)
	}
	code, afterDelete := c.do(http.MethodGet, "/v1/credentials", "acme", "alice@acme.com", nil)
	if code != http.StatusOK {
		t.Fatalf("list after delete: status %d", code)
	}
	if creds, _ := afterDelete["credentials"].([]any); len(creds) != 0 {
		t.Errorf("credentials after delete = %v, want empty", afterDelete["credentials"])
	}
}

// spec: 4.9 (credential rotation through the gateway)
// diagnosis: §4.9 credential rotation diverged through the real
//
//	cmd/lenny-gateway binary. PUT /v1/credentials/{ref} did not
//	install the new secret, return the credential to active, or
//	stamp rotatedAt as §15.1 specifies. The fault-driven
//	rotation pipeline and the lifecycle-channel credentials_
//	rotated emission run against a pod the integration harness
//	does not provide; this exercises the user-facing rotation
//	endpoint that triggers them.
func TestCredentialRotation(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := credClient{t: t, base: gw.BaseURL()}

	code, registered := c.do(http.MethodPost, "/v1/credentials", "acme", "bob@acme.com", map[string]any{
		"provider": "github",
		"secret":   "ghp-original",
	})
	if code != http.StatusCreated {
		t.Fatalf("register credential: status %d (%v)", code, registered)
	}
	ref, _ := registered["ref"].(string)

	// §4.9: rotation installs a new secret and leaves the credential
	// active and usable for lease assignment.
	code, rotated := c.do(http.MethodPut, "/v1/credentials/"+ref, "acme", "bob@acme.com", map[string]any{
		"secret": "ghp-rotated",
	})
	if code != http.StatusOK {
		t.Fatalf("rotate credential: status %d (%v)", code, rotated)
	}
	if rotated["status"] != "active" {
		t.Errorf("rotated status = %v, want active", rotated["status"])
	}
	if rotated["rotatedAt"] == "" || rotated["rotatedAt"] == nil {
		t.Error("rotate did not stamp rotatedAt")
	}

	// §15.1: a rotate with an empty secret is a validation error.
	code, _ = c.do(http.MethodPut, "/v1/credentials/"+ref, "acme", "bob@acme.com", map[string]any{
		"secret": "",
	})
	if code != http.StatusBadRequest {
		t.Errorf("rotate with empty secret: want 400, got %d", code)
	}

	// §15.1: rotating an unknown ref returns 404, never leaking the
	// existence of another credential.
	code, _ = c.do(http.MethodPut, "/v1/credentials/cred_does_not_exist", "acme", "bob@acme.com", map[string]any{
		"secret": "x",
	})
	if code != http.StatusNotFound {
		t.Errorf("rotate unknown ref: want 404, got %d", code)
	}
}

// spec: 4.9 (emergency credential revocation through the gateway)
// diagnosis: §4.9 credential revocation diverged through the real
//
//	cmd/lenny-gateway binary. POST /v1/credentials/{ref}/revoke
//	did not transition the credential to revoked, or a caller
//	revoked a credential they do not own. The cross-replica
//	revocation propagation via the Redis EventBus and the
//	pod-side full_revoke fan-out run against infrastructure the
//	integration harness does not provide; this exercises the
//	user-facing revocation endpoint that drives them.
func TestCredentialRevocation(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := credClient{t: t, base: gw.BaseURL()}

	code, registered := c.do(http.MethodPost, "/v1/credentials", "acme", "carol@acme.com", map[string]any{
		"provider": "anthropic_direct",
		"secret":   "sk-ant-to-revoke",
	})
	if code != http.StatusCreated {
		t.Fatalf("register credential: status %d (%v)", code, registered)
	}
	ref, _ := registered["ref"].(string)

	// §4.9: revocation transitions the credential to revoked so no
	// further lease assignment can draw on it.
	code, revoked := c.do(http.MethodPost, "/v1/credentials/"+ref+"/revoke", "acme", "carol@acme.com", nil)
	if code != http.StatusOK {
		t.Fatalf("revoke credential: status %d (%v)", code, revoked)
	}
	if revoked["status"] != "revoked" {
		t.Errorf("revoked status = %v, want revoked", revoked["status"])
	}

	// §15.1: a different user must not revoke another user's
	// credential — the cross-user attempt returns 404 so the
	// credential's existence does not leak.
	code, _ = c.do(http.MethodPost, "/v1/credentials/"+ref+"/revoke", "acme", "dave@acme.com", nil)
	if code != http.StatusNotFound {
		t.Errorf("cross-user revoke: want 404, got %d", code)
	}

	// §15.1: revoking an unknown ref returns 404.
	code, _ = c.do(http.MethodPost, "/v1/credentials/cred_does_not_exist/revoke", "acme", "carol@acme.com", nil)
	if code != http.StatusNotFound {
		t.Errorf("revoke unknown ref: want 404, got %d", code)
	}
}
