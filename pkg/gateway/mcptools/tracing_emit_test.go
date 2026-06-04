// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span a handler emitted,
// then restores the prior provider when the test ends. The §16.3 emit
// sites resolve their tracer through tracing.NewTracer(nil), which reads
// the process-global provider this recorder installs. spec: §16.3.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func spanByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// callCtx is call with a caller-supplied context so a test can cancel a
// blocking tool mid-flight (the MCP handler threads r.Context() into the
// tool handler).
func callCtx(t *testing.T, h http.Handler, ctx context.Context, tool, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body))).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return resp
}

// TestAwaitChildrenEmitsAwaitChildSpan_spec_16_3 proves the gateway-side
// await/collect path opens one delegation.await_child span per await call.
// Both children are already terminal, so the handler settles on the first
// poll and the span ends cleanly. The previous gap left
// SpanDelegationAwaitChild a catalog-only constant with no emit site.
func TestAwaitChildrenEmitsAwaitChildSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c1", session.StateCompleted, "sess_p")
	mkSession(t, store, "sess_c2", session.StateCompleted, "sess_p")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c1","sess_c2"],"mode":"all"}`)
	_ = resultText(t, resp) // fail the test if the tool returned an error

	span := spanByName(rec.Ended(), "delegation.await_child")
	if span == nil {
		t.Fatalf("delegation.await_child span not emitted; got %d spans", len(rec.Ended()))
	}
	var gotCount int64 = -1
	for _, a := range span.Attributes() {
		if string(a.Key) == "delegation.await.child_count" {
			gotCount = a.Value.AsInt64()
		}
	}
	if gotCount != 2 {
		t.Errorf("delegation.await.child_count = %d, want 2", gotCount)
	}
}

// TestAwaitChildrenSpanRecordsErrorOnCancel_spec_16_3 covers the span's
// error-recording branch. A still-running child never settles, so the poll
// loop blocks until the request context is cancelled; the ctx.Done branch
// records the context error on the span. The span opens at the poll-loop
// entry, after the authorization pre-check passes.
func TestAwaitChildrenSpanRecordsErrorOnCancel_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_child", session.StateRunning, "sess_p")

	h := srv.Handler()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan map[string]any, 1)
	go func() {
		// A still-running child never settles, so the poll loop blocks until
		// the request context is cancelled, driving the span's ctx.Done
		// error-recording branch.
		done <- callCtx(t, h, ctx, "lenny/await_children",
			`{"sessionId":"sess_p","childIds":["sess_child"],"mode":"all"}`)
	}()
	// Give the handler time to open the span and enter the poll loop.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("await_children did not return after the context was cancelled")
	}

	span := spanByName(rec.Ended(), "delegation.await_child")
	if span == nil {
		t.Fatalf("delegation.await_child span not emitted on the cancel path; got %d spans", len(rec.Ended()))
	}
	if got := span.Status().Code.String(); got != "Error" {
		t.Errorf("span status = %q, want Error after context cancellation", got)
	}
}

// TestRequestElicitationEmitsElicitationSpan_spec_16_3 proves the
// request_elicitation handler opens the gateway hop's mcp.elicitation span
// around the chain dispatch. The root resolves the elicitation so the
// blocking handler returns; the span (which wraps only the dispatch, not
// the human-response wait) is emitted with the initiator attribute.
func TestRequestElicitationEmitsElicitationSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve?","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", "yes")
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the root resolved it")
	}

	span := spanByName(rec.Ended(), "mcp.elicitation")
	if span == nil {
		t.Fatalf("mcp.elicitation span not emitted; got %d spans", len(rec.Ended()))
	}
	var sawInitiator bool
	for _, a := range span.Attributes() {
		if string(a.Key) == "mcp.elicitation.initiator" &&
			a.Value.AsString() == string(elicitation.InitiatorAgent) {
			sawInitiator = true
		}
	}
	if !sawInitiator {
		t.Errorf("span missing mcp.elicitation.initiator=agent attribute; attrs=%v", span.Attributes())
	}
}

// TestRequestElicitationSpanRecordsErrorOnURLModeRejection_spec_16_3 covers
// the dispatch error path: an agent-initiated url-mode elicitation against a
// non-allowlisted domain is dropped inside dispatch, so the span records the
// error status. The zero-value allowlist blocks every agent url-mode
// elicitation (the §9.2 default).
func TestRequestElicitationSpanRecordsErrorOnURLModeRejection_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, store, _ := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"login","schema":{},"url":"https://evil.example/oauth","elicitationId":"elic_y"}`)
	if resp["result"] == nil {
		t.Fatalf("expected a tool result, got %+v", resp)
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected an isError result for the disallowed url-mode domain, got %+v", result)
	}

	span := spanByName(rec.Ended(), "mcp.elicitation")
	if span == nil {
		t.Fatalf("mcp.elicitation span not emitted on the url-mode rejection path; got %d spans", len(rec.Ended()))
	}
	if got := span.Status().Code.String(); got != "Error" {
		t.Errorf("span status = %q, want Error on a url-mode rejection", got)
	}
}
