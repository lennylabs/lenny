// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"
)

// spec: 15.4.6 (Conformance Test Suite, heartbeat busy), 28.5.3 (CH-MSGSOCK)
//
// The heartbeat busy check passes a runtime that reports busy with the
// sessionId while the message is outstanding and a serial runtime whose ack
// follows its response, and fails one that answers a bare ack, or an
// addressed ack without busy, while it handles the message.
func TestHeartbeatBusyJudgesTheAckOrder_spec_15_4_6(t *testing.T) {
	sid := complianceSessionID
	cases := []struct {
		name   string
		frames []string
		pass   bool
	}{
		{"busy ack before the response", []string{
			`{"type":"heartbeat_ack","sessionId":"` + sid + `","busy":true}`,
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
		}, true},
		{"serial ack after the response", []string{
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
			`{"type":"heartbeat_ack","sessionId":"` + sid + `"}`,
		}, true},
		{"bare ack after the response", []string{
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
			`{"type":"heartbeat_ack"}`,
		}, true},
		{"bare ack while handling", []string{
			`{"type":"heartbeat_ack"}`,
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
		}, false},
		{"addressed ack without busy while handling", []string{
			`{"type":"heartbeat_ack","sessionId":"` + sid + `"}`,
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
		}, false},
		{"busy ack naming another session", []string{
			`{"type":"heartbeat_ack","sessionId":"sess_other","busy":true}`,
			`{"type":"response","sessionId":"` + sid + `","output":[]}`,
		}, false},
		{"no response", []string{`{"type":"heartbeat_ack","sessionId":"` + sid + `","busy":true}`}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := judgeHeartbeatBusy(c.frames, false)
			if (err == nil) != c.pass {
				t.Fatalf("judgeHeartbeatBusy = %v, want pass=%v", err, c.pass)
			}
		})
	}
}

// spec: 15.4.6 (Conformance Test Suite, heartbeat busy), 28.5.3 (CH-MSGSOCK)
//
// diagnosis: a failure means the heartbeat busy check rejects the echo
// reference runtime, whose single-threaded loop answers the heartbeat after
// the response, so the Basic battery fails a conforming serial runtime.
func TestHeartbeatBusyPassesTheEchoRuntime_spec_15_4_6(t *testing.T) {
	detail, err := checkHeartbeatBusy(echoBinary, 30*time.Second, false)
	if err != nil {
		t.Fatalf("echo runtime failed the heartbeat busy check: %q, %v", detail, err)
	}
	if !strings.Contains(detail, "busy") {
		t.Fatalf("detail %q does not describe the check", detail)
	}
}
