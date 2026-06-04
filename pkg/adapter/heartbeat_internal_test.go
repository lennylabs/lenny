// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// spec: §15.4.1 line 1823 — the inbound heartbeat frame is
// {"type":"heartbeat","ts":<unix epoch seconds>}.
func TestHeartbeatMonitorFrameIsValid_spec_15_4_1_1823(t *testing.T) {
	m := newHeartbeatMonitor(time.Second, time.Second, func([]byte) error { return nil })
	m.nowUnix = func() int64 { return 1717430400 }
	var got struct {
		Type string `json:"type"`
		TS   int64  `json:"ts"`
	}
	if err := json.Unmarshal(m.frame(), &got); err != nil {
		t.Fatalf("heartbeat frame not JSON: %v", err)
	}
	if got.Type != "heartbeat" {
		t.Errorf("frame type = %q, want heartbeat", got.Type)
	}
	if got.TS != 1717430400 {
		t.Errorf("frame ts = %d, want the injected unix time", got.TS)
	}
}

// spec: §15.4.1 line 1826 — when no ack arrives within the window the
// adapter considers the process hung; the monitor closes its hung
// channel after sending at least one heartbeat.
func TestHeartbeatMonitorFiresHungWithoutAck_spec_15_4_1_1826(t *testing.T) {
	var beats int32
	m := newHeartbeatMonitor(10*time.Millisecond, 30*time.Millisecond, func([]byte) error {
		atomic.AddInt32(&beats, 1)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.run(ctx)

	select {
	case <-m.hung:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor never fired hung despite no ack")
	}
	if atomic.LoadInt32(&beats) == 0 {
		t.Error("monitor fired hung without sending any heartbeat")
	}
}

// spec: §15.4.1 line 1826 — an acked runtime is not declared hung. The
// monitor keeps probing while every beat is answered.
func TestHeartbeatMonitorAckPreventsHung_spec_15_4_1_1826(t *testing.T) {
	beat := make(chan struct{}, 8)
	m := newHeartbeatMonitor(10*time.Millisecond, 40*time.Millisecond, func([]byte) error {
		select {
		case beat <- struct{}{}:
		default:
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.run(ctx)

	// Ack every heartbeat the monitor sends for a few intervals.
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

// The monitor goroutine exits when the Attach RPC's context is cancelled,
// so it is bounded by the session it probes and leaks nothing.
func TestHeartbeatMonitorStopsOnContextCancel(t *testing.T) {
	m := newHeartbeatMonitor(5*time.Millisecond, time.Second, func([]byte) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not return after context cancel")
	}
	select {
	case <-m.hung:
		t.Fatal("monitor fired hung on a clean context cancel")
	default:
	}
}

// A non-positive ack timeout falls back to the §15.4.1 line 1826 default.
func TestNewHeartbeatMonitorDefaultsAckTimeout_spec_15_4_1_1826(t *testing.T) {
	m := newHeartbeatMonitor(time.Second, 0, func([]byte) error { return nil })
	if m.ackTimeout != defaultHeartbeatAckTimeout {
		t.Errorf("ackTimeout = %v, want the 10s default", m.ackTimeout)
	}
}
