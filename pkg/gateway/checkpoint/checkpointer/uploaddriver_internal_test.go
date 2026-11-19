// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint/partialmanifeststore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §10.1.7 — the chunk object basename is
// chunk-{n}.{chunk_encoding} with {n} a zero-padded 5-digit monotonic index
// starting at 00000, so the signed key matches the reassembly layout the
// resume path enumerates and lexicographic list order tracks index order.
// diagnosis: a failure here (chunk-0.tar.gz rather than chunk-00000.tar.gz) means
// the driver signs PUT grants whose keys diverge from the spec chunk-object
// layout, so ListObjectsV2 order stops tracking index order (chunk-10 sorts
// before chunk-2) and the reassembly path cannot find the objects.
func TestChunkURIZeroPadsIndex_spec_10_1(t *testing.T) {
	d := &uploadDriver{
		c:            &Checkpointer{},
		tenantID:     "acme",
		sessionID:    "s1",
		checkpointID: "cp1",
	}
	cases := map[uint32]string{
		0:     "cp1/chunk-00000.tar.gz",
		1:     "cp1/chunk-00001.tar.gz",
		42:    "cp1/chunk-00042.tar.gz",
		12345: "cp1/chunk-12345.tar.gz",
	}
	for idx, want := range cases {
		if got := d.chunkURI(idx).PartID; got != want {
			t.Errorf("chunkURI(%d).PartID = %q, want %q", idx, got, want)
		}
	}
}

// errNoChunk is the Stat error gatedStat returns for an index it holds no
// size for.
var errNoChunk = errors.New("gatedStat: no such chunk")

// gatedStat is an ObjectStat seam that reports a fixed size per chunk object
// and holds the Stat of every index in hold until its channel is closed, so a
// test can force a higher index's confirm to finish before a lower one's.
// entered[idx] is closed when the Stat of idx starts.
type gatedStat struct {
	driver  *uploadDriver
	sizes   map[uint32]int64
	hold    map[uint32]chan struct{}
	entered map[uint32]chan struct{}
}

func (g *gatedStat) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	for idx, size := range g.sizes {
		if g.driver.chunkURI(idx).PartID != u.PartID {
			continue
		}
		if ch, ok := g.entered[idx]; ok {
			close(ch)
		}
		if ch, ok := g.hold[idx]; ok {
			<-ch
		}
		return blobstore.BlobInfo{URI: u, Size: size}, nil
	}
	return blobstore.BlobInfo{}, errNoChunk
}

// confirmHarness builds an upload driver over an in-memory manifest store
// holding this attempt's intent row, and wires stat as its Stat seam.
func confirmHarness(t *testing.T, stat *gatedStat) (*uploadDriver, *partialmanifeststore.MemoryStore) {
	t.Helper()
	store := partialmanifeststore.NewMemoryStore(nil)
	ctx := context.Background()
	if err := store.Put(ctx, partialmanifeststore.Record{
		TenantID:             "acme",
		CheckpointID:         "cp1",
		SessionID:            "s1",
		ChunkObjectKeyPrefix: "/acme/checkpoints/s1/cp1/",
		ReservedBytes:        100,
	}); err != nil {
		t.Fatalf("seed intent row: %v", err)
	}
	d := &uploadDriver{
		c:            &Checkpointer{Manifests: store, ObjectStore: stat},
		ctx:          ctx,
		tenantID:     "acme",
		sessionID:    "s1",
		checkpointID: "cp1",
		prefix:       "/acme/checkpoints/s1/cp1/",
		signedLen:    map[uint32]int64{},
		acked:        map[uint32]bool{},
	}
	stat.driver = d
	return d, store
}

// confirm runs one chunk's confirm synchronously, as the per-ack goroutine
// would.
func confirm(d *uploadDriver, idx uint32) {
	d.confirmWG.Add(1)
	d.confirmChunk(idx, 0)
}

func manifestRow(t *testing.T, store *partialmanifeststore.MemoryStore) partialmanifeststore.Record {
	t.Helper()
	rec, err := store.Get(context.Background(), "acme", "cp1")
	if err != nil {
		t.Fatalf("get manifest row: %v", err)
	}
	return rec
}

