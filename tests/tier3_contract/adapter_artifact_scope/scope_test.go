//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_artifact_scope_test is the Tier 3 contract suite for
// the division of the intra-pod wire between the two published adapter
// artifacts. §15.4 publishes schemas/lenny-adapter-jsonl.schema.json for
// the CH-MSGSOCK stdin/stdout messages and
// schemas/runtime-ops-events.schema.json for the CH-RUNTIMEOPS frames
// the adapter and the runtime exchange on the intra-pod runtime-ops Unix
// socket (§28.5.3). These tests pin that division on the artifacts
// themselves: the JSONL artifact's own description sends the
// runtime-operations frames to the other artifact, it rejects every one
// of them, and the artifact it names accepts them.
package adapter_artifact_scope_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	jsonlArtifact      = "schemas/lenny-adapter-jsonl.schema.json"
	runtimeOpsArtifact = "schemas/runtime-ops-events.schema.json"
)

// sessionIDFixture is the session every session-scoped frame below names.
const sessionIDFixture = "sess_01HX9F0YWXKK0V7QZ7G6P3R5JN"

// sessionScopedRuntimeOpsFrames are the CH-RUNTIMEOPS frames the §28.5.3
// card's **Messages.** bullet names session-scoped. Each carries the
// `sessionId` of the session it concerns, so a multi-session runtime can
// tell which session a checkpoint quiesces and the adapter can attribute
// direct-mode tokens. The process-scoped frames and the runtime's
// correlated replies are absent from this list.
var sessionScopedRuntimeOpsFrames = []string{
	"checkpoint_request",
	"checkpoint_complete",
	"interrupt_request",
	"credentials_rotated",
	"deadline_approaching",
	"files_updated",
	"llm_request_completed",
}

// runtimeOpsFrames are CH-RUNTIMEOPS frames drawn from the §28.5.3
// message-schema table, each at its minimum conforming field set.
func runtimeOpsFrames() map[string]map[string]any {
	return map[string]map[string]any{
		"checkpoint_request": {
			"type":         "checkpoint_request",
			"sessionId":    sessionIDFixture,
			"checkpointId": "ckpt_01HX9F0YWXKK0V7QZ7G6P3R5JN",
			"deadlineMs":   30000,
		},
		"checkpoint_complete": {
			"type":         "checkpoint_complete",
			"sessionId":    sessionIDFixture,
			"checkpointId": "ckpt_01HX9F0YWXKK0V7QZ7G6P3R5JN",
			"status":       "ok",
		},
		"interrupt_request": {
			"type":        "interrupt_request",
			"sessionId":   sessionIDFixture,
			"interruptId": "int_01HX9F0YWXKK0V7QZ7G6P3R5JN",
			"deadlineMs":  5000,
		},
		"credentials_rotated": {
			"type":            "credentials_rotated",
			"sessionId":       sessionIDFixture,
			"provider":        "anthropic",
			"credentialsPath": "/run/lenny/slots/sess_01HX9F0YWXKK0V7QZ7G6P3R5JN/credentials.json",
			"leaseId":         "lease_01HX9F0YWXKK0V7QZ7G6P3R5JN",
		},
		"deadline_approaching": {
			"type":        "deadline_approaching",
			"sessionId":   sessionIDFixture,
			"remainingMs": 60000,
			"trigger":     "session_age",
		},
		"files_updated": {
			"type":      "files_updated",
			"sessionId": sessionIDFixture,
		},
		"llm_request_completed": {
			"type":      "llm_request_completed",
			"sessionId": sessionIDFixture,
			"requestId": "req_01HX9F0YWXKK0V7QZ7G6P3R5JN",
			"provider":  "anthropic",
			"status":    "ok",
		},
		"checkpoint_ready": {
			"type":         "checkpoint_ready",
			"checkpointId": "ckpt_01HX9F0YWXKK0V7QZ7G6P3R5JN",
		},
		"interrupt_acknowledged": {
			"type":        "interrupt_acknowledged",
			"interruptId": "int_01HX9F0YWXKK0V7QZ7G6P3R5JN",
		},
	}
}

