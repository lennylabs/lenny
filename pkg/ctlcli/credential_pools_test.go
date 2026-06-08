// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"testing"
)

// spec: §24.5 rows 1-8 — `lenny-ctl admin credential-pools …` maps 1:1
// to the §15.1 /v1/admin/credential-pools routes.

func TestCredentialPoolsList(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"credentialPools":[]}`,
		"admin", "credential-pools", "list")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/credential-pools" {
		t.Errorf("request %s %s, want GET /v1/admin/credential-pools", got.method, got.path)
	}
}

func TestCredentialPoolsListWithTenant(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"credentialPools":[]}`,
		"admin", "credential-pools", "list", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.query != "tenantId=acme" {
		t.Errorf("query %q, want tenantId=acme", got.query)
	}
}

func TestCredentialPoolsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"claude-prod"}`,
		"admin", "credential-pools", "get", "--pool", "claude-prod")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/credential-pools/claude-prod" {
		t.Errorf("request %s %s", got.method, got.path)
	}
}

func TestCredentialPoolsGetRequiresPool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "credential-pools", "get"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --pool: exit code %d, want 2", code)
	}
}

func TestCredentialPoolsAddCredential(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusCreated, `{"name":"claude-prod"}`,
		"admin", "credential-pools", "add-credential", "--pool", "claude-prod",
		"--id", "key-3", "--secret-ref", "lenny-system/k3")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/credential-pools/claude-prod/credentials" {
		t.Errorf("request %s %s", got.method, got.path)
	}
	if got.body["id"] != "key-3" || got.body["secretRef"] != "lenny-system/k3" {
		t.Errorf("body %+v, want id=key-3 secretRef=lenny-system/k3", got.body)
	}
}

func TestCredentialPoolsAddCredentialRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "credential-pools", "add-credential", "--pool", "claude-prod"},
		&stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --id: exit code %d, want 2", code)
	}
}

func TestCredentialPoolsUpdateCredential(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"claude-prod"}`,
		"admin", "credential-pools", "update-credential", "--pool", "claude-prod",
		"--credential", "key-1", "--secret-ref", "lenny-system/k1-rotated")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/credential-pools/claude-prod/credentials/key-1" {
		t.Errorf("request %s %s", got.method, got.path)
	}
	if got.body["secretRef"] != "lenny-system/k1-rotated" {
		t.Errorf("body %+v", got.body)
	}
}

func TestCredentialPoolsRemoveCredential(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"claude-prod"}`,
		"admin", "credential-pools", "remove-credential", "--pool", "claude-prod",
		"--credential", "key-1")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/credential-pools/claude-prod/credentials/key-1" {
		t.Errorf("request %s %s", got.method, got.path)
	}
}

func TestCredentialPoolsRevokeCredential(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"revokedCredential":"key-1"}`,
		"admin", "credential-pools", "revoke-credential", "--pool", "claude-prod",
		"--credential", "key-1", "--reason", "suspected_exfiltration")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodPost ||
		got.path != "/v1/admin/credential-pools/claude-prod/credentials/key-1/revoke" {
		t.Errorf("request %s %s", got.method, got.path)
	}
	if got.body["reason"] != "suspected_exfiltration" {
		t.Errorf("body %+v, want reason=suspected_exfiltration", got.body)
	}
}

func TestCredentialPoolsRevokePool(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"revokedCredentials":["key-1","key-2"]}`,
		"admin", "credential-pools", "revoke-pool", "--pool", "claude-prod", "--reason", "pool_compromise")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/credential-pools/claude-prod/revoke" {
		t.Errorf("request %s %s", got.method, got.path)
	}
	if got.body["reason"] != "pool_compromise" {
		t.Errorf("body %+v", got.body)
	}
}

func TestCredentialPoolsReEnable(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"reEnabledCredential":"key-1"}`,
		"admin", "credential-pools", "re-enable", "--pool", "claude-prod",
		"--credential", "key-1", "--reason", "false_alarm")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if got.method != http.MethodPost ||
		got.path != "/v1/admin/credential-pools/claude-prod/credentials/key-1/re-enable" {
		t.Errorf("request %s %s", got.method, got.path)
	}
}

func TestCredentialPoolsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "credential-pools", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown subcommand: exit code %d, want 2", code)
	}
}
