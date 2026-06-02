// SPDX-License-Identifier: MIT

package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcp"
)

// spec: §27.3.1 line 142 — the sub-protocol carrier ferries the bearer as
// `lenny.mcp.v1, lenny.bearer.<token>`. WebSocketBearerCarrier promotes the
// token to `Authorization: Bearer` and strips the credential entry,
// leaving only the negotiable sub-protocol so the upgrader can echo it.
func TestWebSocketBearerCarrierPromotesAndRedacts(t *testing.T) {
	var gotAuth, gotProto string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotProto = r.Header.Get("Sec-WebSocket-Protocol")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/v1/ws", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "lenny.mcp.v1, lenny.bearer.eyJabc.def.ghi")

	mcp.WebSocketBearerCarrier(next).ServeHTTP(rr, req)

	if gotAuth != "Bearer eyJabc.def.ghi" {
		t.Errorf("Authorization = %q, want Bearer eyJabc.def.ghi", gotAuth)
	}
	if gotProto != "lenny.mcp.v1" {
		t.Errorf("sanitized Sec-WebSocket-Protocol = %q, want lenny.mcp.v1", gotProto)
	}
	// §27.3.1 line 142 credential treatment: the token must not survive
	// anywhere in the forwarded request headers.
	if strings.Contains(gotProto, "lenny.bearer.") || strings.Contains(gotProto, "eyJabc") {
		t.Errorf("credential leaked into sub-protocol header: %q", gotProto)
	}
}

// spec: §27.3.1 line 142 — a request without the bearer carrier (the
// Authorization-header upgrade path, or any non-WebSocket request) is
// passed through untouched.
func TestWebSocketBearerCarrierNoOpWithoutCarrier(t *testing.T) {
	var gotAuth, gotProto string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotProto = r.Header.Get("Sec-WebSocket-Protocol")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/v1/ws", nil)
	req.Header.Set("Authorization", "Bearer already-set")
	req.Header.Set("Sec-WebSocket-Protocol", "lenny.mcp.v1")

	mcp.WebSocketBearerCarrier(next).ServeHTTP(rr, req)

	if gotAuth != "Bearer already-set" {
		t.Errorf("Authorization = %q, want unchanged", gotAuth)
	}
	if gotProto != "lenny.mcp.v1" {
		t.Errorf("Sec-WebSocket-Protocol = %q, want unchanged", gotProto)
	}
}

// spec: §27.3.1 line 142 — when the caller already presented an
// Authorization header (the non-browser upgrade path that also happens to
// send the carrier), the carrier does not overwrite the existing
// credential but still strips the sub-protocol bearer entry.
func TestWebSocketBearerCarrierDoesNotOverwriteAuthorization(t *testing.T) {
	var gotAuth, gotProto string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotProto = r.Header.Get("Sec-WebSocket-Protocol")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/v1/ws", nil)
	req.Header.Set("Authorization", "Bearer header-token")
	req.Header.Set("Sec-WebSocket-Protocol", "lenny.mcp.v1, lenny.bearer.carrier-token")

	mcp.WebSocketBearerCarrier(next).ServeHTTP(rr, req)

	if gotAuth != "Bearer header-token" {
		t.Errorf("Authorization = %q, want the pre-existing header-token", gotAuth)
	}
	if strings.Contains(gotProto, "carrier-token") {
		t.Errorf("carrier token leaked: %q", gotProto)
	}
}

// spec: §27.3.1 line 142 — when the only offered sub-protocol is the
// bearer carrier (a degenerate client), stripping it leaves no
// sub-protocol header so the upgrader negotiates none.
func TestWebSocketBearerCarrierOnlyBearerEntry(t *testing.T) {
	var present bool
	var gotAuth string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Sec-WebSocket-Protocol"]
		gotAuth = r.Header.Get("Authorization")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/v1/ws", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "lenny.bearer.solo-token")

	mcp.WebSocketBearerCarrier(next).ServeHTTP(rr, req)

	if present {
		t.Errorf("Sec-WebSocket-Protocol header should be removed when only the bearer entry was present")
	}
	if gotAuth != "Bearer solo-token" {
		t.Errorf("Authorization = %q, want Bearer solo-token", gotAuth)
	}
}
