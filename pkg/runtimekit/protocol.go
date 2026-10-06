// SPDX-License-Identifier: MIT

package runtimekit

import "time"

// DefaultSessionStartAckTimeout is the default bound on the adapter's wait
// for the runtime's session_started answer to a session_start. The adapter
// waits inside a start's open sequence when the runtime's CH-RUNTIMEOPS
// connection completed its capability handshake before the sequence began,
// and fails the start when the wait ends without the frame. The spec bounds
// the wait without fixing a value, so the adapter exposes it as an
// operator-tunable flag whose default is this constant.
//
// The value covers a loaded runtime's context creation, which includes a
// credential-file read and LLM client construction, and stays below both
// the 10 s ConfigureWorkspace timeout, leaving room to write session_end and
// answer the RPC, and the 10 s graceful window that bounds the hold-timeout
// termination's wait for the slot serialization. A wait that outlasted that
// window would let a hold-timeout cleanup proceed without the serialization
// while an open sequence still waits.
//
// The constant lives in this standard-library-only package rather than in
// pkg/adapter so the conformance harness (cmd/lenny-compliance), a
// standalone binary third-party runtime authors run, bounds its own
// session_started read by the same default without importing the adapter
// and its dependencies.
//
// spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started), §15.4.6
// (Conformance Test Suite).
const DefaultSessionStartAckTimeout = 5 * time.Second
