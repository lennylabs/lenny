// SPDX-License-Identifier: MIT

//go:build integration

// Shared harness for the tier-4 gateway checkpoint-driver integration
// tests (atomicity, quota-cap, and grant re-mint). It drives the real
// gateway checkpointer over a live gRPC bidirectional Checkpoint stream
// against a chunked producer and a prefix-capable in-memory object store,
// so the driver's grant/confirm/abort/finalise loop runs against real
// manifest-store, quota, presigner, object-store, and cataloging seams.

package tier4_integration_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const (
	cpTenant  = "acme"
	cpSession = "s1"
)

// cpChunkedAdapter is a §10.1 chunked producer: it sends a Probe, runs the
// ChunkReady → Grant → PUT → ChunkCommitted handshake per chunk against the
// gateway driver, and closes with a Summary. Hooks inject the behaviours
// the abort arms react to.
type cpChunkedAdapter struct {
	adapterv1.UnimplementedAdapterServer
	probeBytes int64
	chunkLens  []int64
	// putBytes[i] overrides the size the store records for chunk i (larger
	// than the declared length exercises the over-size confirm).
	putBytes map[int]int64
	// failAfter >= 0 terminates the stream with a CheckpointFailed frame
	// carrying failCode after chunk failAfter commits.
	failAfter int
	failCode  string
	// truncateAfter >= 0 closes the stream (EOF) after that many chunks
	// commit, without a Summary.
	truncateAfter int
	// stall, when true, blocks the stream after committing chunk stallAfter
	// until the stream context is cancelled, standing in for a pod that goes
	// silent so the gateway's own attempt deadline fires.
	stall      bool
	stallAfter int
	// remintFor, when non-nil, requests a fresh grant for the listed index
	// exactly once before committing it, standing in for a grant-expiry
	// retry. remintObserved records the grants the gateway minted per index.
	remintFor      map[uint32]bool
	remintObserved map[uint32]int
	grantLens      map[uint32][]int64

	store *cpStore
	mu    sync.Mutex
}

func (a *cpChunkedAdapter) grantCount(index uint32) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.remintObserved[index]
}

func (a *cpChunkedAdapter) grantLengths(index uint32) []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int64(nil), a.grantLens[index]...)
}

func (a *cpChunkedAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: a.probeBytes}},
	}); err != nil {
		return err
	}
	var total int64
	for i, ln := range a.chunkLens {
		idx := uint32(i)
		grant, aborted, err := a.declareAndAwait(stream, idx, ln)
		if err != nil {
			return err
		}
		if aborted {
			return nil
		}
		// A grant-expiry retry: re-declare the same index once and take the
		// fresh grant, without a second PUT or reservation on the gateway.
		if a.remintFor[idx] {
			a.remintFor[idx] = false
			fresh, ab, rerr := a.declareAndAwait(stream, idx, ln)
			if rerr != nil {
				return rerr
			}
			if ab {
				return nil
			}
			grant = fresh
		}
		wrote := ln
		if b, ok := a.putBytes[i]; ok {
			wrote = b
		}
		a.store.putFromGrant(grant, wrote)
		total += wrote
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkCommitted{ChunkCommitted: &adapterv1.ChunkCommitted{Index: idx}},
		}); err != nil {
			return err
		}
		if a.stall && i == a.stallAfter {
			// Go silent so the gateway's attempt deadline fires: block until
			// the gateway cancels the stream on its own timeout.
			<-stream.Context().Done()
			return stream.Context().Err()
		}
		if a.truncateAfter >= 0 && i >= a.truncateAfter {
			return nil
		}
		if a.failAfter >= 0 && i == a.failAfter {
			return stream.Send(&adapterv1.CheckpointServerMessage{
				Msg: &adapterv1.CheckpointServerMessage_Failed{Failed: &adapterv1.CheckpointFailed{
					Reason: "retry_exhausted", Index: idx, HttpStatus: 503, ErrorCode: a.failCode,
				}},
			})
		}
	}
	return stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Summary{Summary: &adapterv1.CheckpointSummary{
			ChunkCount: uint32(len(a.chunkLens)), TotalBytes: total,
		}},
	})
}

