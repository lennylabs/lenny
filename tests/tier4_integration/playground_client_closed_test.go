// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §27.6 browser-close best-effort
// cancel hint: a real playground MCP WebSocket sending session.cancel
// (the `lenny/cancel_session` tool with reason `playground_client_closed`,
// the exact frame pkg/gateway/mcpfabric/playground/ui/app.js's
// `beforeunload` handler emits) against a running session, and the
// dropped-connection fallback onto the real §27.6 idle-timeout override
// when the frame never arrives.
//
// The create path (sessionserver.Server with SetPlaygroundCaps) and the
// cancel dispatch (mcptools.Register's real lenny/cancel_session handler)
// are wired against the same in-memory store, matching how
// cmd/lenny-gateway composes both against one sessionstore.Store. The
// per-handler unit edges (terminal/unknown-session idempotency) are
// pinned in pkg/gateway/mcpfabric/mcptools/session_control_test.go; this
// file exercises the two paths that require a live WebSocket and a real
// watchdog sweep: the happy-path cancel over the wire, and the
// dropped-socket fallback onto the §27.6 idle reclaim.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
)

// newPlaygroundCancelMCPServer wires a real mcp.Server with the real
// mcptools.Register session-lifecycle family (the same call
// cmd/lenny-gateway makes to expose lenny/cancel_session) against store,
// so a dialed WebSocket dispatches through the identical handler that
// backs the playground's chat pane Cancel button and its
// `beforeunload` best-effort hint.
func newPlaygroundCancelMCPServer(store sessionstore.Store) *mcp.Server {
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		TenantID: "acme",
	})
	return srv
}

// dialPlaygroundWS opens a real WebSocket against the §4.1 transport
// (the same handler /mcp/v1/ws serves in production) and returns the
// connection plus a teardown. Mirrors
// pkg/gateway/mcpfabric/mcp/websocket_test.go's dialTestServer.
func dialPlaygroundWS(t *testing.T, srv *mcp.Server) (context.Context, *websocket.Conn, func()) {
	t.Helper()
	httpSrv := httptest.NewServer(srv.WebSocketHandler())
	wsURL := strings.Replace(httpSrv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		httpSrv.Close()
		t.Fatalf("websocket dial: %v", err)
	}
	teardown := func() {
		cancel()
		httpSrv.Close()
	}
	return ctx, conn, teardown
}

// spec: §27.6 — "On browser close / navigation away, the client sends
// session.cancel with reason playground_client_closed. Gateway treats
// this as a best-effort hint" (falling through to cancellation on a
// still-running session, per the §27.5.3/§27.6.5-cited cancel_session
// handler's precondition path — a best-effort hint on a live session
// still executes the real §15.1 cancel transition, only a
// terminal/unknown target is special-cased into a no-op).
//
// diagnosis: a failure here means the exact wire frame the playground UI
// sends on browser close (pkg/gateway/mcpfabric/playground/ui/app.js's
// beforeunload handler: tools/call lenny/cancel_session with
// {sessionId, reason: "playground_client_closed"}), delivered over a
// real WebSocket against a running session, does not cancel the session
// or does not surface the reason back to the caller — so the primary,
// most common browser-close edge (the tab closing while a session is
// still running) would silently fail to release the pod, leaving the
// session to age out only via the §27.6 idle/duration caps instead of
// being cancelled promptly as the spec's best-effort hint intends.
func TestPlaygroundClientClosedCancelsRunningSessionOverWebSocket(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_open_tab", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	srv := newPlaygroundCancelMCPServer(store)
	ctx, conn, teardown := dialPlaygroundWS(t, srv)
	defer teardown()

	// The exact frame shape app.js's beforeunload handler sends, with an
	// "id" added so the test can read the response (the real browser
	// fires the notification form and does not wait for one).
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "lenny/cancel_session",
			"arguments": map[string]any{
				"sessionId": "sess_open_tab",
				"reason":    "playground_client_closed",
			},
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write session.cancel frame: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, data)
	}
	if resp.Result.IsError {
		t.Fatalf("session.cancel returned an error result: %s", data)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("session.cancel response has no content: %s", data)
	}
	text := resp.Result.Content[0].Text
	if !strings.Contains(text, `"cancelled"`) {
		t.Errorf("session.cancel response = %q, want the cancelled state", text)
	}
	if !strings.Contains(text, "playground_client_closed") {
		t.Errorf("session.cancel response = %q, want the playground_client_closed reason recorded back to the caller", text)
	}

	row, err := store.Get(context.Background(), "acme", "sess_open_tab")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateCancelled {
		t.Errorf("session state = %q, want cancelled", row.State)
	}
}

