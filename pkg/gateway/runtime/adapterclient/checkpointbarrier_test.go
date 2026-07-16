// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// okCheckpointTransport accepts every chunk PUT with a 200 so the
// gateway-driven Checkpoint stream a barrier holds for can run to a
// CheckpointSummary. It is the minimal transport the adapter needs to
// pass its configured-transport gate on the Checkpoint RPC.
type okCheckpointTransport struct{}

func (okCheckpointTransport) PutChunk(_ context.Context, _ string, _ map[string]string, _ int64, body io.Reader) (int, string, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return 0, "", err
	}
	return 200, "", nil
}

func (okCheckpointTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, io.EOF
}

// barrierServer builds an adapter server with a started session and
// returns it alongside a client dialed to it. The session is claimed
// via the StartSession RPC so the server's checkSession gate passes. The
// pod is configured to serve the gateway-driven Checkpoint stream so a
// quiesce-and-hold barrier can be released by driving that stream.
func barrierServer(t *testing.T) (*adapter.Server, *adapterclient.Client) {
	t.Helper()
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.ManifestDir = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.CheckpointTransport = okCheckpointTransport{}
	srv.Runtime = &fakeRuntime{}
	cl := dialAdapter(t, srv)
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID: "s1",
		Runtime:   "claude-code",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return srv, cl
}

// driveBarrierCheckpointStream plays the gateway side of the Checkpoint
// stream the held barrier waits for: it opens the stream with a
// CheckpointStart carrying checkpointID, answers each ChunkReady with a
// presigned grant, and returns once the adapter closes the stream with a
// CheckpointSummary. Its termination fires the adapter's barrier-complete
// hook, echoing checkpointID back on the barrier ack.
func driveBarrierCheckpointStream(t *testing.T, cl *adapterclient.Client, ctx context.Context, checkpointID string) {
	t.Helper()
	stream, err := cl.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   checkpointID,
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send CheckpointStart: %v", err)
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("Checkpoint stream Recv: %v", err)
		}
		switch m := msg.GetMsg().(type) {
		case *adapterv1.CheckpointServerMessage_ChunkReady:
			if err := stream.Send(&adapterv1.CheckpointClientMessage{
				Msg: &adapterv1.CheckpointClientMessage_Grant{Grant: &adapterv1.CheckpointGrant{
					Index:         m.ChunkReady.GetIndex(),
					Url:           "https://objectstore.example/chunk",
					ContentLength: m.ChunkReady.GetLength(),
				}},
			}); err != nil {
				t.Fatalf("send CheckpointGrant: %v", err)
			}
		case *adapterv1.CheckpointServerMessage_Summary:
			return
		case *adapterv1.CheckpointServerMessage_Failed:
			t.Fatalf("Checkpoint stream failed: %+v", m.Failed)
		}
	}
}

// spec: §10.1 lines 163-181 — the gateway-side CheckpointBarrier client
// drives the graceful-drain barrier against a fenced, matched-generation
// pod. The adapter quiesces and holds the barrier open, the gateway drives
// the Checkpoint stream against the held pod, and the ack the client maps
// back echoes the gateway-minted checkpoint_id that stream carried. A
// pre-quiesce-and-hold barrier that acked before any stream ran would
// return an empty checkpoint_ref; the current barrier blocks the ack until
// the stream terminates.
func TestCheckpointBarrierMapsAck_spec_10_1(t *testing.T) {
	srv, cl := barrierServer(t)
	ctx := context.Background()

	// Fence the session to generation 9 so the matched-generation barrier
	// passes the adapter's generation gate.
	if _, err := srv.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}

	type barrierResult struct {
		res adapterclient.CheckpointBarrierResult
		err error
	}
	resultCh := make(chan barrierResult, 1)
	go func() {
		res, err := cl.CheckpointBarrier(ctx, "s1", 9, "b-1")
		resultCh <- barrierResult{res, err}
	}()

	// The barrier holds quiescence open; once its gate is open the
	// gateway-driven Checkpoint stream links its minted checkpoint_id and,
	// on termination, releases the barrier.
	waitBarrierGateOpen(t, srv)
	driveBarrierCheckpointStream(t, cl, ctx, "gw-ckpt-b-1")

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("CheckpointBarrier: %v", got.err)
		}
		if got.res.BarrierID != "b-1" {
			t.Errorf("barrier id = %q, want b-1", got.res.BarrierID)
		}
		if got.res.CheckpointRef != "gw-ckpt-b-1" {
			t.Errorf("checkpoint ref = %q, want the echoed gateway checkpoint_id gw-ckpt-b-1", got.res.CheckpointRef)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CheckpointBarrier never returned after the Checkpoint stream terminated")
	}
}

// waitBarrierGateOpen spins until the adapter's CheckpointBarrier RPC has
// opened its quiesce-and-hold gate, so the caller can drive the Checkpoint
// stream and know the stream's CheckpointStart will link into the barrier.
func waitBarrierGateOpen(t *testing.T, srv *adapter.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.BarrierWaiting() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("CheckpointBarrier never opened its quiesce-and-hold gate")
}

// spec: §10.1 line 165 — a generation-stale barrier is rejected with
// FailedPrecondition so the gateway can record it as a false-positive
// without aborting the drain.
func TestCheckpointBarrierGenerationStale_spec_10_1(t *testing.T) {
	srv, cl := barrierServer(t)
	ctx := context.Background()
	if _, err := srv.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	_, err := cl.CheckpointBarrier(ctx, "s1", 3, "b-1")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition on stale barrier, got %v", err)
	}
}
