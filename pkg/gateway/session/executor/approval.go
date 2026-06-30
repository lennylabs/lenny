// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"encoding/json"
)

// PendingToolCall is a §7.2 tool_call that the runtime flagged with
// `approvalRequired: true` over the §4.7 Attach stream. The gateway
// records it as a KindToolUse interaction and publishes the
// `tool_use_requested(tool_call_id, tool, args)` SSE event before
// blocking the call on a user resolution.
// spec: §7.2 line 134; schemas/lenny-adapter-jsonl.schema.json tool_call.
type PendingToolCall struct {
	// ID is the tool_call.id — the tool_call_id the §15.1
	// approve/deny endpoint resolves against.
	ID string
	// Name is the tool name (`tool` in the SSE event).
	Name string
	// Arguments is the tool's raw argument object (`args` in the SSE
	// event).
	Arguments json.RawMessage
	// SlotID is the §5.2 concurrent-workspace slot the call belongs to,
	// empty in session mode.
	SlotID string
}

// ApprovalDecision is the verdict the gateway returns to a blocked
// PodExecutor read once the user resolves a pending tool-use approval.
// Approved is true for a §7.2 line 124 approve and false for a line 125
// deny (or a timeout / cancellation, which a non-approve treats as a
// denial). Reason carries the optional deny reason.
type ApprovalDecision struct {
	Approved bool
	Reason   string
}

// ApprovalGate is the gateway-side authority a pod-backed executor
// consults when the runtime emits a tool_call requiring approval. The
// gate creates the §6/§9.2 KindToolUse interaction, publishes the
// `tool_use_requested` SSE event, and blocks until the §15.1
// approve/deny endpoint resolves the call. A nil gate leaves the
// executor's prior behavior intact (the approval-required frame is
// skipped like any other intermediate frame). spec: §7.2 lines 124-134.
// F-7.2.9, F-7.2.18.
type ApprovalGate interface {
	// AwaitApproval records the pending tool-use approval for the
	// session, emits the SSE event, and blocks until the user resolves
	// it (approve / deny), the context is cancelled, or the gate's own
	// timeout fires. A returned error aborts the in-flight Send; a
	// non-error ApprovalDecision with Approved=false is a denial the
	// executor relays to the runtime as a tool_result error.
	AwaitApproval(ctx context.Context, tenantID, sessionID string, call PendingToolCall) (ApprovalDecision, error)
}
