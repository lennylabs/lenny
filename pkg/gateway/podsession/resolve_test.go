// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

func warmPool(name, templateRef string) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: templateRef, MinWarm: 1, MaxWarm: 5},
	}
}

func sandboxTemplate(name, runtimeRef, isolation string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: isolation},
	}
}

func serviceTemplate(name, runtimeRef, isolation string, maxConcurrent int32) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       runtimeRef,
			IsolationProfile: isolation,
			ExecutionMode:    "service",
			MaxConcurrent:    maxConcurrent,
		},
	}
}

// fakePolicyReader is a podsession.PoolPolicyReader test double keyed by
// pool name, so a test can assert ResolvePool folds the gateway-enforced
// §5.2 sessionPolicy mirror into the PoolMatch.
type fakePolicyReader struct {
	mirrors map[string]podsession.PoolPolicyMirror
	err     error
}

func (f fakePolicyReader) PoolPolicy(_ context.Context, name string) (podsession.PoolPolicyMirror, bool, error) {
	if f.err != nil {
		return podsession.PoolPolicyMirror{}, false, f.err
	}
	m, ok := f.mirrors[name]
	return m, ok, nil
}

// spec: §5.2 (sessionPolicy block, gateway-enforced subset)
// TestResolvePoolFoldsPolicyMirror covers the §5.2 re-source: the
// gateway-enforced sessionPolicy fields (maxConcurrentSessions,
// allowCrossTenantReuse, the concurrent-workspace pod-uptime cap) live on
// the poolstore mirror, not the CRD pair, so ResolvePool reads them
// through the PoolPolicyReader keyed by the resolved pool name.
func TestResolvePoolFoldsPolicyMirror(t *testing.T) {
	tmpl := sandboxTemplate("conc-tmpl", "conc-runtime", "microvm")
	c := k8sClient(t, warmPool("conc-pool", "conc-tmpl"), tmpl)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"conc-pool": {
			MaxConcurrentSessions: 4,
			AllowCrossTenantReuse: true,
			MaxPodUptimeSeconds:   86400,
		},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "conc-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MaxConcurrentSessions != 4 {
		t.Errorf("MaxConcurrentSessions = %d, want 4 (from the mirror)", got.MaxConcurrentSessions)
	}
	if !got.AllowCrossTenantReuse {
		t.Error("AllowCrossTenantReuse = false, want true (from the mirror)")
	}
	if got.MaxPodUptimeSeconds != 86400 {
		t.Errorf("MaxPodUptimeSeconds = %d, want 86400 (from the mirror)", got.MaxPodUptimeSeconds)
	}
}

// spec: §5.2 / §4.6.1 (onPoolExhausted, maxQueueWaitSeconds folded from the
// gateway-enforced mirror)
// TestResolvePoolFoldsPoolExhaustionDisposition covers the queue-vs-reject
// dispatch fields reaching the PoolMatch from the poolstore mirror so the
// start path's claim queue reads the gateway-enforced values rather than the
// always-empty CRD pair.
func TestResolvePoolFoldsPoolExhaustionDisposition(t *testing.T) {
	tmpl := sandboxTemplate("queue-tmpl", "queue-runtime", "sandboxed")
	c := k8sClient(t, warmPool("queue-pool", "queue-tmpl"), tmpl)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"queue-pool": {OnPoolExhausted: "queue", MaxQueueWaitSeconds: 45},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "queue-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.OnPoolExhausted != "queue" {
		t.Errorf("OnPoolExhausted = %q, want %q (from the mirror)", got.OnPoolExhausted, "queue")
	}
	if got.MaxQueueWaitSeconds != 45 {
		t.Errorf("MaxQueueWaitSeconds = %d, want 45 (from the mirror)", got.MaxQueueWaitSeconds)
	}
}

