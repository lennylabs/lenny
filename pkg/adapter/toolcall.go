// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/adapter/localtools"
)

// toolResultPart is one §15.4.1 OutputPart in a tool_result's content
// array. An adapter-local tool result uses a single inline text part.
type toolResultPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

// toolResultFrame is the §15.4.1 tool_result frame the adapter writes
// to the runtime's stdin in answer to an adapter-local tool_call.
type toolResultFrame struct {
	Type    string           `json:"type"`
	ID      string           `json:"id"`
	Content []toolResultPart `json:"content"`
	IsError bool             `json:"isError"`
}

// extractToolCallID returns the id field of a JSONL tool_call frame, or
// the empty string when the frame is not a tool_call or cannot be
// parsed. Used to record the §10.1 line 173 last_tool_call_id on the
// adapter so a subsequent CheckpointBarrier ack surfaces it.
func extractToolCallID(frame []byte) string {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return ""
	}
	if probe.Type != "tool_call" {
		return ""
	}
	return probe.ID
}

// extractToolCallName returns the name field of a JSONL tool_call frame,
// or the empty string when the frame is not a tool_call or cannot be
// parsed. Used to stamp the §16.3 `session.tool_call` span with the
// invoked tool's name. The name is a tool identifier (e.g. read_file),
// never tool arguments, so it carries no secrets or PII.
func extractToolCallName(frame []byte) string {
	var probe struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return ""
	}
	if probe.Type != "tool_call" {
		return ""
	}
	return probe.Name
}

// HandleToolCall inspects one stdout frame from the runtime. When the
// frame is a tool_call for an adapter-local tool (read_file,
// write_file, list_dir, delete_file), it runs the tool via
// localtools.Dispatch against workspaceRoot and returns the matching
// tool_result frame to write back to the runtime's stdin. handled is
// false for any other frame — a non-tool_call frame, a malformed
// frame, or a tool_call for a platform MCP tool (lenny/...) — which
// the caller relays unchanged.
func HandleToolCall(frame []byte, workspaceRoot string) (result []byte, handled bool) {
	var call struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(frame, &call); err != nil {
		return nil, false
	}
	if call.Type != "tool_call" || !localtools.IsLocalTool(call.Name) {
		return nil, false
	}
	res := localtools.Dispatch(workspaceRoot, call.Name, call.Arguments)
	encoded, err := json.Marshal(toolResultFrame{
		Type:    "tool_result",
		ID:      call.ID,
		Content: []toolResultPart{{Type: "text", Inline: res.Content}},
		IsError: res.IsError,
	})
	if err != nil {
		return nil, false
	}
	return encoded, true
}
