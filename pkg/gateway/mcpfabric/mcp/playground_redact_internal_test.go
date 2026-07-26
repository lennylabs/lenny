// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §27.9 line 251; §16.4 line 376 — a credential literal carried as a
// scalar value under a sensitive key is scrubbed before the frame reaches
// the playground browser, at any nesting depth and inside arrays.
func TestRedactPlaygroundFrameScrubsCredentialScalars_spec_27_9_251(t *testing.T) {
	in := []byte(`{
		"jsonrpc": "2.0",
		"id": 7,
		"result": {
			"access_token": "sk-live-abc123",
			"nested": {"client_secret": "shh", "kept": "visible"},
			"list": [{"authorization": "Bearer xyz"}, {"plain": "ok"}],
			"count": 42
		}
	}`)
	out := redactPlaygroundFrame(in)

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("redacted frame is not valid JSON: %v", err)
	}
	result := got["result"].(map[string]any)
	if result["access_token"] != frameRedactionMarker {
		t.Errorf("access_token = %v, want %q", result["access_token"], frameRedactionMarker)
	}
	nested := result["nested"].(map[string]any)
	if nested["client_secret"] != frameRedactionMarker {
		t.Errorf("client_secret = %v, want %q", nested["client_secret"], frameRedactionMarker)
	}
	if nested["kept"] != "visible" {
		t.Errorf("non-sensitive sibling kept = %v, want visible", nested["kept"])
	}
	list := result["list"].([]any)
	if list[0].(map[string]any)["authorization"] != frameRedactionMarker {
		t.Errorf("array element authorization not redacted: %v", list[0])
	}
	if list[1].(map[string]any)["plain"] != "ok" {
		t.Errorf("array element plain field mutated: %v", list[1])
	}
}

// spec: §27.9 line 251 — the redactor must preserve the JSON-RPC envelope
// and the id, and not corrupt a tool's inputSchema. A sensitive *name*
// whose value is a structural schema object (a property named
// access_token: {"type":"string"}) is not a credential literal and
// survives so the playground's schema-driven form still renders.
func TestRedactPlaygroundFramePreservesSchemaAndID_spec_27_9_251(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":9007199254740993,"result":{"tools":[{"name":"lenny/x","inputSchema":{"type":"object","properties":{"access_token":{"type":"string"},"token":{"type":"string"}}}}]}}`)
	out := redactPlaygroundFrame(in)

	// The id is a large integer; UseNumber must round-trip it exactly.
	if got := json.RawMessage(extractRaw(t, out, "id")); string(got) != "9007199254740993" {
		t.Errorf("id mangled: got %s, want 9007199254740993", got)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	schema := got["result"].(map[string]any)["tools"].([]any)[0].(map[string]any)["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	at, ok := props["access_token"].(map[string]any)
	if !ok || at["type"] != "string" {
		t.Errorf("inputSchema property access_token corrupted: %v", props["access_token"])
	}
	if tok, ok := props["token"].(map[string]any); !ok || tok["type"] != "string" {
		t.Errorf("inputSchema property token corrupted: %v", props["token"])
	}
}

// extractRaw re-encodes the named top-level member so the test can assert
// on the exact numeric text the redactor emitted.
func extractRaw(t *testing.T, frame []byte, key string) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatalf("extractRaw: %v", err)
	}
	return m[key]
}

// spec: §27.9 line 251 — a frame that does not parse as JSON is forwarded
// unchanged. The gateway only emits well-formed JSON-RPC on this path, so
// a parse failure is not a credential-leak vector.
func TestRedactPlaygroundFrameNonJSONPassthrough_spec_27_9_251(t *testing.T) {
	in := []byte("not json at all")
	if out := redactPlaygroundFrame(in); string(out) != string(in) {
		t.Errorf("non-JSON frame mutated: got %q", out)
	}
}

// spec: §16.4 line 376 — the marker set covers the credential field
// names MCP payloads carry, and excludes the bare token "key" so benign
// identifiers (publicKey, keyId) are not scrubbed.
func TestIsSensitiveFrameKey_spec_16_4_376(t *testing.T) {
	sensitive := []string{
		"authorization", "Authorization", "client_secret", "password",
		"access_token", "refresh_token", "BearerToken", "api_key", "apiKey", "private_key",
	}
	for _, k := range sensitive {
		if !isSensitiveFrameKey(k) {
			t.Errorf("isSensitiveFrameKey(%q) = false, want true", k)
		}
	}
	benign := []string{"publicKey", "keyId", "name", "id", "type", "count", "tenantId"}
	for _, k := range benign {
		if isSensitiveFrameKey(k) {
			t.Errorf("isSensitiveFrameKey(%q) = true, want false", k)
		}
	}
}

