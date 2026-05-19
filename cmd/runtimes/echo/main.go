// SPDX-License-Identifier: MIT

// Command echo is the Basic-level reference adapter binary for the §4.7
// sidecar deployment model. It exchanges JSON Lines with the adapter,
// conforming to the contract published in
// schemas/lenny-adapter-jsonl.schema.json.
//
// The Basic level (spec §15.4.3) requires only the JSONL channel. No
// adapter manifest, no MCP, no lifecycle channel. Echo serves three
// purposes:
//
//   - It is the reference implementation for runtime authors writing
//     their own Basic-level sidecar runtimes.
//   - It is the target the Phase 2 contract tests
//     (tests/tier3_contract/adapter_jsonl/) exec to verify protocol
//     conformance end-to-end.
//   - It is the binary the conformance harness (cmd/lenny-compliance/)
//     exercises during its --level=basic battery.
//
// Transport (spec §4.7 deployment models): the §15.4.1 JSONL framing is
// identical under both §4.7 sidecar transports; only the byte channel
// differs. When LENNY_ADAPTER_SOCKET is set — the §4.7 sidecar pod
// model, where the adapter runs in a separate container — echo dials
// that abstract Unix socket and runs its loop over the connection.
// Otherwise echo uses stdin/stdout, the §15.4 contract transport the
// conformance harness and the contract tests drive directly. The
// transport is selected by the shared runtimekit helper; the §15.4.1
// loop in pkg/runtimekit/echocore is identical for both.
//
// The same echocore loop runs in cmd/runtimes/echo-embedded under the
// §4.7 embedded deployment model, where the adapter is linked into the
// runtime image and the loop runs in-process. echo is the sidecar half
// of that pair.
//
// Exit codes (spec §15.4):
//
//	0   success
//	1   runtime error (unexpected internal failure)
//	2   protocol error (malformed inbound JSONL the adapter cannot recover from)
//	137 SIGKILL (set by the OS, not by this binary)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

func main() {
	// §4.7: resolve the transport. LENNY_ADAPTER_SOCKET selects the
	// sidecar-pod abstract socket; its absence selects stdin/stdout.
	transport, err := runtimekit.Open(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitRuntimeError)
	}
	defer transport.Close()

	err = echocore.Run(context.Background(), transport.Reader, transport.Writer, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe echocore.ProtocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}
