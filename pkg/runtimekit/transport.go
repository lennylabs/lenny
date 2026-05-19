// SPDX-License-Identifier: MIT

// Package runtimekit provides the transport selection a §4.7 reference
// runtime uses to run under either deployment model.
//
// A §15.4.1 runtime exchanges newline-delimited JSON frames with the
// adapter. The framing is identical in both deployment models; only the
// byte transport differs:
//
//   - stdin/stdout: the runtime reads os.Stdin and writes os.Stdout.
//     This is the §15.4 contract transport — the conformance harness
//     (cmd/lenny-compliance) and the Tier 3 contract tests exec the
//     runtime binary directly and drive it over its standard streams.
//
//   - abstract Unix socket: in the §4.7 sidecar deployment model the
//     adapter runs as a separate container and listens on an abstract
//     Unix socket. The runtime container has no stdin attached, so the
//     runtime dials the socket and exchanges the same JSONL frames over
//     it. The adapter passes the socket name to the runtime container in
//     the LENNY_ADAPTER_SOCKET environment variable.
//
// Open resolves the transport from the environment: when
// LENNY_ADAPTER_SOCKET is set it dials that socket, otherwise it
// returns os.Stdin/os.Stdout. A reference runtime calls Open once at
// startup and runs its existing JSONL loop against the returned
// io.Reader and io.Writer unchanged.
package runtimekit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// SocketEnvVar is the environment variable the §4.7 adapter sets on the
// runtime container in the sidecar deployment model. Its value is the
// adapter's abstract Unix socket name (a Linux abstract address begins
// with "@"). When the variable is unset or empty the runtime uses the
// §15.4 stdin/stdout transport.
const SocketEnvVar = "LENNY_ADAPTER_SOCKET"

// dialTimeout bounds how long DialSocket waits for the adapter's
// listener to accept. The adapter spawns the runtime container after it
// has already bound the socket (§4.7 startup sequence), but the runtime
// process may still race the listener; a bounded retry absorbs that.
const dialTimeout = 5 * time.Second

// Transport is a resolved §15.4.1 byte transport: an io.Reader for
// inbound frames, an io.Writer for outbound frames, and a Close that
// releases the underlying connection. For the stdin/stdout transport
// Close is a no-op; for the socket transport it closes the connection.
type Transport struct {
	// Reader carries inbound JSONL frames (adapter → runtime).
	Reader io.Reader
	// Writer carries outbound JSONL frames (runtime → adapter).
	Writer io.Writer
	// Socket reports whether the transport is the §4.7 abstract Unix
	// socket. A reference runtime keys diagnostics on this; the JSONL
	// loop itself is identical for both transports.
	Socket bool

	closer io.Closer
}

// Close releases the transport. For the socket transport it closes the
// connection; for stdin/stdout it is a no-op.
func (t *Transport) Close() error {
	if t.closer == nil {
		return nil
	}
	return t.closer.Close()
}

// Open resolves the §15.4.1 transport from the process environment. When
// SocketEnvVar names an adapter socket it dials that socket and returns
// a socket-backed Transport; otherwise it returns a Transport over
// os.Stdin and os.Stdout. ctx bounds the socket dial.
func Open(ctx context.Context) (*Transport, error) {
	socket := strings.TrimSpace(os.Getenv(SocketEnvVar))
	if socket == "" {
		return &Transport{Reader: os.Stdin, Writer: os.Stdout}, nil
	}
	conn, err := DialSocket(ctx, socket)
	if err != nil {
		return nil, fmt.Errorf("runtimekit: dial adapter socket %q: %w", socket, err)
	}
	return &Transport{Reader: conn, Writer: conn, Socket: true, closer: conn}, nil
}

// DialSocket dials the adapter's runtime socket. A Linux abstract
// address begins with "@"; the kernel resolves it after the leading
// byte is replaced with NUL. A filesystem path is dialed as-is so the
// helper also works on platforms without abstract-socket support. The
// dial is retried within dialTimeout to absorb a startup race with the
// adapter's listener.
func DialSocket(ctx context.Context, socket string) (net.Conn, error) {
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	deadline := time.Now().Add(dialTimeout)
	var lastErr error
	for {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), lastErr)
		}
	}
}
