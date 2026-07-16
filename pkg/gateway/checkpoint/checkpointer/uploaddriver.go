// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// DefaultChunkSizeBytes is the §10.1 `partialChunkSizeBytes` the gateway
// signs into every chunk grant and clamps every adapter-declared chunk
// length against: a fixed 16 MiB block. It gives every PUT an exact,
// signable Content-Length and bounds the worst-case unreconciled overage
// against a non-enforcing object store to `grantWindow × ChunkSizeBytes`.
//
// spec: §10.1 line 139 (fixed-size chunked-object model); §13.2 (the
// residual-exposure bound).
const DefaultChunkSizeBytes = 16 << 20

// chunkMimeType is the content type recorded for every checkpoint chunk
// object; the chunks are opaque tar (or tar.gz) blocks.
const chunkMimeType = "application/octet-stream"

// cleanupTimeout bounds the detached context on which a terminal abort or
// deadline-fire arm commits its durable manifest finalisation, reservation
// release, and chunk sweep. The attempt's own d.ctx is already cancelled or
// deadline-expired on those arms (a latched abort cancels the stream; a
// deadline fire is what drove stream.Recv to return), so the cleanup writes
// run on a fresh short-lived context detached from that cancellation.
const cleanupTimeout = 15 * time.Second

// StorageQuota is the §11.2 reservation seam the upload driver reserves
// the probed workspace size against before it mints any grant and
// reconciles against the confirmed total on every terminal arm. The
// production storagequota.Failover / storagequota.Memory satisfy it.
//
// spec: §11.2 (checkpoint quota reservation and reconciliation).
type StorageQuota interface {
	// Reserve increments the tenant counter by incoming, rejecting the
	// reservation with storagequota.ErrQuotaExceeded when it would carry
	// the tenant past limit. It returns the prior counter value.
	Reserve(ctx context.Context, tenantID string, incoming, limit int64) (int64, error)
	// Adjust applies a signed relative delta to the tenant counter,
	// clamping at zero. The driver releases an aborted attempt's
	// unconfirmed reservation with a negative delta.
	Adjust(ctx context.Context, tenantID string, delta int64) error
}

// ObjectStat is the §11.2 confirm seam: the driver Stats each committed
// chunk to read the byte count the object store actually wrote, which is
// the "bytes actually written" the quota accounting keys on and the
// defence-in-depth check against a body larger than the signed
// Content-Length on a non-enforcing backend. blobstore.Store satisfies it.
//
// spec: §11.2 (bytes-actually-written confirmation).
type ObjectStat interface {
	Stat(u blobstore.URI) (blobstore.BlobInfo, error)
}

// ChunkRecorder is the §12.5 catalog seam: the driver inserts the
// artifact_store row for each confirmed chunk from its Stat-confirmed
// size, because a presigned PUT bypasses blobstore.Store.Put's own
// cataloging path. cataloging.Store satisfies it.
//
// spec: §12.5 line 309 (every artifact_store row is inserted alongside
// the bucket object).
type ChunkRecorder interface {
	RecordPut(u blobstore.URI, size int64, mimeType string) error
}

// DriverMetrics is the gateway-side telemetry the upload driver emits on
// the §4.4 / §10.1 counters that the adapter no longer owns once the
// object-store call moved off the adapter. gatewaymetrics.Metrics
// satisfies it. A nil DriverMetrics disables the emissions.
//
// spec: §4.4 lines 254, 262; §10.1 supersede-on-write; §12.5 line 303.
type DriverMetrics interface {
	// IncCheckpointSizeExceeded stamps lenny_checkpoint_size_exceeded_total
	// when the adapter's workspace-size probe rejects the run.
	IncCheckpointSizeExceeded(pool, level string)
	// IncCheckpointStorageFailure stamps
	// lenny_checkpoint_storage_failure_total{reason="retry_exhausted"} when
	// a chunk PUT fails past the §4.4 retry budget.
	IncCheckpointStorageFailure(pool, level, trigger string)
	// IncCheckpointPartialManifestsSuperseded stamps
	// lenny_checkpoint_partial_manifests_superseded_total once per prior
	// aborted attempt this attempt supersedes.
	IncCheckpointPartialManifestsSuperseded(pool string)
	// IncCheckpointKMSUnavailable stamps
	// lenny_checkpoint_storage_failure_total{reason="kms_unavailable"} on a
	// kms:-coded object-store rejection.
	IncCheckpointKMSUnavailable()
}

