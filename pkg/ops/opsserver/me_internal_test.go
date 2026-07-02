// SPDX-License-Identifier: MIT

package opsserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §25.4 line 1668 — F-COV-1. When the MCP Management Server is
// unwired (s.mcp == nil), GET /v1/admin/me/authorized-tools returns 503
// AUTHORIZED_TOOLS_UNAVAILABLE with a fallback hint that names the
// gateway-hosted OpenAPI document absolute-to-gateway, so an agent that
// follows the hint reaches the gateway rather than a route lenny-ops does
// not serve. This asserts the corrected hint and fails against the pre-fix
// code, which named the relative `/v1/openapi.json` that 404s against
// lenny-ops's own origin.
func TestAuthorizedToolsHintAbsoluteToGateway_spec_25_4_1668(t *testing.T) {
	srv := &Server{me: &MeConfig{GatewayURL: "https://lenny-gateway:8443"}} // mcp nil

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil)
	rr := httptest.NewRecorder()
	srv.handleAuthorizedTools(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when MCP is unwired", rr.Code)
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "AUTHORIZED_TOOLS_UNAVAILABLE" {
		t.Fatalf("code = %q, want AUTHORIZED_TOOLS_UNAVAILABLE", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "https://lenny-gateway:8443/v1/openapi.json") {
		t.Errorf("hint must name the absolute-to-gateway OpenAPI document; message=%q", resp.Error.Message)
	}
}

// spec: §25.4 line 1668 — F-COV-1. On the dev / embedded path with no
// gateway URL, the AUTHORIZED_TOOLS_UNAVAILABLE hint falls back to the
// relative OpenAPI path rather than emitting a bare host.
func TestAuthorizedToolsHintRelativeFallback_spec_25_4_1668(t *testing.T) {
	srv := &Server{me: &MeConfig{}} // no GatewayURL, mcp nil

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil)
	rr := httptest.NewRecorder()
	srv.handleAuthorizedTools(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The message names the relative OpenAPI path; it carries no absolute
	// host when no gateway URL is configured.
	if strings.Contains(resp.Error.Message, "http") {
		t.Errorf("hint must be relative when no gateway URL is set; message=%q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "/v1/openapi.json") {
		t.Errorf("hint must still name the relative OpenAPI path; message=%q", resp.Error.Message)
	}
}

// spec: §25.4 (`/me` links resolve to the gateway-hosted resource) —
// F-COV-1. gatewayLink joins a gateway-resident path to the configured
// gateway base URL, trimming a trailing slash, and falls back to the
// relative path when no gateway URL is configured.
func TestGatewayLinkJoin(t *testing.T) {
	cases := []struct {
		name string
		me   *MeConfig
		path string
		want string
	}{
		{"absolute", &MeConfig{GatewayURL: "https://lenny-gateway:8443"}, "/v1/openapi.json", "https://lenny-gateway:8443/v1/openapi.json"},
		{"trailing slash trimmed", &MeConfig{GatewayURL: "https://lenny-gateway:8443/"}, "/v1/openapi.json", "https://lenny-gateway:8443/v1/openapi.json"},
		{"empty gateway falls back to relative", &MeConfig{}, "/v1/openapi.json", "/v1/openapi.json"},
		{"nil config falls back to relative", nil, "/v1/openapi.json", "/v1/openapi.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{me: tc.me}
			if got := s.gatewayLink(tc.path); got != tc.want {
				t.Errorf("gatewayLink(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
