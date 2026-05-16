// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

// respondingRuntime is an adapter.RuntimeProcess that replies to every
// written envelope with a §15.4.1 response frame on its output stream.
type respondingRuntime struct {
	out chan []byte
}

func (r *respondingRuntime) Start(context.Context, string) error { return nil }
func (r *respondingRuntime) WriteEnvelope(string, []byte) error {
	r.out <- []byte(`{"type":"response","text":"ack"}`)
	return nil
}
func (r *respondingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *respondingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *respondingRuntime) Close(context.Context, string) error           { return nil }

// dialPodAdapter serves an adapter backed by rt over bufconn, starts a
// session on it, and returns a connected adapterclient.Client.
func dialPodAdapter(t *testing.T, rt adapter.RuntimeProcess) *adapterclient.Client {
	t.Helper()
	srv := adapter.New("podexec-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt
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
	if err := cl.StartSession(context.Background(), "sess-pod", "echo", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return cl
}

func TestPodExecutorSendStreamsResponse(t *testing.T) {
	cl := dialPodAdapter(t, &respondingRuntime{out: make(chan []byte, 8)})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", SandboxName: "sbx-1", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil)
	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out) != 1 || out[0].Type != "text" || out[0].Text != "ack" {
		t.Errorf("Send output = %+v, want one text part \"ack\"", out)
	}
}

func TestPodExecutorSendReusesTheStreamAcrossMessages(t *testing.T) {
	cl := dialPodAdapter(t, &respondingRuntime{out: make(chan []byte, 8)})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil)
	for i := 0; i < 3; i++ {
		out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
			{Role: "user", Content: "msg"},
		})
		if err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		if len(out) != 1 || out[0].Text != "ack" {
			t.Errorf("Send %d output = %+v, want one \"ack\" part", i, out)
		}
	}
}

func TestPodExecutorSendUnboundSession(t *testing.T) {
	pe := executor.NewPodExecutor(podsession.NewRegistry(), nil)
	if _, err := pe.Send(context.Background(), "sess-absent", []executor.Message{{Content: "x"}}); err == nil {
		t.Error("Send for an unbound session succeeded, want a failure")
	}
}

func TestPodExecutorCloseUnboundSession(t *testing.T) {
	pe := executor.NewPodExecutor(podsession.NewRegistry(), nil)
	if err := pe.Close(context.Background(), "sess-absent"); err != nil {
		t.Errorf("Close of an unbound session = %v, want nil", err)
	}
}
