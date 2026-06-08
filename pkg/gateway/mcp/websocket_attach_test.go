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
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
)

func attachTS() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// attachWiredServer builds a §4.1 WebSocket MCP server whose §15.2 attach
// channel is wired to bus under tenant "acme". The optional authorize gate
// lets a test exercise the §7.2 authorization rejection path.
func attachWiredServer(bus *sessionevents.Bus, authorize func(ctx context.Context, tenantID, sessionID string) error) *mcp.Server {
	srv := mcp.NewServer()
	srv.SetAttach(mcp.AttachConfig{
		Events:            bus,
		TenantFromRequest: func(*http.Request) string { return "acme" },
		Authorize:         authorize,
		Now:               attachTS,
	})
	return srv
}

// sendAttach sends the attach_session tools/call frame.
func sendAttach(t *testing.T, ctx context.Context, conn *websocket.Conn, sessionID string, resumeFromSeq uint64) {
	t.Helper()
	args := map[string]any{"sessionId": sessionID}
	if resumeFromSeq > 0 {
		args["resumeFromSeq"] = resumeFromSeq
	}
	writeJSON(t, ctx, conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      "attach-1",
		"method":  "tools/call",
		"params":  map[string]any{"name": "lenny/attach_session", "arguments": args},
	})
}

// sessionEventParams reads the next frame and asserts it is a
// notifications/lenny/sessionEvent, returning its params.
func sessionEventParams(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	frame := readJSON(t, ctx, conn)
	if frame["method"] != "notifications/lenny/sessionEvent" {
		t.Fatalf("frame method = %v, want notifications/lenny/sessionEvent (frame=%v)", frame["method"], frame)
	}
	params, ok := frame["params"].(map[string]any)
	if !ok {
		t.Fatalf("sessionEvent params missing: %v", frame)
	}
	return params
}

// spec: §15.2 lines 1331/1370; §27.5 R2; F-27.4.7 — an attach_session
// tools/call over the WebSocket acks, replays the retained backlog as
// notifications/lenny/sessionEvent frames, then pushes live events on the
// same socket. This is the stream the playground chat consumes.
func TestWebSocketAttachStreamsBacklogThenLive_spec_15_2_F_27_4_7(t *testing.T) {
	bus := sessionevents.NewBus(256)
	bus.PublishForTenant("acme", "sess-1", "status_change", `{"state":"running"}`, attachTS())
	bus.PublishForTenant("acme", "sess-1", "response", `{"type":"text","text":"hi"}`, attachTS())

	ctx, conn, _, teardown := dialTestServer(t, attachWiredServer(bus, nil))
	defer teardown()

	sendAttach(t, ctx, conn, "sess-1", 0)

	// The ack precedes any event frame.
	ack := readJSON(t, ctx, conn)
	result, ok := ack["result"].(map[string]any)
	if !ok || result["attached"] != true {
		t.Fatalf("attach ack = %v, want result.attached=true", ack)
	}

	// Backlog: status_change (seq 1) then response (seq 2).
	p1 := sessionEventParams(t, ctx, conn)
	if p1["type"] != "status_change" {
		t.Fatalf("backlog frame 1 type = %v, want status_change", p1["type"])
	}
	p2 := sessionEventParams(t, ctx, conn)
	if p2["type"] != "response" {
		t.Fatalf("backlog frame 2 type = %v, want response", p2["type"])
	}

	// Live: publish after the subscription is active (ack already read).
	bus.PublishForTenant("acme", "sess-1", "response", `{"type":"text","text":"world"}`, attachTS())
	live := sessionEventParams(t, ctx, conn)
	if live["type"] != "response" {
		t.Fatalf("live frame type = %v, want response", live["type"])
	}
	data, _ := live["data"].(map[string]any)
	if data["text"] != "world" {
		t.Fatalf("live frame text = %v, want world", data["text"])
	}
}

// spec: §15.2 line 1331; F-27.4.7 — when the resume cursor sits below the
// oldest retained event the WebSocket leg emits one gap_detected frame ahead
// of the backlog, mirroring the SSE leg.
func TestWebSocketAttachGapDetected_spec_15_2(t *testing.T) {
	bus := sessionevents.NewBus(2) // retains the last 2 events
	for i := 0; i < 4; i++ {
		bus.PublishForTenant("acme", "sess-1", "response", `{}`, attachTS())
	}
	// history now holds seq 3,4; oldest retained = 3.

	ctx, conn, _, teardown := dialTestServer(t, attachWiredServer(bus, nil))
	defer teardown()

	sendAttach(t, ctx, conn, "sess-1", 1)
	if _, ok := readJSON(t, ctx, conn)["result"]; !ok {
		t.Fatal("expected attach ack first")
	}

	gap := readJSON(t, ctx, conn)
	if gap["method"] != "notifications/lenny/gapDetected" {
		t.Fatalf("expected gap_detected frame, got %v", gap)
	}
	params := gap["params"].(map[string]any)
	if params["lastSeenSeq"].(float64) != 1 || params["nextSeq"].(float64) != 3 {
		t.Fatalf("gap params = %v, want lastSeenSeq=1 nextSeq=3", params)
	}
	// The backlog (seq 3) follows the gap marker.
	if p := sessionEventParams(t, ctx, conn); p["seq"].(float64) != 3 {
		t.Fatalf("frame after gap seq = %v, want 3", p["seq"])
	}
}

