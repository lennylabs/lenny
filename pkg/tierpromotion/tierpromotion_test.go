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
// stage targeted failures.
func goodInputs() tierpromotion.Inputs {
	return tierpromotion.Inputs{
		From:                          tierpromotion.Tier2,
		To:                            tierpromotion.Tier3,
		ChartValuesTier:               tierpromotion.Tier3,
		GatewayReplicas:               5,
		ControllerReplicas:            3,
		OpsReplicas:                   3,
		PostgresUsesPersistentStorage: true,
		RedisUsesPersistentStorage:    true,
		SecretEncryptionVerified:      true,
		AuditRetainDays:               90,
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
	if len(report) != 6 {
		t.Errorf("report has %d checks; want 6", len(report))
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
		From:               tierpromotion.Tier1,
		To:                 tierpromotion.Tier2,
		ChartValuesTier:    tierpromotion.Tier2,
		GatewayReplicas:    3,
		ControllerReplicas: 2,
		OpsReplicas:        2,
		AuditRetainDays:    30,
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
			ChartValuesTier:          tierpromotion.Tier3,
			AuditRetainDays:          90,
			SecretEncryptionVerified: true,
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
