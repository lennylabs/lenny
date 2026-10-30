// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"
)

// defaultHeartbeatAckTimeout is the §28.5.3 window the runtime has to
// answer a heartbeat before the adapter treats it as hung and ends the
// session's stream. spec: §28.5.3.
const defaultHeartbeatAckTimeout = 10 * time.Second

// jsonlFrameType returns the top-level `type` discriminant of a §28.5.3
// JSONL frame, or the empty string when the frame does not parse as a
// JSON object with a string `type`. It is the cheap classifier the
// Attach loop uses to recognize protocol-level frames (`heartbeat_ack`,
// `set_tracing_context`) the adapter consumes rather than relays.
func jsonlFrameType(line []byte) string {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return ""
	}
	return probe.Type
}

// heartbeatMonitor implements the §28.5.3 adapter-side liveness probe for
// one session: it sends a `{type:heartbeat,ts,sessionId}` frame to the
// runtime every interval and expects a `heartbeat_ack` within ackTimeout.
// When an ack does not arrive in time the runtime is considered hung for
// the session: hung closes and onHung runs once.
//
// A monitor belongs to its session's registry entry rather than to an
// Attach stream. markSessionStartWritten starts it when the session's
// session_start is written, writeSessionEnd stops and joins it before the
// session's session_end, and deregisterSlotLocked cancels it when the entry
// is removed, so it runs whether or not the gateway holds an Attach stream
// for the session. The goroutine never takes the server's mutex: its write
// is the runtime's WriteEnvelope, and onHung takes no lock.
//
// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
type heartbeatMonitor struct {
	interval   time.Duration
	ackTimeout time.Duration
	// sessionID is the session the heartbeat frame names.
	sessionID string
	// write sends one heartbeat frame to the runtime. It is
	// Runtime.WriteEnvelope bound to the session.
	write func([]byte) error
	// onHung runs once, on the monitor's own context, after hung closes.
	onHung func(context.Context)
	// nowUnix returns the heartbeat `ts` value. A seam so tests assert a
	// deterministic timestamp; production uses time.Now().Unix.
	nowUnix func() int64

	// ctx is the monitor's own context; cancelFn cancels it.
	ctx      context.Context
	cancelFn context.CancelFunc
	// done closes when run returns.
	done chan struct{}
	// acks carries a signal per observed heartbeat_ack. Buffered so the
	// activity tap never blocks delivering one.
	acks chan struct{}
	// hung closes once when the ack deadline elapses with no ack.
	hung     chan struct{}
	hungOnce sync.Once
}

// newHeartbeatMonitor builds a monitor for one session. interval must be > 0
// (the caller gates on heartbeatsReachRuntime); ackTimeout falls back to the
// §28.5.3 10s default when non-positive. A nil onHung runs nothing.
func newHeartbeatMonitor(interval, ackTimeout time.Duration, sessionID string, write func([]byte) error, onHung func(context.Context)) *heartbeatMonitor {
	if ackTimeout <= 0 {
		ackTimeout = defaultHeartbeatAckTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &heartbeatMonitor{
		interval:   interval,
		ackTimeout: ackTimeout,
		sessionID:  sessionID,
		write:      write,
		onHung:     onHung,
		nowUnix:    func() int64 { return time.Now().Unix() },
		ctx:        ctx,
		cancelFn:   cancel,
		done:       make(chan struct{}),
		acks:       make(chan struct{}, 1),
		hung:       make(chan struct{}),
	}
}

// start launches the monitor's goroutine.
func (m *heartbeatMonitor) start() {
	go m.run()
}

// cancel ends the monitor's context and returns at once. It is safe under
// the server's mutex and idempotent.
func (m *heartbeatMonitor) cancel() {
	m.cancelFn()
}

// stop cancels the monitor and waits for its goroutine to return, so a
// heartbeat write in flight when stop is called has returned when stop
// returns. Never call it with the server's mutex held.
func (m *heartbeatMonitor) stop() {
	m.cancel()
	<-m.done
}

// frame builds the §28.5.3 inbound heartbeat
// `{"type":"heartbeat","ts":<unix>,"sessionId":"<id>"}`.
func (m *heartbeatMonitor) frame() []byte {
	id, _ := json.Marshal(m.sessionID)
	return []byte(`{"type":"heartbeat","ts":` + strconv.FormatInt(m.nowUnix(), 10) + `,"sessionId":` + string(id) + `}`)
}

// ack records that the runtime answered the outstanding heartbeat. The
// activity tap calls it on a `heartbeat_ack` naming the session, or on one
// naming no session, and it never blocks: a full buffer means an ack is
// already queued, which is enough to clear the pending beat.
func (m *heartbeatMonitor) ack() {
	select {
	case m.acks <- struct{}{}:
	default:
	}
}

// run drives the send/ack/deadline loop until the monitor is cancelled, a
// heartbeat write fails, or the runtime misses the ack deadline. A single
// ack deadline is armed on the first unacked beat and held until an ack
// arrives, so a runtime that goes silent is caught within ackTimeout of
// the first beat it failed to answer rather than being granted a fresh
// window every interval.
func (m *heartbeatMonitor) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// ackTimer starts stopped-and-drained; it is armed only while a beat
	// is awaiting its ack, so its channel never fires spuriously.
	ackTimer := time.NewTimer(time.Hour)
	if !ackTimer.Stop() {
		<-ackTimer.C
	}
	pending := false

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.write(m.frame()); err != nil {
				// The transport is gone; stop probing.
				return
			}
			if !pending {
				pending = true
				ackTimer.Reset(m.ackTimeout)
			}
		case <-m.acks:
			if pending {
				pending = false
				if !ackTimer.Stop() {
					select {
					case <-ackTimer.C:
					default:
					}
				}
			}
		case <-ackTimer.C:
			// spec: §28.5.3 — no ack within the window; the runtime is
			// hung for this session. Close hung, then escalate once.
			m.hungOnce.Do(func() { close(m.hung) })
			if m.onHung != nil {
				m.onHung(m.ctx)
			}
			return
		}
	}
}

