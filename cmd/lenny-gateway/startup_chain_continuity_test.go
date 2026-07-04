// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// chainRowsQuerier is a minimal integrity.Querier that serves the two
// queries the §12.3 line 101 startup chain-continuity check issues: the
// distinct-tenant scan and the per-tenant recent-rows load. It returns
// one tenant's rows built from an audit.Row slice, encoding each row's
// columns exactly as audit_log stores them (prev_hash as bytea, id as
// text) so scanChainRows reconstructs the same chain the verifier walks.
type chainRowsQuerier struct {
	tenantID string
	rows     []audit.Row
}

func (q *chainRowsQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	// The tenants-in-deletion skip-set query the co-located continuity
	// check issues against the control-plane pool; this querier serves no
	// deleting tenant, so the skip-set is empty and every tenant is walked.
	if strings.Contains(sql, "FROM tenants WHERE state") {
		return &emptyScanRows{}, nil
	}
	if strings.Contains(sql, "SELECT DISTINCT tenant_id") {
		return &tenantScanRows{tenantID: q.tenantID}, nil
	}
	return &chainScanRows{rows: q.rows}, nil
}

// emptyScanRows is a pgx.Rows that yields no rows, used for the
// tenants-in-deletion skip-set query when no tenant is being deleted.
type emptyScanRows struct {
	pgx.Rows
}

func (r *emptyScanRows) Next() bool        { return false }
func (r *emptyScanRows) Scan(...any) error { return nil }
func (r *emptyScanRows) Close()            {}
func (r *emptyScanRows) Err() error        { return nil }

func (q *chainRowsQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("QueryRow is not used by the startup chain-continuity check")
}

// tenantScanRows returns the single tenant id for the auditTenants scan.
type tenantScanRows struct {
	pgx.Rows
	tenantID string
	done     bool
}

func (r *tenantScanRows) Next() bool {
	if r.done {
		return false
	}
	r.done = true
	return true
}

func (r *tenantScanRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.tenantID
	return nil
}

func (r *tenantScanRows) Close()     {}
func (r *tenantScanRows) Err() error { return nil }

// chainScanRows feeds audit.Row values back through the seven-column
// audit_log projection scanChainRows reads (sequence_number, event_type,
// payload, created_at, prev_hash, id::text, event_schema_version).
type chainScanRows struct {
	pgx.Rows
	rows []audit.Row
	idx  int
}

func (r *chainScanRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *chainScanRows) Scan(dest ...any) error {
	row := r.rows[r.idx-1]
	prevBytes, err := hex.DecodeString(row.PrevHash)
	if err != nil {
		return err
	}
	*dest[0].(*int64) = int64(row.Seq)
	*dest[1].(*string) = row.EventType
	*dest[2].(*[]byte) = []byte(row.Payload)
	*dest[3].(*time.Time) = row.Timestamp
	*dest[4].(*[]byte) = prevBytes
	*dest[5].(*string) = row.ID
	*dest[6].(*string) = row.EventSchemaVersion
	return nil
}

func (r *chainScanRows) Close()     {}
func (r *chainScanRows) Err() error { return nil }

// linkedChain constructs a well-linked tenant chain of the given
// sequence numbers, chaining each row's prev_hash to its predecessor the
// way sealAndInsert commits rows, so scanChainRows reconstructs a chain
// verifyChainWindow accepts. An interior sequence jump models a nextval
// rollback (the successor still links to the committed tail).
func linkedChain(tenantID string, seqs []uint64) []audit.Row {
	rows := make([]audit.Row, 0, len(seqs))
	var prev audit.Row
	for i, seq := range seqs {
		r := audit.Row{
			ID:                 "id-" + strconv.FormatUint(seq, 10),
			Seq:                seq,
			TenantID:           tenantID,
			EventType:          "test.event",
			EventSchemaVersion: audit.DefaultEventSchemaVersion,
			Payload:            json.RawMessage(`{"seq":` + strconv.FormatUint(seq, 10) + `}`),
			Timestamp:          time.Unix(int64(seq), 0).UTC(),
		}
		if i == 0 {
			r.PrevHash = audit.GenesisPrevHash
		} else {
			r.PrevHash = audit.LinkHash(prev)
		}
		r.Hash = audit.ComputeHash(r)
		rows = append(rows, r)
		prev = r
	}
	return rows
}

