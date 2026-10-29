// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// classifyFixture builds a valid contiguous chain of n rows (sequence 1..n)
// for tenant acme, each row linking to its predecessor with the canonical
// §11.7 construction. Each case below mutates a copy of it.
func classifyFixture(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		r := Row{
			ID:                 fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1),
			Seq:                uint64(i + 1),
			TenantID:           "acme",
			EventType:          "admin.tenant.updated",
			EventSchemaVersion: DefaultEventSchemaVersion,
			Payload:            json.RawMessage(fmt.Sprintf(`{"step":%d}`, i+1)),
			Timestamp:          ts().Add(time.Duration(i) * time.Minute),
		}
		if i == 0 {
			r.PrevHash = GenesisPrevHash
		} else {
			r.PrevHash = LinkHash(rows[i-1])
		}
		r.Hash = ComputeHash(r)
		rows[i] = r
	}
	return rows
}

// deleteSeqs returns rows with every row whose sequence number is listed
// removed, modelling an erasure-mode DELETE of committed rows.
func deleteSeqs(rows []Row, seqs ...uint64) []Row {
	drop := map[uint64]bool{}
	for _, s := range seqs {
		drop[s] = true
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if !drop[r.Seq] {
			out = append(out, r)
		}
	}
	return out
}

// TestVerifyRowsClassifiesByLinkBeforeSequence pins the §25.9 per-row
// chainIntegrity classification of every chain condition the audit-events
// list can surface. The prev_hash linkage decides between a benign gap
// and a break: a sequence gap whose prev_hash links across it is
// gap_suspected, and a gap whose prev_hash does not link (a removed
// committed row) is broken. Before BUILD-GAPS F-25.9.21 the classifier
// checked sequence density first, so the deleted-interior-row cases below
// reported gap_suspected and chainIntegrityReport.broken stayed 0.
//
// spec: 25.9 (Audit Log Query API, Chain gap detection), 11.7 (Audit
// Logging, Authoritative total order)
func TestVerifyRowsClassifiesByLinkBeforeSequence(t *testing.T) {
	t.Parallel()
	type want = map[uint64]ChainIntegrity
	cases := []struct {
		name     string
		rows     func() []Row
		receipts map[uint64]RedactionReceipt
		want     want
		broken   int
	}{
		{
			name: "intact chain verifies every row",
			rows: func() []Row { return classifyFixture(5) },
			want: want{1: ChainVerified, 2: ChainVerified, 3: ChainVerified, 4: ChainVerified, 5: ChainVerified},
		},
		{
			name: "deleted interior row breaks the row after the gap",
			rows: func() []Row { return deleteSeqs(classifyFixture(5), 3) },
			// Row 4's prev_hash links to the absent row 3, not to row 2.
			// Row 5 still links to row 4, so only row 4 carries the break.
			want:   want{1: ChainVerified, 2: ChainVerified, 4: ChainBroken, 5: ChainVerified},
			broken: 1,
		},
		{
			name:   "two adjacent deleted interior rows are one break",
			rows:   func() []Row { return deleteSeqs(classifyFixture(6), 3, 4) },
			want:   want{1: ChainVerified, 2: ChainVerified, 5: ChainBroken, 6: ChainVerified},
			broken: 1,
		},
		{
			name:   "two separate deleted interior rows are two breaks",
			rows:   func() []Row { return deleteSeqs(classifyFixture(7), 2, 5) },
			want:   want{1: ChainVerified, 3: ChainBroken, 4: ChainVerified, 6: ChainBroken, 7: ChainVerified},
			broken: 2,
		},
		{
			name: "tampered payload breaks only the tampered row",
			rows: func() []Row {
				rows := classifyFixture(4)
				rows[2].Payload = json.RawMessage(`{"step":"forged"}`)
				return rows
			},
			// Row 4 links to row 3's stored hash, which is unchanged.
			want:   want{1: ChainVerified, 2: ChainVerified, 3: ChainBroken, 4: ChainVerified},
			broken: 1,
		},
		{
			name: "rewritten prev_hash on a contiguous row is broken",
			rows: func() []Row {
				rows := classifyFixture(3)
				rows[1].PrevHash = "deadbeef"
				rows[1].Hash = ComputeHash(rows[1])
				return rows
			},
			want:   want{1: ChainVerified, 2: ChainBroken, 3: ChainBroken},
			broken: 2,
		},
		{
			name: "nextval rollback gap with an intact link is gap_suspected",
			rows: func() []Row {
				rows := classifyFixture(2)
				// Sequence 3 was rolled back; the committed sequence-4 row
				// links to the committed tail row 2.
				r := Row{
					ID: "00000000-0000-4000-8000-000000000004", Seq: 4, TenantID: "acme",
					EventType: "admin.tenant.updated", EventSchemaVersion: DefaultEventSchemaVersion,
					Payload: json.RawMessage(`{"step":4}`), Timestamp: ts().Add(3 * time.Minute),
					PrevHash: LinkHash(rows[1]),
				}
				r.Hash = ComputeHash(r)
				return append(rows, r)
			},
			want: want{1: ChainVerified, 2: ChainVerified, 4: ChainGapSuspected},
		},
		{
			name:   "deleted first row breaks the new head",
			rows:   func() []Row { return deleteSeqs(classifyFixture(4), 1) },
			want:   want{2: ChainBroken, 3: ChainVerified, 4: ChainVerified},
			broken: 1,
		},
		{
			// A trailing deletion leaves no successor carrying a stale
			// prev_hash, so the per-row chain cannot observe it. The §11.7
			// SIEM delivery high-water mark covers tail truncation.
			name: "deleted last row leaves the remaining rows verified",
			rows: func() []Row { return deleteSeqs(classifyFixture(4), 4) },
			want: want{1: ChainVerified, 2: ChainVerified, 3: ChainVerified},
		},
		{
			name: "receipted GDPR redaction is redacted_gdpr and its successor verifies",
			rows: func() []Row {
				rows := classifyFixture(3)
				rows[1].Payload = json.RawMessage(`{"step":"[REDACTED]"}`)
				rows[1].Redacted = true
				return rows
			},
			receipts: map[uint64]RedactionReceipt{2: {RedactedSeq: 2, Signature: "kms-sig"}},
			want:     want{1: ChainVerified, 2: ChainRedactedGDPR, 3: ChainVerified},
		},
		{
			name: "redaction without a receipt is broken",
			rows: func() []Row {
				rows := classifyFixture(3)
				rows[1].Payload = json.RawMessage(`{"step":"[REDACTED]"}`)
				rows[1].Redacted = true
				return rows
			},
			want:   want{1: ChainVerified, 2: ChainBroken, 3: ChainVerified},
			broken: 1,
		},
		{
			name: "cross-tenant row is broken",
			rows: func() []Row {
				rows := classifyFixture(3)
				rows[2].TenantID = "globex"
				rows[2].Hash = ComputeHash(rows[2])
				return rows
			},
			want:   want{1: ChainVerified, 2: ChainVerified, 3: ChainBroken},
			broken: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := tc.rows()
			receipts := map[uint64]RedactionReceipt{}
			for seq, r := range tc.receipts {
				// The receipt binds the row's preserved pre-redaction hash.
				for _, row := range rows {
					if row.Seq == seq {
						r.OriginalHash = row.Hash
					}
				}
				receipts[seq] = r
			}
			got := ChainFromRows("acme", rows, receipts).VerifyRows()
			if len(got) != len(tc.want) {
				t.Fatalf("classified %d rows, want %d (%v)", len(got), len(tc.want), got)
			}
			broken := 0
			for seq, w := range tc.want {
				if got[seq] != w {
					t.Errorf("row %d = %q, want %q", seq, got[seq], w)
				}
				if got[seq] == ChainBroken {
					broken++
				}
			}
			if broken != tc.broken {
				t.Errorf("broken rows = %d, want %d", broken, tc.broken)
			}
			// The collapsed verdict agrees: any per-row break is a broken
			// chain, and a chain with no break is never reported broken.
			v := ChainFromRows("acme", rows, receipts).Verify()
			if (tc.broken > 0) != (v.Integrity == ChainBroken) {
				t.Errorf("Verify = %q (%s), want broken=%v", v.Integrity, v.Detail, tc.broken > 0)
			}
		})
	}
}

// TestVerifyRowsDeletionAcrossPageBoundary pins that the per-row verdict
// does not depend on where a page boundary falls. The §25.9 list computes
// every row's verdict over the tenant's full ordered chain and pages the
// result by sequence number, so a deletion between the last row of one
// page and the first row of the next still breaks the first row of the
// next page, and the rows on either side of the boundary otherwise verify.
//
// spec: 25.9 (Audit Log Query API, Chain gap detection)
func TestVerifyRowsDeletionAcrossPageBoundary(t *testing.T) {
	t.Parallel()
	const pageSize = 3
	full := classifyFixture(10)
	// Delete sequence 4: page 1 is rows 1..3, page 2 starts at row 5.
	rows := deleteSeqs(full, 4)
	got := ChainFromRows("acme", rows, nil).VerifyRows()

	for p := 0; p*pageSize < len(rows); p++ {
		end := min((p+1)*pageSize, len(rows))
		for _, row := range rows[p*pageSize : end] {
			want := ChainVerified
			if row.Seq == 5 {
				want = ChainBroken
			}
			if got[row.Seq] != want {
				t.Errorf("page %d row %d = %q, want %q", p+1, row.Seq, got[row.Seq], want)
			}
		}
	}
}
