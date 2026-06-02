// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// spec: §4.5 blob URI scheme + immutability.

func TestParseURIHappyPath(t *testing.T) {
	got, err := blobstore.ParseURI("lenny-blob://acme/sess_1/part_xyz?ttl=3600&enc=aes256gcm")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if got.TenantID != "acme" || got.SessionID != "sess_1" || got.PartID != "part_xyz" {
		t.Errorf("URI: got %+v", got)
	}
	if got.TTL != time.Hour {
		t.Errorf("TTL: got %v, want 1h", got.TTL)
	}
	if got.Encoding != blobstore.Encoding {
		t.Errorf("Encoding: got %q, want %q", got.Encoding, blobstore.Encoding)
	}
}

func TestParseURIDefaultsEncoding(t *testing.T) {
	got, err := blobstore.ParseURI("lenny-blob://acme/sess_1/part_xyz?ttl=60")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if got.Encoding != blobstore.Encoding {
		t.Errorf("Encoding default: got %q, want %q", got.Encoding, blobstore.Encoding)
	}
}

func TestParseURIRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"http://acme/sess/part?ttl=1",
		"lenny-blob://acme/sess?ttl=1",  // missing part
		"lenny-blob:///sess/part?ttl=1", // empty tenant
		"lenny-blob://acme/sess/part",   // missing ttl
		"lenny-blob://acme/sess/part?ttl=0",
		"lenny-blob://acme/sess/part?ttl=-5",
		"lenny-blob://acme/sess/part?ttl=abc",
	}
	for _, raw := range cases {
		_, err := blobstore.ParseURI(raw)
		if !errors.Is(err, blobstore.ErrInvalidURI) {
			t.Errorf("ParseURI(%q): got %v, want ErrInvalidURI", raw, err)
		}
	}
}

// TestURIRoundTrip_LegacyForm asserts the §12.5 ll. 295 4-segment
// serialiser accepts the pre-{object_type} 3-segment form and
// re-emits it in the canonical 4-segment shape (stamping the
// default ObjectTypeUpload on the parsed URI).
//
// spec: §12.5 ll. 295.
func TestURIRoundTrip_LegacyForm(t *testing.T) {
	in := "lenny-blob://acme/sess_1/part_xyz?ttl=300&enc=aes256gcm"
	u, err := blobstore.ParseURI(in)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if u.ObjectType != blobstore.ObjectTypeUpload {
		t.Errorf("ObjectType: got %q, want %q (legacy default)",
			u.ObjectType, blobstore.ObjectTypeUpload)
	}
	canonical := "lenny-blob://acme/upload/sess_1/part_xyz?ttl=300&enc=aes256gcm"
	if u.String() != canonical {
		t.Errorf("round-trip: got %q, want %q", u.String(), canonical)
	}
}

// TestURIRoundTrip_CanonicalForm asserts the §12.5 ll. 295 4-segment
// canonical form round-trips identically.
//
// spec: §12.5 ll. 295.
func TestURIRoundTrip_CanonicalForm(t *testing.T) {
	original := "lenny-blob://acme/checkpoint/sess_1/part_xyz?ttl=300&enc=aes256gcm"
	u, err := blobstore.ParseURI(original)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if u.ObjectType != blobstore.ObjectTypeCheckpoint {
		t.Errorf("ObjectType: got %q, want checkpoint", u.ObjectType)
	}
	if u.String() != original {
		t.Errorf("round-trip: got %q, want %q", u.String(), original)
	}
}

func TestMemoryStorePutGet(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, body, err := s.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.MimeType != "text/plain" {
		t.Errorf("mimeType: got %q", info.MimeType)
	}
	if info.Size != 5 {
		t.Errorf("size: got %d, want 5", info.Size)
	}
	bs, _ := io.ReadAll(body)
	if string(bs) != "hello" {
		t.Errorf("body: got %q", string(bs))
	}
}

func TestMemoryStorePutRejectsOverwrite(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("first")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	_, err := s.Put(u, "text/plain", strings.NewReader("second"))
	if !errors.Is(err, blobstore.ErrConflict) {
		t.Errorf("Put 2: got %v, want ErrConflict", err)
	}
}

