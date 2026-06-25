// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// toolListedSchema returns the InputSchema the MCP `tools/list` method
// publishes for the named tool. It feeds the spec-alignment assertions
// below — the schema MUST match the §8.5 contract verbatim, so the
// tests read it from the live MCP surface rather than the source.
func toolListedSchema(t *testing.T, h http.Handler, name string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list response decode: %v", err)
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		if m["name"] == name {
			schema, _ := m["inputSchema"].(map[string]any)
			return schema
		}
	}
	t.Fatalf("tools/list did not surface %q", name)
	return nil
}

// requiredSet returns the JSON Schema `required` slice as a set so
// assertions read like "X is required, Y is not".
func requiredSet(t *testing.T, schema map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	req, _ := schema["required"].([]any)
	for _, r := range req {
		if s, ok := r.(string); ok {
			out[s] = true
		}
	}
	return out
}

// schemaServer builds a minimal MCP server with the §8.5 tools wired so
// the alignment assertions can read schemas via tools/list.
func schemaServer(t *testing.T) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        memstore.New(),
		InputWaits:   inputwait.NewRegistry(),
		Events:       sessionevents.NewBus(0),
		Interactions: interactionstore.NewMemory(),
		Memory:       memorystore.NewInMemory(0, nil),
		TenantID:     "acme",
	})
	return srv
}

// TestOutputSchemaMatchesSpec asserts the §8.5 line 544 schema:
// `required: ["output"]` only. The legacy `sessionId` is accepted as a
// transport fallback property but MUST NOT be required.
// spec: §8.5 line 544; F-8.5.11.
func TestOutputSchemaMatchesSpec_spec_8_5_F_8_5_11(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/output")
	req := requiredSet(t, schema)
	if !req["output"] {
		t.Errorf("schema.required = %v, want output (§8.5 line 544)", req)
	}
	if req["sessionId"] {
		t.Errorf("schema.required includes sessionId; the §8.5 line 544 schema lists only `output`")
	}
}

