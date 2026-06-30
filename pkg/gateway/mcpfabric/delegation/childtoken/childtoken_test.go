// SPDX-License-Identifier: MIT

package childtoken_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/childtoken"
)

// fixedClock is a deterministic clock for the exchange tests.
func fixedClock() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// revStub is an in-memory RevocationChecker.
type revStub struct{ revoked map[string]bool }

func (r revStub) IsRevoked(jti string) bool { return r.revoked[jti] }

// timeoutLock is an AuditLock that always reports contention.
type timeoutLock struct{}

func (timeoutLock) Acquire(context.Context, string) (func(), error) {
	return nil, errors.New("audit lock acquire timeout")
}

// okLock is an AuditLock that always grants.
type okLock struct{ released *bool }

func (l okLock) Acquire(context.Context, string) (func(), error) {
	return func() {
		if l.released != nil {
			*l.released = true
		}
	}, nil
}

func baseParams() delegation.ChildTokenParams {
	return delegation.ChildTokenParams{
		TenantID:              "acme",
		ChildSessionID:        "child-1",
		ParentSessionID:       "parent-1",
		ParentSubject:         "alice@acme.com",
		ParentJTI:             "jti_parent",
		ParentDelegationDepth: 2,
		ParentScope:           []string{"sessions:write", "tools:call"},
		RequestedScope:        []string{"tools:call"},
		ParentCallerType:      "agent",
	}
}

// spec: §8.2 lines 59-60 — a clean exchange narrows scope, builds the
// act chain naming the parent, fixes delegation_depth at parent + 1, and
// caps exp at now + the per-dialect ceiling.
func TestMintChildToken_HappyPath_Spec8_2(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{},
		Clock:       fixedClock,
		IDFunc:      func() string { return "jti_child" },
		TTL:         time.Hour,
	})
	got, err := m.MintChildToken(context.Background(), baseParams())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.JTI != "jti_child" {
		t.Errorf("jti = %q, want jti_child", got.JTI)
	}
	if got.DelegationDepth != 3 {
		t.Errorf("delegation_depth = %d, want 3 (parent 2 + 1)", got.DelegationDepth)
	}
	if len(got.Scope) != 1 || got.Scope[0] != "tools:call" {
		t.Errorf("scope = %v, want [tools:call]", got.Scope)
	}
	if len(got.Audience) != 1 || got.Audience[0] != delegation.ChildTokenAudience {
		t.Errorf("audience = %v, want [%s]", got.Audience, delegation.ChildTokenAudience)
	}
	wantExp := fixedClock().Add(time.Hour)
	if !got.Exp.Equal(wantExp) {
		t.Errorf("exp = %v, want %v", got.Exp, wantExp)
	}
	if len(got.Act) != 1 {
		t.Fatalf("act chain len = %d, want 1", len(got.Act))
	}
	if got.Act[0].SessionID != "parent-1" || got.Act[0].Sub != "alice@acme.com" || got.Act[0].DelegationDepth != 2 {
		t.Errorf("act[0] = %+v, want parent claims", got.Act[0])
	}
}

// spec: §8.2 line 61 — the actor-token freshness check rejects when the
// parent jti is revoked mid-flight, returning the typed ErrParentRevoked
// the §8.5 handler maps to DELEGATION_PARENT_REVOKED.
func TestMintChildToken_ParentRevoked_Spec8_2_61(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{revoked: map[string]bool{"jti_parent": true}},
		Clock:       fixedClock,
	})
	_, err := m.MintChildToken(context.Background(), baseParams())
	if !errors.Is(err, delegation.ErrParentRevoked) {
		t.Fatalf("err = %v, want ErrParentRevoked", err)
	}
}

// spec: §8.2 line 61 — a parent whose jti is not in the revocation cache
// is admitted (the common case).
func TestMintChildToken_ParentNotRevoked_Admitted(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{revoked: map[string]bool{"jti_other": true}},
		Clock:       fixedClock,
	})
	if _, err := m.MintChildToken(context.Background(), baseParams()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// spec: §8.2 line 63 — an audit advisory-lock timeout during the
// exchange fails closed with the retryable ErrAuditContention before any
// token is issued.
func TestMintChildToken_AuditContention_Spec8_2_63(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{},
		AuditLock:   timeoutLock{},
		Clock:       fixedClock,
	})
	_, err := m.MintChildToken(context.Background(), baseParams())
	if !errors.Is(err, delegation.ErrAuditContention) {
		t.Fatalf("err = %v, want ErrAuditContention", err)
	}
}

// spec: §8.2 line 63 — the audit lock is released after a successful
// exchange (the release func runs).
func TestMintChildToken_AuditLockReleased(t *testing.T) {
	released := false
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{},
		AuditLock:   okLock{released: &released},
		Clock:       fixedClock,
	})
	if _, err := m.MintChildToken(context.Background(), baseParams()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !released {
		t.Error("audit lock release was not called")
	}
}

// spec: §13.3 scope-subset — a requested scope outside the parent's
// granted scope is rejected by the underlying §13.3 validator (the child
// can only narrow). The error is neither ErrParentRevoked nor
// ErrAuditContention.
func TestMintChildToken_ScopeWiden_Rejected_Spec13_3(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{Revocations: revStub{}, Clock: fixedClock})
	p := baseParams()
	p.RequestedScope = []string{"admin:everything"} // not in ParentScope
	_, err := m.MintChildToken(context.Background(), p)
	if err == nil {
		t.Fatal("expected scope-subset rejection, got nil")
	}
	if errors.Is(err, delegation.ErrParentRevoked) || errors.Is(err, delegation.ErrAuditContention) {
		t.Errorf("err = %v, want a generic exchange rejection", err)
	}
}

// spec: §8.2 line 61 — an empty parent jti skips the freshness check (no
// jti to resolve) and still mints a token.
func TestMintChildToken_NoParentJTI_SkipsFreshness(t *testing.T) {
	m := childtoken.NewMinter(childtoken.Options{
		Revocations: revStub{revoked: map[string]bool{"": true}},
		Clock:       fixedClock,
	})
	p := baseParams()
	p.ParentJTI = ""
	if _, err := m.MintChildToken(context.Background(), p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
