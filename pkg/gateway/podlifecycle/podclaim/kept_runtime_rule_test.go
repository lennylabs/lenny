// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// The cases in this file pin the acquisition rule for a pool whose
// configuration keeps no runtime process across sessions (KeepsRuntime
// false): every acquisition refuses a pod that has served a session, drains
// a refused idle pod no claim holds, and ends a reserved hold with the
// precondition-guarded DELETE instead of rebinding it.

// claimOutside runs one Claim with KeepsRuntime false for acme.
func claimOutside(claimer *podclaim.Claimer, pool, session string) (*lennyv1.SandboxClaim, error) {
	return claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: pool, SessionID: session, TenantID: "acme",
	})
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions)), 4.6.1 (Reserved hold)
// diagnosis: a pod that served a session is dispatched into, or left
// undrained, on a pool whose configuration keeps no runtime process, or an
// acquisition in progress on another replica loses its pod.
func TestOutsideRuleScanRefusesAndDrainsServedPods_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn("or-drain", "or1-a", "idle"),
		sandboxIn("or-drain", "or1-b", "idle"),
		sandboxIn("or-drain", "or1-z", "idle"),
		pinnedPod("or-drain", "or1-a", "acme"),
		pinnedPod("or-drain", "or1-b", "acme"),
		pinnedPod("or-drain", "or1-z", ""),
		sandboxIn("or-held", "or2-a", "idle"),
		sandboxIn("or-held", "or2-b", "idle"),
		sandboxIn("or-held", "or2-z", "idle"),
		pinnedPod("or-held", "or2-a", "acme"),
		pinnedPod("or-held", "or2-b", "acme"),
		pinnedPod("or-held", "or2-z", ""),
	)
	createBareClaim(t, base, "or2-a", "acme")
	createBareClaim(t, base, "or2-b", "acme")
	claimer := &podclaim.Claimer{Client: base, Namespace: testNS}

	claim, err := claimOutside(claimer, "or-drain", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "or1-z" {
		t.Errorf("bound %q, want the unpinned or1-z", claim.Spec.SandboxRef)
	}
	for _, pod := range []string{"or1-a", "or1-b"} {
		if !claimAbsent(t, base, pod) {
			t.Errorf("a claim was created on the served pod %s", pod)
		}
		if !hasDrainRequest(t, base, pod) {
			t.Errorf("served pod %s carries no drain request", pod)
		}
	}

	claim, err = claimOutside(claimer, "or-held", "s2")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "or2-z" {
		t.Errorf("bound %q, want the unpinned or2-z", claim.Spec.SandboxRef)
	}
	for _, pod := range []string{"or2-a", "or2-b"} {
		if hasDrainRequest(t, base, pod) {
			t.Errorf("pod %s held by a claim was drained", pod)
		}
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions)), 4.6.1 (Reserved hold)
// diagnosis: on a pool whose configuration keeps no runtime process, a
// reserved pod is rebound to its tenant's next session, its hold survives the
// acquisition, a drain request lands before the pod returns to idle, or the
// returned idle pod is never drained.
func TestOutsideRuleEndsReservedHoldInsteadOfRebinding_spec_4_6_1(t *testing.T) {
	base := newEnvtestClient(
		t,
		reservedIn("rh-pool", "rh-held", "acme"),
		pinnedPod("rh-pool", "rh-held", "acme"),
		sandboxIn("rh-pool", "rh-z", "idle"),
		pinnedPod("rh-pool", "rh-z", ""),
		reservedIn("rh-solo", "rh2-held", "acme"),
		pinnedPod("rh-solo", "rh2-held", "acme"),
	)
	seedReservedClaim(t, base, "rh-held", "acme", fixedNow, time.Minute)
	seedReservedClaim(t, base, "rh2-held", "acme", fixedNow, time.Minute)
	var rebound []string
	claimer := &podclaim.Claimer{
		Client: base, Namespace: testNS,
		Now:      func() time.Time { return fixedNow.Add(5 * time.Second) },
		OnRebind: func(pod string) { rebound = append(rebound, pod) },
	}

	claim, err := claimOutside(claimer, "rh-pool", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "rh-z" {
		t.Errorf("bound %q, want rh-z (no rebind outside the rule)", claim.Spec.SandboxRef)
	}
	if len(rebound) != 0 {
		t.Errorf("OnRebind fired for %v", rebound)
	}
	if !claimAbsent(t, base, "rh-held") {
		t.Error("the reserved claim survived the acquisition")
	}
	if hasDrainRequest(t, base, "rh-held") {
		t.Error("a drain request landed while the pod still projected reserved")
	}

	if _, err := claimOutside(claimer, "rh-solo", "s2"); !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("Claim with only a reserved pod = %v, want ErrNoIdlePod", err)
	}
	if !claimAbsent(t, base, "rh2-held") {
		t.Error("the sole reserved claim survived the acquisition")
	}

	// The occupancy projection returns the pod to idle; the next acquisition
	// refuses it and requests its drain.
	setSandboxPhase(t, base, "rh2-held", "idle")
	if _, err := claimOutside(claimer, "rh-solo", "s3"); !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("second Claim = %v, want ErrNoIdlePod", err)
	}
	if !hasDrainRequest(t, base, "rh2-held") {
		t.Error("the returned idle pod carries no drain request")
	}
}

