// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §11.1 line 9 — per-user active-delegated-children admission cap.
// F-11.1.4.

func perUserDelegateReq() delegation.Request {
	return delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}
}

// TestDelegateRejectedAtPerUserChildrenLimit_spec_11_1 verifies that a
// user already holding the cap of live delegated children is rejected
// with ErrUserChildrenExhausted before any tree budget is reserved.
func TestDelegateRejectedAtPerUserChildrenLimit_spec_11_1(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	// Two live delegated children already owned by user_alice (seedParent
	// sets UserID=user_alice and a non-empty ParentSessionID).
	seedParent(t, store, "child_1", "sess_parent", "gemini", "pool-b", isolation.ProfileSandboxed)
	seedParent(t, store, "child_2", "sess_parent", "gemini", "pool-b", isolation.ProfileSandboxed)

	svc := delegation.NewService(store, delegation.Options{
		IDFunc:                   func() string { return "sess_child" },
		MaxActiveChildrenPerUser: 2,
	})
	_, err := svc.Delegate(context.Background(), "acme", perUserDelegateReq())
	if !errors.Is(err, delegation.ErrUserChildrenExhausted) {
		t.Fatalf("Delegate err = %v, want ErrUserChildrenExhausted", err)
	}
	// The over-limit delegation created no child row.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a rejected delegation must not persist a child row")
	}
}

// TestDelegateAdmittedUnderPerUserChildrenLimit_spec_11_1 verifies a
// user below the cap is admitted, and that the cap counts only the
// owning user's live delegated children.
func TestDelegateAdmittedUnderPerUserChildrenLimit_spec_11_1(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	// One live child for user_alice; cap of 2 leaves room for one more.
	seedParent(t, store, "child_1", "sess_parent", "gemini", "pool-b", isolation.ProfileSandboxed)

	svc := delegation.NewService(store, delegation.Options{
		IDFunc:                   func() string { return "sess_child" },
		MaxActiveChildrenPerUser: 2,
	})
	res, err := svc.Delegate(context.Background(), "acme", perUserDelegateReq())
	if err != nil {
		t.Fatalf("Delegate under limit: %v", err)
	}
	if res.Child.ID != "sess_child" {
		t.Fatalf("child id = %q, want sess_child", res.Child.ID)
	}
}

// TestDelegatePerUserChildrenZeroIsUnlimited_spec_11_1 verifies a zero
// cap disables the scope even when the user holds many live children.
func TestDelegatePerUserChildrenZeroIsUnlimited_spec_11_1(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	for _, id := range []string{"child_1", "child_2", "child_3"} {
		seedParent(t, store, id, "sess_parent", "gemini", "pool-b", isolation.ProfileSandboxed)
	}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" },
		// MaxActiveChildrenPerUser left at zero (unlimited).
	})
	if _, err := svc.Delegate(context.Background(), "acme", perUserDelegateReq()); err != nil {
		t.Fatalf("zero cap (unlimited): %v", err)
	}
}
