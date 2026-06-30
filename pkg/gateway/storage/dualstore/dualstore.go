// SPDX-License-Identifier: MIT

// Package dualstore implements the §10.1 "Dual-store unavailability"
// degraded mode for a gateway replica.
//
// When both Postgres and Redis are simultaneously unreachable a replica
// cannot acquire or verify coordination leases through either mechanism.
// The Monitor in this package detects that state on a periodic probe and
// drives the observable contract §10.1 mandates:
//
//   - The `lenny_dual_store_unavailable` gauge is pinned to 1 the moment
//     both stores are declared unreachable and cleared to 0 once at
//     least one recovers (which also fires the §16.5 DualStoreUnavailable
//     alert).
//   - Every active client SSE stream receives a `PLATFORM_DEGRADED`
//     event carrying `{"reason":"dual_store_unavailable","retry_after":10}`
//     within one probe interval of detection.
//   - `Unavailable()` reports the live state so the session-creation
//     handler can reject `session.create` with 503 + `Retry-After: 10`.
//   - A per-replica `dualStoreUnavailableMaxSeconds` countdown is
//     anchored at the moment this replica first detects the outage (not
//     reset by coordinator crashes). When it elapses the Monitor calls
//     the OnHoldExpired hook so the operator-facing graceful-termination
//     path can run.
//
// The Monitor is store-handle agnostic: it takes two reachability probes
// (Postgres, Redis) so a test can drive the state machine deterministically
// and the gateway can wire `pgxpool.Pool.Ping` / `redis.Client.Ping`.
//
// spec: §10.1 — "Dual-store unavailability (Redis + Postgres both down)".
package dualstore

import (
	"context"
	"sync"
	"time"
)

// DefaultMaxUnavailable is the §10.1 item 4 `dualStoreUnavailableMaxSeconds`
// default: the per-replica window after which sessions with no successful
// store interaction begin graceful termination.
// spec: §10.1 item 4 — "If dual unavailability exceeds
// dualStoreUnavailableMaxSeconds (default: 60s)".
const DefaultMaxUnavailable = 60 * time.Second

// DefaultProbeInterval is the cadence at which the Monitor re-probes both
// stores. It is well under the 1-second SSE-notification budget for
// detection-to-broadcast so a transition is observed and broadcast
// promptly, while staying cheap (a Ping per store per tick).
const DefaultProbeInterval = 2 * time.Second

// degradedReason is the §10.1 PLATFORM_DEGRADED payload reason field.
const degradedReason = "dual_store_unavailable"

// PlatformDegradedEventType is the SSE event type pushed to active client
// streams while the dual-store degraded mode holds. spec: §10.1 item 5.
const PlatformDegradedEventType = "PLATFORM_DEGRADED"

// PlatformDegradedData is the §10.1 item 5 SSE payload:
// `{"reason":"dual_store_unavailable","retry_after":10}`.
const PlatformDegradedData = `{"reason":"dual_store_unavailable","retry_after":10}`

// Probe reports whether a backing store is reachable. The gateway wires
// one Probe per store (Postgres, Redis); a nil Probe is treated as
// "reachable" so a replica with only one store wired never enters the
// dual-store degraded mode (it has no second store that can be "also"
// down).
type Probe func(ctx context.Context) bool

// GaugeSetter receives the §10.1 line 45 `lenny_dual_store_unavailable`
// gauge transitions. gatewaymetrics.Metrics satisfies it.
type GaugeSetter interface {
	SetDualStoreUnavailable(unavailable bool)
}

// Broadcaster pushes a platform-level SSE event to every active client
// stream. sessionevents.Bus satisfies it via Broadcast. The return value
// is the number of streams reached (used only for the structured log).
type Broadcaster interface {
	Broadcast(eventType, data string, now time.Time) int
}

