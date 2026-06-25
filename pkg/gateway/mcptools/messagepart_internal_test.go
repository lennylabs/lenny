// SPDX-License-Identifier: MIT

package mcptools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcp"
)

// TestValidateMessagePart_spec_15_4_1_inline_ref_conflict exercises the
// §15.4.1 lines 1542-1548 MessagePart ingress invariants the gateway
// enforces on every `lenny/output` part. F-15.4.1 (15.4-HIGH-007).
func TestValidateMessagePart_spec_15_4_1_inline_ref_conflict(t *testing.T) {
	cases := []struct {
		name     string
		part     string
		wantCode string // "" means accepted
	}{
		{name: "inline only", part: `{"type":"text","inline":"hello"}`, wantCode: ""},
		{name: "ref only", part: `{"type":"text","ref":"lenny-blob://acme/s/p"}`, wantCode: ""},
		{name: "neither (container)", part: `{"type":"execution_result","parts":[]}`, wantCode: ""},
		{
			name:     "both inline and ref",
			part:     `{"type":"text","inline":"hi","ref":"lenny-blob://acme/s/p"}`,
			wantCode: "MESSAGEPART_INLINE_REF_CONFLICT",
		},
		{name: "explicit ref null is absent", part: `{"type":"text","inline":"hi","ref":null}`, wantCode: ""},
		{name: "malformed (array)", part: `["not","an","object"]`, wantCode: "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMessagePart(json.RawMessage(tc.part))
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("validateMessagePart(%s) = %v, want nil", tc.part, err)
				}
				return
			}
			var te *mcp.ToolError
			if !errorAs(err, &te) {
				t.Fatalf("validateMessagePart(%s) = %v, want *mcp.ToolError code %s", tc.part, err, tc.wantCode)
			}
			if te.Code != tc.wantCode {
				t.Fatalf("validateMessagePart(%s) code = %q, want %q", tc.part, te.Code, tc.wantCode)
			}
		})
	}
}

// TestValidateMessagePart_spec_15_4_1_too_large rejects a part above the
// §15.4.1 line 1548 50 MB ceiling. F-15.4.1 (15.4-HIGH-007).
func TestValidateMessagePart_spec_15_4_1_too_large(t *testing.T) {
	// Build a part whose marshaled length exceeds 50 MB by inlining a
	// payload one byte past the ceiling.
	big := strings.Repeat("a", maxMessagePartBytes+1)
	part, _ := json.Marshal(map[string]any{"type": "text", "inline": big})
	if len(part) <= maxMessagePartBytes {
		t.Fatalf("test setup: part length %d not above ceiling %d", len(part), maxMessagePartBytes)
	}
	err := validateMessagePart(part)
	var te *mcp.ToolError
	if !errorAs(err, &te) || te.Code != "MESSAGEPART_TOO_LARGE" {
		t.Fatalf("validateMessagePart(oversize) = %v, want MESSAGEPART_TOO_LARGE", err)
	}
	if got := te.Details["maxBytes"]; got != maxMessagePartBytes {
		t.Fatalf("details.maxBytes = %v, want %d", got, maxMessagePartBytes)
	}
}

// errorAs is a local errors.As shim kept tiny so the test file does not
// import errors solely for one call.
func errorAs(err error, target **mcp.ToolError) bool {
	if te, ok := err.(*mcp.ToolError); ok {
		*target = te
		return true
	}
	return false
}
