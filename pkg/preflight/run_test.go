// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/preflight"
)

const preflightNS = "lenny-system"

func runScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	// spec: §10 line 437 — the crd-schema-version check fetches the
	// installed CRDs by name; tests register apiextensions.k8s.io/v1
	// so the fake reader can deserialize the baseline CRD objects.
	// F-15.5.12.
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensions AddToScheme: %v", err)
	}
	return s
}

// baselineCRDs returns one healthy CRD per LennyCRDNames so the
// crd-schema-version check passes by default when an existing Run test
// only cares about a different §17.9 dimension. F-15.5.12.
func baselineCRDs() []client.Object {
	out := make([]client.Object, 0, len(preflight.LennyCRDNames))
	for _, name := range preflight.LennyCRDNames {
		out = append(out, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					preflight.CRDSchemaVersionAnnotation: preflight.CurrentCRDSchemaVersion,
				},
			},
		})
	}
	return out
}

func validatingWebhook(name string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	fail := admissionregistrationv1.Fail
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name:          name + ".lenny.dev",
			FailurePolicy: &fail,
			ClientConfig:  admissionregistrationv1.WebhookClientConfig{CABundle: []byte("ca-cert")},
		}},
	}
}

func phaseStampCM(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: preflight.PhaseStampConfigMapName, Namespace: preflightNS},
		Data:       data,
	}
}

func runClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	all := append(baselineCRDs(), objs...)
	return fake.NewClientBuilder().WithScheme(runScheme(t)).WithObjects(all...).Build()
}

// allBaselineWebhooks returns the baseline ValidatingWebhookConfigurations.
func allBaselineWebhooks() []client.Object {
	names := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	out := make([]client.Object, 0, len(names))
	for _, n := range names {
		out = append(out, validatingWebhook(n))
	}
	return out
}

func resultByName(report []preflight.CheckResult, name string) preflight.Decision {
	for _, r := range report {
		if r.Name == name {
			return r.Decision
		}
	}
	return preflight.Decision{}
}

func TestRunPassesWhenWebhooksHealthyAndNoPhaseStamp(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if preflight.Failed(report) {
		for _, r := range report {
			if !r.Decision.Passed {
				t.Errorf("check %q failed: %s", r.Name, r.Decision.Reason)
			}
		}
	}
}

// spec: §12.9 line 1050 — Run always emits the volume-encryption check.
// A default install (no devMode, no attestation) is non-blocking (WARNING);
// devMode exempts it; attestation clears the warning.
func TestRunEmitsVolumeEncryptionCheck_spec_12_9_1050(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	def := resultByName(preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS}), "volume-encryption")
	if !def.Passed || !strings.HasPrefix(def.Reason, "WARNING") {
		t.Errorf("default install: want non-blocking warning, got passed=%v %q", def.Passed, def.Reason)
	}

	dev := resultByName(preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS, DevMode: true}), "volume-encryption")
	if !dev.Passed || !strings.Contains(dev.Reason, "devMode") {
		t.Errorf("devMode: want clean exempt pass, got passed=%v %q", dev.Passed, dev.Reason)
	}

	att := resultByName(preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS, AttestVolumeEncryption: true}), "volume-encryption")
	if !att.Passed || strings.HasPrefix(att.Reason, "WARNING") {
		t.Errorf("attested: want clean pass, got passed=%v %q", att.Passed, att.Reason)
	}
}

func TestRunFailsOnFailOpenWebhook(t *testing.T) {
	// preflight runs before the chart's main phase, so a not-yet-deployed
	// webhook is not a gap. The genuine fail-open gap the inventory check
	// catches is a DEPLOYED webhook whose failurePolicy is not Fail.
	objs := allBaselineWebhooks()
	names := preflight.ExpectedValidatingWebhooks(preflight.WebhookFeatureFlags{})
	ignore := admissionregistrationv1.Ignore
	failOpen := validatingWebhook(names[len(names)-1])
	failOpen.Webhooks[0].FailurePolicy = &ignore
	objs[len(objs)-1] = failOpen
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !preflight.Failed(report) {
		t.Fatal("Run passed despite a fail-open (failurePolicy: Ignore) webhook")
	}
	if resultByName(report, "admission-webhook-inventory").Passed {
		t.Error("the admission-webhook-inventory check passed despite a fail-open webhook")
	}
}

