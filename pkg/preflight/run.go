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

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
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
	// AgentNamespaces lists the §17.2 agent namespaces (lenny-agents,
	// lenny-agents-kata, and any future additions) that the §13.1
	// host-sharing and credential-fsGroup audits cover in addition to the
	// release namespace. The lenny-preflight Job passes the chart's
	// agentNamespaces[].name values. When empty the host-sharing scan is
	// scoped to the release namespace only and the agent-pod credential
	// audit is skipped.
	//
	// spec: §13.1 line 23 (host-sharing scan covers all Lenny-managed
	// Deployments, DaemonSets, and Jobs), §13.1 line 25
	// (fsGroup/supplementalGroups audit on every agent-pod template).
	// F-13.1.7 / F-13.1.4.
	AgentNamespaces []string
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
	// RedisConfigProber, when non-nil, runs the §12.4
	// maxmemory-policy=noeviction audit against the operator's
	// bring-your-own Redis. The lenny-preflight Job constructs a real
	// prober when --redis-url is supplied; the self-managed profile
	// uses it to verify the policy the cloud Terraform sets natively but
	// the chart cannot enforce on a BYO instance. F-12.4.15.
	RedisConfigProber RedisConfigProber
	// ClusterCIDR carries the §13.2 NET-040 / NET-022 CIDR values the
	// cluster-CIDR-discovery check validates against the kube-apiserver
	// Service ClusterIP and the node pod CIDRs. An empty KubeAPIServerCIDR
	// skips the check (the chart always supplies it). F-13.2.13.
	ClusterCIDR ClusterCIDRConfig
	// OTLP carries the §13.2 OTLP-068 observability values the otlp-tls
	// check evaluates. An empty Endpoint skips the check. F-13.2.9.
	OTLP OTLPTLSConfig
	// OTLPTLSProber, when non-nil and OTLP.TLSEnabled with a configured
	// endpoint, runs the §13.2 OTLP-068 live TLS handshake probe. The
	// lenny-preflight Job constructs a real prober; the scheme guard runs
	// regardless of whether the prober is wired. F-13.2.9.
	OTLPTLSProber OTLPTLSProber
	// OpsAdminTLS carries the §25.4 NET-070 admin-API TLS values the
	// ops-admin-tls check evaluates. An empty Endpoint or
	// InternalEnabled=false skips the check. F-25.4.19.
	OpsAdminTLS OpsAdminTLSConfig
	// OpsAdminTLSProber, when non-nil and OpsAdminTLS.InternalEnabled with
	// a configured endpoint, runs the §25.4 NET-070 live TLS handshake +
	// SAN-coverage probe against the gateway internal admin API. F-25.4.19.
	OpsAdminTLSProber OpsAdminTLSProber
	// DevMode is the global.devMode chart value. It exempts the §12.9
	// line 1050 volume-encryption check (local development volumes). F-12.9.12.
	DevMode bool
	// AttestVolumeEncryption is the preflight.attestVolumeEncryption chart
	// value: the operator's attestation that the BYO Postgres and Redis
	// volumes are backed by encrypted storage when the §12.9 line 1050
	// check cannot verify it directly. F-12.9.12.
	AttestVolumeEncryption bool
	// VolumeEncryptionProber, when non-nil, determines the live §12.9
	// line 1050 encryption posture of the Postgres and Redis volumes. The
	// §17.3 BYO topology has no in-cluster volumes to probe, so v1 leaves
	// it nil and the check routes through the attestation path. F-12.9.12.
	VolumeEncryptionProber VolumeEncryptionProber
	// CertManagerEnabled is the certmanager.enabled chart value: when
	// true the chart relies on the cert-manager-backed mTLS PKI, so the
	// §10.3 line 304 version check requires cert-manager >= v1.12.0. When
	// false the operator manages mTLS through a service mesh and the
	// check is a no-op. F-10.3.12.
	CertManagerEnabled bool
	// CertManagerProber discovers the installed cert-manager version. The
	// lenny-preflight Job wires one that reads the cert-manager CRD
	// version label; a nil prober while CertManagerEnabled routes through
	// the can't-determine advisory. F-10.3.12.
	CertManagerProber CertManagerProber
	// MonitoringFormat is the monitoring.format chart value
	// (prometheusrule | configmap | both). When it selects the Prometheus
	// Operator CRDs but they are not installed, the §16.9 R8 preflight
	// raises a non-blocking advisory that the chart fell back to a
	// ConfigMap. Empty skips the check. F-16.9.4.
	MonitoringFormat string
	// Prometheus carries the §25.4 "Preflight validation" inputs the
	// prometheus-reachability check evaluates (ops.prometheus.url,
	// global.deploymentTier, monitoring.acknowledgeNoPrometheus). The
	// check is non-blocking and emits a tier-specific INFO/WARN. F-25.4.25.
	Prometheus PrometheusConfig
	// PrometheusProber, when non-nil, runs the live §25.4 reachability
	// probe against ops.prometheus.url. A nil prober (skip-network-probes
	// / airgap) treats a configured URL as reachable. F-25.4.25.
	PrometheusProber PrometheusProber
	// SkipNetworkProbes is the preflight.skipNetworkProbes chart value.
	// When true the backend-reachability probes that dial an endpoint
	// outside the kube-apiserver are skipped: the §12.5 MinIO server-side
	// encryption check, the §12.4 BYO-Redis maxmemory-policy audit, the
	// §13.2 OTLP collector TLS handshake, and the §25.4 lenny-ops ->
	// gateway internal-TLS handshake. The cluster-API checks still run.
	// The §17.9.2 airgap answer file sets this so the preflight Job
	// succeeds with no egress to the managed backends. spec: §17.9.2 line
	// 1372. F-17.9.11.
	SkipNetworkProbes bool
	// ObjectStorage carries the §17.9.4 cloud object-storage lifecycle
	// inputs (objectStorage.provider, objectStorage.bucket). An empty or
	// `minio` Provider skips the cloud-object-storage-lifecycle check
	// (MinIO lifecycle is configured by the post-install Job). F-17.9.3.
	ObjectStorage ObjectStorageConfig
	// CloudObjectStorageLifecycleProber, when non-nil and ObjectStorage
	// names a cloud provider, reads the live §17.9.4 lifecycle posture
	// through the provider SDK. A nil prober (skip-network-probes, or a
	// provider whose SDK reader is not wired) routes the check through
	// the advisory path. F-17.9.3.
	CloudObjectStorageLifecycleProber CloudObjectStorageLifecycleProber
	// KubernetesVersion is the API server GitVersion the binary read via
	// the discovery client. Empty skips the §17.6 line 503 minimum-version
	// gate. F-17.6.1.
	KubernetesVersion string
	// Environment is reused by the §17.6 line 517 SIEM advisory (already
	// defined above for the cosign-production advisory).
	//
	// SIEMEndpoint is the audit.siem.endpoint chart value. Empty in a
	// production environment triggers the non-blocking SIEM advisory.
	// F-17.6.1.
	SIEMEndpoint string
	// StorageRouterRegions are the §12.6 data-residency regions the chart
	// declares, each with its configured backends. Empty skips the §17.6
	// line 504 region-coverage audit. F-17.6.1.
	StorageRouterRegions []StorageRouterRegion
	// LegalHold carries the §17.6 line 505 legal-hold per-region escrow
	// inputs. Disabled skips the check. F-17.6.1.
	LegalHold LegalHoldConfig
	// PgBouncerConfigProber, when non-nil, runs the §17.6 lines 487-488
	// PgBouncer pool_mode / connect_query audit against the live PgBouncer
	// admin console. Nil skips the check. F-17.6.1.
	PgBouncerConfigProber PgBouncerConfigProber
	// BillingTriggerProber, when non-nil, runs the §17.6 line 489
	// billing/audit integrity-trigger audit. Nil skips the check.
	// F-17.6.1.
	BillingTriggerProber BillingTriggerProber
	// OpsIngressClusterIssuer is the cert-manager.io/cluster-issuer
	// annotation value on ops.ingress. When set, the §17.6 line 520
	// advisory verifies the ClusterIssuer exists. Empty skips it.
	// F-17.6.1.
	OpsIngressClusterIssuer string
	// Monitoring carries the §17.6 line 521 monitoring-namespace advisory
	// inputs. F-17.6.1.
	Monitoring MonitoringConfig
	// OpsServiceAccount is the fully-qualified lenny-ops SA username
	// (system:serviceaccount:<ns>:lenny-ops-sa) the §17.6 line 519 RBAC
	// audit reviews. F-17.6.1.
	OpsServiceAccount string
	// OpsSARBACProber, when non-nil, runs the §17.6 line 519 lenny-ops-sa
	// SubjectAccessReview audit. Nil skips the check. F-17.6.1.
	OpsSARBACProber OpsSARBACProber
	// IngressController carries the §13.2 NET-038 ingress-controller
	// advisory inputs (ingressControllerNamespace,
	// ingress.controllerPodLabel.key/value). An empty Namespace skips the
	// check. F-13.2.8.
	IngressController IngressControllerConfig
}

