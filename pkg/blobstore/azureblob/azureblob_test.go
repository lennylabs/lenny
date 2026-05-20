// SPDX-License-Identifier: MIT

package azureblob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// fakeBlob mirrors the Azure Blob surface in memory.
type fakeBlob struct {
	objects map[string]*fakeObject
}

type fakeObject struct {
	body         []byte
	mimeType     string
	lastModified time.Time
	metadata     map[string]*string
}

func newFakeBlob() *fakeBlob { return &fakeBlob{objects: map[string]*fakeObject{}} }

func (f *fakeBlob) UploadBuffer(_ context.Context, key string, body []byte, opts *azblob.UploadBufferOptions) error {
	mt := ""
	if opts != nil && opts.HTTPHeaders != nil && opts.HTTPHeaders.BlobContentType != nil {
		mt = *opts.HTTPHeaders.BlobContentType
	}
	f.objects[key] = &fakeObject{
		body:         append([]byte(nil), body...),
		mimeType:     mt,
		lastModified: time.Now().UTC(),
		metadata:     map[string]*string{},
	}
	return nil
}

func (f *fakeBlob) DownloadStream(_ context.Context, key string, _ *azblob.DownloadStreamOptions) (azblob.DownloadStreamResponse, error) {
	obj, ok := f.objects[key]
	if !ok {
		return azblob.DownloadStreamResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "BlobNotFound"}
	}
	size := int64(len(obj.body))
	mt := obj.mimeType
	lm := obj.lastModified
	return azblob.DownloadStreamResponse{
		DownloadResponse: blob.DownloadResponse{
			Body:          io.NopCloser(bytes.NewReader(obj.body)),
			ContentType:   &mt,
			ContentLength: &size,
			LastModified:  &lm,
		},
	}, nil
}

func (f *fakeBlob) GetProperties(_ context.Context, key string, _ *blob.GetPropertiesOptions) (blob.GetPropertiesResponse, error) {
	obj, ok := f.objects[key]
	if !ok {
		return blob.GetPropertiesResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "BlobNotFound"}
	}
	size := int64(len(obj.body))
	mt := obj.mimeType
	lm := obj.lastModified
	meta := map[string]*string{}
	for k, v := range obj.metadata {
		meta[k] = v
	}
	return blob.GetPropertiesResponse{
		ContentType:   &mt,
		ContentLength: &size,
		LastModified:  &lm,
		Metadata:      meta,
	}, nil
}

func (f *fakeBlob) SetMetadata(_ context.Context, key string, metadata map[string]*string, _ *blob.SetMetadataOptions) (blob.SetMetadataResponse, error) {
	obj, ok := f.objects[key]
	if !ok {
		return blob.SetMetadataResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "BlobNotFound"}
	}
	for k, v := range metadata {
		obj.metadata[k] = v
	}
	return blob.SetMetadataResponse{}, nil
}

func (f *fakeBlob) Delete(_ context.Context, key string, _ *azblob.DeleteBlobOptions) error {
	delete(f.objects, key)
	return nil
}

func (f *fakeBlob) List(_ context.Context) ([]string, error) {
	out := []string{}
	for k := range f.objects {
		out = append(out, k)
	}
	return out, nil
}

func newStore(t *testing.T) (*Store, *fakeBlob) {
	t.Helper()
	f := newFakeBlob()
	return newWithClient(f, "lenny-test", nil), f
}

func testURI(tenant, session, part string) blobstore.URI {
	return blobstore.URI{TenantID: tenant, SessionID: session, PartID: part}
}

// spec: §4.5 — Put + Get round-trip.
// diagnosis: a freshly-written blob reads back byte-for-byte.
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
// diagnosis: BlobNotFound surfaces as ErrNotFound.
func TestGetMissing(t *testing.T) {
	store, _ := newStore(t)
	_, _, err := store.Get(testURI("acme", "s1", "missing"))
	if !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get: %v, want ErrNotFound", err)
	}
}

// spec: §12.5 — SoftDelete tombstones the blob.
// diagnosis: the lenny_deleted_at metadata key marks the blob soft-deleted; Get / Stat return ErrNotFound.
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
	f.objects[objectKey(u)].metadata[tombstoneMeta] = metaPtr(old)
	count := store.HardPrune(time.Now(), 24*time.Hour)
	if count != 1 {
		t.Errorf("HardPrune: got %d, want 1", count)
	}
	if _, ok := f.objects[objectKey(u)]; ok {
		t.Error("HardPrune did not delete the tombstoned blob")
	}
}
