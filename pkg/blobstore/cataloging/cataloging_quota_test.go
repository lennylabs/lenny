// SPDX-License-Identifier: MIT

package cataloging

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// fakeQuota is a QuotaReleaser that records the net delta per tenant so
// a test can assert the §11 line 37 storage-quota decrement fired with
// the deleted artifacts' byte total.
type fakeQuota struct {
	mu    sync.Mutex
	delta map[string]int64
	err   error
}

func newFakeQuota() *fakeQuota { return &fakeQuota{delta: map[string]int64{}} }

func (q *fakeQuota) Adjust(_ context.Context, tenantID string, delta int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.delta[tenantID] += delta
	return nil
}

func (q *fakeQuota) get(tenantID string) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.delta[tenantID]
}

func putSized(t *testing.T, store *Store, tenantID, sessionID, partID string, body string) {
	t.Helper()
	u := blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  sessionID,
		PartID:     partID,
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/gzip", strings.NewReader(body)); err != nil {
		t.Fatalf("Put %s: %v", partID, err)
	}
}

// spec: §11 line 37 — GC-triggered decrement. SoftDeleteSession (the
// §7.1 retention GC path) releases the deleted artifacts' bytes back to
// the per-tenant storage-quota counter once the catalog rows commit.
func TestSoftDeleteSessionReleasesStorageQuota(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	// Three artifacts under s_1 (5 + 7 + 3 bytes) and one under s_2 that
	// must not be released by the s_1 sweep.
	putSized(t, store, "acme", "s_1", "a", "hello")   // 5
	putSized(t, store, "acme", "s_1", "b", "goodbye") // 7
	putSized(t, store, "acme", "s_1", "c", "abc")     // 3
	putSized(t, store, "acme", "s_2", "x", "untouched")

	if _, err := store.SoftDeleteSession(context.Background(), "acme", "s_1", time.Hour); err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}
	if got := quota.get("acme"); got != -(5 + 7 + 3) {
		t.Errorf("storage-quota delta = %d, want %d", got, -(5 + 7 + 3))
	}
}

// spec: §11 line 37 — the decrement is keyed to the live→soft_deleted
// transition. A second SoftDeleteSession finds no live rows and must
// not double-decrement (the catalog's single-writer guard).
func TestSoftDeleteSessionDecrementIsIdempotent(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	putSized(t, store, "acme", "s_1", "a", "hello") // 5

	ctx := context.Background()
	if _, err := store.SoftDeleteSession(ctx, "acme", "s_1", time.Hour); err != nil {
		t.Fatalf("SoftDeleteSession #1: %v", err)
	}
	if _, err := store.SoftDeleteSession(ctx, "acme", "s_1", time.Hour); err != nil {
		t.Fatalf("SoftDeleteSession #2: %v", err)
	}
	if got := quota.get("acme"); got != -5 {
		t.Errorf("storage-quota delta after double sweep = %d, want -5", got)
	}
}

// spec: §11 line 37 — the §12.8 erasure DeleteBySession path releases a
// session's live artifact bytes too.
func TestDeleteBySessionReleasesStorageQuota(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	putSized(t, store, "acme", "s_1", "a", "hello")   // 5
	putSized(t, store, "acme", "s_1", "b", "goodbye") // 7

	if _, err := store.DeleteBySession(context.Background(), "acme", "s_1"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if got := quota.get("acme"); got != -(5 + 7) {
		t.Errorf("storage-quota delta = %d, want %d", got, -(5 + 7))
	}
}

// spec: §11 line 37 — a single-object SoftDelete releases that object's
// bytes; a replayed SoftDelete on the already-deleted row does not.
func TestSoftDeleteSingleObjectReleasesOnceThenIsIdempotent(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "a",
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/gzip", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.SoftDelete(u, time.Hour); err != nil {
		t.Fatalf("SoftDelete #1: %v", err)
	}
	if err := store.SoftDelete(u, time.Hour); err != nil {
		t.Fatalf("SoftDelete #2: %v", err)
	}
	if got := quota.get("acme"); got != -5 {
		t.Errorf("storage-quota delta = %d, want -5", got)
	}
}

