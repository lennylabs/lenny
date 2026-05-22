// SPDX-License-Identifier: MIT

package concurrency

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/leaseextension"
	"github.com/lennylabs/lenny/pkg/quota"
)

// TestAuditChainAppendsAreSerialized runs N goroutines appending to
// one Chain. The §11.7 invariant: every row has a unique sequence
// number, Verify reports ChainVerified, and the row count equals the
// goroutine count.
func TestAuditChainAppendsAreSerialized(t *testing.T) {
	t.Parallel()
	chain := audit.NewChain("acme")
	const N = 200
	payload := json.RawMessage(`{"v":1}`)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = chain.Append("session.created", payload, time.Now())
		}()
	}
	wg.Wait()
	if got := chain.Len(); got != N {
		t.Errorf("Len=%d want %d", got, N)
	}
	v := chain.Verify()
	if v.Integrity != audit.ChainVerified {
		t.Errorf("Integrity=%s want ChainVerified", v.Integrity)
	}
	rows := chain.Rows()
	seen := map[uint64]bool{}
	for _, r := range rows {
		if seen[r.Seq] {
			t.Errorf("duplicate seq %d", r.Seq)
		}
		seen[r.Seq] = true
	}
}

// TestQuotaHierarchicalCheckIsPure asserts HierarchicalCheck has no
// hidden shared state: N goroutines invoking it on independent inputs
// observe the documented State for their inputs.
func TestQuotaHierarchicalCheckIsPure(t *testing.T) {
	t.Parallel()
	h := quota.Hierarchy{Global: 1000, Tenant: 100, User: 10}
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// User-exceeded scenario.
			res := quota.HierarchicalCheck(int64(i%50), int64(i%50), 100, h)
			if res.State != quota.StateHardExceeded {
				t.Errorf("State=%s want HardExceeded (user limit hit)", res.State)
			}
		}()
	}
	wg.Wait()
}

// TestIdempotencyDetectReuseIsPure asserts DetectReuse has no shared
// state between concurrent invocations.
func TestIdempotencyDetectReuseIsPure(t *testing.T) {
	t.Parallel()
	key := idempotency.Key{TenantID: "acme", Value: "k-1"}
	now := time.Now()
	hash := idempotency.HashBody([]byte(`{"x":1}`))
	stored := idempotency.Record{Key: key, BodyHash: hash, StoredAt: now}
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			action, err := idempotency.DetectReuse(stored, hash, now)
			if action != idempotency.ActionReplay || err != nil {
				t.Errorf("action=%v err=%v want (Replay, nil)", action, err)
			}
		}()
	}
	wg.Wait()
}

// TestLeaseExtensionGrantNeverOversteps asserts Grant always returns
// a non-negative granted increment that keeps current+granted ≤ ceiling
// under N goroutines.
func TestLeaseExtensionGrantNeverOversteps(t *testing.T) {
	t.Parallel()
	const ceiling = int64(100)
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			current := int64(i % 80)
			requested := current + int64(i%50)
			granted, _ := leaseextension.Grant(current, requested, ceiling)
			if granted < 0 {
				t.Errorf("§4.9 violated: granted=%d < 0", granted)
			}
			if current+granted > ceiling {
				t.Errorf("§4.9 violated: current(%d)+granted(%d) > ceiling(%d)", current, granted, ceiling)
			}
		}()
	}
	wg.Wait()
}
