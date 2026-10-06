// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/linefanout"
)

// maxJSONLFrameBytes is the largest single §28.5.3 JSONL frame the
// sidecar scanner admits. It matches the §28.5.3 50 MB
// MessagePart ceiling so a legal large part frames before the gateway's
// ingress check runs. spec: §28.5.3. F-15.4.1 (15.4-INFO-031).
const maxJSONLFrameBytes = 50 * 1024 * 1024

// errRuntimeConnectionEnded is the error Start and Output return once the
// runtime's connection has ended, because the runtime closed its end or the
// pod-scope teardown closed it. The runtime process is not re-created or
// reconnected inside the pod (§4.7.10), so the ended state is sticky and a
// later session's start fails at once rather than waiting out the accept
// bound. spec: §4.7.10 (Runtime process lifetime); §5.2 (Runtime not live).
var errRuntimeConnectionEnded = errors.New("adapter: runtime connection ended")

// SocketRuntimeProcess is the §4.7 sidecar-model RuntimeProcess: the
// adapter listens on an abstract Unix socket and the runtime — running
// in a separate pod container — dials it and exchanges §28.5.3 JSONL
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
// For tests, SpawnPath may be set: Start then execs that binary with
// LENNY_ADAPTER_SOCKET pointing at the bound socket, so one host can
// exercise the sidecar transport without a pod. No production caller sets
// it; the developer loop's cmd/lenny-adapter --runtime-bin flag builds a
// SubprocessExecutor instead.
//
// One runtime process per pod serves every slot, multiplexed on the
// frame's sessionId over the single connection. Start is idempotent
// across slots: the first call accepts the connection and starts the
// fan-out reader, and a later Start for a sibling slot's session reuses
// the live connection. WriteEnvelope writes any session's
// sessionId-tagged envelope over that one connection, and each Output
// subscriber receives every frame the runtime emits; the Attach handler
// demultiplexes by sessionId.
//
// The transport lives as long as the pod. The connection is accepted at the
// pod's first session start and serves every later session the pod serves,
// across occupancy zero, multiplexed by sessionId. Close and Interrupt end
// nothing: no session teardown, interrupt, or heartbeat escalation closes
// the connection or signals the process. The pod-scope teardown,
// CloseListener, is the transport's only close. The adapter runs it at
// process exit and at the coordinator hold timeout.
//
// When the runtime closes its end, the fan-out reader records a sticky
// ended state, and every later Start and Output fails at once with
// errRuntimeConnectionEnded. ServesNextSession reports whether the
// transport can serve the next session, which the whole-pod scrub report
// carries to the gateway. The listener is bound once at construction,
// before the pod is claimable, and installs one authenticated connection.
//
// The listener admits only the expected agent UID, which SO_PEERCRED
// reports for each connecting process (see SocketPeerAuth), outside
// nonce-only mode and the embedded model. A connection the peer check admits
// then runs the runtime connection handshake in every mode: its first line
// must carry the mcpNonce of the manifest published at that moment, and in
// nonce-only mode the runtime must also answer a per-connection HMAC
// challenge. A connection that fails either check is logged and closed with
// no protocol response, and the accept loop waits for the next connection,
// so a foreign or unauthenticated process neither becomes the runtime
// connection nor consumes the accept the runtime's own dial is owed.
// spec: §4.7.9, §4.7.10 (Runtime process lifetime), §4.7.11 (Separate UIDs
// and connection authentication, Runtime connection handshake), §5.2
// (Runtime not live), §28.5.3 (CH-MSGSOCK).
type SocketRuntimeProcess struct {
	listener net.Listener

	// auth is the peer-authentication posture the listener enforces, fixed
	// at construction.
	auth SocketPeerAuth
	// nonce reports the mcpNonce of the currently published manifest, which
	// the runtime connection handshake compares the nonce line with. It is
	// fixed at construction and read at each accept. spec: §4.7.11 (Runtime
	// connection handshake).
	nonce func() string
	// peerUID, when set, replaces the SO_PEERCRED lookup of a connecting
	// peer's UID. It is a test seam: a test process cannot dial from a
	// second UID without root, so a tier-1 case stands in the UID the
	// lookup reports. It is written only before the first Start.
	peerUID func(net.Conn) (uint32, error)
	// logger receives the refusal record for each refused peer. Nil logs
	// to slog.Default.
	logger *slog.Logger

	// SpawnPath, when non-empty, is a runtime binary Start execs with
	// LENNY_ADAPTER_SOCKET set. Empty means the runtime connects on its
	// own — the §4.7 separate-container pod model.
	SpawnPath string

	// AcceptTimeout bounds how long Start waits for the runtime to
	// connect. Zero defaults to 30s.
	AcceptTimeout time.Duration

	mu        sync.Mutex
	connected bool
	conn      net.Conn
	// reader is the buffered reader the handshake read the installed
	// connection through. The fan-out reader scans it rather than conn, so a
	// first frame sent in the same write as the nonce line is not lost.
	reader *bufio.Reader
	cmd    *exec.Cmd
	// hub is the installed connection's subscriber set, which its single
	// reader broadcasts to. spec: §28.5.3.
	hub *linefanout.Hub
	// ended records that the runtime's connection has ended: the fan-out
	// reader sets it when its scan ends, before it closes the subscribers,
	// and CloseListener sets it before it closes the connection. It is
	// never cleared, because the runtime is not reconnected inside the pod.
	// Guarded by mu. spec: §4.7.10.
	ended bool
	// tornDown records that CloseListener has run, so a second call is a
	// no-op. Guarded by mu.
	tornDown bool
	// reading records that the fan-out reader runs over conn. A connection
	// the accept loop installs while no Start waits gets its reader at the
	// next Start, so every reader start is a Start's. Guarded by mu.
	reading bool

	// acceptOnce starts the single accept loop at the first Start. The loop
	// outlives a Start that times out or is cancelled, so a runtime that
	// dials late is installed for the next Start rather than taken by an
	// abandoned accept. spec: §4.7.10 (Runtime process lifetime).
	acceptOnce sync.Once
	// connReady is closed when the accept loop has installed the runtime's
	// connection in conn.
	connReady chan struct{}
	// acceptDone is closed when the accept loop ends without installing a
	// connection, after acceptErr records why: the listener closed, or the
	// pod-scope teardown ran while the accepted connection was in flight.
	acceptDone chan struct{}
	acceptErr  error
}

