// SPDX-License-Identifier: MIT

package poolscaling

import (
	"errors"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func tmplKey(ns, name string) denialKey {
	return denialKey{namespace: ns, pool: name, crd: crdSandboxTmpl}
}

// spec: §4.6.2 item 3 (consecutive admission rejections trigger stuck state)
// diagnosis: the admission-retry tracker increments its per-tuple count
// on every Forbidden error from the lenny-pool-config-validator
// webhook. At the configured ceiling the pool appears in stuckPools so
// the §16.5 PoolScalingAdmissionStuck alert can fire.
func TestAdmissionRetryStuckAtCeiling(t *testing.T) {
	s := newAdmissionRetryState(3)
	key := tmplKey("lenny-agents", "echo-pool")
	now := time.Unix(0, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"}, "echo-pool", errors.New("validator rejected"))

	stuck, n := s.recordOutcome(key, forbidden, now)
	if stuck || n != 1 {
		t.Errorf("first denial: stuck=%v n=%d, want stuck=false n=1", stuck, n)
	}
	stuck, n = s.recordOutcome(key, forbidden, now)
	if stuck || n != 2 {
		t.Errorf("second denial: stuck=%v n=%d, want stuck=false n=2", stuck, n)
	}
	stuck, n = s.recordOutcome(key, forbidden, now)
	if !stuck || n != 3 {
		t.Errorf("third denial: stuck=%v n=%d, want stuck=true n=3", stuck, n)
	}
	want := "lenny-agents/echo-pool"
	if got := s.stuckPools(); len(got) != 1 || got[0] != want {
		t.Errorf("stuckPools = %v, want [%s]", got, want)
	}
}

// spec: §4.6.2 item 2 (per-tuple exponential backoff 1s→60s)
// diagnosis: the nth consecutive denial pauses the tuple for 1s, 2s,
// 4s, ... doubling to a 60s ceiling. readyToSync gates the tuple until
// the backoff window elapses.
func TestAdmissionRetryBackoffSchedule(t *testing.T) {
	s := newAdmissionRetryState(100) // high ceiling so backoff (not stuck) drives the gate
	key := tmplKey("lenny-agents", "echo-pool")
	base := time.Unix(1000, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, d := range want {
		s.recordOutcome(key, forbidden, base)
		// Just before the window elapses the tuple is not ready.
		if s.readyToSync(key, base.Add(d-time.Millisecond)) {
			t.Errorf("denial %d: ready %v before backoff %v elapsed", i+1, d-time.Millisecond, d)
		}
		// At the window boundary the tuple is ready again.
		if !s.readyToSync(key, base.Add(d)) {
			t.Errorf("denial %d: not ready at backoff boundary %v", i+1, d)
		}
	}
}

// spec: §4.6.2 item 2 (a clean sync resets the backoff to zero)
// diagnosis: a successful Sync after a series of denials clears the
// entry so a recovering tuple exits backoff and the stuck state.
func TestAdmissionRetryClearsOnSuccess(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := tmplKey("lenny-agents", "echo-pool")
	now := time.Unix(0, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	s.recordOutcome(key, forbidden, now)
	stuck, _ := s.recordOutcome(key, forbidden, now)
	if !stuck {
		t.Fatalf("expected stuck after 2 denials")
	}
	stuck, n := s.recordOutcome(key, nil, now)
	if stuck || n != 0 {
		t.Errorf("clean sync after stuck: stuck=%v n=%d, want stuck=false n=0", stuck, n)
	}
	if got := s.stuckPools(); len(got) != 0 {
		t.Errorf("stuckPools after recovery = %v, want []", got)
	}
	if !s.readyToSync(key, now) {
		t.Errorf("tuple not ready after a clean sync cleared its entry")
	}
}

// spec: §4.6.2 item 3 condition (c) (operator resume-reconciliation)
// diagnosis: resumePool clears the in-memory denial state for every
// CRD tuple of one pool so a stuck pool is retried on the next tick
// without requiring a configuration change. Other pools are untouched.
func TestAdmissionRetryResumePool(t *testing.T) {
	s := newAdmissionRetryState(2)
	now := time.Unix(0, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	stuckTmpl := denialKey{namespace: "lenny-agents", pool: "stuck-pool", crd: crdSandboxTmpl}
	stuckPool := denialKey{namespace: "lenny-agents", pool: "stuck-pool", crd: crdSandboxPool}
	other := denialKey{namespace: "lenny-agents", pool: "other-pool", crd: crdSandboxTmpl}
	for _, k := range []denialKey{stuckTmpl, stuckPool, other} {
		s.recordOutcome(k, forbidden, now)
		s.recordOutcome(k, forbidden, now)
	}

	cleared := s.resumePool("lenny-agents", "stuck-pool")
	if cleared != 2 {
		t.Errorf("resumePool cleared %d tuples, want 2", cleared)
	}
	if !s.readyToSync(stuckTmpl, now) || !s.readyToSync(stuckPool, now) {
		t.Errorf("resumed pool tuples not ready after resume")
	}
	if s.readyToSync(other, now) {
		t.Errorf("resume cleared an unrelated pool's denial state")
	}
	if got := s.resumePool("lenny-agents", "stuck-pool"); got != 0 {
		t.Errorf("second resume cleared %d, want 0 (already clear)", got)
	}
}

// spec: §4.6.2 (non-admission errors do not count or back off)
// diagnosis: only Forbidden / Invalid / BadRequest status errors from
// the admission webhook count as admission denials. Transport errors,
// timeouts, and internal errors are a separate fault category that
// aborts the pass rather than extending the per-tuple backoff.
func TestAdmissionRetryIgnoresNonAdmissionErrors(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := tmplKey("lenny-agents", "echo-pool")
	now := time.Unix(0, 0)

	cases := []error{
		errors.New("transport timeout"),
		apierrors.NewInternalError(errors.New("apiserver down")),
		apierrors.NewServerTimeout(schema.GroupResource{}, "create", 10),
	}
	for _, err := range cases {
		stuck, n := s.recordOutcome(key, err, now)
		if stuck || n != 0 {
			t.Errorf("non-admission error %v: stuck=%v n=%d, want stuck=false n=0", err, stuck, n)
		}
		if !s.readyToSync(key, now) {
			t.Errorf("non-admission error %v left the tuple in backoff", err)
		}
	}
}

// spec: §4.6.2 (Invalid errors also count)
// diagnosis: an Invalid status error from the validator (the field-
// level rejection path) is also an admission denial. The retry counter
// increments on both Invalid and Forbidden.
func TestAdmissionRetryInvalidStatusCounts(t *testing.T) {
	s := newAdmissionRetryState(2)
	key := tmplKey("lenny-agents", "echo-pool")
	now := time.Unix(0, 0)
	invalid := apierrors.NewInvalid(schema.GroupKind{Group: "lenny.dev", Kind: "SandboxWarmPool"}, "echo-pool", nil)

	stuck, n := s.recordOutcome(key, invalid, now)
	if stuck || n != 1 {
		t.Errorf("first Invalid: stuck=%v n=%d, want stuck=false n=1", stuck, n)
	}
	stuck, n = s.recordOutcome(key, invalid, now)
	if !stuck || n != 2 {
		t.Errorf("second Invalid: stuck=%v n=%d, want stuck=true n=2", stuck, n)
	}
}

// spec: §4.6.2 item 2 (per-tuple isolation)
// diagnosis: a stuck (pool, crd) tuple does not infect a healthy
// tuple. A denial on one pool's template never blocks its warm pool or
// another pool.
func TestAdmissionRetryIsolatesPerTuple(t *testing.T) {
	s := newAdmissionRetryState(2)
	now := time.Unix(0, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	stuckTmpl := denialKey{namespace: "lenny-agents", pool: "stuck-pool", crd: crdSandboxTmpl}
	healthyPool := denialKey{namespace: "lenny-agents", pool: "stuck-pool", crd: crdSandboxPool}
	s.recordOutcome(stuckTmpl, forbidden, now)
	s.recordOutcome(stuckTmpl, forbidden, now)

	if !s.readyToSync(healthyPool, now) {
		t.Errorf("warm-pool tuple blocked by the template tuple's denial")
	}
	if got := s.consecutiveDenials("lenny-agents", "stuck-pool"); got != 2 {
		t.Errorf("consecutiveDenials = %d, want 2 (max across tuples)", got)
	}
}

// spec: §16.1 (lenny_pool_scaling_admission_denied_total counter)
// diagnosis: every admission rejection increments the Prometheus
// counter the §16.5 alert reads, labeled by pool, crd, and the
// webhook-supplied reason code parsed from the denial message.
func TestAdmissionDeniedTotalLabelsAndMonotonic(t *testing.T) {
	s := newAdmissionRetryState(5)
	now := time.Unix(0, 0)
	key := denialKey{namespace: "lenny-agents", pool: "metrics-test-pool", crd: crdSandboxPool}
	// The validator prefixes its message with the reason code; the API
	// server wraps it in the admission-webhook envelope.
	rejected := apierrors.NewForbidden(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"}, "metrics-test-pool",
		errors.New("admission webhook \"vpool.lenny.dev\" denied the request: INVALID_POOL_CONFIGURATION: warmCount exceeds budget"))

	before := metricValue(t, "metrics-test-pool", crdSandboxPool, "INVALID_POOL_CONFIGURATION")
	for i := 0; i < 3; i++ {
		s.recordOutcome(key, rejected, now)
	}
	after := metricValue(t, "metrics-test-pool", crdSandboxPool, "INVALID_POOL_CONFIGURATION")
	if after-before != 3 {
		t.Errorf("metric delta = %v, want 3 after 3 denials", after-before)
	}

	// A clean Sync must not roll back the monotonic counter.
	s.recordOutcome(key, nil, now)
	stable := metricValue(t, "metrics-test-pool", crdSandboxPool, "INVALID_POOL_CONFIGURATION")
	if stable != after {
		t.Errorf("counter rolled back on success: after=%v after-reset=%v", after, stable)
	}
}

// spec: §16.1 (reason label falls back when unparseable)
// diagnosis: an admission error with no recognizable reason-code prefix
// is recorded under the generic admission_denied label so the metric
// never carries a free-form high-cardinality string.
func TestAdmissionDeniedReasonFallback(t *testing.T) {
	s := newAdmissionRetryState(5)
	now := time.Unix(0, 0)
	key := denialKey{namespace: "lenny-agents", pool: "fallback-pool", crd: crdSandboxTmpl}
	// NewForbidden builds a message without a reason-code prefix.
	bare := apierrors.NewForbidden(schema.GroupResource{}, "fallback-pool", errors.New("nope"))

	before := metricValue(t, "fallback-pool", crdSandboxTmpl, defaultReason)
	s.recordOutcome(key, bare, now)
	if got := metricValue(t, "fallback-pool", crdSandboxTmpl, defaultReason) - before; got != 1 {
		t.Errorf("admission_denied delta = %v, want 1", got)
	}
}

// metricValue reads the current value of the
// lenny_pool_scaling_admission_denied_total counter for one
// (pool, crd, reason) label tuple.
func metricValue(t *testing.T, pool, crd, reason string) float64 {
	t.Helper()
	m := admissionDeniedTotal.WithLabelValues(pool, crd, reason)
	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		t.Fatalf("read metric: %v", err)
	}
	return pb.Counter.GetValue()
}

// spec: §4.6.2 item 3 (default ceiling is 10)
// diagnosis: a misconfigured runner that passes zero (or a negative
// value) for the ceiling silently falls back to
// DefaultAdmissionDeniedRetryCeiling (10) rather than disabling the
// gate.
func TestAdmissionRetryZeroCeilingFallsBackToDefault(t *testing.T) {
	if DefaultAdmissionDeniedRetryCeiling != 10 {
		t.Fatalf("DefaultAdmissionDeniedRetryCeiling = %d, want 10 per §4.6.2 item 3", DefaultAdmissionDeniedRetryCeiling)
	}
	s := newAdmissionRetryState(0)
	key := tmplKey("lenny-agents", "echo-pool")
	now := time.Unix(0, 0)
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("denied"))

	for i := 1; i < DefaultAdmissionDeniedRetryCeiling; i++ {
		stuck, _ := s.recordOutcome(key, forbidden, now)
		if stuck {
			t.Fatalf("became stuck after %d denials; default ceiling is %d", i, DefaultAdmissionDeniedRetryCeiling)
		}
	}
	stuck, _ := s.recordOutcome(key, forbidden, now)
	if !stuck {
		t.Errorf("expected stuck after default ceiling denials")
	}
}
