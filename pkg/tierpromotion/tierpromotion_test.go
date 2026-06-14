// SPDX-License-Identifier: MIT

package tierpromotion_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/tierpromotion"
)

// goodInputs returns an Inputs fixture that passes every check for a
// Tier 2 → Tier 3 promotion. Test cases override individual fields to
// stage targeted failures. The §17.8.3 Phase 13.5 attestations are all
// set; the §17.8.2 SCL-036 inputs match the Tier 3 KEDA preset
// (minReplicas: 5 via the §17.8.2 line 975 carve-out).
func goodInputs() tierpromotion.Inputs {
	return tierpromotion.Inputs{
		From:                            tierpromotion.Tier2,
		To:                              tierpromotion.Tier3,
		ChartValuesTier:                 tierpromotion.Tier3,
		GatewayReplicas:                 5,
		ControllerReplicas:              3,
		OpsReplicas:                     3,
		PostgresUsesPersistentStorage:   true,
		RedisUsesPersistentStorage:      true,
		SecretEncryptionVerified:        true,
		AuditRetainDays:                 90,
		AutoscalingProvider:             "keda",
		MinReplicas:                     5,
		MaxSessionsPerReplica:           400,
		LLMProxyExtractionAttested:      true,
		GatewayGCPauseAttested:          true,
		MaxSessionsPerReplicaCalibrated: true,
		AdmissionWebhooks: []tierpromotion.WebhookPosture{
			{Name: "lenny-label-immutability", FailurePolicy: "Fail", HasCABundle: true},
			{Name: "lenny-pod-security", FailurePolicy: "Fail", HasCABundle: true},
		},
	}
}

func TestTierIsValidAndRank(t *testing.T) {
	cases := []struct {
		tier  tierpromotion.Tier
		valid bool
		rank  int
	}{
		{tierpromotion.Tier1, true, 1},
		{tierpromotion.Tier2, true, 2},
		{tierpromotion.Tier3, true, 3},
		{tierpromotion.Tier(""), false, 0},
		{tierpromotion.Tier("tier4"), false, 0},
	}
	for _, c := range cases {
		if c.tier.IsValid() != c.valid {
			t.Errorf("%q.IsValid()=%v, want %v", c.tier, c.tier.IsValid(), c.valid)
		}
		if c.tier.Rank() != c.rank {
			t.Errorf("%q.Rank()=%d, want %d", c.tier, c.tier.Rank(), c.rank)
		}
	}
}

func TestAllTiersIsExhaustive(t *testing.T) {
	all := tierpromotion.AllTiers()
	if len(all) != 3 {
		t.Fatalf("AllTiers() yielded %d tiers; want 3", len(all))
	}
	if all[0] != tierpromotion.Tier1 || all[1] != tierpromotion.Tier2 || all[2] != tierpromotion.Tier3 {
		t.Errorf("AllTiers() must be ordered tier1, tier2, tier3; got %v", all)
	}
}

func TestValidateInputsPassesGoodFixture(t *testing.T) {
	report := tierpromotion.ValidateInputs(goodInputs())
	if !report.Passed() {
		t.Errorf("good fixture failed the gate: %v", report.Failures())
	}
	if len(report) != 9 {
		t.Errorf("report has %d checks; want 9", len(report))
	}
}

func TestValidateInputsRejectsChartValuesTierMismatch(t *testing.T) {
	in := goodInputs()
	in.ChartValuesTier = tierpromotion.Tier2
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "chart-values-diff")
	if !strings.Contains(failed.Detail, "tier2") || !strings.Contains(failed.Detail, "tier3") {
		t.Errorf("chart-values-diff detail %q does not name the tiers", failed.Detail)
	}
}

func TestValidateInputsRejectsChartValuesTierUnset(t *testing.T) {
	in := goodInputs()
	in.ChartValuesTier = ""
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "chart-values-diff")
	if !strings.Contains(failed.Detail, "unset") {
		t.Errorf("chart-values-diff detail %q does not flag the unset value", failed.Detail)
	}
}

