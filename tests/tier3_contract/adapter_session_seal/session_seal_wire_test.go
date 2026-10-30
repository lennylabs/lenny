//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_session_seal_test is the Tier 3 contract suite for the
// §4.7.1 session seal on the gateway → pod adapter gRPC contract: the gateway
// adapter client and the adapter's handlers agree on the SealSession and
// UnsealSession wire fields over a real gRPC transport, a sealed session's
// message writes are refused with session_sealed, and an unseal lifts it.
package adapter_session_seal_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// tappedRuntime is a runtime double whose output the adapter can tap, so the
// adapter observes activity and accepts a seal.
type tappedRuntime struct {
	mu    sync.Mutex
	lines [][]byte
	tap   func([]byte)
}

func (r *tappedRuntime) Start(context.Context, string) error { return nil }

func (r *tappedRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, append([]byte(nil), envelope...))
	return nil
}

func (r *tappedRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *tappedRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *tappedRuntime) Close(context.Context, string) error { return nil }

func (r *tappedRuntime) TapOutput(fn func(line []byte)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tap = fn
}

// sealPod starts one adapter over bufconn with one session open, and returns
// the gateway adapter client.
func sealPod(t *testing.T, rt adapter.RuntimeProcess, sessionID string) *adapterclient.Client {
	t.Helper()
	s := adapter.New("contract")
	s.WorkspaceBase = t.TempDir()
	s.ManifestDir = t.TempDir()
	s.Runtime = rt
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterclient.New(conn)
}

// spec: 4.7.1 (Role and Gateway RPC Contract), 28.5.1 (CH-ATTACH)
//
// diagnosis: the gateway adapter client and the adapter disagree on the
// SealSession or UnsealSession wire contract, so the gateway's idle suspension
// cannot seal a session, misreads the idle age, or the adapter keeps
// delivering messages to a sealed session (a message could reach a runtime
// whose pod the gateway is about to release).
func TestSessionSealWireContract_spec_4_7_1(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("seal_refuses_messages_and_unseal_lifts", func(t *testing.T) {
		cl := sealPod(t, &tappedRuntime{}, "alice")
		sealed, _, err := cl.SealSession(ctx, "alice", 0, 1)
		if err != nil {
			t.Fatalf("SealSession: %v", err)
		}
		if sealed {
			t.Fatal("sealed a session whose session_start was just written (activity inside the window)")
		}
		time.Sleep(1100 * time.Millisecond)
		sealed, _, err = cl.SealSession(ctx, "alice", 0, 1)
		if err != nil || !sealed {
			t.Fatalf("SealSession after the window = %v, %v; want sealed", sealed, err)
		}
		err = cl.SendMessage(ctx, "alice", []byte(`{"type":"message","id":"m1"}`))
		if st, _ := status.FromError(err); st.Code() != codes.FailedPrecondition || !strings.HasPrefix(st.Message(), "session_sealed:") {
			t.Fatalf("SendMessage to a sealed session = %v, want FailedPrecondition session_sealed:", err)
		}
		if err := cl.UnsealSession(ctx, "alice", 0); err != nil {
			t.Fatalf("UnsealSession: %v", err)
		}
		if err := cl.SendMessage(ctx, "alice", []byte(`{"type":"message","id":"m2"}`)); err != nil {
			t.Fatalf("SendMessage after the unseal: %v", err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cl := sealPod(t, &tappedRuntime{}, "alice")
		if _, _, err := cl.SealSession(ctx, "", 0, 1); status.Code(err) != codes.InvalidArgument {
			t.Errorf("empty session id = %v, want InvalidArgument", err)
		}
		if _, _, err := cl.SealSession(ctx, "alice", 0, 0); status.Code(err) != codes.InvalidArgument {
			t.Errorf("zero window = %v, want InvalidArgument", err)
		}
		if _, _, err := cl.SealSession(ctx, "bob", 0, 1); err == nil {
			t.Error("sealed an unbound session")
		}
	})

	t.Run("untapped_runtime", func(t *testing.T) {
		cl := sealPod(t, untappedRuntime{}, "alice")
		_, _, err := cl.SealSession(ctx, "alice", 0, 1)
		if st, _ := status.FromError(err); st.Code() != codes.FailedPrecondition || !strings.HasPrefix(st.Message(), "activity_untapped:") {
			t.Fatalf("seal on an untapped runtime = %v, want FailedPrecondition activity_untapped:", err)
		}
	})
}

// untappedRuntime offers no output tap.
type untappedRuntime struct{}

func (untappedRuntime) Start(context.Context, string) error           { return nil }
func (untappedRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (untappedRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (untappedRuntime) Close(context.Context, string) error           { return nil }
func (untappedRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
