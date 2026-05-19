// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.2/§25.4 shared API conventions —
// the canonical error envelope, the dry-run/confirm pattern, and
// pagination — as they appear across the §25 operability endpoints.
package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// TestErrorEnvelopeShape confirms a §25 operability error carries the
// §25.2 canonical envelope: a nested error object with code, category,
// message, retryable, and documentationUrl.
//
// spec: 25.2 (canonical error response envelope)
// diagnosis: A §25 endpoint returned an error whose body is not the
// §25.2 envelope, or omits a required field — code, category,
// retryable, or documentationUrl. An agent's retry logic keys on
// category and retryable, so a malformed envelope breaks remediation.
func TestErrorEnvelopeShape(t *testing.T) {
	srv := opsServer(t)
	// A GET on an unknown lock is a §25.4 REMEDIATION_LOCK_NOT_FOUND.
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/remediation-locks/lock-absent", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertJSONContentType(t, rec)
	errObj := errorEnvelope(t, body)
	for _, field := range []string{"code", "category", "message", "documentationUrl"} {
		if _, ok := errObj[field]; !ok {
			t.Errorf("error envelope is missing the %q field", field)
		}
	}
	if errObj["code"] != "REMEDIATION_LOCK_NOT_FOUND" {
		t.Errorf("error code = %v, want REMEDIATION_LOCK_NOT_FOUND", errObj["code"])
	}
	// A 404 is the PERMANENT category and is not retryable.
	if errObj["category"] != "PERMANENT" {
		t.Errorf("category = %v, want PERMANENT for a 404", errObj["category"])
	}
	if errObj["retryable"] != false {
		t.Errorf("retryable = %v, want false for a PERMANENT error", errObj["retryable"])
	}
	if url, _ := errObj["documentationUrl"].(string); url == "" {
		t.Error("documentationUrl is empty")
	}
}

// TestErrorCategoriesMatchHTTPStatus confirms the §25.2 category on each
// error code matches its documented HTTP-status family: a 409 conflict
// is POLICY, a 403 is AUTH, a 503 is TRANSIENT.
//
// spec: 25.2 (error categories and retry semantics)
// diagnosis: A §25 endpoint returned an error whose category does not
// match its HTTP status family. An agent that retries on TRANSIENT but
// not PERMANENT will retry the wrong failures if the category is
// miscategorized.
func TestErrorCategoriesMatchHTTPStatus(t *testing.T) {
	srv := opsServer(t)

	// 409 conflict on a held lock scope → POLICY.
	hdr := map[string]string{"X-Lenny-Caller": "agent"}
	request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", rec.Code)
	}
	if cat := errorEnvelope(t, body)["category"]; cat != "POLICY" {
		t.Errorf("409 category = %v, want POLICY", cat)
	}

	// 403 forbidden on a platform scope for a tenant-admin → AUTH.
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Role": "tenant-admin", "X-Lenny-Tenant-ID": "acme", "X-Lenny-Caller": "a"},
		map[string]any{"scope": "platform:*", "operation": "x", "ttlSeconds": 300})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want 403", rec.Code)
	}
	if cat := errorEnvelope(t, body)["category"]; cat != "AUTH" {
		t.Errorf("403 category = %v, want AUTH", cat)
	}
}

// TestDryRunConfirmPattern confirms the §25.2 dry-run/confirm pattern on
// a non-convergent mutating endpoint: without confirm:true the endpoint
// returns 200 with dryRun:true and a preview, and mutates no state.
//
// spec: 25.2 (dry-run / confirm pattern)
// diagnosis: A §25 non-convergent mutating endpoint applied a change
// without confirm:true, or returned a non-200 instead of a preview. The
// confirm gate is a control: skipping it lets an agent mutate platform
// state it intended only to preview.
func TestDryRunConfirmPattern(t *testing.T) {
	srv := opsServer(t)
	// A drift snapshot refresh without confirm:true is a 200 preview.
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"new": map[string]any{}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("no-confirm refresh status = %d, want 200 (a preview, not an error)", rec.Code)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true on a no-confirm mutating call", body["dryRun"])
	}
	if _, ok := body["preview"]; !ok {
		t.Error("the dry-run response is missing the preview object")
	}
	// The snapshot was not replaced — a subsequent report still compares
	// against the original desired state.
	_, report := request(t, srv, http.MethodGet, "/v1/admin/drift?scope=pools", nil, nil)
	if report["desiredStateSource"] != "snapshot" {
		t.Errorf("desiredStateSource = %v, want snapshot still in place", report["desiredStateSource"])
	}
}

// TestDryRunConfirmApplies confirms the §25.2 dry-run/confirm pattern
// applies the change when confirm:true is supplied.
//
// spec: 25.2 (dry-run / confirm pattern — confirmed path)
// diagnosis: A §25 mutating endpoint with confirm:true returned a
// preview instead of applying the change, or did not apply it. An agent
// that supplied confirm expects the mutation to take effect.
func TestDryRunConfirmApplies(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"new": map[string]any{}}},
		"confirm": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed refresh status = %d, want 200", rec.Code)
	}
	if body["dryRun"] == true {
		t.Error("a confirm:true call returned a dry-run preview")
	}
	if body["replaced"] != true {
		t.Errorf("replaced = %v, want true on a confirmed refresh", body["replaced"])
	}
}

// TestPaginationEnvelope confirms a §25 list endpoint returns the
// §25.4 canonical pagination envelope: an items array and a pagination
// object carrying limit and hasMore.
//
// spec: 25.2 (pagination — canonical response envelope)
// diagnosis: A §25 list endpoint returned a response without the
// canonical pagination envelope, or omitted limit/hasMore. An agent
// paging through results needs the envelope to know whether to
// continue.
func TestPaginationEnvelope(t *testing.T) {
	srv := opsServer(t)
	request(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "info", "summary": "first"})
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/escalations?limit=50", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	if _, ok := body["items"].([]any); !ok {
		t.Error("the list response is missing the items array")
	}
	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("the list response is missing the pagination envelope")
	}
	if limit, _ := pagination["limit"].(float64); limit != 50 {
		t.Errorf("pagination.limit = %v, want the requested 50", pagination["limit"])
	}
	if _, ok := pagination["hasMore"]; !ok {
		t.Error("pagination is missing the hasMore field")
	}
}

// TestPaginationLimitCappedAtMax confirms the §25.4 limit parameter is
// capped at the documented maximum of 1000.
//
// spec: 25.2 (pagination — limit max 1000)
// diagnosis: A §25 list endpoint honored a limit above the §25.4
// ceiling of 1000. An unbounded page size lets a caller force an
// oversized scan.
func TestPaginationLimitCappedAtMax(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/escalations?limit=99999", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pagination, _ := body["pagination"].(map[string]any)
	if limit, _ := pagination["limit"].(float64); limit != 1000 {
		t.Errorf("pagination.limit = %v, want the 1000 ceiling", pagination["limit"])
	}
}

// TestInvalidPaginationParamRejected confirms a malformed §25.4
// pagination parameter is rejected with a PERMANENT error.
//
// spec: 25.2 (pagination — parameter validation)
// diagnosis: A §25 list endpoint accepted a malformed since/until/limit
// parameter instead of rejecting it. A silently-ignored bad parameter
// returns the wrong page without the caller knowing.
func TestInvalidPaginationParamRejected(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/escalations?since=not-a-timestamp", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed since parameter", rec.Code)
	}
	if errorEnvelope(t, body)["category"] != "PERMANENT" {
		t.Error("a malformed parameter should be a PERMANENT error")
	}
}
