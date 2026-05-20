// SPDX-License-Identifier: MIT

package poolscaling

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// spec: §4.6.2 (consecutive admission rejections trigger stuck state)
// diagnosis: the admission-retry tracker increments its counter on
// every Forbidden error from the lenny-pool-config-validator
// webhook. At the configured ceiling the pool key appears in
// StuckPools so the §16.5 PoolScalingAdmissionStuck alert can fire.
func TestAdmissionRetryStuckAtCeiling(t *testing.T) {
	s := newAdmissionRetryState(3)
	key := poolKey("lenny-agents", "echo-pool")
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"}, "echo-pool", errors.New("validator rejected"))

	stuck, n := s.recordOutcome(key, forbidden)
	if stuck || n != 1 {
		t.Errorf("first denial: stuck=%v n=%d, want stuck=false n=1", stuck, n)
	}
	stuck, n = s.recordOutcome(key, forbidden)
	if stuck || n != 2 {
		t.Errorf("second denial: stuck=%v n=%d, want stuck=false n=2", stuck, n)
	}
	stuck, n = s.recordOutcome(key, forbidden)
	if !stuck || n != 3 {
		t.Errorf("third denial: stuck=%v n=%d, want stuck=true n=3", stuck, n)
	}
	if got := s.StuckPools(); len(got) != 1 || got[0] != key {
		t.Errorf("StuckPools = %v, want [%s]", got, key)
	}
}

// spec: §4.6.2 (clean sync clears the counter)
// diagnosis: a successful Sync after a series of denials resets the
// counter so a recovering pool exits the stuck state immediately.
func TestAdmissionRetryClearsOnSuccess(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := poolKey("lenny-agents", "echo-pool")
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	_, _ = s.recordOutcome(key, forbidden)
	stuck, _ := s.recordOutcome(key, forbidden)
	if !stuck {
		t.Fatalf("expected stuck after 2 denials")
	}
	stuck, n := s.recordOutcome(key, nil)
	if stuck || n != 0 {
		t.Errorf("clean sync after stuck: stuck=%v n=%d, want stuck=false n=0", stuck, n)
	}
	if got := s.StuckPools(); len(got) != 0 {
		t.Errorf("StuckPools after recovery = %v, want []", got)
	}
}

// spec: §4.6.2 (non-admission errors do not count)
// diagnosis: only Forbidden / Invalid status errors from the
// admission webhook count as admission denials. Transport errors,
// timeouts, and Postgres source errors are separate fault categories
// the §16.5 alert taxonomy does not track here.
func TestAdmissionRetryIgnoresNonAdmissionErrors(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := poolKey("lenny-agents", "echo-pool")

	cases := []error{
		errors.New("transport timeout"),
		apierrors.NewInternalError(errors.New("apiserver down")),
		apierrors.NewServerTimeout(schema.GroupResource{}, "create", 10),
	}
	for _, err := range cases {
		stuck, n := s.recordOutcome(key, err)
		if stuck || n != 0 {
			t.Errorf("non-admission error %v: stuck=%v n=%d, want stuck=false n=0", err, stuck, n)
		}
	}
}

// spec: §4.6.2 (Invalid errors also count)
// diagnosis: an Invalid status error from the validator (the webhook
// path that emits a field-level rejection rather than a blanket
// Forbidden) is also an admission denial. The retry counter
// increments on both.
func TestAdmissionRetryInvalidStatusCounts(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := poolKey("lenny-agents", "echo-pool")
	invalid := apierrors.NewInvalid(schema.GroupKind{Group: "lenny.dev", Kind: "SandboxWarmPool"}, "echo-pool", nil)

	stuck, n := s.recordOutcome(key, invalid)
	if stuck || n != 1 {
		t.Errorf("first Invalid: stuck=%v n=%d, want stuck=false n=1", stuck, n)
	}
	stuck, n = s.recordOutcome(key, invalid)
	if !stuck || n != 2 {
		t.Errorf("second Invalid: stuck=%v n=%d, want stuck=true n=2", stuck, n)
	}
}

// spec: §4.6.2 (per-pool isolation)
// diagnosis: a stuck pool does not infect a healthy pool's counter.
// Each (namespace, name) key tracks its own consecutive count.
func TestAdmissionRetryIsolatesPerPool(t *testing.T) {
	s := newAdmissionRetryState(2)
	stuckKey := poolKey("lenny-agents", "stuck-pool")
	healthyKey := poolKey("lenny-agents", "healthy-pool")
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	_, _ = s.recordOutcome(stuckKey, forbidden)
	_, _ = s.recordOutcome(stuckKey, forbidden)
	if got := s.ConsecutiveDenials(healthyKey); got != 0 {
		t.Errorf("healthy pool counter = %d, want 0 after only the stuck pool denied", got)
	}
	if got := s.StuckPools(); len(got) != 1 || got[0] != stuckKey {
		t.Errorf("StuckPools = %v, want [%s]", got, stuckKey)
	}
}

// spec: §4.6.2 (zero ceiling falls back to the documented default)
// diagnosis: a misconfigured runner that passes zero (or a negative
// value) for the ceiling silently falls back to
// DefaultAdmissionDeniedRetryCeiling rather than disabling the gate.
func TestAdmissionRetryZeroCeilingFallsBackToDefault(t *testing.T) {
	s := newAdmissionRetryState(0)
	key := poolKey("lenny-agents", "echo-pool")
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	for i := 1; i < DefaultAdmissionDeniedRetryCeiling; i++ {
		stuck, _ := s.recordOutcome(key, forbidden)
		if stuck {
			t.Fatalf("became stuck after %d denials; default ceiling is %d", i, DefaultAdmissionDeniedRetryCeiling)
		}
	}
	stuck, _ := s.recordOutcome(key, forbidden)
	if !stuck {
		t.Errorf("expected stuck after default ceiling denials")
	}
}
