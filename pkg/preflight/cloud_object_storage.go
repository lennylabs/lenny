// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strings"
)

// CloudObjectStorageLifecycleStatus is the §17.9.4 lifecycle posture of
// a cloud object-storage bucket as read through a provider SDK. The
// zero value reports a bucket with no versioning and no lifecycle rules.
//
// spec: §17.9.4 (Cloud Object Storage Lifecycle Requirements);
// §17.6 line 494.
type CloudObjectStorageLifecycleStatus struct {
	// VersioningEnabled reports whether bucket versioning is enabled
	// (S3 GetBucketVersioning Status=Enabled, GCS bucket versioning,
	// Azure BlobServiceProperties.IsVersioningEnabled).
	VersioningEnabled bool
	// NoncurrentVersionExpirationDays is the shortest configured
	// noncurrent-version expiration rule in days, or 0 when no such rule
	// exists. The §17.9.4 target is 1 day; the §17.6 check admits any
	// value within the MaxExpirationDays ceiling (default 7).
	NoncurrentVersionExpirationDays int
	// DeleteMarkerExpirationEnabled reports whether an expired-object
	// delete-marker rule is present (S3/S3-compatible
	// Expiration.ExpiredObjectDeleteMarker=true). GCS and Azure have no
	// delete markers, so the check does not require it for those
	// providers.
	DeleteMarkerExpirationEnabled bool
}

// CloudObjectStorageLifecycleProber reads the §17.9.4 lifecycle posture
// of a bucket through the provider SDK. It is the seam the real
// per-provider SDK reader and test fakes satisfy. The lenny-preflight
// Job wires a real S3 reader when objectStorage.provider=s3; GCS and
// Azure route through the advisory path until their readers are wired.
//
// spec: §17.6 line 494 (S3 GetBucketVersioning +
// GetBucketLifecycleConfiguration; GCS storage.buckets.get; Azure
// BlobServiceProperties.IsVersioningEnabled + ManagementPolicy GET).
type CloudObjectStorageLifecycleProber interface {
	GetLifecycle(ctx context.Context, bucket string) (CloudObjectStorageLifecycleStatus, error)
}

// CloudObjectStorageLifecycleProbeFunc adapts a function to
// CloudObjectStorageLifecycleProber.
type CloudObjectStorageLifecycleProbeFunc func(ctx context.Context, bucket string) (CloudObjectStorageLifecycleStatus, error)

// GetLifecycle calls f.
func (f CloudObjectStorageLifecycleProbeFunc) GetLifecycle(ctx context.Context, bucket string) (CloudObjectStorageLifecycleStatus, error) {
	return f(ctx, bucket)
}

// DefaultCloudLifecycleMaxExpirationDays is the §17.6 line 494 ceiling
// for the noncurrent-version expiration rule: rules that expire
// noncurrent versions within this many days satisfy the check. The
// §17.9.4 configured value is 1 day; the preflight admits anything up to
// 7 so a deployer who set a slightly longer window still installs.
const DefaultCloudLifecycleMaxExpirationDays = 7

// CloudObjectStorageLifecycleCheck is the §17.6 line 494 cloud
// object-storage lifecycle audit. When objectStorage.provider names a
// cloud backend (s3 | gcs | azure) it verifies, through a provider SDK
// read, that (a) bucket versioning is enabled and (b) a noncurrent-
// version expiration rule within MaxExpirationDays exists, and (for
// S3/S3-compatible) an expired-object-delete-marker rule exists. The
// check is skipped for provider=minio (the post-install Job configures
// MinIO lifecycle via `mc ilm add`).
//
// spec: §17.9.4; §17.6 line 494.
type CloudObjectStorageLifecycleCheck struct {
	// Provider is the objectStorage.provider chart value
	// (minio | s3 | gcs | azure). Empty or "minio" skips the check.
	Provider string
	// Bucket is the cloud bucket / container name. Required for a cloud
	// provider.
	Bucket string
	// Prober reads the live lifecycle posture. A nil prober for a cloud
	// provider routes through the advisory path: the rules cannot be
	// validated automatically (skip-network-probes, or a provider whose
	// SDK reader is not wired), so the check passes with a warning that
	// the deployer must apply the §17.9.4 rules out of band.
	Prober CloudObjectStorageLifecycleProber
	// MaxExpirationDays is the noncurrent-version expiration ceiling.
	// Zero falls back to DefaultCloudLifecycleMaxExpirationDays.
	MaxExpirationDays int
}

