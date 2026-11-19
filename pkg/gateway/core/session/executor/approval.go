// SPDX-License-Identifier: MIT

package executor

import "github.com/lennylabs/lenny/pkg/gateway/seam"

// PendingToolCall is a §7.2 tool_call that the runtime flagged with
// `approvalRequired: true` over the §4.7 Attach stream: its tool_call.id,
// which the approve/deny endpoint resolves against, its tool name, and its
// raw argument object. The gateway records it as a KindToolUse interaction
// and publishes the `tool_use_requested(tool_call_id, tool, args)` SSE event
// before blocking the call on a user resolution. It is the value the seam
// interface carries to the platform tools seam's approval gate.
// spec: §7.2; schemas/lenny-adapter-jsonl.schema.json tool_call.
type PendingToolCall = seam.PendingToolCall

// ApprovalDecision is the verdict the gateway returns to a blocked
// PodExecutor read once the user resolves a pending tool-use approval.
// Approved is true for the §7.2 tool-use approve endpoint and false for the
// deny endpoint (or a timeout or cancellation, which a non-approve treats as
// a denial). Reason carries the optional deny reason.
type ApprovalDecision = seam.ApprovalDecision

// ApprovalGate is the gateway-side authority a pod-backed executor consults
// when the runtime emits a tool_call requiring approval: the platform tools
// seam's approval gate, reached through GWSEAM-AwaitApproval. The gate
// creates the KindToolUse interaction, publishes the `tool_use_requested`
// SSE event, and blocks until the approve/deny endpoint resolves the call,
// the context is cancelled, or the gate's own timeout fires. A returned
// error aborts the in-flight Send; a decision with Approved false is a
// denial the executor relays to the runtime as a tool_result error. A nil
// gate leaves the executor's prior behavior intact (the approval-required
// frame is skipped like any other intermediate frame). spec: §7.2.
// F-7.2.9, F-7.2.18.
type ApprovalGate = seam.ToolApprovalGate