func TestMemoryStoreGetReturnsNotFoundForUnknown(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_x", TTL: time.Hour}
	if _, _, err := s.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get unknown: got %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Stat unknown: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreGetReturnsNotFoundAfterTTL(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var now time.Time
	clock := func() time.Time { return now }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: 60 * time.Second}
	now = t0
	if _, err := s.Put(u, "text/plain", strings.NewReader("hi")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	now = t0.Add(time.Hour) // well past TTL
	if _, _, err := s.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get post-TTL: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreSweepDropsExpired(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	uA := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "a", TTL: time.Minute}
	uB := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "b", TTL: time.Hour}
	_, _ = s.Put(uA, "text/plain", strings.NewReader("a"))
	_, _ = s.Put(uB, "text/plain", strings.NewReader("b"))

	dropped := s.Sweep(clock().Add(30 * time.Minute))
	if dropped != 1 {
		t.Errorf("Sweep dropped: got %d, want 1", dropped)
	}
	if _, _, err := s.Get(uA); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("uA should be swept: got %v", err)
	}
	if _, _, err := s.Get(uB); err != nil {
		t.Errorf("uB should be retained: got %v", err)
	}
}

// spec: 12.5
// diagnosis: SoftDelete did not honor the §12.5 tombstone contract.
// After SoftDelete, Get and Stat must return ErrNotFound; the body
// must be cleared so the stored bytes are gone from memory at
// soft-delete time (not deferred to HardPrune). SoftDelete must be
// idempotent against absent and already-tombstoned blobs.
func TestMemoryStoreSoftDeleteContract(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "a", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := s.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if !s.Tombstoned(u) {
		t.Error("Tombstoned: should be true after SoftDelete")
	}
	if _, _, err := s.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get post-SoftDelete: got %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Stat post-SoftDelete: got %v, want ErrNotFound", err)
	}

	// SoftDelete is idempotent on the same row.
	if err := s.SoftDelete(u); err != nil {
		t.Errorf("SoftDelete twice: got %v, want nil", err)
	}

	// SoftDelete on a missing row is a no-op.
	missing := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "missing", TTL: time.Hour}
	if err := s.SoftDelete(missing); err != nil {
		t.Errorf("SoftDelete missing: got %v, want nil", err)
	}
}

// spec: 12.5
// diagnosis: HardPrune did not respect the §12.5 tombstone-retention
// window. The sweep must physically remove tombstones whose
// deleted_at is older than `now - retention` and leave live blobs +
// fresh tombstones alone.
func TestMemoryStoreHardPruneRespectsRetention(t *testing.T) {
	var now time.Time
	clock := func() time.Time { return now }
	s := blobstore.NewMemoryStore(clock)
	uOld := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "old", TTL: time.Hour}
	uFresh := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "fresh", TTL: time.Hour}
	uLive := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "live", TTL: time.Hour}

	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Put(uOld, "text/plain", strings.NewReader("o")); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	if err := s.SoftDelete(uOld); err != nil {
		t.Fatalf("SoftDelete old: %v", err)
	}

	now = now.Add(25 * time.Hour)
	if _, err := s.Put(uFresh, "text/plain", strings.NewReader("f")); err != nil {
		t.Fatalf("Put fresh: %v", err)
	}
	if err := s.SoftDelete(uFresh); err != nil {
		t.Fatalf("SoftDelete fresh: %v", err)
	}
	if _, err := s.Put(uLive, "text/plain", strings.NewReader("l")); err != nil {
		t.Fatalf("Put live: %v", err)
	}

	// HardPrune with a 24h retention window: uOld is older than the
	// retention threshold; uFresh and uLive are not.
	removed := s.HardPrune(now, 24*time.Hour)
	if removed != 1 {
		t.Errorf("HardPrune removed: got %d, want 1 (uOld)", removed)
	}
	if s.Tombstoned(uFresh) != true {
		t.Errorf("uFresh should still be tombstoned post-HardPrune")
	}
	if _, _, err := s.Get(uLive); err != nil {
		t.Errorf("uLive should still be readable: %v", err)
	}

	// A second HardPrune at the same time is a no-op.
	if r := s.HardPrune(now, 24*time.Hour); r != 0 {
		t.Errorf("HardPrune second pass: got %d, want 0", r)
	}
}

