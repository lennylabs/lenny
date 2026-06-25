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

// Lifecycle is the §15.4.3 Full-level lifecycle channel surface a
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

// LifecycleEvent is a decoded lifecycle-channel frame handed to a
// runtime callback. Raw carries the full frame for fields the typed
// accessors do not cover.
type LifecycleEvent struct {
	Type string
	Raw  json.RawMessage
}

// lifecycleHooks holds the optional runtime callbacks for lifecycle
// events. A nil hook means the SDK answers with the default behavior.
type lifecycleHooks struct {
	onCheckpoint func(checkpointID string) error
	onInterrupt  func(interruptID string) error
	onRotate     func(creds *CredentialBundle)
	onDeadline   func(LifecycleEvent)
}

// LifecycleOption configures the Full-level lifecycle channel callbacks.
type LifecycleOption func(*lifecycleHooks)

// OnCheckpoint registers a callback invoked on a §15.4.3
// checkpoint_request before the SDK replies with checkpoint_ready. The
// callback quiesces runtime output; a non-nil error is logged and the
// SDK still replies so the adapter is not left waiting.
func OnCheckpoint(fn func(checkpointID string) error) LifecycleOption {
	return func(h *lifecycleHooks) { h.onCheckpoint = fn }
}

// OnInterrupt registers a callback invoked on a §15.4.3
// interrupt_request before the SDK replies with interrupt_acknowledged.
// The callback brings the runtime to a safe stop point.
func OnInterrupt(fn func(interruptID string) error) LifecycleOption {
	return func(h *lifecycleHooks) { h.onInterrupt = fn }
}

// OnCredentialsRotated registers a callback invoked after the SDK
// re-reads the §4.7 credential file on a credentials_rotated event. The
// refreshed bundle is passed to the callback.
func OnCredentialsRotated(fn func(creds *CredentialBundle)) LifecycleOption {
	return func(h *lifecycleHooks) { h.onRotate = fn }
}

// OnDeadline registers a callback invoked on a §15.4.3
// deadline_approaching or deadline_signal event.
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

// dialLifecycle opens the §15.4.3 lifecycle channel: it dials the
// manifest-advertised socket, completes the lifecycle_capabilities /
// lifecycle_support handshake, and starts the event loop. cancel stops
// the frame loop when the adapter sends a terminate event.
func (s *session) dialLifecycle(ctx context.Context, cancel context.CancelFunc) (*Lifecycle, error) {
	if s.manifest == nil || s.manifest.LifecycleChannel == nil || s.manifest.LifecycleChannel.Socket == "" {
		return nil, errors.New("adapter manifest has no lifecycle channel socket")
	}
	conn, err := dialUnixSocket(ctx, s.manifest.LifecycleChannel.Socket, s.cfg.dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial lifecycle socket: %w", err)
	}
	lc := &Lifecycle{
		conn:   conn,
		w:      newFrameWriter(conn),
		cancel: cancel,
		hooks:  s.cfg.lifecycleHooks,
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

	go lc.loop(ctx, reader, s)
	return lc, nil
}

// loop processes inbound lifecycle-channel frames until the connection
// closes or the adapter sends terminate.
func (lc *Lifecycle) loop(ctx context.Context, reader *bufio.Reader, s *session) {
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
			s.cfg.logf("runtime: lifecycle read error: %v", err)
			return
		}
		var ft frameType
		if err := json.Unmarshal(line, &ft); err != nil {
			s.cfg.logf("runtime: malformed lifecycle frame: %v", err)
			continue
		}
		ev := LifecycleEvent{Type: ft.Type, Raw: append(json.RawMessage(nil), line...)}
		switch ft.Type {
		case "checkpoint_request":
			lc.handleCheckpoint(line, s)
		case "interrupt_request":
			lc.handleInterrupt(line, s)
		case "credentials_rotated":
			lc.handleCredentialsRotated(line, s)
		case "deadline_approaching", "deadline_signal":
			lc.handleDeadline(ev, s)
		case "terminate":
			lc.handleTerminate(ctx, line, s)
			return
		default:
			s.cfg.logf("runtime: ignoring unknown lifecycle event %q", ft.Type)
		}
	}
}

