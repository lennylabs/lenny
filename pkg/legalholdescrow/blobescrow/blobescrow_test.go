// SPDX-License-Identifier: MIT

package blobescrow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
)

func t4Resolver(string) (string, bool, error) { return "tenant:acme", true, nil }

// TestStoreOptionsReuseTheArtifactProvider_spec_12_8 pins how the escrow
// store options derive from the artifact store's: the escrow bucket
// replaces the artifact bucket, the escrow endpoint overrides the MinIO
// endpoint, the SSE resolver is dropped, a filesystem root moves outside
// the artifact root, and the artifact options are left unchanged.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestStoreOptionsReuseTheArtifactProvider_spec_12_8(t *testing.T) {
	root := t.TempDir()
	artifact := providerflags.Options{
		Provider: providerflags.ProviderMinIO, Bucket: "artifacts", MinIOBucket: "artifacts",
		MinIOEndpoint: "minio:9000", MinIOAccessKey: "ak", MinIOSecretKey: "sk", MinIOUseSSL: true,
		Region: "eu-west-1", FilesystemRoot: root, SSEKeyResolver: t4Resolver,
	}
	got := StoreOptions(artifact, "escrow", "")
	if got.Bucket != "escrow" || got.MinIOBucket != "escrow" || got.SSEKeyResolver != nil {
		t.Fatalf("bucket=%q minioBucket=%q sse=%v", got.Bucket, got.MinIOBucket, got.SSEKeyResolver != nil)
	}
	if got.Provider != artifact.Provider || got.MinIOEndpoint != "minio:9000" || got.MinIOAccessKey != "ak" ||
		got.MinIOSecretKey != "sk" || !got.MinIOUseSSL || got.Region != "eu-west-1" {
		t.Fatalf("provider, credentials, or endpoint not reused: %+v", got)
	}
	if rel, err := filepath.Rel(root, got.FilesystemRoot); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("escrow filesystem root %q lies under the artifact root %q", got.FilesystemRoot, root)
	}
	if over := StoreOptions(artifact, "escrow", "escrow-minio:9000"); over.MinIOEndpoint != "escrow-minio:9000" {
		t.Fatalf("endpoint override = %q", over.MinIOEndpoint)
	}
	if artifact.Bucket != "artifacts" || artifact.SSEKeyResolver == nil {
		t.Fatal("StoreOptions mutated the artifact options")
	}
	if noFS := StoreOptions(providerflags.Options{}, "escrow", ""); noFS.FilesystemRoot != "" {
		t.Fatalf("filesystem root set with none configured: %q", noFS.FilesystemRoot)
	}
}

// TestNewStoreBypassesTheTenantSSEResolver_spec_12_8 pins the escrow store
// construction: no bucket builds no store, an unknown provider fails, and
// a built store accepts a write for a tenant the artifact store's SSE
// resolver marks as T4, which the artifact store refuses.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestNewStoreBypassesTheTenantSSEResolver_spec_12_8(t *testing.T) {
	ctx := context.Background()
	artifact := providerflags.Options{Provider: providerflags.ProviderMemory, SSEKeyResolver: t4Resolver}
	if s, err := NewStore(ctx, artifact, "", ""); err != nil || s != nil {
		t.Fatalf("no bucket: store=%v err=%v, want nil and nil", s, err)
	}
	if _, err := NewStore(ctx, providerflags.Options{Provider: "bogus"}, "escrow", ""); err == nil {
		t.Fatal("an unknown provider built an escrow store")
	}
	artifactStore, err := providerflags.Resolve(ctx, artifact)
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	escrowStore, err := NewStore(ctx, artifact, "escrow", "")
	if err != nil || escrowStore == nil {
		t.Fatalf("escrow store: %v", err)
	}
	u := ObjectURI("acme", legalholdescrow.EscrowObjectKey("acme", "artifact", "r"))
	if _, err := artifactStore.Put(u, "application/octet-stream", bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("artifact store accepted a T4 write; the discriminating case is vacuous")
	}
	if err := (Writer{Blobs: escrowStore, TenantID: "acme"}).Write(ctx, legalholdescrow.EscrowObjectKey("acme", "artifact", "r"), []byte("x")); err != nil {
		t.Fatalf("escrow store refused the sealed write: %v", err)
	}
}

