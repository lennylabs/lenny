// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
)

// This file wires the §12.8 Phase 3.5 force-delete override / escrow path
// into the gateway-hosted tenant-deletion controller: the EscrowMigrator
// that re-encrypts held evidence under the region-scoped escrow KEK and
// migrates it to the escrow bucket, and the OverrideSink that emits the
// gdpr.legal_hold_overridden_tenant critical event.
//
// spec: §12.8 lines 880-889. F-12.8.2, F-24.10.2, F-24.10.5.

// escrowCipherAdapter adapts pkg/kms/envelope.Cipher to the
// legalholdescrow.Cipher seam, returning the encoded sealed blob.
type escrowCipherAdapter struct{ c *envelope.Cipher }

func (a escrowCipherAdapter) Seal(ctx context.Context, plaintext []byte) ([]byte, error) {
	sealed, err := a.c.Seal(ctx, plaintext)
	if err != nil {
		return nil, err
	}
	return envelope.Encode(sealed)
}

// escrowCipherFactory builds an envelope.Cipher bound to a region-scoped
// escrow KEK alias over the resolved §4 kms.Provider.
func escrowCipherFactory(provider kms.Provider) legalholdescrow.CipherFactory {
	return func(alias string) (legalholdescrow.Cipher, error) {
		c, err := envelope.New(provider, alias)
		if err != nil {
			return nil, err
		}
		return escrowCipherAdapter{c: c}, nil
	}
}

// blobEscrowSource reads a held artifact's plaintext from the live
// tenant-keyed ArtifactStore (the §4 store decrypts under the tenant KEK,
// still live at Phase 3.5).
type blobEscrowSource struct{ blobs blobstore.Store }

func (s blobEscrowSource) Read(_ context.Context, a legalholdescrow.HeldArtifact) ([]byte, error) {
	u, err := blobstore.ParseURI(a.BlobURI)
	if err != nil {
		return nil, err
	}
	_, rc, err := s.blobs.Get(u)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// blobEscrowWriter migrates re-encrypted payloads into the region-scoped
// legal-hold escrow bucket. The escrow object lives under the
// `legal_hold_escrow` object type, keyed by the §12.8 escrow object key.
// In production the bucket is a MinIO/S3 endpoint with COMPLIANCE-mode
// object-lock (retain-until-hold-release); the escrow-GC and the Phase 4
// skip logic enforce the retention contract at the platform layer.
type blobEscrowWriter struct {
	blobs    blobstore.Store
	tenantID string
}

func (w blobEscrowWriter) Write(_ context.Context, key string, sealed []byte) error {
	u := blobstore.URI{
		TenantID:   w.tenantID,
		ObjectType: blobstore.ObjectType("legal_hold_escrow"),
		SessionID:  "escrow",
		// The §12.8 escrow object key carries slashes and the original blob
		// URI; base64url it into the part id so it is a single valid object
		// segment across every backend.
		PartID:   base64.RawURLEncoding.EncodeToString([]byte(key)),
		Encoding: blobstore.Encoding,
	}
	if _, err := w.blobs.Put(u, "application/octet-stream", bytes.NewReader(sealed)); err != nil {
		// An idempotent re-entry overwrites the same key; a backend that
		// rejects an overwrite (the §4.5 immutability guarantee) is treated
		// as already-escrowed.
		if errors.Is(err, blobstore.ErrConflict) {
			return nil
		}
		return err
	}
	return nil
}

// escrowLedger records the §12.8 sub-step 2/4 ledger events as audit rows
// on the platform tenant (retained under audit.gdprRetentionDays so they
// survive the tenant tombstone). When a RecordStore is wired it also
// persists the durable escrow record the §12.8 line 884 escrow-GC release
// path queries, and serves as the ReleaseLedger that emits
// legal_hold.escrow_released.
type escrowLedger struct {
	appender policy.AuditAppender
	records  legalholdescrow.RecordStore
	clock    func() time.Time
}

func (l escrowLedger) RegionResolved(ctx context.Context, ev legalholdescrow.RegionResolved) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":        ev.TenantID,
		"tenantDeleteJob": ev.TenantDeleteJob,
		"requestedRegion": ev.RequestedRegion,
		"resolvedRegion":  ev.ResolvedRegion,
		"escrowKekId":     ev.EscrowKEKID,
	})
	if err != nil {
		return err
	}
	_, err = l.appender.Append(ctx, ev.TenantID, "legal_hold.escrow_region_resolved", payload, l.clock())
	return err
}