// spec: §10.1.7 (preStop Hook Drain), §11.2 (Budgets and Quotas)
// The persisted chunk_count and workspace_bytes_uploaded describe the same
// chunks when chunk 1's confirm finishes before chunk 0's: the row stays at
// zero while only chunk 1 is confirmed, and reaches two chunks with both
// chunks' bytes once chunk 0 confirms, rather than pairing two chunks with
// chunk 1's bytes alone.
// diagnosis: a failure here means the confirm path persists a total that
// depends on goroutine order, so the durable workspace_bytes_uploaded is
// under-reported and reservation reconciliation and the resume threshold read
// the wrong byte count.
func TestConfirmOutOfOrderPersistsCountAndBytesOfTheSameChunks(t *testing.T) {
	stat := &gatedStat{
		sizes:   map[uint32]int64{0: 7, 1: 11},
		hold:    map[uint32]chan struct{}{0: make(chan struct{})},
		entered: map[uint32]chan struct{}{0: make(chan struct{})},
	}
	d, store := confirmHarness(t, stat)

	d.confirmWG.Add(1)
	go d.confirmChunk(0, 7)
	<-stat.entered[0]

	d.confirmWG.Add(1)
	d.confirmChunk(1, 11)
	if got := manifestRow(t, store); got.ChunkCount != 0 || got.WorkspaceBytesUploaded != 0 {
		t.Fatalf("with only chunk 1 confirmed: chunk_count=%d bytes=%d, want 0/0", got.ChunkCount, got.WorkspaceBytesUploaded)
	}

	close(stat.hold[0])
	d.confirmWG.Wait()
	if got := manifestRow(t, store); got.ChunkCount != 2 || got.WorkspaceBytesUploaded != 18 {
		t.Errorf("after both confirms: chunk_count=%d bytes=%d, want 2/18", got.ChunkCount, got.WorkspaceBytesUploaded)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.confirmedBytes != 18 || d.confirmedCount != 2 {
		t.Errorf("in-memory confirmed total = %d bytes / %d chunks, want 18/2", d.confirmedBytes, d.confirmedCount)
	}
}

// spec: §10.1.7 (preStop Hook Drain), §11.2 (Budgets and Quotas)
// Concurrent confirms released together, in any completion order, leave the
// row at the full chunk count paired with the full byte sum.
// diagnosis: a failure here means concurrent confirms can leave the durable
// row with a chunk_count and a workspace_bytes_uploaded from different sets of
// chunks.
func TestConcurrentConfirmsPersistTheFullCountAndSum(t *testing.T) {
	const chunks = 16
	sizes := map[uint32]int64{}
	var want int64
	for i := uint32(0); i < chunks; i++ {
		sizes[i] = int64(i + 1)
		want += int64(i + 1)
	}
	d, store := confirmHarness(t, &gatedStat{sizes: sizes})
	var start sync.WaitGroup
	start.Add(1)
	for i := uint32(chunks); i > 0; i-- {
		d.confirmWG.Add(1)
		go func(idx uint32) {
			start.Wait()
			d.confirmChunk(idx, sizes[idx])
		}(i - 1)
	}
	start.Done()
	d.confirmWG.Wait()
	if got := manifestRow(t, store); got.ChunkCount != chunks || got.WorkspaceBytesUploaded != want {
		t.Errorf("chunk_count=%d bytes=%d, want %d/%d", got.ChunkCount, got.WorkspaceBytesUploaded, chunks, want)
	}
}

// spec: §10.1.7 (preStop Hook Drain), §11.2 (Budgets and Quotas)
// A confirmed chunk beyond a missing index is not counted in the row until the
// missing index confirms, and a repeat confirm of a chunk adds nothing.
// diagnosis: a failure here means the row counts a chunk past a gap (so the
// resume path would find an index missing below chunk_count) or counts a
// re-confirmed chunk twice.
func TestConfirmCountsOnlyTheContiguousPrefixOnce(t *testing.T) {
	d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{0: 5, 1: 6, 2: 7}})
	confirm(d, 0)
	confirm(d, 2)
	if got := manifestRow(t, store); got.ChunkCount != 1 || got.WorkspaceBytesUploaded != 5 {
		t.Fatalf("with chunks 0 and 2 confirmed: chunk_count=%d bytes=%d, want 1/5", got.ChunkCount, got.WorkspaceBytesUploaded)
	}
	confirm(d, 2)
	confirm(d, 1)
	confirm(d, 0)
	if got := manifestRow(t, store); got.ChunkCount != 3 || got.WorkspaceBytesUploaded != 18 {
		t.Errorf("after the gap fills: chunk_count=%d bytes=%d, want 3/18", got.ChunkCount, got.WorkspaceBytesUploaded)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.confirmedBytes != 18 || d.confirmedCount != 3 {
		t.Errorf("in-memory confirmed total = %d bytes / %d chunks, want 18/3", d.confirmedBytes, d.confirmedCount)
	}
}

