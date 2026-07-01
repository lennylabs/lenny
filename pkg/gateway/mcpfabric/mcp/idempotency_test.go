// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
)

// rpcWithTenant POSTs an MCP JSON-RPC request and sets the
// X-Lenny-Tenant-ID header the idempotency hook expects to resolve.
func rpcWithTenant(t *testing.T, h http.Handler, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §11.5 line 277 — `idempotencyKey` in MCP tool input collapses
// retries of CreateSession / SpawnChild to one execution. The hook
// replays the cached ToolResult on duplicate; the inner handler runs at
// most once. Closes F-11.5.1, F-11.5.6.
func TestMCPIdempotency_ReplaysCachedToolResult_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	var calls int32
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, args json.RawMessage) (mcp.ToolResult, error) {
			atomic.AddInt32(&calls, 1)
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: `{"sessionId":"sess-1","state":"running"}`}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"abc-123"}}}`
	r1 := rpcWithTenant(t, s.Handler(), "acme", body)
	r2 := rpcWithTenant(t, s.Handler(), "acme", body)

	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("handler called %d times, want 1 (idempotent retry must replay)", calls)
	}
	if !bytes.Contains(r1.Body.Bytes(), []byte("sess-1")) {
		t.Errorf("first response missing payload: %q", r1.Body.String())
	}
	if !bytes.Contains(r2.Body.Bytes(), []byte("sess-1")) {
		t.Errorf("replay missing payload: %q", r2.Body.String())
	}
	if r1.Body.String() != r2.Body.String() {
		t.Errorf("replay must be byte-equal:\nfirst: %q\nrepla: %q", r1.Body.String(), r2.Body.String())
	}
}

// spec: §11.5 line 277 — a tool error releases the pending row so a
// retry re-executes against a (hopefully) healthy backend.
// Closes F-11.5.1, F-11.5.2 (release on MCP error).
func TestMCPIdempotency_ToolErrorReleasesPending_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	var calls int32
	s.RegisterTool(mcp.Tool{Name: "lenny/delegate_task", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return mcp.ToolResult{}, errors.New("transient backend failure")
			}
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "ok"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/delegate_task": true},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":{"parentSessionId":"p","runtimeRef":"echo","idempotencyKey":"k1"}}}`
	r1 := rpcWithTenant(t, s.Handler(), "acme", body)
	if !bytes.Contains(r1.Body.Bytes(), []byte("transient backend failure")) {
		t.Errorf("first response must surface tool error, got %q", r1.Body.String())
	}
	r2 := rpcWithTenant(t, s.Handler(), "acme", body)
	if !bytes.Contains(r2.Body.Bytes(), []byte(`"text":"ok"`)) {
		t.Errorf("retry must re-execute (pending row released), got %q", r2.Body.String())
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("handler called %d times, want 2 (failed + succeed)", calls)
	}
}

