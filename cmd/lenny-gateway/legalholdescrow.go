// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
	"github.com/lennylabs/lenny/pkg/legalholdescrow/blobescrow"
)

// This file wires the §12.8 Phase 3.5 force-delete override / escrow path
// into the gateway-hosted tenant-deletion controller: the EscrowMigrator
// that re-encrypts held evidence under the region-scoped escrow KEK and
// migrates it to the escrow bucket, and the OverrideSink that emits the
// gdpr.legal_hold_overridden_tenant critical event.
//
// spec: §12.8. F-12.8.2, F-24.10.2, F-24.10.5.

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

// escrowLedger records the §12.8 sub-step 2/4 ledger events as audit rows
// appended under the platform tenant with the original tenant as
// target_tenant_id (§16.7, the §11.7 cross-tenant write path), retained
// under audit.gdprRetentionDays, so the tenant's Phase 4 audit eraser
// never deletes them and they survive the tenant tombstone. When a
// RecordStore is wired it also
// persists the durable escrow record the §12.8 escrow-GC release
// path queries, and serves as the ReleaseLedger that emits
// legal_hold.escrow_released.
type escrowLedger struct {
	appender policy.AuditAppender
	records  legalholdescrow.RecordStore
	clock    func() time.Time
}

func (l escrowLedger) RegionResolved(ctx context.Context, ev legalholdescrow.RegionResolved) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":         ev.TenantID,
		"target_tenant_id": ev.TenantID,
		"tenantDeleteJob":  ev.TenantDeleteJob,
		"requestedRegion":  ev.RequestedRegion,
		"resolvedRegion":   ev.ResolvedRegion,
		"escrowKekId":      ev.EscrowKEKID,
	})
	if err != nil {
		return err
	}
	return l.appendPlatform(ctx, "legal_hold.escrow_region_resolved", payload)
}

// appendPlatform appends one ledger event under the platform tenant. The
// payload carries target_tenant_id so the §11.7 residency gate routes the
// row to the original tenant's region.
//
// spec: §12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope)), §16.7 (Section 25 Audit Events)
func (l escrowLedger) appendPlatform(ctx context.Context, eventType string, payload []byte) error {
	_, err := l.appender.Append(ctx, jwtaudit.PlatformTenantID, eventType, payload, l.clock())
	return err
}

func (l escrowLedger) Escrowed(ctx context.Context, ev legalholdescrow.Escrowed) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":          ev.TenantID,
		"target_tenant_id":  ev.TenantID,
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
	if err := l.appendPlatform(ctx, "legal_hold.escrowed", payload); err != nil {
		return err
	}
	// Persist the durable escrow record. It is the release pointer the
	// §12.8 escrow-GC release reads to locate and delete the escrow object
	// after Phase 4 has deleted the original, so a save failure aborts the
	// migration on this row rather than leaving an escrow object with no
	// release pointer.
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

// EscrowReleased emits the §16.7 legal_hold.escrow_released audit
// event on the platform tenant when the escrow-GC deletes a released
// object. escrowLedger satisfies legalholdescrow.ReleaseLedger.
func (l escrowLedger) EscrowReleased(ctx context.Context, ev legalholdescrow.Released) error {
	payload, err := json.Marshal(map[string]any{
		"tenantId":         ev.TenantID,
		"target_tenant_id": ev.TenantID,
		"resourceType":     ev.ResourceType,
		"resourceId":       ev.ResourceID,
		"escrowObjectKey":  ev.EscrowObjectKey,
		"escrowRegion":     ev.EscrowRegion,
		"clearedAt":        ev.ClearedAt.UTC().Format(time.RFC3339Nano),
		"clearedBy":        ev.ClearedBy,
	})
	if err != nil {
		return err
	}
	return l.appendPlatform(ctx, "legal_hold.escrow_released", payload)
}

// tenantEscrowMigrator is the gateway-side tenantdeletion.EscrowMigrator.
// It resolves the tenant's escrow region, enumerates the held artifact
// blobs, and drives the legalholdescrow.Migrator, which reads originals
// from the artifact store (blobs) and writes copies only to the escrow
// store (escrowBlobs). A nil escrow store (no escrow bucket configured)
// fails the override closed as an unresolvable region. On an unresolvable
// region it emits the DataResidencyViolationAttempt audit event,
// increments lenny_legal_hold_escrow_region_unresolvable_total, and
// returns tenantdeletion.ErrEscrowRegionUnresolvable so the controller
// pauses Phase 3.5 pending operator remediation.
//
// spec: §12.8.
type tenantEscrowMigrator struct {
	cfg       legalholdescrow.Config
	tenants   tenantstore.Store
	artifacts artifactcatalog.Store
	// blobs is the tenant-keyed artifact store the held originals are
	// read from.
	blobs blobstore.Store
	// escrowBlobs is the escrow store the sealed copies are written to.
	escrowBlobs blobstore.Store
	// records is the durable escrow record store; Migrate reads it to skip
	// a hold an earlier pass already escrowed.
	records  legalholdescrow.RecordStore
	cipher   legalholdescrow.CipherFactory
	ledger   legalholdescrow.Ledger
	metrics  *gatewaymetrics.Metrics
	appender policy.AuditAppender
	clock    func() time.Time
}