// spec: §4.4.2 (Checkpoint Atomicity and Partial Manifests), §10.1.7 (preStop Hook Drain)
// A terminal summary whose chunk count and byte total match the confirmed
// chunks still finalises partial when the confirmed indices leave a gap,
// because the row records only the contiguous run and the resume path cannot
// reassemble across the gap.
// diagnosis: a failure here means a checkpoint with a missing chunk index is
// finalised complete with a row that omits confirmed chunks.
func TestSummaryWithAGapInConfirmedIndicesFinalisesPartial(t *testing.T) {
	d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{0: 5, 2: 7}})
	confirm(d, 0)
	confirm(d, 2)
	if _, _, err := d.onSummary(&adapterv1.CheckpointSummary{ChunkCount: 2, TotalBytes: 12}); err == nil {
		t.Fatal("onSummary finalised complete with a gap in the confirmed indices")
	}
	got := manifestRow(t, store)
	if !got.Partial || got.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("row partial=%v reason=%q, want partial stream_truncated", got.Partial, got.ManifestReason)
	}
}

// spec: §4.4.2 (Checkpoint Atomicity and Partial Manifests), §10.1.7 (preStop Hook Drain)
// A terminal summary over a contiguous confirmed run finalises complete with
// the row's count and bytes equal to the summary, whatever order the chunks
// confirmed in.
// diagnosis: a failure here means the contiguity check refuses a well-formed
// checkpoint, or the completed row does not carry the confirmed totals.
func TestSummaryOverContiguousConfirmsFinalisesComplete(t *testing.T) {
	d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{0: 5, 1: 7}})
	confirm(d, 1)
	confirm(d, 0)
	id, total, err := d.onSummary(&adapterv1.CheckpointSummary{ChunkCount: 2, TotalBytes: 12})
	if err != nil || id != "cp1" || total != 12 {
		t.Fatalf("onSummary = (%q, %d, %v), want (cp1, 12, nil)", id, total, err)
	}
	got := manifestRow(t, store)
	if got.Partial || got.ChunkCount != 2 || got.WorkspaceBytesUploaded != 12 {
		t.Errorf("row partial=%v chunk_count=%d bytes=%d, want complete 2/12", got.Partial, got.ChunkCount, got.WorkspaceBytesUploaded)
	}
}

// discardStream is a checkpoint stream whose Send drops every frame, so a
// confirm that latches an abort can send the adapter its CheckpointAbort in a
// test with no adapter on the other end. Every other method is unused.
type discardStream struct {
	adapterv1.Adapter_CheckpointClient
}

func (discardStream) Send(*adapterv1.CheckpointRequest) error { return nil }

// adjustRecorder is a StorageQuota that records every Adjust delta.
type adjustRecorder struct {
	mu     sync.Mutex
	deltas []int64
}

func (q *adjustRecorder) Reserve(context.Context, string, int64, int64) (int64, error) {
	return 0, nil
}

func (q *adjustRecorder) Adjust(_ context.Context, _ string, delta int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deltas = append(q.deltas, delta)
	return nil
}

// sweepSpy is the chunk catalog and object store an abort sweep runs
// against. It holds one object per seeded chunk index under the attempt's
// prefix and records the catalog rows it soft-deletes.
type sweepSpy struct {
	mu          sync.Mutex
	objects     []blobstore.URI
	softDeleted []string
}

func (s *sweepSpy) seed(d *uploadDriver, indices ...uint32) {
	for _, idx := range indices {
		s.objects = append(s.objects, d.chunkURI(idx))
	}
}