// captureLog redirects the standard logger for the duration of fn and
// returns everything it wrote. runStartupChainContinuityCheck logs
// through the package-level log.Printf.
func captureLog(fn func()) string {
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// TestRunStartupChainContinuityCheckTamperEmitsCommittedRowMessage pins
// the S9 rework: a committed-row removal leaves a non-linking prev_hash
// across the resulting sequence gap, so verifyChainWindow returns a
// boundary-populated ChainBroken and the startup check must emit the
// §12.3 line 101 committed-row-tamper-or-removal WARN string — not the
// pre-fix "T2 audit events were lost from the in-memory batch buffer"
// wording, which misclassified a tamper as benign buffered-T2 loss. This
// test fails against the pre-S9 code because that code emitted the
// buffered-loss string on this branch.
//
// spec: 12.3 (startup chain-continuity check line 101), 11.7 (prev_hash
// linkage is the tamper authority), 25.9. F-11.2.10.
//
// diagnosis: the reworked GapHighSeq()>0 WARN branch no longer emits the
// §12.3 line 101 committed-row-tamper string, or still emits the retired
// buffered-T2-loss string, so an operator would treat a committed-row
// tamper as a benign compliance gap instead of escalating it.
func TestRunStartupChainContinuityCheckTamperEmitsCommittedRowMessage(t *testing.T) {
	// Committed rows 1,2,3,5,6 where row 5's stored prev_hash still links
	// to a removed committed row 4 (not to committed tail row 3): a
	// committed-row removal, so the link across the 3→5 gap does not
	// verify and the chain is broken with populated boundaries.
	full := linkedChain("acme", []uint64{1, 2, 3, 4, 5, 6})
	// Drop committed row 4 while row 5 keeps its prev_hash link to it.
	gapped := append(append([]audit.Row{}, full[:3]...), full[4:]...)
	q := &chainRowsQuerier{tenantID: "acme", rows: gapped}

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	out := captureLog(func() {
		runStartupChainContinuityCheck(context.Background(), q, q, 1000, m)
	})

	if !strings.Contains(out, "prev_hash does not link across sequence 3 to 5") {
		t.Errorf("WARN log = %q, want the §12.3 committed-row-tamper string with gap boundaries 3 to 5", out)
	}
	if !strings.Contains(out, "committed audit row was tampered with or removed") {
		t.Errorf("WARN log = %q, want the committed-row-tamper-or-removal wording", out)
	}
	if strings.Contains(out, "T2 audit events were lost") {
		t.Errorf("WARN log = %q, must not emit the retired buffered-T2-loss wording", out)
	}
}

// TestRunStartupChainContinuityCheckBenignGapEmitsNothing pins that a
// benign nextval-rollback gap with an intact prev_hash link is
// detected-but-not-broken: verifyChainWindow returns ChainVerified, so
// the startup check logs no WARN at all. The pre-S8/S9 gap-first verifier
// returned ChainBroken here and the pre-S9 branch would have emitted the
// buffered-T2-loss WARN, so this test fails against that code.
//
// spec: 12.3 (intact-link gap is detected-but-not-broken), 11.7 (nextval
// leaves benign rollback gaps). F-11.2.10.
//
// diagnosis: a benign nextval-rollback gap fires a startup WARN (and
// thus AuditChainGap through the metric), meaning the gateway alarms on
// an ordinary transaction rollback.
func TestRunStartupChainContinuityCheckBenignGapEmitsNothing(t *testing.T) {
	// nextval allocated 4 but its transaction rolled back; committed rows
	// run 1,2,3,5,6 with row 5 linking to the committed tail row 3.
	rows := linkedChain("acme", []uint64{1, 2, 3, 5, 6})
	q := &chainRowsQuerier{tenantID: "acme", rows: rows}

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	out := captureLog(func() {
		runStartupChainContinuityCheck(context.Background(), q, q, 1000, m)
	})

	if strings.TrimSpace(out) != "" {
		t.Errorf("WARN log = %q, want no output for a benign intact-link gap", out)
	}
}

// ctrlDeletionQuerier is a control-plane integrity.Querier that serves
// only the §12.8 deletion skip-set query, reporting a fixed set of
// tenants in state='deleting'/'deleted'. It records whether the skip-set
// query was issued against it so a wiring test can assert the startup
// check reads the skip-set from the control-plane pool (the second
// argument) rather than from the ledger pool.
type ctrlDeletionQuerier struct {
	deletingIDs []string
	sawSkipSet  bool
}

func (q *ctrlDeletionQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM tenants WHERE state") {
		q.sawSkipSet = true
		return &stringSkipSetRows{ids: q.deletingIDs}, nil
	}
	// The control-plane pool serves no audit_log enumeration in this test;
	// a chain query reaching it means the split-pool wiring is inverted.
	return &emptyScanRows{}, nil
}