// spec: §11 line 37 — with no releaser wired (dev mode / tests), the
// soft-delete paths must not panic and still transition the rows.
func TestSoftDeleteSessionWithoutReleaserDoesNotPanic(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})

	putSized(t, store, "acme", "s_1", "a", "hello")
	count, err := store.SoftDeleteSession(context.Background(), "acme", "s_1", time.Hour)
	if err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

// spec: §12.5 rule 4 — SoftDeleteRow soft-deletes a chunk's artifact_store
// row and releases its bytes exactly once, without tombstoning the bucket
// object (the §12.5 checkpoint chunk-release path hard-deletes the object
// per key itself). A replay over the already-soft-deleted row issues no
// second decrement.
func TestSoftDeleteRowReleasesBytesOnceWithoutTombstone(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  "s_1",
		PartID:     "cp-1/chunk-00000.tar",
		TTL:        time.Hour,
	}
	ref, err := store.Put(u, "application/x-tar", strings.NewReader("chunkbytes")) // 10
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	ctx := context.Background()
	if err := store.SoftDeleteRow(ctx, ref, time.Hour); err != nil {
		t.Fatalf("SoftDeleteRow #1: %v", err)
	}
	if got := quota.get("acme"); got != -10 {
		t.Errorf("storage-quota delta = %d, want -10", got)
	}
	// SoftDeleteRow does not tombstone the bucket object: it leaves it for
	// the caller's per-key hard delete, so the object is still live here.
	if _, err := inner.Stat(u); err != nil {
		t.Errorf("object must stay live after SoftDeleteRow (no bucket tombstone): %v", err)
	}
	// Replay over the already-soft-deleted row: no second decrement.
	if err := store.SoftDeleteRow(ctx, ref, time.Hour); err != nil {
		t.Fatalf("SoftDeleteRow #2: %v", err)
	}
	if got := quota.get("acme"); got != -10 {
		t.Errorf("storage-quota delta after replay = %d, want -10 (exactly once)", got)
	}
}

// spec: §12.5 rule 4 — a chunk with no catalog row (written before the
// cataloging wiring, or an uncounted straggler an outstanding grant landed)
// is a no-op for SoftDeleteRow: no error, no decrement.
func TestSoftDeleteRowOnMissingRowIsNoOp(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	quota := newFakeQuota()
	store := New(inner, cat, Options{QuotaReleaser: quota})

	if err := store.SoftDeleteRow(context.Background(),
		"lenny-blob://acme/checkpoints/s_1/cp-1%2Fchunk-00000.tar?ttl=3600&enc=aes256gcm", time.Hour); err != nil {
		t.Fatalf("SoftDeleteRow on absent row: %v", err)
	}
	if got := quota.get("acme"); got != 0 {
		t.Errorf("storage-quota delta = %d, want 0 for a missing row", got)
	}
}

// spec: §11 line 37 — SetQuotaReleaser installs the counter after
// construction (the gateway resolves the Redis-backed counter after the
// decorator is built). A delete after the setter decrements.
func TestSetQuotaReleaserInstallsDecrement(t *testing.T) {
	inner := blobstore.NewMemoryStore(nil)
	cat := newFakeCatalog()
	store := New(inner, cat, Options{})
	quota := newFakeQuota()
	store.SetQuotaReleaser(quota)

	putSized(t, store, "acme", "s_1", "a", "world!") // 6
	if _, err := store.SoftDeleteSession(context.Background(), "acme", "s_1", time.Hour); err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}
	if got := quota.get("acme"); got != -6 {
		t.Errorf("storage-quota delta = %d, want -6", got)
	}
}
