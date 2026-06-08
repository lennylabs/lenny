// SPDX-License-Identifier: MIT

// This file holds the §6.1 SDK-warm circuit breaker. The breaker is a
// pure decision function: given the rolling demotion rate, the current
// persisted breaker state, the configured min-open duration, and the
// current time, it returns the next breaker state and whether
// `spec.sdkWarmDisabled` should be true.
//
// §6.1 fixes the trip threshold at a 90% rolling 5-minute demotion
// rate (the rate at which SDK-warm pods fail to warm and are demoted
// to pod-warm). The threshold is a hardcoded safety value and is not
// operator-configurable. The min-open grace duration IS
// operator-tunable per pool via the ScalePolicy
// SDKWarmCircuitBreakerMinOpenSeconds field (default 1800s).
//
// The rolling demotion window itself lives in PoolScalingController
// memory and is not persisted. Only the open/closed decision is
// persisted, on SandboxWarmPool.status.sdkWarmCircuitBreaker, so the
// decision survives a PSC leader failover. On a fresh leader the
// in-memory window starts at zero; the minOpenUntil grace period masks
// that cold start by holding a tripped breaker open until the window
// has had time to refill.

package poolscaling

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// SDKWarmDemotionRateTripThreshold is the §6.1 rolling 5-minute
// demotion-rate ceiling. When the observed rate is at or above this
// value the PoolScalingController trips the SDK-warm circuit breaker.
// §6.1 fixes this as a hardcoded safety threshold; unlike the
// min-open grace duration it is deliberately not operator-tunable.
const SDKWarmDemotionRateTripThreshold = 0.90

// defaultSDKWarmCircuitBreakerMinOpen is the §6.1 default grace window
// the breaker stays open before the PSC re-evaluates the demotion
// rate. ScalePolicy.SDKWarmCircuitBreakerMinOpenSeconds overrides it
// per pool; a configured 0 disables the grace window entirely.
const defaultSDKWarmCircuitBreakerMinOpen = 30 * time.Minute

// Breaker-trip reason codes written to
// status.sdkWarmCircuitBreaker.openedReason (§6.1).
const (
	// breakerReasonDemotionRate marks a breaker tripped automatically
	// because the rolling demotion rate crossed the trip threshold.
	breakerReasonDemotionRate = "demotion_rate_exceeded"
	// breakerReasonOperatorManual marks a breaker held open by the §6.1
	// line 63 `circuitBreakerOverride: disabled` operator override. Unlike
	// an automatic trip it carries no minOpenUntil — it stays open until the
	// operator sets `enabled` or `auto`. spec: §6.1 line 54.
	breakerReasonOperatorManual = "operator_manual"
)

// BreakerState is the input the breaker decision consumes and the
// output it produces. It mirrors the persisted
// SDKWarmCircuitBreakerStatus carve-out: an Open breaker has a
// non-nil OpenedAt and MinOpenUntil; a closed breaker has both nil.
type BreakerState struct {
	// Open reports whether the breaker is currently tripped. When true
	// the PoolScalingController writes spec.sdkWarmDisabled: true.
	Open bool
	// OpenedAt is when the breaker was tripped. It is nil while the
	// breaker is closed.
	OpenedAt *time.Time
	// OpenedReason is the machine-readable trip code. It is empty while
	// the breaker is closed.
	OpenedReason string
	// MinOpenUntil is the earliest time the breaker may auto-close. It
	// is nil while the breaker is closed.
	MinOpenUntil *time.Time
}

// BreakerInputs are the per-reconcile inputs to the breaker decision.
type BreakerInputs struct {
	// DemotionRate is the rolling 5-minute SDK-warm demotion rate in
	// [0,1] computed from the PSC's in-memory window: demotions divided
	// by claims. A pool with no claims in the window reports 0.
	DemotionRate float64
	// HasWindowSample is true once the rolling window holds a usable
	// sample. While it is false the decision never auto-closes an open
	// breaker on a low rate, because a cold post-failover window reads
	// zero demotions regardless of the real rate. The minOpenUntil
	// grace period is the primary cold-start guard; this flag is the
	// belt-and-braces second guard for the post-grace reconcile.
	HasWindowSample bool
	// Current is the prior persisted breaker state, read back from
	// status.sdkWarmCircuitBreaker. A zero value is a closed breaker.
	Current BreakerState
	// MinOpenDuration is the configured grace window the breaker stays
	// open once tripped. A zero value disables the grace window (the
	// breaker may close on the very next reconcile once the rate
	// recovers). A negative value is treated as the §6.1 default.
	MinOpenDuration time.Duration
	// Now is the reconcile timestamp. The decision compares it against
	// MinOpenUntil to decide whether the grace window has elapsed.
	Now time.Time
}

// BreakerDecision is the result of one breaker evaluation.
type BreakerDecision struct {
	// State is the breaker state to persist to
	// status.sdkWarmCircuitBreaker.
	State BreakerState
	// SDKWarmDisabled is the value to write to spec.sdkWarmDisabled. It
	// equals State.Open.
	SDKWarmDisabled bool
}

