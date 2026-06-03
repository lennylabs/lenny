// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net/http"
	"testing"
)

// spec: §24.8 line 113 — `lenny-ctl admin external-adapters validate
// --name <name>` issues POST /v1/admin/external-adapters/{name}/validate.
func TestExternalAdaptersValidateTargetsGateway(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"acme-a2a","status":"active"}`,
		"admin", "external-adapters", "validate", "--name", "acme-a2a")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/external-adapters/acme-a2a/validate" {
		t.Errorf("request = %s %s, want POST /v1/admin/external-adapters/acme-a2a/validate", got.method, got.path)
	}
}

// spec: §24.8 — a failing validation (HTTP 422) must exit non-zero so CI
// gates can branch on it.
func TestExternalAdaptersValidateFailExitsNonZero(t *testing.T) {
	code, _ := runAgainstGateway(t, http.StatusUnprocessableEntity,
		`{"error":{"code":"ADAPTER_VALIDATION_FAILED","message":"failed"}}`,
		"admin", "external-adapters", "validate", "--name", "acme-a2a")
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero on validation_failed")
	}
}

func TestExternalAdaptersValidateRequiresName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "external-adapters", "validate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestExternalAdaptersList(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"externalAdapters":[]}`,
		"admin", "external-adapters", "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/external-adapters" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
}

func TestExternalAdaptersGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"acme-a2a"}`,
		"admin", "external-adapters", "get", "acme-a2a")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.path != "/v1/admin/external-adapters/acme-a2a" {
		t.Errorf("path = %s", got.path)
	}
}

func TestExternalAdaptersRegisterSendsBody(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusCreated, `{"name":"acme-a2a","status":"pending_validation"}`,
		"admin", "external-adapters", "register",
		"--name", "acme-a2a", "--binary-path", "/usr/local/bin/acme-a2a", "--level", "standard", "--protocol", "a2a")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/external-adapters" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
	if got.body["name"] != "acme-a2a" || got.body["binaryPath"] != "/usr/local/bin/acme-a2a" ||
		got.body["level"] != "standard" || got.body["protocol"] != "a2a" {
		t.Errorf("body = %+v", got.body)
	}
}

func TestExternalAdaptersUpdateTargetsName(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"acme-a2a"}`,
		"admin", "external-adapters", "update", "--name", "acme-a2a", "--display-name", "Acme")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/external-adapters/acme-a2a" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
}

func TestExternalAdaptersDelete(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusNoContent, ``,
		"admin", "external-adapters", "delete", "acme-a2a")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.method != http.MethodDelete || got.path != "/v1/admin/external-adapters/acme-a2a" {
		t.Errorf("request = %s %s", got.method, got.path)
	}
}

func TestExternalAdaptersUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "external-adapters", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestExternalAdaptersNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "external-adapters"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
