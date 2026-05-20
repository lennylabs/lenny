// SPDX-License-Identifier: MIT

package tenantkms

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
	// DefaultProbeInterval.
	Interval time.Duration
	// Now returns the current time. A nil value defaults to
	// time.Now().UTC, the same clock the Lifecycle uses.
	Now func() time.Time
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
	if interval <= 0 {
		interval = DefaultProbeInterval
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
	now := p.now()
	for _, tenant := range tenants {
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
