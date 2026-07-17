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
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
	// crashBeforeProbe makes the adapter close the stream with a transport
	// error after consuming CheckpointStart but before sending a Probe,
	// standing in for a pod crash during the workspace-size probe. The gateway
	// reaches its terminal abort with no intent row written.
	crashBeforeProbe bool
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
	if a.crashBeforeProbe {
		return status.Error(codes.Unavailable, "adapter crashed before the probe")
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

func (d *prefixDeleter) sweptPrefix(prefix string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.swept {
		if p == prefix {
			return true
		}
	}
	return false
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
	sessions  sessionstore.Store
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
	return &driverHarness{cp: cp, manifests: manifests, quota: quota, store: store, deleter: deleter, sessions: sessions}, "s1"
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

// spec: §16.1 line 195 — every terminal abort arm emits
// lenny_checkpoint_partial_total once, with recovered = false, the
// manifest_reason that named the arm, and the trigger the attempt was
// started with. The Checkpoint entry point drives a periodic attempt, so the
// trigger is periodic.
//
// diagnosis: a missing emission means an aborted partial checkpoint is
// invisible to the recovery-rate metric; a recovered = true label on a write
// arm, or a manifest_reason / trigger outside the §16.1 domain, corrupts the
// counter's label domain.
func TestDriverEmitsPartialCounterOnAbortArm_spec_16_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: 0, // close after chunk 0 commits, no Summary
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded on a truncated stream, want failure")
	}
	if len(m.partial) != 1 {
		t.Fatalf("partial counter emissions = %d, want 1 on a truncated-stream abort", len(m.partial))
	}
	got := m.partial[0]
	if got.recovered {
		t.Errorf("recovered = true, want false on a write-path abort arm")
	}
	if got.manifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("manifest_reason = %q, want %q", got.manifestReason, partialmanifeststore.ReasonStreamTruncated)
	}
	if got.trigger != checkpoint.TriggerPeriodic {
		t.Errorf("trigger = %q, want %q (the trigger the attempt was started with)", got.trigger, checkpoint.TriggerPeriodic)
	}
	if !got.trigger.IsValid() {
		t.Errorf("trigger = %q is not a member of the closed checkpoint.Trigger enum", got.trigger)
	}
}

// spec: §16.1 line 195 — the partial counter increments once per partial-
// manifest row finalised. An attempt whose stream errors before onProbe
// commits the intent row finalises zero rows, so no lenny_checkpoint_partial_
// total is emitted.
//
// diagnosis: finaliseAbort emitted IncCheckpointPartial unconditionally after
// Manifests.Finalise returned, ignoring that Finalise no-ops (zero rows) when
// no intent row was ever written. A pod crash during the workspace-size probe
// routes to onStreamError with intentWritten == false; the pre-fix code counted
// a recovered=false partial-manifest write that never happened, deflating the
// recovery-rate denominator. The fix gates the emission on d.intentWritten, so
// this test observes zero emissions where the pre-fix code observed one.
func TestDriverEmitsNoPartialCounterWhenNoIntentRowWritten_spec_16_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		crashBeforeProbe: true,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded on a stream that crashed before the probe, want failure")
	}
	if len(m.partial) != 0 {
		t.Fatalf("partial counter emissions = %d, want 0 when no intent row was finalised", len(m.partial))
	}
}

// spec: §16.1 line 195 — the success arm (onSummary, partial = false) is not
// a partial-manifest write, so it emits no lenny_checkpoint_partial_total.
//
// diagnosis: an emission on the complete arm would count a successful full
// checkpoint as a partial write, inflating the counter and breaking the
// recovery-rate denominator.
func TestDriverEmitsNoPartialCounterOnCompleteArm_spec_16_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: -1,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(m.partial) != 0 {
		t.Fatalf("partial counter emissions = %d, want 0 on a complete checkpoint", len(m.partial))
	}
}

