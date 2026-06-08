// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §24.9 line 119; §17.6 line 472 — `lenny-ctl admin users
// rotate-token` calls the gateway's rotate-token endpoint.

// TestRotateTokenCallsGatewayEndpoint checks the happy path: the CLI
// POSTs to the gateway rotate-token route and reports success, including
// the Secret location returned by the gateway.
func TestRotateTokenCallsGatewayEndpoint(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK,
		`{"secretCreated":true,"secretNamespace":"lenny-system","secretName":"lenny-admin-token","username":"lenny-admin"}`,
		"--token", "old-tok-111",
		"admin", "users", "rotate-token", "--user", "lenny-admin")
	if code != 0 {
		t.Fatalf("rotate-token: exit %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/users/lenny-admin/rotate-token" {
		t.Fatalf("rotate-token request: %s %s, want POST /v1/admin/users/lenny-admin/rotate-token", got.method, got.path)
	}
}

// TestRotateTokenPrintsRetrieveCommand checks the operator is told how to
// read the new token from the Secret the gateway patched.
func TestRotateTokenPrintsRetrieveCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"secretCreated":true,"secretNamespace":"ops","secretName":"lenny-admin-token","username":"lenny-admin"}`))
	}))
	defer srv.Close()
	code := run([]string{"--api-url", srv.URL, "--token", "cur",
		"admin", "users", "rotate-token", "--user", "lenny-admin"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("rotate-token: exit %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "kubectl get secret lenny-admin-token -n ops") {
		t.Errorf("stdout %q, want the retrieve command for the returned Secret", stdout.String())
	}
}

// TestRotateTokenRequiresUser checks the --user flag is mandatory.
func TestRotateTokenRequiresUser(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--token", "cur", "admin", "users", "rotate-token"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("rotate-token without --user: exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--user") {
		t.Errorf("stderr %q, want a --user diagnostic", stderr.String())
	}
}

// TestRotateTokenRequiresAdminToken checks rotation fails fast when no
// caller token is configured (the platform-admin requirement of §24.9).
func TestRotateTokenRequiresAdminToken(t *testing.T) {
	clearCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "https://gw.example.com",
		"admin", "users", "rotate-token", "--user", "lenny-admin"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("rotate-token without a token: exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no admin token configured") {
		t.Errorf("stderr %q, want the admin-token diagnostic", stderr.String())
	}
}

// TestRotateTokenAPIErrorExits1 checks a non-2xx from the gateway exits 1.
func TestRotateTokenAPIErrorExits1(t *testing.T) {
	code, _ := runAgainstGateway(t, http.StatusForbidden,
		`{"error":{"code":"FORBIDDEN","message":"caller is not platform-admin"}}`,
		"--token", "cur",
		"admin", "users", "rotate-token", "--user", "lenny-admin")
	if code != 1 {
		t.Fatalf("rotate-token 403: exit %d, want 1", code)
	}
}

// TestAdminUsersUnknownSubcommand checks an unknown users subcommand is a
// usage error.
func TestAdminUsersUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--token", "cur", "admin", "users", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown users subcommand: exit %d, want 2", code)
	}
}
