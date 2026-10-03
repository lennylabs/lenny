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
// before the pod is claimable, and accepts one connection.
//
// The listener admits only the expected agent UID, which SO_PEERCRED
// reports for each connecting process (see SocketPeerAuth). A refused
// connection is logged and closed inside the listener's Accept, which then
// waits for the next connection, so a foreign process neither becomes the
// runtime connection nor consumes the accept the runtime's own dial is
// owed. The manifest-nonce handshake the CH-MSGSOCK card also states is not
// performed.
// spec: §4.7.9, §4.7.10 (Runtime process lifetime), §4.7.11 (Separate UIDs
// and connection authentication), §5.2 (Runtime not live), §28.5.3
// (CH-MSGSOCK).
type SocketRuntimeProcess struct {
	listener net.Listener

	// auth is the peer-authentication posture the listener enforces, fixed
	// at construction.
	auth SocketPeerAuth
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

	mu          sync.Mutex
	connected   bool
	conn        net.Conn
	cmd         *exec.Cmd
	subscribers map[*subscriber]struct{}
	// ended records that the runtime's connection has ended: the fan-out
	// reader sets it when its scan ends, before it closes the subscribers,
	// and CloseListener sets it before it closes the connection. It is
	// never cleared, because the runtime is not reconnected inside the pod.
	// Guarded by mu. spec: §4.7.10.
	ended bool
	// tornDown records that CloseListener has run, so a second call is a
	// no-op. Guarded by mu.
	tornDown bool
}

// subscriber is one Output consumer of the shared runtime connection. The
// fan-out reader hands each frame to feed; a dedicated pump goroutine
// drains feed into out, so one slow per-slot Attach stream never blocks the
// reader from delivering a sibling slot's frames. done closes the pump and
// out when the consumer's Output context is cancelled or the runtime
// connection closes. spec: §28.5.3.
type subscriber struct {
	feed chan []byte
	out  chan []byte
	done chan struct{}
	// closeOnce guards done so the two concurrent closers — closeSubscribers
	// when the fan-out reader hits EOF (the runtime closed its end, or the
	// pod-scope teardown closed the connection), and unsubscribe on the
	// Output context's cancellation — resolve to a single close(done) rather
	// than racing into a double close. spec: §28.5.3.
	closeOnce sync.Once
}

// newSubscriber starts a subscriber and its pump. The pump forwards each
// fed frame to out and closes out when done is closed, so the Attach demux
// observes the runtime's connection close.
func newSubscriber() *subscriber {
	s := &subscriber{
		feed: make(chan []byte, 64),
		out:  make(chan []byte),
		done: make(chan struct{}),
	}
	go s.pump()
	return s
}

// pump drains the buffered feed into out until done is closed, then closes
// out so the consumer observes the stream end.
func (s *subscriber) pump() {
	defer close(s.out)
	for {
		select {
		case line := <-s.feed:
			select {
			case s.out <- line:
			case <-s.done:
				return
			}
		case <-s.done:
			return
		}
	}
}

// send hands one frame to the subscriber's buffered feed, abandoning it if
// the subscriber is done so the shared reader never blocks on a dead
// consumer. A full buffer blocks only this subscriber's pump, never the
// reader's delivery to siblings, since each send targets a distinct feed.
func (s *subscriber) send(line []byte) {
	select {
	case s.feed <- line:
	case <-s.done:
	}
}

// close stops the pump and closes out exactly once. closeOnce makes it
// safe under the concurrent closers: closeSubscribers on the runtime EOF
// and unsubscribe on the Output context cancellation can both call it, and
// only the first closes done.
func (s *subscriber) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// SocketPeerAuth is the connection-authentication posture of the
// CH-MSGSOCK listener. The zero value requires the peer to run as UID 0,
// so a caller that omits the agent UID admits no unprivileged process
// rather than every process.
// spec: §4.7.11 (Separate UIDs and connection authentication), §28.5.3
// (CH-MSGSOCK, Endpoint).
type SocketPeerAuth struct {
	// ExpectedUID is the agent UID the adapter accepts the runtime's
	// connection from: the runtime container's runAsUser, which is a UID
	// within the pod's user namespace. The adapter process receives it as
	// --runtime-uid. A test that dials from its own process sets it to
	// os.Getuid().
	ExpectedUID uint32
	// NonceOnly records that Runtime.spec.requireSoPeercred is false
	// (--require-so-peercred=false), the mode for a confirmed gVisor
	// SO_PEERCRED divergence. The specification states that the peer check
	// is unavailable in this mode and that the manifest nonce and the
	// per-connection HMAC-SHA256 challenge authenticate the connection
	// instead, so the listener applies no peer check. Neither exchange
	// exists on CH-MSGSOCK, which leaves the socket unauthenticated in this
	// mode (BUILD-GAPS F-4.7.25).
	NonceOnly bool
}

