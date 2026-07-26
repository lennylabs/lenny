// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §27.9 raw-frame inspector
// guarantee: a session event carrying credential material is scrubbed
// by the gateway before the frame reaches the browser, and the scrub
// keeps exactly the fields the §16.4 audit-log rule permits a
// credential-bearing payload to record.
//
// The journey is driven end to end through the production types
// cmd/lenny-gateway/httpsurface.go composes: a real dev-mode
// playground.Handler mint, the real pkg/gateway/middleware/auth bearer
// chain, the real mcp.Server WebSocket transport with its
// SetWebSocketAuth principal extractor reading the authmw context, and
// the real §15.2 attach_session event push. Nothing about the origin
// claim is stubbed — the bearer that opens the socket is the one the
// mint endpoint issued.
//
// The suite does not boot the compiled cmd/lenny-gateway binary with
// --playground-enabled: doing so currently crash-loops on an unrelated
// defect (pkg/gateway/mcpfabric/playground/metrics.go registers the
// lenny_playground_page_views_total counter with the camelCase label
// "authMode", which the §16.1.1 snake_case validator rejects fatally at
// startup, under every playground.authMode). That defect is already
// tracked in BUILD-GAPS.md (§16.1 Metrics Finding 8) and is already the
// stated reason tests/tier4_integration/playground_ws_carrier_test.go,
// playground_idle_override_test.go, and playground_authmode_matrix_test.go
// compose the real middleware and handler types directly. This suite
// follows the same convention.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// frameRedactionSecrets are the credential literals the test plants in
// the session event payload. Each one is checked against the raw bytes
// the browser leg receives, so a partial scrub (one field handled, a
// sibling missed) fails rather than passing on the first assertion.
var frameRedactionSecrets = []string{
	"sk-live-alice-must-not-reach-the-browser",
	"cs-live-alice-must-not-reach-the-browser",
	"Bearer alice-must-not-reach-the-browser",
}

// frameRedactionMarker is the placeholder the gateway substitutes for a
// scrubbed credential value. It is asserted as a literal because it is
// what the raw-frame inspector renders in place of the credential; the
// browser sees this string, so the test pins it from the outside rather
// than reading the gateway's unexported constant.
const frameRedactionMarker = "[REDACTED]"

// frameRedactionEventData is one §15.1 tool_use session event whose
// arguments carry a credential lease: the two fields §16.4 permits a
// credential-bearing payload to record (lease id and provider type)
// alongside the secret material §16.4 excludes. The gateway projects it
// to a notifications/lenny/toolCall frame whose `arguments` member is a
// structured JSON object, which is the field the playground chat pane's
// raw-frame inspector renders.
func frameRedactionEventData() string {
	data, err := json.Marshal(map[string]any{
		"tool_call_id": "tc-1",
		"tool":         "acme/connect_provider",
		"phase":        "completed",
		"args": map[string]any{
			"lease_id":      "lease-7",
			"provider":      "github",
			"endpoint":      "https://api.acme.example",
			"access_token":  frameRedactionSecrets[0],
			"client_secret": frameRedactionSecrets[1],
			"authorization": frameRedactionSecrets[2],
		},
		"result": map[string]any{"outcome": "ok"},
	})
	if err != nil {
		panic("marshal frame-redaction event data: " + err.Error())
	}
	return string(data)
}

// frameRedactionStack composes the production handler chain and returns
// the running server, the event bus the test publishes into, and the
// signer both the playground mint and the control bearer are issued by.
func frameRedactionStack(t *testing.T) (*httptest.Server, *sessionevents.Bus, jwt.Signer) {
	t.Helper()

	signer := jwt.NewHMACSigner("pg-frame-redaction-test", []byte("playground-frame-redaction-test-secret"))

	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	}); err != nil {
		t.Fatalf("seed tenant registry: %v", err)
	}

	pg := playground.New(playground.Config{
		Enabled:     true,
		AuthMode:    playground.AuthModeDev,
		MultiTenant: true,
		DevTenantID: "acme",
		BearerTTL:   900 * time.Second,
	}, playground.Options{Signer: signer, Tenants: tenants})

	bus := sessionevents.NewBus(256)

	mcpSrv := mcp.NewServer()
	mcpSrv.SetAttach(mcp.AttachConfig{
		Events: bus,
		TenantFromRequest: func(r *http.Request) string {
			p, ok := authmw.FromContext(r.Context())
			if !ok {
				return ""
			}
			return p.TenantID
		},
	})
	// §27.9 — the same principal extractor cmd/lenny-gateway/httpsurface.go
	// installs: the origin claim that gates the egress redaction is read
	// from the authmw context the verified bearer populated, so this test
	// cannot accidentally assert on a test-only origin signal.
	mcpSrv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
		p, ok := authmw.FromContext(r.Context())
		if !ok {
			return mcp.WSPrincipal{}, false
		}
		return mcp.WSPrincipal{Tenant: p.TenantID, JTI: p.JTI, Origin: p.Origin}, true
	}, nil, 0)

	mux := http.NewServeMux()
	mux.Handle("/v1/playground/token", pg.TokenRoutes())
	mux.Handle("/mcp/v1/ws", mcpSrv.WebSocketHandler())

	handler := authmw.Wrap(mux, authmw.Options{
		MultiTenant: true,
		Verifier:    signer,
		Registry:    tenants,
	})

	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)
	return httpSrv, bus, signer
}

