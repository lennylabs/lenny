// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/agentpodstate/memstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// scrubWiringNS is the agent namespace the scrub-report wiring test seeds.
const scrubWiringNS = "lenny-agents"

// scrubWiringScheme registers the clientgo (Pod, Namespace) and lenny.dev
// (SandboxClaim) types the wiring exercises.
func scrubWiringScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("clientgoscheme: %v", err)
	}
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("lennyv1: %v", err)
	}
	return s
}

// TestScrubReportServiceWiringDrivesRecycle exercises the §4.7 end-to-end
// gateway wiring: newScrubReportService builds the ScrubReporter over the
// concrete recycle seams, and a leasecontrol.Service wired with it serves
// ReportSessionScrub and ReportPodScrub against a real apiserver. The test
// proves the handlers no longer return Unimplemented (the pre-S26 state),
// that a session-scrub increments sessions_served on the agent_pod_state
// mirror, and that a clean whole-pod scrub on a schedulable host drives the
// recycle disposition onto the SandboxClaim (recycling → reserved).
//
// spec: §4.7 (ReportSessionScrub/ReportPodScrub gateway side), §3.4
// (recycle disposition), §5.2 (scrub model), §6.39 (host-node schedulability).
//
// diagnosis: a failure means the gateway either left the scrub-report RPCs
// unwired (returning Unimplemented to every adapter), mis-wired the recycle
// counters so the session cap never advances, or failed to drive the claim
// disposition so a scrubbed pod is never reserved for its tenant.
func TestScrubReportServiceWiringDrivesRecycle_spec_4_7(t *testing.T) {
	env := envtest.Start(t)
	cl, err := client.New(env.RESTConfig(), client.Options{Scheme: scrubWiringScheme(t)})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	if err := cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: scrubWiringNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}

	const podID = "pod-recycle-1"
	const poolName = "agents"

	// Seed the agent Pod with the pool + schedulable-host labels the
	// inspector reads.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podID,
			Namespace: scrubWiringNS,
			Labels: map[string]string{
				labelPool:                    poolName,
				"lenny.dev/host-schedulable": "true",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "busybox"}}},
	}
	if err := cl.Create(ctx, pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// Seed the per-pod SandboxClaim through bound → recycling, the
	// precondition the recycle disposition patches from.
	claimName := podclaim.ClaimName(podID)
	if err := cl.Create(ctx, &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: scrubWiringNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: podID, TenantID: "acme"},
	}); err != nil {
		t.Fatalf("create claim: %v", err)
	}
	if err := podclaim.WriteBoundStatus(ctx, cl, scrubWiringNS, claimName); err != nil {
		t.Fatalf("seed bound: %v", err)
	}
	if err := podclaim.WriteRecyclingStatus(ctx, cl, scrubWiringNS, claimName, nil); err != nil {
		t.Fatalf("seed recycling: %v", err)
	}

	// Seed the agent_pod_state recycle-counter row.
	counters := memstore.New(func() time.Time { return time.Unix(0, 0) })
	counters.Put(agentpodstate.PodState{PodID: podID, PoolID: poolName})

	// Resolve the pod's pool to a recycling policy with generous limits so a
	// clean scrub reuses rather than retires, on a non-preConnect runtime.
	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          poolName,
		RuntimeRef:    "rt",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{Recycle: &runtimestore.RecyclePolicy{
			Enabled: true, AcknowledgeBestEffortScrub: true,
			OnScrubFailure:   runtimestore.CleanupFailureWarn,
			MaxScrubFailures: 3, MaxSessionsPerPod: 50,
		}},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "rt"}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	svc := newScrubService(t, cl, counters, pools, runtimes)

	// A clean session release increments sessions_served.
	if _, err := svc.ReportSessionScrub(ctx, &adapterv1.ReportSessionScrubRequest{
		PodId:     podID,
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}); err != nil {
		if status.Code(err) == codes.Unimplemented {
			t.Fatal("ReportSessionScrub returned Unimplemented: the scrub-report service is not wired")
		}
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	served, found, err := counters.RecycleCounters(ctx, podID)
	if err != nil || !found {
		t.Fatalf("agent_pod_state row missing after session scrub: found=%v err=%v", found, err)
	}
	if served.SessionsServed != 1 {
		t.Errorf("sessions_served = %d, want 1", served.SessionsServed)
	}

	// A clean whole-pod scrub on a schedulable host drives the recycle
	// disposition: the non-preConnect claim is reserved.
	if _, err := svc.ReportPodScrub(ctx, &adapterv1.ReportPodScrubRequest{
		PodId:   podID,
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED,
	}); err != nil {
		if status.Code(err) == codes.Unimplemented {
			t.Fatal("ReportPodScrub returned Unimplemented: the scrub-report service is not wired")
		}
		t.Fatalf("ReportPodScrub: %v", err)
	}
	var got lennyv1.SandboxClaim
	if err := cl.Get(ctx, client.ObjectKey{Namespace: scrubWiringNS, Name: claimName}, &got); err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Status.Phase != "reserved" {
		t.Errorf("claim phase = %q, want reserved (recycle disposition reused the pod)", got.Status.Phase)
	}
}

