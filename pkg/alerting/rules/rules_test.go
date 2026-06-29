// SPDX-License-Identifier: MIT

package rules

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSeverityIsValid(t *testing.T) {
	cases := map[Severity]bool{
		SeverityCritical:     true,
		SeverityWarning:      true,
		Severity(""):         false,
		Severity("page"):     false,
		Severity("CRITICAL"): false,
	}
	for s, want := range cases {
		if got := s.IsValid(); got != want {
			t.Errorf("Severity(%q).IsValid() = %v, want %v", s, got, want)
		}
	}
}

func TestRuleValidateAcceptsWellFormed(t *testing.T) {
	r := Rule{
		Name:     "Example",
		Expr:     `up == 0`,
		For:      30 * time.Second,
		Severity: SeverityWarning,
		Summary:  "test alert",
		SpecRef:  "§16.5",
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate should accept well-formed rule, got %v", err)
	}
}

func TestRuleValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"missing name", Rule{Expr: "up == 0", Severity: SeverityWarning, Summary: "s"}, "Name is required"},
		{"missing expr", Rule{Name: "X", Severity: SeverityWarning, Summary: "s"}, "Expr is required"},
		{"bad severity", Rule{Name: "X", Expr: "up == 0", Severity: "bogus", Summary: "s"}, "Severity"},
		{"missing summary", Rule{Name: "X", Expr: "up == 0", Severity: SeverityWarning}, "Summary is required"},
		{"negative for", Rule{Name: "X", Expr: "up == 0", For: -1, Severity: SeverityWarning, Summary: "s"}, "For must be non-negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.rule.Validate()
			if err == nil {
				t.Fatal("Validate should have rejected the rule")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error should mention %q, got %v", c.want, err)
			}
		})
	}
}

func TestRuleValidateRejectsBadPromQL(t *testing.T) {
	r := Rule{
		Name:     "X",
		Expr:     "up ===",
		Severity: SeverityWarning,
		Summary:  "s",
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate should reject malformed PromQL")
	}
	if !strings.Contains(err.Error(), "PromQL") {
		t.Errorf("error should mention PromQL, got %v", err)
	}
}

func TestRuleValidateCriticalRequiresRunbook(t *testing.T) {
	r := Rule{
		Name:     "X",
		Expr:     "up == 0",
		Severity: SeverityCritical,
		Summary:  "s",
	}
	err := r.Validate()
	if err == nil {
		t.Fatal("critical alert without runbook should be rejected")
	}
	if !strings.Contains(err.Error(), "RunbookURL") {
		t.Errorf("error should mention RunbookURL, got %v", err)
	}
}

func TestValidationErrorIsRetrievable(t *testing.T) {
	r := Rule{Name: "X"}
	err := r.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if ve.Rule != "X" {
		t.Errorf("ValidationError.Rule: want X, got %q", ve.Rule)
	}
	if len(ve.Violations) == 0 {
		t.Errorf("ValidationError should list violations")
	}
}

func TestCatalogValidates(t *testing.T) {
	c := Catalog()
	if len(c) == 0 {
		t.Fatal("Catalog should not be empty")
	}
	// A rule's identity is its Name plus its static disambiguating
	// labels: the §17.2 AdmissionPlaneFeatureFlagDowngrade alert is
	// rendered as four rules that share one Name but carry distinct
	// (flag_name, expected_webhook_name) labels, so keying uniqueness on
	// Name alone would falsely flag that spec-mandated decomposition while
	// still catching accidental copy-paste of an identical rule.
	identities := map[string]bool{}
	for _, r := range c {
		if err := r.Validate(); err != nil {
			t.Errorf("Catalog rule %q fails Validate: %v", r.Name, err)
		}
		id := r.Name + "\x00" + r.Labels["flag_name"] + "\x00" + r.Labels["expected_webhook_name"]
		if identities[id] {
			t.Errorf("duplicate rule identity %q in Catalog (name + disambiguating labels)", id)
		}
		identities[id] = true
	}
}