// IngressControllerConfig carries the §13.2 NET-038 chart values the
// ingress-controller advisory evaluates against the cluster.
//
// spec: §13.2 line 292. F-13.2.8.
type IngressControllerConfig struct {
	// Namespace is the ingressControllerNamespace value.
	Namespace string
	// PodLabelKey is the ingress.controllerPodLabel.key value.
	PodLabelKey string
	// PodLabelValue is the ingress.controllerPodLabel.value value.
	PodLabelValue string
}

// MonitoringConfig carries the §17.6 line 521 monitoring.* chart values
// the monitoring-namespace advisory evaluates.
//
// spec: §17.6 line 521. F-17.6.1.
type MonitoringConfig struct {
	// Namespace is the monitoring.namespace value.
	Namespace string
	// PodLabel is the monitoring.podLabel selector (key=value).
	PodLabel string
}

// LegalHoldConfig carries the §17.6 line 505 legal-hold per-region
// escrow chart values the legal-hold-escrow check evaluates.
//
// spec: §17.6 line 505; §12.8 Phase 3.5. F-17.6.1.
type LegalHoldConfig struct {
	// Enabled is the legalHold.enabled chart value.
	Enabled bool
	// Regions is the full set of declared StorageRouter region names.
	Regions []string
	// EscrowRegions is the set of region names that declare an escrow KEK.
	EscrowRegions map[string]bool
}

