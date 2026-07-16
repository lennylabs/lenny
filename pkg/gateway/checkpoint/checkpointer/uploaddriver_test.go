// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// chunkedAdapter is a §10.1 chunked producer test double. It sends a
// Probe, then for each declared chunk length runs the
// ChunkReady → Grant → PUT → ChunkCommitted handshake against the gateway
// driver, and closes with a Summary. Hooks let a test inject the
// behaviours the abort arms react to.
type chunkedAdapter struct {
	adapterv1.UnimplementedAdapterServer
	probeBytes int64
	// chunkLens is the length the adapter declares for each chunk. The
	// actual bytes "written" default to the declared length; putBytes
	// overrides the confirmed size a store observes.
	chunkLens []int64
	// putBytes[i], when set, is the size the store records for chunk i
	// (larger than the declared length exercises the over-size confirm).
	putBytes map[int]int64
	// sizeReject makes the adapter reject at the probe with
	// FailedPrecondition, standing in for the workspace-size limit.
	sizeReject bool
	// failCode, when set, terminates the stream with a CheckpointFailed
	// frame carrying this error code after chunk 0 commits.
	failCode string
	// truncateAfter, when >= 0, closes the stream (returns) after that many
	// chunks commit, without a Summary — a truncated stream.
	truncateAfter int
	// store is the object store the adapter "PUTs" into: it records the
	// object so the gateway's Stat confirm observes it.
	store *blobstore.MemoryStore
}

