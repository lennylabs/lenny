// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/externaladapterstore"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreateExternalAdapterDryRun_spec_15_1(t *testing.T) {
	router, store, aud := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters?dryRun=true", samplePayload())
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.ExternalAdapterPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "acme-a2a" || resp.Status != string(externaladapterstore.StatusPendingValidation) {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the adapter was not created.
	if _, err := store.Get(context.Background(), "acme-a2a"); err == nil {
		t.Error("dry-run create must not persist the adapter")
	}
	if len(aud.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", aud.snapshot())
	}
}

func TestUpdateExternalAdapterDryRun_spec_15_1(t *testing.T) {
	router, store, aud := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	if err := store.Create(context.Background(), externaladapterstore.ExternalAdapter{
		Name:       "acme-a2a",
		Protocol:   "a2a",
		PathPrefix: "/a2a",
		BinaryPath: "/usr/local/bin/acme-a2a",
		Level:      "standard",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Drain the create audit so only the dry-run window is asserted.
	aud0 := len(aud.snapshot())
	rr := eaReq(t, router.Handler(), http.MethodPut, "/v1/admin/external-adapters/acme-a2a?dryRun=true",
		admin.ExternalAdapterPayload{DisplayName: "Acme Agent"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.ExternalAdapterPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayName != "Acme Agent" {
		t.Errorf("preview displayName = %q, want the merged value", resp.DisplayName)
	}
	// No persistence: the stored adapter is unchanged.
	row, _ := store.Get(context.Background(), "acme-a2a")
	if row.DisplayName != "" {
		t.Errorf("dry-run update must not persist: stored displayName = %q", row.DisplayName)
	}
	if len(aud.snapshot()) != aud0 {
		t.Errorf("dry-run must not emit audit events: %+v", aud.snapshot())
	}
}

// Validation still runs under dryRun: a body that clears the level on a
// brand-new adapter (missing required level) returns 400.
func TestCreateExternalAdapterDryRunValidates_spec_15_1(t *testing.T) {
	router, _, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	bad := samplePayload()
	bad.Level = "" // missing required level
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters?dryRun=true", bad)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
