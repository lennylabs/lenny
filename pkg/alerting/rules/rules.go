// SPDX-License-Identifier: MIT

// Package rules declares Lenny's PrometheusRule alerts as typed Rule
// values. Spec §16.5 enumerates the catalog of alerts; each entry here
// corresponds to one row of those tables.
//
// Phase 2.5 ships the rule type, the PromQL validator, the
// PrometheusRule YAML renderer, and a representative sample of rules
// drawn from the spec's critical and warning tables. The full catalog
// lands incrementally as the feature surfaces it monitors ship — the
// alert for WarmPoolExhausted only carries weight once the warm-pool
// controller emits lenny_warmpool_idle_pods, and so on. Tests assert
// the type contract; the Catalog function returns the rules that exist
// today.
package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

// Severity is the alert severity enum from §16.5.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// IsValid reports whether s is one of the allowed severities.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityCritical, SeverityWarning:
		return true
	}
	return false
}

// Rule is one PrometheusRule entry. Field names align with the
// PrometheusRule CRD's shape so the Helm rendering layer can marshal a
// slice of Rule values directly into the chart's alert manifests.
type Rule struct {
	// Name is the alert name from §16.5 (e.g., WarmPoolExhausted).
	// Required.
	Name string

	// Expr is the PromQL expression that triggers the alert. Required;
	// must parse via prometheus/prometheus/promql/parser.
	Expr string

	// For is the sustain duration before the alert fires. Spec §16.5
	// expresses this as "for > 60s" in the condition column. Zero means
	// the alert fires immediately on the first true evaluation.
	For time.Duration

	// Severity is one of "critical" or "warning". Required.
	Severity Severity

	// Summary is a one-line human description. Required.
	Summary string

	// Description elaborates on cause, impact, and operator response.
	// Optional but strongly encouraged for paging alerts.
	Description string

	// RunbookURL points operators at the §17.7 runbook for this alert.
	// Optional for warnings; required for critical alerts per the
	// §17.7 runbook obligation.
	RunbookURL string

	// SpecRef is the spec section that defines the alert
	// (e.g., "§16.5", "§4.6.1"). Optional but useful for traceability.
	SpecRef string
}

// Validate reports the violations of a Rule's invariants. Returns nil
// when the rule is well-formed. Callers should validate at process
// startup so misconfigured catalogs fail loudly.
func (r Rule) Validate() error {
	v := []string{}
	if strings.TrimSpace(r.Name) == "" {
		v = append(v, "Name is required")
	}
	if strings.TrimSpace(r.Expr) == "" {
		v = append(v, "Expr is required")
	} else if _, err := parser.ParseExpr(r.Expr); err != nil {
		v = append(v, fmt.Sprintf("Expr does not parse as PromQL: %v", err))
	}
	if !r.Severity.IsValid() {
		v = append(v, fmt.Sprintf("Severity %q is not one of critical, warning", r.Severity))
	}
	if strings.TrimSpace(r.Summary) == "" {
		v = append(v, "Summary is required")
	}
	if r.For < 0 {
		v = append(v, "For must be non-negative")
	}
	if r.Severity == SeverityCritical && strings.TrimSpace(r.RunbookURL) == "" {
		v = append(v, "Critical severity requires a RunbookURL")
	}
	if len(v) == 0 {
		return nil
	}
	return &ValidationError{Rule: r.Name, Violations: v}
}

// ValidationError aggregates Rule.Validate failures. Use errors.As to
// retrieve the typed value.
type ValidationError struct {
	Rule       string
	Violations []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("rule %q: %s", e.Rule, strings.Join(e.Violations, "; "))
}

// Catalog returns the Rule values shipped in Phase 2.5. The catalog
// grows as features land; tests use this slice to enumerate every rule
// the platform claims to define. The slice is fresh on every call so
// callers may sort or filter it freely.
func Catalog() []Rule {
	rs := []Rule{
		{
			Name:        "WarmPoolExhausted",
			Expr:        `min by (pool) (lenny_warmpool_idle_pods) == 0`,
			For:         60 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Warm pool has no available pods",
			Description: "Available warm pods = 0 for at least one pool. New session creation blocks on pod claim until the controller replenishes the pool.",
			RunbookURL:  "https://docs.lenny.dev/runbooks/warm-pool-exhausted",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PostgresReplicationLagHigh",
			Expr:        `lenny_postgres_replication_lag_seconds > 1`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Postgres sync replica lag exceeds 1 second",
			Description: "Sync-replica replication lag exceeds 1 second sustained for 30 seconds. Session state writes risk read-after-write inconsistency.",
			RunbookURL:  "https://docs.lenny.dev/runbooks/postgres-replication-lag",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CredentialPoolLow",
			Expr:        `lenny_credential_pool_utilization > 0.80`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Credential pool utilisation above 80 percent",
			Description: "Pool utilisation exceeds 80 percent. Available credentials are below 20 percent of pool size; credential rotation pressure is elevated.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "WarmPoolLow",
			Expr:        `lenny_warmpool_idle_pods / on(pool) group_left lenny_warmpool_min_warm < 0.25`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Warm pool below 25 percent of minWarm",
			Description: "Available warm pods are below 25 percent of the pool's minWarm setting. Pool replenishment is lagging behind session arrival rate.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CertExpiryImminent",
			Expr:        `min(lenny_cert_expiry_seconds) < 3600`,
			Severity:    SeverityWarning,
			Summary:     "mTLS certificate expiry under 1 hour",
			Description: "An mTLS certificate is expiring within the hour. Cert-manager should auto-renew; firing indicates a renewal failure.",
			SpecRef:     "§16.5",
		},
	}
	return rs
}
