// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// This file holds the per-phase steps Run dispatches to and the guarded
// job writes they make. Each step takes the job as the runner last read or
// wrote it and returns the job its last write returned, so the next guard
// compares against the attempt this runner holds.
//
// spec: §12.8 (Erasure job phase field (crash recovery)).

// write applies mutate to the stored job only while SameAttempt holds
// between the stored job and read, and returns ErrJobMoved, writing
// nothing, otherwise. It also refuses a writer that read the job before
// another executor recorded the erasers done: recording ErasedAt does not
// always change the phase, so without this check a stale executor of the
// same attempt could rewrite ErasedAt after the receipt was built from it.
func (r *Runner) write(ctx context.Context, read Job, mutate func(*Job)) (Job, error) {
	return r.jobs.Update(ctx, read.ID, func(j *Job) error {
		if !SameAttempt(*j, read) || (!j.ErasedAt.IsZero() && read.ErasedAt.IsZero()) {
			return ErrJobMoved
		}
		mutate(j)
		return nil
	})
}

// advance moves the job to phase p, appends the transition to PhaseLog,
// and applies extra, when non-nil, in the same guarded write.
func (r *Runner) advance(ctx context.Context, read Job, p Phase, extra func(*Job)) (Job, error) {
	return r.write(ctx, read, func(j *Job) {
		if extra != nil {
			extra(j)
		}
		j.Phase = p
		j.PhaseLog = append(j.PhaseLog, PhaseTransition{Phase: p, At: r.clock()})
	})
}

// fail transitions the job to PhaseFailed with cause as the recorded
// failure reason, increments the failure counter under failurePhase, and
// observes the job's duration, all only when the guarded write applied.
// extra, when non-nil, records a partial result in the same write. When ctx
// has ended, fail writes nothing and returns cause, so the job stays in its
// persisted phase for the next leader. A write refused by the guard returns
// ErrJobMoved with no counter increment.
func (r *Runner) fail(ctx context.Context, read Job, failurePhase string, cause error, extra func(*Job)) error {
	if ctx.Err() != nil {
		return cause
	}
	job, err := r.write(ctx, read, func(j *Job) {
		if extra != nil {
			extra(j)
		}
		j.Phase = PhaseFailed
		j.PhaseLog = append(j.PhaseLog, PhaseTransition{Phase: PhaseFailed, At: r.clock()})
		j.Failure = cause.Error()
		j.CompletedAt = r.clock()
	})
	if err != nil {
		return err
	}
	if r.onFailure != nil {
		r.onFailure(job.TenantID, failurePhase)
	}
	r.observeDuration(job)
	return cause
}

// observeDuration records the job's wall-clock duration.
func (r *Runner) observeDuration(j Job) {
	if r.lifecycle != nil {
		r.lifecycle.ObserveErasureJobDuration(r.clock().Sub(j.StartedAt).Seconds())
	}
}

// runInitiated runs the MemoryStore preflight, the session drain, and the
// legal-hold re-check, then advances to store_deleting. Every failure here
// leaves the restriction set and no store deleted.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (DeleteByUser dependency-ordered deletion sequence).
func (r *Runner) runInitiated(ctx context.Context, j Job) (Job, error) {
	if r.memoryPreflight != nil {
		if err := r.memoryPreflight(ctx); err != nil {
			return j, r.fail(ctx, j, FailurePhaseMemoryStorePreflight,
				fmt.Errorf("%w: %v", errMemoryStorePreflight, err), nil)
		}
	}
	if err := r.runDrain(ctx, j); err != nil {
		return j, r.fail(ctx, j, FailurePhaseStoreDelete, err, nil)
	}
	if err := r.recheckHolds(ctx, j); err != nil {
		return j, r.fail(ctx, j, FailurePhaseStoreDelete, err, nil)
	}
	return r.advance(ctx, j, PhaseStoreDeleting, nil)
}

