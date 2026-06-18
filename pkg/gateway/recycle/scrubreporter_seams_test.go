// SPDX-License-Identifier: MIT

package recycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/agentpodstate/memstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

const testNS = "lenny-agents"

// fakePoolReader resolves pools from an in-memory map for the inspector
// tests. A pool absent from the map reports poolstore.ErrNotFound.
type fakePoolReader struct{ pools map[string]poolstore.Pool }

func (f fakePoolReader) Get(_ context.Context, name string) (poolstore.Pool, error) {
	p, ok := f.pools[name]
	if !ok {
		return poolstore.Pool{}, poolstore.ErrNotFound
	}
	return p, nil
}

// fakeRuntimeReader resolves runtimes from an in-memory map. A runtime
// absent from the map reports runtimestore.ErrNotFound.
type fakeRuntimeReader struct {
	runtimes map[string]runtimestore.Runtime
}

func (f fakeRuntimeReader) Get(_ context.Context, name string) (runtimestore.Runtime, error) {
	r, ok := f.runtimes[name]
	if !ok {
		return runtimestore.Runtime{}, runtimestore.ErrNotFound
	}
	return r, nil
}

// recyclingPool builds a session-mode pool with recycle.enabled and the
// given recycle knobs, mapped to a runtime carrying the preConnect flag.
func recyclingPool(name, runtimeRef string, profile isolation.Profile, r *runtimestore.RecyclePolicy) poolstore.Pool {
	return poolstore.Pool{
		Name:             name,
		RuntimeRef:       runtimeRef,
		IsolationProfile: profile,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		SessionPolicy:    &runtimestore.SessionPolicy{Recycle: r},
	}
}

// agentPod builds an agent Pod with the pool and host-schedulable labels
// the inspector reads, created at createdAt for the uptime computation.
func agentPod(name, pool, hostSchedulable string, createdAt time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels: map[string]string{
				warmpool.LabelPool:            pool,
				warmpool.LabelHostSchedulable: hostSchedulable,
			},
			CreationTimestamp: metav1.Time{Time: createdAt},
		},
	}
}

// recycleScheme registers lenny.dev/v1alpha1 and corev1 so the fake client can
// read the SandboxClaim the inspector reads at the recycle boundary alongside
// the agent Pod.
func recycleScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lennyv1: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	return s
}

// recyclingClaim builds the per-pod SandboxClaim the inspector reads to
// confirm the claim still exists at the recycle boundary. Its presence keeps
// InspectForRecycle on the found=true path; deleting or omitting it is the
// §4.6.1 gone-claim race.
func recyclingClaim(podID string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(podID), Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: podID, TenantID: "acme"},
	}
}

// inspectorClient builds a fake client over the lenny.dev scheme seeded with
// the supplied objects, the common construction the inspector unit tests share
// now that InspectForRecycle reads the SandboxClaim.
func inspectorClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(recycleScheme(t)).WithObjects(objs...).Build()
}

// TestRecycleCounterStoreUnpacksAgentPodState verifies the agent_pod_state
// adapter maps the RecycleCounters struct onto the leasecontrol tuple and
// preserves the found flag and increment values.
// spec: 4.7 (sessionsServed / scrubFailureCount increments), 5.2 (recycle disposition)
//
// diagnosis: a failure means the gateway's recycle-counter seam mis-maps the
// agent_pod_state row onto the disposition inputs, so the recycle session
// and scrub-failure caps evaluate against wrong counts.
func TestRecycleCounterStoreUnpacksAgentPodState_spec_4_7(t *testing.T) {
	store := memstore.New(func() time.Time { return time.Unix(0, 0) })
	store.Put(agentpodstate.PodState{PodID: "pod-1", PoolID: "agents"})
	seam := recycle.NewRecycleCounterStore(store)

	got, found, err := seam.IncrementSessionsServed(context.Background(), "pod-1")
	if err != nil || !found || got != 1 {
		t.Fatalf("IncrementSessionsServed = (%d,%v,%v), want (1,true,nil)", got, found, err)
	}
	got, found, err = seam.IncrementScrubFailureCount(context.Background(), "pod-1")
	if err != nil || !found || got != 1 {
		t.Fatalf("IncrementScrubFailureCount = (%d,%v,%v), want (1,true,nil)", got, found, err)
	}
	served, scrub, found, err := seam.RecycleCounters(context.Background(), "pod-1")
	if err != nil || !found || served != 1 || scrub != 1 {
		t.Fatalf("RecycleCounters = (%d,%d,%v,%v), want (1,1,true,nil)", served, scrub, found, err)
	}
}

