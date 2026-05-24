// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"testing"
)

// TestStripRuntimeFrom_spec_jsonl_line74 verifies the adapter strips a
// runtime-set `from` field from outbound frames before relaying them to
// the gateway, per schemas/lenny-adapter-jsonl.schema.json line 74 ("the
// `from` field is adapter-injected; runtimes MUST NOT set it").
func TestStripRuntimeFrom_spec_jsonl_line74(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantFrom bool // whether `from` should survive
		wantSame bool // whether the bytes should be returned unchanged
	}{
		{
			name:     "spoofed client from is stripped",
			in:       `{"type":"message","from":{"kind":"client","id":"client_evil"},"input":[]}`,
			wantFrom: false,
		},
		{
			name:     "spoofed agent from is stripped",
			in:       `{"type":"message","from":{"kind":"agent","id":"sess_x"}}`,
			wantFrom: false,
		},
		{
			name:     "frame without from passes through unchanged",
			in:       `{"type":"message","input":[]}`,
			wantFrom: false,
			wantSame: true,
		},
		{
			name:     "non-object frame passes through unchanged",
			in:       `"not-an-object"`,
			wantSame: true,
		},
		{
			name:     "malformed json passes through unchanged",
			in:       `{"type":"message","from":`,
			wantSame: true,
		},
		{
			name:     "from substring in a string value is not stripped",
			in:       `{"type":"message","text":"the word from appears here"}`,
			wantSame: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripRuntimeFrom([]byte(tc.in))
			if tc.wantSame {
				if string(got) != tc.in {
					t.Fatalf("stripRuntimeFrom returned %q, want unchanged %q", got, tc.in)
				}
				return
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(got, &obj); err != nil {
				t.Fatalf("output is not valid JSON object: %v (%q)", err, got)
			}
			if _, ok := obj["from"]; ok != tc.wantFrom {
				t.Fatalf("from present = %v, want %v (%q)", ok, tc.wantFrom, got)
			}
			// Sibling fields survive the rewrite.
			if _, ok := obj["type"]; !ok {
				t.Fatalf("stripRuntimeFrom dropped sibling field: %q", got)
			}
		})
	}
}
