// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"
)

// spec: §15.4.6 line 2405 — the harness validates a runtime's response
// frame against schemas/lenny-adapter-jsonl.schema.json. The published
// schemas are compiled from the embedded FS so the check runs against a
// third-party runtime in its own repository.
func TestValidateJSONLFrame_spec_15_4_6_2405(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		ok    bool
	}{
		{"valid response with output", `{"type":"response","output":[{"schemaVersion":1,"type":"text","inline":"hi"}]}`, true},
		{"valid text-shorthand response", `{"type":"response","text":"hi"}`, true},
		{"valid heartbeat_ack", `{"type":"heartbeat_ack"}`, true},
		{"response output not an array", `{"type":"response","output":"nope"}`, false},
		{"not json", `this is not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJSONLFrame([]byte(tc.frame))
			if tc.ok && err != nil {
				t.Fatalf("validateJSONLFrame(%s) = %v, want nil", tc.frame, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validateJSONLFrame(%s) = nil, want a schema error", tc.frame)
			}
		})
	}
}

// spec: §15.4.6 line 2408 — every OutputPart the runtime emits validates
// against schemas/outputpart.schema.json, including the required
// schemaVersion the §15.4.1 producer contract mandates.
func TestValidateOutputPart_spec_15_4_6_2408(t *testing.T) {
	cases := []struct {
		name string
		part string
		ok   bool
	}{
		{"valid text part", `{"schemaVersion":1,"type":"text","inline":"hi"}`, true},
		{"missing schemaVersion", `{"type":"text","inline":"hi"}`, false},
		{"inline and ref mutually exclusive", `{"schemaVersion":1,"type":"text","inline":"x","ref":"lenny-blob://acme/s/p"}`, false},
		{"not json", `{`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOutputPart(json.RawMessage(tc.part))
			if tc.ok && err != nil {
				t.Fatalf("validateOutputPart(%s) = %v, want nil", tc.part, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validateOutputPart(%s) = nil, want a schema error", tc.part)
			}
		})
	}
}

// validateResponseFrame validates the frame and every OutputPart it
// carries, so a response with a schema-invalid part is rejected even
// when the envelope itself is well-formed. spec: §15.4.6 lines 2405, 2408.
func TestValidateResponseFrame_rejectsBadOutputPart_spec_15_4_6_2408(t *testing.T) {
	// Envelope is valid JSONL, but the output part omits schemaVersion.
	frame := `{"type":"response","output":[{"type":"text","inline":"hi"}]}`
	if err := validateResponseFrame([]byte(frame)); err == nil {
		t.Fatal("validateResponseFrame accepted a response whose OutputPart violates the schema")
	}
	good := `{"type":"response","output":[{"schemaVersion":1,"type":"text","inline":"hi"}]}`
	if err := validateResponseFrame([]byte(good)); err != nil {
		t.Fatalf("validateResponseFrame rejected a valid response: %v", err)
	}
}

// The embedded schemas compile cleanly; a failure here means the embed
// FS or the schema $id wiring is broken.
func TestLoadSchemasCompiles(t *testing.T) {
	if err := loadSchemas(); err != nil {
		t.Fatalf("loadSchemas: %v", err)
	}
	if jsonlSchema == nil || outputPartSchema == nil {
		t.Fatal("schemas did not compile")
	}
}
