// SPDX-License-Identifier: MIT

package cataloging

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
)

// fakeCatalog is a thread-safe in-memory artifactcatalog.Store for
// unit testing the decorator. It mirrors the public surface of
// artifactcatalog.PgStore without a Postgres dependency.
type fakeCatalog struct {
	mu        sync.Mutex
	rows      map[string]artifactcatalog.Record
	insertErr error
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{rows: map[string]artifactcatalog.Record{}}
}

func (f *fakeCatalog) Insert(_ context.Context, r artifactcatalog.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.rows[r.URI] = r
	return nil
}

func (f *fakeCatalog) Get(_ context.Context, uri string) (artifactcatalog.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[uri]
	if !ok {
		return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
	}
	return r, nil
}

func (f *fakeCatalog) SumLiveBytes(_ context.Context, tenantID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum int64
	for _, r := range f.rows {
		if r.TenantID == tenantID && r.State == artifactcatalog.StateLive {
			sum += r.SizeBytes
		}
	}
	return sum, nil
}

func (f *fakeCatalog) SoftDelete(_ context.Context, uri string, deadline time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	if r.State != artifactcatalog.StateLive {
		return artifactcatalog.ErrNotFound
	}
	r.State = artifactcatalog.StateSoftDeleted
	r.SoftDeletedAt = time.Now().UTC()
	r.TombstoneDeadline = deadline
	f.rows[uri] = r
	return nil
}

func (f *fakeCatalog) Tombstone(_ context.Context, uri string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[uri]
	if !ok || r.State != artifactcatalog.StateSoftDeleted {
		return artifactcatalog.ErrNotFound
	}
	r.State = artifactcatalog.StateTombstoned
	f.rows[uri] = r
	return nil
}

func (f *fakeCatalog) HardPruneExpired(_ context.Context, now time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for uri, r := range f.rows {
		// Mirror the PgStore predicate: every non-live row past its
		// deadline (soft_deleted from the retention sweep or tombstoned
		// from the erasure fast-path) is pruned, not just tombstoned
		// rows. F-12.5.25.
		if r.State != artifactcatalog.StateLive && !r.TombstoneDeadline.After(now) && !r.LegalHold {
			delete(f.rows, uri)
			count++
		}
	}
	return count, nil
}

func (f *fakeCatalog) ListBySession(_ context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []artifactcatalog.Record
	for _, r := range f.rows {
		if r.TenantID == tenantID && r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeCatalog) SetLegalHold(_ context.Context, uri string, hold bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	r.LegalHold = hold
	f.rows[uri] = r
	return nil
}

func (f *fakeCatalog) IsLegalHeldAt(_ context.Context, tenantID, sessionID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.TenantID == tenantID && r.SessionID == sessionID && r.LegalHold {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeCatalog) SessionsWithLegalHoldAndCheckpoints(_ context.Context) ([]artifactcatalog.SessionRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]struct{}{}
	var out []artifactcatalog.SessionRef
	for _, r := range f.rows {
		if !r.LegalHold || r.ArtifactType != artifactcatalog.ArtifactTypeCheckpoint {
			continue
		}
		key := r.TenantID + "|" + r.SessionID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, artifactcatalog.SessionRef{TenantID: r.TenantID, SessionID: r.SessionID})
	}
	return out, nil
}

// DeleteByTenant mirrors PgStore.DeleteByTenant: drop every non-held
// row for the tenant and return the count removed.
func (f *fakeCatalog) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for uri, r := range f.rows {
		if r.TenantID == tenantID && !r.LegalHold {
			delete(f.rows, uri)
			count++
		}
	}
	return count, nil
}