// spec: §27.6 — "a dropped WebSocket that cannot send the frame falls
// back to the idle-timeout path described above, which, because of the
// override, fires within the 5-minute playground default rather than
// after the platform maxClientIdleSeconds default."
//
// diagnosis: a failure here means a playground session whose browser tab
// closed abruptly (the WebSocket drops before the beforeunload frame is
// flushed, reproduced below by opening then closing the connection
// without ever writing a session.cancel frame) is not reclaimed by the
// real §27.6 watchdog idle sweep within the effective
// playground.maxIdleTimeSeconds window — leaving an abandoned session
// running past the spec's 5-minute reclamation bound because the
// best-effort cancel hint never arrived and nothing else picked up the
// slack.
func TestPlaygroundClientClosedIdleFallbackWhenWebSocketDropsSilently(t *testing.T) {
	rt := runtimestore.NewMemory()
	if err := rt.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
		// A looser runtime bound: the playground override below must be
		// the one that actually reclaims the session.
		SessionPolicy: &runtimestore.SessionPolicy{MaxClientIdleSeconds: 600},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	srv, store := newPlaygroundServer(t, rt, playground.Config{
		MaxIdleTimeSeconds: 300,
		MaxSessionMinutes:  1000,
	})
	row := createPlaygroundSession(t, srv, store, "claude-code")

	// The client dials the same WebSocket transport a real playground tab
	// would use, then the tab is closed before it can flush the
	// session.cancel(playground_client_closed) frame: the connection
	// drops with no frame ever written, which is the scenario the spec
	// sentence above describes as falling back to the idle-timeout path.
	cancelSrv := newPlaygroundCancelMCPServer(store)
	_, conn, teardown := dialPlaygroundWS(t, cancelSrv)
	// CloseNow tears down the TCP connection immediately, without the
	// close handshake a graceful Close performs — the closest in-test
	// approximation of a browser tab disappearing mid-connection, before
	// any frame (let alone the beforeunload session.cancel hint) reaches
	// the wire.
	if err := conn.CloseNow(); err != nil {
		t.Logf("CloseNow returned %v (acceptable — the point is no frame was sent)", err)
	}
	teardown()

	running, err := store.Update(context.Background(), "acme", row.ID, func(r *sessionstore.Session) error {
		r.State = session.StateRunning
		return nil
	})
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}

	// The dropped connection must not itself have cancelled the session:
	// the whole point of the fallback is that no cancel ever lands.
	if running.State != session.StateRunning {
		t.Fatalf("session state after the WebSocket drop = %q, want running (no cancel frame was ever sent)", running.State)
	}

	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxCreatedSeconds:              idlePlaygroundCapDisabled,
		MaxSessionAgeSeconds:           idlePlaygroundCapDisabled,
		MaxAwaitingClientActionSeconds: idlePlaygroundCapDisabled,
		MaxSuspendedPodHoldSeconds:     idlePlaygroundCapDisabled,
		MaxResumePendingSeconds:        idlePlaygroundCapDisabled,
	}, nil).WithIdleResolver(sessionidle.NewResolver(rt, nil))

	clk := clockstep.New(running.UpdatedAt)

	// Under the 300s effective playground idle cap: the abandoned session
	// still survives (the fallback has a bound, not an immediate reap).
	clk.Advance(299 * time.Second)
	res, err := w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Fatalf("IdleExpirations at 299s = %d, want 0 (under the 300s effective idle-timeout fallback)", res.IdleExpirations)
	}

	// Past the 300s effective cap: the idle-timeout fallback reclaims the
	// session the dropped WebSocket never got to cancel.
	clk.Advance(2 * time.Second)
	res, err = w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations at 301s = %d, want 1 (the idle-timeout fallback must reclaim the session the dropped WebSocket never cancelled)", res.IdleExpirations)
	}
	got, err := store.Get(context.Background(), "acme", row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateExpired {
		t.Errorf("state = %q, want expired", got.State)
	}
	if got.FailureReason != string(session.FailureExpiredIdle) {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, session.FailureExpiredIdle)
	}
}
