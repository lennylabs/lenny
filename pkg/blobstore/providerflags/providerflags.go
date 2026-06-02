// SPDX-License-Identifier: MIT

// Package providerflags wires the §17.9.3 `objectStorage.provider`
// selector onto the §12.5 ArtifactStore blobstore.Store adapter set.
// The gateway exposes a single provider knob (`minio | s3 | gcs |
// azure`, plus the dev-only in-memory backend) so an operator picks
// the object-store backend from Helm values, and the cloud adapters
// (which ship as separate `pkg/blobstore/{s3,gcs,azureblob}`
// packages) reach the binary through the resolved blobstore.Store
// returned by Resolve.
//
// spec: §17.5 bullet 1 ("Storage backends are pluggable"); §17.9.3
// ("Cloud-Managed Backends", the canonical `objectStorage.provider:
// s3 | gcs | azure | minio` selector). Before this seam the adapter
// packages existed but the gateway recognised only MinIO and the
// in-memory store, so the spec's provider switch was unimplemented at
// the wiring boundary (F-17.5.1 / F-12.7.3).
package providerflags

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/azureblob"
	"github.com/lennylabs/lenny/pkg/blobstore/gcs"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/blobstore/s3"
)

// Provider names the §17.9.3 object-storage backend selectors.
const (
	// ProviderMinIO is the self-managed MinIO backend (§17.1). It is
	// the chart default; when no MinIO endpoint is configured the
	// gateway falls back to the in-memory store for the minimal dev
	// posture.
	ProviderMinIO = "minio"
	// ProviderMemory forces the process-local in-memory store. Dev
	// only; nothing persists across a restart.
	ProviderMemory = "memory"
	// ProviderS3 is the AWS S3 backend (pkg/blobstore/s3).
	ProviderS3 = "s3"
	// ProviderGCS is the Google Cloud Storage backend (pkg/blobstore/gcs).
	ProviderGCS = "gcs"
	// ProviderAzure is the Azure Blob Storage backend
	// (pkg/blobstore/azureblob).
	ProviderAzure = "azure"
)

// TenantSSEResolver is the §12.5 ll. 297-303 per-tenant SSE-KMS key
// resolver the gateway supplies. It is keyed by tenant id; Resolve
// adapts it to each cloud backend's URI-keyed resolver hook so the T4
// fail-closed write contract holds regardless of which provider is
// selected.
type TenantSSEResolver func(tenantID string) (keyID string, requireKey bool, err error)

// Options captures every operator-tunable object-storage knob. Only
// the fields the resolved provider needs must be populated.
type Options struct {
	// Provider names the §17.9.3 backend: minio | memory | s3 | gcs |
	// azure. Empty is treated as minio (the chart default).
	Provider string

	// Bucket is the shared §17.9.3 `objectStorage.bucket` value: the
	// S3 bucket, the GCS bucket, or the Azure container name.
	Bucket string

	// Region is the §17.9.3 `objectStorage.region` value, consumed by
	// the S3 backend. Empty falls back to the AWS SDK default chain
	// (AWS_REGION).
	Region string

	// AzureAccountURL is the Azure Blob storage account URL
	// (https://<account>.blob.core.windows.net) for ProviderAzure.
	AzureAccountURL string

	// MinIO connection fields, consumed by ProviderMinIO.
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	MinIOUseSSL    bool

	// SSEKeyResolver is the §12.5 tenant-keyed SSE-KMS resolver. It is
	// passed verbatim to MinIO (which is already tenant-keyed) and
	// adapted to the URI-keyed hook the S3 and GCS backends expose. A
	// nil resolver lets the bucket/account default encryption apply.
	SSEKeyResolver TenantSSEResolver
}

// Resolve constructs the blobstore.Store the operator selected. The
// returned store may also satisfy blobstore.Tombstoner,
// drainreadiness.Prober, and the artifact-metrics callback interface;
// callers type-assert for those optional surfaces.
func Resolve(ctx context.Context, opts Options) (blobstore.Store, error) {
	switch prov := strings.ToLower(strings.TrimSpace(opts.Provider)); prov {
	case "", ProviderMinIO:
		// The chart default. MinIO when an endpoint is configured,
		// otherwise the in-memory store (the §17.4 minimal/dev posture).
		if opts.MinIOEndpoint == "" {
			return blobstore.NewMemoryStore(nil), nil
		}
		return miniostore.New(miniostore.Config{
			Endpoint:       opts.MinIOEndpoint,
			AccessKey:      opts.MinIOAccessKey,
			SecretKey:      opts.MinIOSecretKey,
			Bucket:         opts.MinIOBucket,
			UseSSL:         opts.MinIOUseSSL,
			SSEKeyResolver: opts.SSEKeyResolver,
		})
	case ProviderMemory:
		return blobstore.NewMemoryStore(nil), nil
	case ProviderS3:
		return resolveS3(ctx, opts)
	case ProviderGCS:
		return resolveGCS(ctx, opts)
	case ProviderAzure:
		return resolveAzure(opts)
	default:
		return nil, fmt.Errorf("blobstore/providerflags: unknown objectStorage.provider %q (want minio|s3|gcs|azure)", opts.Provider)
	}
}

// uriResolver adapts the §12.5 tenant-keyed SSE resolver to the
// URI-keyed hook the S3 and GCS backends expose (both name the hook
// `func(blobstore.URI) (keyID string, requireKey bool, err error)`).
// A nil tenant resolver yields a nil hook so bucket-default
// encryption applies.
func uriResolver(r TenantSSEResolver) func(blobstore.URI) (string, bool, error) {
	if r == nil {
		return nil
	}
	return func(u blobstore.URI) (string, bool, error) {
		return r(u.TenantID)
	}
}

func resolveS3(ctx context.Context, opts Options) (blobstore.Store, error) {
	if opts.Bucket == "" {
		return nil, errors.New("blobstore/providerflags: objectStorage.provider=s3 requires objectStorage.bucket")
	}
	cfg, err := loadAWSConfig(ctx, opts.Region)
	if err != nil {
		return nil, fmt.Errorf("blobstore/providerflags: load AWS SDK config: %w", err)
	}
	return s3.New(s3.Config{
		AWSConfig:      cfg,
		Bucket:         opts.Bucket,
		SSEKeyResolver: uriResolver(opts.SSEKeyResolver),
	})
}

func resolveGCS(ctx context.Context, opts Options) (blobstore.Store, error) {
	if opts.Bucket == "" {
		return nil, errors.New("blobstore/providerflags: objectStorage.provider=gcs requires objectStorage.bucket")
	}
	return gcs.New(ctx, gcs.Config{
		Bucket:         opts.Bucket,
		KMSKeyResolver: uriResolver(opts.SSEKeyResolver),
	})
}

func resolveAzure(opts Options) (blobstore.Store, error) {
	if opts.AzureAccountURL == "" {
		return nil, errors.New("blobstore/providerflags: objectStorage.provider=azure requires objectStorage.accountUrl")
	}
	if opts.Bucket == "" {
		return nil, errors.New("blobstore/providerflags: objectStorage.provider=azure requires objectStorage.bucket (the container name)")
	}
	cred, err := loadAzureCredential()
	if err != nil {
		return nil, fmt.Errorf("blobstore/providerflags: resolve Azure credential: %w", err)
	}
	// CPKResolver is left nil: Azure encryption at rest is configured
	// at the storage-account / CMEK level per §17.9.4, not via a
	// per-object customer-provided key derived from a tenant alias.
	return azureblob.New(azureblob.Config{
		ServiceURL: opts.AzureAccountURL,
		Credential: cred,
		Container:  opts.Bucket,
	})
}
