// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMCPServer is an in-process §15.4.3 platform MCP server: a
// JSON-RPC 2.0 listener that enforces the manifest-nonce handshake and
// dispatches a fixed tool set. It records the tools it dispatched.
type fakeMCPServer struct {
	ln    net.Listener
	nonce string

	mu      sync.Mutex
	calls   []string
	initErr bool
}

// startFakeMCP listens on a Unix socket under dir and serves the MCP
// protocol. The returned server's Close stops the listener.
func startFakeMCP(t *testing.T, dir, nonce string) *fakeMCPServer {
	t.Helper()
	sock := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	s := &fakeMCPServer{ln: ln, nonce: nonce}
	t.Cleanup(func() { _ = ln.Close() })
	go s.accept()
	return s
}

func (s *fakeMCPServer) socket() string { return s.ln.Addr().String() }

func (s *fakeMCPServer) toolsCalled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *fakeMCPServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

func (s *fakeMCPServer) serve(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var initReq struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params map[string]any  `json:"params"`
	}
	if err := dec.Decode(&initReq); err != nil || initReq.Method != "initialize" {
		return
	}
	// §15.4.3 nonce handshake: a missing or wrong nonce is rejected.
	if n, _ := initReq.Params["_lennyNonce"].(string); n != s.nonce {
		s.mu.Lock()
		s.initErr = true
		s.mu.Unlock()
		return
	}
	_ = enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": initReq.ID,
		"result": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
		},
	})
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		_ = enc.Encode(s.dispatch(req.ID, req.Method, req.Params))
	}
}

func (s *fakeMCPServer) dispatch(id json.RawMessage, method string, params json.RawMessage) map[string]any {
	switch method {
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{}}}
	case "tools/call":
		var call struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &call)
		s.mu.Lock()
		s.calls = append(s.calls, call.Name)
		s.mu.Unlock()
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": s.toolResult(call.Name)}
	default:
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}}
	}
}

// toolResult returns a canned result for each §8.5 platform tool.
func (s *fakeMCPServer) toolResult(name string) any {
	switch name {
	case "lenny/delegate_task":
		return map[string]any{"taskId": "child_1", "state": "running"}
	case "lenny/await_children":
		return []map[string]any{{
			"schemaVersion": 1, "taskId": "child_1", "state": "completed",
			"output": map[string]any{"parts": []map[string]any{
				{"schemaVersion": 1, "type": "text", "inline": "child output"},
			}},
		}}
	case "lenny/request_input":
		return map[string]any{"parts": []map[string]any{
			{"schemaVersion": 1, "type": "text", "inline": "confirmed"},
		}}
	default:
		return map[string]any{"ok": true}
	}
}

// writeManifest writes a §4.7 adapter manifest pointing at the fake MCP
// server and returns its path.
func writeManifest(t *testing.T, dir, nonce, platformSock string) string {
	t.Helper()
	path := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{
		"version":           1,
		"sessionId":         "sess_test",
		"taskId":            "task_test",
		"mcpNonce":          nonce,
		"platformMcpServer": map[string]any{"socket": platformSock},
	})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// delegateHandler runs the §8.5 delegation flow through the SDK tools.
type delegateHandler struct {
	gotTools bool
	gotCreds bool
}

func (h *delegateHandler) OnCreate(ctx context.Context, req CreateRequest) error {
	if req.SessionID != "sess_test" {
		return nil
	}
	return nil
}

func (h *delegateHandler) OnMessage(ctx context.Context, m Message) (Reply, error) {
	tools := ToolsFrom(ctx)
	if tools == nil {
		return Reply{Parts: m.Envelope.Input, Final: true}, nil
	}
	h.gotTools = true
	handle, err := tools.DelegateTask("child", m.Envelope.Input, nil)
	if err != nil {
		return Reply{Error: &ResponseError{Code: "E", Message: err.Error()}, Final: true}, nil
	}
	results, err := tools.AwaitChildren([]string{handle.TaskID}, "all")
	if err != nil || len(results) == 0 {
		return Reply{Error: &ResponseError{Code: "E"}, Final: true}, nil
	}
	if _, err := tools.RequestInput([]MessagePart{Text("confirm?")}); err != nil {
		return Reply{Error: &ResponseError{Code: "E"}, Final: true}, nil
	}
	if err := tools.Output(results[0].Output.Parts); err != nil {
		return Reply{Error: &ResponseError{Code: "E"}, Final: true}, nil
	}
	return Reply{Parts: results[0].Output.Parts, Final: true}, nil
}

