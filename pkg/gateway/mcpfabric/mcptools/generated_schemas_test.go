// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
)

// committedSchemas binds each overlap tool name to the committed generated
// schema variable. The drift guard re-generates from the live OpenAPI
// document and asserts each matches its committed counterpart, which is the
// §15.2.1 rule-4 "structural consistency by construction" check.
func committedSchemas() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"lenny/create_session": mcptools.GeneratedCreateSessionInputSchema,
	}
}

// TestGeneratedSchemasMatchOpenAPI re-runs the build-pipeline generation for
// every overlapping operation and asserts the committed generated schema is
// byte-identical to a fresh generation from the canonical OpenAPI document.
// A diff means OpenAPI changed without re-running `go generate
// ./pkg/gateway/mcptools/...`; regenerate to fix. spec: §15.2.1 rule 4
// line 1386. F-15.2.7.
func TestGeneratedSchemasMatchOpenAPI_spec_15_2_1_1386(t *testing.T) {
	doc := openapi.Document()
	committed := committedSchemas()

	overlaps := mcpschemagen.DefaultOverlaps()
	if len(overlaps) == 0 {
		t.Fatal("no overlapping operations registered")
	}
	for _, ov := range overlaps {
		fresh, err := mcpschemagen.BuildToolInputSchema(doc, ov.OperationID, ov.Options)
		if err != nil {
			t.Fatalf("%s: generate from openapi: %v", ov.ToolName, err)
		}
		got, ok := committed[ov.ToolName]
		if !ok {
			t.Fatalf("%s: overlap registered but no committed generated schema bound in the test", ov.ToolName)
		}
		if string(fresh) != string(got) {
			t.Errorf("%s: committed generated schema drifted from OpenAPI.\n committed: %s\n openapi:   %s\n run: go generate ./pkg/gateway/mcptools/...",
				ov.ToolName, got, fresh)
		}
	}
}

// TestCreateSessionToolUsesGeneratedSchema asserts the create_session tool
// advertises the OpenAPI-derived field set (not the prior hand-authored
// three-field schema), so the MCP and REST surfaces overlap structurally.
// spec: §15.2.1 rule 4 line 1386. F-15.2.7.
func TestCreateSessionToolUsesGeneratedSchema_spec_15_2_1_1386(t *testing.T) {
	srv := newMCPWithCreator(t, &fakeSessionCreator{})
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var schema map[string]any
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "lenny/create_session" {
			schema, _ = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("create_session tool or its inputSchema not found")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"runtimeRef", "userId", "environment", "workspacePlan", "isolationProfile", "metadata", "retryPolicy", "idempotencyKey"} {
		if _, ok := props[field]; !ok {
			t.Errorf("create_session inputSchema missing OpenAPI-derived field %q", field)
		}
	}
}

// TestCreateSessionForwardsFullRequest asserts the create_session tool now
// forwards the OpenAPI-derived fields (workspacePlan, isolationProfile,
// metadata) to the shared service, so an MCP client can drive the same
// create surface as REST instead of the prior three-field subset.
// spec: §15.2.1 rule 4 line 1386. F-15.2.7.
func TestCreateSessionForwardsFullRequest_spec_15_2_1_1386(t *testing.T) {
	creator := &fakeSessionCreator{}
	creator.resp.ID = "sess_full"
	creator.resp.State = "created"

	srv := newMCPWithCreator(t, creator)
	args := `{"runtimeRef":"echo","userId":"alice@acme.com","workspacePlan":{"sources":[]},"isolationProfile":"sandboxed","metadata":{"team":"x"}}`
	call(t, srv.Handler(), "lenny/create_session", args)

	if string(creator.gotReq.WorkspacePlan) == "" {
		t.Error("workspacePlan not forwarded to the shared service")
	}
	if string(creator.gotReq.IsolationProfile) != "sandboxed" {
		t.Errorf("isolationProfile: got %q, want sandboxed", creator.gotReq.IsolationProfile)
	}
	if creator.gotReq.Metadata["team"] != "x" {
		t.Errorf("metadata not forwarded: got %v", creator.gotReq.Metadata)
	}
}

// TestEveryOperationHasUniqueOperationID asserts the OpenAPI document is
// structurally addressable: every operation carries an operationId and no
// two collide, which the generator needs to key on. spec: §15.2.1 rule 4
// line 1386 — "the OpenAPI spec is the single authoritative schema".
// F-15.2.7.
func TestEveryOperationHasUniqueOperationID_spec_15_2_1_1386(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openapi.Document(), &doc); err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true}
	seen := map[string]string{}
	count := 0
	for path, item := range doc.Paths {
		for method, raw := range item {
			if !methods[method] {
				continue
			}
			count++
			var op struct {
				OperationID string `json:"operationId"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
			if op.OperationID == "" {
				t.Errorf("%s %s: missing operationId", method, path)
				continue
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %q reused by %s %s and %s", op.OperationID, method, path, prev)
			}
			seen[op.OperationID] = method + " " + path
		}
	}
	if count == 0 {
		t.Fatal("no operations found in OpenAPI document")
	}
}
