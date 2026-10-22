// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"fmt"
	"time"
)

// HeldArtifact is one source resource the override path escrows. The
// caller resolves the held resource tuple (from the §12.8 Phase 3.5
// enumeration) to a concrete blob reference plus the original hold-set
// instant for the ledger record.
type HeldArtifact struct {
	// ResourceType is the §12.8 legal-hold ledger resource class
	// (session, artifact, audit_range, workspace_snapshot).
	ResourceType string
	// ResourceID identifies the held resource within its type.
	ResourceID string
	// BlobURI is the source blob the SourceReader reads the held
	// ciphertext from. Empty for a resource with no blob body (skipped).
	BlobURI string
	// HoldSetAt is the instant the hold was placed, recorded as
	// original_hold_set_at in the legal_hold.escrowed event.
	HoldSetAt time.Time
	// SessionID is the owning session of an escrowed artifact, recorded on
	// the escrow record so clearing the session hold releases it. Empty for
	// a non-session-scoped resource.
	SessionID string
	// ArtifactURI is the raw artifact URI (BlobURI), recorded on the escrow
	// record so clearing the artifact's own hold releases exactly it.
	ArtifactURI string
}

// SourceReader reads a held resource's plaintext from the live
// tenant-keyed store. The tenant KEK is still live at Phase 3.5 (Phase
// 4a destroys it only after escrow completes), so the source store
// decrypts the payload under the tenant KEK.
type SourceReader interface {
	// Read returns the held resource's plaintext. A resource the source
	// cannot find is a hard error: the override must not silently drop
	// held evidence.
	Read(ctx context.Context, a HeldArtifact) ([]byte, error)
}

// EscrowWriter seals re-encrypted payloads into the region-scoped
// legal-hold escrow bucket. The write is immutable under
// retain-until-hold-release object lock (COMPLIANCE mode on a MinIO/S3
// backend). The escrow bucket sits outside every tenant store, so Phase
// 4's DeleteByTenant deletes the held originals and leaves the escrow
// copy, which the escrow-GC release deletes only after the hold clears.
type EscrowWriter interface {
	// Write stores the sealed payload at the escrow object key. It is
	// idempotent: re-writing the same key during a re-entered Phase 3.5
	// is a no-op, satisfying §12.8 "the segregation is idempotent".
	Write(ctx context.Context, key string, sealed []byte) error
}

// Cipher seals a plaintext payload under a region-scoped escrow KEK,
// returning the §4 envelope-encoded blob (wrapped DEK + nonce +
// ciphertext). pkg/kms/envelope.Cipher adapts to it.
type Cipher interface {
	// Seal envelope-encrypts plaintext under the escrow KEK the Cipher
	// was built for and returns the encoded blob to persist.
	Seal(ctx context.Context, plaintext []byte) ([]byte, error)
}

// CipherFactory builds an escrow Cipher bound to the given KEK alias
// (platform:legal_hold_escrow:<region>). The gateway backs it with
// envelope.New over the resolved §4 kms.Provider.
type CipherFactory func(kekAlias string) (Cipher, error)

// RegionResolved is the §12.8 sub-step 2 legal_hold.escrow_region_resolved
// INFO ledger event recording the residency-at-write-time decision.
type RegionResolved struct {
	TenantID        string
	TenantDeleteJob string
	RequestedRegion string
	ResolvedRegion  string
	EscrowKEKID     string
}

// Escrowed is the §12.8 sub-step 4 legal_hold.escrowed ledger event
// written for each migrated resource.
type Escrowed struct {
	TenantID        string
	ResourceType    string
	ResourceID      string
	OriginalHoldSet time.Time
	EscrowObjectKey string
	EscrowRegion    string
	EscrowKEKID     string
	TenantDeleteJob string
	MigratedAt      time.Time
	// SessionID / ArtifactURI carry the release-lookup keys onto the escrow
	// record the Ledger persists, so the §12.8 escrow-GC release
	// can resolve the objects a cleared hold protected.
	SessionID   string
	ArtifactURI string
}

