// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §27.5 Sec-WebSocket-Protocol bearer
// carrier: a real playground-minted bearer, driven through the real
// production middleware chain (mcp.WebSocketBearerCarrier wrapping the
// correlation access-log middleware wrapping the real mcp.Server
// WebSocket transport and the real playground.Handler mint endpoint,
// composed in the same outer-to-inner order cmd/lenny-gateway/httpsurface.go
// wires them in), asserting the credential is never observable in the
// access log or the audit trail.
//
// The suite does not boot the compiled cmd/lenny-gateway binary with
// --playground-enabled: doing so currently crash-loops on an unrelated
// defect (pkg/gateway/mcpfabric/playground/metrics.go registers the
// lenny_playground_page_views_total counter with the camelCase label
// "authMode", which the §16.1.1 snake_case validator rejects fatally at
// startup the instant playground.enabled=true is set, under every
// playground.authMode). That defect is already tracked (BUILD-GAPS.md
// §16.1 Metrics Finding 8) and already recorded as a needs-human blocker
// on several sibling findings. Composing the real middleware and handler
// types directly exercises the identical code paths the compiled binary
// runs for this concern without depending on that unrelated fix.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/correlation"
	"github.com/lennylabs/lenny/tests/testinfra/mcpschema"
)

// wsCarrierLogSink is a slog io.Writer that records every JSON log line
// the correlation access-log middleware emits and signals once a line
// naming the /mcp/v1/ws path has been captured. correlation.Wrap emits
// the request-completion line only after the handler returns — for a
// WebSocket upgrade that means only after the connection closes — so the
// test waits on this signal rather than sleeping or polling.
type wsCarrierLogSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	done chan struct{}
}

func newWSCarrierLogSink() *wsCarrierLogSink {
	return &wsCarrierLogSink{done: make(chan struct{})}
}

func (s *wsCarrierLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.buf.Write(p)
	s.mu.Unlock()
	if strings.Contains(string(p), `"path":"/mcp/v1/ws"`) {
		s.once.Do(func() { close(s.done) })
	}
	return len(p), nil
}