// frameRedactionMint runs the real §27.3.1 dev-mode mint and returns the
// session-capability bearer, which carries the origin=playground claim.
func frameRedactionMint(t *testing.T, httpSrv *httptest.Server) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/playground/token", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build mint request: %v", err)
	}
	resp, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: status = %d, want 200", resp.StatusCode)
	}
	var minted struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	if minted.BearerToken == "" {
		t.Fatal("mint response carried no bearerToken")
	}
	return minted.BearerToken
}

// frameRedactionAttach dials /mcp/v1/ws with bearer, sends the §15.2
// attach_session tools/call, reads the ack, and returns the raw bytes of
// the first pushed event frame. Returning the raw frame (rather than a
// decoded map) lets the caller scan for credential literals anywhere in
// the delivered payload, including inside nested strings.
func frameRedactionAttach(t *testing.T, httpSrv *httptest.Server, bearer, sessionID string) []byte {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/mcp/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: httpSrv.Client(),
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + bearer}},
	})
	if err != nil {
		t.Fatalf("dial /mcp/v1/ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	attach, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "attach-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "lenny/attach_session",
			"arguments": map[string]any{"sessionId": sessionID},
		},
	})
	if err != nil {
		t.Fatalf("marshal attach frame: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, attach); err != nil {
		t.Fatalf("write attach frame: %v", err)
	}

	_, ack, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read attach ack: %v", err)
	}
	var ackFrame map[string]any
	if err := json.Unmarshal(ack, &ackFrame); err != nil {
		t.Fatalf("unmarshal attach ack: %v; frame %s", err, ack)
	}
	result, _ := ackFrame["result"].(map[string]any)
	if result == nil || result["attached"] != true {
		t.Fatalf("attach ack = %s, want result.attached=true", ack)
	}

	_, frame, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read pushed event frame: %v", err)
	}
	return frame
}

// frameRedactionArguments decodes the pushed notifications/lenny/toolCall
// frame and returns its params.arguments object, the member the raw-frame
// inspector renders for a tool-call event.
func frameRedactionArguments(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(frame, &decoded); err != nil {
		t.Fatalf("unmarshal pushed frame: %v; frame %s", err, frame)
	}
	if decoded["method"] != "notifications/lenny/toolCall" {
		t.Fatalf("pushed frame method = %v, want notifications/lenny/toolCall; frame %s", decoded["method"], frame)
	}
	params, _ := decoded["params"].(map[string]any)
	if params == nil {
		t.Fatalf("pushed frame carried no params: %s", frame)
	}
	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		t.Fatalf("pushed frame carried no params.arguments: %s", frame)
	}
	return args
}

