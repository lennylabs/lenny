// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Observed §5.1 / §15.4.3 integration levels. The adapter reports one of
// these on GetObservedIntegrationLevel so the gateway can compare against
// the runtime's declared integrationLevel. F-5.1.11.
const (
	observedLevelBasic    = "basic"
	observedLevelStandard = "standard"
	observedLevelFull     = "full"
)

// markMCPHandshakeSeen records that the runtime connected to the platform
// MCP server with a valid §15.4.3 nonce handshake. startPlatformMCP wires
// it as the server's OnHandshake hook.
func (s *Server) markMCPHandshakeSeen() {
	s.mu.Lock()
	s.mcpHandshakeSeen = true
	s.mu.Unlock()
}

// mcpHandshakeWasSeen reports whether the runtime completed an MCP
// initialize handshake during the current session.
func (s *Server) mcpHandshakeWasSeen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpHandshakeSeen
}

// observedIntegrationLevel classifies the §5.1 integration level the
// adapter has observed the runtime actually implement. A runtime that
// completed the §4.7 lifecycle handshake is Full (the §5.1 "runtime source
// of truth"); a runtime that connected only to the platform MCP server is
// Standard; a runtime that did neither is Basic (stdin/stdout binary
// protocol only). When the adapter offers a lifecycle channel it waits up
// to wait for the handshake so a Full runtime that is slow to dial is not
// misclassified; an adapter with no lifecycle channel cannot host a Full
// runtime, so it classifies from the MCP signal without waiting. F-5.1.11.
func (s *Server) observedIntegrationLevel(ctx context.Context, wait time.Duration) string {
	if s.Lifecycle != nil {
		if s.Lifecycle.WaitHandshake(ctx, wait) {
			return observedLevelFull
		}
	}
	if s.mcpHandshakeWasSeen() {
		return observedLevelStandard
	}
	return observedLevelBasic
}

// GetObservedIntegrationLevel reports the §5.1 / §15.4.3 integration level
// the adapter observed the runtime implement. The gateway calls it once
// per runtime on the first session assignment to enforce the declared
// integrationLevel (RUNTIME_LEVEL_UNDERPERFORMS on underperformance). The
// request's wait_ms bounds how long the adapter waits for the lifecycle
// handshake before classifying. F-5.1.11.
func (s *Server) GetObservedIntegrationLevel(ctx context.Context, req *adapterv1.GetObservedIntegrationLevelRequest) (*adapterv1.GetObservedIntegrationLevelResponse, error) {
	wait := time.Duration(req.GetWaitMs()) * time.Millisecond
	return &adapterv1.GetObservedIntegrationLevelResponse{
		ObservedLevel: s.observedIntegrationLevel(ctx, wait),
	}, nil
}
