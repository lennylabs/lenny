// SPDX-License-Identifier: MIT

// Tests for the §15.4.6 Basic-conformance deadline enforcement: the
// heartbeat ack must arrive within 10s (15.4-MED-022) and the binary
// must exit cleanly before the shutdown deadline elapses (15.4-MED-023).
// The pass paths run against the echo reference runtime built by
// TestMain; the fail paths use a fake binary that stalls past the
// deadline so the boundary is exercised fast.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeStallScript writes an executable /bin/sh script that runs body and
// returns its path. Skips on platforms without a POSIX shell.
func writeStallScript(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stall-script fixtures require a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stall script: %v", err)
	}
	return path
}

// TestHeartbeatAckWithinDeadline_spec_15_4_6_2406 asserts the echo
// reference runtime acks a heartbeat within the 10s deadline.
func TestHeartbeatAckWithinDeadline_spec_15_4_6_2406(t *testing.T) {
	detail, err := checkHeartbeatAck(echoBinary, 10*time.Second, true)
	if err != nil {
		t.Fatalf("echo runtime failed heartbeat deadline check: %v", err)
	}
	if detail == "" {
		t.Error("expected a non-empty pass detail")
	}
}

// TestHeartbeatAckExceedsDeadline_spec_15_4_6_2406 asserts a runtime that
// never acks within the (shrunk) deadline fails. A small caller timeout
// shrinks the effective deadline so the test stays fast.
func TestHeartbeatAckExceedsDeadline_spec_15_4_6_2406(t *testing.T) {
	// Reads nothing, never acks, just stalls — driveAdapter force-kills it
	// at the deadline and the check reports the missed ack.
	stall := writeStallScript(t, "slow-heartbeat", "sleep 5")
	if _, err := checkHeartbeatAck(stall, 200*time.Millisecond, false); err == nil {
		t.Fatal("expected the heartbeat deadline check to fail for a non-acking runtime")
	}
}

// TestShutdownWithinDeadline_spec_15_4_6_2407 asserts the echo reference
// runtime exits cleanly before the shutdown deadline elapses.
func TestShutdownWithinDeadline_spec_15_4_6_2407(t *testing.T) {
	detail, err := runShutdownDeadlineCheck(echoBinary, shutdownDeadlineMs)
	if err != nil {
		t.Fatalf("echo runtime failed shutdown deadline check: %v", err)
	}
	if detail == "" {
		t.Error("expected a non-empty pass detail")
	}
}

// TestShutdownExceedsDeadline_spec_15_4_6_2407 asserts a runtime that
// ignores the shutdown deadline and hangs is force-killed and fails. A
// small deadline keeps the test fast.
func TestShutdownExceedsDeadline_spec_15_4_6_2407(t *testing.T) {
	// Ignores stdin entirely and sleeps well past the deadline+slack, so
	// driveAdapter kills it (non-zero exit) and the check fails.
	stall := writeStallScript(t, "hang-on-shutdown", "sleep 10")
	if _, err := runShutdownDeadlineCheck(stall, 200); err == nil {
		t.Fatal("expected the shutdown deadline check to fail for a hanging runtime")
	}
}
