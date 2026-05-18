// SPDX-License-Identifier: MIT

package ocsf

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// TranslationStore is the §11.7 row surface the translator state
// machine drives. It reads rows pending translation and writes back
// their ocsf_translation_state. pkg/gateway/auditstore implements it
// against Postgres; the in-memory fake in tests implements it too.
//
// The store updates only ocsf_translation_state and retry_count;
// neither is part of the §11.7 payload_canonical_json hash input, so a
// translation-state write never re-hashes the chain.
type TranslationStore interface {
	// PendingTranslation returns up to limit rows in
	// (pending | retry_pending) state, oldest-first, across all
	// tenants. The translator processes them one cycle at a time.
	PendingTranslation(ctx context.Context, limit int) ([]TranslatableRow, error)

	// SetTranslationState transitions a row's ocsf_translation_state
	// and sets retry_count. It is idempotent: re-setting the same
	// state is a no-op.
	SetTranslationState(ctx context.Context, tenantID string, seq uint64,
		state audit.OCSFTranslationState, retryCount int) error
}

// TranslatableRow is the canonical-tuple view plus the translator
// bookkeeping columns. The translator builds an ocsf.Input from it.
type TranslatableRow struct {
	Input      Input
	Topic      string
	State      audit.OCSFTranslationState
	RetryCount int
}

// TranslationConfig pins the §11.7 retry parameters. The zero value is
// not usable; build it with DefaultTranslationConfig.
type TranslationConfig struct {
	// RetryInterval is audit.ocsf.retryInterval (default 30s): how
	// often the background retry loop re-attempts a retry_pending row.
	RetryInterval time.Duration

	// MaxAttempts is audit.ocsf.maxAttempts (default 10): on the final
	// attempt's failure the row transitions to dead_lettered.
	MaxAttempts int

	// BatchSize bounds how many rows one translation cycle processes.
	BatchSize int
}

// DefaultTranslationConfig returns the §11.7 default retry parameters.
func DefaultTranslationConfig() TranslationConfig {
	return TranslationConfig{
		RetryInterval: 30 * time.Second,
		MaxAttempts:   10,
		BatchSize:     256,
	}
}

// Sink consumes a successfully translated OCSF record. The §11.7
// translator runs once and multicasts its output to every downstream
// sink (SIEM, pgaudit consumer, webhook subscribers), so a Sink is the
// fan-out seam. A nil Sink is a valid no-op.
type Sink interface {
	// Deliver hands one translated OCSF record to the sink. The record
	// is the OCSF wire form; topic is the source EventTopic.
	Deliver(ctx context.Context, tenantID, topic string, rec Record) error
}

// Metrics is the §11.7 translator metric surface. A nil Metrics is a
// valid no-op so the translator runs without a metric backend wired.
type Metrics interface {
	// TranslationFailed increments lenny_audit_ocsf_translation_failed_total
	// labeled by event_type and error_class.
	TranslationFailed(eventType string, class ErrorClass)

	// TranslationSucceeded counts a row that reached the succeeded state.
	TranslationSucceeded(eventType string)

	// DeadLettered counts a row that reached the dead_lettered state.
	// Every dead-letter is an operator-visible event (§16.5
	// OCSFTranslationBacklog).
	DeadLettered(eventType string)
}

// Translator drives the §11.7 OCSF translation state machine over a
// TranslationStore. On first-attempt failure it writes retry_pending;
// a background retry loop re-attempts every RetryInterval up to
// MaxAttempts; on final-attempt failure it writes dead_lettered and
// emits the translation-failure receipt to the sink so the SIEM
// delivery pointer advances past the row.
type Translator struct {
	store   TranslationStore
	sink    Sink
	cfg     TranslationConfig
	metrics Metrics
	now     func() time.Time
}

// NewTranslator returns a Translator over store. A nil sink or nil
// metrics is tolerated. cfg's zero fields are filled from
// DefaultTranslationConfig.
func NewTranslator(store TranslationStore, sink Sink, cfg TranslationConfig, metrics Metrics) *Translator {
	def := DefaultTranslationConfig()
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = def.RetryInterval
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	return &Translator{store: store, sink: sink, cfg: cfg, metrics: metrics, now: time.Now}
}

// CycleResult reports what one RunCycle pass did.
type CycleResult struct {
	// Translated is the number of rows that reached the succeeded
	// state this cycle.
	Translated int

	// RetryScheduled is the number of rows that failed and were
	// written back as retry_pending.
	RetryScheduled int

	// DeadLettered is the number of rows that exhausted MaxAttempts
	// and transitioned to dead_lettered.
	DeadLettered int
}