// spec: 12.5
// diagnosis: HardPrune mistakenly removed live (non-tombstoned)
// blobs. Live blobs must survive every HardPrune pass regardless of
// the retention window.
func TestMemoryStoreHardPrunePreservesLiveBlobs(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "live", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("l")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if r := s.HardPrune(clock().Add(48*time.Hour), time.Hour); r != 0 {
		t.Errorf("HardPrune removed a live blob: %d", r)
	}
	if _, _, err := s.Get(u); err != nil {
		t.Errorf("live blob lost: %v", err)
	}
}

// spec: §12.5 line 320 — HardDeleteObject physically removes exactly
// the named object and is idempotent on an absent object. F-12.5.23.
func TestMemoryStoreHardDeleteObject(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	keep := blobstore.URI{TenantID: "acme", SessionID: "keep", PartID: "p", TTL: time.Hour}
	drop := blobstore.URI{TenantID: "acme", SessionID: "drop", PartID: "p", TTL: time.Hour}
	for _, u := range []blobstore.URI{keep, drop} {
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s: %v", u.SessionID, err)
		}
	}
	if err := s.HardDeleteObject(drop); err != nil {
		t.Fatalf("HardDeleteObject: %v", err)
	}
	if _, _, err := s.Get(drop); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("dropped object Get: %v, want ErrNotFound", err)
	}
	if _, _, err := s.Get(keep); err != nil {
		t.Errorf("kept object lost: %v", err)
	}
	// Idempotent: deleting an absent object is a no-op.
	if err := s.HardDeleteObject(drop); err != nil {
		t.Errorf("HardDeleteObject on absent object: %v, want nil", err)
	}
}

// spec: §12.5 ll. 333-339 — StatIncludingTombstones distinguishes a
// soft-deleted (still discoverable) blob from one that is absent.
// Stat collapses both into ErrNotFound; this surface lets the GC
// sweep and observability paths tell the two terminal states apart.
func TestMemoryStoreStatIncludingTombstonesActive(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, state, err := s.StatIncludingTombstones(u)
	if err != nil {
		t.Fatalf("StatIncludingTombstones: %v", err)
	}
	if state != blobstore.BlobStateActive {
		t.Errorf("state = %q, want active", state)
	}
	if info.Size != int64(len("payload")) {
		t.Errorf("size = %d, want 7", info.Size)
	}
}

// spec: §12.5 ll. 333-339 — a soft-deleted blob surfaces as
// SoftDeleted with metadata intact, distinguishing it from a
// physically absent blob.
func TestMemoryStoreStatIncludingTombstonesSoftDeleted(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	_, state, err := s.StatIncludingTombstones(u)
	if err != nil {
		t.Fatalf("StatIncludingTombstones: %v", err)
	}
	if state != blobstore.BlobStateSoftDeleted {
		t.Errorf("state = %q, want soft_deleted", state)
	}

	// Stat still returns ErrNotFound — the soft-delete is opaque to
	// non-GC callers.
	if _, err := s.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Stat post-SoftDelete: got %v, want ErrNotFound", err)
	}
}

// spec: §12.5 ll. 333-339 — a blob that never existed surfaces as
// NotFound, paired with ErrNotFound.
func TestMemoryStoreStatIncludingTombstonesAbsent(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "missing", TTL: time.Hour}
	_, state, err := s.StatIncludingTombstones(u)
	if !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if state != blobstore.BlobStateNotFound {
		t.Errorf("state = %q, want not_found", state)
	}
}

// spec: §12.5 ll. 333-339 — an expired blob (past TTL but never
// soft-deleted) surfaces as NotFound, matching the §4.5 TTL contract.
func TestMemoryStoreStatIncludingTombstonesExpired(t *testing.T) {
	var now time.Time
	clock := func() time.Time { return now }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Put(u, "text/plain", strings.NewReader("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	now = now.Add(2 * time.Hour) // past TTL
	_, state, err := s.StatIncludingTombstones(u)
	if !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for an expired blob", err)
	}
	if state != blobstore.BlobStateNotFound {
		t.Errorf("state = %q, want not_found for an expired blob", state)
	}
}

func TestNewPartIDFormat(t *testing.T) {
	got := blobstore.NewPartID()
	if !strings.HasPrefix(got, "part_") || len(got) != 5+16 {
		t.Errorf("NewPartID: got %q (length %d)", got, len(got))
	}
}

func TestMemoryStoreCrossTenantSegregation(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	uA := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "x", TTL: time.Hour}
	uB := blobstore.URI{TenantID: "globex", SessionID: "sess_1", PartID: "x", TTL: time.Hour}
	if _, err := s.Put(uA, "text/plain", strings.NewReader("acme-data")); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if _, err := s.Put(uB, "text/plain", strings.NewReader("globex-data")); err != nil {
		t.Fatalf("Put B (diff tenant): %v", err)
	}
	_, body, err := s.Get(uA)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	defer body.Close()
	bs, _ := io.ReadAll(body)
	if string(bs) != "acme-data" {
		t.Errorf("acme returned %q (cross-tenant data leak?)", string(bs))
	}
}

