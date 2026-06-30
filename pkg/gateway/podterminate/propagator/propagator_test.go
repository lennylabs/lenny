// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// recordingTerminator records every §11.4 pod-termination request a
// propagator applies to it. It stands in for the gateway's
// podTerminateFanOut over the per-replica podsession.Registry.
type recordingTerminator struct {
	mu       sync.Mutex
	requests []Request
}

func (r *recordingTerminator) TerminateLocal(_ context.Context, req Request) Result {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return Result{PodsTerminated: len(req.SessionIDs)}
}

func (r *recordingTerminator) applied() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.requests...)
}

// TestChannelIsDistinctFromRevocation pins the §11.4 termination channel
// to a value distinct from the §4.9/§13.3 revocation channel — a
// termination carries a session set and drives the pod adapter, not a
// deny list, so it does not share the revocation keyspace. spec: §11.4
// step 2.
func TestChannelIsDistinctFromRevocation_spec_11_4_step_2(t *testing.T) {
	if Channel != "lenny:session:terminate" {
		t.Errorf("Channel = %q, want lenny:session:terminate", Channel)
	}
	if Channel == "credential:denylist:events" {
		t.Error("§11.4 termination must not reuse the §4.9 revocation channel")
	}
}

// TestApplySkipsSelfOriginatedRequest asserts the publishing replica
// does not re-terminate its own pods when its own message round-trips:
// it already terminated them synchronously for the response counts.
// spec: §11.4 step 2.
func TestApplySkipsSelfOriginatedRequest_spec_11_4_step_2(t *testing.T) {
	local := &recordingTerminator{}
	p := New(local, nil, "replica-A")

	// A request this replica originated is skipped.
	p.apply(context.Background(), mustJSON(t, Request{Origin: "replica-A", SessionIDs: []string{"run_a"}}))
	if got := local.applied(); len(got) != 0 {
		t.Fatalf("self-originated request applied %d times, want 0", len(got))
	}

	// A peer's request is applied.
	p.apply(context.Background(), mustJSON(t, Request{Origin: "replica-B", SessionIDs: []string{"run_b"}}))
	if got := local.applied(); len(got) != 1 || got[0].SessionIDs[0] != "run_b" {
		t.Fatalf("peer request applied = %+v, want one request for run_b", got)
	}
}

// TestApplyIgnoresMalformedPayload asserts a payload that does not decode
// is dropped without terminating anything and without stalling the loop.
func TestApplyIgnoresMalformedPayload_spec_11_4_step_2(t *testing.T) {
	local := &recordingTerminator{}
	p := New(local, nil, "replica-A")
	p.apply(context.Background(), []byte("{not json"))
	if got := local.applied(); len(got) != 0 {
		t.Fatalf("malformed payload applied %d requests, want 0", len(got))
	}
}

// TestPublishNilBusIsNoop and the empty-session guard: a single-replica
// gateway (nil Bus) or a request with no sessions publishes nothing,
// terminates nothing, and never panics.
func TestPublishNilBusIsNoop_spec_11_4_step_2(t *testing.T) {
	p := New(&recordingTerminator{}, nil, "replica-A")
	p.Publish(context.Background(), Request{SessionIDs: []string{"run_a"}}) // nil bus
	bus := pubsub.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}))
	p2 := New(&recordingTerminator{}, bus, "replica-A")
	p2.Publish(context.Background(), Request{SessionIDs: nil}) // empty set
	// No assertions beyond "did not panic / publish": the nil bus and the
	// empty session set are the two no-op branches of Publish.
}

// TestRunNilBusBlocksUntilCancel confirms a nil-Bus propagator's Run
// blocks until the context is cancelled and applies nothing — the
// single-replica subscribe posture.
func TestRunNilBusBlocksUntilCancel_spec_11_4_step_2(t *testing.T) {
	local := &recordingTerminator{}
	p := New(local, nil, "replica-A")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run on a nil Bus did not return after the context was cancelled")
	}
	if got := local.applied(); len(got) != 0 {
		t.Fatalf("nil-Bus Run applied %d requests, want 0", len(got))
	}
}

// TestCrossReplicaFanOut is the §11.4 step-2 fix: a full_revoke on
// replica A reaches replica B's pods. Replica A publishes the
// termination request; replica B, subscribed over a shared Redis,
// terminates the pods it holds for the user's sessions. spec: §11.4
// step 2.
func TestCrossReplicaFanOut_spec_11_4_step_2(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	localB := &recordingTerminator{}
	propA := New(&recordingTerminator{}, pubsub.New(clientA), "replica-A")
	propB := New(localB, pubsub.New(clientB), "replica-B")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go propB.Run(ctx)

	// miniredis drops a message published before SUBSCRIBE registers, so
	// re-publish until replica B's terminator observes the request.
	req := Request{TenantID: "acme", UserID: "alice@acme.com", Reason: "USER_REVOKED", SessionIDs: []string{"run_a", "run_b"}}
	waitForCondition(t, 3*time.Second, func() bool {
		propA.Publish(ctx, req)
		return len(localB.applied()) > 0
	})

	got := localB.applied()
	if len(got) == 0 {
		t.Fatal("replica B never received the §11.4 termination request")
	}
	last := got[len(got)-1]
	if last.UserID != "alice@acme.com" || last.TenantID != "acme" || last.Origin != "replica-A" {
		t.Errorf("replica B applied request = %+v, want origin replica-A for acme/alice", last)
	}
	if len(last.SessionIDs) != 2 {
		t.Errorf("replica B applied %d sessions, want 2", len(last.SessionIDs))
	}
}

func mustJSON(t *testing.T, req Request) []byte {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
