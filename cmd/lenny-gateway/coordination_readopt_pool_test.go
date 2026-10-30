// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// readoptPublishOnce runs readoptAndFence with an accepting fencer over a live
// adapter, invokes the publish callback, and returns the one binding it put.
func readoptPublishOnce(t *testing.T, row sessionstore.Session, pools readoptPoolResolver) *podsession.BindResult {
	t.Helper()
	pub := &recordingPublisher{}
	seams := readoptSeams{
		dialer:   &fakeReadoptDialer{podIP: "10.0.0.9", adapter: dialSeamsAdapter(t)},
		fencer:   &fakeReadoptFencer{},
		registry: pub,
		sessions: fakeSandboxReader{row: row},
		releaser: &recordingReleaser{},
		pools:    pools,
	}
	publish, err := readoptAndFence(context.Background(), seams, row.TenantID, row.ID, "replica-A", 4)
	if err != nil {
		t.Fatalf("readoptAndFence: %v", err)
	}
	publish()
	if len(pub.published) != 1 {
		t.Fatalf("published %d bindings, want 1", len(pub.published))
	}
	return pub.published[0]
}

// spec: 5.2 (Pool Configuration and Execution Modes), 10.1.2 (Coordinator
// Handoff Protocol)
// A takeover on a concurrent-workspace pool publishes a slot binding keyed by
// the session ID, so the release after the takeover frees one slot through
// Binder.ReleaseSlot rather than tearing down the shared pod as an exclusive
// release would. Before the fix the readopt published no slot ID.
func TestReadoptPublishesSlotBindingOnConcurrentPool_spec_5_2(t *testing.T) {
	row := sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1", PoolRef: "conc-pool"}
	got := readoptPublishOnce(t, row, fakePoolResolver{match: podsession.PoolMatch{Pool: "conc-pool", MaxConcurrentSessions: 4, Recycle: true}})
	if got.SlotID != "s1" {
		t.Fatalf("SlotID = %q, want the session ID on a concurrent-workspace pool", got.SlotID)
	}
	if !got.Recycle {
		t.Error("Recycle = false, want the pool's recycle disposition")
	}
	if got.CoordinationGeneration != 4 {
		t.Errorf("CoordinationGeneration = %d, want the handoff generation 4", got.CoordinationGeneration)
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes), 10.1.2 (Coordinator
// Handoff Protocol)
// A takeover on an exclusive recycling pool publishes a binding with no slot
// that carries the recycle disposition and the whole-pod scrub parameters, so
// the release recycles the pod with its cleanup commands rather than
// retiring it.
func TestReadoptPublishesRecycleBindingWithoutSlotOnExclusivePool_spec_5_2(t *testing.T) {
	row := sessionstore.Session{ID: "s2", TenantID: "acme", PodAssignment: "sbx-2", PoolRef: "excl-pool"}
	match := podsession.PoolMatch{
		Pool: "excl-pool", MaxConcurrentSessions: 1, Recycle: true,
		CleanupCommands: []string{"rm -rf /tmp/work"}, CleanupTimeoutSeconds: 30,
	}
	got := readoptPublishOnce(t, row, fakePoolResolver{match: match})
	if got.SlotID != "" {
		t.Fatalf("SlotID = %q, want empty on an exclusive pool", got.SlotID)
	}
	if !got.Recycle || got.CleanupTimeoutSeconds != 30 || len(got.CleanupCommands) != 1 || got.CleanupCommands[0] != "rm -rf /tmp/work" {
		t.Fatalf("binding recycle fields = (%v, %v, %d), want the pool's recycle disposition and scrub parameters",
			got.Recycle, got.CleanupCommands, got.CleanupTimeoutSeconds)
	}
	if got.SandboxName != "sbx-2" || got.PodIP != "10.0.0.9" {
		t.Errorf("binding pod = (%s, %s), want sbx-2 at 10.0.0.9", got.SandboxName, got.PodIP)
	}
}

// spec: 10.1.2 (Coordinator Handoff Protocol), 5.2 (Pool Configuration and
// Execution Modes)
// A pool-resolve error before the dial publishes nothing, dials nothing, and
// releases the lease the takeover acquired, so its lapse surfaces for a later
// re-adopt.
func TestReadoptResolveErrorPublishesNothingAndReleasesLease_spec_10_1_2(t *testing.T) {
	dialer := &fakeReadoptDialer{podIP: "10.0.0.9"}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	seams := readoptSeams{
		dialer:   dialer,
		fencer:   &fakeReadoptFencer{},
		registry: pub,
		sessions: fakeSandboxReader{row: sessionstore.Session{ID: "s3", TenantID: "acme", PodAssignment: "sbx-3", PoolRef: "gone"}},
		releaser: rel,
		pools:    fakePoolResolver{err: podsession.ErrNoMatchingPool},
	}
	publish, err := readoptAndFence(context.Background(), seams, "acme", "s3", "replica-A", 4)
	if !errors.Is(err, podsession.ErrNoMatchingPool) {
		t.Fatalf("err = %v, want the wrapped resolve error", err)
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback after a resolve error")
	}
	if dialer.dialAttempts != 0 {
		t.Fatal("the pod was dialed after the pool failed to resolve")
	}
	if len(pub.published) != 0 {
		t.Fatal("a binding was published after the pool failed to resolve")
	}
	if len(rel.released) != 1 || rel.released[0] != (releaseCall{"acme", "s3", "replica-A"}) {
		t.Fatalf("lease releases = %v, want one release of acme/s3 held by replica-A", rel.released)
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes), 10.1.2 (Coordinator
// Handoff Protocol)
// The production resolver pins resolution to the pool the gateway placed the
// session on (PoolRef) and ignores the client pin (Pool). Two pools back the
// runtime here, so an unpinned resolve is ambiguous, and the client pin names
// the pool the session does not run on.
func TestPoolResolverPinsPlacedPoolRef_spec_5_2(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	const ns = "lenny-agents"
	objs := []*lennyv1.SandboxTemplate{
		{ObjectMeta: metav1.ObjectMeta{Name: "tmpl-a", Namespace: ns}, Spec: lennyv1.SandboxTemplateSpec{RuntimeRef: "rt"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "tmpl-b", Namespace: ns}, Spec: lennyv1.SandboxTemplateSpec{RuntimeRef: "rt"}},
	}
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	builder = builder.WithObjects(
		&lennyv1.SandboxWarmPool{ObjectMeta: metav1.ObjectMeta{Name: "pool-a", Namespace: ns}, Spec: lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-a"}},
		&lennyv1.SandboxWarmPool{ObjectMeta: metav1.ObjectMeta{Name: "pool-b", Namespace: ns}, Spec: lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-b"}},
	)
	r := poolResolver{reader: builder.Build(), namespace: ns}

	match, err := r.ResolveReadoptPool(context.Background(), sessionstore.Session{ID: "s4", RuntimeRef: "rt", Pool: "pool-a", PoolRef: "pool-b"})
	if err != nil {
		t.Fatalf("ResolveReadoptPool: %v", err)
	}
	if match.Pool != "pool-b" {
		t.Fatalf("resolved pool = %q, want the placed pool pool-b rather than the client pin pool-a", match.Pool)
	}
}

// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination)
// BindingGeneration reports 0 for a nil registry and an unbound session, and
// otherwise the stamp of the current binding, following a re-put at a new
// generation.
func TestCoordinationBindingsBindingGeneration_spec_10_1_5(t *testing.T) {
	if got := (coordinationBindings{w: &gatewayWiring{}}).BindingGeneration("s1"); got != 0 {
		t.Fatalf("nil registry BindingGeneration = %d, want 0", got)
	}
	reg := podsession.NewRegistry()
	b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}
	if got := b.BindingGeneration("s1"); got != 0 {
		t.Fatalf("unbound BindingGeneration = %d, want 0", got)
	}
	reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", CoordinationGeneration: 3})
	if got := b.BindingGeneration("s1"); got != 3 {
		t.Fatalf("BindingGeneration = %d, want the stamp 3", got)
	}
	reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", CoordinationGeneration: 5})
	if got := b.BindingGeneration("s1"); got != 5 {
		t.Fatalf("BindingGeneration after a re-put = %d, want the new stamp 5", got)
	}
	reg.Remove("s1")
	if got := b.BindingGeneration("s1"); got != 0 {
		t.Fatalf("BindingGeneration after removal = %d, want 0", got)
	}
}
