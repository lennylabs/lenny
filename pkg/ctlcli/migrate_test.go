// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"testing"
)

// spec: §24.13 line 150 — `migrate status` maps to
// GET /v1/admin/schema/migrations/status.
func TestMigrateStatusMapsToEndpoint(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"currentVersion":3,"dirty":false}`,
		"migrate", "status")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/schema/migrations/status" {
		t.Errorf("request: %s %s", got.method, got.path)
	}
}

// spec: §24.13 line 151 — `migrate down --version N --confirm` maps to
// POST /v1/admin/schema/migrations/{N}/down with {confirm:true}.
func TestMigrateDownMapsToEndpoint(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"version":3,"dirtyFlagCleared":true}`,
		"migrate", "down", "--version", "3", "--confirm", "--reason", "view dep")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/schema/migrations/3/down" {
		t.Fatalf("request: %s %s", got.method, got.path)
	}
	if got.body["confirm"] != true {
		t.Errorf("body confirm: %+v", got.body)
	}
	if got.body["reason"] != "view dep" {
		t.Errorf("body reason: %+v", got.body)
	}
}

// `migrate down` without --confirm fails before any request (the
// operation is destructive). spec: §24.13 line 151.
func TestMigrateDownRequiresConfirm(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"migrate", "down", "--version", "3"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --confirm: exit code %d, want 2", code)
	}
}

// `migrate down` without --version fails before any request.
func TestMigrateDownRequiresVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"migrate", "down", "--confirm"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing --version: exit code %d, want 2", code)
	}
}
