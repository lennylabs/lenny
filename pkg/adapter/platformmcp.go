// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// startPlatformMCP starts the §4.7 platform MCP server on the adapter's
// configured Unix socket, authenticated by the manifest nonce. The socket
// is pod-wide, so the server is started at most once per pod, by the
// claim that took the once-per-pod start. It is a no-op when no MCP
// socket is configured or no nonce was issued (no manifest, so the
// runtime could not read one). The server runs until a release leaves the
// pod's shared runtime process serving no session.
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
	// §4.7: when SO_PEERCRED is disabled, the static nonce
	// is replayable, so the server adds a per-connection challenge-response.
	srv.RequireChallenge = s.NonceOnlyMode
	// spec: §5.1 — record that the runtime connected to the platform MCP
	// server so the observed-integration-level probe classifies it as at
	// least Standard. F-5.1.11.
	srv.OnHandshake = s.markMCPHandshakeSeen
	// §9.1: serve the platform tool catalog and forward every
	// tools/call to the gateway over GatewayControl, scoped to this
	// session. Without a forwarder the server serves an empty catalog (the
	// dev path with no gateway link). F-9.1.1.
	if s.PlatformForwarder != nil {
		srv.Provider = &platformToolProvider{forwarder: s.PlatformForwarder, server: s}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.mcpCancel = serveUntilCancelled(cancel, func() { _ = srv.Serve(ctx, serveLis, nonce) })
	s.mcpArmedNonce = nonce
	s.mu.Unlock()
	return nil
}

// serveUntilCancelled runs serve on its own goroutine and returns the stop
// function that cancels it and waits for the server to return. The wait is
// what makes the socket reusable: Serve owns the listener and closes it on
// cancel, so a caller that arms a fresh server on the same pod-wide socket
// after stopping the previous one finds the address free and the socket
// file already unlinked. It is also the single closer of that listener, so
// no second close can unlink a socket a later server created.
// spec: §15.4.3; §9.3.
func serveUntilCancelled(cancel context.CancelFunc, serve func()) context.CancelFunc {
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve()
	}()
	return func() {
		cancel()
		<-done
	}
}
