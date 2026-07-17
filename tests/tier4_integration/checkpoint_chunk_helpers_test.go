// SPDX-License-Identifier: MIT

//go:build integration

// Shared helpers for the tier-4 §10.1 line 155 reassembly-on-resume tests
// that drive the gateway's chunk resolver, workspace-download stream, and
// derive chunk-copy against a live MinIO container. A checkpoint's chunks
// are written as real objects under the §10.1 chunk-object layout
// `/{tenant}/checkpoints/{session}/{checkpoint_id}/chunk-{n}.{enc}`, and a
// checkpoint_manifest row records the chunk_count / chunk_encoding the
// resolver reads.

package tier4_integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// newLiveChunkStore starts a MinIO container and returns a miniostore.Store
// bound to it, plus the container handle. The tenant is T3 (no SSE-KMS), so
// PUT / GET / Copy / presign all work without a KMS key.
func newLiveChunkStore(t *testing.T) *miniostore.Store {
	t.Helper()
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	store, err := miniostore.New(miniostore.Config{
		Endpoint:  mio.Endpoint,
		AccessKey: mio.AccessKey,
		SecretKey: mio.SecretKey,
		Bucket:    mio.Bucket,
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	return store
}

// putChunk writes one chunk object under the checkpoint's chunk prefix at
// the canonical zero-padded key, returning the bytes written.
func putChunk(t *testing.T, store *miniostore.Store, tenant, session, checkpointID string, index uint32, encoding partialmanifeststore.ChunkEncoding, data []byte) {
	t.Helper()
	u := resumechunks.ChunkObjectURI(tenant, session, checkpointID, index, encoding)
	u.TTL = time.Hour
	if _, err := store.Put(u, "application/octet-stream", bytes.NewReader(data)); err != nil {
		t.Fatalf("put chunk %d: %v", index, err)
	}
}

// putRawChunk writes a stray non-zero-padded chunk key (chunk-{n}.{enc}),
// used to construct the out-of-order contiguity arm: it parses to an index
// below chunk_count but sorts after the zero-padded keys.
func putRawChunk(t *testing.T, store *miniostore.Store, tenant, session, checkpointID string, index int, encoding partialmanifeststore.ChunkEncoding, data []byte) {
	t.Helper()
	u := blobstore.URI{
		TenantID:   tenant,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  session,
		PartID:     fmt.Sprintf("%s/chunk-%d.%s", checkpointID, index, encoding),
		TTL:        time.Hour,
	}
	if _, err := store.Put(u, "application/octet-stream", bytes.NewReader(data)); err != nil {
		t.Fatalf("put raw chunk %d: %v", index, err)
	}
}

// seedCheckpoint writes chunks and records the manifest row. The chunk
// bodies are deterministic per index so a reassembled stream can be
// compared byte-for-byte with the source. It returns the concatenated
// archive bytes (the whole workspace archive the chunks slice).
func seedCheckpoint(t *testing.T, store *miniostore.Store, mstore *partialmanifeststore.MemoryStore, tenant, session, checkpointID string, chunkLens []int, encoding partialmanifeststore.ChunkEncoding) []byte {
	t.Helper()
	var archive []byte
	var total int64
	for i, ln := range chunkLens {
		body := chunkBody(checkpointID, i, ln)
		putChunk(t, store, tenant, session, checkpointID, uint32(i), encoding, body)
		archive = append(archive, body...)
		total += int64(ln)
	}
	seedCompleteManifest(t, mstore, tenant, session, checkpointID, len(chunkLens), total, encoding)
	return archive
}

// chunkBody builds a deterministic chunk payload for (checkpointID, index).
func chunkBody(checkpointID string, index, length int) []byte {
	body := make([]byte, length)
	seed := byte(len(checkpointID) + index)
	for i := range body {
		body[i] = seed + byte(i)
	}
	return body
}

// seedCompleteManifest records a complete (partial=false) manifest row via
// the store's intent-row model (Put → ConfirmChunk → Finalise).
func seedCompleteManifest(t *testing.T, mstore *partialmanifeststore.MemoryStore, tenant, session, checkpointID string, chunkCount int, totalBytes int64, encoding partialmanifeststore.ChunkEncoding) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	rec := partialmanifeststore.Record{
		TenantID:             tenant,
		CheckpointID:         checkpointID,
		SessionID:            session,
		SlotID:               partialmanifeststore.SlotDefault,
		ChunkObjectKeyPrefix: fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, session, checkpointID),
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        encoding,
		CheckpointStartedAt:  now,
		CheckpointTimeoutAt:  now.Add(90 * time.Second),
	}
	if err := mstore.Put(ctx, rec); err != nil {
		t.Fatalf("manifest Put: %v", err)
	}
	if chunkCount > 0 {
		if err := mstore.ConfirmChunk(ctx, tenant, checkpointID, chunkCount-1, totalBytes); err != nil {
			t.Fatalf("manifest ConfirmChunk: %v", err)
		}
	}
	if err := mstore.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("manifest Finalise: %v", err)
	}
}