// compileJSONL compiles the JSONL artifact with the MessagePart schema
// resolved locally, because the artifact $refs it by its $id URL.
func compileJSONL(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	return schematest.MustCompile(t, c, jsonlArtifact)
}

// spec: 15.4 (runtime adapter artifacts), 28.5.3 (CH-MSGSOCK and
//
//	CH-RUNTIMEOPS intra-pod channels)
//
// diagnosis: the published JSONL artifact's description no longer sends
//
//	the CH-RUNTIMEOPS frames to the artifact that schematizes
//	them. A reader of schemas/lenny-adapter-jsonl.schema.json is
//	then told the wrong transport and the wrong peers for
//	cooperative quiesce, interrupt, credential rotation, and the
//	deadline warning, and the artifact contradicts the §15.4
//	artifact list.
func TestJSONLArtifactDescriptionNamesTheRuntimeOpsArtifact(t *testing.T) {
	t.Parallel()

	description := artifactDescription(t, jsonlArtifact)
	if !strings.Contains(description, runtimeOpsArtifact) {
		t.Fatalf("%s description must send the CH-RUNTIMEOPS frames to %s, got: %s",
			jsonlArtifact, runtimeOpsArtifact, description)
	}
	if !strings.Contains(description, "CH-RUNTIMEOPS") {
		t.Errorf("%s description must name the CH-RUNTIMEOPS channel, got: %s", jsonlArtifact, description)
	}
	for _, wrong := range []string{"CH-ADAPTEREVENTS", "AdapterEvents", "gRPC"} {
		if strings.Contains(description, wrong) {
			t.Errorf("%s description must not place the CH-RUNTIMEOPS frames on %s, got: %s",
				jsonlArtifact, wrong, description)
		}
	}
}

// spec: 15.4 (runtime adapter artifacts), 28.5.3 (CH-MSGSOCK message set)
//
// diagnosis: schemas/lenny-adapter-jsonl.schema.json accepted a
//
//	CH-RUNTIMEOPS frame. The stdin/stdout artifact schematizes the
//	CH-MSGSOCK messages only, so accepting a runtime-operations
//	frame means its `oneOf` gained a branch that belongs to
//	schemas/runtime-ops-events.schema.json and the two artifacts now
//	overlap on the same `type` discriminator.
func TestJSONLArtifactRejectsRuntimeOpsFrames(t *testing.T) {
	t.Parallel()

	validator := compileJSONL(t)
	for name, frame := range runtimeOpsFrames() {
		name, frame := name, frame
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validator.Validate(frame); err == nil {
				t.Fatalf("%s must reject the CH-RUNTIMEOPS frame %q", jsonlArtifact, name)
			}
		})
	}
}

// spec: 15.4 (runtime adapter artifacts), 28.5.3 (CH-RUNTIMEOPS message
//
//	schema table)
//
// diagnosis: the artifact the JSONL description points at does not
//
//	schematize the frames it is credited with. Either
//	schemas/runtime-ops-events.schema.json lost a $defs branch or a
//	field of the §28.5.3 message-schema table changed without the
//	artifact following, and the frames are now schematized nowhere.
func TestRuntimeOpsArtifactAcceptsTheFramesTheJSONLArtifactScopesOut(t *testing.T) {
	t.Parallel()

	validator := schematest.Compile(t, runtimeOpsArtifact)
	for name, frame := range runtimeOpsFrames() {
		name, frame := name, frame
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validator.Validate(frame); err != nil {
				t.Fatalf("%s must accept the CH-RUNTIMEOPS frame %q, got: %v", runtimeOpsArtifact, name, err)
			}
		})
	}
}