// NewSocketRuntimeProcess binds the adapter's runtime socket and returns
// a RuntimeProcess that bridges the runtime over it. socket is a
// filesystem path or, on Linux, an abstract address beginning with "@".
// The socket is bound immediately so it is ready before the §4.7
// startup sequence spawns or schedules the runtime. auth sets the
// SO_PEERCRED check the listener applies to each connecting process, and
// nonce reports the published manifest's mcpNonce, which the runtime
// connection handshake checks on every accepted connection (see
// PublishedManifestNonce). spec: §4.7.11 (Separate UIDs and connection
// authentication, Runtime connection handshake).
func NewSocketRuntimeProcess(socket string, auth SocketPeerAuth, nonce func() string) (*SocketRuntimeProcess, error) {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("adapter: bind runtime socket %s: %w", socket, err)
	}
	p := &SocketRuntimeProcess{
		auth:       auth,
		nonce:      nonce,
		connReady:  make(chan struct{}),
		acceptDone: make(chan struct{}),
	}
	p.listener = auth.wrap(l, p.checkRuntimePeer, p.logRefusedPeer)
	return p, nil
}

// checkRuntimePeer admits conn only when its peer runs as the expected agent
// UID. It reads the UID through SO_PEERCRED unless a test installed the
// peerUID seam.
func (p *SocketRuntimeProcess) checkRuntimePeer(conn net.Conn) error {
	if p.peerUID != nil {
		return matchPeerUID(conn, p.auth.ExpectedUID, p.peerUID)
	}
	return p.auth.check(conn)
}

// logRefusedPeer records a refused CH-MSGSOCK connection. See
// logPeerRefusal for the record's fields.
func (p *SocketRuntimeProcess) logRefusedPeer(err error) {
	logPeerRefusal(p.logger, "runtime_peer_refused", p.listener.Addr().String(), p.auth.ExpectedUID, err)
}

// SocketPath is the address the runtime dials to reach the adapter. It
// is the value the adapter writes into the runtime container's
// LENNY_ADAPTER_SOCKET environment variable.
func (p *SocketRuntimeProcess) SocketPath() string {
	return p.listener.Addr().String()
}

// Start makes the runtime live for a session. The first Start starts the
// listener's single accept loop and waits, bounded by AcceptTimeout and ctx,
// for the loop to install the runtime's connection; when SpawnPath is set it
// first execs that binary with LENNY_ADAPTER_SOCKET pointing at the bound
// socket. A Start that times out or is cancelled leaves the loop running, so
// a runtime that dials afterwards is installed and the next Start returns on
// it. Every Start on an installed connection, for a sibling slot or for a
// later session after occupancy zero, returns nil without accepting. Once the
// connection has ended, Start returns errRuntimeConnectionEnded at once.
// spec: §4.7.9, §4.7.10 (Runtime process lifetime), §5.2.
func (p *SocketRuntimeProcess) Start(ctx context.Context, _ string) error {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		return errRuntimeConnectionEnded
	}
	if p.connected {
		p.startReaderLocked()
		p.mu.Unlock()
		return nil
	}
	spawn := p.SpawnPath
	timeout := p.AcceptTimeout
	p.mu.Unlock()

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	if spawn != "" {
		if err := p.spawn(spawn); err != nil {
			return err
		}
	}

	if err := p.awaitConnection(ctx, timeout); err != nil {
		p.killSpawned()
		return err
	}
	return nil
}

