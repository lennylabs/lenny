// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// ErrSessionIDRequired is the §7.2 fail-closed dispatch invariant. It is
// internal: a client never supplies the address, so this is not a
// client-facing rejection. Every session is bound to a slot on every pod
// and the wire addresses that slot by the session identifier, so per-slot
// dispatch with no session identifier is a routing bug on a pool of
// either concurrency. The executor fails closed rather than opening a
// stream the adapter cannot attribute. spec: §7.2; §5.2.
var ErrSessionIDRequired = errors.New("podexec: dispatch resolved no session identifier for the stream")

// PodExecutor is the Executor backed by Kubernetes agent pods. It
// drives a session's bound pod over the §4.7 Attach content stream:
// Send forwards message envelopes to the pod's runtime and collects the
// agent's response, Close tears the pod down. The per-session pod
// binding comes from the Registry, which the gateway's session-start
// path populates. The Attach stream is opened on the first Send and held
// until the session's binding is released, on a context detached from the
// delivering request, because the adapter admits a single content consumer
// per session and a request's end must not end the stream. spec: §28.5.1
// (CH-ATTACH Timing.).
type PodExecutor struct {
	registry *podsession.Registry
	binder   *podsession.Binder

	// approvals is the §7.2 tool-use approval authority. When set, the
	// stream's reader gates every tool_call frame carrying
	// approvalRequired:true, whether or not a turn is in flight, and
	// relays the verdict; nil preserves the prior behavior of skipping the
	// frame. F-7.2.9, F-7.2.18.
	approvals ApprovalGate

	// onStreamEnd observes every end of a held stream that the gateway did
	// not cause, after the conn has left the cache. Nil observes nothing.
	onStreamEnd func(tenantID, sessionID, sandboxName string, err error)

	// mu guards streams. It is taken before any attachConn mutex.
	mu      sync.Mutex
	streams map[string]*attachConn
}

// NewPodExecutor returns a PodExecutor over the given registry and
// binder. The registry supplies the per-session pod bindings; the
// binder releases the pod on Close.
func NewPodExecutor(registry *podsession.Registry, binder *podsession.Binder) *PodExecutor {
	return &PodExecutor{
		registry: registry,
		binder:   binder,
		streams:  make(map[string]*attachConn),
	}
}

// SetApprovalGate wires the §7.2 tool-use approval authority. The
// gateway calls it during wiring after the interaction store and event
// bus exist. A nil gate (the dev / echo posture) leaves approval-
// required tool_call frames skipped, matching the prior behavior.
// spec: §7.2. F-7.2.9, F-7.2.18.
func (e *PodExecutor) SetApprovalGate(g ApprovalGate) {
	e.approvals = g
}

var (
	_ Executor        = (*PodExecutor)(nil)
	_ SessionReleaser = (*PodExecutor)(nil)
)

// Send delivers each message to the session's bound pod over its held
// Attach stream and returns the agent's response output parts. Each message
// is one turn: Send takes the stream's turn token, writes the envelope, and
// waits for the reader to hand it the turn's `response`. When ctx ends
// mid-turn, Send returns the context error and leaves the turn to the
// reader, which consumes its late reply. spec: §28.5.1 (CH-ATTACH Timing.),
// §28.5.3, §7.2.
func (e *PodExecutor) Send(ctx context.Context, sessionID string, messages []Message) (Response, error) {
	conn, err := e.streamFor(ctx, sessionID)
	if err != nil {
		return Response{}, err
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
			// spec: §28.5.3 — every session is bound to a slot on every
			// pod, and the wire addresses it by its session identifier, so
			// the envelope carries the session it is being sent on
			// whatever the pool's concurrency.
			SessionID: sessionID,
		}
		line, err := json.Marshal(env)
		if err != nil {
			return Response{}, err
		}
		res, err := conn.runTurn(ctx, line)
		if err != nil {
			return Response{}, err
		}
		out = append(out, res.parts...)
		envAnn = mergeAnnotations(envAnn, res.ann)
	}
	return Response{Parts: out, Annotations: envAnn}, nil
}