func (q *ctrlDeletionQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("QueryRow is not used by the startup chain-continuity check")
}

// stringSkipSetRows replays the single-column tenant-id result set the
// deletion skip-set query returns, scanning each id into a *string.
type stringSkipSetRows struct {
	pgx.Rows
	ids []string
	idx int
}

func (r *stringSkipSetRows) Next() bool {
	if r.idx >= len(r.ids) {
		return false
	}
	r.idx++
	return true
}

func (r *stringSkipSetRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.ids[r.idx-1]
	return nil
}

func (r *stringSkipSetRows) Close()     {}
func (r *stringSkipSetRows) Err() error { return nil }

// TestRunStartupChainContinuityCheckResolvesSkipSetFromCtrlPool pins the
// S7 (C4) wiring: runStartupChainContinuityCheck enumerates audit_log
// from the ledger pool (first argument) but resolves the §12.8
// deletion skip-set from the control-plane pool (second argument). It
// seeds a tenant whose ledger remnant is a broken chain (a committed-row
// removal, exactly the false-positive the §12.8 teardown leaves), and a
// separate control-plane querier that reports that tenant as
// state='deleting'. The tenant must be skipped, so no committed-row-tamper
// WARN is emitted and the skip-set query lands on the control-plane pool.
//
// This fails against the pre-S7 code, whose runStartupChainContinuityCheck
// took a single pool and used it as both the ledger and the control-plane
// source: with the ledger pool reporting an empty skip-set the broken
// remnant would be walked and the WARN emitted. It also fails against a
// wiring that forwards the ledger pool as ctrlDB, because the ledger
// querier serves an empty skip-set for the FROM tenants query.
//
// spec: 12.3 (split billing/audit Postgres, line 103), 12.8 (post-teardown
// remnant exempt, line 840), 11.7 (chain integrity). F-12.8.4.
//
// diagnosis: the startup check reads the tenant-deletion skip-set from the
// ledger pool instead of the control-plane pool, so under the §12.3 split
// billing/audit-pool topology a torn-down tenant's retained gdpr.*-only
// remnant fires a false §16.5 AuditChainGap WARN at gateway startup.
func TestRunStartupChainContinuityCheckResolvesSkipSetFromCtrlPool(t *testing.T) {
	// Ledger pool: tenant "acme" carries a broken remnant (row 4 removed
	// while row 5 keeps its prev_hash link to it, a committed-row removal).
	full := linkedChain("acme", []uint64{1, 2, 3, 4, 5, 6})
	gapped := append(append([]audit.Row{}, full[:3]...), full[4:]...)
	ledger := &chainRowsQuerier{tenantID: "acme", rows: gapped}
	// Control-plane pool: "acme" is state='deleting', so its remnant is
	// exempt from continuity verification and must be skipped.
	ctrl := &ctrlDeletionQuerier{deletingIDs: []string{"acme"}}

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	out := captureLog(func() {
		runStartupChainContinuityCheck(context.Background(), ledger, ctrl, 1000, m)
	})

	if strings.TrimSpace(out) != "" {
		t.Errorf("WARN log = %q, want no output: a deleting tenant's remnant must be skipped", out)
	}
	if !ctrl.sawSkipSet {
		t.Error("the deletion skip-set query must be issued against the control-plane pool (second argument)")
	}
}

// Assert the fake queriers satisfy the integrity.Querier contract the
// startup check consumes, so a signature drift fails at compile time.
var (
	_ integrity.Querier = (*chainRowsQuerier)(nil)
	_ integrity.Querier = (*ctrlDeletionQuerier)(nil)
)
