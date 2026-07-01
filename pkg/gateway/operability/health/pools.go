// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"sort"
	"strings"
	"time"
)

// PoolHealth is the §25.17 pool-keyed health body the watchdog reads to
// confirm recovery. The §25.17 worked example (Step 6, spec line 5254)
// issues `GET /v1/admin/health/default-gvisor` immediately after the
// gateway diagnostic call and reads off whether the WarmPoolExhausted
// alert has resolved. The gateway's /v1/admin/health/{component} route
// resolves a warm-pool name to this view when no health subsystem of
// that name is registered, so the scenario's single-base-URL reading
// holds (the call lands on the same gateway host as the rest of the
// admin health surface).
//
// spec: §25.17 line 5254.
type PoolHealth struct {
	// Pool is the §5.2 pool identifier the call resolved.
	Pool string `json:"pool"`

	// Status is the same vocabulary the rest of the health surface uses:
	// "healthy" when no warm-pool alert is firing and the pool is not
	// draining, otherwise "degraded".
	Status string `json:"status"`

	// Phase mirrors the §15.1 line 797 pool lifecycle phase the admin
	// pool GET surfaces ("active" or "draining").
	Phase string `json:"phase"`

	// ActiveAlerts lists the §16.5 warm-pool alerts currently firing on
	// this replica's in-process tracker. An empty list is the signal the
	// §25.17 watchdog reads as "the WarmPoolExhausted alert has resolved".
	//
	// The in-process alert tracker (§25.13) is rule-grained, not
	// per-series, so this is the set of firing warm-pool alerts across
	// every pool rather than alerts attributed to this pool alone. The
	// Prometheus-backed alert surface carries the per-pool label set; the
	// in-process fallback reports at the granularity it can observe.
	ActiveAlerts []string `json:"activeAlerts"`

	// LastTransition is the most recent pool lifecycle transition time
	// (the drain timestamp when draining, otherwise the pool's last
	// update). Omitted when zero.
	LastTransition time.Time `json:"lastTransition,omitempty"`
}

// PoolHealthResolver resolves a warm-pool name to its §25.17 health view.
// The health Handler consults it for an /v1/admin/health/{component}
// request whose component is not a registered subsystem. A nil resolver
// disables pool resolution, so an unknown name returns the §25.3 line 547
// UNKNOWN_HEALTH_COMPONENT 404 as before.
type PoolHealthResolver interface {
	// PoolHealth returns the named pool's health view. ok is false when
	// no pool of that name exists, in which case the Handler falls
	// through to the 404 path.
	PoolHealth(ctx context.Context, name string) (PoolHealth, bool)
}

// FuncPoolHealthResolver adapts plain functions into a PoolHealthResolver
// so the gateway can wire its poolstore and in-process alert tracker
// without the health package importing either. It owns the §25.17 status
// derivation (firing-alert filtering and the healthy/degraded verdict) so
// that logic is unit-testable independent of the gateway wiring.
type FuncPoolHealthResolver struct {
	// Pool reports a pool's lifecycle phase ("active"/"draining") and its
	// last-transition time. ok is false when no such pool exists. Required.
	Pool func(ctx context.Context, name string) (phase string, lastTransition time.Time, ok bool)

	// Firing returns the names of every alert currently firing on this
	// replica. May be nil (the §25.13 in-process tracker can be disabled),
	// in which case ActiveAlerts is always empty.
	Firing func() []string
}

// PoolHealth implements PoolHealthResolver.
func (f FuncPoolHealthResolver) PoolHealth(ctx context.Context, name string) (PoolHealth, bool) {
	if f.Pool == nil {
		return PoolHealth{}, false
	}
	phase, last, ok := f.Pool(ctx, name)
	if !ok {
		return PoolHealth{}, false
	}
	alerts := []string{}
	if f.Firing != nil {
		for _, n := range f.Firing() {
			if IsWarmPoolAlert(n) {
				alerts = append(alerts, n)
			}
		}
		sort.Strings(alerts)
	}
	status := StatusHealthy
	if len(alerts) > 0 || phase == PhaseDraining {
		status = StatusDegraded
	}
	return PoolHealth{
		Pool:           name,
		Status:         string(status),
		Phase:          phase,
		ActiveAlerts:   alerts,
		LastTransition: last,
	}, true
}

// PhaseDraining is the §15.1 line 797 pool lifecycle phase string the
// resolver treats as degraded. It matches poolstore.PhaseDraining; it is
// duplicated here so the health package does not depend on poolstore.
const PhaseDraining = "draining"

// IsWarmPoolAlert reports whether the named §16.5 alert pertains to a
// warm pool, so the §25.17 pool-keyed health view attributes it to the
// pool. Warm-pool alerts (WarmPool*) and pool scaling / bootstrap /
// config alerts (Pool*) qualify; credential-pool alerts (CredentialPool*)
// and the PgBouncer connection-pool alert are a different concern and are
// excluded by the prefix test.
//
// spec: §16.5 (alert catalogue); §25.17 line 5254 (pool health view).
func IsWarmPoolAlert(name string) bool {
	return strings.HasPrefix(name, "WarmPool") || strings.HasPrefix(name, "Pool")
}