func TestValidateInputsRejectsGatewayReplicaShortfall(t *testing.T) {
	in := goodInputs()
	in.GatewayReplicas = 2
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "deployed-replicas")
	if !strings.Contains(failed.Detail, "lenny-gateway") {
		t.Errorf("deployed-replicas detail %q does not name the gateway", failed.Detail)
	}
	if !strings.Contains(failed.Detail, "5") {
		t.Errorf("deployed-replicas detail %q does not name the Tier 3 floor 5", failed.Detail)
	}
}

func TestValidateInputsRejectsControllerReplicaShortfall(t *testing.T) {
	in := goodInputs()
	in.ControllerReplicas = 1
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "deployed-replicas")
	if !strings.Contains(failed.Detail, "lenny-controller") {
		t.Errorf("deployed-replicas detail %q does not name the controller", failed.Detail)
	}
}

func TestValidateInputsRejectsOpsReplicaShortfall(t *testing.T) {
	in := goodInputs()
	in.OpsReplicas = 1
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "deployed-replicas")
	if !strings.Contains(failed.Detail, "lenny-ops") {
		t.Errorf("deployed-replicas detail %q does not name lenny-ops", failed.Detail)
	}
}

func TestValidateInputsRejectsPostgresEphemeralStorage(t *testing.T) {
	in := goodInputs()
	in.PostgresUsesPersistentStorage = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "persistent-storage")
	if !strings.Contains(failed.Detail, "Postgres") {
		t.Errorf("persistent-storage detail %q does not name Postgres", failed.Detail)
	}
}

func TestValidateInputsRejectsRedisEphemeralStorage(t *testing.T) {
	in := goodInputs()
	in.RedisUsesPersistentStorage = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "persistent-storage")
	if !strings.Contains(failed.Detail, "Redis") {
		t.Errorf("persistent-storage detail %q does not name Redis", failed.Detail)
	}
}

func TestValidateInputsSkipsPersistentStorageAndSecretEncryptionAtTier1Target(t *testing.T) {
	in := tierpromotion.Inputs{
		From: tierpromotion.Tier1, To: tierpromotion.Tier1,
	}
	// Tier 1 → Tier 1 is a no-op, every check is SKIP.
	report := tierpromotion.ValidateInputs(in)
	for _, c := range report {
		if c.Status != tierpromotion.StatusSkip {
			t.Errorf("Tier 1 → Tier 1 produced %s for %q; want SKIP", c.Status, c.Name)
		}
	}
}

func TestValidateInputsRejectsUnverifiedSecretEncryption(t *testing.T) {
	in := goodInputs()
	in.SecretEncryptionVerified = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "secret-encryption")
	if !strings.Contains(failed.Detail, "encryption") {
		t.Errorf("secret-encryption detail %q does not name encryption", failed.Detail)
	}
}

func TestValidateInputsRejectsRetainDaysBelowFloor(t *testing.T) {
	in := goodInputs()
	in.AuditRetainDays = 7
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "audit-retention")
	if !strings.Contains(failed.Detail, "90") {
		t.Errorf("audit-retention detail %q does not name the Tier 3 floor 90", failed.Detail)
	}
}

func TestValidateInputsRejectsFailOpenWebhook(t *testing.T) {
	in := goodInputs()
	in.AdmissionWebhooks[0].FailurePolicy = "Ignore"
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "admission-webhook-posture")
	if !strings.Contains(failed.Detail, "Ignore") || !strings.Contains(failed.Detail, "Fail") {
		t.Errorf("admission-webhook-posture detail %q does not name the failurePolicy mismatch", failed.Detail)
	}
}