func (l escrowLedger) Escrowed(ctx context.Context, ev legalholdescrow.Escrowed) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":          ev.TenantID,
		"resourceType":      ev.ResourceType,
		"resourceId":        ev.ResourceID,
		"originalHoldSetAt": ev.OriginalHoldSet.UTC().Format(time.RFC3339Nano),
		"escrowObjectKey":   ev.EscrowObjectKey,
		"escrowRegion":      ev.EscrowRegion,
		"escrowKekId":       ev.EscrowKEKID,
		"tenantDeleteJob":   ev.TenantDeleteJob,
		"migratedAt":        ev.MigratedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	if _, err := l.appender.Append(ctx, ev.TenantID, "legal_hold.escrowed", payload, l.clock()); err != nil {
		return err
	}
	// Persist the durable escrow record the §12.8 line 884 release path
	// queries. The save shares the migration's logical step (it is the
	// "mark the record legal_hold_escrow: true" marker), so a save failure
	// aborts the migration on this row rather than leaving an escrow object
	// with no release pointer.
	if l.records != nil {
		return l.records.Save(ctx, legalholdescrow.Record{
			TenantID:        ev.TenantID,
			ResourceType:    ev.ResourceType,
			ResourceID:      ev.ResourceID,
			EscrowObjectKey: ev.EscrowObjectKey,
			EscrowRegion:    ev.EscrowRegion,
			EscrowKEKID:     ev.EscrowKEKID,
			TenantDeleteJob: ev.TenantDeleteJob,
			SessionID:       ev.SessionID,
			ArtifactURI:     ev.ArtifactURI,
			OriginalHoldSet: ev.OriginalHoldSet,
			MigratedAt:      ev.MigratedAt,
		})
	}
	return nil
}

// EscrowReleased emits the §16.7 line 694 legal_hold.escrow_released audit
// event on the platform tenant when the escrow-GC deletes a released
// object. escrowLedger satisfies legalholdescrow.ReleaseLedger.
func (l escrowLedger) EscrowReleased(ctx context.Context, ev legalholdescrow.Released) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":        ev.TenantID,
		"resourceType":    ev.ResourceType,
		"resourceId":      ev.ResourceID,
		"escrowObjectKey": ev.EscrowObjectKey,
		"escrowRegion":    ev.EscrowRegion,
		"clearedAt":       ev.ClearedAt.UTC().Format(time.RFC3339Nano),
		"clearedBy":       ev.ClearedBy,
	})
	if err != nil {
		return err
	}
	_, err = l.appender.Append(ctx, ev.TenantID, "legal_hold.escrow_released", payload, l.clock())
	return err
}

// blobEscrowDeleter physically removes a released escrow object from the
// escrow bucket. The escrow object lives at the same blob URI the
// blobEscrowWriter wrote (PartID = base64url(escrow object key)); clearing
// the hold lifts the retain-until-hold-release object lock so the object
// becomes deletable. spec: §12.8 line 884.
type blobEscrowDeleter struct {
	blobs blobstore.Store
}