func TestRunPassesWhenAFeatureGatedWebhookIsNotYetDeployed(t *testing.T) {
	// An upgrade that newly enables a feature-gated webhook: the prior
	// webhooks are deployed fail-closed, the new one lands only in the
	// chart's main phase. preflight must not abort the upgrade for it.
	objs := allBaselineWebhooks()
	c := runClient(t, objs[:len(objs)-1]...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "admission-webhook-inventory").Passed != true {
		t.Errorf("admission-webhook-inventory failed though every deployed webhook is fail-closed: %s",
			resultByName(report, "admission-webhook-inventory").Reason)
	}
}

func TestRunFailsOnUnacknowledgedPhaseStampDowngrade(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, phaseStampCM(map[string]string{
		"llmProxy": `{"enabled":true,"enabledAt":"2026-05-15T00:00:00Z"}`,
	}))
	c := runClient(t, objs...)

	// Features.LLMProxy is false: the recorded flag is being downgraded.
	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "phase-stamp-consistency").Passed {
		t.Error("the phase-stamp-consistency check passed an unacknowledged downgrade")
	}
}

func TestRunPassesWhenPhaseStampConsistent(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, phaseStampCM(map[string]string{
		"security.elicitationContentIntegrity.floor": "off",
		"compliance": `{"enabled":true,"enabledAt":"2026-05-15T00:00:00Z"}`,
	}))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		Features:  preflight.WebhookFeatureFlags{Compliance: true},
	})
	if resultByName(report, "phase-stamp-consistency").Passed != true {
		t.Errorf("phase-stamp-consistency failed though compliance is still enabled: %s",
			resultByName(report, "phase-stamp-consistency").Reason)
	}
}

func lennyDeployment(name string, hostPID bool) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: preflightNS,
			Labels:    map[string]string{"app.kubernetes.io/name": "lenny"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{HostPID: hostPID},
			},
		},
	}
}

func TestRunPassesWhenWorkloadsHaveNoHostSharing(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, lennyDeployment("lenny-gateway", false))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !resultByName(report, "host-sharing-flags").Passed {
		t.Errorf("host-sharing-flags failed for a compliant Deployment: %s",
			resultByName(report, "host-sharing-flags").Reason)
	}
}

func TestRunFailsOnHostSharingWorkload(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, lennyDeployment("lenny-gateway", true)) // hostPID enabled
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "host-sharing-flags").Passed {
		t.Error("host-sharing-flags passed a Deployment with hostPID true")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a host-sharing workload")
	}
}

func lennyDeploymentNS(ns, name string, hostPID bool) *appsv1.Deployment {
	d := lennyDeployment(name, hostPID)
	d.Namespace = ns
	return d
}

// agentPod builds a controller-spawned agent Pod carrying the
// lenny.dev/managed label the §13.1 preflight audits select on.
func agentPod(ns, name string, fsGroup *int64, supp []int64, hostPID bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"lenny.dev/managed": "true"},
		},
		Spec: corev1.PodSpec{
			HostPID: hostPID,
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup:            fsGroup,
				SupplementalGroups: supp,
			},
			Containers: []corev1.Container{{Name: "agent"}},
		},
	}
}

// spec: §13.1 lines 23/25 — a compliant agent pod passes both the
// host-sharing and credential-fsGroup audits. F-13.1.7 / F-13.1.4.
func TestRunPassesWhenAgentPodsCompliant(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, agentPod("lenny-agents", "alice-0", i64(credReadersGID), []int64{credReadersGID}, false))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:       preflightNS,
		AgentNamespaces: []string{"lenny-agents", "lenny-agents-kata"},
	})
	if d := resultByName(report, "host-sharing-flags"); !d.Passed {
		t.Errorf("host-sharing-flags failed for a compliant agent pod: %s", d.Reason)
	}
	if d := resultByName(report, "agent-pod-cred-fsgroup"); !d.Passed {
		t.Errorf("agent-pod-cred-fsgroup failed for a compliant agent pod: %s", d.Reason)
	}
}

// spec: §13.1 line 23 — an agent-namespace pod with hostPID escaped the
// release-namespace-only scan; the audit must now catch it. F-13.1.7.
func TestRunFailsOnAgentPodHostSharing(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, agentPod("lenny-agents", "alice-0", i64(credReadersGID), []int64{credReadersGID}, true))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:       preflightNS,
		AgentNamespaces: []string{"lenny-agents"},
	})
	if resultByName(report, "host-sharing-flags").Passed {
		t.Error("host-sharing-flags passed an agent pod with hostPID true")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a host-sharing agent pod")
	}
}

