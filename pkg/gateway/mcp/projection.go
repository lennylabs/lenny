// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
)

// The §15.2.1 "Per-kind MCP wire projection" requires the
// MCPAdapter.OutboundChannel.Send implementation to map each
// SessionEvent.Kind to exactly one MCP wire frame on the session's SSE
// stream. The live session-event bus carries string-typed events
// (status_change, response, elicitation_request, tool_use_requested,
// ...); this file is the classifier that lifts those onto the §15.0
// closed SessionEventKind enum and the projector that renders each kind
// as its method-specific JSON-RPC frame. Event types that fall outside
// the closed enum (message_delivered, session_expiring_soon,
// workspace_plan_warning, children_reattached, ...) project to the
// generic notifications/lenny/sessionEvent frame so a client still
// observes them without the transport interrupting the stream.
//
// spec: §15.2 lines 1356-1374 (per-kind wire projection); §8.8 lines
// 857-883 (Lenny-state → MCP-Tasks mapping). F-15.2.13, F-15.2.14.

// sessionEventKind mirrors the §15.0 closed SessionEventKind enum. It is
// duplicated here as an unexported string type so the transport package
// classifies a live bus event without importing adapterregistry (which
// would couple the wire transport to the adapter-registration layer).
// spec: §15 line 318.
type sessionEventKind string

const (
	kindStateChange  sessionEventKind = "state_change"
	kindOutput       sessionEventKind = "output"
	kindElicitation  sessionEventKind = "elicitation"
	kindToolUse      sessionEventKind = "tool_use"
	kindError        sessionEventKind = "error"
	kindTerminated   sessionEventKind = "terminated"
	kindUnclassified sessionEventKind = ""
)

// terminalLennyStates is the §8.8 set of task/session states that map to
// a terminal MCP Tasks final-state frame. A status_change carrying one of
// these classifies as SessionEventTerminated rather than
// SessionEventStateChange so the projection emits the task-completion
// frame and the client knows to expect the stream to close.
var terminalLennyStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
	"expired":   true,
}

// classifyEventKind lifts a live session-event-bus type string onto the
// §15.0 closed SessionEventKind enum, consulting the payload where the
// kind depends on the event's state (a terminal status_change maps to
// SessionEventTerminated). It returns kindUnclassified for bus event
// types outside the closed enum so the caller falls back to the generic
// notifications/lenny/sessionEvent frame. spec: §15.2 lines 1356-1368.
func classifyEventKind(eventType string, data []byte) sessionEventKind {
	switch eventType {
	case "status_change":
		if terminalLennyStates[stateFromPayload(data)] {
			return kindTerminated
		}
		return kindStateChange
	case "session_complete", "terminated":
		return kindTerminated
	case "response", "response_degraded", "agent_output":
		return kindOutput
	case "elicitation_request":
		return kindElicitation
	case "error", "child_failed", "submit_error":
		return kindError
	}
	// tool_use_requested and any future tool_use_* phase events.
	if eventType == "tool_use_requested" || hasPrefix(eventType, "tool_use") {
		return kindToolUse
	}
	return kindUnclassified
}