// spec: §5.2 (empty onPoolExhausted defaults to reject)
// TestResolvePoolDefaultsToRejectWithoutMirror covers the absence of a queue
// disposition: a pool with no mirror row (or an empty disposition) leaves
// OnPoolExhausted empty, which the start path treats as reject.
func TestResolvePoolDefaultsToRejectWithoutMirror(t *testing.T) {
	tmpl := sandboxTemplate("reject-tmpl", "reject-runtime", "sandboxed")
	c := k8sClient(t, warmPool("reject-pool", "reject-tmpl"), tmpl)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "reject-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.OnPoolExhausted != "" {
		t.Errorf("OnPoolExhausted = %q, want empty (reject default) without a mirror row", got.OnPoolExhausted)
	}
	if got.MaxQueueWaitSeconds != 0 {
		t.Errorf("MaxQueueWaitSeconds = %d, want 0 without a mirror row", got.MaxQueueWaitSeconds)
	}
}

// spec: §5.2 (service mode, gateway-enforced routing capacity)
// TestResolvePoolMirrorOverridesServiceMaxConcurrent covers the
// service-mode capacity re-source: the poolstore mirror is authoritative
// for the per-pod request capacity, so a non-zero mirror value overrides
// the CRD-derived MaxConcurrent.
func TestResolvePoolMirrorOverridesServiceMaxConcurrent(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("svc-pool", "svc-tmpl"),
		serviceTemplate("svc-tmpl", "load-svc-runtime", "sandboxed", 8),
	)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"svc-pool": {MaxConcurrent: 16},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "load-svc-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MaxConcurrent != 16 {
		t.Errorf("MaxConcurrent = %d, want 16 (the mirror is authoritative)", got.MaxConcurrent)
	}
}

// spec: §5.2 (gateway-enforced subset, fail-closed read)
// TestResolvePoolPropagatesPolicyError covers the error path: a mirror
// read failure surfaces as a ResolvePool error rather than silently
// falling back to always-zero dispatch fields.
func TestResolvePoolPropagatesPolicyError(t *testing.T) {
	tmpl := sandboxTemplate("err-tmpl", "err-runtime", "sandboxed")
	c := k8sClient(t, warmPool("err-pool", "err-tmpl"), tmpl)
	policy := fakePolicyReader{err: errors.New("mirror down")}

	_, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "err-runtime", "sandboxed", "")
	if err == nil {
		t.Fatal("ResolvePool: want a propagated error when the policy mirror read fails")
	}
}

// spec: §5.2 (gateway-enforced subset, missing mirror row)
// TestResolvePoolKeepsCRDDefaultsWhenMirrorAbsent covers the
// Postgres-only / CRD-only posture: a pool with no mirror row leaves the
// dispatch fields at their CRD-derived defaults instead of erroring.
func TestResolvePoolKeepsCRDDefaultsWhenMirrorAbsent(t *testing.T) {
	tmpl := sandboxTemplate("nomirror-tmpl", "nomirror-runtime", "sandboxed")
	c := k8sClient(t, warmPool("nomirror-pool", "nomirror-tmpl"), tmpl)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, testNS, "nomirror-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MaxConcurrentSessions != 0 || got.AllowCrossTenantReuse || got.MaxPodUptimeSeconds != 0 {
		t.Errorf("dispatch fields = %+v, want zero when no mirror row exists", got)
	}
}

// TestResolvePoolReturnsServiceDispatchFields covers the gateway
// dispatch path: ResolvePool surfaces ExecutionMode and MaxConcurrent so
// the start path routes a service-mode runtime through its claimless,
// request-routed path. The maxConcurrentSessions and pod-uptime dispatch
// fields now reach the gateway through the poolstore sessionPolicy
// mirror; the CRD carries only the execution mode and per-pod slot bound.
func TestResolvePoolReturnsServiceDispatchFields(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("svc-pool", "svc-tmpl"),
		serviceTemplate("svc-tmpl", "load-svc-runtime", "sandboxed", 8),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "load-svc-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "svc-pool" {
		t.Errorf("resolved pool = %q, want svc-pool", got.Pool)
	}
	if got.ExecutionMode != "service" {
		t.Errorf("executionMode = %q, want service", got.ExecutionMode)
	}
	if got.MaxConcurrent != 8 {
		t.Errorf("maxConcurrent = %d, want 8", got.MaxConcurrent)
	}
}

