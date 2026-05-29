// SPDX-License-Identifier: MIT

package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// fakeS3 mirrors the S3 PutObject / GetObject / HeadObject /
// PutObjectTagging / GetObjectTagging / ListObjectsV2 / DeleteObject
// surface in memory. Object versions and replication are out of
// scope; the fake stores one body + one tag set per key.
type fakeS3 struct {
	objects map[string]*fakeObject
}

type fakeObject struct {
	body         []byte
	mimeType     string
	lastModified time.Time
	tags         []types.Tag
	sseKMSKeyID  string
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string]*fakeObject{}} }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[awssdk.ToString(in.Key)] = &fakeObject{
		body:         body,
		mimeType:     awssdk.ToString(in.ContentType),
		lastModified: time.Now().UTC(),
		sseKMSKeyID:  awssdk.ToString(in.SSEKMSKeyId),
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := awssdk.ToString(in.Key)
	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(obj.body)),
		ContentType:   awssdk.String(obj.mimeType),
		ContentLength: awssdk.Int64(int64(len(obj.body))),
		LastModified:  &obj.lastModified,
	}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	key := awssdk.ToString(in.Key)
	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NotFound{}
	}
	return &s3.HeadObjectOutput{
		ContentType:   awssdk.String(obj.mimeType),
		ContentLength: awssdk.Int64(int64(len(obj.body))),
		LastModified:  &obj.lastModified,
	}, nil
}

func (f *fakeS3) PutObjectTagging(_ context.Context, in *s3.PutObjectTaggingInput, _ ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error) {
	key := awssdk.ToString(in.Key)
	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NotFound{}
	}
	obj.tags = in.Tagging.TagSet
	return &s3.PutObjectTaggingOutput{}, nil
}

func (f *fakeS3) GetObjectTagging(_ context.Context, in *s3.GetObjectTaggingInput, _ ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error) {
	key := awssdk.ToString(in.Key)
	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectTaggingOutput{TagSet: obj.tags}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, awssdk.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := awssdk.ToString(in.Prefix)
	out := &s3.ListObjectsV2Output{}
	t := false
	for k := range f.objects {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		key := k
		out.Contents = append(out.Contents, types.Object{Key: &key})
	}
	out.IsTruncated = &t
	return out, nil
}

func (f *fakeS3) CopyObject(_ context.Context, in *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	src := awssdk.ToString(in.CopySource)
	// CopySource is "{bucket}/{key}" — strip the leading bucket
	// segment to recover the source key.
	if i := strings.Index(src, "/"); i >= 0 {
		src = src[i+1:]
	}
	srcObj, ok := f.objects[src]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	dstKey := awssdk.ToString(in.Key)
	dstBody := append([]byte(nil), srcObj.body...)
	f.objects[dstKey] = &fakeObject{
		body:         dstBody,
		mimeType:     srcObj.mimeType,
		lastModified: time.Now().UTC(),
		sseKMSKeyID:  awssdk.ToString(in.SSEKMSKeyId),
	}
	return &s3.CopyObjectOutput{}, nil
}

func newStore(t *testing.T) (*Store, *fakeS3) {
	t.Helper()
	f := newFakeS3()
	return newWithClient(f, "lenny-test", nil), f
}

func testURI(tenant, session, part string) blobstore.URI {
	return blobstore.URI{TenantID: tenant, SessionID: session, PartID: part}
}

// spec: §4.5 — Put + Get round-trip on a fresh URI.
// diagnosis: a freshly-written blob reads back with the same body + mime type.
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
	if info.Size != 5 {
		t.Errorf("size: got %d, want 5", info.Size)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "hello" {
		t.Errorf("body: got %q, want hello", got)
	}
}