// stateFromPayload extracts the Lenny state string from a session event
// payload, consulting `state` first (the status_change shape) and then
// `status` (the materialized TaskResult shape carried by
// session_complete). Returns "" when neither is present.
func stateFromPayload(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if s, ok := m["state"].(string); ok && s != "" {
		return s
	}
	if s, ok := m["status"].(string); ok && s != "" {
		return s
	}
	return ""
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// projectMCPSessionEvent renders one session-bus event as the §15.2.1
// per-kind MCP wire frame. Every frame is a JSON-RPC message; the SSE
// `id:` line (written by the caller from ev.Seq) lets resumeFromSeq /
// Last-Event-ID replay the frame verbatim. An event outside the closed
// SessionEventKind enum falls back to the generic
// notifications/lenny/sessionEvent frame. A marshal failure returns nil
// and the caller drops the frame. spec: §15.2 lines 1356-1374.
func projectMCPSessionEvent(ev sessionevents.Event) []byte {
	data := []byte(ev.Data)
	switch classifyEventKind(ev.Type, data) {
	case kindStateChange:
		return projectStatusUpdate(ev, false)
	case kindTerminated:
		return projectStatusUpdate(ev, true)
	case kindOutput:
		return projectOutput(ev)
	case kindElicitation:
		return projectElicitationCreate(ev)
	case kindToolUse:
		return projectToolUse(ev)
	case kindError:
		return projectError(ev)
	default:
		return marshalGenericSessionEvent(ev)
	}
}

// lennyStateToMCPTask maps a Lenny session/task state to its MCP Tasks
// surface per the §8.8 protocol-mapping tables. It returns the MCP status
// string plus the metadata annotations the spec attaches to a state
// (subState for input_required-bearing transitions, suspended/resuming
// for the non-terminal recovery states). spec: §8.8 lines 857-883.
func lennyStateToMCPTask(state string) (status string, metadata map[string]any) {
	metadata = map[string]any{}
	switch state {
	case "submitted", "created", "finalizing", "ready", "starting":
		// Pre-running states collapse to submitted: external protocols
		// have no equivalent granularity for pre-execution phases.
		return "submitted", metadata
	case "running":
		return "working", metadata
	case "completed":
		return "completed", metadata
	case "failed":
		return "failed", metadata
	case "cancelled":
		// MCP uses American spelling; Lenny uses cancelled internally.
		return "canceled", metadata
	case "expired":
		// §8.8: expired has no MCP equivalent and always maps to failed
		// with a structured expired:* error code.
		metadata["errorCode"] = "expired"
		return "failed", metadata
	case "input_required", "awaiting_client_action":
		return "input_required", metadata
	case "suspended":
		// §8.8 session-level: surfaced as working + metadata.suspended so
		// a client does not mistake the pause for a request for input.
		metadata["suspended"] = true
		return "working", metadata
	case "resume_pending":
		metadata["resuming"] = true
		return "working", metadata
	default:
		// An unmodeled state is surfaced as working rather than dropped so
		// the client sees activity; the raw state is preserved for debug.
		metadata["lennyState"] = state
		return "working", metadata
	}
}

// projectStatusUpdate renders a SessionEventStateChange or
// SessionEventTerminated as a notifications/tasks/statusUpdate frame. The
// non-terminal projection carries {to, subState?} in params.metadata; the
// terminal projection sets final:true and surfaces the
// TerminationReason.Detail under result.metadata.terminationDetail. The
// state is translated through the §8.8 Lenny-state → MCP-Tasks mapping.
// spec: §15.2 lines 1360, 1368; §8.8 lines 857-883.
func projectStatusUpdate(ev sessionevents.Event, terminal bool) []byte {
	state := stateFromPayload([]byte(ev.Data))
	status, metadata := lennyStateToMCPTask(state)
	params := map[string]any{
		"taskId": ev.SessionID,
		"status": status,
	}
	if terminal {
		params["final"] = true
		if detail := terminationDetail([]byte(ev.Data)); detail != "" {
			metadata["terminationDetail"] = detail
		}
	} else {
		// §15.2.1 line 1360: from/to/subState live in params.metadata. The
		// live status_change payload carries only the destination state, so
		// `to` is always populated and `from` is omitted when unknown.
		metadata["to"] = status
	}
	if len(metadata) > 0 {
		params["metadata"] = metadata
	}
	return marshalNotification("notifications/tasks/statusUpdate", params)
}

// terminationDetail pulls the §8.8 TerminationReason.Detail out of a
// terminal event payload, consulting the failure-reason / detail fields
// the gateway stamps onto session_complete and status_change(terminal).
func terminationDetail(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	for _, k := range []string{"terminationDetail", "detail", "failureReason", "failure_reason", "reason"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// projectOutput renders a SessionEventOutput as a streaming task-content
// frame: the §15.2.1 row maps output to "MCP streaming content block(s)
// appended to the attach_session task response" with no standalone
// notification method, so the projection emits a working-status task
// frame carrying the translated OutputPart(s) under params.content.
// Per §8.8 agent output is produced while the task is in the working
// state; the presence of params.content distinguishes a content append
// from a pure state transition. spec: §15.2 line 1361; §8.8.
func projectOutput(ev sessionevents.Event) []byte {
	params := map[string]any{
		"taskId":  ev.SessionID,
		"status":  "working",
		"content": outputContentBlocks([]byte(ev.Data)),
	}
	return marshalNotification("notifications/tasks/statusUpdate", params)
}

// outputContentBlocks translates the OutputPart payload the gateway
// publishes (`{type, text, ref}`) into the MCP content-block array the
// streaming task response carries. A `text` part becomes an MCP text
// content block; a `ref` part becomes a resource_link block carrying the
// reference so the client can dereference it. spec: §15.2 line 1361
// (Translation Fidelity Matrix MCP column / ref dereference obligation).
func outputContentBlocks(data []byte) []map[string]any {
	var p struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Ref  string `json:"ref"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &p)
	}
	if p.Ref != "" {
		return []map[string]any{{"type": "resource_link", "uri": p.Ref}}
	}
	return []map[string]any{{"type": "text", "text": p.Text}}
}

// projectElicitationCreate renders a SessionEventElicitation as a native
// MCP elicitation/create request. The client's reply flows back over the
// same SSE channel; the gateway routes it through the §9.2 hop-by-hop
// elicitation chain. The request carries the elicitation_id as the
// JSON-RPC id so the reply correlates, the {message, requestedSchema}
// pair, and the §9.2 provenance metadata the client UI displays.
// spec: §15.2 line 1362; §9.2 lines 56-82.
func projectElicitationCreate(ev sessionevents.Event) []byte {
	var p struct {
		ElicitationID   string          `json:"elicitationId"`
		Message         string          `json:"message"`
		Schema          json.RawMessage `json:"schema,omitempty"`
		OriginPod       string          `json:"originPod"`
		InitiatorType   string          `json:"initiatorType"`
		DelegationDepth int             `json:"delegationDepth"`
		OriginRuntime   string          `json:"originRuntime,omitempty"`
	}
	if len(ev.Data) > 0 {
		_ = json.Unmarshal([]byte(ev.Data), &p)
	}
	params := map[string]any{
		"message": p.Message,
		"_meta": map[string]any{
			"lenny/elicitationId":   p.ElicitationID,
			"lenny/originPod":       p.OriginPod,
			"lenny/initiatorType":   p.InitiatorType,
			"lenny/delegationDepth": p.DelegationDepth,
			"lenny/originRuntime":   p.OriginRuntime,
			"lenny/sessionId":       ev.SessionID,
		},
	}
	if len(p.Schema) > 0 {
		params["requestedSchema"] = p.Schema
	}
	return marshalServerRequest(elicitationRequestID(p.ElicitationID, ev.Seq), "elicitation/create", params)
}

// projectToolUse renders a SessionEventToolUse. The §15.2.1 phase-split
// rules: the approval-required requested phase is the only tool_use
// projection that uses MCP Elicitation (elicitation/create carrying the
// tool-approval prompt); every other phase (auto-approved/replay
// requested, approved, denied, completed) is a notifications/lenny/
// toolCall observability frame. The live tool_use_requested event is
// emitted by the approval gate, so it is always approval-required.
// spec: §15.2 lines 1363-1366, 1372.
func projectToolUse(ev sessionevents.Event) []byte {
	var p struct {
		ToolCallID string          `json:"tool_call_id"`
		Tool       string          `json:"tool"`
		Args       json.RawMessage `json:"args,omitempty"`
		SlotID     string          `json:"slotId,omitempty"`
		Phase      string          `json:"phase,omitempty"`
		Result     json.RawMessage `json:"result,omitempty"`
	}
	if len(ev.Data) > 0 {
		_ = json.Unmarshal([]byte(ev.Data), &p)
	}
	phase := p.Phase
	if phase == "" {
		phase = "requested"
	}
	// The approval gate only publishes tool_use_requested for calls that
	// require approval; the requested phase therefore projects to an
	// approval elicitation. An explicit non-requested phase (or a future
	// auto-approved annotation) projects to the toolCall notification.
	approvalRequired := ev.Type == "tool_use_requested" && phase == "requested"
	if approvalRequired {
		params := map[string]any{
			"message": "Approve tool call: " + p.Tool,
			"_meta": map[string]any{
				"lenny/toolCallId": p.ToolCallID,
				"lenny/tool":       p.Tool,
				"lenny/arguments":  rawOrNull(p.Args),
				"lenny/sessionId":  ev.SessionID,
				"lenny/kind":       "tool_use_approval",
			},
		}
		return marshalServerRequest(toolApprovalRequestID(p.ToolCallID, ev.Seq), "elicitation/create", params)
	}
	notifyParams := map[string]any{
		"toolCallId": p.ToolCallID,
		"tool":       p.Tool,
		"phase":      phase,
		"sessionId":  ev.SessionID,
	}
	if len(p.Args) > 0 {
		notifyParams["arguments"] = json.RawMessage(p.Args)
	}
	if len(p.Result) > 0 {
		notifyParams["result"] = json.RawMessage(p.Result)
	}
	return marshalNotification("notifications/lenny/toolCall", notifyParams)
}

// projectError renders a SessionEventError as a notifications/lenny/error
// frame carrying the {code, category, message, retryable, details?}
// shared error taxonomy. Non-terminal mid-session errors only; terminal
// errors are surfaced via the SessionEventTerminated task frame. The live
// error payloads vary by source (error / child_failed / submit_error), so
// the projection passes the payload through under params unchanged.
// spec: §15.2 line 1367.
func projectError(ev sessionevents.Event) []byte {
	params := map[string]any{"sessionId": ev.SessionID}
	var m map[string]any
	if len(ev.Data) > 0 && json.Unmarshal([]byte(ev.Data), &m) == nil {
		for k, v := range m {
			params[k] = v
		}
	}
	return marshalNotification("notifications/lenny/error", params)
}

// elicitationRequestID derives the JSON-RPC id for an elicitation/create
// request. The elicitation_id is the natural correlation key; the seq is
// the fallback when the payload omitted it.
func elicitationRequestID(elicitationID string, seq uint64) string {
	if elicitationID != "" {
		return "elicit:" + elicitationID
	}
	return "elicit:seq:" + uintToStr(seq)
}

// toolApprovalRequestID derives the JSON-RPC id for a tool-approval
// elicitation/create request from the tool_call_id.
func toolApprovalRequestID(toolCallID string, seq uint64) string {
	if toolCallID != "" {
		return "toolapprove:" + toolCallID
	}
	return "toolapprove:seq:" + uintToStr(seq)
}

func uintToStr(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func rawOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

// jsonRPCServerRequest is a server-initiated JSON-RPC 2.0 request — the
// wire form of an elicitation/create the gateway pushes to the client
// over the §15.2 SSE channel. Unlike a notification it carries an id so
// the client's reply correlates.
type jsonRPCServerRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func marshalNotification(method string, params any) []byte {
	b, err := json.Marshal(jsonRPCNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return nil
	}
	return b
}

func marshalServerRequest(id, method string, params any) []byte {
	b, err := json.Marshal(jsonRPCServerRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil
	}
	return b
}

// marshalGenericSessionEvent renders an event outside the §15.0 closed
// SessionEventKind enum as the generic notifications/lenny/sessionEvent
// frame under the §15.2 line 1370 notifications/lenny/* extension
// namespace, which conforming clients ignore if unrecognized. This is the
// projection fallback for bus event types that are not part of the
// per-kind table (message_delivered, session_expiring_soon,
// workspace_plan_warning, children_reattached, ...). spec: §15.2 line 1370.
func marshalGenericSessionEvent(ev sessionevents.Event) []byte {
	data := json.RawMessage(ev.Data)
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	return marshalNotification("notifications/lenny/sessionEvent", map[string]any{
		"seq":       ev.Seq,
		"sessionId": ev.SessionID,
		"type":      ev.Type,
		"data":      data,
		"timestamp": ev.Timestamp.UTC().Format(time.RFC3339Nano),
	})
}
