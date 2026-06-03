// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// The §25.5 line 2785-2791 event-stream and webhook-delivery metrics.
// Each is registered on the process default registry the §16.9 lenny-ops
// /metrics exposition scrapes. A registration error leaves the variable
// nil so the call sites no-op rather than panic.

var eventsWebhookDeliveryTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_events_webhook_delivery_total",
		Help: "§25.5 webhook delivery outcomes by subscription and status.",
	}, []string{"subscription_id", "status"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

var eventsWebhookDeliveryLatency = func() *prometheus.HistogramVec {
	h, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_ops_events_webhook_delivery_latency_seconds",
		Help:    "§25.5 webhook delivery latency by subscription.",
		Buckets: prometheus.DefBuckets,
	}, []string{"subscription_id"})
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, h)
	return h
}()

var eventsStreamLength = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_events_stream_length",
		Help: "§25.5 current length of the Redis ops:events:stream.",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

var eventsSSEActiveConnections = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_ops_events_sse_active_connections",
		Help: "§25.5 active SSE event-stream connections.",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, g)
	return g
}()

var eventsStreamGapsTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_events_stream_gaps_total",
		Help: "§25.5 subscriber requests whose cursor was already evicted (gapDetected).",
	}, nil)
	if err != nil {
		return nil
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}()

// deliveryMetricsObserver implements opsservice.DeliveryMetrics over the
// webhook-delivery counter and latency histogram. spec: §25.5 lines
// 2790-2791.
type deliveryMetricsObserver struct{}

func (deliveryMetricsObserver) ObserveDelivery(subID, status string, latency time.Duration) {
	if eventsWebhookDeliveryTotal != nil {
		eventsWebhookDeliveryTotal.WithLabelValues(subID, status).Inc()
	}
	if eventsWebhookDeliveryLatency != nil {
		eventsWebhookDeliveryLatency.WithLabelValues(subID).Observe(latency.Seconds())
	}
}

// observeStreamGap backs the §25.5 lenny_ops_events_stream_gaps_total
// counter; it is wired to opsstream.Options.OnGap. spec: §25.5 line 2788.
func observeStreamGap() {
	if eventsStreamGapsTotal != nil {
		eventsStreamGapsTotal.WithLabelValues().Inc()
	}
}

// subscriberCounter reports the live SSE subscriber count.
type subscriberCounter interface{ SubscriberCount() int }

// sampleEventStreamGauges refreshes the §25.5 stream-length and
// SSE-connection gauges. The stream length is the Redis XLEN of
// ops:events:stream (0 when Redis is not wired); the SSE-connection
// gauge reads the live subscriber count from the local stream service.
// spec: §25.5 lines 2787, 2789.
func sampleEventStreamGauges(ctx context.Context, client redis.UniversalClient, subs subscriberCounter) {
	if eventsStreamLength != nil && client != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if n, err := client.XLen(cctx, events.DefaultStreamKey).Result(); err == nil {
			eventsStreamLength.WithLabelValues().Set(float64(n))
		}
		cancel()
	}
	if eventsSSEActiveConnections != nil && subs != nil {
		eventsSSEActiveConnections.WithLabelValues().Set(float64(subs.SubscriberCount()))
	}
}