// TestRecycleCounterStoreMissingPod verifies a pod absent from the mirror
// reports found=false rather than fabricating a zero row.
// spec: 4.7 (ReportSessionScrub fails closed on an unknown pod)
//
// diagnosis: a failure means a scrub report for an unknown pod is mapped to a
// fabricated zero count instead of the fail-closed not-found signal the
// ScrubReporter keys ErrPodNotInMirror on.
func TestRecycleCounterStoreMissingPod_spec_4_7(t *testing.T) {
	store := memstore.New(func() time.Time { return time.Unix(0, 0) })
	seam := recycle.NewRecycleCounterStore(store)
	if _, _, found, err := seam.RecycleCounters(context.Background(), "ghost"); err != nil || found {
		t.Fatalf("RecycleCounters(ghost) found=%v err=%v, want found=false nil", found, err)
	}
}

// TestInspectForRecyclePreConnectRecyclingPool verifies the inspector
// resolves the recycle policy, preConnect flag, host-schedulable label,
// runtime_class, and pod uptime for a recycling preConnect pool.
// spec: 5.2 (recycle policy resolution), 6.2 (preConnect), 6.39 (host-node schedulability), 16.1 (pool/runtime_class labels)
//
// diagnosis: a failure means the gateway resolves the wrong recycle policy or
// pod facts at the recycle boundary, so the disposition reuses or retires a
// pod against an incorrect policy or mislabels its metrics.
func TestInspectForRecyclePreConnectRecyclingPool_spec_5_2(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	created := now.Add(-90 * time.Second)
	cl := inspectorClient(t, agentPod("pod-1", "agents", "true", created), recyclingClaim("pod-1"))
	pools := fakePoolReader{pools: map[string]poolstore.Pool{
		"agents": recyclingPool("agents", "rt", isolation.ProfileSandboxed, &runtimestore.RecyclePolicy{
			Enabled: true, OnScrubFailure: runtimestore.CleanupFailureWarn,
			MaxScrubFailures: 3, MaxSessionsPerPod: 50, MaxPodUptimeSeconds: 3600,
		}),
	}}
	runtimes := fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{
		"rt": {Name: "rt", Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: true}},
	}}
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client: cl, Namespace: testNS, Pools: pools, Runtimes: runtimes,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || !found {
		t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if !policy.PreConnect {
		t.Error("PreConnect = false, want true")
	}
	if policy.OnScrubFailure != podscrub.OnCleanupWarn {
		t.Errorf("OnScrubFailure = %q, want warn", policy.OnScrubFailure)
	}
	if policy.MaxScrubFailures != 3 || policy.MaxSessionsPerPod != 50 || policy.MaxPodUptimeSeconds != 3600 {
		t.Errorf("limits = %+v, want 3/50/3600", policy)
	}
	if !policy.HostSchedulable {
		t.Error("HostSchedulable = false, want true (label reads \"true\")")
	}
	if policy.PodUptimeSeconds != 90 {
		t.Errorf("PodUptimeSeconds = %d, want 90", policy.PodUptimeSeconds)
	}
	if policy.Pool != "agents" || policy.RuntimeClass != "gvisor" {
		t.Errorf("pool/runtime_class = %q/%q, want agents/gvisor", policy.Pool, policy.RuntimeClass)
	}
}