// spec: §13.1 line 25 — an agent pod missing the cred-readers fsGroup
// fails the install-time backstop. F-13.1.4.
func TestRunFailsOnAgentPodMissingFSGroup(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, agentPod("lenny-agents", "alice-0", nil, nil, false))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:       preflightNS,
		AgentNamespaces: []string{"lenny-agents"},
	})
	if resultByName(report, "agent-pod-cred-fsgroup").Passed {
		t.Error("agent-pod-cred-fsgroup passed an agent pod with no fsGroup")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a non-compliant agent pod fsGroup")
	}
}

// spec: §13.1 line 23 — the host-sharing scan now covers Deployments in
// the agent namespaces, not only the release namespace. F-13.1.7.
func TestRunFailsOnHostSharingDeploymentInAgentNamespace(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, lennyDeploymentNS("lenny-agents", "lenny-agent-helper", true))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:       preflightNS,
		AgentNamespaces: []string{"lenny-agents"},
	})
	if resultByName(report, "host-sharing-flags").Passed {
		t.Error("host-sharing-flags passed a Deployment with hostPID in an agent namespace")
	}
}

// When no agent namespaces are configured the credential audit is skipped
// (and the host-sharing scan stays release-scoped). F-13.1.4.
func TestRunSkipsAgentAuditWhenNoAgentNamespaces(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "agent-pod-cred-fsgroup") != (preflight.Decision{}) {
		t.Error("agent-pod-cred-fsgroup ran with no agent namespaces configured")
	}
}

func TestRunReportsAClusterReadFailureFailClosed(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*admissionregistrationv1.ValidatingWebhookConfigurationList); ok {
					return errors.New("api server unavailable")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a cluster read error")
	}
	if resultByName(report, "admission-webhook-inventory").Passed {
		t.Error("admission-webhook-inventory passed despite a failed List")
	}
}

func TestRunIgnoresNonLennyWebhooks(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, validatingWebhook("third-party-webhook"))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if preflight.Failed(report) {
		t.Error("Run failed though only an unrelated third-party webhook was extra")
	}
}

// Run runs the §12.4 redis-maxmemory-policy check only when a BYO Redis
// prober is configured (the cloud profile sets the policy natively); a
// drifted policy fails the install. F-12.4.15.
func TestRunWiresRedisMaxmemoryPolicyCheck_spec_12_4(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	noProber := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(noProber, "redis-maxmemory-policy") != (preflight.Decision{}) {
		t.Error("redis-maxmemory-policy check ran with no prober configured")
	}

	drift := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		RedisConfigProber: preflight.RedisConfigProbeFunc(func(context.Context, string) (string, error) {
			return "allkeys-lru", nil
		}),
	})
	if d := resultByName(drift, "redis-maxmemory-policy"); d.Passed {
		t.Error("redis-maxmemory-policy passed against an allkeys-lru BYO Redis")
	}
	if !preflight.Failed(drift) {
		t.Error("Run did not fail the install despite a drifted Redis eviction policy")
	}
}

// Run runs the §17.6 line 488 cloud-pooler sentinel defense only when
// the effective connectionPooler is external; an absent lenny_tenant_guard
// trigger fails the install fail-closed, while a non-external pooler or a
// nil prober short-circuits to a pass. F-17.9.2.
func TestRunWiresCloudPoolerSentinelCheck_spec_17_9_2(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	// Non-external pooler: the check does not run at all.
	pg := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:        preflightNS,
		ConnectionPooler: "pgbouncer",
		PoolerSentinelProber: preflight.PoolerSentinelProbeFunc(func(context.Context) ([]string, error) {
			return []string{"sessions"}, nil
		}),
	})
	if resultByName(pg, "cloud-pooler-sentinel-defense") != (preflight.Decision{}) {
		t.Error("cloud-pooler-sentinel-defense ran for a pgbouncer pooler")
	}

	// External pooler with a gap: the install fails closed.
	gap := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:        preflightNS,
		ConnectionPooler: "external",
		PoolerSentinelProber: preflight.PoolerSentinelProbeFunc(func(context.Context) ([]string, error) {
			return []string{"sessions"}, nil
		}),
	})
	if d := resultByName(gap, "cloud-pooler-sentinel-defense"); d.Passed {
		t.Error("cloud-pooler-sentinel-defense passed despite a missing lenny_tenant_guard trigger")
	}
	if !preflight.Failed(gap) {
		t.Error("Run did not fail the install despite an unprotected tenant-scoped table under external pooler")
	}

	// External pooler with no DSN wired: defers to the runtime defense
	// (advisory pass) rather than blocking the install.
	noProber := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:        preflightNS,
		ConnectionPooler: "external",
	})
	if d := resultByName(noProber, "cloud-pooler-sentinel-defense"); !d.Passed {
		t.Errorf("cloud-pooler-sentinel-defense should defer to the runtime defense with no prober; got %q", d.Reason)
	}
}

