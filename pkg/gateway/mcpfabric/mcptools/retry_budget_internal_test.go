// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fixedBudget is a RetryBudget that resolves every row to one budget,
// standing in for the session server's deployer caps.
type fixedBudget int

func (b fixedBudget) EffectiveMaxRetries(sessionstore.Session) int { return int(b) }

// TestResolveChildRetriesExhaustedUsesRetryBudget_spec_8_8 pins the
// row-only §8.8 TaskResult error block on the await path to the
// effective §7.3 retry budget. With no resolver wired, a row without a
// retryPolicy resolves to the §7.3 default of two, so one retry leaves
// the budget unspent. A wired resolver carrying a deployer cap of one
// reports the same row as spent.
// spec: §8.8 (TaskRecord and TaskResult Schema), §7.3 (Retry and Resume).
func TestResolveChildRetriesExhaustedUsesRetryBudget_spec_8_8(t *testing.T) {
	cases := []struct {
		name   string
		budget RetryBudget
		want   bool
	}{
		{"no resolver, default budget left", nil, false},
		{"resolver cap spent", fixedBudget(1), true},
		{"resolver cap left", fixedBudget(3), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			now := time.Now()
			if err := store.Create(context.Background(), sessionstore.Session{
				ID: "c", TenantID: "acme", State: session.StateFailed, ParentSessionID: "p",
				FailureReason: string(session.FailurePodEvicted), RetryCount: 1,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed child: %v", err)
			}
			oc, err := resolveChild(context.Background(), store, nil, nil, tc.budget, "acme", "c")
			if err != nil {
				t.Fatalf("resolveChild: %v", err)
			}
			if oc.result.Error == nil {
				t.Fatal("error = nil, want a populated error block")
			}
			if oc.result.Error.RetriesExhausted != tc.want {
				t.Errorf("retriesExhausted = %v, want %v", oc.result.Error.RetriesExhausted, tc.want)
			}
		})
	}
}
