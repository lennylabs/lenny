// SPDX-License-Identifier: MIT

// Tests for the session_start prefix the battery writes before a
// session-scoped frame and for the frame readers that skip a
// session_started acknowledgement. A runtime that keeps per-session context
// answers session_start with session_started, so a reader that took the
// first stdout line would assert against the acknowledgement rather than
// the frame its check is about.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start)
//
// The prefix opens the battery's session with the fields the frame
// requires and omits credentialsPath, which the frame never carries as an
// empty string.
func TestSessionStartPrefixOpensTheComplianceSession_spec_28_5_3(t *testing.T) {
	lines := withSessionStart("next")
	if len(lines) != 2 || lines[1] != "next" {
		t.Fatalf("withSessionStart = %q, want the prefix followed by the check's line", lines)
	}
	var f map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &f); err != nil {
		t.Fatalf("prefix is not JSON: %v", err)
	}
	if f["type"] != "session_start" || f["sessionId"] != complianceSessionID || f["startId"] != "compliance-1" {
		t.Fatalf("prefix = %v, want session_start for %s with startId compliance-1", f, complianceSessionID)
	}
	if _, ok := f["credentialsPath"]; ok {
		t.Fatalf("prefix carries credentialsPath %v; the harness provisions no credential file for it", f["credentialsPath"])
	}
	if err := validateJSONLFrame([]byte(lines[0])); err != nil {
		t.Fatalf("prefix does not validate against the published JSONL schema: %v", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Outbound: session_started)
//
// scanFrame skips session_started frames and returns the next frame, and
// reports the end of stdout when nothing else follows.
func TestScanFrameSkipsSessionStarted_spec_28_5_3(t *testing.T) {
	out := `{"type":"session_started","sessionId":"` + complianceSessionID + `","startId":"compliance-1"}` + "\n" +
		`{"type":"response","sessionId":"` + complianceSessionID + `","text":"pong"}` + "\n" +
		`{"type":"session_started","sessionId":"sess_other","startId":"x"}` + "\n"
	s := bufio.NewScanner(strings.NewReader(out))
	line, ok := scanFrame(s)
	if !ok || !strings.Contains(line, `"type":"response"`) {
		t.Fatalf("scanFrame = (%q, %v), want the response after the acknowledgement", line, ok)
	}
	if line, ok := scanFrame(s); ok {
		t.Fatalf("scanFrame = %q, want end of stdout after only a session_started", line)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Outbound: session_started), 15.4.3
//
// diagnosis: a failure means a Basic check reads a runtime's
// session_started acknowledgement as its response, so the battery fails
// every runtime that acknowledges the session it was handed.
func TestBasicCheckSkipsSessionStartedBeforeResponse_spec_28_5_3(t *testing.T) {
	body := `read start; read msg; ` +
		`printf '%s\n' '{"type":"session_started","sessionId":"` + complianceSessionID + `","startId":"compliance-1"}'; ` +
		`printf '%s\n' '{"type":"response","sessionId":"` + complianceSessionID + `","output":[{"type":"text","inline":"pong"}]}'`
	bin := writeStallScript(t, "acks-session", body)
	if _, err := checkResponseEchoesSessionID(bin, 10*time.Second, false); err != nil {
		t.Fatalf("the echo check failed a runtime that acknowledges session_start before its response: %v", err)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS, Messages), 15.4.6
//
// openComplianceSession writes the session_start prefix and one message
// for the session, and returns once the response arrives past any
// session_started. It fails when stdout ends first and when the first
// non-acknowledgement frame is not a response.
func TestOpenComplianceSessionWaitsForTheResponse_spec_28_5_3(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		wantErr string
	}{
		{
			name:   "an acknowledgement then a response",
			stdout: `{"type":"session_started","sessionId":"` + complianceSessionID + `","startId":"compliance-1"}` + "\n" + `{"type":"response","text":"pong"}` + "\n",
		},
		{name: "stdout ends first", stdout: "", wantErr: "closed stdout"},
		{name: "a frame other than a response", stdout: `{"type":"heartbeat_ack"}` + "\n", wantErr: "expected a response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdin strings.Builder
			err := openComplianceSession(&stdin, bufio.NewScanner(strings.NewReader(tc.stdout)), time.Second)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("openComplianceSession: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("openComplianceSession err = %v, want one naming %q", err, tc.wantErr)
			}
			written := strings.Split(strings.TrimSpace(stdin.String()), "\n")
			if len(written) != 2 || written[0] != complianceSessionStart || !strings.Contains(written[1], `"type":"message"`) {
				t.Fatalf("stdin = %q, want the session_start prefix followed by one message", written)
			}
		})
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS, Messages), 15.4.6
//
// openComplianceSession gives up when no response arrives within its
// wait, so a runtime that never answers fails the check rather than
// stalling it.
func TestOpenComplianceSessionTimesOut_spec_28_5_3(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	err := openComplianceSession(io.Discard, bufio.NewScanner(pr), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("openComplianceSession err = %v, want a timeout", err)
	}
}