// TestResolvePoolSessionModeLeavesDispatchFieldsEmpty covers the
// negative case: a session-mode pool with no per-pod slot bound leaves
// the dispatch fields empty so the start path takes the session-claim
// path.
func TestResolvePoolSessionModeLeavesDispatchFieldsEmpty(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("session-pool", "session-tmpl"),
		sandboxTemplate("session-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.ExecutionMode != "" {
		t.Errorf("executionMode = %q, want empty for the default session mode", got.ExecutionMode)
	}
	if got.MaxConcurrentSessions != 0 {
		t.Errorf("maxConcurrentSessions = %d, want 0 with no policy mirror", got.MaxConcurrentSessions)
	}
	if got.MaxConcurrent != 0 {
		t.Errorf("maxConcurrent = %d, want 0", got.MaxConcurrent)
	}
}

// TestResolvePoolSurfacesScrubProfile covers the §5.2 Kata/microvm scrub
// variant: ResolvePool surfaces the sessionPolicy.recycle scrubProfile so
// the start path can select the §7.1 scrubPolicy variant. This is the
// only recycle dispatch field the CRD carries; the rest reach the gateway
// through the poolstore mirror.
//
// spec: §5.2 (Kata/microvm scrub variant).
func TestResolvePoolSurfacesScrubProfile(t *testing.T) {
	tmpl := serviceTemplate("recycle-tmpl", "recycle-runtime", "microvm", 1)
	tmpl.Spec.ExecutionMode = "session"
	tmpl.Spec.SessionPolicy = &lennyv1.SessionPolicy{
		Recycle: &lennyv1.RecyclePolicy{
			ScrubProfile:                    "in-place",
			AcknowledgeMicrovmResidualState: true,
		},
	}
	c := k8sClient(t, warmPool("recycle-pool", "recycle-tmpl"), tmpl)

	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "recycle-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MicrovmScrubMode != "in-place" {
		t.Errorf("MicrovmScrubMode = %q, want in-place", got.MicrovmScrubMode)
	}
}

// TestResolvePoolLeavesScrubProfileUnsetWithoutRecycle covers the default:
// a pool with no recycle block leaves the scrub-profile dispatch field
// empty.
func TestResolvePoolLeavesScrubProfileUnsetWithoutRecycle(t *testing.T) {
	tmpl := sandboxTemplate("plain-tmpl", "plain-runtime", "sandboxed")
	c := k8sClient(t, warmPool("plain-pool", "plain-tmpl"), tmpl)

	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "plain-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MicrovmScrubMode != "" {
		t.Errorf("MicrovmScrubMode = %q, want empty (no recycle block)", got.MicrovmScrubMode)
	}
}

func TestResolvePoolMatchesByRuntime(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got.Pool)
	}
}

func TestResolvePoolNoMatch(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "other-runtime", "", "")
	if !errors.Is(err, podsession.ErrNoMatchingPool) {
		t.Errorf("error = %v, want ErrNoMatchingPool", err)
	}
}

func TestResolvePoolDisambiguatesByIsolation(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-gvisor", "tmpl-gvisor"),
		sandboxTemplate("tmpl-gvisor", "claude-code", "sandboxed"),
		warmPool("claude-kata", "tmpl-kata"),
		sandboxTemplate("tmpl-kata", "claude-code", "microvm"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-kata" {
		t.Errorf("resolved pool = %q, want claude-kata", got.Pool)
	}
}

