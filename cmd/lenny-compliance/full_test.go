// SPDX-License-Identifier: MIT

// Tests for the Full-level battery. The fake adapter writes the
// per-session credential file it names, and every Full check that writes a
// session-scoped CH-RUNTIMEOPS frame first reads the session_started that
// answers its session_start. The reject cases drive the stub runtime in
// stub_test.go, each valid up to its one defect, and each also runs the
// stub with the defect removed and asserts the check passes, so a case
// whose stub fails for another reason fails its test.
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testAckWait is the short session_started wait the tests drive the Full
// checks with, in place of the battery's adapter-default wait.
const testAckWait = 500 * time.Millisecond

// spec: 4.7.11 (item 4, runtime credential file contract), 6.1
// (per-session credential file)
//
// A harness that names a credential file it never writes lets the
// rotation check pass for a runtime that never opens the file, and a
// bundle in a layout the SDKs do not decode makes every conforming runtime
// report a read failure.
func TestFakeAdapterWritesTheCredentialFileItNames(t *testing.T) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		t.Fatalf("newFakeAdapter: %v", err)
	}
	defer cleanup()

	want := filepath.Join(fa.dir, "run", "lenny", "slots", complianceSessionID, "credentials.json")
	if fa.credentialsPath != want {
		t.Fatalf("credential path = %q, want the per-session slot path %q", fa.credentialsPath, want)
	}
	body, err := os.ReadFile(fa.credentialsPath)
	if err != nil {
		t.Fatalf("read the credential file: %v", err)
	}
	var bundle struct {
		Providers []struct {
			LeaseID            string          `json:"leaseId"`
			Provider           string          `json:"provider"`
			DeliveryMode       string          `json:"deliveryMode"`
			MaterializedConfig json.RawMessage `json:"materializedConfig"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &bundle); err != nil {
		t.Fatalf("decode credential bundle: %v", err)
	}
	if len(bundle.Providers) != 1 {
		t.Fatalf("credential bundle providers = %+v, want one entry", bundle.Providers)
	}
	p := bundle.Providers[0]
	if p.Provider != "anthropic" || p.LeaseID == "" || p.DeliveryMode != "direct" || len(p.MaterializedConfig) == 0 {
		t.Fatalf("providers[0] = %+v, want an anthropic direct entry with a lease and a materializedConfig", p)
	}
}

// sessionScopedChecks are the Full checks that write a session-scoped
// CH-RUNTIMEOPS frame, plus the CH-RUNTIMEOPS opening check, which reads
// the acknowledgement the same way.
var sessionScopedChecks = []struct {
	name string
	fn   fullCheck
}{
	{"runtime_ops_handshake", checkRuntimeOpsHandshake},
	{"checkpoint_quiesce_resume", checkCheckpointQuiesce},
	{"interrupt_acknowledgement", checkInterruptAck},
	{"credential_rotation_no_disruption", checkCredentialRotation},
	{"deadline_signal_handling", checkDeadlineSignal},
}

// spec: 15.4.6 (Test categories by integration level), 28.5.3 (CH-MSGSOCK
// Outbound: session_started)
//
// diagnosis: a failure means a Full check wrote its session-scoped
// CH-RUNTIMEOPS frame without reading the runtime's session_started, or
// failed a runtime that never acknowledges with an error a caller cannot
// tell apart from the end of stdout.
func TestFullChecksFailWhenSessionStartedNeverArrives_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubAckEnv: "never"})
	for _, c := range sessionScopedChecks {
		t.Run(c.name, func(t *testing.T) {
			detail, err := c.fn(bin, testAckWait)
			if !errors.Is(err, errSessionStartedWait) {
				t.Fatalf("%s = (%q, %v), want an error wrapping errSessionStartedWait", c.name, detail, err)
			}
			var r Report
			r.recordCheck(c.name, "15.4.6", detail, err)
			if r.Checks[0].Pass || !strings.Contains(r.Checks[0].Detail, err.Error()) {
				t.Fatalf("recorded check = %+v, want Pass=false with the expiry error in Detail", r.Checks[0])
			}
		})
	}
}

// spec: 15.4.6 (Test categories by integration level), 28.5.3 (CH-RUNTIMEOPS
// Messages)
//
// diagnosis: a failure means a Full check writes its session-scoped frame
// before its session_started read succeeds (the stub exits when a frame
// precedes its delayed acknowledgement), or rejects a runtime that
// acknowledges within the wait.
func TestFullChecksWaitForADelayedSessionStarted_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubAckEnv: "delayed", stubAckDelayEnv: "200ms"})
	for _, c := range sessionScopedChecks {
		t.Run(c.name, func(t *testing.T) {
			if detail, err := c.fn(bin, testAckWait); err != nil {
				t.Fatalf("%s failed a runtime that acknowledges within the wait: %q, %v", c.name, detail, err)
			}
		})
	}
}

// spec: 15.4.6 (CH-RUNTIMEOPS opening), 28.5.3 (CH-MSGSOCK Outbound:
// session_started)
//
// diagnosis: a failure means the CH-RUNTIMEOPS opening check passes a
// runtime that completes the capability handshake but answers its
// session_start with an error or for another start, or fails a runtime
// that acknowledges correctly.
func TestRuntimeOpsHandshakeRequiresTheMatchingAcknowledgement_spec_15_4_6(t *testing.T) {
	cases := []struct {
		ack     string
		wantErr string
	}{
		{ack: "error", wantErr: "carries error RUNTIME_ERROR"},
		{ack: "wrongstart", wantErr: "no session_started"},
		{ack: "immediate"},
	}
	for _, tc := range cases {
		t.Run(tc.ack, func(t *testing.T) {
			bin := setStub(t, map[string]string{stubAckEnv: tc.ack})
			detail, err := checkRuntimeOpsHandshake(bin, testAckWait)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("the check failed a runtime that acknowledges its start: %q, %v", detail, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkRuntimeOpsHandshake = (%q, %v), want an error naming %q", detail, err, tc.wantErr)
			}
		})
	}
}

// spec: 15.4.6 (deadline signal handling), 4.7.10 (Runtime process lifetime)
//
// diagnosis: a failure means the deadline check passes a runtime that
// exits on deadline_approaching, including one that answers the heartbeat
// first and exits right after, or writes a second response for the
// session, or fails a runtime that answers its message once and keeps
// running.
func TestDeadlineSignalCheckRejectsExitAndSecondResponse_spec_15_4_6(t *testing.T) {
	cases := []struct {
		mode    string
		wantErr string
	}{
		{mode: "exit", wantErr: "exited after deadline_approaching"},
		{mode: "exitafterack", wantErr: "exited after deadline_approaching"},
		{mode: "second", wantErr: "second response"},
		{mode: "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			bin := setStub(t, map[string]string{stubDeadlineEnv: tc.mode})
			detail, err := checkDeadlineSignal(bin, testAckWait)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("the check failed a runtime that answers once and keeps running: %q, %v", detail, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkDeadlineSignal = (%q, %v), want an error naming %q", detail, err, tc.wantErr)
			}
		})
	}
}

// spec: 15.4.6 (deadline signal handling), 28.5.3 (CH-MSGSOCK Outbound:
// status)
//
// diagnosis: a failure means the deadline check rejects a runtime that
// writes the optional outbound status frame before its response or its
// heartbeat_ack, or passes such a runtime when it writes a second response
// after deadline_approaching.
func TestDeadlineSignalCheckSkipsStatusFrames_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubStatusEnv: "1"})
	if detail, err := checkDeadlineSignal(bin, testAckWait); err != nil {
		t.Fatalf("the check failed a runtime that writes status before its response: %q, %v", detail, err)
	}
	bin = setStub(t, map[string]string{stubStatusEnv: "1", stubDeadlineEnv: "second"})
	if detail, err := checkDeadlineSignal(bin, testAckWait); err == nil || !strings.Contains(err.Error(), "second response") {
		t.Fatalf("checkDeadlineSignal = (%q, %v), want an error naming a second response", detail, err)
	}
}

// spec: 15.4.6 (credential rotation handling), 28.5.3 (CH-RUNTIMEOPS
// credentials_rotated)
//
// diagnosis: a failure means the rotation check passes a runtime that
// acknowledges credentials_rotated without re-reading the file it names,
// or fails a runtime that re-reads it.
func TestCredentialRotationCheckRequiresTheReread_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubNoRereadEnv: "1"})
	_, err := checkCredentialRotation(bin, testAckWait)
	if !errors.Is(err, errNoCredentialReread) {
		t.Fatalf("checkCredentialRotation = %v, want an error wrapping errNoCredentialReread", err)
	}
	bin = setStub(t, nil)
	if detail, err := checkCredentialRotation(bin, testAckWait); err != nil {
		t.Fatalf("the check failed a runtime that re-reads its credential file: %q, %v", detail, err)
	}
}

// spec: 15.4.6 (Conformance Test Suite)
//
// diagnosis: a failure means the Full battery dropped or renamed a
// category, which changes the check names lenny runtime validate maps to
// integration levels.
func TestFullBatteryRunsEveryFullCategory_spec_15_4_6(t *testing.T) {
	var names []string
	for _, c := range fullCases() {
		names = append(names, c.name)
	}
	want := "runtime_ops_handshake checkpoint_quiesce_resume interrupt_acknowledgement credential_rotation_no_disruption deadline_signal_handling"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("Full categories = %s, want %s", got, want)
	}
}
