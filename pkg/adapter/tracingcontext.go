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

// setTracingContextFrameType is the frame type the unaddressed-frame
// counter labels this path's rejections with. spec: §16.1.
const setTracingContextFrameType = "set_tracing_context"

// setTracingContextFrame is the §28.5.3 outbound JSONL frame a
// runtime writes on stdout to register distributed-tracing identifiers:
// `{"type":"set_tracing_context","context":{"langsmith_run_id":"..."}}`.
type setTracingContextFrame struct {
	Type    string            `json:"type"`
	Context map[string]string `json:"context"`
}

// frameAddressing is the disposition of a session-scoped inbound frame
// against the Attach stream that delivered it. The two rejections are
// separate because they are counted on separate series: an unaddressed
// frame the pod cannot resolve is a runtime that emitted no identifier on
// a pod holding more than one slot, and a misaddressed frame names a
// session that is not this stream's live binding.
type frameAddressing int

const (
	// frameAddressed resolves to the delivering stream's session.
	frameAddressed frameAddressing = iota
	// frameUnaddressed carries no identifier on a pod holding more than
	// one slot, so no stream may claim it.
	frameUnaddressed
	// frameMisaddressed names an identifier that is not the delivering
	// stream's live binding.
	frameMisaddressed
)

// resolveTracingFrame resolves a set_tracing_context frame against the
// Attach stream that delivered it, which is bound to (sessionID, slotID).
// Both §28.5.3 conditions are evaluated here:
//
//  1. Address resolution. A frame carrying the per-session identifier is
//     addressed to the stream when the identifier equals the stream's own,
//     as exact string equality. A frame carrying no identifier resolves to
//     the stream's own binding while the pod holds at most one slot, and is
//     rejected on a pod holding more than one, where nothing in the frame
//     says which session it addresses. The slot count is every entry the
//     registry holds, bound or registered-but-unbound, so the rule fails
//     closed while a second session's workspace is being prepared. A count
//     of zero falls in the same arm as a count of one: the ending session's
//     stream is still draining the shared runtime's output after its entry
//     was deleted, and the frame resolves to it, where condition 2 then
//     rejects it as naming no live binding.
//  2. Live-binding confirmation: the registry still holds the stream's
//     address and the entry it holds still names this stream's session.
//     Every session is bound to a slot on every pod, so there is one
//     resolution here rather than a slotless case beside a slot-bound
//     one, and the entry is the only thing that can confirm the binding.
//
// Condition 2 reads state that changes over the session's lifetime, so it
// may only reject. The registry is read under a single s.mu hold so the
// slot count and both of condition 2's terms read one consistent state.
// Modeled on checkSessionBound, which validates the same binding at Attach
// bind time.
// spec: §28.5.3, §6.4.
func (s *Server) resolveTracingFrame(sessionID, frameSlot, slotID string) frameAddressing {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case frameSlot == "":
		if len(s.slots) > 1 {
			return frameUnaddressed
		}
	case frameSlot != slotID:
		return frameMisaddressed
	}
	st, ok := s.slotStateLocked(slotID)
	if !ok || st.sessionID != sessionID {
		return frameMisaddressed
	}
	return frameAddressed
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
// fanned out to every Attach stream, so a frame that does not resolve to
// this stream is rejected, counted, and logged as a protocol error, on
// the unaddressed counter when the frame names no session on a pod
// holding more than one slot and on the drop counter when it names one
// that is not this stream's live binding. The
// adapter relays nothing onward and returns nothing to the runtime: the
// inbound message set on this channel admits no report frame.
// spec: §28.5.3, §8.3.
func (s *Server) handleSetTracingContext(ctx context.Context, sessionID, slotID string, line []byte) {
	frameSlot := frameSlotID(line)
	switch s.resolveTracingFrame(sessionID, frameSlot, slotID) {
	case frameUnaddressed:
		incUnaddressedFrameRejected(setTracingContextFrameType)
		log.Printf("lenny-adapter: protocol error: set_tracing_context frame carrying no session identifier rejected on the stream bound to session %s, which shares the pod with another slot",
			sessionID)
		return
	case frameMisaddressed:
		incSetTracingContextDropped()
		log.Printf("lenny-adapter: protocol error: set_tracing_context frame for session %q dropped on the stream bound to session %s slot %q",
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
