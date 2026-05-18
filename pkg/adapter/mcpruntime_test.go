// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// mcpReferenceBinary is the path to the compiled cmd/runtimes/mcp-
// reference binary, built once per test-process invocation by TestMain.
var mcpReferenceBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "mcp-reference-*")
	if err != nil {
		panic("adapter TestMain: mkdtemp: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	mcpReferenceBinary = filepath.Join(tmp, "mcp-reference")
	cmd := exec.Command("go", "build", "-o", mcpReferenceBinary, "./cmd/runtimes/mcp-reference")
	cmd.Dir = repoRootForTest()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("adapter TestMain: build mcp-reference: " + err.Error())
	}
	os.Exit(m.Run())
}

// repoRootForTest walks up from the test working directory to the
// module root (the directory holding go.mod).
func repoRootForTest() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return wd
}

// messageEnvelope builds a §15.4.1 `message` envelope that the type:
// mcp adapter path maps onto an MCP tools/call: it names the tool and
// carries the call arguments.
func messageEnvelope(id, tool string, args map[string]any) []byte {
	env := map[string]any{
		"type": "message",
		"id":   id,
		"tool": tool,
	}
	if args != nil {
		raw, _ := json.Marshal(args)
		env["arguments"] = json.RawMessage(raw)
	}
	b, _ := json.Marshal(env)
	return b
}

// --- MCPRuntime driving the real mcp-reference runtime ----------------

func TestMCPRuntimeStartDrivesReferenceRuntime(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	// The initialize handshake negotiated the reference server's identity.
	init := rt.InitializeResult()
	if init.ProtocolVersion == "" {
		t.Error("Start did not record a negotiated MCP protocol version")
	}
	if init.ServerInfo.Name != "lenny-mcp-reference" {
		t.Errorf("server name = %q, want lenny-mcp-reference", init.ServerInfo.Name)
	}
}

func TestMCPRuntimeStartRejectsMissingCommand(t *testing.T) {
	rt := &adapter.MCPRuntime{}
	if err := rt.Start(context.Background(), "sess-1"); err != adapter.ErrMCPRuntimeNotConfigured {
		t.Errorf("Start with no command = %v, want ErrMCPRuntimeNotConfigured", err)
	}
}

