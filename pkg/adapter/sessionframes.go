// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

// This file holds the adapter's writes of the CH-MSGSOCK session frames:
// session_start, which opens a session on the runtime and carries the
// session's own context, and session_end, which releases it. Both are
// written through one helper pair over RuntimeProcess.WriteEnvelope, so
// every transport carries them without a change to the RuntimeProcess
// interface, and every start writes its session_start inside one open
// sequence. The paths that write either frame are the rows of the
// §28.5.3 Session frame writes table; no other path writes them.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §4.7.10 (Runtime
// process lifetime).

// sessionStartFrameType and sessionEndFrameType are the §28.5.3 type
// discriminators of the two adapter-written session frames.
const (
	sessionStartFrameType   = "session_start"
	sessionEndFrameType     = "session_end"
	sessionStartedFrameType = "session_started"
)

// sessionStartFrame is the §28.5.3 CH-MSGSOCK session_start frame. The
// three context members are pointers or maps without omitempty, so a
// session that has none writes them as JSON null, which the frame's
// schema admits as "absent or null". credentialsPath is omitted rather
// than written empty when no credential file was provisioned, because the
// schema rejects the empty string and a runtime given no path loads no
// credential bundle for the session.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start).
type sessionStartFrame struct {
	Type              string                     `json:"type"`
	SessionID         string                     `json:"sessionId"`
	StartID           string                     `json:"startId"`
	CredentialsPath   string                     `json:"credentialsPath,omitempty"`
	ExperimentContext *ManifestExperimentContext `json:"experimentContext"`
	TracingContext    map[string]string          `json:"tracingContext"`
	LLM               *ManifestLLM               `json:"llm"`
}

// sessionEndFrame is the §28.5.3 CH-MSGSOCK session_end frame. It carries
// the session alone: it is not acknowledged and has no reason member.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end).
type sessionEndFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
}

// buildSessionStartFrame assembles one start's session_start from the
// same inputs the start passes to writeSessionManifest. credentialsPath is
// set only when the session's registry entry, read under s.mu, records
// that AssignCredentials ran for it, which is when the adapter has
// provisioned the session's credential file; it is omitted otherwise, and
// also on an adapter wired with no credentials root, which resolves the
// path to the empty string. A session identifier that is not a safe path
// segment is an error rather than a path outside the slot tree.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §4.7.11.
func (s *Server) buildSessionStartFrame(sessionID, startID string, in manifestInputs) (sessionStartFrame, error) {
	f := sessionStartFrame{
		Type:              sessionStartFrameType,
		SessionID:         sessionID,
		StartID:           startID,
		ExperimentContext: manifestExperimentContext(in.experimentContext),
		LLM:               s.manifestLLM(sessionID),
	}
	if len(in.tracingContext) > 0 {
		f.TracingContext = in.tracingContext
	}
	s.mu.Lock()
	st, ok := s.slots[sessionID]
	assigned := ok && st.assigned
	s.mu.Unlock()
	if !assigned {
		return f, nil
	}
	path, err := s.sessionCredentialsPath(sessionID)
	if err != nil {
		return sessionStartFrame{}, err
	}
	f.CredentialsPath = path
	return f, nil
}

// writeSessionStart writes one start's session_start to the runtime. A
// type: mcp runtime exchanges no CH-MSGSOCK frame, so nothing is written
// for it. A failed write is returned, and the caller treats the frame as
// undelivered.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start; Session frame writes).
func (s *Server) writeSessionStart(sessionID, startID string, in manifestInputs) error {
	if s.RuntimeKind == RuntimeKindMCP {
		return nil
	}
	f, err := s.buildSessionStartFrame(sessionID, startID, in)
	if err != nil {
		return fmt.Errorf("build session_start for session %s: %w", sessionID, err)
	}
	b, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode session_start for session %s: %w", sessionID, err)
	}
	if err := s.Runtime.WriteEnvelope(sessionID, b); err != nil {
		return fmt.Errorf("write session_start for session %s: %w", sessionID, err)
	}
	return nil
}

// writeSessionEnd writes the session's session_end to the runtime. It
// returns nothing: session_end is not acknowledged, every caller is a
// teardown that proceeds whether or not the frame was delivered, and the
// runtime releases a session it was never told about when the connection
// that carries it ends. A failed write is logged at debug level. A
// type: mcp runtime exchanges no CH-MSGSOCK frame, so nothing is written
// for it.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end; Session frame writes).
func (s *Server) writeSessionEnd(sessionID string) {
	if s.RuntimeKind == RuntimeKindMCP || s.Runtime == nil {
		return
	}
	b, err := json.Marshal(sessionEndFrame{Type: sessionEndFrameType, SessionID: sessionID})
	if err == nil {
		err = s.Runtime.WriteEnvelope(sessionID, b)
	}
	if err != nil {
		slog.Debug("session_end_write_failed", "slot_id", sessionID, "error", err)
	}
}

