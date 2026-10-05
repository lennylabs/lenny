// SPDX-License-Identifier: MIT

// Package runtime is the Lenny Go runtime-author SDK. It lets a
// developer write a Lenny agent runtime in Go by implementing the
// Handler interface and calling Run; the SDK drives the §28.5.3 adapter
// binary protocol, the §15.4.2 RPC lifecycle state machine, the §8.5
// platform MCP tool helpers, and the Full-level CH-RUNTIMEOPS.
//
// This SDK is the runtime-author counterpart of the client SDK at
// sdks/client/go/lenny. The client SDK wraps the gateway REST API for
// application developers; this SDK targets the agent process that runs
// inside a Lenny-managed pod and speaks the Runtime Adapter Specification
// (§15.4).
//
// # Integration levels
//
// The SDK covers the §15.4.3 integration levels:
//
//   - Basic: the stdin/stdout JSON Lines protocol. Run with no
//     options exercises Basic level fully: session_start and
//     session_end per session, message/response round trip, heartbeat
//     acknowledgement, shutdown within the deadline, and
//     forward-compatible handling of unknown frame types.
//   - Standard: the SDK additionally dials the manifest-advertised
//     platform MCP server and connector MCP servers with the §15.4.3
//     manifest-nonce handshake, and exposes typed §8.5 platform tool
//     helpers through the Tools value passed to handlers.
//   - Full: the SDK additionally opens the §15.4.3 CH-RUNTIMEOPS,
//     completes the lifecycle_capabilities / lifecycle_support
//     handshake, and surfaces checkpoint, interrupt, credential
//     rotation, and deadline events on the CH-RUNTIMEOPS, each routed
//     to the session the event names.
//
// # Sessions
//
// One runtime process serves every session the pod holds. Each
// session_start opens a session with its own context, goroutine, and
// message queue, and each session_end releases it, so OnCreate,
// OnMessage, and OnTerminate run once per session and concurrently across
// sessions (§4.7.10, §15.7).
//
// # Minimal runtime
//
// A Basic-level echo runtime is a Handler whose OnMessage echoes the
// inbound parts:
//
//	type echo struct{}
//
//	func (echo) OnCreate(context.Context, runtime.CreateRequest) error { return nil }
//	func (echo) OnTerminate(context.Context, string, runtime.TerminationReason) error { return nil }
//	func (echo) OnMessage(_ context.Context, m runtime.Message) (runtime.Reply, error) {
//	    return runtime.Reply{Parts: m.Envelope.Input, Final: true}, nil
//	}
//
//	func main() {
//	    if err := runtime.Run(echo{}); err != nil {
//	        os.Exit(1)
//	    }
//	}
//
// # Transport
//
// The §28.5.3 JSON Lines framing is identical under both §4.7 deployment
// models; only the byte transport differs. By default Run reads os.Stdin
// and writes os.Stdout. When the adapter runs as a separate container it
// sets LENNY_ADAPTER_SOCKET to an abstract Unix socket name; Run dials
// that socket when WithSocketTransport is enabled (the default).
package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Handler is the single interface a runtime author implements. One
// runtime process serves any number of sessions, one after another and
// side by side. The SDK invokes OnCreate when a session_start opens a
// session, and writes the session's session_started once OnCreate
// returns; OnMessage for each of the session's messages; and OnTerminate
// once when that session ends, on its session_end or at the end of the
// connection. Calls for different sessions run concurrently on different
// goroutines, so an implementation keeps per-session state keyed by
// session and is safe for concurrent use. Calls for one session never
// overlap. A session whose OnCreate fails is answered as the CH-MSGSOCK
// Session errors rule states, and the process keeps serving its other
// sessions.
//
// spec: §15.7 (API surface, Handler), §4.7.10 (runtime process lifetime),
// §28.5.3 (CH-MSGSOCK, Inbound: session_start, Session errors).
type Handler interface {
	// OnCreate receives the session's context snapshot before the
	// session's first Message is delivered. A non-nil error fails the
	// session's creation: the SDK writes session_started with error and
	// answers the session's messages with a RUNTIME_ERROR response.
	OnCreate(ctx context.Context, req CreateRequest) error
	// OnMessage handles one inbound message and returns the turn's
	// Reply. A non-nil error is reported to the adapter as a structured
	// response error and the session continues with its next message.
	OnMessage(ctx context.Context, msg Message) (Reply, error)
	// OnTerminate runs once when the session ends, after the session's
	// last handler call returned. It SHOULD return before the shutdown
	// deadline elapses.
	OnTerminate(ctx context.Context, sessionID string, reason TerminationReason) error
}

