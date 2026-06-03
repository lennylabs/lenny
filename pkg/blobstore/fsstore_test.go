// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

func newFSStore(t *testing.T, clock func() time.Time) *blobstore.FilesystemStore {
	t.Helper()
	s, err := blobstore.NewFilesystemStore(t.TempDir(), clock)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	return s
}

func fsPut(t *testing.T, s *blobstore.FilesystemStore, u blobstore.URI, body string) {
	t.Helper()
	if _, err := s.Put(u, "text/plain", strings.NewReader(body)); err != nil {
		t.Fatalf("Put %s: %v", u.PartID, err)
	}
}

// spec: §17.4 line 165 — the filesystem store round-trips a blob through
// the same Store contract as MinIO.
func TestFilesystemStorePutGet_spec_17_4_165(t *testing.T) {
	s := newFSStore(t, nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	fsPut(t, s, u, "hello world")

	info, rc, err := s.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
	if info.Size != int64(len("hello world")) {
		t.Fatalf("size = %d, want %d", info.Size, len("hello world"))
	}
	if info.MimeType != "text/plain" {
		t.Fatalf("mime = %q", info.MimeType)
	}
}

// spec: §17.4 line 186 — state persists across a restart: a second store
// rooted at the same directory reads back a previously written blob.
func TestFilesystemStorePersistsAcrossReopen_spec_17_4_186(t *testing.T) {
	root := t.TempDir()
	s1, err := blobstore.NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	fsPut(t, s1, u, "durable")

	// Simulate `lenny down` / `lenny up`: a fresh store over the same root.
	s2, err := blobstore.NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	info, rc, err := s2.Get(u)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "durable" {
		t.Fatalf("body after reopen = %q, want %q", got, "durable")
	}
	_ = info
}

// §4.5 write-once: a second Put on the same key is rejected.
func TestFilesystemStorePutRejectsOverwrite(t *testing.T) {
	s := newFSStore(t, nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	fsPut(t, s, u, "first")
	if _, err := s.Put(u, "text/plain", strings.NewReader("second")); err != blobstore.ErrConflict {
		t.Fatalf("overwrite err = %v, want ErrConflict", err)
	}
}

func TestFilesystemStoreGetUnknownIsNotFound(t *testing.T) {
	s := newFSStore(t, nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "absent", TTL: time.Hour}
	if _, _, err := s.Get(u); err != blobstore.ErrNotFound {
		t.Fatalf("Get unknown = %v, want ErrNotFound", err)
	}
}

func TestFilesystemStoreGetExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	clk := now
	s := newFSStore(t, func() time.Time { return clk })
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: 60 * time.Second}
	fsPut(t, s, u, "x")
	clk = now.Add(2 * time.Minute)
	if _, _, err := s.Get(u); err != blobstore.ErrNotFound {
		t.Fatalf("expired Get = %v, want ErrNotFound", err)
	}
}

// spec: §12.5 — SoftDelete tombstones the blob: reads return NotFound
// but StatIncludingTombstones still reports SoftDeleted until hard-prune.
func TestFilesystemStoreSoftDeleteContract_spec_12_5(t *testing.T) {
	s := newFSStore(t, nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	fsPut(t, s, u, "x")

	if err := s.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := s.Get(u); err != blobstore.ErrNotFound {
		t.Fatalf("Get after soft-delete = %v, want ErrNotFound", err)
	}
	_, state, err := s.StatIncludingTombstones(u)
	if err != nil {
		t.Fatalf("StatIncludingTombstones: %v", err)
	}
	if state != blobstore.BlobStateSoftDeleted {
		t.Fatalf("state = %q, want soft_deleted", state)
	}
	// Idempotent.
	if err := s.SoftDelete(u); err != nil {
		t.Fatalf("second SoftDelete: %v", err)
	}
	// Soft-delete on an absent blob is a no-op.
	absent := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "absent", TTL: time.Hour}
	if err := s.SoftDelete(absent); err != nil {
		t.Fatalf("SoftDelete absent: %v", err)
	}
}

// spec: §12.5 — HardPrune removes tombstoned blobs past the retention
// window and leaves fresher tombstones in place.
func TestFilesystemStoreHardPruneRespectsRetention_spec_12_5(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	clk := now
	s := newFSStore(t, func() time.Time { return clk })
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
	fsPut(t, s, u, "x")
	if err := s.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	// Not yet past retention.
	if n := s.HardPrune(now.Add(30*time.Minute), time.Hour); n != 0 {
		t.Fatalf("premature prune removed %d", n)
	}
	if n := s.HardPrune(now.Add(2*time.Hour), time.Hour); n != 1 {
		t.Fatalf("prune removed %d, want 1", n)
	}
	if _, st, err := s.StatIncludingTombstones(u); err != blobstore.ErrNotFound || st != blobstore.BlobStateNotFound {
		t.Fatalf("after hard-prune: err=%v state=%v", err, st)
	}
}

