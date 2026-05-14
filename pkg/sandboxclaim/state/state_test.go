// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// spec: 4.6.1
// diagnosis: SandboxClaim states diverged from spec §4.6.1 / ADR-007.
//
//	The state machine has exactly three states: bound, released,
//	failed. Released and failed are terminal.
func TestStatesAndTerminals(t *testing.T) {
	t.Parallel()
	gotAll := state.All()
	if len(gotAll) != 3 {
		t.Fatalf("state.All() has %d entries, want 3", len(gotAll))
	}
	for _, s := range []state.State{state.Bound, state.Released, state.Failed} {
		found := false
		for _, x := range gotAll {
			if x == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("state.All() missing %q", s)
		}
	}
	if state.IsTerminal(state.Bound) {
		t.Errorf("Bound must not be terminal")
	}
	if !state.IsTerminal(state.Released) {
		t.Errorf("Released must be terminal")
	}
	if !state.IsTerminal(state.Failed) {
		t.Errorf("Failed must be terminal")
	}
}

// spec: 4.6.1
// diagnosis: IsValid rejected or accepted a transition contrary to
//
//	ValidTransitions(). Note: the admission-webhook double-claim
//	prevention is a separate deliverable in
//	pkg/admission/sandboxclaim_guard/ that ships alongside this
//	state machine; it does not change the legal-edge set.
func TestIsValidCanonicalTransitions(t *testing.T) {
	t.Parallel()
	for _, tr := range state.ValidTransitions() {
		tr := tr
		name := string(tr.From) + "_to_" + string(tr.To)
		if tr.From == "" {
			name = "create_to_" + string(tr.To)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := state.IsValid(tr.From, tr.To); err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil", tr.From, tr.To, err)
			}
		})
	}
}

// spec: 4.6.1
// diagnosis: IsValid accepted a transition not in ValidTransitions().
//
//	SandboxClaim states are bound → released | failed; the
//	terminal states are sinks and there is no edge that
//	resurrects a released or failed claim.
func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	t.Parallel()
	illegal := []state.Transition{
		{state.Released, state.Bound},
		{state.Failed, state.Bound},
		{state.Released, state.Failed},
		{state.Failed, state.Released},
		{"", state.Released}, // no initial-to-terminal edge
		{"", state.Failed},   // no initial-to-terminal edge
	}
	for _, tr := range illegal {
		tr := tr
		t.Run(string(tr.From)+"_to_"+string(tr.To), func(t *testing.T) {
			t.Parallel()
			err := state.IsValid(tr.From, tr.To)
			if err == nil {
				t.Errorf("IsValid(%q, %q) returned nil, expected error", tr.From, tr.To)
			}
			var ite *state.InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Errorf("IsValid(%q, %q) returned %T, want *InvalidTransitionError", tr.From, tr.To, err)
			}
		})
	}
}
