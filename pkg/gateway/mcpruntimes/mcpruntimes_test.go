// SPDX-License-Identifier: MIT

package mcpruntimes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// stubDispatcher records each Dispatch invocation and returns the
// canned response.
type stubDispatcher struct {
	calls    []string
	response []byte
	err      error
}

func (s *stubDispatcher) Dispatch(_ context.Context, name string, _ []byte) ([]byte, error) {
	s.calls = append(s.calls, name)
	return s.response, s.err
}

func mustStore(t *testing.T, runtimes ...runtimestore.Runtime) runtimestore.Store {
	t.Helper()
	store := runtimestore.NewMemory()
	for _, rt := range runtimes {
		if err := store.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %q: %v", rt.Name, err)
		}
	}
	return store
}

// spec: §4.1 — POST /mcp/runtimes/{name} for a known type:mcp runtime
// routes the JSON-RPC body to the dispatcher and returns its response.
func TestHandlerDispatchesMCPRuntime(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{
		Name: "echo-mcp",
		Type: runtimestore.TypeMCP,
	})
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)}
	h := mcpruntimes.New(store, disp)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp", strings.NewReader(body))
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("body = %q, want dispatcher response", rr.Body.String())
	}
	if len(disp.calls) != 1 || disp.calls[0] != "echo-mcp" {
		t.Fatalf("dispatcher calls = %v, want [echo-mcp]", disp.calls)
	}
}

// spec: §4.1 — an unknown runtime returns HTTP 404 with the
// RESOURCE_NOT_FOUND lenny error code so callers can't enumerate
// runtime names.
func TestHandlerUnknownRuntimeReturns404(t *testing.T) {
	store := mustStore(t)
	h := mcpruntimes.New(store, &stubDispatcher{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/does-not-exist",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "does-not-exist")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var resp struct {
		Error struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
	}
	if resp.Error.Data.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("error.data.code = %q, want RESOURCE_NOT_FOUND", resp.Error.Data.Code)
	}
}

// spec: §4.1 — a type:agent runtime is not an MCP server; the
// endpoint returns HTTP 400 INVALID_RUNTIME_TYPE.
func TestHandlerNonMCPRuntimeReturns400(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
	})
	h := mcpruntimes.New(store, &stubDispatcher{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/claude-code",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "claude-code")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	var resp struct {
		Error struct {
			Data struct {
				Code     string `json:"code"`
				Category string `json:"category"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Data.Code != "INVALID_RUNTIME_TYPE" {
		t.Fatalf("error.data.code = %q, want INVALID_RUNTIME_TYPE", resp.Error.Data.Code)
	}
	if resp.Error.Data.Category != "PERMANENT" {
		t.Fatalf("error.data.category = %q, want PERMANENT", resp.Error.Data.Category)
	}
}

// spec: §4.1 — when no dispatcher is wired, every request that
// passes the runtime-type check returns RUNTIME_UNAVAILABLE.
func TestHandlerNoDispatcherReturnsRuntimeUnavailable(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP})
	h := mcpruntimes.New(store, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"RUNTIME_UNAVAILABLE"`) {
		t.Fatalf("body = %q, want RUNTIME_UNAVAILABLE", rr.Body.String())
	}
}

// spec: §4.1 — a Dispatcher returning ErrNoActiveClient is surfaced
// as 503 RUNTIME_UNAVAILABLE so a client can retry against another
// replica.
func TestHandlerDispatcherNoActiveClient(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP})
	disp := &stubDispatcher{err: mcpruntimes.ErrNoActiveClient}
	h := mcpruntimes.New(store, disp)

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §4.1 — a dispatcher error other than ErrNoActiveClient
// becomes HTTP 502 INTERNAL_ERROR.
func TestHandlerDispatcherFailureReturns502(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP})
	disp := &stubDispatcher{err: errors.New("mcp connection reset")}
	h := mcpruntimes.New(store, disp)

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}

// spec: §4.1 — non-POST methods return 405 with an Allow header.
func TestHandlerRejectsNonPOST(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP})
	h := mcpruntimes.New(store, &stubDispatcher{})

	req := httptest.NewRequest(http.MethodGet, "/mcp/runtimes/echo-mcp", nil)
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow header = %q, want POST", got)
	}
}

// spec: §4.1 — a malformed JSON-RPC body returns a 400 VALIDATION_ERROR.
func TestHandlerInvalidJSONRPC(t *testing.T) {
	store := mustStore(t, runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP})
	h := mcpruntimes.New(store, &stubDispatcher{})

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"malformed json", "{this is not json"},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":1,"method":"x"}`},
		{"missing method", `{"jsonrpc":"2.0","id":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp", strings.NewReader(c.body))
			req.SetPathValue("name", "echo-mcp")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %q; body=%s", rr.Code, c.name, rr.Body.String())
			}
		})
	}
}

// spec: §4.1 — a nil runtime store yields RESOURCE_NOT_FOUND so the
// endpoint does not panic on a misconfigured gateway.
func TestHandlerNilStoreReturns404(t *testing.T) {
	h := mcpruntimes.New(nil, &stubDispatcher{})
	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	req.SetPathValue("name", "echo")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// spec: §4.1 — a soft-deleted runtime is reported as not found so
// the endpoint matches the §5.1 "DeletedAt" semantics.
func TestHandlerSoftDeletedRuntimeReturns404(t *testing.T) {
	store := runtimestore.NewMemory()
	if err := store.Create(context.Background(), runtimestore.Runtime{Name: "echo-mcp", Type: runtimestore.TypeMCP}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SoftDelete(context.Background(), "echo-mcp", time.Now()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	h := mcpruntimes.New(store, &stubDispatcher{})

	req := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/echo-mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.SetPathValue("name", "echo-mcp")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for soft-deleted runtime", rr.Code)
	}
}
