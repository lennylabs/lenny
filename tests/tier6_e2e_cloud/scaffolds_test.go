// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 cloud-verify scaffolds. Each test corresponds to one of the
// TESTING.md §12.6 suites. The cloud-guard is
// cloud.SkipUnlessAvailable: a test only proceeds when
// LENNY_CLOUD_PROVIDER is set, the provider CLI is on PATH, and the
// CLI is authenticated. After the cloud-guard each test calls t.Skip
// with a "blocked:" reason that names the prerequisite the cloud
// harness needs and that is not built yet. None of the per-suite
// prerequisites — the per-provider Terraform under
// deploy/terraform/cloud/<provider>/, the cloud-KMS Provider beyond
// the documented seam, the cloud-managed object storage backend
// beyond pkg/blobstore/miniostore, the cloud-managed TokenStore
// adapter, the OTLP exporter wired to the provider-native collector,
// or the billing-export sink — exists in v1. The skip messages spell
// out the specific missing piece so the diagnosis is actionable when
// a Wave 7 implementer picks up the suite.

package tier6_e2e_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/cloud"
)

// requireCloud is the cloud-guard every tier-6 test starts with. It
// skips when LENNY_CLOUD_PROVIDER is unset, the provider CLI is
// missing, or the CLI is unauthenticated. The cloud helper's skip
// message names the missing piece for either of those cases.
func requireCloud(t *testing.T) cloud.Provider {
	t.Helper()
	p := cloud.FromEnv()
	cloud.SkipUnlessAvailable(t, p)
	return p
}

// spec: 6.1 (pre-warmed pod anatomy: RuntimeClass selection — gVisor variant)
// diagnosis: the §6.1 sandboxed isolation profile does not place
// agent pods on the provider's gVisor (or provider-equivalent)
// sandbox node pool. The TESTING.md §12.6 gvisor_isolation suite
// asserts that the default sandboxed profile lands a claim on a
// gVisor-runtime node and that the documented syscall-filtering and
// capability-dropping invariants hold.
func TestGvisorIsolation(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the per-provider sandbox node pool and the matching " +
		"isolation-profile-sandboxed pool wiring are not built; the " +
		"per-provider Terraform under deploy/terraform/cloud/<provider>/ " +
		"is absent and scripts/cloud/<provider>/up.sh is a placeholder")
}

// spec: 6.1 (pre-warmed pod anatomy: RuntimeClass selection — Kata / microVM variant)
// diagnosis: the §6.1 Kata or microVM RuntimeClass path does not
// boot and drain a pod within the documented latency budget. The
// TESTING.md §12.6 kata_isolation suite asserts that a pod backed
// by Kata (GKE/AKS) or Firecracker-via-Fargate (EKS) boots, accepts
// a claim, and tears down inside the §6.3 startup-latency envelope.
func TestKataIsolation(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the Kata or microVM RuntimeClass and the matching " +
		"isolation-profile pool wiring are not built; the per-provider " +
		"Terraform under deploy/terraform/cloud/<provider>/ is absent " +
		"and scripts/cloud/<provider>/up.sh is a placeholder")
}

// spec: 17.3 (disaster recovery: zone-local Postgres failover with RPO=0 and RTO under 30s)
// diagnosis: the §17.3 multi-AZ Postgres failover path does not
// preserve an in-flight session across a zone outage. The
// TESTING.md §12.6 multi_zone_dr suite asserts that the provider's
// native HA Postgres offering (Cloud SQL HA, RDS Multi-AZ, Azure
// Database for PostgreSQL Flexible Server with HA) fails over with
// RPO=0 and RTO under 30 seconds while a session is mid-stream.
func TestMultiZoneDR(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: a provider-native HA Postgres instance and the " +
		"failover injector are not wired; the per-provider Terraform " +
		"under deploy/terraform/cloud/<provider>/ is absent")
}

// spec: 17.5 (cloud portability: provider-native ingress and TLS termination)
// diagnosis: the §17.5 cloud-portability story does not stand up an
// operator-supplied Ingress with an external load balancer in front
// of the gateway. The TESTING.md §12.6 managed_ingress suite is the
// pre-release environment check that an operator can attach a
// provider-native LB (GCP HTTPS LB, AWS ALB via the AWS Load Balancer
// Controller, Azure Application Gateway) and reach the gateway over
// HTTPS on managed Kubernetes; the Helm chart renders no Ingress,
// the operator provides one out of band, and the suite confirms
// the rendered Service and gateway reach the LB cleanly.
func TestManagedIngress(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the operator-supplied Ingress example, the cert " +
		"issuer wiring, and the per-provider DNS delegation for the " +
		"pre-release run are not in place; the per-provider Terraform " +
		"under deploy/terraform/cloud/<provider>/ is absent")
}