// TestInspectForRecycleRuntimeDefaultProfile verifies the §16.1 runtime_class
// label falls back to the pool runtime's default §5.3 profile when the pool
// carries no IsolationProfile override. A recycling pool with an empty
// IsolationProfile mapped to a microvm-default runtime resolves to the kata
// runtime_class rather than the empty/standard label.
// spec: 5.3 (pool profile overrides the runtime default), 16.1 (runtime_class label)
//
// diagnosis: a failure means a pool relying on the runtime's default isolation
// profile carries the wrong runtime_class on the recycle metrics, so the §16.1
// scrub-failure and retirement series are mislabeled (or empty) for pools
// without an explicit profile override.
func TestInspectForRecycleRuntimeDefaultProfile_spec_16_1(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	pool := recyclingPool("agents", "rt", "", &runtimestore.RecyclePolicy{
		Enabled: true, MaxSessionsPerPod: 50,
	})
	cl := inspectorClient(t, agentPod("pod-1", "agents", "true", now), recyclingClaim("pod-1"))
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client: cl, Namespace: testNS,
		Pools: fakePoolReader{pools: map[string]poolstore.Pool{"agents": pool}},
		Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{
			"rt": {Name: "rt", IsolationProfile: isolation.ProfileMicrovm},
		}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || !found {
		t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if policy.RuntimeClass != "kata" {
		t.Errorf("RuntimeClass = %q, want kata (runtime default microvm profile)", policy.RuntimeClass)
	}
}

// TestInspectForRecyclePoolProfileOverridesRuntimeDefault verifies the pool's
// IsolationProfile override wins over the runtime default when both are set,
// so the §16.1 runtime_class label reflects the pool's effective profile.
// spec: 5.3 (pool profile overrides the runtime default), 16.1 (runtime_class label)
//
// diagnosis: a failure means the inspector ignores a pool's explicit isolation
// override and labels the recycle metrics with the runtime default, so an
// operator's per-pool profile choice is not reflected in the §16.1 series.
func TestInspectForRecyclePoolProfileOverridesRuntimeDefault_spec_16_1(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	pool := recyclingPool("agents", "rt", isolation.ProfileSandboxed, &runtimestore.RecyclePolicy{
		Enabled: true, MaxSessionsPerPod: 50,
	})
	cl := inspectorClient(t, agentPod("pod-1", "agents", "true", now), recyclingClaim("pod-1"))
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client: cl, Namespace: testNS,
		Pools: fakePoolReader{pools: map[string]poolstore.Pool{"agents": pool}},
		Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{
			"rt": {Name: "rt", IsolationProfile: isolation.ProfileMicrovm},
		}},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || !found {
		t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if policy.RuntimeClass != "gvisor" {
		t.Errorf("RuntimeClass = %q, want gvisor (pool override wins over runtime default)", policy.RuntimeClass)
	}
}

// TestInspectForRecycleAbsentHostScheduleLabelFailsSafe verifies a missing
// lenny.dev/host-schedulable label resolves to unschedulable, the §6.39
// fail-safe so a cordoned-or-unknown host retires rather than reuses.
// spec: 6.39 (absent label treated as unschedulable)
//
// diagnosis: a failure means an agent pod whose host-schedulable label has not
// been written yet is treated as schedulable, so a pod on a cordoned node is
// reused and the next session is handed a soon-to-be-evicted pod.
func TestInspectForRecycleAbsentHostScheduleLabelFailsSafe_spec_6_39(t *testing.T) {
	pod := agentPod("pod-1", "agents", "", time.Unix(0, 0))
	delete(pod.Labels, warmpool.LabelHostSchedulable)
	cl := inspectorClient(t, pod, recyclingClaim("pod-1"))
	insp := mustInspector(t, cl, map[string]poolstore.Pool{
		"agents": recyclingPool("agents", "rt", isolation.ProfileStandard, &runtimestore.RecyclePolicy{
			Enabled: true, MaxSessionsPerPod: 50,
		}),
	}, map[string]runtimestore.Runtime{"rt": {Name: "rt"}})
	policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || !found {
		t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if policy.HostSchedulable {
		t.Error("HostSchedulable = true for an absent label, want false (fail-safe)")
	}
}

// TestInspectForRecycleMissingPodIsNoOp verifies a whole-pod scrub for a pod
// the apiserver no longer knows reports found=false so the disposition skips.
// spec: 3.4 (recycle disposition; nothing to recycle for a gone pod)
//
// diagnosis: a failure means a scrub report racing a concurrent retirement
// errors instead of cleanly skipping, surfacing a transient Internal to the
// adapter for a pod that no longer exists.
func TestInspectForRecycleMissingPodIsNoOp_spec_3_4(t *testing.T) {
	cl := fake.NewClientBuilder().Build()
	insp := mustInspector(t, cl, nil, nil)
	_, found, err := insp.InspectForRecycle(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("InspectForRecycle(ghost) err = %v, want nil", err)
	}
	if found {
		t.Error("found = true for a missing pod, want false")
	}
}

// TestInspectForRecycleClaimGonePodPresentIsNoOp verifies the inspector reports
// found=false when the SandboxClaim has been concurrently reclaimed (a racing
// hold-expiry DELETE or the §4.6.1 orphan GC) while the agent Pod object
// survives, so the disposition is skipped before any counter advances.
// spec: 3.4 (skip when the claim is gone), 4.6.1 (orphan GC reclaiming a recycling claim), 4.7 (concurrent-retirement no-op)
//
// diagnosis: a failure means the inspector advances counters and runs the
// disposition against a claim that a hold-expiry DELETE or the orphan GC already
// reclaimed, the opposite of the §4.7 skip the contract promises.
func TestInspectForRecycleClaimGonePodPresentIsNoOp_spec_4_7(t *testing.T) {
	// The Pod survives but no SandboxClaim is seeded: the gone-claim race.
	cl := inspectorClient(t, agentPod("pod-1", "agents", "true", time.Unix(0, 0)))
	insp := mustInspector(t, cl, map[string]poolstore.Pool{
		"agents": recyclingPool("agents", "rt", isolation.ProfileStandard, &runtimestore.RecyclePolicy{
			Enabled: true, MaxSessionsPerPod: 50,
		}),
	}, map[string]runtimestore.Runtime{"rt": {Name: "rt"}})
	_, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil {
		t.Fatalf("InspectForRecycle with gone claim: err = %v, want nil", err)
	}
	if found {
		t.Error("found = true with a gone claim, want false (skip)")
	}
}

// TestInspectForRecycleNonRecyclingPoolIsNoOp verifies a whole-pod scrub for
// a pod in a non-recycling pool (no recycle block) reports found=false: the
// pod is one-session-per-pod and is not a recycle candidate.
// spec: 5.2 (recycle disposition applies only to recycling pools)
//
// diagnosis: a failure means a one-session-per-pod pod is run through the
// recycle disposition, reserving or re-warming a pod the pool model retires on
// session end.
func TestInspectForRecycleNonRecyclingPoolIsNoOp_spec_5_2(t *testing.T) {
	cl := inspectorClient(t, agentPod("pod-1", "agents", "true", time.Unix(0, 0)), recyclingClaim("pod-1"))
	insp := mustInspector(t, cl, map[string]poolstore.Pool{
		"agents": {Name: "agents", RuntimeRef: "rt", ExecutionMode: runtimestore.ExecutionModeSession},
	}, map[string]runtimestore.Runtime{"rt": {Name: "rt"}})
	_, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
	if err != nil || found {
		t.Fatalf("InspectForRecycle non-recycling = (found=%v, err=%v), want (false, nil)", found, err)
	}
}

// TestInspectForRecycleNoPoolLabelFailsClosed verifies a managed pod with no
// lenny.dev/pool label fails closed rather than recycling against a guessed
// policy.
// spec: 5.2 (recycle policy resolution keyed on the pool)
//
// diagnosis: a failure means a pod with no resolvable pool is recycled against
// a default or empty policy, risking reuse of a pod whose recycle caps cannot
// be evaluated.
func TestInspectForRecycleNoPoolLabelFailsClosed_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "", "true", time.Unix(0, 0))
	delete(pod.Labels, warmpool.LabelPool)
	cl := inspectorClient(t, pod, recyclingClaim("pod-1"))
	insp := mustInspector(t, cl, nil, nil)
	if _, found, err := insp.InspectForRecycle(context.Background(), "pod-1"); err == nil || found {
		t.Fatalf("no pool label = (found=%v, err=%v), want (false, non-nil)", found, err)
	}
}