func TestValidateInputsRejectsMissingCABundle(t *testing.T) {
	in := goodInputs()
	in.AdmissionWebhooks[0].HasCABundle = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "admission-webhook-posture")
	if !strings.Contains(failed.Detail, "caBundle") {
		t.Errorf("admission-webhook-posture detail %q does not name the caBundle gap", failed.Detail)
	}
}

func TestValidateInputsRejectsEmptyWebhookList(t *testing.T) {
	in := goodInputs()
	in.AdmissionWebhooks = nil
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "admission-webhook-posture")
	if !strings.Contains(failed.Detail, "absent") {
		t.Errorf("admission-webhook-posture detail %q does not flag the absent admission plane", failed.Detail)
	}
}

func TestValidateInputsTier1ToTier2SkipsPostTier1ChecksOnlyWhenAppropriate(t *testing.T) {
	// Tier 1 → Tier 2 still requires persistent storage and secret
	// encryption to be set; this asserts the SKIP carve-out only
	// applies to a Tier 1 target.
	in := tierpromotion.Inputs{
		From:                  tierpromotion.Tier1,
		To:                    tierpromotion.Tier2,
		ChartValuesTier:       tierpromotion.Tier2,
		GatewayReplicas:       3,
		ControllerReplicas:    2,
		OpsReplicas:           2,
		AuditRetainDays:       30,
		AutoscalingProvider:   "keda",
		MinReplicas:           3,
		MaxSessionsPerReplica: 200,
		AdmissionWebhooks: []tierpromotion.WebhookPosture{
			{Name: "lenny-pod-security", FailurePolicy: "Fail", HasCABundle: true},
		},
	}
	// PostgresUsesPersistentStorage and SecretEncryptionVerified left false.
	report := tierpromotion.ValidateInputs(in)
	if report.Passed() {
		t.Fatalf("Tier 1 → Tier 2 with ephemeral storage was accepted: %+v", report)
	}
	failuresByName(t, report, "persistent-storage")
	failuresByName(t, report, "secret-encryption")
}

// spec: §17.8.3 line 1285 — F-17.8.10. The Tier 3 gate must reject a
// deployment whose chart still carries autoscaling.provider: hpa.
func TestValidateInputsRejectsHPAProviderAtTier3_spec_17_8_3_1285(t *testing.T) {
	in := goodInputs()
	in.AutoscalingProvider = "hpa"
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "autoscaling-provider")
	if !strings.Contains(failed.Detail, "KEDA") && !strings.Contains(failed.Detail, "keda") {
		t.Errorf("autoscaling-provider detail %q does not name KEDA", failed.Detail)
	}
	if !strings.Contains(failed.Detail, "hpa") {
		t.Errorf("autoscaling-provider detail %q does not name the offending provider", failed.Detail)
	}
}

// spec: §17.8.3 line 1285 — F-17.8.10. An unset autoscaling.provider
// fails the Tier 3 gate (the rendered chart did not commit to a path).
func TestValidateInputsRejectsUnsetProviderAtTier3_spec_17_8_3_1285(t *testing.T) {
	in := goodInputs()
	in.AutoscalingProvider = ""
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "autoscaling-provider")
	if !strings.Contains(failed.Detail, "unset") {
		t.Errorf("autoscaling-provider detail %q does not flag the unset value", failed.Detail)
	}
}