// ProtocolError signals a non-recoverable inbound-format violation. Run
// returns it wrapped; an entrypoint maps it to the §15.4 protocol-error
// exit code (2). Use ErrIsProtocol to test for it.
type ProtocolError struct{ Msg string }

// Error implements error.
func (e ProtocolError) Error() string { return "protocol error: " + e.Msg }

// ErrIsProtocol reports whether err is or wraps a ProtocolError. An
// entrypoint uses it to select the §15.4 protocol-error exit code.
func ErrIsProtocol(err error) bool {
	var pe ProtocolError
	return errors.As(err, &pe)
}

// maxFrameBytes caps an inbound JSON Lines frame at the §28.5.3
// MessagePart hard limit. A larger frame is a protocol error.
const maxFrameBytes = 50 * 1024 * 1024

// socketEnvVar is the §4.7 environment variable the adapter sets on the
// runtime container in the sidecar deployment model. Its value is the
// adapter's abstract Unix socket name.
const socketEnvVar = "LENNY_ADAPTER_SOCKET"

// manifestEnvVar overrides the §4.7 adapter manifest path. The default
// path is /run/lenny/adapter-manifest.json.
const manifestEnvVar = "LENNY_ADAPTER_MANIFEST"

// defaultManifestPath is the §4.7 adapter manifest path.
const defaultManifestPath = "/run/lenny/adapter-manifest.json"

// Run wires up the §28.5.3 stdin/stdout framing, dials the
// manifest-advertised abstract Unix sockets (platform MCP server,
// connector MCP servers, CH-RUNTIMEOPS) once per process with the
// §15.4.3 manifest-nonce handshake, and drives the frame loop. Each
// session_start opens a session with its own context and goroutine, and
// each session loads its credentials from the path its session_start
// names. Run blocks until the adapter closes the inbound stream or sends
// a shutdown frame, then ends every session the process holds and
// returns nil on a clean exit.
//
// Run with no options covers the Basic level. WithStandardLevel and
// WithFullLevel opt into the higher integration levels.
//
// spec: §15.7 (API surface, Run), §4.7.10 (runtime process lifetime).
func Run(h Handler, opts ...Option) error {
	if h == nil {
		return errors.New("runtime: Run requires a non-nil Handler")
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return newProcess(h, cfg).run(context.Background())
}

// process holds the SDK state of one Run call. The manifest, the MCP
// connections, and CH-RUNTIMEOPS are process-scoped and shared by every
// session; each session's own context lives in its sessionState.
type process struct {
	handler Handler
	cfg     config
	w       *frameWriter

	// ctx is the process context every session context derives from.
	ctx context.Context

	manifest  *AdapterManifest
	tools     *Tools
	lifecycle *Lifecycle

	sessions *sessionTable
	// wg counts the session goroutines, including a state a session_end
	// removed whose context is still being released.
	wg sync.WaitGroup

	state atomic.Int32 // adapterState

	// exitReason holds the TerminationReason resolved by the shutdown
	// frame, if any. run reads it after the frame loop ends.
	exitReasonMu sync.Mutex
	exitReason   *TerminationReason
}

// adapterState enumerates the §15.4.2 RPC lifecycle states the SDK
// tracks on behalf of the runtime. The adapter owns the authoritative
// state machine; the SDK mirrors it so handlers can observe progress.
type adapterState int32

const (
	stateInit adapterState = iota
	stateReady
	stateActive
	stateDraining
	stateTerminated
)

func (s adapterState) String() string {
	switch s {
	case stateInit:
		return "INIT"
	case stateReady:
		return "READY"
	case stateActive:
		return "ACTIVE"
	case stateDraining:
		return "DRAINING"
	case stateTerminated:
		return "TERMINATED"
	default:
		return "UNKNOWN"
	}
}

// newProcess assembles a process from the handler and resolved config.
func newProcess(h Handler, cfg config) *process {
	return &process{handler: h, cfg: cfg, sessions: newSessionTable(), ctx: context.Background()}
}

// run drives one runtime process: resolve the transport, load the
// manifest, dial the higher-level channels for the configured level, run
// the §28.5.3 frame loop, and end every session once the loop ends.
func (p *process) run(ctx context.Context) error {
	p.state.Store(int32(stateInit))

	transport, err := p.cfg.openTransport(ctx)
	if err != nil {
		return fmt.Errorf("runtime: resolve transport: %w", err)
	}
	defer transport.Close()

	p.w = newFrameWriter(transport.Writer)

	// §4.7 manifest. It is optional: a Basic-level runtime is exercised
	// without one. A malformed manifest is a hard error only when a
	// higher integration level needs it.
	p.loadManifest()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.ctx = ctx

	if err := p.startChannels(ctx, cancel); err != nil {
		return err
	}
	defer p.closeChannels()

	// A CH-RUNTIMEOPS terminate event cancels ctx while the frame
	// loop may be blocked on a stdin read. Closing the transport on
	// cancellation unblocks that read so the loop observes EOF and the
	// runtime exits. For the stdin/stdout transport Close is a no-op and
	// the adapter's stdin close drives the exit instead.
	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		<-ctx.Done()
		_ = transport.Close()
	}()
	defer func() {
		cancel()
		<-closerDone
	}()

	p.state.Store(int32(stateReady))

	loopErr := p.loop(ctx, transport.Reader, cancel)

	// Every live session drains the messages it queued and then runs
	// OnTerminate with the shutdown frame's reason, or stdin_closed when
	// the adapter closed the transport without one. Run returns after
	// every session goroutine returned, including one a session_end
	// removed whose release is still running.
	p.closeSessions(p.terminationReason())
	p.wg.Wait()
	p.state.Store(int32(stateTerminated))
	return loopErr
}

