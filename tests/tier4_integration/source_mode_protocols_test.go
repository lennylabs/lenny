// SPDX-License-Identifier: MIT

//go:build integration

// Source Mode protocol-by-runtime coverage (§17.4 line 213: "Gateway,
// controller-sim, and a single agent container run as goroutines in
// one process"; §17.4 line 218: "Runtime adapter authors testing
// their adapter against the gateway contract without full pod
// scheduling"). source_mode_smoke_test.go drives Source Mode's REST
// surface against the built-in echo runtime only. Because Source
// Mode boots the real cmd/lenny-gateway binary, every §15
// external-protocol adapter the binary registers (REST, MCP, OpenAI
// Chat Completions, Open Responses) is live in that same process, and
// a runtime author substitutes a custom binary via --runtime-bin
// (§17.4 line 323) in place of the built-in echo executor. This file
// extends the protocol-by-runtime matrix with two more cells: the MCP
// lenny/send_message tool and the OpenAI Chat Completions endpoint,
// both driven against the streaming-echo Full-level reference runtime
// (cmd/runtimes/streaming-echo) run as a --runtime-bin child process
// rather than the built-in echo executor.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// buildStreamingEchoBinary compiles cmd/runtimes/streaming-echo (the
// §15.4.3 Full-level reference adapter) into a throwaway binary so the
// gateway's --runtime-bin subprocess executor has a non-echo-builtin
// runtime to dispatch to.
func buildStreamingEchoBinary(t *testing.T) string {
	t.Helper()
	root := schematest.RepoRoot(t)
	out := filepath.Join(t.TempDir(), "streaming-echo")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/runtimes/streaming-echo")
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build streaming-echo: %v\n%s", err, combined)
	}
	return out
}

// spec: §17.4 line 208-213 ("Source Mode runs the Lenny source tree
// directly without Kubernetes ... Gateway, controller-sim, and a
// single agent container run as goroutines in one process") and line
// 218 ("Runtime adapter authors testing their adapter against the
// gateway contract without full pod scheduling"). The existing Source
// Mode smoke test only exercises REST against the built-in echo
// runtime; a regression confined to the MCP or OpenAI Chat
// Completions wire adapters, or to the --runtime-bin subprocess path
// a runtime author actually uses, would pass that test undetected.
// This test starts one gateway subprocess with --runtime-bin pointed
// at streaming-echo and drives both the MCP and the OpenAI Chat
// Completions surfaces against it.
func TestSourceModeProtocols_StreamingEcho_spec_17_4(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	bin := buildStreamingEchoBinary(t)
	gw := gateway.StartWith(t, "--dev-mode", "--runtime-bin", bin)
	c := mcpClient{t: t, base: gw.BaseURL()}

	t.Run("mcp_send_message", func(t *testing.T) {
		testSourceModeMCPSendMessage(t, c)
	})
	t.Run("openai_chat_completions", func(t *testing.T) {
		testSourceModeOpenAIChatCompletions(t, c.base)
	})
}

// testSourceModeMCPSendMessage drives create+start (REST) -> prompt
// (MCP lenny/send_message) -> terminate (MCP lenny/terminate_session)
// against a gateway whose backing runtime is the streaming-echo
// subprocess, and asserts the delivered response carries the
// streaming-echo reference runtime's echo of the prompt.
func testSourceModeMCPSendMessage(t *testing.T, c mcpClient) {
	t.Helper()
	code, created := c.rest(http.MethodPost, "/v1/sessions/start", map[string]any{
		"runtimeRef": "streaming-echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create+start session: status %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}

	const prompt = "source mode MCP round trip"
	sendArgs, err := json.Marshal(map[string]any{"to": sid, "message": prompt})
	if err != nil {
		t.Fatalf("marshal send_message args: %v", err)
	}
	sendRPC := c.callTool("lenny/send_message", string(sendArgs))
	sendText, isErr := toolResultText(t, sendRPC)
	if isErr {
		t.Fatalf("lenny/send_message failed: %s", sendText)
	}
	var sendPayload struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(sendText), &sendPayload); err != nil {
		t.Fatalf("decode send_message result: %v; body=%s", err, sendText)
	}
	if sendPayload.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("MCP send_message delivery receipt status: got %q, want delivered (%s)",
			sendPayload.DeliveryReceipt.Status, sendText)
	}
	sawEcho := false
	for _, part := range sendPayload.Output {
		if strings.Contains(part.Text, prompt) {
			sawEcho = true
		}
	}
	if !sawEcho {
		t.Fatalf("MCP send_message output does not contain the streaming-echo reply to %q: %s", prompt, sendText)
	}

	termArgs, err := json.Marshal(map[string]any{"sessionId": sid})
	if err != nil {
		t.Fatalf("marshal terminate_session args: %v", err)
	}
	termRPC := c.callTool("lenny/terminate_session", string(termArgs))
	termText, isErr := toolResultText(t, termRPC)
	if isErr {
		t.Fatalf("lenny/terminate_session failed: %s", termText)
	}
	var termPayload struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(termText), &termPayload); err != nil {
		t.Fatalf("decode terminate_session result: %v; body=%s", err, termText)
	}
	if termPayload.State != "completed" {
		t.Fatalf("MCP terminate_session state: got %q, want completed", termPayload.State)
	}
}

// testSourceModeOpenAIChatCompletions drives a single non-streaming
// POST /v1/chat/completions against a gateway whose backing runtime
// is the streaming-echo subprocess, and asserts the assistant message
// carries the streaming-echo reference runtime's echo of the prompt.
func testSourceModeOpenAIChatCompletions(t *testing.T, base string) {
	t.Helper()
	const prompt = "source mode OpenAI Chat Completions round trip"
	reqBody, err := json.Marshal(map[string]any{
		"model":    "streaming-echo",
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	if err != nil {
		t.Fatalf("marshal chat request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build chat request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read chat response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat completions status: %d, body=%s", resp.StatusCode, raw)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode chat completions response: %v; body=%s", err, raw)
	}
	if len(out.Choices) == 0 {
		t.Fatalf("chat completions response carried no choices: %s", raw)
	}
	if !strings.Contains(out.Choices[0].Message.Content, prompt) {
		t.Errorf("chat completions content %q does not contain the streaming-echo reply to %q",
			out.Choices[0].Message.Content, prompt)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("chat completions finish_reason: got %q, want stop", out.Choices[0].FinishReason)
	}
}