func (d blobEscrowDeleter) Delete(_ context.Context, tenantID, _ string, key string) error {
	// HardDeleteObject lives on the §4.5 Tombstoner sub-interface; the
	// escrow GC needs a physical single-object delete (the retain-until-
	// hold-release lock having been lifted by the clear). A backend that
	// cannot hard-delete cannot GC escrow.
	tomb, ok := d.blobs.(blobstore.Tombstoner)
	if !ok {
		return errors.New("legalholdescrow: blob store does not support escrow object deletion")
	}
	u := blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectType("legal_hold_escrow"),
		SessionID:  "escrow",
		PartID:     base64.RawURLEncoding.EncodeToString([]byte(key)),
		Encoding:   blobstore.Encoding,
	}
	if err := tomb.HardDeleteObject(u); err != nil {
		// An already-absent object is a no-op: a re-cleared hold must not
		// fail the release.
		if errors.Is(err, blobstore.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// tenantEscrowMigrator is the gateway-side tenantdeletion.EscrowMigrator.
// It resolves the tenant's escrow region, enumerates the held artifact
// blobs, and drives the legalholdescrow.Migrator. On an unresolvable
// region it emits the DataResidencyViolationAttempt audit event,
// increments lenny_legal_hold_escrow_region_unresolvable_total, and
// returns tenantdeletion.ErrEscrowRegionUnresolvable so the controller
// pauses Phase 3.5 pending operator remediation.
//
// spec: §12.8 lines 880-889.
type tenantEscrowMigrator struct {
	cfg       legalholdescrow.Config
	tenants   tenantstore.Store
	artifacts artifactcatalog.Store
	blobs     blobstore.Store
	cipher    legalholdescrow.CipherFactory
	ledger    legalholdescrow.Ledger
	metrics   *gatewaymetrics.Metrics
	appender  policy.AuditAppender
	clock     func() time.Time
}

func (m tenantEscrowMigrator) EscrowHolds(ctx context.Context, req tenantdeletion.EscrowRequest) (tenantdeletion.EscrowOutcome, error) {
	residency := ""
	if t, err := m.tenants.Get(ctx, req.TenantID); err == nil {
		residency = t.DataResidencyRegion
	}

	// Resolve the held artifact blobs (those carrying a hold) so they can
	// be re-encrypted; the session-level hold tuples are carried as
	// blobless entries so the override is recorded even with no held blob.
	holds := m.resolveHolds(ctx, req)

	mig := &legalholdescrow.Migrator{
		Config: m.cfg,
		Cipher: m.cipher,
		Source: blobEscrowSource{blobs: m.blobs},
		Escrow: blobEscrowWriter{blobs: m.blobs, tenantID: req.TenantID},
		Ledger: m.ledger,
		Clock:  m.clock,
	}
	res, err := mig.Migrate(ctx, legalholdescrow.Input{
		TenantID:        req.TenantID,
		ResidencyRegion: residency,
		JobID:           req.JobID,
		Holds:           holds,
	})
	if err != nil {
		if errors.Is(err, legalholdescrow.ErrRegionUnresolvable) {
			m.reportResidencyViolation(ctx, req.TenantID, residency)
			return tenantdeletion.EscrowOutcome{}, tenantdeletion.ErrEscrowRegionUnresolvable
		}
		return tenantdeletion.EscrowOutcome{}, err
	}
	return tenantdeletion.EscrowOutcome{
		ResolvedRegion:   res.ResolvedRegion,
		EscrowKEKID:      res.EscrowKEKID,
		EscrowObjectKeys: res.EscrowObjectKeys,
	}, nil
}

// resolveHolds maps the controller's held-resource tuples to concrete
// escrow inputs: each held artifact blob under the tenant is resolved to
// its blob URI, and session-level holds are carried as blobless entries.
func (m tenantEscrowMigrator) resolveHolds(ctx context.Context, req tenantdeletion.EscrowRequest) []legalholdescrow.HeldArtifact {
	var artifactRecs []artifactcatalog.Record
	if m.artifacts != nil {
		recs, err := m.artifacts.ListLegalHeld(ctx, req.TenantID)
		if err == nil {
			artifactRecs = recs
		}
	}
	bySession := map[string][]artifactcatalog.Record{}
	for _, r := range artifactRecs {
		bySession[r.SessionID] = append(bySession[r.SessionID], r)
	}

	var holds []legalholdescrow.HeldArtifact
	seen := map[string]bool{}
	for _, h := range req.Holds {
		if h.ResourceType == "artifact" {
			for _, rec := range bySession[h.ResourceID] {
				if seen[rec.URI] {
					continue
				}
				seen[rec.URI] = true
				holds = append(holds, legalholdescrow.HeldArtifact{
					ResourceType: "artifact",
					ResourceID:   base64.RawURLEncoding.EncodeToString([]byte(rec.URI)),
					BlobURI:      rec.URI,
					HoldSetAt:    rec.LegalHoldSetAt,
					// §12.8 line 884 escrow-GC release keys: the owning session
					// (h.ResourceID) and the raw artifact URI, so clearing either
					// the session hold or the artifact's own hold releases it.
					SessionID:   h.ResourceID,
					ArtifactURI: rec.URI,
				})
			}
			continue
		}
		// session / audit_range / workspace_snapshot: recorded, no blob body.
		holds = append(holds, legalholdescrow.HeldArtifact{
			ResourceType: h.ResourceType,
			ResourceID:   h.ResourceID,
		})
	}
	return holds
}

// reportResidencyViolation emits the §12.8 line 883 DataResidencyViolationAttempt
// audit event and increments the unresolvable counter when escrow-region
// resolution fails.
func (m tenantEscrowMigrator) reportResidencyViolation(ctx context.Context, tenantID, requestedRegion string) {
	m.metrics.IncLegalHoldEscrowRegionUnresolvable(tenantID)
	if m.appender == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"operation":       "legal_hold_escrow",
		"tenantId":        tenantID,
		"requestedRegion": requestedRegion,
	})
	if err != nil {
		return
	}
	_, _ = m.appender.Append(ctx, tenantID, "DataResidencyViolationAttempt", payload, m.clock())
}

