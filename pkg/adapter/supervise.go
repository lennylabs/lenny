// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
)

// This file holds the adapter's end of CH-SUPERVISE: the single accept
// before READY (AwaitSupervisor) and the link that carries one request at a
// time to the runtime supervisor, reads its replies and exit reports, and
// pings it while no other request is outstanding.
//
// spec: §28.5.3 (CH-SUPERVISE); §4.7.9 (step 4); §4.7.10 (Runtime
// supervisor, Liveness); §4.7.11 (Runtime supervisor, Property self-test).

// ErrSuperviseLinkFailed is the error every request returns once the link has
// failed: the connection closed, a request went unanswered within its bound,
// or a message could not be decoded strictly. After it the adapter sends no
// further request. spec: §28.5.3 (CH-SUPERVISE, Degradation); §5.2
// (Supervised scrub sequence).
var ErrSuperviseLinkFailed = errors.New("CH-SUPERVISE link failure")

// RuntimeLifetime is the pod's runtime-process lifetime, fixed when the pod
// is created and read from the adapter's --runtime-process flag. spec: §5.2
// (Runtime-process lifetime).
type RuntimeLifetime string

const (
	// LifetimeKeep keeps one runtime process across the pod's sessions.
	LifetimeKeep RuntimeLifetime = "keep"
	// LifetimeRestart ends the runtime process in each whole-pod scrub.
	LifetimeRestart RuntimeLifetime = "restart"
)

// AwaitSupervisorConfig configures AwaitSupervisor.
type AwaitSupervisorConfig struct {
	// Socket is the abstract socket name the adapter listens on, with its
	// leading "@" (@lenny-supervise in a pod).
	Socket string
	// Auth is the SO_PEERCRED posture: outside nonce-only mode the listener
	// admits only Auth.ExpectedUID, the agent UID. spec: §4.7.11 item 8.
	Auth SocketPeerAuth
	// Lifetime selects the property gate: a restart pod refuses a ready that
	// reports a failed gating property.
	Lifetime RuntimeLifetime
	// Timeout bounds the wait for the connection and its ready together.
	Timeout time.Duration
	// PeerUID, when set, replaces the SO_PEERCRED lookup of a connecting
	// peer's UID. It is a test seam: a test process cannot dial from a second
	// UID without root.
	PeerUID func(net.Conn) (uint32, error)
	// Logger receives the property and refusal records. Nil logs to
	// slog.Default.
	Logger *slog.Logger
}

// Adapter log events for the CH-SUPERVISE link. The cluster tests read them
// from the adapter container's log: the ready record dates the supervisor's
// connection, which the pod's READY must not precede, and the failure record
// marks a link the adapter has taken down. spec: §4.7.10 (Runtime supervisor,
// Launch; Liveness); §4.7.9 (step 4).
const (
	// LogSupervisorReady is logged once AwaitSupervisor has accepted the
	// supervisor's connection and its ready has passed the property gate.
	LogSupervisorReady = "supervisor_ready"
	// LogSuperviseLinkFailure is logged when an attached link fails.
	LogSuperviseLinkFailure = "supervise_link_failure"
)

