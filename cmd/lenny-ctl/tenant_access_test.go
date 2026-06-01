// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRuntimesGrantAccess_spec_24_3 covers `lenny-ctl admin runtimes
// grant-access --runtime <name> --tenant <id>` mapping to
// POST /v1/admin/runtimes/{name}/tenant-access with a {tenantId} body.
func TestRuntimesGrantAccess_spec_24_3(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusCreated, `{"resource":"claude-code","tenantId":"acme"}`,
		"admin", "runtimes", "grant-access", "--runtime", "claude-code", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/runtimes/claude-code/tenant-access" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
	if got.body["tenantId"] != "acme" {
		t.Errorf("body: %+v, want tenantId=acme", got.body)
	}
}

// TestRuntimesListAccess_spec_24_3 covers `list-access --runtime <name>`
// mapping to GET /v1/admin/runtimes/{name}/tenant-access.
func TestRuntimesListAccess_spec_24_3(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenantAccess":[]}`,
		"admin", "runtimes", "list-access", "--runtime", "claude-code")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/runtimes/claude-code/tenant-access" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

// TestRuntimesRevokeAccess_spec_24_3 covers `revoke-access --runtime
// <name> --tenant <id>` mapping to DELETE
// /v1/admin/runtimes/{name}/tenant-access/{tenantId}.
func TestRuntimesRevokeAccess_spec_24_3(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusNoContent, ``,
		"admin", "runtimes", "revoke-access", "--runtime", "claude-code", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/runtimes/claude-code/tenant-access/acme" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

// TestRuntimesGrantAccessRequiresFlags_spec_24_3 asserts the spec'd
// required flags are validated before any request: grant/revoke need
// both --runtime and --tenant, list needs --runtime.
func TestRuntimesGrantAccessRequiresFlags_spec_24_3(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"grant missing tenant", []string{"admin", "runtimes", "grant-access", "--runtime", "rt"}},
		{"grant missing runtime", []string{"admin", "runtimes", "grant-access", "--tenant", "acme"}},
		{"revoke missing tenant", []string{"admin", "runtimes", "revoke-access", "--runtime", "rt"}},
		{"list missing runtime", []string{"admin", "runtimes", "list-access"}},
		{"unknown flag", []string{"admin", "runtimes", "grant-access", "--runtime", "rt", "--bogus", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 2 {
				t.Errorf("exit code: got %d, want 2 (stderr=%q)", code, stderr.String())
			}
		})
	}
}

// TestParseGlobalFlagsSection2416 asserts the §24.16 cross-command
// flags parse into globalFlags with the documented defaults and values.
func TestParseGlobalFlagsSection2416(t *testing.T) {
	clearCLIEnv(t)
	f, rest := parseGlobalFlags([]string{
		"--timeout", "5",
		"--insecure-skip-verify",
		"--output", "json",
		"--quiet",
		"admin", "tenants", "list",
	})
	if f.timeout != 5*time.Second {
		t.Errorf("timeout: %v, want 5s", f.timeout)
	}
	if !f.insecure {
		t.Error("insecure: want true")
	}
	if f.output != "json" {
		t.Errorf("output: %q, want json", f.output)
	}
	if !f.quiet {
		t.Error("quiet: want true")
	}
	if len(rest) != 3 || rest[0] != "admin" {
		t.Errorf("rest: %v", rest)
	}
}

// TestParseGlobalFlagsDefaultsSection2416 asserts the timeout and output
// defaults when the §24.16 flags are absent.
func TestParseGlobalFlagsDefaultsSection2416(t *testing.T) {
	clearCLIEnv(t)
	f, _ := parseGlobalFlags([]string{"health"})
	if f.timeout != 30*time.Second {
		t.Errorf("default timeout: %v, want 30s", f.timeout)
	}
	if f.output != "json" {
		t.Errorf("default output: %q, want json", f.output)
	}
	if f.insecure || f.quiet {
		t.Errorf("default insecure/quiet: %v/%v, want false/false", f.insecure, f.quiet)
	}
}

// TestRunRejectsUnsupportedOutput_spec_24_16 asserts only `--output
// json` is accepted; any other format exits 2 before a request runs.
func TestRunRejectsUnsupportedOutput_spec_24_16(t *testing.T) {
	clearCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--output", "yaml", "admin", "tenants", "list"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported --output") {
		t.Errorf("stderr: %q, want unsupported --output message", stderr.String())
	}
}

// TestBootstrapQuietSuppressesWaiting_spec_24_16 asserts --quiet
// suppresses the informational "waiting up to …" progress line while
// the command still completes. The fake gateway answers /healthz 200
// immediately so the readiness poll exits on the first iteration.
func TestBootstrapQuietSuppressesWaiting_spec_24_16(t *testing.T) {
	clearCLIEnv(t)
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(seed, []byte("tenants: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	run2 := func(quiet bool) string {
		var stdout, stderr bytes.Buffer
		args := []string{"--api-url", srv.URL}
		if quiet {
			args = append(args, "--quiet")
		}
		args = append(args, "bootstrap", "--from-values", seed, "--wait-timeout", "5")
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("bootstrap exit code: got %d, want 0 (stderr=%q)", code, stderr.String())
		}
		return stderr.String()
	}

	if got := run2(true); strings.Contains(got, "waiting up to") {
		t.Errorf("--quiet stderr still has waiting line: %q", got)
	}
	if got := run2(false); !strings.Contains(got, "waiting up to") {
		t.Errorf("non-quiet stderr missing waiting line: %q", got)
	}
}
