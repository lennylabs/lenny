// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
	"github.com/lennylabs/lenny/pkg/legalholdescrow/blobescrow"
)

// countingBlobs wraps a blob store and counts the reads and writes each
// store receives, so a test can assert which store the escrow path reads
// originals from and which it writes copies to.
type countingBlobs struct {
	blobstore.Store
	mu   sync.Mutex
	gets int
	puts int
}

func (c *countingBlobs) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	return c.Store.Get(u)
}

func (c *countingBlobs) Put(u blobstore.URI, mime string, r io.Reader) (string, error) {
	c.mu.Lock()
	c.puts++
	c.mu.Unlock()
	return c.Store.Put(u, mime, r)
}

func (c *countingBlobs) counts() (gets, puts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets, c.puts
}

// escrowFixture is a tenant with artifacts in an artifact store, a
// separate escrow store, and the escrow migrator and Releaser wired over
// them as buildAdminRouter wires them.
type escrowFixture struct {
	artifacts *countingBlobs
	escrow    *countingBlobs
	records   *legalholdescrow.MemRecordStore
	appender  *fakeAppender
	mig       tenantEscrowMigrator
	releaser  *legalholdescrow.Releaser
	uris      map[string]string // part id -> artifact URI
}

func newEscrowFixture(t *testing.T, catalog stubCatalog, parts ...string) *escrowFixture {
	t.Helper()
	ctx := context.Background()
	clock := fixedClock()
	fx := &escrowFixture{
		artifacts: &countingBlobs{Store: blobstore.NewMemoryStore(clock)},
		escrow:    &countingBlobs{Store: blobstore.NewMemoryStore(clock)},
		records:   legalholdescrow.NewMemRecordStore(),
		appender:  &fakeAppender{},
		uris:      map[string]string{},
	}
	for _, part := range parts {
		u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "sess-1", PartID: part, TTL: time.Hour, Encoding: blobstore.Encoding}
		if _, err := fx.artifacts.Store.Put(u, "application/octet-stream", bytes.NewReader([]byte("evidence-"+part))); err != nil {
			t.Fatalf("seed %s: %v", part, err)
		}
		fx.uris[part] = u.String()
	}
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(ctx, tenantstore.Tenant{ID: "acme"})
	metrics, _ := gatewaymetrics.New()
	ledger := escrowLedger{appender: fx.appender, records: fx.records, clock: clock}
	fx.mig = tenantEscrowMigrator{
		cfg:         escrowConfigFromFlags("escrow-default", ""),
		tenants:     tenants,
		artifacts:   catalog,
		blobs:       fx.artifacts,
		escrowBlobs: fx.escrow,
		records:     fx.records,
		cipher:      escrowCipherFactory(localKMS(t)),
		ledger:      ledger,
		metrics:     metrics,
		appender:    fx.appender,
		clock:       clock,
	}
	fx.releaser = &legalholdescrow.Releaser{
		Records: fx.records,
		// The counting wrapper hides the Tombstoner surface, so the
		// deleter runs over the escrow store it wraps.
		Deleter: blobescrow.Deleter{Blobs: fx.escrow.Store},
		Ledger:  ledger,
		Clock:   clock,
	}
	return fx
}

func catalogRecord(uri, part string, state artifactcatalog.State, held bool) artifactcatalog.Record {
	return artifactcatalog.Record{
		URI: uri, TenantID: "acme", SessionID: "sess-1", PartID: part, State: state,
		LegalHold: held, LegalHoldSetAt: time.Unix(1699990000, 0).UTC(),
	}
}

// TestEscrowHoldsReturnsHoldListingErrors_spec_12_8 pins that a failed
// ListLegalHeld or ListBySession aborts Phase 3.5 with the listing error
// and writes no escrow copy, because Phase 4's prefix delete would destroy
// any held artifact the listing missed.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope)), 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func TestEscrowHoldsReturnsHoldListingErrors_spec_12_8(t *testing.T) {
	listErr := errors.New("catalog unreachable")
	cases := []struct {
		name    string
		catalog stubCatalog
		holds   []tenantdeletion.HeldResource
	}{
		{"ListLegalHeld error", stubCatalog{heldErr: listErr}, []tenantdeletion.HeldResource{{ResourceType: "artifact", ResourceID: "sess-1"}}},
		{"ListBySession error", stubCatalog{sessionErr: listErr}, []tenantdeletion.HeldResource{{ResourceType: "session", ResourceID: "sess-1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newEscrowFixture(t, tc.catalog, "a")
			_, err := fx.mig.EscrowHolds(context.Background(), tenantdeletion.EscrowRequest{TenantID: "acme", JobID: "j", Holds: tc.holds})
			if !errors.Is(err, listErr) {
				t.Fatalf("err = %v, want the listing error", err)
			}
			if _, puts := fx.escrow.counts(); puts != 0 {
				t.Fatalf("escrow puts = %d after a listing error", puts)
			}
			if len(fx.appender.snapshot()) != 0 {
				t.Fatal("a ledger event was written after a listing error")
			}
		})
	}
}

