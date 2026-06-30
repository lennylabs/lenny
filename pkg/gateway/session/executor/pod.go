// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// ErrSlotIDRequired is the §7.2 SLOT_ID_REQUIRED fail-closed invariant. It is
// internal: a client never supplies a slotId, so this is not a client-facing
// rejection. The gateway derives a session's slot from its bind, so a
// well-routed concurrent-session bind (maxConcurrentSessions > 1) always
// carries a non-empty SlotID. ErrSlotIDRequired fires only when per-slot
// dispatch is reached for a concurrent-session pod whose bind resolved no slot
// (MaxConcurrentSessions > 1 with an empty SlotID, a routing bug): the
// executor fails closed rather than misdelivering the message to another
// session's slot. A maxConcurrentSessions <= 1 bind never triggers it; it
// keeps whole-pod routing with no slotId. spec: §7.2 (per-slot routing,
// SLOT_ID_REQUIRED), §5.2.
var ErrSlotIDRequired = errors.New("podexec: SLOT_ID_REQUIRED: concurrent-session bind resolved no slot for the session")

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

	// approvals is the §7.2 tool-use approval authority. When set, a
	// tool_call frame carrying approvalRequired:true blocks the
	// in-flight Send until the user resolves it; nil preserves the
	// prior behavior of skipping the frame. F-7.2.9, F-7.2.18.
	approvals ApprovalGate

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

// SetApprovalGate wires the §7.2 tool-use approval authority. The
// gateway calls it during wiring after the interaction store and event
// bus exist. A nil gate (the dev / echo posture) leaves approval-
// required tool_call frames skipped, matching the prior behavior.
// spec: §7.2 lines 124-134. F-7.2.9, F-7.2.18.
func (e *PodExecutor) SetApprovalGate(g ApprovalGate) {
	e.approvals = g
}

var (
	_ Executor        = (*PodExecutor)(nil)
	_ SessionReleaser = (*PodExecutor)(nil)
)

// Send delivers each message to the session's bound pod over its Attach
// stream and returns the agent's response output parts.
func (e *PodExecutor) Send(ctx context.Context, sessionID string, messages []Message) (Response, error) {
	stream, err := e.streamFor(ctx, sessionID)
	if err != nil {
		return Response{}, err
	}
	// The §7.2 KindToolUse interaction the approval gate records is keyed
	// on the session's tenant; capture it from the live binding once so
	// each approval-required frame on this Send carries it. The same live
	// bind carries the §6.4 slot the gateway resolved for the session: a
	// concurrent-pool bind (SlotID != "") stamps the slotId on every
	// outbound envelope so the reconciled adapter and runtime key on it,
	// while an exclusive session-mode bind leaves it empty. spec: §7.2
	// (per-slot routing), §15.4.1 (slotId multiplexing).
	var tenantID, slotID string
	if bind, ok := e.registry.Get(sessionID); ok {
		tenantID = bind.TenantID
		slotID = bind.SlotID
	}
	var out []MessagePart
	var envAnn map[string]any
	for _, m := range messages {
		env := messageEnvelope{
			SchemaVersion: 1,
			Type:          "message",
			ID:            newMessageID(),
			From:          resolveFromBlock(m),
			Input:         []wireMessagePart{{Type: "text", Inline: m.Content}},
			SlotID:        slotID,
		}
		line, err := json.Marshal(env)
		if err != nil {
			return Response{}, err
		}
		if err := stream.Send(line); err != nil {
			return Response{}, fmt.Errorf("podexec: send to pod: %w", err)
		}
		parts, ann, err := e.readAttachResponse(ctx, tenantID, sessionID, stream)
		if err != nil {
			return Response{}, err
		}
		out = append(out, parts...)
		envAnn = mergeAnnotations(envAnn, ann)
	}
	return Response{Parts: out, Annotations: envAnn}, nil
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
	// spec: §7.2 (per-slot routing, SLOT_ID_REQUIRED) — per-slot dispatch on a
	// maxConcurrentSessions > 1 pod requires a resolved slot. The gateway
	// derives the slot from the session, so a well-routed concurrent-session
	// bind always carries a non-empty SlotID; an empty SlotID on such a bind
	// is a routing bug. Fail closed rather than open an Attach stream with no
	// slotId, which the adapter would route to the pod's whole-pod base path
	// and misdeliver into another session's slot. A maxConcurrentSessions <= 1
	// bind keeps whole-pod routing with no slotId.
	if bind.MaxConcurrentSessions > 1 && bind.SlotID == "" {
		return nil, fmt.Errorf("podexec: session %s: %w", sessionID, ErrSlotIDRequired)
	}
	// A concurrent-pool bind (SlotID != "") carries the adapter-assigned slot
	// onto the Attach stream so the reconciled adapter and the runtime's
	// dispatch loop key on it; an exclusive session-mode bind leaves it empty
	// and the single-session path emits no slotId. spec: §7.2 (per-slot
	// routing), §15.4.1 (slotId multiplexing).
	s, err := bind.Adapter.Attach(ctx, sessionID, bind.SlotID)
	if err != nil {
		return nil, fmt.Errorf("podexec: open attach stream: %w", err)
	}
	e.streams[sessionID] = s
	return s, nil
}