// streamFor returns the session's held Attach stream, opening it on first
// use. A cache miss caches a new conn under e.mu on a context detached from
// ctx's cancellation, so the stream outlives the request that opened it, and
// opens the stream on a separate goroutine so a slow open never holds e.mu.
// Every caller waits for the open to complete or for its own ctx to end.
// spec: §28.5.1 (CH-ATTACH Timing.).
func (e *PodExecutor) streamFor(ctx context.Context, sessionID string) (*attachConn, error) {
	// spec: §7.2; §5.2 — the stream is addressed by the session
	// identifier, which names the slot that session holds on the pod
	// whatever the pool's concurrency. Fail closed on an empty one before
	// any lookup, rather than opening a stream the adapter cannot
	// attribute or serving one cached under the empty address.
	if sessionID == "" {
		return nil, fmt.Errorf("podexec: %w", ErrSessionIDRequired)
	}
	e.mu.Lock()
	c, ok := e.streams[sessionID]
	if !ok {
		bind, bound := e.registry.Get(sessionID)
		if !bound {
			e.mu.Unlock()
			return nil, fmt.Errorf("podexec: session %s is not bound to a pod", sessionID)
		}
		c = newAttachConn(ctx, bind)
		e.streams[sessionID] = c
		go e.openConn(c, bind.Adapter)
	}
	e.mu.Unlock()
	if err := c.waitReady(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// EvictStream ends the session's held Attach stream and removes it from the
// cache: under the executor lock it marks the conn as closed by the gateway,
// cancels the conn's context, and deletes the entry. Cancelling, rather than
// half-closing, ends the stream on the adapter side too, and the mark keeps
// the reader from treating the end as a stream failure. The pod and the
// binding are untouched. Release and the coordination sweep's binding
// eviction call it; streamFor consults the cache before the registry, so
// without this eviction a re-adopt that republishes a fresh BindResult would
// never Attach over the new binding. Evicting a session with no cached
// stream is a no-op. spec: §28.5.1 (CH-ATTACH Timing.), §4.7, §4.6.1.
func (e *PodExecutor) EvictStream(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evictStreamLocked(sessionID)
}

// evictStreamLocked is EvictStream's body. The caller holds e.mu.
func (e *PodExecutor) evictStreamLocked(sessionID string) {
	if c, ok := e.streams[sessionID]; ok {
		c.closedByGateway.Store(true)
		c.cancel()
		delete(e.streams, sessionID)
	}
}

// unbind removes the session's binding from the registry and evicts its held
// stream under one hold of e.mu. streamFor reads the registry under e.mu
// before it caches a conn, so once unbind returns no Send can open a stream
// over the removed binding. Evicting first and removing outside the lock
// would leave a gap in which a Send reads the still-published binding and
// caches a stream that outlives the release. spec: §28.5.1 (CH-ATTACH
// Timing.), §7.2.
func (e *PodExecutor) unbind(sessionID string) (*podsession.BindResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	bind, ok := e.registry.Remove(sessionID)
	e.evictStreamLocked(sessionID)
	return bind, ok
}

// toolCallFrame is the subset of the §28.5.3 tool_call frame the
// executor inspects to decide whether the call needs user approval.
type toolCallFrame struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ApprovalRequired bool            `json:"approvalRequired"`
	// SessionID names the session the runtime addressed the call to.
	// spec: §28.5.3.
	SessionID string `json:"sessionId,omitempty"`
}

// toolResultFrame is the §28.5.3 tool_result the executor writes back to
// the runtime on a denial (isError:true) so the blocked tool call
// returns the deny reason rather than executing.
type toolResultFrame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Content []wireMessagePart `json:"content"`
	IsError bool              `json:"isError"`
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
	// The stream and the binding go together, before the binder runs.
	bind, ok := e.unbind(sessionID)
	if !ok {
		return nil
	}
	if bind.SlotID != "" {
		return e.binder.ReleaseSlot(ctx, bind)
	}
	// The session-terminal disposition (completed/failed/cancelled/expired)
	// is recorded on the Postgres session model and surfaced on the Sandbox
	// only as a Terminated condition; it is no longer a coarse
	// Sandbox.status.phase value (spec: §6.2, §7.2). Release maps the
	// disposition string to that condition reason and drains the pod.
	return e.binder.Release(ctx, bind, string(disposition))
}