// spec: §17.8.2 line 963 — F-17.8.10. KEDA is optional at Tier 1 / 2,
// so the autoscaling-provider check SKIPs when the target is not Tier 3.
func TestValidateInputsSkipsProviderCheckBelowTier3_spec_17_8_2_963(t *testing.T) {
	in := tierpromotion.Inputs{
		From:                          tierpromotion.Tier1,
		To:                            tierpromotion.Tier2,
		ChartValuesTier:               tierpromotion.Tier2,
		GatewayReplicas:               3,
		ControllerReplicas:            2,
		OpsReplicas:                   2,
		AuditRetainDays:               30,
		AutoscalingProvider:           "hpa", // permitted at Tier 1 / 2
		MinReplicas:                   9,
		MaxSessionsPerReplica:         200,
		PostgresUsesPersistentStorage: true,
		RedisUsesPersistentStorage:    true,
		SecretEncryptionVerified:      true,
		AdmissionWebhooks: []tierpromotion.WebhookPosture{
			{Name: "lenny-pod-security", FailurePolicy: "Fail", HasCABundle: true},
		},
	}
	report := tierpromotion.ValidateInputs(in)
	found := false
	for _, c := range report {
		if c.Name == "autoscaling-provider" {
			found = true
			if c.Status != tierpromotion.StatusSkip {
				t.Errorf("autoscaling-provider at Tier 2 target = %s, want SKIP", c.Status)
			}
		}
	}
	if !found {
		t.Error("report missing autoscaling-provider entry")
	}
}

// spec: §17.8.3 line 1282 — F-17.8.11. The Tier 3 gate must reject a
// promotion when the LLM Proxy extraction-ratio benchmark has not been
// attested.
func TestValidateInputsRejectsUnattestedLLMProxy_spec_17_8_3_1282(t *testing.T) {
	in := goodInputs()
	in.LLMProxyExtractionAttested = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "phase-13.5-attestations")
	if !strings.Contains(failed.Detail, "LLM Proxy") {
		t.Errorf("phase-13.5-attestations detail %q does not name LLM Proxy", failed.Detail)
	}
}

// spec: §17.8.3 line 1283 — F-17.8.11. The Tier 3 gate must reject a
// promotion when the gateway GC pause benchmark has not been attested.
func TestValidateInputsRejectsUnattestedGCPause_spec_17_8_3_1283(t *testing.T) {
	in := goodInputs()
	in.GatewayGCPauseAttested = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "phase-13.5-attestations")
	if !strings.Contains(failed.Detail, "GC pause") {
		t.Errorf("phase-13.5-attestations detail %q does not name GC pause", failed.Detail)
	}
}

// spec: §17.8.3 line 1284 — F-17.8.11. The Tier 3 gate must reject a
// promotion when the maxSessionsPerReplica calibration has not been
// attested (i.e., the provisional value is still in place).
func TestValidateInputsRejectsUncalibratedMaxSessions_spec_17_8_3_1284(t *testing.T) {
	in := goodInputs()
	in.MaxSessionsPerReplicaCalibrated = false
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "phase-13.5-attestations")
	if !strings.Contains(failed.Detail, "maxSessionsPerReplica") {
		t.Errorf("phase-13.5-attestations detail %q does not name maxSessionsPerReplica", failed.Detail)
	}
}

// spec: §17.8.3 Phase 13.5 — F-17.8.11. Tier 2 promotions do not gate
// on Phase 13.5 attestations; the check SKIPs.
func TestValidateInputsSkipsPhase135BelowTier3_spec_17_8_3(t *testing.T) {
	in := tierpromotion.Inputs{
		From:                          tierpromotion.Tier1,
		To:                            tierpromotion.Tier2,
		ChartValuesTier:               tierpromotion.Tier2,
		GatewayReplicas:               3,
		ControllerReplicas:            2,
		OpsReplicas:                   2,
		AuditRetainDays:               30,
		AutoscalingProvider:           "keda",
		MinReplicas:                   3,
		MaxSessionsPerReplica:         200,
		PostgresUsesPersistentStorage: true,
		RedisUsesPersistentStorage:    true,
		SecretEncryptionVerified:      true,
		AdmissionWebhooks: []tierpromotion.WebhookPosture{
			{Name: "lenny-pod-security", FailurePolicy: "Fail", HasCABundle: true},
		},
	}
	report := tierpromotion.ValidateInputs(in)
	for _, c := range report {
		if c.Name == "phase-13.5-attestations" && c.Status != tierpromotion.StatusSkip {
			t.Errorf("phase-13.5-attestations at Tier 2 target = %s, want SKIP", c.Status)
		}
	}
}