func (m tenantEscrowMigrator) EscrowHolds(ctx context.Context, req tenantdeletion.EscrowRequest) (tenantdeletion.EscrowOutcome, error) {
	residency := ""
	if t, err := m.tenants.Get(ctx, req.TenantID); err == nil {
		residency = t.DataResidencyRegion
	}

	// Resolve the held artifact blobs so they can be re-encrypted. A
	// listing error aborts Phase 3.5, because Phase 4's prefix delete would
	// otherwise destroy held artifacts that were never escrowed.
	holds, err := m.resolveHolds(ctx, req)
	if err != nil {
		return tenantdeletion.EscrowOutcome{}, err
	}

	cfg := m.cfg
	if m.escrowBlobs == nil {
		// No escrow store: fail closed as an unresolvable region.
		cfg = legalholdescrow.Config{}
	}
	mig := &legalholdescrow.Migrator{
		Config:  cfg,
		Cipher:  m.cipher,
		Source:  blobescrow.Source{Blobs: m.blobs},
		Escrow:  blobescrow.Writer{Blobs: m.escrowBlobs, TenantID: req.TenantID},
		Ledger:  m.ledger,
		Records: m.records,
		Clock:   m.clock,
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
// escrow inputs. An artifact hold (ResourceID the owning session) yields
// each held artifact of that session from ListLegalHeld; a session hold
// yields every live catalog record of the session from ListBySession,
// because Phase 4's prefix delete removes every original. Each artifact is
// escrowed once however many holds cover it, and a non-live record is
// skipped. Session-level tuples are also carried as blobless entries so
// the override is recorded even with no held blob. Any listing error is
// returned.
//
// spec: §12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func (m tenantEscrowMigrator) resolveHolds(ctx context.Context, req tenantdeletion.EscrowRequest) ([]legalholdescrow.HeldArtifact, error) {
	heldBySession, err := m.heldArtifactsBySession(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}
	var holds []legalholdescrow.HeldArtifact
	seen := map[string]bool{}
	add := func(sessionID string, rec artifactcatalog.Record) {
		if seen[rec.URI] {
			return
		}
		seen[rec.URI] = true
		holds = append(holds, heldArtifactFromRecord(sessionID, rec))
	}
	for _, h := range req.Holds {
		switch h.ResourceType {
		case "artifact":
			for _, rec := range heldBySession[h.ResourceID] {
				add(h.ResourceID, rec)
			}
			continue
		case "session":
			recs, err := m.liveSessionArtifacts(ctx, req.TenantID, h.ResourceID)
			if err != nil {
				return nil, err
			}
			for _, rec := range recs {
				add(h.ResourceID, rec)
			}
		}
		// session / audit_range / workspace_snapshot: recorded, no blob body.
		holds = append(holds, legalholdescrow.HeldArtifact{
			ResourceType: h.ResourceType,
			ResourceID:   h.ResourceID,
		})
	}
	return holds, nil
}

// heldArtifactsBySession groups the tenant's artifact-held catalog records
// by owning session. A ListLegalHeld error is returned.
func (m tenantEscrowMigrator) heldArtifactsBySession(ctx context.Context, tenantID string) (map[string][]artifactcatalog.Record, error) {
	bySession := map[string][]artifactcatalog.Record{}
	if m.artifacts == nil {
		return bySession, nil
	}
	recs, err := m.artifacts.ListLegalHeld(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list legal-held artifacts for tenant %q: %w", tenantID, err)
	}
	for _, r := range recs {
		bySession[r.SessionID] = append(bySession[r.SessionID], r)
	}
	return bySession, nil
}

// liveSessionArtifacts returns the live catalog records of a held session.
// A ListBySession error is returned.
func (m tenantEscrowMigrator) liveSessionArtifacts(ctx context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	if m.artifacts == nil {
		return nil, nil
	}
	recs, err := m.artifacts.ListBySession(ctx, tenantID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts of held session %q: %w", sessionID, err)
	}
	live := recs[:0:0]
	for _, r := range recs {
		if r.State == artifactcatalog.StateLive {
			live = append(live, r)
		}
	}
	return live, nil
}

// heldArtifactFromRecord builds the escrow input for one catalog record.
// The §12.8 escrow-GC release keys are the owning session and the raw
// artifact URI, so clearing either the session hold or the artifact's own
// hold releases it.
func heldArtifactFromRecord(sessionID string, rec artifactcatalog.Record) legalholdescrow.HeldArtifact {
	return legalholdescrow.HeldArtifact{
		ResourceType: "artifact",
		ResourceID:   base64.RawURLEncoding.EncodeToString([]byte(rec.URI)),
		BlobURI:      rec.URI,
		HoldSetAt:    rec.LegalHoldSetAt,
		SessionID:    sessionID,
		ArtifactURI:  rec.URI,
	}
}

// reportResidencyViolation emits the §12.8 DataResidencyViolationAttempt
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