// lockFlight acquires the per-(session, slot) single-flight lock so a
// periodic checkpoint and a concurrent seal of the same workspace cannot
// interleave their supersede / finalise manifest writes. The returned
// func releases the lock. The lock map is lazily populated so a
// zero-value Checkpointer needs no constructor.
//
// spec: §10.1 supersede-on-write (the supersede set must be stable for
// the duration of one attempt).
func (c *Checkpointer) lockFlight(sessionID, slotID string) func() {
	key := sessionID + "\x00" + slotID
	actual, _ := c.flights.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// chunkSize returns the gateway-chosen fixed chunk size, applying
// DefaultChunkSizeBytes when ChunkSizeBytes is unset.
func (c *Checkpointer) chunkSize() int64 {
	if c.ChunkSizeBytes > 0 {
		return c.ChunkSizeBytes
	}
	return DefaultChunkSizeBytes
}

// chunkEncoding returns the wire encoding the gateway declares in Start,
// defaulting to tar.
func (c *Checkpointer) chunkEncoding() partialmanifeststore.ChunkEncoding {
	if c.ChunkEncoding != "" {
		return partialmanifeststore.ChunkEncoding(c.ChunkEncoding)
	}
	return partialmanifeststore.ChunkEncodingTar
}

// levelLabel returns the level-label value stamped onto the driver's
// telemetry, defaulting to checkpoint.LevelBasic.
func (c *Checkpointer) levelLabel() string {
	if c.Level != "" {
		return string(c.Level)
	}
	return string(checkpoint.LevelBasic)
}

// errSizeLimit reports the adapter rejected the run at its workspace-size
// probe (a gRPC FailedPrecondition before any grant). The driver maps it
// onto lenny_checkpoint_size_exceeded_total and the checkpoint.skipped
// session event and writes no manifest row.
var errSizeLimit = errors.New("checkpointer: workspace size limit exceeded")

// pendingChunk is a ChunkReady declaration the driver has validated but
// deferred: minting its grant would exceed the outstanding-grant window, so
// it waits in FIFO order for an ack to free a window slot (spec: §10.1 line
// 131 — the gateway keeps at most `window` grants outstanding by pacing new
// grants against acks rather than aborting a producer that pipelines its
// declarations ahead).
type pendingChunk struct {
	index  uint32
	length int64
}

// uploadDriver runs one §10.1 gateway-side grant/confirm attempt on the
// open bidirectional Checkpoint stream. It reads the adapter's Probe,
// reserves quota, writes the intent row, mints a bounded window of
// presigned PUT capabilities in response to each ChunkReady, confirms
// each committed chunk off the grant's critical path, and finalises the
// manifest row on the terminal frame. Every abort arm finalises
// partial = true with its manifest_reason and releases the reservation
// the attempt did not keep.
type uploadDriver struct {
	c            *Checkpointer
	ctx          context.Context
	cancel       context.CancelFunc
	stream       adapterv1.Adapter_CheckpointClient
	tenantID     string
	sessionID    string
	slotID       string
	checkpointID string
	prefix       string
	trigger      checkpoint.Trigger
	window       int // §5.2 effective grant window: max outstanding grants
	// coordinationGen and recoveryGen are the session's fenced coordinator
	// generation and recovery counter at attempt time. onProbe writes them
	// into the §10.1 intent row so the supersede fence rejects a stale
	// coordinator and the resume selector picks the highest-generation row.
	// baselineBytes is the session's last_checkpoint_workspace_bytes frozen
	// as this attempt's resume-threshold denominator; nil denotes a session
	// with no prior successful full checkpoint.
	// spec: §10.1 lines 137, 155 (generation fence, resume selector, baseline).
	coordinationGen int64
	recoveryGen     int64
	baselineBytes   *int64

	// intentWritten records that onProbe committed the §10.1 intent row, so
	// onChunkReady may mint a chunk capability. It is written and read only on
	// the reader goroutine (onProbe then onChunkReady process sequentially),
	// so it needs no lock.
	intentWritten bool

	// sendMu serialises stream.Send so the main reader loop and a confirm
	// goroutine that latches an abort never issue two concurrent Sends,
	// which the gRPC client stream forbids.
	sendMu sync.Mutex

	// mu guards the confirm-side counters and the abort latch, which the
	// per-ack confirm goroutines mutate concurrently with the main reader.
	mu             sync.Mutex
	signedLen      map[uint32]int64 // per-index Content-Length signed into the grant
	acked          map[uint32]bool  // indices whose ChunkCommitted the reader saw
	outstanding    int              // granted-but-not-yet-acked index count
	pending        []pendingChunk   // declared chunks awaiting a free window slot
	confirmedBytes int64            // sum of Stat-confirmed chunk sizes
	confirmedCount int              // number of chunks Stat-confirmed
	reservedBytes  int64            // the reservation taken from the probe
	signedTotal    int64            // sum of Content-Lengths signed so far
	// quotaCap is the ceiling on signedTotal: the attempt's reservation plus
	// the tenant's remaining headroom (limit − prior usage). quotaCapSet is
	// false on the dev-mode / test path where no reservation is taken, which
	// disables the at-signing quota gate.
	quotaCap    int64
	quotaCapSet bool
	aborted     bool
	abortReason string
	abortDetail string
	confirmWG   sync.WaitGroup
}

// send issues one gateway → adapter frame under sendMu.
func (d *uploadDriver) send(msg *adapterv1.CheckpointClientMessage) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	return d.stream.Send(msg)
}