// AwaitSupervisor accepts the runtime supervisor's one connection and reads
// its ready. It listens through peerCheckedListener with the agent-UID check
// (none in nonce-only mode), accepts one connection, and closes the listener,
// so a later dial is refused. It reads ready within the same timeout, logs
// each failed property under its enumerated name, and, on a restart pod,
// fails when a gating property failed. It logs LogSupervisorReady once the
// ready is accepted. A missing, late, or malformed ready, or a first message
// other than ready, is an error. On success it returns the
// link with its reader running; the caller starts the ping with RunPing.
// spec: §4.7.11 (Runtime supervisor, Property self-test); §28.5.3
// (CH-SUPERVISE, Endpoint; CH-SUPERVISE, Preconditions; CH-SUPERVISE,
// Exclusivity).
func AwaitSupervisor(ctx context.Context, cfg AwaitSupervisorConfig) (*SupervisorLink, error) {
	if cfg.Timeout <= 0 {
		return nil, errors.New("await supervisor: a positive timeout is required")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	conn, err := acceptOneSupervisor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	l := newSupervisorLink(conn, cfg.Logger)
	props, err := l.readReady(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := gateProperties(props, cfg.Lifetime, l.logger); err != nil {
		_ = conn.Close()
		return nil, err
	}
	l.properties = props
	l.logger.Info(LogSupervisorReady)
	go l.readLoop()
	return l, nil
}

// acceptOneSupervisor listens on the socket, accepts the first connection the
// peer check admits, and closes the listener on every path.
func acceptOneSupervisor(ctx context.Context, cfg AwaitSupervisorConfig) (net.Conn, error) {
	raw, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return nil, fmt.Errorf("await supervisor: listen %s: %w", cfg.Socket, err)
	}
	logger := loggerOr(cfg.Logger)
	check := func(conn net.Conn) error {
		if cfg.PeerUID != nil {
			return matchPeerUID(conn, cfg.Auth.ExpectedUID, cfg.PeerUID)
		}
		return cfg.Auth.check(conn)
	}
	onReject := func(err error) {
		logPeerRefusal(logger, "supervisor_peer_refused", cfg.Socket, cfg.Auth.ExpectedUID, err)
	}
	lis := cfg.Auth.wrap(raw, check, onReject)
	type accepted struct {
		conn net.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := lis.Accept()
		ch <- accepted{c, err}
	}()
	select {
	case a := <-ch:
		_ = lis.Close()
		if a.err != nil {
			return nil, fmt.Errorf("await supervisor: accept: %w", a.err)
		}
		return a.conn, nil
	case <-ctx.Done():
		_ = lis.Close()
		if a := <-ch; a.conn != nil {
			_ = a.conn.Close()
		}
		return nil, fmt.Errorf("await supervisor: no connection on %s: %w", cfg.Socket, ctx.Err())
	}
}

// gateProperties logs each failed property and refuses a restart pod whose
// ready reports a failed gating property. spec: §4.7.11 (Runtime supervisor,
// Property self-test).
func gateProperties(props map[string]bool, lifetime RuntimeLifetime, logger *slog.Logger) error {
	var gateFailed []string
	for _, name := range supervisorselftest.Properties {
		if props[name] {
			continue
		}
		logger.Warn("supervisor_property_failed", "property", name, "gating", supervisorselftest.Gates(name))
		if supervisorselftest.Gates(name) {
			gateFailed = append(gateFailed, name)
		}
	}
	if lifetime == LifetimeRestart && len(gateFailed) > 0 {
		return fmt.Errorf("await supervisor: gating properties failed on a restart pod: %v", gateFailed)
	}
	return nil
}

func loggerOr(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

// pendingRequest is the one request the link has outstanding.
type pendingRequest struct {
	reply string
	gen   uint64
	id    uint64
	ch    chan superviseMessage
}

// SupervisorLink is the adapter's accepted CH-SUPERVISE connection. It
// carries at most one request at a time, matches each reply to it by type,
// gen, and id, and reports a runtime process's exit through the exited
// handler. Its failure is sticky: once the link has failed every request
// returns ErrSuperviseLinkFailed without writing, and the failure handler
// runs once. spec: §28.5.3 (CH-SUPERVISE).
type SupervisorLink struct {
	conn   net.Conn
	reader *bufio.Reader
	logger *slog.Logger
	// properties is the self-test result ready carried. Written once before
	// the reader starts.
	properties map[string]bool

	// reqMu serializes requests, so at most one is outstanding. The ping
	// takes it with TryLock, so a ping is sent only while no other request
	// is outstanding. spec: §28.5.3 (CH-SUPERVISE, Preconditions).
	reqMu sync.Mutex

	// mu guards the fields below.
	mu      sync.Mutex
	pending *pendingRequest
	nextID  uint64
	// gen is the generation of the latest launch the adapter sent, 0 before
	// the first. An exited of another generation is a link failure.
	gen       uint64
	failed    bool
	failErr   error
	onFailure func(error)
	onExited  func(gen uint64, exit RuntimeExit)

	failedCh chan struct{}
	readDone chan struct{}
}

func newSupervisorLink(conn net.Conn, logger *slog.Logger) *SupervisorLink {
	return &SupervisorLink{
		conn:     conn,
		reader:   bufio.NewReaderSize(conn, 4096),
		logger:   loggerOr(logger),
		failedCh: make(chan struct{}),
		readDone: make(chan struct{}),
	}
}

// Properties returns the self-test result the supervisor's ready reported.
func (l *SupervisorLink) Properties() map[string]bool {
	out := make(map[string]bool, len(l.properties))
	for k, v := range l.properties {
		out[k] = v
	}
	return out
}

// setHandlers installs the failure and exited handlers. A failure that
// already happened is delivered at once. Neither handler may block.
func (l *SupervisorLink) setHandlers(onFailure func(error), onExited func(uint64, RuntimeExit)) {
	l.mu.Lock()
	l.onFailure = onFailure
	l.onExited = onExited
	failed, err := l.failed, l.failErr
	l.mu.Unlock()
	if failed && onFailure != nil {
		onFailure(err)
	}
}

// Failed reports whether the link has failed.
func (l *SupervisorLink) Failed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failed
}

// Close closes the connection, on which the supervisor exits and ends every
// process in the runtime container. The close is the adapter's own and is not
// reported as a link failure to the failure handler.
func (l *SupervisorLink) Close() error {
	l.mu.Lock()
	l.onFailure = nil
	l.onExited = nil
	l.mu.Unlock()
	l.fail(errors.New("closed by the adapter"))
	<-l.readDone
	return nil
}

// readReady reads the first line, which must be ready, within ctx. The read
// ends at ctx's deadline and also when ctx is cancelled: RunPreReady's signal
// path cancels the gate, and a gate still blocked in this read would hold the
// adapter until the wait timeout rather than letting main return.
// spec: §4.7.9 (step 4); §6.1 (Adapter SIGTERM behavior during
// sdk_connecting).
func (l *SupervisorLink) readReady(ctx context.Context) (map[string]bool, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = l.conn.SetReadDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = l.conn.SetReadDeadline(time.Now()) })
	line, err := readSuperviseLine(l.reader)
	stop()
	_ = l.conn.SetReadDeadline(time.Time{})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("await supervisor: read ready: %w", ctxErr)
		}
		return nil, fmt.Errorf("await supervisor: read ready: %w", err)
	}
	m, err := decodeSuperviseMessage(line)
	if err != nil {
		return nil, fmt.Errorf("await supervisor: %w", err)
	}
	if m.Type != superviseReady {
		return nil, fmt.Errorf("await supervisor: %w: first message is %s, not ready", errSuperviseDecode, m.Type)
	}
	return m.Properties, nil
}

