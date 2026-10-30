// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"strings"

	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// t4DefaultEncryptionConfig carries the per-tenant default-encryption
// declarations that the GCS V4 signed-URL and Azure SAS checkpoint PUT
// paths cannot bind per request. On these two backends the presigned
// capability signs no encryption header, so a workspaceTier T4 tenant's
// per-tenant encryption rests on a backend default: a per-tenant GCS
// bucket-default CMEK, or an Azure container-level default encryption
// scope pinned with DenyEncryptionScopeOverride. These are read from the
// same objectStorage.{gcs,azure} configuration keys the §17.6 preflight
// check reads.
//
// spec: §12.5; §17.9.7.
type t4DefaultEncryptionConfig struct {
	gcsBucketDefaultCMEK             string
	azureDefaultEncryptionScope      string
	azureDenyEncryptionScopeOverride bool
}

// validateT4DefaultEncryption is the fail-closed replacement for the SigV4
// signature binding that the GCS V4 and Azure SAS paths cannot carry per
// request (spec §12.5): because the mint signs no encryption
// header on these two backends, a misconfigured deployment would silently
// write T4 checkpoints under the deployment-wide key rather than the
// tenant-scoped key, defeating the §12.9 cryptographic-erasure property.
// It returns a non-nil error when a gcs or azure backend serves any
// workspaceTier T4 tenant without the required backend default:
//
//   - gcs requires a per-tenant bucket-default CMEK, which the T4
//     checkpoint PUT inherits.
//   - azure requires both a container-level default encryption scope and
//     DenyEncryptionScopeOverride, so a chunk PUT cannot land under any
//     other scope.
//
// A backend other than gcs/azure, or a deployment serving no T4 tenant,
// returns nil: the SigV4 backends fold the SSE-KMS key into the signature
// and fail closed at request time, and a deployment with no T4 tenant has
// no per-tenant key requirement to assert. The caller escalates a non-nil
// error to log.Fatalf so the gateway refuses to boot before it serves a
// T4 tenant.
//
// spec: §12.5; §17.9.7.
func validateT4DefaultEncryption(provider string, servesT4Tenant bool, cfg t4DefaultEncryptionConfig) error {
	if !servesT4Tenant {
		return nil
	}
	switch provider {
	case blobproviderflags.ProviderGCS:
		if strings.TrimSpace(cfg.gcsBucketDefaultCMEK) == "" {
			return fmt.Errorf("§12.5: object-storage-provider=gcs serves a workspaceTier T4 tenant but declares no per-tenant bucket-default CMEK; set objectStorage.gcs.bucketDefaultCmek (--object-storage-gcs-bucket-default-cmek / LENNY_OBJECT_STORAGE_GCS_BUCKET_DEFAULT_CMEK) — the GCS V4 signed URL cannot carry a per-request CMEK, so the T4 checkpoint PUT inherits the bucket default and the gateway fails closed without it")
		}
	case blobproviderflags.ProviderAzure:
		if strings.TrimSpace(cfg.azureDefaultEncryptionScope) == "" {
			return fmt.Errorf("§12.5: object-storage-provider=azure serves a workspaceTier T4 tenant but declares no container default encryption scope; set objectStorage.azure.defaultEncryptionScope (--object-storage-azure-default-encryption-scope / LENNY_OBJECT_STORAGE_AZURE_DEFAULT_ENCRYPTION_SCOPE) — the Azure SAS carries no encryption scope, so the T4 chunk PUT lands under the container default and the gateway fails closed without it")
		}
		if !cfg.azureDenyEncryptionScopeOverride {
			return fmt.Errorf("§12.5: object-storage-provider=azure serves a workspaceTier T4 tenant with a container default encryption scope but no override prevention; set objectStorage.azure.denyEncryptionScopeOverride=true (--object-storage-azure-deny-encryption-scope-override / LENNY_OBJECT_STORAGE_AZURE_DENY_ENCRYPTION_SCOPE_OVERRIDE) so a chunk PUT cannot land under any other scope")
		}
	}
	return nil
}