// Ledger records the §12.8 sub-step 2/4 ledger events and persists the
// durable escrow record. That record is the release pointer: the
// escrow-GC release reads it to locate and delete the escrow object once
// the hold clears, after Phase 4 has deleted the original. A ledger write
// is routed to the region's platform-Postgres (CMP-058); a routing
// failure aborts the migration on that row (§12.8 sub-step 4
// fail-closed) rather than leaving escrow ciphertext with no pointer.
type Ledger interface {
	// RegionResolved records the residency decision once per override.
	RegionResolved(ctx context.Context, ev RegionResolved) error
	// Escrowed records one migrated resource and persists its escrow
	// record.
	Escrowed(ctx context.Context, ev Escrowed) error
}

// Migrator performs the §12.8 Phase 3.5 override sub-steps 2-4: region
// resolution, re-encryption under the region-scoped escrow KEK,
// migration to the escrow bucket, and the ledger record. Sub-step 1
// (held-resource enumeration) is the caller's; the resolved holds are
// passed to Migrate.
type Migrator struct {
	// Config is the deployment's region-scoped escrow map plus default.
	Config Config
	// Cipher builds an escrow Cipher for a region's KEK alias.
	Cipher CipherFactory
	// Source reads held resource ciphertext from the live tenant store.
	Source SourceReader
	// Escrow writes sealed payloads into the region-scoped escrow bucket.
	Escrow EscrowWriter
	// Ledger records the §12.8 ledger events and the escrow record.
	Ledger Ledger
	// Records is the durable escrow record store. Migrate reads it to skip
	// a hold that an earlier pass already escrowed (§12.8 Phase 3.5
	// re-entry), so a re-entered migration whose originals Phase 4 already
	// deleted does not read a missing original. Nil skips the lookup.
	Records RecordStore
	// Clock supplies the migration timestamp. Nil defaults to time.Now UTC.
	Clock func() time.Time
}

// Input is one §12.8 override migration request.
type Input struct {
	// TenantID is the tenant being force-deleted.
	TenantID string
	// ResidencyRegion is the tenant's dataResidencyRegion (empty for an
	// unscoped tenant, which resolves to the single-region default).
	ResidencyRegion string
	// JobID is the tenant-delete job id, recorded on the ledger events.
	JobID string
	// Holds are the resolved held resources to escrow (§12.8 sub-step 1).
	Holds []HeldArtifact
}

// Result is the outcome of a successful override migration, carried onto
// the deletion job for the §12.8 gdpr.legal_hold_overridden_tenant event.
type Result struct {
	// ResolvedRegion is the region the evidence was escrowed to.
	ResolvedRegion string
	// EscrowKEKID is the region-scoped escrow KEK identifier.
	EscrowKEKID string
	// EscrowObjectKeys are the escrow bucket keys written, in hold order.
	EscrowObjectKeys []string
}

func (m *Migrator) now() time.Time {
	if m.Clock != nil {
		return m.Clock()
	}
	return time.Now().UTC()
}