func (a *chunkedAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	start, err := stream.Recv()
	if err != nil {
		return err
	}
	_ = start.GetStart()
	if a.sizeReject {
		return status.Error(codes.FailedPrecondition, "workspace size 999 exceeds limit 100")
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: a.probeBytes}},
	}); err != nil {
		return err
	}
	var total int64
	for i, ln := range a.chunkLens {
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: uint32(i), Length: ln}},
		}); err != nil {
			return err
		}
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if msg.GetAbort() != nil {
			return nil
		}
		grant := msg.GetGrant()
		if grant == nil {
			return status.Errorf(codes.Internal, "expected grant for chunk %d", i)
		}
		// "PUT" the chunk into the store so the gateway Stat confirm sees it.
		wrote := ln
		if a.putBytes != nil {
			if b, ok := a.putBytes[i]; ok {
				wrote = b
			}
		}
		a.putObject(grant, wrote)
		total += wrote
		if err := stream.Send(&adapterv1.CheckpointServerMessage{
			Msg: &adapterv1.CheckpointServerMessage_ChunkCommitted{ChunkCommitted: &adapterv1.ChunkCommitted{Index: uint32(i)}},
		}); err != nil {
			return err
		}
		if a.truncateAfter >= 0 && i >= a.truncateAfter {
			// Close the stream without a Summary: the gateway sees EOF.
			return nil
		}
		if a.failCode != "" && i == 0 {
			return stream.Send(&adapterv1.CheckpointServerMessage{
				Msg: &adapterv1.CheckpointServerMessage_Failed{Failed: &adapterv1.CheckpointFailed{
					Reason: "chunk rejected", Index: uint32(i), HttpStatus: 503, ErrorCode: a.failCode,
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

// putObject writes a placeholder object of size bytes at the grant's key
// so the gateway's Stat confirm observes the size. The grant URL encodes
// the object key; the fake reconstructs the URI from the checkpoint id in
// the grant so the store key matches the gateway's Stat key. To keep the
// key resolution simple, the fake stores under the same URI the gateway
// signs, which the presignerFake records in a shared map.
func (a *chunkedAdapter) putObject(grant *adapterv1.CheckpointGrant, size int64) {
	if a.store == nil {
		return
	}
	uri, ok := lookupGrantURI(grant.GetUrl())
	if !ok {
		return
	}
	_, _ = a.store.Put(uri, "application/octet-stream", strings.NewReader(strings.Repeat("x", int(size))))
}

// presignerFake mints grant URLs and records the URI each URL maps to so
// the chunked adapter can write to the same key the gateway will Stat.
type presignerFake struct{}

var grantURIs sync.Map // url -> blobstore.URI

func lookupGrantURI(url string) (blobstore.URI, bool) {
	v, ok := grantURIs.Load(url)
	if !ok {
		return blobstore.URI{}, false
	}
	return v.(blobstore.URI), true
}

func (p presignerFake) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	url := fmt.Sprintf("https://obj.test/%s/%s?len=%d", u.SessionID, u.PartID, contentLength)
	grantURIs.Store(url, u)
	return blobstore.Grant{URL: url, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (p presignerFake) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "https://obj.test/get"}, nil
}

// prefixDeleter records the prefixes swept so a test asserts the abort
// sweep ran, and deletes objects under the prefix from the store.
type prefixDeleter struct {
	mu    sync.Mutex
	swept []string
	store *blobstore.MemoryStore
}

func (d *prefixDeleter) DeleteByPrefix(_ context.Context, prefix string) (int, error) {
	d.mu.Lock()
	d.swept = append(d.swept, prefix)
	d.mu.Unlock()
	return 0, nil
}

func (d *prefixDeleter) sweptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.swept)
}

// driverHarness wires a Checkpointer with the full seam set over a
// chunked adapter and an in-memory object store, manifest store, and
// quota counter.
type driverHarness struct {
	cp        *checkpointer.Checkpointer
	manifests *partialmanifeststore.MemoryStore
	quota     *storagequota.Memory
	store     *blobstore.MemoryStore
	deleter   *prefixDeleter
}

func newDriverHarness(t *testing.T, adapter *chunkedAdapter, limit int64) (*driverHarness, string) {
	t.Helper()
	store := blobstore.NewMemoryStore(time.Now)
	adapter.store = store
	client := dialAdapter(t, adapter)
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: client})
	sessions := memstore.New()
	runningSession(t, sessions, "acme", "s1")

	manifests := partialmanifeststore.NewMemoryStore(nil)
	quota := storagequota.NewMemory()
	deleter := &prefixDeleter{store: store}
	cp := &checkpointer.Checkpointer{
		Sessions:      sessions,
		Registry:      registry,
		Manifests:     manifests,
		Quota:         quota,
		QuotaLimitFor: func(context.Context, string) (int64, error) { return limit, nil },
		Presigner:     presignerFake{},
		ObjectStore:   store,
		ChunkDeleter:  deleter,
		Deadline:      5 * time.Second,
	}
	return &driverHarness{cp: cp, manifests: manifests, quota: quota, store: store, deleter: deleter}, "s1"
}

// spec: §10.1 — a complete checkpoint whose every declared byte is
// Stat-confirmed finalises partial = false / complete, and the
// reservation reconciles against the confirmed total.
func TestDriverFinalisesCompleteWhenEveryByteConfirmed_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: -1,
	}, 1<<30)
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rec := latestManifest(t, h, "acme", sid)
	if rec.Partial {
		t.Errorf("manifest partial = true, want false after a complete checkpoint")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonComplete {
		t.Errorf("manifest_reason = %q, want complete", rec.ManifestReason)
	}
	if rec.ChunkCount != 3 {
		t.Errorf("chunk_count = %d, want 3", rec.ChunkCount)
	}
	// The reservation reconciled: reserved 30, confirmed 30, counter = 30.
	used, _ := h.quota.Used(context.Background(), "acme")
	if used != 30 {
		t.Errorf("storage counter = %d, want 30 (confirmed total)", used)
	}
	if rec.ReservationReleasedAt.IsZero() {
		t.Errorf("reservation was not released on a complete checkpoint")
	}
}

