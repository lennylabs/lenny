// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer records the requests it receives and replays a scripted
// response per call so a test can drive the Streamable-HTTP client
// without a network.
type fakeDoer struct {
	responses []*http.Response
	errs      []error
	reqs      []*http.Request
	bodies    []string
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.bodies = append(f.bodies, string(b))
	} else {
		f.bodies = append(f.bodies, "")
	}
	i := len(f.reqs) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.responses[i], nil
}

func jsonResp(status int, body string, headers map[string]string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func sseResp(status int, body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

// TestInitializeNegotiatesAndCarriesBearer_spec_9_3_142 verifies the
// outbound initialize handshake records the negotiated version and the
// server's Mcp-Session-Id, sends the initialized notification, and
// carries the bearer credential the OAuth flow stored.
func TestInitializeNegotiatesAndCarriesBearer_spec_9_3_142(t *testing.T) {
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"github","version":"9"}}}`,
				map[string]string{"Mcp-Session-Id": "sess-xyz"}),
			jsonResp(202, ``, nil), // notifications/initialized
		},
	}
	c := New(doer)
	sess, res, err := c.Initialize(context.Background(), "https://mcp.example.com", "tok-abc")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion != "2025-03-26" {
		t.Errorf("negotiated version = %q, want 2025-03-26", res.ProtocolVersion)
	}
	if sess.NegotiatedVersion() != "2025-03-26" {
		t.Errorf("session version = %q", sess.NegotiatedVersion())
	}
	if got := doer.reqs[0].Header.Get("Authorization"); got != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want Bearer tok-abc", got)
	}
	if got := doer.reqs[0].Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept = %q, want it to include text/event-stream", got)
	}
	// The initialized notification reuses the assigned session id.
	if got := doer.reqs[1].Header.Get("Mcp-Session-Id"); got != "sess-xyz" {
		t.Errorf("notification Mcp-Session-Id = %q, want sess-xyz", got)
	}
	if !strings.Contains(doer.bodies[1], "notifications/initialized") {
		t.Errorf("second request was not the initialized notification: %s", doer.bodies[1])
	}
}

// TestListToolsParsesAnnotations_spec_5_1 confirms tools/list decodes the
// MCP annotation block the §5.1 capability inference consults.
func TestListToolsParsesAnnotations_spec_5_1(t *testing.T) {
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read_file","annotations":{"readOnlyHint":true}},{"name":"delete_repo","annotations":{"destructiveHint":true}}]}}`, nil),
		},
	}
	c := New(doer)
	sess, _, err := c.Initialize(context.Background(), "https://mcp.example.com", "")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Annotations == nil || tools[0].Annotations.ReadOnlyHint == nil || !*tools[0].Annotations.ReadOnlyHint {
		t.Errorf("read_file readOnlyHint not parsed: %+v", tools[0].Annotations)
	}
	if tools[1].Annotations == nil || tools[1].Annotations.DestructiveHint == nil || !*tools[1].Annotations.DestructiveHint {
		t.Errorf("delete_repo destructiveHint not parsed: %+v", tools[1].Annotations)
	}
	// A public connector dial carries no Authorization header.
	if got := doer.reqs[0].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty for public connector", got)
	}
}

// TestCallToolOverSSE_spec_9_3_142 verifies the client recovers a
// JSON-RPC result delivered as a Streamable-HTTP SSE event.
func TestCallToolOverSSE_spec_9_3_142(t *testing.T) {
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			sseResp(200, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n\n"),
		},
	}
	c := New(doer)
	sess, _, err := c.Initialize(context.Background(), "https://mcp.example.com", "")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	raw, err := sess.CallTool(context.Background(), "do_thing", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(got.Content) != 1 || got.Content[0]["text"] != "ok" {
		t.Errorf("unexpected tools/call result: %s", raw)
	}
}

// TestCallToolPropagatesRPCError_spec_9_3_142 verifies a JSON-RPC error
// object surfaces as a Go error rather than a silent empty result.
func TestCallToolPropagatesRPCError_spec_9_3_142(t *testing.T) {
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"unknown tool"}}`, nil),
		},
	}
	c := New(doer)
	sess, _, err := c.Initialize(context.Background(), "https://mcp.example.com", "")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := sess.CallTool(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected rpc error, got nil")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %v, want it to mention the rpc message", err)
	}
}

// TestInitializeRejectsNon2xx_spec_9_3_142 confirms an HTTP-level failure
// (for example a 401 from a connector requiring auth) becomes an error.
func TestInitializeRejectsNon2xx_spec_9_3_142(t *testing.T) {
	doer := &fakeDoer{responses: []*http.Response{jsonResp(401, `unauthorized`, nil)}}
	c := New(doer)
	if _, _, err := c.Initialize(context.Background(), "https://mcp.example.com", ""); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}