// runDrain runs the session drain hook and logs how long it took.
func (r *Runner) runDrain(ctx context.Context, j Job) error {
	if r.drain == nil {
		return ErrSessionDrainUnwired
	}
	started := r.clock()
	err := r.drain(ctx, j.TenantID, j.UserID)
	r.logger.InfoContext(ctx, "erasure job session drain finished",
		"job_id", j.ID, "tenant_id", j.TenantID,
		"elapsed_seconds", r.clock().Sub(started).Seconds(), "ok", err == nil)
	if err != nil {
		return fmt.Errorf("session drain: %w", err)
	}
	return nil
}

// recheckHolds runs the legal-hold re-check and returns an error naming
// every hold the job's override does not exempt.
func (r *Runner) recheckHolds(ctx context.Context, j Job) error {
	if r.holdCheck == nil {
		return ErrHoldCheckUnwired
	}
	holds, err := r.holdCheck(ctx, j.TenantID, j.UserID)
	if err != nil {
		return fmt.Errorf("legal-hold re-check: %w", err)
	}
	exempt := overriddenHolds(j)
	var blocking []string
	for _, h := range holds {
		if !exempt[h] {
			blocking = append(blocking, h.ResourceType+":"+h.ResourceID)
		}
	}
	if len(blocking) > 0 {
		return fmt.Errorf("legal-hold re-check: %d hold(s) block the erasure: %s",
			len(blocking), strings.Join(blocking, ", "))
	}
	return nil
}

// overriddenHolds returns the holds the job's OverrideReceipt lists, or
// none once the job has failed: a retry ran its own preflight with no
// override, so a retried job exempts no hold. It reads both the form the
// erase handler builds and the form a JSON round trip of the job returns.
func overriddenHolds(j Job) map[Hold]bool {
	for _, t := range j.PhaseLog {
		if t.Phase == PhaseFailed {
			return nil
		}
	}
	out := map[Hold]bool{}
	add := func(m map[string]any) {
		rt, _ := m["resourceType"].(string)
		id, _ := m["resourceId"].(string)
		if rt != "" && id != "" {
			out[Hold{ResourceType: rt, ResourceID: id}] = true
		}
	}
	switch hs := j.OverrideReceipt["holds"].(type) {
	case []map[string]any:
		for _, m := range hs {
			add(m)
		}
	case []any:
		for _, v := range hs {
			if m, ok := v.(map[string]any); ok {
				add(m)
			}
		}
	}
	return out
}

// runStoreDeleting runs DeleteByUser and the dead-letter redaction,
// merging each store's count into Deleted with a per-store maximum so a
// re-run that deletes nothing never lowers a recorded count. Without a
// BillingEraser it records the erasers done; otherwise it advances to
// pseudonymizing.
func (r *Runner) runStoreDeleting(ctx context.Context, j Job) (Job, error) {
	res, eraseErr := r.erase.DeleteByUser(ctx, j.TenantID, j.UserID)
	j, err := r.write(ctx, j, func(w *Job) { mergeDeleted(w, res.Deleted) })
	if err != nil {
		return j, err
	}
	if eraseErr != nil {
		return j, r.fail(ctx, j, FailurePhaseStoreDelete, eraseErr, nil)
	}
	redacted := 0
	if r.deadLetterRedact != nil {
		n, rerr := r.deadLetterRedact(ctx, j.TenantID, j.UserID)
		if rerr != nil {
			return j, r.fail(ctx, j, FailurePhaseDeadLetterRedaction, rerr, nil)
		}
		redacted = n
	}
	recordRedacted := func(w *Job) {
		if redacted > w.DeadLetterRedacted {
			w.DeadLetterRedacted = redacted
		}
	}
	if r.billing == nil {
		return r.write(ctx, j, func(w *Job) {
			recordRedacted(w)
			w.ErasedAt = r.clock()
		})
	}
	return r.advance(ctx, j, PhasePseudonymizing, recordRedacted)
}

