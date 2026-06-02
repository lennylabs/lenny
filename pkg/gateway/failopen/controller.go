// SPDX-License-Identifier: MIT

package failopen

import (
	"context"
	"time"
)

// AuditEmitter writes the §16.7 quota_failopen_started audit event when a
// replica enters fail-open mode. The gateway adapts its §11.7 audit
// appender to this seam.
//
// spec: §12.4 line 224; §16.7 (quota_failopen_started).
type AuditEmitter interface {
	EmitQuotaFailOpenStarted(ctx context.Context, serviceInstanceID string, at time.Time)
}

// Controller is the per-replica §12.4 fail-open decision point. The §11.1
// ratelimit middleware routes its counter-error edge through Enter/Exit
// and consults Evaluate for every request admitted while the shared Redis
// counter is unreachable.
//
// spec: §12.4 lines 220-224.
type Controller struct {
	timer    *CumulativeTimer
	backstop *Backstop
	replicas *ReplicaCount

	userFraction      float64
	perReplicaHardCap int64

	audit      AuditEmitter
	instanceID string
	now        func() time.Time
}

// ControllerConfig configures a Controller.
type ControllerConfig struct {
	// Timer is the cumulative fail-open timer. Required.
	Timer *CumulativeTimer
	// Backstop is the in-memory per-key emergency counter. Required.
	Backstop *Backstop
	// Replicas is the cached replica count. Required.
	Replicas *ReplicaCount
	// UserFraction is quotaUserFailOpenFraction. Non-positive selects
	// DefaultUserFailOpenFraction.
	UserFraction float64
	// PerReplicaHardCap is the global quotaPerReplicaHardCap default. A
	// non-positive value lets ComputeCeilings default it per tenant to
	// tenant_limit / 2.
	PerReplicaHardCap int64
	// Audit emits quota_failopen_started on the fail-open edge. Optional.
	Audit AuditEmitter
	// InstanceID is the OTel service_instance_id stamped on the audit
	// event identifying this replica.
	InstanceID string
	// Now overrides the clock. Nil selects time.Now.
	Now func() time.Time
}

// NewController assembles a Controller from cfg.
func NewController(cfg ControllerConfig) *Controller {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Controller{
		timer:             cfg.Timer,
		backstop:          cfg.Backstop,
		replicas:          cfg.Replicas,
		userFraction:      cfg.UserFraction,
		perReplicaHardCap: cfg.PerReplicaHardCap,
		audit:             cfg.Audit,
		instanceID:        cfg.InstanceID,
		now:               now,
	}
}

// Enter is called on the healthy→fail-open edge. It starts the cumulative
// episode and, exactly once per episode, emits the §16.7
// quota_failopen_started audit event asynchronously (the edge is rare and
// the emit must not block the admission hot path). spec: §12.4 line 224.
func (c *Controller) Enter() {
	if c == nil || c.timer == nil {
		return
	}
	if !c.timer.Enter() {
		return
	}
	if c.audit != nil {
		at := c.now()
		instance := c.instanceID
		emitter := c.audit
		go emitter.EmitQuotaFailOpenStarted(context.Background(), instance, at)
	}
}

// Exit is called on the fail-open→healthy (Redis recovery) edge. It closes
// the cumulative episode and resets the per-replica backstop counters so a
// recovered window starts clean. spec: §12.4 lines 222, 224.
func (c *Controller) Exit() {
	if c == nil {
		return
	}
	if c.timer != nil {
		c.timer.Exit()
	}
	if c.backstop != nil {
		c.backstop.Reset()
	}
}

// FailOpenRequest describes one admission request evaluated during the
// fail-open window.
type FailOpenRequest struct {
	// TenantKey identifies the per-tenant backstop counter. Empty skips
	// the per-tenant ceiling (unauthenticated request).
	TenantKey string
	// UserKey identifies the per-user backstop counter. Empty skips the
	// per-user ceiling.
	UserKey string
	// TenantLimit is the configured per-tenant per-window request limit
	// the §12.4 ceiling formula divides by cached_replica_count. A
	// non-positive value leaves the tenant unbounded (no fail-open
	// ceiling applies).
	TenantLimit int64
	// PerReplicaHardCap overrides the global hard cap for this tenant. A
	// non-positive value defaults to TenantLimit / 2 per §12.4 line 224.
	PerReplicaHardCap int64
}