// Migrate runs the §12.8 sub-steps 2-4 for the resolved holds. It is
// idempotent: a hold with an active escrow record is skipped and its
// recorded escrow key returned, and a hold escrowed without a record (a
// ledger failure after the write) is re-sealed and re-written under the
// same key. It returns ErrRegionUnresolvable when the tenant's
// region has no escrow configuration (the caller maps this to
// LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE, emits the
// DataResidencyViolationAttempt audit event, raises the
// LegalHoldEscrowResidencyViolation alert, and pauses Phase 3.5).
func (m *Migrator) Migrate(ctx context.Context, in Input) (Result, error) {
	if len(in.Holds) == 0 {
		return Result{}, ErrNoHolds
	}
	// Sub-step 2: resolve the target region. A failure here is the
	// fail-closed residency gate — no escrow ciphertext is written.
	region, _, err := m.Config.Resolve(in.ResidencyRegion)
	if err != nil {
		return Result{}, err
	}
	kekAlias := KEKAlias(region)

	cipher, err := m.Cipher(kekAlias)
	if err != nil {
		return Result{}, fmt.Errorf("legalholdescrow: build escrow cipher for %q: %w", kekAlias, err)
	}

	// legal_hold.escrow_region_resolved (INFO): records the residency
	// decision before any ciphertext is moved.
	if err := m.Ledger.RegionResolved(ctx, RegionResolved{
		TenantID:        in.TenantID,
		TenantDeleteJob: in.JobID,
		RequestedRegion: in.ResidencyRegion,
		ResolvedRegion:  region,
		EscrowKEKID:     kekAlias,
	}); err != nil {
		return Result{}, fmt.Errorf("legalholdescrow: record region-resolved: %w", err)
	}

	res := Result{ResolvedRegion: region, EscrowKEKID: kekAlias}
	for _, h := range in.Holds {
		if h.BlobURI == "" {
			// A held resource with no blob body (e.g. a session row whose
			// artifacts are escrowed under their own artifact holds) has
			// nothing to re-encrypt; the hold itself stays set.
			continue
		}
		if key, done, err := m.alreadyEscrowed(ctx, in.TenantID, h); err != nil {
			return Result{}, err
		} else if done {
			res.EscrowObjectKeys = append(res.EscrowObjectKeys, key)
			continue
		}
		plaintext, err := m.Source.Read(ctx, h)
		if err != nil {
			return Result{}, fmt.Errorf("legalholdescrow: read held %s/%s: %w", h.ResourceType, h.ResourceID, err)
		}
		sealed, err := cipher.Seal(ctx, plaintext)
		if err != nil {
			return Result{}, fmt.Errorf("legalholdescrow: seal %s/%s under escrow KEK: %w", h.ResourceType, h.ResourceID, err)
		}
		key := EscrowObjectKey(in.TenantID, h.ResourceType, h.ResourceID)
		if err := m.Escrow.Write(ctx, key, sealed); err != nil {
			return Result{}, fmt.Errorf("legalholdescrow: write escrow %s: %w", key, err)
		}
		// Sub-step 4: the ledger record is the durable pointer the
		// escrow-GC release reads, and Phase 4 does not proceed without it.
		// A ledger failure aborts the migration on this row (the escrow
		// object exists but is not yet pointed at, so the retry re-records
		// it).
		if err := m.Ledger.Escrowed(ctx, Escrowed{
			TenantID:        in.TenantID,
			ResourceType:    h.ResourceType,
			ResourceID:      h.ResourceID,
			OriginalHoldSet: h.HoldSetAt,
			EscrowObjectKey: key,
			EscrowRegion:    region,
			EscrowKEKID:     kekAlias,
			TenantDeleteJob: in.JobID,
			MigratedAt:      m.now(),
			SessionID:       h.SessionID,
			ArtifactURI:     h.ArtifactURI,
		}); err != nil {
			return Result{}, fmt.Errorf("legalholdescrow: record escrowed %s: %w", key, err)
		}
		res.EscrowObjectKeys = append(res.EscrowObjectKeys, key)
	}
	return res, nil
}

// alreadyEscrowed reports whether an earlier Phase 3.5 pass escrowed h,
// returning the recorded escrow object key. A lookup error is returned
// so the migration aborts rather than reading an original that Phase 4
// may already have deleted.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func (m *Migrator) alreadyEscrowed(ctx context.Context, tenantID string, h HeldArtifact) (string, bool, error) {
	if m.Records == nil || h.ArtifactURI == "" {
		return "", false, nil
	}
	recs, err := m.Records.ActiveForArtifact(ctx, tenantID, h.ArtifactURI)
	if err != nil {
		return "", false, fmt.Errorf("legalholdescrow: look up escrow record for %s/%s: %w", h.ResourceType, h.ResourceID, err)
	}
	if len(recs) == 0 {
		return "", false, nil
	}
	return recs[0].EscrowObjectKey, true, nil
}
