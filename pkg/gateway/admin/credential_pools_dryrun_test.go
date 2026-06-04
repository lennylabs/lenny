// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreateCredentialPoolDryRun_spec_15_1(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p-dry")
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.CredentialPoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "p-dry" || resp.Provider != "anthropic_direct" || len(resp.Credentials) != 1 {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the pool was not created.
	if _, err := store.Get(context.Background(), "acme", "p-dry"); err == nil {
		t.Error("dry-run create must not persist the credential pool")
	}
}

func TestUpdateCredentialPoolDryRun_spec_15_1(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validCredentialPool("acme", "p1")
	body.MaxConcurrentSessions = 42
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/p1?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.CredentialPoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MaxConcurrentSessions != 42 || len(resp.Credentials) != 1 {
		t.Errorf("preview = %+v, want maxConcurrentSessions=42 with 1 credential", resp)
	}
	// No persistence: the stored pool is unchanged.
	row, _ := store.Get(context.Background(), "acme", "p1")
	if row.MaxConcurrentSessions != 10 || len(row.Credentials) != 0 {
		t.Errorf("dry-run update must not persist: stored = %+v", row)
	}
}

// A missing pool 404s ahead of the dry-run branch.
func TestUpdateCredentialPoolDryRunMissing_spec_15_1(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/ghost?dryRun=true",
		validCredentialPool("acme", "ghost"), withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("dry-run update of a missing pool: status %d, want 404", rr.Code)
	}
}

// Validation still runs under dryRun: a credential entry without an id
// returns 400.
func TestCreateCredentialPoolDryRunValidates_spec_15_1(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p-bad")
	body.Credentials = []admin.CredentialEntryPayload{{SecretRef: "lenny-system/x"}} // missing id
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