// spec: 4.5 (artifact store: provider-native object storage backend)
// diagnosis: the §4.5 ArtifactStore backend for the per-provider
// native object store (GCS, S3, Azure Blob) is not wired. The
// TESTING.md §12.6 cloud_csi suite asserts that the same interface
// tests that pass against MinIO also pass against the cloud-managed
// backend with provider-side service-account binding.
func TestCloudCSI(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: pkg/blobstore ships only the MinIO backend " +
		"(pkg/blobstore/miniostore); the GCS, S3, and Azure Blob " +
		"adapters and the provider-side service-account binding are not built")
}

// spec: 4.3 (Token Service: cloud KMS-backed KEK provider)
// diagnosis: the §4.3 / §12.9 T4 per-tenant KEK is not allocated
// against the provider's native KMS. The TESTING.md §12.6 cloud_kms
// suite asserts the KMS probe, KEK rotation, and the
// key-unavailable fail-closed posture against Cloud KMS, AWS KMS,
// or Azure Key Vault, where pkg/kms today ships only the Local
// provider plus the documented CloudProviderSeam.
func TestCloudKMS(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: pkg/kms ships only the in-process Local provider " +
		"and documents the cloud-KMS path as CloudProviderSeam; no " +
		"Cloud KMS, AWS KMS, or Azure Key Vault Provider implementation " +
		"exists, and the per-tenant KEK allocator wiring is not built")
}

// spec: 13 (security model: workload identity / IRSA / Workload Identity Federation)
// diagnosis: the §13 cloud-identity binding does not issue
// provider-mapped IAM tokens to gateway and warm-pool pods. The
// TESTING.md §12.6 cloud_oidc suite asserts that pods receive
// provider-issued tokens that map to cloud IAM roles, configured
// through the Helm chart per the documented service-account binding.
func TestCloudOIDC(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the per-provider IAM binding (Workload Identity " +
		"Federation on GKE, IRSA on EKS, Azure Workload Identity on AKS) " +
		"and the OIDC service-account configuration in the Helm chart " +
		"are not built; the per-provider Terraform that would issue the " +
		"IAM resources is absent")
}

// spec: 4.3 (Token Service: cloud-managed TokenStore backend)
// diagnosis: the §4.3 TokenStore does not delegate to a
// provider-managed secret backend. The TESTING.md §12.6
// cloud_secret_store suite asserts that Secret Manager (GCP),
// Secrets Manager (AWS), or Key Vault (Azure) can serve as the
// alternative TokenStore through the documented credential
// plumbing.
func TestCloudSecretStore(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: pkg/credential and pkg/connectoroauth route secrets " +
		"through the in-cluster credentialstore; the per-provider " +
		"TokenStore adapter and the documented credential plumbing are not built")
}

// spec: 12.5 (artifact store: MinIO multi-zone replication)
// diagnosis: the §12.5 MinIO multi-zone replication is not enabled
// in the Helm chart. The TESTING.md §12.6 multi_az_minio suite is
// optional per provider and asserts cross-zone replication with
// near-zero RPO when MinIO is the artifact store rather than the
// provider's native object storage.
func TestMultiAZMinIO(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the MinIO Helm chart does not enable cross-zone " +
		"site-replication; pkg/blobstore/replication ships the driver " +
		"and controller seam but the chart values and the per-provider " +
		"zone topology required to exercise the path are not wired")
}

// spec: 16.1 (metrics: OTLP delivery to the provider-native collector)
// diagnosis: the §16.1 OTLP exporter is not wired to the
// provider-native collector. The TESTING.md §12.6 cloud_observability
// suite asserts that gateway traces and metrics flow to Cloud Trace
// and Cloud Logging on GCP, X-Ray and CloudWatch on AWS, or
// Application Insights and Log Analytics on AKS.
func TestCloudObservability(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: the OTLP exporter is built but the per-provider " +
		"observability bundle (collector endpoint, credentials, and the " +
		"per-provider Helm values that select it) is not wired")
}

// spec: 11.2 (budgets and quotas: per-tenant usage events to a provider-native billing-export sink)
// diagnosis: the §11.2 billing event stream does not deliver
// per-tenant usage events to a cloud-managed billing-export
// destination. The TESTING.md §12.6 cloud_billing_export suite
// asserts that events flow to BigQuery, Athena, or Azure Data Lake
// in the documented format.
func TestCloudBillingExport(t *testing.T) {
	_ = requireCloud(t)
	t.Skip("blocked: pkg/gateway/billingstore persists billing events " +
		"to Postgres; the billing-event publisher and the per-provider " +
		"sink configuration (BigQuery, Athena, Azure Data Lake) are not built")
}
