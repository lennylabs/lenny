// SPDX-License-Identifier: MIT

// Package externalmcp implements a minimal Streamable-HTTP MCP server
// stub that stands in for a registered §9.3 connector's external
// endpoint. It answers the `initialize` handshake, the
// `notifications/initialized` notification, and `tools/call` with a
// canned result — standing in for a tool result an external,
// untrusted connector controls — and records every request it
// receives.
//
// Use this when a tier-4 integration test needs to drive the
// gateway's real outbound connector-invocation path
// (pkg/gateway/connectors/connectorinvoke) without a live third-party
// endpoint, and needs to assert whether the gateway ever dialed out at
// all — for example, to confirm a policy-denied connector call never
// reaches the external server.
//
// Usage:
//
//	stub := externalmcp.Start(t, "attacker-controlled output")
//	conn := connectorstore.Connector{MCPServerURL: stub.URL(), ...}
//	invoker := connectorinvoke.NewInvoker(connectors, creds,
//	    connectorinvoke.New(stub.Client()), nil, authz)
//	// ... drive the call, then:
//	if stub.RequestCount() != 0 { t.Error("dialed out despite denial") }
package externalmcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Stub is a running external-connector MCP endpoint bound to a random
// loopback port over TLS (the §9.3 connector registry requires an
// https:// MCPServerURL).
type Stub struct {
	toolResultText string
	server         *httptest.Server
	requests       int64
}

// Start launches the stub and registers a t.Cleanup that closes it.
// toolResultText is the text content every tools/call result carries.
func Start(t testing.TB, toolResultText string) *Stub {
	t.Helper()
	s := &Stub{toolResultText: toolResultText}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

// URL is the stub's https:// endpoint, suitable for
// connectorstore.Connector.MCPServerURL.
func (s *Stub) URL() string { return s.server.URL }

// Client returns an *http.Client that trusts the stub's self-signed
// leaf certificate, suitable for connectorinvoke.New.
func (s *Stub) Client() *http.Client { return s.server.Client() }

// RequestCount returns the number of HTTP requests the stub has
// received so far, across every JSON-RPC method (initialize, the
// initialized notification, and tools/call). A test asserting that a
// policy-denied connector call never dials out checks this stays at
// its pre-call value.
func (s *Stub) RequestCount() int64 { return atomic.LoadInt64(&s.requests) }

// jsonrpcRequest is the subset of an inbound JSON-RPC 2.0 request the
// stub needs to dispatch a response.
type jsonrpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&s.requests, 1)
	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "externalmcp stub: decode request: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch req.Method {
	case "initialize":
		s.writeResult(w, req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]string{"name": "externalmcp-stub", "version": "1"},
		})
	case "notifications/initialized":
		// A notification carries no id and expects no response body.
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		s.writeResult(w, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": s.toolResultText}},
		})
	default:
		http.Error(w, "externalmcp stub: unsupported method "+req.Method, http.StatusNotFound)
	}
}

func (s *Stub) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
