// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// spec: §8.2 lines 12-34 — the `lenny/delegate_task` tool surface matches
// the normative `(target: string, task: TaskSpec, lease_slice?: LeaseSlice)`
// signature. F-8.2.1.

// TestDelegateTaskSchemaMatchesSection82Contract asserts the advertised
// inputSchema carries the opaque `target` and the `task` envelope and no
// longer carries the pre-§8.2 `runtimeRef` / `taskInput` / per-call
// `maxDepth` fields. spec: §8.2 lines 12-34. F-8.2.1.
func TestDelegateTaskSchemaMatchesSection82Contract_spec_8_2(t *testing.T) {
	srv, _ := newMCPForDelegate(t, newRecordingExecutor(), nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var schema map[string]any
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "lenny/delegate_task" {
			schema, _ = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("delegate_task tool or its inputSchema not found")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, want := range []string{"target", "task"} {
		if _, ok := props[want]; !ok {
			t.Errorf("delegate_task inputSchema missing §8.2 field %q", want)
		}
	}
	for _, gone := range []string{"runtimeRef", "taskInput", "maxDepth"} {
		if _, ok := props[gone]; ok {
			t.Errorf("delegate_task inputSchema still advertises pre-§8.2 field %q", gone)
		}
	}
	// `target` must be in the required set; `runtimeRef` must not be.
	required, _ := schema["required"].([]any)
	var hasTarget bool
	for _, r := range required {
		if r == "target" {
			hasTarget = true
		}
		if r == "runtimeRef" {
			t.Error("delegate_task still requires pre-§8.2 field runtimeRef")
		}
	}
	if !hasTarget {
		t.Error("delegate_task inputSchema does not require `target`")
	}
	// `task.input` must be advertised as the MessagePart[] envelope.
	taskSchema, _ := props["task"].(map[string]any)
	taskProps, _ := taskSchema["properties"].(map[string]any)
	if _, ok := taskProps["input"]; !ok {
		t.Error("delegate_task task envelope missing `input` (MessagePart[])")
	}
	if _, ok := taskProps["workspaceFiles"]; !ok {
		t.Error("delegate_task task envelope missing `workspaceFiles`")
	}
}

// TestDelegateTaskSchemaAdvertisesCredentialPropagation asserts the
// advertised inputSchema carries the §8.3 credentialPropagation lease
// field as a closed enum (inherit, independent, deny), so a client can
// discover the propagation mode from the tool contract. spec: §8.3.
func TestDelegateTaskSchemaAdvertisesCredentialPropagation_spec_8_3(t *testing.T) {
	srv, _ := newMCPForDelegate(t, newRecordingExecutor(), nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var schema map[string]any
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "lenny/delegate_task" {
			schema, _ = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("delegate_task tool or its inputSchema not found")
	}
	props, _ := schema["properties"].(map[string]any)
	cp, ok := props["credentialPropagation"].(map[string]any)
	if !ok {
		t.Fatal("delegate_task inputSchema missing §8.3 field credentialPropagation")
	}
	enumRaw, _ := cp["enum"].([]any)
	got := map[string]bool{}
	for _, e := range enumRaw {
		if s, ok := e.(string); ok {
			got[s] = true
		}
	}
	for _, want := range []string{"inherit", "independent", "deny"} {
		if !got[want] {
			t.Errorf("credentialPropagation enum missing %q; got %v", want, enumRaw)
		}
	}
	if len(enumRaw) != 3 {
		t.Errorf("credentialPropagation enum = %v, want exactly the closed §8.3 set", enumRaw)
	}
}

// TestDelegateTaskRejectsMissingTarget asserts an omitted opaque `target`
// is rejected with VALIDATION_ERROR before any side effect. spec: §8.2
// line 13. F-8.2.1.
func TestDelegateTaskRejectsMissingTarget_spec_8_2(t *testing.T) {
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, nil)
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","task":{"input":[{"type":"text","inline":"x"}]}}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "VALIDATION_ERROR" {
		t.Errorf("envelope.code = %v, want VALIDATION_ERROR", env["code"])
	}
	if got := rec.received("sess_child"); len(got) != 0 {
		t.Errorf("no child should be created on a missing target; child received %v", got)
	}
}

// TestDelegateTaskFlattensMultipartInput asserts a multi-part MessagePart[]
// `task.input` is concatenated in order (text parts only) and delivered to
// the child as its first message. spec: §8.2 lines 25-28; §15.4.1. F-8.2.1.
func TestDelegateTaskFlattensMultipartInput_spec_8_2(t *testing.T) {
	rec := newRecordingExecutor()
	srv, _ := newMCPForDelegate(t, rec, nil)
	args := `{"parentSessionId":"sess_parent","target":"echo","task":{"input":[` +
		`{"type":"text","inline":"first "},` +
		`{"type":"image","ref":"blob://skip-me"},` +
		`{"type":"text","inline":"second"}` +
		`]}}`
	if got := resultText(t, call(t, srv.Handler(), "lenny/delegate_task", args)); got == "" {
		t.Fatal("delegate_task returned no result")
	}
	got := rec.received("sess_child")
	if len(got) != 1 || got[0] != "first second" {
		t.Errorf("child received %v, want [\"first second\"]", got)
	}
}
