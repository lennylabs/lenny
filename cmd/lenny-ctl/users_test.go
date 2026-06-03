// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §24.9 line 119 — `lenny-ctl admin users rotate-token`.

// stubSecretPatcher replaces the kubectl patcher with a recorder for the
// duration of a test and returns the recorded call.
type patchCall struct {
	namespace string
	secret    string
	token     string
	called    bool
}

func stubSecretPatcher(t *testing.T, fail bool) *patchCall {
	t.Helper()
	rec := &patchCall{}
	orig := adminTokenSecretPatcher
	adminTokenSecretPatcher = func(namespace, secret, token string) error {
		rec.namespace, rec.secret, rec.token, rec.called = namespace, secret, token, true
		if fail {
			return errTest
		}
		return nil
	}
	t.Cleanup(func() { adminTokenSecretPatcher = orig })
	return rec
}

var errTest = &stubErr{"kubectl unavailable"}

type stubErr struct{ s string }

func (e *stubErr) Error() string { return e.s }

// TestRotateTokenExchangesAndPatches checks the happy path: the
// token-exchange request carries the RFC 8693 grant with the current
// token as subject_token, and the returned token is written to the
// lenny-admin-token Secret.
func TestRotateTokenExchangesAndPatches(t *testing.T) {
	rec := stubSecretPatcher(t, false)
	code, got := runAgainstGateway(t, http.StatusOK,
		`{"access_token":"new-tok-999","token_type":"Bearer","expires_in":3600}`,
		"--token", "old-tok-111",
		"admin", "users", "rotate-token", "--user", "lenny-admin")
	if code != 0 {
		t.Fatalf("rotate-token: exit %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/oauth/token" {
		t.Fatalf("rotate-token request: %s %s, want POST /v1/oauth/token", got.method, got.path)
	}
	if got.body["grant_type"] != grantTypeTokenExchange {
		t.Errorf("grant_type = %v, want %s", got.body["grant_type"], grantTypeTokenExchange)
	}
	if got.body["subject_token"] != "old-tok-111" {
		t.Errorf("subject_token = %v, want the current token", got.body["subject_token"])
	}
	if got.body["subject_token_type"] != tokenTypeJWT || got.body["requested_token_type"] != tokenTypeJWT {
		t.Errorf("token types = %v / %v, want %s", got.body["subject_token_type"], got.body["requested_token_type"], tokenTypeJWT)
	}
	if !rec.called || rec.token != "new-tok-999" {
		t.Errorf("secret patch: called=%v token=%q, want called with new-tok-999", rec.called, rec.token)
	}
	if rec.secret != adminTokenSecretName || rec.namespace != "lenny-system" {
		t.Errorf("secret patch target = %s/%s, want lenny-system/%s", rec.namespace, rec.secret, adminTokenSecretName)
	}
}

// TestRotateTokenCustomNamespace checks --namespace overrides the patch
// target namespace.
func TestRotateTokenCustomNamespace(t *testing.T) {
	rec := stubSecretPatcher(t, false)
	code, _ := runAgainstGateway(t, http.StatusOK, `{"access_token":"t2"}`,
		"--token", "cur",
		"admin", "users", "rotate-token", "--user", "lenny-admin", "--namespace", "ops")
	if code != 0 {
		t.Fatalf("rotate-token --namespace: exit %d, want 0", code)
	}
	if rec.namespace != "ops" {
		t.Errorf("patch namespace = %q, want ops", rec.namespace)
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

// TestRotateTokenSecretWriteFailurePrintsToken checks that when the
// Secret write fails after the server-side exchange, the new token and
// the manual patch command are surfaced (the old token is already
// invalid) and the command exits non-zero.
func TestRotateTokenSecretWriteFailurePrintsToken(t *testing.T) {
	stubSecretPatcher(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"rescued-tok"}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", srv.URL, "--token", "cur",
		"admin", "users", "rotate-token", "--user", "lenny-admin"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("secret write failure: exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "rescued-tok") {
		t.Errorf("the rotated token must be printed on a Secret write failure; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "kubectl patch secret") {
		t.Errorf("the manual patch command must be printed; stderr=%q", stderr.String())
	}
}

// TestRotateTokenAPIErrorExits1 checks a non-2xx from the token service
// exits 1 without attempting a Secret write.
func TestRotateTokenAPIErrorExits1(t *testing.T) {
	rec := stubSecretPatcher(t, false)
	code, _ := runAgainstGateway(t, http.StatusForbidden,
		`{"error":{"code":"invalid_client","message":"caller is not platform-admin"}}`,
		"--token", "cur",
		"admin", "users", "rotate-token", "--user", "lenny-admin")
	if code != 1 {
		t.Fatalf("token-exchange 403: exit %d, want 1", code)
	}
	if rec.called {
		t.Error("the Secret must not be patched when the exchange fails")
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
