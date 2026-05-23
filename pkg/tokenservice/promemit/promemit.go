// SPDX-License-Identifier: MIT

// Package promemit is the Prometheus implementation of the §16.1 Token
// Service metric catalog. The Token Service binary owns a private
// registry constructed here; the binary's metrics HTTP listener serves
// that registry on the §16.1 metrics port. spec: §16.1
// lenny_token_service_request_duration_seconds /
// lenny_token_service_errors_total /
// lenny_token_service_secret_reloads_total and §16.5
// TokenServiceUnavailable; §13.3 lines 607–611
// lenny_oauth_token_rate_limited_total /
// lenny_oauth_token_rate_limited_sampled_total.
package promemit

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// Emitter holds the registered §16.1 Token Service metric vectors and
// implements tokenservice.Metrics.
type Emitter struct {
	reg                *prometheus.Registry
	requestDuration    *prometheus.HistogramVec
	errors             *prometheus.CounterVec
	secretReloads      *prometheus.CounterVec
	rateLimited        *prometheus.CounterVec
	rateLimitedSampled *prometheus.CounterVec
}

// New constructs and registers the Token Service metric set against a
// fresh private registry.
func New() (*Emitter, error) {
	reg := prometheus.NewRegistry()

	requestDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_token_service_request_duration_seconds",
		Help:    "§16.1 Token Service request duration by operation.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})
	if err != nil {
		return nil, err
	}
	errs, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_token_service_errors_total",
		Help: "§16.1 Token Service errors by operation and error class.",
	}, []string{"operation", "error_type"})
	if err != nil {
		return nil, err
	}
	secretReloads, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_token_service_secret_reloads_total",
		Help: "§16.1 Token Service signing-secret reloads by outcome.",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	rateLimited, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_oauth_token_rate_limited_total",
		Help: "§13.3 line 609 token-endpoint rate-limit rejections by §13.3 limit_tier.",
	}, []string{"limit_tier"})
	if err != nil {
		return nil, err
	}
	rateLimitedSampled, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_oauth_token_rate_limited_sampled_total",
		Help: "§13.3 line 609 sampled rate-limit rejections (one row per (tenant, sub, tier) per window).",
	}, []string{"limit_tier"})
	if err != nil {
		return nil, err
	}

	reg.MustRegister(requestDuration, errs, secretReloads, rateLimited, rateLimitedSampled)
	return &Emitter{
		reg:                reg,
		requestDuration:    requestDuration,
		errors:             errs,
		secretReloads:      secretReloads,
		rateLimited:        rateLimited,
		rateLimitedSampled: rateLimitedSampled,
	}, nil
}

// RecordRequestDuration implements tokenservice.Metrics.
func (e *Emitter) RecordRequestDuration(operation string, d time.Duration) {
	e.requestDuration.WithLabelValues(operation).Observe(d.Seconds())
}

// IncErrors implements tokenservice.Metrics.
func (e *Emitter) IncErrors(operation, errorClass string) {
	e.errors.WithLabelValues(operation, errorClass).Inc()
}

// IncRateLimited implements tokenservice.Metrics.
func (e *Emitter) IncRateLimited(limitTier string) {
	e.rateLimited.WithLabelValues(limitTier).Inc()
}

// IncRateLimitedSampled implements tokenservice.Metrics.
func (e *Emitter) IncRateLimitedSampled(limitTier string) {
	e.rateLimitedSampled.WithLabelValues(limitTier).Inc()
}

// IncSecretReload records one signing-secret reload outcome. The
// §16.1 lenny_token_service_secret_reloads_total counter is wired
// through this; production reload paths land here once §4.9.1 KEK
// rotation is implemented end-to-end. outcome takes "success" or
// "failure".
func (e *Emitter) IncSecretReload(outcome string) {
	e.secretReloads.WithLabelValues(outcome).Inc()
}

// Handler returns the Prometheus /metrics scrape handler over the
// Token Service's private registry.
func (e *Emitter) Handler() http.Handler {
	return promhttp.HandlerFor(e.reg, promhttp.HandlerOpts{})
}

// Registry exposes the underlying Prometheus registry so a future
// subsystem in this binary can register its own collectors against the
// shared registry.
func (e *Emitter) Registry() *prometheus.Registry { return e.reg }

var _ tokenservice.Metrics = (*Emitter)(nil)
