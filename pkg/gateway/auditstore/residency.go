// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// This file implements the §11.7 lines 430-435 CMP-058 platform-tenant
// audit residency gate (fail-closed). An audit event written under the
// platform tenant that references a non-platform tenant via
// target_tenant_id MUST be written to the target tenant's regional
// platform-Postgres whenever that tenant has a dataResidencyRegion set,
// because the fact that the target tenant was the subject of an
// impersonation / escrow migration / forged-tenant rejection is itself
// regulated personal data describing that tenant. Routing it to another
// region would be a prohibited cross-border transfer under GDPR Art.
// 44-46. The write path resolves routing in three rules:
//
//  1. target tenant has dataResidencyRegion set and resolvable -> write to
//     that region's platform-Postgres (PlatformPostgresForRegion).
//  2. target tenant has no dataResidencyRegion (or its tombstone snapshot
//     is unavailable) -> fall back to the global platform-Postgres.
//  3. dataResidencyRegion is set but has no storage.regions.<region>.-
//     postgresEndpoint entry, or that region's platform-Postgres is
//     unreachable -> reject fail-closed with
//     PLATFORM_AUDIT_REGION_UNRESOLVABLE (HTTP 422), emit a
//     DataResidencyViolationAttempt audit event (routed to global per
//     rule 2 so the incident does not disappear into the unreachable
//     region), and bump the residency counters.
//
// spec: §11.7 lines 430-435.

const (
	// operationPlatformAuditWrite is the §11.7 line 433 operation label on
	// the DataResidencyViolationAttempt event and the shared
	// lenny_data_residency_violation_total counter.
	operationPlatformAuditWrite = "platform_audit_write"
	// failureModeMissingEntry / failureModePostgresUnreachable are the
	// §11.7 line 433 failure_mode values distinguishing a region absent
	// from storage.regions from a present-but-unreachable region.
	failureModeMissingEntry        = "missing_entry"
	failureModePostgresUnreachable = "postgres_unreachable"
	// eventDataResidencyViolationAttempt mirrors
	// observability/audit.EventDataResidencyViolationAttempt. It is a
	// local literal so this package does not import the catalog (avoiding
	// a dependency cycle); TestPlatformAuditViolationEventTypeMatchesCatalog
	// pins the equality so the two cannot drift.
	eventDataResidencyViolationAttempt = "DataResidencyViolationAttempt"
	// defaultPlatformTenantID mirrors jwtaudit.PlatformTenantID; the
	// platform-tenant chain id the CMP-058 gate keys on when the wiring
	// passes an empty platform tenant id.
	defaultPlatformTenantID = "platform"
)

// PlatformRegionRouter resolves the region-scoped platform-Postgres pool
// for a CMP-058 residency-routed write. *storerouter.SingleShardRouter
// satisfies it via PlatformPostgresForRegion. spec: §11.7 line 431.
type PlatformRegionRouter interface {
	PlatformPostgresForRegion(ctx context.Context, region string) (*pgxpool.Pool, error)
}

// ResidencyLookup resolves a target tenant's dataResidencyRegion. A
// not-found tenant (or a tombstone whose region snapshot is unavailable)
// resolves to "" so the write falls back to the global platform-Postgres
// (rule 2). spec: §11.7 line 432.
type ResidencyLookup interface {
	TargetResidencyRegion(ctx context.Context, targetTenantID string) (string, error)
}

// ResidencyMetrics receives the §11.7 line 433 residency-violation
// counters on a fail-closed CMP-058 abort. *gatewaymetrics.Metrics
// satisfies it. spec: §11.7 line 433.
type ResidencyMetrics interface {
	IncDataResidencyViolation(operation string)
	IncPlatformAuditRegionUnresolvable(region, failureMode string)
}

// compile-time assertion: the v1 router satisfies the region seam.
var _ PlatformRegionRouter = (*storerouter.SingleShardRouter)(nil)

// platformResidencyRouter carries the §11.7 CMP-058 gate's collaborators.
type platformResidencyRouter struct {
	platformTenantID string
	regions          PlatformRegionRouter
	lookup           ResidencyLookup
	metrics          ResidencyMetrics
	// ping reports whether a resolved regional pool is reachable. The
	// default calls (*pgxpool.Pool).Ping; tests inject a fake to exercise
	// the postgres_unreachable branch without a live database.
	ping func(ctx context.Context, pool *pgxpool.Pool) error
}

