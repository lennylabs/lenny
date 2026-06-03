// SPDX-License-Identifier: MIT

package operations_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// spec §25.2 line 393: a lookup before any completion reports no baseline.
func TestMemoryBaselineStoreEmptyLookup(t *testing.T) {
	s := operations.NewMemoryBaselineStore()
	if _, ok, err := s.Lookup(context.Background(), operations.KindPlatformUpgrade); err != nil || ok {
		t.Fatalf("Lookup empty = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

// spec §25.2 lines 393-394: completions fold into the kind's p50/p90 and
// the sample_size grows with each recorded completion.
func TestMemoryBaselineStoreRecordsPercentiles(t *testing.T) {
	s := operations.NewMemoryBaselineStore()
	ctx := context.Background()
	// Ten samples: 1..10 minutes. Nearest-rank p50 (rank=ceil(0.5*10)=5)
	// is the 5th smallest = 5 min; p90 (rank=9) = 9 min.
	for i := 1; i <= 10; i++ {
		if err := s.RecordCompletion(ctx, operations.KindRestore, time.Duration(i)*time.Minute); err != nil {
			t.Fatalf("RecordCompletion: %v", err)
		}
	}
	b, ok, err := s.Lookup(ctx, operations.KindRestore)
	if err != nil || !ok {
		t.Fatalf("Lookup = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if b.SampleSize != 10 {
		t.Errorf("SampleSize = %d, want 10", b.SampleSize)
	}
	if b.P50 != 5*time.Minute {
		t.Errorf("P50 = %v, want 5m", b.P50)
	}
	if b.P90 != 9*time.Minute {
		t.Errorf("P90 = %v, want 9m", b.P90)
	}
	if b.LastUpdated.IsZero() {
		t.Errorf("LastUpdated is zero")
	}
}

// spec §25.2 line 394: a single completion seeds both percentiles with
// that one sample (sample_size 1, below the historical_p50 threshold).
func TestMemoryBaselineStoreSingleSample(t *testing.T) {
	s := operations.NewMemoryBaselineStore()
	ctx := context.Background()
	_ = s.RecordCompletion(ctx, operations.KindBackup, 7*time.Minute)
	b, ok, _ := s.Lookup(ctx, operations.KindBackup)
	if !ok || b.SampleSize != 1 || b.P50 != 7*time.Minute || b.P90 != 7*time.Minute {
		t.Fatalf("single sample baseline = %+v (ok=%v)", b, ok)
	}
}