// terminationReason is the reason EOF or shutdown ends the live sessions
// with.
func (p *process) terminationReason() TerminationReason {
	p.exitReasonMu.Lock()
	defer p.exitReasonMu.Unlock()
	if p.exitReason != nil {
		return *p.exitReason
	}
	return TerminationReason{Reason: "stdin_closed"}
}

// setExitReason records the reason the process is ending with.
func (p *process) setExitReason(r TerminationReason) {
	p.exitReasonMu.Lock()
	p.exitReason = &r
	p.exitReasonMu.Unlock()
}

// startChannels dials the §15.4.3 platform MCP server, connector MCP
// servers, and CH-RUNTIMEOPS for the configured integration level, once
// per process. cancel lets a CH-RUNTIMEOPS terminate event stop the
// frame loop.
//
// When a higher-level channel is configured but the adapter manifest
// does not advertise it (no manifest, or a manifest without the socket
// fields), the SDK logs the gap and degrades to the highest level the
// manifest supports. A Standard- or Full-level binary therefore still
// runs in a Basic-only environment: the conformance harness exercises
// the Basic checks against such a binary without a manifest. A dial
// that fails after the socket is advertised is a hard error, since the
// adapter promised a channel the runtime could not reach.
//
// spec: §15.7 (Run dials the sockets once per process).
func (p *process) startChannels(ctx context.Context, cancel context.CancelFunc) error {
	if p.cfg.level >= levelStandard {
		switch {
		case !p.manifestHasPlatformMCP():
			p.cfg.logf("runtime: no platform MCP server in the manifest; degrading to Basic level")
		default:
			tools, err := p.dialTools(ctx)
			if err != nil {
				return fmt.Errorf("runtime: Standard-level MCP setup: %w", err)
			}
			p.tools = tools
		}
	}
	if p.cfg.level >= levelFull {
		switch {
		case !p.manifestHasLifecycle():
			p.cfg.logf("runtime: the manifest advertises no CH-RUNTIMEOPS socket; lifecycle features disabled")
		default:
			lc, err := p.dialLifecycle(ctx, cancel)
			if err != nil {
				return fmt.Errorf("runtime: Full-level lifecycle setup: %w", err)
			}
			p.lifecycle = lc
		}
	}
	return nil
}

