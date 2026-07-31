// SPDX-License-Identifier: MIT

// Package mcptools holds the tool definitions the gateway serves.
package mcptools

// tool is one served MCP tool.
type tool struct {
	Name        string
	Description string
}

// claimSandbox is the sandbox claim tool.
//
// spec: §4.6 line 5
var claimSandbox = tool{
	Name: "lenny/claim_sandbox",
	// The description is served to a client, so the citation it carries
	// is stripped rather than converted.
	//
	// spec: §4.6 line 5
	Description: "Claim a sandbox for the session (spec: §4.6 line 5).",
}
