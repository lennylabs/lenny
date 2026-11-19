// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/runtime/poolstatus"
)

// podSet returns the PodSet of one pool: it is named after the pool, carries
// the runtime and isolation profile ResolvePool matches on, and carries the
// warm-pool targets.
func podSet(ns, name, runtimeRef, isolation string) *lennyv1.PodSet {
	return &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: lennyv1.PodSetSpec{
			RuntimeRef:       runtimeRef,
			IsolationProfile: isolation,
			MinWarm:          1,
			MaxWarm:          5,
		},
	}
}

func spiffeBindingPodSet(ns, name, runtimeRef, isolation, spiffeBinding string) *lennyv1.PodSet {
	ps := podSet(ns, name, runtimeRef, isolation)
	ps.Spec.SpiffeBinding = spiffeBinding
	return ps
}

func servicePodSet(ns, name, runtimeRef, isolation string, maxConcurrent int32) *lennyv1.PodSet {
	ps := podSet(ns, name, runtimeRef, isolation)
	ps.Spec.ExecutionMode = "service"
	ps.Spec.MaxConcurrent = maxConcurrent
	return ps
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
// the poolstore mirror, not the PodSet, so ResolvePool reads them
// through the PoolPolicyReader keyed by the resolved pool name.
func TestResolvePoolFoldsPolicyMirror(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "conc-pool", "conc-runtime", "microvm")
	c := k8sClient(t, ps)
	slotRetries := 3
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"conc-pool": {
			MaxConcurrentSessions: 4,
			AllowCrossTenantReuse: true,
			MaxPodUptimeSeconds:   86400,
			CleanupCommands:       []string{"rm -rf /tmp/work", "reset"},
			CleanupTimeoutSeconds: 30,
			SlotRetries:           &slotRetries,
		},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "conc-runtime", "microvm", "")
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
	// The §5.2 whole-pod scrub cleanup commands and their cap fold in beside
	// AllowCrossTenantReuse so the recycle-path Shutdown can deliver them.
	if got.CleanupTimeoutSeconds != 30 {
		t.Errorf("CleanupTimeoutSeconds = %d, want 30 (from the mirror)", got.CleanupTimeoutSeconds)
	}
	if len(got.CleanupCommands) != 2 || got.CleanupCommands[0] != "rm -rf /tmp/work" || got.CleanupCommands[1] != "reset" {
		t.Errorf("CleanupCommands = %v, want [rm -rf /tmp/work reset] (from the mirror)", got.CleanupCommands)
	}
	// spec: §5.2 (Slot retry policy) — the slot retry budget folds in from
	// the mirror so the concurrent-workspace retry loop reads the pool value.
	if got.SlotRetries == nil || *got.SlotRetries != 3 {
		t.Errorf("SlotRetries = %v, want 3 (from the mirror)", got.SlotRetries)
	}
}

// spec: §10.1.7 / §5.2 — the per-pool checkpointGrantWindow
// override is declared on the PodSet, so ResolvePool copies it
// onto the PoolMatch for the checkpoint driver to prefer over the
// deployment-wide default. A pool that declares no override leaves the
// field nil so the driver falls back to the deployment-wide default.
func TestResolvePoolCopiesCheckpointGrantWindowFromTemplate_spec_10_1(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "cp-pool", "cp-runtime", "microvm")
	window := int32(8)
	ps.Spec.CheckpointGrantWindow = &window
	c := k8sClient(t, ps)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "cp-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.CheckpointGrantWindow == nil {
		t.Fatal("CheckpointGrantWindow = nil, want the PodSet override 8")
	}
	if *got.CheckpointGrantWindow != 8 {
		t.Errorf("CheckpointGrantWindow = %d, want 8 (from the PodSet)", *got.CheckpointGrantWindow)
	}
}

// spec: §10.1.7 / §5.2 — a pool that declares no per-pool
// checkpointGrantWindow leaves the PoolMatch field nil so the checkpoint
// driver resolves the deployment-wide default.
func TestResolvePoolLeavesCheckpointGrantWindowNilWithoutOverride_spec_10_1(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "cp-pool", "cp-runtime", "microvm")
	c := k8sClient(t, ps)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "cp-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.CheckpointGrantWindow != nil {
		t.Errorf("CheckpointGrantWindow = %d, want nil (no per-pool override)", *got.CheckpointGrantWindow)
	}
}