func (s *sweepSpy) ListByPrefix(_ context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []blobstore.BlobInfo
	for _, o := range s.objects {
		if o.TenantID == u.TenantID && o.SessionID == u.SessionID && strings.HasPrefix(o.PartID, u.PartID) {
			out = append(out, blobstore.BlobInfo{URI: o})
		}
	}
	return out, nil
}

func (s *sweepSpy) HardDeleteObject(u blobstore.URI) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.objects[:0]
	for _, o := range s.objects {
		if o.PartID != u.PartID {
			kept = append(kept, o)
		}
	}
	s.objects = kept
	return nil
}

func (s *sweepSpy) SoftDeleteRow(_ context.Context, uri string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.softDeleted = append(s.softDeleted, uri)
	return nil
}

func (s *sweepSpy) state() (remaining, softDeleted int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects), len(s.softDeleted)
}

// flakyConfirm is a manifest store whose first failConfirms ConfirmChunk
// calls fail, standing in for a lost counter write.
type flakyConfirm struct {
	*partialmanifeststore.MemoryStore
	mu           sync.Mutex
	failConfirms int
}

func (f *flakyConfirm) ConfirmChunk(ctx context.Context, tenantID, checkpointID string, n int, uploaded int64) error {
	f.mu.Lock()
	fail := f.failConfirms > 0
	if fail {
		f.failConfirms--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("flakyConfirm: write lost")
	}
	return f.MemoryStore.ConfirmChunk(ctx, tenantID, checkpointID, n, uploaded)
}

// spec: §10.1.7 (preStop Hook Drain), §4.4.2 (Checkpoint Atomicity and Partial Manifests), §12.5 (Artifact Store)
// A deadline fire whose confirmed run is empty, although chunks 1 and 2
// confirmed, sweeps those chunks, because Finalise soft-deletes the
// zero-chunk row and no resume or backstop would reclaim them. A deadline fire
// with a non-empty run keeps its chunks for the resume path.
// diagnosis: a failure here means a timed-out attempt whose chunk 0 never
// confirmed leaves its later chunks in the object store with live catalog rows
// charged to the tenant and no row that selects them, or that a resumable
// timeout row lost its chunks.
func TestTimeoutWithEmptyConfirmedRunSweepsConfirmedChunks(t *testing.T) {
	t.Run("empty run sweeps", func(t *testing.T) {
		d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{1: 6, 2: 7}})
		spy := &sweepSpy{}
		spy.seed(d, 1, 2)
		d.c.ChunkCatalog, d.c.ChunkObjects = spy, spy
		confirm(d, 1)
		confirm(d, 2)
		if err := d.finaliseAbort(partialmanifeststore.ReasonTimeout); err != nil {
			t.Fatalf("finaliseAbort: %v", err)
		}
		if remaining, softDeleted := spy.state(); remaining != 0 || softDeleted != 2 {
			t.Errorf("after the sweep: %d object(s) remain and %d catalog row(s) soft-deleted, want 0 and 2", remaining, softDeleted)
		}
		if got := manifestRow(t, store); got.DeletedAt.IsZero() || got.ManifestReason != partialmanifeststore.ReasonTimeout {
			t.Errorf("row deleted_at=%v reason=%q, want soft-deleted timeout", got.DeletedAt, got.ManifestReason)
		}
	})
	t.Run("non-empty run is retained", func(t *testing.T) {
		d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{0: 5, 2: 7}})
		spy := &sweepSpy{}
		spy.seed(d, 0, 2)
		d.c.ChunkCatalog, d.c.ChunkObjects = spy, spy
		confirm(d, 0)
		confirm(d, 2)
		if err := d.finaliseAbort(partialmanifeststore.ReasonTimeout); err != nil {
			t.Fatalf("finaliseAbort: %v", err)
		}
		if remaining, softDeleted := spy.state(); remaining != 2 || softDeleted != 0 {
			t.Errorf("%d object(s) remain and %d catalog row(s) soft-deleted, want both chunks retained for the resume path", remaining, softDeleted)
		}
		if got := manifestRow(t, store); !got.DeletedAt.IsZero() || got.ChunkCount != 1 || got.WorkspaceBytesUploaded != 5 {
			t.Errorf("row deleted_at=%v chunk_count=%d bytes=%d, want active 1/5", got.DeletedAt, got.ChunkCount, got.WorkspaceBytesUploaded)
		}
	})
}

