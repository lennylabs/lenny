// SPDX-License-Identifier: MIT

package pgstore

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/correctionstore"
)

// fakeRow assigns a pre-set list of column values onto the Scan
// destinations in order, so scanCorrection's row→PendingCorrection
// mapping can be exercised at tier-1 without a live Postgres.
type fakeRow struct {
	vals []any
}

func (f fakeRow) Scan(dest ...any) error {
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = f.vals[i].(string)
		case *int64:
			*d = f.vals[i].(int64)
		case *float64:
			*d = f.vals[i].(float64)
		case *bool:
			*d = f.vals[i].(bool)
		case *time.Time:
			*d = f.vals[i].(time.Time)
		case **time.Time:
			if f.vals[i] == nil {
				*d = nil
			} else {
				t := f.vals[i].(time.Time)
				*d = &t
			}
		}
	}
	return nil
}

// spec: §11.2.1 — scanCorrection maps a billing_correction_pending row
// into a PendingCorrection: the BIGINT columns widen back to uint64,
// the reason_code/state strings re-type, and a non-NULL decided_at
// round-trips as a UTC timestamp on a decided request. F-11.2.11.
func TestScanCorrectionDecidedRow_spec_11_2_1(t *testing.T) {
	submitted := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 6, 1, 11, 30, 0, 0, time.UTC)
	row := fakeRow{vals: []any{
		"abc123", "acme", int64(42), "duplicate_charge", "operator note",
		int64(1000), int64(2000), 3.5, "approved", "alice@acme.com",
		"bob@acme.com", true, int64(99), submitted, decided,
	}}
	got, err := scanCorrection(row)
	if err != nil {
		t.Fatalf("scanCorrection: %v", err)
	}
	if got.ID != "abc123" || got.TenantID != "acme" {
		t.Errorf("id/tenant = %q/%q", got.ID, got.TenantID)
	}
	if got.CorrectsSequence != 42 || got.CommittedSequence != 99 {
		t.Errorf("sequences = %d/%d, want 42/99", got.CorrectsSequence, got.CommittedSequence)
	}
	if got.TokensInput != 1000 || got.TokensOutput != 2000 || got.PodMinutes != 3.5 {
		t.Errorf("cost = %d/%d/%v", got.TokensInput, got.TokensOutput, got.PodMinutes)
	}
	if got.ReasonCode != billingstore.ReasonCode("duplicate_charge") {
		t.Errorf("reason = %q", got.ReasonCode)
	}
	if got.State != correctionstore.StateApproved {
		t.Errorf("state = %q, want approved", got.State)
	}
	if !got.DualControl || got.DecidedBy != "bob@acme.com" {
		t.Errorf("dualControl/decidedBy = %v/%q", got.DualControl, got.DecidedBy)
	}
	if !got.DecidedAt.Equal(decided) {
		t.Errorf("decidedAt = %v, want %v", got.DecidedAt, decided)
	}
}

// spec: §11.2.1 — a pending request has a NULL decided_at; scanCorrection
// must map SQL NULL to the zero time the in-memory store uses for an
// undecided request rather than panicking on a nil pointer. F-11.2.11.
func TestScanCorrectionPendingNullDecidedAt_spec_11_2_1(t *testing.T) {
	submitted := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	row := fakeRow{vals: []any{
		"id1", "acme", int64(7), "operator_adjustment", "",
		int64(0), int64(0), 0.0, "pending", "alice@acme.com",
		"", false, int64(0), submitted, nil,
	}}
	got, err := scanCorrection(row)
	if err != nil {
		t.Fatalf("scanCorrection: %v", err)
	}
	if got.State != correctionstore.StatePending {
		t.Errorf("state = %q, want pending", got.State)
	}
	if !got.DecidedAt.IsZero() {
		t.Errorf("decidedAt = %v, want zero for a pending request", got.DecidedAt)
	}
	if got.DecidedBy != "" || got.CommittedSequence != 0 {
		t.Errorf("undecided request carries decidedBy=%q committed=%d", got.DecidedBy, got.CommittedSequence)
	}
}

// spec: §11.2.1 — DecidedAt round-trips through SQL as NULL while
// undecided (zero time) and a concrete UTC timestamp once decided, so
// the in-memory and Postgres registries agree on the undecided marker.
// F-11.2.11.
func TestDecidedAtArg_spec_11_2_1(t *testing.T) {
	if got := decidedAtArg(time.Time{}); got != nil {
		t.Errorf("decidedAtArg(zero) = %v, want nil (SQL NULL)", got)
	}
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	got, ok := decidedAtArg(at).(time.Time)
	if !ok {
		t.Fatalf("decidedAtArg(non-zero) = %T, want time.Time", decidedAtArg(at))
	}
	if got.Location() != time.UTC {
		t.Errorf("decidedAtArg normalizes to UTC, got %v", got.Location())
	}
	if !got.Equal(at) {
		t.Errorf("decidedAtArg changed the instant: %v != %v", got, at)
	}
}

// spec: §11.2.1 — the List filter composes tenant_id and state
// predicates; whereClause must AND multiple predicates and emit nothing
// for an unfiltered (Counts-style) scan. F-11.2.11.
func TestWhereClause_spec_11_2_1(t *testing.T) {
	if got := whereClause(nil); got != "" {
		t.Errorf("whereClause(nil) = %q, want empty", got)
	}
	if got := whereClause([]string{"tenant_id = $1"}); got != " WHERE tenant_id = $1" {
		t.Errorf("whereClause(one) = %q", got)
	}
	got := whereClause([]string{"tenant_id = $1", "state = $2"})
	if got != " WHERE tenant_id = $1 AND state = $2" {
		t.Errorf("whereClause(two) = %q", got)
	}
}