// spec: §4.5 line 311 — Copy duplicates parent bytes for a derived
// session and enforces tenant/conflict invariants.
func TestFilesystemStoreCopy_spec_4_5_311(t *testing.T) {
	s := newFSStore(t, nil)
	src := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "parent", PartID: "snap", TTL: time.Hour}
	fsPut(t, s, src, "snapshot-bytes")
	dst := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "child", PartID: "snap", TTL: time.Hour}
	if err := s.Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	_, rc, err := s.Get(dst)
	if err != nil {
		t.Fatalf("Get copy: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "snapshot-bytes" {
		t.Fatalf("copy body = %q", got)
	}
	// Cross-tenant copy is refused.
	foreign := dst
	foreign.TenantID = "globex"
	if err := s.Copy(src, foreign); err != blobstore.ErrCrossTenant {
		t.Fatalf("cross-tenant Copy = %v, want ErrCrossTenant", err)
	}
	// Copy onto a live dst conflicts.
	if err := s.Copy(src, dst); err != blobstore.ErrConflict {
		t.Fatalf("conflicting Copy = %v, want ErrConflict", err)
	}
	// Copy from a missing src is NotFound.
	missing := src
	missing.PartID = "ghost"
	if err := s.Copy(missing, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "x", PartID: "y", TTL: time.Hour}); err != blobstore.ErrNotFound {
		t.Fatalf("missing-src Copy = %v, want ErrNotFound", err)
	}
}

// spec: §12.8 step 7 — DeleteBySession drops every object for a session
// across object types and leaves other sessions untouched.
func TestFilesystemStoreDeleteBySession_spec_12_8(t *testing.T) {
	s := newFSStore(t, nil)
	fsPut(t, s, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "victim", PartID: "a", TTL: time.Hour}, "1")
	fsPut(t, s, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "victim", PartID: "b", TTL: time.Hour}, "2")
	fsPut(t, s, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "survivor", PartID: "c", TTL: time.Hour}, "3")

	n, err := s.DeleteBySession(context.Background(), "acme", "victim")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteBySession removed %d, want 2", n)
	}
	if _, _, err := s.Get(blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "survivor", PartID: "c", TTL: time.Hour}); err != nil {
		t.Fatalf("survivor blob lost: %v", err)
	}
	// No-op on a session with no blobs.
	if n, _ := s.DeleteBySession(context.Background(), "acme", "nobody"); n != 0 {
		t.Fatalf("empty DeleteBySession removed %d, want 0", n)
	}
}

// spec: §12.5 line 295 / §12.8 Phase 4 — DeleteByTenant prefix-purges one
// tenant and an empty id is a guarded no-op.
func TestFilesystemStoreDeleteByTenant_spec_12_8(t *testing.T) {
	s := newFSStore(t, nil)
	fsPut(t, s, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s1", PartID: "a", TTL: time.Hour}, "1")
	fsPut(t, s, blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s2", PartID: "b", TTL: time.Hour}, "2")
	fsPut(t, s, blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s3", PartID: "c", TTL: time.Hour}, "3")

	n, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d, want 2", n)
	}
	if _, _, err := s.Get(blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s3", PartID: "c", TTL: time.Hour}); err != nil {
		t.Fatalf("foreign tenant blob lost: %v", err)
	}
	// Empty tenant is a guarded no-op.
	if n, _ := s.DeleteByTenant(context.Background(), ""); n != 0 {
		t.Fatalf("empty-tenant DeleteByTenant removed %d, want 0", n)
	}
	// DeleteByUser is a no-op on the artifact store.
	if n, _ := s.DeleteByUser(context.Background(), "globex", "alice"); n != 0 {
		t.Fatalf("DeleteByUser removed %d, want 0", n)
	}
}

// A tenant or session id that tries to traverse out of root is rejected
// rather than escaping the store directory.
func TestFilesystemStoreRejectsPathTraversal(t *testing.T) {
	s := newFSStore(t, nil)
	// A literal dot segment is the only value that would traverse, so it
	// is rejected.
	for _, bad := range []string{"..", "."} {
		u := blobstore.URI{TenantID: bad, ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err == nil {
			t.Fatalf("Put with tenant %q unexpectedly succeeded", bad)
		}
	}
	// Ids that embed separators or dot sequences are URL-escaped into a
	// single literal directory name, so they stay inside root and
	// round-trip safely rather than traversing.
	for _, safe := range []string{"a/b", "../../etc"} {
		u := blobstore.URI{TenantID: safe, ObjectType: blobstore.ObjectTypeUpload, SessionID: "s", PartID: "p", TTL: time.Hour}
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put with escaped tenant %q: %v", safe, err)
		}
		if _, err := s.Stat(u); err != nil {
			t.Fatalf("Stat escaped tenant %q: %v", safe, err)
		}
	}
}

func TestNewFilesystemStoreRejectsEmptyRoot(t *testing.T) {
	if _, err := blobstore.NewFilesystemStore("", nil); err == nil {
		t.Fatal("expected error for empty root")
	}
}
