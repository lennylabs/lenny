// SPDX-License-Identifier: MIT

package bindrelease

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// recordingTerminator records every §11.4 pod-termination request a
// propagator applies to it. It stands in for the gateway's
// bindReleaseFanOut over the per-replica podsession.Registry.
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
	req := Request{TenantID: "acme", UserID: "alice@acme.com", SessionIDs: []string{"run_a", "run_b"}}
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

// primaryGatedTerminator is a peer replica's subscriber-side releaser
// as §11.4 step 2 defines it: it holds bindings, reads each named
// session's state from its primary row, releases only the bindings
// whose row is terminal, and returns the credential leases of the
// bindings it released.
type primaryGatedTerminator struct {
	mu       sync.Mutex
	primary  map[string]session.State // session id -> primary row state
	bindings map[string]bool          // session id -> binding held here
	leases   map[string]string        // session id -> credential lease held
	returned []string
}

func (g *primaryGatedTerminator) TerminateLocal(_ context.Context, req Request) Result {
	g.mu.Lock()
	defer g.mu.Unlock()
	var res Result
	for _, id := range req.SessionIDs {
		if !g.bindings[id] || !session.IsTerminal(g.primary[id]) {
			continue
		}
		delete(g.bindings, id)
		if lease, ok := g.leases[id]; ok {
			g.returned = append(g.returned, lease)
			delete(g.leases, id)
		}
		res.PodsTerminated++
	}
	return res
}

func (g *primaryGatedTerminator) snapshot() (bindings map[string]bool, leases map[string]string, returned []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	bindings, leases = map[string]bool{}, map[string]string{}
	for k, v := range g.bindings {
		bindings[k] = v
	}
	for k, v := range g.leases {
		leases[k] = v
	}
	return bindings, leases, append([]string(nil), g.returned...)
}

// TestSubscriberReleasesOnlyTerminalPrimaryBindings_spec_28_5_7 drives a
// CH-BINDRELEASE request from replica A through replica B's subscriber
// over a shared Redis. The request names a session whose primary row is
// terminal and one whose primary row is running, both bound on B. The
// propagator delivers the whole session set unfiltered, and B releases
// only the terminal session's binding, leaves the running session's
// binding and lease in place, and returns the released binding's
// credential lease.
//
// spec: §28.5.7 (Gateway-to-store), §11.4 (User Invalidation), §12.8
// (Compliance Interfaces)
func TestSubscriberReleasesOnlyTerminalPrimaryBindings_spec_28_5_7(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	localB := &primaryGatedTerminator{
		primary:  map[string]session.State{"run_done": session.StateFailed, "run_live": session.StateRunning},
		bindings: map[string]bool{"run_done": true, "run_live": true},
		leases:   map[string]string{"run_done": "lease_done", "run_live": "lease_live"},
	}
	recorder := &recordingTerminator{}
	propA := New(&recordingTerminator{}, pubsub.New(clientA), "replica-A")
	propB := New(teeTerminator{localB, recorder}, pubsub.New(clientB), "replica-B")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go propB.Run(ctx)

	req := Request{TenantID: "acme", UserID: "alice@acme.com", SessionIDs: []string{"run_done", "run_live"}}
	waitForCondition(t, 3*time.Second, func() bool {
		propA.Publish(ctx, req)
		return len(recorder.applied()) > 0
	})

	got := recorder.applied()[0]
	if len(got.SessionIDs) != 2 || got.TenantID != "acme" {
		t.Errorf("replica B's subscriber received %+v, want both sessions of acme", got)
	}
	bindings, leases, returned := localB.snapshot()
	if bindings["run_done"] {
		t.Error("the binding of the session whose primary row is terminal was not released")
	}
	if !bindings["run_live"] {
		t.Error("the binding of the session whose primary row is running was released")
	}
	if len(returned) != 1 || returned[0] != "lease_done" {
		t.Errorf("returned credential leases = %v, want [lease_done]", returned)
	}
	if leases["run_live"] != "lease_live" {
		t.Errorf("the running session's lease = %q, want lease_live kept", leases["run_live"])
	}
}

// teeTerminator applies a request to a releaser and records it.
type teeTerminator struct {
	release LocalTerminator
	record  *recordingTerminator
}

func (t teeTerminator) TerminateLocal(ctx context.Context, req Request) Result {
	res := t.release.TerminateLocal(ctx, req)
	t.record.TerminateLocal(ctx, req)
	return res
}

// TestRequestCarriesNoReason pins the §11.4 step-2 release request's JSON
// encoding to the tenant, the user, the origin, and the session set. The
// release path's own Shutdown carries its own cause, so the request carries
// no reason field for a peer to forward.
//
// spec: §11.4 (User Invalidation)
func TestRequestCarriesNoReason_spec_11_4(t *testing.T) {
	var fields map[string]any
	if err := json.Unmarshal(mustJSON(t, Request{
		TenantID: "acme", UserID: "alice@acme.com", Origin: "replica-A", SessionIDs: []string{"run_a"},
	}), &fields); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	want := map[string]bool{"tenantId": true, "userId": true, "origin": true, "sessionIds": true}
	if len(fields) != len(want) {
		t.Errorf("request fields = %v, want exactly %v", fields, want)
	}
	for k := range fields {
		if !want[k] {
			t.Errorf("request carries unexpected field %q", k)
		}
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
