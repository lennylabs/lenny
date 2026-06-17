// SPDX-License-Identifier: MIT

// Package rules declares Lenny's PrometheusRule alerts as typed Rule
// values. Spec §16.5 enumerates the catalog of alerts; each entry here
// corresponds to one row of those tables.
//
// The package ships the rule type, the PromQL validator, the
// PrometheusRule YAML renderer, and the full §16.5 alert catalog. The
// Catalog function returns every alert §16.5 defines: the critical
// alerts, the warning alerts, and the multi-window SLO burn-rate
// alerts. Each multi-window burn-rate SLO is rendered as two Rule
// values, a fast-window critical rule and a slow-window warning rule,
// because §16.5 requires both windows to be present simultaneously
// for a page.
//
// The catalog is the single source consumed by two surfaces: the
// gateway compiles it into an in-process alert tracker (§25.13), and
// the Helm chart renders it into a PrometheusRule CRD via
// RenderPrometheusRule. A cross-check test asserts the rendered chart
// manifest matches this catalog exactly.
package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// Severity is the alert severity enum from §16.5. The §16.5 tables use
// "critical" and "warning"; the operator-driven informational alerts
// (e.g., PlatformUpgradeAvailable) carry "info".
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// IsValid reports whether s is one of the allowed severities.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityCritical, SeverityWarning, SeverityInfo:
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

	// RunbookShortName is the §25.7 / §25.17 runbook slug carried in the
	// alert_fired operational-event payload's `runbook` field (e.g.
	// "warm-pool-exhaustion"). It matches the on-disk filename under
	// docs/runbooks/<slug>.md and the runbook front matter `name`, which
	// is what a watchdog routes on (§25.17 line 5177 reads `runbook`
	// directly off the event). When empty, RunbookSlug derives the slug
	// from the last path segment of RunbookURL.
	// spec: §25.7 line 3236; §25.17 line 5172.
	RunbookShortName string

	// SuggestedAction is the §25.17 proposed remediation carried on the
	// alert_fired payload so a watchdog can route to a concrete action
	// without a separate diagnostic call (§25.17 line 5216 "the agent
	// decides to follow the suggestedAction"). nil omits the field. The
	// runtime-accurate body (e.g. the exact minWarm) is produced by the
	// §25.6 pool diagnostic; the rule-level value is the routing template.
	// spec: §25.17 line 5172.
	SuggestedAction *conventions.SuggestedAction

	// SLO names the §16.5 service-level objective this alert defends,
	// for the burn-rate alerts that pair one-to-one with an SLO row in
	// the §16.5 SLO target table. Empty for threshold alerts that do
	// not derive from an SLO. When set, RenderPrometheusRule emits it
	// as the rule's "slo" annotation.
	SLO string

	// SpecRef is the spec section that defines the alert
	// (e.g., "§16.5", "§4.6.1"). Optional but useful for traceability.
	SpecRef string

	// Labels are extra static label key/values stamped on every firing
	// of this rule, merged with the mandatory "severity" label at render
	// time. Used by the §16.5 / §17.2 AdmissionPlaneFeatureFlagDowngrade
	// per-(flag, webhook)-pair decomposition, where four rules share one
	// alert Name but each carries distinct flag_name / expected_webhook_name
	// labels so a firing identifies the missing admission-plane surface.
	// Empty for the common single-rule alert. A label here must not be
	// "severity"; the renderer rejects a catalogue that tries to override
	// it.
	Labels map[string]string

	// Annotations are extra static annotation key/values stamped on
	// every firing of this rule, merged with the typed annotations the
	// renderer derives from Summary, Description, RunbookURL, and SLO.
	// They carry arbitrary operator metadata the §25.13 spec sketch's
	// open-ended `Annotations` map anchors — for example Alertmanager
	// routing keys (`dashboard`, `team`, `priority`) that downstream
	// receivers consume. A key here must not collide with a renderer-
	// owned annotation (summary, description, runbook_url, slo); Validate
	// rejects a catalogue that tries to override one so each annotation
	// has a single source. Empty for the common alert.
	// spec: §25.13 line 4684 (the Annotations field of the Go Rule shape).
	Annotations map[string]string
}

