// SPDX-License-Identifier: MIT

// Package evictionfallback implements the §4.4 lines 263–289 eviction
// fallback writer. When an eviction checkpoint cannot persist the
// workspace to MinIO (all retry attempts exhausted), the gateway falls
// back to the Postgres minimal-state record. This package owns:
//
//   - The 2 KB inline / MinIO key chooser per §4.4 line 271. Small
//     last-message contexts are stored inline as a TEXT column; large
//     contexts are uploaded to MinIO at
//     `/{tenant_id}/eviction/{session_id}/context` and the row carries
//     the object key.
//   - The MinIO-unavailable degradation per §4.4 line 271: truncate
//     the context to 2 KB and store it inline with
//     `context_truncated: true`.
//   - The §4.4 lines 283–289 total-loss path: when both stores fail,
//     emit the `lenny_session_eviction_total_loss_total` counter, log
//     `CRITICAL`, and best-effort-publish a `session.lost` event with
//     `reason: "eviction_total_loss"`.
//
// The §4.4 line 277 Postgres-fallback retry budget (60 s) is the
// responsibility of F-4.4.14 (a follow-on batch). This package
// presents the writer interface and the total-loss orchestration; the
// retry wrapper layers on top.
//
// spec: §4.4 lines 263–291.
package evictionfallback

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
)

// MaxInlineContextBytes is the §4.4 line 271 2 KB cutoff for the
// inline-vs-MinIO chooser. A context ≤ this threshold is stored
// inline as TEXT; a larger context is uploaded to MinIO and the row
// carries the object key.
//
// spec: §4.4 line 271 — "for contexts ≤ 2KB the value is stored
// inline".
const MaxInlineContextBytes = 2 * 1024

// MaxTruncatedContextBytes is the §4.4 line 271 truncation cap when
// MinIO is unavailable. The writer truncates the context to this size
// and stores it inline with `context_truncated: true` so the resume
// path can detect partial context. The §4.4 spec wording "truncated
// to 2KB" pairs the truncation size with the inline threshold so the
// row stays small under the same TOAST-avoidance constraint.
//
// spec: §4.4 line 271 — "On MinIO unavailability during a fallback
// write, the context is truncated to 2KB and stored inline".
const MaxTruncatedContextBytes = MaxInlineContextBytes

// MaxOriginalContextBytes is the §4.4 line 271 original context-size
// ceiling. The writer rejects payloads larger than this before
// touching either store so a runaway producer cannot drive the
// fallback path into unbounded buffering.
//
// spec: §4.4 line 271 — "The original context may reach up to 64KB
// before truncation".
const MaxOriginalContextBytes = 64 * 1024

// ContextObjectUploader uploads the eviction-context object to MinIO
// when the context exceeds MaxInlineContextBytes. The writer hands
// the implementation the canonical object key
// (`/{tenant_id}/eviction/{session_id}/context`) and the context
// bytes; the implementation returns nil on success, or an error to
// trigger the §4.4 line 271 MinIO-unavailable degradation.
//
// The interface is narrow so unit tests can stub it without pulling
// in a S3 client. The production wiring lives in pkg/blobstore.
//
// spec: §4.4 line 271 — MinIO object key path.
type ContextObjectUploader interface {
	// Upload writes the supplied bytes to MinIO at the canonical
	// eviction-context key for (tenant, session). Returns nil on
	// success, an error on storage failure (network blip, leader
	// election, permission). Implementations are expected to be
	// idempotent against repeated writes of the same key.
	Upload(ctx context.Context, tenantID, sessionID string, body io.Reader, sizeBytes int) error
}