// spec: §10.1.7 / §5.2 — a poolstore mirror row that carries a
// checkpointGrantWindow override wins over the PodSet-derived value, so
// the gateway-enforced pool policy governs the driver's per-pool window.
func TestResolvePoolMirrorOverridesCheckpointGrantWindow_spec_10_1(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "cp-pool", "cp-runtime", "microvm")
	crdWindow := int32(8)
	ps.Spec.CheckpointGrantWindow = &crdWindow
	c := k8sClient(t, ps)
	mirrorWindow := int32(2)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"cp-pool": {CheckpointGrantWindow: &mirrorWindow},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "cp-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.CheckpointGrantWindow == nil || *got.CheckpointGrantWindow != 2 {
		t.Errorf("CheckpointGrantWindow = %v, want 2 (mirror override wins over the PodSet)", got.CheckpointGrantWindow)
	}
}

// spec: §5.2 (Recycle lifecycle): a `standard` or `in-place` recycling
// pool keeps the pod for its tenant through the claim's `reserved` state,
// as the microvm cross-tenant variants do.
//
// diagnosis: a failure here means a standard-profile recycling pool's
// PoolMatch.Recycle never turns true, so the release path
// (BindResult.Recycle, resolve.go's caller) always retires the pod
// instead of scrubbing and reusing it — the PodSet carries
// SessionPolicy.Recycle only for the microvm scrub variants
// (sessionPolicyToCRD), so a standard-profile pool's recycle.enabled
// reaches the gateway solely through this mirror fold.
func TestResolvePoolFoldsRecycleForStandardScrubProfile(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "reuse-pool", "reuse-runtime", "standard")
	c := k8sClient(t, ps)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"reuse-pool": {Recycle: true},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "reuse-runtime", "standard", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if !got.Recycle {
		t.Error("Recycle = false, want true (folded from the mirror for a standard-profile recycling pool)")
	}
}

// spec: §5.2 (Recycle lifecycle; Deployer acknowledgment (runtime process
// kept across sessions)), §7.1 (sessionIsolationLevel) — podReuse is true
// exactly when the pool's recycle.enabled is true, and the claim made at
// session creation uses the same resolution as the isolation level. The
// stored recycle.enabled (the poolstore mirror) therefore decides
// PoolMatch.Recycle whenever the pool has a mirror row. The CRD's
// sessionPolicy.recycle block is a proxy rendered for a scrub profile or a
// runtime-process lifetime, and it lags an admitted pool update until the
// PoolScalingController's next reconcile; it decides only when no mirror
// row exists.
//
// diagnosis: a failure on a "stale CRD" row means a session created right
// after an update to recycle.enabled: false reports podReuse: true and
// recycles its pod at release, because the CRD block from before the update
// overrode the stored policy (the tier-9
// TestPoolEditOutsideRuleNeverDispatchesIntoKeptRuntime symptom). A failure
// on a "no mirror row" row means the Postgres-less posture lost its only
// recycle signal.
func TestResolvePoolRecycleFollowsStoredPolicyOverCRDBlock_spec_5_2(t *testing.T) {
	keep := &lennyv1.SessionPolicy{Recycle: &lennyv1.RecyclePolicy{RuntimeProcess: "keep"}}
	vmRestart := &lennyv1.SessionPolicy{Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "vm-restart"}}
	cases := []struct {
		name      string
		isolation string
		crd       *lennyv1.SessionPolicy
		mirror    *podsession.PoolPolicyMirror // nil: the reader has no row for the pool
		nilReader bool
		want      bool
	}{
		{
			name: "stale CRD keep block, stored recycle disabled", isolation: "standard",
			crd: keep, mirror: &podsession.PoolPolicyMirror{Recycle: false}, want: false,
		},
		{
			name: "stale CRD vm-restart block, stored recycle disabled", isolation: "microvm",
			crd: vmRestart, mirror: &podsession.PoolPolicyMirror{AllowCrossTenantReuse: true}, want: false,
		},
		{
			name: "CRD vm-restart block, stored recycle enabled", isolation: "microvm",
			crd: vmRestart, mirror: &podsession.PoolPolicyMirror{Recycle: true, AllowCrossTenantReuse: true}, want: true,
		},
		{
			name: "CRD not yet rendered, stored recycle enabled", isolation: "standard",
			crd: nil, mirror: &podsession.PoolPolicyMirror{Recycle: true}, want: true,
		},
		{
			name: "no CRD block, stored recycle disabled", isolation: "standard",
			crd: nil, mirror: &podsession.PoolPolicyMirror{}, want: false,
		},
		{
			name: "no mirror row falls back to the CRD block", isolation: "standard",
			crd: keep, mirror: nil, want: true,
		},
		{
			name: "no reader falls back to the CRD block", isolation: "microvm",
			crd: vmRestart, nilReader: true, want: true,
		},
		{
			name: "no reader and no CRD block", isolation: "standard",
			crd: nil, nilReader: true, want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := testNamespace(t)
			ps := podSet(ns, "recycle-pool", "recycle-runtime", tc.isolation)
			ps.Spec.SessionPolicy = tc.crd
			c := k8sClient(t, ps)
			var policy podsession.PoolPolicyReader
			if !tc.nilReader {
				mirrors := map[string]podsession.PoolPolicyMirror{}
				if tc.mirror != nil {
					mirrors["recycle-pool"] = *tc.mirror
				}
				policy = fakePolicyReader{mirrors: mirrors}
			}

			got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "recycle-runtime", tc.isolation, "")
			if err != nil {
				t.Fatalf("ResolvePool: %v", err)
			}
			if got.Recycle != tc.want {
				t.Errorf("Recycle = %v, want %v", got.Recycle, tc.want)
			}
		})
	}
}