// run drives the attempt to its terminal frame and returns the
// gateway-minted checkpoint_id and the confirmed byte total on success,
// or a wrapped error on any abort arm.
func (d *uploadDriver) run() (string, int64, error) {
	d.signedLen = map[uint32]int64{}
	d.acked = map[uint32]bool{}
	for {
		msg, err := d.stream.Recv()
		if err != nil {
			return d.onStreamError(err)
		}
		switch {
		case msg.GetProbe() != nil:
			if perr := d.onProbe(msg.GetProbe()); perr != nil {
				return "", 0, perr
			}
		case msg.GetChunkReady() != nil:
			// onChunkReady latches an abort on a protocol violation and
			// lets the loop reach the terminal finalisation path when the
			// aborted stream closes, rather than returning without
			// finalising the intent row.
			d.onChunkReady(msg.GetChunkReady())
		case msg.GetChunkCommitted() != nil:
			d.onChunkCommitted(msg.GetChunkCommitted())
		case msg.GetSummary() != nil:
			return d.onSummary(msg.GetSummary())
		case msg.GetFailed() != nil:
			return d.onFailed(msg.GetFailed())
		}
	}
}

// onProbe reserves the probed workspace size against the tenant's storage
// quota and writes the §10.1 intent row (partial = true,
// manifest_reason = in_progress), superseding any prior aborted attempt
// for the same (session, slot) first.
func (d *uploadDriver) onProbe(p *adapterv1.CheckpointProbe) error {
	c := d.c
	if c.Manifests == nil {
		// No manifest store wired (dev-mode / degenerate test path): the
		// attempt still records the WorkspaceSnapshot from the Summary
		// total, but no intent row, reservation, or grant is minted.
		return nil
	}
	probed := p.GetWorkspaceBytes()
	if rerr := d.reserve(probed); rerr != nil {
		return rerr
	}
	if serr := d.supersedePriorAttempts(); serr != nil {
		d.releaseReservation()
		return serr
	}
	now := c.now()
	rec := partialmanifeststore.Record{
		TenantID:     d.tenantID,
		CheckpointID: d.checkpointID,
		SessionID:    d.sessionID,
		SlotID:       d.slotID,
		// spec: §10.1 lines 137, 155 — the intent row carries the session's
		// coordinator generation and recovery counter so the supersede fence
		// rejects a stale coordinator (an active row at a strictly higher
		// generation) and the resume path selects the highest-generation row.
		// BaselineFullCheckpointBytes freezes the session's
		// last_checkpoint_workspace_bytes as the resume-threshold denominator;
		// a nil pointer denotes no prior successful full checkpoint.
		CoordinationGeneration:      d.coordinationGen,
		RecoveryGeneration:          d.recoveryGen,
		BaselineFullCheckpointBytes: d.baselineBytes,
		Partial:                     true,
		ManifestReason:              partialmanifeststore.ReasonInProgress,
		ChunkObjectKeyPrefix:        d.prefix,
		ChunkSizeBytes:              c.chunkSize(),
		ChunkEncoding:               c.chunkEncoding(),
		ReservedBytes:               probed,
		CheckpointStartedAt:         now,
		CheckpointTimeoutAt:         now.Add(c.Deadline),
	}
	if err := c.Manifests.Put(d.ctx, rec); err != nil {
		d.releaseReservation()
		return fmt.Errorf("checkpointer: write intent row: %w", err)
	}
	// spec: §10.1 line 130 — only after the intent-row INSERT commits may the
	// gateway mint a chunk-upload capability, so no chunk object can be
	// orphaned under a checkpoint_id whose manifest row was never written.
	// onChunkReady fences on this flag rather than trusting adapter frame
	// order.
	d.intentWritten = true
	return nil
}