// nextStartID mints the startId of the next session_start this Server
// writes, as a decimal string. spec: §28.5.3 (CH-MSGSOCK, Inbound:
// session_start).
func (s *Server) nextStartID() string {
	return strconv.FormatUint(s.startIDs.Add(1), 10)
}

// openRuntimeSession is the open sequence every start runs once the
// runtime is live for the session: StartSession after Runtime.Start,
// Resume after Runtime.Start, and the fresh arm of the SDK-warm
// ConfigureWorkspace after the pre-connected SDK is pointed at the
// workspace. It reports whether the start took the rule-8 record, at which
// the slot reaches running; a false result with a nil error is a refused
// confirmation, which the caller answers on its existing rollback arm, and
// an error is a start that failed, which the caller answers on its error
// arm. errSessionStartUnacknowledged is the error of a start whose
// session_started wait failed.
//
// The sequence runs under the slot's per-slot guard, which is the §5.2
// slot serialization. A caller that already holds the guard (Resume holds
// it from ahead of its claim) passes guardHeld; every other caller has the
// sequence take it here, bounded by ctx, the starting request's own
// deadline. The guard's current holder's deadline plays no part. An
// acquisition that outlives ctx fails the start with nothing written.
//
// Whether the start waits for session_started is decided as the sequence
// begins: it waits when the runtime's CH-RUNTIMEOPS connection has already
// completed its capability handshake. Under the guard the sequence:
//
//  1. Confirms, under s.mu, that the registry still holds the entry the
//     start's claim was admitted against, by pointer identity as
//     reclaimSlotIfOwnedLocked compares it. This is the first rule-8
//     confirmation. On a mismatch it writes no frame and refuses.
//  2. Mints the startId and writes session_start. A waiting start first
//     subscribes to the runtime's output and resets the entry's
//     acknowledgement gate to pending, so a session_started written at
//     once is neither missed nor finds the gate unprepared; a start that
//     does not wait resets the gate to not awaiting after the write. A
//     failed write fails the start, and the frame is treated as
//     undelivered.
//  3. A waiting start reads its subscription until the session_started
//     carrying its startId, bounded by the earlier of ctx and
//     SessionStartAckTimeout. When the wait ends without the frame, or the
//     frame carries error, it fails the gate, writes session_end, and
//     fails the start.
//  4. Confirms again and takes the record through noteRuntimeStarted.
//  5. When that second confirmation is refused, writes session_end and
//     refuses.
//
// Every return other than a taken record fails the entry's gate, unless a
// removal already released it, so a session-scoped CH-RUNTIMEOPS sender
// waiting on the gate returns without writing. The transition runs while
// the sequence still holds the slot guard and names this start's startId,
// so it never fails a later start's gate on the same entry.
//
// The refused arm of step 5 writes session_end without re-reading the
// registry, and needs no re-read. Step 1 established identity under the
// slot serialization, and while the sequence holds the guard no other
// entry for the identifier can be created: any removal of the entry opens
// the §5.2 reclaim hold in the same critical section, and that hold does
// not end before the removing section's cleanup completes, which requires
// the guard this sequence holds. The entry can therefore only have been
// removed, never replaced, and the token comparison inside
// noteRuntimeStarted detects exactly that. So no successor attempt's
// session_start can have been written for the identifier, and this
// attempt's session_end cannot reach the runtime after a later attempt's
// session_start.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §28.5.3 (CH-MSGSOCK,
// Outbound: session_started); §4.7.1 (role and gateway RPC contract), rule
// 8; §5.2 (slot-identifier reclaim hold)
func (s *Server) openRuntimeSession(ctx context.Context, sessionID string, claim slotClaim, in manifestInputs, guardHeld bool) (confirmed bool, err error) {
	awaiting := s.startAwaitsSessionStarted(ctx)
	if !guardHeld {
		unlock, guarded := s.lockSlotGuard(ctx, sessionID)
		defer unlock()
		if !guarded {
			failStartGate(claim, "")
			warnSlotGuardNotAcquired(sessionID, "openRuntimeSession")
			return false, fmt.Errorf("open session %s: slot serialization not acquired before the request deadline: %w",
				sessionID, ctx.Err())
		}
	}
	// Registered after the guard's unlock, so it runs first: the gate
	// moves to failed while the slot serialization is still held, before a
	// later start on the entry can take the guard and reset the gate.
	startID := ""
	defer func() {
		if !confirmed || err != nil {
			failStartGate(claim, startID)
		}
	}()
	if !s.registryHoldsClaimEntry(sessionID, claim) {
		return false, nil
	}
	startID = s.nextStartID()
	if err := s.writeStartFrame(ctx, sessionID, startID, claim.entry, in, awaiting); err != nil {
		return false, err
	}
	if s.noteRuntimeStarted(sessionID, claim.attempt) {
		return true, nil
	}
	s.writeSessionEnd(sessionID)
	return false, nil
}

