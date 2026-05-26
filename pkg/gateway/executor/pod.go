// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// PodExecutor is the Executor backed by Kubernetes agent pods. It
// drives a session's bound pod over the §4.7 Attach content stream:
// Send forwards message envelopes to the pod's runtime and collects the
// agent's response, Close tears the pod down. The per-session pod
// binding comes from the Registry, which the gateway's session-start
// path populates. The Attach stream is opened lazily on the first Send
// and held for the session's duration, because the adapter admits a
// single content consumer per session.
type PodExecutor struct {
	registry *podsession.Registry
	binder   *podsession.Binder

	mu      sync.Mutex
	streams map[string]*adapterclient.AttachStream
}

// NewPodExecutor returns a PodExecutor over the given registry and
// binder. The registry supplies the per-session pod bindings; the
// binder releases the pod on Close.
func NewPodExecutor(registry *podsession.Registry, binder *podsession.Binder) *PodExecutor {
	return &PodExecutor{
		registry: registry,
		binder:   binder,
		streams:  make(map[string]*adapterclient.AttachStream),
	}
}

var (
	_ Executor        = (*PodExecutor)(nil)
	_ SessionReleaser = (*PodExecutor)(nil)
)

// Send delivers each message to the session's bound pod over its Attach
// stream and returns the agent's response output parts.
func (e *PodExecutor) Send(ctx context.Context, sessionID string, messages []Message) ([]OutputPart, error) {
	stream, err := e.streamFor(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var out []OutputPart
	for _, m := range messages {
		env := messageEnvelope{
			SchemaVersion: 1,
			Type:          "message",
			ID:            newMessageID(),
			From:          fromBlock{Kind: "client", ID: "client_gateway"},
			Input:         []wireOutputPart{{Type: "text", Inline: m.Content}},
		}
		line, err := json.Marshal(env)
		if err != nil {
			return nil, err
		}
		if err := stream.Send(line); err != nil {
			return nil, fmt.Errorf("podexec: send to pod: %w", err)
		}
		parts, err := readAttachResponse(stream)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	return out, nil
}

// streamFor returns the session's Attach stream, opening it on first
// use. The lock is held across the open so a session never races two
// streams into existence.
func (e *PodExecutor) streamFor(ctx context.Context, sessionID string) (*adapterclient.AttachStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.streams[sessionID]; ok {
		return s, nil
	}
	bind, ok := e.registry.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("podexec: session %s is not bound to a pod", sessionID)
	}
	s, err := bind.Adapter.Attach(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("podexec: open attach stream: %w", err)
	}
	e.streams[sessionID] = s
	return s, nil
}

// readAttachResponse reads Attach frames until a `response` envelope
// and returns its output parts. heartbeat_ack, status, and unparseable
// frames are skipped.
func readAttachResponse(stream *adapterclient.AttachStream) ([]OutputPart, error) {
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return nil, fmt.Errorf("podexec: runtime output ended before responding")
		}
		if err != nil {
			return nil, fmt.Errorf("podexec: receive from pod: %w", err)
		}
		var env responseEnvelope
		if err := json.Unmarshal(frame, &env); err != nil {
			continue
		}
		if env.Type != "response" {
			continue
		}
		parts := make([]OutputPart, 0, len(env.Output))
		if env.Text != "" && len(env.Output) == 0 {
			parts = append(parts, OutputPart{Type: "text", Text: env.Text})
		}
		for _, p := range env.Output {
			op := OutputPart{Type: p.Type, Ref: p.Ref}
			if p.Type == "text" {
				op.Text = p.Inline
			}
			parts = append(parts, op)
		}
		return parts, nil
	}
}

// Close removes the session's binding, closes its Attach stream, and
// releases the pod. Closing a session that was never bound is a no-op.
//
// The release path branches on the §5.2 mode the bind was opened in:
// a session-mode bind (BindResult.SlotID == "") drains the pod via
// binder.Release per §6.2 (claimed → draining → terminated). A
// concurrent-mode bind (BindResult.SlotID != "") releases only that
// slot via binder.ReleaseSlot — the pod's sibling slots stay live and
// the pod returns to idle only when its last slot drains, per §5.2.
// Routing every concurrent termination through the session-mode
// drain would (a) tear down sibling slots that did not terminate and
// (b) leak the SandboxClaim that the slot reservation created.
func (e *PodExecutor) Close(ctx context.Context, sessionID string) error {
	// No disposition: the pod still drains, but the §6.2 terminal phase is
	// not recorded. The terminal-state path uses Release instead.
	return e.Release(ctx, sessionID, "")
}

// Release implements SessionReleaser: it tears the session's pod down like
// Close and records the session's terminal disposition on the backing
// Sandbox (§6.2 attached → completed/failed/cancelled/expired) before
// draining the pod. A concurrent-mode (slot) bind has no pod-level terminal
// phase — the per-slot lifecycle tracks that — so it releases the slot
// without a disposition.
func (e *PodExecutor) Release(ctx context.Context, sessionID string, disposition Disposition) error {
	e.mu.Lock()
	if s, ok := e.streams[sessionID]; ok {
		_ = s.CloseSend()
		delete(e.streams, sessionID)
	}
	e.mu.Unlock()

	bind, ok := e.registry.Remove(sessionID)
	if !ok {
		return nil
	}
	if bind.SlotID != "" {
		return e.binder.ReleaseSlot(ctx, bind)
	}
	return e.binder.Release(ctx, bind, dispositionPhase(disposition))
}

// dispositionPhase maps a session's terminal Disposition to the §6.2 Sandbox
// phase Release records before draining the pod. An unrecognized or empty
// disposition maps to the empty phase, which Release treats as "no terminal
// phase to record" and drains directly.
func dispositionPhase(d Disposition) state.State {
	switch d {
	case DispositionCompleted:
		return state.Completed
	case DispositionFailed:
		return state.Failed
	case DispositionCancelled:
		return state.Cancelled
	case DispositionExpired:
		return state.Expired
	default:
		return ""
	}
}
