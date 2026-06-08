// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"strings"
)

// cloudPoolerSentinelFailMessage is reproduced verbatim from the §17.6
// line 488 preflight-check table ("Cloud-managed pooler sentinel
// defense"). The check appends the offending table list so the operator
// knows which tenant-scoped tables lack the trigger.
const cloudPoolerSentinelFailMessage = "Cloud-managed pooler detected but per-transaction tenant validation trigger 'lenny_tenant_guard' not found; required because cloud-managed proxies cannot enforce the __unset__ sentinel via connect_query — see Section 12.3"

// PoolerSentinelProber returns the tenant-scoped tables that lack an
// enabled lenny_tenant_guard trigger on the operator's Postgres. It is
// the seam the real pgx-backed prober (over the
// integrity.TenantGuardCoverageGaps catalog query) and test fakes
// satisfy. A nil prober skips the live probe; an empty slice with a nil
// error means every tenant-scoped table is protected.
//
// spec: §17.6 line 488; §12.3 line 56.
type PoolerSentinelProber interface {
	TenantGuardCoverageGaps(ctx context.Context) ([]string, error)
}

// PoolerSentinelProbeFunc adapts a function to PoolerSentinelProber.
type PoolerSentinelProbeFunc func(ctx context.Context) ([]string, error)

// TenantGuardCoverageGaps implements PoolerSentinelProber.
func (f PoolerSentinelProbeFunc) TenantGuardCoverageGaps(ctx context.Context) ([]string, error) {
	return f(ctx)
}

// CloudPoolerSentinelCheck is the §17.6 line 488 / §17.9.7 cloud-managed
// pooler sentinel defense. When postgres.connectionPooler is "external"
// the deployment fronts Postgres with a managed proxy (RDS Proxy, Cloud
// SQL Auth Proxy, Azure PgBouncer) that cannot run the connect_query
// __unset__ sentinel, so the per-transaction lenny_tenant_guard trigger
// is the load-bearing RLS isolation defense. This check connects to
// Postgres and verifies the trigger exists on every tenant-scoped table,
// failing the install fail-closed when any table is unprotected.
//
// This is the §17.9.7 install-time half of the layered defense; the
// gateway's startup VerifyCloudManagedPoolerDefense (LENNY_POOLER_MODE)
// is the runtime half that also catches trigger removal after install.
// When the pooler is not external the trigger is defense-in-depth and the
// check is a no-op. When no Postgres connection is wired (the DSN secret
// is absent at the pre-install hook, or the operator did not supply one)
// the check defers to the runtime defense rather than blocking the
// install on a connection it cannot make.
//
// spec: §17.6 line 488; §17.9.7 line 1541; §12.3 line 56.
type CloudPoolerSentinelCheck struct {
	// ConnectionPooler is the effective postgres.connectionPooler value
	// (pgbouncer | external). Only "external" arms the live probe.
	ConnectionPooler string
	// Prober reads the live tenant-guard coverage. Nil routes the check
	// through the runtime-defense advisory.
	Prober PoolerSentinelProber
}

// Decide evaluates the §17.6 line 488 cloud-managed pooler sentinel
// defense.
func (c CloudPoolerSentinelCheck) Decide(ctx context.Context) Decision {
	if !strings.EqualFold(strings.TrimSpace(c.ConnectionPooler), "external") {
		return Decision{Passed: true, Reason: "SKIPPED: postgres.connectionPooler is not external; " +
			"the in-cluster PgBouncer enforces the connect_query __unset__ sentinel (§12.3)"}
	}
	if c.Prober == nil {
		return Decision{Passed: true, Reason: "SKIPPED: no Postgres connection wired for the cloud-pooler sentinel probe; " +
			"the gateway LENNY_POOLER_MODE=external startup defense (§12.3 line 56) is the load-bearing check"}
	}
	gaps, err := c.Prober.TenantGuardCoverageGaps(ctx)
	if err != nil {
		return Decision{Reason: "POSTGRES_UNREACHABLE: lenny_tenant_guard coverage query failed: " + err.Error()}
	}
	if len(gaps) > 0 {
		return Decision{Reason: cloudPoolerSentinelFailMessage + " (unprotected tenant-scoped tables: " + strings.Join(gaps, ", ") + ")"}
	}
	return Decision{Passed: true, Reason: "lenny_tenant_guard trigger present on all tenant-scoped tables"}
}
