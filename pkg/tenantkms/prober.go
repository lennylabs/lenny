// SPDX-License-Identifier: MIT

package tenantkms

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// DefaultProbeInterval is the §12.5 continuous-probe cadence. The
// `T4KmsKeyUnusable` alert fires when the
// `lenny_t4_kms_probe_last_success_timestamp` gauge is older than
// 2 * the cadence, so a 5-minute interval lets the alert fire within
// 10 minutes of a KMS outage.
const DefaultProbeInterval = 5 * time.Minute

// MinProbeInterval is the §12.5 line 307 floor on the continuous-probe
// cadence ("the interval is bounded below by a minimum of 60s enforced
// at Helm-validate to prevent excessive KMS API spend"). Start clamps a
// configured interval up to this floor as defense-in-depth so a binary
// launched with a sub-60s interval cannot busy-probe the KMS backend
// even when the Helm gate is bypassed.
const MinProbeInterval = 60 * time.Second

// DefaultProbeRateLimit is the §12.5 line 307 default token-bucket
// ceiling on probe issuance (`storage.t4KmsProbeRateLimit`, default
// 10 probes/sec) so a fleet of 10 000 T4 tenants does not burst the KMS
// backend within a single sweep.
const DefaultProbeRateLimit = 10.0

// TenantSource is the read seam the continuous probe uses to enumerate
// the T4 tenants whose keys it must probe each cycle. The §15.1 admin
// tenant store is the production implementation; tests inject a static
// slice through StaticTenantSource.
type TenantSource interface {
	// T4Tenants returns the tenant IDs currently at workspaceTier T4.
	// The order is irrelevant. An error from T4Tenants ends the
	// current probe cycle without updating any gauge; the next tick
	// retries.
	T4Tenants(ctx context.Context) ([]string, error)
}

// StaticTenantSource is a TenantSource backed by a fixed slice. It is
// the test wiring; production calls plug in the admin tenant store.
type StaticTenantSource struct {
	Tenants []string
}

// T4Tenants implements TenantSource.
func (s StaticTenantSource) T4Tenants(_ context.Context) ([]string, error) {
	out := make([]string, len(s.Tenants))
	copy(out, s.Tenants)
	return out, nil
}

// ProbeMetrics is the metric surface the continuous probe maintains.
// `lenny_t4_kms_probe_last_success_timestamp` is the Unix time of the
// most recent successful per-tenant probe (the `T4KmsKeyUnusable`
// alert reads it). `lenny_t4_kms_probe_result_total` counts probe
// outcomes labeled by `result` so a dashboard can correlate failures
// with operator-driven changes.
type ProbeMetrics struct {
	LastSuccessTimestamp *prometheus.GaugeVec
	ResultTotal          *prometheus.CounterVec
}

// NewProbeMetrics returns a ProbeMetrics ready to register against
// reg. Pass nil reg to register against prometheus.DefaultRegisterer.
func NewProbeMetrics(reg prometheus.Registerer) (*ProbeMetrics, error) {
	gauge, err := metrics.NewGauge(
		prometheus.GaugeOpts{
			Name: "lenny_t4_kms_probe_last_success_timestamp",
			Help: "Unix time of the last successful T4 KMS probe.",
		},
		[]string{"tenant_id"},
	)
	if err != nil {
		return nil, fmt.Errorf("tenantkms: build probe success gauge: %w", err)
	}
	counter, err := metrics.NewCounter(
		prometheus.CounterOpts{
			Name: "lenny_t4_kms_probe_result_total",
			Help: "T4 KMS probe results by outcome.",
		},
		[]string{"tenant_id", "result"},
	)
	if err != nil {
		return nil, fmt.Errorf("tenantkms: build probe result counter: %w", err)
	}
	metrics.MustRegister(reg, gauge)
	metrics.MustRegister(reg, counter)
	return &ProbeMetrics{LastSuccessTimestamp: gauge, ResultTotal: counter}, nil
}

