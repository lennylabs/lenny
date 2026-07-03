// SPDX-License-Identifier: MIT

package integrity

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// spec: §12.3 line 101 — startup chain-continuity check windowed over the
// most recent audit.startupChainCheckEntries rows. F-12.3.9.
// spec: 11.7 (nextval sequence, prev_hash linkage is the tamper
// authority), 12.3 (startup chain-continuity check). F-11.2.10.

// buildChain constructs a well-linked tenant chain of n rows exactly as
// loadChainRows reconstructs persisted rows: prev_hash links the
// predecessor and Hash is recomputed from the row's stored fields.
func buildChain(tenantID string, n int) []audit.Row {
	rows := make([]audit.Row, 0, n)
	var prev audit.Row
	for i := 1; i <= n; i++ {
		r := audit.Row{
			Seq:       uint64(i),
			TenantID:  tenantID,
			EventType: "test.event",
			Payload:   json.RawMessage(`{"i":` + strconv.Itoa(i) + `}`),
			Timestamp: time.Unix(int64(i), 0).UTC(),
		}
		if i == 1 {
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

// buildChainSeqs constructs a well-linked tenant chain whose committed
// rows carry the given sequence numbers, chaining each row's prev_hash to
// its immediate predecessor in the slice exactly as sealAndInsert does
// (prev_hash = LinkHash(committed tail)). It models an audit chain whose
// nextval allocator skipped one or more values (a transaction rollback
// consumed a sequence value without committing a row) so the sequence
// numbers have interior gaps while the prev_hash chain stays intact.
func buildChainSeqs(tenantID string, seqs []uint64) []audit.Row {
	rows := make([]audit.Row, 0, len(seqs))
	var prev audit.Row
	for i, seq := range seqs {
		r := audit.Row{
			Seq:       seq,
			TenantID:  tenantID,
			EventType: "test.event",
			Payload:   json.RawMessage(`{"seq":` + strconv.FormatUint(seq, 10) + `}`),
			Timestamp: time.Unix(int64(seq), 0).UTC(),
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

func TestVerifyChainWindowEmptyAndFullVerified(t *testing.T) {
	if res, _ := verifyChainWindow("acme", nil); res.Integrity != audit.ChainVerified {
		t.Errorf("empty window = %q, want verified", res.Integrity)
	}
	res, _ := verifyChainWindow("acme", buildChain("acme", 5))
	if res.Integrity != audit.ChainVerified {
		t.Errorf("intact full chain = %q (%s), want verified", res.Integrity, res.Detail)
	}
}

// TestVerifyChainWindowPartialDoesNotAssertGenesis is the crux of the
// windowed check: a recent-N window that starts past sequence 1 must
// verify on its internal links alone, without flagging the first
// windowed row for not carrying the genesis sentinel.
func TestVerifyChainWindowPartialDoesNotAssertGenesis(t *testing.T) {
	full := buildChain("acme", 10)
	window := full[5:] // sequences 6..10; window[0].PrevHash != genesis.
	res, _ := verifyChainWindow("acme", window)
	if res.Integrity != audit.ChainVerified {
		t.Errorf("partial window = %q (%s), want verified — genesis must not be asserted on a windowed head", res.Integrity, res.Detail)
	}
}

// TestVerifyChainWindowCommittedRowRemovalIsBroken covers a committed
// audit row removed from the middle of the chain: the successor's stored
// prev_hash still points at the removed row, so the link across the
// resulting sequence gap does not verify and the chain is broken. The
// break is attributed to the non-linking prev_hash (the §11.7 authority),
// with the gap boundaries populated for the §12.3 WARN message.
// spec: 11.7 (prev_hash linkage is the tamper authority), 12.3 (startup
// chain-continuity check). F-11.2.10.
func TestVerifyChainWindowCommittedRowRemovalIsBroken(t *testing.T) {
	full := buildChain("acme", 6)
	// Drop the committed sequence 4: rows now run 1,2,3,5,6, but row 5's
	// stored prev_hash still links to the removed row 4.
	gapped := append(append([]audit.Row{}, full[:3]...), full[4:]...)
	res, gap := verifyChainWindow("acme", gapped)
	if res.Integrity != audit.ChainBroken {
		t.Fatalf("removed committed row = %q, want broken", res.Integrity)
	}
	if res.BreakSeq != 5 {
		t.Errorf("BreakSeq = %d, want 5", res.BreakSeq)
	}
	if gap.lowSeq != 3 || gap.highSeq != 5 {
		t.Errorf("gap boundary = (%d,%d), want (3,5)", gap.lowSeq, gap.highSeq)
	}
	if !gap.start.Equal(full[2].Timestamp) || !gap.end.Equal(full[4].Timestamp) {
		t.Errorf("gap range = (%s,%s), want (%s,%s)", gap.start, gap.end, full[2].Timestamp, full[4].Timestamp)
	}
}

// TestVerifyChainWindowBenignNextvalGapNotBroken is the S8 regression
// guard for the §11.7 Path A nextval switch. A transaction rollback
// consumes a nextval sequence value without committing a row, so the
// committed chain skips that sequence number while the successor's
// prev_hash still links to the committed tail. This intact-link sequence
// gap is benign — detected but not a broken chain — so it must not stamp
// ChainBroken, must not populate the gap boundaries, and must not fire
// §16.5 AuditChainGap or trip audit.hardFailOnDrift. The pre-fix
// gap-first verifier returned ChainBroken here, so this test fails
// against it.
// spec: 11.7 (nextval leaves benign rollback gaps, prev_hash linkage is
// the tamper authority), 12.3 (intact-link gap is detected-but-not-broken).
// F-11.2.10.
func TestVerifyChainWindowBenignNextvalGapNotBroken(t *testing.T) {
	// nextval allocated 4 but its transaction rolled back, so the committed
	// chain runs 1,2,3,5,6 with row 5 linking to the committed tail row 3.
	rows := buildChainSeqs("acme", []uint64{1, 2, 3, 5, 6})
	res, gap := verifyChainWindow("acme", rows)
	if res.Integrity != audit.ChainVerified {
		t.Fatalf("benign nextval-rollback gap = %q (%s), want verified — an intact-link sequence gap is not a broken chain", res.Integrity, res.Detail)
	}
	if res.BreakSeq != 0 {
		t.Errorf("BreakSeq = %d, want 0 for a benign gap", res.BreakSeq)
	}
	if gap != (chainGap{}) {
		t.Errorf("gap boundary = %+v, want empty for a benign gap", gap)
	}
}

// TestVerifyChainWindowBenignNextvalGapPartialWindow covers the benign
// nextval-rollback gap inside a recent-N window that starts past sequence
// 1: the windowed head does not carry the genesis sentinel, and an
// interior intact-link sequence gap must still verify. spec: 11.7, 12.3.
// F-11.2.10.
func TestVerifyChainWindowBenignNextvalGapPartialWindow(t *testing.T) {
	// A window of committed rows 12,13,15,16 (nextval skipped 14).
	rows := buildChainSeqs("acme", []uint64{12, 13, 15, 16})
	res, gap := verifyChainWindow("acme", rows)
	if res.Integrity != audit.ChainVerified {
		t.Fatalf("partial-window benign gap = %q (%s), want verified", res.Integrity, res.Detail)
	}
	if gap != (chainGap{}) {
		t.Errorf("gap boundary = %+v, want empty for a benign gap", gap)
	}
}

// TestVerifyChainWindowDroppedBatchBufferedLossNotBroken pins the S5
// accepted-loss policy. A gateway crash that drops an accepted buffered
// T2 batch after its nextval calls but before commit loses events, yet
// the next committed row still links to the pre-batch committed tail, so
// the gap is chain-indistinguishable from a benign nextval rollback: an
// intact-link gap. verifyChainWindow must not classify it ChainBroken,
// because buffered T2 crash-window loss is the accepted, unsignaled
// tradeoff of the opt-in audit.batchingEnabled path (disabled by
// default) rather than a chain-signaled event; no chain-level alert
// covers it. Only a committed-row tamper or removal severs the link.
// spec: 11.7 (intact-link gap is not a break), 12.3 (buffered T2 loss is
// the accepted unsignaled tradeoff). F-11.2.10.
func TestVerifyChainWindowDroppedBatchBufferedLossNotBroken(t *testing.T) {
	// A batch of sequences 5..8 was accepted (nextval consumed) but its
	// commit was lost to a crash, so the committed chain runs
	// 1,2,3,4,9,10 and row 9 links to the committed tail row 4.
	rows := buildChainSeqs("acme", []uint64{1, 2, 3, 4, 9, 10})
	res, gap := verifyChainWindow("acme", rows)
	if res.Integrity != audit.ChainVerified {
		t.Fatalf("dropped-batch buffered-loss gap = %q (%s), want verified — an intact-link gap is not a broken chain and buffered T2 loss is unsignaled", res.Integrity, res.Detail)
	}
	if gap != (chainGap{}) {
		t.Errorf("gap boundary = %+v, want empty for an intact-link gap", gap)
	}
}

// TestVerifyChainWindowTamperAcrossGap covers the composite case the S8
// reorder must still catch: a committed-row removal that also leaves a
// sequence gap. The link across the gap does not verify, so the chain is
// broken and the detail names the non-linking prev_hash rather than the
// bare sequence gap. spec: 11.7, 12.3. F-11.2.10.
func TestVerifyChainWindowTamperAcrossGap(t *testing.T) {
	// Committed rows 1,2,4,5 (a benign nextval gap at 3), then tamper row
	// 4's payload so row 5's stored prev_hash no longer links across it.
	rows := buildChainSeqs("acme", []uint64{1, 2, 4, 5})
	rows[2].Payload = json.RawMessage(`{"seq":4,"tampered":true}`)
	rows[2].Hash = audit.ComputeHash(rows[2])
	res, gap := verifyChainWindow("acme", rows)
	if res.Integrity != audit.ChainBroken {
		t.Fatalf("tamper across gap = %q, want broken", res.Integrity)
	}
	if res.BreakSeq != 5 {
		t.Errorf("BreakSeq = %d, want 5 (link break follows the tampered row)", res.BreakSeq)
	}
	if gap.lowSeq != 4 || gap.highSeq != 5 {
		t.Errorf("gap boundary = (%d,%d), want (4,5)", gap.lowSeq, gap.highSeq)
	}
}

// TestVerifyChainWindowLinkBreakOnTamper covers tampering: a row whose
// stored payload was rewritten breaks the successor's prev_hash link
// even though the sequence numbers stay contiguous.
func TestVerifyChainWindowLinkBreakOnTamper(t *testing.T) {
	chain := buildChain("acme", 5)
	// Tamper row 3's payload as loadChainRows would observe it (Hash is
	// recomputed from the stored payload), leaving row 4's stored
	// prev_hash pointing at the pre-tamper row 3.
	chain[2].Payload = json.RawMessage(`{"i":3,"tampered":true}`)
	chain[2].Hash = audit.ComputeHash(chain[2])
	res, gap := verifyChainWindow("acme", chain)
	if res.Integrity != audit.ChainBroken {
		t.Fatalf("tampered chain = %q, want broken", res.Integrity)
	}
	if res.BreakSeq != 4 {
		t.Errorf("BreakSeq = %d, want 4 (link break follows the tampered row)", res.BreakSeq)
	}
	if gap.lowSeq != 3 || gap.highSeq != 4 {
		t.Errorf("gap boundary = (%d,%d), want (3,4)", gap.lowSeq, gap.highSeq)
	}
}

// TestVerifyChainWindowGenesisBreak covers a window that does start at
// sequence 1 but whose head does not carry the genesis sentinel.
func TestVerifyChainWindowGenesisBreak(t *testing.T) {
	chain := buildChain("acme", 3)
	chain[0].PrevHash = "deadbeef" // not the genesis sentinel
	res, _ := verifyChainWindow("acme", chain)
	if res.Integrity != audit.ChainBroken {
		t.Fatalf("bad genesis = %q, want broken", res.Integrity)
	}
	if res.BreakSeq != 1 {
		t.Errorf("BreakSeq = %d, want 1", res.BreakSeq)
	}
}

// TestVerifyChainWindowSingleRowWindow covers the boundary where the
// recent-N window holds exactly one row past genesis: there is no
// adjacent pair to link-check, so it verifies.
func TestVerifyChainWindowSingleRowWindow(t *testing.T) {
	full := buildChain("acme", 4)
	res, _ := verifyChainWindow("acme", full[3:]) // just sequence 4
	if res.Integrity != audit.ChainVerified {
		t.Errorf("single-row window = %q, want verified", res.Integrity)
	}
}
