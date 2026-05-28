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
		"/v1/sessions/{id}/eval",
		"/v1/blobs/{ref}",
		"/v1/usage",
		"/v1/metering/events",
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
		"/v1/admin/users/{user_id}/invalidate",
		"/v1/admin/users/{user_id}/erase",
		"/v1/admin/erasure-jobs/{job_id}",
		"/v1/admin/legal-hold",
		"/v1/admin/experiments",
		"/v1/admin/experiments/{name}",
		"/v1/admin/experiments/{name}/results",
		"/v1/admin/environments",
		"/v1/admin/environments/{name}",
		"/v1/admin/issued-tokens/{jti}/revoke",
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

// spec: §15.1 line 589 — `info.version` field in the spec matches the
// gateway's release version. F-15.1.18.
func TestHandlerStampsGatewayReleaseVersionIntoInfoVersion_spec_15_1_589(t *testing.T) {
	const release = "1.4.2"
	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	rr := httptest.NewRecorder()
	openapi.HandlerWithVersion(release).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	info, _ := doc["info"].(map[string]any)
	if got := info["version"]; got != release {
		t.Errorf("info.version: got %v, want %q", got, release)
	}
}

// spec: §15.1 line 589 — empty / "dev" build version leaves the
// embedded default in place so unconfigured deployments still serve a
// non-empty version string.
func TestHandlerKeepsEmbeddedVersionWhenBuildVersionIsEmptyOrDev_spec_15_1_589(t *testing.T) {
	for _, v := range []string{"", "dev"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
		rr := httptest.NewRecorder()
		openapi.HandlerWithVersion(v).ServeHTTP(rr, req)
		var doc map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode for %q: %v", v, err)
		}
		info, _ := doc["info"].(map[string]any)
		ver, _ := info["version"].(string)
		if ver == "" {
			t.Errorf("info.version unexpectedly empty for buildVersion %q", v)
		}
	}
}

// spec: §15.1 line 589 — version templating preserves the rest of the
// document (paths and MCP-extensions remain intact).
func TestHandlerVersionTemplatingPreservesPaths_spec_15_1_589(t *testing.T) {
	doc := openapi.DocumentWithVersion("9.9.9")
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	if _, ok := paths["/v1/sessions"]; !ok {
		t.Error("versioned document dropped /v1/sessions path")
	}
}
