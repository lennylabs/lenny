// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// lennyManagedLabel selects the workloads the chart labels as
// Lenny-managed; the host-sharing check scans only these.
var lennyManagedLabel = client.MatchingLabels{"app.kubernetes.io/name": "lenny"}

// PhaseStampConfigMapName is the name of the §17.2 phase-stamp ConfigMap.
const PhaseStampConfigMapName = "lenny-deployment-phase-stamp"

// elicitationFloorKey is the phase-stamp ConfigMap data key that holds
// the elicitation-content-integrity floor rather than a feature-flag
// record; the phase-stamp consistency check skips it.
const elicitationFloorKey = "security.elicitationContentIntegrity.floor"

// Config is the preflight run configuration the lenny-preflight Job
// supplies from the chart values.
type Config struct {
	// Namespace is the release namespace holding the phase-stamp
	// ConfigMap.
	Namespace string
	// Features are the incoming chart feature-flag values.
	Features WebhookFeatureFlags
	// AcceptDowngrade maps a feature flag to its
	// acceptFeatureFlagDowngrade override.
	AcceptDowngrade map[string]bool
	// SPIFFETrustDomain is the global.spiffeTrustDomain value under
	// installation, checked for §13.2 NET-064 uniqueness against the
	// trust domains already in use cluster-wide.
	SPIFFETrustDomain string
	// SATokenAudience is the global.saTokenAudience value under
	// installation, checked for §13.2 NET-064 uniqueness against the
	// audiences already in use cluster-wide.
	SATokenAudience string
	// ComplianceProfile names the §11.7 / §12.5 regulated posture
	// (soc2 | fedramp | hipaa) the chart is being installed under.
	// Empty selects no profile.
	ComplianceProfile string
	// MinIOBucket is the artifact bucket the §12.5 line 297 SSE
	// preflight check audits. Empty skips the check.
	MinIOBucket string
	// MinIOEncryptionProber, when non-nil and a bucket name is
	// configured, runs the §12.5 line 297 SSE check against the
	// artifact bucket. The lenny-preflight Job constructs a real
	// prober against MinIO; tests pass a fake.
	MinIOEncryptionProber MinIOEncryptionProber
	// RequiredRuntimeClasses lists the §5.3 RuntimeClasses the install
	// requires (one per enabled, externally-managed
	// runtimeClasses.profiles entry). Empty skips the §5.3 line 676
	// RuntimeClass presence check.
	RequiredRuntimeClasses []RuntimeClassRequirement
	// Playground holds the §27.2 playground.* chart values the
	// playground-config check evaluates. The check runs
	// unconditionally; it is a no-op when Playground.Enabled is false.
	Playground PlaygroundConfig
	// CRDSchemaVersion is the schema version the chart release expects
	// every installed Lenny CRD to declare via the
	// `lenny.dev/schema-version` annotation. Empty falls back to
	// CurrentCRDSchemaVersion so the chart can rely on the binary's
	// embedded default until a future release bumps the value.
	// spec: §10 line 443. F-15.5.12.
	CRDSchemaVersion string
	// CRDNames overrides the default LennyCRDNames set the
	// schema-version check fans across. Empty falls back to the
	// chart-shipped CRDs.
	// spec: §10 line 443. F-15.5.12.
	CRDNames []string
	// Environment is the chart `environment` value (dev | staging |
	// prod). It feeds the §5.3 line 669 cosign-production advisory; a
	// production-or-staging install with cosign disabled gets a
	// non-blocking WARNING. F-5.3.5.
	Environment string
}

// CheckResult pairs a §17.9 check name with its outcome.
type CheckResult struct {
	// Name identifies the check.
	Name string
	// Decision is the check outcome.
	Decision Decision
}

// Failed reports whether any check in the report failed.
func Failed(report []CheckResult) bool {
	for _, r := range report {
		if !r.Decision.Passed {
			return true
		}
	}
	return false
}