func (s *wsCarrierLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// spec: §27.5 (spec/27_web-playground.md:145) — "Browsers that cannot set
// `Authorization` headers on WebSocket upgrades MUST use the
// `Sec-WebSocket-Protocol` sub-protocol carrier defined for this purpose:
// the client sends `Sec-WebSocket-Protocol: lenny.mcp.v1,
// lenny.bearer.<bearerToken>` and the gateway echoes back
// `Sec-WebSocket-Protocol: lenny.mcp.v1`. The `lenny.bearer.*`
// sub-protocol entry MUST be treated as a credential (not logged, not
// emitted in access logs, redacted in audit traces) — the gateway strips
// it before audit-event emission."
//
// diagnosis: a failure here means a real playground-minted bearer,
// carried through the browser-only Sec-WebSocket-Protocol path, either
// does not negotiate the plain `lenny.mcp.v1` sub-protocol on a live
// WebSocket upgrade, or the raw bearer token leaks into the gateway's
// structured access-log line or the playground.bearer_minted audit
// event — a credential-exposure regression in the one WebSocket auth
// path available to a browser that cannot set an Authorization header.
func TestPlaygroundBearerCarrierRedactedFromAccessLogAndAudit_spec_27_5(t *testing.T) {
	signer := jwt.NewHMACSigner("pg-ws-carrier-test", []byte("playground-ws-carrier-test-secret"))
	emitter := playground.NewMemoryAuditEmitter()
	pg := playground.New(playground.Config{
		Enabled:     true,
		AuthMode:    playground.AuthModeDev,
		DevTenantID: "acme",
		BearerTTL:   900 * time.Second,
	}, playground.Options{
		Signer: signer,
	}).WithAuditEmitter(emitter)

	mcpSrv := mcp.NewServer()

	mux := http.NewServeMux()
	mux.Handle("/v1/playground/token", pg.TokenRoutes())
	mux.Handle("/mcp/v1/ws", mcpSrv.WebSocketHandler())

	sink := newWSCarrierLogSink()
	logger := slog.New(slog.NewJSONHandler(sink, nil))

	// The same outer-to-inner order cmd/lenny-gateway/httpsurface.go
	// wires: the WebSocket bearer carrier is the outermost middleware so
	// it strips the credential before the correlation access-log
	// middleware (or anything else) ever observes the header.
	var handler http.Handler = mux
	handler = correlation.Wrap(handler, correlation.Options{Logger: logger})
	handler = mcp.WebSocketBearerCarrier(handler)

	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	// Mint a real playground bearer over the real §27.3.1 mode-polymorphic
	// endpoint, dev-mode admission (empty body, no admission material).
	mintReq, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build mint request: %v", err)
	}
	mintResp, err := http.DefaultClient.Do(mintReq)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer mintResp.Body.Close()
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.NewDecoder(mintResp.Body).Decode(&minted); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	if mintResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: status = %d, want 200", mintResp.StatusCode)
	}
	if minted.BearerToken == "" {
		t.Fatal("mint response carried no bearerToken")
	}

	// Dial /mcp/v1/ws the way a browser that cannot set an Authorization
	// header on a WebSocket upgrade must: the bearer travels only inside
	// Sec-WebSocket-Protocol, exactly the wire form spec/27_web-playground.md:145
	// names.
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/mcp/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"lenny.mcp.v1", "lenny.bearer." + minted.BearerToken},
	})
	if err != nil {
		t.Fatalf("dial /mcp/v1/ws with the Sec-WebSocket-Protocol bearer carrier: %v", err)
	}

	// spec/27_web-playground.md:145 — "the gateway echoes back
	// `Sec-WebSocket-Protocol: lenny.mcp.v1`" (exactly that value, with the
	// bearer entry stripped).
	if got := conn.Subprotocol(); got != "lenny.mcp.v1" {
		conn.Close(websocket.StatusProtocolError, "")
		t.Fatalf("negotiated Sec-WebSocket-Protocol = %q, want exactly %q", got, "lenny.mcp.v1")
	}

	// Prove the connection is genuinely live under the carried bearer, not
	// just that the upgrade handshake completed.
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpschema.CurrentVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "playground-ws-carrier-test", "version": "0.0.0"},
		},
	}
	body, err := json.Marshal(initReq)
	if err != nil {
		t.Fatalf("marshal initialize frame: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write initialize frame: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	var initResp map[string]any
	if err := json.Unmarshal(data, &initResp); err != nil {
		t.Fatalf("unmarshal initialize response: %v; frame %s", err, data)
	}
	if _, isErr := initResp["error"]; isErr {
		t.Fatalf("initialize over the carrier-authenticated connection returned an error frame: %s", data)
	}

	conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the /mcp/v1/ws access-log completion line")
	}

	// spec/27_web-playground.md:145 — "not logged, not emitted in access
	// logs". The captured buffer holds every structured log line the
	// gateway's access-log middleware emitted for both requests (the mint
	// and the WebSocket upgrade); none of them may carry the raw bearer.
	logs := sink.String()
	if strings.Contains(logs, minted.BearerToken) {
		t.Errorf("access log leaked the raw bearer token: %s", logs)
	}
	if strings.Contains(logs, "lenny.bearer.") {
		t.Errorf("access log leaked the Sec-WebSocket-Protocol bearer carrier entry: %s", logs)
	}
	if !strings.Contains(logs, `"path":"/mcp/v1/ws"`) || !strings.Contains(logs, `"path":"/v1/playground/token"`) {
		t.Fatalf("access log is missing the expected request-completion lines, so the leak assertion above is not meaningful: %s", logs)
	}

	// spec/27_web-playground.md:145 — "redacted in audit traces". The
	// playground.bearer_minted event carries the jti, never the signed
	// bearer string; assert that guarantee against the real emitted event
	// rather than the event's shape alone.
	events := emitter.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want exactly 1 playground.bearer_minted event", len(events))
	}
	if events[0].Type != "playground.bearer_minted" {
		t.Errorf("audit event type = %q, want playground.bearer_minted", events[0].Type)
	}
	eventJSON, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal audit events: %v", err)
	}
	if strings.Contains(string(eventJSON), minted.BearerToken) {
		t.Errorf("audit event payload leaked the raw bearer token: %s", eventJSON)
	}
}