// spec: §16.1 line 195 — the supersede arm finalises the prior row
// manifest_reason = 'superseded', so it emits lenny_checkpoint_partial_total
// once with that reason (recovered = false), alongside the supersede-specific
// counter.
func TestDriverEmitsPartialCounterOnSupersedeArm_spec_16_1(t *testing.T) {
	// First attempt truncates, leaving a partial = true active row.
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 30, chunkLens: []int64{10, 10, 10}, truncateAfter: 0,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	_ = h.cp.Checkpoint(context.Background(), "acme", sid)

	// A fresh completing attempt on the same (session, slot) supersedes the
	// retained row.
	second := &chunkedAdapter{probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1, store: h.store}
	client := dialAdapter(t, second)
	h.cp.Registry.Put(&podsession.BindResult{SessionID: sid, TenantID: "acme", Adapter: client})
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("second Checkpoint: %v", err)
	}
	var superseded int
	for _, e := range m.partial {
		if e.manifestReason == partialmanifeststore.ReasonSuperseded {
			superseded++
			if e.recovered {
				t.Errorf("supersede arm recovered = true, want false")
			}
			if !e.trigger.IsValid() {
				t.Errorf("supersede arm trigger = %q is not in the closed enum", e.trigger)
			}
		}
	}
	if superseded != 1 {
		t.Fatalf("superseded partial-counter emissions = %d, want 1", superseded)
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
	// spec: §10.1 line 132 — a zero-chunk finalisation soft-deletes the row
	// in the same transaction, so an empty partial manifest is never left
	// active occupying the (session, slot) slot for a later supersede or the
	// §12.5 backstop to reclaim.
	if rec.DeletedAt.IsZero() {
		t.Errorf("zero-chunk abort left the manifest row active, want soft-deleted at finalisation")
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

// spec: §16.1 — the gateway-side checkpoint counters are labeled by pool.
// One Checkpointer serves sessions across multiple pools, so the label must
// be the session's own bound pool resolved from its session-store row, not
// the static Checkpointer.Pool (which production never sets).
//
// diagnosis: the supersede, storage-failure, and size-exceeded counters
// stamped the static c.Pool, which is "" in production, so every series was
// emitted with pool="" regardless of the session's pool. The driver now
// carries the session's resolved pool. Against the pre-fix code
// supersededPool reads "" and this assertion fails.
func TestDriverCountersCarrySessionBoundPool_spec_16_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 30, chunkLens: []int64{10, 10, 10}, truncateAfter: 0,
	}, 1<<30)
	// The session is scheduled against a named pool; the Checkpointer's own
	// static Pool label stays empty, as it is in production.
	if _, err := h.sessions.Update(context.Background(), "acme", sid,
		func(s *sessionstore.Session) error { s.PoolRef = "tier-1"; return nil }); err != nil {
		t.Fatalf("set session pool: %v", err)
	}
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m
	_ = h.cp.Checkpoint(context.Background(), "acme", sid)

	second := &chunkedAdapter{probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1, store: h.store}
	client := dialAdapter(t, second)
	h.cp.Registry.Put(&podsession.BindResult{SessionID: sid, TenantID: "acme", Adapter: client})
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("second Checkpoint: %v", err)
	}
	if m.superseded == 0 {
		t.Fatal("supersede counter did not fire")
	}
	if m.supersededPool != "tier-1" {
		t.Errorf("supersede counter pool label = %q, want %q (the session's bound pool)", m.supersededPool, "tier-1")
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

// spec: §10.1 lines 137, 155 — the supersede predicate fences on
// coordination_generation. A prior active row at a strictly higher
// generation is a fenced newer writer, so a stale coordinator (a lower
// incoming generation) must not release its reservation or sweep its chunk
// objects; the stale attempt's own intent-row Put is rejected instead.
//
// diagnosis: supersedePriorAttempts released the prior row's reservation and
// swept its chunk prefix under only a slot / checkpoint-id guard, with no
// coordination_generation check, so a stale coordinator destroyed a live
// newer writer's state before its own Put rejected the write as stale.
// Against the pre-fix code the higher-generation row's reservation is
// released and its prefix swept; the fix leaves both intact.
func TestDriverSupersedeSkipsHigherGenerationActiveRow_spec_10_1(t *testing.T) {
	// The incoming attempt runs at the session's generation 0 (runningSession
	// seeds generation 0), so it is stale against a prior active row at
	// generation 1.
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m

	const higherPrefix = "/acme/checkpoint/s1/cp-higher/"
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:               "acme",
		CheckpointID:           "cp-higher",
		SessionID:              sid,
		SlotID:                 partialmanifeststore.SlotDefault,
		CoordinationGeneration: 1, // a fenced newer writer
		ChunkObjectKeyPrefix:   higherPrefix,
		ReservedBytes:          50,
	}); err != nil {
		t.Fatalf("seed higher-generation active row: %v", err)
	}

	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("stale-generation Checkpoint succeeded, want a stale-generation rejection")
	}

	// The higher-generation writer's state must be untouched: its reservation
	// stays outstanding, its row active, its chunk prefix unswept, and no
	// supersede counted.
	rec, err := h.manifests.Get(context.Background(), "acme", "cp-higher")
	if err != nil {
		t.Fatalf("get higher-generation row: %v", err)
	}
	if !rec.ReservationReleasedAt.IsZero() {
		t.Errorf("higher-generation row reservation released, want untouched by the stale coordinator")
	}
	if !rec.DeletedAt.IsZero() {
		t.Errorf("higher-generation row soft-deleted, want left active")
	}
	if h.deleter.sweptPrefix(higherPrefix) {
		t.Errorf("higher-generation row chunk prefix swept, want untouched")
	}
	if m.superseded != 0 {
		t.Errorf("supersede counter fired %d times, want 0 (the stale attempt supersedes nothing)", m.superseded)
	}
}