// TestAdmissionPlaneFeatureFlagDowngradePairs verifies the §16.5 / §17.2
// AdmissionPlaneFeatureFlagDowngrade alert is the union of one rule per
// (flag, webhook) pair in the §17.2 Feature-gated chart inventory: four
// pairs sharing the alert Name, each with the static flag_name /
// expected_webhook_name labels and an expression matching the
// kube-state-metrics slug label the phase-stamp ConfigMap renders.
// F-17.2.6.
func TestAdmissionPlaneFeatureFlagDowngradePairs_spec_17_2(t *testing.T) {
	type pair struct {
		flag     string
		labelKey string
	}
	want := map[string]pair{
		"lenny-direct-mode-isolation":    {"features.llmProxy", "label_lenny_dev_flag_llm_proxy_enabled"},
		"lenny-drain-readiness":          {"features.drainReadiness", "label_lenny_dev_flag_drain_readiness_enabled"},
		"lenny-data-residency-validator": {"features.compliance", "label_lenny_dev_flag_compliance_enabled"},
		"lenny-t4-node-isolation":        {"features.compliance", "label_lenny_dev_flag_compliance_enabled"},
	}
	got := map[string]bool{}
	for _, r := range Catalog() {
		if r.Name != "AdmissionPlaneFeatureFlagDowngrade" {
			continue
		}
		webhook := r.Labels["expected_webhook_name"]
		exp, ok := want[webhook]
		if !ok {
			t.Errorf("unexpected downgrade pair for webhook %q", webhook)
			continue
		}
		got[webhook] = true
		if r.Labels["flag_name"] != exp.flag {
			t.Errorf("pair %q flag_name = %q, want %q", webhook, r.Labels["flag_name"], exp.flag)
		}
		if r.Severity != SeverityWarning {
			t.Errorf("pair %q severity = %q, want warning", webhook, r.Severity)
		}
		if r.For != 2*time.Minute {
			t.Errorf("pair %q For = %v, want 2m", webhook, r.For)
		}
		if !strings.Contains(r.Expr, exp.labelKey+`="true"`) {
			t.Errorf("pair %q expr missing flag label %q: %s", webhook, exp.labelKey, r.Expr)
		}
		if !strings.Contains(r.Expr, `name="`+webhook+`"`) {
			t.Errorf("pair %q expr missing webhook matcher: %s", webhook, r.Expr)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("found %d downgrade pairs, want %d", len(got), len(want))
	}
}

// TestAdmissionPlaneDowngradeLabelsRender verifies the per-pair static
// labels survive into the rendered PrometheusRule alongside the
// mandatory severity label. F-17.2.6.
func TestAdmissionPlaneDowngradeLabelsRender_spec_17_2(t *testing.T) {
	groups, err := RenderRuleGroupsBySeverity("lenny", Catalog())
	if err != nil {
		t.Fatalf("RenderRuleGroupsBySeverity: %v", err)
	}
	rendered := string(groups)
	for _, frag := range []string{
		`expected_webhook_name: lenny-direct-mode-isolation`,
		`expected_webhook_name: lenny-t4-node-isolation`,
		`flag_name: features.compliance`,
	} {
		if !strings.Contains(rendered, frag) {
			t.Errorf("rendered rule groups missing %q", frag)
		}
	}
}

func TestCatalogCoversCanonicalAlerts(t *testing.T) {
	want := []string{
		"WarmPoolExhausted",
		"PostgresReplicationLagHigh",
		"CredentialPoolLow",
		"ExperimentIsolationRejections",
		"StorageQuotaHigh",
		"CircuitBreakerActive",
		"CircuitBreakerStale",
	}
	got := map[string]bool{}
	for _, r := range Catalog() {
		got[r.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("Catalog missing canonical rule %q", w)
		}
	}
}

// spec165CriticalAlerts is the §16.5 "Critical alerts (page)" table —
// every first-column alert name, transcribed in spec order.
var spec165CriticalAlerts = []string{
	"WarmPoolExhausted", "PostgresReplicationLagHigh", "GatewayNoHealthyReplicas",
	"SessionStoreUnavailable", "RedisUnavailable", "CheckpointStorageUnavailable",
	"MinIOUnavailable", "T4KmsKeyUnusable", "EtcdUnavailable", "CredentialPoolExhausted",
	"CredentialCompromised", "TokenServiceUnavailable", "ControllerLeaderElectionFailed",
	"DedicatedDNSUnavailable", "CosignWebhookUnavailable", "AuditGrantDrift",
	"NetworkPolicyCIDRDrift", "AdmissionWebhookUnavailable", "SandboxClaimGuardUnavailable",
	"DualStoreUnavailable", "DataResidencyWebhookUnavailable", "DataResidencyViolationAttempt",
	"ArtifactReplicationResidencyViolation", "LegalHoldEscrowResidencyViolation",
	"PlatformAuditResidencyViolation", "PgBouncerAllReplicasDown", "SessionEvictionTotalLoss",
	"DelegationBudgetKeysExpired", "BillingStreamEntryAgeHigh", "OTLPPlaintextEgressDetected",
	"OpsAdminAPIPlaintextDetected", "ElicitationContentTamperDetected",
}

// spec165WarningAlerts is the §16.5 "Warning alerts" table — every
// first-column alert name. The table mixes a few Critical-severity
// rows in among the warnings (AuditRedactionReceiptMissing,
// LLMUpstreamEgressAnomaly, TokenStoreUnavailable, BackupReconcileBlocked,
// MinIOArtifactReplicationLagCritical, LenniOpsLockSplitBrainDetected);
// the catalog files those under criticalAlerts() but the name still
// belongs to the §16.5 surface this test enumerates.
var spec165WarningAlerts = []string{
	"WarmPoolLow", "RedisMemoryHigh", "CredentialPoolLow", "CredentialProactiveRenewalExhausted",
	"OutstandingInflightAtRotationCeiling", "GatewayActiveStreamsHigh", "GatewaySessionBudgetNearExhaustion",
	"PDBBlockedEvictions", "Tier3GCPressureHigh", "ArtifactGCBacklog", "CheckpointStale",
	"RateLimitDegraded", "QuotaFailOpenCumulativeThreshold", "QuotaFailOpenUserFractionInoperative",
	"CertExpiryImminent", "ElicitationBacklogHigh", "ElicitationContentIntegrityPermissiveTamper",
	"ElicitationContentIntegrityWeakened", "DelegationBudgetNearExhaustion", "DelegationBudgetIrrecoverable",
	"CycleDetectionModeUnsafe", "CycleDetectionWarnModeBlocking", "AuditSIEMNotConfigured",
	"AuditChainGap", "AuditRedactionReceiptMissing", "CheckpointStorageHigh", "StorageQuotaHigh",
	"CheckpointDurationHigh", "PreStopCapFallbackRateHigh", "PodClaimQueueSaturated",
	"PodStateMirrorStale", "KMSSigningUnavailable", "GatewaySubsystemCircuitOpen",
	"GatewayQueueDepthHigh", "GatewayLatencyHigh", "PoolConfigDrift", "PoolConfigValidatorUnavailable",
	"PoolScalingAdmissionStuck", "LabelImmutabilityWebhookUnavailable", "DirectModeIsolationWebhookUnavailable",
	"T4NodeIsolationWebhookUnavailable", "DrainReadinessWebhookUnavailable", "CrdConversionWebhookUnavailable",
	"EphemeralContainerCredGuardUnavailable", "AdmissionPlaneFeatureFlagDowngrade",
	"WarmPoolReplenishmentSlow", "WarmPoolReplenishmentFailing", "PoolScaleoutBlockedByQuota",
	"SDKConnectTimeout", "ErasureJobFailed", "ErasureJobOverdue", "MemoryStoreGrowthHigh",
	"MemoryStoreErasureDurationHigh", "LegalHoldOverrideUsed", "LegalHoldOverrideUsedTenant",
	"CompliancePostureDecommissioned", "LegalHoldCheckpointAccumulationProjectedBreach",
	"TenantDeletionOverdue", "KmsKeyDeletionFailed", "BillingCorrectionRateHigh",
	"BillingStreamBackpressure", "BillingCorrectionApprovalBacklog", "BillingWriteAheadBufferHigh",
	"AuditRetentionLow", "PostgresWriteSaturation", "ScatterGatherSlowQuery", "PostgresWriteBurstIops",
	"PgBouncerPoolSaturated", "PoolBootstrapMode", "PoolBootstrapUnderprovisioned",
	"WarmPoolIdleCostHigh", "SandboxClaimOrphanRateHigh", "OrphanTasksPerTenantHigh",
	"EtcdQuotaNearLimit", "EtcdWriteLatencyHigh", "ControllerWorkQueueDepthHigh", "WorkspaceSealStuck",
	"InboxDrainFailure", "DurableInboxRedisUnavailable", "FinalizerStuck", "DedicatedDNSDegraded",
	"AuditSIEMDeliveryLag", "AuditPartitionDropBlocked", "WarmPoolBootstrapping", "PoolSecurityDegraded",
	"CRDSSAConflictStuck",
	"ExperimentTargetingCircuitOpen", "LLMUpstreamEgressAnomaly", "InterceptorMTLSHandshakeFailure",
	"PgAuditSinkDeliveryFailed", "OCSFTranslationBacklog", "AuditLockContention",
	"EventBusPublishDropped", "EventBusPublishFinalFailure", "TokenStoreUnavailable",
	"TokenRevocationPropagationLag", "GatewayClockDrift", "GatewayRateLimitStorm",
	"DelegationLuaScriptLatencyHigh", "CoordinatorHandoffSlow", "RuntimeUpgradeStuck",
	"CircuitBreakerActive", "CircuitBreakerStale", "LLMTranslationLatencyHigh",
	"LLMTranslationSchemaDrift", "ExperimentIsolationRejections", "PlatformUpgradeAvailable",
	"PlatformUpgradeStuck", "PlatformVersionDrift", "BackupOverdue", "BackupFailed",
	"BackupStorageHigh", "BackupReconcileBlocked", "MinIOArtifactReplicationLagHigh",
	"MinIOArtifactReplicationLagCritical", "MinIOArtifactReplicationFailed", "CircuitBreakerOpen",
	"LenniOpsSelfHealthDegraded", "LenniOpsLockSplitBrainDetected", "OperationStalled",
	"OpsClockSkewExceeded",
}

// spec165BurnRateSLOs is the §16.5 "SLO error-budget burn-rate alerts"
// table. Each name yields a fast-window critical rule and a slow-window
// warning rule.
var spec165BurnRateSLOs = []string{
	"SessionCreationSuccessRateBurnRate", "SessionCreationLatencyBurnRate",
	"SessionAvailabilityBurnRate", "GatewayAvailabilityBurnRate", "StartupLatencyBurnRate",
	"StartupLatencyGVisorBurnRate", "TTFTBurnRate", "CheckpointDurationBurnRate",
}

// TestCatalogIsCompleteAgainstSpec165 asserts the catalog contains
// every alert §16.5 enumerates: the critical table, the warning table,
// and both windows of every multi-window burn-rate SLO. This is the
// completion-gate check that the Catalog is the §16.5 surface in code,
// not a representative sample.
func TestCatalogIsCompleteAgainstSpec165(t *testing.T) {
	got := map[string]bool{}
	for _, r := range Catalog() {
		got[r.Name] = true
	}
	for _, name := range spec165CriticalAlerts {
		if !got[name] {
			t.Errorf("§16.5 critical alert %q is missing from Catalog", name)
		}
	}
	for _, name := range spec165WarningAlerts {
		if !got[name] {
			t.Errorf("§16.5 warning alert %q is missing from Catalog", name)
		}
	}
	for _, name := range spec165BurnRateSLOs {
		if !got[name] {
			t.Errorf("§16.5 burn-rate SLO %q is missing its fast-window rule", name)
		}
		if !got[name+"Slow"] {
			t.Errorf("§16.5 burn-rate SLO %q is missing its slow-window rule (%sSlow)", name, name)
		}
	}
}

// TestCatalogHasNoUnspecifiedAlerts asserts the catalog does not invent
// alerts §16.5 does not list. Every Catalog rule must be either a named
// §16.5 alert or the slow-window sibling of a §16.5 burn-rate SLO.
func TestCatalogHasNoUnspecifiedAlerts(t *testing.T) {
	allowed := map[string]bool{}
	for _, n := range spec165CriticalAlerts {
		allowed[n] = true
	}
	for _, n := range spec165WarningAlerts {
		allowed[n] = true
	}
	for _, n := range spec165BurnRateSLOs {
		allowed[n] = true
		allowed[n+"Slow"] = true
	}
	for _, r := range Catalog() {
		if !allowed[r.Name] {
			t.Errorf("Catalog rule %q is not a §16.5 alert — the catalog must transcribe §16.5, not invent rules", r.Name)
		}
	}
}

// TestPostgresWriteBurstIopsCalibration asserts the §16.5
// PostgresWriteBurstIops alert encodes the "> 3 in 10 minutes" trigger
// the spec stipulates. A single burst is not actionable; the alert
// must require repetition across a 10-minute window before paging
// operators. The expression is a count_over_time over a 1-minute
// resolution subquery of the burst-exceedance indicator; the test
// asserts the expression carries the count-over-time tally, the 10m
// window, the 1m resolution, and the > 3 repetition threshold.
func TestPostgresWriteBurstIopsCalibration(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "PostgresWriteBurstIops" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("PostgresWriteBurstIops missing from Catalog")
	}
	if got.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", got.Severity)
	}
	wantFragments := []string{
		"count_over_time",
		"lenny_postgres_write_burst_iops",
		"lenny_postgres_write_burst_ceiling_iops",
		"[10m:1m]",
		"> 3",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(got.Expr, frag) {
			t.Errorf("expression %q is missing required fragment %q "+
				"(§16.5 burst-exceedance count_over_time calibration)", got.Expr, frag)
		}
	}
}

