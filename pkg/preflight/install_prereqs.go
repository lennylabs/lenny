// SPDX-License-Identifier: MIT

// This file implements the §17.6 "Checks performed" install-prerequisite
// checks that are pure functions over chart values or a backend probe
// seam (no cluster read): the Kubernetes server-version gate, the SIEM
// endpoint advisory, the StorageRouter region-coverage and legal-hold
// per-region escrow config audits, and the PgBouncer / billing-trigger
// live-database probes. The cluster-reader checks (ResourceQuota,
// LimitRange, ClusterIssuer, monitoring namespace) live alongside the
// run.go orchestrator.
//
// spec: §17.6 lines 478-525. F-17.6.1.
package preflight

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// MinKubernetesMinor is the §17.6 line 503 minimum supported Kubernetes
// minor version (1.27). Older clusters lack API features Lenny relies
// on, so the install aborts fail-closed.
const MinKubernetesMinor = 27

// KubernetesVersionCheck is the §17.6 line 503 server-version gate. It
// fails the install when the API server reports a version below 1.27.
// An empty or unparseable version is non-blocking (the binary could not
// read the version; the gate degrades to advisory rather than blocking
// an install on a discovery quirk).
//
// spec: §17.6 line 503.
type KubernetesVersionCheck struct {
	// Version is the API server GitVersion (for example "v1.29.4" or a
	// distro-suffixed "v1.27.6+vmware.1"). Empty skips the check.
	Version string
}

// Decide evaluates the §17.6 line 503 minimum-version gate.
func (c KubernetesVersionCheck) Decide() Decision {
	raw := strings.TrimSpace(c.Version)
	if raw == "" {
		return Decision{Passed: true, Reason: "SKIPPED: API server version not available"}
	}
	major, minor, ok := parseKubernetesMinor(raw)
	if !ok {
		return Decision{Passed: true, Reason: fmt.Sprintf("WARNING: could not parse Kubernetes version %q; the §17.6 minimum-version gate did not run", raw)}
	}
	if major < 1 || (major == 1 && minor < MinKubernetesMinor) {
		return Decision{Passed: false, Reason: fmt.Sprintf("Kubernetes version %s unsupported; minimum 1.%d required", raw, MinKubernetesMinor)}
	}
	return Decision{Passed: true, Reason: fmt.Sprintf("Kubernetes version %s satisfies the 1.%d minimum", raw, MinKubernetesMinor)}
}

