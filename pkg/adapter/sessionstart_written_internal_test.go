// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// sendTestMessage runs SendMessage for sessionID with a minimal envelope.
func sendTestMessage(s *Server, sessionID string) error {
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: []byte(`{"type":"message","id":"m1","input":[]}`),
	})
	return err
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 28.5.3 (CH-MSGSOCK Outbound: session_started)
// The message-writing paths admit a session only once its open sequence
// has written the session_start. The admission starts at that write and
// does not wait for session_started, and a start that fails after the
// write and writes the session's session_end withdraws it, so a message
// that reaches the runtime always follows the session_start of a start
// that has not been ended.
func TestMessageAdmissionFollowsTheSessionStartWrite_spec_28_5_3(t *testing.T) {
	t.Run("no entry", func(t *testing.T) {
		s := New("written-test")
		if err := s.checkSessionStartWritten("sess-none"); !errors.Is(err, errSessionNotOpened) {
			t.Fatalf("checkSessionStartWritten = %v, want errSessionNotOpened", err)
		}
	})
	t.Run("claimed before the open sequence", func(t *testing.T) {
		s, _, rt := ackServer(t)
		if _, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if err := sendTestMessage(s, "sess-a"); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SendMessage before session_start = %v, want FailedPrecondition", err)
		}
		if got := rt.FrameTypes(); len(got) != 0 {
			t.Fatalf("frames = %v, want none", got)
		}
	})
	t.Run("admitted during the session_started wait and withdrawn by session_end", func(t *testing.T) {
		s, _, rt := ackServer(t)
		s.SessionStartAckTimeout = 300 * time.Millisecond
		rt.SetReply(ackruntime.Withhold)
		claim, err := s.claimSessionSlot("sess-a", slotResolve{allowCreate: true}, false, false)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := s.openRuntimeSession(context.Background(), "sess-a", claim, manifestInputs{}, false)
			done <- err
		}()
		waitForStartID(t, rt)
		if err := sendTestMessage(s, "sess-a"); err != nil {
			t.Fatalf("SendMessage during the session_started wait = %v, want admitted", err)
		}
		if err := <-done; !errors.Is(err, errSessionStartUnacknowledged) {
			t.Fatalf("openRuntimeSession = %v, want errSessionStartUnacknowledged", err)
		}
		if err := sendTestMessage(s, "sess-a"); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SendMessage after the start's session_end = %v, want FailedPrecondition", err)
		}
		want := []string{"session_start", "message", "session_end"}
		if got := rt.FrameTypes(); !equalTypes(got, want) {
			t.Fatalf("frames = %v, want %v", got, want)
		}
	})
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start)
// A write that fails for a reason other than an unopened session keeps the
// Internal code, so a gateway caller retries only the refusal it can.
func TestEnvelopeWriteStatusCodes_spec_28_5_3(t *testing.T) {
	if got := status.Code(envelopeWriteStatus("deliver", errSessionNotOpened)); got != codes.FailedPrecondition {
		t.Errorf("unopened session = %s, want FailedPrecondition", got)
	}
	if got := status.Code(envelopeWriteStatus("deliver", errors.New("broken pipe"))); got != codes.Internal {
		t.Errorf("failed write = %s, want Internal", got)
	}
}
