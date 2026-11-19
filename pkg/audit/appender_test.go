// SPDX-License-Identifier: MIT

package audit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// spec: §11.7 (Audit Logging)
func TestChainSetAppenderSealsRowsOnTheTenantChain(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	chains := audit.NewChainSet()
	a := audit.NewChainSetAppender(chains, func() time.Time { return clock })

	first, err := a.Append(context.Background(), "acme", "node.drain.forced", json.RawMessage(`{"n":1}`), time.Time{})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !first.Timestamp.Equal(clock) {
		t.Fatalf("Timestamp = %v for a zero time, want the appender clock %v", first.Timestamp, clock)
	}
	at := clock.Add(time.Minute)
	second, err := a.Append(context.Background(), "acme", "node.drain.forced", json.RawMessage(`{"n":2}`), at)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !second.Timestamp.Equal(at) {
		t.Fatalf("Timestamp = %v, want the given time %v", second.Timestamp, at)
	}
	if second.PrevHash != first.Hash {
		t.Fatalf("second row PrevHash = %q, want the first row's hash %q", second.PrevHash, first.Hash)
	}
}

func TestNewChainSetAppenderDefaultsItsClockToUTC(t *testing.T) {
	a := audit.NewChainSetAppender(audit.NewChainSet(), nil)
	row, err := a.Append(context.Background(), "acme", "node.drain.forced", json.RawMessage(`{}`), time.Time{})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if row.Timestamp.IsZero() || row.Timestamp.Location() != time.UTC {
		t.Fatalf("Timestamp = %v, want a UTC time from the default clock", row.Timestamp)
	}
}