// ObjectStorageConfig carries the §17.9.4 chart objectStorage.* values
// the cloud-object-storage-lifecycle check evaluates.
//
// spec: §17.9.4; §17.6 line 494. F-17.9.3.
type ObjectStorageConfig struct {
	// Provider is the objectStorage.provider value
	// (minio | s3 | gcs | azure).
	Provider string
	// Bucket is the cloud bucket / container name.
	Bucket string
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

	// §13.1 line 23 — the host-sharing audit covers all Lenny-managed
	// Deployments, DaemonSets, and Jobs in the release namespace and the
	// §17.2 agent namespaces, plus the bare agent Pods the controller
	// spawns from Sandbox CRs. Scoping the scan to the release namespace
	// alone let an agent-namespace pod with hostPID escape the gate
	// (F-13.1.7). The same agent-Pod scan feeds the §13.1 line 25
	// credential-fsGroup audit (F-13.1.4).
	workloadNS := dedupeNamespaces(append([]string{cfg.Namespace}, cfg.AgentNamespaces...))
	workloads, werr := gatherWorkloadPodSpecs(ctx, reader, workloadNS)
	agentPods, aerr := gatherAgentPods(ctx, reader, cfg.AgentNamespaces)
	switch {
	case werr != nil:
		report = append(report, CheckResult{
			Name:     "host-sharing-flags",
			Decision: Decision{Reason: "list Lenny-managed workloads: " + werr.Error()},
		})
	case aerr != nil:
		report = append(report, CheckResult{
			Name:     "host-sharing-flags",
			Decision: Decision{Reason: "list Lenny-managed agent pods: " + aerr.Error()},
		})
	default:
		hostSharing := workloads
		for _, p := range agentPods {
			hostSharing = append(hostSharing, p.HostSharingPodSpec)
		}
		report = append(report, CheckResult{
			Name:     "host-sharing-flags",
			Decision: CheckHostSharing(hostSharing),
		})
	}

	// §13.1 line 25 — the lenny-preflight Job validates the presence of
	// the lenny-cred-readers fsGroup and supplementalGroups on every
	// agent-pod template, the install-time backstop to the admission
	// webhook's per-CREATE check (F-13.1.4). The audit runs only when the
	// chart names the agent namespaces; a fresh install has no agent pods
	// and the check passes cleanly.
	if len(cfg.AgentNamespaces) > 0 {
		if aerr != nil {
			report = append(report, CheckResult{
				Name:     "agent-pod-cred-fsgroup",
				Decision: Decision{Reason: "list Lenny-managed agent pods: " + aerr.Error()},
			})
		} else {
			report = append(report, CheckResult{
				Name:     "agent-pod-cred-fsgroup",
				Decision: CheckAgentPodCredFSGroup(agentPods, podspec.CredReadersGID),
			})
		}
	}

	// §13.1 lines 6-8 — the pod-security baseline (non-root, all
	// capabilities dropped, read-only root filesystem) on every
	// Lenny-managed workload in the release namespace. The chart authors
	// these settings, so a failure means a value override, a patched Job,
	// or an injected sidecar weakened the baseline with no other signal.
	// F-13.1.12.
	if sec, err := gatherWorkloadPodSecurity(ctx, reader, []string{cfg.Namespace}); err != nil {
		report = append(report, CheckResult{
			Name:     "pod-security-baseline",
			Decision: Decision{Reason: "list Lenny-managed workloads: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "pod-security-baseline",
			Decision: CheckPodSecurityBaseline(sec),
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
	if !cfg.SkipNetworkProbes && cfg.MinIOBucket != "" && cfg.MinIOEncryptionProber != nil {
		report = append(report, CheckResult{
			Name: "minio-server-side-encryption",
			Decision: MinIOEncryptionCheck{
				Bucket:            cfg.MinIOBucket,
				ComplianceProfile: cfg.ComplianceProfile,
				Prober:            cfg.MinIOEncryptionProber,
			}.Decide(ctx),
		})
	}

	// §17.9.4 / §17.6 line 494 — cloud object-storage lifecycle rules.
	// The check self-skips for objectStorage.provider=minio (the
	// post-install Job configures MinIO via `mc ilm add`). For a cloud
	// provider it verifies bucket versioning + noncurrent-version /
	// delete-marker expiration through a provider SDK read. The prober is
	// nil under skip-network-probes (airgap) or for a provider without a
	// wired SDK reader, which routes the check through its advisory path.
	{
		prober := cfg.CloudObjectStorageLifecycleProber
		if cfg.SkipNetworkProbes {
			prober = nil
		}
		report = append(report, CheckResult{
			Name: "cloud-object-storage-lifecycle",
			Decision: CloudObjectStorageLifecycleCheck{
				Provider: cfg.ObjectStorage.Provider,
				Bucket:   cfg.ObjectStorage.Bucket,
				Prober:   prober,
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

	// spec: §27.9 line 255 — the `playground.apiKeyMode` row emits a
	// non-blocking WARNING when the playground ships apiKey auth mode
	// outside dev mode without playground.acknowledgeApiKeyMode, the
	// single install-time touchpoint for the paste-form phishing-surface
	// acknowledgement. Surfaced as its own row so the operator-visible
	// report carries the exact name the spec names. F-27.9.2.
	report = append(report, CheckResult{
		Name:     "playground.apiKeyMode",
		Decision: CheckPlaygroundAPIKeyMode(cfg.Playground),
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

	// spec: §16.9 R8 — when monitoring.format selects the Prometheus
	// Operator CRDs (prometheusrule/both) but they are not installed, the
	// chart falls back to a ConfigMap rule file and skips the scrape
	// monitors at render time. The preflight names the missing CRDs so the
	// fallback is not silent. Advisory only (the install is not aborted).
	// F-16.9.4.
	report = append(report, CheckResult{
		Name:     "prometheus-operator-crds",
		Decision: PrometheusOperatorCRDCheck{Format: cfg.MonitoringFormat}.Decide(ctx, reader),
	})

	// spec: §10.3 line 304 — when the cert-manager-backed mTLS PKI is
	// enabled, cert-manager >= v1.12.0 must be installed; an absent or
	// stale cert-manager aborts the install rather than failing silently
	// at the first Certificate-resource creation. F-10.3.12.
	report = append(report, CheckResult{
		Name: "cert-manager-version",
		Decision: CertManagerVersionCheck{
			Required: cfg.CertManagerEnabled,
			Prober:   cfg.CertManagerProber,
		}.Decide(ctx),
	})

	// spec: §15.5 line 2438 / §17.2 line 58 — the lenny-crd-conversion
	// webhook is a Phase 3.5 baseline. The admission-webhook-inventory
	// check excludes it (it is a CRD conversion endpoint, not a
	// ValidatingWebhookConfiguration), so verify its Service presence and
	// Deployment readiness here. A missing or unready webhook aborts the
	// upgrade because it makes every multi-version CRD operation fail.
	// F-15.5.3 / F-17.2.4 / F-10.5.6.
	if state, err := gatherConversionWebhook(ctx, reader, cfg.Namespace); err != nil {
		report = append(report, CheckResult{
			Name:     "conversion-webhook-availability",
			Decision: Decision{Reason: "read lenny-crd-conversion workload: " + err.Error()},
		})
	} else {
		report = append(report, CheckResult{
			Name:     "conversion-webhook-availability",
			Decision: CheckConversionWebhook(state),
		})
	}

	// spec: §12.4 — the billing stream and per-tenant counters must not
	// silently evict. Runs only when a BYO Redis URL was supplied; the
	// cloud Terraform sets maxmemory-policy=noeviction natively, but the
	// self-managed profile's operator-supplied Redis is unverified
	// otherwise. F-12.4.15.
	if !cfg.SkipNetworkProbes && cfg.RedisConfigProber != nil {
		report = append(report, CheckResult{
			Name:     "redis-maxmemory-policy",
			Decision: CheckRedisMaxmemoryPolicy(ctx, cfg.RedisConfigProber),
		})
	}

	// §13.2 lines 230-262 (NET-040) / 416 (NET-022) — the kube-apiserver
	// Service ClusterIP must fall within kubeApiServerCIDR and the cluster
	// service CIDR exclusion, and node pod CIDRs within the pod CIDR
	// exclusion. A mismatch means the gateway cannot reach the control
	// plane or the broad external-HTTPS egress does not exclude in-cluster
	// IPs (SSRF). The chart always supplies kubeApiServerCIDR. F-13.2.13.
	if cfg.ClusterCIDR.KubeAPIServerCIDR != "" {
		if clusterIP, podCIDRs, err := gatherClusterCIDRDiscovery(ctx, reader); err != nil {
			report = append(report, CheckResult{
				Name:     "cluster-cidr-discovery",
				Decision: Decision{Reason: "read kubernetes.default Service / Nodes: " + err.Error()},
			})
		} else {
			report = append(report, CheckResult{
				Name:     "cluster-cidr-discovery",
				Decision: CheckClusterCIDRDiscovery(clusterIP, podCIDRs, cfg.ClusterCIDR),
			})
		}
	}

	// §13.2 lines 176-178 (OTLP-068) — when OTLP export runs over TLS, the
	// endpoint scheme must not be http:// and the live collector TLS
	// handshake must complete with a SAN matching the endpoint. Runs only
	// when an endpoint is configured; the scheme guard is a pure check, the
	// handshake runs only when the Job wires a prober. F-13.2.9.
	if !cfg.SkipNetworkProbes && cfg.OTLP.Endpoint != "" {
		report = append(report, CheckResult{
			Name: "otlp-tls",
			Decision: OTLPTLSCheck{
				Config: cfg.OTLP,
				Prober: cfg.OTLPTLSProber,
			}.Decide(ctx),
		})
	}

	// §25.4 lines 2544-2546 (NET-070) — when ops.tls.internalEnabled is
	// set, the lenny-ops → gateway admin-API link runs over TLS; the
	// ops-admin-tls probe verifies the gateway internal-TLS handshake
	// completes and the server certificate's SAN covers the lenny-gateway
	// ClusterIP hostname. Skipped when internal TLS is disabled (the
	// acknowledged plaintext path). F-25.4.19.
	if !cfg.SkipNetworkProbes && cfg.OpsAdminTLS.InternalEnabled && cfg.OpsAdminTLS.Endpoint != "" {
		report = append(report, CheckResult{
			Name: "ops-admin-tls",
			Decision: OpsAdminTLSCheck{
				Config: cfg.OpsAdminTLS,
				Prober: cfg.OpsAdminTLSProber,
			}.Decide(ctx),
		})
	}

	// §25.4 lines 1462-1470 — the prometheus-reachability check is
	// non-blocking and tier-specific: INFO at Tier 1, WARN at Tier 2/3
	// (suppressed by monitoring.acknowledgeNoPrometheus). The live dial
	// is skipped under skip-network-probes (a configured URL is then
	// assumed reachable); the advisory still runs. F-25.4.25.
	prober := cfg.PrometheusProber
	if cfg.SkipNetworkProbes {
		prober = nil
	}
	report = append(report, CheckResult{
		Name: "prometheus-reachability",
		Decision: PrometheusReachabilityCheck{
			Config: cfg.Prometheus,
			Prober: prober,
		}.Decide(ctx),
	})

	// §12.9 line 1050 — the T2 storage-layer encryption baseline: the
	// Postgres and Redis volumes must be backed by encrypted storage. The
	// check runs unconditionally; global.devMode exempts it, and the §17.3
	// BYO topology routes through the preflight.attestVolumeEncryption
	// attestation when no in-cluster volume can be probed. F-12.9.12.
	report = append(report, CheckResult{
		Name: "volume-encryption",
		Decision: VolumeEncryptionCheck{
			DevMode:  cfg.DevMode,
			Attested: cfg.AttestVolumeEncryption,
			Prober:   cfg.VolumeEncryptionProber,
		}.Decide(ctx),
	})

	// §17.6 line 503 — the API server must be >= 1.27. The binary reads
	// the version via the discovery client; an empty/unparseable value is
	// non-blocking. F-17.6.1.
	report = append(report, CheckResult{
		Name:     "kubernetes-version",
		Decision: KubernetesVersionCheck{Version: cfg.KubernetesVersion}.Decide(),
	})

	// §17.6 line 517 — non-blocking SIEM-endpoint advisory: a production
	// install with no audit.siem.endpoint stores audit logs in Postgres
	// only, which is below the compliance-grade integrity bar. F-17.6.1.
	report = append(report, CheckResult{
		Name:     "siem-endpoint",
		Decision: SIEMEndpointCheck{Environment: cfg.Environment, SIEMEndpoint: cfg.SIEMEndpoint}.Decide(),
	})

	// §17.6 line 504 — every declared StorageRouter region must carry a
	// Postgres and an object-storage backend. Empty region set skips.
	// F-17.6.1.
	report = append(report, CheckResult{
		Name:     "storagerouter-region-coverage",
		Decision: StorageRouterRegionCoverageCheck{Regions: cfg.StorageRouterRegions}.Decide(),
	})

	// §17.6 line 505 — when legal hold is enabled every region must have
	// a region-scoped escrow KEK (§12.8 Phase 3.5). Disabled skips.
	// F-17.6.1.
	report = append(report, CheckResult{
		Name: "legal-hold-escrow",
		Decision: LegalHoldEscrowCheck{
			Enabled:       cfg.LegalHold.Enabled,
			Regions:       cfg.LegalHold.Regions,
			EscrowRegions: cfg.LegalHold.EscrowRegions,
		}.Decide(),
	})

	// §17.6 lines 487-488 — PgBouncer pool_mode==transaction + tenant
	// sentinel connect_query. Runs only when a live PgBouncer admin
	// connection is wired (nil prober skips). F-17.6.1.
	report = append(report, CheckResult{
		Name:     "pgbouncer-config",
		Decision: PgBouncerConfigCheck{Prober: cfg.PgBouncerConfigProber}.Decide(ctx),
	})

	// §17.6 line 489 — billing/audit integrity triggers enabled. Runs
	// only when a billing-database connection is wired (nil prober skips).
	// F-17.6.1.
	report = append(report, CheckResult{
		Name:     "billing-audit-trigger",
		Decision: BillingTriggerCheck{Prober: cfg.BillingTriggerProber}.Decide(ctx),
	})

	// §17.6 lines 501-502 — every existing agent namespace must carry a
	// ResourceQuota and a LimitRange. Not-yet-created namespaces (fresh
	// install, chart applies them in the main phase) are skipped.
	// F-17.6.1.
	if len(cfg.AgentNamespaces) > 0 {
		if statuses, err := gatherNamespaceGovernance(ctx, reader, cfg.AgentNamespaces); err != nil {
			report = append(report,
				CheckResult{Name: "namespace-resourcequota", Decision: Decision{Reason: "read agent-namespace resource governance: " + err.Error()}},
				CheckResult{Name: "namespace-limitrange", Decision: Decision{Reason: "read agent-namespace resource governance: " + err.Error()}},
			)
		} else {
			report = append(report,
				CheckResult{Name: "namespace-resourcequota", Decision: CheckNamespaceResourceQuotas(statuses)},
				CheckResult{Name: "namespace-limitrange", Decision: CheckNamespaceLimitRanges(statuses)},
			)
		}
	}

	// §17.6 line 520 — non-blocking ops.ingress ClusterIssuer advisory.
	// Runs only when the annotation names an issuer. F-17.6.1.
	if cfg.OpsIngressClusterIssuer != "" {
		exists, err := clusterIssuerExists(ctx, reader, cfg.OpsIngressClusterIssuer)
		if err != nil {
			report = append(report, CheckResult{Name: "ops-ingress-clusterissuer", Decision: Decision{Reason: "read ClusterIssuer: " + err.Error()}})
		} else {
			report = append(report, CheckResult{
				Name:     "ops-ingress-clusterissuer",
				Decision: OpsIngressClusterIssuerCheck{IssuerName: cfg.OpsIngressClusterIssuer, Exists: exists}.Decide(),
			})
		}
	}

	// §17.6 line 521 — non-blocking monitoring-namespace advisory. Runs
	// only when both a monitoring namespace and a pod label are
	// configured. F-17.6.1.
	if cfg.Monitoring.Namespace != "" && cfg.Monitoring.PodLabel != "" {
		hasPod, err := monitoringPodPresent(ctx, reader, cfg.Monitoring.Namespace, cfg.Monitoring.PodLabel)
		if err != nil {
			report = append(report, CheckResult{Name: "monitoring-namespace", Decision: Decision{Reason: "list monitoring pods: " + err.Error()}})
		} else {
			report = append(report, CheckResult{
				Name: "monitoring-namespace",
				Decision: MonitoringNamespaceCheck{
					Namespace:      cfg.Monitoring.Namespace,
					PodLabel:       cfg.Monitoring.PodLabel,
					HasMatchingPod: hasPod,
				}.Decide(),
			})
		}
	}

	// §17.6 line 519 — lenny-ops-sa RBAC audit. Runs only when a
	// SubjectAccessReview prober is wired (nil skips). F-17.6.1.
	report = append(report, CheckResult{
		Name: "lenny-ops-sa-rbac",
		Decision: OpsSARBACCheck{
			ServiceAccount: cfg.OpsServiceAccount,
			Rules:          CanonicalOpsSARBACRules(cfg.Namespace),
			Prober:         cfg.OpsSARBACProber,
		}.Decide(ctx),
	})

	// §13.2 line 292 NET-038 — non-blocking ingress-controller advisory.
	// Validates that the ingressControllerNamespace exists and runs at
	// least one pod carrying the configured controllerPodLabel, so the
	// allow-gateway-ingress NetworkPolicy actually admits external HTTPS
	// to the gateway. A read error degrades to a non-blocking WARNING so a
	// transient list failure does not abort an otherwise-valid install.
	// F-13.2.8.
	if cfg.IngressController.Namespace != "" {
		nsExists, hasPod, err := gatherIngressController(ctx, reader,
			cfg.IngressController.Namespace, cfg.IngressController.PodLabelKey, cfg.IngressController.PodLabelValue)
		if err != nil {
			report = append(report, CheckResult{Name: "ingress-controller", Decision: Decision{Passed: true,
				Reason: "WARNING: could not determine ingress-controller posture: " + err.Error()}})
		} else {
			report = append(report, CheckResult{
				Name: "ingress-controller",
				Decision: IngressControllerCheck{
					Namespace:               cfg.IngressController.Namespace,
					PodLabelKey:             cfg.IngressController.PodLabelKey,
					PodLabelValue:           cfg.IngressController.PodLabelValue,
					NamespaceExists:         nsExists,
					HasRunningControllerPod: hasPod,
				}.Decide(),
			})
		}
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

// forEachLennyWorkload visits the pod template of every Lenny-managed
// Deployment, DaemonSet, and Job in each namespace, passing a
// namespace-scoped workload identifier so a per-workload finding is
// unambiguous. It is the shared lister the host-sharing (§13.1 line 23)
// and pod-security-baseline (§13.1 lines 6-8) projections both build on,
// so each gather makes one pass over the same workloads.
func forEachLennyWorkload(ctx context.Context, reader client.Reader, namespaces []string, visit func(workload string, spec *corev1.PodSpec)) error {
	for _, namespace := range namespaces {
		var deploys appsv1.DeploymentList
		if err := reader.List(ctx, &deploys, client.InNamespace(namespace), lennyManagedLabel); err != nil {
			return err
		}
		for i := range deploys.Items {
			d := &deploys.Items[i]
			visit(namespace+"/Deployment/"+d.Name, &d.Spec.Template.Spec)
		}

		var daemonsets appsv1.DaemonSetList
		if err := reader.List(ctx, &daemonsets, client.InNamespace(namespace), lennyManagedLabel); err != nil {
			return err
		}
		for i := range daemonsets.Items {
			ds := &daemonsets.Items[i]
			visit(namespace+"/DaemonSet/"+ds.Name, &ds.Spec.Template.Spec)
		}

		var jobs batchv1.JobList
		if err := reader.List(ctx, &jobs, client.InNamespace(namespace), lennyManagedLabel); err != nil {
			return err
		}
		for i := range jobs.Items {
			j := &jobs.Items[i]
			visit(namespace+"/Job/"+j.Name, &j.Spec.Template.Spec)
		}
	}
	return nil
}

// gatherWorkloadPodSpecs lists the Lenny-managed Deployments,
// DaemonSets, and Jobs in each namespace and projects each pod template
// onto a HostSharingPodSpec.
//
// spec: §13.1 line 23 — the audit covers every Lenny-managed workload,
// not only those in the release namespace. F-13.1.7.
func gatherWorkloadPodSpecs(ctx context.Context, reader client.Reader, namespaces []string) ([]HostSharingPodSpec, error) {
	var out []HostSharingPodSpec
	err := forEachLennyWorkload(ctx, reader, namespaces, func(workload string, spec *corev1.PodSpec) {
		out = append(out, projectHostSharing(workload, spec))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// gatherWorkloadPodSecurity lists the Lenny-managed workloads in each
// namespace and projects each pod template onto its §13.1 pod-security
// baseline. The release namespace alone is passed: the §13.1 baseline
// for agent-namespace pods is enforced by the controller podspec and the
// agent-pod host-sharing / credential audits.
//
// spec: §13.1 lines 6-8, 16. F-13.1.12.
func gatherWorkloadPodSecurity(ctx context.Context, reader client.Reader, namespaces []string) ([]WorkloadPodSecurity, error) {
	var out []WorkloadPodSecurity
	err := forEachLennyWorkload(ctx, reader, namespaces, func(workload string, spec *corev1.PodSpec) {
		out = append(out, projectPodSecurity(workload, spec))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// gatherClusterCIDRDiscovery reads the §13.2 cluster addressing the
// CIDR-discovery check validates: the kubernetes.default Service
// ClusterIP (NET-040) and every node-reported pod CIDR (NET-022).
//
// spec: §13.2 lines 230-262. F-13.2.13.
func gatherClusterCIDRDiscovery(ctx context.Context, reader client.Reader) (clusterIP string, podCIDRs []string, err error) {
	var svc corev1.Service
	if err := reader.Get(ctx, client.ObjectKey{Namespace: "default", Name: "kubernetes"}, &svc); err != nil {
		return "", nil, err
	}
	var nodes corev1.NodeList
	if err := reader.List(ctx, &nodes); err != nil {
		return "", nil, err
	}
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if len(n.Spec.PodCIDRs) > 0 {
			podCIDRs = append(podCIDRs, n.Spec.PodCIDRs...)
			continue
		}
		if n.Spec.PodCIDR != "" {
			podCIDRs = append(podCIDRs, n.Spec.PodCIDR)
		}
	}
	return svc.Spec.ClusterIP, podCIDRs, nil
}

// agentManagedLabel selects the bare agent Pods the WarmPoolController
// stamps `lenny.dev/managed: "true"` (warmpool.LabelManaged); agent pods
// do not carry the chart's app.kubernetes.io/name label, so the
// host-sharing and credential audits select them by the controller-owned
// managed label instead.
var agentManagedLabel = client.MatchingLabels{"lenny.dev/managed": "true"}

// gatherAgentPods lists the controller-spawned agent Pods in each agent
// namespace and projects each onto an AgentPodSpec carrying both the
// §13.1 host-sharing flags and the pod-level fsGroup / supplementalGroups
// the §13.1 credential-delivery path requires. An empty namespace list
// returns no pods so a deployment that does not declare agentNamespaces
// runs neither agent-scoped audit.
//
// spec: §13.1 line 16 (CRD-driven RuntimePool pods are in the
// forbidden-flag set), §13.1 line 25 (fsGroup/supplementalGroups on every
// agent-pod template). F-13.1.7 / F-13.1.4.
func gatherAgentPods(ctx context.Context, reader client.Reader, namespaces []string) ([]AgentPodSpec, error) {
	var out []AgentPodSpec
	for _, namespace := range namespaces {
		var pods corev1.PodList
		if err := reader.List(ctx, &pods, client.InNamespace(namespace), agentManagedLabel); err != nil {
			return nil, err
		}
		for i := range pods.Items {
			p := &pods.Items[i]
			out = append(out, projectAgentPod(namespace+"/Pod/"+p.Name, &p.Spec))
		}
	}
	return out, nil
}

// dedupeNamespaces returns ns with empty entries dropped and duplicates
// removed, preserving first-seen order. The release namespace can also
// appear in agentNamespaces on misconfigured installs; the host-sharing
// scan must not list it twice.
func dedupeNamespaces(ns []string) []string {
	seen := make(map[string]bool, len(ns))
	out := ns[:0]
	for _, n := range ns {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
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
