//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// frameRecordingRuntime is a RuntimeProcess that records every frame the
// adapter writes to it, so a contract case reads the session frames off the
// wire the runtime would read rather than off the adapter's structs.
type frameRecordingRuntime struct {
	mu     sync.Mutex
	frames [][]byte
}

func (r *frameRecordingRuntime) Start(context.Context, string) error { return nil }

func (r *frameRecordingRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, append([]byte(nil), envelope...))
	return nil
}

func (r *frameRecordingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *frameRecordingRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *frameRecordingRuntime) Close(context.Context, string) error { return nil }

func (r *frameRecordingRuntime) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.frames...)
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 28.5.3 (CH-MSGSOCK,
//
//	Inbound: session_end), 28.5.3 (CH-MSGSOCK, Session frame writes)
//
// diagnosis: a session_start or session_end the adapter writes on a start
//
//	and a teardown does not validate against the published JSON Lines
//	schema, or the two are not written in the order the Session frame
//	writes table states. A runtime built against the schema then rejects
//	the frame that carries its session's context, or never learns that the
//	session ended, and keeps that session's context for the life of the
//	pod. The frames are built from Go structs whose JSON tags no compiler
//	checks against the schema, so the check has to read the wire.
func TestAdapterSessionFramesValidateAgainstSchema_spec_28_5_3(t *testing.T) {
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	schema := schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")
	rt := &frameRecordingRuntime{}
	s := adapter.New("contract")
	s.WorkspaceBase = t.TempDir()
	s.CredentialsDir = t.TempDir()
	s.Runtime = rt
	ctx := context.Background()

	starts := []*adapterv1.StartSessionRequest{
		{SessionId: &adapterv1.SessionId{Value: "sess-plain"}},
		{
			SessionId:         &adapterv1.SessionId{Value: "sess-context"},
			ExperimentContext: &adapterv1.ExperimentContext{ExperimentId: "exp-1", VariantId: "treatment", Inherited: true},
			TracingContext:    map[string]string{"otel_trace_id": "0af7651916cd43dd"},
		},
	}
	for _, req := range starts {
		if _, err := s.StartSession(ctx, req); err != nil {
			t.Fatalf("StartSession(%s): %v", req.GetSessionId().GetValue(), err)
		}
		if _, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
			SessionId:             req.GetSessionId(),
			UnconditionalTeardown: true,
		}); err != nil {
			t.Fatalf("Shutdown(%s): %v", req.GetSessionId().GetValue(), err)
		}
	}

	frames := rt.snapshot()
	wantTypes := []string{"session_start", "session_end", "session_start", "session_end"}
	if len(frames) != len(wantTypes) {
		t.Fatalf("adapter wrote %d frames, want %d: %q", len(frames), len(wantTypes), frames)
	}
	for i, frame := range frames {
		var doc map[string]any
		dec := json.NewDecoder(bytes.NewReader(frame))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("frame %d is not JSON: %v (%s)", i, err, frame)
		}
		if doc["type"] != wantTypes[i] {
			t.Errorf("frame %d type = %v, want %s", i, doc["type"], wantTypes[i])
		}
		if err := schema.Validate(doc); err != nil {
			t.Errorf("frame %d does not validate against the published schema: %v\n%s", i, err, frame)
		}
	}
}
