// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
)

// spec: §12.8 lines 880-889 — the gateway-side Phase 3.5 force-delete
// escrow migrator. F-12.8.2, F-24.10.2, F-24.10.5.

// stubCatalog is a minimal artifactcatalog.Store exposing only the held
// records the escrow migrator enumerates; the rest are no-ops.
type stubCatalog struct{ held []artifactcatalog.Record }

func (s stubCatalog) ListLegalHeld(_ context.Context, tenantID string) ([]artifactcatalog.Record, error) {
	var out []artifactcatalog.Record
	for _, r := range s.held {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (stubCatalog) Insert(context.Context, artifactcatalog.Record) error { return nil }
func (stubCatalog) Get(context.Context, string) (artifactcatalog.Record, error) {
	return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
}
func (stubCatalog) SoftDelete(context.Context, string, time.Time) error { return nil }
func (stubCatalog) Tombstone(context.Context, string) error             { return nil }
func (stubCatalog) HardPruneExpired(context.Context, time.Time) (int, error) {
	return 0, nil
}
func (stubCatalog) ListPrunable(context.Context, time.Time) ([]string, error) { return nil, nil }
func (stubCatalog) HardPruneURIs(context.Context, []string) (int, error)      { return 0, nil }
func (stubCatalog) ListBySession(context.Context, string, string) ([]artifactcatalog.Record, error) {
	return nil, nil
}

func (stubCatalog) SetLegalHold(context.Context, string, bool, string, time.Time, string) error {
	return nil
}

func (stubCatalog) IsLegalHeldAt(context.Context, string, string) (bool, error) { return false, nil }

func (stubCatalog) SessionsWithLegalHoldAndCheckpoints(context.Context) ([]artifactcatalog.SessionRef, error) {
	return nil, nil
}
func (stubCatalog) SumLiveBytes(context.Context, string) (int64, error) { return 0, nil }
func (stubCatalog) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }

func localKMS(t *testing.T) kms.Provider {
	t.Helper()
	p, err := kms.NewLocal(bytes.Repeat([]byte{0x5a}, kms.DEKSize))
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	return p
}

// An unconfigured escrow (no bucket) fails the override closed:
// ErrEscrowRegionUnresolvable, the DataResidencyViolationAttempt audit
// event, and the unresolvable counter.
func TestEscrowMigratorUnresolvableRegion_spec_12_8_883(t *testing.T) {
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	appender := &fakeAppender{}
	metrics, _ := gatewaymetrics.New()
	clock := fixedClock()

	mig := tenantEscrowMigrator{
		cfg:      escrowConfigFromFlags("", ""), // no escrow configured
		tenants:  tenants,
		blobs:    blobstore.NewMemoryStore(clock),
		cipher:   escrowCipherFactory(localKMS(t)),
		ledger:   escrowLedger{appender: appender, clock: clock},
		metrics:  metrics,
		appender: appender,
		clock:    clock,
	}
	_, err := mig.EscrowHolds(context.Background(), tenantdeletion.EscrowRequest{
		TenantID: "acme",
		JobID:    "acme",
		Holds:    []tenantdeletion.HeldResource{{ResourceType: "session", ResourceID: "sess-1"}},
	})
	if err != tenantdeletion.ErrEscrowRegionUnresolvable {
		t.Fatalf("err = %v, want ErrEscrowRegionUnresolvable", err)
	}
	var sawViolation bool
	for _, c := range appender.snapshot() {
		if c.eventType == "DataResidencyViolationAttempt" {
			sawViolation = true
		}
	}
	if !sawViolation {
		t.Error("DataResidencyViolationAttempt audit event not emitted on unresolvable region")
	}
}

// A configured escrow re-encrypts the held artifact blob under the
// region-scoped escrow KEK, migrates it to the escrow bucket, and records
// the ledger events.
func TestEscrowMigratorReEncryptsHeldArtifact_spec_12_8_880(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()
	provider := localKMS(t)
	blobs := blobstore.NewMemoryStore(clock)

	// Seed a held artifact blob in the source store.
	srcURI := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "sess-1",
		PartID:     "snapshot",
		TTL:        time.Hour,
		Encoding:   blobstore.Encoding,
	}
	plaintext := []byte("held-evidence-bytes")
	if _, err := blobs.Put(srcURI, "application/octet-stream", bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	tenants := tenantstore.NewMemory()
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}) // unscoped → single-region default
	catalog := stubCatalog{held: []artifactcatalog.Record{{
		URI: srcURI.String(), TenantID: "acme", SessionID: "sess-1", PartID: "snapshot",
		LegalHold: true, LegalHoldSetAt: time.Unix(1699990000, 0).UTC(),
	}}}
	appender := &fakeAppender{}
	metrics, _ := gatewaymetrics.New()

	mig := tenantEscrowMigrator{
		cfg:       escrowConfigFromFlags("escrow-default", "minio:9000"),
		tenants:   tenants,
		artifacts: catalog,
		blobs:     blobs,
		cipher:    escrowCipherFactory(provider),
		ledger:    escrowLedger{appender: appender, clock: clock},
		metrics:   metrics,
		appender:  appender,
		clock:     clock,
	}
	out, err := mig.EscrowHolds(ctx, tenantdeletion.EscrowRequest{
		TenantID: "acme",
		JobID:    "acme",
		Holds:    []tenantdeletion.HeldResource{{ResourceType: "artifact", ResourceID: "sess-1"}},
	})
	if err != nil {
		t.Fatalf("EscrowHolds: %v", err)
	}
	if out.ResolvedRegion != legalholdescrow.DefaultRegion {
		t.Fatalf("region = %q", out.ResolvedRegion)
	}
	if out.EscrowKEKID != "platform:legal_hold_escrow:default" {
		t.Fatalf("kek = %q", out.EscrowKEKID)
	}
	if len(out.EscrowObjectKeys) != 1 {
		t.Fatalf("escrow keys = %v", out.EscrowObjectKeys)
	}

	// The ledger recorded the residency decision and the migration.
	var sawRegion, sawEscrowed bool
	for _, c := range appender.snapshot() {
		switch c.eventType {
		case "legal_hold.escrow_region_resolved":
			sawRegion = true
		case "legal_hold.escrowed":
			sawEscrowed = true
		}
	}
	if !sawRegion || !sawEscrowed {
		t.Fatalf("ledger events: region=%v escrowed=%v", sawRegion, sawEscrowed)
	}

	// The escrow object is the re-encrypted payload, decryptable only under
	// the region-scoped escrow KEK — proving it was re-wrapped, not copied.
	escrowURI := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectType("legal_hold_escrow"),
		SessionID:  "escrow",
		Encoding:   blobstore.Encoding,
	}
	// Reconstruct the part id from the migrator's deterministic key: the
	// artifact resourceID is the base64url of the source URI, and the
	// EscrowWriter base64url-encodes the whole escrow key into the part id.
	resourceID := base64.RawURLEncoding.EncodeToString([]byte(srcURI.String()))
	wantKey := legalholdescrow.EscrowObjectKey("acme", "artifact", resourceID)
	escrowURI.PartID = base64.RawURLEncoding.EncodeToString([]byte(wantKey))
	_, rc, err := blobs.Get(escrowURI)
	if err != nil {
		t.Fatalf("escrow object not written: %v", err)
	}
	sealedBytes, _ := io.ReadAll(rc)
	_ = rc.Close()

	sealed, err := envelope.Decode(sealedBytes)
	if err != nil {
		t.Fatalf("escrow object is not an envelope blob: %v", err)
	}
	escrowCipher, err := envelope.New(provider, "platform:legal_hold_escrow:default")
	if err != nil {
		t.Fatalf("escrow cipher: %v", err)
	}
	got, err := escrowCipher.Open(ctx, sealed)
	if err != nil {
		t.Fatalf("open escrow under escrow KEK: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("escrow plaintext = %q, want %q", got, plaintext)
	}
}
