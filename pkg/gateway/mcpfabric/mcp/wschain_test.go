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

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	correlationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/correlation"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	recovermw "github.com/lennylabs/lenny/pkg/gateway/middleware/recover"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// spec: §27.5 / §27.3.1 line 142 — the MCP WebSocket transport at
// /mcp/v1/ws upgrades through the gateway's full middleware chain.
// nhooyr.io/websocket performs a direct http.Hijacker type assertion on
// the ResponseWriter it is handed, so every writer-wrapping middleware on
// the path (ratelimit, request metrics, panic recovery, correlation) must
// re-expose Hijack or the upgrade fails. This test stands up the WS
// handler behind that chain, in production order, and verifies a full
// dispatch round-trip survives. Without the Hijack passthroughs the
// upgrade returns an error and the playground cannot complete a session.
func TestWebSocketUpgradeSurvivesMiddlewareChain(t *testing.T) {
	srv := mcp.NewServer()

	var h http.Handler = srv.WebSocketHandler()
	// Inner-to-outer wrapping order matches cmd/lenny-gateway/main.go: the
	// writer the handler receives is wrapped by every middleware below. A
	// non-nil Counter forces the rate-limit writer wrapper (rateLimitRW)
	// onto the path, matching production where a counter is always wired —
	// otherwise the middleware passes the writer through unwrapped and the
	// rateLimitRW.Hijack passthrough would go untested.
	h = ratelimitmw.Wrap(h, ratelimitmw.Options{Counter: rlcounter.NewMemory(), GlobalPerMinute: 1000})
	metrics, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	h = metrics.Middleware(h, func(_ *http.Request) string { return "/mcp/v1/ws" })
	h = recovermw.Middleware(h)
	h = correlationmw.Wrap(h, correlationmw.Options{})
	h = mcp.WebSocketBearerCarrier(h)

	httpSrv := httptest.NewServer(h)
	defer httpSrv.Close()
	wsURL := strings.Replace(httpSrv.URL, "http://", "ws://", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket upgrade through the middleware chain failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	resp := readJSON(t, ctx, conn)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping through the middleware chain should succeed: %v", resp)
	}
}