// parseKubernetesMinor extracts the major and minor version numbers from
// an API server GitVersion string, tolerating a leading "v" and any
// distro suffix on the minor segment ("27+", "27-eks-1234").
func parseKubernetesMinor(v string) (major, minor int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(leadingDigits(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	minorDigits := leadingDigits(parts[1])
	if minorDigits == "" {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(minorDigits)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// leadingDigits returns the leading run of ASCII digits in s (empty when
// s does not start with a digit). It strips a distro suffix such as the
// "+" in a GKE "27+" minor segment.
func leadingDigits(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

// SIEMEndpointCheck is the §17.6 line 517 SIEM-endpoint advisory. When
// the deployment runs in production with no audit.siem.endpoint
// configured it emits a non-blocking warning: audit logs are stored in
// Postgres only, which does not meet compliance-grade audit-integrity
// requirements. The check always passes (it never blocks an install).
//
// spec: §17.6 line 517.
type SIEMEndpointCheck struct {
	// Environment is the chart `environment` value (dev | staging |
	// prod | production).
	Environment string
	// SIEMEndpoint is the audit.siem.endpoint value. Empty triggers the
	// warning in a production environment.
	SIEMEndpoint string
}

// Decide evaluates the §17.6 line 517 SIEM advisory.
func (c SIEMEndpointCheck) Decide() Decision {
	if !isProductionEnvironment(c.Environment) || strings.TrimSpace(c.SIEMEndpoint) != "" {
		return Decision{Passed: true}
	}
	return Decision{
		Passed: true,
		Reason: "WARNING: audit.siem.endpoint is not configured. Audit logs will be stored in Postgres only. A database superuser can bypass INSERT-only grants. This deployment does not meet compliance-grade audit integrity requirements (SOC2 CC7.2, FedRAMP AU-9, HIPAA §164.312(b)). Configure audit.siem.endpoint before using for regulated workloads (Section 11.7).",
	}
}

// isProductionEnvironment reports whether the chart environment value
// names a production posture. §17.6 line 517 keys on LENNY_ENV=production;
// the chart `environment` value uses `prod`, so both spellings match.
func isProductionEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	}
	return false
}

// StorageRouterRegion names one data-residency region the chart declares
// and the backends configured for it. A region with no Postgres or
// object-storage backend cannot satisfy the §12.6 StorageRouter routing
// contract for tenants pinned to it.
type StorageRouterRegion struct {
	// Name is the region identifier (for example "us-east-1").
	Name string
	// HasPostgres reports whether the region declares a Postgres backend.
	HasPostgres bool
	// HasObjectStorage reports whether the region declares an
	// object-storage backend.
	HasObjectStorage bool
}

// StorageRouterRegionCoverageCheck is the §17.6 / §12.6 region-coverage
// audit: every declared StorageRouter region must carry both a Postgres
// and an object-storage backend so a tenant pinned to that region has a
// complete data plane. A region missing either backend fails the install
// fail-closed. An empty region set skips the check (single-region
// deployments do not declare regions).
//
// spec: §17.6 line 504 (StorageRouter region coverage); §12.6.
type StorageRouterRegionCoverageCheck struct {
	Regions []StorageRouterRegion
}

// Decide evaluates the §17.6 line 504 region-coverage audit.
func (c StorageRouterRegionCoverageCheck) Decide() Decision {
	if len(c.Regions) == 0 {
		return Decision{Passed: true, Reason: "SKIPPED: no StorageRouter regions declared (single-region deployment)"}
	}
	var problems []string
	for _, r := range c.Regions {
		var missing []string
		if !r.HasPostgres {
			missing = append(missing, "Postgres")
		}
		if !r.HasObjectStorage {
			missing = append(missing, "object storage")
		}
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("StorageRouter region %q has no %s backend configured", r.Name, strings.Join(missing, " or ")))
		}
	}
	if len(problems) > 0 {
		return Decision{Passed: false, Reason: strings.Join(problems, "; ")}
	}
	return Decision{Passed: true, Reason: fmt.Sprintf("all %d StorageRouter regions have Postgres and object-storage backends", len(c.Regions))}
}

// LegalHoldEscrowCheck is the §17.6 line 505 legal-hold escrow audit.
// When legal hold is enabled, every declared StorageRouter region must
// have a region-scoped escrow KEK so a force-delete under hold can seal
// the escrow copy in-region (§12.8 Phase 3.5 residency). A region without
// an escrow key fails the install fail-closed. The check skips when legal
// hold is disabled or no regions are declared.
//
// spec: §17.6 line 505; §12.8 Phase 3.5.
type LegalHoldEscrowCheck struct {
	// Enabled is the legalHold.enabled chart value.
	Enabled bool
	// EscrowRegions is the set of regions that declare an escrow KEK.
	EscrowRegions map[string]bool
	// Regions is the full set of declared StorageRouter regions.
	Regions []string
}

// Decide evaluates the §17.6 line 505 escrow per-region audit.
func (c LegalHoldEscrowCheck) Decide() Decision {
	if !c.Enabled {
		return Decision{Passed: true, Reason: "SKIPPED: legal hold is not enabled"}
	}
	if len(c.Regions) == 0 {
		return Decision{Passed: true, Reason: "SKIPPED: no StorageRouter regions declared"}
	}
	var missing []string
	for _, region := range c.Regions {
		if !c.EscrowRegions[region] {
			missing = append(missing, region)
		}
	}
	if len(missing) > 0 {
		return Decision{Passed: false, Reason: fmt.Sprintf("CONFIG_INVALID: legal hold is enabled but regions %s have no escrow KEK configured (§12.8 Phase 3.5 region-scoped escrow)", strings.Join(missing, ", "))}
	}
	return Decision{Passed: true, Reason: "every StorageRouter region has a legal-hold escrow KEK"}
}

// PgBouncerConfigProber reads the live PgBouncer admin console
// (`SHOW CONFIG`) to return the effective pool_mode and whether the §4.2
// tenant-isolation connect_query sentinel is set on the configured
// database. It is the seam the real pgx-over-PgBouncer-admin dialer and
// test fakes satisfy. A configured pooler with no wired prober skips the
// check (the v1 preflight Job is not granted the PgBouncer admin
// connection); the chart-side connect_query ConfigMap is validated at
// render time.
//
// spec: §17.6 lines 487-488 (PgBouncer pool_mode == transaction;
// connect_query sets the tenant sentinel).
type PgBouncerConfigProber interface {
	ProbePgBouncer(ctx context.Context) (poolMode string, hasTenantSentinel bool, err error)
}

// PgBouncerConfigCheck is the §17.6 lines 487-488 PgBouncer audit. When a
// prober is wired it verifies pool_mode == transaction and that the
// per-connection tenant sentinel connect_query is present. A nil prober
// skips the check.
//
// spec: §17.6 lines 487-488.
type PgBouncerConfigCheck struct {
	// Prober reads the live PgBouncer config. Nil skips the check.
	Prober PgBouncerConfigProber
}

// Decide evaluates the §17.6 lines 487-488 PgBouncer audit.
func (c PgBouncerConfigCheck) Decide(ctx context.Context) Decision {
	if c.Prober == nil {
		return Decision{Passed: true, Reason: "SKIPPED: no PgBouncer admin connection wired (chart-side connect_query ConfigMap validated at render time)"}
	}
	poolMode, hasSentinel, err := c.Prober.ProbePgBouncer(ctx)
	if err != nil {
		return Decision{Passed: false, Reason: "PgBouncer admin SHOW CONFIG failed: " + err.Error()}
	}
	var problems []string
	if !strings.EqualFold(strings.TrimSpace(poolMode), "transaction") {
		problems = append(problems, fmt.Sprintf("PgBouncer pool_mode is %q (must be transaction for §4.2 tenant isolation)", poolMode))
	}
	if !hasSentinel {
		problems = append(problems, "PgBouncer connect_query missing tenant sentinel (§4.2)")
	}
	if len(problems) > 0 {
		return Decision{Passed: false, Reason: strings.Join(problems, "; ")}
	}
	return Decision{Passed: true, Reason: "PgBouncer pool_mode=transaction with the §4.2 tenant sentinel connect_query"}
}

// BillingTriggerProber reads whether the §11.2 / §12.3 billing and audit
// integrity triggers are enabled (and immutable) on the billing/audit
// database. It is the seam the real database dialer and test fakes
// satisfy. A nil prober skips the check.
//
// spec: §17.6 line 489 (billing/audit trigger enabled).
type BillingTriggerProber interface {
	ProbeBillingTriggers(ctx context.Context) (enabled bool, err error)
}

// BillingTriggerCheck is the §17.6 line 489 integrity-trigger audit. When
// a prober is wired it verifies the billing/audit integrity triggers are
// enabled. A nil prober skips the check (the v1 preflight Job is not
// granted the billing-database superuser connection the SHOW requires).
//
// spec: §17.6 line 489.
type BillingTriggerCheck struct {
	// Prober reads the live trigger state. Nil skips the check.
	Prober BillingTriggerProber
}

// Decide evaluates the §17.6 line 489 integrity-trigger audit.
func (c BillingTriggerCheck) Decide(ctx context.Context) Decision {
	if c.Prober == nil {
		return Decision{Passed: true, Reason: "SKIPPED: no billing-database connection wired for the integrity-trigger audit"}
	}
	enabled, err := c.Prober.ProbeBillingTriggers(ctx)
	if err != nil {
		return Decision{Passed: false, Reason: "billing/audit integrity-trigger query failed: " + err.Error()}
	}
	if !enabled {
		return Decision{Passed: false, Reason: "Integrity trigger on the billing/audit database is disabled; re-apply the §11.2 / §12.3 migration"}
	}
	return Decision{Passed: true, Reason: "billing/audit integrity triggers are enabled"}
}