// conflictStore rejects every Put as an overwrite of an immutable object.
type conflictStore struct{ blobstore.Store }

func (conflictStore) Put(blobstore.URI, string, io.Reader) (string, error) {
	return "", blobstore.ErrConflict
}

// failStore rejects every Put and every hard delete.
type failStore struct{ *blobstore.MemoryStore }

func (failStore) Put(blobstore.URI, string, io.Reader) (string, error) {
	return "", errors.New("bucket unreachable")
}
func (failStore) HardDeleteObject(blobstore.URI) error { return errors.New("bucket unreachable") }

// TestWriterSourceAndDeleterRoundTrip_spec_12_8 pins the adapters: the
// Writer stores the sealed payload at ObjectURI under the original tenant,
// an overwrite refused as a conflict counts as already escrowed, any other
// write error is returned, the Source reads an original by its blob URI,
// and the Deleter removes the object, treats an absent one as released,
// and refuses a store with no single-object delete.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestWriterSourceAndDeleterRoundTrip_spec_12_8(t *testing.T) {
	ctx := context.Background()
	mem := blobstore.NewMemoryStore(nil)
	key := legalholdescrow.EscrowObjectKey("acme", "artifact", "r")
	if err := (Writer{Blobs: mem, TenantID: "acme"}).Write(ctx, key, []byte("sealed")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_, rc, err := mem.Get(ObjectURI("acme", key))
	if err != nil {
		t.Fatalf("escrow object not at ObjectURI: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "sealed" {
		t.Fatalf("escrow object = %q", got)
	}
	if err := (Writer{Blobs: conflictStore{mem}, TenantID: "acme"}).Write(ctx, key, []byte("sealed")); err != nil {
		t.Fatalf("conflict on re-entry: %v, want nil", err)
	}
	if err := (Writer{Blobs: failStore{mem}, TenantID: "acme"}).Write(ctx, key, []byte("sealed")); err == nil {
		t.Fatal("Write swallowed a backend error")
	}

	src := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s", PartID: "p", TTL: time.Hour, Encoding: blobstore.Encoding}
	if _, err := mem.Put(src, "application/octet-stream", bytes.NewReader([]byte("original"))); err != nil {
		t.Fatalf("seed original: %v", err)
	}
	if b, err := (Source{Blobs: mem}).Read(ctx, legalholdescrow.HeldArtifact{BlobURI: src.String()}); err != nil || string(b) != "original" {
		t.Fatalf("Source.Read = %q, %v", b, err)
	}
	if _, err := (Source{Blobs: mem}).Read(ctx, legalholdescrow.HeldArtifact{BlobURI: "not a uri"}); err == nil {
		t.Fatal("Source.Read accepted a malformed blob URI")
	}

	del := Deleter{Blobs: mem}
	if err := del.Delete(ctx, "acme", "default", key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := mem.Get(ObjectURI("acme", key)); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("escrow object after Delete: %v", err)
	}
	if err := del.Delete(ctx, "acme", "default", key); err != nil {
		t.Fatalf("Delete of an absent object: %v, want nil", err)
	}
	if err := (Deleter{Blobs: conflictStore{mem}}).Delete(ctx, "acme", "default", key); !errors.Is(err, ErrNoTombstoner) {
		t.Fatalf("Delete without Tombstoner: %v, want ErrNoTombstoner", err)
	}
	if err := (Deleter{Blobs: failStore{mem}}).Delete(ctx, "acme", "default", key); err == nil {
		t.Fatal("Delete swallowed a backend error")
	}
}