// mergeDeleted merges counts into j.Deleted with a per-store maximum and
// recomputes Total from the merged map.
func mergeDeleted(j *Job, counts map[string]int) {
	if j.Deleted == nil && len(counts) > 0 {
		j.Deleted = make(map[string]int, len(counts))
	}
	for store, n := range counts {
		if n > j.Deleted[store] {
			j.Deleted[store] = n
		}
	}
	total := 0
	for _, n := range j.Deleted {
		total += n
	}
	j.Total = total
}

// runPseudonymizing runs Pseudonymize unless the job already recorded its
// billing outcome, and persists the outcome in the write that advances to
// verifying, or, for an exempt tenant, in the write that records the
// erasers done.
func (r *Runner) runPseudonymizing(ctx context.Context, j Job) (Job, error) {
	billing := j.Billing
	if billing.Disposition == "" {
		if r.billing == nil {
			return j, r.fail(ctx, j, FailurePhasePseudonymization, ErrBillingUnwired, nil)
		}
		var err error
		billing, err = r.billing.Pseudonymize(ctx, j.TenantID, j.UserID)
		if err != nil {
			return j, r.fail(ctx, j, FailurePhasePseudonymization, err, nil)
		}
	}
	if billing.Disposition == billingExempt {
		return r.write(ctx, j, func(w *Job) {
			w.Billing = billing
			w.ErasedAt = r.clock()
		})
	}
	return r.advance(ctx, j, PhaseVerifying, recordBilling(billing))
}

// runVerifying re-runs the §12.8 post-pseudonymization check and records
// the erasers done when it passes.
func (r *Runner) runVerifying(ctx context.Context, j Job) (Job, error) {
	if r.billing == nil {
		return j, r.fail(ctx, j, FailurePhaseVerification, ErrBillingUnwired, nil)
	}
	verified, err := r.billing.Verify(ctx, j.TenantID, j.UserID)
	if err != nil {
		return j, r.fail(ctx, j, FailurePhaseVerification, err, setVerified(false))
	}
	if !verified {
		return j, r.fail(ctx, j, FailurePhaseVerification, errBillingVerification, setVerified(false))
	}
	return r.write(ctx, j, func(w *Job) {
		w.Billing.Verified = true
		w.ErasedAt = r.clock()
	})
}

// recordBilling returns a job mutator that records the §12.8 billing
// erasure outcome on the job.
func recordBilling(o BillingErasureOutcome) func(*Job) {
	return func(j *Job) { j.Billing = o }
}

// setVerified returns a job mutator that records the verification result.
func setVerified(v bool) func(*Job) {
	return func(j *Job) { j.Billing.Verified = v }
}

// finish fixes the job's ReceiptAt once, writes the completion receipt
// built from the job that write returned, and only after the receipt write
// succeeded calls Complete, which marks the job completed and clears the
// restriction where it names the job. A receipt error, or no receipt sink,
// leaves the job in progress; the next run writes a byte-identical receipt.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
func (r *Runner) finish(ctx context.Context, j Job) error {
	j, err := r.write(ctx, j, func(w *Job) {
		if w.ReceiptAt.IsZero() {
			w.ReceiptAt = r.clock()
		}
	})
	if err != nil {
		return err
	}
	if r.receipt == nil {
		return ErrReceiptUnwired
	}
	if err := r.receipt(ctx, j); err != nil {
		return fmt.Errorf("erasure job %s: completion receipt: %w", j.ID, err)
	}
	done, err := r.jobs.Complete(ctx, j.ID, j.ReceiptAt, j)
	if err != nil {
		if errors.Is(err, ErrJobMoved) {
			return ErrJobMoved
		}
		return fmt.Errorf("erasure job %s: complete: %w", j.ID, err)
	}
	r.observeDuration(done)
	// §12.5: fire the erasure-completion hook after the completion is
	// durable. It is best-effort and never fails the completed job.
	if r.onComplete != nil {
		r.onComplete(ctx, done.TenantID, done.UserID)
	}
	return nil
}
