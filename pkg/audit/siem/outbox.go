// SPDX-License-Identifier: MIT

package siem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// ForwardRow is one committed audit_log row the outbox forwarder is
// about to deliver to the SIEM. Input carries the OCSF translation
// source; Sequence and CreatedAt advance the per-tenant-chain delivery
// high-water mark in siem_delivery_state once the SIEM acknowledges the
// record.
//
// spec: §12.3 line 97.
type ForwardRow struct {
	TenantID  string
	Sequence  uint64
	Input     ocsf.Input
	Topic     string
	CreatedAt time.Time
}

// DeliveryStore is the §12.3 outbox / change-data-capture source the
// SIEM forwarder tails. The Postgres implementation
// (auditstore.*Store) reads committed audit_log rows past each tenant
// chain's acknowledged sequence and persists the advanced high-water
// mark in siem_delivery_state, so a forwarder restart replays from the
// last confirmed delivery point without duplication or gap.
//
// spec: §12.3 line 97 — "checkpoint its delivery position durably
// (e.g., a siem_delivery_state table)".
type DeliveryStore interface {
	// PendingForward returns up to limit committed audit rows whose
	// sequence_number is past their tenant chain's acknowledged
	// high-water mark, oldest-first across all tenants.
	PendingForward(ctx context.Context, limit int) ([]ForwardRow, error)

	// Checkpoint advances a tenant chain's SIEM delivery high-water
	// mark to seq (committed at ackedAt). The forwarder calls it only
	// after the SIEM has acknowledged the record, so the mark never
	// runs ahead of a confirmed delivery.
	Checkpoint(ctx context.Context, tenantID string, seq uint64, ackedAt time.Time) error

	// DeliveryLag returns the seconds between the latest committed
	// audit event in Postgres and the latest SIEM-acknowledged event.
	// It is 0 when the forwarder is fully caught up.
	DeliveryLag(ctx context.Context) (float64, error)
}

// LagGauge receives the computed SIEM delivery lag after each forward
// cycle. The gateway implementation sets
// lenny_audit_siem_delivery_lag_seconds. A nil LagGauge is a valid
// no-op.
//
// spec: §16.1 line 228.
type LagGauge interface {
	SetSIEMDeliveryLagSeconds(seconds float64)
}

// OutboxConfig pins the §12.3 outbox forwarder cadence and per-cycle
// batch size. Zero fields are filled from DefaultOutboxConfig.
type OutboxConfig struct {
	// PollInterval is how often the forwarder polls the audit table for
	// newly committed rows (default 5s). It bounds the steady-state
	// delivery lag, so it must stay well under
	// audit.siem.maxDeliveryLagSeconds (default 30s).
	PollInterval time.Duration

	// BatchSize bounds how many committed rows one forward cycle
	// drains (default 256).
	BatchSize int
}

// DefaultOutboxConfig returns the §12.3 outbox forwarder defaults.
func DefaultOutboxConfig() OutboxConfig {
	return OutboxConfig{PollInterval: 5 * time.Second, BatchSize: 256}
}

// Outbox is the §12.3 SIEM outbox / CDC forwarder. It tails committed
// audit_log rows through a DeliveryStore, translates each to the
// canonical OCSF wire form, delivers it to the external SIEM through
// the retrying Forwarder, and advances the durable per-tenant delivery
// high-water mark only after the SIEM acknowledges the record. This
// gives the §12.3 completeness guarantee (HIPAA AU-9, FedRAMP AU-10,
// SOC2 CC7.2): a gateway crash after a Postgres commit but before SIEM
// delivery leaves the row in siem_delivery_state's gap, so the next
// forwarder pass re-reads and re-delivers it from Postgres instead of
// losing it.
type Outbox struct {
	store DeliveryStore
	fwd   *Forwarder
	lag   LagGauge
	cfg   OutboxConfig
}