func TestResolvePoolAmbiguous(t *testing.T) {
	// Two pools with the same runtime and isolation: the gateway cannot
	// pick one.
	c := k8sClient(
		t,
		warmPool("pool-a", "tmpl-a"),
		sandboxTemplate("tmpl-a", "claude-code", "sandboxed"),
		warmPool("pool-b", "tmpl-b"),
		sandboxTemplate("tmpl-b", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "")
	if !errors.Is(err, podsession.ErrAmbiguousPool) {
		t.Errorf("error = %v, want ErrAmbiguousPool", err)
	}
}

func TestResolvePoolSkipsDanglingTemplateRef(t *testing.T) {
	// The pool with a dangling template ref is skipped; the valid pool
	// still resolves.
	c := k8sClient(
		t,
		warmPool("broken-pool", "missing-tmpl"),
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got.Pool)
	}
}

// seedPoolStatus stamps the §5.2 PoolWarmingUp condition on the template
// and the warm/ready counts on the pool, mirroring what the
// WarmPoolController writes, so ResolvePool and PoolStatusLookup can be
// exercised against a realistic bootstrap-window status.
func seedPoolStatus(t *testing.T, c client.Client, templateName, poolName string, warm, ready int32, warming bool) {
	t.Helper()
	ctx := context.Background()

	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: templateName}, &tmpl); err != nil {
		t.Fatalf("get template %s: %v", templateName, err)
	}
	status, reason := metav1.ConditionFalse, "Available"
	if warming {
		status, reason = metav1.ConditionTrue, "Provisioning"
	}
	tmpl.Status.Conditions = []metav1.Condition{{
		Type:               "PoolWarmingUp",
		Status:             status,
		Reason:             reason,
		Message:            "test",
		LastTransitionTime: metav1.Now(),
	}}
	if err := c.Status().Update(ctx, &tmpl); err != nil {
		t.Fatalf("seed template status %s: %v", templateName, err)
	}

	var pool lennyv1.SandboxWarmPool
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: poolName}, &pool); err != nil {
		t.Fatalf("get pool %s: %v", poolName, err)
	}
	pool.Status.WarmCount, pool.Status.ReadyCount = warm, ready
	if err := c.Status().Update(ctx, &pool); err != nil {
		t.Fatalf("seed pool status %s: %v", poolName, err)
	}
}

// spec: §5.2 lines 594, 602-625 — ResolvePool surfaces the PoolWarmingUp
// condition and the warming-pod count so the start path can answer a
// session creation against a bootstrapping pool with the 503 Pool Not
// Ready response instead of burning a claim attempt.
func TestResolvePoolSurfacesPoolWarmingUp(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("warming-pool", "warming-tmpl"),
		sandboxTemplate("warming-tmpl", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "warming-tmpl", "warming-pool", 2, 0, true)

	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if !got.PoolWarmingUp {
		t.Error("PoolWarmingUp = false, want true while the pool is bootstrapping")
	}
	if got.PodsWarming != 2 {
		t.Errorf("PodsWarming = %d, want 2 (warm 2 - ready 0)", got.PodsWarming)
	}
}

// spec: §5.2 line 600 — a pool with idle pods is not warming; PodsWarming
// clamps at zero.
func TestResolvePoolNotWarmingWhenReady(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("ready-pool", "ready-tmpl"),
		sandboxTemplate("ready-tmpl", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "ready-tmpl", "ready-pool", 3, 3, false)

	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.PoolWarmingUp {
		t.Error("PoolWarmingUp = true, want false once idle pods are ready")
	}
	if got.PodsWarming != 0 {
		t.Errorf("PodsWarming = %d, want 0", got.PodsWarming)
	}
}

