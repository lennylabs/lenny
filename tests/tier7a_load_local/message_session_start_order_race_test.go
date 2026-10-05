// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the rule that a session's
// session_start precedes every other frame addressed to the session on the
// runtime connection. The gateway's SendMessage for a bound session can
// arrive while the session's StartSession is still in Runtime.Start, so
// the adapter has to hold the order itself: a message it admits is written
// behind the session_start, and one it refuses is retried by the caller
// after the start.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start), §28.5.3 (CH-MSGSOCK,
// Outbound: session_started), §4.7.1 (role and gateway RPC contract).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// sendUntilAdmitted retries SendMessage while the adapter refuses it with
// FailedPrecondition, as a gateway caller retries a session that has not
// opened yet, and returns the first other outcome.
func sendUntilAdmitted(s *adapter.Server, sessionID string, deadline time.Time) error {
	env := []byte(`{"schemaVersion":1,"type":"message","id":"msg_race","sessionId":"` + sessionID +
		`","from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"ping"}]}`)
	for {
		_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
			SessionId:    &adapterv1.SessionId{Value: sessionID},
			EnvelopeJson: env,
		})
		if status.Code(err) != codes.FailedPrecondition {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("message never admitted: %w", err)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 28.5.3 (CH-MSGSOCK,
//
//	Outbound: session_started), 4.7.1 (role and gateway RPC contract)
//
// diagnosis: a SendMessage that raced a StartSession for a session already
//
//	bound by AssignCredentials reached the runtime ahead of the session's
//	session_start. The adapter admitted the message on the registry entry
//	alone, without checking that the open sequence had written the
//	session_start, so a runtime that keeps per-session context would answer
//	it as an unknown session.
func TestMessageRacingStartSessionFollowsSessionStart_spec_28_5_3(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		sessionID := fmt.Sprintf("sess-%d", i)
		rt := &frameOrderRuntime{}
		s := adapter.New("message-order")
		s.WorkspaceBase = t.TempDir()
		s.CredentialsDir = t.TempDir()
		s.Runtime = rt
		if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
			BindAttempt: "attempt-" + sessionID,
			SessionId:   &adapterv1.SessionId{Value: sessionID},
			Leases: map[string]*adapterv1.CredentialLease{
				"anthropic": {LeaseId: "lease-" + sessionID, Provider: "anthropic", Payload: []byte("{}")},
			},
		}); err != nil {
			t.Fatalf("iteration %d: AssignCredentials: %v", i, err)
		}

		var wg sync.WaitGroup
		var startErr, sendErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, startErr = s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
				SessionId: &adapterv1.SessionId{Value: sessionID},
			})
		}()
		go func() {
			defer wg.Done()
			sendErr = sendUntilAdmitted(s, sessionID, time.Now().Add(10*time.Second))
		}()
		wg.Wait()
		if startErr != nil {
			t.Fatalf("iteration %d: StartSession: %v", i, startErr)
		}
		if sendErr != nil {
			t.Fatalf("iteration %d: SendMessage: %v", i, sendErr)
		}
		got := rt.snapshot()
		if len(got) != 2 || got[0] != "session_start" || got[1] != "message" {
			t.Fatalf("iteration %d: runtime read %v, want [session_start message]", i, got)
		}
	}
}