func TestMCPRuntimeMessageMapsToToolCall(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	out, err := rt.Output(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	// A §15.4.1 message naming the `echo` tool maps onto an MCP
	// tools/call; the result comes back as a §15.4.1 `response` frame.
	env := messageEnvelope("msg-1", "echo", map[string]any{"text": "hello mcp"})
	if err := rt.WriteEnvelope("sess-1", env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	frame := receiveFrame(t, ctx, out)
	var resp struct {
		Type      string `json:"type"`
		InReplyTo string `json:"inReplyTo"`
		Output    []struct {
			Type   string `json:"type"`
			Inline string `json:"inline"`
		} `json:"output"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(frame, &resp); err != nil {
		t.Fatalf("decode response frame %q: %v", frame, err)
	}
	if resp.Type != "response" {
		t.Errorf("frame type = %q, want response", resp.Type)
	}
	if resp.InReplyTo != "msg-1" {
		t.Errorf("inReplyTo = %q, want msg-1", resp.InReplyTo)
	}
	if resp.Error != nil {
		t.Fatalf("response carried an error: %v", resp.Error)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "text" {
		t.Fatalf("response output = %+v, want a single text part", resp.Output)
	}
	if !strings.Contains(resp.Output[0].Inline, "hello mcp") {
		t.Errorf("echoed text = %q, want it to contain the input", resp.Output[0].Inline)
	}
}

func TestMCPRuntimeMessageWithoutToolErrors(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	env := []byte(`{"type":"message","id":"msg-1"}`)
	if err := rt.WriteEnvelope("sess-1", env); err == nil {
		t.Error("WriteEnvelope for a message naming no tool returned nil, want an error")
	}
}

func TestMCPRuntimeIgnoresLifecycleFrames(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	// A heartbeat is a JSONL lifecycle frame with no MCP analogue for a
	// type: mcp runtime; the adapter ignores it without error.
	if err := rt.WriteEnvelope("sess-1", []byte(`{"type":"heartbeat"}`)); err != nil {
		t.Errorf("WriteEnvelope for a heartbeat = %v, want nil (ignored)", err)
	}
}

func TestMCPRuntimeReportsBadEnvelope(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	if err := rt.WriteEnvelope("sess-1", []byte("not json")); err == nil {
		t.Error("WriteEnvelope for a malformed envelope returned nil, want an error")
	}
}

func TestMCPRuntimeCloseStopsProcessAndOutput(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out, err := rt.Output(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	if err := rt.Close(ctx, "sess-1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close closes the Output channel.
	select {
	case _, open := <-out:
		if open {
			t.Error("Output channel produced a value after Close, want it closed")
		}
	case <-time.After(2 * time.Second):
		t.Error("Output channel was not closed by Close")
	}
	// Close is idempotent.
	if err := rt.Close(ctx, "sess-1"); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestMCPRuntimeInterruptSignalsProcess(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Start(ctx, "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background(), "sess-1") })

	// A clean interrupt (SIGTERM) and a hard interrupt (SIGKILL) both
	// deliver to the live process without error.
	if err := rt.Interrupt(ctx, "sess-1", false); err != nil {
		t.Errorf("clean Interrupt = %v, want nil", err)
	}
}

func TestMCPRuntimeInterruptBeforeStartErrors(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	if err := rt.Interrupt(context.Background(), "sess-1", false); err == nil {
		t.Error("Interrupt before Start returned nil, want an error")
	}
}

// --- the adapter Server driving a type: mcp runtime end to end --------

// TestAdapterServerDrivesMCPRuntime exercises the full §4.7 session
// path for a type: mcp runtime: StartSession → SendMessage → Shutdown,
// with the adapter Server selecting the type: mcp path via RuntimeKind
// and driving the real mcp-reference runtime.
func TestAdapterServerDrivesMCPRuntime(t *testing.T) {
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.RuntimeKind = adapter.RuntimeKindMCP
	s.Runtime = rt

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// StartSession spawns the MCP-server agent and performs the MCP
	// initialize handshake.
	if _, err := s.StartSession(ctx, startReq("sess-mcp")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	out, err := rt.Output(ctx, "sess-mcp")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	// SendMessage delivers a §15.4.1 message; the type: mcp path maps it
	// onto an MCP tools/call against the agent's MCP server.
	env := messageEnvelope("msg-1", "echo", map[string]any{"text": "round trip"})
	if _, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-mcp"},
		EnvelopeJson: env,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	frame := receiveFrame(t, ctx, out)
	if !strings.Contains(string(frame), "round trip") {
		t.Errorf("response frame = %s, want it to echo the input", frame)
	}

	// Shutdown terminates the runtime and releases the pod.
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-mcp"},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.ExitedCleanly {
		t.Error("Shutdown reported an unclean exit for a healthy MCP runtime")
	}
}

func TestAdapterServerSkipsPlatformMCPForMCPRuntime(t *testing.T) {
	// A type: mcp runtime is oblivious to Lenny and never connects to the
	// platform MCP server, so the adapter must not require an MCP socket
	// for it even when one is configured. StartSession succeeds with the
	// MCP runtime kind and an MCP socket left unset.
	rt := &adapter.MCPRuntime{Command: mcpReferenceBinary}
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ManifestDir = t.TempDir()
	s.RuntimeKind = adapter.RuntimeKindMCP
	s.Runtime = rt

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.StartSession(ctx, startReq("sess-mcp")); err != nil {
		t.Fatalf("StartSession for a type: mcp runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-mcp"},
		})
	})
}

// receiveFrame reads one frame from the runtime's Output channel,
// failing the test on timeout or a closed channel.
func receiveFrame(t *testing.T, ctx context.Context, out <-chan []byte) []byte {
	t.Helper()
	select {
	case frame, open := <-out:
		if !open {
			t.Fatal("Output channel closed before producing a frame")
		}
		return frame
	case <-ctx.Done():
		t.Fatalf("timed out waiting for an Output frame: %v", ctx.Err())
		return nil
	}
}
