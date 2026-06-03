// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// sessionConnector is one resolved §9.3 connector for the current
// session: the registry id and the intra-pod Unix socket the adapter
// opens an MCP server on for it. F-9.1.2.
type sessionConnector struct {
	ID     string
	Socket string
}

// sessionConnectors resolves the §9.3 connectors the session's effective
// delegation policy permits and computes each one's intra-pod socket
// name. It is best-effort: a missing forwarder, a disabled intra-pod MCP
// surface (no MCPSocket), a type:mcp runtime, or a gateway resolution
// failure yields no connectors so a session still starts. The gateway
// filters the list by the session's policy, so the adapter opens a
// per-connector server only for connectors the runtime may use.
// spec: §9.3 line 142. F-9.1.2.
func (s *Server) sessionConnectors(ctx context.Context, sessionID string) []sessionConnector {
	if s.ConnectorForwarder == nil || s.MCPSocket == "" || s.RuntimeKind == RuntimeKindMCP {
		return nil
	}
	refs, err := s.ConnectorForwarder.ListSessionConnectors(ctx, sessionID)
	if err != nil {
		slog.WarnContext(ctx, "connector_resolve_failed",
			"session_id", sessionID, "error", err.Error())
		return nil
	}
	out := make([]sessionConnector, 0, len(refs))
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		out = append(out, sessionConnector{ID: r.ID, Socket: connectorSocketName(s.MCPSocket, r.ID)})
	}
	return out
}

// startConnectorMCPServers opens one intra-pod MCP server per resolved
// §9.3 connector, each authenticated by the session's manifest nonce and
// forwarding tools/list and tools/call to the gateway over
// GatewayControl, scoped to the session and that one connector. It is
// best-effort: a per-connector listen failure is logged and skipped so
// the remaining connectors still serve. releaseSession stops every
// server started here. spec: §9.3 lines 142-164. F-9.1.2.
func (s *Server) startConnectorMCPServers(sessionID, nonce string, conns []sessionConnector) {
	if nonce == "" || s.ConnectorForwarder == nil {
		return
	}
	for _, c := range conns {
		if err := s.startConnectorMCP(sessionID, nonce, c); err != nil {
			slog.Warn("connector_mcp_start_failed",
				"session_id", sessionID, "connector_id", c.ID, "socket", c.Socket, "error", err.Error())
		}
	}
}

// startConnectorMCP opens and serves one connector's intra-pod MCP
// server. The server advertises the connector's tool catalog on
// tools/list and forwards every tools/call to the gateway, both scoped
// to the session and the connector. F-9.1.2.
func (s *Server) startConnectorMCP(sessionID, nonce string, c sessionConnector) error {
	serveLis, err := s.listenIntraPodMCP(c.Socket)
	if err != nil {
		return err
	}
	srv := mcp.NewServer()
	// §4.7 lines 879-883: mirror the platform server's per-connection
	// challenge when SO_PEERCRED is disabled so the static nonce is not
	// replayable on the connector sockets either.
	srv.RequireChallenge = s.NonceOnlyMode
	srv.Provider = &connectorToolProvider{forwarder: s.ConnectorForwarder, sessionID: sessionID, connectorID: c.ID}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, serveLis, nonce) }()
	s.mu.Lock()
	s.connectorCancels = append(s.connectorCancels, cancel)
	s.mu.Unlock()
	return nil
}

// listenIntraPodMCP binds an intra-pod MCP Unix socket, applying the §4.7
// / §13 SO_PEERCRED peer-credential check when a runtime UID is
// configured so a process not running as the agent UID cannot connect. It
// is the shared listener path for the platform and per-connector MCP
// servers. F-9.1.2.
func (s *Server) listenIntraPodMCP(socket string) (net.Listener, error) {
	lis, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen on MCP socket %s: %w", socket, err)
	}
	if s.RuntimeUID != 0 {
		uid := s.RuntimeUID
		return &peerCheckedListener{
			Listener: lis,
			check:    func(c net.Conn) error { return checkPeerUID(c, uid) },
		}, nil
	}
	return lis, nil
}

// connectorSocketName derives the intra-pod Unix socket a §9.3
// connector's MCP server binds, relative to the platform MCP socket
// `base`. An abstract base (`@…`, the production form) yields an abstract
// per-connector socket; a filesystem base (the dev / test form) yields a
// sibling `.sock` file. The id is sanitised so a registry id carrying
// path-unsafe characters cannot escape the socket namespace.
// spec: §9.3 line 142. F-9.1.2.
func connectorSocketName(base, id string) string {
	safe := sanitizeConnectorID(id)
	if strings.HasPrefix(base, "@") {
		return "@lenny-connector-" + safe
	}
	return filepath.Join(filepath.Dir(base), "lenny-connector-"+safe+".sock")
}

// sanitizeConnectorID maps any character outside [A-Za-z0-9._-] to a
// dash so a connector id cannot inject a path separator or a null byte
// into the derived socket name.
func sanitizeConnectorID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
