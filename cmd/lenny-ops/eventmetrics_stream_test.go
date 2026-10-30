// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test driving the §25.5 lenny_ops_events_stream_length
// gauge against a real MAXLEN-bounded Redis stream, exercising the
// production sampleEventStreamGauges XLEN sampler rather than a fake
// or miniredis substitute.
package main

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// xaddN issues n exact (non-approximate) MAXLEN-bounded XADDs against
// the §25.5 ops:events:stream key so the caller can drive the stream to
// a precise length: exact trimming (Approx: false) removes entries down
// to maxLen on every call that would exceed it, so after n >= maxLen
// adds the stream holds exactly maxLen entries.
func xaddN(t *testing.T, ctx context.Context, client redis.UniversalClient, maxLen int64, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := client.XAdd(ctx, &redis.XAddArgs{
			Stream: eventbuffer.DefaultStreamKey,
			MaxLen: maxLen,
			Approx: false,
			Values: map[string]any{"event": "{}"},
		}).Result()
		if err != nil {
			t.Fatalf("XAdd %d: %v", i, err)
		}
	}
}

// spec: §25.5 (Memory monitoring) — "Operators should monitor
// lenny_ops_events_stream_length (gauge, current stream length) and
// alert if it stays at MAXLEN for more than a few minutes ... .
// Recommended alert: lenny_ops_events_stream_length /
// lenny_ops_events_stream_maxlen > 0.95 for 5m."
//
// diagnosis: a failure here means sampleEventStreamGauges does not
// track the real Redis XLEN of ops:events:stream, so the
// lenny_ops_events_stream_length gauge an operator's Prometheus alert
// reads from would not reach the >0.95-of-MAXLEN ratio even while the
// stream is genuinely capped at MAXLEN — the recommended alert would
// silently never fire and an operator would not learn that events are
// being evicted faster than they are consumed.
func TestEventsStreamLengthGaugeAtMaxLen_spec_25_5(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const maxLen = int64(20)

	// Below the cap: 15 of 20 slots filled (75%), no trimming yet.
	xaddN(t, ctx, rd.Client, maxLen, 15)
	sampleEventStreamGauges(ctx, rd.Client, nil)
	belowRatio := testutil.ToFloat64(eventsStreamLength.WithLabelValues()) / float64(maxLen)
	if belowRatio >= 0.95 {
		t.Fatalf("stream at 15/%d entries reported ratio %v, want < 0.95 before the stream is capped", maxLen, belowRatio)
	}

	// Push well past the cap so the stream sits at MAXLEN (exact trim
	// keeps it pinned at maxLen entries), mirroring the "stays at
	// MAXLEN" condition the spec names.
	xaddN(t, ctx, rd.Client, maxLen, 25)
	sampleEventStreamGauges(ctx, rd.Client, nil)

	gotLength := testutil.ToFloat64(eventsStreamLength.WithLabelValues())
	if gotLength != float64(maxLen) {
		t.Fatalf("lenny_ops_events_stream_length = %v, want %d (stream pinned at MAXLEN)", gotLength, maxLen)
	}

	atCapRatio := gotLength / float64(maxLen)
	if atCapRatio <= 0.95 {
		t.Fatalf("lenny_ops_events_stream_length / MAXLEN = %v, want > 0.95 once the stream sits at MAXLEN (the §25.5 recommended alert condition)", atCapRatio)
	}
}
