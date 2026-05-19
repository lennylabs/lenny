// SPDX-License-Identifier: MIT

// Package runtime is the Lenny Go runtime-author SDK. It lets a
// developer write a Lenny agent runtime in Go by implementing the
// Handler interface and calling Run; the SDK drives the §15.4.1 adapter
// binary protocol, the §15.4.2 RPC lifecycle state machine, the §8.5
// platform MCP tool helpers, and the Full-level lifecycle channel.
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
//     options exercises Basic level fully — message/response round
//     trip, heartbeat acknowledgement, shutdown within the deadline,
//     and forward-compatible handling of unknown frame types.
//   - Standard: the SDK additionally dials the manifest-advertised
//     platform MCP server and connector MCP servers with the §15.4.3
//     manifest-nonce handshake, and exposes typed §8.5 platform tool
//     helpers through the Tools value passed to handlers.
//   - Full: the SDK additionally opens the §15.4.3 lifecycle channel,
//     completes the lifecycle_capabilities / lifecycle_support
//     handshake, and surfaces checkpoint, interrupt, credential
//     rotation, and deadline events on the Lifecycle channel.
//
// # Minimal runtime
//
// A Basic-level echo runtime is a Handler whose OnMessage echoes the
// inbound parts:
//
//	type echo struct{}
//
//	func (echo) OnCreate(context.Context, runtime.CreateRequest) error { return nil }
//	func (echo) OnTerminate(context.Context, runtime.TerminationReason) error { return nil }
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
// The §15.4.1 JSON Lines framing is identical under both §4.7 deployment
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