// awaitConnection waits for the accept loop to install the runtime's
// connection, bounded by timeout and ctx, and starts the fan-out reader on
// it. Returning on the timeout or ctx leaves the loop running.
func (p *SocketRuntimeProcess) awaitConnection(ctx context.Context, timeout time.Duration) error {
	p.acceptOnce.Do(func() { go p.acceptLoop() })
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.connReady:
	case <-p.acceptDone:
		return fmt.Errorf("adapter: accept runtime connection: %w", p.acceptErr)
	case <-timer.C:
		return fmt.Errorf("adapter: runtime did not connect within %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return errRuntimeConnectionEnded
	}
	p.startReaderLocked()
	return nil
}

// acceptLoop is the listener's single accept path. It accepts each
// connection the peer check admits and runs the runtime connection handshake
// on it through a new buffered reader. A connection that fails the handshake
// is logged and closed with no protocol response, and the loop accepts
// again, so a runtime refused for a replaced nonce is installed when it
// redials. The first connection that passes is installed, with its reader,
// as the runtime's connection, and the loop returns, so the listener installs
// one connection for the pod's life and a later dial stays unaccepted. A
// connection that passes after the pod-scope teardown has run is closed here,
// because no Start can claim it. The loop ends when the listener closes.
// spec: §4.7.10 (Runtime process lifetime), §4.7.11 (Runtime connection
// handshake), §28.5.3 (CH-MSGSOCK).
func (p *SocketRuntimeProcess) acceptLoop() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			p.endAccept(err)
			return
		}
		br := bufio.NewReader(conn)
		if err := authenticateRuntimeConn(conn, br, p.nonce, p.auth.NonceOnly); err != nil {
			p.logRefusedPeer(err)
			_ = conn.Close()
			continue
		}
		p.install(conn, br)
		return
	}
}

// install records conn and its handshake reader as the runtime's connection
// and releases every waiting Start. A connection that arrives after the
// pod-scope teardown is closed instead, and the accept ends.
func (p *SocketRuntimeProcess) install(conn net.Conn, br *bufio.Reader) {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		_ = conn.Close()
		p.endAccept(errRuntimeConnectionEnded)
		return
	}
	p.conn = conn
	p.reader = br
	p.connected = true
	// The connection gets its own subscriber set, which its reader acts on.
	p.hub = linefanout.New()
	p.mu.Unlock()
	close(p.connReady)
}

// endAccept records why the accept loop ended without a connection and
// releases every waiting Start.
func (p *SocketRuntimeProcess) endAccept(err error) {
	p.acceptErr = err
	close(p.acceptDone)
}

// startReaderLocked starts the fan-out reader over the installed connection
// once. One reader goroutine over the single connection fans every frame out
// to all subscribers, so concurrent per-slot Attach streams each see the
// runtime's full output and demultiplex by sessionId. The caller holds mu.
//
// When the scan ends, the connection has ended. The reader records the
// sticky ended state under p.mu before the hub closes its subscribers, so an
// Output call either registered before the record and is closed by the end,
// or runs after it and fails; no subscriber is left open on an ended
// connection. spec: §4.7.10, §5.2 (Runtime not live), §28.5.3.
func (p *SocketRuntimeProcess) startReaderLocked() {
	if p.reading {
		return
	}
	p.reading = true
	// spec: §28.5.3 — a single MessagePart may be up to 50 MB.
	// The sidecar scanner must admit a frame at that ceiling; a 16 MB cap
	// would fail framing on a legal 17–50 MB part before it reached the
	// gateway's §28.5.3 ingress validation. Matches echocore and the
	// runtime SDK, which both already use 50 MB. F-15.4.1 (15.4-INFO-031).
	// The scan runs over the handshake's reader, which may already hold the
	// first frame. spec: §4.7.11 (Runtime connection handshake).
	p.hub.Serve(p.reader, maxJSONLFrameBytes, p.markEnded)
}

