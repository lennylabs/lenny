// SPDX-License-Identifier: MIT

// Package legalholdescrow implements the §12.8 Phase 3.5 legal-hold
// segregation override path: the region-scoped escrow of held tenant
// evidence before a force-delete destroys the tenant KMS key.
//
// When a platform-admin invokes POST /v1/admin/tenants/{id}/force-delete
// with {"acknowledgeHoldOverride": true, "justification": "<text>"}, the
// deletion controller does not skip Phase 3.5 — it authorizes it. The
// Migrator here performs the four §12.8 sub-steps for the override:
//
//  1. Enumerate held resources (the caller passes them in).
//  2. Resolve the target escrow region from the tenant's
//     dataResidencyRegion (or the single-region default) and re-encrypt
//     each held resource under the region-scoped escrow KEK
//     (platform:legal_hold_escrow:<region>), distinct from the tenant
//     KEK. An unresolvable region aborts fail-closed.
//  3. Migrate the re-encrypted payloads to the region-scoped legal-hold
//     escrow bucket with a retain-until-hold-release object lock.
//  4. Record each migration in the legal-hold ledger and mark the source
//     record so Phase 4's DeleteByTenant skips it.
//
// The escrow KEK is platform-managed, single, long-lived, and per-region
// — it outlives every tenant KEK so held evidence stays readable after
// the tenant record is tombstoned, while decrypt capability stays inside
// the originating tenant's jurisdiction.
//
// spec: spec/12_storage-architecture.md lines 872, 878-889.
package legalholdescrow

import (
	"errors"
	"fmt"
)

// Sentinel errors.
var (
	// ErrRegionUnresolvable is the §12.8 line 883
	// LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE fail-closed condition: the
	// resolved target escrow region has no matching
	// storage.regions.<region>.legalHoldEscrow entry (and no single-region
	// default applies). There is no silent fallback to a default or
	// cross-region escrow bucket — an unresolvable region is a hard error.
	ErrRegionUnresolvable = errors.New("legalholdescrow: escrow region unresolvable")

	// ErrNoHolds is returned when Migrate is called with no held
	// resources. The override path is only entered when Phase 3.5 found
	// active holds, so an empty set is a caller error.
	ErrNoHolds = errors.New("legalholdescrow: no held resources to escrow")
)

// DefaultRegion is the §12.8 single-region escrow identifier used when a
// tenant carries no dataResidencyRegion and the deployment configures
// only the scalar storage.legalHoldEscrowDefault values. The escrow KEK
// is then resolvable as platform:legal_hold_escrow:default.
const DefaultRegion = "default"

// RegionEscrow is one region's §12.8 legal-hold escrow configuration:
// the escrow bucket endpoint and name, the escrow KMS key id, and the
// region's platform-Postgres endpoint for the ledger residency route
// (CMP-058, §12.8 sub-step 4). A region is "configured" for escrow when
// it has a non-empty Bucket.
type RegionEscrow struct {
	// Endpoint is the S3-compatible escrow bucket endpoint.
	Endpoint string
	// Bucket is the region-scoped legal-hold escrow bucket name.
	Bucket string
	// KMSKeyID is the region's escrow KMS key id.
	KMSKeyID string
	// PostgresEndpoint is the region's platform-Postgres endpoint the
	// ledger row is routed to (CMP-058). Empty when the deployment runs
	// a single platform-Postgres for all regions.
	PostgresEndpoint string
}

// Config is the deployment's §12.8 region-scoped escrow map plus the
// single-region default. A deployment with no Regions map operates on
// Default alone; a multi-region deployment resolves a tenant's
// dataResidencyRegion against Regions and fails closed for an
// unconfigured region.
type Config struct {
	// Default is the single-region escrow configuration
	// (storage.legalHoldEscrowDefault). It backs tenants with no
	// dataResidencyRegion. Nil when the deployment configures no default.
	Default *RegionEscrow
	// Regions maps a residency region to its escrow configuration
	// (storage.regions.<region>.legalHoldEscrow). Nil/empty for a
	// single-region deployment.
	Regions map[string]RegionEscrow
}

// Configured reports whether the deployment has any escrow configuration
// at all. A deployment with neither a Default nor any Regions cannot
// honor a force-delete override and every resolution fails closed.
func (c Config) Configured() bool {
	return c.Default != nil || len(c.Regions) > 0
}

// Resolve maps a tenant's dataResidencyRegion to the escrow region name
// and its configuration. A scoped tenant (non-empty residencyRegion)
// whose region is not in Regions fails closed with ErrRegionUnresolvable
// — there is no fallback to the default for a residency-scoped tenant,
// because routing held EU evidence to a US default bucket is exactly the
// cross-border transfer §12.8 forbids. An unscoped tenant
// (residencyRegion == "") resolves to the single-region Default.
//
// spec: §12.8 sub-step 2, line 883.
func (c Config) Resolve(residencyRegion string) (region string, esc RegionEscrow, err error) {
	if residencyRegion != "" {
		r, ok := c.Regions[residencyRegion]
		if !ok || r.Bucket == "" {
			return "", RegionEscrow{}, fmt.Errorf("%w: region %q has no legalHoldEscrow entry", ErrRegionUnresolvable, residencyRegion)
		}
		return residencyRegion, r, nil
	}
	if c.Default != nil && c.Default.Bucket != "" {
		return DefaultRegion, *c.Default, nil
	}
	return "", RegionEscrow{}, fmt.Errorf("%w: no single-region default configured", ErrRegionUnresolvable)
}

// KEKAlias is the §12.8 region-scoped escrow KEK identifier
// platform:legal_hold_escrow:<region>. It is the alias the KMS provider
// resolves to the region's escrow KEK and the value recorded as
// escrow_kek_id in the legal-hold ledger.
func KEKAlias(region string) string {
	return "platform:legal_hold_escrow:" + region
}

// EscrowObjectKey is the §12.8 sub-step 3 escrow bucket object key
// legal-hold-escrow/{tenantID}/{resourceType}/{resourceID}.
func EscrowObjectKey(tenantID, resourceType, resourceID string) string {
	return fmt.Sprintf("legal-hold-escrow/%s/%s/%s", tenantID, resourceType, resourceID)
}