// heartbeatsReachRuntime reports whether the adapter runs heartbeat
// monitors: heartbeats are enabled, the adapter reads the runtime's output
// through the activity tap (an untapped runtime's acks would reach no
// monitor, so every session would read as hung), and the runtime exchanges
// CH-MSGSOCK frames (a type: mcp runtime drops heartbeat). With no monitor
// SealSession decides on frames and platform and connector calls alone.
// spec: §28.5.3 (CH-MSGSOCK), §4.7.1 (Role and Gateway RPC Contract)
func (s *Server) heartbeatsReachRuntime() bool {
	return s.HeartbeatInterval > 0 && s.activityTapped && s.RuntimeKind != RuntimeKindMCP
}

// startHeartbeatLocked starts entry's heartbeat monitor for sessionID when
// heartbeats reach the runtime and the entry holds no running monitor. A
// stopped monitor that did not find the runtime hung is replaced, so a
// session whose session_start is written again on the same entry is probed
// again; a hung monitor stays, so hung stays closed until the entry is
// removed. It is the only start site. Callers hold s.mu.
// spec: §28.5.3 (CH-MSGSOCK)
func (s *Server) startHeartbeatLocked(sessionID string, entry *slotState) {
	if !s.heartbeatsReachRuntime() || s.Runtime == nil {
		return
	}
	if entry.hb != nil && !entry.hb.replaceable() {
		return
	}
	rt := s.Runtime
	mon := newHeartbeatMonitor(s.HeartbeatInterval, s.HeartbeatAckTimeout, sessionID,
		func(frame []byte) error { return rt.WriteEnvelope(sessionID, frame) },
		func(ctx context.Context) { s.onHeartbeatHung(ctx, sessionID, rt) })
	entry.hb = mon
	mon.start()
}

// replaceable reports whether the monitor has stopped without finding the
// runtime hung.
func (m *heartbeatMonitor) replaceable() bool {
	select {
	case <-m.hung:
		return false
	default:
	}
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// stopEntryHeartbeat stops and joins entry's monitor, read under s.mu and
// stopped with s.mu released. writeSessionEnd calls it before it writes the
// session_end, so no heartbeat for the session follows that frame.
func (s *Server) stopEntryHeartbeat(entry *slotState) {
	if entry == nil {
		return
	}
	s.mu.Lock()
	hb := entry.hb
	s.mu.Unlock()
	if hb != nil {
		hb.stop()
	}
}

// entryHeartbeatHung returns the hung channel of the named session's
// monitor, nil when the entry has none.
func (s *Server) entryHeartbeatHung(sessionID string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok || st.hb == nil {
		return nil
	}
	return st.hb.hung
}

// onHeartbeatHung performs the §28.5.3 unresponsive-agent escalation:
// it calls the clean Runtime.Interrupt for the session and logs the
// escalation. The session's heartbeat monitor calls it once, after its hung
// channel closes; an open Attach stream for the session then ends.
// Interrupt signals a process only on the developer-loop executor; the
// sidecar transport ends nothing, because no heartbeat escalation signals
// the runtime process.
// spec: §28.5.3; §4.7.10 (Runtime process lifetime).
//
// On a supervised pod the escalation is the hang cause of the not-live
// state, on every pod kind, so no later start joins the hung process.
// spec: §4.7.10 (Runtime supervisor, Liveness).
func (s *Server) onHeartbeatHung(ctx context.Context, sessionID string, rt RuntimeProcess) {
	log.Printf("lenny-adapter: runtime for session %s missed the heartbeat ack deadline; ending the session's stream (§28.5.3)", sessionID)
	if sr := s.supervisedRuntime(); sr != nil {
		sr.MarkNotLive(CauseHang)
	}
	if err := rt.Interrupt(ctx, sessionID, false); err != nil {
		log.Printf("lenny-adapter: interrupt of hung runtime for session %s failed: %v", sessionID, err)
	}
}