// MetricsSink receives the §4.4 lines 283–289 total-loss counter
// increment. The gatewaymetrics.Metrics satisfies it. Nil is
// permitted; the writer still runs.
//
// spec: §4.4 line 286 — `lenny_session_eviction_total_loss_total`
// counter, labels (`pool`, `had_prior_checkpoint`).
type MetricsSink interface {
	// IncSessionEvictionTotalLoss bumps the
	// `lenny_session_eviction_total_loss_total` counter labeled by
	// pool and had_prior_checkpoint. Called only on the §4.4 lines
	// 283–289 total-loss path (both MinIO and Postgres failed).
	IncSessionEvictionTotalLoss(pool string, hadPriorCheckpoint bool)
}

// EventEmitter publishes the best-effort §4.4 line 285 `session.lost`
// event onto the session's event stream. The gateway-side wiring uses
// the existing sessionevents.Bus; nil makes the emit a no-op (logged
// only).
//
// spec: §4.4 line 285 — "Emit a session.lost event on the session's
// event stream with reason: 'eviction_total_loss'".
type EventEmitter interface {
	// EmitSessionLost publishes a `session.lost` event with the
	// supplied reason and the diagnostic fields. Best-effort: when
	// the event stream is unavailable the implementation logs and
	// returns; the total-loss path proceeds regardless.
	EmitSessionLost(ctx context.Context, sessionID, reason string, fields map[string]any)
}

// ContextEncoder turns the writer's per-call inputs (a Record that
// captures the cursor + generations + workspace_lost flag plus the
// raw context bytes) into the persisted Record value the
// EvictionStateStore writes. The encoder is split out so the
// chooser-logic can be unit-tested in isolation from the store
// surface.
type ContextEncoder struct{}

// EncodeInline produces the Record carrying the supplied context as
// inline TEXT (≤ MaxInlineContextBytes). The IsMinIOKey flag is left
// false; ContextTruncated is set when the caller asks the encoder to
// flag a truncation result.
func (ContextEncoder) EncodeInline(template evictionstatestore.Record, context []byte, truncated bool) evictionstatestore.Record {
	out := template
	out.LastMessageContext = append([]byte(nil), context...)
	out.IsMinIOKey = false
	out.ContextTruncated = truncated
	return out
}

// EncodeMinIOKey produces the Record carrying the canonical MinIO
// object key for the eviction-context object. The IsMinIOKey flag is
// set true so the §12.5 GC sweep deletes the object alongside the
// row.
func (ContextEncoder) EncodeMinIOKey(template evictionstatestore.Record, key string) evictionstatestore.Record {
	out := template
	out.LastMessageContext = []byte(key)
	out.IsMinIOKey = true
	out.ContextTruncated = false
	return out
}

// EvictionContextObjectKey returns the §4.4 line 271 canonical MinIO
// object key for the supplied (tenant, session): the path the
// uploader writes to and the row stores as the LastMessageContext
// value when the context is large enough for the MinIO path.
//
// spec: §4.4 line 271 — `/{tenant_id}/eviction/{session_id}/context`.
func EvictionContextObjectKey(tenantID, sessionID string) string {
	return "/" + tenantID + "/eviction/" + sessionID + "/context"
}

// WriteParams collects the per-call inputs to Writer.Write. The
// writer combines the gateway-side template (tenant, session,
// generations, cursor, evicted-at) with the raw context bytes and
// the optional pool / prior-checkpoint signals used for the
// total-loss telemetry.
type WriteParams struct {
	// Record is the §12.2.1 row template carrying the tenant,
	// session, generations, cursor, and evicted-at fields. The
	// writer derives LastMessageContext, IsMinIOKey, and
	// ContextTruncated from the encoder and overwrites them on the
	// returned row. WorkspaceLost is forced true by construction
	// (the §4.4 minimal-state record always represents a lost
	// workspace).
	Record evictionstatestore.Record

	// Context is the raw last-message-context payload to persist.
	// The writer chooses inline vs MinIO based on its length.
	Context []byte

	// Pool is the pool-label value stamped onto the total-loss
	// telemetry. Empty omits the label.
	Pool string

	// HadPriorCheckpoint is the second total-loss-metric label.
	// Distinguishes evictions that had at least one durable
	// checkpoint (resumable from an earlier snapshot) from those
	// without (complete data loss).
	HadPriorCheckpoint bool

	// MinIOError, when set, is the error summary the writer
	// includes on the §4.4 line 285 session.lost event when the
	// total-loss path fires. This is the prior MinIO error from the
	// failed eviction-checkpoint storage attempt that drove the
	// fallback.
	MinIOError error
}

