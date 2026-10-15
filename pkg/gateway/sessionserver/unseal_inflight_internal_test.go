// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// receivingRuntime receives messages and answers none, so a Send waits until
// its stream ends, and signals when the first message reaches it.
type receivingRuntime struct {
	out      chan []byte
	received chan struct{}
	once     sync.Once
}

func (r *receivingRuntime) TapOutput(func([]byte)) {}

func (r *receivingRuntime) Start(context.Context, string) error { return nil }

func (r *receivingRuntime) WriteEnvelope(_ string, envelope []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(envelope, &probe) == nil && probe.Type == "message" {
		r.once.Do(func() { close(r.received) })
	}
	return nil
}

func (r *receivingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *receivingRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *receivingRuntime) Close(context.Context, string) error           { return nil }

// dialUnsealAdapter serves a real adapter over bufconn with rt as its
// runtime, starts sessionID on it, and returns the connected client. The
// adapter is never sealed, which is the adapter's state once it has lifted
// the seal partway through an UnsealSession call.
func dialUnsealAdapter(t *testing.T, rt *receivingRuntime, sessionID string) *adapterclient.Client {
	t.Helper()
	srv := adapter.New("unseal-inflight-test")
	srv.WorkspaceBase = t.TempDir()
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
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID: sessionID, Runtime: "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return cl
}

// liftingUnsealer grants every seal, and on an unseal signals entered as the
// adapter would after lifting the seal, then blocks until release closes and
// answers err.
type liftingUnsealer struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (*liftingUnsealer) SealSession(context.Context, string, int64, int) (bool, time.Duration, error) {
	return true, 0, nil
}

func (u *liftingUnsealer) UnsealSession(ctx context.Context, _ string, _ int64) error {
	close(u.entered)
	select {
	case <-u.release:
		return u.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// awaitSignal fails the test when ch does not close within five seconds.
func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// spec: §4.7.1 (Role and Gateway RPC Contract), §6.2 (maxClientIdleSeconds
// clock behavior across states)
// The adapter lifts a seal partway through the UnsealSession call, and a
// message written after that reaches the runtime. A Send whose message the
// runtime received while the unseal was in flight, and whose stream then
// ended, returns a stream end rather than ErrSessionSealed, whether the
// unseal later succeeds or fails, because only a refused message has not
// reached the runtime and routing a delivered one again would deliver it
// twice.
func TestUnsealInFlightStreamEndIsNotARefusal_spec_4_7_1(t *testing.T) {
	for _, tc := range []struct {
		name      string
		unsealErr error
	}{
		{name: "unseal succeeds"},
		{name: "unseal fails", unsealErr: errUnsealFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sessionID = "sess-unseal-inflight"
			rt := &receivingRuntime{out: make(chan []byte, 1), received: make(chan struct{})}
			cl := dialUnsealAdapter(t, rt, sessionID)
			bind := &podsession.BindResult{SessionID: sessionID, Adapter: cl}
			reg := podsession.NewRegistry()
			reg.Put(bind)
			pe := executor.NewPodExecutor(reg, nil)

			u := &liftingUnsealer{entered: make(chan struct{}), release: make(chan struct{}), err: tc.unsealErr}
			s := &Server{sealerFor: func(*podsession.BindResult) sessionSealer { return u }}
			ctx := context.Background()
			if sealed, _, err := s.sealSession(ctx, bind, 1); err != nil || !sealed {
				t.Fatalf("seal: sealed %v err %v; want a granted seal", sealed, err)
			}

			unsealDone := make(chan error, 1)
			go func() { unsealDone <- s.unsealSession(ctx, bind) }()
			awaitSignal(t, u.entered, "the unseal call to reach the adapter")

			sendDone := make(chan error, 1)
			go func() {
				_, err := pe.Send(ctx, sessionID, []executor.Message{{Role: "user", Content: "one"}})
				sendDone <- err
			}()
			awaitSignal(t, rt.received, "the runtime to receive the message")
			pe.EvictStream(sessionID)

			var sendErr error
			select {
			case sendErr = <-sendDone:
			case <-time.After(5 * time.Second):
				t.Fatal("Send did not return after its stream ended")
			}
			if sendErr == nil || errors.Is(sendErr, executor.ErrSessionSealed) {
				t.Fatalf("Send = %v during an in-flight unseal; want a stream end, not ErrSessionSealed", sendErr)
			}

			close(u.release)
			if err := <-unsealDone; !errors.Is(err, tc.unsealErr) {
				t.Fatalf("unseal = %v, want %v", err, tc.unsealErr)
			}
			if bind.Sealed() != (tc.unsealErr != nil) {
				t.Fatalf("marked after the unseal = %v, want %v", bind.Sealed(), tc.unsealErr != nil)
			}
		})
	}
}
