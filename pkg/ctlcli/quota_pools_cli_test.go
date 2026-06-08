// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"testing"
)

// spec: §24.4 line 63 / §24.6 line 99 — the runbook-invoked CLI surface.
// F-17.7.5. These commands back the warm-pool-exhaustion, redis-failure,
// and redis-sentinel-failover runbook remediation steps.

// --- admin pools set-warm-count (§24.4) -------------------------------

func TestAdminPoolsSetWarmCount(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"warmCount":15}`,
		"admin", "pools", "set-warm-count", "--pool", "default-gvisor", "--min", "15")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPut || got.path != "/v1/admin/pools/default-gvisor/warm-count" {
		t.Errorf("request: %s %s, want PUT /v1/admin/pools/default-gvisor/warm-count", got.method, got.path)
	}
	if got.body["minWarm"] != float64(15) {
		t.Errorf("body minWarm = %v, want 15", got.body["minWarm"])
	}
	if got.body["confirm"] != true {
		t.Errorf("body confirm = %v, want true (apply without --dry-run)", got.body["confirm"])
	}
}

func TestAdminPoolsSetWarmCountDryRun(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"dryRun":true}`,
		"admin", "pools", "set-warm-count", "--pool", "p", "--min", "5", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["confirm"] != false {
		t.Errorf("body confirm = %v, want false (preview)", got.body["confirm"])
	}
}

func TestAdminPoolsSetWarmCountRequiresPoolAndMin(t *testing.T) {
	cases := [][]string{
		{"admin", "pools", "set-warm-count", "--min", "5"},
		{"admin", "pools", "set-warm-count", "--pool", "p"},
		{"admin", "pools", "set-warm-count", "--pool", "p", "--min", "-1"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		full := append([]string{"--api-url", "http://127.0.0.1:0"}, args...)
		if code := run(full, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// --- admin quota reconcile (§24.6) ------------------------------------

func TestAdminQuotaReconcileAllTenants(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenantsReconciled":3}`,
		"admin", "quota", "reconcile", "--all-tenants")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/quota/reconcile" {
		t.Errorf("request: %s %s, want POST /v1/admin/quota/reconcile", got.method, got.path)
	}
	if got.body["allTenants"] != true {
		t.Errorf("body allTenants = %v, want true", got.body["allTenants"])
	}
}

func TestAdminQuotaReconcileSingleTenant(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"tenantsReconciled":1}`,
		"admin", "quota", "reconcile", "--tenant", "acme")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["tenantId"] != "acme" {
		t.Errorf("body tenantId = %v, want acme", got.body["tenantId"])
	}
}

func TestAdminQuotaReconcileRequiresExactlyOneScope(t *testing.T) {
	cases := [][]string{
		{"admin", "quota", "reconcile"},
		{"admin", "quota", "reconcile", "--all-tenants", "--tenant", "acme"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		full := append([]string{"--api-url", "http://127.0.0.1:0"}, args...)
		if code := run(full, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestAdminQuotaUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "quota", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown quota subcommand: exit %d, want 2", code)
	}
}
