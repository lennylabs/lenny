//go:build contract

// SPDX-License-Identifier: MIT

package adapter_session_address_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// capturingRuntime records the JSONL lines the adapter writes to the
// pod's shared runtime. The assertion reads those bytes rather than a Go
// value, because the envelope reaches the runtime as a line of JSON that
// no compiler checks.
type capturingRuntime struct {
	mu    sync.Mutex
	lines [][]byte
}

func (r *capturingRuntime) Start(context.Context, string) error { return nil }

func (r *capturingRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, append([]byte(nil), envelope...))
	return nil
}

func (r *capturingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *capturingRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *capturingRuntime) Close(context.Context, string) error { return nil }

// written returns the lines recorded so far.
func (r *capturingRuntime) written() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.lines...)
}

// stampPod starts one adapter holding a slot for each named session, with
// a runtime double capturing every line the adapter writes.
func stampPod(t *testing.T, sessions ...string) (*adapter.Server, *capturingRuntime) {
	t.Helper()
	rt := &capturingRuntime{}
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.ManifestDir = t.TempDir()
	s.Runtime = rt
	for _, sessionID := range sessions {
		if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
			Runtime:   "echo",
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", sessionID, err)
		}
	}
	return s, rt
}

// spec: 28.5.3 (the adapter populates the per-session identifier on every
// session-scoped frame, on every pod), 5.2 (a session-mode slot's
// identifier is its session's identifier)
//
// diagnosis: the SendMessage handler forwarded the gateway's envelope to
// the shared runtime without stamping the session's address, so a
// session-scoped frame reached the runtime with nothing saying which
// session it addresses. On a pod holding one slot the frame is
// indistinguishable from the retired pod-global form, so the defect is
// silent there; the two-slot arm is where the runtime has to guess.
func TestSendMessageStampsTheSessionAddressOnTheRuntimeFrame_spec_28_5_3(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sessions []string
		target   string
	}{
		{name: "one_slot", sessions: []string{"alice"}, target: "alice"},
		{name: "two_slots", sessions: []string{"alice", "bob"}, target: "bob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, rt := stampPod(t, tc.sessions...)
			if _, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
				SessionId:    &adapterv1.SessionId{Value: tc.target},
				EnvelopeJson: []byte(`{"type":"message","id":"m1"}`),
			}); err != nil {
				t.Fatalf("SendMessage(%s): %v", tc.target, err)
			}
			lines := rt.written()
			if len(lines) != 1 {
				t.Fatalf("runtime received %d frames, want 1", len(lines))
			}
			var frame map[string]any
			if err := json.Unmarshal(lines[0], &frame); err != nil {
				t.Fatalf("the frame the adapter wrote is not a JSON object: %v (%s)", err, lines[0])
			}
			got, ok := frame["sessionId"].(string)
			if !ok {
				t.Fatalf("the message frame carries no sessionId key: %s", lines[0])
			}
			if got != tc.target {
				t.Errorf("frame sessionId = %q, want %q", got, tc.target)
			}
			if frame["id"] != "m1" || frame["type"] != "message" {
				t.Errorf("the stamp dropped the gateway's own fields: %s", lines[0])
			}
		})
	}
}
