// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestSetPoolWarmingUp_spec_5_2_627 verifies the alert-support gauge
// tracks the warming flag so the WarmPoolBootstrapping rule's
// `lenny_pool_warming_up == 1` expression has a live producer.
//
// spec: §5.2 line 627.
func TestSetPoolWarmingUp_spec_5_2_627(t *testing.T) {
	const pool = "warming-gauge-set"
	t.Cleanup(func() { forgetPoolWarmingUp(pool) })

	setPoolWarmingUp(pool, true)
	if g := testutil.ToFloat64(poolWarmingUp.WithLabelValues(pool)); g != 1 {
		t.Fatalf("warming gauge after true = %v, want 1", g)
	}

	setPoolWarmingUp(pool, false)
	if g := testutil.ToFloat64(poolWarmingUp.WithLabelValues(pool)); g != 0 {
		t.Fatalf("warming gauge after false = %v, want 0", g)
	}
}

// TestForgetPoolWarmingUp_spec_5_2_627 verifies a deleted pool's gauge
// series is removed so a removed pool leaves no stale series.
//
// spec: §5.2 line 627.
func TestForgetPoolWarmingUp_spec_5_2_627(t *testing.T) {
	const pool = "warming-gauge-forget"
	setPoolWarmingUp(pool, true)
	if c := testutil.CollectAndCount(poolWarmingUp); c == 0 {
		t.Fatalf("expected at least one series after set, got %d", c)
	}
	forgetPoolWarmingUp(pool)
	// The forgotten series is gone; a fresh read would re-create it at 0,
	// so assert via the metric family count for this exact label set.
	if m, err := poolWarmingUp.GetMetricWithLabelValues(pool); err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	} else if v := testutil.ToFloat64(m); v != 0 {
		t.Fatalf("warming gauge after forget = %v, want 0 (re-created series)", v)
	}
	t.Cleanup(func() { forgetPoolWarmingUp(pool) })
}

// TestPoolWarmingUpConditionDrivesGauge_spec_5_2_627 binds the gauge to the
// exact condition-status expression the reconcile uses at the call site, so
// a True condition yields gauge=1 and a non-True condition yields gauge=0.
//
// spec: §5.2 line 627.
func TestPoolWarmingUpConditionDrivesGauge_spec_5_2_627(t *testing.T) {
	const pool = "warming-gauge-condition"
	t.Cleanup(func() { forgetPoolWarmingUp(pool) })

	cases := []struct {
		name             string
		minWarm          int
		warm, ready      int
		wantGauge        float64
		wantConditionVal metav1.ConditionStatus
	}{
		{"warming: no idle, one warming", 1, 1, 0, 1, metav1.ConditionTrue},
		{"drained: no idle, no warming", 1, 0, 0, 0, metav1.ConditionFalse},
		{"available: idle pods present", 1, 2, 2, 0, metav1.ConditionFalse},
		{"minWarm zero", 0, 0, 0, 0, metav1.ConditionFalse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cond := poolWarmingUpCondition(tc.minWarm, tc.warm, tc.ready)
			if cond.Status != tc.wantConditionVal {
				t.Fatalf("condition status = %v, want %v", cond.Status, tc.wantConditionVal)
			}
			setPoolWarmingUp(pool, cond.Status == metav1.ConditionTrue)
			if g := testutil.ToFloat64(poolWarmingUp.WithLabelValues(pool)); g != tc.wantGauge {
				t.Fatalf("gauge = %v, want %v", g, tc.wantGauge)
			}
		})
	}
}

// TestSetPoolSecurityDegraded_spec_4_7 verifies the §4.7 alert-support
// gauge tracks the degraded flag so the bundled
// `lenny_pool_security_degraded == 1` rule has a live producer.
//
// spec: §4.7 (nonce-only operation MUST be covered by an alert); §16.5.
func TestSetPoolSecurityDegraded_spec_4_7(t *testing.T) {
	const pool = "security-gauge-set"
	t.Cleanup(func() { forgetPoolSecurityDegraded(pool) })

	setPoolSecurityDegraded(pool, true)
	if g := testutil.ToFloat64(poolSecurityDegraded.WithLabelValues(pool)); g != 1 {
		t.Fatalf("security-degraded gauge after true = %v, want 1", g)
	}

	setPoolSecurityDegraded(pool, false)
	if g := testutil.ToFloat64(poolSecurityDegraded.WithLabelValues(pool)); g != 0 {
		t.Fatalf("security-degraded gauge after false = %v, want 0", g)
	}
}

// TestForgetPoolSecurityDegraded_spec_4_7 verifies a deleted pool's gauge
// series is removed so a removed pool leaves no stale series behind.
//
// spec: §4.7; §16.5.
func TestForgetPoolSecurityDegraded_spec_4_7(t *testing.T) {
	const pool = "security-gauge-forget"
	setPoolSecurityDegraded(pool, true)
	if c := testutil.CollectAndCount(poolSecurityDegraded); c == 0 {
		t.Fatalf("expected at least one series after set, got %d", c)
	}
	forgetPoolSecurityDegraded(pool)
	if m, err := poolSecurityDegraded.GetMetricWithLabelValues(pool); err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	} else if v := testutil.ToFloat64(m); v != 0 {
		t.Fatalf("security-degraded gauge after forget = %v, want 0 (re-created series)", v)
	}
	t.Cleanup(func() { forgetPoolSecurityDegraded(pool) })
}

// TestSecurityDegradedConditionDrivesGauge_spec_4_7 binds the gauge to the
// exact condition-status expression the reconcile uses at the call site, so
// a True condition yields gauge=1 and a False condition yields gauge=0.
//
// spec: §4.7; §16.5.
func TestSecurityDegradedConditionDrivesGauge_spec_4_7(t *testing.T) {
	const pool = "security-gauge-condition"
	t.Cleanup(func() { forgetPoolSecurityDegraded(pool) })

	cases := []struct {
		name             string
		degraded         bool
		wantGauge        float64
		wantConditionVal metav1.ConditionStatus
	}{
		{"nonce-only pool renders degraded", true, 1, metav1.ConditionTrue},
		{"enforcing pool renders clean", false, 0, metav1.ConditionFalse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cond := securityDegradedCondition(tc.degraded)
			if cond.Status != tc.wantConditionVal {
				t.Fatalf("condition status = %v, want %v", cond.Status, tc.wantConditionVal)
			}
			setPoolSecurityDegraded(pool, cond.Status == metav1.ConditionTrue)
			if g := testutil.ToFloat64(poolSecurityDegraded.WithLabelValues(pool)); g != tc.wantGauge {
				t.Fatalf("gauge = %v, want %v", g, tc.wantGauge)
			}
		})
	}
}