// NewSocketRuntimeProcess binds the adapter's runtime socket and returns
// a RuntimeProcess that bridges the runtime over it. socket is a
// filesystem path or, on Linux, an abstract address beginning with "@".
// The socket is bound immediately so it is ready before the §4.7
// startup sequence spawns or schedules the runtime. auth sets the
// SO_PEERCRED check the listener applies to each connecting process.
// spec: §4.7.11 (Separate UIDs and connection authentication).
func NewSocketRuntimeProcess(socket string, auth SocketPeerAuth) (*SocketRuntimeProcess, error) {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("adapter: bind runtime socket %s: %w", socket, err)
	}
	p := &SocketRuntimeProcess{auth: auth}
	p.listener = p.authenticatedListener(l)
	return p, nil
}

// authenticatedListener wraps l in the SO_PEERCRED peer check unless the
// pod runs in nonce-only mode, where the specification states that the
// check is unavailable. spec: §4.7.11 (Nonce-only fallback), §28.5.3
// (CH-MSGSOCK, Endpoint).
func (p *SocketRuntimeProcess) authenticatedListener(l net.Listener) net.Listener {
	if p.auth.NonceOnly {
		return l
	}
	return &peerCheckedListener{
		Listener: l,
		check:    p.checkRuntimePeer,
		onReject: p.logRefusedPeer,
	}
}

// checkRuntimePeer admits conn only when its peer runs as the expected agent
// UID. It reads the UID through SO_PEERCRED unless a test installed the
// peerUID seam.
func (p *SocketRuntimeProcess) checkRuntimePeer(conn net.Conn) error {
	if p.peerUID != nil {
		return matchPeerUID(conn, p.auth.ExpectedUID, p.peerUID)
	}
	return checkPeerUID(conn, p.auth.ExpectedUID)
}

// logRefusedPeer records a refused CH-MSGSOCK connection with the peer UID
// SO_PEERCRED reported, or with the lookup error when the UID could not be
// read. The record carries identifiers only; the socket carries no secret
// at this point because the refused connection never received a frame.
func (p *SocketRuntimeProcess) logRefusedPeer(err error) {
	logger := p.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		"socket", p.listener.Addr().String(),
		"expected_uid", p.auth.ExpectedUID,
	}
	var mismatch *PeerUIDMismatchError
	if errors.As(err, &mismatch) {
		attrs = append(attrs, "peer_uid", mismatch.Peer)
	} else {
		attrs = append(attrs, "err", err)
	}
	logger.Warn("runtime_peer_refused", attrs...)
}

// SocketPath is the address the runtime dials to reach the adapter. It
// is the value the adapter writes into the runtime container's
// LENNY_ADAPTER_SOCKET environment variable.
func (p *SocketRuntimeProcess) SocketPath() string {
	return p.listener.Addr().String()
}