// spec: §11.5 line 277 — a concurrent retry while the original is
// in-flight is rejected with IDEMPOTENCY_KEY_IN_FLIGHT (POLICY,
// retryable=true). Closes F-11.5.1, F-11.5.2.
func TestMCPIdempotency_ConcurrentRetryInFlight_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	gate := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			atomic.AddInt32(&calls, 1)
			gate <- struct{}{}
			<-release
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "done"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"race-1"}}}`
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- rpcWithTenant(t, s.Handler(), "acme", body) }()
	<-gate

	r2 := rpcWithTenant(t, s.Handler(), "acme", body)
	if !bytes.Contains(r2.Body.Bytes(), []byte("IDEMPOTENCY_KEY_IN_FLIGHT")) {
		t.Errorf("concurrent retry must surface IDEMPOTENCY_KEY_IN_FLIGHT, got %q", r2.Body.String())
	}

	close(release)
	r1 := <-done
	if !bytes.Contains(r1.Body.Bytes(), []byte("done")) {
		t.Errorf("original response: %q", r1.Body.String())
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("handler called %d times, want 1", calls)
	}
}

// spec: §11.5 line 277 — same key with a different arguments body
// returns IDEMPOTENCY_KEY_REUSED on the MCP path, mirroring the REST
// 422. Closes F-11.5.1.
func TestMCPIdempotency_DifferentBodyRejected_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "first"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})

	body1 := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"dup-1"}}}`
	body2 := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"other","idempotencyKey":"dup-1"}}}`
	_ = rpcWithTenant(t, s.Handler(), "acme", body1)
	r2 := rpcWithTenant(t, s.Handler(), "acme", body2)
	if !bytes.Contains(r2.Body.Bytes(), []byte("IDEMPOTENCY_KEY_REUSED")) {
		t.Errorf("mismatched body must surface IDEMPOTENCY_KEY_REUSED, got %q", r2.Body.String())
	}
}

// spec: §11.5 line 277 — calls to tools not in the allow-list ignore
// idempotencyKey (opt-in per tool). Closes F-11.5.1.
func TestMCPIdempotency_NonAllowlistedToolIgnoresKey_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	var calls int32
	s.RegisterTool(mcp.Tool{Name: "lenny/send_message", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			atomic.AddInt32(&calls, 1)
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "ok"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true}, // send_message NOT in list
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/send_message","arguments":{"sessionId":"s","content":"hi","idempotencyKey":"k1"}}}`
	_ = rpcWithTenant(t, s.Handler(), "acme", body)
	_ = rpcWithTenant(t, s.Handler(), "acme", body)
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("non-allowlisted tool must not cache: calls=%d, want 2", calls)
	}
}

// spec: §11.5 line 277 — a missing tenant fails closed with
// errInvalidRequest, matching the REST middleware's tenant_required
// posture. Closes F-11.5.1.
func TestMCPIdempotency_FailsClosedWhenTenantMissing_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			t.Fatalf("handler must not be invoked when tenant is missing")
			return mcp.ToolResult{}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(_ *http.Request) string { return "" },
		Tools:             map[string]bool{"lenny/create_session": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"k1"}}}`
	r := rpcWithTenant(t, s.Handler(), "", body)
	if !bytes.Contains(r.Body.Bytes(), []byte("tenant could not be resolved")) {
		t.Errorf("expected tenant-missing error, got %q", r.Body.String())
	}
}

// spec: §11.5 line 277 — with no idempotencyKey field the path falls
// through to the normal handler; no caching. Regression guard for the
// opt-in surface. Closes F-11.5.1.
func TestMCPIdempotency_NoKeyFallsThrough_spec_11_5(t *testing.T) {
	s := mcp.NewServer()
	var calls int32
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			atomic.AddInt32(&calls, 1)
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "ok"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idemmw.NewMemoryStore(),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo"}}}`
	_ = rpcWithTenant(t, s.Handler(), "acme", body)
	_ = rpcWithTenant(t, s.Handler(), "acme", body)
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("absent idempotencyKey must not cache: calls=%d, want 2", calls)
	}
}

// spec: §11.5 line 277 — the per-tool key namespacing prevents REST
// and MCP cross-transport collisions. A REST "abc-123" and an MCP
// "abc-123" map to distinct rows. The MCP key is "mcp/<tool>/<caller>"
// per pkg/gateway/mcp/idempotency.go's mcpKey helper. Regression
// guard. Closes F-11.5.1.
func TestMCPIdempotency_KeyNamespacing_spec_11_5(t *testing.T) {
	store := idemmw.NewMemoryStore()
	s := mcp.NewServer()
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "mcp-result"}}}, nil
		})
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             store,
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"shared"}}}`
	_ = rpcWithTenant(t, s.Handler(), "acme", body)

	// The row for the REST surface ("shared") must NOT exist; only the
	// MCP-namespaced row ("mcp/lenny/create_session/shared") should.
	if _, found, err := store.Get(context.Background(), "acme", "shared"); err != nil || found {
		t.Errorf("rest-keyed row should not exist: found=%v err=%v", found, err)
	}
	if _, found, err := store.Get(context.Background(), "acme", "mcp/lenny/create_session/shared"); err != nil || !found {
		t.Errorf("mcp-namespaced row should exist: found=%v err=%v", found, err)
	}
}
