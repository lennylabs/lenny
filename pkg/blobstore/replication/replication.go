// SPDX-License-Identifier: MIT

// Package replication implements the §25.11 ArtifactStore continuous
// bucket replication: the MinIO workspace bucket is replicated to an
// off-cluster destination so a primary-site disaster does not lose
// workspace snapshots, checkpoints, transcripts, and uploaded files.
// The Postgres backup pipeline (pkg/ops/backup) covers Postgres,
// configuration, and CRDs; this package covers the artifact bucket,
// which is replicated rather than archived.
//
// The package has three parts. Config is the §25.11 minio.artifactBackup
// Helm values plus their per-region form. Driver is the MinIO-facing
// seam — configure replication, suspend it, resume it, probe the
// destination bucket's jurisdiction tag, resolve the destination
// endpoint — so the orchestration logic is unit-testable without a
// MinIO cluster. Controller runs the §25.11 runtime residency
// preflight: it verifies the destination resides in the same
// jurisdiction before every replication batch and on a periodic tick,
// and suspends replication fail-closed on a jurisdiction mismatch.
package replication

import (
	"errors"
	"fmt"
	"time"
)

// jurisdictionTagKey is the mandatory bucket tag operators set on every
// MinIO / S3 / GCS / Azure destination bucket participating in Lenny
// replication. The §25.11 runtime residency preflight reads it and
// compares it to the source region's dataResidencyRegion.
const jurisdictionTagKey = "lenny.dev/jurisdiction-region"

// §25.11 replication-lag and residency-check defaults.
const (
	// DefaultReplicationLagRpoSeconds is the §25.11 Tier-2/Tier-3
	// replicationLagRpoSeconds default: 15 minutes.
	DefaultReplicationLagRpoSeconds = 900
	// DefaultResidencyCheckIntervalSeconds is the §25.11
	// residencyCheckIntervalSeconds default: 5 minutes.
	DefaultResidencyCheckIntervalSeconds = 300
	// MinResidencyCheckIntervalSeconds and MaxResidencyCheckIntervalSeconds
	// bound residencyCheckIntervalSeconds per §25.11.
	MinResidencyCheckIntervalSeconds = 60
	MaxResidencyCheckIntervalSeconds = 3600
	// DefaultResidencyAuditSamplingWindowSeconds is the §25.11
	// residencyAuditSamplingWindowSeconds default: 1 hour.
	DefaultResidencyAuditSamplingWindowSeconds = 3600
)

// State is the §25.11 ops_artifact_replication_state status of a
// region's ArtifactStore replication.
type State string

const (
	// StateActive: replication is running and the last residency
	// preflight passed.
	StateActive State = "active"
	// StateSuspendedResidencyViolation: the runtime residency preflight
	// observed a jurisdiction mismatch and suspended replication
	// fail-closed. It does not auto-resume.
	StateSuspendedResidencyViolation State = "suspended_residency_violation"
	// StateSuspendedOperator: an operator suspended replication.
	StateSuspendedOperator State = "suspended_operator"
)

// ErrRegionUnresolvable is the §25.11 ARTIFACT_REPLICATION_REGION_-
// UNRESOLVABLE failure: the runtime residency preflight observed a
// jurisdiction-tag mismatch, a missing tag, a DNS rebinding outside the
// allowed CIDRs, or a failed tag probe.
var ErrRegionUnresolvable = errors.New("ARTIFACT_REPLICATION_REGION_UNRESOLVABLE")

// Target is the §25.11 minio.artifactBackup.target block: the
// off-cluster destination an ArtifactStore bucket replicates to.
type Target struct {
	// Endpoint is the off-cluster S3-compatible endpoint.
	Endpoint string
	// Bucket is the destination bucket name.
	Bucket string
	// AccessCredentialSecret names the Kubernetes Secret holding the
	// destination {accessKey, secretKey}. The driver resolves it; the
	// orchestration logic only carries the name.
	AccessCredentialSecret string
	// KMSKeyID is the KMS key on the destination side. When the source
	// region has dataResidencyRegion set it MUST reside in the same
	// jurisdiction.
	KMSKeyID string
}

// valid reports whether the §25.11 target block is fully declared. A
// region with a tenant that has dataResidencyRegion set MUST have a
// complete target; lenny-ops rejects an incomplete one at startup with
// CONFIG_INVALID.
func (t Target) valid() error {
	if t.Endpoint == "" {
		return errors.New("target.endpoint is empty")
	}
	if t.Bucket == "" {
		return errors.New("target.bucket is empty")
	}
	if t.AccessCredentialSecret == "" {
		return errors.New("target.accessCredentialSecret is empty")
	}
	return nil
}