// spec: §17.8.2 line 950 (SCL-036) — F-17.8.13. The KEDA path's
// pipeline-lag is 20s; raising maxSessionsPerReplica without raising
// minReplicas at Tier 3 must trip the burst-absorption check below the
// §17.8.2 line 975 carve-out of 5.
func TestValidateInputsRejectsKEDAMinReplicasBelowFloor_spec_17_8_2_SCL_036(t *testing.T) {
	in := goodInputs()
	// Tier 3 / KEDA / maxSessionsPerReplica=400: raw floor = ceil(200*20/400) = 10.
	// The §17.8.2 line 975 carve-out drops the required floor to 5.
	// minReplicas=4 sits below that carve-out and must fail.
	in.MinReplicas = 4
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "burst-absorption")
	if !strings.Contains(failed.Detail, "minReplicas=4") {
		t.Errorf("burst-absorption detail %q does not name the offending minReplicas", failed.Detail)
	}
	if !strings.Contains(failed.Detail, "carve-out") {
		t.Errorf("burst-absorption detail %q does not flag the §17.8.2 line 975 carve-out", failed.Detail)
	}
}

// spec: §17.8.2 line 975 (KEDA Tier 3 carve-out) — F-17.8.13. The
// carve-out lets Tier 3 KEDA deployments set minReplicas=5 even though
// the raw SCL-036 floor is 10.
func TestValidateInputsAcceptsTier3KEDACarveOut_spec_17_8_2_975(t *testing.T) {
	in := goodInputs()
	in.MinReplicas = 5
	report := tierpromotion.ValidateInputs(in)
	for _, c := range report {
		if c.Name == "burst-absorption" && c.Status == tierpromotion.StatusFail {
			t.Errorf("Tier 3 KEDA minReplicas=5 should pass the carve-out, got FAIL: %s", c.Detail)
		}
	}
}

// spec: §17.8.2 line 989-991 (HPA Tier 3 non-viable) — F-17.8.13. The
// Prometheus Adapter path at Tier 3 needs minReplicas=30 (the raw
// SCL-036 floor with pipeline_lag=60s and maxSessionsPerReplica=400);
// no carve-out applies, so a deployment that left the HPA path on with
// minReplicas=5 fails the burst-absorption check. (The
// autoscaling-provider check also fails separately at Tier 3.)
func TestValidateInputsRejectsHPATier3UnderfloorMinReplicas_spec_17_8_2_991(t *testing.T) {
	in := goodInputs()
	in.AutoscalingProvider = "hpa"
	in.MinReplicas = 5
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "burst-absorption")
	if !strings.Contains(failed.Detail, "minReplicas=5") {
		t.Errorf("burst-absorption detail %q does not name the offending minReplicas", failed.Detail)
	}
	if !strings.Contains(failed.Detail, "30") {
		t.Errorf("burst-absorption detail %q does not name the SCL-036 floor 30 for HPA Tier 3", failed.Detail)
	}
}

// spec: §17.8.2 line 950 (SCL-036) — F-17.8.13. A zero
// maxSessionsPerReplica is a malformed deployment; the gate refuses to
// run the formula and reports the missing input.
func TestValidateInputsRejectsZeroMaxSessions_spec_17_8_2_950(t *testing.T) {
	in := goodInputs()
	in.MaxSessionsPerReplica = 0
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "burst-absorption")
	if !strings.Contains(failed.Detail, "maxSessionsPerReplica") {
		t.Errorf("burst-absorption detail %q does not name the missing input", failed.Detail)
	}
}