// Prober drives the §12.5 continuous KMS availability probe. On each
// tick it enumerates T4 tenants from Tenants, runs
// Lifecycle.ProbeAvailability against each, and updates ProbeMetrics
// with the per-tenant outcome.
type Prober struct {
	// Lifecycle is the source of the per-tenant Probe; the success
	// path also stamps Lifecycle.LastProbeSuccess for the admin API
	// to read.
	Lifecycle *Lifecycle
	// Tenants enumerates the T4 tenants to probe each cycle.
	Tenants TenantSource
	// Metrics receives the per-tenant success timestamp and the
	// per-outcome counter. Pass nil to skip metric updates (useful
	// for tests whose only assertion is on the Lifecycle's recorded
	// last-success time).
	Metrics *ProbeMetrics
	// Interval is the probe cadence. A non-positive value selects
	// DefaultProbeInterval; a positive value below MinProbeInterval is
	// clamped up to the floor at Start.
	Interval time.Duration
	// RateLimit bounds probe issuance to RateLimit probes/sec via a
	// token bucket (§12.5 line 307 storage.t4KmsProbeRateLimit). A
	// non-positive value disables rate limiting. The bucket is built
	// once and shared across cycles, so a sweep that overruns Interval
	// under a large T4 fleet keeps honouring the ceiling instead of
	// resetting its burst allowance each tick.
	RateLimit float64
	// Now returns the current time. A nil value defaults to
	// time.Now().UTC, the same clock the Lifecycle uses.
	Now func() time.Time

	limiterOnce sync.Once
	limiter     *rate.Limiter
}

var (
	_ manager.Runnable               = (*Prober)(nil)
	_ manager.LeaderElectionRunnable = (*Prober)(nil)
)

// Start runs the probe loop until ctx is cancelled. Start runs one
// cycle immediately so a freshly-elected leader does not wait a full
// interval before refreshing the gauge.
func (p *Prober) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("tenantkms-prober")
	interval := p.Interval
	switch {
	case interval <= 0:
		interval = DefaultProbeInterval
	case interval < MinProbeInterval:
		// spec: §12.5 line 307 — the cadence floor is 60s; clamp up so
		// a misconfigured value cannot busy-probe the KMS backend.
		logger.Info("t4 KMS probe interval below floor; clamping",
			"configured", interval.String(), "floor", MinProbeInterval.String())
		interval = MinProbeInterval
	}

	p.runCycle(ctx, logger)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			p.runCycle(ctx, logger)
		}
	}
}

// NeedLeaderElection reports that only the elected leader runs the
// probe loop, so replicas never produce competing gauge samples for
// the same tenant.
func (p *Prober) NeedLeaderElection() bool { return true }

// RunCycle runs a single probe pass against every T4 tenant the
// TenantSource returns. It is the public entry point for tests and
// for one-shot callers (the admin re-assert path); the Start loop
// calls it on every tick.
func (p *Prober) RunCycle(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("tenantkms-prober")
	return p.runCycle(ctx, logger)
}

type probeLogger interface {
	Error(err error, msg string, keysAndValues ...any)
	Info(msg string, keysAndValues ...any)
}

func (p *Prober) runCycle(ctx context.Context, logger probeLogger) error {
	tenants, err := p.Tenants.T4Tenants(ctx)
	if err != nil {
		logger.Error(err, "list T4 tenants for KMS probe")
		return err
	}
	limiter := p.rateLimiter()
	now := p.now()
	for _, tenant := range tenants {
		// spec: §12.5 line 307 — token-bucket rate limit so a large T4
		// fleet does not burst the KMS backend. A cancelled ctx ends
		// the sweep cleanly; the next tick retries from the top.
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return nil
			}
		}
		err := p.Lifecycle.ProbeAvailability(ctx, tenant, WorkspaceTierT4)
		if err == nil {
			if p.Metrics != nil {
				p.Metrics.LastSuccessTimestamp.WithLabelValues(tenant).Set(float64(now.Unix()))
				p.Metrics.ResultTotal.WithLabelValues(tenant, "success").Inc()
			}
			continue
		}
		result := classifyProbeError(err)
		if p.Metrics != nil {
			p.Metrics.ResultTotal.WithLabelValues(tenant, result).Inc()
		}
		logger.Error(err, "T4 KMS probe failed", "tenant_id", tenant, "result", result)
	}
	return nil
}

// classifyProbeError reduces a Lifecycle.ProbeAvailability error to a
// bounded label value so the result counter stays low-cardinality.
func classifyProbeError(err error) string {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		return "key_not_found"
	case errors.Is(err, ErrKeyUnavailable):
		return "key_unavailable"
	default:
		return "error"
	}
}

func (p *Prober) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// rateLimiter lazily builds the §12.5 line 307 token bucket from
// RateLimit and caches it so the burst allowance is shared across every
// cycle. A non-positive RateLimit returns nil (rate limiting disabled).
func (p *Prober) rateLimiter() *rate.Limiter {
	p.limiterOnce.Do(func() {
		if p.RateLimit > 0 {
			burst := int(p.RateLimit)
			if burst < 1 {
				burst = 1
			}
			p.limiter = rate.NewLimiter(rate.Limit(p.RateLimit), burst)
		}
	})
	return p.limiter
}
