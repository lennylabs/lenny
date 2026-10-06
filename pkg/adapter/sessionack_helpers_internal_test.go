// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// startAckedSession starts sessionID through the adapter on a runtime that
// answers session_start with session_started, installing that runtime when
// the Server has none. A test that drives a session-scoped CH-RUNTIMEOPS
// sender after the runtime's capability handshake calls it first, because
// the sender writes its frame only after the adapter has read the session's
// session_started. spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started);
// §28.5.3 (CH-RUNTIMEOPS, Messages).
func startAckedSession(t *testing.T, s *Server, sessionID string) *ackruntime.Runtime {
	t.Helper()
	rt, ok := s.Runtime.(*ackruntime.Runtime)
	if !ok {
		rt = ackruntime.New(t)
		s.Runtime = rt
	}
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
	return rt
}
