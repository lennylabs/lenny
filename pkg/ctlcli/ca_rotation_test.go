// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"testing"
)

// spec: §10.3 lines 344-350 — `admin ca-rotation status` maps to
// GET /v1/admin/ca-rotation. F-10.3.21.
func TestAdminCARotationStatus(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"stage":"idle","currentCaId":"lenny-mtls-ca"}`,
		"admin", "ca-rotation", "status")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/ca-rotation" {
		t.Errorf("request: %s %s, want GET /v1/admin/ca-rotation", got.method, got.path)
	}
}

// `admin ca-rotation begin <newCaId>` POSTs {newCaId} to /begin.
func TestAdminCARotationBegin(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"stage":"new_ca_deployed"}`,
		"admin", "ca-rotation", "begin", "lenny-mtls-ca-2")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/ca-rotation/begin" {
		t.Fatalf("request: %s %s", got.method, got.path)
	}
	if got.body["newCaId"] != "lenny-mtls-ca-2" {
		t.Errorf("body: %+v, want newCaId=lenny-mtls-ca-2", got.body)
	}
}

// `admin ca-rotation begin` without an id fails before any request.
func TestAdminCARotationBeginRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "ca-rotation", "begin"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("missing newCaId: exit code %d, want 2", code)
	}
}

// `admin ca-rotation promote` and `retire` POST to their bare routes.
func TestAdminCARotationPromoteAndRetire(t *testing.T) {
	for _, sub := range []string{"promote", "retire"} {
		code, got := runAgainstGateway(t, http.StatusOK, `{"stage":"promoted"}`,
			"admin", "ca-rotation", sub)
		if code != 0 {
			t.Fatalf("%s exit code: got %d, want 0", sub, code)
		}
		if got.method != http.MethodPost || got.path != "/v1/admin/ca-rotation/"+sub {
			t.Errorf("%s request: %s %s", sub, got.method, got.path)
		}
	}
}
