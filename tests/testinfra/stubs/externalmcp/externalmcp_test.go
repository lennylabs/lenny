// SPDX-License-Identifier: MIT

package externalmcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/externalmcp"
)

// spec: 9.3 (gateway acts as MCP client to the external tool)
func TestStubServesInitializeAndToolsCall(t *testing.T) {
	stub := externalmcp.Start(t, "hello from the stub")
	client := connectorinvoke.New(stub.Client())

	sess, res, err := client.Initialize(context.Background(), stub.URL(), "")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion == "" {
		t.Error("negotiated protocol version is empty")
	}

	raw, err := sess.CallTool(context.Background(), "any_tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "hello from the stub" {
		t.Errorf("tools/call result = %s, want it to carry the configured text", raw)
	}
	// initialize + notifications/initialized + tools/call = 3 requests.
	if got := stub.RequestCount(); got != 3 {
		t.Errorf("RequestCount = %d, want 3", got)
	}
}

// spec: 9.3 (a fresh stub records no requests until dialed)
func TestStubStartsWithZeroRequests(t *testing.T) {
	stub := externalmcp.Start(t, "unused")
	if got := stub.RequestCount(); got != 0 {
		t.Errorf("RequestCount on a fresh stub = %d, want 0", got)
	}
}