// spec: §12.5 ll. 309 — every artifact_store row is inserted alongside
// the bucket object. A successful Put through the decorator both
// stores the bytes and creates the matching catalog row.
func TestPutInsertsCatalogRow(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "part_a",
		TTL:        time.Hour,
	}
	ref, err := store.Put(u, "application/gzip", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref != u.String() {
		t.Errorf("Put ref = %q, want %q", ref, u.String())
	}

	row, err := cat.Get(context.Background(), u.String())
	if err != nil {
		t.Fatalf("catalog Get: %v", err)
	}
	if row.URI != u.String() ||
		row.TenantID != "acme" ||
		row.SessionID != "s_1" ||
		row.PartID != "part_a" {
		t.Errorf("catalog row mismatch: %+v", row)
	}
	if row.SizeBytes != int64(len("hello")) {
		t.Errorf("catalog SizeBytes = %d, want %d", row.SizeBytes, len("hello"))
	}
	if row.State != artifactcatalog.StateLive {
		t.Errorf("catalog State = %q, want %q", row.State, artifactcatalog.StateLive)
	}
	if row.ArtifactType != artifactcatalog.ArtifactTypeWorkspace {
		t.Errorf("catalog ArtifactType = %q, want %q", row.ArtifactType, artifactcatalog.ArtifactTypeWorkspace)
	}

	// And the bytes are readable through the decorator.
	info, body, err := store.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	gotBytes, _ := io.ReadAll(body)
	if string(gotBytes) != "hello" {
		t.Errorf("Get body = %q, want %q", string(gotBytes), "hello")
	}
	if info.Size != 5 {
		t.Errorf("Get info.Size = %d, want 5", info.Size)
	}
}

// spec: §12.5 — the catalog must reflect the ObjectType the URI
// embeds so the GC sweep selects rows by artifact class.
func TestPutMapsObjectTypeToArtifactType(t *testing.T) {
	cases := []struct {
		ot   blobstore.ObjectType
		want artifactcatalog.ArtifactType
	}{
		{blobstore.ObjectTypeWorkspace, artifactcatalog.ArtifactTypeWorkspace},
		{blobstore.ObjectTypeUpload, artifactcatalog.ArtifactTypeWorkspace},
		{blobstore.ObjectTypeTranscript, artifactcatalog.ArtifactTypeWorkspace},
		{blobstore.ObjectTypeCheckpoint, artifactcatalog.ArtifactTypeCheckpoint},
		{blobstore.ObjectTypeEviction, artifactcatalog.ArtifactTypeEvictionContext},
		{blobstore.ObjectTypeExport, artifactcatalog.ArtifactTypeExport},
		{blobstore.ObjectTypeSessionLog, artifactcatalog.ArtifactTypeSessionLog},
	}
	for _, tc := range cases {
		t.Run(string(tc.ot), func(t *testing.T) {
			inner := blobstore.NewMemoryStore(nil)
			cat := newFakeCatalog()
			store := New(inner, cat, Options{})
			u := blobstore.URI{
				TenantID:   "acme",
				ObjectType: tc.ot,
				SessionID:  "s_" + string(tc.ot),
				PartID:     "p",
				TTL:        time.Hour,
			}
			if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
				t.Fatalf("Put: %v", err)
			}
			row, err := cat.Get(context.Background(), u.String())
			if err != nil {
				t.Fatalf("catalog Get: %v", err)
			}
			if row.ArtifactType != tc.want {
				t.Errorf("ArtifactType for ObjectType %q = %q, want %q",
					tc.ot, row.ArtifactType, tc.want)
			}
		})
	}
}

// spec: §12.5 — a catalog insert failure surfaces as a wrapped error
// to the caller. Without this contract the catalog would silently
// drift from the bucket, breaking the size-accounting and GC sweep
// invariants.
func TestPutCatalogInsertFailureSurfaced(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	cat.insertErr = errors.New("synthetic catalog failure")

	var logged []string
	store := New(inner, cat, Options{
		LogOnCatalogFailure: func(uri string, _ error) { logged = append(logged, uri) },
	})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "p",
		TTL:        time.Hour,
	}
	_, err := store.Put(u, "application/gzip", strings.NewReader("body"))
	if err == nil {
		t.Fatal("Put: want error from catalog insert failure")
	}
	if !strings.Contains(err.Error(), "synthetic catalog failure") {
		t.Errorf("Put err = %v, want wrap of catalog error", err)
	}
	if len(logged) != 1 || logged[0] != u.String() {
		t.Errorf("LogOnCatalogFailure not invoked exactly once with the failing URI: %v", logged)
	}
	// And the bucket-side object exists despite the catalog failure
	// (consistent with the documented "best-effort rollback through
	// the next GC sweep" contract).
	if _, _, err := inner.Get(u); err != nil {
		t.Errorf("inner Get post-failure: %v, want bucket object present", err)
	}
}

