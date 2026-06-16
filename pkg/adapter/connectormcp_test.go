// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeConnectorForwarder records the session/connector/tool it is
// forwarded with and returns a canned ref list, catalog, and result.
// spec: §9.3 lines 142-164. F-9.1.2.
type fakeConnectorForwarder struct {
	refs       []mcp.ConnectorRef
	list       []mcp.Tool
	result     json.RawMessage
	gotSession string
	gotConn    string
	gotTool    string
}

func (f *fakeConnectorForwarder) ListSessionConnectors(_ context.Context, sessionID string) ([]mcp.ConnectorRef, error) {
	f.gotSession = sessionID
	return f.refs, nil
}

func (f *fakeConnectorForwarder) ListConnectorTools(_ context.Context, sessionID, connectorID string) ([]mcp.Tool, error) {
	f.gotSession = sessionID
	f.gotConn = connectorID
	return f.list, nil
}

func (f *fakeConnectorForwarder) CallConnectorTool(_ context.Context, sessionID, connectorID, toolName string, _ json.RawMessage) (json.RawMessage, error) {
	f.gotSession = sessionID
	f.gotConn = connectorID
	f.gotTool = toolName
	return f.result, nil
}

// spec: §9.3 line 142 + lines 142-164 — when a ConnectorForwarder is
// wired, StartSession lists each policy-permitted connector in the
// manifest connectorServers array and opens a per-connector MCP server
// whose tools/list and tools/call forward to the gateway scoped to the
// session and the connector. F-9.1.2.
func TestConnectorMCPForwardsToGateway_spec_9_3_142(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.ManifestDir = t.TempDir()
	// The derived per-connector sun_path must fit darwin's ~104-byte limit.
	s.MCPSocket = shortSocketName(t, "m")
	fwd := &fakeConnectorForwarder{
		refs:   []mcp.ConnectorRef{{ID: "github", DisplayName: "GitHub"}},
		list:   []mcp.Tool{{Name: "list_repos", Description: "list"}},
		result: json.RawMessage(`{"content":[{"type":"text","text":"forwarded"}]}`),
	}
	s.ConnectorForwarder = fwd

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
		})
	})

	b, err := os.ReadFile(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(m.ConnectorServers) != 1 || m.ConnectorServers[0].ID != "github" {
		t.Fatalf("manifest connectorServers = %+v, want one github entry", m.ConnectorServers)
	}
	sock := m.ConnectorServers[0].Socket
	if sock == "" {
		t.Fatal("connector entry carries no socket")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial connector MCP socket %q: %v", sock, err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	mustSend := func(id int, method string, params any) {
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatalf("send %s: %v", method, err)
		}
	}
	mustRead := func() map[string]json.RawMessage {
		var resp map[string]json.RawMessage
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("read response: %v", err)
		}
		return resp
	}

	mustSend(1, "initialize", map[string]any{"_lennyNonce": m.MCPNonce, "protocolVersion": "2025-03-26"})
	mustRead()

	// tools/list returns the connector catalog fetched via the forwarder.
	mustSend(2, "tools/list", nil)
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(mustRead()["result"], &list); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "list_repos" {
		t.Errorf("tools/list = %+v, want the connector catalog", list.Tools)
	}
	if fwd.gotConn != "github" {
		t.Errorf("forwarder list connector = %q, want github", fwd.gotConn)
	}

	// tools/call forwards to the gateway scoped to this session + connector.
	mustSend(3, "tools/call", map[string]any{"name": "list_repos", "arguments": map[string]any{}})
	callResp := mustRead()
	if _, isErr := callResp["error"]; isErr {
		t.Fatalf("tools/call errored: %s", callResp["error"])
	}
	if fwd.gotSession != "sess-1" || fwd.gotConn != "github" || fwd.gotTool != "list_repos" {
		t.Errorf("forwarder call = (%q, %q, %q), want (sess-1, github, list_repos)", fwd.gotSession, fwd.gotConn, fwd.gotTool)
	}
	if string(callResp["result"]) != `{"content":[{"type":"text","text":"forwarded"}]}` {
		t.Errorf("tools/call result = %s, want the gateway result verbatim", callResp["result"])
	}
}

// spec: §4.7 connectorServers is never absent — with no forwarder wired,
// the manifest still carries an empty array. F-9.1.2.
func TestConnectorServersEmptyWithoutForwarder_spec_9_3_142(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortSocketName(t, "m")

	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-1"},
		})
	})
	b, err := os.ReadFile(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	// The raw JSON carries connectorServers as [] rather than null.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if got := string(raw["connectorServers"]); got != "[]" {
		t.Errorf("connectorServers = %s, want [] (never absent)", got)
	}
}
