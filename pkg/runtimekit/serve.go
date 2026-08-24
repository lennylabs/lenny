// SPDX-License-Identifier: MIT

package runtimekit

import (
	"context"
	"io"
	"time"
)

// Loop is one §28.5.3 JSONL dispatch loop over a resolved transport. It
// returns when the adapter closes the inbound stream or when the loop
// fails.
type Loop func(ctx context.Context, in io.Reader, out io.Writer) error

// reconnectPause bounds how fast Serve reopens the §4.7 sidecar socket
// after a session's connection ends. An adapter that accepted and closed
// immediately would otherwise spin the loop; the pause costs nothing on
// the recycle boundary, where the next session's Start is seconds away.
const reconnectPause = 100 * time.Millisecond

// Serve resolves the §28.5.3 transport and runs loop over it.
//
// Under the §15.4 stdin/stdout transport there is one loop over the
// process's standard streams and Serve returns when that loop ends.
//
// Under the §4.7 sidecar socket transport Serve runs one loop per adapter
// connection. The adapter closes the connection when it closes an ending
// session's runtime, and §5.2 has the pod keep its process alive across
// the recycle boundary and reuse it for the next session. The runtime
// container is started by the kubelet rather than by the adapter, and a
// pool pod's restart policy never restarts it, so a runtime that exited on
// that close would leave the recycled pod with no runtime to talk to and
// the next session's start would find nothing on the socket. Serve
// therefore redials the adapter's socket and runs a fresh loop for the
// next session, which carries no state from the session that ended.
//
// The pod's own teardown ends the loop: once the adapter process is gone
// its listener is unbound, the redial exhausts its retry window, and Serve
// returns nil so the runtime exits cleanly. A loop that fails is returned
// to the caller unchanged, so the §15.4 exit codes are unaffected.
//
// spec: §4.7 (sidecar deployment model); §5.2 (recycle lifecycle);
// §15.4 (runtime exit codes); §28.5.3 (JSONL framing).
func Serve(ctx context.Context, loop Loop) error {
	transport, err := Open(ctx)
	if err != nil {
		return err
	}
	for {
		runErr := loop(ctx, transport.Reader, transport.Writer)
		socket := transport.Socket
		// The transport is finished either way: a socket connection the
		// adapter has already closed, or the process's own standard
		// streams, whose Close is a no-op.
		_ = transport.Close()
		if runErr != nil {
			return runErr
		}
		if !socket || ctx.Err() != nil {
			return nil
		}
		select {
		case <-time.After(reconnectPause):
		case <-ctx.Done():
			return nil
		}
		next, err := Open(ctx)
		if err != nil {
			// The adapter is gone with the pod: there is no next session
			// to serve, so this is the clean exit rather than a failure.
			return nil
		}
		transport = next
	}
}