// TestScrubReportServiceWiringResolvesPerPoolDrainThreshold exercises the
// §5.2 unhealthy-threshold drain ledger end to end on a recycling
// concurrent-session pool (the "Concurrent" preset, maxConcurrentSessions: 8,
// threshold ceil(8/2)=4). It proves the gateway wiring resolves the drain
// threshold against the pod's pool rather than a fixed maxConcurrent=1: a
// single leaked session scrub leaves the pod undrained (no
// lenny.dev/drain-request), and the pod is stamped only once it accumulates
// four leaked sessions in the window.
//
// spec: §4.7 (ReportSessionScrub leaked feeds the drain ledger), §5.2
// (ceil(maxConcurrentSessions/2) unhealthy threshold), §3.4 (recycle
// disposition), §6.39 (gateway stamps drain-request).
//
// diagnosis: a failure means the gateway hard-wires the drain threshold to 1
// instead of resolving the pod's pool maxConcurrentSessions, so a recycling
// concurrent-session pod is drained on its first leaked slot rather than at
// the ceil(maxConcurrentSessions/2) trigger, retiring a still-healthy pod.
func TestScrubReportServiceWiringResolvesPerPoolDrainThreshold_spec_5_2(t *testing.T) {
	env := envtest.Start(t)
	cl, err := client.New(env.RESTConfig(), client.Options{Scheme: scrubWiringScheme(t)})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	if err := cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: scrubWiringNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}

	const podID = "pod-concurrent-1"
	const poolName = "concurrent-agents"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podID,
			Namespace: scrubWiringNS,
			Labels: map[string]string{
				labelPool:                    poolName,
				"lenny.dev/host-schedulable": "true",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "busybox"}}},
	}
	if err := cl.Create(ctx, pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	counters := memstore.New(func() time.Time { return time.Unix(0, 0) })
	counters.Put(agentpodstate.PodState{PodID: podID, PoolID: poolName})

	// A recycling concurrent-session pool: maxConcurrentSessions 8 yields a
	// ceil(8/2)=4 unhealthy threshold, so the first three leaks must not drain.
	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          poolName,
		RuntimeRef:    "rt",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            8,
			AcknowledgeProcessLevelIsolation: true,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled: true, AcknowledgeBestEffortScrub: true,
				OnScrubFailure: runtimestore.CleanupFailureWarn, MaxSessionsPerPod: 50,
			},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "rt"}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	svc := newScrubService(t, cl, counters, pools, runtimes)

	reportLeak := func(sessionID string) {
		t.Helper()
		if _, err := svc.ReportSessionScrub(ctx, &adapterv1.ReportSessionScrubRequest{
			PodId:     podID,
			SessionId: &adapterv1.SessionId{Value: sessionID},
			Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED,
		}); err != nil {
			t.Fatalf("ReportSessionScrub(%s) leaked: %v", sessionID, err)
		}
	}
	drainStamped := func() bool {
		t.Helper()
		var got corev1.Pod
		if err := cl.Get(ctx, client.ObjectKey{Namespace: scrubWiringNS, Name: podID}, &got); err != nil {
			t.Fatalf("get pod: %v", err)
		}
		return got.Annotations[lennyv1.AnnotationDrainRequest] != ""
	}

	// Three leaks are below the ceil(8/2)=4 threshold: a fixed-1 wiring would
	// have drained on the first.
	reportLeak("sess-1")
	reportLeak("sess-2")
	reportLeak("sess-3")
	if drainStamped() {
		t.Fatal("drain-request stamped at 3 leaks on a maxConcurrentSessions:8 pool, below the ceil(8/2)=4 threshold")
	}
	// The fourth leak crosses the threshold.
	reportLeak("sess-4")
	if !drainStamped() {
		t.Error("drain-request not stamped at 4 leaks, the ceil(8/2) unhealthy threshold")
	}
}

// newScrubService builds a leasecontrol.Service wired with the §4.7 scrub
// reporter the gateway constructs in main, so the test drives the same
// handlers the GatewayControl listener serves.
func newScrubService(t *testing.T, cl client.Client, counters *memstore.Store, pools poolstore.Store, runtimes runtimestore.Store) *leasecontrol.Service {
	t.Helper()
	scrubReports, err := newScrubReportService(cl, counters, pools, runtimes, nil, scrubWiringNS, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatalf("newScrubReportService: %v", err)
	}
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:      budgets,
		Tenants:      budgets,
		ScrubReports: scrubReports,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}