// Handler is the single interface a runtime author implements. The SDK
// invokes OnCreate once before the first message of a task, OnMessage
// for every inbound §15.4.1 message frame, and OnTerminate once when the
// adapter closes stdin or sends a shutdown frame.
//
// In §5.2 task-mode pools the SDK invokes OnCreate again with a new
// TaskID after each task_ready lifecycle signal; in session-mode pools
// OnCreate is invoked once.
type Handler interface {
	// OnCreate receives the task-scoped context snapshot before the
	// first Message is delivered. A non-nil error aborts the runtime.
	OnCreate(ctx context.Context, req CreateRequest) error
	// OnMessage handles one inbound message and returns the turn's
	// Reply. A non-nil error is reported to the adapter as a structured
	// response error and the runtime continues with the next frame.
	OnMessage(ctx context.Context, msg Message) (Reply, error)
	// OnTerminate runs once when the session ends. It SHOULD return
	// before the shutdown deadline elapses.
	OnTerminate(ctx context.Context, reason TerminationReason) error
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

// maxFrameBytes caps an inbound JSON Lines frame at the §15.4.1
// OutputPart hard limit. A larger frame is a protocol error.
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

// defaultCredentialsPath is the §4.7 runtime credential file path.
const defaultCredentialsPath = "/run/lenny/credentials.json"

// Run wires up the §15.4.1 stdin/stdout framing, optionally dials the
// manifest-advertised abstract Unix sockets (platform MCP server,
// connector MCP servers, lifecycle channel) with the §15.4.3
// manifest-nonce handshake, parses the §4.7 credential file, and drives
// the §15.4.2 dispatch loop. It blocks until the adapter closes the
// inbound stream or sends a shutdown frame and returns nil on a clean
// exit.
//
// Run with no options covers the Basic level. WithStandardLevel and
// WithFullLevel opt into the higher integration levels.
func Run(h Handler, opts ...Option) error {
	if h == nil {
		return errors.New("runtime: Run requires a non-nil Handler")
	}
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return newSession(h, cfg).run(context.Background())
}

// session holds the per-process SDK state for one Run call.
type session struct {
	handler Handler
	cfg     config
	w       *frameWriter

	manifest    *AdapterManifest
	credentials *CredentialBundle
	credMu      sync.RWMutex

	tools     *Tools
	lifecycle *Lifecycle

	state      atomic.Int32 // adapterState
	seq        atomic.Uint64
	terminated atomic.Bool

	// exitReason holds the TerminationReason resolved by the shutdown
	// frame, if any. run reads it after the dispatch worker drains.
	exitReasonMu sync.Mutex
	exitReason   *TerminationReason
}

// adapterState enumerates the §15.4.2 RPC lifecycle states the SDK
// tracks on behalf of the runtime. The adapter owns the authoritative
// state machine; the SDK mirrors it so handlers can observe progress
// and so internal invariants (no dispatch before READY) hold.
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

// newSession assembles a session from the handler and resolved config.
func newSession(h Handler, cfg config) *session {
	return &session{handler: h, cfg: cfg}
}

// run drives one runtime lifecycle: resolve the transport, load the
// manifest and credentials, dial the higher-level channels for the
// configured level, then run the §15.4.1 frame loop.
func (s *session) run(ctx context.Context) error {
	s.state.Store(int32(stateInit))

	transport, err := s.cfg.openTransport(ctx)
	if err != nil {
		return fmt.Errorf("runtime: resolve transport: %w", err)
	}
	defer transport.Close()

	s.w = newFrameWriter(transport.Writer)

	// §4.7 manifest and credential file. Both are optional: a Basic-level
	// runtime is exercised without a manifest, and a runtime whose pool
	// has no active lease has no credential file. A malformed manifest is
	// a hard error only when a higher integration level needs it.
	s.loadManifest()
	s.loadCredentials()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := s.startChannels(ctx, cancel); err != nil {
		return err
	}
	defer s.closeChannels()

	// A lifecycle-channel terminate event cancels ctx while the frame
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

	s.state.Store(int32(stateReady))

	// OnCreate runs once before the first message with the task-scoped
	// snapshot the SDK assembled from the manifest and credential file.
	if err := s.invokeCreate(ctx); err != nil {
		return fmt.Errorf("runtime: OnCreate: %w", err)
	}

	// The dispatch worker processes message frames one at a time, in the
	// order the loop reads them: this is the §15.4.1 coordinator-local
	// FIFO contract for a session-mode or task-mode stdin. The loop
	// itself keeps reading while a handler is in flight, so a
	// tool_result correlated to a tool_call the handler emits, and
	// heartbeats, are still serviced (§15.4.1 interleaved delivery).
	messages := make(chan *MessageEnvelope, 64)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for env := range messages {
			s.handleMessage(ctx, env)
		}
	}()

	loopErr := s.loop(ctx, transport.Reader, cancel, messages)

	// Close the message channel and wait for the worker to drain every
	// queued message and write its response frame before OnTerminate.
	close(messages)
	<-workerDone

	// OnTerminate runs once on the way out. The reason is the shutdown
	// frame's reason when the loop exited on a shutdown; otherwise the
	// adapter closed the transport without one. A lifecycle terminate,
	// if it fired, already invoked OnTerminate and this call is a no-op.
	reason := TerminationReason{Reason: "stdin_closed"}
	s.exitReasonMu.Lock()
	if s.exitReason != nil {
		reason = *s.exitReason
	}
	s.exitReasonMu.Unlock()
	s.invokeTerminate(ctx, reason)
	s.state.Store(int32(stateTerminated))
	return loopErr
}

// startChannels dials the §15.4.3 platform MCP server, connector MCP
// servers, and lifecycle channel for the configured integration level.
// cancel lets a lifecycle-channel terminate event stop the frame loop.
//
// When a higher-level channel is configured but the adapter manifest
// does not advertise it (no manifest, or a manifest without the socket
// fields), the SDK logs the gap and degrades to the highest level the
// manifest supports. A Standard- or Full-level binary therefore still
// runs in a Basic-only environment: the conformance harness exercises
// the Basic checks against such a binary without a manifest. A dial
// that fails after the socket is advertised is a hard error, since the
// adapter promised a channel the runtime could not reach.
func (s *session) startChannels(ctx context.Context, cancel context.CancelFunc) error {
	if s.cfg.level >= levelStandard {
		switch {
		case !s.manifestHasPlatformMCP():
			s.cfg.logf("runtime: no platform MCP server in the manifest; degrading to Basic level")
		default:
			tools, err := s.dialTools(ctx)
			if err != nil {
				return fmt.Errorf("runtime: Standard-level MCP setup: %w", err)
			}
			s.tools = tools
		}
	}
	if s.cfg.level >= levelFull {
		switch {
		case !s.manifestHasLifecycle():
			s.cfg.logf("runtime: no lifecycle channel in the manifest; lifecycle features disabled")
		default:
			lc, err := s.dialLifecycle(ctx, cancel)
			if err != nil {
				return fmt.Errorf("runtime: Full-level lifecycle setup: %w", err)
			}
			s.lifecycle = lc
		}
	}
	return nil
}