// spec: §10.1 line 137 — supersede is scoped to (session_id, slot_id). In a
// multi-slot session the driver releases the prior active row for the exact
// slot it is superseding, not a session-wide winner that may belong to a
// different slot.
//
// diagnosis: supersedePriorAttempts resolved the prior row via the
// session-scoped LatestActive, which returns the highest-generation row
// across every slot. With a higher-generation row active on another slot,
// the release skipped on the slot guard while Put still soft-deleted this
// slot's prior row, leaking its reservation and orphaning its chunks.
// Against the pre-fix code the target slot's row is never released; the fix
// releases it and sweeps its chunks.
func TestDriverSupersedeReleasesTargetSlotPriorRow_spec_10_1(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes: 20, chunkLens: []int64{10, 10}, truncateAfter: -1,
	}, 1<<30)
	m := &captureDriverMetrics{}
	h.cp.DriverMetrics = m

	// The incoming attempt targets the default slot at generation 0. A prior
	// active row sits on the default slot (the row it must supersede), and a
	// higher-generation row sits on a different slot (the session-wide
	// selector's winner).
	const targetPrefix = "/acme/checkpoint/s1/cp-target/"
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:             "acme",
		CheckpointID:         "cp-target",
		SessionID:            sid,
		SlotID:               partialmanifeststore.SlotDefault,
		ChunkObjectKeyPrefix: targetPrefix,
		ReservedBytes:        40,
	}); err != nil {
		t.Fatalf("seed target-slot active row: %v", err)
	}
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:               "acme",
		CheckpointID:           "cp-other",
		SessionID:              sid,
		SlotID:                 "slot-other",
		CoordinationGeneration: 1, // higher generation: the session-wide selector's winner
		ChunkObjectKeyPrefix:   "/acme/checkpoint/s1/cp-other/",
		ReservedBytes:          40,
	}); err != nil {
		t.Fatalf("seed other-slot active row: %v", err)
	}

	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The target-slot prior row's reservation is released and its prefix swept.
	target, err := h.manifests.Get(context.Background(), "acme", "cp-target")
	if err != nil {
		t.Fatalf("get target-slot row: %v", err)
	}
	if target.ReservationReleasedAt.IsZero() {
		t.Errorf("target-slot prior row reservation not released; the supersede release missed it")
	}
	if !h.deleter.sweptPrefix(targetPrefix) {
		t.Errorf("target-slot prior row chunk prefix not swept")
	}
	if m.superseded != 1 {
		t.Errorf("supersede counter fired %d times, want 1", m.superseded)
	}
	// The other slot's row is untouched: it is a different slot the incoming
	// attempt does not supersede.
	other, err := h.manifests.Get(context.Background(), "acme", "cp-other")
	if err != nil {
		t.Fatalf("get other-slot row: %v", err)
	}
	if !other.ReservationReleasedAt.IsZero() {
		t.Errorf("other-slot row reservation released, want untouched (different slot)")
	}
	if !other.DeletedAt.IsZero() {
		t.Errorf("other-slot row soft-deleted, want left active (different slot)")
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

// spec: §11.2 line 35 — reservation release is exactly-once and guarded, and
// a config with Quota wired but QuotaLimitFor nil disables reservation by
// contract: no Reserve runs, so no abort-arm release may decrement the tenant
// counter even though the intent row records a probed reserved_bytes.
//
// diagnosis: onProbe set d.reservedBytes unconditionally and reconcile guarded
// only on Quota != nil, so an aborted checkpoint in the reservation-disabled
// config decremented the counter by the unconfirmed probed size, drifting the
// tenant's storage_bytes_used below its true value. Against the pre-fix code
// the counter reads 80 (100 seeded minus a wrongful 20-byte release); the fix
// keeps it at 100.
func TestDriverAbortDoesNotReleaseWhenReservationDisabled_spec_11_2(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		truncateAfter: 0, // close after chunk 0 (10 bytes) commits, no Summary
	}, 1<<30)
	// Reservations disabled by contract: Quota is wired but no limit resolver.
	h.cp.QuotaLimitFor = nil
	// Seed the counter with unrelated tenant usage the abort must not touch.
	if err := h.quota.Set(context.Background(), "acme", 100); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded on a truncated stream, want failure")
	}
	used, _ := h.quota.Used(context.Background(), "acme")
	if used != 100 {
		t.Errorf("storage counter = %d, want 100 (no release when reservation is disabled)", used)
	}
}