// cloudObjectStorageProviders is the set of cloud (non-MinIO) providers
// the §17.9.4 lifecycle requirement applies to.
var cloudObjectStorageProviders = map[string]bool{
	"s3":    true,
	"gcs":   true,
	"azure": true,
}

// Decide evaluates the §17.9.4 lifecycle posture and returns a preflight
// Decision. A cloud provider whose bucket is misconfigured fails
// fail-closed so the install aborts before the gateway writes its first
// checkpoint to a bucket without versioning.
//
// spec: §17.9.4; §17.6 line 494.
func (c CloudObjectStorageLifecycleCheck) Decide(ctx context.Context) Decision {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" || provider == "minio" {
		return Decision{Passed: true, Reason: "SKIPPED: objectStorage.provider=minio (lifecycle configured by the post-install Job)"}
	}
	if !cloudObjectStorageProviders[provider] {
		// The chart validates the provider enum; an unrecognized value
		// here is a config error the schema should have rejected, so
		// fail-closed rather than silently skip.
		return Decision{Passed: false, Reason: fmt.Sprintf("CONFIG_INVALID: objectStorage.provider=%q is not a recognized cloud provider (s3 | gcs | azure)", c.Provider)}
	}
	if strings.TrimSpace(c.Bucket) == "" {
		return Decision{Passed: false, Reason: fmt.Sprintf("CONFIG_INVALID: objectStorage.bucket is required when objectStorage.provider=%q", provider)}
	}
	if c.Prober == nil {
		return Decision{
			Passed: true,
			Reason: fmt.Sprintf("WARNING: cloud object storage lifecycle for provider %q could not be validated automatically; apply the §17.9.4 rules (versioning + noncurrent-version/delete-marker expiration) to bucket %q before relying on this install", provider, c.Bucket),
		}
	}

	status, err := c.Prober.GetLifecycle(ctx, c.Bucket)
	if err != nil {
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("Cloud object storage bucket %q lifecycle could not be read via the %s SDK: %v. Configure versioning and lifecycle rules before installing Lenny — see Section 17.9 (Cloud Object Storage Lifecycle Requirements).", c.Bucket, provider, err),
		}
	}

	max := c.MaxExpirationDays
	if max <= 0 {
		max = DefaultCloudLifecycleMaxExpirationDays
	}

	var missing []string
	if !status.VersioningEnabled {
		missing = append(missing, "bucket versioning is not enabled")
	}
	switch {
	case status.NoncurrentVersionExpirationDays <= 0:
		missing = append(missing, "no noncurrent-version expiration rule is configured")
	case status.NoncurrentVersionExpirationDays > max:
		missing = append(missing, fmt.Sprintf("noncurrent-version expiration is %d days (must be ≤ %d)", status.NoncurrentVersionExpirationDays, max))
	}
	// Delete markers exist only on S3/S3-compatible object stores; GCS
	// and Azure have no delete-marker concept, so the check does not
	// require the rule for them.
	if provider == "s3" && !status.DeleteMarkerExpirationEnabled {
		missing = append(missing, "no expired-object-delete-marker rule is configured")
	}

	if len(missing) > 0 {
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("Cloud object storage bucket %q is missing required lifecycle rules: %s. Configure versioning and lifecycle rules before installing Lenny — see Section 17.9 (Cloud Object Storage Lifecycle Requirements).",
				c.Bucket, strings.Join(missing, ", ")),
		}
	}

	return Decision{
		Passed: true,
		Reason: fmt.Sprintf("Cloud object storage bucket %q has versioning enabled and a compliant noncurrent-version expiration rule", c.Bucket),
	}
}