// spec: §17.8.2 line 988 — F-17.8.13. The Prometheus Adapter path at
// Tier 2 needs minReplicas >= ceil(30*60/200) = 9. The gate honors the
// HPA-path lag of 60s when the rendered chart selects provider=hpa.
func TestValidateInputsHPAPathUsesProperLag_spec_17_8_2_988(t *testing.T) {
	in := tierpromotion.Inputs{
		From:                          tierpromotion.Tier1,
		To:                            tierpromotion.Tier2,
		ChartValuesTier:               tierpromotion.Tier2,
		GatewayReplicas:               3,
		ControllerReplicas:            2,
		OpsReplicas:                   2,
		AuditRetainDays:               30,
		AutoscalingProvider:           "hpa",
		MinReplicas:                   3, // below the HPA Tier 2 floor of 9
		MaxSessionsPerReplica:         200,
		PostgresUsesPersistentStorage: true,
		RedisUsesPersistentStorage:    true,
		SecretEncryptionVerified:      true,
		AdmissionWebhooks: []tierpromotion.WebhookPosture{
			{Name: "lenny-pod-security", FailurePolicy: "Fail", HasCABundle: true},
		},
	}
	report := tierpromotion.ValidateInputs(in)
	failed := failuresByName(t, report, "burst-absorption")
	if !strings.Contains(failed.Detail, "9") {
		t.Errorf("burst-absorption detail %q does not name the HPA Tier 2 floor 9", failed.Detail)
	}
}

func TestValidateRejectsInvalidTransition(t *testing.T) {
	cases := []struct {
		name string
		from tierpromotion.Tier
		to   tierpromotion.Tier
		want string
	}{
		{"bad-from", tierpromotion.Tier("tier4"), tierpromotion.Tier2, "from tier"},
		{"bad-to", tierpromotion.Tier1, tierpromotion.Tier("tier4"), "to tier"},
		{"downgrade", tierpromotion.Tier3, tierpromotion.Tier1, "downgrade"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tierpromotion.Validate(context.Background(), nopGatherer{}, c.from, c.to)
			if err == nil {
				t.Fatalf("Validate should reject %s → %s", c.from, c.to)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
		})
	}
}

func TestValidateNoOpProducesSkipReport(t *testing.T) {
	report, err := tierpromotion.Validate(context.Background(), failingGatherer{}, tierpromotion.Tier2, tierpromotion.Tier2)
	if err != nil {
		t.Fatalf("no-op transition errored: %v", err)
	}
	for _, c := range report {
		if c.Status != tierpromotion.StatusSkip {
			t.Errorf("check %q has status %s; expected SKIP for a no-op transition", c.Name, c.Status)
		}
	}
}

func TestValidatePropagatesGatherError(t *testing.T) {
	_, err := tierpromotion.Validate(context.Background(), failingGatherer{}, tierpromotion.Tier2, tierpromotion.Tier3)
	if err == nil {
		t.Fatal("Validate should surface a gather error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should wrap the gather failure", err)
	}
}

