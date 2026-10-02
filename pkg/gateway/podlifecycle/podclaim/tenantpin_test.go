// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// spec: 5.2 (Tenant pinning), 5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: an idle pod pinned to one tenant can be admitted for another
// tenant, a pod pinned `unassigned` is admitted, a pinned pod is admitted on a
// pool that keeps no runtime process, or AdmitTenantPin writes to the Pod or
// reads a SandboxClaim.
func TestAdmitTenantPinDecisions_spec_5_2(t *testing.T) {
	const pool = "pin-decide"
	base := newEnvtestClient(
		t,
		pinnedPod(pool, "pd-unpinned", ""),
		pinnedPod(pool, "pd-acme", "acme"),
		pinnedPod(pool, "pd-globex", "globex"),
		pinnedPod(pool, "pd-unassigned", "unassigned"),
	)
	rec := &pinRecorder{}
	c := rec.wrap(base)

	cases := []struct {
		pod   string
		keeps bool
		want  bool
	}{
		{"pd-unpinned", true, true},
		{"pd-acme", true, true},
		{"pd-absent", true, true},
		{"pd-globex", true, false},
		{"pd-unassigned", true, false},
		{"pd-unpinned", false, true},
		{"pd-absent", false, true},
		{"pd-acme", false, false},
		{"pd-globex", false, false},
		{"pd-unassigned", false, false},
	}
	for _, tc := range cases {
		got, err := podclaim.AdmitTenantPin(context.Background(), c, testNS, tc.pod,
			podclaim.ClaimRequest{Pool: pool, SessionID: "s", TenantID: "acme", KeepsRuntime: tc.keeps})
		if err != nil {
			t.Fatalf("AdmitTenantPin(%s, keeps=%v): %v", tc.pod, tc.keeps, err)
		}
		if got != tc.want {
			t.Errorf("AdmitTenantPin(%s, keeps=%v) = %v, want %v", tc.pod, tc.keeps, got, tc.want)
		}
	}
	patches, claimGets, creates := rec.snapshot()
	if len(patches) != 0 || len(claimGets) != 0 || len(creates) != 0 {
		t.Errorf("AdmitTenantPin issued writes or claim reads: patches=%v claimGets=%v creates=%v", patches, claimGets, creates)
	}
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Pool exhaustion behavior)
// diagnosis: a failed pin read is reported as pool exhaustion, lets the scan
// try a later candidate, creates a claim, or writes a drain request, so an
// API-server read failure surfaces as WARM_POOL_EXHAUSTED or binds a pod whose
// pin was never read.
func TestPinReadErrorEndsAcquisitionWithoutExhaustion_spec_5_2(t *testing.T) {
	injected := apierrors.NewInternalError(errors.New("injected pod read failure"))
	base := newEnvtestClient(
		t,
		sandboxIn(testPool, "a-pinerr", "idle"),
		sandboxIn(testPool, "b-ok", "idle"),
		pinnedPod(testPool, "a-pinerr", ""),
		pinnedPod(testPool, "b-ok", ""),
	)
	rec := &pinRecorder{failPodGet: map[string]error{"a-pinerr": injected}}
	c := rec.wrap(base)

	for _, keeps := range []bool{true, false} {
		rec.reset()
		claimer := &podclaim.Claimer{Client: c, Namespace: testNS}
		_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
			Pool: testPool, SessionID: "s", TenantID: "acme", KeepsRuntime: keeps,
		})
		if err == nil || !errors.Is(err, injected) {
			t.Fatalf("keeps=%v: Claim error = %v, want one wrapping the injected read failure", keeps, err)
		}
		if errors.Is(err, podclaim.ErrNoIdlePod) {
			t.Errorf("keeps=%v: a failed pin read must not be ErrNoIdlePod", keeps)
		}
		patches, _, creates := rec.snapshot()
		if len(creates) != 0 || len(patches) != 0 {
			t.Errorf("keeps=%v: creates=%v patches=%v, want none", keeps, creates, patches)
		}
		if !claimAbsent(t, base, "a-pinerr") || !claimAbsent(t, base, "b-ok") {
			t.Errorf("keeps=%v: a claim was created", keeps)
		}
	}

	rec.reset()
	slot := &podclaim.SlotClaimer{Client: c, Namespace: testNS, Counter: newCounter(t)}
	_, err := slot.ClaimSlot(context.Background(), slotReq("s-slot", "acme", 4))
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("ClaimSlot error = %v, want one wrapping the injected read failure", err)
	}
	for _, sentinel := range []error{podclaim.ErrNoIdlePod, podclaim.ErrNoConcurrentSlot, podclaim.ErrTenantMismatch} {
		if errors.Is(err, sentinel) {
			t.Errorf("ClaimSlot error matches %v; a failed pin read is no exhaustion sentinel", sentinel)
		}
	}
	if _, _, creates := rec.snapshot(); len(creates) != 0 {
		t.Errorf("ClaimSlot created claims %v on a failed pin read", creates)
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: a refused pod on a pool outside the process-reuse rule is not
// drained, a pod whose acquisition is in flight elsewhere is drained, or a
// failed claim read or drain stamp goes unlogged.
func TestDrainRefusedPodStampsOnlyUnclaimedPod_spec_5_2(t *testing.T) {
	const pool = "drain-refused"
	base := newEnvtestClient(
		t,
		pinnedPod(pool, "dr-free", "acme"),
		pinnedPod(pool, "dr-held", "acme"),
		pinnedPod(pool, "dr-claimerr", "acme"),
		pinnedPod(pool, "dr-patcherr", "acme"),
	)
	createBareClaim(t, base, "dr-held", "acme")
	rec := &pinRecorder{
		failClaimGet: map[string]error{podclaim.ClaimName("dr-claimerr"): apierrors.NewInternalError(errors.New("claim read boom"))},
		failPodPatch: map[string]error{"dr-patcherr": apierrors.NewInternalError(errors.New("patch boom"))},
	}
	c := rec.wrap(base)
	logs := captureSlog(t)
	ctx := context.Background()

	podclaim.DrainRefusedPod(ctx, c, testNS, pool, "dr-free", fixedNow)
	if !hasDrainRequest(t, base, "dr-free") {
		t.Error("unclaimed refused pod carries no drain request")
	}
	if got := drainRecords(logs(), "INFO", "dr-free"); len(got) != 1 {
		t.Errorf("want one Info record for the stamp, got %d", len(got))
	}

	podclaim.DrainRefusedPod(ctx, c, testNS, pool, "dr-held", fixedNow)
	if hasDrainRequest(t, base, "dr-held") || rec.patched("dr-held") {
		t.Error("a pod held by a SandboxClaim was patched")
	}
	if recs := logs(); len(recs) != 0 {
		t.Errorf("a claimed pod logged %d records, want none", len(recs))
	}

	podclaim.DrainRefusedPod(ctx, c, testNS, pool, "dr-claimerr", fixedNow)
	if rec.patched("dr-claimerr") {
		t.Error("a failed claim read still patched the Pod")
	}
	warns := drainRecords(logs(), "WARN", "dr-claimerr")
	if len(warns) != 1 || !errorMentions(warns[0], "claim read boom") {
		t.Errorf("want one Warn naming the claim read error, got %v", warns)
	}

	podclaim.DrainRefusedPod(ctx, c, testNS, pool, "dr-patcherr", fixedNow)
	warns = drainRecords(logs(), "WARN", "dr-patcherr")
	if len(warns) != 1 || !errorMentions(warns[0], "patch boom") {
		t.Errorf("want one Warn naming the stamp error, got %v", warns)
	}
}

// spec: 5.2 (Tenant pinning)
// diagnosis: the concurrent-slot idle pass places a slot on a pod pinned to
// another tenant, refuses a pod pinned to the request's own tenant, or
// drains a pod it refused.
func TestClaimSlotIdlePassAdmitsOwnTenantPinOnly_spec_5_2(t *testing.T) {
	claimer, c, _ := slotClaimerFor(
		t,
		sandboxIn(testPool, "a-other", "idle"),
		sandboxIn(testPool, "b-own", "idle"),
		pinnedPod(testPool, "a-other", "globex"),
		pinnedPod(testPool, "b-own", "acme"),
	)
	res, err := claimer.ClaimSlot(context.Background(), slotReq("s-1", "acme", 4))
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if res.SandboxName != "b-own" {
		t.Errorf("slot placed on %q, want b-own", res.SandboxName)
	}
	if !claimAbsent(t, c, "a-other") {
		t.Error("a claim was created on the pod pinned to globex")
	}
	if hasDrainRequest(t, c, "a-other") || hasDrainRequest(t, c, "b-own") {
		t.Error("the slot pass wrote a drain request")
	}
	// The slot path always passes KeepsRuntime; under the zero value both
	// pods would be refused.
	for _, pod := range []string{"a-other", "b-own"} {
		ok, err := podclaim.AdmitTenantPin(context.Background(), c, testNS, pod,
			podclaim.ClaimRequest{Pool: testPool, TenantID: "acme"})
		if err != nil || ok {
			t.Errorf("AdmitTenantPin(%s, zero KeepsRuntime) = %v, %v; want refused", pod, ok, err)
		}
	}
}

// spec: 5.2 (Tenant pinning)
// diagnosis: an acquisition on a pool that keeps its runtime process binds,
// stamps, or otherwise writes to a pod pinned to another tenant, which then
// leaves the pod's tenant before it is drained.
func TestKeptRuntimeRefusalWritesNothing_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn("kr-only", "kr1-a", "idle"),
		sandboxIn("kr-only", "kr1-b", "idle"),
		pinnedPod("kr-only", "kr1-a", "globex"),
		pinnedPod("kr-only", "kr1-b", "globex"),
		sandboxIn("kr-mixed", "kr2-a", "idle"),
		sandboxIn("kr-mixed", "kr2-b", "idle"),
		sandboxIn("kr-mixed", "kr2-z", "idle"),
		pinnedPod("kr-mixed", "kr2-a", "globex"),
		pinnedPod("kr-mixed", "kr2-b", "globex"),
		pinnedPod("kr-mixed", "kr2-z", ""),
	)
	rec := &pinRecorder{}
	claimer := &podclaim.Claimer{Client: rec.wrap(base), Namespace: testNS}

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: "kr-only", SessionID: "s1", TenantID: "acme", KeepsRuntime: true,
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("Claim over pods pinned elsewhere = %v, want ErrNoIdlePod", err)
	}
	if patches, _, creates := rec.snapshot(); len(patches) != 0 || len(creates) != 0 {
		t.Errorf("patches=%v creates=%v, want none", patches, creates)
	}

	rec.reset()
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: "kr-mixed", SessionID: "s2", TenantID: "acme", KeepsRuntime: true,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "kr2-z" {
		t.Errorf("bound %q, want the unpinned kr2-z", claim.Spec.SandboxRef)
	}
	if rec.patched("kr2-a") || rec.patched("kr2-b") {
		t.Error("a pod pinned to another tenant was patched")
	}
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Postgres-backed fallback claim)
// diagnosis: the idle scan's no-idle-pod result omits a pod it refused on the
// tenant pin or names one it did not refuse, so the Postgres fallback reads a
// refused pod again or skips an admissible one.
func TestIdleScanNoIdlePodResultNamesRefusedPods_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn(testPool, "n-a", "idle"),
		sandboxIn(testPool, "n-b", "idle"),
		sandboxIn(testPool, "n-taken", "idle"),
		pinnedPod(testPool, "n-a", "globex"),
		pinnedPod(testPool, "n-b", "globex"),
	)
	createBareClaim(t, base, "n-taken", "acme")
	claimer := &podclaim.Claimer{Client: base, Namespace: testNS}
	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "s", TenantID: "acme", KeepsRuntime: true,
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("Claim = %v, want ErrNoIdlePod", err)
	}
	var noIdle *podclaim.NoIdlePodError
	if !errors.As(err, &noIdle) {
		t.Fatalf("Claim error %T is not a *NoIdlePodError", err)
	}
	got := slices.Sorted(slices.Values(noIdle.Refused))
	if !slices.Equal(got, []string{"n-a", "n-b"}) {
		t.Errorf("Refused = %v, want [n-a n-b]", got)
	}
}