// Writer is the §4.4 line 271 eviction-fallback writer. It chooses
// inline vs MinIO storage for the last-message context, executes the
// Postgres write, and on a Postgres failure drives the §4.4 lines
// 283–289 total-loss orchestration.
//
// The writer is configuration-bag-style so the gateway can wire each
// dependency independently. A nil ContextUploader forces the
// truncation-on-failure path even for in-bounds contexts that would
// otherwise prefer MinIO (treated as MinIO-unavailable).
type Writer struct {
	// Store is the EvictionStateStore the writer persists rows to.
	// Required.
	Store evictionstatestore.Store
	// ContextUploader uploads the eviction-context object when the
	// context exceeds MaxInlineContextBytes. Nil forces the
	// truncation-on-failure path so dev-mode deployments without an
	// uploader still produce a Postgres row.
	ContextUploader ContextObjectUploader
	// Metrics receives the total-loss counter increment. Nil is
	// permitted; the writer still runs.
	Metrics MetricsSink
	// Events publishes the best-effort session.lost event on the
	// total-loss path. Nil is permitted; the writer logs the loss
	// regardless.
	Events EventEmitter
	// Encoder produces the persisted Record from the writer's
	// per-call inputs. The zero ContextEncoder is acceptable.
	Encoder ContextEncoder
}

// Outcome enumerates the §4.4 line 271 writer outcomes.
type Outcome string

const (
	// OutcomeInline: context ≤ 2 KB, stored inline.
	OutcomeInline Outcome = "inline"
	// OutcomeMinIOKey: context > 2 KB, uploaded to MinIO and the row
	// stores the object key.
	OutcomeMinIOKey Outcome = "minio_key"
	// OutcomeTruncated: context > 2 KB but MinIO was unavailable;
	// the writer truncated to 2 KB and stored inline with
	// `context_truncated: true`.
	OutcomeTruncated Outcome = "truncated"
	// OutcomeTotalLoss: both stores failed; the §4.4 lines 283–289
	// total-loss path fired.
	OutcomeTotalLoss Outcome = "total_loss"
)

// Result reports the outcome of a single Write call. Outcome is
// always set; PersistedRecord carries the row the writer wrote to
// the EvictionStateStore (empty on OutcomeTotalLoss).
type Result struct {
	Outcome         Outcome
	PersistedRecord evictionstatestore.Record
}