// spec: §12.5 ll. 311-313 — soft-delete transitions the catalog row
// to `soft_deleted` and stamps the tombstone deadline. The bucket-
// side SoftDelete (when the inner store implements Tombstoner) runs
// alongside.
func TestSoftDeleteTransitionsCatalogAndBucket(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	clock := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := New(inner, cat, Options{Now: func() time.Time { return clock }})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	retention := 24 * time.Hour
	if err := store.SoftDelete(u, retention); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	// The catalog row is now soft_deleted with deadline at clock+retention.
	row, err := cat.Get(context.Background(), u.String())
	if err != nil {
		t.Fatalf("catalog Get: %v", err)
	}
	if row.State != artifactcatalog.StateSoftDeleted {
		t.Errorf("State = %q, want %q", row.State, artifactcatalog.StateSoftDeleted)
	}
	if !row.TombstoneDeadline.Equal(clock.Add(retention)) {
		t.Errorf("TombstoneDeadline = %v, want %v",
			row.TombstoneDeadline, clock.Add(retention))
	}
	// And the bucket-side Get reads ErrNotFound (the MemoryStore
	// Tombstoner implementation).
	if _, _, gerr := inner.Get(u); !errors.Is(gerr, blobstore.ErrNotFound) {
		t.Errorf("inner Get post-SoftDelete: %v, want ErrNotFound", gerr)
	}
}

// spec: §12.5 — SoftDelete on a row that predates the catalog is a
// no-op so the wiring can be deployed incrementally without a forced
// backfill.
func TestSoftDeleteIdempotentOnMissingRow(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "p",
		TTL:        time.Hour,
	}
	// No Put — the row is absent. SoftDelete must not error.
	if err := store.SoftDelete(u, time.Hour); err != nil {
		t.Errorf("SoftDelete on missing row: %v, want nil", err)
	}
}

// spec: §12.5 ll. 341 — HardPrune removes catalog rows whose
// tombstone deadline has elapsed and increments the
// `lenny_gc_tombstones_pruned_total` counter (the count is the value
// returned here).
func TestHardPruneRemovesExpiredRows(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	clock := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := New(inner, cat, Options{Now: func() time.Time { return clock }})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Soft-delete with a short retention; the catalog deadline lands
	// before our prune-time clock so the row is hard-pruned.
	if err := store.SoftDelete(u, time.Minute); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	pruneTime := clock.Add(2 * time.Minute)
	count, err := store.HardPrune(context.Background(), pruneTime)
	if err != nil {
		t.Fatalf("HardPrune: %v", err)
	}
	if count != 1 {
		t.Errorf("HardPrune count = %d, want 1", count)
	}
	if _, err := cat.Get(context.Background(), u.String()); !errors.Is(err, artifactcatalog.ErrNotFound) {
		t.Errorf("catalog Get post-HardPrune: %v, want ErrNotFound", err)
	}
}

// spec: §4.5 ll. 311 — Copy through the decorator records the
// derived session's artifact_store row so the §12.5 GC sweep sees
// both parent and child rows independently. A foreign-tenant Copy
// surfaces the inner store's ErrCrossTenant and never inserts.
func TestCopyInsertsDerivedRowAndRejectsCrossTenant(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "parent",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := store.Put(src, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put src: %v", err)
	}

	// Cross-tenant Copy must fail.
	foreign := src
	foreign.TenantID = "globex"
	foreign.SessionID = "derived"
	if err := store.Copy(src, foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Copy cross-tenant: %v, want ErrCrossTenant", err)
	}
	if _, err := cat.Get(context.Background(), foreign.String()); !errors.Is(err, artifactcatalog.ErrNotFound) {
		t.Error("cross-tenant Copy created a catalog row")
	}

	// Same-tenant Copy succeeds and produces a derived catalog row.
	dst := src
	dst.SessionID = "derived"
	if err := store.Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	row, err := cat.Get(context.Background(), dst.String())
	if err != nil {
		t.Fatalf("catalog Get derived: %v", err)
	}
	if row.SessionID != "derived" {
		t.Errorf("derived row SessionID = %q, want derived", row.SessionID)
	}
	if row.State != artifactcatalog.StateLive {
		t.Errorf("derived row State = %q, want live", row.State)
	}
}