// TestPoolSecurityDegradedRule pins the §16.5 / §4.7 PoolSecurityDegraded
// warning alert to the WarmPoolController-published gauge and to its
// nonce-only-mode runbook. The rule is defined on
// lenny_pool_security_degraded == 1 rather than on the adapter counter
// because no default scrape surface reaches agent pods (§16.9), so a
// counter rule would evaluate an empty vector. The runbook slug must
// resolve to docs/runbooks/nonce-only-mode.md (the tier-11 docs gate
// cross-checks this).
//
// spec: §4.7 (escalation requirement); §16.5 (PoolSecurityDegraded);
// §17.7 (runbook). F-5.3.14.
// TestOpsClockSkewExceededRule_spec_16_5 pins the F-SH-1
// OpsClockSkewExceeded warning alert: it selects the postgres-redis pair
// on the lenny_ops_clock_skew_seconds gauge, fires above the 10s NTP
// tolerance, and resolves to the ops-clock-skew runbook. Pre-fix the
// alert did not exist, so the gauge a producer now publishes had no rule
// to read it.
//
// spec: §16.5 (OpsClockSkewExceeded warning-alert row); §25.4 line 2280
// (Postgres-Redis skew >10s alert).
func TestOpsClockSkewExceededRule_spec_16_5(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "OpsClockSkewExceeded" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("OpsClockSkewExceeded missing from Catalog")
	}
	if got.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", got.Severity)
	}
	if got.Expr != `lenny_ops_clock_skew_seconds{pair="postgres-redis"} > 10` {
		t.Errorf("expr = %q, want the postgres-redis pair gauge above the 10s tolerance", got.Expr)
	}
	if !strings.Contains(got.Expr, `pair="postgres-redis"`) {
		t.Errorf("expr %q must select the postgres-redis pair label", got.Expr)
	}
	if !strings.Contains(got.Expr, "> 10") {
		t.Errorf("expr %q must fire above the 10s tolerance per §25.4 line 2280", got.Expr)
	}
	if got.RunbookSlug() != "ops-clock-skew" {
		t.Errorf("runbook slug = %q, want ops-clock-skew", got.RunbookSlug())
	}
	if err := got.Validate(); err != nil {
		t.Errorf("OpsClockSkewExceeded does not validate: %v", err)
	}
}

