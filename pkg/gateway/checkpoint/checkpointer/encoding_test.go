// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// keyRecordingPresigner records the object key of every PUT capability it
// signs.
type keyRecordingPresigner struct {
	mu   sync.Mutex
	keys []blobstore.URI
}

func (p *keyRecordingPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	p.mu.Lock()
	p.keys = append(p.keys, u)
	p.mu.Unlock()
	return presignerFake{}.PresignPut(u, contentLength, ttl)
}

func (p *keyRecordingPresigner) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	return presignerFake{}.PresignGet(u, ttl)
}

func (p *keyRecordingPresigner) signed() []blobstore.URI {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]blobstore.URI(nil), p.keys...)
}

// spec: 10.1 (Horizontal Scaling) — Chunked-object storage model: the
// chunk suffix "reflects the actual on-disk payload". The adapter's
// checkpoint producer writes a gzip-compressed tar, so the opening frame
// declares tar.gz.
// diagnosis: a failure means the gateway declares an encoding that does
// not match the bytes ArchiveTree writes, so §4.4 manual recovery, which
// selects its decode pipeline from the declared encoding, feeds gzip bytes
// to a bare tar -x.
func TestCheckpointStartDeclaresTarGz_spec_10_1(t *testing.T) {
	rec := &startRecorder{}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dialAdapter(t, rec)})
	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	start := rec.recorded()
	if start == nil {
		t.Fatal("adapter recorded no CheckpointStart")
	}
	if got := start.GetChunkEncoding(); got != string(partialmanifeststore.ChunkEncodingTarGz) {
		t.Errorf("CheckpointStart.chunk_encoding = %q, want tar.gz", got)
	}
}

// spec: 10.1 (Horizontal Scaling) — Chunked-object storage model and
// Reassembly on resume: the manifest's chunk_encoding is captured at
// intent-row INSERT, every chunk is named chunk-{n}.{chunk_encoding}, and
// the resume path derives each chunk key from that column. The writer's
// signed keys and the reader's derived keys therefore agree for tar.gz.
// diagnosis: a failure means the manifest, the signed chunk keys, and the
// resume-side key derivation disagree on the encoding, so a resume or a
// workspace download looks for objects that were never written.
func TestManifestAndChunkKeysCarryTarGz_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: -1,
	}, 1<<30)
	presigner := &keyRecordingPresigner{}
	h.cp.Presigner = presigner
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rec := latestManifest(t, h, "acme", sid)
	if rec.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
		t.Fatalf("manifest chunk_encoding = %q, want tar.gz", rec.ChunkEncoding)
	}
	keys := presigner.signed()
	if len(keys) != rec.ChunkCount || len(keys) != 3 {
		t.Fatalf("signed %d chunk keys for chunk_count %d, want 3", len(keys), rec.ChunkCount)
	}
	for n, k := range keys {
		if !strings.HasSuffix(k.PartID, ".tar.gz") {
			t.Errorf("chunk %d key %q lacks the .tar.gz suffix", n, k.PartID)
		}
		want := resumechunks.ChunkObjectURI("acme", sid, rec.CheckpointID, uint32(n), rec.ChunkEncoding)
		if k.PartID != want.PartID {
			t.Errorf("chunk %d: signed key %q, resume derives %q", n, k.PartID, want.PartID)
		}
	}
}

// spec: 10.1 (Horizontal Scaling) — the declared encoding matches the
// producer. ArchiveTree, the checkpoint producer, emits a gzip stream, so
// tar.gz is the truthful declaration.
// diagnosis: a failure means ArchiveTree stopped gzipping; the gateway's
// fixed tar.gz declaration would then misstate the payload and must change
// with it.
func TestArchiveTreeProducesGzip_spec_10_1(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alice"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := workspace.ArchiveTree([]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: dir}}, &buf); err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}
	if b := buf.Bytes(); len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		t.Fatalf("ArchiveTree output does not start with the gzip magic bytes")
	}
}
