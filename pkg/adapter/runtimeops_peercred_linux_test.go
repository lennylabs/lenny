// SPDX-License-Identifier: MIT

//go:build linux

package adapter

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// The real-kernel cases are Linux-only because SO_PEERCRED is a Linux socket
// option; on other platforms checkPeerUID admits every peer.

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-RUNTIMEOPS)
func TestRuntimeOpsRefusesOtherUIDThroughSOPeercred_spec_4_7_11(t *testing.T) {
	_, sock := runRuntimeOps(t, SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1}, func(lc *RuntimeOps) {
		lc.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	})
	requireOpsRefused(t, dialOps(t, sock), "peer running as another UID")
}

// spec: 4.7.11 (Separate UIDs and connection authentication)
func TestRuntimeOpsFailsClosedWithoutAnAgentUID_spec_4_7_11(t *testing.T) {
	// The zero posture names no agent UID and admits only UID 0.
	_, sock := runRuntimeOps(t, SocketPeerAuth{}, func(lc *RuntimeOps) {
		lc.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	})
	fr := dialOps(t, sock)
	if os.Getuid() == 0 {
		requireOpsServed(t, fr, "root peer under the zero posture")
		return
	}
	requireOpsRefused(t, fr, "unprivileged peer under the zero posture")
}