// reservedAnnotationKeys are the annotation names RenderPrometheusRule
// derives from a Rule's typed fields. An operator-supplied Annotations
// map must not set these; the typed field is the single source.
// spec: §25.13 line 4684.
var reservedAnnotationKeys = map[string]struct{}{
	"summary":     {},
	"description": {},
	"runbook_url": {},
	"slo":         {},
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
		v = append(v, fmt.Sprintf("Severity %q is not one of critical, warning, info", r.Severity))
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
	if _, ok := r.Labels["severity"]; ok {
		v = append(v, "Labels must not override the reserved \"severity\" label")
	}
	for k := range r.Annotations {
		if _, reserved := reservedAnnotationKeys[k]; reserved {
			v = append(v, fmt.Sprintf("Annotations must not override the renderer-owned %q annotation", k))
		}
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

// runbook builds a §17.7 runbook URL for an alert page slug. The URL
// convention is `https://docs.lenny.dev/runbooks/<slug>` where <slug>
// is the on-disk filename under `docs/runbooks/<slug>.md` minus the
// `.md` extension. The convention is documented for operators in
// `docs/runbooks/index.md` (the runbook_url annotation note); spec
// §17.7 version-controls the source files but does not define the
// rendered-site host pattern.
func runbook(slug string) string {
	return "https://docs.lenny.dev/runbooks/" + slug
}

// RunbookSlug returns the §25.7 / §25.17 short runbook slug for the
// alert_fired payload's `runbook` field. It prefers the explicit
// RunbookShortName; absent that, it derives the slug from the last path
// segment of RunbookURL (the docs/runbooks/<slug>.md convention), so a
// rule that sets only RunbookURL still emits the slug rather than the
// full URL. Returns "" when neither is set.
//
// spec: §25.7 line 3236 — "include a runbook field ... set to the
// alert's runbook annotation" (the short slug, per §25.17 line 5172).
func (r Rule) RunbookSlug() string {
	if r.RunbookShortName != "" {
		return r.RunbookShortName
	}
	if r.RunbookURL == "" {
		return ""
	}
	if i := strings.LastIndex(r.RunbookURL, "/"); i >= 0 && i < len(r.RunbookURL)-1 {
		return r.RunbookURL[i+1:]
	}
	return r.RunbookURL
}

// admissionPlaneDowngradeRule builds one (flag, webhook) pair of the
// §16.5 / §17.2 AdmissionPlaneFeatureFlagDowngrade alert. The expression
// fires when the phase-stamp ConfigMap records the flag as enabled
// (surfaced as the kube_configmap_labels label labelKey) but the gated
// ValidatingWebhookConfiguration is absent from the cluster, sustained
// for more than 2 minutes. flagName and webhook are stamped as static
// rule labels so each firing identifies the specific missing surface.
// spec: §16.5 line 487; §17.2 line 80. F-17.2.6.
func admissionPlaneDowngradeRule(flagName, webhook, labelKey string) Rule {
	return Rule{
		Name: "AdmissionPlaneFeatureFlagDowngrade",
		Expr: fmt.Sprintf(
			`(kube_configmap_labels{configmap="lenny-deployment-phase-stamp", %s="true"}) unless on() (kube_validatingwebhookconfiguration_info{name=%q})`,
			labelKey, webhook,
		),
		For:         2 * time.Minute,
		Severity:    SeverityWarning,
		Summary:     "Admission-plane feature-flag downgrade drift",
		Description: "The lenny-deployment-phase-stamp ConfigMap records a feature flag enabled: true but a ValidatingWebhookConfiguration the flag gates is absent from the cluster. Typically an operator helm-upgrade mistake or an out-of-band kubectl delete.",
		SpecRef:     "§16.5",
		Labels: map[string]string{
			"flag_name":             flagName,
			"expected_webhook_name": webhook,
		},
	}
}

// Catalog returns the full §16.5 alert catalog: every critical alert,
// every warning alert, and the multi-window SLO burn-rate alerts (each
// rendered as a fast-window critical rule and a slow-window warning
// rule). Tests cross-check this slice against the rendered Helm
// PrometheusRule manifest so the two surfaces cannot drift. The slice
// is fresh on every call so callers may sort or filter it freely.
func Catalog() []Rule {
	rs := []Rule{}
	rs = append(rs, criticalAlerts()...)
	rs = append(rs, warningAlerts()...)
	rs = append(rs, burnRateAlerts()...)
	return rs
}

// criticalAlerts is the §16.5 "Critical alerts (page)" table.
func criticalAlerts() []Rule {
	return []Rule{
		{
			Name: "WarmPoolExhausted",
			// §4.6.1 cold-start fill: suppress while the pool is inside its
			// initial-fill grace period (lenny_warmpool_fill_grace_active == 1),
			// so a fresh or re-activated pool does not page during expected
			// fill time.
			Expr:        `(min by (pool) (lenny_warmpool_idle_pods) == 0) unless on (pool) (lenny_warmpool_fill_grace_active == 1)`,
			For:         60 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Warm pool has no available pods",
			Description: "Available warm pods = 0 for any pool for more than 60s. New session creation blocks on pod claim until the controller replenishes the pool.",
			// spec: §25.17 line 5172 — runbook slug is "warm-pool-exhaustion",
			// matching docs/runbooks/warm-pool-exhaustion.md.
			RunbookURL:       runbook("warm-pool-exhaustion"),
			RunbookShortName: "warm-pool-exhaustion",
			// §25.17 line 5172: the alert_fired payload carries a
			// suggestedAction so a watchdog routes straight to the scale
			// call. The concrete minWarm comes from the §25.6 pool
			// diagnostic; this rule-level value is the action template.
			SuggestedAction: &conventions.SuggestedAction{
				Action:    "SCALE_WARM_POOL",
				Endpoint:  "PUT /v1/admin/pools/{name}/warm-count",
				Reasoning: "Warm pool is exhausted; raise the warm-pod floor so new sessions stop blocking on pod claim.",
				Runbook:   "warm-pool-exhaustion",
			},
			SpecRef: "§16.5",
		},
		{
			Name:        "PostgresReplicationLagHigh",
			Expr:        `lenny_postgres_replication_lag_seconds > 1`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Postgres sync replica lag exceeds 1 second",
			Description: "Sync-replica replication lag exceeds 1 second sustained for 30 seconds. Session state writes risk read-after-write inconsistency.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/postgres-failover.md.
			RunbookURL: runbook("postgres-failover"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "GatewayNoHealthyReplicas",
			Expr:        `lenny_gateway_replica_count < scalar(lenny_gateway_min_replicas)`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Healthy gateway replicas below tier minimum",
			Description: "Healthy gateway replicas have fallen below the tier minimum (§17.8) for more than 30s. Request capacity is degraded and session creation may be rejected.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/gateway-replica-failure.md.
			RunbookURL: runbook("gateway-replica-failure"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "SessionStoreUnavailable",
			Expr:        `lenny_postgres_primary_up == 0`,
			For:         15 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Postgres primary unreachable",
			Description: "The Postgres primary has been unreachable for more than 15s. Session state writes fail and new session creation is rejected.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/postgres-failover.md.
			RunbookURL: runbook("postgres-failover"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "RedisUnavailable",
			Expr:        `rate(lenny_quota_redis_fallback_total[2m]) > 0`,
			For:         1 * time.Minute,
			Severity:    SeverityCritical,
			Summary:     "Cluster-wide Quota/Rate-Limiting Redis unavailable",
			Description: "Any non-zero rate means at least one gateway replica has transitioned into quota fail-open mode because the Quota/Rate Limiting Redis instance is unreachable. Sustained Redis unavailability triggers the cumulative fail-open budget after which replicas transition to fail-closed and new session creation is rejected.",
			RunbookURL:  runbook("redis-failure"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "CheckpointStorageUnavailable",
			Expr:        `rate(lenny_checkpoint_storage_failure_total[5m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "Checkpoint upload to MinIO failed during eviction",
			Description: "Checkpoint upload to MinIO failed after all retries during eviction; the Postgres minimal-state fallback was attempted.",
			RunbookURL:  runbook("minio-failure"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "MinIOUnavailable",
			Expr:        `rate(lenny_artifact_upload_error_total{error_type="minio_unreachable"}[2m]) > 0`,
			For:         1 * time.Minute,
			Severity:    SeverityCritical,
			Summary:     "Cluster-wide MinIO ArtifactStore unavailable",
			Description: "Any non-zero rate on the minio_unreachable label means at least one gateway or pod replica is failing ArtifactStore PUTs after exhausting its retry budget because MinIO is unreachable. Blocks workspace uploads at session creation and seal-and-export at session termination.",
			RunbookURL:  runbook("minio-failure"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "T4KmsKeyUnusable",
			Expr:        `time() - max by (tenant_id) (lenny_t4_kms_probe_last_success_timestamp) > 2 * 300`,
			Severity:    SeverityCritical,
			Summary:     "T4 tenant KMS key unusable",
			Description: "The leader-elected continuous KMS probe has not successfully encrypted and decrypted against a T4 tenant's KMS key for at least two probe cycles. Any checkpoint or artifact write for this tenant will be rejected with CLASSIFICATION_CONTROL_VIOLATION / kms_unavailable.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/kms-unavailable.md.
			RunbookURL: runbook("kms-unavailable"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "EtcdUnavailable",
			Expr:        `rate(lenny_etcd_connectivity_errors_total[1m]) > 0`,
			For:         15 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "API server etcd connectivity errors",
			Description: "API server etcd connectivity errors have been sustained for more than 15s. CRD reads and writes fail and warm-pool reconciliation stalls.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/etcd-operations.md.
			RunbookURL: runbook("etcd-operations"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "CredentialPoolExhausted",
			Expr:        `min by (pool) (lenny_credential_pool_assignable_count) == 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Credential pool has no assignable credentials",
			Description: "A credential pool has 0 assignable credentials (all exhausted, in cooldown, or revoked) for more than 30s. New session creation returns CREDENTIAL_POOL_EXHAUSTED for this pool.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/credential-pool-exhaustion.md.
			RunbookURL: runbook("credential-pool-exhaustion"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "CredentialCompromised",
			Expr:        `(max by (pool, provider) (lenny_credential_revoked_with_active_leases) > 0) or (max by (tenant_id, provider) (lenny_user_credential_revoked_with_active_leases) > 0)`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Revoked credential still has active leases",
			Description: "At least one credential in revoked state (pool-scoped or user-scoped) still has active leases alive against it for more than 30s, indicating revocation propagation failure and that the compromised key may still be in use.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/credential-revocation.md.
			RunbookURL: runbook("credential-revocation"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "TokenServiceUnavailable",
			Expr:        `lenny_token_service_circuit_state == 2`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Token Service circuit breaker open",
			Description: "The Token Service circuit breaker has been open for more than 30s; new sessions requiring credentials will fail. Existing sessions are unaffected until lease expiry.",
			RunbookURL:  runbook("token-service-outage"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "ControllerLeaderElectionFailed",
			Expr:        `lenny_controller_leader_lease_renewal_age_seconds > 15`,
			Severity:    SeverityCritical,
			Summary:     "Controller Lease has not been renewed",
			Description: "A controller's Lease has not been renewed within leaseDuration (15s); failover is imminent or in progress. Auto-resolves when a new leader acquires the lease.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/controller-leader-election.md.
			RunbookURL: runbook("controller-leader-election"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "DedicatedDNSUnavailable",
			Expr:        `kube_deployment_status_replicas_ready{deployment="lenny-agent-dns"} == 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "All dedicated agent CoreDNS replicas are down",
			Description: "All dedicated CoreDNS replicas for the agent namespace have zero ready pods. Agent pods lose DNS resolution entirely and cannot reach external tools or LLM endpoints.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/dns-outage.md.
			RunbookURL: runbook("dns-outage"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "CosignWebhookUnavailable",
			Expr:        `up{job="lenny-cosign-webhook"} == 0`,
			For:         60 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Cosign image-verification webhook unreachable",
			Description: "The cosign ValidatingAdmissionWebhook (failurePolicy: Fail) has become unreachable. Pod admission is blocked for the agent namespace — no new pods can be scheduled, halting warm pool replenishment.",
			RunbookURL:  runbook("admission-webhook-outage"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditGrantDrift",
			Expr:        `lenny_audit_grant_drift_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Unexpected grants detected on audit tables",
			Description: "The periodic background grant check detected that unexpected UPDATE or DELETE grants have been added to audit tables for the lenny_app role after startup. If audit.hardFailOnDrift is enabled, the gateway initiates graceful shutdown.",
			RunbookURL:  runbook("audit-grant-drift"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "NetworkPolicyCIDRDrift",
			Expr:        `lenny_network_policy_cidr_drift_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Internet egress NetworkPolicy except blocks are stale",
			Description: "The continuous CIDR drift check detected that the installed internet egress NetworkPolicy except blocks no longer match the cluster's actual pod or service CIDRs. Agent pods with internet egress may be able to reach internal cluster IPs, enabling lateral movement.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/network-policy-drift.md.
			RunbookURL: runbook("network-policy-drift"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "AdmissionWebhookUnavailable",
			Expr:        `up{job="lenny-admission-policy-webhook"} == 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "RuntimeClass-aware admission webhook unreachable",
			Description: "The RuntimeClass-aware admission policy webhook (OPA/Gatekeeper or Kyverno) has been unreachable for more than 30s. With failurePolicy: Fail, pod admission is denied — no new pods can be scheduled, halting warm pool replenishment.",
			RunbookURL:  runbook("admission-webhook-outage"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "SandboxClaimGuardUnavailable",
			Expr:        `up{job="lenny-sandboxclaim-guard"} == 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "SandboxClaim guard webhook unreachable",
			Description: "The lenny-sandboxclaim-guard ValidatingAdmissionWebhook has been unreachable for more than 30s. With failurePolicy: Fail, every SandboxClaim CREATE is blocked; new pod acquisition is prevented, halting session creation. Double-claim prevention is suspended until the webhook recovers.",
			RunbookURL:  runbook("sandboxclaim-guard-unavailable"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "DualStoreUnavailable",
			Expr:        `lenny_dual_store_unavailable == 1`,
			Severity:    SeverityCritical,
			Summary:     "Postgres primary and Redis simultaneously unreachable",
			Description: "Both the Postgres primary and Redis are simultaneously unreachable. The gateway is operating in dual-store degraded mode: new session creation is rejected (503) and PLATFORM_DEGRADED events are pushed to all active client streams.",
			RunbookURL:  runbook("dual-store-unavailable"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "DataResidencyWebhookUnavailable",
			Expr:        `up{job="lenny-data-residency-validator"} == 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Data-residency validator webhook unreachable",
			Description: "The lenny-data-residency-validator ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 30s. All operations on tenant-scoped CRD resources with a dataResidencyRegion field are denied.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/admission-webhook-outage.md.
			RunbookURL: runbook("admission-webhook-outage"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "DataResidencyViolationAttempt",
			Expr:        `lenny_data_residency_violation_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Cross-border data-residency transfer attempt",
			Description: "A StorageRouter write, pod pool delegation, Postgres backup, ArtifactStore replication preflight, legal-hold escrow migration, or platform-tenant audit write was rejected because the resolved region did not match the controlling dataResidencyRegion. Indicates a misconfiguration or code-path bypass.",
			RunbookURL:  runbook("data-residency-violation"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "ArtifactReplicationResidencyViolation",
			Expr:        `rate(lenny_minio_replication_residency_violation_total[5m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "ArtifactStore replication residency violation",
			Description: "The ArtifactStore runtime residency preflight observed a jurisdiction-tag mismatch, missing tag, DNS rebinding outside the allowlisted CIDRs, or a failed destination tag-probe. Replication for the affected region is suspended.",
			RunbookURL:  runbook("artifact-replication-residency-violation"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "LegalHoldEscrowResidencyViolation",
			Expr:        `rate(lenny_legal_hold_escrow_region_unresolvable_total[5m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "Legal-hold escrow region unresolvable",
			Description: "Phase 3.5 of a tenant force-delete aborted because the resolved target escrow region has no corresponding storage.regions.<region>.legalHoldEscrow entry, or that region's escrow KMS key or bucket endpoint is unreachable.",
			RunbookURL:  runbook("legal-hold-escrow-residency-violation"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "PlatformAuditResidencyViolation",
			Expr:        `rate(lenny_platform_audit_region_unresolvable_total[5m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "Platform-tenant audit region unresolvable",
			Description: "A platform-tenant audit event referencing a non-platform target_tenant_id failed to commit because the target tenant's dataResidencyRegion resolves to no storage.regions.<region>.postgresEndpoint entry or that region's platform-Postgres is unreachable.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/data-residency-violation.md.
			RunbookURL: runbook("data-residency-violation"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "PgBouncerAllReplicasDown",
			Expr:        `kube_deployment_status_replicas_ready{deployment="lenny-pgbouncer"} == 0`,
			Severity:    SeverityCritical,
			Summary:     "All PgBouncer replicas are down",
			Description: "All PgBouncer pods in lenny-system have zero ready replicas (self-managed backends only). Postgres is unreachable for all gateway components — session creation and state writes will fail immediately.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/pgbouncer-saturation.md.
			RunbookURL: runbook("pgbouncer-saturation"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "SessionEvictionTotalLoss",
			Expr:        `lenny_session_eviction_total_loss_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Session lost with no durable state",
			Description: "Both MinIO and Postgres were unavailable during an eviction checkpoint, leaving the session unrecoverable with no durable state saved. Any non-zero value is immediately actionable.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/session-eviction-loss.md.
			RunbookURL: runbook("session-eviction-loss"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "DelegationBudgetKeysExpired",
			Expr:        `lenny_delegation_budget_keys_expired_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Delegation budget keys expired while tree active",
			Description: "A Lua script returned BUDGET_KEYS_EXPIRED, indicating the delegation budget keys for a root session have expired while the tree was still active. The gateway initiates tree cleanup (cascade cancel + root to failed).",
			// spec: §17.7 line 745 — slug matches docs/runbooks/delegation-budget-recovery.md.
			RunbookURL: runbook("delegation-budget-recovery"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "BillingStreamEntryAgeHigh",
			Expr:        `lenny_billing_redis_stream_oldest_entry_age_seconds > 2880`,
			Severity:    SeverityCritical,
			Summary:     "Billing Redis stream entry near TTL expiry",
			Description: "The oldest unacknowledged entry in a per-tenant billing Redis stream exceeds 80% of billingStreamTTLSeconds. A small number of billing events have been sitting unflushed long enough to be at imminent risk of TTL expiry and permanent loss.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/billing-stream-backlog.md.
			RunbookURL: runbook("billing-stream-backlog"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "OTLPPlaintextEgressDetected",
			Expr:        `rate(lenny_otlp_export_tls_handshake_total{result="plaintext"}[5m]) > 0`,
			For:         60 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "OTLP trace export is shipping plaintext",
			Description: "At least one gateway or pod OTel exporter is shipping trace payloads without negotiating TLS to the configured collector endpoint. Trace payloads carry tenant/session metadata and error bodies; any non-zero rate is an active confidentiality regression.",
			RunbookURL:  runbook("otlp-plaintext-egress-detected"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "OpsAdminAPIPlaintextDetected",
			Expr:        `rate(lenny_ops_admin_api_tls_handshake_total{result="plaintext"}[5m]) > 0`,
			For:         60 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "lenny-ops is calling the gateway admin API over plaintext",
			Description: "lenny-ops is calling the gateway admin API over plaintext HTTP. The admin-API link transports a platform-admin-scoped JWT in every request and carries pool configs, connector settings, upgrade state, and audit-bearing event envelopes; any non-zero rate is an active confidentiality regression.",
			RunbookURL:  runbook("ops-admin-api-plaintext-detected"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "ElicitationContentTamperDetected",
			Expr:        `increase(lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce"}[5m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "Elicitation content tamper detected under enforce mode",
			Description: "An intermediate pod re-emitted an MCP elicitation/create wire frame for an existing elicitation_id carrying a {message, schema} pair that diverges from the gateway-recorded original. Under enforce the forward was dropped; any tamper attempt indicates a prompt-injected or hostile intermediate runtime.",
			RunbookURL:  runbook("elicitation-content-tamper-detected"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditRedactionReceiptMissing",
			Expr:        `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0`,
			Severity:    SeverityCritical,
			Summary:     "GDPR redaction receipt missing for a redacted audit row",
			Description: "A row is classified chainIntegrity=redacted_gdpr by the verifier but the corresponding signed RedactionReceipt is absent, signature-invalid, or mismatches the chain rewrite boundary. Distinguishes an orphaned GDPR redaction from a genuine tamper.",
			RunbookURL:  runbook("audit-redaction-receipt-missing"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "LLMUpstreamEgressAnomaly",
			Expr:        `lenny_gateway_llm_upstream_egress_anomaly_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Gateway LLM upstream egress anomaly",
			Description: "The gateway observed an outbound connection attempt from the gateway pod to a destination outside the allow-gateway-egress-llm-upstream NetworkPolicy allowlist. Steady-state value is zero; any non-zero rate is a potential compromise signal.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/llm-egress-anomaly.md.
			RunbookURL: runbook("llm-egress-anomaly"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "TokenStoreUnavailable",
			Expr:        `rate(lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}[1m]) > 0`,
			For:         30 * time.Second,
			Severity:    SeverityCritical,
			Summary:     "Token store unavailable",
			Description: "/v1/oauth/token has been returning 503 token_store_unavailable for more than 30s. Postgres primary is unreachable for token issuance; session creation, delegation minting, and credential leasing are all failing.",
			RunbookURL:  runbook("token-store-unavailable"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "BackupReconcileBlocked",
			Expr:        `lenny_backup_reconcile_blocked_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "Post-restore erasure reconciler blocked replay",
			Description: "The post-restore GDPR erasure reconciler blocked replay because the legal-hold ledger is stale relative to the restore point. The gateway is not restarted until an operator confirms ledger currency.",
			RunbookURL:  runbook("backup-reconcile-blocked"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "MinIOArtifactReplicationLagCritical",
			Expr:        `lenny_minio_replication_lag_seconds > 3600`,
			Severity:    SeverityCritical,
			Summary:     "ArtifactStore replication lag exceeds 4x RPO",
			Description: "ArtifactStore replication lag exceeds 4x the configured RPO. The replication is severely degraded and a full-site disaster at this point would lose a materially larger artifact window than the deployment contract allows.",
			// spec: §17.7 line 745 — slug matches docs/runbooks/minio-replication-lag.md.
			RunbookURL: runbook("minio-replication-lag"),
			SpecRef:    "§16.5",
		},
		{
			Name:        "LenniOpsLockSplitBrainDetected",
			Expr:        `lenny_ops_lock_split_brain_detected_total > 0`,
			Severity:    SeverityCritical,
			Summary:     "lenny-ops remediation lock split-brain detected",
			Description: "Two lenny-ops replicas briefly believed they held the same remediation lock; outage-epoch reconciliation resolved the conflict but the event requires auditing.",
			RunbookURL:  runbook("ops-lock-split-brain"),
			SpecRef:     "§16.5",
		},
	}
}

// warningAlerts is the §16.5 "Warning alerts" table. The
// GatewayClockDrift entry is the warning lower bound of the
// threshold-graduated rule whose stricter thresholds escalate to
// critical and to replica self-removal. The AuditChainGap table places
// AuditRedactionReceiptMissing (critical) in criticalAlerts above.
func warningAlerts() []Rule {
	return []Rule{
		{
			Name: "WarmPoolLow",
			// §4.6.1 re-activation grace period: suppress while the pool is
			// inside its initial-fill grace window, covering scale-from-zero
			// resumes and minWarm 0→positive re-activations.
			Expr:        `(lenny_warmpool_idle_pods / on(pool) group_left lenny_warmpool_min_warm < 0.25) unless on (pool) (lenny_warmpool_fill_grace_active == 1)`,
			Severity:    SeverityWarning,
			Summary:     "Warm pool below 25 percent of minWarm",
			Description: "Available warm pods are below 25 percent of the pool's minWarm setting. Pool replenishment is lagging behind session arrival rate.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "RedisMemoryHigh",
			Expr:        `lenny_redis_memory_used_bytes / lenny_redis_maxmemory_bytes > 0.80`,
			Severity:    SeverityWarning,
			Summary:     "Redis memory above 80 percent of maxmemory",
			Description: "Redis memory exceeds 80 percent of maxmemory. Under the noeviction policy required for delegation budget keys, sustained growth risks write rejections.",
			SpecRef:     "§16.5",
		},
		{
			Name: "CredentialPoolLow",
			// spec: §25.13 line 4737 — tier-dependent utilisation
			// ceiling. The gateway emits the configured fraction on
			// lenny_credential_pool_low_threshold (default 0.80,
			// monitoring.alertThresholds.credentialPoolLow.utilizationThreshold).
			// The scalar lookup keeps the rendered expression valid
			// when the operator tightens the threshold via the tier
			// preset without re-rendering the rule body. F-25.13.2.
			Expr:        `lenny_credential_pool_utilization > scalar(lenny_credential_pool_low_threshold)`,
			Severity:    SeverityWarning,
			Summary:     "Credential pool utilisation above the configured ceiling",
			Description: "Pool utilisation exceeds the configured fraction for any pool. Defaults to 80 percent (Tier 1); tighter tier presets (Tier 2 / Tier 3) lower the ceiling via monitoring.alertThresholds.credentialPoolLow.utilizationThreshold.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CredentialProactiveRenewalExhausted",
			Expr:        `lenny_credential_proactive_renewal_exhausted_total > 0`,
			Severity:    SeverityWarning,
			Summary:     "Credential proactive renewal retries exhausted",
			Description: "All proactive renewal retries for a credential lease were exhausted before expiry. The session falls through to the standard credential fallback flow.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "OutstandingInflightAtRotationCeiling",
			Expr:        `lenny_credential_rotation_inflight_ceiling_hit_total > 0`,
			Severity:    SeverityWarning,
			Summary:     "Credential rotation hit the in-flight ceiling",
			Description: "The 300-second in-flight gate ceiling was hit for a rotation whose trigger is not proactive_renewal and the adapter had to force credentials_rotated regardless of outstanding in-flight LLM requests. Indicates a compromised or buggy runtime.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewayActiveStreamsHigh",
			Expr:        `lenny_gateway_active_streams / scalar(lenny_gateway_stream_ceiling) > 0.80`,
			Severity:    SeverityWarning,
			Summary:     "Gateway active streams above 80 percent of ceiling",
			Description: "lenny_gateway_active_streams exceeds 80 percent of the replica's configured stream ceiling on any replica.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewaySessionBudgetNearExhaustion",
			Expr:        `lenny_gateway_active_sessions / scalar(lenny_gateway_max_sessions_per_replica) > 0.90`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Gateway session budget near exhaustion",
			Description: "lenny_gateway_active_sessions / gateway.maxSessionsPerReplica exceeds 90 percent on any replica for more than 60s; HPA scale-out is lagging behind session arrival rate.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PDBBlockedEvictions",
			Expr:        `increase(lenny_pdb_blocked_evictions_total{pdb="lenny-gateway"}[10m]) > 5`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Gateway PDB-blocked evictions are sustained",
			Description: "Voluntary evictions against the gateway PDB are being rejected repeatedly (eviction API returning 429). Sustained high rates indicate minReplicas set too low, a preStop checkpoint cap hitting its bound, or a competing non-HPA disruption source.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "Tier3GCPressureHigh",
			Expr:        `lenny_gateway_gc_pause_fleet_p99_ms{deployment_tier="tier3"} > 50`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Tier 3 fleet-wide GC pressure high",
			Description: "Fleet-wide P99 GC pause exceeds 50 ms for more than 5 min on a Tier 3 deployment. The combined gateway binary is experiencing shared-process GC pressure — the LLM Proxy subsystem should be extracted or maxSessionsPerReplica reduced.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ArtifactGCBacklog",
			Expr:        `lenny_artifact_gc_backlog > scalar(lenny_artifact_gc_backlog_threshold)`,
			Severity:    SeverityWarning,
			Summary:     "Expired artifacts pending cleanup exceed the tier threshold",
			Description: "Expired artifacts pending cleanup exceeds the tier-dependent threshold (§17.8).",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CheckpointStale",
			Expr:        `lenny_checkpoint_stale_sessions > 0`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Active sessions have stale checkpoints",
			Description: "One or more active sessions have a last checkpoint age exceeding periodicCheckpointIntervalSeconds for any pool/level for more than 60s and are at elevated risk of workspace loss on eviction.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "RateLimitDegraded",
			Expr:        `lenny_rate_limit_failopen_active == 1`,
			Severity:    SeverityWarning,
			Summary:     "Rate limiting in fail-open mode",
			Description: "Rate limiting has entered fail-open mode. Per-replica in-memory enforcement is in effect because Redis is unreachable.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "QuotaFailOpenCumulativeThreshold",
			Expr:        `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * 300`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Quota fail-open cumulative budget near exhaustion",
			Description: "At least one gateway replica has spent 80 percent of the configured cumulative fail-open budget within the rolling 1-hour window — the pre-breach warning issued before the replica transitions to fail-closed for quota enforcement.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "QuotaFailOpenUserFractionInoperative",
			Expr:        `lenny_quota_user_failopen_fraction >= 0.5`,
			Severity:    SeverityWarning,
			Summary:     "Per-user quota fail-open fraction substantially weakened",
			Description: "The configured per-user fail-open fraction is at or above 0.5; a single user can consume at least half the tenant's per-replica fail-open allocation during a Redis outage, so the monopolization-prevention intent is largely inoperative.",
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
		{
			Name:        "ElicitationBacklogHigh",
			Expr:        `lenny_elicitation_pending > 50`,
			For:         30 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Pending elicitation requests above 50",
			Description: "Pending elicitation requests exceed 50 for more than 30s.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ElicitationContentIntegrityPermissiveTamper",
			Expr:        `increase(lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only"}[5m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Elicitation content divergence observed under detect-only",
			Description: "A {message, schema} divergence was observed under effective detect-only mode: the gateway forwarded the divergent payload to the client but the detector recorded the event. Either a legitimate intermediate transformation is triggering the detector or a genuine tamper is being tolerated by policy.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ElicitationContentIntegrityWeakened",
			Expr:        `lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce > 0`,
			Severity:    SeverityWarning,
			Summary:     "A tenant's effective elicitation content integrity mode is weaker than enforce",
			Description: "At least one tenant has an effective elicitation content integrity enforcement mode weaker than enforce. Fires continuously while the condition holds so operators who joined after the weakening remain aware of the reduced-integrity posture.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DelegationBudgetNearExhaustion",
			Expr:        `max by (pool, tenant_id) (lenny_delegation_budget_utilization_ratio) > 0.90`,
			Severity:    SeverityWarning,
			Summary:     "Delegation budget utilization above 90 percent",
			Description: "Delegation budget utilization ratio exceeds 90 percent for any active delegation tree.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DelegationBudgetIrrecoverable",
			Expr:        `increase(lenny_delegation_budget_reconstruction_total{outcome="irrecoverable"}[5m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Delegation tree budget state unrecoverable",
			Description: "A delegation tree moved to awaiting_client_action due to BUDGET_STATE_UNRECOVERABLE during Redis recovery.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CycleDetectionModeUnsafe",
			Expr:        `lenny_delegation_cycle_detection_mode_permissive == 1`,
			Severity:    SeverityWarning,
			Summary:     "Delegation cycle detection is in permissive mode",
			Description: "The chart-rendered gateway.cycleDetection.mode Helm value is permissive — no runtime-identity cycle check runs and self-recursive delegation hops are admitted silently. Fires continuously while the condition holds.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CycleDetectionWarnModeBlocking",
			Expr:        `sum by (tenant_id) (rate(lenny_delegation_would_have_blocked_total{mode="warn"}[10m])) > 0`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Cycle detection warn-mode is admitting would-be-blocked hops",
			Description: "The deployment is running gateway.cycleDetection.mode: warn and at least one tenant is producing self-recursive delegation hops that would be rejected under mode: enforce. The alert exists so a diagnostic rollout cannot be forgotten.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditSIEMNotConfigured",
			Expr:        `lenny_audit_siem_configured == 0 and lenny_env_production == 1`,
			Severity:    SeverityWarning,
			Summary:     "Audit SIEM endpoint not configured in production",
			Description: "LENNY_ENV=production and audit.siem.endpoint is not set. Fires at startup and persists until an endpoint is configured. When any active tenant has a regulated complianceProfile the gateway refuses to start instead.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditChainGap",
			Expr:        `increase(lenny_audit_chain_integrity_total{state="broken"}[15m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Audit hash chain gap detected",
			Description: "The startup chain-continuity check detected a broken hash chain for one or more tenants. Rows classified redacted_gdpr with a verified RedactionReceipt are excluded. Genuine broken events indicate buffered events lost during a prior crash or tampering.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CheckpointStorageHigh",
			Expr:        `lenny_checkpoint_storage_bytes_total / lenny_tenant_storage_quota_bytes > 0.80 and lenny_tenant_storage_quota_bytes > 0`,
			Severity:    SeverityWarning,
			Summary:     "Per-tenant checkpoint storage above 80 percent of quota",
			Description: "Per-tenant checkpoint storage exceeds 80 percent of the tenant's storageQuotaBytes. Checkpoints and other artifacts share the same per-tenant quota bucket.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "StorageQuotaHigh",
			Expr:        `lenny_storage_quota_bytes_used / lenny_tenant_storage_quota_bytes > 0.80 and lenny_tenant_storage_quota_bytes > 0`,
			Severity:    SeverityWarning,
			Summary:     "Per-tenant storage quota above 80 percent",
			Description: "Per-tenant artifact storage exceeds 80 percent of the tenant's storageQuotaBytes. New uploads or checkpoints will be rejected at 100 percent.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CheckpointDurationHigh",
			Expr:        `histogram_quantile(0.95, sum by (le) (rate(lenny_checkpoint_duration_seconds_bucket[5m]))) > 2.5`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Checkpoint P95 duration above 2.5 seconds",
			Description: "P95 of lenny_checkpoint_duration_seconds for Full-level or embedded-adapter pools exceeds 2.5 seconds over a 5-minute window (25 percent headroom above the 2s SLO for workspaces under 100MB).",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PreStopCapFallbackRateHigh",
			Expr:        `(sum by (service_instance_id, pool) (increase(lenny_prestop_cap_selection_total{source=~"postgres_null|cache_miss_max_tier"}[15m])) / sum by (service_instance_id, pool) (increase(lenny_prestop_cap_selection_total[15m]))) > 0.05`,
			Severity:    SeverityWarning,
			Summary:     "preStop checkpoint cap fallback rate high on a replica",
			Description: "For a gateway replica, the combined share of 90s-conservative-fallback selections among all preStop cap selections exceeds 5 percent over a 15-minute window. Indicates a cold in-replica cache or Postgres unreachability during preStop.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PodClaimQueueSaturated",
			Expr:        `lenny_pod_claim_queue_depth > 0.25 * lenny_pool_min_warm and on(pool) lenny_warmpool_idle_pods > 0`,
			For:         30 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Pod claim queue saturated while warm pods exist",
			Description: "For a pool, claim queue depth exceeds 25 percent of that pool's minWarm for more than 30s while idle pods exist for that same pool; the claim queue is backing up even though warm pods exist.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PodStateMirrorStale",
			Expr:        `max by (pool) (lenny_agent_pod_state_mirror_lag_seconds) > 60`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "agent_pod_state Postgres mirror is stale",
			Description: "The agent_pod_state Postgres mirror has not been updated for the affected pool within the staleness threshold — the WarmPoolController is not writing state transitions, likely a controller crash or leader-election failure.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "KMSSigningUnavailable",
			Expr:        `rate(lenny_gateway_kms_signing_errors_total[30s]) > 1`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Gateway KMS signing circuit breaker tripped",
			Description: "The gateway JWTSigner circuit breaker has tripped open — new session creation is failing with KMS_SIGNING_UNAVAILABLE. Existing sessions are unaffected until JWT expiry.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewaySubsystemCircuitOpen",
			Expr:        `max by (subsystem) (lenny_gateway_subsystem_circuit_state) == 2`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "A gateway subsystem circuit breaker is open",
			Description: "A gateway subsystem breaker has been open for more than 60s; the affected subsystem is rejecting requests while the other subsystems continue serving.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewayQueueDepthHigh",
			Expr:        `max by (subsystem) (lenny_gateway_subsystem_queue_depth) > scalar(lenny_gateway_queue_depth_threshold)`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "A gateway subsystem queue depth is high",
			Description: "A gateway subsystem queue depth exceeds the tier-scaled threshold sustained for more than 5 min; the subsystem is admitting work faster than it can drain and precedes GatewaySubsystemCircuitOpen if the condition continues.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewayLatencyHigh",
			Expr:        `histogram_quantile(0.95, sum by (le, subsystem) (rate(lenny_gateway_subsystem_request_duration_seconds_bucket[5m]))) > scalar(lenny_gateway_latency_threshold_seconds)`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "A gateway subsystem P95 latency is high",
			Description: "P95 request duration within a gateway subsystem exceeds the tier-scaled threshold sustained for more than 10 min; degraded end-to-end performance that may precede circuit-breaker trips.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolConfigDrift",
			Expr:        `lenny_pool_config_reconciliation_lag_seconds > 60`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Pool config generation drift",
			Description: "A pool's Postgres pool_config_generation differs from its CRD config-generation annotation for more than 60s; PoolScalingController reconciliation is stalled or the controller is down.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolConfigValidatorUnavailable",
			Expr:        `up{job="lenny-pool-config-validator"} == 0`,
			For:         30 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Pool config validator webhook unreachable",
			Description: "The lenny-pool-config-validator ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 30s. Manual and reconciliation-driven SandboxTemplate/SandboxWarmPool updates stall until the webhook recovers.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolScalingAdmissionStuck",
			Expr:        `increase(lenny_pool_scaling_admission_denied_total[5m]) >= 10`,
			Severity:    SeverityWarning,
			Summary:     "PoolScalingController is persistently rejected by the validator",
			Description: "The PoolScalingController has been rejected by the lenny-pool-config-validator webhook for the same (pool, crd) tuple admissionDeniedRetryCeiling times consecutively. The pool is held in the stuck-pool abort state.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LabelImmutabilityWebhookUnavailable",
			Expr:        `up{job="lenny-label-immutability"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Label immutability webhook unreachable",
			Description: "The lenny-label-immutability ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 5 min. Writes that mutate the immutable agent-pod labels are denied while the webhook is down.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DirectModeIsolationWebhookUnavailable",
			Expr:        `up{job="lenny-direct-mode-isolation"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Direct-mode isolation webhook unreachable",
			Description: "The lenny-direct-mode-isolation ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 5 min. New pods combining deliveryMode: direct with isolationProfile: standard cannot be admitted in multi-tenant mode.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "T4NodeIsolationWebhookUnavailable",
			Expr:        `up{job="lenny-t4-node-isolation"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "T4 node isolation webhook unreachable",
			Description: "The lenny-t4-node-isolation ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 5 min. Admission of T4-isolation pods stalls until the webhook recovers.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DrainReadinessWebhookUnavailable",
			Expr:        `up{job="lenny-drain-readiness"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Drain readiness webhook unreachable",
			Description: "The lenny-drain-readiness ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 5 min. Pod evictions that require a MinIO pre-drain health check are blocked until the webhook recovers.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CrdConversionWebhookUnavailable",
			Expr:        `up{job="lenny-crd-conversion"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "CRD conversion webhook unreachable",
			Description: "The lenny-crd-conversion CRD conversion webhook has been unreachable for more than 5 min. Reads and writes on Lenny CRDs at non-storage versions fail until the webhook recovers.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "EphemeralContainerCredGuardUnavailable",
			Expr:        `up{job="lenny-ephemeral-container-cred-guard"} == 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Ephemeral-container cred guard webhook unreachable",
			Description: "The lenny-ephemeral-container-cred-guard ValidatingAdmissionWebhook (failurePolicy: Fail) has been unreachable for more than 5 min. All update operations on pods/ephemeralcontainers in agent namespaces are denied while the webhook is down.",
			SpecRef:     "§16.5",
		},
		// spec: §16.5 line 487 / §17.2 line 80 —
		// AdmissionPlaneFeatureFlagDowngrade is the union of one
		// single-pair rule per (flag, webhook) entry in the §17.2
		// Feature-gated chart inventory. All four rules share the alert
		// Name and each carries static flag_name / expected_webhook_name
		// labels so a firing identifies the specific missing admission-plane
		// surface (features.compliance gates two webhooks, so a full
		// compliance downgrade emits two firings with identical flag_name
		// but distinct expected_webhook_name). The label selector
		// label_lenny_dev_flag_<slug>_enabled is the kube-state-metrics
		// rendering of the phase-stamp ConfigMap's lenny.dev/flag-<slug>-enabled
		// label (see phase-stamp-configmap.yaml). F-17.2.6.
		admissionPlaneDowngradeRule("features.llmProxy", "lenny-direct-mode-isolation", "label_lenny_dev_flag_llm_proxy_enabled"),
		admissionPlaneDowngradeRule("features.drainReadiness", "lenny-drain-readiness", "label_lenny_dev_flag_drain_readiness_enabled"),
		admissionPlaneDowngradeRule("features.compliance", "lenny-data-residency-validator", "label_lenny_dev_flag_compliance_enabled"),
		admissionPlaneDowngradeRule("features.compliance", "lenny-t4-node-isolation", "label_lenny_dev_flag_compliance_enabled"),
		{
			Name: "WarmPoolReplenishmentSlow",
			// spec: §16.5 line 488 — fire at 2× the pool's
			// scalingPolicy.podWarmupSecondsBaseline. The
			// PoolScalingController mirrors each pool's baseline into the
			// per-pool lenny_pool_warmup_seconds_baseline gauge so the
			// threshold tracks the operator-configured value instead of a
			// fixed 60s (= 2× the 30s default). The comparison matches on
			// the shared `pool` label.
			Expr:        `histogram_quantile(0.95, sum by (le, pool) (rate(lenny_warmpool_pod_startup_duration_seconds_bucket[5m]))) > 2 * lenny_pool_warmup_seconds_baseline`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Warm pool replenishment slower than expected",
			Description: "P95 of lenny_warmpool_pod_startup_duration_seconds exceeds 2x the pool's podWarmupSecondsBaseline for more than 5 min; pool refill is slower than expected and WarmPoolLow may not self-correct.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "WarmPoolReplenishmentFailing",
			Expr:        `rate(lenny_warmpool_warmup_failure_total[5m]) * 60 > 1`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Warm pool warm-up failures sustained",
			Description: "lenny_warmpool_warmup_failure_total rate exceeds 1 failure/min for any pool for more than 5 min; pods are failing to reach idle state during warm-up. WarmPoolLow / WarmPoolExhausted may follow.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolScaleoutBlockedByQuota",
			Expr:        `increase(lenny_warmpool_warmup_failure_total{error_type="resource_quota_exceeded"}[5m]) > 0`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Warm pool scale-out blocked by ResourceQuota",
			Description: "The namespace ResourceQuota is rejecting warm-pool pod creation — desired replicas cannot be realised and warm-pool scaling silently stalls without HPA or cluster-autoscaler signals.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "SDKConnectTimeout",
			Expr:        `rate(lenny_warmpool_sdk_connect_timeout_total[5m]) * 60 > 0.1`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "SDK-warm connect timeouts sustained",
			Description: "lenny_warmpool_sdk_connect_timeout_total rate exceeds 0.1/min for more than 5 min on a pool; SDK warm startup is systematically failing to complete and phantom warm pods may be accumulating.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ErasureJobFailed",
			Expr:        `lenny_erasure_job_failed_total > 0`,
			Severity:    SeverityWarning,
			Summary:     "A user-level erasure job has failed",
			Description: "A user-level erasure job has failed. The user's processing_restricted flag remains set, blocking all new sessions for that user. The failure_phase label distinguishes failure modes.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ErasureJobOverdue",
			Expr:        `lenny_erasure_job_age_seconds > scalar(lenny_erasure_job_deadline_seconds)`,
			Severity:    SeverityWarning,
			Summary:     "A user-level erasure job is overdue",
			Description: "A user-level erasure job has exceeded its tier-specific deadline (72h for T3, 1h for T4) without completing. Requires operator investigation.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "MemoryStoreGrowthHigh",
			Expr:        `sum by (tenant_id) (rate(lenny_memory_store_user_count_over_threshold_total[5m])) > 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Memory store per-user retention headroom is being exhausted",
			Description: "At least one user in the affected tenant has a memory record count at or above 80 percent of memory.maxMemoriesPerUser and the MemoryStore.Write path is continuing to emit threshold-crossing increments.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "MemoryStoreErasureDurationHigh",
			Expr:        `histogram_quantile(0.99, sum by (le, backend) (rate(lenny_memory_store_operation_duration_seconds_bucket{operation="delete_by_user"}[5m]))) > 60`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Memory store whole-scope erasure is exceeding its SLO",
			Description: "The MemoryStore backend is taking longer than the whole-scope erasure SLO (60s per-user, 5 min per-tenant) to complete synchronous DeleteByUser / DeleteByTenant calls. Sustained breach puts tiered erasure deadlines at risk.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LegalHoldOverrideUsed",
			Expr:        `increase(lenny_gdpr_legal_hold_overridden_total[1h]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "User-level legal-hold override used",
			Description: "A platform-admin invoked the user erase endpoint with acknowledgeHoldOverride: true to bypass the DeleteByUser legal-hold preflight. The erasure proceeded despite active legal holds on the target user.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LegalHoldOverrideUsedTenant",
			Expr:        `increase(lenny_gdpr_legal_hold_overridden_tenant_total[1h]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Tenant-level legal-hold override used",
			Description: "A platform-admin invoked tenant force-delete with acknowledgeHoldOverride: true to bypass the Phase 3.5 legal-hold segregation gate. Tenant deletion proceeded despite active legal holds.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CompliancePostureDecommissioned",
			Expr:        `increase(lenny_compliance_profile_decommissioned_total[1h]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "A regulated compliance profile was decommissioned",
			Description: "A platform-admin lowered a regulated complianceProfile via the decommission endpoint. The tenant's SIEM hard-requirement, pgaudit requirement, grant-check floor, cross-user-cache prohibition, and GDPR retention floor are all relaxed.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LegalHoldCheckpointAccumulationProjectedBreach",
			Expr:        `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0`,
			Severity:    SeverityWarning,
			Summary:     "Projected legal-hold checkpoint growth will breach quota",
			Description: "24-hour projected legal-hold checkpoint growth will push the tenant's shared storageQuotaBytes bucket past 90 percent utilization. Fires ahead of the reactive 80 percent StorageQuotaHigh / CheckpointStorageHigh thresholds.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "TenantDeletionOverdue",
			Expr:        `lenny_tenant_deletion_duration_seconds > scalar(lenny_tenant_deletion_sla_seconds)`,
			Severity:    SeverityWarning,
			Summary:     "A tenant deletion has exceeded its SLA",
			Description: "A tenant has been in disabling or deleting state longer than 80 percent of its tier SLA (T3: 72h, T4: 4h) without reaching deleted. Indicates a stalled deletion phase.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "KmsKeyDeletionFailed",
			Expr:        `lenny_kms_key_deletion_failed_total > 0`,
			Severity:    SeverityWarning,
			Summary:     "Per-tenant KMS key deletion failed",
			Description: "Phase 4a of the tenant deletion lifecycle failed to disable or schedule deletion of the per-tenant KMS key. The tenant's T4 KMS key may remain active, violating least-privilege.",
			SpecRef:     "§16.5",
		},
		{
			// spec: §11.2.1 line 187 — "deployer-configurable percentage (default 5%)".
			// The threshold is read from scalar(lenny_billing_correction_rate_threshold),
			// a startup-set gauge the gateway emits from the
			// billing.correctionRateThreshold Helm value. F-11.2.23.
			Name:        "BillingCorrectionRateHigh",
			Expr:        `lenny_billing_correction_rate_24h > scalar(lenny_billing_correction_rate_threshold)`,
			Severity:    SeverityWarning,
			Summary:     "Billing correction rate above the configured threshold",
			Description: "Billing correction events exceed the deployer-configurable percentage (default 5 percent) of total billing events in a rolling 24h window. May indicate compromised credentials, operational error, or a systematic metering bug.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "BillingStreamBackpressure",
			Expr:        `lenny_billing_redis_stream_depth / scalar(lenny_billing_redis_stream_max_len) > 0.80`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Billing Redis stream depth at 80 percent of capacity",
			Description: "Per-tenant billing Redis stream depth has reached 80 percent of billingRedisStreamMaxLen for more than 60s. Indicates sustained Postgres write failure — billing events are at risk of permanent loss if the stream TTL expires.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "BillingCorrectionApprovalBacklog",
			Expr:        `lenny_billing_correction_pending_total{state="pending"} > 10`,
			For:         60 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Billing correction approval queue backlog",
			Description: "The pending billing correction approval queue exceeds the deployer-configurable threshold (default 10) for more than billing.approvalBacklogAlertMinutes. Corrections awaiting approval may block dispute resolution workflows.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "BillingWriteAheadBufferHigh",
			Expr:        `lenny_billing_write_ahead_buffer_utilization > 0.80`,
			For:         30 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Billing write-ahead buffer above 80 percent",
			Description: "The in-memory billing write-ahead buffer utilization exceeds 0.80 for any tenant for more than 30s. When the buffer reaches capacity the gateway begins rejecting new billable work.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditRetentionLow",
			Expr:        `lenny_audit_retention_days < 365 and lenny_audit_siem_configured == 0 and lenny_env_production == 1`,
			Severity:    SeverityWarning,
			Summary:     "Audit retention below 365 days without a SIEM",
			Description: "LENNY_ENV=production and audit.retentionDays is below 365 days without a configured SIEM. Fires at startup. Suppressed when a SIEM is configured.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PostgresWriteSaturation",
			Expr:        `lenny_postgres_write_iops / scalar(lenny_postgres_write_ceiling_iops) > 0.80`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Sustained Postgres write IOPS above 80 percent of ceiling",
			Description: "Sustained Postgres write IOPS exceed 80 percent of the estimated instance ceiling for more than 5 min. The primary instance is approaching its write capacity.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ScatterGatherSlowQuery",
			Expr:        `histogram_quantile(0.99, sum by (le, query_type) (rate(lenny_store_router_scatter_gather_duration_seconds_bucket[5m]))) > 30`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Scatter-gather query P99 above 30 seconds",
			Description: "P99 of lenny_store_router_scatter_gather_duration_seconds for any query_type exceeds 30s sustained for more than 5 min. A multi-shard scatter-gather path is degraded. Trivially suppressed in v1 (single shard).",
			SpecRef:     "§16.5",
		},
		{
			Name: "PostgresWriteBurstIops",
			// §16.5 names the trigger "repeated triggers (> 3 in 10
			// minutes)". The single-event indicator is
			// (lenny_postgres_write_burst_iops > burst_ceiling); a single
			// crossing is not actionable, so the alert fires only when
			// the indicator has been true for more than 3 of the past 10
			// 1-minute samples. The 30-second measurement window the
			// spec stipulates is captured upstream in the
			// lenny_postgres_write_burst_iops gauge itself (the gateway
			// emits a wal_bytes/s → IOPS conversion over the rolling
			// 30s window); the alert's count_over_time tallies how many
			// scrape intervals the conversion exceeded the ceiling.
			Expr:        `count_over_time((lenny_postgres_write_burst_iops > scalar(lenny_postgres_write_burst_ceiling_iops))[10m:1m]) > 3`,
			Severity:    SeverityWarning,
			Summary:     "Postgres write burst IOPS exceeded the burst ceiling more than 3 times in 10 minutes",
			Description: "Instantaneous Postgres write IOPS exceeded the configured burst ceiling more than 3 times in the past 10 minutes. A single burst is not actionable; sustained repetition indicates quota-flush storms or session-creation spikes regularly saturating burst headroom. Review postgres.writeCeilingIops against the Phase 13.5 Lenny-specific write-pattern benchmark.",
			SpecRef:     "§16.5",
		},
		{
			Name: "PgBouncerPoolSaturated",
			// spec: §12.3 line 47 / §16.5 line 510 — the pgbouncer_exporter
			// sidecar exposes the SHOW POOLS maxwait stat (cl_waiting_time)
			// as pgbouncer_pools_client_maxwait_seconds; the alert fires when
			// any pool's longest-waiting client exceeds 1s. F-12.3.11.
			Expr:        `max(pgbouncer_pools_client_maxwait_seconds) > 1`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "PgBouncer connection pool saturated",
			Description: "PgBouncer cl_waiting_time exceeds 1s for more than 60s (self-managed profile only). Client requests are queuing behind connection limits — pool size or max_client_conn must be increased.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolBootstrapMode",
			Expr:        `lenny_pool_bootstrap_mode == 1`,
			For:         72 * time.Hour,
			Severity:    SeverityWarning,
			Summary:     "A pool is in bootstrap mode for over 72 hours",
			Description: "A pool has status.scalingMode: bootstrap for more than 72 hours. The pool has not received sufficient traffic for formula-driven convergence or the operator has not reviewed the initial sizing.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PoolBootstrapUnderprovisioned",
			Expr:        `lenny_pool_bootstrap_target_min_warm > 3 * lenny_pool_bootstrap_min_warm_override`,
			Severity:    SeverityWarning,
			Summary:     "Bootstrapping pool is significantly undersized",
			Description: "The PoolScalingController's formula-computed target_minWarm for a bootstrapping pool exceeds 3x the current bootstrapMinWarm override. The operator must increase bootstrapMinWarm manually before the controller can converge.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "WarmPoolIdleCostHigh",
			Expr:        `increase(lenny_warmpool_idle_pod_minutes[24h]) > scalar(lenny_warmpool_idle_cost_threshold)`,
			Severity:    SeverityWarning,
			Summary:     "Warm pool idle cost above the configured threshold",
			Description: "Cumulative lenny_warmpool_idle_pod_minutes for a pool exceeds the deployer-configured cost threshold over a rolling 24h window. The minWarm target or scale-to-zero schedule may need adjustment.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "SandboxClaimOrphanRateHigh",
			Expr:        `increase(lenny_orphaned_claims_total[15m]) > 10`,
			Severity:    SeverityWarning,
			Summary:     "SandboxClaim orphan rate high",
			Description: "lenny_orphaned_claims_total rate exceeds 10 orphaned claims in any 15-minute window. Indicates potential gateway instability — gateways creating claims but crashing before persisting sessions.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "OrphanTasksPerTenantHigh",
			Expr:        `lenny_orphan_tasks_active_per_tenant > 0.80 * scalar(lenny_max_orphan_tasks_per_tenant)`,
			Severity:    SeverityWarning,
			Summary:     "Per-tenant active orphan task count high",
			Description: "Per-tenant active orphan task count exceeds 80 percent of maxOrphanTasksPerTenant. A malicious or misbehaving orchestrator is accumulating detached orphan tasks; when the cap is reached the gateway falls back to cancel_all.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "EtcdQuotaNearLimit",
			Expr:        `etcd_debugging_mvcc_db_total_size_in_bytes / etcd_server_quota_backend_bytes > 0.80`,
			Severity:    SeverityWarning,
			Summary:     "etcd backend database above 80 percent of quota",
			Description: "etcd backend database size exceeds 80 percent of --quota-backend-bytes. Operators must defragment or increase quota before etcd enters alarm state and write operations are blocked.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "EtcdWriteLatencyHigh",
			Expr:        `histogram_quantile(0.99, sum by (le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m]))) > 0.025`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "etcd WAL fsync P99 latency high",
			Description: "P99 etcd WAL fsync latency exceeds 25 ms for more than 2 min. Sustained CRD status-update traffic approaches shared-etcd write limits; elevated latency is the leading indicator of saturation before quota exhaustion or EtcdUnavailable.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ControllerWorkQueueDepthHigh",
			Expr:        `lenny_controller_workqueue_depth > 0.50 * scalar(lenny_controller_workqueue_max_depth)`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Controller work queue depth high",
			Description: "Instantaneous lenny_controller_workqueue_depth exceeds 50 percent of the configured Work queue max depth for more than 2 min. The controller cannot process reconciliation events as fast as they arrive.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "WorkspaceSealStuck",
			Expr:        `increase(lenny_workspace_seal_duration_seconds_count{outcome="timeout"}[5m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Workspace seal-and-export stuck",
			Description: "A session's seal-and-export operation has been retrying for longer than maxWorkspaceSealDurationSeconds without success. Indicates sustained MinIO unavailability; pods held in draining beyond this deadline are forcibly terminated.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "InboxDrainFailure",
			Expr:        `increase(lenny_inbox_drain_failure_total[5m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "Inbox-to-DLQ drain failed",
			Description: "The atomic inbox-to-DLQ drain failed during a resume_pending state transition and the in-memory inbox messages for that session were permanently lost. This is the only acknowledged silent-data-loss path for inter-session messages.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DurableInboxRedisUnavailable",
			Expr:        `rate(lenny_inbox_redis_unavailable_total[5m]) > 0`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Durable-inbox Redis unavailable",
			Description: "Durable-inbox enqueue is failing because the coordination Redis instance is unreachable; senders with durableInbox: true sessions are receiving error delivery receipts and inter-session coordination messages are being rejected.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "FinalizerStuck",
			Expr:        `lenny_sandbox_finalizer_terminating_seconds > 300`,
			Severity:    SeverityWarning,
			Summary:     "A Sandbox pod finalizer is stuck",
			Description: "A Sandbox pod has been in Terminating state with the lenny.dev/session-cleanup finalizer present for more than 5 min. The warm pool controller could not confirm session checkpoint and safe finalizer removal.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DedicatedDNSDegraded",
			Expr:        `kube_deployment_status_replicas_ready{deployment="lenny-agent-dns"} > 0 and kube_deployment_status_replicas_ready{deployment="lenny-agent-dns"} < scalar(lenny_agent_dns_min_replicas)`,
			Severity:    SeverityWarning,
			Summary:     "Dedicated agent CoreDNS is degraded",
			Description: "The number of ready dedicated CoreDNS replicas for the agent namespace has dropped below the configured minimum but not to zero. DNS query logging, rate limiting, and response filtering are partially unavailable.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditSIEMDeliveryLag",
			Expr:        `lenny_audit_siem_delivery_lag_seconds > scalar(lenny_audit_siem_max_delivery_lag_seconds)`,
			Severity:    SeverityWarning,
			Summary:     "Audit SIEM delivery lag high",
			Description: "The lag between the latest committed audit event in Postgres and the latest SIEM-acknowledged event exceeds audit.siem.maxDeliveryLagSeconds. Events are at risk of being unacknowledged if the outbox forwarder crashes.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditPartitionDropBlocked",
			Expr:        `lenny_audit_partition_drop_blocked > 0`,
			Severity:    SeverityWarning,
			Summary:     "Audit partition drop blocked by SIEM backlog",
			Description: "An audit partition has exceeded its retention TTL but the SIEM forwarder has not consumed all events in the partition. The partition GC is holding the partition to prevent permanent data loss.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "WarmPoolBootstrapping",
			Expr:        `lenny_pool_warming_up == 1`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "A pool has not reached its minWarm target",
			Description: "A pool with minWarm > 0 has status.conditionPoolWarmingUp = True for more than warmupDeadlineSeconds. Possible causes: image pull errors, node resource pressure, or insufficient quota.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CRDSSAConflictStuck",
			Expr:        `increase(lenny_crd_ssa_conflict_total[5m]) > 10`,
			Severity:    SeverityWarning,
			Summary:     "Abnormal CRD Server-Side Apply ownership dispute",
			Description: "The SSA conflict counter for any single resource exceeds 10 in a 5-minute window. Indicates an abnormal ownership dispute between controllers on the same CRD field.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ExperimentTargetingCircuitOpen",
			Expr:        `lenny_experiment_targeting_circuit_open == 1`,
			For:         60 * time.Second,
			Severity:    SeverityWarning,
			Summary:     "Experiment targeting circuit breaker open",
			Description: "The experiment targeting circuit breaker has been open for more than 60s. Experiment assignments are falling back to the control variant.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "InterceptorMTLSHandshakeFailure",
			Expr:        `rate(lenny_interceptor_mtls_handshake_duration_seconds_count{result!="success"}[5m]) > 0`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Gateway is rejecting interceptor mTLS handshakes",
			Description: "The gateway is rejecting TLS handshakes to at least one registered in-cluster interceptor — SAN mismatch, missing or expired certificate, or generic TLS error. Handshake failures translate into INTERCEPTOR_TIMEOUT rejections in the policy phase.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PgAuditSinkDeliveryFailed",
			Expr:        `rate(lenny_pgaudit_sink_delivery_failed_total[5m]) > 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "pgaudit log shipper delivery failures",
			Description: "The pgaudit log shipper has been reporting delivery failures for more than audit.pgaudit.sinkFailureAlertMinutes. Audit events from the pgaudit source are not reaching the configured sink.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "OCSFTranslationBacklog",
			Expr:        `lenny_audit_ocsf_retry_pending_rows > 10`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "OCSF translation backlog",
			Description: "Either retry_pending OCSF rows have accumulated past audit.ocsf.alertThreshold for more than 5 min, or an audit row transitioned to ocsf_translation_state=dead_lettered in the last hour. The first is a transient regression; the second is an event-schema gap.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "AuditLockContention",
			Expr:        `histogram_quantile(0.99, sum by (le) (rate(lenny_audit_lock_acquire_seconds_bucket[5m]))) > 0.05`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Audit advisory-lock contention exceeds SLO",
			Description: "Either P99 audit advisory-lock acquisition exceeds the 50ms SLO for more than 5 min, or audit concurrency timeouts are sustained. Postgres I/O may be saturated or a stuck transaction is holding the lock.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "EventBusPublishDropped",
			Expr:        `rate(lenny_event_bus_publish_dropped_total[5m]) * 60 > scalar(lenny_event_bus_drop_alert_threshold)`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "EventBus publishes dropped after durable commit",
			Description: "CloudEvents publishes are failing after durable source-transaction commit at a rate above eventBus.dropAlertThreshold for more than 5 min. Subscribers must reconcile via the failed-publish admin query.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "EventBusPublishFinalFailure",
			Expr:        `increase(lenny_event_bus_retranscribe_attempts_total{outcome="failure"}[1h]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "An EventBus row exhausted all republish retries",
			Description: "An individual audit row's CloudEvents re-publish has exhausted all automated retries and will not be retried again until an operator invokes the manual republish endpoint.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "TokenRevocationPropagationLag",
			Expr:        `histogram_quantile(0.99, sum by (le) (rate(lenny_token_revocation_propagation_seconds_bucket{outcome="eventbus"}[5m]))) > 0.05`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Token revocation propagation lag exceeds SLO",
			Description: "Revoked tokens are taking longer than the 50ms P99 SLO to propagate to peer replicas via EventBus. Peer replicas are falling back to the Postgres check, still correct but at higher validation latency.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewayClockDrift",
			Expr:        `abs(lenny_time_drift_seconds) > 0.5`,
			Severity:    SeverityWarning,
			Summary:     "Gateway wall-clock drift detected",
			Description: "Absolute NTP drift exceeds 0.5s on a replica (warning); the stricter thresholds escalate to critical at 2.0s and cause the replica to self-remove from Service endpoints at 5.0s. NTP is misconfigured or the node clock source is unstable.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "GatewayRateLimitStorm",
			Expr:        `sum by (tenant_id) (rate(lenny_oauth_token_rate_limited_sampled_total[1m])) > 50`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Sustained rate-limit storm against the token endpoint",
			Description: "The sampled rate-limit counter is high, implying a sustained brute-force or misbehaving-automation burst against /v1/oauth/token. On-call should investigate the source subs and apply a tenant-level block or upstream-IdP lockout.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "DelegationLuaScriptLatencyHigh",
			Expr:        `histogram_quantile(0.99, sum by (le) (rate(lenny_redis_lua_script_duration_seconds_bucket{script="budget_reserve"}[5m]))) > 0.005`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Delegation budget Lua script latency high",
			Description: "P99 of the budget_reserve Lua script exceeds 5 ms for more than 2 min. Redis Lua serialization for delegation budget reservation has breached the LeaseStore SLO ceiling — lease renewal SETs in the blocking window are delayed.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CoordinatorHandoffSlow",
			Expr:        `histogram_quantile(0.95, sum by (le, pool) (rate(lenny_coordinator_handoff_duration_seconds_bucket[5m]))) > 5`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Coordinator handoff P95 above 5 seconds",
			Description: "P95 of lenny_coordinator_handoff_duration_seconds exceeds 5s for any pool over a 5-minute window. The 3-step coordinator handoff protocol is experiencing sustained delays — lease contention, network partition, or a high fence-retry rate.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "RuntimeUpgradeStuck",
			Expr:        `lenny_runtime_upgrade_state{state=~"expanding|draining|contracting"} == 1`,
			For:         10 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Runtime upgrade state machine stalled",
			Description: "lenny_runtime_upgrade_state for any pool has remained in a non-terminal state for longer than runtimeUpgrade.phaseTimeoutSeconds. The 6-state upgrade machine is stalled.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CircuitBreakerActive",
			Expr:        `lenny_circuit_breaker_open == 1`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "A circuit breaker has been open for over 5 minutes",
			Description: "Any global circuit breaker has been open for more than 5 minutes without a close action. Requests scoped to the breaker are being rejected; an operator should confirm the breaker is still warranted or close it.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "CircuitBreakerStale",
			Expr:        `lenny_circuit_breaker_cache_stale_seconds > 60`,
			Severity:    SeverityWarning,
			Summary:     "Circuit-breaker admission cache is stale",
			Description: "The AdmissionController's in-process circuit-breaker cache has not been refreshed from Redis within the 5s poll interval; admission decisions are being served against stale state.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LLMTranslationLatencyHigh",
			Expr:        `histogram_quantile(0.95, sum by (le, pool, provider, direction) (rate(lenny_gateway_llm_translation_duration_seconds_bucket[5m]))) > 0.1`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "LLM native-translator P95 latency high",
			Description: "P95 native-translator CPU time exceeds 100 ms on a request or response leg. Possible causes include translator algorithmic regression, unexpected payload size, or schema-validation drift under load.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LLMTranslationSchemaDrift",
			Expr:        `rate(lenny_gateway_llm_translation_errors_total{error_type="schema_mismatch"}[5m]) > 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "LLM translator schema drift",
			Description: "Either the translator is receiving incoming pod requests with unexpected shapes or an upstream provider's response schema has drifted outside the translator's validator. Steady-state value is zero under healthy operation.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "ExperimentIsolationRejections",
			Expr:        `rate(lenny_experiment_isolation_rejections_total[5m]) > 0`,
			For:         2 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "ExperimentRouter is failing closed on isolation-monotonicity checks",
			Description: "A variant pool's isolationProfile is weaker than an enrolled session's minIsolationProfile, and affected sessions are rejected with VARIANT_ISOLATION_UNAVAILABLE. Steady-state value is zero; a sustained rate means the §10.7 admission-time monotonicity check was bypassed.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PlatformUpgradeAvailable",
			Expr:        `lenny_platform_upgrade_available > 0`,
			Severity:    SeverityInfo,
			Summary:     "A new Lenny platform release is available",
			Description: "/v1/admin/platform/upgrade-check reports a new Lenny release. Informational; used to drive agent-initiated upgrade workflows.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PlatformUpgradeStuck",
			Expr:        `lenny_platform_upgrade_phase > 0`,
			For:         1 * time.Hour,
			Severity:    SeverityWarning,
			Summary:     "Platform upgrade state machine stalled",
			Description: "The lenny_platform_upgrade_phase gauge has remained in a non-terminal phase for more than 1h. The upgrade state machine has stalled — operator must resume, roll back, or force-complete.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "PlatformVersionDrift",
			Expr:        `lenny_platform_version_drift > 0`,
			For:         5 * time.Minute,
			Severity:    SeverityWarning,
			Summary:     "Platform component version drift",
			Description: "A component's reported version differs from the value compiled into the active lenny-ops binary for more than 5 min.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "BackupOverdue",
			Expr:        `time() - lenny_backup_last_successful_timestamp{type="full"} > 172800`,
			Severity:    SeverityWarning,
			Summary:     "A full backup is overdue",
			Description: "A full backup has not completed within the expected 48h window.",
			RunbookURL:  runbook("backup-overdue"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "BackupFailed",
			Expr:        `increase(lenny_backup_total{status="failed"}[1h]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "A backup Job failed",
			Description: "A backup Job terminated with failure; see ops_backups.lastError for the cause.",
			RunbookURL:  runbook("backup-failed"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "BackupStorageHigh",
			Expr:        `lenny_backup_storage_used_bytes / lenny_backup_storage_quota_bytes > 0.80`,
			Severity:    SeverityWarning,
			Summary:     "Backup object storage above 80 percent of quota",
			Description: "Backup object storage utilization exceeds 80 percent of the provisioned quota. Retention policy may need tightening or the backup bucket may need resizing.",
			RunbookURL:  runbook("backup-storage-high"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "MinIOArtifactReplicationLagHigh",
			Expr:        `lenny_minio_replication_lag_seconds > 900`,
			Severity:    SeverityWarning,
			Summary:     "ArtifactStore replication lag exceeds 1x RPO",
			Description: "lenny_minio_replication_lag_seconds exceeds minio.artifactBackup.replicationLagRpoSeconds (1x RPO). The ArtifactStore bucket is falling behind its replication target; artifacts written in the lag window are at risk in a full-site disaster.",
			RunbookURL:  runbook("minio-replication-lag"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "MinIOArtifactReplicationFailed",
			Expr:        `rate(lenny_minio_replication_failed_total[5m]) > 0`,
			Severity:    SeverityWarning,
			Summary:     "ArtifactStore object-level replication failures",
			Description: "Object-level replication failures (permission, network, destination bucket unavailable or full) over 5 min. The ArtifactStore backup posture is broken until the failure is resolved.",
			RunbookURL:  runbook("minio-replication-lag"),
			SpecRef:     "§16.5",
		},
		{
			Name:        "CircuitBreakerOpen",
			Expr:        `lenny_circuit_breaker_open == 1`,
			Severity:    SeverityWarning,
			Summary:     "An operator-managed circuit breaker is open",
			Description: "Any operator-managed circuit breaker reports state open. Agent-oriented counterpart of CircuitBreakerActive with a lower firing threshold for AI triage.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "LenniOpsSelfHealthDegraded",
			Expr:        `lenny_ops_self_health_status != 1`,
			Severity:    SeverityWarning,
			Summary:     "lenny-ops self-health degraded",
			Description: "lenny-ops itself is degraded — dependent operability endpoints may return stale or partial data.",
			SpecRef:     "§16.5",
		},
		{
			Name:        "OperationStalled",
			Expr:        `lenny_ops_operations_stalled > 0`,
			Severity:    SeverityWarning,
			Summary:     "An in-flight operation has stalled",
			Description: "One or more in-flight operations exceeded their expected inter-step cadence (stalledForSeconds > 0) per the §25.2 Progress Envelope. The gauge clears when every operation advances within its cadence. Investigate via GET /v1/admin/operations/{id}.",
			SpecRef:     "§16.5",
		},
	}
}

// burnRateAlerts is the §16.5 multi-window SLO error-budget burn-rate
// table. Each SLO in the canonical SLODefinitions catalog yields a
// fast-window critical rule (1h at burnRateFastMultiplier, default 14x)
// and a slow-window warning rule (6h at burnRateSlowMultiplier, default
// 3x). §16.5 requires both windows to be present simultaneously for a
// page, so each SLO is two distinct rules in the rendered manifest. The
// SLODefinitions catalog is the single source the §16.10 OpenSLO export
// also derives from (slo.go), so the rendered OpenSLO AlertPolicy
// conditions stay identical to these rules.
func burnRateAlerts() []Rule {
	defs := SLODefinitions()
	rs := make([]Rule, 0, len(defs)*2)
	for _, d := range defs {
		rs = append(rs, Rule{
			Name:        d.AlertName,
			Expr:        fmt.Sprintf(`%s > %s`, d.BurnRateExpr, burnRateFastMultiplierThreshold),
			For:         burnRateFastWindow,
			Severity:    SeverityCritical,
			Summary:     "Fast-window error-budget burn for " + d.Objective,
			Description: "Fast-window (1h) multi-window burn-rate alert. Fires when the SLO error budget is consumed at more than slo.burnRate.fastMultiplier (default 14) times the sustainable rate over a 1-hour window. The fast-window alert pages on-call.",
			RunbookURL:  runbook(d.RunbookSlug),
			SLO:         d.Objective,
			SpecRef:     "§16.5",
		})
		rs = append(rs, Rule{
			Name:        d.AlertName + "Slow",
			Expr:        fmt.Sprintf(`%s > %s`, d.BurnRateExpr, burnRateSlowMultiplierThreshold),
			For:         burnRateSlowWindow,
			Severity:    SeverityWarning,
			Summary:     "Slow-window error-budget burn for " + d.Objective,
			Description: "Slow-window (6h) multi-window burn-rate alert. Fires when the SLO error budget is consumed at more than slo.burnRate.slowMultiplier (default 3) times the sustainable rate over a 6-hour window, catching slow-burn degradation that threshold-only alerts would miss.",
			SLO:         d.Objective,
			SpecRef:     "§16.5",
		})
	}
	return rs
}