// readSuperviseLine reads one line bounded by superviseMaxLine, without its
// terminator.
func readSuperviseLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > superviseMaxLine {
			return nil, fmt.Errorf("%w: line longer than %d bytes", errSuperviseDecode, superviseMaxLine)
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// readLoop reads every message after ready until the link fails.
func (l *SupervisorLink) readLoop() {
	defer close(l.readDone)
	for {
		line, err := readSuperviseLine(l.reader)
		if err != nil {
			l.fail(fmt.Errorf("connection: %w", err))
			return
		}
		m, err := decodeSuperviseMessage(line)
		if err != nil {
			l.fail(err)
			return
		}
		if err := l.dispatch(m); err != nil {
			l.fail(err)
			return
		}
	}
}

// dispatch routes one decoded message. A second ready, a reply that matches
// no outstanding request, and an exited of another generation are link
// failures. spec: §28.5.3 (CH-SUPERVISE, Degradation).
func (l *SupervisorLink) dispatch(m superviseMessage) error {
	switch m.Type {
	case superviseReady:
		return fmt.Errorf("%w: a second ready", errSuperviseDecode)
	case superviseExited:
		l.mu.Lock()
		gen, onExited := l.gen, l.onExited
		l.mu.Unlock()
		if m.Gen != gen {
			return fmt.Errorf("%w: exited for generation %d, current %d", errSuperviseDecode, m.Gen, gen)
		}
		if onExited != nil {
			onExited(m.Gen, m.Exit)
		}
		return nil
	default:
		l.mu.Lock()
		p := l.pending
		if p == nil || p.reply != m.Type || p.gen != m.Gen || p.id != m.ID {
			l.mu.Unlock()
			return fmt.Errorf("%w: %s gen %d id %d matches no outstanding request", errSuperviseDecode, m.Type, m.Gen, m.ID)
		}
		l.pending = nil
		l.mu.Unlock()
		p.ch <- m
		return nil
	}
}

// fail records the link failure once, closes the connection, and runs the
// failure handler.
func (l *SupervisorLink) fail(err error) {
	l.mu.Lock()
	if l.failed {
		l.mu.Unlock()
		return
	}
	l.failed = true
	l.failErr = err
	onFailure := l.onFailure
	l.mu.Unlock()
	close(l.failedCh)
	_ = l.conn.Close()
	if onFailure != nil {
		l.logger.Warn(LogSuperviseLinkFailure, "error", err)
		onFailure(err)
	}
}

// request sends one request and waits for its reply. bound is the request's
// own bound; a deadline on ctx that falls earlier replaces it. Cancellation of
// ctx without a deadline does not abandon the request, because an abandoned
// request's late reply would match nothing and fail the link. A request
// unanswered within its bound is a link failure. spec: §28.5.3 (CH-SUPERVISE,
// Timing).
func (l *SupervisorLink) request(ctx context.Context, req superviseRequest, bound time.Duration) (superviseMessage, error) {
	l.reqMu.Lock()
	defer l.reqMu.Unlock()
	return l.requestLocked(ctx, req, bound)
}

// requestLocked is request with reqMu held.
func (l *SupervisorLink) requestLocked(ctx context.Context, req superviseRequest, bound time.Duration) (superviseMessage, error) {
	if dl, ok := ctx.Deadline(); ok {
		if until := time.Until(dl); until < bound {
			bound = until
		}
	}
	p, err := l.register(&req)
	if err != nil {
		return superviseMessage{}, err
	}
	line, err := encodeSuperviseRequest(req)
	if err != nil {
		l.clearPending(p)
		return superviseMessage{}, err
	}
	_ = l.conn.SetWriteDeadline(time.Now().Add(bound))
	if _, err := l.conn.Write(line); err != nil {
		l.fail(fmt.Errorf("write %s: %w", req.Type, err))
		return superviseMessage{}, fmt.Errorf("%w: write %s: %v", ErrSuperviseLinkFailed, req.Type, err)
	}
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case m := <-p.ch:
		return m, nil
	case <-l.failedCh:
		return superviseMessage{}, fmt.Errorf("%w: %s", ErrSuperviseLinkFailed, req.Type)
	case <-timer.C:
		l.fail(fmt.Errorf("%s unanswered within %s", req.Type, bound))
		return superviseMessage{}, fmt.Errorf("%w: %s unanswered within %s", ErrSuperviseLinkFailed, req.Type, bound)
	}
}