// manifestHasPlatformMCP reports whether the manifest advertises a
// platform MCP server socket.
func (p *process) manifestHasPlatformMCP() bool {
	return p.manifest != nil &&
		p.manifest.PlatformMCPServer != nil &&
		p.manifest.PlatformMCPServer.Socket != ""
}

// manifestHasLifecycle reports whether the manifest advertises a
// CH-RUNTIMEOPS socket.
func (p *process) manifestHasLifecycle() bool {
	return p.manifest != nil &&
		p.manifest.RuntimeOps != nil &&
		p.manifest.RuntimeOps.Socket != ""
}

// closeChannels releases the higher-level channels opened by
// startChannels.
func (p *process) closeChannels() {
	if p.tools != nil {
		p.tools.close()
	}
	if p.lifecycle != nil {
		p.lifecycle.close()
	}
}

// loop is the §28.5.3 frame loop. It reads newline-delimited JSON from
// in and routes each frame by type without blocking on any session's
// work: session_start, message, and session_end go to the addressed
// session's state, heartbeat and tool_result are serviced inline, and a
// shutdown frame ends the loop. Unknown frame types are ignored for
// forward compatibility. It returns when in reaches EOF, a shutdown
// frame arrives, or an unrecoverable error occurs.
//
// spec: §28.5.3 (CH-MSGSOCK).
func (p *process) loop(ctx context.Context, in io.Reader, cancel context.CancelFunc) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		done, err := p.routeFrame(line)
		if err != nil || done {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("input read error: %v", err)}
	}
	return nil
}

// routeFrame handles one inbound frame. done reports a shutdown frame,
// which ends the loop.
func (p *process) routeFrame(line []byte) (done bool, err error) {
	var ft frameType
	if err := json.Unmarshal(line, &ft); err != nil {
		return false, ProtocolError{Msg: fmt.Sprintf("malformed JSON Lines on input: %v", err)}
	}
	switch ft.Type {
	case "session_start":
		return false, p.handleSessionStart(line)
	case "session_end":
		p.handleSessionEnd(line)
	case "message":
		p.state.Store(int32(stateActive))
		env, perr := decodeMessage(line)
		if perr != nil {
			return false, perr
		}
		p.routeMessage(env)
	case "heartbeat":
		if err := p.w.write(outboundHeartbeatAck{Type: "heartbeat_ack"}); err != nil {
			return false, fmt.Errorf("runtime: write heartbeat_ack: %w", err)
		}
	case "tool_result":
		p.handleToolResult(line)
	case "shutdown":
		return true, p.handleShutdown(line)
	default:
		p.cfg.logf("runtime: ignoring unknown frame type %q", ft.Type)
	}
	return false, nil
}

// decodeMessage decodes one §28.5.3 message frame. A malformed frame is
// a non-recoverable protocol error.
func decodeMessage(line []byte) (*MessageEnvelope, error) {
	var env MessageEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, ProtocolError{Msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}
	return &env, nil
}

// handleMessage invokes OnMessage for one of the session's messages and
// writes the resulting response frame. It runs on the session's
// goroutine, one message at a time. A handler error is reported as a
// structured response error so the adapter records the failure without
// losing context (§28.5.3 error reporting via response). A response for
// a session that already ended is dropped.
func (p *process) handleMessage(st *sessionState, env *MessageEnvelope) {
	msg := Message{
		Envelope:  env,
		SessionID: st.id,
		TaskID:    st.id,
		Sequence:  st.seq.Add(1),
	}

	// §28.5.3 adapter-local tools are reachable for the duration of the
	// turn. Every tool_call carries the session's identifier and is
	// dropped once the session ended.
	// spec: §28.5.3 (sessionId on every session-scoped frame)
	mctx := context.WithValue(p.withSessionContext(st), ctxKeyAdapterTools, &AdapterTools{
		w:       p.w,
		timeout: p.cfg.dialTimeout,
		owner:   st,
	})
	reply, err := p.handler.OnMessage(mctx, msg)
	if err != nil {
		p.cfg.logf("runtime: session %s: OnMessage error: %v", st.id, err)
		p.writeFor(st, outboundResponse{
			Type:      "response",
			Output:    []MessagePart{},
			Error:     &ResponseError{Code: "RUNTIME_ERROR", Message: err.Error()},
			SessionID: st.id,
		})
		return
	}

	// A turn marked Streaming and not Final defers the response frame —
	// the runtime emits the terminal Reply on a later turn or via the
	// lenny/output platform MCP tool. The §28.5.3 contract still requires
	// a final response frame, so the SDK emits it once the runtime
	// returns a Final Reply.
	if reply.Streaming && !reply.Final {
		return
	}
	p.writeFor(st, outboundResponse{
		Type:      "response",
		Output:    stampParts(reply.Parts),
		Error:     reply.Error,
		SessionID: st.id,
	})
}

