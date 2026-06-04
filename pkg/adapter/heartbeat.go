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

// defaultHeartbeatAckTimeout is the §15.4.1 line 1826 window the runtime
// has to answer a heartbeat before the adapter treats it as hung and
// sends SIGTERM. spec: §15.4.1 line 1826.
const defaultHeartbeatAckTimeout = 10 * time.Second

// jsonlFrameType returns the top-level `type` discriminant of a §15.4.1
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

// heartbeatMonitor implements the §15.4.1 adapter-side liveness probe:
// it sends a `{type:heartbeat,ts}` frame to the runtime every interval
// and expects a `heartbeat_ack` within ackTimeout. When an ack does not
// arrive in time the runtime is considered hung and onHung fires once
// (the Attach loop SIGTERMs the runtime and ends the stream). spec:
// §15.4.1 lines 1442, 1826, 2061.
type heartbeatMonitor struct {
	interval   time.Duration
	ackTimeout time.Duration
	// write sends one heartbeat frame to the runtime. It is
	// Runtime.WriteEnvelope bound to the session.
	write func([]byte) error
	// nowUnix returns the heartbeat `ts` value. A seam so tests assert a
	// deterministic timestamp; production uses time.Now().Unix.
	nowUnix func() int64

	// acks carries a signal per observed heartbeat_ack. Buffered so the
	// Attach loop never blocks delivering one.
	acks chan struct{}
	// hung closes once when the ack deadline elapses with no ack.
	hung     chan struct{}
	hungOnce sync.Once
}

// newHeartbeatMonitor builds a monitor for one Attach session. interval
// must be > 0 (the caller gates on s.HeartbeatInterval); ackTimeout falls
// back to the §15.4.1 line 1826 10s default when non-positive.
func newHeartbeatMonitor(interval, ackTimeout time.Duration, write func([]byte) error) *heartbeatMonitor {
	if ackTimeout <= 0 {
		ackTimeout = defaultHeartbeatAckTimeout
	}
	return &heartbeatMonitor{
		interval:   interval,
		ackTimeout: ackTimeout,
		write:      write,
		nowUnix:    func() int64 { return time.Now().Unix() },
		acks:       make(chan struct{}, 1),
		hung:       make(chan struct{}),
	}
}

// frame builds the §15.4.1 line 1823 inbound heartbeat
// `{"type":"heartbeat","ts":<unix>}`.
func (m *heartbeatMonitor) frame() []byte {
	return []byte(`{"type":"heartbeat","ts":` + strconv.FormatInt(m.nowUnix(), 10) + `}`)
}

// ack records that the runtime answered the outstanding heartbeat. It is
// called from the Attach loop on a `heartbeat_ack` frame and never
// blocks: a full buffer means an ack is already queued, which is enough
// to clear the pending beat.
func (m *heartbeatMonitor) ack() {
	select {
	case m.acks <- struct{}{}:
	default:
	}
}

// run drives the send/ack/deadline loop until ctx is cancelled (the
// Attach RPC ended) or the runtime misses the ack deadline. A single
// ack deadline is armed on the first unacked beat and held until an ack
// arrives, so a runtime that goes silent is caught within ackTimeout of
// the first beat it failed to answer rather than being granted a fresh
// window every interval.
func (m *heartbeatMonitor) run(ctx context.Context) {
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
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.write(m.frame()); err != nil {
				// The transport is gone; the Attach loop's own error
				// handling tears the session down. Stop probing.
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
			// spec: §15.4.1 line 1826 — no ack within the window; the
			// process is hung. Signal the Attach loop once.
			m.hungOnce.Do(func() { close(m.hung) })
			return
		}
	}
}

// startHeartbeat launches the §15.4.1 heartbeat monitor for an Attach
// session when s.HeartbeatInterval is configured, returning the monitor
// (nil when heartbeats are disabled). The monitor goroutine exits when
// ctx is cancelled — the Attach RPC's stream context — so it is bounded
// by the session it probes. spec: §15.4.1 lines 1442, 1826.
func (s *Server) startHeartbeat(ctx context.Context, sessionID string) *heartbeatMonitor {
	if s.HeartbeatInterval <= 0 {
		return nil
	}
	mon := newHeartbeatMonitor(s.HeartbeatInterval, s.HeartbeatAckTimeout, func(frame []byte) error {
		return s.Runtime.WriteEnvelope(sessionID, frame)
	})
	go mon.run(ctx)
	return mon
}

// onHeartbeatHung performs the §15.4.1 line 1826 unresponsive-agent
// escalation: it sends SIGTERM (the clean Interrupt) to the hung runtime
// and logs the escalation. The Attach loop calls it once when the
// monitor's hung channel closes, then ends the stream.
func (s *Server) onHeartbeatHung(ctx context.Context, sessionID string) {
	log.Printf("lenny-adapter: runtime for session %s missed the heartbeat ack deadline; sending SIGTERM (§15.4.1 line 1826)", sessionID)
	if err := s.Runtime.Interrupt(ctx, sessionID, false); err != nil {
		log.Printf("lenny-adapter: SIGTERM of hung runtime for session %s failed: %v", sessionID, err)
	}
}
