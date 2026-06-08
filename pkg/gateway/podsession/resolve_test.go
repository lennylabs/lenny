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

func concurrentTemplate(name, runtimeRef, isolation, style string, maxConcurrent int32) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       runtimeRef,
			IsolationProfile: isolation,
			ExecutionMode:    "concurrent",
			ConcurrencyStyle: style,
			MaxConcurrent:    maxConcurrent,
		},
	}
}

// TestResolvePoolReturnsConcurrentDispatchFields covers the gateway
// dispatch fix: ResolvePool must surface ExecutionMode,
// ConcurrencyStyle, and MaxConcurrent so startOnPod can route a
// concurrent-mode runtime through BindSlot rather than Bind. A
// regression here would put concurrent-mode sandboxes into `claimed`
// instead of `slot_active`.
func TestResolvePoolReturnsConcurrentDispatchFields(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("cstateless-pool", "cstateless-tmpl"),
		concurrentTemplate("cstateless-tmpl", "load-cstateless-runtime", "sandboxed", "stateless", 8),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "load-cstateless-runtime", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "cstateless-pool" {
		t.Errorf("resolved pool = %q, want cstateless-pool", got.Pool)
	}
	if got.ExecutionMode != "concurrent" {
		t.Errorf("executionMode = %q, want concurrent (the start path dispatches to BindSlot when this is concurrent)", got.ExecutionMode)
	}
	if got.ConcurrencyStyle != "stateless" {
		t.Errorf("concurrencyStyle = %q, want stateless", got.ConcurrencyStyle)
	}
	if got.MaxConcurrent != 8 {
		t.Errorf("maxConcurrent = %d, want 8", got.MaxConcurrent)
	}
}

// TestResolvePoolSessionModeLeavesDispatchFieldsEmpty covers the
// negative case: a session-mode pool must not carry concurrent-mode
// dispatch fields, so startOnPod takes the Bind path.
func TestResolvePoolSessionModeLeavesDispatchFieldsEmpty(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("session-pool", "session-tmpl"),
		sandboxTemplate("session-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.ExecutionMode != "" {
		t.Errorf("executionMode = %q, want empty for the default session mode", got.ExecutionMode)
	}
	if got.ConcurrencyStyle != "" {
		t.Errorf("concurrencyStyle = %q, want empty", got.ConcurrencyStyle)
	}
	if got.MaxConcurrent != 0 {
		t.Errorf("maxConcurrent = %d, want 0", got.MaxConcurrent)
	}
}

// TestResolvePoolSurfacesConcurrentMaxPodUptime covers the §6.2 lines
// 166-167 retirement cap: ResolvePool must surface the concurrent-
// workspace pool's maxPodUptimeSeconds so the slot-claim path drains an
// over-uptime pod before its next slot assignment.
//
// spec: §6.2 lines 166-167.
func TestResolvePoolSurfacesConcurrentMaxPodUptime(t *testing.T) {
	tmpl := concurrentTemplate("cw-tmpl", "cw-runtime", "sandboxed", "workspace", 4)
	uptime := int64(86400)
	tmpl.Spec.ConcurrentWorkspacePolicy = &lennyv1.ConcurrentWorkspacePolicy{
		AcknowledgeProcessLevelIsolation: true,
		MaxPodUptimeSeconds:              &uptime,
	}
	c := k8sClient(t, warmPool("cw-pool", "cw-tmpl"), tmpl)

	got, err := podsession.ResolvePool(context.Background(), c, testNS, "cw-runtime", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MaxPodUptimeSeconds != 86400 {
		t.Errorf("MaxPodUptimeSeconds = %d, want 86400", got.MaxPodUptimeSeconds)
	}
}

// TestResolvePoolLeavesUptimeUnsetWithoutPolicy covers the optional cap:
// a concurrent-workspace pool with no maxPodUptimeSeconds leaves the
// PoolMatch field zero so the slot-claim path disables the check.
func TestResolvePoolLeavesUptimeUnsetWithoutPolicy(t *testing.T) {
	tmpl := concurrentTemplate("cw2-tmpl", "cw2-runtime", "sandboxed", "workspace", 4)
	tmpl.Spec.ConcurrentWorkspacePolicy = &lennyv1.ConcurrentWorkspacePolicy{
		AcknowledgeProcessLevelIsolation: true,
	}
	c := k8sClient(t, warmPool("cw2-pool", "cw2-tmpl"), tmpl)

	got, err := podsession.ResolvePool(context.Background(), c, testNS, "cw2-runtime", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MaxPodUptimeSeconds != 0 {
		t.Errorf("MaxPodUptimeSeconds = %d, want 0 (cap unset)", got.MaxPodUptimeSeconds)
	}
}

func TestResolvePoolMatchesByRuntime(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "")
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
	_, err := podsession.ResolvePool(context.Background(), c, testNS, "other-runtime", "")
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
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "microvm")
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
	_, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
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
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
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
	c := k8sClient(t,
		warmPool("warming-pool", "warming-tmpl"),
		sandboxTemplate("warming-tmpl", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "warming-tmpl", "warming-pool", 2, 0, true)

	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
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
	c := k8sClient(t,
		warmPool("ready-pool", "ready-tmpl"),
		sandboxTemplate("ready-tmpl", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "ready-tmpl", "ready-pool", 3, 3, false)

	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
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
	c := k8sClient(t,
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
