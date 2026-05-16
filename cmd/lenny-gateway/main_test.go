// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §4.9 — the gateway's LLM reverse-proxy listener wiring.

func TestNewLLMProxyServerDisabledWhenAddrEmpty(t *testing.T) {
	if srv := newLLMProxyServer("", "2023-06-01"); srv != nil {
		t.Errorf("newLLMProxyServer with an empty address returned %v, want nil", srv)
	}
}

func TestNewLLMProxyServerBindsConfiguredAddress(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01")
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil for a configured address")
	}
	if srv.Addr != ":8443" {
		t.Errorf("Addr = %q, want :8443", srv.Addr)
	}
}

func TestNewLLMProxyServerRoutesTheMessagesEndpoint(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01")
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil")
	}

	// A POST with no lease token reaches the proxy handler, which
	// rejects it — proof the route is wired to the §4.9 handler.
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[]}`))
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST /llm-proxy/v1/messages = %d, want 401 (no lease token)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "LEASE_TOKEN_MISSING") {
		t.Errorf("body = %q, want a LEASE_TOKEN_MISSING rejection", rr.Body.String())
	}
}

func TestNewLLMProxyServerRejectsUnknownPath(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01")
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/no-such-endpoint", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST to an unknown proxy path = %d, want 404", rr.Code)
	}
}

func TestNewLLMProxyServerRejectsNonPost(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01")
	req := httptest.NewRequest(http.MethodGet, "/llm-proxy/v1/messages", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /llm-proxy/v1/messages = %d, want 405", rr.Code)
	}
}