// handleToolResult routes an inbound §28.5.3 tool_result frame to the
// pending stdout tool_call that emitted the matching id. A result with
// no pending call is dropped and logged (§28.5.3 correlation rule).
func (p *process) handleToolResult(line []byte) {
	var tr inboundToolResult
	if err := json.Unmarshal(line, &tr); err != nil {
		p.cfg.logf("runtime: malformed tool_result frame: %v", err)
		return
	}
	if !p.w.deliverToolResult(tr) {
		p.cfg.logf("runtime: tool_result %q has no pending tool_call", tr.ID)
	}
}

// handleShutdown decodes the §28.5.3 shutdown frame and records the
// termination reason that run hands every live session after the loop
// ends. Shutdown is process-scoped: every session drains its queued
// messages before its OnTerminate.
func (p *process) handleShutdown(line []byte) error {
	p.state.Store(int32(stateDraining))
	var sd inboundShutdown
	if err := json.Unmarshal(line, &sd); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
	}
	p.setExitReason(TerminationReason{Reason: sd.Reason, DeadlineMS: sd.DeadlineMS})
	return nil
}

// loadManifest parses the §4.7 adapter manifest. A missing file leaves
// the manifest nil; a malformed file is logged and ignored at Basic
// level and surfaces as a hard error from startChannels at the higher
// levels, which need the socket fields.
func (p *process) loadManifest() {
	path := p.cfg.manifestPath
	data, err := os.ReadFile(path)
	if err != nil {
		p.cfg.logf("runtime: no adapter manifest at %s (%v)", path, err)
		return
	}
	var m AdapterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		p.cfg.logf("runtime: malformed adapter manifest %s: %v", path, err)
		return
	}
	// §4.7 forward-compatibility rule: a manifest version newer than the
	// SDK understands is rejected. Every increment is breaking.
	if m.Version > 1 {
		p.cfg.logf("runtime: adapter manifest %s version %d is newer than supported (1)", path, m.Version)
		return
	}
	p.manifest = &m
}

// stampParts sets SchemaVersion on every part that left it zero,
// honoring the §28.5.3 producer obligation, and returns a non-nil slice
// so an empty Reply still serializes as output: [].
func stampParts(parts []MessagePart) []MessagePart {
	out := make([]MessagePart, len(parts))
	for i, p := range parts {
		if p.SchemaVersion == 0 {
			p.SchemaVersion = schemaVersion
		}
		out[i] = p
	}
	return out
}

// --- frame writer ------------------------------------------------------

// frameWriter serializes §28.5.3 outbound frames. Every write is
// followed by a flush before the next inbound read, honoring the
// §28.5.3 stdout-flushing requirement. It also correlates outbound
// tool_call frames with inbound tool_result frames, and holds the drop
// mark of every ended session under its lock (sessionState.ended).
type frameWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	out io.Writer

	pending map[string]pendingToolCall
}

// pendingToolCall is a tool_call awaiting its tool_result. owner is the
// session whose turn issued it.
type pendingToolCall struct {
	ch    chan inboundToolResult
	owner *sessionState
}

// errSessionEnded is returned for a frame addressed to a session whose
// session_end the runtime already read. The frame is not written.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
var errSessionEnded = errors.New("session ended; frame dropped")