// assertT4DefaultEncryption fails the gateway boot closed when the resolved
// gcs or azure object-store backend is configured to serve any workspaceTier
// T4 tenant without the per-tenant bucket-default CMEK (GCS) or container-
// level default encryption scope with override prevention (Azure) that the
// presigned PUT cannot bind per request. The gate runs only for gcs/azure —
// the SigV4 backends (minio, s3) fold the SSE-KMS key into the signature and
// fail closed at request time. A failure to enumerate T4 tenants on a
// gcs/azure backend is itself fatal: the gateway cannot verify the T4
// default-encryption posture and must not start.
//
// spec: §12.5; §17.9.7.
func (w *gatewayWiring) assertT4DefaultEncryption(ctx context.Context, tenants tenantstore.Store) error {
	// Normalize the provider exactly as blobproviderflags.Resolve does
	// (lower-cased, whitespace-trimmed) so this fail-closed gate keys off
	// the backend Resolve actually selects. A raw "GCS", "Azure", or
	// " gcs" resolves to a genuine cloud backend; comparing the raw string
	// would let that variant bypass the gate and boot without the required
	// bucket/container default encryption, the fail-open this gate exists
	// to prevent.
	provider := strings.ToLower(strings.TrimSpace(*w.f.objectStorageProvider))
	if provider != blobproviderflags.ProviderGCS && provider != blobproviderflags.ProviderAzure {
		return nil
	}
	t4Tenants, err := (t4TenantSource{store: tenants}).T4Tenants(ctx)
	if err != nil {
		return fmt.Errorf("§12.5 T4 default-encryption startup assertion: cannot enumerate workspaceTier T4 tenants to verify the %s bucket/container default encryption posture: %w", provider, err)
	}
	return validateT4DefaultEncryption(provider, len(t4Tenants) > 0, t4DefaultEncryptionConfig{
		gcsBucketDefaultCMEK:             *w.f.objectStorageGCSBucketDefaultCMEK,
		azureDefaultEncryptionScope:      *w.f.objectStorageAzureDefaultEncryptionScope,
		azureDenyEncryptionScopeOverride: *w.f.objectStorageAzureDenyEncryptionScopeOverride,
	})
}

// newSSEKeyResolver builds the §12.5 ll. 297-303 SSEKeyResolver the
// MinIO blob store calls on every Put. The closure:
//
//   - Returns (tenantkms.AliasFor(tenantID), true, nil) for a T4
//     tenant: MinIO MUST wrap under the per-tenant alias so the §12.5
//     cryptographic-erasure property holds.
//   - Returns ("", false, nil) for a non-T4 tenant: fall through to
//     the bucket-default SSE-S3 / SSE-KMS key.
//   - Returns ("", true, err) for a T4 tenant whose registry row is
//     unreachable: the blobstore maps it onto
//     CLASSIFICATION_CONTROL_VIOLATION and fires the KMS-unavailable
//     callback. Returning requireKey=true on a lookup failure is the
//     fail-closed posture: we cannot infer the tier from a missing
//     row, and a requireKey=false return would silently downgrade an
//     unknown tenant to the bucket-default key.
//
// spec: §12.5 ll. 297-303 — T4 SSE-KMS resolution and fail-closed
// rejection.
func newSSEKeyResolver(tenants tenantstore.Store) func(string) (string, bool, error) {
	return func(tenantID string) (string, bool, error) {
		row, err := tenants.Get(context.Background(), tenantID)
		if err != nil {
			return "", true, fmt.Errorf("lookup tenant %s: %w", tenantID, err)
		}
		if row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			return tenantkms.AliasFor(tenantID), true, nil
		}
		return "", false, nil
	}
}

// t4TenantSource adapts a tenantstore.Store into a
// tenantkms.TenantSource so the §12.5 continuous probe
// enumerates exactly the active tenants at workspaceTier T4 — the only
// tenants holding a tenant-scoped KMS key. Soft-deleted tenants are
// dropped (their key is destroyed in §12.8 Phase 4a, so probing it is
// pointless and would flatline the gauge for a tenant that is gone).
type t4TenantSource struct {
	store tenantstore.Store
}

func (t t4TenantSource) T4Tenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.IsActive() && row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			out = append(out, row.ID)
		}
	}
	return out, nil
}
