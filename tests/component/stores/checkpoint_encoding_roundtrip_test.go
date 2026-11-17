//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.1 chunk_encoding contract across real
// Postgres and real MinIO: an ArchiveTree checkpoint, split into chunks
// named by the manifest's chunk_encoding, survives the manifest round-trip
// and restores through ExtractTree when the resume side derives each chunk
// key from the stored column.
package stores_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 10.1 (Horizontal Scaling) — Chunked-object storage model ("The
// suffix therefore reflects the actual on-disk payload") and Reassembly on
// resume (the manifest's chunk_encoding column "remains the authoritative
// source of truth consulted by the resume path's reassembly pipeline").
// diagnosis: a failure in the tar.gz case means the gzip archive the
// adapter writes cannot be stored under a tar.gz manifest and restored by
// deriving chunk keys from that manifest, so a resume after this change
// finds no chunks or cannot decode them. A failure in the tar case means a
// manifest written before the gateway declared tar.gz, whose chunks are
// gzip bytes under .tar keys, no longer restores.
func TestCheckpointChunkEncodingRoundTripAcrossRealBackends(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	manifests := partialmanifestpg.New(pg.Pool, nil)
	objects, err := miniostore.New(miniostore.Config{
		Endpoint:  mc.Endpoint,
		AccessKey: mc.AccessKey,
		SecretKey: mc.SecretKey,
		Bucket:    mc.Bucket,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	ctx := context.Background()

	src := t.TempDir()
	content := strings.Repeat("alice workspace bytes\n", 4096)
	if err := os.WriteFile(filepath.Join(src, "notes.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := workspace.ArchiveTree([]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: src}}, &archive); err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}

	for _, enc := range []partialmanifeststore.ChunkEncoding{
		partialmanifeststore.ChunkEncodingTarGz,
		partialmanifeststore.ChunkEncodingTar,
	} {
		t.Run(string(enc), func(t *testing.T) {
			tenant := freshTenant(t, ctx, pg)
			sessID := newUUID(t)
			checkpointID := newUUID(t)

			// Split the gzip archive into fixed-size chunks and commit
			// each under the key the manifest's encoding names.
			const chunkSize = 512
			data := archive.Bytes()
			count := 0
			for off := 0; off < len(data); off += chunkSize {
				end := min(off+chunkSize, len(data))
				u := resumechunks.ChunkObjectURI(tenant, sessID, checkpointID, uint32(count), enc)
				if _, err := objects.Put(u, "application/octet-stream", bytes.NewReader(data[off:end])); err != nil {
					t.Fatalf("Put chunk %d: %v", count, err)
				}
				count++
			}
			now := time.Now().UTC()
			if err := manifests.Put(ctx, partialmanifeststore.Record{
				TenantID:             tenant,
				CheckpointID:         checkpointID,
				SessionID:            sessID,
				ChunkObjectKeyPrefix: tenant + "/checkpoints/" + sessID + "/" + checkpointID + "/",
				ChunkSizeBytes:       chunkSize,
				ChunkEncoding:        enc,
				CheckpointStartedAt:  now,
				CheckpointTimeoutAt:  now.Add(time.Minute),
			}); err != nil {
				t.Fatalf("Put manifest: %v", err)
			}

			rec, err := manifests.Get(ctx, tenant, checkpointID)
			if err != nil {
				t.Fatalf("Get manifest: %v", err)
			}
			if rec.ChunkEncoding != enc {
				t.Fatalf("stored chunk_encoding = %q, want %q", rec.ChunkEncoding, enc)
			}

			// Reassemble from the stored column, as the resume path does,
			// and replay through the adapter's extractor.
			readers := make([]io.Reader, 0, count)
			for n := 0; n < count; n++ {
				u := resumechunks.ChunkObjectURI(tenant, sessID, checkpointID, uint32(n), rec.ChunkEncoding)
				u.TTL = time.Hour
				_, body, err := objects.Get(u)
				if err != nil {
					t.Fatalf("Get chunk %d (%s): %v", n, u.PartID, err)
				}
				t.Cleanup(func() { _ = body.Close() })
				readers = append(readers, body)
			}
			dst := t.TempDir()
			if _, err := workspace.ExtractTree([]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: dst}}, io.MultiReader(readers...)); err != nil {
				t.Fatalf("ExtractTree: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dst, "notes.txt"))
			if err != nil {
				t.Fatalf("read restored file: %v", err)
			}
			if string(got) != content {
				t.Errorf("restored file differs from the checkpointed one (%d vs %d bytes)", len(got), len(content))
			}
		})
	}
}