// failStartGate moves the claimed entry's gate to failed for the start
// whose startID it names; a start that ended before minting one passes the
// empty string. A claim with no entry has no gate.
func failStartGate(claim slotClaim, startID string) {
	if claim.entry != nil {
		claim.entry.ack.fail(startID)
	}
}

// errSessionStartUnacknowledged is the error of a start whose wait for
// session_started ended without the frame, or whose frame carried error.
// The open sequence has already written the session's session_end, so the
// caller takes the session back off the runtime on the error arm a failed
// session_start write takes. spec: §28.5.3 (CH-MSGSOCK, Session frame
// writes), the acknowledgement-failure row.
var errSessionStartUnacknowledged = errors.New("session_start not acknowledged by the runtime")

// startAwaitsSessionStarted reports whether a start that begins now waits
// for session_started: the runtime's CH-RUNTIMEOPS connection completed
// its capability handshake, and the runtime exchanges CH-MSGSOCK frames. A
// start that does not wait leaves its session's CH-RUNTIMEOPS frames
// unordered by the acknowledgement until the session's next start.
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started), rule 3.
func (s *Server) startAwaitsSessionStarted(ctx context.Context) bool {
	return s.RuntimeKind != RuntimeKindMCP && s.Lifecycle != nil && s.Lifecycle.WaitHandshake(ctx, 0)
}

// sessionStartAckTimeout is the configured bound on the session_started
// wait, or runtimekit.DefaultSessionStartAckTimeout when none is set.
func (s *Server) sessionStartAckTimeout() time.Duration {
	if s.SessionStartAckTimeout > 0 {
		return s.SessionStartAckTimeout
	}
	return runtimekit.DefaultSessionStartAckTimeout
}

// writeStartFrame is the open sequence's session_start step and, for a
// start that waits, its session_started wait. gate is the entry the
// start's claim was admitted against. A start that does not wait writes
// the frame and then resets the gate to not awaiting, so a sender waiting
// since before the start proceeds at this start, after its session_start.
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started), rule 3.
func (s *Server) writeStartFrame(ctx context.Context, sessionID, startID string, entry *slotState, in manifestInputs, awaiting bool) error {
	if !awaiting {
		if err := s.writeSessionStart(sessionID, startID, in); err != nil {
			return err
		}
		entry.ack.reset(startID, false)
		return nil
	}
	return s.writeAwaitedSessionStart(ctx, sessionID, startID, entry, in)
}

// writeAwaitedSessionStart writes a waiting start's session_start and reads
// the runtime's output for the session_started that answers it. The
// subscription opens before the write, so an answer written at once is not
// missed, and it is cancelled on every return, because an unread
// subscription would stall the transport's shared output reader. The wait
// ends at the earlier of ctx's deadline and SessionStartAckTimeout.
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started); §28.5.3
// (CH-RUNTIMEOPS, Messages).
func (s *Server) writeAwaitedSessionStart(ctx context.Context, sessionID, startID string, entry *slotState, in manifestInputs) error {
	waitCtx, cancel := context.WithTimeout(ctx, s.sessionStartAckTimeout())
	defer cancel()
	frames, err := s.Runtime.Output(waitCtx, sessionID)
	if err != nil {
		return fmt.Errorf("subscribe to runtime output for session %s: %w", sessionID, err)
	}
	entry.ack.reset(startID, true)
	if err := s.writeSessionStart(sessionID, startID, in); err != nil {
		return err
	}
	code, read := readSessionStarted(waitCtx, frames, sessionID, startID)
	if !read || code != "" {
		return s.failUnacknowledgedStart(sessionID, startID, entry, code)
	}
	if entry.ack.settle(startID) || entry.ack.current() == ackReleased {
		// A removal can release the gate during the wait; the second
		// confirmation then refuses the start and writes its session_end.
		return nil
	}
	// The gate left pending for this start without a removal, so it can
	// no longer admit the session's CH-RUNTIMEOPS frames: the start fails
	// rather than take the record behind a gate that refuses them.
	return s.failUnacknowledgedStart(sessionID, startID, entry, ackGateNotPending)
}