// spec: §12.8 GDPR erasure — the blob store's per-session adapter.

func TestMemoryStoreDeleteBySessionRemovesSessionBlobs(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	keep := blobstore.URI{TenantID: "acme", SessionID: "sess_keep", PartID: "k", TTL: time.Hour}
	erase1 := blobstore.URI{TenantID: "acme", SessionID: "sess_erase", PartID: "a", TTL: time.Hour}
	erase2 := blobstore.URI{TenantID: "acme", SessionID: "sess_erase", PartID: "b", TTL: time.Hour}
	for _, u := range []blobstore.URI{keep, erase1, erase2} {
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s: %v", u.PartID, err)
		}
	}
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_erase")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if _, _, err := s.Get(erase1); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("erased blob still present: got %v", err)
	}
	if _, err := s.Stat(keep); err != nil {
		t.Errorf("blob from another session should be retained: got %v", err)
	}
}

func TestMemoryStoreDeleteBySessionScopedByTenant(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	acme := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	globex := blobstore.URI{TenantID: "globex", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	if _, err := s.Put(acme, "text/plain", strings.NewReader("acme")); err != nil {
		t.Fatalf("Put acme: %v", err)
	}
	if _, err := s.Put(globex, "text/plain", strings.NewReader("globex")); err != nil {
		t.Fatalf("Put globex: %v", err)
	}
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (same session id in another tenant must survive)", deleted)
	}
	if _, err := s.Stat(globex); err != nil {
		t.Errorf("globex blob erased by acme erasure (cross-tenant leak): %v", err)
	}
}

func TestMemoryStoreDeleteBySessionUnknownSessionIsNoOp(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_absent")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for a session with no blobs", deleted)
	}
}

// TestObjectTypeEnumIsClosed asserts every spec-defined §12.5 line
// 295 object_type segment is recognised by the parser, and a
// typo is rejected.
//
// spec: §12.5 ll. 295, 315.
func TestObjectTypeEnumIsClosed(t *testing.T) {
	cases := []blobstore.ObjectType{
		blobstore.ObjectTypeWorkspace,
		blobstore.ObjectTypeCheckpoint,
		blobstore.ObjectTypeTranscript,
		blobstore.ObjectTypeUpload,
		blobstore.ObjectTypeEviction,
		blobstore.ObjectTypeExport,
		blobstore.ObjectTypeSessionLog,
	}
	for _, ot := range cases {
		if !blobstore.IsValidObjectType(ot) {
			t.Errorf("IsValidObjectType(%q) = false; spec §12.5 ll. 295 names this segment", ot)
		}
	}
	if blobstore.IsValidObjectType("not_a_type") {
		t.Error("IsValidObjectType: unknown segment must be rejected")
	}
}

// TestParseURIRejectsUnknownObjectType asserts the parser refuses
// a URI whose object_type segment is not in the spec's closed
// enum.
//
// spec: §12.5 ll. 295.
func TestParseURIRejectsUnknownObjectType(t *testing.T) {
	_, err := blobstore.ParseURI("lenny-blob://acme/totally_not_an_object_type/sess/p?ttl=60")
	if !errors.Is(err, blobstore.ErrInvalidURI) {
		t.Errorf("ParseURI: got %v, want ErrInvalidURI for an unknown object_type", err)
	}
}