func TestPoolSecurityDegradedRule(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "PoolSecurityDegraded" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("PoolSecurityDegraded missing from Catalog")
	}
	if got.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", got.Severity)
	}
	if got.Expr != "lenny_pool_security_degraded == 1" {
		t.Errorf("expr = %q, want the WarmPoolController gauge lenny_pool_security_degraded == 1", got.Expr)
	}
	if strings.Contains(got.Expr, "lenny_adapter_sopeercred_disabled_total") {
		t.Errorf("expr %q must not key off the agent-pod adapter counter; no default scrape surface reaches agent pods (§16.9)", got.Expr)
	}
	if got.RunbookSlug() != "nonce-only-mode" {
		t.Errorf("runbook slug = %q, want nonce-only-mode", got.RunbookSlug())
	}
	if err := got.Validate(); err != nil {
		t.Errorf("PoolSecurityDegraded does not validate: %v", err)
	}
}

// spec: §12.3 line 47 / §16.5 line 510 — PgBouncerPoolSaturated must
// evaluate the pgbouncer_exporter sidecar's native client-wait stat
// (pgbouncer_pools_client_maxwait_seconds). The earlier
// lenny_pgbouncer_client_waiting_seconds metric was never emitted by any
// code or exporter, so the alert silently matched no series. F-12.3.11.
func TestPgBouncerPoolSaturatedUsesExporterMetric_spec_12_3_F_12_3_11(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "PgBouncerPoolSaturated" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("PgBouncerPoolSaturated missing from Catalog")
	}
	if !strings.Contains(got.Expr, "pgbouncer_pools_client_maxwait_seconds") {
		t.Errorf("expression %q must reference the exporter metric pgbouncer_pools_client_maxwait_seconds", got.Expr)
	}
	if strings.Contains(got.Expr, "lenny_pgbouncer_client_waiting_seconds") {
		t.Errorf("expression %q still references the never-emitted lenny_pgbouncer_client_waiting_seconds metric", got.Expr)
	}
}

