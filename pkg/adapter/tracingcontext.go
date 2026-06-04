// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// setTracingContextForwardTimeout bounds the gateway round-trip the
// adapter makes when it registers a runtime's tracing context, so a slow
// or unreachable gateway cannot stall the Attach output relay.
const setTracingContextForwardTimeout = 5 * time.Second

// setTracingContextFrame is the §15.4.1 line 1455 outbound JSONL frame a
// runtime writes on stdout to register distributed-tracing identifiers:
// `{"type":"set_tracing_context","context":{"langsmith_run_id":"..."}}`.
type setTracingContextFrame struct {
	Type    string            `json:"type"`
	Context map[string]string `json:"context"`
}

// handleSetTracingContext consumes a §15.4.1 line 1455 set_tracing_context
// frame and registers its identifiers with the gateway so they propagate
// through subsequent lenny/delegate_task calls. It reuses the gateway's
// lenny/set_tracing_context platform tool — which merges the identifiers
// into the session row and enforces the §8.3 validation rules — forwarded
// over the same GatewayControl link the intra-pod platform MCP server
// uses. The adapter makes the call itself rather than routing it through
// the runtime's MCP client, so the frame works at all tiers including
// Basic (which has no MCP access). The bound sessionID is injected, so a
// runtime cannot register tracing context against a session it does not
// own. spec: §15.4.1 line 1455, §8.3.
func (s *Server) handleSetTracingContext(ctx context.Context, sessionID string, line []byte) {
	if s.PlatformForwarder == nil {
		// Dev path with no gateway link: there is nothing to register
		// against, so the frame is dropped after being consumed.
		return
	}
	var frame setTracingContextFrame
	if err := json.Unmarshal(line, &frame); err != nil || len(frame.Context) == 0 {
		// A malformed or empty frame carries no identifiers to register.
		return
	}
	args, err := json.Marshal(struct {
		SessionID string            `json:"sessionId"`
		Context   map[string]string `json:"context"`
	}{SessionID: sessionID, Context: frame.Context})
	if err != nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, setTracingContextForwardTimeout)
	defer cancel()
	if _, err := s.PlatformForwarder.CallPlatformTool(callCtx, sessionID, "lenny/set_tracing_context", args); err != nil {
		// §8.3 validation failures and transport errors are logged; the
		// runtime continues. The tracing identifiers are advisory metadata
		// and a registration failure must not fail the task.
		log.Printf("lenny-adapter: forward set_tracing_context for session %s: %v", sessionID, err)
	}
}
