// SPDX-License-Identifier: MIT

// Package jwtaudit emits the `platform.ca_rotated` audit row for §10.3
// CA-rotation stage transitions. The package is the bridge between
// pkg/mtls (which knows when a rotation happened but not how the audit
// chain is wired) and pkg/gateway/policy.AuditAppender (the per-tenant
// chain writer). It also names the platform tenant that platform-wide
// audit rows are committed under.
package jwtaudit

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/mtls"
)

// PlatformTenantID is the tenant scope a platform-wide audit row
// is committed under. Per pkg/gateway/admin.ChainAuditSink, a
// platform-admin action with no actor tenant lands on the
// `platform` chain so a tenant-scoped audit query never returns
// platform-wide events.
const PlatformTenantID = "platform"

// Option configures a CAObserver at construction.
type Option func(*CAObserver)

// WithTenantID overrides the tenant the audit row lands on. The
// default is PlatformTenantID; tests use this to write to a
// fixture tenant chain.
func WithTenantID(id string) Option {
	return func(o *CAObserver) { o.tenantID = id }
}

// WithAppendTimeout overrides the per-append context timeout. The
// default of 2s caps the synchronous audit write so a slow audit
// backend does not stall the rotation that emits the row.
func WithAppendTimeout(d time.Duration) Option {
	return func(o *CAObserver) {
		o.appendCtx = func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), d)
		}
	}
}

// WithLogger overrides the failure logger. The default routes to
// the stdlib log package.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(o *CAObserver) { o.logf = logf }
}

// EventTypeCARotated is the §10.3 audit event type the gateway
// writes per CA-rotation stage transition. The name lands in the
// `platform.*` namespace, which §11.7 maps to OCSF apiActivity (6003).
const EventTypeCARotated = "platform.ca_rotated"

// CAPayload is the wire format persisted in the audit row's payload
// blob for one §10.3 CA-rotation stage transition. The fields mirror
// mtls.CARotationEvent so a downstream consumer reads the same
// structure CARotationObserver receives.
type CAPayload struct {
	// From is the stage the rotation just left.
	From string `json:"from"`

	// To is the stage the rotation just entered.
	To string `json:"to"`

	// CurrentCAID is the CA that signs new leaves after the
	// transition.
	CurrentCAID string `json:"current_ca_id"`

	// TrustedCAIDs are the CAs in the trust bundle after the
	// transition.
	TrustedCAIDs []string `json:"trusted_ca_ids"`

	// At is the rotation-clock instant the transition committed.
	At time.Time `json:"at"`
}

// CAObserver implements mtls.CARotationObserver by committing one
// `platform.ca_rotated` audit row per stage transition on the platform
// chain. The append is synchronous so the chain hash is sealed before
// the observer returns; a failed append is logged and counted but does
// not roll back the rotation, which has already committed.
type CAObserver struct {
	appender    policy.AuditAppender
	tenantID    string
	appendCtx   func() (context.Context, context.CancelFunc)
	logf        func(format string, v ...any)
	failedCount int
}

// NewCAObserver returns a CAObserver that writes via appender.
// Panics on a nil appender — the construction-time invariant
// prevents the gateway from wiring the CA rotation audit without a
// working chain backend.
func NewCAObserver(appender policy.AuditAppender, opts ...Option) *CAObserver {
	if appender == nil {
		panic("jwtaudit: NewCAObserver: nil AuditAppender")
	}
	o := &CAObserver{
		appender: appender,
		tenantID: PlatformTenantID,
		appendCtx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 2*time.Second)
		},
		logf: log.Printf,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// OnCARotation implements mtls.CARotationObserver: it commits one
// audit row carrying the §10.3 stage transition.
func (o *CAObserver) OnCARotation(ev mtls.CARotationEvent) {
	payload, err := json.Marshal(CAPayload{
		From:         string(ev.From),
		To:           string(ev.To),
		CurrentCAID:  ev.CurrentCAID,
		TrustedCAIDs: ev.TrustedCAIDs,
		At:           ev.At,
	})
	if err != nil {
		o.failedCount++
		o.logf("jwtaudit: marshal ca_rotated payload: %v", err)
		return
	}
	ctx, cancel := o.appendCtx()
	defer cancel()
	if _, err := o.appender.Append(ctx, o.tenantID, EventTypeCARotated, payload, ev.At); err != nil {
		o.failedCount++
		o.logf("jwtaudit: append ca_rotated row: %v", err)
	}
}

// FailedAppends returns the count of audit appends that have failed
// since the observer was constructed.
func (o *CAObserver) FailedAppends() int { return o.failedCount }

// Compile-time check: CAObserver satisfies mtls.CARotationObserver,
// so it drops into CARotation construction without an adapter.
var _ mtls.CARotationObserver = (*CAObserver)(nil)