func (h *delegateHandler) OnTerminate(context.Context, TerminationReason) error { return nil }

// TestStandardLevelDelegationFlow exercises the §8.5 delegation flow
// through the SDK platform tool helpers against an in-process fake MCP
// server.
func TestStandardLevelDelegationFlow(t *testing.T) {
	dir := t.TempDir()
	nonce := "nonce_test_value_0123456789abcdef"
	srv := startFakeMCP(t, dir, nonce)
	manifest := writeManifest(t, dir, nonce, srv.socket())

	h := &delegateHandler{}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"m1","input":[{"type":"text","inline":"delegate this"}]}`,
	}, WithStandardLevel(), WithManifestPath(manifest))

	if !h.gotTools {
		t.Fatal("handler did not receive a Tools value at Standard level")
	}
	if len(frames) != 1 || frames[0]["type"] != "response" {
		t.Fatalf("got %v, want a single response", frames)
	}
	called := strings.Join(srv.toolsCalled(), ",")
	for _, want := range []string{"lenny/delegate_task", "lenny/await_children", "lenny/request_input", "lenny/output"} {
		if !strings.Contains(called, want) {
			t.Fatalf("tool %s not invoked; called: %s", want, called)
		}
	}
}

// TestStandardLevelDegradesWithoutManifest confirms a Standard-level
// runtime degrades to Basic when no manifest is present, so the same
// binary still runs in a Basic-only environment.
func TestStandardLevelDegradesWithoutManifest(t *testing.T) {
	h := &delegateHandler{}
	frames := runSDK(t, h, []string{
		`{"type":"message","id":"m1","input":[{"type":"text","inline":"ping"}]}`,
	}, WithStandardLevel(), WithManifestPath(filepath.Join(t.TempDir(), "absent.json")))

	if h.gotTools {
		t.Fatal("Tools should be nil when no manifest advertises a platform MCP server")
	}
	if len(frames) != 1 || frames[0]["type"] != "response" {
		t.Fatalf("degraded runtime did not echo: %v", frames)
	}
}

// TestManifestParsedIntoCreateRequest confirms the SDK materializes the
// §4.7 manifest fields into the CreateRequest.
func TestManifestParsedIntoCreateRequest(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{
		"version":        1,
		"sessionId":      "sess_abc",
		"taskId":         "task_xyz",
		"runtimeOptions": map[string]any{"model": "claude"},
	})
	if err := os.WriteFile(manifest, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var got CreateRequest
	h := &captureHandler{onCreate: func(r CreateRequest) { got = r }}
	runSDK(t, h, []string{`{"type":"shutdown","reason":"drain","deadline_ms":100}`},
		WithManifestPath(manifest))

	if got.SessionID != "sess_abc" || got.TaskID != "task_xyz" {
		t.Fatalf("CreateRequest ids = %q/%q, want sess_abc/task_xyz", got.SessionID, got.TaskID)
	}
	if got.RuntimeOptions["model"] != "claude" {
		t.Fatalf("RuntimeOptions = %v, want model=claude", got.RuntimeOptions)
	}
}

// captureHandler records the CreateRequest for assertions.
type captureHandler struct {
	onCreate func(CreateRequest)
}

func (h *captureHandler) OnCreate(_ context.Context, r CreateRequest) error {
	if h.onCreate != nil {
		h.onCreate(r)
	}
	return nil
}

func (h *captureHandler) OnMessage(_ context.Context, m Message) (Reply, error) {
	return Reply{Parts: m.Envelope.Input, Final: true}, nil
}

func (h *captureHandler) OnTerminate(context.Context, TerminationReason) error { return nil }

// TestCredentialBundleParsed confirms the SDK parses the §4.7 credential
// file into the CreateRequest.
func TestCredentialBundleParsed(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	body, _ := json.Marshal(map[string]any{
		"mode": "direct", "provider": "anthropic", "leaseId": "lease_1", "apiKey": "sk-test",
	})
	if err := os.WriteFile(credPath, body, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	var got CreateRequest
	h := &captureHandler{onCreate: func(r CreateRequest) { got = r }}
	runSDK(t, h, []string{`{"type":"shutdown","reason":"drain","deadline_ms":100}`},
		WithCredentialsPath(credPath))

	if got.Credentials == nil {
		t.Fatal("CreateRequest.Credentials is nil; the credential file was not parsed")
	}
	if got.Credentials.Provider != "anthropic" || got.Credentials.APIKey != "sk-test" {
		t.Fatalf("credential bundle = %+v", got.Credentials)
	}
}

// waitFor polls cond until it is true or the deadline elapses.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
