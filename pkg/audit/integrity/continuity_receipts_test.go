// SPDX-License-Identifier: MIT

package integrity

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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

// receiptFailingQuerier is a scriptQuerier whose redaction receipt read
// fails, either at the query or while iterating its rows.
type receiptFailingQuerier struct {
	*scriptQuerier
	queryErr error
	iterErr  error
}

func (q receiptFailingQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "original_hash FROM audit_redaction_receipts") {
		if q.queryErr != nil {
			return nil, q.queryErr
		}
		return &scriptRows{iterErr: q.iterErr}, nil
	}
	return q.scriptQuerier.Query(ctx, sql, args...)
}

// TestTheContinuityCheckFailsWhenTheReceiptsCannotBeRead pins that a
// receipt read that fails, at the query or while iterating, fails the
// continuity check with the error rather than reporting the chain, so a
// read failure is never mistaken for a broken or an intact chain.
//
// spec: §12.3 (Postgres HA Requirements), §12.8 (Compliance Interfaces)
func TestTheContinuityCheckFailsWhenTheReceiptsCannotBeRead(t *testing.T) {
	chain := [][]any{
		{int64(5), "session.created", []byte(`{"redacted":true}`), time.Time{}, []byte("aa"), "00000000-0000-4000-8000-000000000005", "v1"},
	}
	readErr := errors.New("receipts unreadable")
	for name, q := range map[string]receiptFailingQuerier{
		"the query fails":     {queryErr: readErr},
		"the iteration fails": {iterErr: readErr},
	} {
		t.Run(name, func(t *testing.T) {
			q.scriptQuerier = &scriptQuerier{tenants: [][]any{{"acme"}}, chainRows: chain}
			if _, err := CheckChainContinuityRecent(context.Background(), q, q, 1000); !errors.Is(err, readErr) {
				t.Fatalf("CheckChainContinuityRecent = %v, want the read error", err)
			}
			if _, err := CheckChainContinuity(context.Background(), q, q); !errors.Is(err, readErr) {
				t.Fatalf("CheckChainContinuity = %v, want the read error", err)
			}
		})
	}
}
