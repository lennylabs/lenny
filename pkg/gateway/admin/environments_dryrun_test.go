// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 line 1140 — the environments dry-run preview validates the
// definition and returns matched runtimes/connectors plus unmatched
// selector terms without persisting or auditing.

func newEnvDryRunAdmin(t *testing.T) (*admin.Router, environmentstore.Store, *recordingAudit) {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "sec-scanner", Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research-bot", Labels: map[string]string{"team": "research"},
	})
	connectors := connectorstore.NewMemory()
	_ = connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "sec-vault", MCPServerURL: "https://sec-vault.example.com",
		Labels: map[string]string{"team": "security"},
	})
	_ = connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "research-db", MCPServerURL: "https://research-db.example.com",
		Labels: map[string]string{"team": "research"},
	})
	envs := environmentstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithEnvironments(envs).WithRuntimes(runtimes).WithConnectors(connectors)
	return router, envs, audit
}

type envDryRunResponse struct {
	Resource struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"resource"`
	Preview struct {
		MatchedRuntimes        []string `json:"matchedRuntimes"`
		MatchedConnectors      []string `json:"matchedConnectors"`
		UnmatchedSelectorTerms []string `json:"unmatchedSelectorTerms"`
	} `json:"preview"`
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestCreateEnvironmentDryRunPreviewAndNoPersist_spec_15_1(t *testing.T) {
	router, envs, audit := newEnvDryRunAdmin(t)
	payload := validEnvironmentPayload("preview-env")
	payload.RuntimeSelector = admin.SelectorPayload{MatchLabels: map[string]string{"team": "security"}}
	payload.ConnectorSelector = admin.ConnectorSelectorPayload{MatchLabels: map[string]string{"team": "security"}}

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/environments?dryRun=true", payload, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("dryRun create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("missing X-Dry-Run header; got %q", rr.Header().Get("X-Dry-Run"))
	}
	var resp envDryRunResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsStr(resp.Preview.MatchedRuntimes, "sec-scanner") || containsStr(resp.Preview.MatchedRuntimes, "research-bot") {
		t.Errorf("matchedRuntimes = %v", resp.Preview.MatchedRuntimes)
	}
	if !containsStr(resp.Preview.MatchedConnectors, "sec-vault") || containsStr(resp.Preview.MatchedConnectors, "research-db") {
		t.Errorf("matchedConnectors = %v", resp.Preview.MatchedConnectors)
	}
	if len(resp.Preview.UnmatchedSelectorTerms) != 0 {
		t.Errorf("unmatchedSelectorTerms = %v, want none", resp.Preview.UnmatchedSelectorTerms)
	}
	// No persistence and no audit under dryRun.
	if _, err := envs.Get(context.Background(), "acme", "preview-env"); err == nil {
		t.Errorf("dryRun create persisted the environment")
	}
	if snap := audit.snapshot(); len(snap) != 0 {
		t.Errorf("dryRun emitted audit events: %+v", snap)
	}
}

func TestCreateEnvironmentDryRunReportsUnmatchedSelectorTerm_spec_15_1(t *testing.T) {
	router, _, _ := newEnvDryRunAdmin(t)
	payload := validEnvironmentPayload("typo-env")
	// "team=security" matches a runtime; "zone=nonexistent" matches nothing
	// and surfaces as an unmatched selector term (a likely label typo).
	payload.RuntimeSelector = admin.SelectorPayload{MatchLabels: map[string]string{
		"team": "security", "zone": "nonexistent",
	}}
	payload.ConnectorSelector = admin.ConnectorSelectorPayload{}

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/environments?dryRun=true", payload, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp envDryRunResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !containsStr(resp.Preview.UnmatchedSelectorTerms, "zone=nonexistent") {
		t.Errorf("unmatchedSelectorTerms = %v, want it to contain zone=nonexistent", resp.Preview.UnmatchedSelectorTerms)
	}
	if containsStr(resp.Preview.UnmatchedSelectorTerms, "team=security") {
		t.Errorf("team=security should not be reported as unmatched: %v", resp.Preview.UnmatchedSelectorTerms)
	}
}

func TestUpdateEnvironmentDryRun_spec_15_1(t *testing.T) {
	router, envs, _ := newEnvDryRunAdmin(t)
	if err := envs.Create(context.Background(), environmentstore.Environment{
		Name: "edit-env", TenantID: "acme", Description: "original",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	payload := validEnvironmentPayload("edit-env")
	payload.Description = "updated description"

	rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/environments/edit-env?dryRun=true", payload, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("dryRun update: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("missing X-Dry-Run header")
	}
	var resp envDryRunResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Resource.Description != "updated description" {
		t.Errorf("preview description = %q, want updated", resp.Resource.Description)
	}
	// The stored row must be unchanged.
	got, err := envs.Get(context.Background(), "acme", "edit-env")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "original" {
		t.Errorf("dryRun update mutated the stored environment: %q", got.Description)
	}
}

func TestUpdateEnvironmentDryRunNotFound_spec_15_1(t *testing.T) {
	router, _, _ := newEnvDryRunAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/environments/ghost?dryRun=true", validEnvironmentPayload("ghost"), withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("dryRun update missing env: status %d, want 404", rr.Code)
	}
}

func TestCreateEnvironmentDryRunValidationError_spec_15_1(t *testing.T) {
	router, envs, _ := newEnvDryRunAdmin(t)
	payload := validEnvironmentPayload("bad-env")
	payload.Members = []admin.MemberPayload{
		{Identity: admin.IdentityPayload{Type: "oidc-group", Value: "g"}, Role: "not-a-role"},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/environments?dryRun=true", payload, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("dryRun invalid role: status %d, want 422", rr.Code)
	}
	if _, err := envs.Get(context.Background(), "acme", "bad-env"); err == nil {
		t.Errorf("dryRun create persisted an invalid environment")
	}
}
