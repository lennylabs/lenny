// SPDX-License-Identifier: MIT

package recycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/agentpodstate/memstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
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
	cl := fake.NewClientBuilder().
		WithObjects(agentPod("pod-1", "agents", "true", created)).
		Build()
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
	if policy.OnScrubFailure != taskcleanup.OnCleanupWarn {
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
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
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

// TestInspectForRecycleNonRecyclingPoolIsNoOp verifies a whole-pod scrub for
// a pod in a non-recycling pool (no recycle block) reports found=false: the
// pod is one-session-per-pod and is not a recycle candidate.
// spec: 5.2 (recycle disposition applies only to recycling pools)
//
// diagnosis: a failure means a one-session-per-pod pod is run through the
// recycle disposition, reserving or re-warming a pod the pool model retires on
// session end.
func TestInspectForRecycleNonRecyclingPoolIsNoOp_spec_5_2(t *testing.T) {
	cl := fake.NewClientBuilder().
		WithObjects(agentPod("pod-1", "agents", "true", time.Unix(0, 0))).
		Build()
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
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
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

// TestDrainLedgerStampsOnUnhealthyThreshold verifies the ledger stamps
// lenny.dev/drain-request on the agent Pod once the pod crosses the §5.2
// unhealthy threshold, and not before. A single-session pool (maxConcurrent
// 1) crosses on the first leak.
// spec: 4.7 (leaked feeds the drain ledger), 4.6.3 (gateway stamps drain-request), 5.2 (unhealthy threshold)
//
// diagnosis: a failure means a leaked session does not drive the drain-request
// annotation at the unhealthy threshold, so the WarmPoolController never
// drains the unhealthy pod, or the ledger stamps prematurely and drains a
// healthy pod.
func TestDrainLedgerStampsOnUnhealthyThreshold_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{
		Tracker: slothealth.New(), Client: cl, Namespace: testNS, MaxConcurrent: 1,
		Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewDrainLedger: %v", err)
	}
	if err := ledger.RecordLeak(context.Background(), "pod-1"); err != nil {
		t.Fatalf("RecordLeak: %v", err)
	}
	var got corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.Annotations[lennyv1.AnnotationDrainRequest] == "" {
		t.Error("drain-request annotation not stamped after the unhealthy threshold was crossed")
	}
}

// TestDrainLedgerBelowThresholdDoesNotStamp verifies a single leak on a
// multi-session pool (threshold above one) does not yet drain the pod.
// spec: 5.2 (ceil(maxConcurrent/2) unhealthy threshold)
//
// diagnosis: a failure means a single leaked slot on a multi-session pod
// drains the whole pod before the ceil(maxConcurrent/2) threshold is reached,
// retiring a still-healthy pod.
func TestDrainLedgerBelowThresholdDoesNotStamp_spec_5_2(t *testing.T) {
	pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
	cl := fake.NewClientBuilder().WithObjects(pod).Build()
	ledger, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{
		Tracker: slothealth.New(), Client: cl, Namespace: testNS, MaxConcurrent: 4,
		Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("NewDrainLedger: %v", err)
	}
	if err := ledger.RecordLeak(context.Background(), "pod-1"); err != nil {
		t.Fatalf("RecordLeak: %v", err)
	}
	var got corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.Annotations[lennyv1.AnnotationDrainRequest] != "" {
		t.Error("drain-request stamped below the ceil(maxConcurrent/2) threshold")
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

	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Client: cl, Namespace: testNS}); err == nil {
		t.Error("NewDrainLedger with nil Tracker: err = nil, want non-nil")
	}
	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Tracker: tracker, Namespace: testNS}); err == nil {
		t.Error("NewDrainLedger with nil Client: err = nil, want non-nil")
	}
	if _, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{Tracker: tracker, Client: cl}); err == nil {
		t.Error("NewDrainLedger with empty Namespace: err = nil, want non-nil")
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
	m.IncRetirement(taskcleanup.ReasonScrubFailuresExhausted, "agents", "gvisor")
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
