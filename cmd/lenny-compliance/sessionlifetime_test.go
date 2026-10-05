// SPDX-License-Identifier: MIT

// Tests for the Basic session lifetime category. The pass arms run the
// echo reference runtime built by TestMain and the stub runtime; the
// reject arm runs the stub configured to exit on its first session_end.
package main

import (
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
