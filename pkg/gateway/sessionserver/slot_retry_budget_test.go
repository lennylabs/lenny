// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

func slotRetriesPtr(n int) *int { return &n }

// TestSlotRetryBudgetFollowsPoolSlotRetries_spec_5_2 pins the §5.2 retry
// budget to the pool's sessionPolicy.slotRetries: an unset value takes the
// default of 1 (two attempts), 0 disables retries (one attempt), a larger
// value allows that many retries, and a negative value that bypassed
// validation is read as 0. Every attempt but the last fails transiently;
// the case reports the attempts made and whether the request ended with
// the §5.2 SlotFailedError. A non-retryable reason ends the loop at once
// whatever the budget.
//
// spec: §5.2 (Slot retry policy)
func TestSlotRetryBudgetFollowsPoolSlotRetries_spec_5_2(t *testing.T) {
	transient := func() error { return slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable) }
	cases := []struct {
		name         string
		slotRetries  *int
		failures     int // leading transient failures before a success
		nonRetryable bool
		wantAttempts int
		wantFailed   bool
	}{
		{name: "unset, retry exhausted", failures: 5, wantAttempts: 2, wantFailed: true},
		{name: "unset, retry succeeds", failures: 1, wantAttempts: 2},
		{name: "zero disables retries", slotRetries: slotRetriesPtr(0), failures: 1, wantAttempts: 1, wantFailed: true},
		{name: "three retries exhausted", slotRetries: slotRetriesPtr(3), failures: 10, wantAttempts: 4, wantFailed: true},
		{name: "three retries, last one succeeds", slotRetries: slotRetriesPtr(3), failures: 3, wantAttempts: 4},
		{name: "negative read as zero", slotRetries: slotRetriesPtr(-2), failures: 1, wantAttempts: 1, wantFailed: true},
		{name: "non-retryable ignores the budget", slotRetries: slotRetriesPtr(3), nonRetryable: true, wantAttempts: 1, wantFailed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binder := &fakeSlotBinder{}
			if tc.nonRetryable {
				binder.errs = []error{slotBindErr("pod-a", "sess-1", "workspace_prep", codes.InvalidArgument)}
			} else {
				for range tc.failures {
					binder.errs = append(binder.errs, transient())
					binder.results = append(binder.results, nil)
				}
				binder.errs = append(binder.errs, nil)
				binder.results = append(binder.results, &podsession.BindResult{SessionID: "sess-1", SandboxName: "pod-a", SlotID: "sess-1"})
			}
			// A large pod keeps every failure below the whole-pod threshold,
			// so no drain interleaves with the retries.
			r := req("pool-x", 100)
			r.SlotRetries = tc.slotRetries
			res, err := applySlotRetryPolicy(context.Background(), binder, slothealth.New(), nil, nil, nil, r)
			if binder.bindCall != tc.wantAttempts {
				t.Errorf("BindSlot attempts = %d, want %d", binder.bindCall, tc.wantAttempts)
			}
			if len(binder.released) != min(tc.wantAttempts, tc.failures+boolInt(tc.nonRetryable)) {
				t.Errorf("released = %d slots, want one per failed attempt", len(binder.released))
			}
			var sf *podsession.SlotFailedError
			if tc.wantFailed {
				if !errors.As(err, &sf) || sf.SessionID != "sess-1" || sf.Pool != "pool-x" {
					t.Fatalf("err = %v, want the §5.2 SlotFailedError for sess-1 on pool-x", err)
				}
				return
			}
			if err != nil || res == nil || res.SandboxName != "pod-a" {
				t.Fatalf("result %+v err %v, want the bound slot", res, err)
			}
		})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestSlotRetriesReachesSlotBindRequest_spec_5_2 pins the plumbing from the
// stored pool to the retry loop: the pool policy mirror copies the stored
// sessionPolicy.slotRetries without aliasing it, an unset value stays nil,
// and slotBindRequest carries the resolved match's value onto the
// SlotBindRequest that applySlotRetryPolicy reads.
//
// spec: §5.2 (Slot retry policy)
func TestSlotRetriesReachesSlotBindRequest_spec_5_2(t *testing.T) {
	ctx := context.Background()
	pools := poolstore.NewMemory()
	for name, sp := range map[string]*runtimestore.SessionPolicy{
		"set":   {MaxConcurrentSessions: 2, AcknowledgeProcessLevelIsolation: true, SlotRetries: slotRetriesPtr(0)},
		"unset": {MaxConcurrentSessions: 2, AcknowledgeProcessLevelIsolation: true},
	} {
		if err := pools.Create(ctx, poolstore.Pool{
			Name: name, RuntimeRef: "rt", ExecutionMode: runtimestore.ExecutionModeSession, SessionPolicy: sp,
		}); err != nil {
			t.Fatalf("create pool %s: %v", name, err)
		}
	}
	reader := NewPoolPolicyReader(pools)

	mirror, found, err := reader.PoolPolicy(ctx, "set")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(set): found=%v err=%v", found, err)
	}
	if mirror.SlotRetries == nil || *mirror.SlotRetries != 0 {
		t.Fatalf("mirror SlotRetries = %v, want 0 from the stored policy", mirror.SlotRetries)
	}
	*mirror.SlotRetries = 9
	again, _, _ := reader.PoolPolicy(ctx, "set")
	if *again.SlotRetries != 0 {
		t.Errorf("mutating the mirror changed the next read to %d; the mirror aliases the stored policy", *again.SlotRetries)
	}
	unset, _, _ := reader.PoolPolicy(ctx, "unset")
	if unset.SlotRetries != nil {
		t.Errorf("unset pool mirror SlotRetries = %d, want nil", *unset.SlotRetries)
	}

	s := &Server{}
	row := sessionstore.Session{UserID: testOwner, ID: "sess-1", TenantID: "acme", RuntimeRef: "rt"}
	match := podsession.PoolMatch{Pool: "set", MaxConcurrentSessions: 2, SlotRetries: slotRetriesPtr(0)}
	got := s.slotBindRequest(ctx, row, match, workspaceplan.Plan{}, nil, nil, nil, "")
	if got.SlotRetries == nil || *got.SlotRetries != 0 || slotRetryBudget(got) != 0 {
		t.Errorf("SlotBindRequest.SlotRetries = %v (budget %d), want 0", got.SlotRetries, slotRetryBudget(got))
	}
	match.SlotRetries = nil
	if got := s.slotBindRequest(ctx, row, match, workspaceplan.Plan{}, nil, nil, nil, ""); slotRetryBudget(got) != defaultSlotRetries {
		t.Errorf("budget for an unset pool value = %d, want the default %d", slotRetryBudget(got), defaultSlotRetries)
	}
}