// TestInspectForRecyclePodGetErrorPropagates verifies a transient apiserver
// read failure surfaces as an error rather than a silent found=false skip.
// spec: 4.7 (ReportPodScrub)
//
// diagnosis: a failure means a transient Pods get fault is swallowed as a
// no-op, so a pod that should have been recycled is silently stranded in
// `recycling` until the orphan GC reclaims it.
func TestInspectForRecyclePodGetErrorPropagates_spec_4_7(t *testing.T) {
	base := fake.NewClientBuilder().Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("apiserver unreachable")
		},
	})
	insp := mustInspectorClient(t, cl, nil, nil)
	if _, found, err := insp.InspectForRecycle(context.Background(), "pod-1"); err == nil || found {
		t.Fatalf("get error = (found=%v, err=%v), want (false, non-nil)", found, err)
	}
}

// concurrentPool builds a recycling session-mode pool whose
// sessionPolicy.maxConcurrentSessions is the §5.2 "Concurrent" preset bound,
// the denominator of the ceil(maxConcurrentSessions/2) unhealthy threshold
// the drain ledger resolves per pod.
func concurrentPool(name string, maxConcurrent int) poolstore.Pool {
	return poolstore.Pool{
		Name:          name,
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: maxConcurrent,
			Recycle:               &runtimestore.RecyclePolicy{Enabled: true, MaxSessionsPerPod: 50},
		},
	}
}

// recordNLeaks records n leaked session-scrub outcomes against podID through
// the ledger, failing the test on any error.
func recordNLeaks(t *testing.T, ledger leasecontrol.DrainLedger, podID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := ledger.RecordLeak(context.Background(), podID); err != nil {
			t.Fatalf("RecordLeak %d: %v", i, err)
		}
	}
}

// drainRequested reports whether the agent Pod carries the
// lenny.dev/drain-request annotation the ledger stamps at the threshold.
func drainRequested(t *testing.T, cl client.Client, podID string) bool {
	t.Helper()
	var got corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podID}, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	return got.Annotations[lennyv1.AnnotationDrainRequest] != ""
}

// mustDrainLedger builds a drain ledger over the supplied client and pool
// fixtures, failing the test on a construction error.
func mustDrainLedger(t *testing.T, cl client.Client, pools map[string]poolstore.Pool) leasecontrol.DrainLedger {
	t.Helper()
	ledger, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{
		Tracker:   slothealth.New(),
		Client:    cl,
		Namespace: testNS,
		Pools:     fakePoolReader{pools: pools},
		Now:       func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewDrainLedger: %v", err)
	}
	return ledger
}

