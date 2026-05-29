// SPDX-License-Identifier: MIT

package gcs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// fakeGCS mirrors the GCS object surface in memory.
type fakeGCS struct {
	objects map[string]*fakeObject
}

type fakeObject struct {
	body         []byte
	mimeType     string
	lastModified time.Time
	metadata     map[string]string
	kmsKeyName   string
}

func newFakeGCS() *fakeGCS { return &fakeGCS{objects: map[string]*fakeObject{}} }

type fakeWriter struct {
	gcs      *fakeGCS
	key      string
	body     bytes.Buffer
	mimeType string
	kmsKey   string
	closed   bool
	written  bool
}

func (w *fakeWriter) Write(p []byte) (int, error) {
	w.written = true
	return w.body.Write(p)
}

func (w *fakeWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.gcs.objects[w.key] = &fakeObject{
		body:         append([]byte(nil), w.body.Bytes()...),
		mimeType:     w.mimeType,
		lastModified: time.Now().UTC(),
		metadata:     map[string]string{},
		kmsKeyName:   w.kmsKey,
	}
	return nil
}

func (f *fakeGCS) NewWriter(_ context.Context, key, contentType, kmsKey string) writeCloser {
	return &fakeWriter{gcs: f, key: key, mimeType: contentType, kmsKey: kmsKey}
}

type fakeReader struct {
	body  *bytes.Reader
	attrs *storage.ReaderObjectAttrs
}

func (r *fakeReader) Read(p []byte) (int, error)        { return r.body.Read(p) }
func (r *fakeReader) Close() error                      { return nil }
func (r *fakeReader) Attrs() *storage.ReaderObjectAttrs { return r.attrs }

func (f *fakeGCS) NewReader(_ context.Context, key string) (readCloser, error) {
	obj, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}
	return &fakeReader{
		body: bytes.NewReader(obj.body),
		attrs: &storage.ReaderObjectAttrs{
			ContentType:  obj.mimeType,
			Size:         int64(len(obj.body)),
			LastModified: obj.lastModified,
		},
	}, nil
}

func (f *fakeGCS) Attrs(_ context.Context, key string) (*storage.ObjectAttrs, error) {
	obj, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}
	meta := map[string]string{}
	for k, v := range obj.metadata {
		meta[k] = v
	}
	return &storage.ObjectAttrs{
		Name:        key,
		ContentType: obj.mimeType,
		Size:        int64(len(obj.body)),
		Updated:     obj.lastModified,
		Metadata:    meta,
	}, nil
}

func (f *fakeGCS) Update(_ context.Context, key string, metadata map[string]string) error {
	obj, ok := f.objects[key]
	if !ok {
		return storage.ErrObjectNotExist
	}
	for k, v := range metadata {
		obj.metadata[k] = v
	}
	return nil
}

func (f *fakeGCS) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func (f *fakeGCS) List(_ context.Context) ([]string, error) {
	out := []string{}
	for k := range f.objects {
		out = append(out, k)
	}
	return out, nil
}

func newStore(t *testing.T) (*Store, *fakeGCS) {
	t.Helper()
	f := newFakeGCS()
	return newWithClient(f, "lenny-test", nil), f
}

func testURI(tenant, session, part string) blobstore.URI {
	return blobstore.URI{TenantID: tenant, SessionID: session, PartID: part}
}

// spec: §4.5 — Put + Get round-trip.
// diagnosis: a freshly-written blob reads back unchanged.
func TestPutGetRoundTrip(t *testing.T) {
	store, _ := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "application/octet-stream", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, body, err := store.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.MimeType != "application/octet-stream" {
		t.Errorf("mime: got %q", info.MimeType)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "hello" {
		t.Errorf("body: got %q", got)
	}
}

// spec: §4.5 — Put on an existing live URI returns ErrConflict.
// diagnosis: the §4.5 write-once invariant rejects an overwrite.
func TestPutRejectsConflict(t *testing.T) {
	store, _ := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("a")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	_, err := store.Put(u, "text/plain", strings.NewReader("b"))
	if !errors.Is(err, blobstore.ErrConflict) {
		t.Errorf("Put 2: %v, want ErrConflict", err)
	}
}

// spec: §4.5 — Get on a missing URI returns ErrNotFound.
// diagnosis: an unknown key surfaces ErrNotFound, not a leak of the GCS-level error.
func TestGetMissing(t *testing.T) {
	store, _ := newStore(t)
	_, _, err := store.Get(testURI("acme", "s1", "missing"))
	if !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

// spec: §12.5 — SoftDelete tombstones the blob.
// diagnosis: the lenny-deleted-at metadata key is the soft-delete marker; Get / Stat return ErrNotFound.
func TestSoftDelete(t *testing.T) {
	store, f := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := store.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get tombstoned: %v, want ErrNotFound", err)
	}
	if _, ok := f.objects[objectKey(u)].metadata[tombstoneMeta]; !ok {
		t.Error("tombstone metadata not written")
	}
}

// spec: §12.5 — HardPrune removes past-retention tombstones.
// diagnosis: tombstones older than retention are deleted.
func TestHardPrune(t *testing.T) {
	store, f := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	f.objects[objectKey(u)].metadata[tombstoneMeta] = old
	count := store.HardPrune(time.Now(), 24*time.Hour)
	if count != 1 {
		t.Errorf("HardPrune count: got %d, want 1", count)
	}
	if _, ok := f.objects[objectKey(u)]; ok {
		t.Error("HardPrune did not delete the tombstoned blob")
	}
}

// spec: §12.5 — KMS resolver picks per-tenant CMEK.
// diagnosis: when KMSKeyResolver returns a non-empty key, the writer's KMSKeyName is set.
func TestPutAppliesKMSResolver(t *testing.T) {
	f := newFakeGCS()
	store := newWithClient(f, "lenny-test", func(u blobstore.URI) string {
		if u.TenantID == "acme" {
			return "projects/p/locations/us/keyRings/r/cryptoKeys/k-acme"
		}
		return ""
	})
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got := f.objects[objectKey(u)].kmsKeyName
	if got != "projects/p/locations/us/keyRings/r/cryptoKeys/k-acme" {
		t.Errorf("KMSKeyName: got %q", got)
	}
}

// TestDeleteByTenant asserts the §12.5 ll. 295 prefix-scoped bulk
// delete removes every object under one tenant prefix while another
// tenant's objects survive.
//
// spec: §12.5 ll. 295.
func TestDeleteByTenant(t *testing.T) {
	store, f := newStore(t)
	for _, u := range []blobstore.URI{
		testURI("acme", "s1", "p1"),
		testURI("acme", "s2", "p2"),
		testURI("globex", "s1", "p1"),
	} {
		if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s/%s: %v", u.TenantID, u.PartID, err)
		}
	}
	deleted, err := store.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if _, ok := f.objects[objectKey(testURI("globex", "s1", "p1"))]; !ok {
		t.Error("globex object erased by acme tenant delete (cross-tenant leak)")
	}
	for _, u := range []blobstore.URI{testURI("acme", "s1", "p1"), testURI("acme", "s2", "p2")} {
		if _, ok := f.objects[objectKey(u)]; ok {
			t.Errorf("acme object %s survived tenant delete", u.PartID)
		}
	}
}