// reserve increments the tenant storage counter by the probed size,
// resolving the tenant's limit through QuotaLimitFor. A nil Quota or a
// nil QuotaLimitFor skips the reservation (dev-mode / test path).
func (d *uploadDriver) reserve(probed int64) error {
	c := d.c
	if c.Quota == nil || c.QuotaLimitFor == nil || probed <= 0 {
		return nil
	}
	limit, err := c.QuotaLimitFor(d.ctx, d.tenantID)
	if err != nil {
		return fmt.Errorf("checkpointer: resolve storage limit: %w", err)
	}
	prior, err := c.Quota.Reserve(d.ctx, d.tenantID, probed, limit)
	if err != nil {
		return fmt.Errorf("checkpointer: reserve checkpoint quota: %w", err)
	}
	// spec: §11.2 observation (2) — the sum of the attempt's signed
	// Content-Lengths may not carry the tenant counter past its limit. The
	// ceiling is the reservation plus the remaining tenant headroom
	// (limit − prior usage), which equals limit − prior. onChunkReady
	// enforces it before signing each grant.
	d.quotaCap = limit - prior
	d.quotaCapSet = true
	// Record the reserved size only on the path that actually incremented the
	// tenant counter, so a later release arm (reconcile, releaseReservation)
	// decrements exactly what this attempt reserved and never a size it never
	// took (spec: §11.2 line 35 — reservation release is exactly-once and
	// guarded).
	d.reservedBytes = probed
	return nil
}

// supersedePriorAttempts releases any prior aborted attempt's reservation
// while its row is still active, so the intent-row transaction that
// soft-deletes it does not orphan the reservation, and emits the §10.1
// supersede counter once per superseded row. The chunk-object sweep of a
// superseded attempt runs through the shared abort/backstop path.
func (d *uploadDriver) supersedePriorAttempts() error {
	c := d.c
	prior, err := c.Manifests.LatestActive(d.ctx, d.tenantID, d.sessionID)
	if err != nil {
		if errors.Is(err, partialmanifeststore.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("checkpointer: read prior active manifest: %w", err)
	}
	if prior.SlotID != d.slotID || prior.CheckpointID == d.checkpointID {
		return nil
	}
	// Release the superseded attempt's outstanding reservation and sweep
	// its chunk objects before Put soft-deletes the row inside the INSERT
	// transaction (spec: §10.1 supersede-on-write). The counter decrement is
	// applied only when the guarded UPDATE reports rows_affected == 1, so a
	// row whose reservation was already released (a retained 'timeout' resume
	// aid this attempt supersedes) reports rows_affected == 0 and is not
	// decremented a second time (spec: §11.2 line 35 — reservation release is
	// exactly-once and guarded).
	if rows, rerr := c.Manifests.ReleaseReservation(d.ctx, prior.TenantID, prior.CheckpointID); rerr == nil && rows == 1 && c.Quota != nil {
		_ = c.Quota.Adjust(d.ctx, prior.TenantID,
			-(prior.ReservedBytes - prior.WorkspaceBytesUploaded))
	}
	if c.ChunkDeleter != nil && prior.ChunkObjectKeyPrefix != "" {
		_, _ = c.ChunkDeleter.DeleteByPrefix(d.ctx, prior.ChunkObjectKeyPrefix)
	}
	if c.DriverMetrics != nil {
		c.DriverMetrics.IncCheckpointPartialManifestsSuperseded(c.Pool)
	}
	return nil
}

// onChunkReady validates the adapter-declared chunk length against the
// gateway-chosen chunk size, then either mints a single-key presigned PUT
// capability at that exact length immediately when the outstanding-grant
// window has a free slot or defers the declaration until an ack frees one.
// A grant-expiry retry re-mints the identical key and length. A declared
// length outside (0, chunk_size_bytes] aborts the attempt with
// stream_truncated before any capability is signed, so the bytes a
// capability can carry are a gateway constant rather than a pod
// self-report (spec: §10.1 line 139, §13.2 residual-exposure bound).
//
// The window is enforced by backpressure, not by aborting: when a fresh
// declaration would exceed the resolved window the driver queues it and
// mints its grant once onChunkCommitted frees a slot, so a producer that
// pipelines its declarations ahead of acks is paced rather than failed
// (spec: §10.1 line 131 — the gateway keeps at most `window` grants
// outstanding at a time).
func (d *uploadDriver) onChunkReady(cr *adapterv1.ChunkReady) {
	c := d.c
	index := cr.GetIndex()
	length := cr.GetLength()
	// spec: §10.1 line 130 — the gateway mints a chunk capability only after
	// the intent-row INSERT commits. A ChunkReady seen before the Probe wrote
	// the row (or on a manifest-disabled dev-mode path) gets no capability, so
	// no PUT can orphan an object under a checkpoint_id with no manifest row.
	if c.Manifests != nil && !d.intentWritten {
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("chunk %d ready before the intent row was written", index))
		return
	}
	if length <= 0 || length > c.chunkSize() {
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("declared chunk %d length %d outside (0, %d]", index, length, c.chunkSize()))
		return
	}
	if c.Presigner == nil {
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			"no presigner configured for chunk grant")
		return
	}
	d.mu.Lock()
	prior, reMint := d.signedLen[index]
	if reMint && prior != length {
		d.mu.Unlock()
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("chunk %d re-declared length %d != signed %d", index, length, prior))
		return
	}
	if reMint {
		// A grant-expiry retry (spec: §4.4) re-signs the identical key and
		// length without advancing the outstanding-grant count, taking a
		// second reservation, or occupying a fresh window slot.
		d.mu.Unlock()
		d.mintGrant(index, length, true)
		return
	}
	// A fresh index whose grant would exceed the resolved window waits for an
	// ack to free a slot: queue it in declaration order and let
	// onChunkCommitted mint it. Pacing the grant rather than aborting lets an
	// adapter pipeline its declarations ahead of acks.
	if d.window > 0 && d.outstanding >= d.window {
		d.pending = append(d.pending, pendingChunk{index: index, length: length})
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	d.mintGrant(index, length, false)
}