// RunCycle processes one batch of pending / retry_pending rows. For
// each row it attempts Translate; on success the row transitions to
// succeeded and the record is delivered to the sink; on failure the
// §11.7 retry / dead-letter state machine advances the row.
//
// RunCycle is the unit the background loop and the §12.3.5 retry
// contract test both drive. It does not sleep — Run wraps it with the
// RetryInterval ticker.
func (t *Translator) RunCycle(ctx context.Context) (CycleResult, error) {
	rows, err := t.store.PendingTranslation(ctx, t.cfg.BatchSize)
	if err != nil {
		return CycleResult{}, fmt.Errorf("ocsf: read pending translations: %w", err)
	}
	var res CycleResult
	for _, row := range rows {
		rec, terr := Translate(row.Input)
		if terr == nil {
			if e := t.deliver(ctx, row, rec); e != nil {
				return res, e
			}
			if e := t.store.SetTranslationState(ctx, row.Input.TenantID,
				row.Input.Sequence, audit.OCSFSucceeded, row.RetryCount); e != nil {
				return res, fmt.Errorf("ocsf: mark succeeded: %w", e)
			}
			if t.metrics != nil {
				t.metrics.TranslationSucceeded(row.Input.EventType)
			}
			res.Translated++
			continue
		}

		// Translation failed. Classify and advance the state machine.
		var te *TranslateError
		if !errors.As(terr, &te) {
			te = &TranslateError{Class: ErrOther, EventType: row.Input.EventType, Detail: terr.Error()}
		}
		if t.metrics != nil {
			t.metrics.TranslationFailed(row.Input.EventType, te.Class)
		}
		attempts := row.RetryCount + 1
		if attempts >= t.cfg.MaxAttempts {
			// Final attempt failed → dead-letter. Emit the §11.7
			// translation-failure receipt so the SIEM pointer advances
			// past this row instead of head-of-line blocking.
			receipt := DeadLetterReceipt(row.Input, te)
			if e := t.deliver(ctx, row, receipt); e != nil {
				return res, e
			}
			if e := t.store.SetTranslationState(ctx, row.Input.TenantID,
				row.Input.Sequence, audit.OCSFDeadLettered, attempts); e != nil {
				return res, fmt.Errorf("ocsf: mark dead_lettered: %w", e)
			}
			if t.metrics != nil {
				t.metrics.DeadLettered(row.Input.EventType)
			}
			res.DeadLettered++
			continue
		}
		// Non-final failure → retry_pending; the next cycle re-attempts.
		if e := t.store.SetTranslationState(ctx, row.Input.TenantID,
			row.Input.Sequence, audit.OCSFRetryPending, attempts); e != nil {
			return res, fmt.Errorf("ocsf: mark retry_pending: %w", e)
		}
		res.RetryScheduled++
	}
	return res, nil
}

// deliver hands a record to the sink when one is wired.
func (t *Translator) deliver(ctx context.Context, row TranslatableRow, rec Record) error {
	if t.sink == nil {
		return nil
	}
	if err := t.sink.Deliver(ctx, row.Input.TenantID, row.Topic, rec); err != nil {
		return fmt.Errorf("ocsf: deliver to sink: %w", err)
	}
	return nil
}

// Run is the §11.7 background retry loop. It drives RunCycle every
// RetryInterval until ctx is cancelled. Callers run it in a goroutine.
func (t *Translator) Run(ctx context.Context) {
	ticker := time.NewTicker(t.cfg.RetryInterval)
	defer ticker.Stop()
	// Drive one cycle immediately so pending rows are not held for a
	// full interval on startup.
	_, _ = t.RunCycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = t.RunCycle(ctx)
		}
	}
}

// CountingMetrics is an in-memory Metrics implementation for tests and
// for the §16.5 OCSFTranslationBacklog signal. It is goroutine-safe.
type CountingMetrics struct {
	mu        sync.Mutex
	failed    map[string]int // keyed by event_type|error_class
	succeeded int
	dead      int
}

// NewCountingMetrics returns an empty CountingMetrics.
func NewCountingMetrics() *CountingMetrics {
	return &CountingMetrics{failed: map[string]int{}}
}

// TranslationFailed records a failure.
func (m *CountingMetrics) TranslationFailed(eventType string, class ErrorClass) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed[eventType+"|"+string(class)]++
}

// TranslationSucceeded records a success.
func (m *CountingMetrics) TranslationSucceeded(string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.succeeded++
}

// DeadLettered records a dead-letter.
func (m *CountingMetrics) DeadLettered(string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead++
}

// Failed returns the failure count for an (event_type, error_class).
func (m *CountingMetrics) Failed(eventType string, class ErrorClass) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failed[eventType+"|"+string(class)]
}

// Succeeded returns the total success count.
func (m *CountingMetrics) Succeeded() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.succeeded
}

// DeadLetters returns the total dead-letter count.
func (m *CountingMetrics) DeadLetters() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dead
}