// spec: §6.3 line 356, §16.5 line 637 — TTFTBurnRate must reference
// the lenny_session_time_to_first_token_seconds histogram directly
// via the le="10" bucket (the 10s TTFT SLO). The earlier
// lenny_session_time_to_first_token_slow_ratio recording rule was
// never shipped and the alert silently matched no series.
func TestTTFTBurnRateUsesInlineHistogramExpression_spec_6_3_F_6_3_3(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "TTFTBurnRate" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatalf("TTFTBurnRate not found in Catalog")
	}
	wantFragments := []string{
		"lenny_session_time_to_first_token_seconds_bucket",
		`le="10"`,
		"lenny_session_time_to_first_token_seconds_count",
		"/ 0.05",
		// §16.5 line 640 — the fast-window page threshold is the
		// operator-tunable multiplier scalar (default 14). F-16.5.3.
		"> scalar(lenny_slo_burn_rate_fast_multiplier or vector(14))",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(got.Expr, frag) {
			t.Errorf("TTFTBurnRate expression %q is missing required fragment %q", got.Expr, frag)
		}
	}
	notWant := "lenny_session_time_to_first_token_slow_ratio"
	if strings.Contains(got.Expr, notWant) {
		t.Errorf("TTFTBurnRate still references the never-shipped recording rule %q", notWant)
	}
}

