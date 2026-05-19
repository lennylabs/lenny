// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 e2e cloud scaffolds. Each test maps to one of the §12.6
// suites from TESTING.md and skips with a precise diagnosis until
// the cloud harness lands. Bring-up is automated by
// scripts/cloud/<provider>/{up,down}.sh; the harness is built around
// `tests/testinfra/cloud/` which will wrap that automation when it
// ships.

package tier6_e2e_cloud_test

import (
	"os"
	"testing"
)

// requireCloudProvider gates every tier-6 test on the presence of an
// authenticated provider context. The harness ships per-provider
// helpers in tests/testinfra/cloud/ later; for now we read the
// LENNY_CLOUD_PROVIDER env var as the marker and skip otherwise.
func requireCloudProvider(t *testing.T) {
	t.Helper()
	if os.Getenv("LENNY_CLOUD_PROVIDER") == "" {
		t.Skip("not implemented: §12.6 cloud harness — set LENNY_CLOUD_PROVIDER=gke|eks|aks and ship tests/testinfra/cloud/ before this runs")
	}
}

// TestGvisorIsolation — default isolation profile (sandboxed) creates
// pods on the gVisor (or provider-equivalent) node pool.
func TestGvisorIsolation(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 gvisor_isolation — requires gVisor or provider-equivalent sandbox node pool + the isolation-profile-sandboxed pool config")
}

// TestKataIsolation — Kata or microVM RuntimeClass pods boot, accept
// claims, and tear down within the documented latency budget.
func TestKataIsolation(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 kata_isolation — requires Kata RuntimeClass (GKE/AKS) or Firecracker-via-Fargate (EKS) + the matching pool config")
}

// TestMultiZoneDR — Postgres failover across zones with RPO=0 and
// RTO under 30 seconds. Session in-flight survives.
func TestMultiZoneDR(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 multi_zone_dr — requires provider-native HA Postgres (Cloud SQL HA, RDS Multi-AZ, Azure Database for PostgreSQL Flexible Server) and a failover injector")
}

// TestManagedIngress — provider-native external LB with TLS
// termination and real cert-manager or provider-managed certs.
func TestManagedIngress(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 managed_ingress — requires the provider-native ingress controller (GCP HTTPS LB / AWS ALB / Azure App Gateway) + cert issuer")
}

// TestCloudCSI — ArtifactStore against the provider's native object
// storage: GCS / S3 / Azure Blob.
func TestCloudCSI(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_csi — requires the pluggable ArtifactStore backend per provider and provider-side service-account binding")
}

// TestCloudKMS — T4 per-tenant keys against the provider's native KMS.
// KMS probe, key rotation, key-unavailable fail-closed validated.
func TestCloudKMS(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_kms — requires the per-provider KMS adapter (Cloud KMS / AWS KMS / Azure Key Vault) + the T4 per-tenant key allocator")
}

// TestCloudOIDC — workload identity / IRSA / Workload Identity
// Federation: pods receive provider-issued tokens that map to cloud
// IAM roles.
func TestCloudOIDC(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_oidc — requires the provider-side IAM binding + the OIDC service-account configuration in the Helm chart")
}

// TestCloudSecretStore — native secret backend integration: Secret
// Manager / Secrets Manager / Key Vault as alternative TokenStore
// backends.
func TestCloudSecretStore(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_secret_store — requires the per-provider TokenStore adapter and the documented credential plumbing")
}

// TestMultiAZMinIO — if using self-managed MinIO, multi-zone
// replication and near-zero RPO.
func TestMultiAZMinIO(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 multi_az_minio — optional per provider; requires the MinIO chart with cross-zone replication enabled")
}

// TestCloudObservability — OTLP delivery to the provider-native
// collector (Cloud Trace + Cloud Logging / X-Ray + CloudWatch /
// Application Insights + Log Analytics).
func TestCloudObservability(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_observability — requires the OTLP exporter wired to the provider-native collector + the per-provider observability bundle")
}

// TestCloudBillingExport — per-tenant usage events flow to the
// provider's billing-export sink (BigQuery / Athena / Azure Data Lake).
func TestCloudBillingExport(t *testing.T) {
	requireCloudProvider(t)
	t.Skip("not implemented: §12.6 cloud_billing_export — requires the billing-event publisher and the per-provider sink configuration")
}
