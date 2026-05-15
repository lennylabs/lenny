// SPDX-License-Identifier: MIT

package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/openapi"
)

// spec: §15.1 OpenAPI document discovery.

func TestServesYAMLEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/yaml" {
		t.Errorf("Content-Type: got %q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.Len() == 0 {
		t.Error("body empty")
	}
}

func TestServesJSONEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	rr := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: got %q", rr.Header().Get("Content-Type"))
	}
	// Body must parse as JSON.
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi version: got %v", doc["openapi"])
	}
}

func TestDocumentMatchesEndpoints(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	// Every endpoint we've shipped should appear in the document.
	expected := []string{
		"/healthz",
		"/v1/sessions",
		"/v1/sessions/start",
		"/v1/sessions/{id}",
		"/v1/sessions/{id}/finalize",
		"/v1/sessions/{id}/start",
		"/v1/sessions/{id}/interrupt",
		"/v1/sessions/{id}/terminate",
		"/v1/sessions/{id}/resume",
		"/v1/sessions/{id}/derive",
		"/v1/sessions/{id}/upload",
		"/v1/sessions/{id}/messages",
		"/v1/sessions/{id}/transcript",
		"/v1/sessions/{id}/tree",
		"/v1/sessions/{id}/events",
		"/v1/sessions/{id}/extend-retention",
		"/v1/blobs/{ref}",
		"/metrics",
		"/mcp",
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/responses/{id}",
		"/v1/oauth/token",
		"/v1/admin/tenants",
		"/v1/admin/tenants/{id}",
		"/v1/admin/runtimes",
		"/v1/admin/runtimes/{name}",
		"/v1/admin/users",
		"/v1/admin/users/{user_id}",
		"/v1/admin/pools",
		"/v1/admin/pools/{name}",
		"/v1/admin/connectors",
		"/v1/admin/connectors/{id}",
		"/v1/admin/circuit-breakers",
		"/v1/admin/circuit-breakers/{name}",
		"/v1/admin/circuit-breakers/{name}/open",
		"/v1/admin/circuit-breakers/{name}/close",
		"/v1/admin/audit-events",
		"/v1/admin/audit-events/{seq}",
		"/v1/admin/audit-events/verify",
		"/v1/admin/health",
		"/v1/admin/health/summary",
		"/v1/admin/health/{component}",
		"/v1/admin/me",
		"/v1/admin/me/authorized-tools",
		"/v1/admin/bootstrap",
	}
	for _, p := range expected {
		if _, ok := paths[p]; !ok {
			t.Errorf("missing path in spec: %s", p)
		}
	}
}

func TestAdminEndpointsCarryMCPExtensions(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	_ = json.Unmarshal(doc, &parsed)
	paths, _ := parsed["paths"].(map[string]any)
	for path, op := range paths {
		if !strings.HasPrefix(path, "/v1/admin/") {
			continue
		}
		methods, _ := op.(map[string]any)
		for method, m := range methods {
			body, _ := m.(map[string]any)
			for _, ext := range []string{
				"x-lenny-mcp-tool", "x-lenny-scope",
				"x-lenny-required-role", "x-lenny-category",
			} {
				if body[ext] == nil {
					t.Errorf("admin endpoint %s %s missing %s", method, path, ext)
				}
			}
		}
	}
}

func TestDocumentReturnsCopy(t *testing.T) {
	a := openapi.Document()
	b := openapi.Document()
	if &a[0] == &b[0] {
		t.Error("Document must return defensive copies")
	}
}