// spec: §5.2 line 629 — PoolStatusLookup feeds the admin pool GET's
// poolCondition and idlePodCount. One client carries a warming pool, a
// ready pool, and an absent pool so the three cases share one envtest.
func TestPoolStatusLookup(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("warming-p", "warming-t"),
		sandboxTemplate("warming-t", "rt-warming", "sandboxed"),
		warmPool("ready-p", "ready-t"),
		sandboxTemplate("ready-t", "rt-ready", "sandboxed"),
	)
	seedPoolStatus(t, c, "warming-t", "warming-p", 2, 0, true)
	seedPoolStatus(t, c, "ready-t", "ready-p", 4, 4, false)

	l := podsession.PoolStatusLookup{Reader: c, Namespace: testNS}
	ctx := context.Background()

	cond, idle, found, err := l.PoolStatus(ctx, "warming-p")
	if err != nil || !found {
		t.Fatalf("warming PoolStatus: found=%v err=%v", found, err)
	}
	if cond != "PoolWarmingUp" || idle != 0 {
		t.Errorf("warming pool: condition=%q idle=%d, want PoolWarmingUp / 0", cond, idle)
	}

	cond, idle, found, err = l.PoolStatus(ctx, "ready-p")
	if err != nil || !found {
		t.Fatalf("ready PoolStatus: found=%v err=%v", found, err)
	}
	if cond != "" || idle != 4 {
		t.Errorf("ready pool: condition=%q idle=%d, want empty / 4", cond, idle)
	}

	if _, _, found, err = l.PoolStatus(ctx, "absent-pool"); err != nil || found {
		t.Errorf("absent pool: found=%v err=%v, want found=false with no error", found, err)
	}
}

// spec: §7.1 (pool selector), §14.1 (CreateSessionRequest.pool)
// diagnosis: ResolvePool dropped a satisfiable client-pinned pool. A
// failure means the §14.1 pool selector is not honored: a session pinned
// to a specific backed pool would schedule elsewhere or fail to resolve.
// TestResolvePoolHonorsPinnedPool drives the F-CS2 honor path: among two
// pools that both back the runtime + profile (ambiguous without a pin),
// the named pin selects exactly that pool.
func TestResolvePoolHonorsPinnedPool_spec_7_1(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("pool-a", "tmpl-a"),
		sandboxTemplate("tmpl-a", "claude-code", "sandboxed"),
		warmPool("pool-b", "tmpl-b"),
		sandboxTemplate("tmpl-b", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, testNS, "claude-code", "sandboxed", "pool-b")
	if err != nil {
		t.Fatalf("ResolvePool with a satisfiable pin: %v", err)
	}
	if got.Pool != "pool-b" {
		t.Errorf("resolved pool = %q, want pool-b (the pinned pool, not pool-a)", got.Pool)
	}
}

// spec: §7.1 (pool selector), §14.1 (CreateSessionRequest.pool)
// diagnosis: ResolvePool failed to reject an unsatisfiable client-pinned
// pool. A failure means an absent, not-backed, or isolation-inconsistent
// pin is silently scheduled on a different pool or surfaced as the wrong
// error class, breaking the F-CS2 fail-closed contract. The cases share
// one envtest client.
func TestResolvePoolRejectsUnsatisfiablePin_spec_14_1(t *testing.T) {
	c := k8sClient(
		t,
		// A pool backed by claude-code / sandboxed.
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
		// A pool backed by a different runtime, isolation microvm.
		warmPool("other-pool", "other-tmpl"),
		sandboxTemplate("other-tmpl", "other-runtime", "microvm"),
	)
	cases := []struct {
		name             string
		runtimeRef       string
		isolationProfile string
		pinnedPool       string
	}{
		{"absent pool name", "claude-code", "sandboxed", "no-such-pool"},
		{"pinned pool not backed by runtime", "claude-code", "sandboxed", "other-pool"},
		{"pinned pool isolation-inconsistent", "other-runtime", "sandboxed", "other-pool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := podsession.ResolvePool(context.Background(), c, nil, testNS,
				tc.runtimeRef, tc.isolationProfile, tc.pinnedPool)
			if !errors.Is(err, podsession.ErrPoolNotSatisfiable) {
				t.Errorf("error = %v, want ErrPoolNotSatisfiable", err)
			}
			// The unsatisfiable-pin error must not collapse to the
			// operator-side ErrNoMatchingPool, which the gateway maps to a
			// different (503) envelope.
			if errors.Is(err, podsession.ErrNoMatchingPool) {
				t.Errorf("error = %v, must not be ErrNoMatchingPool for a client pin", err)
			}
		})
	}
}
