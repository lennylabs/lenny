// SPDX-License-Identifier: MIT

// Package webhookdelivery implements the §25.5 ops event-subscription
// webhook delivery policy: the retry budget and exponential backoff, a
// receiver-status retry classification, and the delivery-tracking
// mode that decides which deliveries are persisted. The package is
// pure — no HTTP transport — so the lenny-ops delivery worker and its
// tests share one policy.
package webhookdelivery

import "time"

// MaxAttempts is the §25.5 webhook delivery attempt limit. After this
// many failed attempts the delivery is marked failed and an
// event_delivery_failed operational event is emitted.
const MaxAttempts = 3

// backoffSchedule is the §25.5 per-attempt exponential backoff.
var backoffSchedule = [MaxAttempts]time.Duration{
	1 * time.Second, 5 * time.Second, 30 * time.Second,
}

// RetryDelay returns the §25.5 backoff before the given 1-based
// delivery attempt. ok is false for an attempt number outside the
// retry budget.
func RetryDelay(attempt int) (delay time.Duration, ok bool) {
	if attempt < 1 || attempt > MaxAttempts {
		return 0, false
	}
	return backoffSchedule[attempt-1], true
}

// Exhausted reports whether a delivery that has made the given number
// of attempts has used its full §25.5 retry budget.
func Exhausted(attempts int) bool {
	return attempts >= MaxAttempts
}

// RetryableStatus reports whether an HTTP response status from a
// webhook receiver is a §25.5 TRANSIENT failure worth retrying. A 5xx
// or 429 is retryable; a 2xx is a success and any other status is a
// PERMANENT failure that is not retried.
func RetryableStatus(httpStatus int) bool {
	if httpStatus == 429 {
		return true
	}
	return httpStatus >= 500 && httpStatus <= 599
}

// TrackingMode is the §25.5 ops.webhooks.deliveryTrackingMode: how
// much of the delivery history a deployment persists.
type TrackingMode string

const (
	// TrackingFull persists every delivery attempt.
	TrackingFull TrackingMode = "full"
	// TrackingFailuresOnly persists only failed deliveries.
	TrackingFailuresOnly TrackingMode = "failures-only"
	// TrackingMetricOnly persists no delivery rows, only counters.
	TrackingMetricOnly TrackingMode = "metric-only"
)

// ShouldRecord reports whether a delivery whose outcome is given by
// failed should be persisted as a row under the §25.5 tracking mode.
// An unrecognized mode records every delivery, so delivery history is
// never silently dropped.
func ShouldRecord(mode TrackingMode, failed bool) bool {
	switch mode {
	case TrackingFailuresOnly:
		return failed
	case TrackingMetricOnly:
		return false
	default:
		return true
	}
}
