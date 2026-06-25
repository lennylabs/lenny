// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
)

// approvalPollInterval is the cadence at which AwaitApproval re-reads the
// shared interaction store for a resolution that landed on another
// replica. The process-local waiter channel is the fast path on the
// coordinating replica; the store poll is the cross-replica wake fallback
// for an approve POSTed to a different replica, which updates the
// Postgres-backed store but never reaches this replica's in-memory
// registry. It mirrors the elicitation await's poll cadence
// (awaitPollInterval in pkg/gateway/mcptools). spec: §7.2.
const approvalPollInterval = 25 * time.Millisecond

// ToolApprovalGate is the gateway-side §7.2 tool-use approval authority.
// When the pod executor reads a tool_call carrying approvalRequired:true
// it calls AwaitApproval, which records the KindToolUse interaction,
// publishes the `tool_use_requested(tool_call_id, tool, args)` SSE event,
// and blocks until the §15.1 approve/deny endpoint resolves the call, the
// context is cancelled, or the configured timeout fires. Resolution wakes
// the block on either of two paths: the process-local waiter registry (the
// fast path on the coordinating replica) or a poll over the Postgres-backed
// shared interaction store (the cross-replica fallback for an approve POSTed
// to a non-coordinator replica, which updates the store but not this
// replica's in-memory registry). It implements executor.ApprovalGate.
// spec: §7.2 lines 124-134. F-7.2.9, F-7.2.18, F-IA1.
type ToolApprovalGate struct {
	store        sessionstore.Store
	interactions interactionstore.Store
	events       *sessionevents.Bus
	waits        *toolapproval.Registry
	now          func() time.Time
	// timeout bounds how long a blocked tool call waits for resolution.
	// Zero blocks until the user resolves it or the context is done.
	timeout time.Duration
}

var _ executor.ApprovalGate = (*ToolApprovalGate)(nil)

// NewToolApprovalGate builds the §7.2 approval authority. store supplies
// the session row's owning user (the §15.1 authorization-triple subject
// the interaction is directed at); interactions records the pending
// approval; events publishes the SSE signal; waits is the registry the
// §15.1 resolution endpoints deliver the verdict onto; now stamps the
// interaction; timeout bounds the block (zero = unbounded). F-7.2.9,
// F-7.2.18.
func NewToolApprovalGate(store sessionstore.Store, interactions interactionstore.Store, events *sessionevents.Bus, waits *toolapproval.Registry, now func() time.Time, timeout time.Duration) *ToolApprovalGate {
	if now == nil {
		now = time.Now
	}
	return &ToolApprovalGate{
		store:        store,
		interactions: interactions,
		events:       events,
		waits:        waits,
		now:          now,
		timeout:      timeout,
	}
}

// AwaitApproval implements executor.ApprovalGate.
func (g *ToolApprovalGate) AwaitApproval(ctx context.Context, tenantID, sessionID string, call executor.PendingToolCall) (executor.ApprovalDecision, error) {
	if g.interactions == nil || g.waits == nil {
		return executor.ApprovalDecision{}, fmt.Errorf("toolapproval: gate is not fully wired")
	}
	// The §15.1 triple is (session_id, user_id, interaction_id). The
	// user the approval is directed at is the session's owning user, so
	// look up the row to stamp UserID; the approve/deny endpoint resolves
	// against the same triple.
	row, err := g.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		return executor.ApprovalDecision{}, fmt.Errorf("toolapproval: load session %s: %w", sessionID, err)
	}

	// Register the waiter before recording the interaction so an approve
	// that races in immediately after the interaction becomes resolvable
	// is buffered on the channel rather than lost.
	ch, err := g.waits.Register(sessionID, call.ID)
	if err != nil {
		return executor.ApprovalDecision{}, fmt.Errorf("toolapproval: register waiter for %s: %w", call.ID, err)
	}

	detail := map[string]any{"tool": call.Name}
	if len(call.Arguments) > 0 {
		detail["args"] = json.RawMessage(call.Arguments)
	}
	if call.SlotID != "" {
		detail["slotId"] = call.SlotID
	}
	if err := g.interactions.Put(ctx, interactionstore.Interaction{
		ID:        call.ID,
		Kind:      interactionstore.KindToolUse,
		SessionID: sessionID,
		TenantID:  tenantID,
		UserID:    row.UserID,
		Phase:     interactionstore.PhasePending,
		Detail:    detail,
		CreatedAt: g.now().UTC(),
	}); err != nil {
		g.waits.Cancel(sessionID, call.ID)
		return executor.ApprovalDecision{}, fmt.Errorf("toolapproval: record interaction %s: %w", call.ID, err)
	}

	// spec: §7.2 line 134 — tool_use_requested(tool_call_id, tool, args).
	g.publishRequested(tenantID, sessionID, call)

	var timeoutC <-chan time.Time
	if g.timeout > 0 {
		t := time.NewTimer(g.timeout)
		defer t.Stop()
		timeoutC = t.C
	}

	// spec: §7.2 — block on two wake paths. The process-local waiter
	// channel is the fast path: on the replica that recorded the
	// interaction the §15.1 approve/deny endpoint resolves the same
	// in-memory registry, so the verdict arrives on `ch` immediately. The
	// store poll is the cross-replica fallback (F-IA1): an approve POSTed
	// to a non-coordinator replica updates the Postgres-backed shared
	// interaction store but never reaches this replica's registry, so
	// without the poll the blocked call would wait out the full
	// approval_timeout. Mirrors the elicitation await's store poll in
	// pkg/gateway/mcptools.
	ticker := time.NewTicker(approvalPollInterval)
	defer ticker.Stop()
	for {
		select {
		case d, ok := <-ch:
			if !ok {
				// Cancelled (e.g. §11.4 user-revocation dismissal): treat as
				// a denial so the runtime's tool call does not execute.
				return executor.ApprovalDecision{Approved: false, Reason: "cancelled"}, nil
			}
			return executor.ApprovalDecision{Approved: d.Approved, Reason: d.Reason}, nil
		case <-timeoutC:
			g.waits.Cancel(sessionID, call.ID)
			return executor.ApprovalDecision{Approved: false, Reason: "approval_timeout"}, nil
		case <-ctx.Done():
			g.waits.Cancel(sessionID, call.ID)
			return executor.ApprovalDecision{}, ctx.Err()
		case <-ticker.C:
			d, ok, err := g.pollResolution(ctx, tenantID, sessionID, row.UserID, call.ID)
			if err != nil {
				g.waits.Cancel(sessionID, call.ID)
				return executor.ApprovalDecision{}, err
			}
			if ok {
				g.waits.Cancel(sessionID, call.ID)
				return d, nil
			}
		}
	}
}