// RegionConfig is the §25.11 per-region artifact-replication
// configuration: minio.regions.<region>.artifactBackup plus the source
// region's data-residency region.
type RegionConfig struct {
	// Region is the region key (e.g. "eu-west-1").
	Region string
	// SourceBucket is the region's ArtifactStore bucket on the source
	// MinIO cluster.
	SourceBucket string
	// DataResidencyRegion is the jurisdiction the source region's data
	// is pinned to. When non-empty the destination bucket's
	// jurisdiction tag MUST equal it. An empty value means the region
	// carries no residency constraint and the residency preflight only
	// verifies the destination is reachable.
	DataResidencyRegion string
	// Target is the destination this region replicates to.
	Target Target
	// AllowedDestinationCIDRs, when non-empty, is the §25.11 second-layer
	// DNS-rebinding guard: the destination endpoint MUST resolve to an IP
	// in one of these CIDRs.
	AllowedDestinationCIDRs []string
}

// Config is the §25.11 minio.artifactBackup configuration: the global
// toggle and tuning values plus the per-region replication
// configurations. A single-region deployment has one Regions entry
// with an empty DataResidencyRegion.
type Config struct {
	// Enabled gates the replication subsystem. Tier 2/3 default true;
	// Tier 1 (dev) default false.
	Enabled bool
	// Versioning records whether source-bucket versioning is enabled, so
	// delete markers replicate without destroying prior versions.
	Versioning bool
	// ReplicationLagRpoSeconds is the §25.11 RPO threshold the
	// replication-lag alert fires on.
	ReplicationLagRpoSeconds int
	// ResidencyCheckIntervalSeconds is the runtime residency-preflight
	// tick. The preflight also runs before every replication batch.
	ResidencyCheckIntervalSeconds int
	// ResidencyAuditSamplingWindowSeconds is the positive-audit sampling
	// window for artifact.cross_region_replication_verified events.
	ResidencyAuditSamplingWindowSeconds int
	// Regions are the per-region replication configurations.
	Regions []RegionConfig
}

// Validate checks the §25.11 startup-time invariants: when replication
// is enabled, every region that carries a dataResidencyRegion MUST have
// a complete target, and the tuning values MUST be in range. A failure
// is the §25.11 CONFIG_INVALID error lenny-ops rejects the install
// with.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if iv := c.ResidencyCheckIntervalSeconds; iv != 0 &&
		(iv < MinResidencyCheckIntervalSeconds || iv > MaxResidencyCheckIntervalSeconds) {
		return fmt.Errorf("CONFIG_INVALID: residencyCheckIntervalSeconds %d out of range [%d,%d]",
			iv, MinResidencyCheckIntervalSeconds, MaxResidencyCheckIntervalSeconds)
	}
	if c.ReplicationLagRpoSeconds < 0 {
		return errors.New("CONFIG_INVALID: replicationLagRpoSeconds must not be negative")
	}
	for _, rc := range c.Regions {
		if rc.Region == "" {
			return errors.New("CONFIG_INVALID: a region entry has an empty region key")
		}
		if rc.SourceBucket == "" {
			return fmt.Errorf("CONFIG_INVALID: minio.regions.%s.artifactBackup source bucket is empty", rc.Region)
		}
		// §25.11: a region with a residency constraint MUST have a
		// complete target — the startup CONFIG_INVALID check.
		if rc.DataResidencyRegion != "" {
			if err := rc.Target.valid(); err != nil {
				return fmt.Errorf("CONFIG_INVALID: minio.regions.%s.artifactBackup.target incomplete: %w",
					rc.Region, err)
			}
		} else if rc.Target.Endpoint != "" {
			// A region without a residency constraint may still declare a
			// target; if it does, the target must be complete too.
			if err := rc.Target.valid(); err != nil {
				return fmt.Errorf("CONFIG_INVALID: minio.regions.%s.artifactBackup.target incomplete: %w",
					rc.Region, err)
			}
		}
	}
	return nil
}

// lagRPO returns the configured replication-lag RPO, applying the
// §25.11 default when unset.
func (c Config) lagRPO() time.Duration {
	s := c.ReplicationLagRpoSeconds
	if s <= 0 {
		s = DefaultReplicationLagRpoSeconds
	}
	return time.Duration(s) * time.Second
}

// residencyCheckInterval returns the configured residency-preflight
// tick, applying the §25.11 default when unset.
func (c Config) residencyCheckInterval() time.Duration {
	s := c.ResidencyCheckIntervalSeconds
	if s <= 0 {
		s = DefaultResidencyCheckIntervalSeconds
	}
	return time.Duration(s) * time.Second
}

// auditSamplingWindow returns the configured positive-audit sampling
// window, applying the §25.11 default when unset.
func (c Config) auditSamplingWindow() time.Duration {
	s := c.ResidencyAuditSamplingWindowSeconds
	if s <= 0 {
		s = DefaultResidencyAuditSamplingWindowSeconds
	}
	return time.Duration(s) * time.Second
}
