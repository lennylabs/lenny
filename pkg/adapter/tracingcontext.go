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

// setTracingContextFrame is the §28.5.3 outbound JSONL frame a
// runtime writes on stdout to register distributed-tracing identifiers:
// `{"type":"set_tracing_context","context":{"langsmith_run_id":"..."}}`.
type setTracingContextFrame struct {
	Type    string            `json:"type"`
	Context map[string]string `json:"context"`
}

// tracingFrameAddressesStream reports whether a set_tracing_context frame
// is addressed to the Attach stream that delivered it, which is bound to
// (sessionID, slotID). Both §28.5.3 conditions are evaluated here:
//
//  1. Address equality: the frame's slotId equals the stream's slotId as
//     exact string equality, with an absent or empty slotId counting as
//     the empty string on both sides. This is the only condition that
//     names a session. The caller reads the frame's address with
//     frameSlotID, which yields the empty string for any frame that
//     carries no readable slotId.
//  2. Live-binding confirmation: the registry still holds the stream's
//     address and the entry it holds still names this stream's session.
//     Every session is bound to a slot on every pod, so there is one
//     resolution here rather than a slotless case beside a slot-bound
//     one, and the entry is the only thing that can confirm the binding.
//
// Condition 2 reads state that changes over the session's lifetime, so it
// may only reject. The registry is read under a single s.mu hold so both
// of its terms read one consistent state. Modeled on checkSessionBound,
// which validates the same binding at Attach bind time.
// spec: §28.5.3, §6.4.
func (s *Server) tracingFrameAddressesStream(sessionID, frameSlot, slotID string) bool {
	if frameSlot != slotID {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(slotID)
	return ok && st.sessionID == sessionID
}

// handleSetTracingContext consumes a §28.5.3 set_tracing_context
// frame and registers its identifiers with the gateway so they propagate
// through subsequent lenny/delegate_task calls. It reuses the gateway's
// lenny/set_tracing_context platform tool — which merges the identifiers
// into the session row and enforces the §8.3 validation rules — forwarded
// over the same GatewayControl link the intra-pod platform MCP server
// uses. The adapter makes the call itself rather than routing it through
// the runtime's MCP client, so the frame works at all tiers including
// Basic (which has no MCP access). The bound sessionID is injected, so a
// runtime cannot register tracing context against a session it does not
// own.
//
// The single pod-global runtime serves every slot and its output is
// fanned out to every Attach stream, so a frame that does not address
// this stream is dropped, counted, and logged as a protocol error. The
// adapter relays nothing onward and returns nothing to the runtime: the
// inbound message set on this channel admits no report frame.
// spec: §28.5.3, §8.3.
func (s *Server) handleSetTracingContext(ctx context.Context, sessionID, slotID string, line []byte) {
	frameSlot := frameSlotID(line)
	if !s.tracingFrameAddressesStream(sessionID, frameSlot, slotID) {
		incSetTracingContextDropped()
		log.Printf("lenny-adapter: protocol error: set_tracing_context frame for slot %q dropped on the stream bound to session %s slot %q",
			frameSlot, sessionID, slotID)
		return
	}
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
