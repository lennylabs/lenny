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
	if strings.Contains(sql, "SELECT DISTINCT tenant_id") {
		return &tenantScanRows{tenantID: q.tenantID}, nil
	}
	return &chainScanRows{rows: q.rows}, nil
}

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
		runStartupChainContinuityCheck(context.Background(), q, 1000, m)
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
		runStartupChainContinuityCheck(context.Background(), q, 1000, m)
	})

	if strings.TrimSpace(out) != "" {
		t.Errorf("WARN log = %q, want no output for a benign intact-link gap", out)
	}
}

// Assert the fake querier satisfies the integrity.Querier contract the
// startup check consumes, so a signature drift fails at compile time.
var _ integrity.Querier = (*chainRowsQuerier)(nil)
