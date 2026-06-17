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
// One runtime process per pod serves every slot, multiplexed on slotId
// over the single connection (spec/05:509, spec/15:1459). Start is
// idempotent across slots: the first call accepts the connection and
// starts the fan-out reader, and a later Start for a sibling slot's
// session reuses the live connection. WriteEnvelope writes any slot's
// slotId-tagged envelope over that one connection, and each Output
// subscriber receives every frame the runtime emits; the Attach handler
// demultiplexes by slotId. Interrupt and Close stay session-keyed and act
// on the shared connection.
type SocketRuntimeProcess struct {
	listener net.Listener

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
}

// subscriber is one Output consumer of the shared runtime connection. The
// fan-out reader hands each frame to feed; a dedicated pump goroutine
// drains feed into out, so one slow per-slot Attach stream never blocks the
// reader from delivering a sibling slot's frames. done closes the pump and
// out when the consumer's Output context is cancelled or the runtime
// connection closes. spec: §15.4.1 line 1459.
type subscriber struct {
	feed chan []byte
	out  chan []byte
	done chan struct{}
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

// close stops the pump and closes out exactly once.
func (s *subscriber) close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
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

// Start makes the runtime live and ensures the single connection is up.
// When SpawnPath is set the first Start execs that binary with
// LENNY_ADAPTER_SOCKET pointing at the bound socket. Start is the §4.7
// startup-sequence step; it returns once the runtime has connected or the
// accept times out. Start is idempotent across slots: one runtime process
// per pod serves every slot over the one connection (spec/05:509), so a
// second Start (for a sibling slot's session) reuses the live connection
// rather than accepting a new one.
func (p *SocketRuntimeProcess) Start(ctx context.Context, _ string) error {
	p.mu.Lock()
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
	// spec: §15.4.1 line 1548 — a single OutputPart may be up to 50 MB.
	// The sidecar scanner must admit a frame at that ceiling; a 16 MB cap
	// would fail framing on a legal 17–50 MB part before it reached the
	// gateway's §15.4.1 ingress validation. Matches echocore and the
	// runtime SDK, which both already use 50 MB. F-15.4.1 (15.4-INFO-031).
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLFrameBytes)

	p.mu.Lock()
	p.conn = conn
	p.connected = true
	p.subscribers = map[*subscriber]struct{}{}
	p.mu.Unlock()

	// One reader goroutine over the single connection fans every frame out
	// to all subscribers, so concurrent per-slot Attach streams each see
	// the runtime's full output and demultiplex by slotId. spec: §15.4.1
	// line 1459.
	go p.fanOut(scanner)
	return nil
}

// fanOut reads every §15.4.1 JSONL frame the runtime writes and broadcasts
// it to all registered Output subscribers. It closes each subscriber
// channel when the runtime closes the connection, so a per-slot Attach
// stream observes the EOF. Each subscriber owns its own buffered intake
// (subscriber.feed), so a slow or dead consumer on one slot's Attach
// stream never head-of-line-blocks the reader from delivering a sibling
// slot's frames. spec: §15.4.1 line 1459.
func (p *SocketRuntimeProcess) fanOut(scanner *bufio.Scanner) {
	defer p.closeSubscribers()
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		p.broadcast(line)
	}
}

// broadcast hands one frame to every current subscriber. Each subscriber
// has a dedicated pump goroutine draining its buffered feed into its Output
// channel, so the send to one subscriber never blocks delivery to another.
func (p *SocketRuntimeProcess) broadcast(line []byte) {
	p.mu.Lock()
	subs := make([]*subscriber, 0, len(p.subscribers))
	for s := range p.subscribers {
		subs = append(subs, s)
	}
	p.mu.Unlock()
	for _, s := range subs {
		s.send(line)
	}
}

// closeSubscribers shuts every still-registered subscriber down so its
// Output channel closes and the per-slot Attach stream observes the
// runtime's connection close. A subscriber the consumer already
// unsubscribed is absent from the map, so each closes exactly once.
func (p *SocketRuntimeProcess) closeSubscribers() {
	p.mu.Lock()
	subs := make([]*subscriber, 0, len(p.subscribers))
	for s := range p.subscribers {
		subs = append(subs, s)
		delete(p.subscribers, s)
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

// WriteEnvelope forwards a pre-encoded §15.4.1 message envelope to the
// runtime over the single connection, terminated by a newline. The
// envelope already carries its slotId (stamped by the Attach handler on a
// concurrent pod), so WriteEnvelope is session-agnostic: every slot's
// frames share the one connection. spec: §15.4.1 line 1459.
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
// every §15.4.1 JSONL frame the runtime writes on the single connection;
// the Attach handler demultiplexes by slotId so each per-slot stream keeps
// only its slot's frames (spec/15:1459). The channel closes when the
// runtime closes the connection, and ctx cancellation unsubscribes so a
// stalled consumer does not stall the shared reader.
func (p *SocketRuntimeProcess) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	p.mu.Lock()
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
// unsubscribe both resolve to a single out-channel close. spec: §15.4.1
// line 1459.
func (p *SocketRuntimeProcess) unsubscribe(sub *subscriber) {
	p.mu.Lock()
	delete(p.subscribers, sub)
	p.mu.Unlock()
	sub.close()
}

// Interrupt terminates the runtime. The §4.7 sidecar model has no host
// process to signal when the runtime runs in a separate container, so
// Interrupt closes the socket: the runtime observes the §15.4 socket EOF
// and exits. When SpawnPath spawned the runtime as a child, a hard
// interrupt also kills that process. One runtime process per pod serves
// every slot (spec/05:509), so Interrupt acts on the shared connection;
// the session id is accepted for the interface contract.
func (p *SocketRuntimeProcess) Interrupt(_ context.Context, _ string, hard bool) error {
	p.mu.Lock()
	conn := p.conn
	cmd := p.cmd
	p.mu.Unlock()
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

// Close tears the runtime down: it closes the shared socket connection
// (the §15.4 clean-exit signal), waits the resolved grace window for a
// spawned child to exit, and closes the listener. One runtime process per
// pod serves every slot over the one connection (spec/05:509), so Close
// tears that single connection down. It is idempotent: a second per-slot
// Close after the connection is already gone is a no-op, since the §6.1
// concurrent pod is terminated and replaced once its sessions end.
//
// The grace window is derived from the §4.7 ShutdownRequest.deadline_ms
// the caller plumbed into ctx (the gateway's §11.4 step-3 10s window).
// A context with no deadline falls back to defaultSocketShutdownGrace,
// preserving the historical 10s behavior. spec: §11.4 line 258.
func (p *SocketRuntimeProcess) Close(ctx context.Context, _ string) error {
	p.mu.Lock()
	if !p.connected {
		p.mu.Unlock()
		return nil
	}
	conn := p.conn
	cmd := p.cmd
	p.conn = nil
	p.connected = false
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
