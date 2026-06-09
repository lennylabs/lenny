// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §10.1 lines 33-37 / §11.3 line 209 — the first CoordinatorFence
// on a pod's lifetime is accepted and records the generation, so the
// gateway-side wrapper maps the CoordinatorFenceResponse back to the
// caller.
func TestCoordinatorFenceFirstFenceAccepted_spec_10_1(t *testing.T) {
	_, cl := barrierServer(t)
	res, err := cl.CoordinatorFence(context.Background(), "s1", 7)
	if err != nil {
		t.Fatalf("CoordinatorFence: %v", err)
	}
	if !res.Accepted {
		t.Errorf("Accepted = false, want true on the first fence")
	}
	if res.LastFencedGeneration != 7 {
		t.Errorf("LastFencedGeneration = %d, want 7", res.LastFencedGeneration)
	}
	if res.GapDetected {
		t.Errorf("GapDetected = true, want false on the first fence")
	}
}

// spec: §10.1 line 165 / §11.3 line 209 — a fence whose generation is
// not strictly greater than the pod's last fenced value is rejected with
// FailedPrecondition so the coordinator records the generation-stale
// handoff and drives the retry/relinquish decision.
func TestCoordinatorFenceStaleRejected_spec_10_1(t *testing.T) {
	srv, cl := barrierServer(t)
	ctx := context.Background()
	if _, err := srv.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("seed fence: %v", err)
	}
	res, err := cl.CoordinatorFence(ctx, "s1", 4)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition on stale fence, got %v", err)
	}
	if res.Accepted {
		t.Errorf("Accepted = true, want false on a stale fence")
	}
}

// spec: §10.1 line 36 — a fence that skips one or more generations is
// accepted but flags the gap so the pod resets transient tool-call
// state; the wrapper surfaces GapDetected.
func TestCoordinatorFenceGapDetected_spec_10_1(t *testing.T) {
	srv, cl := barrierServer(t)
	ctx := context.Background()
	if _, err := srv.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 2,
	}); err != nil {
		t.Fatalf("seed fence: %v", err)
	}
	res, err := cl.CoordinatorFence(ctx, "s1", 5)
	if err != nil {
		t.Fatalf("CoordinatorFence: %v", err)
	}
	if !res.Accepted || !res.GapDetected {
		t.Errorf("Accepted=%v GapDetected=%v, want true/true on a generation gap", res.Accepted, res.GapDetected)
	}
}
