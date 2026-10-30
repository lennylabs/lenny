// SPDX-License-Identifier: MIT

package opsservice

import (
	"testing"
	"time"
)

// spec: §25.4 ops.leaderElection.{leaseDurationSeconds,renewDeadlineSeconds,
// retryPeriodSeconds} — F-25.4.9. A zero field falls back to the built-in
// default; a positive field overrides it.
func TestLeaseTimingsWithDefaults_spec_25_4(t *testing.T) {
	// All zero → built-in 15s / 10s / 2s.
	got := LeaseTimings{}.withDefaults()
	if got.LeaseDuration != LeaseDuration || got.RenewDeadline != RenewDeadline || got.RetryPeriod != RetryPeriod {
		t.Fatalf("zero timings = %+v, want built-ins (%v,%v,%v)", got, LeaseDuration, RenewDeadline, RetryPeriod)
	}

	// Partial override: lease set, the rest fall back.
	got = LeaseTimings{LeaseDuration: 30 * time.Second}.withDefaults()
	if got.LeaseDuration != 30*time.Second {
		t.Errorf("LeaseDuration = %v, want 30s override", got.LeaseDuration)
	}
	if got.RenewDeadline != RenewDeadline || got.RetryPeriod != RetryPeriod {
		t.Errorf("partial override changed defaulted fields: %+v", got)
	}

	// Full override preserves the client-go invariant lease > renew > retry.
	got = LeaseTimings{
		LeaseDuration: 45 * time.Second,
		RenewDeadline: 30 * time.Second,
		RetryPeriod:   5 * time.Second,
	}.withDefaults()
	if !(got.LeaseDuration > got.RenewDeadline && got.RenewDeadline > got.RetryPeriod) {
		t.Errorf("override window %+v violates lease > renew > retry", got)
	}
}