// Run gathers the cluster state the §17.9 admission-plane checks need
// and runs them. A cluster read that fails is surfaced as a failed
// check, consistent with the fail-closed posture of the preflight Job.
func Run(ctx context.Context, reader client.Reader, cfg Config) []CheckResult {
	report := make([]CheckResult, 0, 11)

	// §5.3 line 669 — image provenance verification is a prerequisite
	// for production and staging deployments. A pure-function advisory
	// over the chart values: a production-or-staging install with
	// cosign disabled gets a non-blocking WARNING, mirroring the §17.6
	// disk-encryption posture warning. F-5.3.5.
	report = append(report, CheckResult{
		Name:     "cosign-production-posture",
		Decision: CheckCosignProduction(cfg.Environment, cfg.Features.CosignVerify),
	})

	if deployed, err := gatherWebhooks(ctx, reader); err != nil {
		report = append(report, CheckResult{
			Name:     "admission-webhook-inventory",
			Decision: Decision{Reason: "list ValidatingWebhookConfigurations: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "admission-webhook-inventory",
			Decision: CheckAdmissionWebhooks(ExpectedValidatingWebhooks(cfg.Features), deployed),
		})
	}

	if stamp, err := gatherPhaseStamp(ctx, reader, cfg.Namespace); err != nil {
		report = append(report, CheckResult{
			Name:     "phase-stamp-consistency",
			Decision: Decision{Reason: "read phase-stamp ConfigMap: " + err.Error()},
		})
	} else {
		incoming := map[string]bool{
			"llmProxy":       cfg.Features.LLMProxy,
			"drainReadiness": cfg.Features.DrainReadiness,
			"compliance":     cfg.Features.Compliance,
		}
		report = append(report, CheckResult{
			Name:     "phase-stamp-consistency",
			Decision: CheckPhaseStamp(incoming, stamp, cfg.AcceptDowngrade),
		})
	}

	if pods, err := gatherWorkloadPodSpecs(ctx, reader, cfg.Namespace); err != nil {
		report = append(report, CheckResult{
			Name:     "host-sharing-flags",
			Decision: Decision{Reason: "list Lenny-managed workloads: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "host-sharing-flags",
			Decision: CheckHostSharing(pods),
		})
	}

	// §13.2 NetworkPolicy selector-consistency and parity audits. A
	// failed List is surfaced as a failure on every audit it would
	// feed, keeping the fail-closed posture.
	npChecks := []string{
		"networkpolicy-selector-consistency",
		"networkpolicy-dns-podselector-parity",
		"networkpolicy-ipblock-family-parity",
		"networkpolicy-ssrf-private-range-parity",
		"networkpolicy-cluster-cidr-symmetry",
		"networkpolicy-ops-egress-selector-parity",
	}
	if policies, err := gatherNetworkPolicies(ctx, reader); err != nil {
		for _, name := range npChecks {
			report = append(report, CheckResult{
				Name:     name,
				Decision: Decision{Reason: "list Lenny-managed NetworkPolicies: " + err.Error()},
			})
		}
	} else {
		report = append(
			report,
			CheckResult{Name: "networkpolicy-selector-consistency", Decision: CheckSelectorConsistency(policies)},
			CheckResult{Name: "networkpolicy-dns-podselector-parity", Decision: CheckDNSPodSelectorParity(policies)},
			CheckResult{Name: "networkpolicy-ipblock-family-parity", Decision: CheckIPBlockFamilyParity(policies)},
			CheckResult{Name: "networkpolicy-ssrf-private-range-parity", Decision: CheckSSRFPrivateRangeParity(policies)},
			CheckResult{Name: "networkpolicy-cluster-cidr-symmetry", Decision: CheckClusterCIDRSymmetry(policies)},
			CheckResult{Name: "networkpolicy-ops-egress-selector-parity", Decision: CheckOpsEgressSelectorParity(policies)},
		)
	}

	// §13.2 / §10.3 NET-064 deployment-identity uniqueness audits.
	if gws, err := gatherGatewayIdentities(ctx, reader); err != nil {
		for _, name := range []string{"spiffe-trust-domain-uniqueness", "sa-token-audience-uniqueness"} {
			report = append(report, CheckResult{
				Name:     name,
				Decision: Decision{Reason: "list lenny-gateway Deployments: " + err.Error()},
			})
		}
	} else {
		report = append(
			report,
			CheckResult{
				Name:     "spiffe-trust-domain-uniqueness",
				Decision: CheckSPIFFETrustDomainUniqueness(cfg.SPIFFETrustDomain, cfg.Namespace, gws),
			},
			CheckResult{
				Name:     "sa-token-audience-uniqueness",
				Decision: CheckSATokenAudienceUniqueness(cfg.SATokenAudience, cfg.Namespace, gws),
			},
		)
	}

	// §12.5 line 297 — MinIO server-side encryption posture audit.
	// Runs only when a bucket is configured and the chart wires the
	// prober. A regulated complianceProfile (soc2 | fedramp | hipaa)
	// fails closed on absent SSE; non-regulated installs surface the
	// posture as advisory and pass.
	if cfg.MinIOBucket != "" && cfg.MinIOEncryptionProber != nil {
		report = append(report, CheckResult{
			Name: "minio-server-side-encryption",
			Decision: MinIOEncryptionCheck{
				Bucket:            cfg.MinIOBucket,
				ComplianceProfile: cfg.ComplianceProfile,
				Prober:            cfg.MinIOEncryptionProber,
			}.Decide(ctx),
		})
	}

	// §5.3 line 676 — required RuntimeClass presence. The chart passes
	// one requirement per enabled, externally-managed isolation profile;
	// an absent RuntimeClass fails the install fail-closed before the
	// first warm pod create would be rejected by the API server.
	if len(cfg.RequiredRuntimeClasses) > 0 {
		if existing, err := gatherRuntimeClasses(ctx, reader); err != nil {
			report = append(report, CheckResult{
				Name:     "runtimeclass-presence",
				Decision: Decision{Reason: "list RuntimeClasses: " + err.Error()},
			})
		} else {
			report = append(report, CheckResult{
				Name:     "runtimeclass-presence",
				Decision: CheckRuntimeClasses(cfg.RequiredRuntimeClasses, existing),
			})
		}
	}

	// §5.2 line 516 — node-drain-timeout warning. Existing pools (on
	// upgrade) whose terminationGracePeriodSeconds exceeds the common
	// 600s node drain timeout get an advisory warning. A fresh install
	// has no pools and the check passes cleanly. The check is
	// warning-only: a read failure surfaces as an advisory note and
	// never blocks the install.
	if pools, err := gatherPoolGracePeriods(ctx, reader); err != nil {
		report = append(report, CheckResult{
			Name:     "pool-termination-grace-period",
			Decision: Decision{Passed: true, Reason: "WARNING: list SandboxTemplates: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "pool-termination-grace-period",
			Decision: CheckTerminationGracePeriods(pools),
		})
	}

	// spec: §27.2 lines 41–42 + §27.3 — install-time cross-field
	// rejection of malformed playground configuration plus the
	// non-blocking apiKey-mode acknowledgement warning. The check is a
	// pure function over the chart values, so it runs without any
	// cluster gather. F-27.2.2 / F-27.2.4 / F-27.2.5 / F-27.2.6 /
	// F-27.9.3.
	report = append(report, CheckResult{
		Name:     "playground-config",
		Decision: CheckPlaygroundConfig(cfg.Playground),
	})

	// spec: §10 line 443 — every installed Lenny CRD MUST carry the
	// schema-version annotation matching the version the chart release
	// expects. A stale or absent CRD aborts the install before the
	// gateway and controllers roll. F-15.5.12.
	report = append(report, CheckResult{
		Name: "crd-schema-version",
		Decision: CRDSchemaVersionCheck{
			Expected: cfg.CRDSchemaVersion,
			Names:    cfg.CRDNames,
		}.Decide(ctx, reader),
	})
	return report
}

// gatherGatewayIdentities lists every Lenny-managed lenny-gateway
// Deployment across the cluster and projects each onto its §13.2
// NET-064 deployment-identity annotations. The chart labels the
// gateway Deployment lenny.dev/component: gateway, so the audit scans
// only gateway Deployments and reads their trust-domain and
// SA-token-audience annotations.
func gatherGatewayIdentities(ctx context.Context, reader client.Reader) ([]GatewayIdentity, error) {
	var deploys appsv1.DeploymentList
	if err := reader.List(ctx, &deploys,
		client.MatchingLabels{canonicalComponentLabel: "gateway"}); err != nil {
		return nil, err
	}
	out := make([]GatewayIdentity, 0, len(deploys.Items))
	for i := range deploys.Items {
		d := &deploys.Items[i]
		out = append(out, GatewayIdentity{
			Namespace:         d.Namespace,
			Name:              d.Name,
			SPIFFETrustDomain: d.Annotations[spiffeTrustDomainAnnotation],
			SATokenAudience:   d.Annotations[saTokenAudienceAnnotation],
		})
	}
	return out, nil
}

// gatherWorkloadPodSpecs lists the Lenny-managed Deployments,
// DaemonSets, and Jobs in namespace and projects each pod template
// onto a HostSharingPodSpec.
func gatherWorkloadPodSpecs(ctx context.Context, reader client.Reader, namespace string) ([]HostSharingPodSpec, error) {
	var out []HostSharingPodSpec

	var deploys appsv1.DeploymentList
	if err := reader.List(ctx, &deploys, client.InNamespace(namespace), lennyManagedLabel); err != nil {
		return nil, err
	}
	for i := range deploys.Items {
		d := &deploys.Items[i]
		out = append(out, projectHostSharing("Deployment/"+d.Name, &d.Spec.Template.Spec))
	}

	var daemonsets appsv1.DaemonSetList
	if err := reader.List(ctx, &daemonsets, client.InNamespace(namespace), lennyManagedLabel); err != nil {
		return nil, err
	}
	for i := range daemonsets.Items {
		ds := &daemonsets.Items[i]
		out = append(out, projectHostSharing("DaemonSet/"+ds.Name, &ds.Spec.Template.Spec))
	}

	var jobs batchv1.JobList
	if err := reader.List(ctx, &jobs, client.InNamespace(namespace), lennyManagedLabel); err != nil {
		return nil, err
	}
	for i := range jobs.Items {
		j := &jobs.Items[i]
		out = append(out, projectHostSharing("Job/"+j.Name, &j.Spec.Template.Spec))
	}
	return out, nil
}

// projectHostSharing reduces a pod template to the host-sharing flags
// the §13.1 check inspects.
func projectHostSharing(workload string, spec *corev1.PodSpec) HostSharingPodSpec {
	return HostSharingPodSpec{
		Workload:              workload,
		ShareProcessNamespace: spec.ShareProcessNamespace != nil && *spec.ShareProcessNamespace,
		HostPID:               spec.HostPID,
		HostNetwork:           spec.HostNetwork,
		HostIPC:               spec.HostIPC,
	}
}

// gatherWebhooks lists the lenny-* ValidatingWebhookConfigurations and
// projects each onto a WebhookConfig.
func gatherWebhooks(ctx context.Context, reader client.Reader) ([]WebhookConfig, error) {
	var list admissionregistrationv1.ValidatingWebhookConfigurationList
	if err := reader.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make([]WebhookConfig, 0, len(list.Items))
	for i := range list.Items {
		cfg := &list.Items[i]
		if !strings.HasPrefix(cfg.Name, "lenny-") {
			continue
		}
		out = append(out, projectWebhook(cfg))
	}
	return out, nil
}

// projectWebhook reduces a ValidatingWebhookConfiguration to the fields
// the inventory check inspects. Each Lenny webhook configuration
// carries a single webhook entry, so the projection reads the first.
func projectWebhook(cfg *admissionregistrationv1.ValidatingWebhookConfiguration) WebhookConfig {
	wc := WebhookConfig{Name: cfg.Name}
	if len(cfg.Webhooks) == 0 {
		return wc
	}
	wh := &cfg.Webhooks[0]
	if wh.FailurePolicy != nil {
		wc.FailurePolicy = string(*wh.FailurePolicy)
	}
	wc.HasCABundle = len(wh.ClientConfig.CABundle) > 0
	return wc
}

// gatherPhaseStamp reads and decodes the phase-stamp ConfigMap. A
// missing ConfigMap is the first install, where nothing is recorded
// enabled and no downgrade is possible, so it yields an empty map.
func gatherPhaseStamp(ctx context.Context, reader client.Reader, namespace string) (map[string]PhaseStampEntry, error) {
	var cm corev1.ConfigMap
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: PhaseStampConfigMapName}, &cm)
	if apierrors.IsNotFound(err) {
		return map[string]PhaseStampEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	stamp := make(map[string]PhaseStampEntry, len(cm.Data))
	for key, raw := range cm.Data {
		if key == elicitationFloorKey {
			continue
		}
		var entry PhaseStampEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, fmt.Errorf("phase-stamp key %q is not valid JSON: %w", key, err)
		}
		stamp[key] = entry
	}
	return stamp, nil
}