// Write executes the §4.4 line 271 eviction-fallback writer for the
// supplied params:
//
//  1. If len(Context) ≤ MaxInlineContextBytes: encode the Record
//     inline and write to the EvictionStateStore. On a Postgres
//     failure, fire the §4.4 lines 283–289 total-loss path.
//  2. If len(Context) > MaxInlineContextBytes (capped at
//     MaxOriginalContextBytes): try to upload the context to MinIO at
//     the canonical key.
//     - On success: encode the Record with the MinIO key and write
//       to the EvictionStateStore.
//     - On MinIO failure: degrade to the truncation path —
//       truncate to MaxTruncatedContextBytes, set
//       ContextTruncated=true, write inline. On a Postgres failure,
//       fire the total-loss path.
//  3. WorkspaceLost is forced to true on every persisted record so
//     the §7.2 resume path observes a consistent signal.
//
// spec: §4.4 lines 263–291.
func (w *Writer) Write(ctx context.Context, p WriteParams) (Result, error) {
	if w == nil || w.Store == nil {
		return Result{}, errors.New("evictionfallback: writer requires a Store")
	}
	if p.Record.TenantID == "" || p.Record.SessionID == "" {
		return Result{}, errors.New("evictionfallback: tenant and session ids are required")
	}
	template := p.Record
	template.WorkspaceLost = true
	body := p.Context
	if len(body) > MaxOriginalContextBytes {
		body = body[:MaxOriginalContextBytes]
	}

	if len(body) <= MaxInlineContextBytes {
		row := w.Encoder.EncodeInline(template, body, false)
		if err := w.Store.Put(ctx, row); err != nil {
			return w.driveTotalLoss(ctx, template, p, err), err
		}
		return Result{Outcome: OutcomeInline, PersistedRecord: row}, nil
	}

	// Large context — try MinIO first.
	if w.ContextUploader != nil {
		key := EvictionContextObjectKey(template.TenantID, template.SessionID)
		uploadErr := w.ContextUploader.Upload(ctx, template.TenantID, template.SessionID,
			strings.NewReader(string(body)), len(body))
		if uploadErr == nil {
			row := w.Encoder.EncodeMinIOKey(template, key)
			if err := w.Store.Put(ctx, row); err != nil {
				return w.driveTotalLoss(ctx, template, p, err), err
			}
			return Result{Outcome: OutcomeMinIOKey, PersistedRecord: row}, nil
		}
		// MinIO refused the write — fall through to the truncation path.
	}

	// MinIO unavailable (or no uploader wired): truncate to the
	// inline cap and persist with the truncation flag so the resume
	// path can render partial context awareness.
	truncated := body
	if len(truncated) > MaxTruncatedContextBytes {
		truncated = truncated[:MaxTruncatedContextBytes]
	}
	row := w.Encoder.EncodeInline(template, truncated, true)
	if err := w.Store.Put(ctx, row); err != nil {
		return w.driveTotalLoss(ctx, template, p, err), err
	}
	return Result{Outcome: OutcomeTruncated, PersistedRecord: row}, nil
}

// driveTotalLoss executes the §4.4 lines 283–289 total-loss
// orchestration: emit the counter, log CRITICAL, best-effort-publish
// the session.lost event. The caller still returns the underlying
// Postgres error so its retry / failover policy can re-evaluate.
//
// spec: §4.4 lines 283–289.
func (w *Writer) driveTotalLoss(ctx context.Context, template evictionstatestore.Record, p WriteParams, pgErr error) Result {
	if w.Metrics != nil {
		w.Metrics.IncSessionEvictionTotalLoss(p.Pool, p.HadPriorCheckpoint)
	}
	minioSummary := ""
	if p.MinIOError != nil {
		minioSummary = p.MinIOError.Error()
	}
	pgSummary := ""
	if pgErr != nil {
		pgSummary = pgErr.Error()
	}
	// spec: §4.4 line 287 — "Log a CRITICAL-level structured message
	// including session_id, tenant_id, generation, evicted_at, and the
	// errors from both the MinIO retry exhaustion and the Postgres
	// write failure".
	log.Printf("CRITICAL: session.lost reason=eviction_total_loss tenant=%s session=%s generation=%d evicted_at=%s minio_error=%q postgres_error=%q",
		template.TenantID, template.SessionID, template.RecoveryGeneration,
		template.EvictedAt.Format("2006-01-02T15:04:05Z07:00"), minioSummary, pgSummary)
	if w.Events != nil {
		w.Events.EmitSessionLost(ctx, template.SessionID, "eviction_total_loss",
			map[string]any{
				"session_id":     template.SessionID,
				"tenant_id":      template.TenantID,
				"generation":     template.RecoveryGeneration,
				"evicted_at":     template.EvictedAt,
				"minio_error":    minioSummary,
				"postgres_error": pgSummary,
			})
	}
	return Result{Outcome: OutcomeTotalLoss}
}

