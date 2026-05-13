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
//	ValidTransitions(). Note: ADR-007 admission-webhook
//	double-claim prevention is a separate Phase 3.5 deliverable
//	in pkg/admission/sandboxclaim_guard/.
//
// Phase 1: Phase 3.5 implementation makes this pass; skipped here.
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
			err := state.IsValid(tr.From, tr.To)
			if errors.Is(err, state.ErrNotImplemented) {
				t.Skipf("not implemented in Phase 1; Phase 3.5 makes this pass")
			}
			if err != nil {
				t.Errorf("IsValid(%q, %q) = %v, want nil", tr.From, tr.To, err)
			}
		})
	}
}
