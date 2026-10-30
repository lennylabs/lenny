// SPDX-License-Identifier: MIT

// Tests for the Basic session lifetime category. The pass arms run the
// echo reference runtime built by TestMain and the stub runtime; the
// reject arms run the stub configured to exit on its first session_end or
// right after its last response to the concurrent sessions.
package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// spec: 15.4.6 (Conformance Test Suite, Basic session lifetime), 4.7.10
// (Runtime process lifetime)
//
// diagnosis: a failure means the session lifetime check rejects a runtime
// that serves sequential and concurrent sessions on one process, so the
// Basic battery fails the reference runtimes.
func TestSessionLifetimePasses_spec_15_4_6(t *testing.T) {
	if detail, err := checkSessionLifetime(echoBinary, 30*time.Second, false); err != nil {
		t.Fatalf("echo runtime failed the session lifetime check: %q, %v", detail, err)
	}
	bin := setStub(t, nil)
	if detail, err := checkSessionLifetime(bin, 30*time.Second, false); err != nil {
		t.Fatalf("stub runtime failed the session lifetime check: %q, %v", detail, err)
	}
}

// spec: 15.4.6 (Conformance Test Suite, Basic session lifetime), 4.7.10
// (Runtime process lifetime), 28.5.3 (CH-MSGSOCK Inbound: session_end)
//
// diagnosis: a failure means the session lifetime check passes a runtime
// that exits at a session_end, so the battery certifies a runtime that
// takes every other session on its pod down with the one that ended.
func TestSessionLifetimeRejectsARuntimeThatExitsOnSessionEnd_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubExitOnEndEnv: "1"})
	detail, err := checkSessionLifetime(bin, 30*time.Second, false)
	if err == nil {
		t.Fatalf("the check passed a runtime that exits on session_end: %q", detail)
	}
	if !strings.Contains(err.Error(), "closed stdout before the harness closed stdin") {
		t.Fatalf("failure %q must say the runtime exited before stdin closed", err)
	}
}

// lifetimeResponseCount is the number of responses the session lifetime
// check reads: one each for the sequential sessions A and B, and two each
// for the concurrent sessions C and D.
const lifetimeResponseCount = 6

// spec: 15.4.6 (Conformance Test Suite, Basic session lifetime), 4.7.10
// (Runtime process lifetime)
//
// diagnosis: a failure means the session lifetime check passes a runtime
// that exits right after answering the concurrent sessions while they are
// still open, so the battery certifies a runtime that ends its process
// before the harness closes stdin.
func TestSessionLifetimeRejectsARuntimeThatExitsAfterTheConcurrentSessions_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubExitAfterResponsesEnv: strconv.Itoa(lifetimeResponseCount)})
	detail, err := checkSessionLifetime(bin, 30*time.Second, false)
	if err == nil {
		t.Fatalf("the check passed a runtime that exits after its last concurrent response: %q", detail)
	}
	for _, want := range []string{"after the concurrent sessions", "closed stdout before the harness closed stdin"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("failure %q must contain %q", err, want)
		}
	}

	// The same stub without the defect, exiting only after a response the
	// check never asks for, passes: the rejection above is the exit itself.
	bin = setStub(t, map[string]string{stubExitAfterResponsesEnv: strconv.Itoa(lifetimeResponseCount + 1)})
	if detail, err := checkSessionLifetime(bin, 30*time.Second, false); err != nil {
		t.Fatalf("a stub that stays alive failed the session lifetime check: %q, %v", detail, err)
	}
}

// spec: 15.4.6 (Conformance Test Suite, Basic session lifetime), 28.5.3
// (CH-MSGSOCK Outbound: status)
//
// diagnosis: a failure means the session lifetime check rejects a runtime
// that writes the optional outbound status frame before a response or a
// heartbeat_ack, so the Basic battery fails a conformant runtime that
// reports progress while it serves a message.
func TestSessionLifetimeSkipsStatusFramesBeforeResponsesAndAcks_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubStatusEnv: "1"})
	if detail, err := checkSessionLifetime(bin, 30*time.Second, false); err != nil {
		t.Fatalf("a runtime that writes status frames failed the session lifetime check: %q, %v", detail, err)
	}
}

// spec: 15.4.6 (Conformance Test Suite, Basic session lifetime), 4.7.10
// (Runtime process lifetime)
//
// diagnosis: a failure means the session lifetime check rejects a runtime
// that answers every message with an error-carrying response and stays
// alive, so the Basic battery fails a conformant runtime whose model call
// fails under the check's session_start, which carries no llm and no
// credentialsPath.
func TestSessionLifetimeCountsErrorResponses_spec_15_4_6(t *testing.T) {
	bin := setStub(t, map[string]string{stubResponseErrorEnv: "1"})
	if detail, err := checkSessionLifetime(bin, 30*time.Second, false); err != nil {
		t.Fatalf("a runtime that answers with error responses failed the session lifetime check: %q, %v", detail, err)
	}
}
