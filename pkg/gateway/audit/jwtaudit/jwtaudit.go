// SPDX-License-Identifier: MIT

// Package jwtaudit emits the §10.3 / §13.3 JWT signing-key rotation
// audit row when the gateway's RotatingVerifier advances its key
// lifecycle, and (via CAObserver below) the matching
// `platform.ca_rotated` row for §10.3 CA-rotation stage transitions.
// The package is the bridge between pkg/auth/jwt / pkg/mtls (which
// know when a rotation happened but not how the audit chain is
// wired) and pkg/gateway/policy.AuditAppender (the per-tenant chain
// writer). A jwt.RotationObserver in this package converts each
// jwt.RotationEvent into one `platform.jwt_signing_key_rotated`
// audit row on the platform chain.
package jwtaudit

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/mtls"
)

// EventTypeJWTSigningKeyRotated is the §13.3 audit event type the
// gateway writes per JWT signing-key rotation lifecycle transition.
// The name is the canonical key the §11.7 OCSF prefix catalog
// maps under the `platform.` namespace to OCSF apiActivity (6003).
const EventTypeJWTSigningKeyRotated = "platform.jwt_signing_key_rotated"

// PlatformTenantID is the tenant scope a platform-wide audit row
// is committed under. Per pkg/gateway/admin.ChainAuditSink, a
// platform-admin action with no actor tenant lands on the
// `platform` chain so a tenant-scoped audit query never returns
// platform-wide events.
const PlatformTenantID = "platform"

// Payload is the wire format persisted in the audit row's payload
// blob for one §10.3 JWT signing-key rotation transition. The
// fields mirror the operator-facing runbook description; the row's
// hash chain seals the payload so a downstream consumer reads
// exactly the same bytes the gateway committed.
type Payload struct {
	// FromKeyID is the kid leaving the role named by Transition.
	// On promote_current it is the outgoing current kid (now in
	// the previous set). On retire_previous it is the kid being
	// pruned from the previous set.
	FromKeyID string `json:"from_key_id"`

	// ToKeyID is the kid taking the role named by Transition. On
	// promote_current it is the incoming current kid. On
	// retire_previous it is empty (no successor takes the slot).
	ToKeyID string `json:"to_key_id,omitempty"`

	// Transition names the lifecycle step. The closed set is
	// "promote_current" or "retire_previous"; see
	// jwt.TransitionPromoteCurrent / jwt.TransitionRetirePrevious.
	Transition string `json:"transition"`

	// At is the verifier-clock instant the transition committed.
	// Emitted in RFC 3339 nanosecond precision so a downstream
	// timeline ordering survives sub-second rotations.
	At time.Time `json:"at"`
}

// Observer implements jwt.RotationObserver by committing one
// `platform.jwt_signing_key_rotated` audit row to the platform
// chain per RotationEvent. The audit append is synchronous (per
// §11.7 the chain hash must be sealed before the observer returns);
// a failed append is logged and the rotation is otherwise unaffected
// because the rotation has already committed to the verifier's
// key set.
//
// Observer holds a reference to a policy.AuditAppender so it can
// share the same hash-chain backend (Postgres in production, the
// in-memory chain set in the minimal gateway) used by every other
// audit emitter on the gateway. The 2s timeout caps the synchronous
// audit append so a slow Postgres write does not stall the
// RotatingVerifier's write lock.
type Observer struct {
	appender    policy.AuditAppender
	tenantID    string
	appendCtx   func() (context.Context, context.CancelFunc)
	logf        func(format string, v ...any)
	failedCount int // incremented when an append fails (best-effort, for metrics)
}

// Option configures an Observer at construction.
type Option func(*Observer)

// WithTenantID overrides the tenant the audit row lands on. The
// default is PlatformTenantID; tests use this to write to a
// fixture tenant chain.
func WithTenantID(id string) Option {
	return func(o *Observer) { o.tenantID = id }
}

// WithAppendTimeout overrides the per-append context timeout. The
// default of 2s caps the synchronous Postgres write so a slow
// audit backend does not stall every concurrent JWT verification.
func WithAppendTimeout(d time.Duration) Option {
	return func(o *Observer) {
		o.appendCtx = func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), d)
		}
	}
}

// WithLogger overrides the failure logger. The default routes to
// the stdlib log package.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(o *Observer) { o.logf = logf }
}

// NewObserver returns an Observer that writes via appender. Panics
// on a nil appender — the construction-time invariant prevents the
// gateway from wiring the JWT rotation audit without a working
// chain backend.
func NewObserver(appender policy.AuditAppender, opts ...Option) *Observer {
	if appender == nil {
		panic("jwtaudit: NewObserver: nil AuditAppender")
	}
	o := &Observer{
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

// OnRotation implements jwt.RotationObserver: it commits one audit
// row carrying the §10.3 transition. A serialisation or append
// failure is logged but does not propagate to the rotating
// verifier, which has already advanced its key set; an audit-write
// outage cannot be allowed to halt the rotation.
func (o *Observer) OnRotation(ev jwt.RotationEvent) {
	payload, err := json.Marshal(Payload{
		FromKeyID:  ev.FromKeyID,
		ToKeyID:    ev.ToKeyID,
		Transition: ev.Transition,
		At:         ev.At,
	})
	if err != nil {
		o.failedCount++
		o.logf("jwtaudit: marshal %s payload: %v", ev.Transition, err)
		return
	}
	ctx, cancel := o.appendCtx()
	defer cancel()
	if _, err := o.appender.Append(ctx, o.tenantID, EventTypeJWTSigningKeyRotated, payload, ev.At); err != nil {
		o.failedCount++
		o.logf("jwtaudit: append %s row: %v", ev.Transition, err)
	}
}

// FailedAppends returns the count of audit appends that have
// failed since the observer was constructed. The gateway scrapes
// this for the §16.1 observability surface so a degraded audit
// backend surfaces as a counter rather than as silent dropped
// rotation rows.
func (o *Observer) FailedAppends() int { return o.failedCount }

// Compile-time check: Observer satisfies jwt.RotationObserver, so
// it drops into RotatingVerifier.SetObserver without an adapter.
var _ jwt.RotationObserver = (*Observer)(nil)

// EventTypeCARotated is the §10.3 audit event type the gateway
// writes per CA-rotation stage transition. Like the JWT signing-key
// event above, the name lands in the `platform.*` namespace which
// §11.7 maps to OCSF apiActivity (6003).
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
// `platform.ca_rotated` audit row per stage transition. It shares
// the same policy.AuditAppender wiring as Observer above and the
// same failure semantics: a failed append is logged and counted
// but does not roll back the rotation.
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
	// Reuse the Observer-shaped Option set by routing each Option
	// against a temporary Observer and copying the configured fields.
	// Both observers carry the same configuration surface so a single
	// Option vocabulary applies to either.
	proxy := &Observer{
		appender:  appender,
		tenantID:  o.tenantID,
		appendCtx: o.appendCtx,
		logf:      o.logf,
	}
	for _, opt := range opts {
		opt(proxy)
	}
	o.tenantID = proxy.tenantID
	o.appendCtx = proxy.appendCtx
	o.logf = proxy.logf
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