// declareAndAwait sends a ChunkReady and reads the gateway's response: the
// grant, or aborted when the gateway sent a CheckpointAbort.
func (a *cpChunkedAdapter) declareAndAwait(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage], index uint32, length int64) (*adapterv1.CheckpointGrant, bool, error) {
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: index, Length: length}},
	}); err != nil {
		return nil, false, err
	}
	msg, err := stream.Recv()
	if err != nil {
		return nil, false, err
	}
	if msg.GetAbort() != nil {
		return nil, true, nil
	}
	grant := msg.GetGrant()
	if grant == nil {
		return nil, false, status.Errorf(codes.Internal, "expected a grant for chunk %d", index)
	}
	a.mu.Lock()
	if a.remintObserved == nil {
		a.remintObserved = map[uint32]int{}
	}
	if a.grantLens == nil {
		a.grantLens = map[uint32][]int64{}
	}
	a.remintObserved[index]++
	a.grantLens[index] = append(a.grantLens[index], grant.GetContentLength())
	a.mu.Unlock()
	return grant, false, nil
}

// cpStore is a prefix-capable in-memory object store implementing the driver's
// ObjectStat and ChunkObjectStore seams. It records objects by their canonical
// object path so a prefix walk can enumerate and remove them, and records the
// prefixes ListByPrefix walked so a test asserts the abort / supersede release
// ran on the right prefix.
type cpStore struct {
	mu     sync.Mutex
	sizes  map[string]int64 // object path -> size
	urls   map[string]blobstore.URI
	uris   map[string]blobstore.URI // object path -> URI
	listed []string                 // object-path prefixes ListByPrefix walked
	// failDelete makes HardDeleteObject return an error without removing the
	// object, standing in for an unreachable MinIO during the abort sweep.
	failDelete bool
}

func newCPStore() *cpStore {
	return &cpStore{sizes: map[string]int64{}, urls: map[string]blobstore.URI{}, uris: map[string]blobstore.URI{}}
}

func objectPath(u blobstore.URI) string {
	return fmt.Sprintf("/%s/%s/%s/%s", u.TenantID, u.ObjectType, u.SessionID, u.PartID)
}

// registerGrant records the URI a presigned URL maps to so the adapter can
// resolve the object key from the grant it receives.
func (s *cpStore) registerGrant(url string, u blobstore.URI) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls[url] = u
}

func (s *cpStore) putFromGrant(grant *adapterv1.CheckpointGrant, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.urls[grant.GetUrl()]
	if !ok {
		return
	}
	s.sizes[objectPath(u)] = size
	s.uris[objectPath(u)] = u
}

// Stat implements checkpointer.ObjectStat.
func (s *cpStore) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.sizes[objectPath(u)]
	if !ok {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	return blobstore.BlobInfo{URI: u, Size: size}, nil
}

// count returns the number of live objects under prefix.
func (s *cpStore) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for key := range s.sizes {
		if strings.HasPrefix(key, prefix) {
			n++
		}
	}
	return n
}

// ListByPrefix implements checkpointer.ChunkObjectStore: it records the walked
// prefix and returns the objects under it. The prefix's object path (built from
// a checkpoint URI whose PartID is {checkpoint_id}/) equals the
// chunk_object_key_prefix the manifest row carries, so sweptPrefix can assert on
// that string.
func (s *cpStore) ListByPrefix(_ context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := objectPath(u)
	s.listed = append(s.listed, prefix)
	var out []blobstore.BlobInfo
	for key, uri := range s.uris {
		if strings.HasPrefix(key, prefix) {
			out = append(out, blobstore.BlobInfo{URI: uri, Size: s.sizes[key]})
		}
	}
	return out, nil
}

// HardDeleteObject implements checkpointer.ChunkObjectStore.
func (s *cpStore) HardDeleteObject(u blobstore.URI) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDelete {
		return fmt.Errorf("object store unreachable")
	}
	key := objectPath(u)
	delete(s.sizes, key)
	delete(s.uris, key)
	return nil
}

// cpDriverMetrics is a checkpointer.DriverMetrics capture recording the
// gateway-side checkpoint counter emissions a test asserts on. The abort-sweep
// tests read orphanedObjects, orphanedPool, and orphanedTrigger.
type cpDriverMetrics struct {
	mu              sync.Mutex
	orphanedObjects int
	orphanedPool    string
	orphanedTrigger string
}

func (m *cpDriverMetrics) IncCheckpointSizeExceeded(string, string)          {}
func (m *cpDriverMetrics) IncCheckpointStorageFailure(string, string, string) {}
func (m *cpDriverMetrics) IncCheckpointPartialManifestsSuperseded(string)     {}
func (m *cpDriverMetrics) IncCheckpointPartial(string, bool, string, checkpoint.Trigger) {
}
func (m *cpDriverMetrics) IncCheckpointKMSUnavailable() {}
func (m *cpDriverMetrics) IncCheckpointOrphanedObjects(pool, trigger string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanedObjects++
	m.orphanedPool = pool
	m.orphanedTrigger = trigger
}

func (m *cpDriverMetrics) orphanCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.orphanedObjects
}

