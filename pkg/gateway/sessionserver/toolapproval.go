// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
)

// ToolApprovalGate is the gateway-side §7.2 tool-use approval authority.
// When the pod executor reads a tool_call carrying approvalRequired:true
// it calls AwaitApproval, which records the KindToolUse interaction,
// publishes the `tool_use_requested(tool_call_id, tool, args)` SSE event,
// and blocks until the §15.1 approve/deny endpoint resolves the call
// (via the shared waiter registry), the context is cancelled, or the
// configured timeout fires. It implements executor.ApprovalGate.
// spec: §7.2 lines 124-134. F-7.2.9, F-7.2.18.
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
