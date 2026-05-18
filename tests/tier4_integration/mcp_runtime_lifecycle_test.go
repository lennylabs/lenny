// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for §13.26 Phase 12b — type: mcp runtime
// support. It drives the type: mcp runtime-side adapter path against
// the reference type: mcp runtime (cmd/runtimes/mcp-reference) through
// the full §4.7 session lifecycle: the adapter Server selects the type:
// mcp path via RuntimeKind, StartSession spawns the MCP-server agent
// and performs the MCP initialize handshake, SendMessage maps a
// §15.4.1 message onto an MCP tools/call, and Shutdown terminates the
// runtime and releases the pod.
//
// The gateway-side type: mcp endpoints (/mcp/runtimes/{name}) are a
// separate Phase 5 deliverable; TestMCPRuntimeEndpoints in
// scaffolds_test.go tracks that gap.

package tier4_integration_test

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

// buildMCPReference compiles cmd/runtimes/mcp-reference into a temp
// binary and returns its path.
func buildMCPReference(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mcp-reference")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/runtimes/mcp-reference")
	cmd.Dir = repoRootForMCPTest(t)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build mcp-reference: %v", err)
	}
	return bin
}

// repoRootForMCPTest walks up to the module root.
func repoRootForMCPTest(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("could not locate module root")
	return ""
}

// TestMCPRuntimeLifecycle exercises the type: mcp runtime-side adapter
// path end to end against the reference type: mcp runtime.
func TestMCPRuntimeLifecycle(t *testing.T) {
	bin := buildMCPReference(t)

	rt := &adapter.MCPRuntime{Command: bin}
	s := adapter.New("integration")
	s.WorkspaceRoot = t.TempDir()
	s.RuntimeKind = adapter.RuntimeKindMCP
	s.Runtime = rt

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// StartSession: spawn the MCP-server agent and complete the MCP
	// initialize handshake.
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-mcp-lifecycle"},
		Runtime:   "mcp-reference",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if got := rt.InitializeResult(); got.ServerInfo.Name != "lenny-mcp-reference" {
		t.Errorf("negotiated server name = %q, want lenny-mcp-reference", got.ServerInfo.Name)
	}

	out, err := rt.Output(ctx, "sess-mcp-lifecycle")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	// SendMessage: a §15.4.1 message naming the `echo` tool maps onto an
	// MCP tools/call; the result returns as a §15.4.1 response frame.
	env, _ := json.Marshal(map[string]any{
		"type":      "message",
		"id":        "msg-1",
		"tool":      "echo",
		"arguments": map[string]any{"text": "lifecycle ok"},
	})
	if _, err := s.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-mcp-lifecycle"},
		EnvelopeJson: env,
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	select {
	case frame, open := <-out:
		if !open {
			t.Fatal("Output channel closed before producing a response")
		}
		var resp struct {
			Type      string `json:"type"`
			InReplyTo string `json:"inReplyTo"`
			Output    []struct {
				Inline string `json:"inline"`
			} `json:"output"`
		}
		if err := json.Unmarshal(frame, &resp); err != nil {
			t.Fatalf("decode response frame %q: %v", frame, err)
		}
		if resp.Type != "response" || resp.InReplyTo != "msg-1" {
			t.Errorf("frame type=%q inReplyTo=%q, want response/msg-1", resp.Type, resp.InReplyTo)
		}
		if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, "lifecycle ok") {
			t.Errorf("response output = %+v, want it to echo the input", resp.Output)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for the tools/call response: %v", ctx.Err())
	}

	// Shutdown: terminate the runtime and release the pod.
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-mcp-lifecycle"},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.ExitedCleanly {
		t.Error("Shutdown reported an unclean exit for a healthy MCP runtime")
	}

	// The pod is idle again: a fresh session can be assigned.
	rt2 := &adapter.MCPRuntime{Command: bin}
	s.Runtime = rt2
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-mcp-lifecycle-2"},
		Runtime:   "mcp-reference",
	}); err != nil {
		t.Fatalf("StartSession after Shutdown: pod was not released: %v", err)
	}
	_, _ = s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-mcp-lifecycle-2"},
	})
}
