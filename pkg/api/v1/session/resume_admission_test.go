// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// TestResumeEndpointAdmitsSuspendedAndAwaitingClientAction_spec_7_3 asserts
// that the resume endpoint's precondition admits exactly suspended and
// awaiting_client_action and rejects every other state.
//
// spec: §7.3 (Retry and Resume)
func TestResumeEndpointAdmitsSuspendedAndAwaitingClientAction_spec_7_3(t *testing.T) {
	admitted := map[session.State]bool{
		session.StateSuspended:            true,
		session.StateAwaitingClientAction: true,
	}
	for _, st := range session.AllStates() {
		err := session.Validate(session.PreconditionRequest{Endpoint: session.EndpointResume, CurrentState: st})
		if admitted[st] != (err == nil) {
			t.Errorf("resume from %q: err = %v, want admitted %v", st, err, admitted[st])
		}
	}
}

// TestCredentialRejectedClassification_spec_7_3 asserts that
// credential_rejected classifies non-retryable under the platform defaults,
// whose non-retryable list has three entries, unknown under a client
// non-retryable list that omits it, and retryable under a client retryable
// list that names it.
//
// spec: §7.3 (Retry and Resume)
func TestCredentialRejectedClassification_spec_7_3(t *testing.T) {
	const reason = string(session.FailureCredentialRejected)
	if reason != "credential_rejected" {
		t.Fatalf("FailureCredentialRejected = %q", reason)
	}
	if n := len(session.DefaultNonRetryableFailures()); n != 3 {
		t.Errorf("default non-retryable list has %d entries, want 3", n)
	}
	for _, tc := range []struct {
		name   string
		policy *session.RetryPolicy
		want   session.FailureClassification
	}{
		{"platform defaults", nil, session.FailureNonRetryable},
		{"client non-retryable list without it", &session.RetryPolicy{
			NonRetryableFailures: []string{string(session.FailureSetupCommandFailed)},
		}, session.FailureUnknown},
		{"client retryable list naming it", &session.RetryPolicy{
			RetryableFailures:    []string{reason},
			NonRetryableFailures: []string{string(session.FailureSetupCommandFailed)},
		}, session.FailureRetryable},
	} {
		if got := session.ClassifyFailure(reason, tc.policy); got != tc.want {
			t.Errorf("%s: ClassifyFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}
