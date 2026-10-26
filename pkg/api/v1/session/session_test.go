// SPDX-License-Identifier: MIT

package session

import (
	"errors"
	"testing"
)

func TestAllStatesCountMatchesSpec(t *testing.T) {
	// §15.1 lists 12 base states; §7.2 adds input_required as a sub-state
	// of running that is surfaced on the REST + SSE projection.
	if got := len(AllStates()); got != 13 {
		t.Errorf("AllStates() returned %d, want 13 (§15.1 + §7.2 input_required)", got)
	}
}

func TestIsTerminalCoversFourStates(t *testing.T) {
	terminals := map[State]bool{}
	for _, s := range AllStates() {
		if IsTerminal(s) {
			terminals[s] = true
		}
	}
	want := []State{StateCompleted, StateFailed, StateCancelled, StateExpired}
	if len(terminals) != len(want) {
		t.Errorf("IsTerminal: %d terminal states, want 4", len(terminals))
	}
	for _, s := range want {
		if !terminals[s] {
			t.Errorf("IsTerminal: %q must be terminal", s)
		}
	}
}

// TestIsRecoveryCoversRetryStates verifies the §16.5 Session
// availability SLI recovery-state set: exactly resume_pending, resuming
// (the internal transient §15.1 normalises to resume_pending), and
// awaiting_client_action are retry/recovery states; running, input
// required, and terminal states are not. F-16.5.3.
func TestIsRecoveryCoversRetryStates(t *testing.T) {
	want := []State{StateResumePending, StateResuming, StateAwaitingClientAction}
	if got := RecoveryStates(); len(got) != len(want) {
		t.Errorf("RecoveryStates() returned %d, want %d", len(got), len(want))
	}
	for _, s := range want {
		if !IsRecovery(s) {
			t.Errorf("IsRecovery(%q) = false, want true", s)
		}
	}
	for _, s := range []State{StateRunning, StateCompleted, StateFailed, StateInputRequired, StateSuspended} {
		if IsRecovery(s) {
			t.Errorf("IsRecovery(%q) = true, want false", s)
		}
	}
}

func TestFailureClassEnumIsExhaustive(t *testing.T) {
	got := AllFailureClasses()
	if len(got) != 5 {
		t.Errorf("AllFailureClasses() returned %d, want 5 per §7.1", len(got))
	}
	// Every value must be present.
	seen := map[FailureClass]bool{}
	for _, f := range got {
		seen[f] = true
	}
	for _, want := range []FailureClass{
		FailureClassRuntime, FailureClassStartingTimeout,
		FailureClassBudgetKeysExpired, FailureClassWorkspaceSealTimeout,
		FailureClassDeriveFailure,
	} {
		if !seen[want] {
			t.Errorf("AllFailureClasses() missing %q", want)
		}
	}
}

// §15.1 precondition table: POST /v1/sessions/{id}/finalize is valid
// only from `created`.
func TestValidateFinalizeAdmitsCreatedOnly(t *testing.T) {
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointFinalize,
		CurrentState: StateCreated,
	}); err != nil {
		t.Errorf("finalize from created should be allowed, got %v", err)
	}
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointFinalize,
		CurrentState: StateRunning,
	}); err == nil {
		t.Errorf("finalize from running should be rejected")
	}
}

func TestValidateStartAdmitsReadyOnly(t *testing.T) {
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointStart,
		CurrentState: StateReady,
	}); err != nil {
		t.Errorf("start from ready should be allowed, got %v", err)
	}
	for _, s := range []State{StateCreated, StateFinalizing, StateRunning, StateCompleted} {
		if err := Validate(PreconditionRequest{
			Endpoint:     EndpointStart,
			CurrentState: s,
		}); err == nil {
			t.Errorf("start from %q should be rejected", s)
		}
	}
}

// spec: §7.2 — interrupt admits running and input_required (a
// running sub-state) but no other state.
func TestValidateInterruptAdmitsRunningAndInputRequired_spec_7_2(t *testing.T) {
	for _, allowed := range []State{StateRunning, StateInputRequired} {
		if err := Validate(PreconditionRequest{
			Endpoint:     EndpointInterrupt,
			CurrentState: allowed,
		}); err != nil {
			t.Errorf("interrupt from %q should be allowed, got %v", allowed, err)
		}
	}
	for _, s := range []State{StateReady, StateSuspended, StateStarting, StateFinalizing, StateCompleted, StateFailed, StateCancelled, StateExpired} {
		err := Validate(PreconditionRequest{
			Endpoint:     EndpointInterrupt,
			CurrentState: s,
		})
		if err == nil {
			t.Errorf("interrupt from %q should be rejected", s)
		}
	}
}

