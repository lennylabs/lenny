//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// sessionScopedFrames are the frame definitions the published JSONL schema
// declares for the six session-scoped frame types. Each one addresses a
// session, so each one declares the per-session address property. The two
// protocol-level frames are listed separately below because the addressing
// rule does not reach them.
var sessionScopedFrames = []string{
	"messageEnvelope",
	"tool_call",
	"tool_result",
	"response",
	"status",
	"set_tracing_context",
}

// adapterPopulatedFrames are the session-scoped frames the adapter emits
// towards the runtime. The adapter populates the address on every one of
// them on every pod, so absence has no defined outcome on this leg and the
// published schema requires the property.
var adapterPopulatedFrames = []string{"messageEnvelope", "tool_result"}

// runtimeEmittedFrames are the session-scoped frames the runtime emits
// towards the adapter. An absent address on this leg resolves to the
// receiving stream's own binding on a pod holding at most one slot, so the
// published schema leaves the property optional.
var runtimeEmittedFrames = []string{"tool_call", "response", "status", "set_tracing_context"}

// protocolFrames are the frame definitions that carry no per-session address.
var protocolFrames = []string{"heartbeat", "heartbeat_ack"}

// jsonlFrameDefs reads the published JSONL schema and returns its $defs map.
func jsonlFrameDefs(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join(schematest.RepoRoot(t), "schemas/lenny-adapter-jsonl.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Defs map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Defs) == 0 {
		t.Fatalf("%s declares no $defs", path)
	}
	return doc.Defs
}

// frameProperties returns the properties map of one frame definition.
func frameProperties(t *testing.T, defs map[string]any, frame string) map[string]any {
	t.Helper()
	def, ok := defs[frame].(map[string]any)
	if !ok {
		t.Fatalf("published JSONL schema declares no %q frame (renamed or removed?)", frame)
	}
	props, ok := def["properties"].(map[string]any)
	if !ok {
		t.Fatalf("published JSONL schema: %q declares no properties", frame)
	}
	return props
}

// frameRequired returns the set of property names one frame definition
// declares as required.
func frameRequired(t *testing.T, defs map[string]any, frame string) map[string]bool {
	t.Helper()
	def, ok := defs[frame].(map[string]any)
	if !ok {
		t.Fatalf("published JSONL schema declares no %q frame (renamed or removed?)", frame)
	}
	required := map[string]bool{}
	list, _ := def["required"].([]any)
	for _, name := range list {
		if s, ok := name.(string); ok {
			required[s] = true
		}
	}
	return required
}

// spec: 28.5.3 (session-scoped frame schemas and addressing), 15.4 (published wire artifacts), 5.2 (a slot on every pod)
// diagnosis: the published JSONL schema does not declare the per-session
//
//	address as `sessionId` on every session-scoped frame, or still declares
//	it as `slotId`. The wire addresses a session by its session identifier
//	on this leg, on every pod, so a runtime author reading the published
//	artifact would emit or expect a key the adapter neither populates nor
//	reads. The `status` frame is in the set because the adapter relays it
//	to the addressed session's stream alone; a `status` frame with no
//	declared address would be published as a pod-wide broadcast.
func TestSessionScopedFramesDeclareSessionAddress(t *testing.T) {
	t.Parallel()
	defs := jsonlFrameDefs(t)

	for _, frame := range sessionScopedFrames {
		frame := frame
		t.Run(frame, func(t *testing.T) {
			t.Parallel()
			props := frameProperties(t, defs, frame)
			if _, ok := props["slotId"]; ok {
				t.Errorf("%s declares `slotId`; the per-session address on this leg is `sessionId`", frame)
			}
			addr, ok := props["sessionId"].(map[string]any)
			if !ok {
				t.Fatalf("%s declares no `sessionId` property", frame)
			}
			if addr["type"] != "string" {
				t.Errorf("%s declares `sessionId` as %v, want a string", frame, addr["type"])
			}
			if _, ok := addr["minLength"]; ok {
				t.Errorf("%s constrains the length of `sessionId`; the addressing rule resolves an empty address the way it resolves an absent one, so the published artifact declares the type alone", frame)
			}
			desc, _ := addr["description"].(string)
			if desc == "" {
				t.Errorf("%s declares `sessionId` with no description; the population rule is what the published artifact states", frame)
			}
		})
	}

	for _, frame := range protocolFrames {
		frame := frame
		t.Run(frame, func(t *testing.T) {
			t.Parallel()
			props := frameProperties(t, defs, frame)
			for _, key := range []string{"sessionId", "slotId"} {
				if _, ok := props[key]; ok {
					t.Errorf("%s declares %q; a protocol-level frame carries no per-session address", frame, key)
				}
			}
		})
	}
}

