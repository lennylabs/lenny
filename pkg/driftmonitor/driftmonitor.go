// SPDX-License-Identifier: MIT

// Package driftmonitor implements the §13.3 line 595 NTP clock-drift
// self-monitor. Each gateway replica periodically samples its wall-clock
// offset from a reference source, publishes the absolute drift through
// the `lenny_time_drift_seconds` gauge, and degrades itself once drift
// exceeds 5 seconds: `/healthz` returns 503 (Kubernetes removes the pod
// from Service endpoints) and the Token Service exchange returns
// `503 token_validation_unavailable` rather than issuing or validating
// a token whose `exp` it cannot trust.
//
// The package is decoupled from the offset source: tests inject a
// stub `OffsetFunc`; production wires `clockinject.Offset` so the
// chaos-test injected offset is the drift signal in v1. Operators who
// deploy a real NTP probe (e.g., `chrony tracking` or an `adjtimex`
// reader) supply their own `OffsetFunc` that returns the live offset.
//
// spec: §13.3 line 595, §16.1 lenny_time_drift_seconds, §16.5
// GatewayClockDrift.
package driftmonitor

import (
	"context"
	"sync/atomic"
	"time"
)

// DegradedThreshold is the §13.3 line 595 absolute-drift ceiling above
// which a replica self-removes from the Service endpoints and returns
// 503 on exchange.
const DegradedThreshold = 5 * time.Second

// MetricEmitter publishes the drift gauge to /metrics. The gateway
// implementation in pkg/gateway/gatewaymetrics satisfies this.
type MetricEmitter interface {
	// SetTimeDrift updates the lenny_time_drift_seconds gauge. The
	// value is the signed offset in seconds (positive = ahead,
	// negative = behind).
	SetTimeDrift(seconds float64)
}

// OffsetFunc returns the current signed wall-clock offset from the
// reference. A nil OffsetFunc reports zero drift (the production
// default for a pod whose kernel time is NTP-synchronized).
type OffsetFunc func() time.Duration

// Monitor samples drift on a fixed cadence, publishes the gauge, and
// exposes a Degraded predicate consulted by /healthz and the
// Token Service exchange path.
type Monitor struct {
	source  OffsetFunc
	emitter MetricEmitter
	// offsetNanos holds the latest signed offset in nanoseconds.
	// Atomic to allow lock-free reads from the request hot path.
	offsetNanos atomic.Int64
}

// New constructs a Monitor. source may be nil (reports zero drift);
// emitter may be nil (gauge is not published, predicate still works).
func New(source OffsetFunc, emitter MetricEmitter) *Monitor {
	return &Monitor{source: source, emitter: emitter}
}

// Sample reads the current offset, publishes the gauge, and updates
// the internal state. Callers drive the cadence (Start runs Sample on
// a ticker; tests can call Sample directly without a goroutine).
func (m *Monitor) Sample() {
	if m == nil {
		return
	}
	var off time.Duration
	if m.source != nil {
		off = m.source()
	}
	m.offsetNanos.Store(off.Nanoseconds())
	if m.emitter != nil {
		m.emitter.SetTimeDrift(off.Seconds())
	}
}

// Offset returns the latest sampled signed offset.
func (m *Monitor) Offset() time.Duration {
	if m == nil {
		return 0
	}
	return time.Duration(m.offsetNanos.Load())
}

// Degraded reports whether the latest sampled offset's absolute value
// meets or exceeds the §13.3 DegradedThreshold. The /healthz handler
// and the Token Service exchange path both consult this predicate.
func (m *Monitor) Degraded() bool {
	if m == nil {
		return false
	}
	off := m.Offset()
	if off < 0 {
		off = -off
	}
	return off >= DegradedThreshold
}

// Start runs Sample on the given interval until ctx is cancelled. The
// first Sample fires immediately so the gauge populates and the
// Degraded predicate becomes meaningful without waiting one tick.
func (m *Monitor) Start(ctx context.Context, interval time.Duration) {
	if m == nil || interval <= 0 {
		return
	}
	m.Sample()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Sample()
		}
	}
}