func TestValidateTerminateAdmitsEveryNonTerminal(t *testing.T) {
	for _, s := range AllStates() {
		err := Validate(PreconditionRequest{
			Endpoint:     EndpointTerminate,
			CurrentState: s,
		})
		if IsTerminal(s) {
			if err == nil {
				t.Errorf("terminate from terminal %q should be rejected", s)
			}
		} else {
			if err != nil {
				t.Errorf("terminate from non-terminal %q should be allowed, got %v", s, err)
			}
		}
	}
}

// Upload: created always; running only when midSessionUpload capability
// is enabled.
func TestValidateUploadMidSessionGated(t *testing.T) {
	caps := map[Capability]bool{}
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointUpload,
		CurrentState: StateRunning,
		Capabilities: caps,
	}); err == nil {
		t.Errorf("upload from running without capability should be rejected")
	}
	caps[CapabilityMidSessionUpload] = true
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointUpload,
		CurrentState: StateRunning,
		Capabilities: caps,
	}); err != nil {
		t.Errorf("upload from running WITH midSessionUpload capability should be allowed, got %v", err)
	}
	// Upload from created is always allowed.
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointUpload,
		CurrentState: StateCreated,
	}); err != nil {
		t.Errorf("upload from created should be allowed, got %v", err)
	}
}

// Derive: terminal states always; non-terminal only with allowStale.
func TestValidateDeriveAllowStaleGated(t *testing.T) {
	for _, s := range []State{StateCompleted, StateFailed, StateCancelled, StateExpired} {
		if err := Validate(PreconditionRequest{
			Endpoint:     EndpointDerive,
			CurrentState: s,
		}); err != nil {
			t.Errorf("derive from terminal %q should be allowed, got %v", s, err)
		}
	}
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointDerive,
		CurrentState: StateRunning,
	}); err == nil {
		t.Errorf("derive from running without allowStale should be rejected")
	}
	if err := Validate(PreconditionRequest{
		Endpoint:     EndpointDerive,
		CurrentState: StateRunning,
		Capabilities: map[Capability]bool{CapabilityAllowStaleDerive: true},
	}); err != nil {
		t.Errorf("derive from running WITH allowStale should be allowed, got %v", err)
	}
}

// DELETE: any non-terminal state.
func TestValidateDeleteAdmitsEveryNonTerminal(t *testing.T) {
	for _, s := range AllStates() {
		err := Validate(PreconditionRequest{
			Endpoint:     EndpointDelete,
			CurrentState: s,
		})
		if IsTerminal(s) {
			if err == nil {
				t.Errorf("DELETE from terminal %q should be rejected", s)
			}
		} else if err != nil {
			t.Errorf("DELETE from non-terminal %q should be allowed, got %v", s, err)
		}
	}
}

// PreconditionError carries the §15.1 INVALID_STATE_TRANSITION envelope.
func TestPreconditionErrorEnvelope(t *testing.T) {
	err := Validate(PreconditionRequest{
		Endpoint:     EndpointStart,
		CurrentState: StateRunning,
	})
	var pe *PreconditionError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PreconditionError, got %T", err)
	}
	if pe.Code() != 409 {
		t.Errorf("Code: want 409, got %d", pe.Code())
	}
	if pe.ErrorCode() != "INVALID_STATE_TRANSITION" {
		t.Errorf("ErrorCode: want INVALID_STATE_TRANSITION, got %q", pe.ErrorCode())
	}
	if len(pe.AllowedStates) == 0 {
		t.Errorf("AllowedStates must be populated for the gateway to surface")
	}
	if pe.CurrentState != StateRunning {
		t.Errorf("CurrentState: want running, got %q", pe.CurrentState)
	}
}

func TestValidateUnknownEndpointErrors(t *testing.T) {
	err := Validate(PreconditionRequest{
		Endpoint:     Endpoint("POST /v1/sessions/{id}/bogus"),
		CurrentState: StateRunning,
	})
	if err == nil {
		t.Errorf("unknown endpoint should error")
	}
}