// manifestHasPlatformMCP reports whether the manifest advertises a
// platform MCP server socket.
func (s *session) manifestHasPlatformMCP() bool {
	return s.manifest != nil &&
		s.manifest.PlatformMCPServer != nil &&
		s.manifest.PlatformMCPServer.Socket != ""
}

// manifestHasLifecycle reports whether the manifest advertises a
// lifecycle channel socket.
func (s *session) manifestHasLifecycle() bool {
	return s.manifest != nil &&
		s.manifest.LifecycleChannel != nil &&
		s.manifest.LifecycleChannel.Socket != ""
}

// closeChannels releases the higher-level channels opened by
// startChannels.
func (s *session) closeChannels() {
	if s.tools != nil {
		s.tools.close()
	}
	if s.lifecycle != nil {
		s.lifecycle.close()
	}
}

// loop is the §15.4.1 frame loop. It reads newline-delimited JSON from
// in and routes each frame by type: message frames go to the dispatch
// worker through messages (preserving FIFO), heartbeat and tool_result
// frames are serviced inline so an in-flight handler is not blocked,
// and a shutdown frame ends the loop. Unknown frame types are ignored
// for forward compatibility (§15.4.1). It returns when in reaches EOF,
// a shutdown frame arrives, or an unrecoverable error occurs.
func (s *session) loop(ctx context.Context, in io.Reader, cancel context.CancelFunc, messages chan<- *MessageEnvelope) error {
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
		var ft frameType
		if err := json.Unmarshal(line, &ft); err != nil {
			return ProtocolError{Msg: fmt.Sprintf("malformed JSON Lines on input: %v", err)}
		}
		switch ft.Type {
		case "message":
			s.state.Store(int32(stateActive))
			env, perr := decodeMessage(line)
			if perr != nil {
				return perr
			}
			select {
			case messages <- env:
			case <-ctx.Done():
				return nil
			}
		case "heartbeat":
			if err := s.w.write(outboundHeartbeatAck{Type: "heartbeat_ack"}); err != nil {
				return fmt.Errorf("runtime: write heartbeat_ack: %w", err)
			}
		case "tool_result":
			s.handleToolResult(line)
		case "shutdown":
			return s.handleShutdown(ctx, line, cancel)
		default:
			s.cfg.logf("runtime: ignoring unknown frame type %q", ft.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("input read error: %v", err)}
	}
	return nil
}

// decodeMessage decodes one §15.4.1 message frame. A malformed frame is
// a non-recoverable protocol error.
func decodeMessage(line []byte) (*MessageEnvelope, error) {
	var env MessageEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, ProtocolError{Msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}
	return &env, nil
}