// TestRequestInputSchemaMatchesSpec asserts the §8.5 line 539 contract:
// `lenny/request_input(parts)`. The required input is `parts` (an
// MessagePart[]), not the legacy flat `prompt` string. `sessionId` and
// `requestId` are extension fields and MUST NOT be required.
// spec: §8.5 line 539; F-8.5.12.
func TestRequestInputSchemaMatchesSpec_spec_8_5_F_8_5_12(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/request_input")
	req := requiredSet(t, schema)
	if !req["parts"] {
		t.Errorf("schema.required = %v, want parts (§8.5 line 539)", req)
	}
	if req["prompt"] {
		t.Errorf("schema.required includes prompt; the §8.5 contract is `lenny/request_input(parts)`")
	}
	if req["sessionId"] || req["requestId"] {
		t.Errorf("schema.required = %v; only `parts` is required by §8.5 line 539", req)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["parts"]; !ok {
		t.Errorf("schema.properties is missing `parts`: %v", props)
	}
	if _, ok := props["prompt"]; ok {
		t.Errorf("schema.properties still carries legacy `prompt`; spec replaced it with `parts`")
	}
}

// TestRequestElicitationSchemaMatchesSpec asserts the §8.5 line 559
// schema: `required: ["schema", "message"]`. The legacy `sessionId`
// is accepted as a transport fallback but MUST NOT be required.
// spec: §8.5 line 559; F-8.5.13.
func TestRequestElicitationSchemaMatchesSpec_spec_8_5_F_8_5_13(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/request_elicitation")
	req := requiredSet(t, schema)
	if !req["schema"] || !req["message"] {
		t.Errorf("schema.required = %v, want both `schema` and `message` (§8.5 line 559)", req)
	}
	if req["sessionId"] {
		t.Errorf("schema.required includes sessionId; only `schema` and `message` are required by §8.5 line 559")
	}
}

// TestMemoryWriteSchemaMatchesSpec asserts the §8.5 line 577 schema:
// `required: ["content"]` and `metadata.additionalProperties = string`.
// spec: §8.5 line 577; F-8.5.14.
func TestMemoryWriteSchemaMatchesSpec_spec_8_5_F_8_5_14(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/memory_write")
	req := requiredSet(t, schema)
	if !req["content"] {
		t.Errorf("schema.required = %v, want content (§8.5 line 577)", req)
	}
	if req["sessionId"] {
		t.Errorf("schema.required includes sessionId; only `content` is required by §8.5 line 577")
	}
	props, _ := schema["properties"].(map[string]any)
	meta, _ := props["metadata"].(map[string]any)
	ap, _ := meta["additionalProperties"].(map[string]any)
	if ap["type"] != "string" {
		t.Errorf("metadata.additionalProperties.type = %v, want string (§8.5 line 577)", ap)
	}
}

// TestMemoryQuerySchemaMatchesSpec asserts the §8.5 line 596 schema:
// `required: ["query"]` and `limit.default = 10`.
// spec: §8.5 line 596; F-8.5.14.
func TestMemoryQuerySchemaMatchesSpec_spec_8_5_F_8_5_14(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/memory_query")
	req := requiredSet(t, schema)
	if !req["query"] {
		t.Errorf("schema.required = %v, want query (§8.5 line 596)", req)
	}
	if req["sessionId"] {
		t.Errorf("schema.required includes sessionId; only `query` is required by §8.5 line 596")
	}
	props, _ := schema["properties"].(map[string]any)
	limit, _ := props["limit"].(map[string]any)
	if got, _ := limit["default"].(float64); got != 10 {
		t.Errorf("limit.default = %v, want 10 (§8.5 line 596)", limit["default"])
	}
}

// TestCancelChildSchemaMatchesSpec asserts the §8.5 line 531 contract:
// `lenny/cancel_child(child_id)`. The parent is implicit in the caller's
// principal; the legacy `parentSessionId` field is accepted as a
// transport fallback but MUST NOT be required.
// spec: §8.5 line 531; F-8.5.15.
func TestCancelChildSchemaMatchesSpec_spec_8_5_F_8_5_15(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/cancel_child")
	req := requiredSet(t, schema)
	if !req["childSessionId"] {
		t.Errorf("schema.required = %v, want childSessionId (§8.5 line 531)", req)
	}
	if req["parentSessionId"] {
		t.Errorf("schema.required includes parentSessionId; the §8.5 line 531 contract is `lenny/cancel_child(child_id)`")
	}
}

// TestSendMessageSchemaMatchesSpec asserts the §8.5 line 537 contract:
// `lenny/send_message(to, message)`. The schema MUST name `to` and
// `message` as required; the legacy `sessionId`/`content` names are
// gone.
// spec: §8.5 line 537; F-8.5.16.
func TestSendMessageSchemaMatchesSpec_spec_8_5_F_8_5_16(t *testing.T) {
	schema := toolListedSchema(t, schemaServer(t).Handler(), "lenny/send_message")
	req := requiredSet(t, schema)
	if !req["to"] || !req["message"] {
		t.Errorf("schema.required = %v, want both `to` and `message` (§8.5 line 537)", req)
	}
	if req["sessionId"] || req["content"] {
		t.Errorf("schema.required uses legacy field names; the §8.5 contract is `lenny/send_message(to, message)`")
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["to"]; !ok {
		t.Errorf("schema.properties is missing `to`: %v", props)
	}
	if _, ok := props["message"]; !ok {
		t.Errorf("schema.properties is missing `message`: %v", props)
	}
	if _, ok := props["sessionId"]; ok {
		t.Errorf("schema.properties still carries legacy `sessionId`; the §8.5 contract renamed it to `to`")
	}
	if _, ok := props["content"]; ok {
		t.Errorf("schema.properties still carries legacy `content`; the §8.5 contract renamed it to `message`")
	}
}

// TestMemoryWriteRejectsNonStringMetadata asserts the §8.5 line 577
// schema constraint (`metadata.additionalProperties = string`) is
// enforced at runtime. A numeric metadata value is rejected before the
// store is touched.
// spec: §8.5 line 577; F-8.5.14.
func TestMemoryWriteRejectsNonStringMetadata_spec_8_5_F_8_5_14(t *testing.T) {
	srv, store, _ := newMCPForMemory(t)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_m", TenantID: "acme", UserID: "alice", State: session.StateRunning,
	})
	resp := call(t, srv.Handler(), "lenny/memory_write",
		`{"sessionId":"sess_m","content":"x","metadata":{"k":123}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("non-string metadata should error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "metadata values must be strings") {
		t.Errorf("error text = %q, want it to mention the §8.5 line 577 string-only constraint", msg)
	}
}

// TestMemoryQueryDefaultsLimitToTen asserts the §8.5 line 596 default
// of 10 is applied when the caller omits `limit`.
// spec: §8.5 line 596; F-8.5.14.
func TestMemoryQueryDefaultsLimitToTen_spec_8_5_F_8_5_14(t *testing.T) {
	srv, store, mem := newMCPForMemory(t)
	seedMemorySession(t, store, "sess_q", "alice")
	// Seed 12 memories whose content matches the query string; the
	// default limit must keep us at 10, not 12.
	scope := memorystore.MemoryScope{TenantID: "acme", UserID: "alice", AgentType: "echo", SessionID: "sess_q"}
	for i := 0; i < 12; i++ {
		_ = mem.Write(context.Background(), scope,
			[]memorystore.Memory{{Content: "needle entry"}})
	}
	resp := call(t, srv.Handler(), "lenny/memory_query",
		`{"sessionId":"sess_q","query":"needle"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("memory_query failed: %+v", result)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	body, _ := c0["text"].(string)
	var parsed struct {
		Memories []json.RawMessage `json:"memories"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(parsed.Memories) != 10 {
		t.Errorf("default limit returned %d results, want 10 (§8.5 line 596 default)", len(parsed.Memories))
	}
}

// TestRequestInputEmitsPartsOnEventStream asserts that the F-8.5.12
// elicitation_request event payload carries the structured `parts`
// array, not the legacy flat `prompt` string.
// spec: §8.5 line 539; §7.2 line 136; F-8.5.12.
func TestRequestInputEmitsPartsOnEventStream_spec_8_5_F_8_5_12(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Events:              bus,
		InputWaits:          reg,
		RequestInputTimeout: time.Second,
		TenantID:            "acme",
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_i", TenantID: "acme", State: session.StateRunning,
	})
	h := srv.Handler()
	done := make(chan map[string]any, 1)
	go func() {
		done <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_i","requestId":"req-1","parts":[{"type":"text","text":"hi"}]}`)
	}()
	waitPending(t, reg, "sess_i", "req-1")
	hist := bus.History("sess_i", 0)
	if len(hist) != 1 || hist[0].Type != "elicitation_request" {
		t.Fatalf("history = %+v, want one elicitation_request event", hist)
	}
	if !strings.Contains(hist[0].Data, `"parts"`) {
		t.Errorf("event data = %q, want a `parts` array (F-8.5.12)", hist[0].Data)
	}
	if strings.Contains(hist[0].Data, `"prompt"`) {
		t.Errorf("event data = %q still carries legacy `prompt` field", hist[0].Data)
	}
	reg.Cancel("sess_i", "req-1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request_input did not unblock after cancel")
	}
}