// mintGrant signs a single-key presigned PUT capability for chunk index at
// the exact signed Content-Length and sends it to the adapter. A fresh mint
// takes a window slot, advances the signed total, and refuses to sign a
// capability whose Content-Length would carry the attempt's granted total
// past its reservation plus the remaining tenant headroom (spec: §11.2
// observation (2)); a re-mint re-signs an already-granted index without
// re-accounting so a grant-expiry retry costs no extra reservation or window
// slot.
func (d *uploadDriver) mintGrant(index uint32, length int64, reMint bool) {
	c := d.c
	if !reMint {
		d.mu.Lock()
		// spec: §11.2 observation (2) — the refusal happens before signing, so
		// a pod that declares more in-range chunks than it probed is bounded
		// cumulatively rather than only per grant window.
		if d.quotaCapSet && d.signedTotal+length > d.quotaCap {
			d.mu.Unlock()
			d.abort(partialmanifeststore.ReasonQuotaExceeded,
				fmt.Sprintf("chunk %d granted total %d past quota cap %d", index, d.signedTotal+length, d.quotaCap))
			return
		}
		d.outstanding++
		d.signedTotal += length
		d.signedLen[index] = length
		d.mu.Unlock()
	}
	uri := d.chunkURI(index)
	grant, err := c.Presigner.PresignPut(uri, length, c.capabilityTTL())
	if err != nil {
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("sign chunk %d grant: %v", index, err))
		return
	}
	send := &adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Grant{
			Grant: &adapterv1.CheckpointGrant{
				Index:         index,
				Url:           grant.URL,
				ContentLength: length,
				Headers:       grant.Headers,
			},
		},
	}
	if serr := d.send(send); serr != nil {
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("send chunk %d grant: %v", index, serr))
	}
}

// popPendingLocked removes and returns the oldest deferred declaration under
// d.mu, reporting whether one was waiting. The caller drops the lock before
// minting so mintGrant can re-take it for the counter update.
func (d *uploadDriver) popPendingLocked() (pendingChunk, bool) {
	if len(d.pending) == 0 {
		return pendingChunk{}, false
	}
	next := d.pending[0]
	d.pending = d.pending[1:]
	return next, true
}

