// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// lockServer returns a Server with a fresh in-memory remediation-lock
// store wired.
func lockServer() (*opsserver.Server, *coordination.MemStore) {
	store := coordination.NewMemStore()
	return opsserver.New(opsserver.Options{Locks: store}), store
}

// doJSON issues a request against the Server with optional headers and
// a JSON body and decodes the JSON response.
func doJSON(t *testing.T, srv *opsserver.Server, method, url string, headers map[string]string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func TestAcquireLockReturns201(t *testing.T) {
	srv, _ := lockServer()
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	if body["acquiredBy"] != "watchdog" {
		t.Errorf("acquiredBy = %v, want watchdog", body["acquiredBy"])
	}
	if body["lockStore"] != "memory" {
		t.Errorf("lockStore = %v, want memory", body["lockStore"])
	}
}

func TestAcquireLockConflictReturns409(t *testing.T) {
	srv, _ := lockServer()
	hdr := map[string]string{"X-Lenny-Caller": "agent-a"}
	doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	// §25.4: a held scope returns 409 REMEDIATION_LOCK_CONFLICT with the
	// POLICY category.
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "REMEDIATION_LOCK_CONFLICT" || errObj["category"] != "POLICY" {
		t.Errorf("error = %v, want REMEDIATION_LOCK_CONFLICT/POLICY", errObj)
	}
}

// splitBrainLocks is a RemediationLockService whose Extend returns the
// §25.4 split-brain conflict, so the HTTP error mapping can be asserted.
type splitBrainLocks struct {
	coordination.RemediationLockService
}

func (splitBrainLocks) Extend(_ context.Context, lockID string, _ int) (*coordination.Lock, error) {
	return nil, &coordination.Error{
		Code:         coordination.ErrCodeConflict,
		Message:      "lock " + lockID + " lost a split-brain resolution",
		SplitBrain:   true,
		Winner:       "pre_outage",
		WinnerHolder: "alice",
	}
}

// TestExtendSplitBrainConflictCarriesDetails asserts the §25.4 line 2267
// split-brain 409: a losing holder's heartbeat returns
// REMEDIATION_LOCK_CONFLICT whose details carry splitBrain:true,
// winner:"pre_outage", and winnerHolder set to the pre-outage acquiredBy.
func TestExtendSplitBrainConflictCarriesDetails(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: splitBrainLocks{}})
	rec, body := doJSON(t, srv, http.MethodPatch, "/v1/admin/remediation-locks/lock-loser",
		map[string]string{"X-Lenny-Caller": "bob"},
		map[string]any{"ttlSeconds": 300})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rec.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "REMEDIATION_LOCK_CONFLICT" {
		t.Fatalf("error code = %v, want REMEDIATION_LOCK_CONFLICT", errObj["code"])
	}
	details, _ := errObj["details"].(map[string]any)
	if details["splitBrain"] != true {
		t.Errorf("details.splitBrain = %v, want true", details["splitBrain"])
	}
	if details["winner"] != "pre_outage" {
		t.Errorf("details.winner = %v, want pre_outage", details["winner"])
	}
	if details["winnerHolder"] != "alice" {
		t.Errorf("details.winnerHolder = %v, want alice", details["winnerHolder"])
	}
}

func TestAcquirePlatformScopeForbiddenForTenantAdmin(t *testing.T) {
	srv, _ := lockServer()
	// §25.4: a tenant-admin acquiring a platform-scoped lock is rejected
	// with 403 LOCK_SCOPE_FORBIDDEN — the authorization control.
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Role": "tenant-admin", "X-Lenny-Tenant-ID": "acme", "X-Lenny-Caller": "tenant-agent"},
		map[string]any{"scope": "restore:platform", "operation": "restore", "ttlSeconds": 300})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "LOCK_SCOPE_FORBIDDEN" {
		t.Errorf("error code = %v, want LOCK_SCOPE_FORBIDDEN", errObj["code"])
	}
}