// noopResidencyMetrics drops the counters when the wiring passes no
// metrics sink, so the gate never nil-dereferences.
type noopResidencyMetrics struct{}

func (noopResidencyMetrics) IncDataResidencyViolation(string)               {}
func (noopResidencyMetrics) IncPlatformAuditRegionUnresolvable(_, _ string) {}

// WithPlatformAuditResidency installs the §11.7 CMP-058 platform-tenant
// audit residency gate. platformTenantID is the platform chain id the
// gate keys on (empty defaults to "platform"). regions resolves a
// region's platform-Postgres pool, lookup resolves a target tenant's
// dataResidencyRegion, and metrics receives the fail-closed counters.
// The option is inert (the gate stays disabled) when regions or lookup
// is nil, so a misconfiguration cannot silently swallow audit writes.
// F-11.7.9.
func WithPlatformAuditResidency(platformTenantID string, regions PlatformRegionRouter, lookup ResidencyLookup, metrics ResidencyMetrics) Option {
	return func(s *Store) {
		if regions == nil || lookup == nil {
			return
		}
		if platformTenantID == "" {
			platformTenantID = defaultPlatformTenantID
		}
		if metrics == nil {
			metrics = noopResidencyMetrics{}
		}
		s.platformResidency = &platformResidencyRouter{
			platformTenantID: platformTenantID,
			regions:          regions,
			lookup:           lookup,
			metrics:          metrics,
			ping:             func(ctx context.Context, pool *pgxpool.Pool) error { return pool.Ping(ctx) },
		}
	}
}

