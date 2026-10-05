// SPDX-License-Identifier: MIT

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// lifecycleCapabilities is the §15.4.3 / §15.4.6 set of Full-level
// lifecycle events the SDK handles on the runtime's behalf. It is the
// payload of the lifecycle_support handshake reply.
var lifecycleCapabilities = []string{
	"checkpoint",
	"interrupt",
	"credential_rotation",
	"deadline_signal",
}

// Lifecycle is the §15.4.3 Full-level CH-RUNTIMEOPS surface a
// runtime observes. The SDK answers the protocol-level handshake and
// the checkpoint, interrupt, credential-rotation, and deadline events
// automatically; a runtime that needs to react (quiesce real output,
// reach a safe interrupt point) registers callbacks through the
// LifecycleHandler option set.
//
// The channel is non-nil only when Run was configured with
// WithFullLevel and the manifest advertised a lifecycle socket.
type Lifecycle struct {
	conn   net.Conn
	w      *frameWriter
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	hooks  lifecycleHooks
}

// LifecycleEvent is a decoded CH-RUNTIMEOPS frame handed to a
// runtime callback. SessionID names the session a session-scoped event
// concerns. Raw carries the full frame for fields the typed accessors do
// not cover.
type LifecycleEvent struct {
	Type      string
	SessionID string
	Raw       json.RawMessage
}

// lifecycleHooks holds the optional runtime callbacks for lifecycle
// events. A nil hook means the SDK answers with the default behavior.
type lifecycleHooks struct {
	onCheckpoint func(sessionID, checkpointID string) error
	onInterrupt  func(sessionID, interruptID string) error
	onRotate     func(sessionID string, creds *CredentialBundle)
	onDeadline   func(LifecycleEvent)
}

// LifecycleOption configures the Full-level CH-RUNTIMEOPS callbacks.
type LifecycleOption func(*lifecycleHooks)

// OnCheckpoint registers a callback invoked on a §15.4.3
// checkpoint_request before the SDK replies with checkpoint_ready. The
// callback quiesces the named session's output; a non-nil error is
// logged and the SDK still replies so the adapter is not left waiting.
func OnCheckpoint(fn func(sessionID, checkpointID string) error) LifecycleOption {
	return func(h *lifecycleHooks) { h.onCheckpoint = fn }
}

// OnInterrupt registers a callback invoked on a §15.4.3
// interrupt_request before the SDK replies with interrupt_acknowledged.
// The callback brings the named session's work to a safe stop point.
func OnInterrupt(fn func(sessionID, interruptID string) error) LifecycleOption {
	return func(h *lifecycleHooks) { h.onInterrupt = fn }
}

// OnCredentialsRotated registers a callback invoked after the SDK
// re-reads the credential file a credentials_rotated event names for the
// named session. The session's refreshed bundle is passed to the
// callback.
func OnCredentialsRotated(fn func(sessionID string, creds *CredentialBundle)) LifecycleOption {
	return func(h *lifecycleHooks) { h.onRotate = fn }
}

// OnDeadline registers a callback invoked on a §15.4.3
// deadline_approaching or deadline_signal event for a session the
// runtime holds.
func OnDeadline(fn func(LifecycleEvent)) LifecycleOption {
	return func(h *lifecycleHooks) { h.onDeadline = fn }
}

// WithLifecycleHandlers attaches lifecycle-event callbacks. It implies
// WithFullLevel.
func WithLifecycleHandlers(opts ...LifecycleOption) Option {
	return func(c *config) {
		c.level = levelFull
		for _, o := range opts {
			o(&c.lifecycleHooks)
		}
	}
}

// dialLifecycle opens the §15.4.3 CH-RUNTIMEOPS: it dials the
// manifest-advertised socket, completes the lifecycle_capabilities /
// lifecycle_support handshake, and starts the event loop. It runs once
// per process; the loop routes each session-scoped event to the session
// it names. cancel stops the frame loop when the adapter sends a
// terminate event.
func (p *process) dialLifecycle(ctx context.Context, cancel context.CancelFunc) (*Lifecycle, error) {
	if p.manifest == nil || p.manifest.RuntimeOps == nil || p.manifest.RuntimeOps.Socket == "" {
		return nil, errors.New("adapter manifest has no CH-RUNTIMEOPS socket")
	}
	conn, err := dialUnixSocket(ctx, p.manifest.RuntimeOps.Socket, p.cfg.dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial lifecycle socket: %w", err)
	}
	lc := &Lifecycle{
		conn:   conn,
		w:      newFrameWriter(conn),
		cancel: cancel,
		hooks:  p.cfg.lifecycleHooks,
	}
	reader := bufio.NewReader(conn)

	// §15.4.3 handshake: the adapter sends lifecycle_capabilities; the
	// runtime replies with lifecycle_support naming the events it
	// implements. Anything else on the first frame is a handshake
	// failure.
	first, err := readJSONLine(reader)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("lifecycle handshake read: %w", err)
	}
	var caps frameType
	if err := json.Unmarshal(first, &caps); err != nil || caps.Type != "lifecycle_capabilities" {
		conn.Close()
		return nil, fmt.Errorf("lifecycle handshake: expected lifecycle_capabilities, got %s", strip(first))
	}
	if err := lc.w.write(map[string]any{
		"type":         "lifecycle_support",
		"capabilities": lifecycleCapabilities,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("lifecycle_support write: %w", err)
	}

	go lc.loop(ctx, reader, p)
	return lc, nil
}

