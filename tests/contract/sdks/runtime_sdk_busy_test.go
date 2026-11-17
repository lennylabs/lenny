// SPDX-License-Identifier: MIT

//go:build contract

package sdks_test

import (
	"testing"
	"time"
)

// heartbeatAckFor writes a heartbeat naming sessionID (none when empty) and
// returns the heartbeat_ack answering it, skipping the frames before it.
func (p *probeRuntime) heartbeatAckFor(t *testing.T, sessionID string) map[string]any {
	t.Helper()
	if sessionID == "" {
		p.send(t, `{"type":"heartbeat","ts":1}`)
	} else {
		p.send(t, `{"type":"heartbeat","ts":1,"sessionId":"`+sessionID+`"}`)
	}
	for {
		if f := p.next(t, 10*time.Second); f["type"] == "heartbeat_ack" {
			return f
		}
	}
}

// awaitIdleAck polls heartbeats until the ack naming sessionID omits busy.
func (p *probeRuntime) awaitIdleAck(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ack := p.heartbeatAckFor(t, sessionID)
		if ack["sessionId"] != sessionID {
			t.Fatalf("ack = %v, want sessionId %q", ack, sessionID)
		}
		if _, busy := ack["busy"]; !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session stayed busy: %v", ack)
		}
	}
}

// requireBusyAck asserts the ack naming sessionID reports busy: true.
func (p *probeRuntime) requireBusyAck(t *testing.T, sessionID string) {
	t.Helper()
	ack := p.heartbeatAckFor(t, sessionID)
	if ack["sessionId"] != sessionID || ack["busy"] != true {
		t.Fatalf("ack = %v, want sessionId %q and busy: true", ack, sessionID)
	}
}

// spec: 28.5.3 (CH-MSGSOCK), 15.7 (Runtime Author SDKs)
// diagnosis: an SDK's heartbeat_ack does not report the work the session is
// doing. A runtime built on it is idle-suspended, and its pod released,
// while it handles a message or holds busy work, or it keeps a session busy
// after the work ended.
//
// Each runtime SDK answers a heartbeat naming a held session with busy: true
// while a message is handled and while a busy hold is unreleased, omits busy
// after the response and the release, and answers an unaddressed heartbeat
// and one naming an unknown session with the bare ack.
func TestRuntimeSDKsReportBusyOnHeartbeatAck_spec_28_5_3(t *testing.T) {
	for _, sdk := range probeSDKs {
		t.Run(sdk.name, func(t *testing.T) {
			dir := t.TempDir()
			p := sdk.start(t, dir, writeOpsManifest(t, dir, nil))
			p.send(t, probeStart("sess_a", "st_a", "ok"))
			_ = p.nextOfType(t, "session_started")

			if ack := p.heartbeatAckFor(t, ""); len(ack) != 1 {
				t.Fatalf("unaddressed ack = %v, want the bare ack", ack)
			}
			if ack := p.heartbeatAckFor(t, "sess_x"); len(ack) != 1 {
				t.Fatalf("ack for an unknown session = %v, want the bare ack", ack)
			}
			p.awaitIdleAck(t, "sess_a")

			p.send(t, probeMessage("sess_a", "block-1"))
			p.awaitFile(t, "handling-sess_a")
			p.requireBusyAck(t, "sess_a")
			p.release(t, "release-msg-sess_a")
			p.awaitIdleAck(t, "sess_a")

			p.send(t, probeMessage("sess_a", "hold-1"))
			p.requireBusyAck(t, "sess_a")
			time.Sleep(50 * time.Millisecond)
			p.requireBusyAck(t, "sess_a")
			p.send(t, probeMessage("sess_a", "unhold-1"))
			p.awaitIdleAck(t, "sess_a")
		})
	}
}