// NewOutbox returns an Outbox over store and fwd. cfg's zero fields are
// filled from DefaultOutboxConfig; lag may be nil.
func NewOutbox(store DeliveryStore, fwd *Forwarder, cfg OutboxConfig, lag LagGauge) *Outbox {
	def := DefaultOutboxConfig()
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	return &Outbox{store: store, fwd: fwd, lag: lag, cfg: cfg}
}

// OutboxCycleResult reports what one RunCycle pass did.
type OutboxCycleResult struct {
	// Delivered is the number of rows the SIEM acknowledged this cycle.
	Delivered int

	// DeadLettered is the number of rows that failed OCSF translation
	// and were delivered as a translation-failure receipt so the
	// delivery pointer advances past them.
	DeadLettered int
}

// RunCycle delivers one batch of committed-but-unacknowledged audit
// rows to the SIEM, oldest-first, advancing the durable high-water mark
// after each acknowledged record. A row whose OCSF translation fails is
// delivered as a §11.7 translation-failure receipt so a persistently
// untranslatable event does not head-of-line block the stream. A SIEM
// delivery failure (after the Forwarder exhausts its retries) stops the
// cycle without advancing the mark: the lag grows, the
// AuditSIEMDeliveryLag alert fires, and the next cycle re-delivers from
// the same position. Every return path refreshes the lag gauge.
//
// spec: §12.3 line 97.
func (o *Outbox) RunCycle(ctx context.Context) (OutboxCycleResult, error) {
	var res OutboxCycleResult
	rows, err := o.store.PendingForward(ctx, o.cfg.BatchSize)
	if err != nil {
		return res, fmt.Errorf("siem: read pending forward rows: %w", err)
	}
	for _, row := range rows {
		rec, terr := ocsf.Translate(row.Input)
		if terr != nil {
			var te *ocsf.TranslateError
			if !errors.As(terr, &te) {
				te = &ocsf.TranslateError{Class: ocsf.ErrOther, EventType: row.Input.EventType, Detail: terr.Error()}
			}
			rec = ocsf.DeadLetterReceipt(row.Input, te)
			res.DeadLettered++
		}
		b, merr := ocsf.MarshalRecord(rec)
		if merr != nil {
			o.emitLag(ctx)
			return res, fmt.Errorf("siem: marshal OCSF record (tenant %s seq %d): %w", row.TenantID, row.Sequence, merr)
		}
		if e := o.fwd.ForwardBatch(ctx, []json.RawMessage{b}); e != nil {
			// Delivery failed after retries. Do not advance the
			// high-water mark — the row stays pending for the next
			// cycle so no committed event is skipped.
			o.emitLag(ctx)
			return res, e
		}
		if e := o.store.Checkpoint(ctx, row.TenantID, row.Sequence, row.CreatedAt); e != nil {
			o.emitLag(ctx)
			return res, fmt.Errorf("siem: checkpoint delivery (tenant %s seq %d): %w", row.TenantID, row.Sequence, e)
		}
		res.Delivered++
	}
	o.emitLag(ctx)
	return res, nil
}

// emitLag refreshes the delivery-lag gauge. A lag read failure is
// non-fatal: the cycle's delivery work already succeeded, and the next
// cycle re-evaluates the lag.
func (o *Outbox) emitLag(ctx context.Context) {
	if o.lag == nil {
		return
	}
	lag, err := o.store.DeliveryLag(ctx)
	if err != nil {
		return
	}
	o.lag.SetSIEMDeliveryLagSeconds(lag)
}

// Run is the §12.3 outbox forwarder loop. It drives RunCycle every
// PollInterval until ctx is cancelled. Callers run it in a goroutine.
func (o *Outbox) Run(ctx context.Context) {
	ticker := time.NewTicker(o.cfg.PollInterval)
	defer ticker.Stop()
	// Drive one cycle immediately so committed rows are not held for a
	// full interval on startup.
	_, _ = o.RunCycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = o.RunCycle(ctx)
		}
	}
}