// TestMemoryStoreCopyHappyPath asserts Copier on the in-memory
// store: a Copy duplicates the source bytes under the destination
// URI, and a delete of the source does not affect the dest.
//
// spec: §4.5 ll. 311 — derive copies parent bytes so GC of the
// parent has no effect on the derived session's workspace.
func TestMemoryStoreCopyHappyPath(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "parent",
		PartID:     "snap",
		TTL:        time.Hour,
	}
	if _, err := s.Put(src, "application/x-tar", strings.NewReader("workspace-bytes")); err != nil {
		t.Fatalf("Put src: %v", err)
	}
	dst := src
	dst.SessionID = "derived"
	if err := s.Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if _, _, err := s.Get(dst); err != nil {
		t.Fatalf("Get dst: %v", err)
	}
	// Erase the parent; derived must survive.
	if _, err := s.DeleteBySession(context.Background(), "acme", "parent"); err != nil {
		t.Fatalf("DeleteBySession parent: %v", err)
	}
	if _, _, err := s.Get(dst); err != nil {
		t.Errorf("Get dst after parent erase: %v (spec §4.5 ll. 311 says derived bytes must survive)", err)
	}
}

// TestMemoryStoreCopyRejectsCrossTenant asserts the §4.5 ll. 309
// tenant-isolation guard fires on Copy.
func TestMemoryStoreCopyRejectsCrossTenant(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "p",
		PartID:     "x",
		TTL:        time.Hour,
	}
	dst := src
	dst.TenantID = "globex"
	if _, err := s.Put(src, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put src: %v", err)
	}
	if err := s.Copy(src, dst); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Copy across tenants: got %v, want ErrCrossTenant", err)
	}
}

// TestMemoryStoreCopySrcAbsentIsErrNotFound asserts the Copy
// surface for an absent source.
func TestMemoryStoreCopySrcAbsentIsErrNotFound(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "absent",
		PartID:     "x",
		TTL:        time.Hour,
	}
	dst := src
	dst.SessionID = "derived"
	if err := s.Copy(src, dst); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Copy absent src: got %v, want ErrNotFound", err)
	}
}

// TestMemoryStoreCopyConflictsOnLiveDst asserts Copy refuses to
// overwrite a live destination (§4.5 write-once).
func TestMemoryStoreCopyConflictsOnLiveDst(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "p",
		PartID:     "x",
		TTL:        time.Hour,
	}
	dst := src
	dst.SessionID = "derived"
	for _, u := range []blobstore.URI{src, dst} {
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := s.Copy(src, dst); !errors.Is(err, blobstore.ErrConflict) {
		t.Errorf("Copy onto a live dst: got %v, want ErrConflict", err)
	}
}

// TestTenantScopedRejectsCrossTenant asserts the §4.5 ll. 309
// interface-level tenant-prefix validation: a URI whose tenant
// does not match the decorator's caller tenant is rejected with
// ErrCrossTenant from every read/write entry point, without ever
// reaching the underlying store.
//
// spec: §4.5 ll. 309; §12.5 ll. 295.
func TestTenantScopedRejectsCrossTenant(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	scoped := blobstore.NewTenantScoped("acme", inner)
	foreign := blobstore.URI{
		TenantID:   "globex",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "s",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := scoped.Put(foreign, "text/plain", strings.NewReader("x")); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Put cross-tenant: got %v, want ErrCrossTenant", err)
	}
	if _, _, err := scoped.Get(foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Get cross-tenant: got %v, want ErrCrossTenant", err)
	}
	if _, err := scoped.Stat(foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Stat cross-tenant: got %v, want ErrCrossTenant", err)
	}
	// Confirm the underlying store stayed clean — the wrapper
	// rejects before reaching it.
	if _, err := inner.Stat(foreign); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("inner Stat foreign: got %v, want ErrNotFound (wrapper must reject before delegate)", err)
	}
}

// TestTenantScopedAdmitsMatchingTenant confirms a same-tenant URI
// passes through and the underlying Store is invoked normally.
//
// spec: §4.5 ll. 309.
func TestTenantScopedAdmitsMatchingTenant(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	scoped := blobstore.NewTenantScoped("acme", inner)
	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "s",
		PartID:     "p",
		TTL:        time.Hour,
	}
	if _, err := scoped.Put(u, "text/plain", strings.NewReader("ok")); err != nil {
		t.Fatalf("Put same-tenant: %v", err)
	}
	if _, _, err := scoped.Get(u); err != nil {
		t.Fatalf("Get same-tenant: %v", err)
	}
}