// pollResolution reads the shared interaction store for a resolution that
// landed on another replica. It returns (decision, true, nil) once the
// interaction leaves PhasePending: PhaseApproved yields Approved:true,
// PhaseDenied yields Approved:false with the persisted deny reason, and a
// PhaseDismissed (the §11.4 user-revocation / timeout dismissal) is treated
// as a denial so the runtime's tool call does not execute (fail closed). A
// still-pending interaction returns (_, false, nil); a store error other
// than not-found is propagated. A not-found read returns (_, false, nil) so
// a transient triple miss does not abandon the wait; the process-local
// channel and the configured timeout still bound the block.
// spec: §7.2 (cross-replica approve/deny wake), F-IA1.
func (g *ToolApprovalGate) pollResolution(ctx context.Context, tenantID, sessionID, userID, callID string) (executor.ApprovalDecision, bool, error) {
	cur, err := g.interactions.Get(ctx, tenantID, sessionID, userID, callID)
	if err != nil {
		if errors.Is(err, interactionstore.ErrNotFound) {
			return executor.ApprovalDecision{}, false, nil
		}
		return executor.ApprovalDecision{}, false, fmt.Errorf("toolapproval: poll interaction %s: %w", callID, err)
	}
	switch cur.Phase {
	case interactionstore.PhaseApproved:
		return executor.ApprovalDecision{Approved: true, Reason: cur.Reason}, true, nil
	case interactionstore.PhaseDenied:
		return executor.ApprovalDecision{Approved: false, Reason: cur.Reason}, true, nil
	case interactionstore.PhaseDismissed:
		// A dismissed approval (user revocation or the resolver's timeout
		// sweep) is a denial: the tool call must not execute. Fail closed.
		reason := cur.Reason
		if reason == "" {
			reason = "dismissed"
		}
		return executor.ApprovalDecision{Approved: false, Reason: reason}, true, nil
	default:
		return executor.ApprovalDecision{}, false, nil
	}
}

// publishRequested emits the §7.2 line 134 tool_use_requested SSE event
// on the session's stream. A nil bus is a no-op (the dev posture).
func (g *ToolApprovalGate) publishRequested(tenantID, sessionID string, call executor.PendingToolCall) {
	if g.events == nil {
		return
	}
	payload := struct {
		ToolCallID string          `json:"tool_call_id"`
		Tool       string          `json:"tool"`
		Args       json.RawMessage `json:"args,omitempty"`
		SlotID     string          `json:"slotId,omitempty"`
	}{
		ToolCallID: call.ID,
		Tool:       call.Name,
		Args:       json.RawMessage(call.Arguments),
		SlotID:     call.SlotID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	g.events.PublishForTenant(tenantID, sessionID, "tool_use_requested", string(data), g.now().UTC())
}