// spec: §12.5 — DeleteBySession (the §12.8 erasure path) only the
// inner store hosts; verify the decorator forwards through when the
// inner store implements the optional interface and that the catalog
// reconciles its rows alongside.
func TestDeleteBySessionForwarded(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if store.Inner() != inner {
		t.Error("Store.Inner() did not return the wrapped store")
	}
	// DeleteBySession on the decorator forwards to the inner store
	// and reconciles the catalog row by transitioning it through
	// soft_deleted → tombstoned (the HardPruneExpired pass below
	// removes it).
	deleted, err := store.DeleteBySession(context.Background(), "acme", "s_1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteBySession count = %d, want 1", deleted)
	}
	// Catalog row is now Tombstoned.
	row, err := cat.Get(context.Background(), u.String())
	if err != nil {
		t.Fatalf("catalog Get post-DeleteBySession: %v", err)
	}
	if row.State != artifactcatalog.StateTombstoned {
		t.Errorf("catalog State = %q, want %q", row.State, artifactcatalog.StateTombstoned)
	}
}

// spec: §12.5 ll. 311-313 / §7.1 — SoftDeleteSession transitions
// every live catalog row under (tenant, session) to soft_deleted with
// the §12.5 tombstone-retention deadline. Returns the count of rows
// transitioned so the retention GC's per-session count keeps mapping
// to "objects collected".
func TestSoftDeleteSessionTransitionsEveryLiveRow(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	clock := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := New(inner, cat, Options{Now: func() time.Time { return clock }})

	// Three artifacts under the same session.
	for _, partID := range []string{"a", "b", "c"} {
		u := blobstore.URI{
			TenantID:   "acme",
			ObjectType: blobstore.ObjectTypeWorkspace,
			SessionID:  "s_1",
			PartID:     partID,
			TTL:        time.Hour,
		}
		if _, err := store.Put(u, "application/gzip", strings.NewReader("body")); err != nil {
			t.Fatalf("Put %s: %v", partID, err)
		}
	}
	// And one artifact under a different session — must not transition.
	other := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_2",
		PartID:     "x",
		TTL:        time.Hour,
	}
	if _, err := store.Put(other, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put other: %v", err)
	}

	retention := 24 * time.Hour
	count, err := store.SoftDeleteSession(context.Background(), "acme", "s_1", retention)
	if err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}
	if count != 3 {
		t.Errorf("SoftDeleteSession count = %d, want 3", count)
	}
	// All three s_1 rows are soft_deleted with deadline = clock + retention.
	rows, _ := cat.ListBySession(context.Background(), "acme", "s_1")
	for _, r := range rows {
		if r.State != artifactcatalog.StateSoftDeleted {
			t.Errorf("s_1 row %s State = %q, want soft_deleted", r.URI, r.State)
		}
		if !r.TombstoneDeadline.Equal(clock.Add(retention)) {
			t.Errorf("s_1 row %s TombstoneDeadline = %v, want %v",
				r.URI, r.TombstoneDeadline, clock.Add(retention))
		}
	}
	// The other session's row stays live.
	otherRow, err := cat.Get(context.Background(), other.String())
	if err != nil {
		t.Fatalf("catalog Get other: %v", err)
	}
	if otherRow.State != artifactcatalog.StateLive {
		t.Errorf("s_2 row State = %q, want live (untouched by s_1 sweep)", otherRow.State)
	}
}

