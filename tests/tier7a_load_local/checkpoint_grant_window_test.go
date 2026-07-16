// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §10.1 gateway
// grant/confirm upload driver under the race detector. It pins the
// pipelining invariants the driver's asynchronous confirm and single-flight
// lock must hold:
//
//   - the resolved grant window bounds the grants the gateway keeps
//     outstanding by backpressure: a producer that pipelines its
//     declarations ahead of acks is paced, so the (window+1)th grant is
//     minted only after an earlier chunk's ack frees a window slot;
//   - a lagging Stat confirm never blocks the next grant (confirmation runs
//     off the grant's critical path);
//   - out-of-order chunk acknowledgements confirm each chunk exactly once;
//   - a periodic checkpoint and a concurrent seal of one (session, slot)
//     serialise on the single-flight lock rather than interleaving their
//     manifest writes.
//
// spec: §10.1 line 131 (fire-and-forget confirm, at-most-window grants
// outstanding), §5.2 (checkpointGrantWindow), §10.1 supersede-on-write
// (single-flight).

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
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
	gwTenant  = "acme"
	gwSession = "s1"
)

// gwStore is a prefix-capable in-memory object store with an optional Stat
// delay, implementing the driver's ObjectStat and PartialChunkDeleter seams.
type gwStore struct {
	mu        sync.Mutex
	sizes     map[string]int64
	urls      map[string]blobstore.URI
	statDelay time.Duration
}

func newGWStore() *gwStore {
	return &gwStore{sizes: map[string]int64{}, urls: map[string]blobstore.URI{}}
}

func gwObjectPath(u blobstore.URI) string {
	return fmt.Sprintf("/%s/%s/%s/%s", u.TenantID, u.ObjectType, u.SessionID, u.PartID)
}

func (s *gwStore) registerGrant(url string, u blobstore.URI) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls[url] = u
}

func (s *gwStore) putFromGrant(url string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.urls[url]; ok {
		s.sizes[gwObjectPath(u)] = size
	}
}

