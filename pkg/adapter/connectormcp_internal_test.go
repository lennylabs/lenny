// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// spec: §9.3 line 142 — the per-connector socket derives from the
// platform MCP socket: abstract base yields an abstract socket, a
// filesystem base yields a sibling .sock, and a path-unsafe id is
// sanitised. F-9.1.2.
func TestConnectorSocketName_spec_9_3_142(t *testing.T) {
	cases := []struct {
		base, id, want string
	}{
		{"@lenny-platform-mcp", "github", "@lenny-connector-github"},
		{"/run/lenny/platform.sock", "github", "/run/lenny/lenny-connector-github.sock"},
		{"@lenny-platform-mcp", "weird/slash", "@lenny-connector-weird-slash"},
		{"@lenny-platform-mcp", "a b\x00c", "@lenny-connector-a-b-c"},
	}
	for _, c := range cases {
		if got := connectorSocketName(c.base, c.id); got != c.want {
			t.Errorf("connectorSocketName(%q, %q) = %q, want %q", c.base, c.id, got, c.want)
		}
	}
}

// fakeConnForwarder is an internal ConnectorToolForwarder double.
type fakeConnForwarder struct {
	refs   []mcp.ConnectorRef
	refErr error
}

func (f *fakeConnForwarder) ListSessionConnectors(_ context.Context, _ string) ([]mcp.ConnectorRef, error) {
	return f.refs, f.refErr
}

func (f *fakeConnForwarder) ListConnectorTools(_ context.Context, _, _ string) ([]mcp.Tool, error) {
	return nil, nil
}

func (f *fakeConnForwarder) CallConnectorTool(_ context.Context, _, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

// spec: §9.3 line 142 — sessionConnectors resolves the policy-permitted
// connectors and is a best-effort no-op when the prerequisites are
// absent. F-9.1.2.
func TestSessionConnectors_spec_9_3_142(t *testing.T) {
	fwd := &fakeConnForwarder{refs: []mcp.ConnectorRef{{ID: "github"}, {ID: "slack"}, {ID: ""}}}

	// Happy path: one sessionConnector per non-empty ref, with sockets
	// derived from the platform socket.
	s := &Server{ConnectorForwarder: fwd, MCPSocket: "@lenny-platform-mcp"}
	got := s.sessionConnectors(context.Background(), "sess-1")
	if len(got) != 2 || got[0].ID != "github" || got[0].Socket != "@lenny-connector-github" {
		t.Fatalf("sessionConnectors = %+v, want github+slack with derived sockets", got)
	}

	// No forwarder, no intra-pod socket, and a type:mcp runtime each yield
	// no connectors.
	if c := (&Server{MCPSocket: "@x"}).sessionConnectors(context.Background(), "s"); c != nil {
		t.Errorf("no forwarder = %+v, want nil", c)
	}
	if c := (&Server{ConnectorForwarder: fwd}).sessionConnectors(context.Background(), "s"); c != nil {
		t.Errorf("no MCPSocket = %+v, want nil", c)
	}
	if c := (&Server{ConnectorForwarder: fwd, MCPSocket: "@x", RuntimeKind: RuntimeKindMCP}).sessionConnectors(context.Background(), "s"); c != nil {
		t.Errorf("type:mcp = %+v, want nil", c)
	}

	// A resolution failure degrades to no connectors rather than failing.
	errFwd := &fakeConnForwarder{refErr: errors.New("gateway down")}
	if c := (&Server{ConnectorForwarder: errFwd, MCPSocket: "@x"}).sessionConnectors(context.Background(), "s"); c != nil {
		t.Errorf("resolution error = %+v, want nil", c)
	}
}
