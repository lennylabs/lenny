// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreateRuntimeDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes?dryRun=true", admin.RuntimePayload{
		Name:             "claude-code",
		Type:             "agent",
		Image:            "ghcr.io/anthropic/claude-code@sha256:abcdef",
		ExecutionMode:    "session",
		IsolationProfile: "sandboxed",
		IntegrationLevel: "full",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.RuntimePayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "claude-code" || resp.IsolationProfile != "sandboxed" {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the runtime was not created.
	if _, err := store.Get(context.Background(), "claude-code"); err == nil {
		t.Error("dry-run create must not persist the runtime")
	}
	// No audit emission.
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

func TestUpdateRuntimeDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name:        "echo",
		Image:       "lenny/echo@sha256:abc",
		Description: "original",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	newDesc := "previewed description"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/echo?dryRun=true",
		admin.UpdateRuntimeRequest{Description: &newDesc})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.RuntimePayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Description != "previewed description" {
		t.Errorf("preview description = %q, want the merged value", resp.Description)
	}
	// No persistence: the stored runtime is unchanged.
	row, err := store.Get(context.Background(), "echo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Description != "original" {
		t.Errorf("dry-run update must not persist: stored description = %q", row.Description)
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

// A missing runtime 404s ahead of the dry-run branch.
func TestUpdateRuntimeDryRunMissing_spec_15_1(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	desc := "x"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/ghost?dryRun=true",
		admin.UpdateRuntimeRequest{Description: &desc})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("dry-run update of a missing runtime: status %d, want 404", rr.Code)
	}
}

// Validation still runs under dryRun: an invalid body returns 400.
func TestCreateRuntimeDryRunValidates_spec_15_1(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes?dryRun=true", admin.RuntimePayload{
		Name:  "echo",
		Image: "lenny/echo:v1", // non-digest image, rejected
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