// TestEscrowHoldsEscrowsEveryLiveArtifactOfAHeldSessionOnce_spec_12_8 pins
// that a session hold escrows every live catalog record of the session,
// that an artifact hold overlapping it does not escrow a record twice, and
// that a soft-deleted record is skipped. Before the fix a session hold
// escrowed no artifact, so Phase 4's prefix delete destroyed them.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestEscrowHoldsEscrowsEveryLiveArtifactOfAHeldSessionOnce_spec_12_8(t *testing.T) {
	probe := newEscrowFixture(t, stubCatalog{}, "a", "b", "c")
	catalog := stubCatalog{
		held: []artifactcatalog.Record{catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, true)},
		bySession: []artifactcatalog.Record{
			catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, true),
			catalogRecord(probe.uris["b"], "b", artifactcatalog.StateLive, false),
			catalogRecord(probe.uris["c"], "c", artifactcatalog.StateSoftDeleted, false),
		},
	}
	fx := newEscrowFixture(t, catalog, "a", "b", "c")
	out, err := fx.mig.EscrowHolds(context.Background(), tenantdeletion.EscrowRequest{
		TenantID: "acme", JobID: "j",
		Holds: []tenantdeletion.HeldResource{
			{ResourceType: "session", ResourceID: "sess-1"},
			{ResourceType: "artifact", ResourceID: "sess-1"},
		},
	})
	if err != nil {
		t.Fatalf("EscrowHolds: %v", err)
	}
	if len(out.EscrowObjectKeys) != 2 {
		t.Fatalf("escrow keys = %v, want one per live artifact", out.EscrowObjectKeys)
	}
	if out.EscrowObjectKeys[0] == out.EscrowObjectKeys[1] {
		t.Fatalf("artifact escrowed twice: %v", out.EscrowObjectKeys)
	}
	for _, part := range []string{"a", "b"} {
		recs, _ := fx.records.ActiveForArtifact(context.Background(), "acme", fx.uris[part])
		if len(recs) != 1 || recs[0].SessionID != "sess-1" {
			t.Fatalf("artifact %s escrow records = %+v, want one under sess-1", part, recs)
		}
	}
	if recs, _ := fx.records.ActiveForArtifact(context.Background(), "acme", fx.uris["c"]); len(recs) != 0 {
		t.Fatalf("soft-deleted artifact escrowed: %+v", recs)
	}
}

// TestEscrowLedgerAppendsUnderThePlatformTenant_spec_16_7 pins that every
// escrow ledger event is appended under the platform tenant with the
// original tenant as target_tenant_id, so the tenant's Phase 4 audit
// eraser never deletes it.
//
// spec: 16.7 (Section 25 Audit Events), 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestEscrowLedgerAppendsUnderThePlatformTenant_spec_16_7(t *testing.T) {
	probe := newEscrowFixture(t, stubCatalog{}, "a")
	fx := newEscrowFixture(t, stubCatalog{held: []artifactcatalog.Record{catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, true)}}, "a")
	ctx := context.Background()
	if _, err := fx.mig.EscrowHolds(ctx, tenantdeletion.EscrowRequest{
		TenantID: "acme", JobID: "j",
		Holds: []tenantdeletion.HeldResource{{ResourceType: "artifact", ResourceID: "sess-1"}},
	}); err != nil {
		t.Fatalf("EscrowHolds: %v", err)
	}
	if n, err := fx.releaser.ReleaseForSession(ctx, "acme", "sess-1", "alice"); err != nil || n != 1 {
		t.Fatalf("ReleaseForSession = %d, %v", n, err)
	}
	seen := map[string]bool{}
	for _, c := range fx.appender.snapshot() {
		seen[c.eventType] = true
		if c.tenant != jwtaudit.PlatformTenantID {
			t.Errorf("%s appended under tenant %q, want %q", c.eventType, c.tenant, jwtaudit.PlatformTenantID)
		}
		var p struct {
			Target string `json:"target_tenant_id"`
		}
		if err := json.Unmarshal(c.payload, &p); err != nil || p.Target != "acme" {
			t.Errorf("%s target_tenant_id = %q (%v), want acme", c.eventType, p.Target, err)
		}
	}
	for _, ev := range []string{"legal_hold.escrow_region_resolved", "legal_hold.escrowed", "legal_hold.escrow_released"} {
		if !seen[ev] {
			t.Errorf("%s not appended", ev)
		}
	}
}