// Start makes the runtime live for a session. The first Start accepts the
// runtime's connection; when SpawnPath is set it first execs that binary
// with LENNY_ADAPTER_SOCKET pointing at the bound socket. Every later Start,
// for a sibling slot or for a later session after occupancy zero, returns
// nil on the live connection without accepting. Once the connection has
// ended, Start returns errRuntimeConnectionEnded at once.
// spec: §4.7.9, §4.7.10 (Runtime process lifetime), §5.2.
func (p *SocketRuntimeProcess) Start(ctx context.Context, _ string) error {
	p.mu.Lock()
	if p.ended {
		p.mu.Unlock()
		return errRuntimeConnectionEnded
	}
	if p.connected {
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

	conn, err := p.accept(ctx, timeout)
	if err != nil {
		p.killSpawned()
		return err
	}

	scanner := bufio.NewScanner(conn)
	// spec: §28.5.3 — a single MessagePart may be up to 50 MB.
	// The sidecar scanner must admit a frame at that ceiling; a 16 MB cap
	// would fail framing on a legal 17–50 MB part before it reached the
	// gateway's §28.5.3 ingress validation. Matches echocore and the
	// runtime SDK, which both already use 50 MB. F-15.4.1 (15.4-INFO-031).
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLFrameBytes)

	// The connection gets its own subscriber set, which its reader acts on.
	subs := map[*subscriber]struct{}{}
	p.mu.Lock()
	if p.ended {
		// The pod-scope teardown ran while the accept was in flight. It
		// found no connection to close, so this one is closed here rather
		// than kept on a transport that has ended.
		p.mu.Unlock()
		_ = conn.Close()
		return errRuntimeConnectionEnded
	}
	p.conn = conn
	p.connected = true
	p.subscribers = subs
	p.mu.Unlock()

	// One reader goroutine over the single connection fans every frame out
	// to all subscribers, so concurrent per-slot Attach streams each see
	// the runtime's full output and demultiplex by sessionId.
	// spec: §28.5.3.
	go p.fanOut(scanner, subs)
	return nil
}

// fanOut reads every §28.5.3 JSONL frame the runtime writes and broadcasts
// it to all registered Output subscribers. Each subscriber owns its own
// buffered intake (subscriber.feed), so a slow or dead consumer on one
// slot's Attach stream never head-of-line-blocks the reader from delivering
// a sibling slot's frames.
//
// When the scan ends, the connection has ended. The reader records the
// sticky ended state under p.mu before it closes the subscribers, so an
// Output call either registered before the record and is closed here, or
// runs after it and fails; no subscriber is left open on an ended
// connection. spec: §4.7.10, §5.2 (Runtime not live), §28.5.3.
func (p *SocketRuntimeProcess) fanOut(scanner *bufio.Scanner, subs map[*subscriber]struct{}) {
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		p.broadcast(subs, line)
	}
	p.mu.Lock()
	p.ended = true
	p.mu.Unlock()
	p.closeSubscribers(subs)
}

// broadcast hands one frame to every subscriber currently in set. Each
// subscriber has a dedicated pump goroutine draining its buffered feed into
// its Output channel, so the send to one subscriber never blocks delivery to
// another.
func (p *SocketRuntimeProcess) broadcast(set map[*subscriber]struct{}, line []byte) {
	p.mu.Lock()
	subs := make([]*subscriber, 0, len(set))
	for s := range set {
		subs = append(subs, s)
	}
	p.mu.Unlock()
	for _, s := range subs {
		s.send(line)
	}
}

// closeSubscribers shuts every subscriber still registered in set down so
// its Output channel closes and the per-slot Attach stream observes the
// runtime's connection close. A subscriber the consumer already
// unsubscribed is absent from the set, so each closes exactly once.
// spec: §5.2, §28.5.3.
func (p *SocketRuntimeProcess) closeSubscribers(set map[*subscriber]struct{}) {
	p.mu.Lock()
	subs := make([]*subscriber, 0, len(set))
	for s := range set {
		subs = append(subs, s)
		delete(set, s)
	}
	p.mu.Unlock()
	for _, s := range subs {
		s.close()
	}
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
	sub := newSubscriber()
	p.subscribers[sub] = struct{}{}
	p.mu.Unlock()

	// Unsubscribe and stop the pump on ctx cancellation so a closed Attach
	// stream stops the fan-out from delivering to a dead consumer.
	go func() {
		<-ctx.Done()
		p.unsubscribe(sub)
	}()
	return sub.out, nil
}

// unsubscribe removes a subscriber and stops its pump. close() is
// idempotent, so a concurrent closeSubscribers (on EOF) and a ctx-cancel
// unsubscribe both resolve to a single out-channel close. spec: §28.5.3.
func (p *SocketRuntimeProcess) unsubscribe(sub *subscriber) {
	p.mu.Lock()
	delete(p.subscribers, sub)
	p.mu.Unlock()
	sub.close()
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
// produces. An accept blocked in Start returns net.ErrClosed when the
// listener closes, so its goroutine exits.
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
