// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// mcpAnswer returns a forwarded tool call's answer as an MCP tool handler
// returns it: the holder's result decoded, or its refusal as the tool error.
func mcpAnswer(res seam.ToolResult, err error) (mcp.ToolResult, error) {
	if r, ok := seam.IsRefusal(err); ok {
		return mcp.ToolResult{}, mcp.RefusalError(r)
	}
	if err != nil {
		return mcp.ToolResult{}, err
	}
	return mcp.FromSeamResult(res)
}

// forwardedText returns the text of the single content block of a
// forwarded tool call's result, or the empty string when it carries any
// other content.
func forwardedText(t *testing.T, res seam.ToolResult) string {
	t.Helper()
	out, err := mcp.FromSeamResult(res)
	if err != nil || len(out.Content) != 1 {
		return ""
	}
	return out.Content[0].Text
}