// spec: 15.4 (runtime adapter artifacts), 28.5.3 (CH-RUNTIMEOPS message
//
//	schema table)
//
// diagnosis: a malformed CH-RUNTIMEOPS frame was accepted somewhere. The
//
//	empty frame, a frame missing a required field, and a frame with
//	a wrong-typed field must be rejected by both artifacts;
//	acceptance means one of them carries a pass-through branch that
//	silently swallows an unschematized runtime-operations frame.
func TestNeitherArtifactAcceptsAMalformedRuntimeOpsFrame(t *testing.T) {
	t.Parallel()

	malformed := map[string]any{
		"empty":                map[string]any{},
		"unknown type":         map[string]any{"type": "checkpoint_started"},
		"missing checkpointId": map[string]any{"type": "checkpoint_request", "sessionId": sessionIDFixture, "deadlineMs": 30000},
		"wrong-typed deadline": map[string]any{
			"type":         "checkpoint_request",
			"sessionId":    sessionIDFixture,
			"checkpointId": "ckpt_01HX9F0YWXKK0V7QZ7G6P3R5JN",
			"deadlineMs":   "30000",
		},
	}

	validators := map[string]*jsonschema.Schema{
		jsonlArtifact:      compileJSONL(t),
		runtimeOpsArtifact: schematest.Compile(t, runtimeOpsArtifact),
	}
	for artifact, validator := range validators {
		artifact, validator := artifact, validator
		for name, frame := range malformed {
			name, frame := name, frame
			t.Run(filepath.Base(artifact)+"/"+name, func(t *testing.T) {
				t.Parallel()
				if err := validator.Validate(frame); err == nil {
					t.Fatalf("%s must reject the malformed frame %q", artifact, name)
				}
			})
		}
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages)
//
// diagnosis: schemas/runtime-ops-events.schema.json accepted a
//
//	session-scoped CH-RUNTIMEOPS frame with its `sessionId` removed or
//	empty. The card's **Messages.** bullet makes the address required on
//	every session-scoped frame, because the runtime process serves every
//	session the pod holds: without it a multi-session runtime cannot tell
//	which session a checkpoint quiesces, an interrupt stops, a rotation
//	rebinds, or a deadline warns, and the adapter cannot attribute a
//	direct-mode token count to a session. Acceptance means the frame's
//	`required` list lost `sessionId` or its `minLength` bound was dropped.
func TestRuntimeOpsArtifactRequiresSessionIDOnSessionScopedFrames(t *testing.T) {
	t.Parallel()

	validator := schematest.Compile(t, runtimeOpsArtifact)
	frames := runtimeOpsFrames()
	for _, name := range sessionScopedRuntimeOpsFrames {
		name := name
		frame, ok := frames[name]
		if !ok {
			t.Fatalf("runtimeOpsFrames has no %q frame to check", name)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validator.Validate(frame); err != nil {
				t.Fatalf("%s must accept the addressed %q frame, got: %v", runtimeOpsArtifact, name, err)
			}
			unaddressed := withoutField(frame, "sessionId")
			if err := validator.Validate(unaddressed); err == nil {
				t.Errorf("%s accepted %q with its sessionId removed; the session-scoped frame requires it", runtimeOpsArtifact, name)
			}
			empty := withoutField(frame, "sessionId")
			empty["sessionId"] = ""
			if err := validator.Validate(empty); err == nil {
				t.Errorf("%s accepted %q with an empty sessionId; an empty address names no session", runtimeOpsArtifact, name)
			}
		})
	}
}

// withoutField returns a copy of frame with field removed.
func withoutField(frame map[string]any, field string) map[string]any {
	out := make(map[string]any, len(frame))
	for k, v := range frame {
		if k != field {
			out[k] = v
		}
	}
	return out
}

// artifactDescription reads the top-level `description` of a published
// schema artifact.
func artifactDescription(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var parsed struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	if parsed.Description == "" {
		t.Fatalf("%s has no description", rel)
	}
	return parsed.Description
}
