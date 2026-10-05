// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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
	sessionStartFrameType = "session_start"
	sessionEndFrameType   = "session_end"
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
// an error is a start that failed before the confirmation.
//
// The sequence runs under the slot's per-slot guard, which is the §5.2
// slot serialization. A caller that already holds the guard (Resume holds
// it from ahead of its claim) passes guardHeld; every other caller has the
// sequence take it here, bounded by ctx, the starting request's own
// deadline. The guard's current holder's deadline plays no part. An
// acquisition that outlives ctx fails the start with nothing written.
//
// Under the guard the sequence:
//
//  1. Confirms, under s.mu, that the registry still holds the entry the
//     start's claim was admitted against, by pointer identity as
//     reclaimSlotIfOwnedLocked compares it. This is the first rule-8
//     confirmation. On a mismatch it writes no frame and refuses.
//  2. Mints the startId and writes session_start. A failed write fails
//     the start, and the frame is treated as undelivered.
//  3. Confirms again and takes the record through noteRuntimeStarted.
//  4. When that second confirmation is refused, writes session_end and
//     refuses.
//
// The refused arm of step 4 writes session_end without re-reading the
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
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §4.7.1 (role and
// gateway RPC contract), rule 8; §5.2 (slot-identifier reclaim hold)
func (s *Server) openRuntimeSession(ctx context.Context, sessionID string, claim slotClaim, in manifestInputs, guardHeld bool) (confirmed bool, err error) {
	if !guardHeld {
		unlock, guarded := s.lockSlotGuard(ctx, sessionID)
		defer unlock()
		if !guarded {
			warnSlotGuardNotAcquired(sessionID, "openRuntimeSession")
			return false, fmt.Errorf("open session %s: slot serialization not acquired before the request deadline: %w",
				sessionID, ctx.Err())
		}
	}
	if !s.registryHoldsClaimEntry(sessionID, claim) {
		return false, nil
	}
	if err := s.writeSessionStart(sessionID, s.nextStartID(), in); err != nil {
		return false, err
	}
	if s.noteRuntimeStarted(sessionID, claim.attempt) {
		return true, nil
	}
	s.writeSessionEnd(sessionID)
	return false, nil
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