// TestBurnRateRulesAreDualWindow asserts each burn-rate SLO is rendered
// as a fast-window critical rule and a slow-window warning rule, per
// the §16.5 multi-window burn-rate requirement.
func TestBurnRateRulesAreDualWindow(t *testing.T) {
	bySeverity := map[string]Severity{}
	byFor := map[string]time.Duration{}
	for _, r := range Catalog() {
		bySeverity[r.Name] = r.Severity
		byFor[r.Name] = r.For
	}
	for _, name := range spec165BurnRateSLOs {
		if bySeverity[name] != SeverityCritical {
			t.Errorf("%s (fast window) severity = %q, want critical", name, bySeverity[name])
		}
		if byFor[name] != time.Hour {
			t.Errorf("%s (fast window) for = %v, want 1h", name, byFor[name])
		}
		slow := name + "Slow"
		if bySeverity[slow] != SeverityWarning {
			t.Errorf("%s (slow window) severity = %q, want warning", slow, bySeverity[slow])
		}
		if byFor[slow] != 6*time.Hour {
			t.Errorf("%s (slow window) for = %v, want 6h", slow, byFor[slow])
		}
	}
}

// TestWarmPoolReplenishmentSlowDerivesFromPerPoolBaseline asserts the
// alert compares P95 startup against 2× the per-pool
// lenny_pool_warmup_seconds_baseline gauge the PoolScalingController
// mirrors, rather than the prior fixed 60s threshold. spec: §16.5 line
// 488.
func TestWarmPoolReplenishmentSlowDerivesFromPerPoolBaseline_spec_16_5_488(t *testing.T) {
	var got Rule
	for _, r := range Catalog() {
		if r.Name == "WarmPoolReplenishmentSlow" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("WarmPoolReplenishmentSlow not found in Catalog")
	}
	wantFragments := []string{
		"lenny_warmpool_pod_startup_duration_seconds_bucket",
		"sum by (le, pool)",
		"> 2 * lenny_pool_warmup_seconds_baseline",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(got.Expr, frag) {
			t.Errorf("WarmPoolReplenishmentSlow expression %q is missing required fragment %q", got.Expr, frag)
		}
	}
	// The fixed 2×30s default threshold must be gone — an explicitly
	// baselined pool no longer alerts at the wrong multiple.
	if strings.Contains(got.Expr, "> 60") {
		t.Errorf("WarmPoolReplenishmentSlow still uses the fixed > 60 threshold: %q", got.Expr)
	}
}
