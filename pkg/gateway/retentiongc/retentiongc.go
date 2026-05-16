// SPDX-License-Identifier: MIT

// Package retentiongc implements the §7.1 artifact-retention garbage
// collector. A periodic sweep deletes the workspace snapshot,
// transcript, and blobs of every terminal session whose configurable
// retention TTL has elapsed (`POST /v1/sessions/{id}/extend-retention`
// sets the deadline; the §7.1 default TTL is 7 days).
//
// A session under a §12.8 legal hold is exempt: the sweep skips any
// session whose legal_hold flag is set, so material under a
// preservation order is never collected.
package retentiongc

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// DefaultSweepInterval is how often the retention GC runs when no
// interval is configured.
const DefaultSweepInterval = 15 * time.Minute

// ArtifactDeleter removes a session's artifacts from one store and
// returns the count removed. The transcript store's and the blob
// store's DeleteBySession methods satisfy it.
type ArtifactDeleter func(ctx context.Context, tenantID, sessionID string) (int, error)

// Artifact pairs a store name with its per-session deleter.
type Artifact struct {
	Name   string
	Delete ArtifactDeleter
}

// TenantLister enumerates the tenants the sweep covers.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// StaticTenants is a fixed-slice TenantLister for tests and the
// minimal gateway.
type StaticTenants []string

// ListTenants implements TenantLister.
func (s StaticTenants) ListTenants(_ context.Context) ([]string, error) { return s, nil }

// Collector runs the periodic §7.1 artifact-retention sweep.
type Collector struct {
	store     sessionstore.Store
	tenants   TenantLister
	artifacts []Artifact
	interval  time.Duration
	clock     func() time.Time
}

// Options configures a Collector. A zero field selects its default.
type Options struct {
	// Interval overrides DefaultSweepInterval.
	Interval time.Duration
	// Clock overrides time.Now.
	Clock func() time.Time
}

// New returns a Collector. artifacts is the ordered set of per-session
// artifact stores the sweep deletes from.
func New(store sessionstore.Store, tenants TenantLister, artifacts []Artifact, opts Options) *Collector {
	c := &Collector{
		store:     store,
		tenants:   tenants,
		artifacts: append([]Artifact(nil), artifacts...),
		interval:  opts.Interval,
		clock:     opts.Clock,
	}
	if c.interval <= 0 {
		c.interval = DefaultSweepInterval
	}
	if c.clock == nil {
		c.clock = func() time.Time { return time.Now().UTC() }
	}
	return c
}

// eligible reports whether a session is due for artifact collection at
// now: it must be terminal, past a set retention deadline, and not
// under a §12.8 legal hold.
func eligible(s sessionstore.Session, now time.Time) bool {
	if !session.IsTerminal(s.State) || s.LegalHold {
		return false
	}
	return !s.RetentionExpiresAt.IsZero() && !s.RetentionExpiresAt.After(now)
}

// Tick runs one sweep at now and returns the count of sessions whose
// artifacts it collected.
func (c *Collector) Tick(ctx context.Context, now time.Time) (int, error) {
	tenants, err := c.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	collected := 0
	for _, tenant := range tenants {
		n, err := c.sweepTenant(ctx, tenant, now)
		collected += n
		if err != nil {
			return collected, err
		}
	}
	return collected, nil
}

func (c *Collector) sweepTenant(ctx context.Context, tenant string, now time.Time) (int, error) {
	rows, err := c.store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		return 0, err
	}
	collected := 0
	for _, row := range rows {
		if !eligible(row, now) {
			continue
		}
		// Re-read the row immediately before the irreversible artifact
		// deletion: a legal hold placed since the List snapshot must
		// still exempt the session from collection.
		fresh, err := c.store.Get(ctx, tenant, row.ID)
		if err != nil {
			return collected, err
		}
		if !eligible(fresh, now) {
			continue
		}
		for _, a := range c.artifacts {
			if _, err := a.Delete(ctx, tenant, row.ID); err != nil {
				return collected, fmt.Errorf("retentiongc: %s DeleteBySession(%s): %w", a.Name, row.ID, err)
			}
		}
		// Clear the snapshot reference and retention deadline so the
		// collected session is not swept again.
		if _, err := c.store.Update(ctx, tenant, row.ID, func(s *sessionstore.Session) error {
			s.WorkspaceSnapshot = nil
			s.RetentionExpiresAt = time.Time{}
			return nil
		}); err != nil {
			return collected, err
		}
		collected++
	}
	return collected, nil
}

// Run drives the sweep on the configured interval until ctx is done.
// onTick, when non-nil, receives each sweep's collected-count and
// error.
func (c *Collector) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := c.Tick(ctx, c.clock())
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}
