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

// TestVerifyChainWindowSequenceGap covers the §12.3 "gap between sequence
// n and m" signal: a missing committed row leaves a hole the check
// reports with the boundary sequences and timestamp range.
func TestVerifyChainWindowSequenceGap(t *testing.T) {
	full := buildChain("acme", 6)
	// Drop sequence 4: rows now run 1,2,3,5,6.
	gapped := append(append([]audit.Row{}, full[:3]...), full[4:]...)
	res, gap := verifyChainWindow("acme", gapped)
	if res.Integrity != audit.ChainBroken {
		t.Fatalf("dropped row = %q, want broken", res.Integrity)
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
