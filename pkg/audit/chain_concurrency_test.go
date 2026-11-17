// SPDX-License-Identifier: MIT

package audit_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// This concurrency contract test runs the package's public surface from
// N goroutines with the race detector on and asserts the documented
// invariants hold under contention, as
// `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`
// does (TESTING.md §6.2).

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
