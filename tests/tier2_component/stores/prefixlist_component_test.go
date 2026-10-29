//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the §10.1.7 chunk-prefix listing
// (blobstore.PrefixLister) on the MinIO, S3, and GCS backends, each
// against a real provider API: miniostore and the AWS SDK S3 store over a
// MinIO container, and the GCS store over a fake-gcs-server container.
// They run the shared prefixlisttest conformance suite, list a prefix
// larger than one provider page (1,000 keys for ListObjectsV2 and the GCS
// Objects listing) so the continuation-token and page-token loops run
// against the provider rather than a fake, and resolve a resume chunk set
// end to end through resumechunks.Resolver on the S3 store. No Azure
// emulator is provisioned in tests/testinfra, so the Azure store's listing
// is pinned at tier 1 over its in-package fake only.
package stores_test

import (
	"context"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/lennylabs/lenny/pkg/blobstore/prefixlisttest"
	"github.com/lennylabs/lenny/pkg/blobstore/s3"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// overOnePage is one key more than the 1,000-key page the S3 and GCS
// listings return by default, so a listing of this many keys needs a
// second provider page.
const overOnePage = 1001

// prefixListBackends starts every real-provider backend the component
// tier can reach and returns each as a prefixlisttest.Store.
func prefixListBackends(t *testing.T) map[string]prefixlisttest.Store {
	t.Helper()
	// Each backend gets its own container: the suite writes the same keys
	// on every backend, and blobs are write-once.
	minio, _ := startMinIOStore(t)
	return map[string]prefixlisttest.Store{
		"minio":        minio,
		"s3-on-minio":  s3StoreOver(t, containers.StartMinIO(t, containers.MinIOOptions{})),
		"gcs-fake-gcs": startGCSStore(t),
	}
}

// s3StoreOver builds an AWS SDK S3 store over a MinIO container with
// path-style addressing.
func s3StoreOver(t *testing.T, mc *containers.MinIO) *s3.Store {
	t.Helper()
	store, err := s3.New(s3.Config{
		AWSConfig: awssdk.Config{
			Region:       "us-east-1",
			Credentials:  credentials.NewStaticCredentialsProvider(mc.AccessKey, mc.SecretKey, ""),
			BaseEndpoint: awssdk.String("http://" + mc.Endpoint),
		},
		Bucket:       mc.Bucket,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("s3.New: %v", err)
	}
	return store
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
// diagnosis: a backend's ListByPrefix diverged from the PrefixLister
// contract against a real provider API: it returned another tenant's or
// another session's keys, returned keys out of order, offered a
// soft-deleted chunk, or accepted a URI that cannot name one session
// prefix. The §10.1.7 resume contiguity check reads this listing, so a
// divergence corrupts or empties a restored workspace on that backend.
func TestPrefixListerConformanceAgainstRealBackends(t *testing.T) {
	for name, store := range prefixListBackends(t) {
		t.Run(name, func(t *testing.T) {
			prefixlisttest.Run(t, func(*testing.T) prefixlisttest.Store { return store })
		})
	}
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 4.4 (Event / Checkpoint Store: Checkpoint abort cleanup)
// diagnosis: a backend's ListByPrefix returned only the first provider
// page of a prefix holding more than 1,000 objects, so its
// continuation-token (S3), page-token (GCS), or iterator (MinIO) loop
// does not follow the provider's pagination. A checkpoint with more
// chunks than one page would then fail the resume contiguity check or
// leave chunks behind on abort cleanup.
func TestPrefixListerPaginatesPastOneProviderPage(t *testing.T) {
	for name, store := range prefixListBackends(t) {
		t.Run(name, func(t *testing.T) {
			const sess = "sess-many"
			for n := 0; n < overOnePage; n++ {
				prefixlisttest.PutChunk(t, store, "acme", sess, "ck-many", n, 1)
			}
			prefixlisttest.PutChunk(t, store, "globex", sess, "ck-many", 0, 1)
			got, err := store.ListByPrefix(context.Background(), prefixlisttest.PrefixURI("acme", sess, "ck-many"))
			if err != nil {
				t.Fatalf("ListByPrefix: %v", err)
			}
			if len(got) != overOnePage {
				t.Fatalf("ListByPrefix = %d entries, want %d across provider pages", len(got), overOnePage)
			}
			for i, info := range got {
				if want := prefixlisttest.ChunkURI("acme", sess, "ck-many", i).PartID; info.URI.PartID != want || info.URI.TenantID != "acme" {
					t.Fatalf("entry %d = %s/%s, want acme/%s", i, info.URI.TenantID, info.URI.PartID, want)
				}
			}
		})
	}
}

// stubManifests serves one manifest row to the resolver.
type stubManifests struct{ rec partialmanifeststore.Record }

func (s stubManifests) Get(context.Context, string, string) (partialmanifeststore.Record, error) {
	return s.rec, nil
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 13.2 (Network Isolation: capability model)
// diagnosis: the §10.1.7 resume resolver did not restore a checkpoint's
// chunk set on the AWS SDK S3 backend. F-10.1.45 left the resolver nil on
// S3 because the S3 store had no ListByPrefix; this resolves a complete
// manifest's chunks over a real S3 API and expects one presigned GET
// capability per chunk in ascending index order with the stored sizes.
func TestResumeChunkResolverRestoresChunksOnS3Backend(t *testing.T) {
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	store := s3StoreOver(t, mc)
	const (
		tenant  = "acme"
		sess    = "sess-resume"
		ckpt    = "ck-resume"
		nChunks = 4
	)
	// PutChunk writes the canonical {checkpoint_id}/chunk-{n:05d}.tar.gz
	// key that resumechunks.ChunkObjectURI names.
	for n := nChunks - 1; n >= 0; n-- {
		prefixlisttest.PutChunk(t, store, tenant, sess, ckpt, n, 100+n)
	}
	r := &resumechunks.Resolver{
		Manifests: stubManifests{rec: partialmanifeststore.Record{
			TenantID:      tenant,
			CheckpointID:  ckpt,
			ChunkCount:    nChunks,
			ChunkEncoding: partialmanifeststore.ChunkEncodingTarGz,
		}},
		Presigner: store,
		Lister:    store,
		TTL:       time.Minute,
	}
	res, err := r.Resolve(context.Background(), tenant, sess, ckpt)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Grants) != nChunks {
		t.Fatalf("Resolve = %d grants, want %d", len(res.Grants), nChunks)
	}
	for i, g := range res.Grants {
		if g.Index != uint32(i) || g.Length != int64(100+i) || g.URL == "" {
			t.Errorf("grant %d = {Index %d, Length %d, URL %q}, want index %d length %d with a URL", i, g.Index, g.Length, g.URL, i, 100+i)
		}
	}
}
