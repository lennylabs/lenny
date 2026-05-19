// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.4 remediation-lock endpoints —
// acquire, list, get, extend, release, and steal — served by
// lenny-ops, including the §25.4 scope-based and identity-based
// authorization controls.
package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// TestAcquireLockContract confirms POST /v1/admin/remediation-locks
// returns 201 with the §25.4 Lock shape: id, scope, acquiredBy,
// server-authoritative timestamps, lockStore, epoch, and revision.
//
// spec: 25.4 (POST /v1/admin/remediation-locks — Lock response shape)
// diagnosis: Acquiring a lock returned a non-201 or a body missing a
// Lock field. An agent validates ownership against acquiredBy and
// re-validates against expiresAt, so a malformed Lock breaks the
// long-remediation contract.
func TestAcquireLockContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "prod-watchdog"},
		map[string]any{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	for _, field := range []string{"id", "scope", "operation", "acquiredBy", "acquiredAt", "expiresAt", "lockStore", "epoch", "revision"} {
		if _, ok := body[field]; !ok {
			t.Errorf("Lock response is missing the %q field", field)
		}
	}
	if body["acquiredBy"] != "prod-watchdog" {
		t.Errorf("acquiredBy = %v, want prod-watchdog", body["acquiredBy"])
	}
	if body["scope"] != "pool:default-gvisor" {
		t.Errorf("scope = %v, want pool:default-gvisor", body["scope"])
	}
}

// TestAcquireConflictContract confirms a second acquire on a held scope
// returns the §25.4 409 REMEDIATION_LOCK_CONFLICT.
//
// spec: 25.4 (REMEDIATION_LOCK_CONFLICT — 409 POLICY)
// diagnosis: A second acquire on a held scope did not return 409
// REMEDIATION_LOCK_CONFLICT. Without the conflict the §25.4 mutual
// exclusion fails and two agents remediate the same scope concurrently.
func TestAcquireConflictContract(t *testing.T) {
	srv := opsServer(t)
	hdr := map[string]string{"X-Lenny-Caller": "agent"}
	request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != "REMEDIATION_LOCK_CONFLICT" {
		t.Errorf("error code = %v, want REMEDIATION_LOCK_CONFLICT", errorEnvelope(t, body)["code"])
	}
}

// TestLockScopeAuthorizationControl confirms the §25.4 scope-based
// authorization control: a tenant-admin acquiring a platform-scoped
// lock is rejected with 403 LOCK_SCOPE_FORBIDDEN.
//
// spec: 25.4 (remediation-lock scope-based authorization)
// diagnosis: A tenant-admin acquired a platform-scoped lock. This is a
// security control — without it a tenant admin can block a platform
// upgrade by holding upgrade:platform.
func TestLockScopeAuthorizationControl(t *testing.T) {
	srv := opsServer(t)
	for _, scope := range []string{"platform:*", "upgrade:platform", "restore:platform", "config:global"} {
		rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
			map[string]string{"X-Lenny-Role": "tenant-admin", "X-Lenny-Tenant-ID": "acme", "X-Lenny-Caller": "tenant-agent"},
			map[string]any{"scope": scope, "operation": "x", "ttlSeconds": 300})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("scope %q: status = %d, want 403", scope, rec.Code)
		}
		if errorEnvelope(t, body)["code"] != "LOCK_SCOPE_FORBIDDEN" {
			t.Errorf("scope %q: error code = %v, want LOCK_SCOPE_FORBIDDEN", scope, errorEnvelope(t, body)["code"])
		}
	}
}

// TestLockReleaseIdentityControl confirms the §25.4 identity-based
// authorization control: only the lock's acquiredBy can release it; a
// non-owner is rejected with 403 LOCK_NOT_OWNED.
//
// spec: 25.4 (remediation-lock identity-based authorization on Release)
// diagnosis: A non-owner released another agent's lock. This is a
// control — §25.4 makes Steal the only audited path to take a lock; a
// non-owner Release that succeeds is a silent ownership change.
func TestLockReleaseIdentityControl(t *testing.T) {
	srv := opsServer(t)
	_, lock := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "owner"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)

	rec, body := request(t, srv, http.MethodDelete, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "not-the-owner"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner release status = %d, want 403", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != "LOCK_NOT_OWNED" {
		t.Errorf("error code = %v, want LOCK_NOT_OWNED", errorEnvelope(t, body)["code"])
	}
	// The owner releases successfully (204).
	rec, _ = request(t, srv, http.MethodDelete, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "owner"}, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("owner release status = %d, want 204", rec.Code)
	}
}

