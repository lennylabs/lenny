// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// The cases in this file pin the per-pod refusal of a `keep` pod on a pool
// that reuses pods but does not require the runtime-process acknowledgment,
// such as a sequential pool edited from `keep` to `restart`: an acquisition
// refuses and drains a used `keep` pod, admits a `restart` pod, and ends the
// hold of a reserved `keep` pod instead of rebinding it.

// claimRestartPool runs one Claim for acme on a pool that reuses pods and
// does not require the runtime-process acknowledgment.
func claimRestartPool(claimer *podclaim.Claimer, pool, session string) error {
	_, err := claimRestartPoolClaim(claimer, pool, session)
	return err
}

func claimRestartPoolClaim(claimer *podclaim.Claimer, pool, session string) (string, error) {
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: pool, SessionID: session, TenantID: "acme", ReusesPods: true,
	})
	if err != nil {
		return "", err
	}
	return claim.Spec.SandboxRef, nil
}

// spec: §5.2 (Runtime-process lifetime), §5.2 (Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: after an edit from `keep` to `restart`, the idle scan binds a
// used `keep` pod, leaves a refused `keep` pod undrained (including one
// pinned to another tenant or carrying no lifetime label), drains a
// `restart` pod refused only for its tenant, or refuses a used `restart` pod
// of the requesting tenant.
func TestIdleScanRefusesUsedKeepPodWhenAcknowledgmentNotRequired_spec_5_2(t *testing.T) {
	base := newEnvtestClient(
		t,
		sandboxIn("lt-idle", "a-keep-acme", "idle"),
		lifetimePod("lt-idle", "a-keep-acme", "acme", "keep"),
		sandboxIn("lt-idle", "b-keep-globex", "idle"),
		lifetimePod("lt-idle", "b-keep-globex", "globex", "keep"),
		sandboxIn("lt-idle", "c-nolabel-acme", "idle"),
		lifetimePod("lt-idle", "c-nolabel-acme", "acme", ""),
		sandboxIn("lt-idle", "d-restart-globex", "idle"),
		lifetimePod("lt-idle", "d-restart-globex", "globex", "restart"),
		sandboxIn("lt-idle", "e-restart-acme", "idle"),
		lifetimePod("lt-idle", "e-restart-acme", "acme", "restart"),
	)
	claimer := &podclaim.Claimer{Client: base, Namespace: testNS}
	bound, err := claimRestartPoolClaim(claimer, "lt-idle", "s1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if bound != "e-restart-acme" {
		t.Errorf("bound %q, want the used restart pod e-restart-acme", bound)
	}
	for _, pod := range []string{"a-keep-acme", "b-keep-globex", "c-nolabel-acme"} {
		if !claimAbsent(t, base, pod) {
			t.Errorf("a claim was created on the keep pod %s", pod)
		}
		if !hasDrainRequest(t, base, pod) {
			t.Errorf("refused keep pod %s carries no drain request", pod)
		}
	}
	if hasDrainRequest(t, base, "d-restart-globex") {
		t.Error("a restart pod refused only for its tenant was drained")
	}
}

// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions)), §4.6.1 (Reserved hold)
// diagnosis: on a pool that does not require the acknowledgment, a reserved
// `keep` pod is rebound, its hold survives the acquisition, a reserved
// `restart` pod is not rebound, a pod read error lets the acquisition go on,
// or a reserved pod whose Pod is gone stops the acquisition or loses its
// hold.
func TestRebindRefusesReservedKeepPodWhenAcknowledgmentNotRequired_spec_4_6_1(t *testing.T) {
	base := newEnvtestClient(
		t,
		reservedIn("lt-res", "rk-held", "acme"),
		lifetimePod("lt-res", "rk-held", "acme", "keep"),
		sandboxIn("lt-res", "rk-z", "idle"),
		pinnedPod("lt-res", "rk-z", ""),
		reservedIn("lt-rr", "rr-held", "acme"),
		lifetimePod("lt-rr", "rr-held", "acme", "restart"),
		reservedIn("lt-gone", "rg-held", "acme"),
		sandboxIn("lt-gone", "rg-z", "idle"),
		reservedIn("lt-err", "re-held", "acme"),
		lifetimePod("lt-err", "re-held", "acme", "restart"),
		sandboxIn("lt-err", "re-z", "idle"),
	)
	for _, pod := range []string{"rk-held", "rr-held", "rg-held", "re-held"} {
		seedReservedClaim(t, base, pod, "acme", fixedNow, time.Minute)
	}
	var rebound []string
	now := func() time.Time { return fixedNow.Add(5 * time.Second) }
	claimer := &podclaim.Claimer{
		Client: base, Namespace: testNS, Now: now,
		OnRebind: func(pod string) { rebound = append(rebound, pod) },
	}

	bound, err := claimRestartPoolClaim(claimer, "lt-res", "s1")
	if err != nil {
		t.Fatalf("Claim (keep reserved): %v", err)
	}
	if bound != "rk-z" || len(rebound) != 0 {
		t.Errorf("bound %q rebound %v, want rk-z and no rebind", bound, rebound)
	}
	if !claimAbsent(t, base, "rk-held") {
		t.Error("the reserved keep pod's hold survived the acquisition")
	}
	if hasDrainRequest(t, base, "rk-held") {
		t.Error("a drain request landed while the keep pod still projected reserved")
	}

	bound, err = claimRestartPoolClaim(claimer, "lt-rr", "s2")
	if err != nil {
		t.Fatalf("Claim (restart reserved): %v", err)
	}
	if bound != "rr-held" || len(rebound) != 1 || rebound[0] != "rr-held" {
		t.Errorf("bound %q rebound %v, want the reserved restart pod rebound", bound, rebound)
	}

	// A NotFound pod read skips the candidate and keeps its hold.
	bound, err = claimRestartPoolClaim(claimer, "lt-gone", "s3")
	if err != nil {
		t.Fatalf("Claim (pod gone): %v", err)
	}
	if bound != "rg-z" || claimAbsent(t, base, "rg-held") {
		t.Errorf("bound %q, want rg-z with the reserved claim kept", bound)
	}

	// Any other pod read error ends the acquisition with the error.
	boom := apierrors.NewInternalError(errors.New("reserved pod read boom"))
	rec := &pinRecorder{failPodGet: map[string]error{"re-held": boom}}
	failing := &podclaim.Claimer{Client: rec.wrap(base), Namespace: testNS, Now: now}
	if err := claimRestartPool(failing, "lt-err", "s4"); !errors.Is(err, boom) {
		t.Fatalf("Claim with a failed reserved pod read = %v, want the read error", err)
	}
	if claimAbsent(t, base, "re-held") || !claimAbsent(t, base, "re-z") {
		t.Error("a failed reserved pod read must keep the hold and create no claim")
	}
}