// networkProbeConfig wires every backend-reachability probe (MinIO SSE,
// BYO-Redis maxmemory, OTLP collector TLS, ops-admin internal TLS) with a
// closure that records whether it was invoked. The closures flip *called
// so a test can assert the probes are dropped under skipNetworkProbes.
func networkProbeConfig(skip bool, called *[4]bool) preflight.Config {
	return preflight.Config{
		Namespace:         preflightNS,
		SkipNetworkProbes: skip,
		MinIOBucket:       "lenny-artifacts",
		MinIOEncryptionProber: preflight.MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
			called[0] = true
			return "aws:kms", nil
		}),
		RedisConfigProber: preflight.RedisConfigProbeFunc(func(context.Context, string) (string, error) {
			called[1] = true
			return "noeviction", nil
		}),
		OTLP: preflight.OTLPTLSConfig{Endpoint: "https://otel-collector.lenny-system.svc:4317", TLSEnabled: true},
		OTLPTLSProber: preflight.OTLPTLSProbeFunc(func(context.Context, string) error {
			called[2] = true
			return nil
		}),
		OpsAdminTLS: preflight.OpsAdminTLSConfig{Endpoint: "lenny-gateway.lenny-system.svc:8443", InternalEnabled: true, ExpectedSANHost: "lenny-gateway.lenny-system.svc"},
		OpsAdminTLSProber: preflight.OpsAdminTLSProbeFunc(func(context.Context, string, string) error {
			called[3] = true
			return nil
		}),
	}
}

// spec: §17.9.2 line 1372 / §17.9.11 — preflight.skipNetworkProbes drops
// the backend-reachability probes (MinIO SSE, BYO-Redis maxmemory, OTLP
// TLS handshake, ops-admin internal-TLS handshake) for an air-gapped
// install while the cluster-API checks still run. F-17.9.11.
func TestRunSkipsNetworkProbesWhenAirgapped_spec_17_9_11(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)
	netChecks := []string{
		"minio-server-side-encryption",
		"redis-maxmemory-policy",
		"otlp-tls",
		"ops-admin-tls",
	}

	// Baseline: with skipNetworkProbes false, every backend-reachability
	// probe runs and is present in the report.
	var ran [4]bool
	on := preflight.Run(context.Background(), c, networkProbeConfig(false, &ran))
	for _, name := range netChecks {
		if resultByName(on, name) == (preflight.Decision{}) {
			t.Errorf("%s did not run with skipNetworkProbes=false", name)
		}
	}

	// Airgap: with skipNetworkProbes true, none of the four checks run and
	// none of their probers are dialed.
	var skipped [4]bool
	off := preflight.Run(context.Background(), c, networkProbeConfig(true, &skipped))
	for _, name := range netChecks {
		if resultByName(off, name) != (preflight.Decision{}) {
			t.Errorf("%s ran despite skipNetworkProbes=true", name)
		}
	}
	for i, did := range skipped {
		if did {
			t.Errorf("network prober %d was dialed despite skipNetworkProbes=true", i)
		}
	}

	// The cluster-API checks are unaffected: the admission-webhook
	// inventory still runs and the airgap install is not failed by the
	// skip alone.
	if resultByName(off, "admission-webhook-inventory") == (preflight.Decision{}) {
		t.Error("admission-webhook-inventory was dropped by skipNetworkProbes")
	}
	if preflight.Failed(off) {
		t.Error("skipNetworkProbes failed the install despite healthy cluster-API checks")
	}
}