// spec: §4.5 — Put on an existing live URI returns ErrConflict.
// diagnosis: the §4.5 write-once invariant rejects an overwrite of a live blob.
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
// diagnosis: an unknown key surfaces ErrNotFound, not a leak of the bucket-level S3 error.
func TestGetMissing(t *testing.T) {
	store, _ := newStore(t)
	_, _, err := store.Get(testURI("acme", "s1", "p1"))
	if !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

// spec: §12.5 — SoftDelete tombstones the blob; Get + Stat return ErrNotFound.
// diagnosis: the lenny-deleted-at object tag is the soft-delete marker.
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
	if _, err := store.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Stat tombstoned: %v, want ErrNotFound", err)
	}
	// Verify the tombstone tag was written.
	obj := f.objects[objectKey(u)]
	if len(obj.tags) == 0 || awssdk.ToString(obj.tags[0].Key) != tombstoneTag {
		t.Errorf("tombstone tag not written: %v", obj.tags)
	}
}

// spec: §12.5 — SoftDelete is idempotent.
// diagnosis: SoftDelete on a missing key is a no-op returning nil.
func TestSoftDeleteIdempotent(t *testing.T) {
	store, _ := newStore(t)
	if err := store.SoftDelete(testURI("acme", "s1", "missing")); err != nil {
		t.Errorf("SoftDelete missing: %v, want nil", err)
	}
}

// spec: §12.5 — HardPrune removes tombstoned objects past retention.
// diagnosis: the GC sweep DeleteObject's tombstones older than the retention window.
func TestHardPrune(t *testing.T) {
	store, f := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Tombstone with a deletion timestamp in the past.
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	f.objects[objectKey(u)].tags = []types.Tag{
		{Key: awssdk.String(tombstoneTag), Value: awssdk.String(old)},
	}
	count := store.HardPrune(time.Now(), 24*time.Hour)
	if count != 1 {
		t.Errorf("HardPrune count: got %d, want 1", count)
	}
	if _, ok := f.objects[objectKey(u)]; ok {
		t.Error("HardPrune did not DeleteObject the tombstoned blob")
	}
}

// spec: §12.5 — HardPrune leaves blobs whose tombstone is within retention.
// diagnosis: a recent tombstone must not be hard-pruned.
func TestHardPruneRespectsRetention(t *testing.T) {
	store, f := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	recent := time.Now().UTC().Format(time.RFC3339)
	f.objects[objectKey(u)].tags = []types.Tag{
		{Key: awssdk.String(tombstoneTag), Value: awssdk.String(recent)},
	}
	if count := store.HardPrune(time.Now(), 24*time.Hour); count != 0 {
		t.Errorf("HardPrune count: got %d, want 0", count)
	}
}

// spec: §12.5 — SSE-KMS resolver picks the per-tenant key.
// diagnosis: when SSEKeyResolver returns a non-empty key id, PutObject sets SSE-KMS with that id.
func TestPutAppliesSSEKMSResolver(t *testing.T) {
	f := newFakeS3()
	store := newWithClient(f, "lenny-test", func(u blobstore.URI) string {
		if u.TenantID == "acme" {
			return "alias/lenny/tenant/acme"
		}
		return ""
	})
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := f.objects[objectKey(u)].sseKMSKeyID; got != "alias/lenny/tenant/acme" {
		t.Errorf("SSEKMSKeyID: got %q, want alias/lenny/tenant/acme", got)
	}
}

// TestDeleteByTenant asserts the §12.5 ll. 295 prefix-scoped bulk
// delete removes every object under one tenant prefix (across
// sessions) while another tenant's objects survive.
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

// TestDeleteByTenantEmptyIsNoOp asserts an empty tenantID matches
// nothing so a mis-scoped call cannot wipe the bucket.
//
// spec: §12.5 ll. 295.
func TestDeleteByTenantEmptyIsNoOp(t *testing.T) {
	store, f := newStore(t)
	u := testURI("acme", "s1", "p1")
	if _, err := store.Put(u, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	deleted, err := store.DeleteByTenant(context.Background(), "")
	if err != nil {
		t.Fatalf("DeleteByTenant(\"\"): %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	if _, ok := f.objects[objectKey(u)]; !ok {
		t.Error("object erased by empty-tenant delete")
	}
}
