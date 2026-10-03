// SPDX-License-Identifier: MIT

package podspec

const (
	// PlatformMCPSocketName is the §9.1/§4.7 abstract Unix socket the
	// adapter's platform MCP server binds (@lenny-platform-mcp). A
	// type:agent runtime discovers it from the adapter manifest's
	// platformMcpServer.socket and dials it to reach the platform tools
	// (lenny/delegate_task, ...). It lives in the same kernel abstract
	// namespace as the runtime socket, reachable across the pod's
	// containers. spec: §9.1. F-9.1.1.
	PlatformMCPSocketName = "@lenny-platform-mcp"
)

// platformMCPArgs returns the §9.1 platform MCP server args for the
// adapter container: when the controller is configured with the gateway
// GatewayControl address, the adapter binds the platform MCP socket and
// forwards a type:agent runtime's platform tool calls to that gateway.
// When no gateway address is configured the platform MCP server is not
// started (there is no gateway link to forward to). spec: §9.1. F-9.1.1.
func platformMCPArgs(in Inputs) []string {
	if in.GatewayGRPCAddr == "" {
		return nil
	}
	return []string{
		"--mcp-socket=" + PlatformMCPSocketName,
		"--gateway-grpc-addr=" + in.GatewayGRPCAddr,
	}
}
