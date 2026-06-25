// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"testing"
)

// spec: §15.5 item 7 + §15.4.1 line 1524 — `schemaVersion` on every
// translated `MessagePart` is preserved from the upstream MCP content
// block when the producer set one. A durable consumer reading a §8.8
// TaskRecord that the gateway projected through MCP sees the original
// revision rather than a forced `1`. F-15.5.13.
func TestMCPResultPartsPreservesProducerSchemaVersion_spec_15_5_2452(t *testing.T) {
	result := json.RawMessage(`{
		"content": [
			{"type": "text", "text": "alpha", "schemaVersion": 7},
			{"type": "image", "data": "deadbeef", "mimeType": "image/png", "schemaVersion": 3}
		]
	}`)
	parts := mcpResultParts(result)
	if len(parts) != 2 {
		t.Fatalf("parts: got %d, want 2", len(parts))
	}
	if got := parts[0]["schemaVersion"]; got != 7 {
		t.Errorf("text part schemaVersion: got %v, want 7", got)
	}
	if got := parts[0]["type"]; got != "text" {
		t.Errorf("text part type: got %v, want text", got)
	}
	if got := parts[0]["inline"]; got != "alpha" {
		t.Errorf("text part inline: got %v, want alpha", got)
	}
	if got := parts[1]["schemaVersion"]; got != 3 {
		t.Errorf("data part schemaVersion: got %v, want 3", got)
	}
	if got := parts[1]["type"]; got != "data" {
		t.Errorf("data part type: got %v, want data", got)
	}
	// The hoisted schemaVersion MUST NOT be duplicated in the inline
	// payload, otherwise a forward-compat-aware durable reader sees
	// the field twice (once on the envelope, once on the inline).
	inline, _ := parts[1]["inline"].(string)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(inline), &decoded); err != nil {
		t.Fatalf("inline json: %v", err)
	}
	if _, present := decoded["schemaVersion"]; present {
		t.Errorf("data part inline still carries schemaVersion: %s", inline)
	}
}

// spec: §15.5 item 7 — a content block without producer-stamped
// schemaVersion falls through to 1 so a durable consumer still sees a
// value. F-15.5.13.
func TestMCPResultPartsFallsThroughToOneWhenAbsent_spec_15_5_2452(t *testing.T) {
	result := json.RawMessage(`{
		"content": [
			{"type": "text", "text": "hi"}
		]
	}`)
	parts := mcpResultParts(result)
	if len(parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(parts))
	}
	if got := parts[0]["schemaVersion"]; got != 1 {
		t.Errorf("schemaVersion: got %v, want 1 (default)", got)
	}
}

// spec: §15.5 item 7 — a malformed schemaVersion (string, negative,
// fractional) falls through to 1 rather than corrupting the envelope.
// F-15.5.13.
func TestMCPResultPartsRejectsBadSchemaVersion_spec_15_5_2452(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"string", `{"content":[{"type":"text","text":"x","schemaVersion":"7"}]}`},
		{"negative", `{"content":[{"type":"text","text":"x","schemaVersion":-2}]}`},
		{"fractional", `{"content":[{"type":"text","text":"x","schemaVersion":1.5}]}`},
		{"zero", `{"content":[{"type":"text","text":"x","schemaVersion":0}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := mcpResultParts(json.RawMessage(tc.raw))
			if len(parts) != 1 {
				t.Fatalf("parts: got %d, want 1", len(parts))
			}
			if got := parts[0]["schemaVersion"]; got != 1 {
				t.Errorf("schemaVersion: got %v, want 1 (fallback)", got)
			}
		})
	}
}

// spec: §15.4.1 — an empty result yields an empty part list rather
// than a single placeholder. F-15.5.13.
func TestMCPResultPartsEmptyResult(t *testing.T) {
	if got := mcpResultParts(nil); len(got) != 0 {
		t.Errorf("nil result: got %d parts, want 0", len(got))
	}
}

// spec: §15.4.1 — a non-tool-result JSON value (e.g. a string) is
// wrapped verbatim as a single structured `application/json` part
// with schemaVersion 1 so no information is lost. F-15.5.13.
func TestMCPResultPartsWrapsNonToolResult(t *testing.T) {
	parts := mcpResultParts(json.RawMessage(`"plain string"`))
	if len(parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(parts))
	}
	if got := parts[0]["type"]; got != "data" {
		t.Errorf("type: got %v, want data", got)
	}
	if got := parts[0]["schemaVersion"]; got != 1 {
		t.Errorf("schemaVersion: got %v, want 1", got)
	}
}

// readProducerSchemaVersion is exercised directly to lock the integer
// projection rules used by mcpResultParts. F-15.5.13.
func TestReadProducerSchemaVersion(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"float positive int", float64(5), 5},
		{"float fractional", float64(1.5), 1},
		{"float zero", float64(0), 1},
		{"float negative", float64(-3), 1},
		{"int positive", 7, 7},
		{"int zero", 0, 1},
		{"nil", nil, 1},
		{"string", "9", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readProducerSchemaVersion(c.in); got != c.want {
				t.Errorf("readProducerSchemaVersion(%#v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
