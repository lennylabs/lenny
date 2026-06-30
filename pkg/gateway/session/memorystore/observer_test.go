// SPDX-License-Identifier: MIT

package memorystore_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
)

// fakeObserver captures the §9.4 / §16.1 lines 151-154 instrumentation
// calls so tests can assert observation counts and labels without
// pulling in Prometheus.
type fakeObserver struct {
	mu             sync.Mutex
	ops            []string
	errors         []errEntry
	recordCounts   map[string]int
	overThresholds map[string]int
}

type errEntry struct{ op, errorType string }

func newFake() *fakeObserver {
	return &fakeObserver{
		recordCounts:   map[string]int{},
		overThresholds: map[string]int{},
	}
}

func (o *fakeObserver) ObserveOperation(op string, _ float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ops = append(o.ops, op)
}

func (o *fakeObserver) IncError(op, errorType string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errors = append(o.errors, errEntry{op, errorType})
}

func (o *fakeObserver) SetRecordCount(tenantID string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recordCounts[tenantID] = n
}

func (o *fakeObserver) IncUserOverThreshold(tenantID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.overThresholds[tenantID]++
}

func (o *fakeObserver) opCount(op string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, s := range o.ops {
		if s == op {
			n++
		}
	}
	return n
}

// TestObserverFiresOnAllSixOperations_spec_9_4_F_9_4_1 pins the §9.4
// line 200 "all six operation labels emitted" contract: every public
// Store method calls ObserveOperation with the catalog-cited label.
func TestObserverFiresOnAllSixOperations_spec_9_4_F_9_4_1(t *testing.T) {
	obs := newFake()
	s := memorystore.NewInMemory(0, nil)
	s.SetObserver(obs)
	ctx := context.Background()
	scope := memorystore.MemoryScope{TenantID: "acme", UserID: "alice"}
	if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "hello"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := s.Query(ctx, scope, "hello", 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if _, err := s.List(ctx, scope, memorystore.MemoryFilter{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := s.Delete(ctx, scope, []string{"nonexistent"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if err := s.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	for _, op := range []string{
		memorystore.OpWrite,
		memorystore.OpQuery,
		memorystore.OpList,
		memorystore.OpDelete,
		memorystore.OpDeleteByUser,
		memorystore.OpDeleteByTenant,
	} {
		if obs.opCount(op) == 0 {
			t.Errorf("operation %q recorded 0 observations", op)
		}
	}
}

// TestObserverIncErrorOnEmptyScope_spec_9_4_F_9_4_1 covers the §9.4
// empty-scope rejection branch: the duration histogram still records
// one observation (the call entered the wrapper) and the error
// counter increments with `empty_scope` so operators can distinguish
// caller misuse from backend failure.
func TestObserverIncErrorOnEmptyScope_spec_9_4_F_9_4_1(t *testing.T) {
	obs := newFake()
	s := memorystore.NewInMemory(0, nil)
	s.SetObserver(obs)
	if err := s.Write(context.Background(),
		memorystore.MemoryScope{UserID: "alice"},
		[]memorystore.Memory{{Content: "x"}}); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Fatalf("Write empty tenant: %v", err)
	}
	if obs.opCount(memorystore.OpWrite) != 1 {
		t.Fatalf("OpWrite recorded %d, want 1", obs.opCount(memorystore.OpWrite))
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.errors) != 1 || obs.errors[0].op != memorystore.OpWrite || obs.errors[0].errorType != "empty_scope" {
		t.Fatalf("IncError = %+v; want one empty_scope on write", obs.errors)
	}
}

// TestThresholdCountRoundsToEightyPercent_spec_9_4_F_9_4_6 pins the
// §9.4 line 202 "leaves the user at >= 80%" rounding rule across the
// boundary values the in-memory eviction can produce.
func TestThresholdCountRoundsToEightyPercent_spec_9_4_F_9_4_6(t *testing.T) {
	cases := []struct {
		max, want int
	}{
		{10000, 8000},
		{10, 8},
		{5, 4},
		{4, 3}, // 4*8/10 = 3 → still allows the threshold to be reached
		{1, 1}, // small bound rounds up to 1
		{0, 0},
		{-1, 0},
	}
	for _, c := range cases {
		if got := memorystore.ThresholdCount(c.max); got != c.want {
			t.Errorf("ThresholdCount(%d) = %d, want %d", c.max, got, c.want)
		}
	}
}

// TestWriteIncrementsOverThreshold_spec_9_4_F_9_4_6 pins the §9.4
// line 202 contract: a Write that leaves the user at >= 80% of the
// per-user cap increments the threshold counter and emits the
// structured log line; a Write that stays under the threshold does
// not.
func TestWriteIncrementsOverThreshold_spec_9_4_F_9_4_6(t *testing.T) {
	obs := newFake()
	s := memorystore.NewInMemory(10, nil) // 80% threshold = 8
	s.SetObserver(obs)
	scope := memorystore.MemoryScope{TenantID: "acme", UserID: "alice"}
	ctx := context.Background()
	// Write 7 — under the 8 threshold, no counter.
	for i := 0; i < 7; i++ {
		if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if obs.overThresholds["acme"] != 0 {
		t.Fatalf("over-threshold counter: got %d after 7 writes (max=10), want 0",
			obs.overThresholds["acme"])
	}
	// Write 8th — crosses the 80% threshold.
	if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
		t.Fatalf("Write 8: %v", err)
	}
	if obs.overThresholds["acme"] != 1 {
		t.Fatalf("over-threshold counter after the 8th write: got %d, want 1",
			obs.overThresholds["acme"])
	}
	// 9th, 10th — still >= 80%, each commit increments the counter
	// per spec line 202 "incremented on each Write commit that leaves
	// the writing user at >= 80%".
	if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
		t.Fatalf("Write 9: %v", err)
	}
	if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
		t.Fatalf("Write 10: %v", err)
	}
	if obs.overThresholds["acme"] != 3 {
		t.Fatalf("over-threshold counter after 10 writes: got %d, want 3 (writes 8,9,10)",
			obs.overThresholds["acme"])
	}
}

// TestWriteEmitsRecordCount_spec_9_4_F_9_4_1 pins the §9.4 line 202 /
// §16.1 line 153 record-count gauge update on the Write path. The
// gauge reflects the per-tenant total, not the per-user count.
func TestWriteEmitsRecordCount_spec_9_4_F_9_4_1(t *testing.T) {
	obs := newFake()
	s := memorystore.NewInMemory(0, nil)
	s.SetObserver(obs)
	ctx := context.Background()
	if err := s.Write(ctx,
		memorystore.MemoryScope{TenantID: "acme", UserID: "alice"},
		[]memorystore.Memory{{Content: "a"}}); err != nil {
		t.Fatalf("Write alice: %v", err)
	}
	if err := s.Write(ctx,
		memorystore.MemoryScope{TenantID: "acme", UserID: "bob"},
		[]memorystore.Memory{{Content: "b"}}); err != nil {
		t.Fatalf("Write bob: %v", err)
	}
	if obs.recordCounts["acme"] != 2 {
		t.Fatalf("record count: got %d, want 2 (alice + bob)", obs.recordCounts["acme"])
	}
}

// TestDeleteByTenantEmitsZero_spec_9_4_F_9_4_1 covers the §9.4
// erasure post-condition: after a tenant's records are fully purged
// the gauge falls to 0 so a stale value does not survive the bulk
// erasure.
func TestDeleteByTenantEmitsZero_spec_9_4_F_9_4_1(t *testing.T) {
	obs := newFake()
	s := memorystore.NewInMemory(0, nil)
	s.SetObserver(obs)
	ctx := context.Background()
	scope := memorystore.MemoryScope{TenantID: "acme", UserID: "alice"}
	if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if obs.recordCounts["acme"] != 1 {
		t.Fatalf("record count after write: %d, want 1", obs.recordCounts["acme"])
	}
	if err := s.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if obs.recordCounts["acme"] != 0 {
		t.Fatalf("record count after DeleteByTenant: %d, want 0", obs.recordCounts["acme"])
	}
}

// TestTenantRecordCountsAggregates_spec_9_4_F_9_4_1 pins the §9.4
// line 202 record-count sampler signature: TenantRecordCounts walks
// the in-memory state and emits one entry per tenant with the count
// aggregated across users.
func TestTenantRecordCountsAggregates_spec_9_4_F_9_4_1(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	for _, scope := range []memorystore.MemoryScope{
		{TenantID: "acme", UserID: "alice"},
		{TenantID: "acme", UserID: "bob"},
		{TenantID: "globex", UserID: "alice"},
	} {
		if err := s.Write(ctx, scope, []memorystore.Memory{{Content: "x"}}); err != nil {
			t.Fatalf("Write %+v: %v", scope, err)
		}
	}
	got, err := s.TenantRecordCounts(ctx)
	if err != nil {
		t.Fatalf("TenantRecordCounts: %v", err)
	}
	if got["acme"] != 2 || got["globex"] != 1 {
		t.Fatalf("TenantRecordCounts = %v; want {acme:2, globex:1}", got)
	}
}

// TestClassifyError_spec_9_4_F_9_4_1 pins the §16.1 error_type label
// shape: empty-scope errors collapse to a stable label so the
// catalog cardinality stays bounded.
func TestClassifyError_spec_9_4_F_9_4_1(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{memorystore.ErrEmptyTenant, "empty_scope"},
		{memorystore.ErrEmptyUser, "empty_scope"},
		{errors.New("backend down"), "internal"},
	}
	for _, c := range cases {
		if got := memorystore.ClassifyError(c.err); got != c.want {
			t.Errorf("ClassifyError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// TestNoopObserverIsSafe_spec_9_4_F_9_4_1 confirms a Store with no
// Observer wired runs to completion: SetObserver(NoopObserver{})
// satisfies the interface and never panics.
func TestNoopObserverIsSafe_spec_9_4_F_9_4_1(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	s.SetObserver(memorystore.NoopObserver{})
	if err := s.Write(context.Background(),
		memorystore.MemoryScope{TenantID: "acme", UserID: "alice"},
		[]memorystore.Memory{{Content: "x"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// TestOperationLabelsAreStable_spec_9_4_F_9_4_1 guards against
// accidental rename of a label the §16.5 alerts depend on. The
// catalog cites all six string literals; this test pins the names.
func TestOperationLabelsAreStable_spec_9_4_F_9_4_1(t *testing.T) {
	want := map[string]string{
		memorystore.OpWrite:          "write",
		memorystore.OpQuery:          "query",
		memorystore.OpDelete:         "delete",
		memorystore.OpList:           "list",
		memorystore.OpDeleteByUser:   "delete_by_user",
		memorystore.OpDeleteByTenant: "delete_by_tenant",
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("label drift: constant value %q does not equal expected %q", got, expected)
		}
	}
	// ThresholdFraction is referenced by the structured log line.
	// A drift away from 0.8 breaks the §9.4 line 202 contract.
	if memorystore.ThresholdFraction != 0.8 {
		t.Errorf("ThresholdFraction = %v, want 0.8", memorystore.ThresholdFraction)
	}
	// Ensure ClassifyError returns a stable empty-error code so
	// catalog readers can rely on it.
	if !strings.Contains(memorystore.ClassifyError(memorystore.ErrEmptyTenant), "empty") {
		t.Errorf("ClassifyError ErrEmptyTenant did not contain 'empty'")
	}
}
