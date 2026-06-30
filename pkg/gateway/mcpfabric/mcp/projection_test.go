// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
)

// decodeFrame unmarshals a projected wire frame into a generic map so a
// test can assert on the method and params without a fixed struct.
func decodeFrame(t *testing.T, b []byte) map[string]any {
	t.Helper()
	if b == nil {
		t.Fatalf("projection returned nil frame")
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("frame is not valid JSON: %v\n%s", err, b)
	}
	return m
}

func ev(typ, sessionID, data string) sessionevents.Event {
	return sessionevents.Event{Seq: 7, SessionID: sessionID, Type: typ, Data: data, Timestamp: time.Unix(0, 0)}
}

// spec: §15.2 lines 1356-1368 — classifier lifts live bus types onto the
// §15.0 closed SessionEventKind enum, with the terminal status_change
// branch.
func TestClassifyEventKind_spec_15_2_1356(t *testing.T) {
	cases := []struct {
		typ  string
		data string
		want sessionEventKind
	}{
		{"status_change", `{"state":"running"}`, kindStateChange},
		{"status_change", `{"state":"completed"}`, kindTerminated},
		{"status_change", `{"state":"failed"}`, kindTerminated},
		{"status_change", `{"state":"cancelled"}`, kindTerminated},
		{"status_change", `{"state":"expired"}`, kindTerminated},
		{"session_complete", `{"status":"completed"}`, kindTerminated},
		{"response", `{}`, kindOutput},
		{"response_degraded", `{}`, kindOutput},
		{"agent_output", `{}`, kindOutput},
		{"elicitation_request", `{}`, kindElicitation},
		{"tool_use_requested", `{}`, kindToolUse},
		{"tool_use_completed", `{}`, kindToolUse},
		{"error", `{}`, kindError},
		{"child_failed", `{}`, kindError},
		{"submit_error", `{}`, kindError},
		{"message_delivered", `{}`, kindUnclassified},
		{"session_expiring_soon", `{}`, kindUnclassified},
		{"workspace_plan_warning", `{}`, kindUnclassified},
		{"children_reattached", `{}`, kindUnclassified},
	}
	for _, c := range cases {
		if got := classifyEventKind(c.typ, []byte(c.data)); got != c.want {
			t.Errorf("classifyEventKind(%q,%q)=%q want %q", c.typ, c.data, got, c.want)
		}
	}
}

// spec: §15.2 line 1360; §8.8 — a non-terminal state_change projects to
// notifications/tasks/statusUpdate with the §8.8-mapped status in
// params.metadata.to.
func TestProjectStateChange_spec_15_2_1360(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("status_change", "sess-1", `{"state":"running"}`)))
	if m["method"] != "notifications/tasks/statusUpdate" {
		t.Fatalf("method=%v", m["method"])
	}
	params := m["params"].(map[string]any)
	if params["taskId"] != "sess-1" {
		t.Errorf("taskId=%v", params["taskId"])
	}
	if params["status"] != "working" {
		t.Errorf("status=%v want working", params["status"])
	}
	if _, final := params["final"]; final {
		t.Errorf("non-terminal frame must not set final")
	}
	meta := params["metadata"].(map[string]any)
	if meta["to"] != "working" {
		t.Errorf("metadata.to=%v want working", meta["to"])
	}
}

// spec: §8.8 lines 873-883 — the suspended/resume_pending session states
// surface as working with the suspended/resuming metadata annotations.
func TestProjectStateChangeSubStateAnnotations_spec_8_8_873(t *testing.T) {
	for _, c := range []struct {
		state string
		flag  string
	}{{"suspended", "suspended"}, {"resume_pending", "resuming"}} {
		m := decodeFrame(t, projectMCPSessionEvent(ev("status_change", "s", `{"state":"`+c.state+`"}`)))
		params := m["params"].(map[string]any)
		if params["status"] != "working" {
			t.Errorf("%s status=%v want working", c.state, params["status"])
		}
		meta := params["metadata"].(map[string]any)
		if meta[c.flag] != true {
			t.Errorf("%s metadata.%s not set: %v", c.state, c.flag, meta)
		}
	}
}

// spec: §8.8 lines 857-865 — input_required and awaiting_client_action
// both surface as the MCP input_required status.
func TestProjectStateChangeInputRequired_spec_8_8_857(t *testing.T) {
	for _, st := range []string{"input_required", "awaiting_client_action"} {
		m := decodeFrame(t, projectMCPSessionEvent(ev("status_change", "s", `{"state":"`+st+`"}`)))
		params := m["params"].(map[string]any)
		if params["status"] != "input_required" {
			t.Errorf("%s status=%v want input_required", st, params["status"])
		}
	}
}