// TestDrainLedgerStampsOnUnhealthyThreshold verifies the ledger stamps
// lenny.dev/drain-request on the agent Pod once the pod crosses the §5.2
// unhealthy threshold, and not before. A single-session recycling pool
// (maxConcurrentSessions 1) crosses on the first leak.
// spec: 4.7 (leaked feeds the drain ledger), 4.6.3 (gateway stamps drain-request), 5.2 (unhealthy threshold)
//
// diagnosis: a failure means a leaked session does not drive the drain-request
// annotation at the unhealthy threshold, so the WarmPoolController never
// drains the unhealthy pod, or the ledger stamps prematurely and drains a
// healthy pod.
func TestDrainLedgerStampsOnUnhealthyThreshold_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger := mustDrainLedger(t, cl, map[string]poolstore.Pool{"agents": concurrentPool("agents", 1)})
	recordNLeaks(t, ledger, "pod-1", 1)
	if !drainRequested(t, cl, "pod-1") {
		t.Error("drain-request annotation not stamped after the unhealthy threshold was crossed")
	}
}

// TestDrainLedgerBelowThresholdDoesNotStamp verifies a single leak on a
// recycling concurrent-session pool (the §5.2 "Concurrent" preset,
// maxConcurrentSessions 4, threshold ceil(4/2)=2) does not yet drain the
// pod: the ledger resolves the threshold against the pod's pool rather than
// a fixed bound, so a recycling concurrent pool is not retired on its first
// leaked slot.
// spec: 5.2 (ceil(maxConcurrentSessions/2) unhealthy threshold), 3.4 (recycle disposition)
//
// diagnosis: a failure means a single leaked slot on a recycling
// concurrent-session pod drains the whole pod before the
// ceil(maxConcurrentSessions/2) threshold is reached, retiring a still-healthy
// pod (the hard-wired maxConcurrent=1 regression).
func TestDrainLedgerBelowThresholdDoesNotStamp_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger := mustDrainLedger(t, cl, map[string]poolstore.Pool{"agents": concurrentPool("agents", 4)})
	recordNLeaks(t, ledger, "pod-1", 1)
	if drainRequested(t, cl, "pod-1") {
		t.Error("drain-request stamped below the ceil(maxConcurrentSessions/2) threshold")
	}
}

// TestDrainLedgerStampsAtConcurrentThreshold verifies a recycling
// concurrent-session pool (maxConcurrentSessions 8, threshold ceil(8/2)=4)
// drains only once the pod accumulates four failed-or-leaked slots in the
// window, not on the first leak. This pins the per-pod threshold resolution
// the wiring regression broke by hard-wiring maxConcurrent=1.
// spec: 5.2 (ceil(maxConcurrentSessions/2) unhealthy threshold), 4.7 (leaked feeds the ledger)
//
// diagnosis: a failure means the drain ledger resolves the wrong
// maxConcurrentSessions for a recycling concurrent-session pool, draining
// it either prematurely (a fixed-1 threshold) or never (a too-high
// threshold), so the §5.2 whole-pod replacement trigger fires at the wrong
// leak count.
func TestDrainLedgerStampsAtConcurrentThreshold_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger := mustDrainLedger(t, cl, map[string]poolstore.Pool{"agents": concurrentPool("agents", 8)})
	recordNLeaks(t, ledger, "pod-1", 3)
	if drainRequested(t, cl, "pod-1") {
		t.Fatal("drain-request stamped at 3 leaks, below the ceil(8/2)=4 threshold")
	}
	recordNLeaks(t, ledger, "pod-1", 1)
	if !drainRequested(t, cl, "pod-1") {
		t.Error("drain-request not stamped at 4 leaks, the ceil(8/2) threshold")
	}
}

// TestDrainLedgerUnsetMaxConcurrentDefaultsToOne verifies a recycling pool
// whose SessionPolicy leaves maxConcurrentSessions unset (the common
// single-session recycling case) resolves to the §5.2 default bound of 1 and
// drains on the first leak.
// spec: 5.2 (default maxConcurrentSessions 1 when unset), 4.7 (leaked feeds the ledger)
//
// diagnosis: a failure means a single-session recycling pod with an unset
// maxConcurrentSessions is treated as having a zero or negative threshold, so a
// leaked single-session pod is never drained.
func TestDrainLedgerUnsetMaxConcurrentDefaultsToOne_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	pool := poolstore.Pool{
		Name:          "agents",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{Enabled: true, MaxSessionsPerPod: 50},
		},
	}
	ledger := mustDrainLedger(t, cl, map[string]poolstore.Pool{"agents": pool})
	recordNLeaks(t, ledger, "pod-1", 1)
	if !drainRequested(t, cl, "pod-1") {
		t.Error("drain-request not stamped on the first leak with an unset maxConcurrentSessions (default 1)")
	}
}

