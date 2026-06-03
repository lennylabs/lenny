// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §25.4 lines 2491-2497 — the self-health source gauges report the
// raw inputs behind the self-health statuses. sampleSelfHealthSourceGauges
// sets the redis-consumer-lag and webhook-backlog gauges from the supplied
// functions; a nil function reports 0 (no source wired) rather than
// leaving the series absent.
func TestSampleSelfHealthSourceGauges_spec_25_4(t *testing.T) {
	if opsRedisConsumerLag == nil || opsWebhookBacklog == nil || opsPostgresPoolActive == nil {
		t.Fatal("§25.4 self-health source gauges failed to register")
	}

	sampleSelfHealthSourceGauges(nil, func() int { return 1234 }, func() int { return 42 })

	if got := testutil.ToFloat64(opsRedisConsumerLag.WithLabelValues()); got != 1234 {
		t.Errorf("lenny_ops_redis_consumer_lag = %v, want 1234", got)
	}
	if got := testutil.ToFloat64(opsWebhookBacklog.WithLabelValues()); got != 42 {
		t.Errorf("lenny_ops_webhook_backlog = %v, want 42", got)
	}
	// A nil pool leaves the postgres gauge untouched (no panic); the
	// initial value is 0.
	if got := testutil.ToFloat64(opsPostgresPoolActive.WithLabelValues()); got != 0 {
		t.Errorf("lenny_ops_postgres_pool_active = %v, want 0 with no pool wired", got)
	}
}

// spec: §25.4 lines 2491-2497 — a nil lag/backlog function (no source
// wired) reports 0 rather than panicking, matching the redis-lag
// self-health check's nil-lag treatment.
func TestSampleSelfHealthSourceGauges_NilSources_spec_25_4(t *testing.T) {
	sampleSelfHealthSourceGauges(nil, nil, nil)

	if got := testutil.ToFloat64(opsRedisConsumerLag.WithLabelValues()); got != 0 {
		t.Errorf("lenny_ops_redis_consumer_lag = %v, want 0 with nil source", got)
	}
	if got := testutil.ToFloat64(opsWebhookBacklog.WithLabelValues()); got != 0 {
		t.Errorf("lenny_ops_webhook_backlog = %v, want 0 with nil source", got)
	}
}