// onChunkCommitted confirms a committed chunk off the grant's critical
// path: it Stats the object for the bytes actually written, records the
// artifact_store catalog row, and advances the monotonic manifest
// counter. Confirmation never gates the next grant (spec: §10.1 line 131,
// §2 "confirmation does not gate the next grant"). A bytes-actually-written
// count larger than the signed Content-Length aborts the attempt and
// reconciles the excess (spec: §11.2, the quota-cap-without-server-
// enforcement path).
func (d *uploadDriver) onChunkCommitted(cc *adapterv1.ChunkCommitted) {
	index := cc.GetIndex()
	d.mu.Lock()
	if d.acked[index] {
		d.mu.Unlock()
		return
	}
	d.acked[index] = true
	// The ack releases the outstanding-grant slot (not the async confirm),
	// so a lagging Stat never blocks the next grant.
	if d.outstanding > 0 {
		d.outstanding--
	}
	signed := d.signedLen[index]
	// The freed slot admits the oldest deferred declaration: minting it here,
	// on the reader goroutine before the async confirm, is what paces the
	// pipeline. The (window+1)th grant is minted only once an earlier chunk's
	// ack frees a slot, and because the slot is released on the ack rather
	// than on the confirm, a lagging Stat never blocks it.
	next, hasNext := d.popPendingLocked()
	d.mu.Unlock()
	if hasNext {
		d.mintGrant(next.index, next.length, false)
	}
	if d.c.ObjectStore == nil {
		return
	}
	d.confirmWG.Add(1)
	go d.confirmChunk(index, signed)
}

// confirmChunk runs one chunk's Stat + RecordPut + ConfirmChunk.
func (d *uploadDriver) confirmChunk(index uint32, signed int64) {
	defer d.confirmWG.Done()
	c := d.c
	uri := d.chunkURI(index)
	info, err := c.ObjectStore.Stat(uri)
	if err != nil {
		// The object the ack claims is not present: treat as a truncated
		// stream so the terminal verification fails closed.
		d.abort(partialmanifeststore.ReasonStreamTruncated,
			fmt.Sprintf("stat chunk %d: %v", index, err))
		return
	}
	if c.Cataloging != nil {
		_ = c.Cataloging.RecordPut(uri, info.Size, chunkMimeType)
	}
	d.mu.Lock()
	d.confirmedBytes += info.Size
	d.confirmedCount++
	total := d.confirmedBytes
	d.mu.Unlock()
	if c.Manifests != nil {
		_ = c.Manifests.ConfirmChunk(d.ctx, d.tenantID, d.checkpointID, int(index), total)
	}
	// spec: §11.2 — a body larger than the signed Content-Length on a
	// non-enforcing backend is caught here, not by the object store. Abort
	// and reconcile the excess into the counter.
	if signed > 0 && info.Size > signed {
		d.abort(partialmanifeststore.ReasonQuotaExceeded,
			fmt.Sprintf("chunk %d wrote %d bytes past signed %d", index, info.Size, signed))
	}
}

// onSummary verifies every confirmed byte matches the adapter's declared
// total, finalises the manifest row complete (partial = false), and
// reconciles the reservation against the confirmed total.
func (d *uploadDriver) onSummary(s *adapterv1.CheckpointSummary) (string, int64, error) {
	d.confirmWG.Wait()
	d.mu.Lock()
	aborted := d.aborted
	reason := d.abortReason
	confirmedBytes := d.confirmedBytes
	confirmedCount := d.confirmedCount
	d.mu.Unlock()
	if aborted {
		return d.terminalAbort(reason)
	}
	// spec: §10.1 — the terminal summary finalises complete only when
	// every declared byte is Stat-confirmed. When no confirm seam is
	// wired (dev-mode), the adapter's reported total is authoritative.
	if d.c.ObjectStore != nil {
		if confirmedBytes != s.GetTotalBytes() || confirmedCount != int(s.GetChunkCount()) {
			return d.terminalAbort(partialmanifeststore.ReasonStreamTruncated)
		}
	}
	total := s.GetTotalBytes()
	if d.c.Manifests != nil {
		if err := d.c.Manifests.Finalise(d.ctx, d.tenantID, d.checkpointID,
			false, partialmanifeststore.ReasonComplete); err != nil {
			return "", 0, fmt.Errorf("checkpointer: finalise complete: %w", err)
		}
		d.reconcile(d.ctx, confirmedBytes)
	}
	return d.checkpointID, total, nil
}

// onFailed maps a terminal CheckpointFailed frame. A kms:-coded rejection
// is a §12.5 classification-control violation: fire the telemetry hook and
// abort without retry. Every other rejection is a retry-exhausted storage
// failure.
func (d *uploadDriver) onFailed(f *adapterv1.CheckpointFailed) (string, int64, error) {
	c := d.c
	d.confirmWG.Wait()
	if strings.HasPrefix(f.GetErrorCode(), "kms:") {
		if c.DriverMetrics != nil {
			c.DriverMetrics.IncCheckpointKMSUnavailable()
		}
		if err := d.finaliseAbort(partialmanifeststore.ReasonStreamTruncated); err != nil {
			return "", 0, err
		}
		return "", 0, fmt.Errorf("checkpointer: chunk %d rejected: %s (%s): %w",
			f.GetIndex(), f.GetReason(), f.GetErrorCode(), blobstore.ErrClassificationControlViolation)
	}
	if c.DriverMetrics != nil {
		c.DriverMetrics.IncCheckpointStorageFailure(c.Pool, c.levelLabel(), string(d.trigger))
	}
	if err := d.finaliseAbort(partialmanifeststore.ReasonStreamTruncated); err != nil {
		return "", 0, err
	}
	return "", 0, fmt.Errorf("checkpointer: adapter checkpoint failed: %s (http %d, code %q)",
		f.GetReason(), f.GetHttpStatus(), f.GetErrorCode())
}