// spec: §7.2; §15.1 — `resuming` is an internal
// transient that the API surface normalises to resume_pending → running
// on the GET envelope but synthesises onto the §10.4 coordinator-handoff
// `status_change` SSE frame. The constant must exist in the external
// API package so the SSE emitter can reference it without depending on
// the internal pkg/session/state. F-7.3.19.
func TestResumingStateConstantExposed(t *testing.T) {
	if StateResuming != "resuming" {
		t.Errorf("F-7.3.19: StateResuming = %q, want \"resuming\"", StateResuming)
	}
	// §15.1 — the GET envelope normalises resuming away, so
	// AllStates() (which is the closed §15.1 polling enum) does NOT
	// include it.
	for _, s := range AllStates() {
		if s == StateResuming {
			t.Errorf("F-7.3.19: AllStates() must omit resuming per §15.1 normalisation rule, got %v", AllStates())
		}
	}
	// resuming is non-terminal so accidental terminal-state plumbing
	// cannot leak it into the terminal set.
	if IsTerminal(StateResuming) {
		t.Errorf("F-7.3.19: resuming must not be terminal")
	}
}

// spec: §15.1 — `POST /v1/sessions/{id}/resume` admits only
// awaiting_client_action; resuming is the internal-only transient the
// API never accepts as a precondition. F-7.3.19 must not regress the
// precondition table.
func TestValidateResumeRejectsResumingState(t *testing.T) {
	err := Validate(PreconditionRequest{
		Endpoint:     EndpointResume,
		CurrentState: StateResuming,
	})
	if err == nil {
		t.Errorf("F-7.3.19: resume from resuming must be rejected per §15.1")
	}
}

// AllowedStates is callable with no current state context; useful for
// API documentation and error envelopes.
func TestAllowedStatesIncludesGatedWhenCapabilityHeld(t *testing.T) {
	base := AllowedStates(EndpointUpload, nil)
	if len(base) != 1 || base[0] != StateCreated {
		t.Errorf("base upload AllowedStates: want [created], got %v", base)
	}
	with := AllowedStates(EndpointUpload, map[Capability]bool{CapabilityMidSessionUpload: true})
	if len(with) != 2 {
		t.Errorf("with-capability AllowedStates: want 2 entries, got %v", with)
	}
}

// TestTerminateAdmitsResumingAndEchoesOnlyVisibleStates_spec_7_2 pins the
// terminate precondition: terminate from every precondition state, the
// internal resuming transient included, is admitted. For every endpoint and
// capability set, AllowedStates and the AllowedStates of each rejecting
// Validate hold only AllStates() members, so no 409 names resuming. DELETE
// from expired is the case the pre-fix echo fails, because DELETE admits
// resuming.
//
// spec: §7.2 (Interactive Session Model), §15.1 (REST API)
func TestTerminateAdmitsResumingAndEchoesOnlyVisibleStates_spec_7_2(t *testing.T) {
	for _, st := range append(nonTerminalForTest(), StateResuming) {
		if err := Validate(PreconditionRequest{Endpoint: EndpointTerminate, CurrentState: st}); err != nil {
			t.Errorf("terminate from %s = %v, want admitted", st, err)
		}
	}
	visible := map[State]bool{}
	for _, st := range AllStates() {
		visible[st] = true
	}
	endpoints := []Endpoint{
		EndpointUpload, EndpointFinalize, EndpointStart, EndpointInterrupt, EndpointTerminate,
		EndpointResume, EndpointMessages, EndpointDerive, EndpointDelete,
	}
	capSets := []map[Capability]bool{
		nil,
		{CapabilityMidSessionUpload: true},
		{CapabilityAllowStaleDerive: true},
		{CapabilityMidSessionUpload: true, CapabilityAllowStaleDerive: true},
	}
	checkVisible := func(where string, states []State) {
		for _, st := range states {
			if !visible[st] {
				t.Errorf("%s lists %q, which AllStates() does not", where, st)
			}
		}
	}
	for _, ep := range endpoints {
		for _, caps := range capSets {
			checkVisible(string(ep)+" AllowedStates", AllowedStates(ep, caps))
			for _, st := range append(AllStates(), StateResuming) {
				err := Validate(PreconditionRequest{Endpoint: ep, CurrentState: st, Capabilities: caps})
				var pe *PreconditionError
				if errors.As(err, &pe) {
					checkVisible(string(ep)+" from "+string(st), pe.AllowedStates)
				}
			}
		}
	}
	err := Validate(PreconditionRequest{Endpoint: EndpointDelete, CurrentState: StateExpired})
	var pe *PreconditionError
	if !errors.As(err, &pe) {
		t.Fatalf("DELETE from expired = %v, want a precondition error", err)
	}
	for _, st := range pe.AllowedStates {
		if st == StateResuming {
			t.Errorf("DELETE from expired echoes resuming: %v", pe.AllowedStates)
		}
	}
}

// nonTerminalForTest lists the AllStates() members that are not terminal.
func nonTerminalForTest() []State {
	var out []State
	for _, st := range AllStates() {
		if !IsTerminal(st) {
			out = append(out, st)
		}
	}
	return out
}