// handleCheckpoint answers a §15.4.3 checkpoint_request: it runs the
// runtime quiesce callback, replies with checkpoint_ready, and lets the
// adapter signal checkpoint_complete to resume.
func (lc *Lifecycle) handleCheckpoint(line []byte, s *session) {
	var req struct {
		Type         string `json:"type"`
		CheckpointID string `json:"checkpointId"`
		DeadlineMS   int    `json:"deadlineMs"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		s.cfg.logf("runtime: checkpoint_request decode: %v", err)
		return
	}
	if lc.hooks.onCheckpoint != nil {
		if err := lc.hooks.onCheckpoint(req.CheckpointID); err != nil {
			s.cfg.logf("runtime: OnCheckpoint callback error: %v", err)
		}
	}
	if err := lc.w.write(map[string]any{
		"type":         "checkpoint_ready",
		"checkpointId": req.CheckpointID,
	}); err != nil {
		s.cfg.logf("runtime: checkpoint_ready write: %v", err)
	}
}

// handleInterrupt answers a §15.4.3 interrupt_request: it runs the
// runtime safe-stop callback and replies with interrupt_acknowledged
// carrying the original interruptId.
func (lc *Lifecycle) handleInterrupt(line []byte, s *session) {
	var req struct {
		Type        string `json:"type"`
		InterruptID string `json:"interruptId"`
		DeadlineMS  int    `json:"deadlineMs"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		s.cfg.logf("runtime: interrupt_request decode: %v", err)
		return
	}
	if lc.hooks.onInterrupt != nil {
		if err := lc.hooks.onInterrupt(req.InterruptID); err != nil {
			s.cfg.logf("runtime: OnInterrupt callback error: %v", err)
		}
	}
	if err := lc.w.write(map[string]any{
		"type":        "interrupt_acknowledged",
		"interruptId": req.InterruptID,
	}); err != nil {
		s.cfg.logf("runtime: interrupt_acknowledged write: %v", err)
	}
}

// handleCredentialsRotated answers a §15.4.3 credentials_rotated event:
// it re-reads the §4.7 credential file in place, runs the runtime
// rotation callback, and replies with credentials_acknowledged.
func (lc *Lifecycle) handleCredentialsRotated(line []byte, s *session) {
	var req struct {
		Type     string `json:"type"`
		Provider string `json:"provider"`
		LeaseID  string `json:"leaseId"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		s.cfg.logf("runtime: credentials_rotated decode: %v", err)
		return
	}
	creds := s.reloadCredentials()
	if lc.hooks.onRotate != nil {
		lc.hooks.onRotate(creds)
	}
	if err := lc.w.write(map[string]any{
		"type":     "credentials_acknowledged",
		"leaseId":  req.LeaseID,
		"provider": req.Provider,
	}); err != nil {
		s.cfg.logf("runtime: credentials_acknowledged write: %v", err)
	}
}

// handleDeadline runs the runtime deadline callback for a §15.4.3
// deadline_approaching or deadline_signal event.
func (lc *Lifecycle) handleDeadline(ev LifecycleEvent, s *session) {
	if lc.hooks.onDeadline != nil {
		lc.hooks.onDeadline(ev)
	} else {
		s.cfg.logf("runtime: lifecycle %s: %s", ev.Type, strip(ev.Raw))
	}
}

// handleTerminate answers a §15.4.3 terminate event: it emits a final
// §15.4.1 response frame on stdout (carrying a DEADLINE_EXCEEDED error,
// per the §15.4.6 deadline-signal expectation), invokes OnTerminate,
// and cancels the frame loop so the runtime exits cleanly.
func (lc *Lifecycle) handleTerminate(ctx context.Context, line []byte, s *session) {
	var req struct {
		Type       string `json:"type"`
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadlineMs"`
	}
	_ = json.Unmarshal(line, &req)
	reason := req.Reason
	if reason == "" {
		reason = "lifecycle_terminate"
	}

	// §15.4.6 deadline-signal handling: the runtime writes a final
	// response on the stdout protocol channel before it exits. The
	// adapter maps the error to a DEADLINE_EXCEEDED TaskResult.
	if err := s.w.write(outboundResponse{
		Type:   "response",
		Output: []MessagePart{},
		Error:  &ResponseError{Code: "DEADLINE_EXCEEDED", Message: reason},
	}); err != nil {
		s.cfg.logf("runtime: write terminate response: %v", err)
	}

	s.invokeTerminate(ctx, TerminationReason{Reason: reason, DeadlineMS: req.DeadlineMS})
	if lc.cancel != nil {
		lc.cancel()
	}
}

// Send writes an arbitrary frame on the lifecycle channel. It is the
// escape hatch for lifecycle messages the SDK does not model.
func (lc *Lifecycle) Send(frame any) error {
	if lc == nil {
		return errors.New("lifecycle channel not open")
	}
	lc.mu.Lock()
	closed := lc.closed
	lc.mu.Unlock()
	if closed {
		return errors.New("lifecycle channel closed")
	}
	return lc.w.write(frame)
}

// close releases the lifecycle-channel connection.
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
