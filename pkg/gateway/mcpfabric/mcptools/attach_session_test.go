// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
)

// TestAttachSessionSnapshot verifies the non-streaming attach_session
// snapshot: a WebSocket or plain-JSON caller receives the session's
// current state and the resumeFromSeq cursor (the durable last_seq) to
// reconnect the SSE stream with. spec: §15.2 lines 1289, 1331. F-15.2.2.
func TestAttachSessionSnapshot_spec_15_2(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_a", session.StateRunning, "")

	resp := call(t, srv.Handler(), mcp.AttachToolName, `{"sessionId":"sess_a"}`)
	text := resultText(t, resp)

	var snap struct {
		SessionID     string `json:"sessionId"`
		State         string `json:"state"`
		ResumeFromSeq int64  `json:"resumeFromSeq"`
	}
	if err := json.Unmarshal([]byte(text), &snap); err != nil {
		t.Fatalf("decode snapshot %q: %v", text, err)
	}
	if snap.SessionID != "sess_a" {
		t.Errorf("sessionId = %q, want sess_a", snap.SessionID)
	}
	if snap.State != string(session.StateRunning) {
		t.Errorf("state = %q, want %q", snap.State, session.StateRunning)
	}
	if snap.ResumeFromSeq != 0 {
		t.Errorf("resumeFromSeq = %d, want 0 (no events published)", snap.ResumeFromSeq)
	}
}

// TestAttachSessionMissingID rejects an empty sessionId with the canonical
// VALIDATION_ERROR envelope. spec: §15.2.1 rule 3. F-15.2.2.
func TestAttachSessionMissingID_spec_15_2(t *testing.T) {
	srv, _ := newMCP(t)
	resp := call(t, srv.Handler(), mcp.AttachToolName, `{}`)
	env := readLennyErrorEnvelope(t, resp["result"].(map[string]any))
	if env["code"] != "VALIDATION_ERROR" {
		t.Fatalf("code = %v, want VALIDATION_ERROR", env["code"])
	}
}

// TestAttachSessionUnknown returns RESOURCE_NOT_FOUND for a session the
// caller cannot see. spec: §15.2.1 rule 3. F-15.2.2.
func TestAttachSessionUnknown_spec_15_2(t *testing.T) {
	srv, _ := newMCP(t)
	resp := call(t, srv.Handler(), mcp.AttachToolName, `{"sessionId":"ghost"}`)
	env := readLennyErrorEnvelope(t, resp["result"].(map[string]any))
	if env["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatalf("code = %v, want RESOURCE_NOT_FOUND", env["code"])
	}
}

// TestAttachSessionInToolList verifies attach_session is discoverable in
// tools/list so a client can find the streaming entry point. F-15.2.2.
func TestAttachSessionInToolList_spec_15_2(t *testing.T) {
	srv, _ := newMCP(t)
	if !strings.Contains(toolListNames(t, srv.Handler()), mcp.AttachToolName) {
		t.Fatalf("tools/list does not advertise %s", mcp.AttachToolName)
	}
}

// toolListNames returns the registered tool names as a single string for
// substring assertions.
func toolListNames(t *testing.T, h http.Handler) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	var names []string
	for _, tl := range resp.Result.Tools {
		names = append(names, tl.Name)
	}
	return strings.Join(names, ",")
}