// resumechunkPrefixURI names the chunk-object prefix under which a
// checkpoint's chunks live, for a ListByPrefix enumeration.
func resumechunkPrefixURI(tenant, session, checkpointID string) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenant,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  session,
		PartID:     checkpointID + "/",
	}
}

// seedPartialManifest records a partial (drain-attempt) manifest row with a
// non-NULL baseline_full_checkpoint_bytes: an intent row advanced by
// ConfirmChunk and left partial (no Finalise), so the resolver applies the
// §10.1 recovery-threshold gate.
func seedPartialManifest(t *testing.T, mstore *partialmanifeststore.MemoryStore, tenant, session, checkpointID string, chunkCount int, uploaded, baseline int64, encoding partialmanifeststore.ChunkEncoding) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	rec := partialmanifeststore.Record{
		TenantID:                    tenant,
		CheckpointID:                checkpointID,
		SessionID:                   session,
		SlotID:                      partialmanifeststore.SlotDefault,
		ChunkObjectKeyPrefix:        fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, session, checkpointID),
		ChunkSizeBytes:              16 << 20,
		ChunkEncoding:               encoding,
		BaselineFullCheckpointBytes: &baseline,
		CheckpointStartedAt:         now,
		CheckpointTimeoutAt:         now.Add(90 * time.Second),
	}
	if err := mstore.Put(ctx, rec); err != nil {
		t.Fatalf("partial manifest Put: %v", err)
	}
	if err := mstore.ConfirmChunk(ctx, tenant, checkpointID, chunkCount-1, uploaded); err != nil {
		t.Fatalf("partial manifest ConfirmChunk: %v", err)
	}
}

// chunkResolver builds the gateway resume-chunk resolver over the live
// MinIO store (real Presigner + real prefix Lister) and the memory manifest
// store, with the default §10.1 partialRecoveryThresholdFraction of 0.5.
func chunkResolver(store *miniostore.Store, mstore *partialmanifeststore.MemoryStore) *resumechunks.Resolver {
	return &resumechunks.Resolver{
		Manifests:                        mstore,
		Presigner:                        store,
		Lister:                           store,
		TTL:                              time.Minute,
		PartialRecoveryThresholdFraction: 0.5,
	}
}

// newChunkGateway builds a sessionserver.Handler wired with the live MinIO
// blob store and the memory manifest store, seeded with rows. idFn stamps
// the derived session id on the derive path. It returns the handler and the
// session store so a test can inspect a derived row.
func newChunkGateway(t *testing.T, store *miniostore.Store, mstore *partialmanifeststore.MemoryStore, idFn func() string, rows ...sessionstore.Session) (http.Handler, sessionstore.Store) {
	t.Helper()
	sessions := memstore.New()
	for _, r := range rows {
		if err := sessions.Create(context.Background(), r); err != nil {
			t.Fatalf("seed session %s: %v", r.ID, err)
		}
	}
	srv := sessionserver.New(sessions, sessionserver.Options{
		Blobs:                    store,
		CheckpointManifestReader: mstore,
		CheckpointManifestWriter: mstore,
		IDFunc:                   idFn,
		Clock:                    func() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) },
	})
	return srv.Handler(), sessions
}

// getWorkspace issues GET /v1/sessions/{id}/workspace against the gateway
// handler and returns the recorder.
func getWorkspace(t *testing.T, h http.Handler, tenant, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+session+"/workspace", nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// httpGet fetches url replaying headers verbatim and returns the body.
func httpGet(t *testing.T, url string, headers map[string]string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	return body
}
