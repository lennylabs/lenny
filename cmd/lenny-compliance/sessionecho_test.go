// SPDX-License-Identifier: MIT

// Tests for the Basic-level per-session echo obligation: a Basic-level
// runtime echoes the per-session identifier the adapter handed it on the
// `response` it emits. The pass arm runs against the echo reference
// runtime built by TestMain; the reject arms use fake binaries that omit
// or alter the identifier, or still emit the retired `slotId` key
// alongside it, which is what the check exists to catch.
package main

import (
	"strings"
	"testing"
	"time"
)

// spec: 28.5.3 (the runtime echoes the identifier it was handed), 15.4.3
// (the Basic-level integration row excepts the per-session identifier
// from the fields a Basic-level runtime may ignore)
//
// diagnosis: a failure means the bundled Basic-level reference runtime no
// longer echoes the per-session identifier, so the battery certifies a
// runtime whose `response` frames the adapter rejects on any pod holding
// more than one slot.
func TestResponseEchoesSessionIDPasses_spec_28_5_3(t *testing.T) {
	detail, err := checkResponseEchoesSessionID(echoBinary, 10*time.Second, true)
	if err != nil {
		t.Fatalf("echo runtime failed the per-session echo check: %v", err)
	}
	if !strings.Contains(detail, complianceSessionID) {
		t.Errorf("pass detail %q must name the echoed identifier", detail)
	}
}

// spec: 28.5.3 (the runtime echoes the identifier it was handed), 15.4.3
//
// diagnosis: a failure means the Basic-level check passes a runtime that
// does not echo the identifier, so the obligation is unverified by the
// battery that certifies a third-party runtime against the level it
// declares.
func TestResponseEchoesSessionIDRejects_spec_28_5_3(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a runtime that omits the identifier",
			body: `read line; printf '%s\n' '{"type":"response","output":[{"type":"text","inline":"pong"}]}'`,
			want: "carries no sessionId",
		},
		{
			name: "a runtime that emits the retired slotId alongside the session",
			body: `read line; printf '%s\n' '{"type":"response","sessionId":"sess_01J9X0ZW1ZF7K8Q1V2T3M4N5S1","slotId":"slot_01","output":[{"type":"text","inline":"pong"}]}'`,
			want: "retired slotId key",
		},
		{
			name: "a runtime that alters the identifier",
			body: `read line; printf '%s\n' '{"type":"response","sessionId":"sess_other","output":[{"type":"text","inline":"pong"}]}'`,
			want: "want the inbound",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := writeStallScript(t, "session-echo", tc.body)
			detail, err := checkResponseEchoesSessionID(bin, 10*time.Second, false)
			if err == nil {
				t.Fatalf("the check passed %s: %q", tc.name, detail)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("failure %q must explain the defect (%q)", err.Error(), tc.want)
			}
		})
	}
}
