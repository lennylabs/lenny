// SPDX-License-Identifier: MIT

// Package elicitationfloor sources the §9.2 / §17.2 platform-wide
// elicitation content-integrity enforcement floor from the
// lenny-deployment-phase-stamp ConfigMap at gateway runtime. An operator
// who raises or lowers the floor via `helm upgrade` rewrites the
// ConfigMap's security.elicitationContentIntegrity.floor key; the gateway
// reads that key at startup and on subsequent change events and applies it
// as the lower bound of every tenant's effective enforcement mode
// (effective = max(floor, tenant_stored)) without a pod restart.
//
// spec: §17.2 line 86 ("The gateway reads this key at startup and on
// ConfigMap change events and applies it as the lower bound of every
// tenant's effective enforcement mode"); §9.2 line 64.
package elicitationfloor

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
)

// ConfigMapDataKey is the §17.2 line 86 data key on the
// lenny-deployment-phase-stamp ConfigMap that carries the scalar floor
// string ("enforce" | "detect-only" | "off").
const ConfigMapDataKey = "security.elicitationContentIntegrity.floor"

// DefaultConfigMapName is the §17.2 phase-stamp ConfigMap that co-tenants
// the floor key alongside the feature-flag keys.
const DefaultConfigMapName = "lenny-deployment-phase-stamp"

// DefaultReconcileInterval is the cadence the gateway re-reads the floor
// key from the ConfigMap. The spec wording is "on ConfigMap change
// events"; the gateway client is a direct (non-cached) controller-runtime
// client, so the change is observed by polling at this bounded-staleness
// cadence rather than via an informer watch. The interval matches the
// other gateway gauge/endpoint pollers (§12.4 replica poller, §16.5
// weakened-mode gauge).
const DefaultReconcileInterval = 30 * time.Second

// Provider holds the current platform floor as a raw §9.2 mode string,
// read concurrently by the per-request elicitation mode resolver, the
// admin PUT below-floor guard, and the §16.5 weakened-mode gauge export.
// The zero value reads as "" which the elicitation resolver treats as the
// §9.2 line 64 platform-floor default of off.
type Provider struct {
	mu    sync.RWMutex
	floor string
}

// NewProvider returns a Provider seeded with the startup floor value
// (typically the --elicitation-content-integrity-floor flag). An invalid
// or empty seed is retained verbatim so the existing flag-empty behaviour
// (treated as off, no floor guard) is preserved until the ConfigMap
// reconcile supplies a valid value.
func NewProvider(initial string) *Provider {
	return &Provider{floor: initial}
}

// Floor returns the current platform floor as a raw mode string.
func (p *Provider) Floor() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.floor
}

// Set records a new floor and reports the previous value and whether the
// value changed. An invalid mode string is ignored (changed=false) so a
// malformed ConfigMap value can never weaken or corrupt the in-force
// floor; callers should validate and log before relying on the change
// signal, but Set guards regardless.
func (p *Provider) Set(floor string) (previous string, changed bool) {
	if !elicitation.EnforcementMode(floor).IsValid() {
		p.mu.RLock()
		prev := p.floor
		p.mu.RUnlock()
		return prev, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.floor == floor {
		return p.floor, false
	}
	prev := p.floor
	p.floor = floor
	return prev, true
}

// FloorReader reads the current floor value from the phase-stamp
// ConfigMap. ReadFloor returns (value, present, error): present is false
// when the source carries no usable floor key (the ConfigMap is absent or
// the key is missing/empty), in which case the reconciler retains the
// last-known value rather than weakening to a default. The production
// implementation reads the ConfigMap via the Kubernetes API; tests stub
// it.
type FloorReader interface {
	ReadFloor(ctx context.Context) (value string, present bool, err error)
}

// Reconciler drives a periodic re-read of the §17.2 floor key, updating a
// Provider when the value changes. It mirrors the §12.4 replica-count
// poller: the first read fires immediately so startup converges within one
// API round trip, a read error or absent key retains the last-known floor,
// and an invalid value is logged and ignored.
type Reconciler struct {
	// Reader sources the floor value from the phase-stamp ConfigMap.
	// Required.
	Reader FloorReader
	// Provider receives each adopted floor value. Required.
	Provider *Provider
	// Interval is the re-read cadence. Zero selects
	// DefaultReconcileInterval.
	Interval time.Duration
	// Logf, when set, receives a one-line diagnostic on read errors,
	// invalid values, and adopted changes.
	Logf func(format string, args ...any)
	// OnChange, when set, is invoked after the Provider adopts a new floor
	// value, with the previous and current floor. The §17.2 audit events
	// (platform.elicitation_content_integrity_floor_changed and the
	// per-tenant tenant.elicitation_content_integrity_floor_clamp fanout)
	// carry the operator OIDC sub from the helm-upgrade render path
	// (§16.7 line 676 changed_by_sub) and are emitted by the chart, not by
	// this runtime observation point. OnChange is a logging/metrics hook
	// only; it must not attempt to synthesize those events.
	OnChange func(previous, current string)
}

// Run reconciles until ctx is cancelled. It blocks; callers start it in a
// goroutine. A reconciler missing a required seam is a no-op.
func (r *Reconciler) Run(ctx context.Context) {
	if r.Reader == nil || r.Provider == nil {
		return
	}
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	r.reconcileOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx)
		}
	}
}

func (r *Reconciler) reconcileOnce(ctx context.Context) {
	value, present, err := r.Reader.ReadFloor(ctx)
	if err != nil {
		if r.Logf != nil {
			r.Logf("elicitationfloor: phase-stamp read failed, retaining floor %q: %v", r.Provider.Floor(), err)
		}
		return
	}
	if !present {
		// The ConfigMap is absent, or it exists without a usable floor
		// key (a pre-floor-key or hand-edited ConfigMap). Retain the
		// last-known floor rather than weakening to a default.
		return
	}
	if !elicitation.EnforcementMode(value).IsValid() {
		if r.Logf != nil {
			r.Logf("elicitationfloor: phase-stamp floor value %q is not one of {off, detect-only, enforce}; retaining %q",
				value, r.Provider.Floor())
		}
		return
	}
	prev, changed := r.Provider.Set(value)
	if !changed {
		return
	}
	if r.Logf != nil {
		r.Logf("elicitationfloor: §17.2 platform floor changed %q -> %q from phase-stamp ConfigMap", prev, value)
	}
	if r.OnChange != nil {
		r.OnChange(prev, value)
	}
}