// spec: 28.5.3 (session-scoped frame schemas and addressing), 15.4 (published wire artifacts)
// diagnosis: the published JSONL schema accepted a session-scoped frame whose
//
//	`sessionId` is not a string. The adapter compares the address as exact
//	string equality and reads a value it cannot decode as a string as no
//	address at all, so a non-string value becomes an unaddressed frame that
//	a pod holding more than one slot rejects and relays to no stream. An
//	empty string is a different case and validates: the addressing rule
//	resolves an absent and an empty address alike, and the adapter's
//	demultiplexer reads them alike, so a schema that refuses the empty form
//	publishes as nonconforming a frame the adapter accepts and resolves.
//	The published schema is where a runtime author's authoring mistake is
//	caught, and it catches it only while the property is declared under the
//	name the wire carries.
func TestSessionScopedFramesRejectUnusableSessionAddress(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	frames := map[string]string{
		"status":              `{"type":"status","state":"thinking","sessionId":%s}`,
		"response":            `{"type":"response","text":"done","sessionId":%s}`,
		"tool_call":           `{"type":"tool_call","id":"tc_01J9X0ZW1ZF7K8Q1V2T3M4N5P2","name":"read_file","arguments":{},"sessionId":%s}`,
		"tool_result":         `{"type":"tool_result","id":"tc_01J9X0ZW1ZF7K8Q1V2T3M4N5P2","content":[],"sessionId":%s}`,
		"set_tracing_context": `{"type":"set_tracing_context","context":{},"sessionId":%s}`,
	}

	for frame, tmpl := range frames {
		frame, tmpl := frame, tmpl
		t.Run(frame, func(t *testing.T) {
			t.Parallel()
			addressed := fmt.Sprintf(tmpl, `"sess_abc123"`)
			if err := validateFrame(t, schema, addressed); err != nil {
				t.Errorf("addressed %s frame failed the JSONL schema: %v\n  payload: %s", frame, err, addressed)
			}
			for _, bad := range []string{"1", "null", `{"id":"sess_abc123"}`} {
				payload := fmt.Sprintf(tmpl, bad)
				if err := validateFrame(t, schema, payload); err == nil {
					t.Errorf("%s frame with a non-string address validated, want rejection: %s", frame, payload)
				}
			}
			empty := fmt.Sprintf(tmpl, `""`)
			if err := validateFrame(t, schema, empty); err != nil {
				t.Errorf("%s frame with an empty address failed the JSONL schema: %v\n  payload: %s", frame, err, empty)
			}
		})
	}
}

// spec: 28.5.3 (session-scoped frame schemas and addressing), 15.4 (published wire artifacts), 5.2 (a slot on every pod)
// diagnosis: the published JSONL schema does not encode the population rule's
//
//	split by leg. On the adapter-to-runtime leg the adapter populates the
//	per-session address on every frame on every pod, so absence has no
//	defined outcome there and the canonical frame block declares the
//	property required. On the runtime-to-adapter leg an absent address
//	resolves to the receiving stream's own binding on a pod holding at most
//	one slot, so the property is optional there. A schema that leaves the
//	address optional on the adapter-emitted frames publishes an unaddressed
//	`tool_result` as conforming, and the compliance suite generated from
//	this artifact stops enforcing the invariant on the one leg where
//	absence means nothing.
func TestAdapterPopulatedFramesRequireSessionAddress(t *testing.T) {
	t.Parallel()
	defs := jsonlFrameDefs(t)

	for _, frame := range adapterPopulatedFrames {
		frame := frame
		t.Run(frame, func(t *testing.T) {
			t.Parallel()
			if !frameRequired(t, defs, frame)["sessionId"] {
				t.Errorf("%s does not require `sessionId`; the adapter populates it on every pod, so an unaddressed frame has no defined outcome on this leg", frame)
			}
		})
	}

	for _, frame := range runtimeEmittedFrames {
		frame := frame
		t.Run(frame, func(t *testing.T) {
			t.Parallel()
			if frameRequired(t, defs, frame)["sessionId"] {
				t.Errorf("%s requires `sessionId`; a runtime on a pod holding at most one slot may omit it and have it resolve to the receiving stream's own binding", frame)
			}
		})
	}
}

// spec: 28.5.3 (session-scoped frame schemas and addressing), 15.4 (published wire artifacts)
// diagnosis: the published JSONL schema validated an adapter-emitted
//
//	session-scoped frame that carries no address, or rejected a
//	runtime-emitted one that carries none. A runtime author validates
//	against this artifact, so the first case publishes a `tool_result` or
//	`message` the adapter never emits and the receiving side cannot route,
//	and the second case makes the single-slot omission the addressing rule
//	permits unpublishable.
func TestUnaddressedFramesValidateByLeg(t *testing.T) {
	t.Parallel()
	schema := compileJSONL(t)

	rejected := map[string]string{
		"message":     `{"schemaVersion":1,"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5P1","from":{"kind":"client","id":"client_alice"},"input":[]}`,
		"tool_result": `{"type":"tool_result","id":"tc_01J9X0ZW1ZF7K8Q1V2T3M4N5P2","content":[]}`,
	}
	for frame, payload := range rejected {
		frame, payload := frame, payload
		t.Run("rejects_unaddressed_"+frame, func(t *testing.T) {
			t.Parallel()
			if err := validateFrame(t, schema, payload); err == nil {
				t.Errorf("unaddressed %s frame validated, want rejection: %s", frame, payload)
			}
		})
	}

	accepted := map[string]string{
		"tool_call":           `{"type":"tool_call","id":"tc_01J9X0ZW1ZF7K8Q1V2T3M4N5P2","name":"read_file","arguments":{}}`,
		"response":            `{"type":"response","text":"done"}`,
		"status":              `{"type":"status","state":"thinking"}`,
		"set_tracing_context": `{"type":"set_tracing_context","context":{}}`,
	}
	for frame, payload := range accepted {
		frame, payload := frame, payload
		t.Run("accepts_unaddressed_"+frame, func(t *testing.T) {
			t.Parallel()
			if err := validateFrame(t, schema, payload); err != nil {
				t.Errorf("unaddressed %s frame failed the JSONL schema: %v\n  payload: %s", frame, err, payload)
			}
		})
	}
}
