// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// barrierServer builds an adapter server with a started session and
// returns it alongside a client dialed to it. The session is claimed
// via the StartSession RPC so the server's checkSession gate passes.
func barrierServer(t *testing.T) (*adapter.Server, *adapterclient.Client) {
	t.Helper()
	srv := adapter.New("adapter-test-build")
	srv.WorkspaceRoot = t.TempDir()
	srv.ManifestDir = t.TempDir()
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

// spec: §10.1 lines 163-181 — the gateway-side CheckpointBarrier client
// drives the graceful-drain barrier against a fenced, matched-generation
// pod and maps the CheckpointBarrierAck back to the caller.
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

	res, err := cl.CheckpointBarrier(ctx, "s1", 9, "b-1")
	if err != nil {
		t.Fatalf("CheckpointBarrier: %v", err)
	}
	if res.BarrierID != "b-1" {
		t.Errorf("barrier id = %q, want b-1", res.BarrierID)
	}
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