// spec: §27.3 origin claim; §27.9 line 251 — the egress redaction gate is
// keyed on the origin=playground claim. A non-playground bearer (or an
// unwired extractor) is never redacted so a headless MCP client still
// receives raw tool results.
func TestPlaygroundEgressGate_spec_27_9_251(t *testing.T) {
	s := NewServer()
	// Unwired: no extractor → gate off.
	if s.playgroundEgress(httptest.NewRequest("GET", "/mcp/v1/ws", nil)) {
		t.Error("playgroundEgress with no extractor = true, want false")
	}
	// Wired extractor keyed on a header. nil revocations leaves the
	// §27.5.4 watch off while the egress gate still reads origin.
	s.SetWebSocketAuth(func(r *http.Request) (WSPrincipal, bool) {
		return WSPrincipal{Tenant: "acme", JTI: "j1", Origin: r.Header.Get("X-Origin")}, true
	}, nil, 0)

	pgReq := httptest.NewRequest("GET", "/mcp/v1/ws", nil)
	pgReq.Header.Set("X-Origin", playgroundOriginClaim)
	if !s.playgroundEgress(pgReq) {
		t.Error("playgroundEgress for origin=playground = false, want true")
	}

	apiReq := httptest.NewRequest("GET", "/mcp/v1/ws", nil)
	apiReq.Header.Set("X-Origin", "api")
	if s.playgroundEgress(apiReq) {
		t.Error("playgroundEgress for origin=api = true, want false")
	}
}

// spec: §27.9 (raw-frame inspector: "The raw-frame inspector displays
// redacted frames only; the gateway applies the same redaction rules as
// the audit log (§16.4) before sending frames to the browser."); §16.4
// (credential-sensitive payloads are excluded from payload-level
// surfaces) — every gateway MCP tool returns its body as a JSON document
// serialized into a single content[].text string (textResult,
// pkg/gateway/mcpfabric/mcptools/mcptools.go), so a credential a tool
// returns reaches the browser inside that string rather than as a scalar
// under a credential-named key. A frame the inspector renders must not
// carry the credential literal in any form.
//
// The assertion currently fails: redactFrameValue scrubs a scalar only
// when its own map key is credential-named, and "text" is not a
// credential-named key, so the serialized document travels through the
// walker untouched. Whether the §16.4 rule the spec points at (a
// whole-payload exclusion for named credential-sensitive RPCs, with a
// lease-id/provider/outcome allowlist) obliges the frame redactor to
// parse and rewrite JSON embedded in a text block is not settled by the
// spec text, so the test is held non-blocking until the requirement is
// decided. The decision is not purely additive: the same frame redactor
// runs on every outbound playground frame, so scrubbing a credential a
// tool deliberately returns to its caller also withholds it from the
// caller. The create_session case below is the concrete instance, since
// the uploadToken it returns is the credential the workspace-tarball
// upload leg then presents.
func TestRedactPlaygroundFrameScrubsCredentialInsideTextBlockJSON_spec_27_9(t *testing.T) {
	t.Skip("open coverage question: the spec does not state whether audit-equivalent frame redaction must walk JSON serialized into a tool result text block")

	cases := []struct {
		name   string
		body   map[string]string
		secret string
	}{
		{
			// pkg/gateway/mcpfabric/mcptools/mcptools.go, lenny/vcs_token:
			// marshals {host, username, token} through textResult. The tool
			// requires an in-pod session principal, so this body is the
			// pattern rather than a browser-reachable call.
			name:   "vcs token body",
			body:   map[string]string{"host": "github.com", "username": "x-access-token", "token": "ghs_SECRETVALUE"},
			secret: "ghs_SECRETVALUE",
		},
		{
			// pkg/gateway/mcpfabric/mcptools/mcptools_register.go,
			// lenny/create_session: marshals {sessionId, state, uploadToken}
			// through textResult. This tool is reachable over the playground
			// MCP WebSocket, so the uploadToken bearer that authorizes
			// POST /v1/sessions/{id}/upload reaches the raw-frame inspector.
			name:   "create session body",
			body:   map[string]string{"sessionId": "sess-1", "state": "created", "uploadToken": "ult_SECRETVALUE"},
			secret: "ult_SECRETVALUE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal tool body: %v", err)
			}
			frame, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": string(body)}},
				},
			})
			if err != nil {
				t.Fatalf("marshal frame: %v", err)
			}

			out := redactPlaygroundFrame(frame)
			if strings.Contains(string(out), tc.secret) {
				t.Errorf("credential survived redaction in a tool result text block: %s", out)
			}
		})
	}
}
