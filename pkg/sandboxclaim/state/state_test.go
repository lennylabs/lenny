// SPDX-License-Identifier: MIT

package state_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// spec: 4.6.1 (binding states), 4.6.3 (binding-state enumeration)
// diagnosis: SandboxClaim binding states diverged from spec §4.6.3.
//
//	The enumeration is bound, recycling, reserved, released, failed;
//	only released and failed are terminal. The set must mirror the
//	SandboxClaim.status.phase CRD enum.
func TestStatesAndTerminals(t *testing.T) {
	t.Parallel()
	want := []state.State{state.Bound, state.Recycling, state.Reserved, state.Released, state.Failed}
	gotAll := state.All()
	if len(gotAll) != len(want) {
		t.Fatalf("state.All() has %d entries, want %d", len(gotAll), len(want))
	}
	for _, s := range want {
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
	// Live binding states are non-terminal: the recycle path and the
	// reserved hold all advance further or DELETE rather than sink.
	for _, s := range []state.State{state.Bound, state.Recycling, state.Reserved} {
		if state.IsTerminal(s) {
			t.Errorf("%q must not be terminal", s)
		}
	}
	if !state.IsTerminal(state.Released) {
		t.Errorf("Released must be terminal")
	}
	if !state.IsTerminal(state.Failed) {
		t.Errorf("Failed must be terminal")
	}
}

// spec: 4.6.1 (binding-state edges, rebind), 4.6.3
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

// spec: 4.6.1 (binding-state edges), 4.6.3
// diagnosis: IsValid accepted a transition not in ValidTransitions().
//
//	The recycle path advances bound → recycling → reserved and
//	rebinds reserved → bound; the terminal states are sinks and no
//	edge resurrects a released or failed claim, skips recycling, or
//	jumps straight from bound to reserved.
func TestIsValidIllegalTransitionsRejected(t *testing.T) {
	t.Parallel()
	illegal := []state.Transition{
		{state.Released, state.Bound},
		{state.Failed, state.Bound},
		{state.Released, state.Failed},
		{state.Failed, state.Released},
		{state.Released, state.Reserved},
		{state.Failed, state.Recycling},
		{state.Bound, state.Reserved},  // recycling must intervene
		{state.Recycling, state.Bound}, // rebind is from reserved only
		{"", state.Released},           // no initial-to-terminal edge
		{"", state.Failed},             // no initial-to-terminal edge
		{"", state.Recycling},          // no initial-to-recycling edge
		{"", state.Reserved},           // no initial-to-reserved edge
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
