// SPDX-License-Identifier: MIT

// Package rules declares the §25.3 capacity-recommendation rules as
// typed Rule values, shared by the gateway and lenny-ops. §18 Phase 2.5
// ships the rule type and the PromQL condition validator; the full rule
// catalog is populated alongside the §25.3 Capacity Recommendations
// service. Capacity-recommendation rules follow the same pattern as the
// §16.5 alerting rules in pkg/alerting/rules.
package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

// Category is the §25.3 capacity-recommendation category.
type Category string

const (
	CategoryWarmPoolSizing       Category = "warm_pool_sizing"
	CategoryCredentialPoolSizing Category = "credential_pool_sizing"
	CategoryGatewayScaling       Category = "gateway_scaling"
	CategoryResourceLimits       Category = "resource_limits"
	CategoryRetentionTuning      Category = "retention_tuning"
	CategoryQuotaAdjustment      Category = "quota_adjustment"
)

// AllCategories returns the closed §25.3 category enum.
func AllCategories() []Category {
	return []Category{
		CategoryWarmPoolSizing, CategoryCredentialPoolSizing, CategoryGatewayScaling,
		CategoryResourceLimits, CategoryRetentionTuning, CategoryQuotaAdjustment,
	}
}

// IsValid reports whether c is a known recommendation category.
func (c Category) IsValid() bool {
	for _, v := range AllCategories() {
		if c == v {
			return true
		}
	}
	return false
}

// Rule is one §25.3 capacity-recommendation rule. The §25.3 Capacity
// Recommendations service evaluates a rule's Condition against windowed
// metric values and, when the condition holds, synthesizes an
// actionable recommendation in the rule's Category.
type Rule struct {
	// Name is the unique rule identifier. Required.
	Name string

	// Category is the §25.3 recommendation category. Required.
	Category Category

	// Condition is the PromQL expression that triggers the
	// recommendation when it evaluates true. Required; must parse via
	// the PromQL parser.
	Condition string

	// Window is the sliding-window size the rule aggregates over (§25.3
	// default: 24h for pool sizing, 7d for credential sizing). A
	// positive duration is required.
	Window time.Duration

	// Summary is a one-line human description of the recommendation.
	// Required.
	Summary string

	// Description elaborates on the recommendation logic and the
	// formula that derives the recommended value. Optional.
	Description string

	// SpecRef is the spec section that defines the rule. Optional.
	SpecRef string
}

