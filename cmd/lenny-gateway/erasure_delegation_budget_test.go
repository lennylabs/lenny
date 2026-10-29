// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// budgetEraseCall is one call the delegation budget eraser made, in order.
type budgetEraseCall struct{ op, tenant, root string }

// recordingBudgetBackends records PurgeRoot and DeleteByRoot calls into one
// ordered log and returns the configured counts and errors.
type recordingBudgetBackends struct {
	calls               []budgetEraseCall
	purged, rowsGone    int
	purgeErr, deleteErr error
}

func (r *recordingBudgetBackends) PurgeRoot(_ context.Context, root string) (int, error) {
	r.calls = append(r.calls, budgetEraseCall{"purge", "", root})
	return r.purged, r.purgeErr
}

func (r *recordingBudgetBackends) DeleteByRoot(_ context.Context, tenantID, root string) (int, error) {
	r.calls = append(r.calls, budgetEraseCall{"delete", tenantID, root})
	return r.rowsGone, r.deleteErr
}

// budgetOrchestrator runs the delegation budget eraser over r for a user
// whose sessions are sessionIDs.
func budgetOrchestrator(r *recordingBudgetBackends, sessionIDs ...string) *erasure.Orchestrator {
	return erasure.New(erasure.Config{
		Sessions: func(context.Context, string, string) ([]string, error) { return sessionIDs, nil },
		SessionScoped: []erasure.SessionEraser{
			{Name: "delegation_budget", DeleteBySession: delegationBudgetRootEraser(r, r)},
		},
	})
}

// TestUserErasureDeletesTheRootsDelegationTreeBudgetRow_spec_12_8 pins that
// the user erasure's delegation budget step purges a root's keys and then
// deletes its delegation_tree_budget row under the erased user's tenant, and
// that Orchestrator.DeleteByUser returns the error when either call fails.
// Before the step deleted the row, the erasure left the tree's counters,
// denial, and grants in Postgres after the session rows were gone.
//
// spec: 12.8 (Compliance Interfaces)
func TestUserErasureDeletesTheRootsDelegationTreeBudgetRow_spec_12_8(t *testing.T) {
	ctx := context.Background()

	t.Run("purge then row delete under the received tenant", func(t *testing.T) {
		r := &recordingBudgetBackends{purged: 3, rowsGone: 1}
		res, err := budgetOrchestrator(r, "root-a").DeleteByUser(ctx, "acme", "alice@acme.com")
		if err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		want := []budgetEraseCall{{"purge", "", "root-a"}, {"delete", "acme", "root-a"}}
		if len(r.calls) != len(want) || r.calls[0] != want[0] || r.calls[1] != want[1] {
			t.Fatalf("calls = %v, want %v", r.calls, want)
		}
		if res.Deleted["delegation_budget"] != 4 {
			t.Fatalf("delegation_budget count = %d, want 4", res.Deleted["delegation_budget"])
		}
	})

	failures := []struct {
		name      string
		backends  *recordingBudgetBackends
		wantCalls int
	}{
		{"a PurgeRoot error fails the step before the row delete", &recordingBudgetBackends{purgeErr: errors.New("redis down")}, 1},
		{"a DeleteByRoot error fails the step", &recordingBudgetBackends{deleteErr: errors.New("postgres down")}, 2},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			cause := tc.backends.purgeErr
			if cause == nil {
				cause = tc.backends.deleteErr
			}
			_, err := budgetOrchestrator(tc.backends, "root-a", "root-b").DeleteByUser(ctx, "acme", "alice@acme.com")
			if !errors.Is(err, cause) {
				t.Fatalf("DeleteByUser error = %v, want it to wrap %v", err, cause)
			}
			if !strings.Contains(err.Error(), `"delegation_budget"`) {
				t.Fatalf("DeleteByUser error %q does not name the delegation_budget step", err)
			}
			if len(tc.backends.calls) != tc.wantCalls {
				t.Fatalf("calls = %v, want %d before the job stops", tc.backends.calls, tc.wantCalls)
			}
		})
	}
}

// TestDelegationBudgetSessionEraserPostures_spec_12_8 pins which backends
// the delegation budget eraser runs in each gateway posture: none without
// Redis or Postgres, the row delete alone with Postgres only, and both with
// Redis and Postgres.
//
// spec: 12.8 (Compliance Interfaces)
func TestDelegationBudgetSessionEraserPostures_spec_12_8(t *testing.T) {
	if fn := delegationBudgetRootEraser(nil, nil); fn != nil {
		t.Fatal("the eraser has a function with neither backend")
	}
	rows := &recordingBudgetBackends{rowsGone: 1}
	n, err := delegationBudgetRootEraser(nil, rows)(context.Background(), "acme", "root-a")
	if err != nil || n != 1 || len(rows.calls) != 1 || rows.calls[0] != (budgetEraseCall{"delete", "acme", "root-a"}) {
		t.Fatalf("rows-only eraser = (%d, %v) with calls %v, want one acme row delete", n, err, rows.calls)
	}
	keys := &recordingBudgetBackends{purged: 2}
	n, err = delegationBudgetRootEraser(keys, nil)(context.Background(), "acme", "root-a")
	if err != nil || n != 2 || len(keys.calls) != 1 || keys.calls[0].op != "purge" {
		t.Fatalf("keys-only eraser = (%d, %v) with calls %v, want one purge", n, err, keys.calls)
	}

	w := &gatewayWiring{}
	if w.delegationBudgetSessionEraser() != nil {
		t.Fatal("the wiring has a delegation budget eraser with neither backend")
	}
	w.pgPool = lazyPool(t)
	if w.delegationBudgetSessionEraser() == nil {
		t.Fatal("the wiring has no delegation budget eraser with Postgres configured")
	}
	redisPosture(t, w)
	if w.delegationBudgetSessionEraser() == nil {
		t.Fatal("the wiring has no delegation budget eraser with Redis and Postgres configured")
	}
}
