// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"testing"
)

// spec: §4.9 — the credential assignment strategies: least-loaded,
// round-robin, and sticky-until-failure.

func cand(id string, active int, healthy bool) CredentialCandidate {
	return CredentialCandidate{CredentialID: id, ActiveSessions: active, Healthy: healthy}
}

func TestSelectLeastLoaded(t *testing.T) {
	got, _, err := SelectCredential(StrategyLeastLoaded, []CredentialCandidate{
		cand("a", 5, true),
		cand("b", 2, true),
		cand("c", 8, true),
	}, SelectionState{})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "b" {
		t.Errorf("selected %q, want b (fewest active sessions)", got.CredentialID)
	}
}

func TestSelectLeastLoadedBreaksTiesTowardEarlier(t *testing.T) {
	got, _, err := SelectCredential(StrategyLeastLoaded, []CredentialCandidate{
		cand("a", 3, true),
		cand("b", 3, true),
	}, SelectionState{})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "a" {
		t.Errorf("selected %q, want a (earlier candidate wins a tie)", got.CredentialID)
	}
}

func TestSelectLeastLoadedSkipsUnhealthy(t *testing.T) {
	got, _, err := SelectCredential(StrategyLeastLoaded, []CredentialCandidate{
		cand("a", 1, false), // lowest load but unhealthy
		cand("b", 4, true),
	}, SelectionState{})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "b" {
		t.Errorf("selected %q, want b — the unhealthy lower-load candidate is skipped", got.CredentialID)
	}
}

func TestSelectExhaustedWhenNoHealthyCandidate(t *testing.T) {
	for _, strategy := range AllAssignmentStrategies() {
		t.Run(string(strategy), func(t *testing.T) {
			_, _, err := SelectCredential(strategy, []CredentialCandidate{
				cand("a", 0, false),
				cand("b", 0, false),
			}, SelectionState{})
			if !errors.Is(err, ErrPoolExhausted) {
				t.Errorf("error = %v, want ErrPoolExhausted", err)
			}
		})
	}
}

func TestSelectExhaustedWhenPoolEmpty(t *testing.T) {
	_, _, err := SelectCredential(StrategyRoundRobin, nil, SelectionState{})
	if !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("error = %v, want ErrPoolExhausted for an empty pool", err)
	}
}

func TestSelectRoundRobinRotates(t *testing.T) {
	pool := []CredentialCandidate{cand("a", 0, true), cand("b", 0, true), cand("c", 0, true)}
	state := SelectionState{}
	want := []string{"a", "b", "c", "a"}
	for i, w := range want {
		got, next, err := SelectCredential(StrategyRoundRobin, pool, state)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if got.CredentialID != w {
			t.Errorf("round %d selected %q, want %q", i, got.CredentialID, w)
		}
		state = next
	}
}

func TestSelectRoundRobinSkipsUnhealthy(t *testing.T) {
	pool := []CredentialCandidate{cand("a", 0, true), cand("b", 0, false), cand("c", 0, true)}
	got, next, err := SelectCredential(StrategyRoundRobin, pool, SelectionState{Cursor: 1})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "c" {
		t.Errorf("selected %q, want c — the unhealthy b is skipped", got.CredentialID)
	}
	if next.Cursor != 0 {
		t.Errorf("cursor = %d, want 0 (wrapped past c)", next.Cursor)
	}
}

func TestSelectStickyKeepsHealthyPin(t *testing.T) {
	pool := []CredentialCandidate{cand("a", 9, true), cand("b", 1, true)}
	got, next, err := SelectCredential(StrategyStickyUntilFailure, pool, SelectionState{Sticky: "a"})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "a" {
		t.Errorf("selected %q, want a — the healthy pin is kept despite higher load", got.CredentialID)
	}
	if next.Sticky != "a" {
		t.Errorf("sticky = %q, want a", next.Sticky)
	}
}

func TestSelectStickyRepinsOnFailure(t *testing.T) {
	// The pinned credential a is unhealthy; sticky re-pins the
	// least-loaded healthy candidate.
	pool := []CredentialCandidate{cand("a", 0, false), cand("b", 5, true), cand("c", 2, true)}
	got, next, err := SelectCredential(StrategyStickyUntilFailure, pool, SelectionState{Sticky: "a"})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "c" {
		t.Errorf("selected %q, want c — the least-loaded healthy candidate", got.CredentialID)
	}
	if next.Sticky != "c" {
		t.Errorf("sticky = %q, want c (re-pinned)", next.Sticky)
	}
}

func TestSelectStickyPinsOnFirstAssignment(t *testing.T) {
	pool := []CredentialCandidate{cand("a", 7, true), cand("b", 1, true)}
	got, next, err := SelectCredential(StrategyStickyUntilFailure, pool, SelectionState{})
	if err != nil {
		t.Fatalf("SelectCredential: %v", err)
	}
	if got.CredentialID != "b" || next.Sticky != "b" {
		t.Errorf("first sticky assignment selected %q sticky=%q, want b/b", got.CredentialID, next.Sticky)
	}
}

func TestSelectRejectsUnknownStrategy(t *testing.T) {
	_, _, err := SelectCredential("bogus-strategy", []CredentialCandidate{cand("a", 0, true)}, SelectionState{})
	if err == nil {
		t.Fatal("SelectCredential accepted an unknown strategy")
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Errorf("error %v is not a *ConfigError", err)
	}
}