func (s *gwStore) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	if s.statDelay > 0 {
		time.Sleep(s.statDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.sizes[gwObjectPath(u)]
	if !ok {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	return blobstore.BlobInfo{URI: u, Size: size}, nil
}

func (s *gwStore) DeleteByPrefix(_ context.Context, prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k := range s.sizes {
		if strings.HasPrefix(k, prefix) {
			delete(s.sizes, k)
			n++
		}
	}
	return n, nil
}

// gwPresigner mints grant URLs and tracks the peak number of grants
// outstanding (minted minus acked) so a test can assert the window bound.
type gwPresigner struct {
	store   *gwStore
	minted  atomic.Int64
	current atomic.Int64
	peak    atomic.Int64
}

func (p *gwPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	url := fmt.Sprintf("https://obj.test/%s/%s?len=%d&n=%d", u.SessionID, u.PartID, contentLength, p.minted.Add(1))
	p.store.registerGrant(url, u)
	cur := p.current.Add(1)
	for {
		peak := p.peak.Load()
		if cur <= peak || p.peak.CompareAndSwap(peak, cur) {
			break
		}
	}
	return blobstore.Grant{URL: url, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (p *gwPresigner) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "https://obj.test/get"}, nil
}

func (p *gwPresigner) ackedOne() { p.current.Add(-1) }

// countingRecorder counts RecordPut calls per object path so a test can
// assert each chunk is catalogued exactly once.
type countingRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func newCountingRecorder() *countingRecorder { return &countingRecorder{calls: map[string]int{}} }

func (r *countingRecorder) RecordPut(u blobstore.URI, _ int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[gwObjectPath(u)]++
	return nil
}

func (r *countingRecorder) maxCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := 0
	for _, c := range r.calls {
		if c > m {
			m = c
		}
	}
	return m
}

// gwHarness wires the gateway checkpointer with the full seam set.
type gwHarness struct {
	cp        *checkpointer.Checkpointer
	presigner *gwPresigner
	recorder  *countingRecorder
	manifests *partialmanifeststore.MemoryStore
	store     *gwStore
	sessions  sessionstore.Store
}

// gwHarnessCfg configures the gateway checkpoint harness. window is the
// deployment-wide GrantWindow; poolRef and poolReader wire the per-pool
// checkpointGrantWindow override so a test can assert the driver resolves the
// session's own pool at attempt time.
type gwHarnessCfg struct {
	window     int
	statDelay  time.Duration
	deadline   time.Duration
	poolRef    string
	poolReader podsession.PoolPolicyReader
}

func newGWHarness(t *testing.T, adapter adapterv1.AdapterServer, window int, statDelay time.Duration) *gwHarness {
	return newGWHarnessCfg(t, adapter, gwHarnessCfg{window: window, statDelay: statDelay})
}

func newGWHarnessCfg(t *testing.T, adapter adapterv1.AdapterServer, cfg gwHarnessCfg) *gwHarness {
	t.Helper()
	store := newGWStore()
	store.statDelay = cfg.statDelay
	presigner := &gwPresigner{store: store}
	if s, ok := adapter.(*scriptAdapter); ok {
		s.store = store
		s.presigner = presigner
	}
	client := gwDial(t, adapter)

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: gwSession, TenantID: gwTenant, Adapter: client})
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: gwSession, TenantID: gwTenant, State: session.StateRunning, RuntimeRef: "echo",
		PoolRef: cfg.poolRef,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	manifests := partialmanifeststore.NewMemoryStore(nil)
	recorder := newCountingRecorder()
	deadline := cfg.deadline
	if deadline == 0 {
		deadline = 10 * time.Second
	}
	cp := &checkpointer.Checkpointer{
		Sessions:        sessions,
		Registry:        registry,
		Manifests:       manifests,
		Quota:           storagequota.NewMemory(),
		QuotaLimitFor:   func(context.Context, string) (int64, error) { return 1 << 40, nil },
		Presigner:       presigner,
		ObjectStore:     store,
		Cataloging:      recorder,
		ChunkDeleter:    store,
		GrantWindow:     cfg.window,
		PoolGrantWindow: cfg.poolReader,
		Deadline:        deadline,
	}
	return &gwHarness{cp: cp, presigner: presigner, recorder: recorder, manifests: manifests, store: store, sessions: sessions}
}

// scriptAdapter is a §10.1 chunked producer whose ChunkReady / commit
// ordering is scripted so a test can pipeline declarations ahead of acks or
// acknowledge chunks out of order.
type scriptAdapter struct {
	adapterv1.UnimplementedAdapterServer
	probeBytes int64
	chunkCount int
	chunkLen   int64
	// pipelineAhead declares every chunk before reading any grant, then paces
	// itself against the window: it receives grants up to the window bound,
	// then commits to free a slot for the next deferred grant. window is the
	// gateway's resolved grant window the producer paces against.
	// commitOrder overrides the ack order when set.
	pipelineAhead bool
	window        int
	commitOrder   []uint32
	// mintedAtFirstCommit records the number of grants the gateway had minted
	// at the moment the producer sent its first ack, so a test can assert the
	// gateway minted exactly `window` grants before any ack freed a slot (the
	// backpressure ordering: the (window+1)th grant is minted only after the
	// first is acked).
	mintedAtFirstCommit atomic.Int64

	store     *gwStore
	presigner *gwPresigner
}

func (a *scriptAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: a.probeBytes}},
	}); err != nil {
		return err
	}
	if a.pipelineAhead {
		return a.runPipelineAhead(stream)
	}
	return a.runOrdered(stream)
}

