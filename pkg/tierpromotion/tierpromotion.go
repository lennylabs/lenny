// SPDX-License-Identifier: MIT

// Package tierpromotion implements the §17.8.3 tier-promotion gate. The
// gate validates the operator's deployment against the prerequisites a
// promotion target enumerates: chart-values alignment, replica floors,
// persistent storage on data backends, secret encryption at rest, audit
// retention, and the §13.1/§13.2 admission-webhook posture.
//
// Each check is a pure function over the inputs the gate collects from
// the cluster and the chart values, mirroring the pkg/preflight pattern.
// The lenny-tier-promote binary is a thin shim that gathers the inputs
// and calls Validate. The package can also be embedded in lenny-ops for
// the operator surface described in §25.4.
package tierpromotion

import (
	"context"
	"fmt"
	"sort"
)

// Tier names a §17.8.4 capacity-tier preset. Tier 1 is the smallest
// published envelope; Tier 3 is the largest. The values match the
// `capacityPlanning.tier` field of the preset files
// (charts/lenny/presets/values-tierN.yaml).
type Tier string

const (
	Tier1 Tier = "tier1"
	Tier2 Tier = "tier2"
	Tier3 Tier = "tier3"
)

// AllTiers returns the published tiers in promotion order.
func AllTiers() []Tier { return []Tier{Tier1, Tier2, Tier3} }

// IsValid reports whether t is one of the published tiers.
func (t Tier) IsValid() bool {
	switch t {
	case Tier1, Tier2, Tier3:
		return true
	}
	return false
}

// Rank returns the tier's ordinal position (1, 2, 3). An invalid tier
// returns 0.
func (t Tier) Rank() int {
	switch t {
	case Tier1:
		return 1
	case Tier2:
		return 2
	case Tier3:
		return 3
	}
	return 0
}

// Status is the outcome of one tier-promotion check.
type Status string

const (
	// StatusPass marks a check whose inputs satisfy the §17.8.3 gate.
	StatusPass Status = "PASS"
	// StatusFail marks a check whose inputs violate the gate. The
	// CheckResult.Detail names the violation.
	StatusFail Status = "FAIL"
	// StatusSkip marks a check that does not apply to this transition,
	// e.g., the persistent-storage check on a Tier 1 → Tier 1 no-op.
	StatusSkip Status = "SKIP"
)

// CheckResult pairs a §17.8.3 check name with its status and a
// diagnostic detail. Detail is empty when the check passes; it names
// the specific failure or the reason a check was skipped otherwise.
type CheckResult struct {
	// Name identifies the check (e.g., "chart-values-diff").
	Name string
	// Status is PASS, FAIL, or SKIP.
	Status Status
	// Detail is the human-readable explanation.
	Detail string
}

// Report is the ordered list of CheckResults a Validate call returns.
// The slice preserves the order checks ran in so the operator sees the
// same ordering across runs.
type Report []CheckResult