// lifecycleFrame peeks the type discriminator and the session address of
// an inbound CH-RUNTIMEOPS frame.
type lifecycleFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
}

// sessionScopedEvents are the adapter-to-runtime CH-RUNTIMEOPS frames
// that name a session. The SDK hands each to the session it names and
// drops, without a reply, one naming a session the runtime does not hold
// or whose context it failed to create. The adapter writes them only
// after it reads the session's session_started, so the SDK keeps no
// queue of early events.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
var sessionScopedEvents = map[string]bool{
	"checkpoint_request":   true,
	"checkpoint_complete":  true,
	"interrupt_request":    true,
	"credentials_rotated":  true,
	"deadline_approaching": true,
	"deadline_signal":      true,
	"files_updated":        true,
}

// loop processes inbound CH-RUNTIMEOPS frames until the connection
// closes or the adapter sends terminate.
func (lc *Lifecycle) loop(ctx context.Context, reader *bufio.Reader, p *process) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := readJSONLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			p.cfg.logf("runtime: lifecycle read error: %v", err)
			return
		}
		var ft lifecycleFrame
		if err := json.Unmarshal(line, &ft); err != nil {
			p.cfg.logf("runtime: malformed lifecycle frame: %v", err)
			continue
		}
		if ft.Type == "terminate" {
			lc.handleTerminate(line, p)
			return
		}
		if !sessionScopedEvents[ft.Type] {
			p.cfg.logf("runtime: ignoring unknown lifecycle event %q", ft.Type)
			continue
		}
		st := p.heldSession(ft.SessionID)
		if st == nil {
			p.cfg.logf("runtime: dropping lifecycle event %q for session %q, which this runtime does not hold", ft.Type, ft.SessionID)
			continue
		}
		lc.dispatch(LifecycleEvent{Type: ft.Type, SessionID: st.id, Raw: append(json.RawMessage(nil), line...)}, st, p)
	}
}

// dispatch answers one session-scoped event for the session st.
func (lc *Lifecycle) dispatch(ev LifecycleEvent, st *sessionState, p *process) {
	switch ev.Type {
	case "checkpoint_request":
		lc.handleCheckpoint(ev, p)
	case "interrupt_request":
		lc.handleInterrupt(ev, p)
	case "credentials_rotated":
		lc.handleCredentialsRotated(ev, st, p)
	case "deadline_approaching", "deadline_signal":
		lc.handleDeadline(ev, p)
	default:
		// checkpoint_complete and files_updated need no reply.
	}
}

// handleCheckpoint answers a §15.4.3 checkpoint_request: it runs the
// runtime quiesce callback for the named session and replies with
// checkpoint_ready, correlated by checkpointId.
func (lc *Lifecycle) handleCheckpoint(ev LifecycleEvent, p *process) {
	var req struct {
		CheckpointID string `json:"checkpointId"`
	}
	if err := json.Unmarshal(ev.Raw, &req); err != nil {
		p.cfg.logf("runtime: checkpoint_request decode: %v", err)
		return
	}
	if lc.hooks.onCheckpoint != nil {
		if err := lc.hooks.onCheckpoint(ev.SessionID, req.CheckpointID); err != nil {
			p.cfg.logf("runtime: OnCheckpoint callback error: %v", err)
		}
	}
	if err := lc.w.write(map[string]any{
		"type":         "checkpoint_ready",
		"checkpointId": req.CheckpointID,
	}); err != nil {
		p.cfg.logf("runtime: checkpoint_ready write: %v", err)
	}
}