func (s *cpStore) sweptPrefix(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.listed {
		if p == prefix {
			return true
		}
	}
	return false
}

// cpCatalog is the checkpointer.ChunkCatalogReleaser seam: it records the chunk
// artifact_store rows the abort / supersede release soft-deletes so a test can
// assert the confirmed chunks are released through the cataloging decorator
// rather than deleted around it.
type cpCatalog struct {
	mu      sync.Mutex
	deleted map[string]bool
}

func newCPCatalog() *cpCatalog { return &cpCatalog{deleted: map[string]bool{}} }

func (c *cpCatalog) SoftDeleteRow(_ context.Context, uri string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted[uri] = true
	return nil
}

// cpPresigner mints grant URLs, records each URL's URI on the store, and,
// on a non-enforcing store, signs any Content-Length without rejecting the
// oversize body at PUT time.
type cpPresigner struct {
	store *cpStore
}

func (p cpPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	url := fmt.Sprintf("https://obj.test/%s/%s?len=%d&exp=%d", u.SessionID, u.PartID, contentLength, time.Now().Add(ttl).UnixNano())
	p.store.registerGrant(url, u)
	return blobstore.Grant{URL: url, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (p cpPresigner) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "https://obj.test/get"}, nil
}

// cpDriverHarness wires the gateway checkpointer with the full seam set.
type cpDriverHarness struct {
	cp        *checkpointer.Checkpointer
	manifests *partialmanifeststore.MemoryStore
	quota     *storagequota.Memory
	store     *cpStore
	catalog   *cpCatalog
	metrics   *cpDriverMetrics
	sessions  sessionstore.Store
}

func newCPDriverHarness(t *testing.T, adapter *cpChunkedAdapter) *cpDriverHarness {
	t.Helper()
	store := newCPStore()
	adapter.store = store
	if adapter.putBytes == nil {
		adapter.putBytes = map[int]int64{}
	}
	if adapter.remintFor == nil {
		adapter.remintFor = map[uint32]bool{}
	}
	client := cpDialAdapter(t, adapter)

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: cpSession, TenantID: cpTenant, Adapter: client})
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: cpSession, TenantID: cpTenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	manifests := partialmanifeststore.NewMemoryStore(nil)
	quota := storagequota.NewMemory()
	catalog := newCPCatalog()
	metrics := &cpDriverMetrics{}
	cp := &checkpointer.Checkpointer{
		Sessions:      sessions,
		Registry:      registry,
		Manifests:     manifests,
		Quota:         quota,
		QuotaLimitFor: func(context.Context, string) (int64, error) { return 1 << 40, nil },
		Presigner:     cpPresigner{store: store},
		ObjectStore:   store,
		ChunkCatalog:  catalog,
		ChunkObjects:  store,
		DriverMetrics: metrics,
		Deadline:      5 * time.Second,
	}
	return &cpDriverHarness{cp: cp, manifests: manifests, quota: quota, store: store, catalog: catalog, metrics: metrics, sessions: sessions}
}

// latestManifest resolves the manifest row for the session's latest
// attempt: the active partial row on an abort, or the finalised row the
// session's recorded ref names on success.
func (h *cpDriverHarness) latestManifest(t *testing.T) partialmanifeststore.Record {
	t.Helper()
	rec, err := h.manifests.LatestActive(context.Background(), cpTenant, cpSession)
	if err == nil {
		return rec
	}
	row, gerr := h.sessions.Get(context.Background(), cpTenant, cpSession)
	if gerr == nil && row.WorkspaceSnapshot != nil && row.WorkspaceSnapshot.Ref != "" {
		r, e := h.manifests.Get(context.Background(), cpTenant, row.WorkspaceSnapshot.Ref)
		if e == nil {
			return r
		}
	}
	// A zero-chunk abort soft-deletes its row in the finalising transaction
	// (§10.1 line 132), so it is neither active nor referenced by a session
	// ref. Recover the tombstoned row so the test can assert its disposition.
	deleted, derr := h.manifests.ListSoftDeletedBefore(context.Background(), time.Now().Add(time.Hour))
	if derr == nil {
		var best partialmanifeststore.Record
		for _, r := range deleted {
			if r.TenantID == cpTenant && r.SessionID == cpSession && r.CreatedAt.After(best.CreatedAt) {
				best = r
			}
		}
		if best.CheckpointID != "" {
			return best
		}
	}
	t.Fatalf("no manifest row found for %s/%s", cpTenant, cpSession)
	return partialmanifeststore.Record{}
}

// cpDialAdapter serves srv over an in-memory connection and returns a
// connected adapter client.
func cpDialAdapter(t *testing.T, srv adapterv1.AdapterServer) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}
