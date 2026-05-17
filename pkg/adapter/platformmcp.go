// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"fmt"
	"net"

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
	lis, err := net.Listen("unix", s.MCPSocket)
	if err != nil {
		return fmt.Errorf("listen on MCP socket %s: %w", s.MCPSocket, err)
	}
	srv := mcp.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, lis, nonce) }()
	s.mu.Lock()
	s.mcpCancel = cancel
	s.mu.Unlock()
	return nil
}
