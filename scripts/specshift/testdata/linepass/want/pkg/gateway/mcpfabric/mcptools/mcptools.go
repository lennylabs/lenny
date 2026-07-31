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
// spec: §4.6
var claimSandbox = tool{
	Name: "lenny/claim_sandbox",
	// The description is served to a client, so the citation it carries
	// is stripped rather than converted.
	//
	// spec: §4.6
	Description: "Claim a sandbox for the session.",
}

// sendMessageInputSchema is the served input schema of the send-message
// tool. Its citations sit several lines below the doc comment that ties
// the declaration to the specification, which is where the tie stands
// once the served text is stripped.
//
// spec: §4.6, §4.8
var sendMessageInputSchema = `{
	"type": "object",
	"description": "The envelope the session sends.",
	"properties": {
		"body": { "description": "The plan the message carries." }
	}
}`