// TestTenantScopedCopyRejectsCrossTenantBetweenURIs asserts that
// even when src and dst pass the wrapper's caller-tenant gate
// individually, the Copy is rejected when src.TenantID and
// dst.TenantID differ — the §4.5 ll. 311 derive-copy invariant
// requires same-tenant byte ownership.
//
// spec: §4.5 ll. 309, 311.
func TestTenantScopedCopyRejectsCrossTenantBetweenURIs(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	// Empty caller-tenant disables the per-call gate so the
	// underlying same-tenant check on Copy can run.
	scoped := blobstore.NewTenantScoped("", inner)
	src := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "p",
		PartID:     "x",
		TTL:        time.Hour,
	}
	dst := src
	dst.TenantID = "globex"
	if _, err := scoped.Put(src, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := scoped.Copy(src, dst); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Copy cross-tenant src/dst: got %v, want ErrCrossTenant", err)
	}
}

// TestMemoryStoreDeleteByTenantRemovesOnlyNamedTenant asserts the
// §12.5 ll. 295 prefix-scoped bulk delete erases every object under
// one tenant prefix (across sessions and object types) while a
// different tenant's blobs survive.
//
// spec: §12.5 ll. 295.
func TestMemoryStoreDeleteByTenantRemovesOnlyNamedTenant(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	acme1 := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s1", PartID: "a", TTL: time.Hour}
	acme2 := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: "s2", PartID: "b", TTL: time.Hour}
	globex := blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s1", PartID: "a", TTL: time.Hour}
	for _, u := range []blobstore.URI{acme1, acme2, globex} {
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s/%s: %v", u.TenantID, u.PartID, err)
		}
	}
	deleted, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (both acme blobs across sessions and object types)", deleted)
	}
	if _, err := s.Stat(acme1); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("acme1 survived tenant delete: got %v", err)
	}
	if _, err := s.Stat(acme2); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("acme2 survived tenant delete: got %v", err)
	}
	if _, err := s.Stat(globex); err != nil {
		t.Errorf("globex blob erased by acme tenant delete (cross-tenant leak): %v", err)
	}
}

// TestMemoryStoreDeleteByTenantEmptyTenantIsNoOp asserts an empty
// tenantID matches nothing so a mis-scoped call cannot wipe the store.
//
// spec: §12.5 ll. 295.
func TestMemoryStoreDeleteByTenantEmptyTenantIsNoOp(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	deleted, err := s.DeleteByTenant(context.Background(), "")
	if err != nil {
		t.Fatalf("DeleteByTenant(\"\"): %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for empty tenant", deleted)
	}
	if _, err := s.Stat(u); err != nil {
		t.Errorf("blob erased by empty-tenant delete: %v", err)
	}
}

// TestTenantScopedDeleteByTenantRestrictsToBoundTenant asserts a
// scoped store bound to tenant T only permits DeleteByTenant(T); a
// request for another tenant returns ErrCrossTenant before any object
// is touched, and an unscoped (empty) wrapper forwards any tenant.
//
// spec: §12.5 ll. 295.
func TestTenantScopedDeleteByTenantRestrictsToBoundTenant(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	acme := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	globex := blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	for _, u := range []blobstore.URI{acme, globex} {
		if _, err := inner.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("seed %s: %v", u.TenantID, err)
		}
	}
	scoped := blobstore.NewTenantScoped("acme", inner)
	if _, err := scoped.DeleteByTenant(context.Background(), "globex"); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("DeleteByTenant(globex) on acme-scoped store: got %v, want ErrCrossTenant", err)
	}
	if _, err := inner.Stat(globex); err != nil {
		t.Errorf("globex blob removed despite cross-tenant rejection: %v", err)
	}
	deleted, err := scoped.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant(acme) on acme-scoped store: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	// Unscoped wrapper forwards any tenant (the §12.8 Phase 4 path).
	unscoped := blobstore.NewTenantScoped("", inner)
	if _, err := unscoped.DeleteByTenant(context.Background(), "globex"); err != nil {
		t.Fatalf("unscoped DeleteByTenant(globex): %v", err)
	}
	if _, err := inner.Stat(globex); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("globex blob survived unscoped tenant delete: got %v", err)
	}
}
