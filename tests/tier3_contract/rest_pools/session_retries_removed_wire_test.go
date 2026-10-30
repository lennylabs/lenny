//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the deletion of sessionPolicy.maxSessionRetries.
// retryPolicy.maxRetries is the one retry budget, so the served OpenAPI
// document no longer declares the key, and the admin pool wire surface
// accepts a body that still carries it without echoing it back.
//
// spec: §7.3 (Retry and Resume), §5.2 (Pool Configuration and Execution Modes), §15.1 (REST API).
package rest_pools_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// diagnosis: a failure means the gateway's served /openapi.json still
// declares sessionPolicy.maxSessionRetries on the Runtime schema, so an SDK
// generated from it offers a retry budget the gateway never reads.
// spec: 7.3 (Retry and Resume), 15.1 (REST API)
func TestServedOpenAPIOmitsMaxSessionRetries(t *testing.T) {
	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	sp := doc.Components.Schemas["Runtime"].Properties["sessionPolicy"].Properties
	if len(sp) == 0 {
		t.Fatal("Runtime.sessionPolicy has no properties in the served document")
	}
	if _, ok := sp["maxSessionRetries"]; ok {
		t.Error("Runtime.sessionPolicy still declares maxSessionRetries")
	}
	if strings.Contains(string(body), "maxSessionRetries") {
		t.Error("served document still names maxSessionRetries")
	}
}

// diagnosis: a failure means the admin pool create refused a body that still
// carries the deleted sessionPolicy.maxSessionRetries key, or echoed the key
// back on the create or GET response, so the deleted budget still reaches
// the wire.
// spec: 7.3 (Retry and Resume), 5.2 (Pool Configuration and Execution Modes)
func TestAdminPoolAcceptsAndDropsMaxSessionRetries(t *testing.T) {
	router, _ := newPoolRouter(t)
	h := router.Handler()
	body := json.RawMessage(`{"name":"retired-pool","runtimeRef":"nonce-only","isolationProfile":"sandboxed","acknowledgeNonceOnlyAuth":true,` +
		`"sessionPolicy":{"maxSessionRetries":-1,"cleanupTimeoutSeconds":30}}`)
	rr, created := serve(t, h, http.MethodPost, "/v1/admin/pools", body, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	sp, _ := created["sessionPolicy"].(map[string]any)
	if sp == nil || sp["cleanupTimeoutSeconds"] != float64(30) {
		t.Fatalf("create response sessionPolicy = %v, want cleanupTimeoutSeconds 30", created["sessionPolicy"])
	}
	if _, ok := sp["maxSessionRetries"]; ok {
		t.Errorf("create response echoed maxSessionRetries: %v", sp)
	}
	rr, got := serve(t, h, http.MethodGet, "/v1/admin/pools/retired-pool", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get: got %d; body=%s", rr.Code, rr.Body.String())
	}
	if sp, _ := got["sessionPolicy"].(map[string]any); sp == nil {
		t.Fatalf("GET response has no sessionPolicy: %v", got)
	} else if _, ok := sp["maxSessionRetries"]; ok {
		t.Errorf("GET response carries maxSessionRetries: %v", sp)
	}
}

// diagnosis: a failure means a generated MCP tool schema, either the gateway
// tool input schemas generated from openapi.json or the lenny-ops tool
// inventory, still names sessionPolicy.maxSessionRetries, so an MCP client
// is offered a retry budget the gateway never reads. Regenerate the MCP
// schemas from the current openapi.json.
// spec: 7.3 (Retry and Resume), 15.2.1 (REST/MCP Consistency Contract)
func TestGeneratedMCPSchemasOmitMaxSessionRetries(t *testing.T) {
	if strings.Contains(string(mcptools.GeneratedCreateSessionInputSchema), "maxSessionRetries") {
		t.Error("the generated lenny/create_session input schema names maxSessionRetries")
	}
	// The lenny-ops tool inventory is generated source; its schemas are
	// literals in the committed file.
	for _, rel := range []string{"pkg/ops/mcp/generated_tools.go", "pkg/gateway/mcpfabric/mcptools/generated_schemas.go"} {
		src, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.Contains(string(src), "maxSessionRetries") {
			t.Errorf("%s names maxSessionRetries", rel)
		}
	}
}

// diagnosis: a failure means a runtime create over the admin wire surface
// refused a body that still carries the deleted
// sessionPolicy.maxSessionRetries key, or the key reached the stored runtime
// or a response, so the deleted budget still persists.
// spec: 7.3 (Retry and Resume), 15.1 (REST API)
func TestAdminRuntimeCreateDoesNotPersistMaxSessionRetries(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(runtimes)
	h := router.Handler()
	body := json.RawMessage(`{"name":"rt-retired","image":"lenny/rt@sha256:abc","labels":{"tier":"test"},` +
		`"sessionPolicy":{"maxSessionRetries":3,"cleanupTimeoutSeconds":30}}`)
	rr, created := serve(t, h, http.MethodPost, "/v1/admin/runtimes", body, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "maxSessionRetries") {
		t.Errorf("create response names maxSessionRetries: %v", created)
	}
	stored, err := runtimes.Get(context.Background(), "rt-retired")
	if err != nil {
		t.Fatalf("read the stored runtime: %v", err)
	}
	if stored.SessionPolicy == nil || stored.SessionPolicy.CleanupTimeoutSeconds != 30 {
		t.Fatalf("stored sessionPolicy = %+v, want cleanupTimeoutSeconds 30", stored.SessionPolicy)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal the stored runtime: %v", err)
	}
	if strings.Contains(string(raw), "maxSessionRetries") {
		t.Errorf("stored runtime names maxSessionRetries: %s", raw)
	}
	rr, _ = serve(t, h, http.MethodGet, "/v1/admin/runtimes/rt-retired", nil, "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "maxSessionRetries") {
		t.Errorf("GET = %d %s, want 200 without maxSessionRetries", rr.Code, rr.Body.String())
	}
}