// spec: §15.2 line 1368; §8.8 — a terminal status_change projects to the
// MCP Tasks final-state frame (final:true) with the §8.8-mapped terminal
// status and the termination detail surfaced in metadata.
func TestProjectTerminated_spec_15_2_1368(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("status_change", "sess-2", `{"state":"failed","failureReason":"oom"}`)))
	if m["method"] != "notifications/tasks/statusUpdate" {
		t.Fatalf("method=%v", m["method"])
	}
	params := m["params"].(map[string]any)
	if params["status"] != "failed" {
		t.Errorf("status=%v want failed", params["status"])
	}
	if params["final"] != true {
		t.Errorf("terminal frame must set final:true, got %v", params["final"])
	}
	meta := params["metadata"].(map[string]any)
	if meta["terminationDetail"] != "oom" {
		t.Errorf("terminationDetail=%v want oom", meta["terminationDetail"])
	}
}

// spec: §8.8 line 862 — cancelled maps to MCP American-spelling canceled;
// expired maps to failed with an expired error annotation.
func TestProjectTerminatedSpellingAndExpired_spec_8_8_862(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("status_change", "s", `{"state":"cancelled"}`)))
	if m["params"].(map[string]any)["status"] != "canceled" {
		t.Errorf("cancelled must map to canceled, got %v", m["params"])
	}
	m = decodeFrame(t, projectMCPSessionEvent(ev("status_change", "s", `{"state":"expired"}`)))
	params := m["params"].(map[string]any)
	if params["status"] != "failed" {
		t.Errorf("expired must map to failed, got %v", params["status"])
	}
	if params["metadata"].(map[string]any)["errorCode"] != "expired" {
		t.Errorf("expired must carry errorCode annotation, got %v", params["metadata"])
	}
}

// spec: §15.2 line 1361 — output projects to a working-status task frame
// carrying the translated MessagePart content (text block).
func TestProjectOutputText_spec_15_2_1361(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("response", "s", `{"type":"text","text":"hello"}`)))
	if m["method"] != "notifications/tasks/statusUpdate" {
		t.Fatalf("method=%v", m["method"])
	}
	params := m["params"].(map[string]any)
	if params["status"] != "working" {
		t.Errorf("status=%v want working", params["status"])
	}
	content := params["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len=%d", len(content))
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "hello" {
		t.Errorf("content block=%v", block)
	}
}

// spec: §15.2 line 1361 — a ref MessagePart projects to a resource_link
// content block carrying the reference for client dereference.
func TestProjectOutputRef_spec_15_2_1361(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("agent_output", "s", `{"type":"file","ref":"blob://abc"}`)))
	content := m["params"].(map[string]any)["content"].([]any)
	block := content[0].(map[string]any)
	if block["type"] != "resource_link" || block["uri"] != "blob://abc" {
		t.Errorf("ref block=%v", block)
	}
}

// spec: §15.2 line 1362 — an elicitation projects to a native MCP
// elicitation/create request (carries an id, message, requestedSchema,
// and §9.2 provenance metadata).
func TestProjectElicitationCreate_spec_15_2_1362(t *testing.T) {
	data := `{"elicitationId":"el-1","message":"Pick one","schema":{"type":"string"},"originPod":"pod-9","initiatorType":"agent","delegationDepth":2,"originRuntime":"echo"}`
	m := decodeFrame(t, projectMCPSessionEvent(ev("elicitation_request", "sess-3", data)))
	if m["method"] != "elicitation/create" {
		t.Fatalf("method=%v want elicitation/create", m["method"])
	}
	if m["id"] != "elicit:el-1" {
		t.Errorf("id=%v want elicit:el-1 (so the reply correlates)", m["id"])
	}
	params := m["params"].(map[string]any)
	if params["message"] != "Pick one" {
		t.Errorf("message=%v", params["message"])
	}
	if params["requestedSchema"] == nil {
		t.Errorf("requestedSchema must be carried, params=%v", params)
	}
	meta := params["_meta"].(map[string]any)
	if meta["lenny/originPod"] != "pod-9" || meta["lenny/initiatorType"] != "agent" {
		t.Errorf("provenance _meta=%v", meta)
	}
	if meta["lenny/delegationDepth"].(float64) != 2 {
		t.Errorf("delegationDepth=%v", meta["lenny/delegationDepth"])
	}
}

