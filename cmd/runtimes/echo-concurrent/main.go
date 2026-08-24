// SPDX-License-Identifier: MIT

// Command echo-concurrent is the Basic-level reference runtime for a
// concurrent-workspace pod whose pool sets sessionPolicy.maxConcurrentSessions > 1.
// It is the working multiplexing backend the per-slot integration and
// conformance tiers exercise.
//
// A concurrent pool multiplexes several simultaneous sessions onto one
// pod, each in its own slot. Every session is bound to a slot on every
// pod, the gateway mints the slot at claim time, and the adapter stamps
// the per-session identifier on every session-scoped frame it writes; the
// runtime implements a dispatch loop keyed on sessionId, demultiplexing
// the sessions over the single stdin channel. echo-concurrent implements
// that loop:
//
//   - Each session-scoped frame's `sessionId` field selects a session.
//     Frames for distinct sessions are demultiplexed to mutex-guarded
//     per-session state, each with its own §28.5.3 echo loop and its own
//     sequence counter, so two sessions never share output ordering.
//   - A session's cwd derives from its identifier as
//     /workspace/slots/{sessionId}/current/, and outbound frames echo the
//     same sessionId so the adapter routes each response back to the
//     session that sent it.
//   - A session-scoped frame carrying no sessionId names no session this
//     runtime may act for, and the loop exits with the protocol-error
//     code rather than routing it to a pod-global session.
//   - The protocol-level frames are pod-global and carry no per-session
//     identifier: the front loop answers `heartbeat` with an unstamped
//     `heartbeat_ack` and forwards `shutdown` to every live session.
//
// The per-frame `message` behavior is reused from pkg/runtimekit/echocore
// as a composable primitive: each session runs an independent
// echocore.Run loop fed by a per-session pipe. echo-concurrent does not
// modify echocore; it adds the sessionId-keyed front loop around it, so
// the multiplexing stays out of the shared loop and the single-runtime
// binaries (echo, streaming-echo, echo-embedded).
//
// Transport (spec §4.7 deployment models): identical to cmd/runtimes/echo.
// When LENNY_ADAPTER_SOCKET is set echo-concurrent dials that abstract
// Unix socket; otherwise it reads stdin/stdout. The sessionId
// multiplexing rides the single connection runtimekit.Open returns.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error
// (malformed inbound JSONL), 137 SIGKILL (set by the OS).
//
// spec: §5.2; §6.4; §28.5.3.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

func main() {
	// §4.7: resolve the transport. LENNY_ADAPTER_SOCKET selects the
	// sidecar-pod abstract socket; its absence selects stdin/stdout. The
	// sessionId multiplexing is identical over either transport.
	transport, err := runtimekit.Open(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitRuntimeError)
	}
	defer transport.Close()

	err = run(context.Background(), transport.Reader, transport.Writer, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe protocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}
