//go:build contract

// SPDX-License-Identifier: MIT

// This file carries the wire case for a drain barrier against a session the
// pod holds bound but has never fenced. The pod holds one fenced coordination
// generation per bound session, and within a binding that value is unset until
// the session's first accepted CoordinatorFence. A session that never resumed
// and was never taken over on this pod therefore reaches its drain with no
// recorded generation, and §10.1.8 step 1 states the outcome by reference to
// §10.1.2 step 3: for a session bound to the pod, the barrier is rejected when
// the pod holds a generation for that session that the barrier does not carry,
// and is otherwise accepted.

package adapter_generation_fence_test

import (
	"context"
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

// unfencedSession is the session the barrier case addresses: bound to the pod
// through StartSession and never named by a CoordinatorFence.
const unfencedSession = "sess-unfenced"

// barrierGeneration is the generation the barrier carries. It is the baseline
// a session row holds from creation, which is what a session no replica has
// ever taken over presents on its drain barrier.
const barrierGeneration int64 = 1

// stubTransport satisfies the adapter's checkpoint transport so the Checkpoint
// stream reaches the point where it links the waiting barrier gate. No chunk
// is ever uploaded in this case.
type stubTransport struct{}

func (stubTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 200, "", nil
}

func (stubTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, io.EOF
}

// noopRuntime is a RuntimeProcess that starts and closes without spawning
// anything, so the case can bind a session without a real agent process.
type noopRuntime struct{}

func (noopRuntime) Start(context.Context, string) error           { return nil }
func (noopRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (noopRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (noopRuntime) Close(context.Context, string) error           { return nil }

func (noopRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// boundPod stands up a real adapter Server over a real gRPC transport with
// unfencedSession bound and no fence ever sent for it. It returns the
// connected Adapter client and the server, so the case can poll the pod's
// own barrier state while every assertion is made over the wire.
func boundPod(t *testing.T) (adapterv1.AdapterClient, *adapter.Server) {
	t.Helper()

	s := adapter.New("generation-fence-contract")
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = stubTransport{}
	s.Runtime = noopRuntime{}
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: unfencedSession},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return adapterv1.NewAdapterClient(conn), s
}

// waitBarrierOpen spins until the in-flight CheckpointBarrier has opened the
// session's quiesce-and-hold gate, so the Checkpoint stream that follows links
// into it the way the gateway's concurrently-started stream does.
func waitBarrierOpen(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if s.BarrierWaiting(sessionID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("CheckpointBarrier for %s never opened its quiesce-and-hold gate", sessionID)
}

// TestCheckpointBarrierAcceptedForUnfencedBoundSession drives a real adapter
// over gRPC with a session bound and never fenced, and pins that its drain
// barrier is accepted, is not refused with FailedPrecondition, and leaves the
// pod holding no fenced generation for the session. The last is read back over
// the wire through the next CoordinatorFence: a first fence within a binding
// is exempt from the gap predicate, so a fence at a value several above the
// barrier's reports no gap exactly when the barrier recorded nothing.
//
// spec: 10.1.2 (coordinator handoff protocol, generation validation on every
// gateway-to-pod RPC), 10.1.8 (CheckpointBarrier protocol for rolling
// updates), 4.7 (Gateway to Adapter RPC surface)
//
// diagnosis: the barrier still requires a prior fence on the pod for a session
// the coordinator has never fenced there. A session that never resumed and was
// never taken over then has its drain barrier refused, so the pod is never
// quiesced and the drain checkpoint captures a live workspace. Re-check the
// generation gate in pkg/adapter/coordination.go's CheckpointBarrier: it must
// refuse only when the pod holds a generation for the named session that the
// barrier does not carry.
func TestCheckpointBarrierAcceptedForUnfencedBoundSession(t *testing.T) {
	client, srv := boundPod(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type ackResult struct {
		resp *adapterv1.CheckpointBarrierResponse
		err  error
	}
	ackCh := make(chan ackResult, 1)
	go func() {
		resp, err := client.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
			SessionId:              &adapterv1.SessionId{Value: unfencedSession},
			BarrierId:              "barrier-1",
			CoordinationGeneration: barrierGeneration,
		})
		ackCh <- ackResult{resp, err}
	}()

	// The barrier holds quiescence open until the gateway-driven Checkpoint
	// stream terminates. Drive that stream far enough to link the gate, then
	// end it, which releases the barrier.
	waitBarrierOpen(t, srv, unfencedSession)
	streamCtx, cancelStream := context.WithCancel(ctx)
	stream, err := client.Checkpoint(streamCtx)
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{
			Start: &adapterv1.CheckpointStart{
				SessionId:    &adapterv1.SessionId{Value: unfencedSession},
				CheckpointId: "gw-ckpt-unfenced",
			},
		},
	}); err != nil {
		t.Fatalf("send CheckpointStart: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("await the adapter's first stream frame: %v", err)
	}
	cancelStream()

	got := <-ackCh
	if status.Code(got.err) == codes.FailedPrecondition {
		t.Fatalf("the barrier for a bound session the pod has never fenced must not be refused, got %v", got.err)
	}
	if got.err != nil {
		t.Fatalf("CheckpointBarrier: %v", got.err)
	}
	if got.resp.GetBarrierId() != "barrier-1" {
		t.Fatalf("barrier_id: got %q, want barrier-1", got.resp.GetBarrierId())
	}

	// The accepted barrier records no fenced generation. Over the wire that
	// shows as the next fence being treated as the session's first: it is
	// accepted and reports no gap even though it skips several values above
	// the generation the barrier carried.
	fence, err := client.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: unfencedSession},
		CoordinationGeneration: barrierGeneration + 4,
	})
	if err != nil {
		t.Fatalf("CoordinatorFence: %v", err)
	}
	if !fence.GetAccepted() {
		t.Fatalf("first fence after the barrier must be accepted, got %+v", fence)
	}
	if fence.GetGapDetected() {
		t.Fatalf("the barrier recorded a generation for the session: the following fence was read as a gap, %+v", fence)
	}
	if got := fence.GetLastFencedGeneration(); got != barrierGeneration+4 {
		t.Fatalf("last_fenced_generation: got %d, want %d", got, barrierGeneration+4)
	}
}
