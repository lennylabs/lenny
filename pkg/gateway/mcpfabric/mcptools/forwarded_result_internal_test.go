// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: 29.3 (Interactive message send) Forwarding rule 4, 4.8 (Gateway Policy Engine)
func TestPreToolResultSkipsAForwardedCall_spec_29_3(t *testing.T) {
	calls := 0
	ri := skipForwardedResults(func(_ context.Context, _, _ string, r mcp.ToolResult) (mcp.ToolResult, error) {
		calls++
		r.IsError = true
		return r, nil
	})
	in := mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "x"}}}
	out, err := ri(sessionserver.WithForwarded(context.Background()), "1", "lenny/send_message", in)
	if err != nil || calls != 0 || out.IsError {
		t.Fatalf("forwarded call: interceptor ran %d times (result %+v, err %v), want the result unchanged", calls, out, err)
	}
	out, err = ri(context.Background(), "1", "lenny/send_message", in)
	if err != nil || calls != 1 || !out.IsError {
		t.Fatalf("local call: interceptor ran %d times (result %+v, err %v), want it to run once", calls, out, err)
	}
}

// HolderForwarder is satisfied by the session server, which Deps.HolderForward
// takes in the gateway composition root.
var _ HolderForwarder = (*sessionserver.Server)(nil)
