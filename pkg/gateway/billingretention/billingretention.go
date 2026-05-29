// SPDX-License-Identifier: MIT

// Package billingretention implements the §11.2.1 billing-event
// retention controls: the compliance-aware retention floor enforced at
// gateway startup, and the periodic pruner that deletes billing events
// past the configured `billing.retentionDays` window.
//
// Billing events are append-only (§11.7 immutability). Retention
// pruning is the lifecycle path that removes events once they age past
// the retention window, distinct from the §12.8 tenant-teardown and
// user-erasure paths.
package billingretention

import (
	"context"
	"fmt"
	"time"
)

const (
	// DefaultRetentionDays is the §11.2.1 `billing.retentionDays`
	// default: 395 days (~13 months) to support annual billing cycles
	// and dispute resolution. spec: §11.2.1 line 151.
	DefaultRetentionDays = 395

	// DefaultSweepInterval is how often the retention pruner runs when
	// no interval is configured. The retention window is measured in
	// days, so an hourly sweep holds the ledger within a day of the
	// configured floor without a tight delete loop.
	DefaultSweepInterval = time.Hour

	// MinSweepInterval floors a configured sweep interval so a
	// misconfiguration cannot drive a busy-loop.
	MinSweepInterval = time.Minute
)

// complianceFloorDays maps a regulated §11.2.1 complianceProfile to its
// minimum `billing.retentionDays` floor. An empty or unregulated
// profile has no floor. spec: §11.2.1 line 151.
var complianceFloorDays = map[string]int{
	"hipaa":   2190, // 6 years, 45 C.F.R. §164.530(j)
	"soc2":    365,
	"fedramp": 365,
}

// ComplianceFloorDays returns the §11.2.1 retention floor in days for a
// complianceProfile, or 0 when the profile is empty or unregulated.
// spec: §11.2.1 line 151. F-11.2.15.
func ComplianceFloorDays(profile string) int { return complianceFloorDays[profile] }

// RetentionFloorError reports that the configured `billing.retentionDays`
// is below the floor an active complianceProfile mandates. Its Error()
// is the §11.2.1 CONFIG_INVALID startup message verbatim.
type RetentionFloorError struct {
	RetentionDays int
	Profile       string
	FloorDays     int
}

func (e *RetentionFloorError) Error() string {
	return fmt.Sprintf(
		"CONFIG_INVALID: billing.retentionDays below compliance floor for complianceProfile '%s' (configured %d days, floor %d days)",
		e.Profile, e.RetentionDays, e.FloorDays)
}

// ValidateRetentionDays enforces the §11.2.1 compliance-aware retention
// floor. profiles is the set of complianceProfile values active across
// the deployment's tenants. It returns a *RetentionFloorError naming the
// binding (highest-floor) profile when retentionDays is below that
// floor, and nil otherwise. A non-positive retentionDays is treated as
// DefaultRetentionDays before the comparison so an unset value cannot
// bypass a floor. spec: §11.2.1 line 151. F-11.2.15.
func ValidateRetentionDays(retentionDays int, profiles []string) error {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	bindingProfile := ""
	bindingFloor := 0
	for _, p := range profiles {
		if floor := ComplianceFloorDays(p); floor > bindingFloor {
			bindingFloor = floor
			bindingProfile = p
		}
	}
	if bindingFloor > 0 && retentionDays < bindingFloor {
		return &RetentionFloorError{RetentionDays: retentionDays, Profile: bindingProfile, FloorDays: bindingFloor}
	}
	return nil
}

// ClampSweepInterval applies the sweep-interval bounds: a non-positive
// duration selects DefaultSweepInterval, and a positive duration below
// MinSweepInterval is raised to the floor.
func ClampSweepInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultSweepInterval
	}
	if d < MinSweepInterval {
		return MinSweepInterval
	}
	return d
}

// TenantLister enumerates the tenants the retention sweep covers.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// StaticTenants is a fixed-slice TenantLister for tests and the minimal
// gateway.
type StaticTenants []string

// ListTenants implements TenantLister.
func (s StaticTenants) ListTenants(_ context.Context) ([]string, error) { return s, nil }

// Deleter prunes a tenant's billing events older than cutoff. The
// billingstore.Store interface satisfies it via DeleteOlderThan.
type Deleter interface {
	DeleteOlderThan(ctx context.Context, tenantID string, cutoff time.Time) (int, error)
}

// Pruner runs the periodic §11.2.1 billing-event retention sweep.
type Pruner struct {
	tenants       TenantLister
	store         Deleter
	retentionDays int
	interval      time.Duration
	clock         func() time.Time
}

// Options configures a Pruner. A zero field selects its default.
type Options struct {
	// RetentionDays overrides DefaultRetentionDays. A non-positive
	// value selects the default.
	RetentionDays int
	// Interval overrides DefaultSweepInterval, clamped to MinSweepInterval.
	Interval time.Duration
	// Clock overrides time.Now.
	Clock func() time.Time
}

// New returns a Pruner that prunes via store and enumerates tenants via
// tenants.
func New(store Deleter, tenants TenantLister, opts Options) *Pruner {
	p := &Pruner{
		tenants:       tenants,
		store:         store,
		retentionDays: opts.RetentionDays,
		interval:      ClampSweepInterval(opts.Interval),
		clock:         opts.Clock,
	}
	if p.retentionDays <= 0 {
		p.retentionDays = DefaultRetentionDays
	}
	if p.clock == nil {
		p.clock = func() time.Time { return time.Now().UTC() }
	}
	return p
}

// RetentionDays returns the effective retention window in days.
func (p *Pruner) RetentionDays() int { return p.retentionDays }

// Tick runs one retention sweep at now: it prunes each tenant's billing
// events whose created_at precedes now − retentionDays and returns the
// total count removed. A per-tenant delete error does not abort the
// remaining tenants; the first such error is returned alongside the
// partial count so the sweep is best-effort. spec: §11.2.1 line 151.
func (p *Pruner) Tick(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.AddDate(0, 0, -p.retentionDays)
	tenants, err := p.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	pruned := 0
	var firstErr error
	for _, t := range tenants {
		n, err := p.store.DeleteOlderThan(ctx, t, cutoff)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pruned += n
	}
	return pruned, firstErr
}

// Run drives Tick on the configured interval until ctx is cancelled.
// onTick, when non-nil, receives each sweep's pruned count and error.
func (p *Pruner) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := p.Tick(ctx, p.clock())
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}