// spec: 4.6.1 (Reserved hold)
// diagnosis: ending reserved holds disturbs a claim that a rebind already
// moved off reserved, writes to a pod whose Sandbox projects reserved, or a
// lost precondition race deletes a rebound claim.
func TestOutsideRuleHoldEndSkipsMovedClaimsAndHonoursPrecondition_spec_4_6_1(t *testing.T) {
	base := newEnvtestClient(
		t,
		reservedIn("hp-pool", "hp-bound", "acme"),
		pinnedPod("hp-pool", "hp-bound", "acme"),
		reservedIn("hp-pool", "hp-noclaim", "acme"),
		pinnedPod("hp-pool", "hp-noclaim", "acme"),
		sandboxIn("hp-pool", "hp-z", "idle"),
		reservedIn("hp-race", "hr-held", "acme"),
		pinnedPod("hp-race", "hr-held", "acme"),
		sandboxIn("hp-race", "hr-z", "idle"),
	)
	createBareClaim(t, base, "hp-bound", "acme")
	if err := podclaim.WriteBoundStatus(context.Background(), base, testNS, podclaim.ClaimName("hp-bound")); err != nil {
		t.Fatalf("seed bound: %v", err)
	}
	var before lennyv1.SandboxClaim
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName("hp-bound")}, &before); err != nil {
		t.Fatalf("get bound claim: %v", err)
	}
	claimer := &podclaim.Claimer{Client: base, Namespace: testNS, Now: func() time.Time { return fixedNow }}
	claim, err := claimOutside(claimer, "hp-pool", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "hp-z" {
		t.Errorf("bound %q, want hp-z", claim.Spec.SandboxRef)
	}
	var after lennyv1.SandboxClaim
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName("hp-bound")}, &after); err != nil {
		t.Fatalf("the bound claim was removed: %v", err)
	}
	if after.ResourceVersion != before.ResourceVersion {
		t.Errorf("bound claim resourceVersion %s → %s; it must be untouched", before.ResourceVersion, after.ResourceVersion)
	}
	if hasDrainRequest(t, base, "hp-bound") || hasDrainRequest(t, base, "hp-noclaim") {
		t.Error("a pod projecting reserved was drained")
	}

	// A writer changes the claim between the read and the DELETE, so the
	// DELETE fails its resourceVersion precondition and the hold end aborts.
	seedReservedClaim(t, base, "hr-held", "acme", fixedNow, time.Minute)
	rec := &pinRecorder{onClaimDelete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if obj.GetName() == podclaim.ClaimName("hr-held") {
			bump := client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"labels":{"test.lenny.dev/raced":"true"}}}`))
			if err := cl.Patch(ctx, &lennyv1.SandboxClaim{ObjectMeta: obj.(*lennyv1.SandboxClaim).ObjectMeta}, bump); err != nil {
				return err
			}
		}
		return cl.Delete(ctx, obj, opts...)
	}}
	raced := &podclaim.Claimer{Client: rec.wrap(base), Namespace: testNS, Now: func() time.Time { return fixedNow }}
	claim, err = claimOutside(raced, "hp-race", "s2")
	if err != nil {
		t.Fatalf("Claim under the precondition race: %v", err)
	}
	if claim.Spec.SandboxRef != "hr-z" {
		t.Errorf("bound %q, want hr-z", claim.Spec.SandboxRef)
	}
	if claimAbsent(t, base, "hr-held") {
		t.Error("the DELETE deleted a claim whose resourceVersion changed")
	}
	if hasDrainRequest(t, base, "hr-held") {
		t.Error("the raced pod was drained")
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: a failed drain stamp fails an acquisition that had an admissible
// pod, goes unlogged, or is never retried by the next acquisition.
func TestOutsideRuleFailedStampIsLoggedAndRetried_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn("fs-pool", "fs-a", "idle"),
		pinnedPod("fs-pool", "fs-a", "acme"),
		sandboxIn("fs-pool", "fs-z", "idle"),
		sandboxIn("fs-solo", "fs2-a", "idle"),
		pinnedPod("fs-solo", "fs2-a", "acme"),
	)
	boom := apierrors.NewInternalError(errors.New("pod patch boom"))
	rec := &pinRecorder{failPodPatch: map[string]error{"fs-a": boom, "fs2-a": boom}}
	logs := captureSlog(t)
	claimer := &podclaim.Claimer{Client: rec.wrap(base), Namespace: testNS}

	claim, err := claimOutside(claimer, "fs-pool", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "fs-z" {
		t.Errorf("bound %q, want fs-z", claim.Spec.SandboxRef)
	}
	if !claimAbsent(t, base, "fs-a") {
		t.Error("a claim was created on the refused pod")
	}
	if warns := drainRecords(logs(), "WARN", "fs-a"); len(warns) != 1 {
		t.Errorf("want one Warn for the failed stamp, got %d", len(warns))
	}

	if _, err := claimOutside(claimer, "fs-solo", "s2"); !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("Claim with only the refused pod = %v, want ErrNoIdlePod rather than the Patch error", err)
	}

	retry := &podclaim.Claimer{Client: base, Namespace: testNS}
	if _, err := claimOutside(retry, "fs-solo", "s3"); !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("retry Claim = %v, want ErrNoIdlePod", err)
	}
	if !hasDrainRequest(t, base, "fs2-a") {
		t.Error("the next acquisition did not stamp the refused pod")
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions)), 4.6.1 (Reserved hold)
// diagnosis: a failed claim read during the drain fails an acquisition that
// had an admissible pod or stamps the pod anyway, or a failed read or delete
// while ending a reserved hold lets the acquisition proceed.
func TestOutsideRuleClaimReadFailures_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn("cr-pool", "cr-a", "idle"),
		pinnedPod("cr-pool", "cr-a", "acme"),
		sandboxIn("cr-pool", "cr-z", "idle"),
		reservedIn("cr-res", "cr2-held", "acme"),
		pinnedPod("cr-res", "cr2-held", "acme"),
		sandboxIn("cr-res", "cr2-z", "idle"),
	)
	seedReservedClaim(t, base, "cr2-held", "acme", fixedNow, time.Minute)
	boom := apierrors.NewInternalError(errors.New("claim read boom"))
	rec := &pinRecorder{failClaimGet: map[string]error{podclaim.ClaimName("cr-a"): boom}}
	logs := captureSlog(t)
	claimer := &podclaim.Claimer{Client: rec.wrap(base), Namespace: testNS}

	claim, err := claimOutside(claimer, "cr-pool", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "cr-z" {
		t.Errorf("bound %q, want cr-z", claim.Spec.SandboxRef)
	}
	if hasDrainRequest(t, base, "cr-a") {
		t.Error("a pod whose claim read failed was stamped")
	}
	if warns := drainRecords(logs(), "WARN", "cr-a"); len(warns) != 1 {
		t.Errorf("want one Warn for the failed claim read, got %d", len(warns))
	}

	rec.failClaimGet = map[string]error{podclaim.ClaimName("cr2-held"): boom}
	if _, err := claimOutside(claimer, "cr-res", "s2"); err == nil || !errors.Is(err, boom) {
		t.Fatalf("Claim with a failed reserved-claim read = %v, want the read error", err)
	}
	if claimAbsent(t, base, "cr2-held") || !claimAbsent(t, base, "cr2-z") {
		t.Error("a failed reserved-claim read must keep the hold and create no claim")
	}

	rec.failClaimGet = nil
	deleteBoom := apierrors.NewInternalError(errors.New("delete boom"))
	rec.onClaimDelete = func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if obj.GetName() == podclaim.ClaimName("cr2-held") {
			return deleteBoom
		}
		return cl.Delete(ctx, obj, opts...)
	}
	if _, err := claimOutside(claimer, "cr-res", "s3"); err == nil || !errors.Is(err, deleteBoom) {
		t.Fatalf("Claim with a failed hold-end DELETE = %v, want the delete error", err)
	}
	if claimAbsent(t, base, "cr2-held") || !claimAbsent(t, base, "cr2-z") || hasDrainRequest(t, base, "cr2-held") {
		t.Error("a failed hold-end DELETE must keep the hold, create no claim, and write no annotation")
	}
}