// spec: §11.2 (Budgets and Quotas), §10.1.7 (preStop Hook Drain)
// When chunk 0's Stat fails and chunk 1 confirms, the abort releases the
// reservation less chunk 1's confirmed size, which its catalog row already
// carries, while the row stays at zero chunks and finalises partial
// stream_truncated.
// diagnosis: a failure here means reconciliation keys on the persisted
// contiguous run rather than on every confirmed chunk, so a chunk past a gap
// is released from the reservation while its catalog row still charges it,
// or the abort finalises with the wrong disposition.
func TestAbortReconcilesEveryConfirmedChunkNotOnlyTheRun(t *testing.T) {
	d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{1: 11}})
	quota := &adjustRecorder{}
	d.c.Quota = quota
	d.stream = discardStream{}
	d.reservedBytes, d.quotaCap, d.quotaCapSet = 100, 100, true
	confirm(d, 0)
	confirm(d, 1)
	if _, _, err := d.terminalAbort(partialmanifeststore.ReasonStreamTruncated); err == nil {
		t.Fatal("terminalAbort returned no error")
	}
	quota.mu.Lock()
	deltas := append([]int64(nil), quota.deltas...)
	quota.mu.Unlock()
	if len(deltas) != 1 || deltas[0] != -(100-11) {
		t.Errorf("Adjust deltas = %v, want [%d]", deltas, -(100 - 11))
	}
	got := manifestRow(t, store)
	if got.ChunkCount != 0 || got.WorkspaceBytesUploaded != 0 {
		t.Errorf("row chunk_count=%d bytes=%d, want 0/0", got.ChunkCount, got.WorkspaceBytesUploaded)
	}
	if !got.Partial || got.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("row partial=%v reason=%q, want partial stream_truncated", got.Partial, got.ManifestReason)
	}
}

// spec: §10.1.7 (preStop Hook Drain)
// A confirm whose counter write is lost is corrected by the flush each
// terminal arm issues before Finalise, on the complete arm and on an abort
// arm alike.
// diagnosis: a failure here means a lost per-confirm counter write leaves the
// finalised row behind the chunks the attempt confirmed, so the resume
// threshold and the backstop's reservation release read a short count.
func TestFinaliseFlushesTheConfirmedRunAfterALostWrite(t *testing.T) {
	newFlaky := func(t *testing.T) (*uploadDriver, *partialmanifeststore.MemoryStore) {
		t.Helper()
		d, store := confirmHarness(t, &gatedStat{sizes: map[uint32]int64{0: 9}})
		d.c.Manifests = &flakyConfirm{MemoryStore: store, failConfirms: 1}
		confirm(d, 0)
		if got := manifestRow(t, store); got.ChunkCount != 0 {
			t.Fatalf("precondition: the first counter write landed (chunk_count=%d)", got.ChunkCount)
		}
		return d, store
	}
	t.Run("complete arm", func(t *testing.T) {
		d, store := newFlaky(t)
		if _, _, err := d.onSummary(&adapterv1.CheckpointSummary{ChunkCount: 1, TotalBytes: 9}); err != nil {
			t.Fatalf("onSummary: %v", err)
		}
		if got := manifestRow(t, store); got.Partial || got.ChunkCount != 1 || got.WorkspaceBytesUploaded != 9 {
			t.Errorf("row partial=%v chunk_count=%d bytes=%d, want complete 1/9", got.Partial, got.ChunkCount, got.WorkspaceBytesUploaded)
		}
	})
	t.Run("abort arm", func(t *testing.T) {
		d, store := newFlaky(t)
		if err := d.finaliseAbort(partialmanifeststore.ReasonTimeout); err != nil {
			t.Fatalf("finaliseAbort: %v", err)
		}
		if got := manifestRow(t, store); !got.DeletedAt.IsZero() || got.ChunkCount != 1 || got.WorkspaceBytesUploaded != 9 {
			t.Errorf("row deleted_at=%v chunk_count=%d bytes=%d, want active 1/9", got.DeletedAt, got.ChunkCount, got.WorkspaceBytesUploaded)
		}
	})
}