// Reason classifies a fail-open admission decision.
type Reason string

const (
	// ReasonAdmit — the request is allowed under the fail-open ceilings.
	ReasonAdmit Reason = ""
	// ReasonCumulativeExceeded — the replica has spent more than
	// quotaFailOpenCumulativeMaxSeconds in fail-open mode within the
	// rolling window and is now fail-closed for quota. spec: §12.4 line 224.
	ReasonCumulativeExceeded Reason = "cumulative_exceeded"
	// ReasonUserCeiling — the per-user fail-open ceiling was reached.
	// spec: §12.4 line 222.
	ReasonUserCeiling Reason = "user_ceiling"
	// ReasonTenantCeiling — the per-tenant effective ceiling was reached.
	// spec: §12.4 line 224.
	ReasonTenantCeiling Reason = "tenant_ceiling"
)

// Decision is the outcome of evaluating one request during fail-open.
type Decision struct {
	// Admit reports whether the request is allowed.
	Admit bool
	// Reason names the binding control when Admit is false.
	Reason Reason
	// Ceiling is the per-replica ceiling that bound the rejected scope.
	Ceiling int64
}

// Evaluate decides whether req is admitted during the fail-open window. It
// first applies the §12.4 cumulative-timer fail-closed transition, then
// the per-user and per-tenant in-memory backstop ceilings. A request that
// passes every control is admitted. The cached replica count and the
// configured fraction / hard cap drive the ceiling arithmetic.
//
// spec: §12.4 lines 220-224.
func (c *Controller) Evaluate(req FailOpenRequest, now time.Time) Decision {
	// spec: §12.4 line 224 — once cumulative fail-open time exceeds the
	// configured maximum, the replica is fail-closed for quota: block all
	// new token-consuming requests until Redis recovers.
	if c.timer != nil && c.timer.Exceeded() {
		return Decision{Admit: false, Reason: ReasonCumulativeExceeded}
	}
	if req.TenantLimit <= 0 {
		// No configured tenant limit — nothing to bound during fail-open
		// beyond the cumulative timer above.
		return Decision{Admit: true}
	}
	hardCap := req.PerReplicaHardCap
	if hardCap <= 0 {
		hardCap = c.perReplicaHardCap
	}
	ceil := ComputeCeilings(req.TenantLimit, c.replicas.Get(), hardCap, c.userFraction)

	// spec: §12.4 line 222 — the per-user ceiling binds even when the
	// per-tenant counter still has headroom, so a single user cannot
	// monopolize the tenant's per-replica allocation.
	if req.UserKey != "" && ceil.User > 0 {
		if int64(c.backstop.Incr("u:"+req.UserKey, now)) > ceil.User {
			return Decision{Admit: false, Reason: ReasonUserCeiling, Ceiling: ceil.User}
		}
	}
	if req.TenantKey != "" && ceil.Tenant > 0 {
		if int64(c.backstop.Incr("t:"+req.TenantKey, now)) > ceil.Tenant {
			return Decision{Admit: false, Reason: ReasonTenantCeiling, Ceiling: ceil.Tenant}
		}
	}
	return Decision{Admit: true}
}

// CumulativeExceeded reports whether the replica is fail-closed for quota
// because the cumulative fail-open timer is over its maximum.
func (c *Controller) CumulativeExceeded() bool {
	return c != nil && c.timer != nil && c.timer.Exceeded()
}

// Sweep drops elapsed backstop counters. The gateway calls it on a low
// cadence to bound the per-replica map size.
func (c *Controller) Sweep(now time.Time) {
	if c != nil && c.backstop != nil {
		c.backstop.Sweep(now)
	}
}
