// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
)

// fakeRuntime records what the adapter asks of the pod's runtime.
type fakeRuntime struct {
	started   string
	envelopes [][]byte
	closed    bool
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	f.started = sessionID
	return nil
}

func (f *fakeRuntime) WriteEnvelope(_ string, envelope []byte) error {
	f.envelopes = append(f.envelopes, envelope)
	return nil
}

func (f *fakeRuntime) Close(_ context.Context, _ string) error {
	f.closed = true
	return nil
}

// dialAdapter serves srv over an in-memory connection and returns a
// Client wired to it.
func dialAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

func TestNegotiateVersionSelectsACommonVersion(t *testing.T) {
	cl := dialAdapter(t, adapter.New("adapter-test-build"))

	resp, err := cl.NegotiateVersion(context.Background(), []string{adapter.ProtocolVersionV1})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if resp.GetIncompatible() {
		t.Error("negotiation reported incompatible for a shared version")
	}
	if resp.GetSelectedProtocolVersion() != adapter.ProtocolVersionV1 {
		t.Errorf("selected version = %q, want %q", resp.GetSelectedProtocolVersion(), adapter.ProtocolVersionV1)
	}
	if resp.GetAdapterVersion() != "adapter-test-build" {
		t.Errorf("adapter version = %q, want adapter-test-build", resp.GetAdapterVersion())
	}
}

func TestNegotiateVersionReportsIncompatibleWhenNoVersionShared(t *testing.T) {
	cl := dialAdapter(t, adapter.New("adapter-test-build"))

	resp, err := cl.NegotiateVersion(context.Background(), []string{"9.9.9"})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if !resp.GetIncompatible() {
		t.Error("negotiation did not report incompatible for a disjoint version set")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
	cl := dialAdapter(t, srv)
	ctx := context.Background()

	if err := cl.StartSession(ctx, "sess-x", "claude-code", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if rt.started != "sess-x" {
		t.Errorf("runtime started for %q, want sess-x", rt.started)
	}

	envelope := []byte(`{"type":"user","content":"hello"}`)
	if err := cl.SendMessage(ctx, "sess-x", envelope); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rt.envelopes) != 1 || string(rt.envelopes[0]) != string(envelope) {
		t.Errorf("runtime received %v, want one copy of the envelope", rt.envelopes)
	}

	clean, err := cl.Shutdown(ctx, "sess-x")
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !clean {
		t.Error("Shutdown reported an unclean exit for a clean runtime close")
	}
	if !rt.closed {
		t.Error("the runtime was not closed on Shutdown")
	}
}

func TestSendMessageRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	// No StartSession ran, so the pod holds no session.
	err := cl.SendMessage(context.Background(), "sess-absent", []byte(`{"type":"user"}`))
	if err == nil {
		t.Error("SendMessage to an unassigned session succeeded, want a failure")
	}
}

func TestShutdownRejectsAnUnassignedSession(t *testing.T) {
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)

	clean, err := cl.Shutdown(context.Background(), "sess-absent")
	if err == nil {
		t.Error("Shutdown of an unassigned session succeeded, want a failure")
	}
	if clean {
		t.Error("Shutdown reported a clean exit on an error")
	}
}
