// SPDX-License-Identifier: MIT

package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
)

// outcomeRecorder is a concurrency-safe §27.8 ws-connect-outcome sink for
// SetWebSocketMetrics.
type outcomeRecorder struct {
	mu       sync.Mutex
	outcomes []string
}

func (r *outcomeRecorder) record(outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, outcome)
}

func (r *outcomeRecorder) count(outcome string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, o := range r.outcomes {
		if o == outcome {
			n++
		}
	}
	return n
}

// diagnosis: a failure here means the /mcp/v1/ws accept path (the
// playground UI's live WebSocket entry point) no longer drives the §27.8
// lenny_playground_ws_connect_total{outcome} recorder installed through
// SetWebSocketMetrics for a connection whose principal carries the
// origin=playground claim — i.e. the counter goes back to being
// permanently zero in production because nothing on the real accept path
// calls it.
//
// spec: §27.8 — the metrics table's `lenny_playground_ws_connect_total`
// row: "MCP WebSocket connections opened from the playground
// (success/failure)".
func TestWebSocketAcceptPathRecordsPlaygroundWSConnectOutcome_spec_27_8(t *testing.T) {
	srv := mcp.NewServer()
	srv.SetWebSocketAuth(func(_ *http.Request) (mcp.WSPrincipal, bool) {
		return mcp.WSPrincipal{Tenant: "acme", JTI: "jti-1", Origin: "playground"}, true
	}, fakeRevChecker{revoked: false}, time.Hour)
	rec := &outcomeRecorder{}
	srv.SetWebSocketMetrics(rec.record)

	// A real, successful WebSocket upgrade through /mcp/v1/ws (the same
	// handler the playground UI connects through) must record "success".
	// The ping round trip forces the server-side handler goroutine past
	// the accept-time recordWSConnectOutcome call (it runs strictly
	// before the read loop that answers the ping), so observing the
	// ping response deterministically orders the "success" record ahead
	// of the count check below — without it, the client's Dial return
	// (unblocked by the 101 response nhooyr writes as part of Accept)
	// races the server's next statement.
	ctx, conn, _, teardown := dialTestServer(t, srv)
	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	readJSON(t, ctx, conn)
	teardown()

	// A request that never completes the WebSocket handshake (no
	// Upgrade/Connection headers) fails at websocket.Accept and must
	// record "failure" — this is the connection-opened-from-the-
	// playground failure leg the spec row names. Reading the full HTTP
	// error response (net/http flushes a handler's buffered output when
	// the handler returns) orders the accept-failure branch's
	// recordWSConnectOutcome call, which runs immediately before that
	// return, ahead of the count check below.
	httpSrv := httptest.NewServer(srv.WebSocketHandler())
	defer httpSrv.Close()
	resp, err := http.Get(httpSrv.URL)
	if err != nil {
		t.Fatalf("GET (non-upgrade request): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("non-upgrade request unexpectedly succeeded as a websocket upgrade (status %d)", resp.StatusCode)
	}

	if got := rec.count("success"); got != 1 {
		t.Errorf("ws_connect_total{outcome=success} increments = %d, want 1 (recorded: %v)", got, rec.outcomes)
	}
	if got := rec.count("failure"); got != 1 {
		t.Errorf("ws_connect_total{outcome=failure} increments = %d, want 1 (recorded: %v)", got, rec.outcomes)
	}
}

// diagnosis: a failure here means the /mcp/v1/ws accept path counts a
// non-playground-origin connection against lenny_playground_ws_connect_total,
// which would over-count the metric's documented scope: connections
// "opened from the playground", not every MCP WebSocket client.
//
// spec: §27.8 — the metrics table's `lenny_playground_ws_connect_total`
// row: "MCP WebSocket connections opened from the playground
// (success/failure)".
func TestWebSocketAcceptPathSkipsWSConnectOutcomeForNonPlayground_spec_27_8(t *testing.T) {
	srv := mcp.NewServer()
	srv.SetWebSocketAuth(func(_ *http.Request) (mcp.WSPrincipal, bool) {
		// No origin claim: a headless MCP client, not a playground
		// connection.
		return mcp.WSPrincipal{Tenant: "acme", JTI: "jti-1", Origin: ""}, true
	}, fakeRevChecker{revoked: false}, time.Hour)
	rec := &outcomeRecorder{}
	srv.SetWebSocketMetrics(rec.record)

	// The ping round trip orders the accept-time recordWSConnectOutcome
	// no-op ahead of the count check below (same reasoning as the
	// sibling test above).
	ctx, conn, _, teardown := dialTestServer(t, srv)
	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	readJSON(t, ctx, conn)
	teardown()

	if got := rec.count("success"); got != 0 {
		t.Errorf("ws_connect_total{outcome=success} increments = %d, want 0 for a non-playground connection (recorded: %v)", got, rec.outcomes)
	}
}