// Monitor is the §10.1 per-replica dual-store degraded-mode state machine.
type Monitor struct {
	// PostgresProbe and RedisProbe report each store's reachability. A
	// nil probe is treated as reachable.
	PostgresProbe Probe
	RedisProbe    Probe
	// Gauge receives the lenny_dual_store_unavailable transitions. Nil
	// disables the gauge; the state machine still runs.
	Gauge GaugeSetter
	// Streams receives the PLATFORM_DEGRADED broadcast on detection. Nil
	// disables the broadcast.
	Streams Broadcaster
	// MaxUnavailable is the per-replica dualStoreUnavailableMaxSeconds
	// countdown. Zero falls back to DefaultMaxUnavailable.
	MaxUnavailable time.Duration
	// Interval is the probe cadence. Zero falls back to
	// DefaultProbeInterval.
	Interval time.Duration
	// Now is the injectable clock. Nil falls back to time.Now.
	Now func() time.Time
	// Logf, when set, receives a one-line diagnostic on every state
	// transition and on hold-timer expiry.
	Logf func(format string, args ...any)
	// OnHoldExpired, when set, is invoked once per outage at the moment
	// the per-replica dualStoreUnavailableMaxSeconds countdown elapses
	// while the outage is still ongoing. The §10.1 item-4
	// graceful-termination path (session.terminated with reason
	// store_unavailable, emitted when Postgres recovers) is wired here;
	// the Monitor owns only the timer, not the per-session disposition.
	OnHoldExpired func(outageStart time.Time)

	mu          sync.Mutex
	unavailable bool
	since       time.Time
	holdExpired bool
}

// Unavailable reports whether this replica currently observes both stores
// unreachable. The session-creation handler consults it to reject
// `session.create` with 503 + Retry-After while the degraded mode holds.
// Safe for concurrent use; a nil Monitor reports false (the no-store /
// in-memory posture is never degraded).
func (m *Monitor) Unavailable() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unavailable
}

// Run drives the probe loop until ctx is cancelled. It probes both stores
// every Interval and advances the state machine. Run blocks; callers
// start it in a goroutine.
func (m *Monitor) Run(ctx context.Context) {
	interval := m.Interval
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Probe once immediately so a replica that starts during an outage
	// declares the degraded mode without waiting a full interval.
	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

// tick runs one probe round and advances the state machine. Exposed
// (unexported) so the test suite can step the clock deterministically
// without a real ticker.
func (m *Monitor) tick(ctx context.Context) {
	down := !m.probe(ctx, m.PostgresProbe) && !m.probe(ctx, m.RedisProbe)
	now := m.now()

	m.mu.Lock()
	switch {
	case down && !m.unavailable:
		// Transition into the degraded mode. Anchor the per-replica
		// countdown at this detection instant (§10.1 item 4 timer
		// anchoring) and capture the side effects to run outside the
		// lock.
		m.unavailable = true
		m.since = now
		m.holdExpired = false
		m.mu.Unlock()
		// spec: §10.1 item 5 — fire the gauge and the PLATFORM_DEGRADED
		// broadcast immediately on detection.
		if m.Gauge != nil {
			m.Gauge.SetDualStoreUnavailable(true)
		}
		reached := 0
		if m.Streams != nil {
			reached = m.Streams.Broadcast(PlatformDegradedEventType, PlatformDegradedData, now)
		}
		m.logf("dualstore: Postgres and Redis both unreachable; entering §10.1 degraded mode (PLATFORM_DEGRADED broadcast to %d stream(s), reason=%s)", reached, degradedReason)
		return

	case down && m.unavailable:
		// Still down: check the per-replica hold timer.
		maxUnavail := m.MaxUnavailable
		if maxUnavail <= 0 {
			maxUnavail = DefaultMaxUnavailable
		}
		if !m.holdExpired && now.Sub(m.since) >= maxUnavail {
			m.holdExpired = true
			start := m.since
			m.mu.Unlock()
			m.logf("dualstore: dual-store outage exceeded dualStoreUnavailableMaxSeconds=%s; sessions with no successful store interaction become eligible for graceful termination on store recovery", maxUnavail)
			if m.OnHoldExpired != nil {
				m.OnHoldExpired(start)
			}
			return
		}
		m.mu.Unlock()
		return

	case !down && m.unavailable:
		// Recovery: at least one store is back.
		m.unavailable = false
		start := m.since
		m.since = time.Time{}
		expired := m.holdExpired
		m.holdExpired = false
		m.mu.Unlock()
		if m.Gauge != nil {
			m.Gauge.SetDualStoreUnavailable(false)
		}
		m.logf("dualstore: a store recovered; clearing §10.1 degraded mode (outage lasted %s, holdExpired=%v)", now.Sub(start).Truncate(time.Second), expired)
		return

	default:
		// Steady-state healthy: nothing to do.
		m.mu.Unlock()
	}
}

func (m *Monitor) probe(ctx context.Context, p Probe) bool {
	if p == nil {
		// A store that is not wired cannot be the "also down" half of the
		// dual-store condition, so treat it as reachable.
		return true
	}
	return p(ctx)
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Monitor) logf(format string, args ...any) {
	if m.Logf == nil {
		return
	}
	m.Logf(format, args...)
}