// TestDrainLedgerNoPoolLabelFailsClosed verifies a leak on a pod with no
// lenny.dev/pool label surfaces an error rather than silently dropping the
// leak: an unresolvable pod must not bypass the unhealthy-threshold ledger.
// spec: 5.2 (recycle policy keyed on the pool), 4.7 (leaked feeds the ledger)
//
// diagnosis: a failure means a leaked session on a pod whose pool cannot be
// resolved is silently swallowed, so a degraded pod accumulating leaks is
// never drained.
func TestDrainLedgerNoPoolLabelFailsClosed_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "", "true", time.Unix(0, 0))
	delete(pod.Labels, warmpool.LabelPool)
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger := mustDrainLedger(t, cl, nil)
	if err := ledger.RecordLeak(context.Background(), "pod-1"); err == nil {
		t.Error("RecordLeak on a pod with no pool label: err = nil, want non-nil")
	}
}

// TestDrainLedgerMissingPodFallsBackToDefaultBound verifies a leak on a pod
// the apiserver no longer knows resolves to the §5.2 default
// maxConcurrentSessions of 1 and crosses the threshold without erroring: a
// concurrent retirement that removed the pod must not error the leak path nor
// re-stamp a drain on a gone pod (StampDrainRequest tolerates NotFound).
// spec: 3.4 (pod gone, nothing to drain), 5.2 (default maxConcurrentSessions 1)
//
// diagnosis: a failure means a leaked session racing a pod deletion errors the
// scrub-report RPC instead of cleanly resolving the default bound, surfacing a
// transient Internal to the adapter for a pod that no longer exists.
func TestDrainLedgerMissingPodFallsBackToDefaultBound_spec_3_4(t *testing.T) {
	cl := fake.NewClientBuilder().Build()
	ledger := mustDrainLedger(t, cl, nil)
	if err := ledger.RecordLeak(context.Background(), "ghost"); err != nil {
		t.Fatalf("RecordLeak on a missing pod: %v", err)
	}
}

// TestDrainLedgerMissingPoolFallsBackToDefaultBound verifies a leak on a pod
// whose pool was deleted resolves to the §5.2 default maxConcurrentSessions of
// 1 and stamps the drain on the first leak rather than erroring: a pool
// deleted with its pods leaves the threshold well-defined.
// spec: 3.4 (pool gone), 5.2 (default maxConcurrentSessions 1)
//
// diagnosis: a failure means a leaked session on a pod whose pool was deleted
// errors the scrub-report RPC instead of draining the orphaned pod, so a
// degraded pod in a torn-down pool is never drained.
func TestDrainLedgerMissingPoolFallsBackToDefaultBound_spec_3_4(t *testing.T) {
	pod := agentPod("pod-1", "deleted-pool", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger := mustDrainLedger(t, cl, nil) // no pools => ErrNotFound on resolve
	if err := ledger.RecordLeak(context.Background(), "pod-1"); err != nil {
		t.Fatalf("RecordLeak with a deleted pool: %v", err)
	}
	if !drainRequested(t, cl, "pod-1") {
		t.Error("drain-request not stamped on the first leak under the default bound (deleted pool)")
	}
}

// TestNewSeamsRequireDeps verifies each seam constructor fails closed when a
// required dependency is nil rather than panicking on the request path.
// spec: 4.7 (ReportSessionScrub/ReportPodScrub)
//
// diagnosis: a failure means a misconfigured gateway builds a recycle seam
// with a nil client, store, or namespace and panics on the first scrub report
// instead of failing at construction.
func TestNewSeamsRequireDeps_spec_4_7(t *testing.T) {
	cl := fake.NewClientBuilder().Build()
	tracker := slothealth.New()
	pools := fakePoolReader{}
	runtimes := fakeRuntimeReader{}

	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Client: cl, Namespace: testNS, Pools: pools}); err == nil {
		t.Error("NewDrainLedger with nil Tracker: err = nil, want non-nil")
	}
	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Tracker: tracker, Namespace: testNS, Pools: pools}); err == nil {
		t.Error("NewDrainLedger with nil Client: err = nil, want non-nil")
	}
	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Tracker: tracker, Client: cl, Pools: pools}); err == nil {
		t.Error("NewDrainLedger with empty Namespace: err = nil, want non-nil")
	}
	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Tracker: tracker, Client: cl, Namespace: testNS}); err == nil {
		t.Error("NewDrainLedger with nil Pools: err = nil, want non-nil")
	}
	if _, err := recycle.NewPodInspector(recycle.PodInspectorOptions{Namespace: testNS, Pools: pools, Runtimes: runtimes}); err == nil {
		t.Error("NewPodInspector with nil Client: err = nil, want non-nil")
	}
	if _, err := recycle.NewPodInspector(recycle.PodInspectorOptions{Client: cl, Pools: pools, Runtimes: runtimes}); err == nil {
		t.Error("NewPodInspector with empty Namespace: err = nil, want non-nil")
	}
	if _, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{Namespace: testNS}); err == nil {
		t.Error("NewClaimDispositionDriver with nil Client: err = nil, want non-nil")
	}
	if _, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{Client: cl}); err == nil {
		t.Error("NewClaimDispositionDriver with empty Namespace: err = nil, want non-nil")
	}
}