// markEnded records the sticky ended state. The fan-out reader runs it when
// its scan ends, before the hub closes the subscribers.
func (p *SocketRuntimeProcess) markEnded() {
	p.mu.Lock()
	p.ended = true
	p.mu.Unlock()
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

// WriteEnvelope forwards a pre-encoded §28.5.3 message envelope to the
// runtime over the single connection, terminated by a newline. The
// envelope already carries its sessionId, which the Attach handler
// stamps on every pod, so WriteEnvelope is session-agnostic: every
// session's frames share the one connection. spec: §28.5.3.
func (p *SocketRuntimeProcess) WriteEnvelope(_ string, envelope []byte) error {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("adapter: socket runtime is not connected")
	}
	if _, err := conn.Write(append(envelope, '\n')); err != nil {
		return fmt.Errorf("adapter: write envelope to runtime socket: %w", err)
	}
	return nil
}

// Output subscribes to the runtime's output. It returns a channel carrying
// every §28.5.3 JSONL frame the runtime writes on the single connection;
// the Attach handler demultiplexes by sessionId so each per-slot stream
// keeps only its session's frames. The channel closes when the
// connection ends, and ctx cancellation unsubscribes so a stalled consumer
// does not stall the shared reader. Once the connection has ended, Output
// returns errRuntimeConnectionEnded. spec: §4.7.10, §28.5.3.
func (p *SocketRuntimeProcess) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		return nil, errRuntimeConnectionEnded
	}
	if !p.connected {
		p.mu.Unlock()
		return nil, fmt.Errorf("adapter: socket runtime is not connected")
	}
	hub := p.hub
	p.mu.Unlock()
	// ctx cancellation unsubscribes, so a closed Attach stream or an ended
	// session_started wait stops the fan-out from delivering to a dead
	// consumer.
	out, err := hub.Subscribe(ctx)
	if errors.Is(err, linefanout.ErrEnded) {
		return nil, errRuntimeConnectionEnded
	}
	return out, err
}

// ServesNextSession reports whether the runtime can serve the pod's next
// session: the connection was accepted and has not ended. It is false before
// the first accept, because no runtime has connected, and false for the rest
// of the pod's life once the connection has ended. The whole-pod scrub
// samples it after the scrub, and the gateway retires a pod whose runtime
// cannot serve the next session. spec: §5.2 (Runtime not live); §4.7.10.
func (p *SocketRuntimeProcess) ServesNextSession() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected && !p.ended
}

// Interrupt returns nil. The transport lives as long as the pod and the
// pod-scope teardown, CloseListener, is its only close, so no interrupt,
// clean or hard, and no heartbeat escalation closes the connection or
// signals the runtime process. spec: §4.7.10 (Runtime process lifetime).
func (p *SocketRuntimeProcess) Interrupt(context.Context, string, bool) error {
	return nil
}

// Close returns nil. The transport lives as long as the pod and the
// pod-scope teardown, CloseListener, is its only close, so a session's
// teardown, including the last one before occupancy zero, leaves the
// connection up for the pod's later sessions. spec: §4.7.10 (Runtime process
// lifetime).
func (p *SocketRuntimeProcess) Close(context.Context, string) error {
	return nil
}

// CloseListener is the transport's pod-scope teardown and its only close. It
// sets the sticky ended state, closes the runtime's connection (the §15.4
// clean-exit EOF), waits defaultSocketShutdownGrace for a spawned child to
// exit and then kills it, and closes the pod-scoped listener. Only the
// test-only SpawnPath creates a child, so the production call does not wait.
//
// The adapter process runs it at exit, and the adapter runs it when the
// coordinator hold times out. It is safe to call more than once: a second
// call returns nil rather than the error a second net.Listener.Close
// produces. The accept loop's Accept returns net.ErrClosed when the
// listener closes, so the loop exits and every Start waiting on it returns.
// A connection the loop accepts while the teardown runs is closed by the
// loop, so no accepted connection outlives the teardown unclaimed.
// spec: §4.7.10 (Runtime process lifetime), §10.1.4 (Hold state timeout),
// §15.4, §28.5.3.
func (p *SocketRuntimeProcess) CloseListener() error {
	p.mu.Lock()
	if p.tornDown {
		p.mu.Unlock()
		return nil
	}
	p.tornDown = true
	p.ended = true
	conn := p.conn
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	waitThenKill(cmd, defaultSocketShutdownGrace)
	return p.listener.Close()
}

// waitThenKill waits up to grace for a spawned child to exit and kills it
// once grace has passed. A nil command, or one that never started, is a
// no-op.
func waitThenKill(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(grace):
		_ = cmd.Process.Kill()
		<-done
	}
}

// defaultSocketShutdownGrace bounds the pod-scope teardown's wait for a
// spawned child to exit after its connection closes, before the teardown
// kills the child. Only the test-only SpawnPath creates a child.
const defaultSocketShutdownGrace = 10 * time.Second

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