// spec: §5.2 (whole-pod scrub cleanup commands, gateway-enforced subset)
// TestResolvePoolFoldsCleanupCommands covers the §5.2 whole-pod scrub
// cleanup commands and their aggregate cap reaching PoolMatch from the
// poolstore sessionPolicy mirror, which the recycle-path Shutdown delivers to
// the adapter. A pool with no mirror row leaves both empty rather than
// erroring, matching how AllowCrossTenantReuse is folded.
func TestResolvePoolFoldsCleanupCommands(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "scrub-pool", "scrub-runtime", "microvm")
	c := k8sClient(t, ps)

	// A mirror carrying the two cleanup fields folds them onto PoolMatch.
	withMirror := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"scrub-pool": {CleanupCommands: []string{"purge"}, CleanupTimeoutSeconds: 12},
	}}
	got, err := podsession.ResolvePool(context.Background(), c, withMirror, ns, "scrub-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool (mirror): %v", err)
	}
	if got.CleanupTimeoutSeconds != 12 || len(got.CleanupCommands) != 1 || got.CleanupCommands[0] != "purge" {
		t.Errorf("cleanup fields = %v / %d, want [purge] / 12 (from the mirror)", got.CleanupCommands, got.CleanupTimeoutSeconds)
	}

	// The no-mirror-row case leaves the cleanup fields empty.
	noMirror := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}
	got, err = podsession.ResolvePool(context.Background(), c, noMirror, ns, "scrub-runtime", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool (no mirror): %v", err)
	}
	if len(got.CleanupCommands) != 0 || got.CleanupTimeoutSeconds != 0 {
		t.Errorf("cleanup fields = %v / %d, want empty without a mirror row", got.CleanupCommands, got.CleanupTimeoutSeconds)
	}
}

// spec: §5.2 / §4.6.1 (onPoolExhausted, maxQueueWaitSeconds folded from the
// gateway-enforced mirror)
// TestResolvePoolFoldsPoolExhaustionDisposition covers the queue-vs-reject
// dispatch fields reaching the PoolMatch from the poolstore mirror so the
// start path's claim queue reads the gateway-enforced values rather than the
// always-empty PodSet.
func TestResolvePoolFoldsPoolExhaustionDisposition(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "queue-pool", "queue-runtime", "sandboxed")
	c := k8sClient(t, ps)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"queue-pool": {OnPoolExhausted: "queue", MaxQueueWaitSeconds: 45},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "queue-runtime", "sandboxed", "")
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
	ns := testNamespace(t)
	ps := podSet(ns, "reject-pool", "reject-runtime", "sandboxed")
	c := k8sClient(t, ps)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "reject-runtime", "sandboxed", "")
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
	ns := testNamespace(t)
	c := k8sClient(
		t,
		servicePodSet(ns, "svc-pool", "load-svc-runtime", "sandboxed", 8),
	)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
		"svc-pool": {MaxConcurrent: 16},
	}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "load-svc-runtime", "sandboxed", "")
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
	ns := testNamespace(t)
	ps := podSet(ns, "err-pool", "err-runtime", "sandboxed")
	c := k8sClient(t, ps)
	policy := fakePolicyReader{err: errors.New("mirror down")}

	_, err := podsession.ResolvePool(context.Background(), c, policy, ns, "err-runtime", "sandboxed", "")
	if err == nil {
		t.Fatal("ResolvePool: want a propagated error when the policy mirror read fails")
	}
}

