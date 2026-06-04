// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// maxJSONLFrameBytes is the largest single §15.4.1 JSONL frame the
// sidecar scanner admits. It matches the §15.4.1 line 1548 50 MB
// OutputPart ceiling so a legal large part frames before the gateway's
// ingress check runs. spec: §15.4.1 line 1548. F-15.4.1 (15.4-INFO-031).
const maxJSONLFrameBytes = 50 * 1024 * 1024

// SocketRuntimeProcess is the §4.7 sidecar-model RuntimeProcess: the
// adapter listens on an abstract Unix socket and the runtime — running
// in a separate pod container — dials it and exchanges §15.4.1 JSONL
// frames over the connection. It is the socket counterpart of the
// stdin/stdout SubprocessExecutor: the JSONL framing is identical, only
// the byte transport differs.
//
// The §4.7 deployment model puts the adapter and the runtime in
// separate containers of the same pod. The containers share a network
// namespace, so an abstract Unix socket the adapter binds is reachable
// from the runtime container with no shared filesystem path. The
// adapter never spawns the runtime in this model — the kubelet starts
// the runtime container — so SocketRuntimeProcess binds the socket at
// construction time and waits for the runtime to connect on Start.
//
// For the single-process developer loop (cmd/lenny-adapter
// --runtime-bin), SpawnPath may be set: Start then execs that binary
// with LENNY_ADAPTER_SOCKET pointing at the bound socket, so one host
// can exercise the sidecar transport without a pod.
//
// SocketRuntimeProcess drives one session at a time, matching the §6.1
// one-session-per-pod model and the lifetime of a single pod.
type SocketRuntimeProcess struct {
	listener net.Listener

	// SpawnPath, when non-empty, is a runtime binary Start execs with
	// LENNY_ADAPTER_SOCKET set. Empty means the runtime connects on its
	// own — the §4.7 separate-container pod model.
	SpawnPath string

	// AcceptTimeout bounds how long Start waits for the runtime to
	// connect. Zero defaults to 30s.
	AcceptTimeout time.Duration

	mu      sync.Mutex
	session string
	conn    net.Conn
	scanner *bufio.Scanner
	cmd     *exec.Cmd
}

// NewSocketRuntimeProcess binds the adapter's runtime socket and returns
// a RuntimeProcess that bridges the runtime over it. socket is a
// filesystem path or, on Linux, an abstract address beginning with "@".
// The socket is bound immediately so it is ready before the §4.7
// startup sequence spawns or schedules the runtime.
func NewSocketRuntimeProcess(socket string) (*SocketRuntimeProcess, error) {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("adapter: bind runtime socket %s: %w", socket, err)
	}
	return &SocketRuntimeProcess{listener: l}, nil
}

// SocketPath is the address the runtime dials to reach the adapter. It
// is the value the adapter writes into the runtime container's
// LENNY_ADAPTER_SOCKET environment variable.
func (p *SocketRuntimeProcess) SocketPath() string {
	return p.listener.Addr().String()
}

// Start binds the session to the socket and waits for the runtime to
// connect. When SpawnPath is set Start first execs that binary with
// LENNY_ADAPTER_SOCKET pointing at the bound socket. Start is the §4.7
// startup-sequence step that makes the runtime live; it returns once the
// runtime has connected or the accept times out.
func (p *SocketRuntimeProcess) Start(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	if p.session == sessionID && p.conn != nil {
		p.mu.Unlock()
		return nil
	}
	if p.session != "" && p.session != sessionID {
		p.mu.Unlock()
		return fmt.Errorf("adapter: socket runtime already bound to session %s", p.session)
	}
	p.session = sessionID
	spawn := p.SpawnPath
	timeout := p.AcceptTimeout
	p.mu.Unlock()

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	if spawn != "" {
		if err := p.spawn(spawn); err != nil {
			p.clearSession()
			return err
		}
	}

	conn, err := p.accept(ctx, timeout)
	if err != nil {
		p.killSpawned()
		p.clearSession()
		return err
	}

	scanner := bufio.NewScanner(conn)
	// spec: §15.4.1 line 1548 — a single OutputPart may be up to 50 MB.
	// The sidecar scanner must admit a frame at that ceiling; a 16 MB cap
	// would fail framing on a legal 17–50 MB part before it reached the
	// gateway's §15.4.1 ingress validation. Matches echocore and the
	// runtime SDK, which both already use 50 MB. F-15.4.1 (15.4-INFO-031).
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLFrameBytes)

	p.mu.Lock()
	p.conn = conn
	p.scanner = scanner
	p.mu.Unlock()
	return nil
}