// failingPutManifests fails the intent-row Put so onProbe reaches its
// early-abort releaseReservation arm.
type failingPutManifests struct {
	*partialmanifeststore.MemoryStore
}

func (s failingPutManifests) Put(context.Context, partialmanifeststore.Record) error {
	return fmt.Errorf("failingPutManifests: put rejected")
}

// spec: §11.2 line 35 — the early-abort reservation release on an intent-row
// Put failure is exactly-once and guarded: with reservation disabled
// (QuotaLimitFor nil) no Reserve ran, so releaseReservation must not decrement
// the tenant counter.
//
// diagnosis: releaseReservation guarded only on Quota == nil and adjusted by
// the probed size that onProbe had stamped onto d.reservedBytes before any
// Reserve, so a Put failure in the reservation-disabled config decremented the
// counter for bytes never reserved. Against the pre-fix code the counter reads
// 70 (100 seeded minus a wrongful 30-byte release); the fix keeps it at 100.
func TestDriverEarlyAbortDoesNotReleaseWhenReservationDisabled_spec_11_2(t *testing.T) {
	h, sid := newDriverHarness(t, &chunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10},
		truncateAfter: -1,
	}, 1<<30)
	h.cp.QuotaLimitFor = nil
	h.cp.Manifests = failingPutManifests{h.manifests}
	if err := h.quota.Set(context.Background(), "acme", 100); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if err := h.cp.Checkpoint(context.Background(), "acme", sid); err == nil {
		t.Fatal("Checkpoint succeeded despite an intent-row Put failure, want failure")
	}
	used, _ := h.quota.Used(context.Background(), "acme")
	if used != 100 {
		t.Errorf("storage counter = %d, want 100 (no early-abort release when reservation is disabled)", used)
	}
}

// preProbeChunkAdapter sends a ChunkReady before any Probe, so the intent row
// is never written when the gateway processes the chunk. It records whether
// the gateway responded with an Abort or wrongly minted a Grant.
type preProbeChunkAdapter struct {
	adapterv1.UnimplementedAdapterServer
	reply chan string
}

func (a *preProbeChunkAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	if _, err := stream.Recv(); err != nil { // consume CheckpointStart
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: 0, Length: 10}},
	}); err != nil {
		return err
	}
	msg, err := stream.Recv()
	if err != nil {
		a.reply <- fmt.Sprintf("recv-err:%v", err)
		return nil
	}
	switch {
	case msg.GetAbort() != nil:
		a.reply <- "abort:" + msg.GetAbort().GetReason()
	case msg.GetGrant() != nil:
		a.reply <- "grant"
	default:
		a.reply <- "other"
	}
	return nil
}