func TestReleaseRequiresOwnership(t *testing.T) {
	srv, _ := lockServer()
	_, lock := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "owner"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	// §25.4: a non-owner DELETE is 403 LOCK_NOT_OWNED.
	rec, body := doJSON(t, srv, http.MethodDelete, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "intruder"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner release status = %d, want 403", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "LOCK_NOT_OWNED" {
		t.Errorf("error code = %v, want LOCK_NOT_OWNED", errObj["code"])
	}
	// The owner releases successfully.
	rec, _ = doJSON(t, srv, http.MethodDelete, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "owner"}, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("owner release status = %d, want 204", rec.Code)
	}
}

func TestExtendLockRenewsTTL(t *testing.T) {
	srv, _ := lockServer()
	_, lock := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "owner"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	rec, body := doJSON(t, srv, http.MethodPatch, "/v1/admin/remediation-locks/"+id,
		map[string]string{"X-Lenny-Caller": "owner"}, map[string]any{"ttlSeconds": 600})
	if rec.Code != http.StatusOK {
		t.Fatalf("extend status = %d, want 200; body=%v", rec.Code, body)
	}
	// Extension increments the revision per §25.4.
	if rev, _ := body["revision"].(float64); rev != 1 {
		t.Errorf("revision = %v, want 1 after one extension", body["revision"])
	}
}

func TestStealWithoutConfirmReturnsPreview(t *testing.T) {
	srv, _ := lockServer()
	_, lock := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "routine-agent"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	// §25.2 dry-run/confirm: a steal without confirm:true is a 200
	// preview, not a mutation.
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks/"+id+"/steal",
		map[string]string{"X-Lenny-Caller": "incident-agent"}, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("steal preview status = %d, want 200", rec.Code)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true on a no-confirm steal", body["dryRun"])
	}
	// The lock is still owned by the original holder.
	_, after := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks/"+id, nil, nil)
	if after["acquiredBy"] != "routine-agent" {
		t.Errorf("acquiredBy = %v after a preview, want the original holder", after["acquiredBy"])
	}
}

func TestStealWithConfirmTransfersLock(t *testing.T) {
	srv, _ := lockServer()
	_, lock := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "routine-agent"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks/"+id+"/steal",
		map[string]string{"X-Lenny-Caller": "incident-agent"},
		map[string]any{"confirm": true, "reason": "incident takes priority", "ttlSeconds": 300})
	if rec.Code != http.StatusOK {
		t.Fatalf("steal status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["acquiredBy"] != "incident-agent" || body["stolenFrom"] != "routine-agent" {
		t.Errorf("steal result = %v, want acquiredBy=incident-agent stolenFrom=routine-agent", body)
	}
}

func TestStealWithConfirmRequiresReason(t *testing.T) {
	srv, _ := lockServer()
	_, lock := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "a"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	id, _ := lock["id"].(string)
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks/"+id+"/steal",
		map[string]string{"X-Lenny-Caller": "b"}, map[string]any{"confirm": true})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when confirm:true is supplied without a reason", rec.Code)
	}
}

func TestGetUnknownLockReturns404(t *testing.T) {
	srv, _ := lockServer()
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks/lock-ghost", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "REMEDIATION_LOCK_NOT_FOUND" {
		t.Errorf("error code = %v, want REMEDIATION_LOCK_NOT_FOUND", errObj["code"])
	}
}

func TestListLocksReturnsActiveLocks(t *testing.T) {
	srv, _ := lockServer()
	hdr := map[string]string{"X-Lenny-Caller": "agent"}
	doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:a", "operation": "scale", "ttlSeconds": 300})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	locks, _ := body["locks"].([]any)
	if len(locks) != 1 {
		t.Errorf("got %d locks, want 1", len(locks))
	}
}

func TestLocksUnavailableWithoutStore(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no lock store configured", rec.Code)
	}
}