// recordingSink records the §16.1 metric emissions the retirement-metrics
// adapter forwards, so the typed-RetireReason-to-string mapping is asserted.
type recordingSink struct {
	totals      []string
	gauges      []string
	retirements []string
}

func (s *recordingSink) IncScrubFailureTotal(pool, rc string) {
	s.totals = append(s.totals, pool+"/"+rc)
}

func (s *recordingSink) SetScrubFailureCount(_, pool, rc string, _ int) {
	s.gauges = append(s.gauges, pool+"/"+rc)
}

func (s *recordingSink) IncRetirement(reason, pool, rc string) {
	s.retirements = append(s.retirements, reason+"/"+pool+"/"+rc)
}

// TestRetirementMetricsMapsReasonToLabel verifies the adapter forwards the
// scrub-failure and retirement emissions and maps the typed RetireReason onto
// its string label.
// spec: 16.1 (lenny_pod_scrub_failure_total, lenny_pod_scrub_failure_count, lenny_pod_retirement_total)
//
// diagnosis: a failure means the recycle observability is dropped or the
// retirement reason label is mis-encoded, so the §16.1 metric catalog
// vocabulary does not match the emitter.
func TestRetirementMetricsMapsReasonToLabel_spec_16_1(t *testing.T) {
	sink := &recordingSink{}
	m := recycle.NewRetirementMetrics(sink)
	m.IncScrubFailureTotal("agents", "gvisor")
	m.SetScrubFailureCount("pod-1", "agents", "gvisor", 3)
	m.IncRetirement(podscrub.ReasonScrubFailuresExhausted, "agents", "gvisor")
	if len(sink.totals) != 1 || sink.totals[0] != "agents/gvisor" {
		t.Errorf("totals = %v, want one agents/gvisor", sink.totals)
	}
	if len(sink.gauges) != 1 || sink.gauges[0] != "agents/gvisor" {
		t.Errorf("gauges = %v, want one agents/gvisor", sink.gauges)
	}
	if len(sink.retirements) != 1 || sink.retirements[0] != "scrub_failure_limit/agents/gvisor" {
		t.Errorf("retirements = %v, want one scrub_failure_limit/agents/gvisor", sink.retirements)
	}
}

// TestRetirementMetricsNilSinkIsNil verifies a nil sink yields a nil seam so
// the ScrubReporter falls back to its no-op metrics rather than panicking.
// spec: 16.1 (optional metrics sink)
//
// diagnosis: a failure means wiring the recycle seams without a metrics sink
// builds a non-nil seam that dereferences a nil sink on the first emission.
func TestRetirementMetricsNilSinkIsNil_spec_16_1(t *testing.T) {
	if m := recycle.NewRetirementMetrics(nil); m != nil {
		t.Errorf("NewRetirementMetrics(nil) = %v, want nil", m)
	}
}

// TestClaimDispositionRecycleScrubWarningStampFailureFailsClosed verifies a
// warn-policy recycle whose scrub-warning pod-annotation stamp fails aborts the
// recycle rather than reserving the pod with the marker silently dropped: the
// stamp runs first and a fault fails closed so a warn-policy pod never re-enters
// the pool unmarked.
// spec: 5.2 (warn-policy marker fails closed), 3.4 (recycle disposition)
//
// diagnosis: a failure means a transient Pods patch fault while stamping the
// scrub-warning marker is swallowed and the pod is reserved anyway, so a
// warn-policy pod re-enters the pool with no residual-state marker.
func TestClaimDispositionRecycleScrubWarningStampFailureFailsClosed_spec_5_2(t *testing.T) {
	base := fake.NewClientBuilder().Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("apiserver unreachable")
		},
	})
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: cl, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	if err := d.Recycle(context.Background(), "pod-1", false, true); err == nil {
		t.Error("Recycle with a failing scrub-warning stamp: err = nil, want non-nil (fail closed)")
	}
}