// Passed reports whether every non-skip check in the report passed. A
// report with no checks returns true.
func (r Report) Passed() bool {
	for _, c := range r {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// Failures returns the failed checks in report order.
func (r Report) Failures() []CheckResult {
	var out []CheckResult
	for _, c := range r {
		if c.Status == StatusFail {
			out = append(out, c)
		}
	}
	return out
}

// Inputs is the cluster-and-values projection the gate evaluates. Each
// field is set by the gatherer; the checks are pure over Inputs so
// unit tests can stage every PASS/FAIL outcome without a live cluster.
type Inputs struct {
	// From is the source tier of the proposed promotion. Required.
	From Tier
	// To is the target tier of the proposed promotion. Required.
	To Tier

	// ChartValuesTier is the rendered chart's `capacityPlanning.tier`
	// value. The chart-values-diff check requires it to match To.
	ChartValuesTier Tier

	// GatewayReplicas is the deployed lenny-gateway Deployment's
	// .status.readyReplicas. The replicas check requires it to meet the
	// target tier's §17.8.2 floor.
	GatewayReplicas int
	// ControllerReplicas is the deployed warm-pool controller
	// Deployment's .status.readyReplicas. The replicas check requires
	// it to meet the target tier's §17.8.2 floor.
	ControllerReplicas int
	// OpsReplicas is the deployed lenny-ops Deployment's
	// .status.readyReplicas.
	OpsReplicas int

	// PostgresUsesPersistentStorage reports whether the Postgres data
	// directory is backed by a PersistentVolumeClaim (self-managed) or
	// an external managed endpoint. False indicates emptyDir/hostPath
	// or an embedded engine.
	PostgresUsesPersistentStorage bool
	// RedisUsesPersistentStorage reports whether the Redis data
	// directory is backed by a PersistentVolumeClaim or an external
	// managed endpoint. False indicates ephemeral storage.
	RedisUsesPersistentStorage bool

	// SecretEncryptionVerified is the operator-attested boolean that
	// etcd Secret encryption-at-rest is configured (EKS envelope
	// encryption, GKE CMEK, AKS Key Vault, or a self-managed
	// EncryptionConfiguration). The §17.6 preflight Job cannot verify
	// this programmatically (it lacks etcd access); the promotion gate
	// requires the operator to acknowledge it.
	SecretEncryptionVerified bool

	// AuditRetainDays is the deployed chart's `backups.retention.-
	// retainDays`. The retention check requires it to meet the target
	// tier's §25.11 floor.
	AuditRetainDays int

	// AdmissionWebhooks is the projection of the deployed
	// ValidatingWebhookConfigurations the gate inspects for the
	// §13.1/§13.2 posture. Each entry must be fail-closed with an
	// injected CA bundle. The gate uses the same projection the §17.6
	// preflight Job uses (see pkg/preflight).
	AdmissionWebhooks []WebhookPosture

	// AutoscalingProvider is the rendered chart's
	// `autoscaling.provider` value (e.g., "hpa" or "keda"). §17.8.3
	// line 1285 makes KEDA a NO-GO criterion at Tier 3: a promotion
	// whose chart renders the Prometheus Adapter path fails the gate.
	// spec: §17.8.3 line 1285, §17.8.2 Path A header line 963.
	AutoscalingProvider string

	// MinReplicas is the rendered chart's `autoscaling.minReplicas`
	// value. The SCL-036 burst-absorption check verifies it satisfies
	// the §17.8.2 line 950 formula for the active autoscaling provider.
	// spec: §17.8.2 line 950 (SCL-036).
	MinReplicas int

	// MaxSessionsPerReplica is the rendered chart's
	// `gateway.maxSessionsPerReplica` value. The SCL-036 burst-absorption
	// check uses it as the formula's `sessions_per_replica` term, and
	// the Phase 13.5 calibration attestation must be set before a
	// Tier 3 promotion uses the 400-session provisional value.
	// spec: §17.8.2 line 950 (SCL-036), §17.8.3 line 1284.
	MaxSessionsPerReplica int

	// LLMProxyExtractionAttested is the operator's attestation that
	// the Phase 13.5 LLM Proxy extraction ratio benchmark passed (the
	// `lenny_gateway_llm_proxy_active_connections / lenny_gateway_-
	// active_sessions` ratio remains sustainably below 0.3:1 at Tier 2
	// peak load). §17.8.3 line 1282 marks an unattested ratio above
	// 0.3:1 as a NO-GO for Tier 3 promotion.
	// spec: §17.8.3 lines 1263, 1273, 1282.
	LLMProxyExtractionAttested bool

	// GatewayGCPauseAttested is the operator's attestation that the
	// Phase 13.5 gateway GC pressure benchmark passed
	// (`lenny_gateway_gc_pause_p99_ms` remained below 50 ms at Tier 2
	// peak load). §17.8.3 line 1283 marks an unattested GC pause as a
	// NO-GO for Tier 3 promotion.
	// spec: §17.8.3 lines 1264, 1274, 1283.
	GatewayGCPauseAttested bool

	// MaxSessionsPerReplicaCalibrated is the operator's attestation
	// that the deployed `gateway.maxSessionsPerReplica` value has been
	// empirically calibrated by the Phase 13.5 benchmark harness, as
	// opposed to remaining at its §4.1 provisional baseline. §17.8.3
	// line 1284 makes calibration a NO-GO for Tier 3 promotion.
	// spec: §17.8.3 lines 1265, 1275, 1284.
	MaxSessionsPerReplicaCalibrated bool
}

// WebhookPosture is the inspected state of one deployed
// ValidatingWebhookConfiguration the §13.1/§13.2 posture check
// inspects. The projection mirrors pkg/preflight.WebhookConfig so a
// caller that already gathers webhook state for the preflight Job can
// reuse the same struct values.
type WebhookPosture struct {
	// Name is the ValidatingWebhookConfiguration's metadata.name.
	Name string
	// FailurePolicy is the configuration's failurePolicy; "Fail" is
	// required for the fail-closed admission posture.
	FailurePolicy string
	// HasCABundle reports whether every webhook entry carries a
	// non-empty clientConfig.caBundle.
	HasCABundle bool
}

// requiredReplicas captures the §17.8.2 gateway / controller / ops
// replica floors that each promotion target must meet before the
// promotion is allowed.
type requiredReplicas struct {
	Gateway    int
	Controller int
	Ops        int
}

// tierReplicaFloors lists the §17.8.2 replica floors per target tier.
// The values match the chart presets at
// charts/lenny/presets/values-tierN.yaml: Tier 1 sets 2/2/2,
// Tier 2 sets 3/2/2, and Tier 3 sets 5/3/3.
var tierReplicaFloors = map[Tier]requiredReplicas{
	Tier1: {Gateway: 2, Controller: 2, Ops: 2},
	Tier2: {Gateway: 3, Controller: 2, Ops: 2},
	Tier3: {Gateway: 5, Controller: 3, Ops: 3},
}

// auditRetentionFloors lists the §25.11 backup retention floors per
// target tier. The values match the chart presets: Tier 1 = 7 days,
// Tier 2 = 30 days, Tier 3 = 90 days.
var auditRetentionFloors = map[Tier]int{
	Tier1: 7,
	Tier2: 30,
	Tier3: 90,
}

// Gatherer collects the Inputs the gate needs from a live cluster.
// Implementations read the gateway, controller, and ops Deployments,
// the Postgres and Redis StatefulSet or external endpoint config, and
// the ValidatingWebhookConfigurations. A real implementation lives in
// the lenny-tier-promote binary; unit tests pass a synthetic Inputs
// value directly to Validate via ValidateInputs.
type Gatherer interface {
	// Gather reads the cluster state and returns the populated Inputs.
	// The from and to tiers come from the operator's invocation; the
	// gatherer fills every other field.
	Gather(ctx context.Context, from, to Tier) (Inputs, error)
}

// Validate runs the §17.8.3 promotion gate against a cluster via the
// supplied Gatherer. A no-op transition (from == to) returns a report
// of SKIP entries — the gate has nothing to validate. An invalid tier
// in either argument is returned as an error.
func Validate(ctx context.Context, gatherer Gatherer, from, to Tier) (Report, error) {
	if err := validateTransition(from, to); err != nil {
		return nil, err
	}
	if from == to {
		return skipAll(from, to), nil
	}
	inputs, err := gatherer.Gather(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("tier-promotion: gather cluster state: %w", err)
	}
	inputs.From = from
	inputs.To = to
	return ValidateInputs(inputs), nil
}

// ValidateInputs is the cluster-free entry point. The unit tests use
// it directly so every PASS/FAIL combination can be exercised without
// a live cluster.
func ValidateInputs(in Inputs) Report {
	if in.From == in.To {
		return skipAll(in.From, in.To)
	}
	return Report{
		CheckChartValues(in),
		CheckReplicas(in),
		CheckPersistentStorage(in),
		CheckSecretEncryption(in),
		CheckAuditRetention(in),
		CheckAdmissionPosture(in),
		CheckAutoscalingProvider(in),
		CheckBurstAbsorption(in),
		CheckPhase135Attestations(in),
	}
}

// validateTransition rejects invalid tier arguments and downgrades.
// §17.8.3 explicitly governs the 1 → 2 and 2 → 3 promotions; a
// downgrade is rejected so the operator cannot accidentally invoke the
// promotion gate against a relaxation it is not designed to validate.
func validateTransition(from, to Tier) error {
	if !from.IsValid() {
		return fmt.Errorf("tier-promotion: from tier %q is not a published tier (tier1, tier2, tier3)", from)
	}
	if !to.IsValid() {
		return fmt.Errorf("tier-promotion: to tier %q is not a published tier (tier1, tier2, tier3)", to)
	}
	if to.Rank() < from.Rank() {
		return fmt.Errorf("tier-promotion: %s → %s is a downgrade; this gate validates promotions only", from, to)
	}
	return nil
}

// skipAll renders a report of SKIP entries for a no-op transition
// (from == to). Each check carries a clear reason so the operator can
// see the gate ran but had nothing to validate.
func skipAll(from, to Tier) Report {
	reason := fmt.Sprintf("%s → %s is a no-op; nothing to validate", from, to)
	names := []string{
		"chart-values-diff",
		"deployed-replicas",
		"persistent-storage",
		"secret-encryption",
		"audit-retention",
		"admission-webhook-posture",
		"autoscaling-provider",
		"burst-absorption",
		"phase-13.5-attestations",
	}
	sort.Strings(names)
	out := make(Report, 0, len(names))
	for _, n := range names {
		out = append(out, CheckResult{Name: n, Status: StatusSkip, Detail: reason})
	}
	return out
}