// spec: §10.1 line 130 — the gateway mints a chunk capability only after the
// intent-row INSERT commits. A ChunkReady that arrives before the Probe wrote
// the row gets an Abort, not a Grant, so no PUT can orphan an object under a
// checkpoint_id with no manifest row.
//
// diagnosis: onChunkReady had no precondition check that onProbe committed the
// row and relied on adapter frame ordering; a ChunkReady before the Probe
// minted and sent a presigned PUT grant with no intent row, no reservation,
// and the at-signing quota gate skipped. Against the pre-fix code the adapter
// receives a Grant; the fix makes it receive a stream_truncated Abort.
func TestDriverFencesChunkReadyOnIntentRow_spec_10_1(t *testing.T) {
	adapter := &preProbeChunkAdapter{reply: make(chan string, 1)}
	client := dialAdapter(t, adapter)
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: client})
	sessions := memstore.New()
	runningSession(t, sessions, "acme", "s1")
	manifests := partialmanifeststore.NewMemoryStore(nil)
	quota := storagequota.NewMemory()
	cp := &checkpointer.Checkpointer{
		Sessions:      sessions,
		Registry:      registry,
		Manifests:     manifests,
		Quota:         quota,
		QuotaLimitFor: func(context.Context, string) (int64, error) { return 1 << 30, nil },
		Presigner:     presignerFake{},
		ObjectStore:   blobstore.NewMemoryStore(time.Now),
		Deadline:      5 * time.Second,
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Fatal("Checkpoint succeeded on a pre-Probe ChunkReady, want failure")
	}
	select {
	case got := <-adapter.reply:
		if got != "abort:"+partialmanifeststore.ReasonStreamTruncated {
			t.Errorf("adapter reply = %q, want an %s abort (no grant before the intent row)",
				got, partialmanifeststore.ReasonStreamTruncated)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("adapter received no reply to its pre-Probe ChunkReady")
	}
	// No intent row was written, so no reservation was taken.
	if _, err := manifests.LatestActive(context.Background(), "acme", "s1"); err == nil {
		t.Error("a manifest row was written for a checkpoint whose Probe never ran")
	}
	if used, _ := quota.Used(context.Background(), "acme"); used != 0 {
		t.Errorf("storage counter = %d, want 0 (no reservation before the intent row)", used)
	}
}

// probeThenChunkAdapter sends a Probe followed by a ChunkReady, then records
// whether the gateway replied with an Abort or minted a Grant. It stands in
// for a chunked producer running against a gateway whose manifest store is
// unwired: onProbe returns before writing any intent row, so the capability
// gate must still refuse to sign.
type probeThenChunkAdapter struct {
	adapterv1.UnimplementedAdapterServer
	reply chan string
}

func (a *probeThenChunkAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointClientMessage, adapterv1.CheckpointServerMessage]) error {
	if _, err := stream.Recv(); err != nil { // consume CheckpointStart
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: 4096}},
	}); err != nil {
		return err
	}
	if err := stream.Send(&adapterv1.CheckpointServerMessage{
		Msg: &adapterv1.CheckpointServerMessage_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: 0, Length: 10}},
	}); err != nil {
		return err
	}
	msg, err := stream.Recv()
	if err != nil {
		a.reply <- fmt.Sprintf("recv-err:%v", err)
		return nil
	}
	switch {
	case msg.GetAbort() != nil:
		a.reply <- "abort:" + msg.GetAbort().GetReason()
	case msg.GetGrant() != nil:
		a.reply <- "grant"
	default:
		a.reply <- "other"
	}
	return nil
}

// countingPresigner records how many PUT capabilities it signed so a test can
// assert the gateway minted none.
type countingPresigner struct {
	mu     sync.Mutex
	signed int
}

func (p *countingPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	p.mu.Lock()
	p.signed++
	p.mu.Unlock()
	return presignerFake{}.PresignPut(u, contentLength, ttl)
}

func (p *countingPresigner) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	return presignerFake{}.PresignGet(u, ttl)
}

func (p *countingPresigner) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.signed
}