// spec: §10.1 line 157 — the intent row is partial = true from INSERT and
// stays partial until every declared byte is confirmed. A stream that
// truncates before the Summary leaves partial = true and the sweep removes
// the confirmed chunks.
func TestDriverLeavesPartialTrueOnTruncatedStream_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: 0, // close after chunk 0 commits, no Summary
	}, 1<<30)
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded on a truncated stream, want failure")
	}
	rec := latestManifest(t, h, "acme", sid)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after a truncated stream")
	}
	if h.deleter.sweptCount() == 0 {
		t.Errorf("abort sweep did not run on a truncated stream")
	}
	// The reservation released the unconfirmed remainder: reserved 30,
	// confirmed 10, counter = 10.
	used, _ := h.quota.Used(context.Background(), "acme")
	if used != 10 {
		t.Errorf("storage counter = %d, want 10 (only the confirmed chunk)", used)
	}
}

// spec: §11.2 — a chunk that writes more bytes than the signed
// Content-Length against a non-enforcing store is caught by the Stat
// confirm, aborts the attempt, and reconciles the excess into the counter;
// the over-length declaration itself never gets a capability.
func TestDriverAbortsOnOverSizeConfirm_spec_11_2(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10},
		putBytes:      map[int]int64{0: 25}, // chunk 0 writes 25 bytes past the signed 10
		truncateAfter: -1,
	}, 1<<30)
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded despite an over-size chunk, want abort")
	}
	rec := latestManifest(t, h, "acme", sid)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after an over-size abort")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonQuotaExceeded {
		t.Errorf("manifest_reason = %q, want quota_exceeded", rec.ManifestReason)
	}
}

// spec: §10.1 line 139 / §13.2 — a declared chunk length larger than the
// gateway-chosen chunk_size_bytes gets no capability; the attempt aborts
// with stream_truncated before anything is signed, so the bytes a
// capability can carry are a gateway constant.
func TestDriverRejectsOverChunkSizeDeclaration_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    100,
		chunkLens:     []int64{4 << 20}, // 4 MiB, but the gateway chunk size is 1 MiB below
		truncateAfter: -1,
	}, 1<<30)
	h.cp.ChunkSizeBytes = 1 << 20
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded on an over-chunk_size declaration, want abort")
	}
	rec := latestManifest(t, h, "acme", sid)
	if rec.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("manifest_reason = %q, want stream_truncated", rec.ManifestReason)
	}
	// No chunk was confirmed because no grant was minted.
	if rec.ChunkCount != 0 {
		t.Errorf("chunk_count = %d, want 0 (no grant minted)", rec.ChunkCount)
	}
}

// spec: §12.5 line 303 — a kms:-coded object-store rejection maps onto a
// classification-control violation: no retry, the KMS telemetry fires, and
// the checkpoint fails.
func TestDriverMapsKMSRejectionToControlViolation_spec_12_5(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10},
		failCode:      "kms:KeyUnavailable",
		truncateAfter: -1,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	err := h.cp.Checkpoint(context.Background(), "acme", sid)
	if err == nil {
		t.Fatal("Checkpoint succeeded on a kms: rejection, want failure")
	}
	if !strings.Contains(err.Error(), "CLASSIFICATION_CONTROL_VIOLATION") {
		t.Errorf("error = %v, want a classification-control violation", err)
	}
	if m.kmsUnavailable == 0 {
		t.Errorf("IncCheckpointKMSUnavailable was not fired on a kms: rejection")
	}
}

// spec: §4.4 line 255 — a workspace-size-probe rejection increments
// lenny_checkpoint_size_exceeded_total, emits the checkpoint.skipped
// session event, and writes no manifest row.
func TestDriverEmitsSkippedOnSizeLimit_spec_4_4(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{sizeReject: true, truncateAfter: -1}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	var skipped []string
	h.cp.SkippedEventFunc = func(_ context.Context, _, _, reason string) { skipped = append(skipped, reason) }
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded despite a size-limit rejection, want failure")
	}
	if m.sizeExceeded == 0 {
		t.Errorf("IncCheckpointSizeExceeded was not fired on a size-limit rejection")
	}
	if len(skipped) != 1 || skipped[0] != "workspace_size_limit" {
		t.Errorf("skipped events = %v, want [workspace_size_limit]", skipped)
	}
}

