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

// sendMessageInputSchema is the served input schema of the send-message
// tool. Its citations sit several lines below the doc comment that ties
// the declaration to the specification, which is where the tie stands
// once the served text is stripped.
//
// spec: §4.6 line 5, §4.8 line 14
var sendMessageInputSchema = `{
	"type": "object",
	"description": "The envelope the session sends (spec: §4.6 line 5).",
	"properties": {
		"body": { "description": "The plan the message carries. §4.8 line 14." }
	}
}`

// registerMemoryTools returns the memory tool definition together with
// the message the handler returns when its input is missing.
//
// spec: §4.6
func registerMemoryTools() (tool, string) {
	writeMemory := tool{
		Name: "lenny/write_memory",
		// The description is served text, and the section it names is
		// tied elsewhere in this file rather than in the doc comment
		// over this declaration.
		Description: "Write a memory to the store (spec: §4.8 line 14).",
	}
	// An error message is not a served tool schema, so its citation is
	// an ordinary authoring site and converts.
	return writeMemory, "content is required (§4.6 line 5)"
}