// targetTenantID extracts the target_tenant_id field from a canonical
// audit payload. The OCSF translator routes this field to
// unmapped.lenny.target_tenant_id; the CMP-058 gate keys on its presence
// in the canonical payload. An absent, empty, or malformed payload
// returns "". spec: §11.7 lines 428, 430.
func targetTenantID(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p struct {
		TargetTenantID string `json:"target_tenant_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.TargetTenantID
}

// routeOutcome is the §11.7 CMP-058 routing decision for one
// platform-tenant audit write. Exactly one of its result fields is set:
// global (rule 2 — write to the global platform-Postgres), pool (rule 1 —
// write to this regional pool), or failureMode (rule 3 — fail closed).
type routeOutcome struct {
	// global is true for rule 2: the target tenant carries no
	// dataResidencyRegion, so the write goes to the global platform-Postgres.
	global bool
	// pool is the resolved regional platform-Postgres pool for rule 1.
	pool *pgxpool.Pool
	// region is the target tenant's requested dataResidencyRegion (empty
	// for rule 2).
	region string
	// failureMode is non-empty for rule 3 (missing_entry |
	// postgres_unreachable).
	failureMode string
}

// decide resolves the §11.7 lines 431-433 routing for a platform-tenant
// audit write referencing target. It performs no I/O against Postgres
// beyond the optional reachability ping, so it is exercised directly by
// the unit tests with fake collaborators. A lookup error is returned to
// the caller (the write halts; audit must be durable before any
// externally observable side effect).
func (pr *platformResidencyRouter) decide(ctx context.Context, target string) (routeOutcome, error) {
	region, err := pr.lookup.TargetResidencyRegion(ctx, target)
	if err != nil {
		return routeOutcome{}, fmt.Errorf("auditstore: resolve target tenant %q residency region: %w", target, err)
	}
	if region == "" {
		// Rule 2: no residency constraint -> global platform-Postgres.
		return routeOutcome{global: true}, nil
	}
	pool, perr := pr.regions.PlatformPostgresForRegion(ctx, region)
	if perr != nil || pool == nil {
		// Rule 3, missing_entry: ErrPlatformRegionUnresolvable and any other
		// resolver error are both unresolvable-region conditions.
		return routeOutcome{region: region, failureMode: failureModeMissingEntry}, nil
	}
	if pr.ping != nil {
		if pingErr := pr.ping(ctx, pool); pingErr != nil {
			// Rule 3, postgres_unreachable: the entry exists but the region's
			// platform-Postgres is unreachable.
			return routeOutcome{region: region, failureMode: failureModePostgresUnreachable}, nil
		}
	}
	// Rule 1: write to the resolved regional platform-Postgres.
	return routeOutcome{pool: pool, region: region}, nil
}

// appendPlatformTargeted routes a platform-tenant audit write that
// carries target as its non-platform target_tenant_id through the §11.7
// CMP-058 three-rule residency gate. spec: §11.7 lines 430-433.
func (s *Store) appendPlatformTargeted(ctx context.Context, eventType string, payload json.RawMessage, target string, at time.Time) (audit.Row, error) {
	pr := s.platformResidency
	outcome, err := pr.decide(ctx, target)
	if err != nil {
		return audit.Row{}, err
	}
	if outcome.failureMode != "" {
		return s.failClosedPlatformAudit(ctx, eventType, target, outcome.region, outcome.failureMode, at)
	}
	pool := outcome.pool
	if outcome.global {
		var perr error
		pool, perr = s.writeShard(ctx, pr.platformTenantID)
		if perr != nil {
			return audit.Row{}, perr
		}
	}
	return s.appendOnPool(ctx, pool, pr.platformTenantID, eventType, payload, at)
}

// recordViolation bumps the §11.7 line 433 residency counters: the shared
// data-residency series (labeled operation=platform_audit_write) and the
// dedicated platform-audit series (labeled region + failure_mode).
func (pr *platformResidencyRouter) recordViolation(region, failureMode string) {
	pr.metrics.IncDataResidencyViolation(operationPlatformAuditWrite)
	pr.metrics.IncPlatformAuditRegionUnresolvable(region, failureMode)
}

// failClosedPlatformAudit implements §11.7 line 433: bump the residency
// counters, record the DataResidencyViolationAttempt event (routed to the
// global platform-Postgres per rule 2 so the incident is not lost to the
// unreachable region), and return PLATFORM_AUDIT_REGION_UNRESOLVABLE. The
// violation record is best-effort: a failure to persist it must not mask
// the unresolvable-region error the originating operation needs to see.
func (s *Store) failClosedPlatformAudit(ctx context.Context, eventType, target, region, failureMode string, at time.Time) (audit.Row, error) {
	pr := s.platformResidency
	pr.recordViolation(region, failureMode)

	if vpayload, err := buildViolationPayload(pr.platformTenantID, target, region, eventType, failureMode); err == nil {
		if pool, perr := s.writeShard(ctx, pr.platformTenantID); perr == nil {
			// Write directly to the global pool via appendOnPool (not Append)
			// so the violation record — itself a platform-tenant event that
			// carries target_tenant_id — does not recurse into the gate.
			_, _ = s.appendOnPool(ctx, pool, pr.platformTenantID, eventDataResidencyViolationAttempt, vpayload, at)
		}
	}
	return audit.Row{}, &PlatformAuditRegionUnresolvableError{
		TargetTenantID: target,
		Region:         region,
		FailureMode:    failureMode,
		EventType:      eventType,
	}
}

// buildViolationPayload constructs the §11.7 line 433
// DataResidencyViolationAttempt canonical payload.
func buildViolationPayload(platformTenantID, target, region, eventType, failureMode string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"operation":        operationPlatformAuditWrite,
		"tenant_id":        platformTenantID,
		"target_tenant_id": target,
		"requested_region": region,
		"event_type":       eventType,
		"failure_mode":     failureMode,
	})
}

// PlatformAuditRegionUnresolvableError is the §11.7 line 433 fail-closed
// error a CMP-058 platform-tenant audit write returns when the target
// tenant's dataResidencyRegion cannot be resolved to a reachable regional
// platform-Postgres. It maps to PLATFORM_AUDIT_REGION_UNRESOLVABLE
// (HTTP 422, PERMANENT). spec: §11.7 line 433; §15.1 line 1044.
type PlatformAuditRegionUnresolvableError struct {
	TargetTenantID string
	Region         string
	FailureMode    string
	EventType      string
}

func (e *PlatformAuditRegionUnresolvableError) Error() string {
	return fmt.Sprintf(
		"auditstore: platform-tenant audit write for target tenant %q (event %q) failed closed: region %q unresolvable (%s)",
		e.TargetTenantID, e.EventType, e.Region, e.FailureMode)
}

// Code returns the §15.1 line 1044 error code for HTTP mapping.
func (e *PlatformAuditRegionUnresolvableError) Code() string {
	return "PLATFORM_AUDIT_REGION_UNRESOLVABLE"
}

// HTTPStatus returns the §11.7 line 433 / §15.1 line 1044 HTTP status.
func (e *PlatformAuditRegionUnresolvableError) HTTPStatus() int { return 422 }