// spec: §10.1 supersede-on-write — an aborted attempt's retained active row
// is superseded by the next attempt regardless of trigger, and the
// supersede counter fires once per superseded row.
func TestDriverSupersedesPriorAbortedAttempt_spec_10_1(t *testing.T) {
	// First attempt truncates, leaving a partial = true active row.
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 30, chunkLens: []int64{10, 10, 10}, truncateAfter: 0,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	_ = h.cp.Checkpoint(context.Background(), "acme", sid)

	// Rebind a fresh completing adapter for the second attempt on the same
	// registry so the same (session, slot) supersedes the retained row.
	second := &chunkedAdapter{probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1, store: h.store}
	client := dialAdapter(t, second)
	h.cp.Registry.Put(&podsession.BindResult{SessionID: sid, TenantID: "acme", Adapter: client})
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("second Checkpoint: %v", err)
	}
	if m.superseded == 0 {
		t.Errorf("IncCheckpointPartialManifestsSuperseded was not fired for the retained aborted row")
	}
}

// spec: §11.2 line 35 — reservation release is exactly-once and guarded:
// when a next attempt supersedes a retained 'timeout' resume-aid row whose
// reservation was already released on its own deadline-fire arm, the guarded
// UPDATE reports rows_affected == 0 and the tenant storage counter is NOT
// decremented a second time.
//
// diagnosis: supersedePriorAttempts discarded the rows-affected return and
// applied the counter Adjust whenever ReleaseReservation returned no error,
// double-decrementing the counter for bytes the deadline-fire arm had
// already released. Against the pre-fix code the counter reads 10 (the
// second reservation of 20 minus the wrongful 20-byte decrement plus the
// retained 10); the fix keeps it at 30.
func TestDriverSupersedeDoesNotDoubleReleaseReservation_spec_11_2(t *testing.T) {
	// The next attempt completes cleanly; the scenario under test is what it
	// does to the counter while superseding the retained timeout row.
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1,
	}, 1<<30)

	// Inject a retained 'timeout' resume-aid row for the same (session, slot)
	// whose reservation was already released on its own deadline-fire arm
	// (ReservationReleasedAt set, reserved 30, 10 bytes confirmed). Those 10
	// confirmed bytes are the only storage this row still accounts for, so
	// the tenant counter starts at 10.
	released := time.Now().Add(-time.Minute)
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:               "acme",
		CheckpointID:           "cp-timeout",
		SessionID:              sid,
		SlotID:                 partialmanifeststore.SlotDefault,
		Partial:                true,
		ManifestReason:         partialmanifeststore.ReasonTimeout,
		ChunkObjectKeyPrefix:   "/acme/checkpoint/s1/cp-timeout/",
		ReservedBytes:          30,
		WorkspaceBytesUploaded: 10,
		ReservationReleasedAt:  released,
	}); err != nil {
		t.Fatalf("seed retained timeout row: %v", err)
	}
	if err := h.quota.Set(context.Background(), "acme", 10); err != nil {
		t.Fatalf("seed counter: %v", err)
	}

	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The retained row's reservation was already released, so superseding it
	// must not touch the counter. The counter is the retained 10 plus the
	// second attempt's confirmed 20, reconciled to 0 net at completion = 30.
	used, _ := h.quota.Used(context.Background(), "acme")
	if used != 30 {
		t.Errorf("storage counter = %d, want 30 (no double release of the retained row)", used)
	}
	if m.superseded != 1 {
		t.Errorf("supersede counter fired %d times, want 1", m.superseded)
	}
}

// ctxHonoringManifests fails Finalise and ReleaseReservation when the
// supplied context is already cancelled or expired, mirroring pgstore and
// storagequota (which honour ctx) rather than the ctx-ignoring MemoryStore.
// It exercises the abort-arm cleanup running on a context detached from the
// attempt's cancelled deadline context.
type ctxHonoringManifests struct {
	*partialmanifeststore.MemoryStore
}

