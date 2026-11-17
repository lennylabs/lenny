//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// earlyMessage is the message envelope the gateway would forward for
// sessionID, before or after the session's start.
func earlyMessage(sessionID string) []byte {
	return []byte(`{"schemaVersion":1,"type":"message","id":"msg_early","sessionId":"` + sessionID +
		`","from":{"kind":"client","id":"c"},"input":[{"type":"text","inline":"ping"}]}`)
}

// serveAdapterOverBufconn serves s over an in-memory listener and returns
// a connected adapter client.
func serveAdapterOverBufconn(t *testing.T, s *adapter.Server) adapterv1.AdapterClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}

// attachWithFirstEnvelope opens an Attach stream whose first request
// carries env, and returns the error the stream ends with, nil on a clean
// end. The recording runtime's output is already closed, so a stream the
// adapter admits ends cleanly once the envelope is written.
func attachWithFirstEnvelope(t *testing.T, client adapterv1.AdapterClient, sessionID string, env []byte) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: env,
	}); err != nil {
		t.Fatalf("Send first envelope(%s): %v", sessionID, err)
	}
	_ = stream.CloseSend()
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// recordedTypes returns the type of each frame the runtime read, in order.
func recordedTypes(t *testing.T, rt *frameRecordingRuntime) []string {
	t.Helper()
	var out []string
	for _, f := range rt.snapshot() {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(f, &probe); err != nil {
			t.Fatalf("runtime read a frame that is not JSON: %v (%s)", err, f)
		}
		out = append(out, probe.Type)
	}
	return out
}

// spec: 28.5.3 (CH-MSGSOCK, Inbound: session_start), 28.5.3 (CH-MSGSOCK,
//
//	Session errors), 4.7.1 (role and gateway RPC contract)
//
// diagnosis: the adapter wrote a message for a session ahead of that
//
//	session's session_start because the gateway called SendMessage or
//	opened an Attach stream with a first envelope after AssignCredentials
//	bound the session and before StartSession opened it. A runtime that
//	keeps per-session context answers such a message with a session error,
//	so the client sees a spurious failure for a session that then opens
//	normally. The adapter owns the order on the runtime connection and has
//	to refuse the write rather than rely on the gateway's call order.
func TestMessageBeforeSessionStartIsRefused_spec_28_5_3(t *testing.T) {
	const sessionID = "sess-early"
	rt := &frameRecordingRuntime{}
	s := adapter.New("contract")
	s.WorkspaceBase = t.TempDir()
	s.CredentialsDir = t.TempDir()
	s.Runtime = rt
	client := serveAdapterOverBufconn(t, s)
	ctx := context.Background()

	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		BindAttempt: "attempt-1",
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "lease-1", Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}

	_, err := client.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: earlyMessage(sessionID),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SendMessage before StartSession = %v, want FailedPrecondition", err)
	}
	if err := attachWithFirstEnvelope(t, client, sessionID, earlyMessage(sessionID)); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Attach with a first envelope before StartSession = %v, want FailedPrecondition", err)
	}
	if got := recordedTypes(t, rt); len(got) != 0 {
		t.Fatalf("runtime read %v before StartSession, want no frame", got)
	}

	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := client.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: earlyMessage(sessionID),
	}); err != nil {
		t.Fatalf("SendMessage after StartSession: %v", err)
	}
	if err := attachWithFirstEnvelope(t, client, sessionID, earlyMessage(sessionID)); err != nil {
		t.Fatalf("Attach with a first envelope after StartSession: %v", err)
	}
	want := []string{"session_start", "message", "message"}
	got := recordedTypes(t, rt)
	if len(got) != len(want) {
		t.Fatalf("runtime read %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runtime read %v, want %v", got, want)
		}
	}
}