// onStreamError maps a transport-level stream error. A FailedPrecondition
// is the adapter's workspace-size-probe rejection, which writes no
// manifest row; a deadline fire finalises the intent row with
// manifest_reason = 'timeout' so its row and chunks are retained for the
// resume path; every other error is a stream truncation that finalises any
// intent row partial = true and sweeps its chunks.
func (d *uploadDriver) onStreamError(err error) (string, int64, error) {
	c := d.c
	if isSizeLimitReject(err) {
		if c.DriverMetrics != nil {
			c.DriverMetrics.IncCheckpointSizeExceeded(c.Pool, c.levelLabel())
		}
		if c.SkippedEventFunc != nil {
			c.SkippedEventFunc(d.ctx, d.tenantID, d.sessionID, "workspace_size_limit")
		}
		return "", 0, fmt.Errorf("%w: %v", errSizeLimit, err)
	}
	d.confirmWG.Wait()
	// spec: §10.1 line 132 / §4.4 line 235 — a deadline fire is the recovery
	// aid: finalise 'timeout' so finaliseAbort retains the row and chunks the
	// resume path reassembles rather than sweeping them. A latched abort (a
	// protocol violation or an over-size confirm) cancels the stream with
	// context.Canceled, which this deadline check does not match, so those
	// arms keep their own reason.
	reason := partialmanifeststore.ReasonStreamTruncated
	if isDeadline(err) || errors.Is(d.ctx.Err(), context.DeadlineExceeded) {
		reason = partialmanifeststore.ReasonTimeout
	}
	if ferr := d.finaliseAbort(reason); ferr != nil {
		return "", 0, ferr
	}
	return "", 0, fmt.Errorf("checkpointer: recv checkpoint frame: %w", err)
}

// abort latches the first abort reason, sends the adapter a
// CheckpointAbort so it stops retrying, and cancels the stream. It does
// not finalise the manifest row: the terminal frame handler runs
// finaliseAbort once the reader loop unwinds on the cancelled stream, so
// the intent row is finalised exactly once regardless of whether the abort
// was raised by the reader (a protocol violation) or by a confirm
// goroutine (an over-size chunk).
func (d *uploadDriver) abort(reason, detail string) {
	d.mu.Lock()
	if !d.aborted {
		d.aborted = true
		d.abortReason = reason
		d.abortDetail = detail
	}
	d.mu.Unlock()
	_ = d.send(&adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Abort{
			Abort: &adapterv1.CheckpointAbort{Reason: reason},
		},
	})
	if d.cancel != nil {
		d.cancel()
	}
}

// terminalAbort finalises the aborted intent row and returns the terminal
// abort error so the whole checkpoint fails. It is the Summary-path
// wrapper around finaliseAbort: onSummary reaches it when a confirm
// goroutine latched an abort before the terminal frame, so the attempt
// must not report success.
func (d *uploadDriver) terminalAbort(reason string) (string, int64, error) {
	if err := d.finaliseAbort(reason); err != nil {
		return "", 0, err
	}
	d.mu.Lock()
	latched := d.abortReason
	detail := d.abortDetail
	d.mu.Unlock()
	if latched != "" {
		reason = latched
	}
	if detail != "" {
		return "", 0, fmt.Errorf("checkpointer: checkpoint aborted (%s): %s", reason, detail)
	}
	return "", 0, fmt.Errorf("checkpointer: checkpoint aborted: %s", reason)
}