func (s ctxHonoringManifests) Finalise(ctx context.Context, tenantID, checkpointID string, partial bool, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemoryStore.Finalise(ctx, tenantID, checkpointID, partial, reason)
}

func (s ctxHonoringManifests) ReleaseReservation(ctx context.Context, tenantID, checkpointID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.MemoryStore.ReleaseReservation(ctx, tenantID, checkpointID)
}

// spec: §10.1 line 132 — every abort arm finalises the intent row
// partial = true with its manifest_reason and releases the reservation the
// attempt did not keep, even though a latched abort cancels the attempt's
// stream context before the terminal handler runs.
//
// diagnosis: finaliseAbort and reconcile issued their durable writes on the
// per-attempt context, which abort() cancels via d.cancel() before the
// reader reaches the terminal handler. Against a store that honours ctx the
// Finalise and ReleaseReservation then failed on the dead context, leaving
// the row 'in_progress' with the reservation leaked. The fix runs the
// cleanup on a context detached from the cancelled attempt context.
func TestDriverAbortFinalisesOnDetachedContext_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10},
		putBytes:      map[int]int64{0: 25}, // over-size confirm latches an abort and cancels d.ctx
		truncateAfter: -1,
	}, 1<<30)
	// Swap in a store that honours ctx cancellation so the abort arm's
	// finalisation on the cancelled attempt context would fail if it did not
	// detach.
	h.cp.Manifests = ctxHonoringManifests{h.manifests}

	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded despite an over-size chunk, want abort")
	}
	rec := latestManifest(t, h, "acme", sid)
	if rec.ManifestReason != partialmanifeststore.ReasonQuotaExceeded {
		t.Errorf("manifest_reason = %q, want quota_exceeded (row was not finalised on the detached context)", rec.ManifestReason)
	}
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after an abort arm")
	}
	if rec.ReservationReleasedAt.IsZero() {
		t.Errorf("reservation was not released on the abort arm's detached context")
	}
}

// captureDriverMetrics records the driver's gateway-side counter fires.
type captureDriverMetrics struct {
	sizeExceeded   int
	storageFailure int
	superseded     int
	kmsUnavailable int
}

func (m *captureDriverMetrics) IncCheckpointSizeExceeded(string, string) { m.sizeExceeded++ }

func (m *captureDriverMetrics) IncCheckpointStorageFailure(string, string, string) {
	m.storageFailure++
}

func (m *captureDriverMetrics) IncCheckpointPartialManifestsSuperseded(string) { m.superseded++ }

func (m *captureDriverMetrics) IncCheckpointKMSUnavailable() { m.kmsUnavailable++ }

// latestManifest returns the manifest row for the session's most recent
// attempt. It scans the store for the row whose session matches; the
// single-flight lock guarantees at most one attempt runs at a time.
func latestManifest(t *testing.T, h *driverHarness, tenantID, sessionID string) partialmanifeststore.Record {
	t.Helper()
	// The session row's WorkspaceSnapshot.Ref is the checkpoint id on
	// success; on abort we scan LatestActive, and failing that the most
	// recently created row via the internal helper.
	rec, err := h.manifests.LatestActive(context.Background(), tenantID, sessionID)
	if err == nil {
		return rec
	}
	// No active row (a completed checkpoint finalised partial = false and
	// is not "active"): resolve by the session's recorded ref.
	row, gerr := h.cp.Sessions.Get(context.Background(), tenantID, sessionID)
	if gerr == nil && row.WorkspaceSnapshot != nil && row.WorkspaceSnapshot.Ref != "" {
		r, e := h.manifests.Get(context.Background(), tenantID, row.WorkspaceSnapshot.Ref)
		if e == nil {
			return r
		}
	}
	t.Fatalf("no manifest row found for %s/%s", tenantID, sessionID)
	return partialmanifeststore.Record{}
}

var _ = checkpoint.TriggerPeriodic
