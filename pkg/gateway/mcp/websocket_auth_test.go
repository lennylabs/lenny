// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/gateway/mcp"
)

// fakeRevChecker is a §27.6 playground revocation checker stub.
type fakeRevChecker struct{ revoked bool }

func (f fakeRevChecker) IsBearerRevoked(_ context.Context, _, _ string) (bool, error) {
	return f.revoked, nil
}

// spec: §27.3.1 line 142 — a client that offers `lenny.mcp.v1` (the
// sub-protocol the carrier path uses) receives `lenny.mcp.v1` echoed
// back; without the echo a browser's WebSocket negotiation fails.
func TestWebSocketEchoesSubprotocol(t *testing.T) {
	srv := mcp.NewServer()
	httpSrv, wsURL := newWSTestServer(t, srv)
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"lenny.mcp.v1"},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := conn.Subprotocol(); got != "lenny.mcp.v1" {
		t.Errorf("negotiated subprotocol = %q, want lenny.mcp.v1", got)
	}
}

// spec: §27.3.1 line 167 / §27.5.4 — when an origin=playground bearer is
// revoked mid-stream the gateway closes the WebSocket with code 4401 so
// the in-flight connection is disconnected rather than honored to token
// expiry.
func TestWebSocketRevocationClosesWith4401(t *testing.T) {
	srv := mcp.NewServer()
	srv.SetWebSocketAuth(func(_ *http.Request) (mcp.WSPrincipal, bool) {
		return mcp.WSPrincipal{Tenant: "acme", JTI: "jti-1", Origin: "playground"}, true
	}, fakeRevChecker{revoked: true}, 10*time.Millisecond)

	ctx, conn, _, teardown := dialTestServer(t, srv)
	defer teardown()

	// The watch fires within a couple of poll intervals and closes the
	// idle connection; the blocked read returns a 4401 close error.
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected the connection to close on revocation")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusCode(4401) {
		t.Errorf("close status = %v, want 4401", got)
	}
}

// spec: §27.5.4 — the revocation watch is keyed on the origin=playground
// claim; a non-playground MCP WebSocket client is never watched, so a
// revoked checker (which would never be consulted for it) does not close
// the connection.
func TestWebSocketRevocationWatchSkipsNonPlayground(t *testing.T) {
	srv := mcp.NewServer()
	srv.SetWebSocketAuth(func(_ *http.Request) (mcp.WSPrincipal, bool) {
		return mcp.WSPrincipal{Tenant: "acme", JTI: "jti-1", Origin: ""}, true
	}, fakeRevChecker{revoked: true}, 10*time.Millisecond)

	ctx, conn, _, teardown := dialTestServer(t, srv)
	defer teardown()

	// A few poll intervals elapse during the round-trip; a non-playground
	// connection stays open and serves frames.
	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	resp := readJSON(t, ctx, conn)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping should succeed for a non-playground connection: %v", resp)
	}
}

// spec: §27.5.4 — a playground connection whose bearer is not revoked
// keeps serving frames; the watch does not close a live bearer.
func TestWebSocketRevocationWatchKeepsAliveWhenNotRevoked(t *testing.T) {
	srv := mcp.NewServer()
	srv.SetWebSocketAuth(func(_ *http.Request) (mcp.WSPrincipal, bool) {
		return mcp.WSPrincipal{Tenant: "acme", JTI: "jti-1", Origin: "playground"}, true
	}, fakeRevChecker{revoked: false}, 10*time.Millisecond)

	ctx, conn, _, teardown := dialTestServer(t, srv)
	defer teardown()

	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	resp := readJSON(t, ctx, conn)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping should succeed while the bearer is live: %v", resp)
	}
}

// newWSTestServer mirrors dialTestServer but returns the server + URL so
// callers can dial with custom DialOptions (e.g. sub-protocols).
func newWSTestServer(t *testing.T, srv *mcp.Server) (*httptest.Server, string) {
	t.Helper()
	httpSrv := httptest.NewServer(srv.WebSocketHandler())
	wsURL := strings.Replace(httpSrv.URL, "http://", "ws://", 1)
	return httpSrv, wsURL
}
