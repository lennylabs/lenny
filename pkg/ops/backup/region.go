// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"sort"
)

// RegionBackupConfig is one §12.8 / §25.11 per-region backup endpoint
// entry (backups.regions.<region>). Each region carries its own MinIO
// endpoint, KMS key, access-credential Secret, and bucket so a region's
// pg_dump is written only to that region's jurisdiction and one region's
// backup Job cannot authenticate to another region's MinIO.
// spec: §12.8 line 934.
type RegionBackupConfig struct {
	// MinioEndpoint is backups.regions.<region>.minioEndpoint: the MinIO
	// host:port this region's archive is uploaded to.
	MinioEndpoint string
	// KMSKeyID is backups.regions.<region>.kmsKeyId: the region-resident
	// KMS key used for client-side and SSE-KMS envelope encryption.
	KMSKeyID string
	// AccessCredentialSecret is backups.regions.<region>.accessCredentialSecret:
	// the Secret name holding this region's MinIO credentials.
	AccessCredentialSecret string
	// Bucket overrides the default backup bucket for this region; empty
	// keeps the deployment-wide default bucket name.
	Bucket string
}

// complete reports whether the region entry carries the MinIO endpoint
// and access-credential Secret a per-region dump requires. A region
// present in the map but missing its endpoint or credential Secret is
// unresolvable, the same as a region absent from the map — the §25.11
// line 4336 "or the region's MinIO endpoint / KMS key is unreachable"
// fail-closed condition. spec: §12.8 line 936.
func (c RegionBackupConfig) complete() bool {
	return c.MinioEndpoint != "" && c.AccessCredentialSecret != ""
}

// ShardRegion pairs a Postgres shard with its resolved data-residency
// region.
type ShardRegion struct {
	// ShardID identifies the Postgres shard ("platform" for a single-shard
	// deployment).
	ShardID string
	// Region is the shard's resolved dataResidencyRegion.
	Region string
}

// ShardRegionResolver resolves the Postgres shards a backup must cover to
// their data-residency regions, using the same StorageRouter-backed
// resolution the gateway uses at runtime. The per-region backup pipeline
// enumerates shards by region and runs one pg_dump per region against
// that region's shards only. spec: §12.8 line 935.
type ShardRegionResolver interface {
	ShardRegions(ctx context.Context) ([]ShardRegion, error)
}

// ResidencyMetrics receives the §12.8 lenny_data_residency_violation_total
// increment on a fail-closed backup abort. It is the same counter the
// runtime StorageRouter and the ArtifactStore replication controller
// increment, distinguished by the operation label. A nil sink drops the
// metric. spec: §12.8 line 936.
type ResidencyMetrics interface {
	DataResidencyViolation(operation string)
}

// residencyOperationBackup is the §12.8 line 936 operation label on the
// DataResidencyViolationAttempt audit event and the
// lenny_data_residency_violation_total counter raised when a backup
// aborts BACKUP_REGION_UNRESOLVABLE.
const residencyOperationBackup = "backup"

// dumpsPostgres reports whether a backup type dumps Postgres shards and
// therefore is subject to per-region residency dispatch. A config-only
// backup exports CRDs and config with no shard dump, so it is not
// region-routed. spec: §12.8 line 935 ("one pg_dump per region").
func dumpsPostgres(t Type) bool {
	return t == TypeFull || t == TypePostgres || t == TypePreRestore
}

// regionsInOrder returns the distinct regions covered by shards in
// deterministic (sorted) order, mirroring the §25.11 line 3519
// platform-first / sorted shard ordering convention.
func regionsInOrder(shards []ShardRegion) []string {
	seen := make(map[string]struct{}, len(shards))
	regions := make([]string, 0, len(shards))
	for _, s := range shards {
		if _, ok := seen[s.Region]; ok {
			continue
		}
		seen[s.Region] = struct{}{}
		regions = append(regions, s.Region)
	}
	sort.Strings(regions)
	return regions
}

// shardsInRegion returns the shard ids resolved to region, in input
// order.
func shardsInRegion(shards []ShardRegion, region string) []string {
	out := make([]string, 0, len(shards))
	for _, s := range shards {
		if s.Region == region {
			out = append(out, s.ShardID)
		}
	}
	return out
}