// handleInterrupt answers a §15.4.3 interrupt_request: it runs the
// runtime safe-stop callback for the named session and replies with
// interrupt_acknowledged carrying the original interruptId.
func (lc *Lifecycle) handleInterrupt(ev LifecycleEvent, p *process) {
	var req struct {
		InterruptID string `json:"interruptId"`
	}
	if err := json.Unmarshal(ev.Raw, &req); err != nil {
		p.cfg.logf("runtime: interrupt_request decode: %v", err)
		return
	}
	if lc.hooks.onInterrupt != nil {
		if err := lc.hooks.onInterrupt(ev.SessionID, req.InterruptID); err != nil {
			p.cfg.logf("runtime: OnInterrupt callback error: %v", err)
		}
	}
	if err := lc.w.write(map[string]any{
		"type":        "interrupt_acknowledged",
		"interruptId": req.InterruptID,
	}); err != nil {
		p.cfg.logf("runtime: interrupt_acknowledged write: %v", err)
	}
}

// handleCredentialsRotated answers a §15.4.3 credentials_rotated event:
// it re-reads the credential file the event names into the named
// session's bundle, runs the runtime rotation callback, and replies with
// credentials_acknowledged. The session comes from the frame's sessionId
// rather than from parsing credentialsPath, whose root is
// operator-configurable.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11 (item 4).
func (lc *Lifecycle) handleCredentialsRotated(ev LifecycleEvent, st *sessionState, p *process) {
	var req struct {
		Provider        string `json:"provider"`
		LeaseID         string `json:"leaseId"`
		CredentialsPath string `json:"credentialsPath"`
	}
	if err := json.Unmarshal(ev.Raw, &req); err != nil {
		p.cfg.logf("runtime: credentials_rotated decode: %v", err)
		return
	}
	creds := p.reloadCredentials(st, req.CredentialsPath)
	if lc.hooks.onRotate != nil {
		lc.hooks.onRotate(st.id, creds)
	}
	if err := lc.w.write(map[string]any{
		"type":     "credentials_acknowledged",
		"leaseId":  req.LeaseID,
		"provider": req.Provider,
	}); err != nil {
		p.cfg.logf("runtime: credentials_acknowledged write: %v", err)
	}
}

// handleDeadline runs the runtime deadline callback for a §15.4.3
// deadline_approaching or deadline_signal event.
func (lc *Lifecycle) handleDeadline(ev LifecycleEvent, p *process) {
	if lc.hooks.onDeadline != nil {
		lc.hooks.onDeadline(ev)
	} else {
		p.cfg.logf("runtime: lifecycle %s: %s", ev.Type, strip(ev.Raw))
	}
}

// handleTerminate answers a CH-RUNTIMEOPS terminate event: it emits a
// final §28.5.3 response frame on stdout carrying a DEADLINE_EXCEEDED
// error, records the termination reason every live session's OnTerminate
// receives, and cancels the frame loop so the runtime exits.
func (lc *Lifecycle) handleTerminate(line []byte, p *process) {
	var req struct {
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadlineMs"`
		SessionID  string `json:"sessionId"`
	}
	_ = json.Unmarshal(line, &req)
	reason := req.Reason
	if reason == "" {
		reason = "lifecycle_terminate"
	}
	if err := p.w.write(outboundResponse{
		Type:      "response",
		Output:    []MessagePart{},
		Error:     &ResponseError{Code: "DEADLINE_EXCEEDED", Message: reason},
		SessionID: req.SessionID,
	}); err != nil {
		p.cfg.logf("runtime: write terminate response: %v", err)
	}
	p.setExitReason(TerminationReason{Reason: reason, DeadlineMS: req.DeadlineMS})
	if lc.cancel != nil {
		lc.cancel()
	}
}

// Send writes an arbitrary frame on the CH-RUNTIMEOPS. It is the escape
// hatch for lifecycle messages the SDK does not model. It applies no
// per-session filter, because a runtime may still report a late
// llm_request_completed for a request that started before the session's
// session_end.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
func (lc *Lifecycle) Send(frame any) error {
	if lc == nil {
		return errors.New("CH-RUNTIMEOPS connection not open")
	}
	lc.mu.Lock()
	closed := lc.closed
	lc.mu.Unlock()
	if closed {
		return errors.New("CH-RUNTIMEOPS connection closed")
	}
	return lc.w.write(frame)
}

// close releases the CH-RUNTIMEOPS connection.
func (lc *Lifecycle) close() {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.closed {
		return
	}
	lc.closed = true
	if lc.conn != nil {
		_ = lc.conn.Close()
	}
}

// readJSONLine reads one newline-delimited JSON frame and returns it
// without the trailing newline.
func readJSONLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], err
	}
	return line, err
}

// strip trims a frame for inclusion in a log line.
func strip(b []byte) string {
	const max = 256
	s := string(b)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