// runPipelineAhead declares every chunk up front, ahead of any ack, so the
// gateway must pace its grants against the window by backpressure rather than
// granting them all at once. It then slides the window forward: it receives
// grants while a window slot is free and commits the oldest un-acked chunk to
// free a slot for the next deferred grant, until every chunk is granted,
// uploaded, and committed. It captures the gateway's grant count at the first
// commit so a test can assert the (window+1)th grant was withheld until an
// ack freed a slot.
func (a *scriptAdapter) runPipelineAhead(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	for i := 0; i < a.chunkCount; i++ {
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: uint32(i), Length: a.chunkLen}},
		}); err != nil {
			return err
		}
	}
	received, committed := 0, 0
	for committed < a.chunkCount {
		if received < a.chunkCount && received-committed < a.window {
			// A window slot is free, so the gateway has a grant waiting.
			msg, err := stream.Recv()
			if err != nil {
				return err
			}
			grant := msg.GetGrant()
			if grant == nil {
				return fmt.Errorf("expected grant, got %T", msg.GetMsg())
			}
			a.store.putFromGrant(grant.GetUrl(), a.chunkLen)
			received++
			continue
		}
		// The window is full (or every chunk is granted): commit the oldest
		// un-acked chunk to free a slot. The first commit is the point at
		// which the gateway is allowed to mint the (window+1)th grant, so
		// capture the grant count just before it.
		if committed == 0 {
			a.mintedAtFirstCommit.Store(a.presigner.minted.Load())
		}
		a.presigner.ackedOne()
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkCommitted{ChunkCommitted: &adapterv1.ChunkCommitted{Index: uint32(committed)}},
		}); err != nil {
			return err
		}
		committed++
	}
	return stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Summary{Summary: &adapterv1.CheckpointSummary{
			ChunkCount: uint32(a.chunkCount), TotalBytes: a.chunkLen * int64(a.chunkCount),
		}},
	})
}

// runOrdered runs the ChunkReady → Grant → PUT loop. When commitOrder is
// unset it commits each chunk immediately after its PUT (the serial
// producer, so at most one grant is ever outstanding); when commitOrder is
// set it collects every grant first, then acks the chunks in that order to
// exercise out-of-order confirmation (the caller sizes the window to cover
// the chunk count).
func (a *scriptAdapter) runOrdered(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	declare := func(i int) error {
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: uint32(i), Length: a.chunkLen}},
		}); err != nil {
			return err
		}
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		grant := msg.GetGrant()
		if grant == nil {
			return fmt.Errorf("expected grant for chunk %d", i)
		}
		a.store.putFromGrant(grant.GetUrl(), a.chunkLen)
		return nil
	}
	commit := func(idx uint32) error {
		a.presigner.ackedOne()
		return stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkCommitted{ChunkCommitted: &adapterv1.ChunkCommitted{Index: idx}},
		})
	}
	if a.commitOrder == nil {
		for i := 0; i < a.chunkCount; i++ {
			if err := declare(i); err != nil {
				return err
			}
			if err := commit(uint32(i)); err != nil {
				return err
			}
		}
	} else {
		for i := 0; i < a.chunkCount; i++ {
			if err := declare(i); err != nil {
				return err
			}
		}
		for _, idx := range a.commitOrder {
			if err := commit(idx); err != nil {
				return err
			}
		}
	}
	return stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Summary{Summary: &adapterv1.CheckpointSummary{
			ChunkCount: uint32(a.chunkCount), TotalBytes: a.chunkLen * int64(a.chunkCount),
		}},
	})
}