// spec: §15.2 line 1363 — the approval-required requested phase is the
// only tool_use projection that uses elicitation/create. The live
// tool_use_requested event (emitted by the approval gate) is always
// approval-required.
func TestProjectToolUseApprovalElicitation_spec_15_2_1363(t *testing.T) {
	data := `{"tool_call_id":"tc-1","tool":"shell","args":{"cmd":"ls"},"slotId":"slot-1"}`
	m := decodeFrame(t, projectMCPSessionEvent(ev("tool_use_requested", "sess-4", data)))
	if m["method"] != "elicitation/create" {
		t.Fatalf("approval-required requested phase must project to elicitation/create, got %v", m["method"])
	}
	if m["id"] != "toolapprove:tc-1" {
		t.Errorf("id=%v want toolapprove:tc-1", m["id"])
	}
	meta := m["params"].(map[string]any)["_meta"].(map[string]any)
	if meta["lenny/toolCallId"] != "tc-1" || meta["lenny/tool"] != "shell" {
		t.Errorf("tool-approval _meta=%v", meta)
	}
	if meta["lenny/kind"] != "tool_use_approval" {
		t.Errorf("kind=%v", meta["lenny/kind"])
	}
}

// spec: §15.2 lines 1364-1366, 1372 — non-requested tool_use phases
// (approved/denied/completed) project to notifications/lenny/toolCall, an
// observability frame with no client response expected.
func TestProjectToolUseNotification_spec_15_2_1364(t *testing.T) {
	for _, phase := range []string{"approved", "denied", "completed"} {
		data := `{"tool_call_id":"tc-2","tool":"shell","phase":"` + phase + `","result":[{"type":"text","text":"ok"}]}`
		m := decodeFrame(t, projectMCPSessionEvent(ev("tool_use_"+phase, "s", data)))
		if m["method"] != "notifications/lenny/toolCall" {
			t.Errorf("phase %s method=%v want notifications/lenny/toolCall", phase, m["method"])
		}
		if _, hasID := m["id"]; hasID {
			t.Errorf("phase %s notification frame must not carry a JSON-RPC id", phase)
		}
		params := m["params"].(map[string]any)
		if params["phase"] != phase {
			t.Errorf("phase=%v want %v", params["phase"], phase)
		}
		if params["toolCallId"] != "tc-2" {
			t.Errorf("toolCallId=%v", params["toolCallId"])
		}
	}
}

// spec: §15.2 line 1367 — a non-terminal error projects to
// notifications/lenny/error carrying the shared error taxonomy fields.
func TestProjectError_spec_15_2_1367(t *testing.T) {
	data := `{"code":"UPSTREAM_TIMEOUT","category":"UPSTREAM","message":"slow","retryable":true}`
	m := decodeFrame(t, projectMCPSessionEvent(ev("error", "sess-5", data)))
	if m["method"] != "notifications/lenny/error" {
		t.Fatalf("method=%v", m["method"])
	}
	params := m["params"].(map[string]any)
	if params["code"] != "UPSTREAM_TIMEOUT" || params["category"] != "UPSTREAM" || params["retryable"] != true {
		t.Errorf("error params=%v", params)
	}
	if params["sessionId"] != "sess-5" {
		t.Errorf("sessionId=%v", params["sessionId"])
	}
}

// spec: §15.2 line 1370 — a bus event outside the closed SessionEventKind
// enum falls back to the generic notifications/lenny/sessionEvent frame so
// the client still observes it.
func TestProjectUnclassifiedFallback_spec_15_2_1370(t *testing.T) {
	m := decodeFrame(t, projectMCPSessionEvent(ev("message_delivered", "sess-6", `{"role":"user"}`)))
	if m["method"] != "notifications/lenny/sessionEvent" {
		t.Fatalf("method=%v want generic fallback", m["method"])
	}
	params := m["params"].(map[string]any)
	if params["type"] != "message_delivered" {
		t.Errorf("type=%v", params["type"])
	}
	if params["sessionId"] != "sess-6" {
		t.Errorf("sessionId=%v", params["sessionId"])
	}
}

// Every per-kind frame must be valid JSON and carry jsonrpc:"2.0" so the
// SSE id: line + resumeFromSeq replay the frame verbatim. spec: §15.2
// lines 1356, 1374.
func TestEveryProjectedFrameIsValidJSONRPC_spec_15_2_1374(t *testing.T) {
	samples := []sessionevents.Event{
		ev("status_change", "s", `{"state":"running"}`),
		ev("status_change", "s", `{"state":"completed"}`),
		ev("response", "s", `{"type":"text","text":"x"}`),
		ev("elicitation_request", "s", `{"elicitationId":"e","message":"m"}`),
		ev("tool_use_requested", "s", `{"tool_call_id":"t","tool":"sh"}`),
		ev("error", "s", `{"code":"X"}`),
		ev("message_delivered", "s", `{}`),
	}
	for _, s := range samples {
		m := decodeFrame(t, projectMCPSessionEvent(s))
		if m["jsonrpc"] != "2.0" {
			t.Errorf("type %q frame missing jsonrpc 2.0: %v", s.Type, m)
		}
		if m["method"] == nil {
			t.Errorf("type %q frame missing method", s.Type)
		}
	}
}
