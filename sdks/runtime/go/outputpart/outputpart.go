// SPDX-License-Identifier: MIT

// Package outputpart converts MCP content blocks to the §15.4.1
// OutputPart array, so a runtime author who produces output using
// MCP-familiar content block objects does not have to perform the
// field mapping by hand.
//
// The spec (§15.4.1, "Optional SDK helper from_mcp_content") names this
// sub-package and the FromMCPContent entry point as the Go home of the
// helper. The conversion follows the §15.4.1 "MCP content block →
// OutputPart mapping (inbound translation)" table.
package outputpart

import "github.com/lennylabs/lenny/sdks/runtime/go/runtime"

// MCPContent is one MCP content block. The fields are the union across
// the §15.4.1 inbound-translation table; an absent field is the zero
// value. Resource holds the EmbeddedResource sub-object.
type MCPContent struct {
	// Type is the MCP block type: text, image, or resource.
	Type string `json:"type"`
	// Text carries the content of a TextContent block.
	Text string `json:"text,omitempty"`
	// Data carries base64 image bytes for an ImageContent block.
	Data string `json:"data,omitempty"`
	// MimeType is the block MIME type for image and resource blocks.
	MimeType string `json:"mimeType,omitempty"`
	// URL carries the image URL for a URL-form ImageContent block.
	URL string `json:"url,omitempty"`
	// Resource holds the EmbeddedResource payload.
	Resource *MCPResource `json:"resource,omitempty"`
	// Annotations carries MCP block annotations; well-known keys map
	// onto OutputPart.annotations.
	Annotations map[string]any `json:"annotations,omitempty"`
	// IsError marks the block as an error per the MCP isError
	// annotation; it overrides the mapped OutputPart type to error.
	IsError bool `json:"isError,omitempty"`
}

// MCPResource is the EmbeddedResource payload of an MCP resource block.
// A text resource sets Text; a blob resource sets Blob (base64) or URI.
type MCPResource struct {
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// FromMCPContent converts a slice of MCP content blocks to a §15.4.1
// OutputPart array, applying the inbound-translation mapping: a
// TextContent block becomes a text part, an ImageContent block becomes
// an image part (inline base64 or a ref for the URL form), and an
// EmbeddedResource becomes a file part. A block carrying the MCP
// isError annotation maps to an error part regardless of its block
// type. Every produced part has SchemaVersion set to the current
// revision.
func FromMCPContent(blocks []MCPContent) []runtime.OutputPart {
	parts := make([]runtime.OutputPart, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, fromBlock(b))
	}
	return parts
}

// fromBlock maps one MCP content block to an OutputPart.
func fromBlock(b MCPContent) runtime.OutputPart {
	p := runtime.OutputPart{SchemaVersion: 1}
	switch b.Type {
	case "text":
		p.Type = "text"
		p.MimeType = "text/plain"
		p.Inline = b.Text
	case "image":
		p.Type = "image"
		p.MimeType = b.MimeType
		if b.URL != "" {
			p.Ref = b.URL
		} else {
			p.Inline = b.Data
		}
	case "resource":
		p.Type = "file"
		if b.Resource != nil {
			p.MimeType = b.Resource.MimeType
			switch {
			case b.Resource.Text != "":
				if p.MimeType == "" {
					p.MimeType = "text/plain"
				}
				p.Inline = b.Resource.Text
			case b.Resource.Blob != "":
				p.Inline = b.Resource.Blob
			case b.Resource.URI != "":
				p.Ref = b.Resource.URI
			}
		}
	default:
		// Unknown block types collapse to text with the original block
		// type preserved, matching the §15.4.1 custom-type fallback.
		p.Type = "text"
		p.MimeType = "text/plain"
		p.Inline = b.Text
		p.Annotations = mergeAnnotation(p.Annotations, "originalType", b.Type)
	}
	if len(b.Annotations) > 0 {
		for k, v := range b.Annotations {
			p.Annotations = mergeAnnotation(p.Annotations, k, v)
		}
	}
	if b.IsError {
		p.Type = "error"
	}
	return p
}

// mergeAnnotation inserts a key into an annotations map, allocating the
// map on first use.
func mergeAnnotation(m map[string]any, key string, value any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	return m
}
