// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// fakeMCPServer is a minimal MCP server over a byte stream, used to
// drive the client without spawning a subprocess. It reads newline-
// delimited JSON-RPC requests and replies via the handler.
type fakeMCPServer struct {
	// handler returns the result (or an error object) for one request.
	// A nil result with respond=false models a notification.
	handler func(method string, id json.RawMessage, params json.RawMessage) (result any, errObj *fakeRPCError, respond bool)
	// requests records every method the server received, in order.
	requests []string
}

type fakeRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serve runs the fake server until r reaches EOF.
func (f *fakeMCPServer) serve(r io.Reader, w io.Writer) {
	dec := json.NewDecoder(bufio.NewReader(r))
	enc := json.NewEncoder(w)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		f.requests = append(f.requests, req.Method)
		result, errObj, respond := f.handler(req.Method, req.ID, req.Params)
		if !respond {
			continue
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if errObj != nil {
			resp["error"] = errObj
		} else {
			resp["result"] = result
		}
		_ = enc.Encode(resp)
	}
}

// clientWithServer wires an mcp.Client to a fakeMCPServer over a pair
// of in-memory pipes and returns both.
func clientWithServer(t *testing.T, srv *fakeMCPServer) *mcp.Client {
	t.Helper()
	// clientReads <- serverWrites ; serverReads <- clientWrites
	serverReads, clientWrites := io.Pipe()
	clientReads, serverWrites := io.Pipe()
	go srv.serve(serverReads, serverWrites)
	t.Cleanup(func() {
		_ = clientWrites.Close()
		_ = serverWrites.Close()
	})
	return mcp.NewClient(clientReads, clientWrites)
}

// defaultHandler answers initialize, the initialized notification,
// tools/list, and tools/call with canned results.
func defaultHandler(method string, _ json.RawMessage, params json.RawMessage) (any, *fakeRPCError, bool) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake-server", "version": "9"},
		}, nil, true
	case "notifications/initialized":
		return nil, nil, false // a notification: no reply
	case "tools/list":
		return map[string]any{"tools": []map[string]any{
			{"name": "echo", "description": "echo a string"},
		}}, nil, true
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(params, &call)
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "called " + call.Name}},
		}, nil, true
	default:
		return nil, &fakeRPCError{Code: -32601, Message: "unknown method"}, true
	}
}

func TestClientInitialize(t *testing.T) {
	srv := &fakeMCPServer{handler: defaultHandler}
	c := clientWithServer(t, srv)

	res, err := c.Initialize()
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, mcp.ProtocolVersion)
	}
	if res.ServerInfo.Name != "fake-server" {
		t.Errorf("serverInfo.name = %q, want fake-server", res.ServerInfo.Name)
	}
}

func TestClientInitializeSendsInitializedNotification(t *testing.T) {
	srv := &fakeMCPServer{handler: defaultHandler}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// The MCP spec requires the client to send notifications/initialized
	// after a successful initialize handshake. A follow-up request acts
	// as a barrier: the server processes requests serially, so once the
	// ListTools response arrives the notification has been consumed.
	if _, err := c.ListTools(); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(srv.requests) != 3 ||
		srv.requests[0] != "initialize" ||
		srv.requests[1] != "notifications/initialized" ||
		srv.requests[2] != "tools/list" {
		t.Errorf("server saw requests %v, want [initialize notifications/initialized tools/list]", srv.requests)
	}
}

func TestClientListTools(t *testing.T) {
	srv := &fakeMCPServer{handler: defaultHandler}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tools, err := c.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("ListTools = %+v, want [echo]", tools)
	}
}

func TestClientCallTool(t *testing.T) {
	srv := &fakeMCPServer{handler: defaultHandler}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	result, err := c.CallTool("echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(string(result), "called echo") {
		t.Errorf("CallTool result = %s, want it to contain \"called echo\"", result)
	}
}

func TestClientCallToolSurfacesRPCError(t *testing.T) {
	srv := &fakeMCPServer{handler: func(method string, id json.RawMessage, params json.RawMessage) (any, *fakeRPCError, bool) {
		if method == "tools/call" {
			return nil, &fakeRPCError{Code: -32602, Message: "tool blew up"}, true
		}
		return defaultHandler(method, id, params)
	}}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err := c.CallTool("echo", nil)
	if err == nil || !strings.Contains(err.Error(), "tool blew up") {
		t.Errorf("CallTool error = %v, want it to surface the JSON-RPC error", err)
	}
}

func TestClientCallToolDefaultsEmptyArguments(t *testing.T) {
	var gotArgs string
	srv := &fakeMCPServer{handler: func(method string, id json.RawMessage, params json.RawMessage) (any, *fakeRPCError, bool) {
		if method == "tools/call" {
			var call struct {
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(params, &call)
			gotArgs = string(call.Arguments)
			return map[string]any{"content": []any{}}, nil, true
		}
		return defaultHandler(method, id, params)
	}}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := c.CallTool("echo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// A nil arguments value must be sent as an empty object, not null.
	if gotArgs != "{}" {
		t.Errorf("server received arguments %q, want {}", gotArgs)
	}
}

func TestClientCallAfterCloseFailsFast(t *testing.T) {
	srv := &fakeMCPServer{handler: defaultHandler}
	c := clientWithServer(t, srv)
	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	c.Close()
	if _, err := c.ListTools(); err != mcp.ErrClientClosed {
		t.Errorf("ListTools after Close = %v, want ErrClientClosed", err)
	}
}

func TestClientReportsServerHangup(t *testing.T) {
	// A server that closes the stream without answering: the client must
	// not block forever.
	serverReads, clientWrites := io.Pipe()
	clientReads, serverWrites := io.Pipe()
	go func() {
		buf := make([]byte, 256)
		_, _ = serverReads.Read(buf) // consume the initialize request
		_ = serverWrites.Close()     // hang up without replying
	}()
	t.Cleanup(func() { _ = clientWrites.Close() })
	c := mcp.NewClient(clientReads, clientWrites)
	if _, err := c.Initialize(); err == nil {
		t.Error("Initialize against a hung-up server returned nil, want an error")
	}
}