// spec: §27.9 (spec/27_web-playground.md:254) — "The raw-frame inspector
// displays redacted frames only; the gateway applies the same redaction
// rules as the audit log ([§16.4](16_observability.md)) before sending
// frames to the browser."; §16.4 (spec/16_observability.md:383) —
// "**Credential-sensitive RPCs** (`AssignCredentials`,
// `RotateCredentials`) are excluded from payload-level logging, gRPC
// access logs, and OTel trace span attributes. Only RPC name, lease ID,
// provider type, and outcome are recorded."
//
// The test pins both halves of the §27.9 sentence on one delivered
// frame. The redaction half: every credential literal in the session
// event is replaced with the inspector placeholder and appears nowhere
// in the bytes the browser leg receives. The audit-equivalence half: the
// fields that survive the scrub are exactly the ones §16.4 permits a
// credential-bearing payload to record, cross-checked field for field
// against pkg/adapter.SafeCredentialFields — the helper that implements
// the §16.4 rule for the credential-sensitive gRPC surface — run over an
// AssignCredentialsRequest carrying the same lease.
//
// diagnosis: a failure here means credential material carried on a §15.1
// session event reaches the playground browser's raw-frame inspector.
// Either the origin=playground claim the real mint stamps is no longer
// reaching mcp.Server's egress gate through the authmw principal (the
// SetWebSocketAuth extractor or the middleware chain regressed), or the
// §15.2 attach push path stopped applying the scrub, or the scrub's
// field coverage narrowed and no longer excludes what §16.4 excludes. If
// instead the §16.4-recordable half fails, the scrub has widened past
// the audit rule and is destroying the lease id or provider type the
// inspector needs to stay useful for debugging.
func TestPlaygroundRawFrameInspectorRedactionMatchesAuditRule_spec_27_9(t *testing.T) {
	httpSrv, bus, signer := frameRedactionStack(t)

	// The §16.4 rule's own implementation for the credential-sensitive
	// gRPC surface, run over a lease carrying the identical lease id,
	// provider, and secret payload the session event carries. Its two
	// return values are the complete set of credential-derived fields
	// §16.4 permits to be recorded, so they are what the delivered frame
	// must still show.
	auditLeaseIDs, auditProviders := adapter.SafeCredentialFields(&adapterv1.AssignCredentialsRequest{
		Leases: map[string]*adapterv1.CredentialLease{
			"github": {
				LeaseId:  "lease-7",
				Provider: "github",
				Payload:  []byte(`{"access_token":"` + frameRedactionSecrets[0] + `"}`),
			},
		},
	})
	if len(auditLeaseIDs) != 1 || len(auditProviders) != 1 {
		t.Fatalf("SafeCredentialFields returned leaseIDs=%v providers=%v, want one of each", auditLeaseIDs, auditProviders)
	}

	// ---- playground leg: the browser's connection ----
	bus.PublishForTenant("acme", "sess-redaction", "tool_use_completed", frameRedactionEventData(), time.Now().UTC())
	pgFrame := frameRedactionAttach(t, httpSrv, frameRedactionMint(t, httpSrv), "sess-redaction")

	for _, secret := range frameRedactionSecrets {
		if strings.Contains(string(pgFrame), secret) {
			t.Errorf("credential literal %q reached the playground browser in the delivered frame: %s", secret, pgFrame)
		}
	}

	pgArgs := frameRedactionArguments(t, pgFrame)
	for _, field := range []string{"access_token", "client_secret", "authorization"} {
		if got := pgArgs[field]; got != frameRedactionMarker {
			t.Errorf("delivered frame arguments.%s = %v, want %q", field, got, frameRedactionMarker)
		}
	}

	// Audit equivalence: the §16.4-recordable fields survive, matching
	// the audit-log helper's output value for value.
	if got := pgArgs["lease_id"]; got != auditLeaseIDs[0] {
		t.Errorf("delivered frame arguments.lease_id = %v, want %q (the lease ID §16.4 permits recording)", got, auditLeaseIDs[0])
	}
	if got := pgArgs["provider"]; got != auditProviders[0] {
		t.Errorf("delivered frame arguments.provider = %v, want %q (the provider type §16.4 permits recording)", got, auditProviders[0])
	}
	// A non-credential sibling is untouched, so the inspector is still a
	// usable debugging surface rather than a wholesale blanked payload.
	if got := pgArgs["endpoint"]; got != "https://api.acme.example" {
		t.Errorf("delivered frame arguments.endpoint = %v, want the unmodified value", got)
	}

	// ---- control leg: a non-playground MCP client ----
	// The identical event over a bearer without the origin=playground
	// claim is delivered unredacted. This proves the scrub observed above
	// is caused by the playground origin the mint stamped, rather than by
	// the event bus or the projection dropping the fields upstream of the
	// egress gate.
	apiBearer, err := signer.Sign(jwt.Claims{
		Issuer:    "pg-frame-redaction-test",
		Subject:   "bob@acme.com",
		TenantID:  "acme",
		Typ:       auth.TokenSessionCapability,
		JWTID:     "api-jti-1",
		IssuedAt:  time.Now().Add(-time.Minute).Unix(),
		Expiry:    time.Now().Add(15 * time.Minute).Unix(),
		Scope:     "tools:sessions:*",
		SessionID: "sess-redaction",
	})
	if err != nil {
		t.Fatalf("sign non-playground bearer: %v", err)
	}

	bus.PublishForTenant("acme", "sess-redaction-api", "tool_use_completed", frameRedactionEventData(), time.Now().UTC())
	apiFrame := frameRedactionAttach(t, httpSrv, apiBearer, "sess-redaction-api")
	apiArgs := frameRedactionArguments(t, apiFrame)
	if got := apiArgs["access_token"]; got != frameRedactionSecrets[0] {
		t.Errorf("non-playground client arguments.access_token = %v, want the unredacted value: the egress gate is not keyed on the origin claim", got)
	}
}
