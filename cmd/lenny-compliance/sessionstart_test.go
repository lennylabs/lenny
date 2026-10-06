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
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
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

// spec: 15.4.6 (Test categories by integration level), 28.5.3 (CH-MSGSOCK,
// Outbound: session_started)
//
// awaitSessionStarted skips every frame other than the session_started
// that carries the session and the start, including an acknowledgement
// for another start; it fails when the matching frame carries error, when
// stdout ends first, and, distinguishably under errors.Is, when the wait
// elapses first.
func TestAwaitSessionStartedReadsTheMatchingAcknowledgement_spec_15_4_6(t *testing.T) {
	ack := func(session, start, extra string) string {
		return `{"type":"session_started","sessionId":"` + session + `","startId":"` + start + `"` + extra + `}` + "\n"
	}
	cases := []struct {
		name     string
		stdout   string
		wantErr  string
		wantWait bool
	}{
		{
			name:   "the matching acknowledgement after other frames",
			stdout: `{"type":"response","text":"x"}` + "\n" + ack("sess_other", complianceStartID, "") + ack(complianceSessionID, "compliance-0", "") + ack(complianceSessionID, complianceStartID, ""),
		},
		{
			name:    "an acknowledgement carrying error",
			stdout:  ack(complianceSessionID, complianceStartID, `,"error":{"code":"RUNTIME_ERROR","message":"no context"}`),
			wantErr: "carries error RUNTIME_ERROR",
		},
		{name: "stdout ends first", stdout: ack(complianceSessionID, "compliance-0", ""), wantErr: "closed stdout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newFrameReader(bufio.NewScanner(strings.NewReader(tc.stdout)))
			defer r.close()
			err := awaitSessionStarted(r, complianceSessionID, complianceStartID, time.Second)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("awaitSessionStarted: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("awaitSessionStarted err = %v, want one naming %q", err, tc.wantErr)
			}
			if errors.Is(err, errSessionStartedWait) {
				t.Fatalf("awaitSessionStarted err = %v, which reads as an expired wait", err)
			}
		})
	}
}

// spec: 15.4.6 (Test categories by integration level), 28.5.3 (CH-MSGSOCK,
// Outbound: session_started rule 3)
//
// A read that outlasts its wait fails with an error that wraps
// errSessionStartedWait, so a caller tells an expired wait apart from the
// end of stdout; and the battery's wait is the adapter's default
// acknowledgement timeout plus the fixed margin.
func TestAwaitSessionStartedExpires_spec_15_4_6(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	r := newFrameReader(bufio.NewScanner(pr))
	defer r.close()
	err := awaitSessionStarted(r, complianceSessionID, complianceStartID, 50*time.Millisecond)
	if !errors.Is(err, errSessionStartedWait) {
		t.Fatalf("awaitSessionStarted err = %v, want one wrapping errSessionStartedWait", err)
	}
	if got, want := sessionStartedWait(), runtimekit.DefaultSessionStartAckTimeout+time.Second; got != want {
		t.Fatalf("sessionStartedWait = %s, want the adapter default plus one second (%s)", got, want)
	}
}
