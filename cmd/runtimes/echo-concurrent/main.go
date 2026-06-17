// SPDX-License-Identifier: MIT

// Command echo-concurrent is the Basic-level reference runtime for a
// concurrent-workspace pod whose pool sets sessionPolicy.maxConcurrentSessions > 1.
// It is the working multiplexing backend the per-slot integration and
// conformance tiers exercise.
//
// A concurrent pool multiplexes several simultaneous sessions onto one
// pod, each in its own slot. The adapter assigns a slotId per slot and
// tags every binary-protocol frame with it; the runtime implements a
// dispatch loop keyed on slotId, demultiplexing the slots over the single
// stdin channel. echo-concurrent implements that loop:
//
//   - Each inbound frame's optional `slotId` field selects a slot. Frames
//     for distinct slotIds are demultiplexed to mutex-guarded per-slot
//     state, each slot with its own §15.4.1 echo loop and its own
//     sequence counter, so slot 01 and slot 02 never share output ordering.
//   - When slotId is present the slot's cwd derives from slotId as
//     /workspace/slots/{slotId}/current/, and outbound frames echo the
//     same slotId so the adapter routes each response back to the
//     originating slot.
//   - When slotId is absent the frame takes the single-session whole-pod
//     path. Runtimes on a maxConcurrentSessions: 1 pod never see slotId,
//     so a frame without one is a single-session frame and echo-concurrent
//     also serves a maxConcurrentSessions: 1 pod correctly.
//
// The per-frame `message`/`heartbeat`/`shutdown` behavior is reused from
// pkg/runtimekit/echocore as a composable primitive: each slot (and the
// default whole-pod session) runs an independent echocore.Run loop fed by
// a per-slot pipe. echo-concurrent does not modify echocore; it adds the
// slotId-keyed front loop around it, so per-slot multiplexing stays out
// of the shared loop and the single-session runtimes (echo, streaming-echo,
// echo-embedded).
//
// Transport (spec §4.7 deployment models): identical to cmd/runtimes/echo.
// When LENNY_ADAPTER_SOCKET is set echo-concurrent dials that abstract
// Unix socket; otherwise it reads stdin/stdout. The slotId multiplexing
// rides the single connection runtimekit.Open returns.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol error
// (malformed inbound JSONL), 137 SIGKILL (set by the OS).
//
// spec: §5.2 line 509 (slotId multiplexing over stdin, dispatch loop keyed
// on slotId), §15.4.1 line 1459 (dispatch loop keyed on slotId over a
// single stdin channel), §6.4 line 384 (per-slot cwd, no global
// /workspace/current when maxConcurrentSessions > 1).
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
	// slotId multiplexing is identical over either transport.
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