// spec: §5.2 (gateway-enforced subset, missing mirror row)
// TestResolvePoolKeepsCRDDefaultsWhenMirrorAbsent covers the
// Postgres-only / CRD-only posture: a pool with no mirror row leaves the
// dispatch fields at their CRD-derived defaults instead of erroring.
func TestResolvePoolKeepsCRDDefaultsWhenMirrorAbsent(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "nomirror-pool", "nomirror-runtime", "sandboxed")
	c := k8sClient(t, ps)
	policy := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}

	got, err := podsession.ResolvePool(context.Background(), c, policy, ns, "nomirror-runtime", "sandboxed", "")
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
	ns := testNamespace(t)
	c := k8sClient(
		t,
		servicePodSet(ns, "svc-pool", "load-svc-runtime", "sandboxed", 8),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "load-svc-runtime", "sandboxed", "")
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
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "session-pool", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "sandboxed", "")
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
	ns := testNamespace(t)
	ps := servicePodSet(ns, "recycle-pool", "recycle-runtime", "microvm", 1)
	ps.Spec.ExecutionMode = "session"
	ps.Spec.SessionPolicy = &lennyv1.SessionPolicy{
		Recycle: &lennyv1.RecyclePolicy{
			ScrubProfile:                    "in-place",
			AcknowledgeMicrovmResidualState: true,
		},
	}
	c := k8sClient(t, ps)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "recycle-runtime", "microvm", "")
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
	ns := testNamespace(t)
	ps := podSet(ns, "plain-pool", "plain-runtime", "sandboxed")
	c := k8sClient(t, ps)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "plain-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.MicrovmScrubMode != "" {
		t.Errorf("MicrovmScrubMode = %q, want empty (no recycle block)", got.MicrovmScrubMode)
	}
}

func TestResolvePoolMatchesByRuntime(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "claude-pool", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got.Pool)
	}
}

// spec: §4.9 — ResolvePool copies the pool's PodSet spiffeBinding
// onto the PoolMatch so the session-start credential-delivery gate can pair
// the bound pod's binding with each resolved CredentialPool's effective
// deliveryMode. TestResolvePoolCopiesSpiffeBinding pins the populated case.
func TestResolvePoolCopiesSpiffeBinding(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		spiffeBindingPodSet(ns, "claude-pool", "claude-code", "standard", "disabled"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.SpiffeBinding != "disabled" {
		t.Errorf("SpiffeBinding = %q, want disabled", got.SpiffeBinding)
	}
}

// spec: §4.9 — a PodSet that declares no spiffeBinding leaves the
// PoolMatch field at its empty default rather than fabricating a value the
// credential-delivery gate would misread.
func TestResolvePoolLeavesSpiffeBindingEmptyByDefault(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "claude-pool", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.SpiffeBinding != "" {
		t.Errorf("SpiffeBinding = %q, want empty default", got.SpiffeBinding)
	}
}

func TestResolvePoolNoMatch(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "claude-pool", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, nil, ns, "other-runtime", "", "")
	if !errors.Is(err, podsession.ErrNoMatchingPool) {
		t.Errorf("error = %v, want ErrNoMatchingPool", err)
	}
}

func TestResolvePoolDisambiguatesByIsolation(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "claude-gvisor", "claude-code", "sandboxed"),
		podSet(ns, "claude-kata", "claude-code", "microvm"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "microvm", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-kata" {
		t.Errorf("resolved pool = %q, want claude-kata", got.Pool)
	}
}

func TestResolvePoolAmbiguous(t *testing.T) {
	ns := testNamespace(t)
	// Two pools with the same runtime and isolation: the gateway cannot
	// pick one.
	c := k8sClient(
		t,
		podSet(ns, "pool-a", "claude-code", "sandboxed"),
		podSet(ns, "pool-b", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "sandboxed", "")
	if !errors.Is(err, podsession.ErrAmbiguousPool) {
		t.Errorf("error = %v, want ErrAmbiguousPool", err)
	}
}

// seedPoolStatus stamps the §5.2 PoolWarmingUp condition and the warm/ready
// counts on the pool's PodSet, mirroring what the WarmPoolController writes,
// so ResolvePool and poolstatus.Lookup can be exercised against a realistic
// bootstrap-window status.
func seedPoolStatus(t *testing.T, c client.Client, poolName string, warm, ready int32, warming bool) {
	t.Helper()
	ns := testNamespace(t)
	ctx := context.Background()

	var pool lennyv1.PodSet
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: poolName}, &pool); err != nil {
		t.Fatalf("get pool %s: %v", poolName, err)
	}
	status, reason := metav1.ConditionFalse, "Available"
	if warming {
		status, reason = metav1.ConditionTrue, "Provisioning"
	}
	pool.Status.Conditions = []metav1.Condition{{
		Type:               "PoolWarmingUp",
		Status:             status,
		Reason:             reason,
		Message:            "test",
		LastTransitionTime: metav1.Now(),
	}}
	pool.Status.WarmCount, pool.Status.ReadyCount = warm, ready
	if err := c.Status().Update(ctx, &pool); err != nil {
		t.Fatalf("seed pool status %s: %v", poolName, err)
	}
}

