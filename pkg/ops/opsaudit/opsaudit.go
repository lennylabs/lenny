// SPDX-License-Identifier: MIT

// Package opsaudit is the lenny-ops durable audit-append funnel. Every
// §25.4 / §16.7 audit event lenny-ops originates — remediation-lock
// lifecycle, escalation flush, self-health transitions, identity
// discovery, operations-inventory queries, and the §25.6/§25.10/§25.11/
// §25.8 diagnostics/drift/backup/upgrade events — is platform-scoped
// (§11.7 line 435: every ops_event.* event routes to PlatformPostgres()
// under the platform tenant). The Recorder commits each one to the §11.7
// hash chain through a single appender so the durable audit trail the
// §25.9 audit query API and §11.7 residency rules cross-reference is
// produced, instead of the events vanishing into stderr.
//
// When no durable appender is wired (lenny-ops single-process /
// dev mode, where --postgres-dsn is unset) the Recorder logs each event
// so the emission stays observable. A durable-append failure is logged
// and counted rather than propagated: an audit-store outage must not
// halt the lock release, escalation flush, or health transition that
// produced the event.
//
// spec: §11.7 line 435 (ops_event.* route to the platform tenant);
// §25.4 lines 2338-2340 (lock audit events), 2415 (escalation_persisted),
// 2470-2476 (ops_health_status_changed), 1641 (identity.discovered),
// 1779 (operations.inventory_queried).
package opsaudit

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// PlatformTenantID is the §11.7 platform-tenant chain id lenny-ops audit
// rows commit under. The events are platform-scoped (no actor tenant), so
// they share the gateway's "platform" chain and a tenant-scoped audit
// query never returns platform-wide events. The value matches
// jwtaudit.PlatformTenantID / auditscope's platform tenant so the whole
// platform writes one chain.
//
// spec: §11.7 line 435.
const PlatformTenantID = "platform"

// Appender is the §11.7 per-tenant audit hash-chain append surface the
// Recorder writes to. *pkg/gateway/auditstore.Store satisfies it (its
// Append signature matches), so lenny-ops reuses the same durable backend
// the gateway audit path uses without importing it here. A future
// audit-shard split rotates only the auditstore Router, not this package.
type Appender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Recorder commits lenny-ops audit events to the platform chain. A nil
// appender (Recorder built with New(nil, ...)) keeps the degraded
// log-only posture so single-process / dev deployments stay observable.
type Recorder struct {
	appender Appender
	tenantID string
	timeout  time.Duration
	logf     func(format string, v ...any)
	onError  func(eventType string, err error)
	failed   atomic.Int64
}

// Option configures a Recorder at construction.
type Option func(*Recorder)

// WithTenant overrides the chain a row lands on. The default is
// PlatformTenantID; tests use this to write to a fixture tenant chain.
func WithTenant(id string) Option { return func(r *Recorder) { r.tenantID = id } }

// WithTimeout overrides the per-append context timeout. The default of
// 2s caps the synchronous Postgres write so a slow audit backend does not
// stall the lock/escalation/health path that produced the event, matching
// the jwtaudit observer posture.
func WithTimeout(d time.Duration) Option { return func(r *Recorder) { r.timeout = d } }

// WithLogger overrides the logger used for degraded-mode emission and
// append-failure lines. The default routes to the stdlib log package.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(r *Recorder) { r.logf = logf }
}

// WithOnError registers a callback invoked once per failed durable
// append (after the failure is logged and counted). cmd/lenny-ops wires
// it to a Prometheus counter so a degraded audit backend surfaces as a
// metric rather than only a log line.
func WithOnError(fn func(eventType string, err error)) Option {
	return func(r *Recorder) { r.onError = fn }
}

// New returns a Recorder that appends through appender. A nil appender
// puts the Recorder in degraded log-only mode (Durable reports false).
func New(appender Appender, opts ...Option) *Recorder {
	r := &Recorder{
		appender: appender,
		tenantID: PlatformTenantID,
		timeout:  2 * time.Second,
		logf:     log.Printf,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Durable reports whether a real audit appender is wired. When false the
// Recorder logs events instead of committing durable rows.
func (r *Recorder) Durable() bool { return r != nil && r.appender != nil }

// Record commits one platform-scoped lenny-ops audit event. fields is
// marshalled to the row payload; at defaults to the wall clock when zero
// (the auditstore seals its own clock when at is zero, but stamping here
// keeps the degraded log line and the durable row carrying the same
// instant). In degraded mode (no appender) the event is logged so the
// emission stays observable. A marshal or append failure is logged and
// counted; it never panics or blocks past the configured timeout, so an
// audit-store outage cannot halt the operation that produced the event.
func (r *Recorder) Record(eventType string, fields map[string]any, at time.Time) {
	if r == nil {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if r.appender == nil {
		// Degraded / dev mode: no durable destination, so log the event
		// to keep the emission observable (the pre-wiring posture).
		r.logf("lenny-ops: audit %s %v (no durable audit store wired)", eventType, fields)
		return
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		r.failed.Add(1)
		r.logf("lenny-ops: audit marshal %s: %v", eventType, err)
		if r.onError != nil {
			r.onError(eventType, err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	if _, err := r.appender.Append(ctx, r.tenantID, eventType, payload, at); err != nil {
		r.failed.Add(1)
		r.logf("lenny-ops: audit append %s: %v", eventType, err)
		if r.onError != nil {
			r.onError(eventType, err)
		}
	}
}

// FailedAppends returns the count of durable appends that have failed
// since the Recorder was constructed. cmd/lenny-ops scrapes it for the
// §16.1 observability surface so a degraded audit backend is visible.
func (r *Recorder) FailedAppends() int64 {
	if r == nil {
		return 0
	}
	return r.failed.Load()
}
