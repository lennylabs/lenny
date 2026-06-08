// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// startPlatformMCP starts the §4.7 platform MCP server for the current
// session on the adapter's configured Unix socket, authenticated by
// the session's manifest nonce. It is a no-op when no MCP socket is
// configured or no nonce was issued (no manifest, so the runtime could
// not read one). The server runs until releaseSession stops it.
func (s *Server) startPlatformMCP(nonce string) error {
	if s.MCPSocket == "" || nonce == "" {
		return nil
	}
	// §4.7 / §13: when a runtime UID is configured, the shared listener
	// helper rejects any MCP connection from a process not running as that
	// UID.
	serveLis, err := s.listenIntraPodMCP(s.MCPSocket)
	if err != nil {
		return err
	}
	srv := mcp.NewServer()
	// §4.7 lines 879-883: when SO_PEERCRED is disabled, the static nonce
	// is replayable, so the server adds a per-connection challenge-response.
	srv.RequireChallenge = s.NonceOnlyMode
	// spec: §5.1 — record that the runtime connected to the platform MCP
	// server so the observed-integration-level probe classifies it as at
	// least Standard. F-5.1.11.
	srv.OnHandshake = s.markMCPHandshakeSeen
	// §9.1 lines 14-31: serve the platform tool catalog and forward every
	// tools/call to the gateway over GatewayControl, scoped to this
	// session. Without a forwarder the server serves an empty catalog (the
	// dev path with no gateway link). F-9.1.1.
	if s.PlatformForwarder != nil {
		s.mu.Lock()
		sessionID := s.sessionID
		s.mu.Unlock()
		srv.Provider = &platformToolProvider{forwarder: s.PlatformForwarder, sessionID: sessionID}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, serveLis, nonce) }()
	s.mu.Lock()
	s.mcpCancel = cancel
	s.mu.Unlock()
	return nil
}