// readAttachResponse reads Attach frames until a `response` envelope and
// returns its output parts. heartbeat_ack, status, and unparseable
// frames are skipped. A `tool_call` frame carrying approvalRequired:true
// is the §7.2 line 134 user-approval signal: when an ApprovalGate is
// wired the executor records the interaction, publishes the
// `tool_use_requested` SSE event, and blocks on the gate until the user
// resolves the call, then relays the verdict to the runtime (approve →
// the call is forwarded for execution; deny → a tool_result error is
// written back). Without a gate the frame is skipped like any other
// intermediate frame. F-7.2.9, F-7.2.18.
func (e *PodExecutor) readAttachResponse(ctx context.Context, tenantID, sessionID string, stream *adapterclient.AttachStream) ([]MessagePart, map[string]any, error) {
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return nil, nil, fmt.Errorf("podexec: runtime output ended before responding")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("podexec: receive from pod: %w", err)
		}
		if e.approvals != nil {
			handled, herr := e.maybeGateToolCall(ctx, tenantID, sessionID, frame, stream)
			if herr != nil {
				return nil, nil, herr
			}
			if handled {
				continue
			}
		}
		var env responseEnvelope
		if err := json.Unmarshal(frame, &env); err != nil {
			continue
		}
		if env.Type != "response" {
			continue
		}
		parts, ann := ingestResponse(env)
		return parts, ann, nil
	}
}

// toolCallFrame is the subset of the §15.4.1 tool_call frame the
// executor inspects to decide whether the call needs user approval.
type toolCallFrame struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ApprovalRequired bool            `json:"approvalRequired"`
	SlotID           string          `json:"slotId,omitempty"`
}

// toolResultFrame is the §15.4.1 tool_result the executor writes back to
// the runtime on a denial (isError:true) so the blocked tool call
// returns the deny reason rather than executing.
type toolResultFrame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Content []wireMessagePart `json:"content"`
	IsError bool              `json:"isError"`
}

// maybeGateToolCall inspects one frame. When it is a tool_call requiring
// approval it drives the §7.2 approval handshake and returns handled=true
// so the read loop continues; for any other frame it returns
// handled=false and the caller resumes normal parsing. A gate error or a
// failure to relay the verdict aborts the in-flight Send.
func (e *PodExecutor) maybeGateToolCall(ctx context.Context, tenantID, sessionID string, frame []byte, stream *adapterclient.AttachStream) (bool, error) {
	var call toolCallFrame
	if err := json.Unmarshal(frame, &call); err != nil {
		return false, nil
	}
	if call.Type != "tool_call" || !call.ApprovalRequired {
		return false, nil
	}
	decision, err := e.approvals.AwaitApproval(ctx, tenantID, sessionID, PendingToolCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
		SlotID:    call.SlotID,
	})
	if err != nil {
		return false, fmt.Errorf("podexec: await tool-use approval: %w", err)
	}
	if decision.Approved {
		// §7.2: an approval forwards the call. Re-send the tool_call with
		// the approval flag cleared so the runtime executes it without
		// re-entering the gate.
		approved, mErr := json.Marshal(toolCallFrame{
			Type:      "tool_call",
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			SlotID:    call.SlotID,
		})
		if mErr != nil {
			return false, fmt.Errorf("podexec: encode approved tool_call: %w", mErr)
		}
		if sErr := stream.Send(approved); sErr != nil {
			return false, fmt.Errorf("podexec: forward approved tool_call: %w", sErr)
		}
		return true, nil
	}
	// §7.2 line 125: a denial returns the tool a tool_result carrying
	// isError:true and the deny reason.
	denied, mErr := json.Marshal(toolResultFrame{
		Type:    "tool_result",
		ID:      call.ID,
		Content: []wireMessagePart{{Type: "text", Inline: decision.Reason}},
		IsError: true,
	})
	if mErr != nil {
		return false, fmt.Errorf("podexec: encode deny tool_result: %w", mErr)
	}
	if sErr := stream.Send(denied); sErr != nil {
		return false, fmt.Errorf("podexec: deliver deny tool_result: %w", sErr)
	}
	return true, nil
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
	// The session-terminal disposition (completed/failed/cancelled/expired)
	// is recorded on the Postgres session model and surfaced on the Sandbox
	// only as a Terminated condition; it is no longer a coarse
	// Sandbox.status.phase value (spec: §6.2, §6.37). Release maps the
	// disposition string to that condition reason and drains the pod.
	return e.binder.Release(ctx, bind, string(disposition))
}