func TestClusterGathererReadsCanonicalDeploymentsAndWebhooks(t *testing.T) {
	scheme := newScheme(t)
	objs := []client.Object{
		deployment("lenny-gateway", "lenny-system", 5),
		deployment("lenny-controller", "lenny-system", 3),
		deployment("lenny-ops", "lenny-system", 3),
		statefulSetWithPVC("lenny-postgres", "lenny-system"),
		statefulSetWithPVC("lenny-redis", "lenny-system"),
		failClosedWebhook("lenny-pod-security"),
		failClosedWebhook("lenny-label-immutability"),
		failClosedWebhook("not-lenny-webhook"), // ignored: missing lenny- prefix.
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	g := &tierpromotion.ClusterGatherer{
		Reader: cl,
		Options: tierpromotion.GatherOptions{
			ChartValuesTier:                 tierpromotion.Tier3,
			AuditRetainDays:                 90,
			SecretEncryptionVerified:        true,
			AutoscalingProvider:             "keda",
			MinReplicas:                     5,
			MaxSessionsPerReplica:           400,
			LLMProxyExtractionAttested:      true,
			GatewayGCPauseAttested:          true,
			MaxSessionsPerReplicaCalibrated: true,
		},
	}
	in, err := g.Gather(context.Background(), tierpromotion.Tier2, tierpromotion.Tier3)
	if err != nil {
		t.Fatalf("Gather errored: %v", err)
	}
	if in.GatewayReplicas != 5 || in.ControllerReplicas != 3 || in.OpsReplicas != 3 {
		t.Errorf("ready-replicas mismatch: gateway=%d controller=%d ops=%d",
			in.GatewayReplicas, in.ControllerReplicas, in.OpsReplicas)
	}
	if !in.PostgresUsesPersistentStorage || !in.RedisUsesPersistentStorage {
		t.Errorf("data-plane persistent-storage detection failed: pg=%v redis=%v",
			in.PostgresUsesPersistentStorage, in.RedisUsesPersistentStorage)
	}
	if len(in.AdmissionWebhooks) != 2 {
		t.Errorf("AdmissionWebhooks projected %d webhooks; want 2 (the non-lenny one is dropped)",
			len(in.AdmissionWebhooks))
	}
	report := tierpromotion.ValidateInputs(in)
	if !report.Passed() {
		t.Errorf("ClusterGatherer + good cluster failed the gate: %v", report.Failures())
	}
}

func TestClusterGathererTreatsMissingDeploymentAsZeroReady(t *testing.T) {
	scheme := newScheme(t)
	// No gateway Deployment in the cluster.
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		deployment("lenny-controller", "lenny-system", 3),
	).Build()
	g := &tierpromotion.ClusterGatherer{Reader: cl}
	in, err := g.Gather(context.Background(), tierpromotion.Tier2, tierpromotion.Tier3)
	if err != nil {
		t.Fatalf("Gather should not error on missing Deployment: %v", err)
	}
	if in.GatewayReplicas != 0 {
		t.Errorf("missing gateway should yield 0 ready replicas, got %d", in.GatewayReplicas)
	}
}

func TestClusterGathererHonorsExternalDataPlanes(t *testing.T) {
	scheme := newScheme(t)
	// No StatefulSets at all; the operator wired Postgres and Redis to
	// managed endpoints.
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	g := &tierpromotion.ClusterGatherer{
		Reader: cl,
		Options: tierpromotion.GatherOptions{
			PostgresExternal: true,
			RedisExternal:    true,
		},
	}
	in, err := g.Gather(context.Background(), tierpromotion.Tier2, tierpromotion.Tier3)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !in.PostgresUsesPersistentStorage || !in.RedisUsesPersistentStorage {
		t.Errorf("external data planes were not treated as persistent: pg=%v redis=%v",
			in.PostgresUsesPersistentStorage, in.RedisUsesPersistentStorage)
	}
}

// --- helpers --------------------------------------------------------

func failuresByName(t *testing.T, r tierpromotion.Report, name string) tierpromotion.CheckResult {
	t.Helper()
	for _, c := range r {
		if c.Name == name && c.Status == tierpromotion.StatusFail {
			return c
		}
	}
	t.Fatalf("report did not contain a FAIL for %q: %+v", name, r)
	return tierpromotion.CheckResult{}
}

type nopGatherer struct{}

func (nopGatherer) Gather(context.Context, tierpromotion.Tier, tierpromotion.Tier) (tierpromotion.Inputs, error) {
	return tierpromotion.Inputs{}, nil
}

type failingGatherer struct{}

func (failingGatherer) Gather(context.Context, tierpromotion.Tier, tierpromotion.Tier) (tierpromotion.Inputs, error) {
	return tierpromotion.Inputs{}, errors.New("boom")
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func deployment(name, namespace string, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func statefulSetWithPVC(name, namespace string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
			},
		},
	}
}

func failClosedWebhook(name string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	fail := admissionregistrationv1.Fail
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:          name + ".lenny.dev",
			FailurePolicy: &fail,
			ClientConfig:  admissionregistrationv1.WebhookClientConfig{CABundle: []byte("ca")},
		}},
	}
}
