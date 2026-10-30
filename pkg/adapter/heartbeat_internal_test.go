// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// spec: §28.5.3 (CH-MSGSOCK) — the inbound heartbeat frame names the session
// it probes: {"type":"heartbeat","ts":<unix epoch seconds>,"sessionId":"<id>"}.
func TestHeartbeatFrameNamesTheSession_spec_28_5_3(t *testing.T) {
	m := newHeartbeatMonitor(time.Second, time.Second, "sess-a", func([]byte) error { return nil }, nil)
	m.nowUnix = func() int64 { return 1717430400 }
	var got map[string]any
	if err := json.Unmarshal(m.frame(), &got); err != nil {
		t.Fatalf("heartbeat frame not JSON: %v", err)
	}
	want := map[string]any{"type": "heartbeat", "ts": float64(1717430400), "sessionId": "sess-a"}
	if len(got) != len(want) {
		t.Fatalf("frame = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("frame[%q] = %v, want %v", k, got[k], v)
		}
	}
}

// spec: §28.5.3 — when no ack arrives within the window the adapter considers
// the runtime hung for the session; the monitor closes its hung channel after
// sending at least one heartbeat, escalates exactly once, and returns.
func TestHeartbeatMonitorFiresHungWithoutAck_spec_28_5_3(t *testing.T) {
	var beats, escalations int32
	m := newHeartbeatMonitor(10*time.Millisecond, 30*time.Millisecond, "sess-a", func([]byte) error {
		atomic.AddInt32(&beats, 1)
		return nil
	}, func(context.Context) { atomic.AddInt32(&escalations, 1) })
	m.start()
	select {
	case <-m.hung:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor never fired hung despite no ack")
	}
	<-m.done
	if atomic.LoadInt32(&beats) == 0 {
		t.Error("monitor fired hung without sending any heartbeat")
	}
	if got := atomic.LoadInt32(&escalations); got != 1 {
		t.Errorf("escalations = %d, want 1", got)
	}
	if m.replaceable() {
		t.Error("a hung monitor reports replaceable, so a restart would hide the hang")
	}
}

// spec: §28.5.3 — an acked runtime is not declared hung. The monitor keeps
// probing while every beat is answered.
func TestHeartbeatMonitorAckPreventsHung_spec_28_5_3(t *testing.T) {
	beat := make(chan struct{}, 8)
	m := newHeartbeatMonitor(10*time.Millisecond, 40*time.Millisecond, "sess-a", func([]byte) error {
		select {
		case beat <- struct{}{}:
		default:
		}
		return nil
	}, nil)
	m.start()
	defer m.stop()
	done := time.After(200 * time.Millisecond)
	for {
		select {
		case <-beat:
			m.ack()
		case <-m.hung:
			t.Fatal("monitor declared an acked runtime hung")
		case <-done:
			return
		}
	}
}

// spec: §28.5.3 — stop cancels the monitor and joins its goroutine, so no
// heartbeat is written after stop returns, and a clean stop is not a hang.
func TestHeartbeatMonitorStopJoinsTheGoroutine_spec_28_5_3(t *testing.T) {
	var after atomic.Bool
	var stopped atomic.Bool
	m := newHeartbeatMonitor(time.Millisecond, time.Second, "sess-a", func([]byte) error {
		if stopped.Load() {
			after.Store(true)
		}
		return nil
	}, nil)
	m.start()
	time.Sleep(5 * time.Millisecond)
	m.stop()
	stopped.Store(true)
	time.Sleep(10 * time.Millisecond)
	if after.Load() {
		t.Fatal("a heartbeat was written after stop returned")
	}
	select {
	case <-m.hung:
		t.Fatal("monitor fired hung on a clean stop")
	default:
	}
	if !m.replaceable() {
		t.Error("a cleanly stopped monitor is not replaceable")
	}
}

// A non-positive ack timeout falls back to the §28.5.3 default.
func TestNewHeartbeatMonitorDefaultsAckTimeout_spec_28_5_3(t *testing.T) {
	m := newHeartbeatMonitor(time.Second, 0, "sess-a", func([]byte) error { return nil }, nil)
	if m.ackTimeout != defaultHeartbeatAckTimeout {
		t.Errorf("ackTimeout = %v, want the 10s default", m.ackTimeout)
	}
}
