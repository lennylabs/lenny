// SPDX-License-Identifier: MIT

//go:build linux

package adapter_test

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// The real-kernel refusal case is Linux-only because SO_PEERCRED is a Linux
// socket option; on other platforms checkPeerUID admits every peer.

// spec: 4.7.11 (Separate UIDs and connection authentication), 28.5.3
// (CH-MSGSOCK)
func TestSocketRuntimeRefusesOtherUIDThroughSOPeercred_spec_4_7_11(t *testing.T) {
	sp, err := newSocketRuntime(t, runtimeSocketAddr(t),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid()) + 1})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 300 * time.Millisecond
	sp.SetLoggerForTest(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	started := startAsync(sp, "s1")
	peer := dialRuntimeSocket(t, sp.SocketPath())
	defer peer.Close()
	requireRefused(t, peer, "peer running as another UID")
	if err := <-started; err == nil {
		t.Fatal("Start accepted a peer whose SO_PEERCRED UID is not the agent UID")
	}
}