// accept waits for the runtime's connection, bounded by timeout and ctx.
func (p *SocketRuntimeProcess) accept(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := p.listener.Accept()
		ch <- result{conn: conn, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("adapter: accept runtime connection: %w", r.err)
		}
		return r.conn, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("adapter: runtime did not connect within %s", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// spawn execs the runtime binary for the developer loop, pointing it at
// the bound socket through LENNY_ADAPTER_SOCKET.
func (p *SocketRuntimeProcess) spawn(path string) error {
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_SOCKET="+p.SocketPath())
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("adapter: spawn runtime %q: %w", path, err)
	}
	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()
	return nil
}

// WriteEnvelope forwards a pre-encoded §15.4.1 message envelope to the
// runtime over the socket, terminated by a newline.
func (p *SocketRuntimeProcess) WriteEnvelope(sessionID string, envelope []byte) error {
	p.mu.Lock()
	conn := p.conn
	bound := p.session
	p.mu.Unlock()
	if bound != sessionID {
		return fmt.Errorf("adapter: session %s is not bound to this socket runtime", sessionID)
	}
	if conn == nil {
		return fmt.Errorf("adapter: socket runtime for session %s is not connected", sessionID)
	}
	if _, err := conn.Write(append(envelope, '\n')); err != nil {
		return fmt.Errorf("adapter: write envelope to runtime socket: %w", err)
	}
	return nil
}

// Output streams every §15.4.1 JSONL frame the runtime writes on the
// socket. The channel closes when the runtime closes the connection;
// ctx cancellation stops the reader so a stalled consumer does not leak
// the goroutine.
func (p *SocketRuntimeProcess) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	p.mu.Lock()
	scanner := p.scanner
	bound := p.session
	p.mu.Unlock()
	if bound != sessionID {
		return nil, fmt.Errorf("adapter: session %s is not bound to this socket runtime", sessionID)
	}
	if scanner == nil {
		return nil, fmt.Errorf("adapter: socket runtime for session %s is not connected", sessionID)
	}
	ch := make(chan []byte)
	go func() {
		defer close(ch)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Interrupt terminates the runtime. The §4.7 sidecar model has no host
// process to signal when the runtime runs in a separate container, so
// Interrupt closes the socket: the runtime observes the §15.4 socket
// EOF and exits. When SpawnPath spawned the runtime as a child, a hard
// interrupt also kills that process.
func (p *SocketRuntimeProcess) Interrupt(_ context.Context, sessionID string, hard bool) error {
	p.mu.Lock()
	conn := p.conn
	cmd := p.cmd
	bound := p.session
	p.mu.Unlock()
	if bound != sessionID {
		return fmt.Errorf("adapter: session %s is not bound to this socket runtime", sessionID)
	}
	if conn != nil {
		_ = conn.Close()
	}
	if hard && cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("adapter: kill spawned runtime: %w", err)
		}
	}
	return nil
}

// Close tears the runtime down: it closes the socket connection (the
// §15.4 clean-exit signal), waits the resolved grace window for a
// spawned child to exit, and closes the listener.
//
// The grace window is derived from the §4.7 ShutdownRequest.deadline_ms
// the caller plumbed into ctx (the gateway's §11.4 step-3 10s window).
// A context with no deadline falls back to defaultSocketShutdownGrace,
// preserving the historical 10s behavior. spec: §11.4 line 258.
func (p *SocketRuntimeProcess) Close(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	conn := p.conn
	cmd := p.cmd
	p.conn = nil
	p.scanner = nil
	p.session = ""
	p.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		grace := resolveShutdownGrace(ctx, 0, defaultSocketShutdownGrace)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(grace):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	return p.listener.Close()
}

// defaultSocketShutdownGrace is the SIGTERM-to-SIGKILL pivot window the
// socket runtime falls back to when Close has no plumbed deadline. It
// matches the §11.4 step-3 10s default the gateway sends.
const defaultSocketShutdownGrace = 10 * time.Second

// clearSession resets the bound session after a failed Start.
func (p *SocketRuntimeProcess) clearSession() {
	p.mu.Lock()
	p.session = ""
	p.mu.Unlock()
}

// killSpawned kills a child started by spawn during a failed Start.
func (p *SocketRuntimeProcess) killSpawned() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

// compile-time assertion that SocketRuntimeProcess satisfies the
// RuntimeProcess contract the §4.7 adapter drives.
var _ RuntimeProcess = (*SocketRuntimeProcess)(nil)