// EvaluateBreaker is the §6.1 SDK-warm circuit-breaker decision. It is
// pure: it reads BreakerInputs and returns the next state without
// touching the API server. The PoolScalingController persists the
// result.
//
// The decision rules, in order:
//
//  1. Closed breaker, rate at or above the trip threshold: trip.
//     Record OpenedAt = Now, OpenedReason = demotion_rate_exceeded,
//     MinOpenUntil = Now + MinOpenDuration.
//  2. Closed breaker, rate below the threshold: stay closed.
//  3. Open breaker, still inside the grace window (Now < MinOpenUntil):
//     stay open regardless of the in-memory rate. This is the
//     post-failover guard — a fresh leader keeps a tripped breaker
//     open until the persisted minOpenUntil elapses.
//  4. Open breaker, grace window elapsed, no usable window sample yet:
//     stay open. The fresh window has not refilled, so a zero rate is
//     not yet trustworthy.
//  5. Open breaker, grace window elapsed, rate still at or above the
//     threshold: re-trip with a fresh OpenedAt and MinOpenUntil.
//  6. Open breaker, grace window elapsed, rate recovered below the
//     threshold: close.
func EvaluateBreaker(in BreakerInputs) BreakerDecision {
	minOpen := in.MinOpenDuration
	if minOpen < 0 {
		minOpen = defaultSDKWarmCircuitBreakerMinOpen
	}

	tripped := in.DemotionRate >= SDKWarmDemotionRateTripThreshold

	// Closed breaker: trip only on a high rate.
	if !in.Current.Open {
		if tripped {
			return tripDecision(in.Now, minOpen)
		}
		return closedDecision()
	}

	// Open breaker: hold open until the grace window elapses.
	if in.Current.MinOpenUntil != nil && in.Now.Before(*in.Current.MinOpenUntil) {
		return openDecision(in.Current)
	}

	// Grace window elapsed. Without a usable rolling-window sample the
	// rate cannot be trusted to auto-close the breaker; keep it open
	// until the window has refilled.
	if !in.HasWindowSample {
		return openDecision(in.Current)
	}

	// Grace window elapsed with a usable sample: re-trip on a still-high
	// rate, otherwise close.
	if tripped {
		return tripDecision(in.Now, minOpen)
	}
	return closedDecision()
}

// tripDecision builds the decision for a freshly tripped (or
// re-tripped) breaker.
func tripDecision(now time.Time, minOpen time.Duration) BreakerDecision {
	openedAt := now
	minOpenUntil := now.Add(minOpen)
	st := BreakerState{
		Open:         true,
		OpenedAt:     &openedAt,
		OpenedReason: breakerReasonDemotionRate,
		MinOpenUntil: &minOpenUntil,
	}
	return BreakerDecision{State: st, SDKWarmDisabled: true}
}

// openDecision builds the decision that keeps an already-open breaker
// open with its existing persisted timestamps.
func openDecision(cur BreakerState) BreakerDecision {
	return BreakerDecision{State: cur, SDKWarmDisabled: true}
}

// closedDecision builds the decision for a closed breaker.
func closedDecision() BreakerDecision {
	return BreakerDecision{State: BreakerState{}, SDKWarmDisabled: false}
}

// operatorDisabledDecision builds the decision for the §6.1 line 63
// `circuitBreakerOverride: disabled` override: SDK-warm is forced off
// regardless of the demotion rate. The breaker is recorded open with the
// operator_manual reason and no minOpenUntil, since an operator disable is
// not grace-window bounded. When the breaker is already operator-disabled
// the original openedAt is preserved so a steady-state reconcile does not
// churn the status timestamp.
func operatorDisabledDecision(cur BreakerState, now time.Time) BreakerDecision {
	openedAt := now
	if cur.Open && cur.OpenedReason == breakerReasonOperatorManual && cur.OpenedAt != nil {
		openedAt = *cur.OpenedAt
	}
	st := BreakerState{
		Open:         true,
		OpenedAt:     &openedAt,
		OpenedReason: breakerReasonOperatorManual,
	}
	return BreakerDecision{State: st, SDKWarmDisabled: true}
}

// breakerStateFromStatus reads a persisted SDKWarmCircuitBreakerStatus
// into the BreakerState the decision consumes. A nil status, or one
// with no openedAt, is a closed breaker.
func breakerStateFromStatus(s *lennyv1.SDKWarmCircuitBreakerStatus) BreakerState {
	if s == nil || s.OpenedAt == nil {
		return BreakerState{}
	}
	st := BreakerState{
		Open:         true,
		OpenedReason: s.OpenedReason,
	}
	openedAt := s.OpenedAt.Time
	st.OpenedAt = &openedAt
	if s.MinOpenUntil != nil {
		minOpenUntil := s.MinOpenUntil.Time
		st.MinOpenUntil = &minOpenUntil
	}
	return st
}

// breakerStatusFromState renders a BreakerState back to the persisted
// status carve-out. A closed breaker maps to a nil status so the SSA
// apply clears all three fields.
func breakerStatusFromState(st BreakerState) *lennyv1.SDKWarmCircuitBreakerStatus {
	if !st.Open {
		return nil
	}
	out := &lennyv1.SDKWarmCircuitBreakerStatus{
		OpenedReason: st.OpenedReason,
	}
	if st.OpenedAt != nil {
		out.OpenedAt = &metav1.Time{Time: *st.OpenedAt}
	}
	if st.MinOpenUntil != nil {
		out.MinOpenUntil = &metav1.Time{Time: *st.MinOpenUntil}
	}
	return out
}

// scalePolicyMinOpenDuration resolves the configured circuit-breaker
// grace window from a pool's ScalePolicy. An unset policy or unset
// field selects the §6.1 default; a configured 0 disables the grace
// window; a configured negative value is clamped to 0.
func scalePolicyMinOpenDuration(p *lennyv1.ScalePolicy) time.Duration {
	if p == nil || p.SDKWarmCircuitBreakerMinOpenSeconds == nil {
		return defaultSDKWarmCircuitBreakerMinOpen
	}
	secs := *p.SDKWarmCircuitBreakerMinOpenSeconds
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