func newFrameWriter(w io.Writer) *frameWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &frameWriter{enc: enc, out: w, pending: map[string]pendingToolCall{}}
}

// write serializes one frame and flushes it. json.Encoder.Encode writes
// the trailing newline; a bufio writer behind out, if any, is flushed.
func (w *frameWriter) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encodeLocked(v)
}

// writeFor writes a frame addressed to owner, or drops it and returns
// errSessionEnded when the owner's session already ended. The check and
// the write happen under one lock, so no frame for the session follows
// the frame loop's read of its session_end.
func (w *frameWriter) writeFor(owner *sessionState, v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if owner != nil && owner.ended {
		return errSessionEnded
	}
	return w.encodeLocked(v)
}

func (w *frameWriter) encodeLocked(v any) error {
	if err := w.enc.Encode(v); err != nil {
		return err
	}
	if f, ok := w.out.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// endOwner marks owner ended and cancels its pending tool_call waiters,
// whose calls then return an error at once.
func (w *frameWriter) endOwner(owner *sessionState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	owner.ended = true
	for id, pc := range w.pending {
		if pc.owner == owner {
			delete(w.pending, id)
			close(pc.ch)
		}
	}
}

// registerToolCall records a pending §28.5.3 tool_call id for owner and
// returns the channel its tool_result arrives on. The channel is closed
// without a value when the owner's session ends first.
func (w *frameWriter) registerToolCall(id string, owner *sessionState) chan inboundToolResult {
	ch := make(chan inboundToolResult, 1)
	w.mu.Lock()
	w.pending[id] = pendingToolCall{ch: ch, owner: owner}
	w.mu.Unlock()
	return ch
}

// deliverToolResult routes an inbound tool_result to the channel of its
// matching tool_call. It reports whether a pending call was found.
func (w *frameWriter) deliverToolResult(tr inboundToolResult) bool {
	w.mu.Lock()
	pc, ok := w.pending[tr.ID]
	if ok {
		delete(w.pending, tr.ID)
	}
	w.mu.Unlock()
	if !ok {
		return false
	}
	pc.ch <- tr
	return true
}

// cancelToolCall drops a pending tool_call registration.
func (w *frameWriter) cancelToolCall(id string) {
	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
}

// --- transport ---------------------------------------------------------

// transport is the resolved §28.5.3 byte transport.
type transport struct {
	Reader io.Reader
	Writer io.Writer
	closer io.Closer
}

// Close releases the transport. It is a no-op for stdin/stdout.
func (t *transport) Close() error {
	if t.closer == nil {
		return nil
	}
	return t.closer.Close()
}

// openTransport resolves the §28.5.3 transport. When socket transport is
// enabled and LENNY_ADAPTER_SOCKET names an adapter socket, it dials
// that abstract Unix socket; otherwise it returns os.Stdin/os.Stdout.
func (c config) openTransport(ctx context.Context) (*transport, error) {
	if c.reader != nil || c.writer != nil {
		r := c.reader
		if r == nil {
			r = os.Stdin
		}
		wtr := c.writer
		if wtr == nil {
			wtr = os.Stdout
		}
		return &transport{Reader: r, Writer: wtr}, nil
	}
	if c.socketTransport {
		if name := strings.TrimSpace(os.Getenv(socketEnvVar)); name != "" {
			conn, err := dialUnixSocket(ctx, name, c.dialTimeout)
			if err != nil {
				return nil, fmt.Errorf("dial adapter socket %q: %w", name, err)
			}
			return &transport{Reader: conn, Writer: conn, closer: conn}, nil
		}
	}
	return &transport{Reader: os.Stdin, Writer: os.Stdout}, nil
}

// dialUnixSocket dials a Unix socket. A name beginning with @ is a Linux
// abstract address; the @ is translated to a leading NUL. A filesystem
// path is dialed as-is so the helper also works off Linux. The dial is
// retried within timeout to absorb a startup race with the listener.
func dialUnixSocket(ctx context.Context, name string, timeout time.Duration) (netConn, error) {
	addr := name
	if strings.HasPrefix(name, "@") {
		addr = "\x00" + name[1:]
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := dialContext(ctx, "unix", addr)
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
