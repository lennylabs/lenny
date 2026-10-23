// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"time"
)

// heartbeatAckFrameType is the §28.5.3 outbound frame that answers a
// heartbeat and carries the runtime's busy report.
const heartbeatAckFrameType = "heartbeat_ack"

// outputTapper is a runtime transport that can hand every output frame it
// reads to a tap, whether or not an Output subscriber exists. The socket,
// in-process, and MCP transports, and the developer-loop subprocess
// executor, implement it. spec: §4.7.1 (Role and Gateway RPC Contract),
// §28.5.3 (CH-MSGSOCK).
type outputTapper interface {
	TapOutput(fn func(line []byte))
}

// now returns the activity clock's current time: clock when a test set it,
// and time.Now otherwise.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// installActivityTap installs observeRuntimeFrame as the tap on the
// runtime's output and records that the adapter reads every runtime frame.
// A runtime that cannot tap its output leaves activityTapped false, so
// SealSession refuses its sessions and no heartbeat monitor runs for them.
// NewGRPCServer calls it once, before the server serves.
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
func (s *Server) installActivityTap() {
	t, ok := s.Runtime.(outputTapper)
	if !ok {
		return
	}
	t.TapOutput(s.observeRuntimeFrame)
	s.activityTapped = true
}

// stampLocked records activity on entry at the current time. A nil entry
// records nothing. Callers hold s.mu.
func (s *Server) stampLocked(entry *slotState) {
	if entry != nil {
		entry.lastActivity = s.now()
	}
}

// stampEntry records activity on entry, taking s.mu. The entry is the
// pointer its caller resolved, so a stamp never changes an entry that
// replaced it in the registry.
func (s *Server) stampEntry(entry *slotState) {
	if entry == nil {
		return
	}
	s.mu.Lock()
	s.stampLocked(entry)
	s.mu.Unlock()
}

// heartbeatAckProbe is the part of a heartbeat_ack the tap reads.
type heartbeatAckProbe struct {
	SessionID string `json:"sessionId"`
	Busy      bool   `json:"busy"`
}

// observeRuntimeFrame is the activity tap: the runtime transport calls it
// with each frame the runtime writes, before any Output subscriber sees the
// frame, so it observes every frame whether or not an Attach stream is open.
//
//   - A heartbeat_ack naming a session acks that session's heartbeat monitor
//     and, with busy true, stamps the session. One naming no session acks
//     every entry's monitor and stamps none, so an unattributed busy keeps
//     no session from idle suspension.
//   - A session-scoped frame (sessionScopedFrameTypes) stamps the entry it
//     resolves to as deliverToSession resolves it: the entry its sessionId
//     names, or, for a frame naming no session, the one entry of a pod that
//     holds at most one slot. It neither counts nor logs a rejected frame.
//   - Every other frame is activity for no session.
//
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
func (s *Server) observeRuntimeFrame(line []byte) {
	frameType := jsonlFrameType(line)
	if frameType == heartbeatAckFrameType {
		s.observeHeartbeatAck(line)
		return
	}
	if !sessionScopedFrameTypes[frameType] {
		return
	}
	addr := frameSessionID(line)
	s.mu.Lock()
	defer s.mu.Unlock()
	if addr != "" {
		if st, ok := s.slots[addr]; ok {
			s.stampLocked(st)
		}
		return
	}
	if len(s.slots) != 1 {
		return
	}
	for _, st := range s.slots {
		s.stampLocked(st)
	}
}

// observeHeartbeatAck applies a heartbeat_ack to the heartbeat monitors
// and the activity clock, as observeRuntimeFrame states.
// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
func (s *Server) observeHeartbeatAck(line []byte) {
	var ack heartbeatAckProbe
	if err := json.Unmarshal(line, &ack); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ack.SessionID == "" {
		for _, st := range s.slots {
			if st.hb != nil {
				st.hb.ack()
			}
		}
		return
	}
	st, ok := s.slots[ack.SessionID]
	if !ok {
		return
	}
	if st.hb != nil {
		st.hb.ack()
	}
	if ack.Busy {
		s.stampLocked(st)
	}
}

// observeRuntimeOpsSession stamps the entry of the session a CH-RUNTIMEOPS
// llm_request_completed names. It resolves the entry by the named session
// only; an empty name or a session with no entry stamps nothing.
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-RUNTIMEOPS)
func (s *Server) observeRuntimeOpsSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.slots[sessionID]; ok {
		s.stampLocked(st)
	}
}
