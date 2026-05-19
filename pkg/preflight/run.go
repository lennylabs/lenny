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
	report := make([]CheckResult, 0, 10)

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
		report = append(report,
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
		report = append(report,
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