// escrowOverrideSink is the gateway-side tenantdeletion.OverrideSink: it
// emits the §12.8 gdpr.legal_hold_overridden_tenant critical audit event
// and increments lenny_gdpr_legal_hold_overridden_tenant_total once the
// Phase 3.5 escrow sub-steps complete.
type escrowOverrideSink struct {
	appender policy.AuditAppender
	metrics  *gatewaymetrics.Metrics
	clock    func() time.Time
}

func (s escrowOverrideSink) OverrideApplied(ctx context.Context, ev tenantdeletion.OverrideAppliedEvent) {
	s.metrics.IncLegalHoldOverriddenTenant(ev.TenantID)
	if s.appender == nil {
		return
	}
	tuples := make([]map[string]string, 0, len(ev.OverriddenHolds))
	for _, h := range ev.OverriddenHolds {
		tuples = append(tuples, map[string]string{"resourceType": h.ResourceType, "resourceId": h.ResourceID})
	}
	payload, err := json.Marshal(map[string]any{
		"tenantId":              ev.TenantID,
		"jobId":                 ev.JobID,
		"overrideBy":            ev.OverrideBy,
		"overrideJustification": ev.Justification,
		"overrideAt":            ev.OverrideAt.UTC().Format(time.RFC3339Nano),
		"overriddenHolds":       tuples,
		"escrowObjectKeys":      ev.EscrowObjectKeys,
	})
	if err != nil {
		return
	}
	_, _ = s.appender.Append(ctx, ev.TenantID, "gdpr.legal_hold_overridden_tenant", payload, s.clock())
}

// escrowConfigFromFlags builds the §12.8 single-region escrow Config from
// the gateway flags. An unset bucket leaves the Config empty so a
// force-delete override fails closed (LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE).
func escrowConfigFromFlags(bucket, endpoint string) legalholdescrow.Config {
	if bucket == "" {
		return legalholdescrow.Config{}
	}
	return legalholdescrow.Config{
		Default: &legalholdescrow.RegionEscrow{Bucket: bucket, Endpoint: endpoint},
	}
}
