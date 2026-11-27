// SPDX-License-Identifier: MIT

package integrity

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// TestTheContinuityCheckFollowsTheLinkThroughAReceiptedRedaction pins that
// the startup and periodic chain-continuity check reads a redacted row's
// receipt: the row after a redacted row carries the receipt's
// original_hash, so with the receipt the window links and reads verified,
// and without it the link reads broken at the successor. The redacted row
// is the window's first row and the successor its newest, the layout an
// erasure of a tenant's newest row leaves behind.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §12.3
// (Postgres HA Requirements)
func TestTheContinuityCheckFollowsTheLinkThroughAReceiptedRedaction(t *testing.T) {
	orig := bytes.Repeat([]byte{0xab}, 32)
	chain := [][]any{
		{int64(5), "session.created", []byte(`{"redacted":true,"event_type":"session.created"}`), time.Time{}, []byte("aa"), "00000000-0000-4000-8000-000000000005", "v1"},
		{int64(6), "gdpr.erasure_deadletter_redacted", []byte(`{}`), time.Time{}, orig, "00000000-0000-4000-8000-000000000006", "v1"},
	}
	for _, c := range []struct {
		name     string
		receipts [][]any
		want     audit.ChainIntegrity
	}{
		{"with the receipt", [][]any{{int64(5), orig}}, audit.ChainVerified},
		{"without a receipt", nil, audit.ChainBroken},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := &scriptQuerier{tenants: [][]any{{"acme"}}, chainRows: chain, redaction: c.receipts}
			results, err := CheckChainContinuityRecent(context.Background(), q, q, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Result.Integrity != c.want {
				t.Fatalf("results = %+v, want one result %q", results, c.want)
			}
		})
	}
}

// TestTheContinuityCheckReadsNoReceiptForAWindowWithoutARedaction pins that
// a window with no redaction-marked row costs no receipt read: a querier
// whose receipt rows would fail to scan is never asked for them.
//
// spec: §12.3 (Postgres HA Requirements)
func TestTheContinuityCheckReadsNoReceiptForAWindowWithoutARedaction(t *testing.T) {
	q := &scriptQuerier{
		tenants: [][]any{{"acme"}},
		chainRows: [][]any{
			{int64(5), "session.created", []byte(`{}`), time.Time{}, []byte("aa"), "00000000-0000-4000-8000-000000000005", "v1"},
		},
		redaction: [][]any{{"not-a-sequence", 1.5}},
	}
	if _, err := CheckChainContinuityRecent(context.Background(), q, q, 1000); err != nil {
		t.Fatalf("CheckChainContinuityRecent read the receipts of a window with no redaction: %v", err)
	}
}