// Validate reports the violations of a Rule's invariants, returning nil
// when the rule is well-formed. Callers should validate the catalog at
// process startup so a misconfigured rule fails loudly.
func (r Rule) Validate() error {
	v := []string{}
	if strings.TrimSpace(r.Name) == "" {
		v = append(v, "Name is required")
	}
	if !r.Category.IsValid() {
		v = append(v, fmt.Sprintf("Category %q is not a recognised recommendation category", r.Category))
	}
	if strings.TrimSpace(r.Condition) == "" {
		v = append(v, "Condition is required")
	} else if _, err := parser.ParseExpr(r.Condition); err != nil {
		v = append(v, fmt.Sprintf("Condition does not parse as PromQL: %v", err))
	}
	if r.Window <= 0 {
		v = append(v, "Window must be a positive duration")
	}
	if strings.TrimSpace(r.Summary) == "" {
		v = append(v, "Summary is required")
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
	return fmt.Sprintf("recommendation rule %q: %s", e.Rule, strings.Join(e.Violations, "; "))
}

// Catalog returns the recommendation rules shipped so far. §18 Phase
// 2.5 ships the rule substrate with a representative sample drawn from
// the §25.3 category table; the full catalog is populated alongside the
// Capacity Recommendations service. The slice is fresh on every call so
// callers may sort or filter it freely.
func Catalog() []Rule {
	return []Rule{
		{
			Name:        "WarmPoolUndersized",
			Category:    CategoryWarmPoolSizing,
			Condition:   `increase(lenny_warmpool_exhausted_total[24h]) >= 3`,
			Window:      24 * time.Hour,
			Summary:     "Warm pool exhausted repeatedly — increase minWarm",
			Description: "The pool reached zero idle pods three or more times in 24h. Recommend minWarm = ceil(peak_claim_rate * (startup_p99 + failover_seconds) * 1.3).",
			SpecRef:     "§25.3",
		},
		{
			Name:        "CredentialPoolUndersized",
			Category:    CategoryCredentialPoolSizing,
			Condition:   `lenny_credential_pool_utilization > 0.70`,
			Window:      7 * 24 * time.Hour,
			Summary:     "Credential pool utilisation sustained above 70 percent",
			Description: "Pool utilisation exceeded 70 percent over the 7-day window with rate-limit events. Recommend adding credentials to bring utilisation below 60 percent.",
			SpecRef:     "§25.3",
		},
		{
			Name:        "GatewayScalingPressure",
			Category:    CategoryGatewayScaling,
			Condition:   `avg(lenny_gateway_cpu_utilization_ratio) > 0.70`,
			Window:      15 * time.Minute,
			Summary:     "Gateway CPU sustained above 70 percent — raise the HPA ceiling",
			Description: "Gateway CPU exceeded 70 percent for over 15 minutes. Recommend increasing the HPA max-replica ceiling.",
			SpecRef:     "§25.3",
		},
		{
			Name:        "ResourceLimitsMemoryPressure",
			Category:    CategoryResourceLimits,
			Condition:   `increase(lenny_pod_oom_killed_total[24h]) > 0`,
			Window:      24 * time.Hour,
			Summary:     "Pods OOM-killed in the last 24h — raise the memory limit",
			Description: "One or more pods were OOM-killed in the 24h window. Recommend increasing the memory limit for the affected pool.",
			SpecRef:     "§25.3",
		},
		{
			Name:        "RetentionTuningStoragePressure",
			Category:    CategoryRetentionTuning,
			Condition:   `lenny_storage_utilization_ratio > 0.80`,
			Window:      24 * time.Hour,
			Summary:     "Artifact storage above 80 percent — shorten the retention TTL",
			Description: "Artifact storage utilisation exceeds 80 percent. Recommend reducing the artifact retention TTL to relieve storage pressure.",
			SpecRef:     "§25.3",
		},
		{
			Name:        "QuotaAdjustmentRejections",
			Category:    CategoryQuotaAdjustment,
			Condition:   `lenny_quota_rejection_ratio > 0.05`,
			Window:      24 * time.Hour,
			Summary:     "Session quota rejection rate above 5 percent — raise the tenant quota",
			Description: "More than 5 percent of session-creation requests were rejected by the tenant quota over the 24h window. Recommend increasing the tenant session quota.",
			SpecRef:     "§25.3",
		},
		{
			// spec: §17.8.2 line 1103 first-week monitoring workflow:
			// "if idle pod-minutes per hour exceeds `minWarm × 30` ...
			// `minWarm` is oversized; reduce by 25%."
			Name:      "WarmPoolOversized",
			Category:  CategoryWarmPoolSizing,
			Condition: `increase(lenny_warmpool_idle_pod_minutes[1h]) > 30 * lenny_pool_min_warm`,
			Window:    1 * time.Hour,
			Summary:   "Warm pool idle-pod-minutes per hour exceed minWarm × 30 — reduce minWarm by 25%",
			Description: "First-week tuning (§17.8.2): a pool whose hourly idle pod-minutes " +
				"exceed `minWarm × 30` is oversized. Recommend reducing `minWarm` by 25% and " +
				"re-monitoring for one hour before further adjustment.",
			SpecRef: "§17.8.2 line 1103",
		},
		{
			// spec: §17.8.2 line 1104 first-week monitoring workflow:
			// "P99 claim latency exceeds 2s, the pool is not keeping up
			// with demand; increase `minWarm` or investigate pod startup".
			Name:     "WarmPoolClaimLatencyHigh",
			Category: CategoryWarmPoolSizing,
			Condition: `histogram_quantile(0.99, sum by (le, pool) (rate(lenny_pod_claim_queue_wait_seconds_bucket[5m]))) > 2`,
			Window:   5 * time.Minute,
			Summary:  "Warm pool P99 claim latency exceeds 2s — raise minWarm or investigate pod startup",
			Description: "First-week tuning (§17.8.2): P99 claim queue wait above 2s indicates " +
				"the warm pool is not keeping up with demand. Recommend raising `minWarm` (by ~25%) " +
				"or, when P99 pod-startup time is also rising, investigating image pull and runtime " +
				"warmup before increasing the pool size.",
			SpecRef: "§17.8.2 line 1104",
		},
	}
}
