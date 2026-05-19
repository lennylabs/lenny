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
	names := map[string]bool{}
	for _, r := range c {
		if err := r.Validate(); err != nil {
			t.Errorf("Catalog rule %q fails Validate: %v", r.Name, err)
		}
		if names[r.Name] {
			t.Errorf("duplicate rule name %q in Catalog", r.Name)
		}
		names[r.Name] = true
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
	"AuditSIEMDeliveryLag", "AuditPartitionDropBlocked", "WarmPoolBootstrapping", "CRDSSAConflictStuck",
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