func gwDial(t *testing.T, srv adapterv1.AdapterServer) *adapterclient.Client {
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

// spec: §5.2 / §10.1 line 131 — the resolved grant window bounds outstanding
// grants by backpressure: a producer that pipelines every declaration ahead
// of any ack is paced, not aborted. The gateway keeps at most `window` grants
// outstanding, mints the (window+1)th grant only after the first chunk is
// acked, and the attempt completes with every chunk granted and confirmed.
// diagnosis: a failure here (Checkpoint returns an error, more than `window`
// grants minted before the first ack, or peak outstanding above the window)
// means the driver aborts a pipelining producer with stream_truncated instead
// of pacing it, so a valid checkpoint whose adapter declares ahead of acks is
// failed rather than backpressured.
func TestGrantWindowBoundsOutstandingGrants(t *testing.T) {
	const window = 3
	const chunks = window + 3
	adapter := &scriptAdapter{probeBytes: 100, chunkCount: chunks, chunkLen: 10, pipelineAhead: true, window: window}
	h := newGWHarness(t, adapter, window, 0)

	if err := h.cp.Checkpoint(context.Background(), gwTenant, gwSession); err != nil {
		t.Fatalf("Checkpoint on a pipelining producer, want the window to pace it: %v", err)
	}
	if got := adapter.mintedAtFirstCommit.Load(); got != int64(window) {
		t.Errorf("grants minted before the first ack = %d, want exactly the window %d (the (window+1)th grant is deferred until an ack frees a slot)", got, window)
	}
	if got := h.presigner.peak.Load(); got > int64(window) {
		t.Errorf("peak outstanding grants = %d, want <= window %d", got, window)
	}
	if got := h.presigner.minted.Load(); got != int64(chunks) {
		t.Errorf("total grants minted = %d, want %d (every chunk granted once the pipeline drained)", got, chunks)
	}
	rec := manifestOf(t, h)
	if rec.ChunkCount != chunks {
		t.Errorf("chunk_count = %d, want %d", rec.ChunkCount, chunks)
	}
}

// gwPoolReader is a podsession.PoolPolicyReader that returns a fixed
// per-pool checkpointGrantWindow override for one named pool.
type gwPoolReader struct {
	pool   string
	window int32
}

func (r gwPoolReader) PoolPolicy(_ context.Context, name string) (podsession.PoolPolicyMirror, bool, error) {
	if name != r.pool {
		return podsession.PoolPolicyMirror{}, false, nil
	}
	w := r.window
	return podsession.PoolPolicyMirror{CheckpointGrantWindow: &w}, true, nil
}

// spec: §5.2 — the driver resolves the bound session's own pool at attempt
// time and applies that pool's checkpointGrantWindow override, not the
// static Checkpointer.Pool label. A session whose pool overrides the window
// below the deployment-wide default is paced by the override: the gateway
// keeps at most poolWindow grants outstanding and mints the (poolWindow+1)th
// only after the first ack.
// diagnosis: a failure here (peak outstanding above the pool override, or more
// than the pool override worth of grants minted before the first ack) means
// the driver resolved the window against an empty pool and fell back to the
// deployment default, so the wired per-pool override never takes effect in
// production.
func TestResolvedPoolWindowOverridesDeploymentDefault(t *testing.T) {
	const poolWindow = 2
	const deploymentWindow = 8
	const chunks = poolWindow + 3
	adapter := &scriptAdapter{probeBytes: 100, chunkCount: chunks, chunkLen: 10, pipelineAhead: true, window: poolWindow}
	h := newGWHarnessCfg(t, adapter, gwHarnessCfg{
		window:     deploymentWindow,
		deadline:   2 * time.Second,
		poolRef:    "cp-pool",
		poolReader: gwPoolReader{pool: "cp-pool", window: poolWindow},
	})

	if err := h.cp.Checkpoint(context.Background(), gwTenant, gwSession); err != nil {
		t.Fatalf("Checkpoint under a pool-resolved window, want it to pace the producer: %v", err)
	}
	if got := adapter.mintedAtFirstCommit.Load(); got != int64(poolWindow) {
		t.Errorf("grants minted before the first ack = %d, want the pool override %d, not the deployment default %d", got, poolWindow, deploymentWindow)
	}
	if got := h.presigner.peak.Load(); got > int64(poolWindow) {
		t.Errorf("peak outstanding grants = %d, want <= pool override %d", got, poolWindow)
	}
	if got := h.presigner.minted.Load(); got != int64(chunks) {
		t.Errorf("total grants minted = %d, want %d once the pipeline drained", got, chunks)
	}
}

// spec: §10.1 line 131 — a lagging Stat confirm never blocks the next
// grant: the serial producer completes within the deadline even though
// every confirm sleeps, because the ack (not the Stat) releases the window.
func TestLaggingStatDoesNotBlockNextGrant(t *testing.T) {
	adapter := &scriptAdapter{probeBytes: 50, chunkCount: 5, chunkLen: 10}
	h := newGWHarness(t, adapter, 2, 40*time.Millisecond)

	start := time.Now()
	if err := h.cp.Checkpoint(context.Background(), gwTenant, gwSession); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	elapsed := time.Since(start)
	// Five serialised Stats would cost >= 200 ms; because the confirms run
	// off the grant path, the grant/commit loop is not serialised behind
	// them and the whole attempt finishes well under that.
	if elapsed > 180*time.Millisecond {
		t.Errorf("checkpoint took %v; the lagging Stat appears to gate grants", elapsed)
	}
	rec := manifestOf(t, h)
	if rec.ChunkCount != 5 {
		t.Errorf("chunk_count = %d, want 5", rec.ChunkCount)
	}
}

// spec: §10.1 line 131 — out-of-order chunk acknowledgements confirm each
// chunk exactly once under the monotonic counter and the per-key catalog
// insert.
func TestOutOfOrderAcksConfirmExactlyOnce(t *testing.T) {
	adapter := &scriptAdapter{probeBytes: 40, chunkCount: 4, chunkLen: 10, commitOrder: []uint32{3, 1, 0, 2}}
	h := newGWHarness(t, adapter, 4, 0)

	if err := h.cp.Checkpoint(context.Background(), gwTenant, gwSession); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if got := h.recorder.maxCalls(); got != 1 {
		t.Errorf("max RecordPut calls per chunk = %d, want 1 (exactly-once confirm)", got)
	}
	rec := manifestOf(t, h)
	if rec.ChunkCount != 4 {
		t.Errorf("chunk_count = %d, want 4", rec.ChunkCount)
	}
}

// spec: §10.1 supersede-on-write — a periodic checkpoint and a concurrent
// seal of one (session, slot) serialise on the single-flight lock; under
// the race detector neither corrupts the other's manifest writes and both
// complete.
func TestPeriodicVsSealSerialiseOnSingleFlight(t *testing.T) {
	adapter := &scriptAdapter{probeBytes: 30, chunkCount: 3, chunkLen: 10}
	h := newGWHarness(t, adapter, 4, 0)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = h.cp.Checkpoint(context.Background(), gwTenant, gwSession) }()
	go func() { defer wg.Done(); errs[1] = h.cp.Seal(context.Background(), gwTenant, gwSession) }()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent op %d failed: %v", i, err)
		}
	}
	// The single-flight lock kept the two attempts from interleaving, so the
	// session ends with a coherent completed snapshot.
	row, err := h.sessions.Get(context.Background(), gwTenant, gwSession)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		t.Fatal("no coherent WorkspaceSnapshot recorded after concurrent checkpoint+seal")
	}
}

// manifestOf resolves the manifest row for the session's latest attempt.
func manifestOf(t *testing.T, h *gwHarness) partialmanifeststore.Record {
	t.Helper()
	row, err := h.sessions.Get(context.Background(), gwTenant, gwSession)
	if err == nil && row.WorkspaceSnapshot != nil && row.WorkspaceSnapshot.Ref != "" {
		if r, e := h.manifests.Get(context.Background(), gwTenant, row.WorkspaceSnapshot.Ref); e == nil {
			return r
		}
	}
	rec, err := h.manifests.LatestActive(context.Background(), gwTenant, gwSession)
	if err != nil {
		t.Fatalf("no manifest row for %s/%s: %v", gwTenant, gwSession, err)
	}
	return rec
}
