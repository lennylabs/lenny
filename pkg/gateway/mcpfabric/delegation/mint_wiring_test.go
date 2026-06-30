// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// stubMinter records the params it was called with and returns a
// configurable token or error, so the wiring tests assert the service
// invokes the §8.2 line 59 exchange on the admission path.
type stubMinter struct {
	called    bool
	gotParams delegation.ChildTokenParams
	token     delegation.ChildToken
	err       error
}

func (m *stubMinter) MintChildToken(_ context.Context, p delegation.ChildTokenParams) (delegation.ChildToken, error) {
	m.called = true
	m.gotParams = p
	return m.token, m.err
}

func mintTestService(t *testing.T, store sessionstore.Store, m delegation.ChildTokenMinter) *delegation.Service {
	t.Helper()
	return delegation.NewService(store, delegation.Options{
		Clock:            func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:           func() string { return "child_minted" },
		ChildTokenMinter: m,
	})
}

// spec: §8.2 lines 59-60 — when a ParentToken is present and a minter is
// wired, Delegate invokes the in-process child-token exchange and stamps
// the minted token onto the Result.
func TestDelegate_InvokesChildTokenMinter_Spec8_2_59(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	minter := &stubMinter{token: delegation.ChildToken{
		JTI:             "jti_child",
		Scope:           []string{"tools:call"},
		Audience:        []string{delegation.ChildTokenAudience},
		DelegationDepth: 1,
		Exp:             time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		Act:             []delegation.ActClaim{{Sub: "user_alice", SessionID: "sess_parent"}},
	}}
	svc := mintTestService(t, store, minter)

	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "worker",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ParentToken: &delegation.ParentToken{
			Subject:   "user_alice",
			SessionID: "sess_parent",
			JTI:       "jti_parent",
			Scope:     []string{"tools:call"},
		},
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if !minter.called {
		t.Fatal("minter was not invoked")
	}
	if minter.gotParams.ParentJTI != "jti_parent" || minter.gotParams.ChildSessionID != "child_minted" {
		t.Errorf("minter params = %+v, want parent jti + child id forwarded", minter.gotParams)
	}
	if res.ChildToken == nil || res.ChildToken.JTI != "jti_child" {
		t.Errorf("Result.ChildToken = %+v, want jti_child", res.ChildToken)
	}
}

// spec: §8.2 line 61 — a minter ErrParentRevoked propagates out of
// Delegate and no child row is created.
func TestDelegate_ParentRevoked_NoChild_Spec8_2_61(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := mintTestService(t, store, &stubMinter{err: delegation.ErrParentRevoked})

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "worker",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ParentToken:      &delegation.ParentToken{Subject: "user_alice", SessionID: "sess_parent", JTI: "jti_parent"},
	})
	if !errors.Is(err, delegation.ErrParentRevoked) {
		t.Fatalf("err = %v, want ErrParentRevoked", err)
	}
	if _, gerr := store.Get(context.Background(), "acme", "child_minted"); gerr == nil {
		t.Error("child row was created despite parent revocation")
	}
}

// spec: §8.2 line 63 — a minter ErrAuditContention propagates out of
// Delegate (retryable).
func TestDelegate_AuditContention_Spec8_2_63(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := mintTestService(t, store, &stubMinter{err: delegation.ErrAuditContention})

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "worker",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ParentToken:      &delegation.ParentToken{Subject: "user_alice", SessionID: "sess_parent"},
	})
	if !errors.Is(err, delegation.ErrAuditContention) {
		t.Fatalf("err = %v, want ErrAuditContention", err)
	}
}

// spec: §8.2 line 59 — a request without ParentToken skips the exchange
// leg even when a minter is wired (the in-process minimal / unauthenticated
// path), and the child is still admitted.
func TestDelegate_NoParentToken_SkipsExchange(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	minter := &stubMinter{err: delegation.ErrParentRevoked} // would fail if invoked
	svc := mintTestService(t, store, minter)

	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "worker",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if minter.called {
		t.Error("minter invoked despite nil ParentToken")
	}
	if res.ChildToken != nil {
		t.Error("Result.ChildToken set despite skipped exchange")
	}
}