// erroringTenantDeleter wraps a MemoryStore but fails DeleteByTenant,
// modeling the §12.8 legal-hold abort the production bucket store
// raises before any catalog row is reconciled.
type erroringTenantDeleter struct {
	*blobstore.MemoryStore
	err error
}

func (e erroringTenantDeleter) DeleteByTenant(context.Context, string) (int, error) {
	return 0, e.err
}

// TestDeleteByTenantForwarded asserts the §12.8 Phase 4 prefix-scoped
// bulk delete forwards to the inner store and drops the tenant's
// catalog rows while a different tenant's bucket object and catalog
// row both survive.
//
// spec: §12.5 ll. 295; §12.8 Phase 4.
func TestDeleteByTenantForwarded(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	acme1 := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s1", PartID: "a", TTL: time.Hour}
	acme2 := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: "s2", PartID: "b", TTL: time.Hour}
	globex := blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s1", PartID: "a", TTL: time.Hour}
	for _, u := range []blobstore.URI{acme1, acme2, globex} {
		if _, err := store.Put(u, "application/gzip", strings.NewReader("b")); err != nil {
			t.Fatalf("Put %s: %v", u.TenantID, err)
		}
	}

	deleted, err := store.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if _, err := inner.Stat(acme1); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("acme1 bucket object survived: %v", err)
	}
	if _, err := inner.Stat(globex); err != nil {
		t.Errorf("globex bucket object erased (cross-tenant leak): %v", err)
	}
	if _, err := cat.Get(context.Background(), acme1.String()); !errors.Is(err, artifactcatalog.ErrNotFound) {
		t.Errorf("acme1 catalog row survived: %v", err)
	}
	if _, err := cat.Get(context.Background(), globex.String()); err != nil {
		t.Errorf("globex catalog row erased (cross-tenant leak): %v", err)
	}
}

// TestDeleteByTenantPreservesLegalHeldCatalogRow asserts the catalog's
// `legal_hold = false` guard keeps a §12.8-held row out of the Phase 4
// bulk delete; the unheld row in the same tenant is removed.
//
// spec: §12.5 ll. 295; §12.8 line 735.
func TestDeleteByTenantPreservesLegalHeldCatalogRow(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	held := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: "s_held", PartID: "h", TTL: time.Hour}
	free := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s_free", PartID: "f", TTL: time.Hour}
	for _, u := range []blobstore.URI{held, free} {
		if _, err := store.Put(u, "application/gzip", strings.NewReader("b")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := cat.SetLegalHold(context.Background(), held.String(), true); err != nil {
		t.Fatalf("SetLegalHold: %v", err)
	}

	if _, err := store.DeleteByTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, err := cat.Get(context.Background(), held.String()); err != nil {
		t.Errorf("legal-held catalog row removed by tenant delete: %v", err)
	}
	if _, err := cat.Get(context.Background(), free.String()); !errors.Is(err, artifactcatalog.ErrNotFound) {
		t.Errorf("unheld catalog row survived tenant delete: %v", err)
	}
}

// TestDeleteByTenantInnerAbortLeavesCatalog asserts that when the
// bucket store aborts the bulk delete (the §12.8 legal-hold abort),
// the error propagates and no catalog row is reconciled — a held
// tenant survives intact.
//
// spec: §12.5 ll. 295; §12.8 line 735.
func TestDeleteByTenantInnerAbortLeavesCatalog(t *testing.T) {
	mem := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	holdErr := errors.New("under §12.8 legal hold")
	store := New(erroringTenantDeleter{MemoryStore: mem, err: holdErr}, cat, Options{})

	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s1", PartID: "p", TTL: time.Hour}
	if _, err := store.Put(u, "application/gzip", strings.NewReader("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := store.DeleteByTenant(context.Background(), "acme"); !errors.Is(err, holdErr) {
		t.Fatalf("DeleteByTenant: got %v, want the inner abort error", err)
	}
	if _, err := cat.Get(context.Background(), u.String()); err != nil {
		t.Errorf("catalog row removed despite inner abort: %v", err)
	}
}
