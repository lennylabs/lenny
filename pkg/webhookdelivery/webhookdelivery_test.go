// SPDX-License-Identifier: MIT

package webhookdelivery_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

func TestRetryDelaySchedule(t *testing.T) {
	want := []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}
	for attempt := 1; attempt <= webhookdelivery.MaxAttempts; attempt++ {
		got, ok := webhookdelivery.RetryDelay(attempt)
		if !ok || got != want[attempt-1] {
			t.Errorf("RetryDelay(%d) = %v, %v; want %v, true", attempt, got, ok, want[attempt-1])
		}
	}
	for _, attempt := range []int{0, -1, 4, 10} {
		if _, ok := webhookdelivery.RetryDelay(attempt); ok {
			t.Errorf("RetryDelay(%d) reported ok, want false (outside the retry budget)", attempt)
		}
	}
}

func TestExhausted(t *testing.T) {
	if webhookdelivery.Exhausted(2) {
		t.Error("a delivery with 2 attempts is not exhausted")
	}
	if !webhookdelivery.Exhausted(webhookdelivery.MaxAttempts) {
		t.Error("a delivery at MaxAttempts must be exhausted")
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, status := range []int{500, 502, 503, 429} {
		if !webhookdelivery.RetryableStatus(status) {
			t.Errorf("RetryableStatus(%d) = false, want true (transient)", status)
		}
	}
	for _, status := range []int{200, 204, 301, 400, 404, 422} {
		if webhookdelivery.RetryableStatus(status) {
			t.Errorf("RetryableStatus(%d) = true, want false (not transient)", status)
		}
	}
}

func TestShouldRecord(t *testing.T) {
	cases := []struct {
		mode   webhookdelivery.TrackingMode
		failed bool
		want   bool
	}{
		{webhookdelivery.TrackingFull, false, true},
		{webhookdelivery.TrackingFull, true, true},
		{webhookdelivery.TrackingFailuresOnly, false, false},
		{webhookdelivery.TrackingFailuresOnly, true, true},
		{webhookdelivery.TrackingMetricOnly, false, false},
		{webhookdelivery.TrackingMetricOnly, true, false},
		{webhookdelivery.TrackingMode("unknown"), false, true},
	}
	for _, c := range cases {
		if got := webhookdelivery.ShouldRecord(c.mode, c.failed); got != c.want {
			t.Errorf("ShouldRecord(%q, failed=%v) = %v, want %v", c.mode, c.failed, got, c.want)
		}
	}
}
