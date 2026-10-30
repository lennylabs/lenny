// SPDX-License-Identifier: MIT

package messagepart

import "testing"

// TestFromMCPText confirms a TextContent block maps to a text part.
func TestFromMCPText(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{Type: "text", Text: "hello"}})
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	p := parts[0]
	if p.Type != "text" || p.Inline != "hello" || p.MimeType != "text/plain" {
		t.Fatalf("text mapping = %+v", p)
	}
	if p.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d, want 1", p.SchemaVersion)
	}
}

// TestFromMCPImageInline confirms a base64 ImageContent block maps to an
// inline image part.
func TestFromMCPImageInline(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{Type: "image", Data: "aGk=", MimeType: "image/png"}})
	p := parts[0]
	if p.Type != "image" || p.Inline != "aGk=" || p.MimeType != "image/png" || p.Ref != "" {
		t.Fatalf("inline image mapping = %+v", p)
	}
}

// TestFromMCPImageURL confirms a URL-form ImageContent block maps to a
// ref-bearing image part.
func TestFromMCPImageURL(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{Type: "image", URL: "https://example.com/x.png"}})
	p := parts[0]
	if p.Type != "image" || p.Ref != "https://example.com/x.png" || p.Inline != "" {
		t.Fatalf("url image mapping = %+v", p)
	}
}

// TestFromMCPResourceText confirms an EmbeddedResource text blob maps to
// a file part with inline content.
func TestFromMCPResourceText(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{
		Type:     "resource",
		Resource: &MCPResource{Text: "file body", MimeType: "text/markdown"},
	}})
	p := parts[0]
	if p.Type != "file" || p.Inline != "file body" || p.MimeType != "text/markdown" {
		t.Fatalf("resource text mapping = %+v", p)
	}
}

// TestFromMCPResourceURI confirms an EmbeddedResource URI maps to a file
// part carrying the URI as a ref.
func TestFromMCPResourceURI(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{
		Type:     "resource",
		Resource: &MCPResource{URI: "lenny-blob://t/s/p", MimeType: "application/pdf"},
	}})
	p := parts[0]
	if p.Type != "file" || p.Ref != "lenny-blob://t/s/p" || p.Inline != "" {
		t.Fatalf("resource uri mapping = %+v", p)
	}
}

// TestFromMCPIsErrorOverridesType confirms the MCP isError annotation
// overrides the mapped part type to error.
func TestFromMCPIsErrorOverridesType(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{Type: "text", Text: "boom", IsError: true}})
	if parts[0].Type != "error" {
		t.Fatalf("isError block type = %q, want error", parts[0].Type)
	}
}

// TestFromMCPUnknownBlockCollapsesToText confirms an unknown block type
// collapses to text with the original type preserved.
func TestFromMCPUnknownBlockCollapsesToText(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{Type: "video", Text: "fallback"}})
	p := parts[0]
	if p.Type != "text" {
		t.Fatalf("unknown block type = %q, want text", p.Type)
	}
	if p.Annotations["originalType"] != "video" {
		t.Fatalf("originalType annotation = %v, want video", p.Annotations["originalType"])
	}
}

// TestFromMCPAnnotationsCarryThrough confirms block annotations land on
// the produced part.
func TestFromMCPAnnotationsCarryThrough(t *testing.T) {
	parts := FromMCPContent([]MCPContent{{
		Type:        "text",
		Text:        "x",
		Annotations: map[string]any{"role": "primary"},
	}})
	if parts[0].Annotations["role"] != "primary" {
		t.Fatalf("role annotation lost: %+v", parts[0].Annotations)
	}
}

// TestFromMCPContentEmpty confirms an empty input yields an empty,
// non-nil slice.
func TestFromMCPContentEmpty(t *testing.T) {
	parts := FromMCPContent(nil)
	if parts == nil || len(parts) != 0 {
		t.Fatalf("FromMCPContent(nil) = %v, want empty slice", parts)
	}
}
