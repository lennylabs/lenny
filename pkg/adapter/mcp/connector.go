// SPDX-License-Identifier: MIT

package mcp

// ConnectorRef identifies one §9.3 connector a session's effective
// delegation policy permits: the registry id (e.g. `github`) and the
// human-facing display name. The adapter derives the intra-pod
// @lenny-connector-<id> socket name from the id and opens one MCP server
// per ref. It lives in the shared mcp package so the gateway-control
// forwarder and the adapter both reference it without an import cycle,
// mirroring how the platform-tool path shares Tool.
//
// spec: §9.3 line 142. F-9.1.2.
type ConnectorRef struct {
	ID          string
	DisplayName string
}
