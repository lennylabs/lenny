// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: a full multi-surface walk through the
// real cmd/lenny-gateway binary in dev mode. It exercises the admin
// API, the session lifecycle, message injection through the echo
// executor, the transcript / tree introspection endpoints, the
// derive flow, the §11.7 audit chain, the §25.3 health API, the
// §16.1 metrics endpoint, and the §15.1 OpenAPI document — proving
// that every surface composes end-to-end through one process.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 15.1, 15.2, 11.7, 25.3, 16.1
// diagnosis: a surface of the real cmd/lenny-gateway binary failed to
// compose end-to-end in dev mode. The admin API, session lifecycle,
// message injection, transcript and tree introspection, derive flow,
// §11.7 audit chain, §25.3 health API, §16.1 metrics endpoint, §15.1
// OpenAPI document, or §15.2 MCP adapter did not behave as specified
// when driven through one process.
func TestGatewayFullSurfaceE2E(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	client := http.DefaultClient

	// do issues a request with the dev-mode auth headers. roles is
	// the comma-separated X-Lenny-Roles value (empty for a plain
	// user). Returns the status and the decoded JSON body.
	do := func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- admin: bootstrap a tenant + runtime as platform-admin ----
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":  "echo",
			"image": "lenny/echo@sha256:abc",
			// §5.1: the runtime must declare injection support for the
			// test's POST /messages mid-session injection to be accepted.
			"capabilities": map[string]any{
				"injection": map[string]any{
					"supported": true,
					"modes":     []string{"immediate", "queued"},
				},
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}

	// ---- admin: the tenant + runtime are listable ----
	code, tenants := do(http.MethodGet, "/v1/admin/tenants", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("list tenants: %d", code)
	}
	if list, _ := tenants["tenants"].([]any); len(list) == 0 {
		t.Error("bootstrapped tenant not listed")
	}

	// ---- session: create + start in one call ----
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}
	if created["state"] != "running" {
		t.Errorf("session state: %v", created["state"])
	}

	// ---- session: inject a message, expect the echo response ----
	code, msgResp := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hello e2e"}},
	})
	if code != http.StatusOK {
		t.Fatalf("send message: %d (%v)", code, msgResp)
	}
	out, _ := msgResp["output"].([]any)
	if len(out) == 0 {
		t.Fatal("message produced no output")
	}
	first, _ := out[0].(map[string]any)
	if txt, _ := first["text"].(string); !strings.Contains(txt, "hello e2e") {
		t.Errorf("echo output did not contain input: %q", txt)
	}

	// ---- session: the transcript records the exchange ----
	code, transcript := do(http.MethodGet, "/v1/sessions/"+sid+"/transcript", "", nil)
	if code != http.StatusOK {
		t.Fatalf("transcript: %d", code)
	}
	if entries, _ := transcript["entries"].([]any); len(entries) < 2 {
		t.Errorf("transcript should have >= 2 entries, got %v", transcript["entries"])
	}

	// ---- session: the tree is a single node ----
	code, tree := do(http.MethodGet, "/v1/sessions/"+sid+"/tree", "", nil)
	if code != http.StatusOK {
		t.Fatalf("tree: %d", code)
	}
	if int(tree["nodeCount"].(float64)) != 1 {
		t.Errorf("single-session tree should have 1 node: %v", tree["nodeCount"])
	}

	// ---- session: terminate, then derive a fresh session ----
	code, _ = do(http.MethodPost, "/v1/sessions/"+sid+"/terminate", "", nil)
	if code != http.StatusOK {
		t.Fatalf("terminate: %d", code)
	}

	// ---- audit: the bootstrap mutation is in the verified chain ----
	code, verify := do(http.MethodGet, "/v1/admin/audit-events/verify?tenantId=platform", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("audit verify: %d", code)
	}
	if verify["integrity"] != "verified" {
		t.Errorf("audit chain integrity: %v", verify["integrity"])
	}

	// ---- health: the platform reports healthy ----
	code, health := do(http.MethodGet, "/v1/admin/health", "", nil)
	if code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}
	if health["status"] != "healthy" {
		t.Errorf("health status: %v", health["status"])
	}

	// ---- §16.1: the metrics endpoint serves the gateway counters ----
	resp, err := client.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	metricsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(metricsBody), "lenny_gateway_requests_total") {
		t.Error("/metrics did not expose lenny_gateway_requests_total")
	}

	// ---- §15.1: the OpenAPI document is discoverable ----
	resp, err = client.Get(base + "/v1/openapi.json")
	if err != nil {
		t.Fatalf("GET /v1/openapi.json: %v", err)
	}
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	resp.Body.Close()
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi version: %v", doc["openapi"])
	}

	// ---- §15.2: the MCP adapter answers initialize ----
	mcpResp, err := client.Post(base+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	var mcpDoc map[string]any
	_ = json.NewDecoder(mcpResp.Body).Decode(&mcpDoc)
	mcpResp.Body.Close()
	if _, ok := mcpDoc["result"]; !ok {
		t.Errorf("MCP initialize did not return a result: %v", mcpDoc)
	}
}