// spec: §5.2 — ResolvePool surfaces the PoolWarmingUp
// condition and the warming-pod count so the start path can answer a
// session creation against a bootstrapping pool with the 503 Pool Not
// Ready response instead of burning a claim attempt.
func TestResolvePoolSurfacesPoolWarmingUp(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "warming-pool", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "warming-pool", 2, 0, true)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "sandboxed", "")
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

// spec: §5.2 — a pool with idle pods is not warming; PodsWarming
// clamps at zero.
func TestResolvePoolNotWarmingWhenReady(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "ready-pool", "claude-code", "sandboxed"),
	)
	seedPoolStatus(t, c, "ready-pool", 3, 3, false)

	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "sandboxed", "")
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

// spec: §5.2 — poolstatus.Lookup feeds the admin pool GET's
// poolCondition and idlePodCount. One client carries a warming pool, a
// ready pool, and an absent pool so the three cases share one envtest.
func TestPoolStatusLookup(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "warming-p", "rt-warming", "sandboxed"),
		podSet(ns, "ready-p", "rt-ready", "sandboxed"),
	)
	seedPoolStatus(t, c, "warming-p", 2, 0, true)
	seedPoolStatus(t, c, "ready-p", 4, 4, false)

	l := poolstatus.Lookup{Reader: c, Namespace: ns}
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
	ns := testNamespace(t)
	c := k8sClient(
		t,
		podSet(ns, "pool-a", "claude-code", "sandboxed"),
		podSet(ns, "pool-b", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, nil, ns, "claude-code", "sandboxed", "pool-b")
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
	ns := testNamespace(t)
	c := k8sClient(
		t,
		// A pool backed by claude-code / sandboxed.
		podSet(ns, "claude-pool", "claude-code", "sandboxed"),
		// A pool backed by a different runtime, isolation microvm.
		podSet(ns, "other-pool", "other-runtime", "microvm"),
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
			_, err := podsession.ResolvePool(context.Background(), c, nil, ns,
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

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: the resolved pool match drops or crosses the predicate pair, so
// the claim refuses a pod its tenant may reuse, admits a used `keep` pod on a
// pool that does not require the acknowledgment, or reports a pool with no
// mirror row or no reader as one that reuses pods.
func TestResolvePoolCopiesReuseAndAcknowledgmentPredicates_spec_5_2(t *testing.T) {
	ns := testNamespace(t)
	ps := podSet(ns, "keep-pool", "keep-runtime", "sandboxed")
	c := k8sClient(t, ps)

	for _, tc := range []struct{ reuse, ack bool }{{true, true}, {true, false}, {false, false}} {
		withMirror := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{
			"keep-pool": {ReusesPods: tc.reuse, RequiresProcessAcknowledgment: tc.ack},
		}}
		got, err := podsession.ResolvePool(context.Background(), c, withMirror, ns, "keep-runtime", "sandboxed", "")
		if err != nil {
			t.Fatalf("ResolvePool (mirror %+v): %v", tc, err)
		}
		if got.ReusesPods != tc.reuse || got.RequiresProcessAcknowledgment != tc.ack {
			t.Errorf("mirror %+v: match = (%v, %v)", tc, got.ReusesPods, got.RequiresProcessAcknowledgment)
		}
	}

	noRow := fakePolicyReader{mirrors: map[string]podsession.PoolPolicyMirror{}}
	got, err := podsession.ResolvePool(context.Background(), c, noRow, ns, "keep-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool (no mirror row): %v", err)
	}
	if got.ReusesPods || got.RequiresProcessAcknowledgment {
		t.Error("a pool with no mirror row reads a predicate true, want both false")
	}

	got, err = podsession.ResolvePool(context.Background(), c, nil, ns, "keep-runtime", "sandboxed", "")
	if err != nil {
		t.Fatalf("ResolvePool (no reader): %v", err)
	}
	if got.ReusesPods || got.RequiresProcessAcknowledgment {
		t.Error("a resolution with no reader reads a predicate true, want both false")
	}
}