// handleMessage invokes OnMessage for one decoded §15.4.1 message and
// writes the resulting response frame. It runs on the dispatch worker
// goroutine, one message at a time. A handler error is reported as a
// structured response error so the adapter records the failure without
// losing context (§15.4.1 error reporting via response). A write error
// is logged; the SDK continues since the adapter has likely closed the
// transport.
func (s *session) handleMessage(ctx context.Context, env *MessageEnvelope) {
	msg := Message{
		Envelope:  env,
		SessionID: s.manifestSessionID(),
		TaskID:    s.manifestTaskID(),
		Sequence:  s.seq.Add(1),
	}

	// §15.4.1 adapter-local tools are reachable for the duration of the
	// turn. The slot id from the inbound envelope is threaded onto every
	// tool_call so concurrent-workspace runtimes stay correlated.
	mctx := context.WithValue(s.withSessionContext(ctx), ctxKeyAdapterTools, &AdapterTools{
		w:       s.w,
		timeout: s.cfg.dialTimeout,
		slotID:  env.SlotID,
	})
	reply, err := s.handler.OnMessage(mctx, msg)
	if err != nil {
		s.cfg.logf("runtime: OnMessage error: %v", err)
		if werr := s.w.write(outboundResponse{
			Type:   "response",
			Output: []OutputPart{},
			Error:  &ResponseError{Code: "RUNTIME_ERROR", Message: err.Error()},
			SlotID: env.SlotID,
		}); werr != nil {
			s.cfg.logf("runtime: write error response: %v", werr)
		}
		return
	}

	// A turn marked Streaming and not Final defers the response frame —
	// the runtime emits the terminal Reply on a later turn or via the
	// lenny/output platform MCP tool. The §15.4.1 contract still requires
	// a final response frame, so the SDK emits it once the runtime
	// returns a Final Reply.
	if reply.Streaming && !reply.Final {
		return
	}
	if werr := s.w.write(outboundResponse{
		Type:   "response",
		Output: stampParts(reply.Parts),
		Error:  reply.Error,
		SlotID: env.SlotID,
	}); werr != nil {
		s.cfg.logf("runtime: write response: %v", werr)
	}
}

// handleToolResult routes an inbound §15.4.1 tool_result frame to the
// pending stdout tool_call that emitted the matching id. A result with
// no pending call is dropped and logged (§15.4.1 correlation rule).
func (s *session) handleToolResult(line []byte) {
	var tr inboundToolResult
	if err := json.Unmarshal(line, &tr); err != nil {
		s.cfg.logf("runtime: malformed tool_result frame: %v", err)
		return
	}
	if !s.w.deliverToolResult(tr) {
		s.cfg.logf("runtime: tool_result %q has no pending tool_call", tr.ID)
	}
}

// handleShutdown decodes the §15.4.1 shutdown frame, records the
// termination reason for run to apply after draining in-flight
// handlers, and returns nil to end the frame loop for a clean exit.
func (s *session) handleShutdown(_ context.Context, line []byte, cancel context.CancelFunc) error {
	s.state.Store(int32(stateDraining))
	var sd inboundShutdown
	if err := json.Unmarshal(line, &sd); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
	}
	s.exitReasonMu.Lock()
	s.exitReason = &TerminationReason{Reason: sd.Reason, DeadlineMS: sd.DeadlineMS}
	s.exitReasonMu.Unlock()
	cancel()
	return nil
}

// invokeCreate calls Handler.OnCreate once with the task-scoped snapshot.
func (s *session) invokeCreate(ctx context.Context) error {
	s.credMu.RLock()
	creds := s.credentials
	s.credMu.RUnlock()
	req := CreateRequest{
		SessionID:        s.manifestSessionID(),
		TaskID:           s.manifestTaskID(),
		Credentials:      creds,
		ManifestSnapshot: s.manifest,
	}
	if s.manifest != nil {
		req.RuntimeOptions = s.manifest.RuntimeOptions
	}
	return s.handler.OnCreate(s.withSessionContext(ctx), req)
}

// invokeTerminate calls Handler.OnTerminate at most once. The terminated
// guard makes a lifecycle-channel terminate and the stdin shutdown path
// idempotent.
func (s *session) invokeTerminate(ctx context.Context, reason TerminationReason) {
	if !s.terminated.CompareAndSwap(false, true) {
		return
	}
	if err := s.handler.OnTerminate(s.withSessionContext(ctx), reason); err != nil {
		s.cfg.logf("runtime: OnTerminate error: %v", err)
	}
}

// loadManifest parses the §4.7 adapter manifest. A missing file leaves
// the manifest nil; a malformed file is logged and ignored at Basic
// level and surfaces as a hard error from startChannels at the higher
// levels, which need the socket fields.
func (s *session) loadManifest() {
	path := s.cfg.manifestPath
	data, err := os.ReadFile(path)
	if err != nil {
		s.cfg.logf("runtime: no adapter manifest at %s (%v)", path, err)
		return
	}
	var m AdapterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		s.cfg.logf("runtime: malformed adapter manifest %s: %v", path, err)
		return
	}
	// §4.7 forward-compatibility rule: a manifest version newer than the
	// SDK understands is rejected. Every increment is breaking.
	if m.Version > 1 {
		s.cfg.logf("runtime: adapter manifest %s version %d is newer than supported (1)", path, m.Version)
		return
	}
	s.manifest = &m
}