// spec: §7.2 isolation; F-27.4.7 — a foreign or missing session is rejected
// with a structured JSON-RPC error before any event frame, and the
// connection stays usable for a corrected next frame.
func TestWebSocketAttachAuthorizeRejection_spec_7_2(t *testing.T) {
	bus := sessionevents.NewBus(16)
	authorize := func(_ context.Context, _, sessionID string) error {
		return mcp.NewToolError("RESOURCE_NOT_FOUND", "session not found", nil)
	}
	ctx, conn, _, teardown := dialTestServer(t, attachWiredServer(bus, authorize))
	defer teardown()

	sendAttach(t, ctx, conn, "ghost", 0)
	resp := readJSON(t, ctx, conn)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got %v", resp)
	}
	data := errObj["data"].(map[string]any)
	if data["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatalf("error code = %v, want RESOURCE_NOT_FOUND", data["code"])
	}

	// The connection survives the rejection.
	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "p", "method": "ping"})
	if _, ok := readJSON(t, ctx, conn)["result"]; !ok {
		t.Error("ping after attach rejection should still succeed")
	}
}

// spec: §4.1 / §15.2; F-27.4.7 — request/response frames keep working while
// an attach push is live on the same socket. The read loop dispatches a ping
// while the push goroutine tails the bus.
func TestWebSocketAttachInterleavesWithRequestResponse(t *testing.T) {
	bus := sessionevents.NewBus(16)
	ctx, conn, _, teardown := dialTestServer(t, attachWiredServer(bus, nil))
	defer teardown()

	sendAttach(t, ctx, conn, "sess-1", 0)
	if _, ok := readJSON(t, ctx, conn)["result"]; !ok {
		t.Fatal("expected attach ack")
	}

	// A request/response frame is served while attached.
	writeJSON(t, ctx, conn, map[string]any{"jsonrpc": "2.0", "id": "ping-1", "method": "ping"})
	pong := readJSON(t, ctx, conn)
	if _, ok := pong["result"].(map[string]any); !ok {
		t.Fatalf("ping result missing: %v", pong)
	}

	// A live session event then pushes over the same socket.
	bus.PublishForTenant("acme", "sess-1", "response", `{"type":"text","text":"streamed"}`, attachTS())
	live := sessionEventParams(t, ctx, conn)
	if live["data"].(map[string]any)["text"] != "streamed" {
		t.Fatalf("live frame = %v", live)
	}
}

// spec: §15.2; F-27.4.7 — an attach_session with no sessionId is a
// VALIDATION_ERROR and never opens a stream.
func TestWebSocketAttachMissingSessionID(t *testing.T) {
	bus := sessionevents.NewBus(16)
	ctx, conn, _, teardown := dialTestServer(t, attachWiredServer(bus, nil))
	defer teardown()

	writeJSON(t, ctx, conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      "attach-bad",
		"method":  "tools/call",
		"params":  map[string]any{"name": "lenny/attach_session", "arguments": map[string]any{}},
	})
	resp := readJSON(t, ctx, conn)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got %v", resp)
	}
	if errObj["data"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("error code = %v, want VALIDATION_ERROR", errObj["data"])
	}
}

// spec: §27.9 line 251; F-27.4.7 — a pushed session-event frame on a
// playground-origin connection is run through the same §16.4 redaction the
// request/response path applies, so a credential field in the event payload
// reaches the raw-frame inspector scrubbed.
func TestWebSocketAttachRedactsPlaygroundEgress_spec_27_9_251(t *testing.T) {
	bus := sessionevents.NewBus(16)
	srv := attachWiredServer(bus, nil)
	srv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
		return mcp.WSPrincipal{Tenant: "acme", JTI: "j1", Origin: r.Header.Get("X-Test-Origin")}, true
	}, nil, 0)

	httpSrv := httptest.NewServer(srv.WebSocketHandler())
	defer httpSrv.Close()
	wsURL := strings.Replace(httpSrv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Test-Origin": []string{"playground"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	sendAttach(t, ctx, conn, "sess-1", 0)
	if _, ok := readJSON(t, ctx, conn)["result"]; !ok {
		t.Fatal("expected attach ack")
	}

	bus.PublishForTenant("acme", "sess-1", "response", `{"text":"ok","access_token":"sk-secret"}`, attachTS())
	live := sessionEventParams(t, ctx, conn)
	data := live["data"].(map[string]any)
	if data["access_token"] != "[REDACTED]" {
		t.Fatalf("playground egress did not redact access_token: %v", data)
	}
	if data["text"] != "ok" {
		t.Fatalf("redaction corrupted benign field: %v", data)
	}
}