// claimNotFound is the NotFound the binding-state writers surface when the
// SandboxClaim was concurrently reclaimed: the status-subresource SSA cannot
// create the vanished object and WriteRewarmStartedStatus's get-first wraps a
// NotFound.
func claimNotFound(podID string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxclaims"}, podclaim.ClaimName(podID))
}

// TestClaimDispositionRecycleNonPreConnectClaimGoneIsNoOp verifies a
// non-preConnect Recycle whose claim was concurrently reclaimed is a no-op: the
// WriteReservedStatus status-subresource SSA returns NotFound (a status apply
// cannot create the object), which the driver absorbs as "nothing to recycle".
// spec: 3.4 (disposition skipped when the claim is gone), 4.6.1 (orphan-GC crash recovery)
//
// diagnosis: a failure means a ReportPodScrub racing a hold-expiry DELETE errors
// instead of skipping, so the adapter sees a failed report for a pod whose claim
// was already reclaimed.
func TestClaimDispositionRecycleNonPreConnectClaimGoneIsNoOp_spec_3_4(t *testing.T) {
	base := fake.NewClientBuilder().Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return claimNotFound("pod-1")
		},
	})
	d := mustDispositionDriver(t, cl)
	if err := d.Recycle(context.Background(), "pod-1", false, false); err != nil {
		t.Errorf("Recycle with gone claim: err = %v, want nil (no-op)", err)
	}
}

// TestClaimDispositionRecyclePreConnectClaimGoneIsNoOp verifies a preConnect
// Recycle whose claim was concurrently reclaimed is a no-op:
// WriteRewarmStartedStatus's get-first returns a wrapped NotFound, which the
// driver absorbs. spec: 6.2 (preConnect re-warm), 3.4 (skip when the claim is gone)
//
// diagnosis: a failure means a preConnect ReportPodScrub racing a concurrent
// reclaim maps to an Internal RPC error for a pod whose claim no longer exists.
func TestClaimDispositionRecyclePreConnectClaimGoneIsNoOp_spec_3_4(t *testing.T) {
	base := fake.NewClientBuilder().Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return claimNotFound("pod-1")
		},
	})
	d := mustDispositionDriver(t, cl)
	if err := d.Recycle(context.Background(), "pod-1", true, false); err != nil {
		t.Errorf("preConnect Recycle with gone claim: err = %v, want nil (no-op)", err)
	}
}

// TestClaimDispositionRetireClaimGoneIsNoOp verifies a Retire whose claim was
// concurrently reclaimed is a no-op: WriteDispositionStatus's status SSA returns
// NotFound, which the driver absorbs. spec: 3.4 (retire skipped when the claim
// is gone), 4.6.1 (orphan-GC crash recovery)
//
// diagnosis: a failure means a retiring ReportPodScrub racing the orphan GC's
// reclaim of a recycling claim errors instead of skipping.
func TestClaimDispositionRetireClaimGoneIsNoOp_spec_3_4(t *testing.T) {
	base := fake.NewClientBuilder().Build()
	cl := interceptor.NewClient(base, interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return claimNotFound("pod-1")
		},
	})
	d := mustDispositionDriver(t, cl)
	if err := d.Retire(context.Background(), "pod-1", true, false, "cleanup_fail_policy", "shred timed out"); err != nil {
		t.Errorf("Retire with gone claim: err = %v, want nil (no-op)", err)
	}
}

// mustDispositionDriver builds a claim disposition driver around an arbitrary
// client (so an error-injecting interceptor can be passed) with a fixed clock.
func mustDispositionDriver(t *testing.T, cl client.Client) leasecontrol.ClaimDispositionDriver {
	t.Helper()
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: cl, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	return d
}

// mustInspector builds a pod inspector around a fake.WithWatch client and the
// supplied pool/runtime fixtures, failing the test on a construction error.
func mustInspector(t *testing.T, cl client.WithWatch, pools map[string]poolstore.Pool, runtimes map[string]runtimestore.Runtime) leasecontrol.PodInspector {
	t.Helper()
	return mustInspectorClient(t, cl, pools, runtimes)
}

// mustInspectorClient builds a pod inspector around an arbitrary client (so an
// error-injecting interceptor can be passed) and the supplied fixtures.
func mustInspectorClient(t *testing.T, cl client.Client, pools map[string]poolstore.Pool, runtimes map[string]runtimestore.Runtime) leasecontrol.PodInspector {
	t.Helper()
	insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client:    cl,
		Namespace: testNS,
		Pools:     fakePoolReader{pools: pools},
		Runtimes:  fakeRuntimeReader{runtimes: runtimes},
		Now:       func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewPodInspector: %v", err)
	}
	return insp
}