// finaliseAbort finalises the intent row partial = true with reason and
// releases the reservation the attempt did not keep, then sweeps the
// chunk objects the aborted attempt wrote. It returns only a store error;
// the caller constructs the terminal abort error.
func (d *uploadDriver) finaliseAbort(reason string) error {
	c := d.c
	if c.Manifests == nil {
		return nil
	}
	d.mu.Lock()
	// The first latched abort reason wins: an over-size confirm that
	// latched quota_exceeded must not be overwritten by the stream_truncated
	// the reader's terminal path passes when the cancelled stream errors.
	if d.aborted && d.abortReason != "" {
		reason = d.abortReason
	} else {
		d.aborted = true
		d.abortReason = reason
	}
	confirmed := d.confirmedBytes
	d.mu.Unlock()
	// Commit the durable finalisation, reservation release, and chunk sweep
	// on a context detached from the attempt's already-cancelled or
	// deadline-expired d.ctx. Against a store that honours ctx these writes
	// would otherwise fail on the dead context, leaving the row
	// manifest_reason='in_progress' with a leaked reservation and unswept
	// chunks (spec: §10.1 line 132 — every abort arm finalises partial=true
	// and releases the reservation the attempt did not keep).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(d.ctx), cleanupTimeout)
	defer cancel()
	if err := c.Manifests.Finalise(ctx, d.tenantID, d.checkpointID, true, reason); err != nil {
		return fmt.Errorf("checkpointer: finalise partial (%s): %w", reason, err)
	}
	d.reconcile(ctx, confirmed)
	// A deadline-fire (timeout) row is retained for the resume path; every
	// other abort arm's chunks are swept because no resume will consume
	// them (spec: §10.1 line 157 / §3 abort-arm sweep).
	if reason != partialmanifeststore.ReasonTimeout && c.ChunkDeleter != nil && d.prefix != "" {
		_, _ = c.ChunkDeleter.DeleteByPrefix(ctx, d.prefix)
	}
	return nil
}

// reconcile releases the unconfirmed portion of the reservation exactly
// once, so an aborted checkpoint charges the tenant only for the chunks
// it confirmed and a complete one for its confirmed total. The counter
// decrement is applied only when the guarded UPDATE reports
// rows_affected == 1 (spec: §11.2 line 35 — reservation release is
// exactly-once and guarded). The caller passes the context the durable
// write runs on: the live attempt context on the complete path, a detached
// cleanup context on an abort arm whose d.ctx is already cancelled.
func (d *uploadDriver) reconcile(ctx context.Context, confirmed int64) {
	c := d.c
	rows, err := c.Manifests.ReleaseReservation(ctx, d.tenantID, d.checkpointID)
	if err != nil || rows != 1 {
		return
	}
	// The guarded UPDATE above clears the row's reservation flag regardless.
	// The counter decrement fires only when this attempt actually took a
	// reservation: a config with Quota wired but QuotaLimitFor nil disables
	// reservation by contract, so there is nothing to release even though the
	// row carried a probed reserved_bytes.
	if !d.quotaCapSet {
		return
	}
	_ = c.Quota.Adjust(ctx, d.tenantID, -(d.reservedBytes - confirmed))
}

// releaseReservation releases the reservation on an early abort (a supersede
// or intent-row Put failure) before the intent row's counters are
// established. It decrements only when this attempt actually took a
// reservation, matching the reserve() predicate: a config with Quota wired
// but QuotaLimitFor nil disables reservation by contract, so no Reserve ran
// and there is nothing to release (spec: §11.2 line 35 — reservation release
// is exactly-once and guarded).
func (d *uploadDriver) releaseReservation() {
	if !d.quotaCapSet {
		return
	}
	_ = d.c.Quota.Adjust(d.ctx, d.tenantID, -d.reservedBytes)
}

// chunkURI builds the object URI for chunk index under this attempt's
// checkpoint prefix. The basename is `chunk-{n}.{chunk_encoding}` where
// {n} is a zero-padded 5-digit monotonic index starting at `00000`, so the
// signed key matches the §10.1 chunk-object layout the resume/reassembly
// path enumerates and lexicographic list order tracks index order.
// spec: §10.1 line 139 (zero-padded 5-digit chunk index).
func (d *uploadDriver) chunkURI(index uint32) blobstore.URI {
	return blobstore.URI{
		TenantID:   d.tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  d.sessionID,
		PartID:     fmt.Sprintf("%s/chunk-%05d.%s", d.checkpointID, index, d.c.chunkEncoding()),
		TTL:        d.c.capabilityTTL(),
	}
}

// isSizeLimitReject reports whether err is the adapter's gRPC
// FailedPrecondition workspace-size-probe rejection, which the adapter
// returns before any grant is minted (spec: §4.4 line 255).
func isSizeLimitReject(err error) bool {
	return status.Code(err) == codes.FailedPrecondition
}

// isDeadline reports whether err is a gRPC DeadlineExceeded status, the
// error the client stream returns when the attempt's own deadline fires.
func isDeadline(err error) bool {
	return status.Code(err) == codes.DeadlineExceeded
}