// TestEscrowHoldsReentrySkipsEscrowedHolds_spec_12_8 pins the Phase 3.5
// re-entry: a second EscrowHolds after Phase 4 deleted the held originals
// succeeds without reading an original and returns the same escrow keys.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func TestEscrowHoldsReentrySkipsEscrowedHolds_spec_12_8(t *testing.T) {
	probe := newEscrowFixture(t, stubCatalog{}, "a", "b")
	catalog := stubCatalog{bySession: []artifactcatalog.Record{
		catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, false),
		catalogRecord(probe.uris["b"], "b", artifactcatalog.StateLive, false),
	}}
	fx := newEscrowFixture(t, catalog, "a", "b")
	ctx := context.Background()
	req := tenantdeletion.EscrowRequest{
		TenantID: "acme", JobID: "j",
		Holds: []tenantdeletion.HeldResource{{ResourceType: "session", ResourceID: "sess-1"}},
	}
	first, err := fx.mig.EscrowHolds(ctx, req)
	if err != nil {
		t.Fatalf("first EscrowHolds: %v", err)
	}
	if _, err := fx.artifacts.Store.(*blobstore.MemoryStore).DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("delete originals: %v", err)
	}
	getsBefore, _ := fx.artifacts.counts()
	second, err := fx.mig.EscrowHolds(ctx, req)
	if err != nil {
		t.Fatalf("re-entered EscrowHolds after the originals were deleted: %v", err)
	}
	if gets, _ := fx.artifacts.counts(); gets != getsBefore {
		t.Fatalf("re-entry read %d originals", gets-getsBefore)
	}
	if strings.Join(first.EscrowObjectKeys, ",") != strings.Join(second.EscrowObjectKeys, ",") {
		t.Fatalf("keys differ: %v then %v", first.EscrowObjectKeys, second.EscrowObjectKeys)
	}
}

// TestEscrowHoldsReadsArtifactStoreWritesEscrowStore_spec_12_8 pins the
// store split: originals are read only from the artifact store and copies
// written only to the escrow store, so the artifact store's tenant prefix
// delete leaves the escrow copy, which the Releaser deletes from the
// escrow store once the hold clears.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestEscrowHoldsReadsArtifactStoreWritesEscrowStore_spec_12_8(t *testing.T) {
	probe := newEscrowFixture(t, stubCatalog{}, "a")
	fx := newEscrowFixture(t, stubCatalog{held: []artifactcatalog.Record{catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, true)}}, "a")
	ctx := context.Background()
	out, err := fx.mig.EscrowHolds(ctx, tenantdeletion.EscrowRequest{
		TenantID: "acme", JobID: "j",
		Holds: []tenantdeletion.HeldResource{{ResourceType: "artifact", ResourceID: "sess-1"}},
	})
	if err != nil {
		t.Fatalf("EscrowHolds: %v", err)
	}
	if gets, puts := fx.artifacts.counts(); gets != 1 || puts != 0 {
		t.Fatalf("artifact store gets=%d puts=%d, want 1 and 0", gets, puts)
	}
	if gets, puts := fx.escrow.counts(); gets != 0 || puts != 1 {
		t.Fatalf("escrow store gets=%d puts=%d, want 0 and 1", gets, puts)
	}
	escrowURI := blobescrow.ObjectURI("acme", out.EscrowObjectKeys[0])
	if _, err := fx.artifacts.Store.(*blobstore.MemoryStore).DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("prefix delete: %v", err)
	}
	if _, rc, err := fx.escrow.Store.Get(escrowURI); err != nil {
		t.Fatalf("escrow copy gone after the artifact prefix delete: %v", err)
	} else {
		_ = rc.Close()
	}
	if n, err := fx.releaser.ReleaseForArtifact(ctx, "acme", fx.uris["a"], "alice"); err != nil || n != 1 {
		t.Fatalf("ReleaseForArtifact = %d, %v", n, err)
	}
	if _, _, err := fx.escrow.Store.Get(escrowURI); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("escrow copy after release: err = %v, want ErrNotFound", err)
	}
}

// TestEscrowHoldsWithoutEscrowStoreFailsClosed_spec_12_8 pins that a
// migrator with no escrow store refuses the override as an unresolvable
// region, even when an escrow bucket is named in its config.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestEscrowHoldsWithoutEscrowStoreFailsClosed_spec_12_8(t *testing.T) {
	probe := newEscrowFixture(t, stubCatalog{}, "a")
	fx := newEscrowFixture(t, stubCatalog{held: []artifactcatalog.Record{catalogRecord(probe.uris["a"], "a", artifactcatalog.StateLive, true)}}, "a")
	fx.mig.escrowBlobs = nil
	_, err := fx.mig.EscrowHolds(context.Background(), tenantdeletion.EscrowRequest{
		TenantID: "acme", JobID: "j",
		Holds: []tenantdeletion.HeldResource{{ResourceType: "artifact", ResourceID: "sess-1"}},
	})
	if !errors.Is(err, tenantdeletion.ErrEscrowRegionUnresolvable) {
		t.Fatalf("err = %v, want ErrEscrowRegionUnresolvable", err)
	}
	if gets, _ := fx.artifacts.counts(); gets != 0 {
		t.Fatalf("an original was read with no escrow store: gets=%d", gets)
	}
}