// spec: §10.1 line 130 — the chunk-capability gate is unconditional: no PUT
// capability may be minted before an intent row commits. A gateway with a
// Presigner wired but no manifest store writes no intent row (onProbe returns
// early), so a ChunkReady must still receive an Abort and sign nothing;
// otherwise a real PUT capability would orphan an object under a checkpoint_id
// with no manifest row and no reclaimer.
//
// diagnosis: the gate qualified minting on c.Manifests != nil, so the
// manifest-disabled path (intentWritten stays false) skipped the gate entirely
// and fell through to sign a real grant. Against the pre-fix code the adapter
// receives a Grant and the presigner signs once; the fix makes it receive a
// stream_truncated Abort with zero capabilities signed.
func TestDriverFencesChunkReadyWhenManifestsUnwired_spec_10_1(t *testing.T) {
	adapter := &probeThenChunkAdapter{reply: make(chan string, 1)}
	client := dialAdapter(t, adapter)
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: client})
	sessions := memstore.New()
	runningSession(t, sessions, "acme", "s1")
	presigner := &countingPresigner{}
	cp := &checkpointer.Checkpointer{
		Sessions:    sessions,
		Registry:    registry,
		Presigner:   presigner, // wired, but Manifests is nil
		ObjectStore: blobstore.NewMemoryStore(time.Now),
		Deadline:    5 * time.Second,
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Fatal("Checkpoint succeeded on a ChunkReady with no intent row, want failure")
	}
	select {
	case got := <-adapter.reply:
		if got != "abort:"+partialmanifeststore.ReasonStreamTruncated {
			t.Errorf("adapter reply = %q, want an %s abort (no grant without an intent row)",
				got, partialmanifeststore.ReasonStreamTruncated)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("adapter received no reply to its ChunkReady")
	}
	if n := presigner.count(); n != 0 {
		t.Errorf("presigner signed %d capabilities, want 0 (no grant before an intent row)", n)
	}
}

// captureDriverMetrics records the driver's gateway-side counter fires and
// the pool label each pool-carrying counter was stamped with, so a test can
// assert the counters carry the session's bound pool rather than the static
// Checkpointer.Pool.
type captureDriverMetrics struct {
	sizeExceeded       int
	storageFailure     int
	superseded         int
	kmsUnavailable     int
	sizeExceededPool   string
	storageFailurePool string
	supersededPool     string
	// partial records every lenny_checkpoint_partial_total emission so a test
	// can assert the {recovered, manifest_reason, trigger} tuple each abort arm
	// stamps.
	partial []partialEmit
}

type partialEmit struct {
	pool           string
	recovered      bool
	manifestReason string
	trigger        checkpoint.Trigger
}

func (m *captureDriverMetrics) IncCheckpointSizeExceeded(pool, _ string) {
	m.sizeExceeded++
	m.sizeExceededPool = pool
}

func (m *captureDriverMetrics) IncCheckpointStorageFailure(pool, _, _ string) {
	m.storageFailure++
	m.storageFailurePool = pool
}

func (m *captureDriverMetrics) IncCheckpointPartialManifestsSuperseded(pool string) {
	m.superseded++
	m.supersededPool = pool
}

func (m *captureDriverMetrics) IncCheckpointKMSUnavailable() { m.kmsUnavailable++ }

func (m *captureDriverMetrics) IncCheckpointPartial(pool string, recovered bool, manifestReason string, trigger checkpoint.Trigger) {
	m.partial = append(m.partial, partialEmit{pool, recovered, manifestReason, trigger})
}

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
	// A zero-chunk abort soft-deletes its row in the finalising transaction
	// (§10.1 line 132), so LatestActive no longer returns it and the failed
	// attempt left no session ref. Recover the tombstoned row so the test can
	// assert its finalised disposition.
	deleted, derr := h.manifests.ListSoftDeletedBefore(context.Background(), time.Now().Add(time.Hour))
	if derr == nil {
		var best partialmanifeststore.Record
		for _, r := range deleted {
			if r.TenantID == tenantID && r.SessionID == sessionID && r.CreatedAt.After(best.CreatedAt) {
				best = r
			}
		}
		if best.CheckpointID != "" {
			return best
		}
	}
	t.Fatalf("no manifest row found for %s/%s", tenantID, sessionID)
	return partialmanifeststore.Record{}
}

var _ = checkpoint.TriggerPeriodic