// loadCredentials parses the §4.7 runtime credential file. A missing
// file is normal when the runtime's pool has no active lease.
func (s *session) loadCredentials() {
	data, err := os.ReadFile(s.cfg.credentialsPath)
	if err != nil {
		return
	}
	var c CredentialBundle
	if err := json.Unmarshal(data, &c); err != nil {
		s.cfg.logf("runtime: malformed credential file %s: %v", s.cfg.credentialsPath, err)
		return
	}
	s.credMu.Lock()
	s.credentials = &c
	s.credMu.Unlock()
}

// reloadCredentials re-reads the §4.7 credential file in place. The
// lifecycle channel calls it on a credentials_rotated event so a
// Full-level runtime continues without restart.
func (s *session) reloadCredentials() *CredentialBundle {
	s.loadCredentials()
	s.credMu.RLock()
	defer s.credMu.RUnlock()
	return s.credentials
}

// Credentials returns the current §4.7 credential bundle, refreshed in
// place by the lifecycle channel on rotation. It is safe to call from
// any goroutine.
func (s *session) Credentials() *CredentialBundle {
	s.credMu.RLock()
	defer s.credMu.RUnlock()
	return s.credentials
}

func (s *session) manifestSessionID() string {
	if s.manifest != nil {
		return s.manifest.SessionID
	}
	return ""
}

func (s *session) manifestTaskID() string {
	if s.manifest != nil {
		return s.manifest.TaskID
	}
	return ""
}

// stampParts sets SchemaVersion on every part that left it zero,
// honoring the §15.4.1 producer obligation, and returns a non-nil slice
// so an empty Reply still serializes as output: [].
func stampParts(parts []OutputPart) []OutputPart {
	out := make([]OutputPart, len(parts))
	for i, p := range parts {
		if p.SchemaVersion == 0 {
			p.SchemaVersion = schemaVersion
		}
		out[i] = p
	}
	return out
}

// --- frame writer ------------------------------------------------------

// frameWriter serializes §15.4.1 outbound frames. Every write is
// followed by a flush before the next inbound read, honoring the
// §15.4.1 stdout-flushing requirement. It also correlates outbound
// tool_call frames with inbound tool_result frames.
type frameWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	out io.Writer

	pending map[string]chan inboundToolResult
}

func newFrameWriter(w io.Writer) *frameWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &frameWriter{enc: enc, out: w, pending: map[string]chan inboundToolResult{}}
}

// write serializes one frame and flushes it. json.Encoder.Encode writes
// the trailing newline; a bufio writer behind out, if any, is flushed.
func (w *frameWriter) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(v); err != nil {
		return err
	}
	if f, ok := w.out.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// registerToolCall records a pending §15.4.1 tool_call id and returns
// the channel its tool_result will arrive on.
func (w *frameWriter) registerToolCall(id string) chan inboundToolResult {
	ch := make(chan inboundToolResult, 1)
	w.mu.Lock()
	w.pending[id] = ch
	w.mu.Unlock()
	return ch
}

// deliverToolResult routes an inbound tool_result to the channel of its
// matching tool_call. It reports whether a pending call was found.
func (w *frameWriter) deliverToolResult(tr inboundToolResult) bool {
	w.mu.Lock()
	ch, ok := w.pending[tr.ID]
	if ok {
		delete(w.pending, tr.ID)
	}
	w.mu.Unlock()
	if !ok {
		return false
	}
	ch <- tr
	return true
}

// cancelToolCall drops a pending tool_call registration.
func (w *frameWriter) cancelToolCall(id string) {
	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
}

// --- transport ---------------------------------------------------------

// transport is the resolved §15.4.1 byte transport.
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

// openTransport resolves the §15.4.1 transport. When socket transport is
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