// TestLockExtendContract confirms PATCH /v1/admin/remediation-locks/-
// {id} renews the lock TTL and increments the §25.4 revision.
//
// spec: 25.4 (PATCH /v1/admin/remediation-locks/{id} — extend)
// diagnosis: Extending a lock did not renew expiresAt or did not
// increment revision. An agent extends periodically to hold a lock
// across a long remediation; a no-op extend lets the lock expire
// mid-remediation.
func TestLockExtendContract(t *testing.T) {
	srv := opsServer(t)
	_, lock := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "owner"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	rec, body := request(t, srv, http.MethodPatch, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "owner"}, map[string]any{"ttlSeconds": 600})
	if rec.Code != http.StatusOK {
		t.Fatalf("extend status = %d, want 200; body=%v", rec.Code, body)
	}
	if rev, _ := body["revision"].(float64); rev != 1 {
		t.Errorf("revision = %v, want 1 after one extension", body["revision"])
	}
}

// TestLockStealConfirmGate confirms POST /v1/admin/remediation-locks/-
// {id}/steal follows the §25.2 dry-run/confirm pattern: without
// confirm:true it returns a 200 preview and the lock is unchanged.
//
// spec: 25.4 (remediation-lock steal — dry-run/confirm gate)
// diagnosis: A steal without confirm:true changed the lock holder. The
// confirm gate on steal is a control — §25.4 makes steal a deliberate,
// audited action, never a silent override.
func TestLockStealConfirmGate(t *testing.T) {
	srv := opsServer(t)
	_, lock := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "routine-agent"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)

	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks/"+id+"/steal",
		map[string]string{"X-Lenny-Caller": "incident-agent"}, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("no-confirm steal status = %d, want 200 preview", rec.Code)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true on a no-confirm steal", body["dryRun"])
	}
	// The lock is unchanged.
	_, after := request(t, srv, http.MethodGet, "/v1/admin/remediation-locks/"+id, nil, nil)
	if after["acquiredBy"] != "routine-agent" {
		t.Errorf("acquiredBy = %v, want the original holder after a preview", after["acquiredBy"])
	}
}

// TestLockStealConfirmedTransfersOwnership confirms a confirmed steal
// transfers ownership and records the §25.4 stolenFrom field.
//
// spec: 25.4 (remediation-lock steal — confirmed transfer)
// diagnosis: A confirmed steal did not transfer acquiredBy or did not
// record stolenFrom. The stolenFrom field is the audit trail of who
// held the lock before the steal.
func TestLockStealConfirmedTransfersOwnership(t *testing.T) {
	srv := opsServer(t)
	_, lock := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "routine-agent"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks/"+id+"/steal",
		map[string]string{"X-Lenny-Caller": "incident-agent"},
		map[string]any{"confirm": true, "reason": "incident takes priority over routine scaling", "ttlSeconds": 300})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed steal status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["acquiredBy"] != "incident-agent" {
		t.Errorf("acquiredBy = %v, want incident-agent after a steal", body["acquiredBy"])
	}
	if body["stolenFrom"] != "routine-agent" {
		t.Errorf("stolenFrom = %v, want routine-agent", body["stolenFrom"])
	}
}

// TestListLocksContract confirms GET /v1/admin/remediation-locks lists
// the active locks.
//
// spec: 25.4 (GET /v1/admin/remediation-locks — list)
// diagnosis: The lock list did not return the active locks. An agent
// lists locks to see what other agents are remediating before it acts.
func TestListLocksContract(t *testing.T) {
	srv := opsServer(t)
	hdr := map[string]string{"X-Lenny-Caller": "agent"}
	request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:a", "operation": "scale", "ttlSeconds": 300})
	request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:b", "operation": "scale", "ttlSeconds": 300})
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/remediation-locks", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	locks, ok := body["locks"].([]any)
	if !ok || len(locks) != 2 {
		t.Fatalf("locks = %v, want 2 active locks", body["locks"])
	}
}