// register assigns the request its id and records it as outstanding. A
// launch makes its generation the current one, before any reply or exited for
// it can arrive. A ping is stamped with the current generation, the one the
// latest launch sent on this link assigned, so a ping never names a
// generation whose launch the supervisor has not been sent. The caller holds
// reqMu. spec: §28.5.3 (CH-SUPERVISE, Messages; CH-SUPERVISE, Preconditions).
func (l *SupervisorLink) register(req *superviseRequest) (*pendingRequest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed {
		return nil, fmt.Errorf("%w: %s not sent", ErrSuperviseLinkFailed, req.Type)
	}
	l.nextID++
	req.ID = l.nextID
	switch req.Type {
	case superviseLaunch:
		l.gen = req.Gen
	case supervisePing:
		req.Gen = l.gen
	}
	p := &pendingRequest{reply: superviseReplyType[req.Type], gen: req.Gen, id: req.ID, ch: make(chan superviseMessage, 1)}
	l.pending = p
	return p, nil
}

func (l *SupervisorLink) clearPending(p *pendingRequest) {
	l.mu.Lock()
	if l.pending == p {
		l.pending = nil
	}
	l.mu.Unlock()
}

// RunPing sends ping every interval while no other request is outstanding and
// fails the link when a pong misses timeout. Each ping carries the
// generation of the latest launch sent on the link. It returns when ctx ends
// or the link fails. spec: §28.5.3 (CH-SUPERVISE, Preconditions; CH-SUPERVISE,
// Timing); §4.7.10 (Runtime supervisor, Liveness).
func (l *SupervisorLink) RunPing(ctx context.Context, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.failedCh:
			return
		case <-t.C:
		}
		if !l.reqMu.TryLock() {
			continue
		}
		_, err := l.requestLocked(context.Background(), superviseRequest{Type: supervisePing}, timeout)
		l.reqMu.Unlock()
		if err != nil {
			return
		}
	}
}