// failUnacknowledgedStart ends a start whose session_started wait ended
// without the frame, or whose frame carried error: it logs
// session_start_unacknowledged, fails the entry's gate, and only then
// writes session_end, with no record taken. spec: §28.5.3
// (CH-MSGSOCK, Session frame writes), the acknowledgement-failure row.
func (s *Server) failUnacknowledgedStart(sessionID, startID string, entry *slotState, code string) error {
	slog.Warn("session_start_unacknowledged", "slot_id", sessionID, "error_code", code)
	entry.ack.fail(startID)
	s.writeSessionEnd(sessionID)
	return fmt.Errorf("open session %s: %w (%s)", sessionID, errSessionStartUnacknowledged, code)
}

// Error codes session_start_unacknowledged carries when the wait ended
// without a frame rather than on a session_started carrying error, or when
// the frame was read but the entry's gate had already left pending for
// this start.
const (
	ackWaitTimedOut     = "SESSION_START_ACK_TIMEOUT"
	ackWaitOutputClosed = "RUNTIME_OUTPUT_CLOSED"
	ackGateNotPending   = "SESSION_START_GATE_NOT_PENDING"
)

// sessionStartedFrame is the part of a §28.5.3 session_started frame the
// open sequence reads.
type sessionStartedFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	StartID   string `json:"startId"`
	Error     *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// readSessionStarted reads frames until the session_started for sessionID
// carrying startID. It reports read true and the frame's error code, empty
// when the frame carried no error, or read false and the reason the wait
// ended without one. A session_started carrying another start's startId is
// dropped, as is every other frame, which the Attach stream's own
// subscription still receives.
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started), rule 5.
func readSessionStarted(ctx context.Context, frames <-chan []byte, sessionID, startID string) (code string, read bool) {
	for {
		select {
		case line, ok := <-frames:
			if !ok {
				if ctx.Err() != nil {
					return ackWaitTimedOut, false
				}
				return ackWaitOutputClosed, false
			}
			if code, match := matchSessionStarted(line, sessionID, startID); match {
				return code, true
			}
		case <-ctx.Done():
			return ackWaitTimedOut, false
		}
	}
}

// matchSessionStarted reports whether line is the session_started for
// sessionID carrying startID, and the frame's error code when it carries
// one. A frame that carries an error object with no code reports the
// generic RUNTIME_ERROR.
func matchSessionStarted(line []byte, sessionID, startID string) (code string, match bool) {
	var f sessionStartedFrame
	if err := json.Unmarshal(line, &f); err != nil {
		return "", false
	}
	if f.Type != sessionStartedFrameType || f.SessionID != sessionID || f.StartID != startID {
		return "", false
	}
	if f.Error == nil {
		return "", true
	}
	if f.Error.Code == "" {
		return "RUNTIME_ERROR", true
	}
	return f.Error.Code, true
}

// awaitSessionStarted gates one session-scoped CH-RUNTIMEOPS frame on the
// acknowledgement gate of st, the entry its caller resolved once for the
// request. It never looks the session up again, because a successor
// attempt's entry under the same key carries a new gate. It returns nil at
// once, without consulting ctx, when the gate is read or not awaiting; an
// error when the gate failed or was released; and otherwise waits for the
// gate's next transition or the end of ctx. A caller holds neither s.ops
// nor a per-slot guard while it waits, and on an error writes no frame and
// ends as it ends when the runtime does not answer.
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started); §28.5.3
// (CH-RUNTIMEOPS, Messages).
func (s *Server) awaitSessionStarted(ctx context.Context, st *slotState) error {
	if st == nil {
		return errSessionStartNotAcknowledged
	}
	return st.ack.await(ctx)
}

// sessionStartedNow is awaitSessionStarted for a caller that holds s.ops
// and so must not wait: a gate that is not started or pending is an error.
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
func (s *Server) sessionStartedNow(st *slotState) error {
	if st == nil {
		return errSessionStartNotAcknowledged
	}
	return st.ack.ready()
}

// gateBound bounds ctx for a gate wait by d, or by SessionStartAckTimeout
// when d is not positive, so every wait on the gate ends even when no
// start ever settles it.
func (s *Server) gateBound(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = s.sessionStartAckTimeout()
	}
	return context.WithTimeout(ctx, d)
}

// registryHoldsClaimEntry reports, under s.mu, whether the registry still
// holds under sessionID the entry claim was admitted against. Pointer
// identity is compared rather than the token alone, because two untokened
// entries for one session carry the same empty token.
// spec: §4.7.1 (role and gateway RPC contract), rule 8.
func (s *Server) registryHoldsClaimEntry(sessionID string, claim slotClaim) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.slots[sessionID]
	return ok && cur == claim.entry
}
