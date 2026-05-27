// SPDX-License-Identifier: MIT

package session_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// spec: §7.3 lines 377-393 — every enumerated retry mode is valid;
// unknown values fail the closed-enum check.
func TestRetryModeIsValid(t *testing.T) {
	cases := []struct {
		mode session.RetryMode
		want bool
	}{
		{session.RetryModeAutoThenClient, true},
		{session.RetryModeClientOnly, true},
		{"", false},
		{"continuous", false},
		{"AUTO_THEN_CLIENT", false},
	}
	for _, tc := range cases {
		if got := tc.mode.IsValid(); got != tc.want {
			t.Errorf("(%q).IsValid() = %t, want %t", tc.mode, got, tc.want)
		}
	}
}

// spec: §7.3 line 382 — RetryMode.Resolve folds unset / unknown values
// to the auto_then_client default.
func TestRetryModeResolveDefaultsAutoThenClient(t *testing.T) {
	if got := session.RetryMode("").Resolve(); got != session.RetryModeAutoThenClient {
		t.Errorf("empty Resolve() = %q, want %q", got, session.RetryModeAutoThenClient)
	}
	if got := session.RetryMode("bogus").Resolve(); got != session.RetryModeAutoThenClient {
		t.Errorf("bogus Resolve() = %q, want %q", got, session.RetryModeAutoThenClient)
	}
	if got := session.RetryModeClientOnly.Resolve(); got != session.RetryModeClientOnly {
		t.Errorf("client_only Resolve() = %q, want %q", got, session.RetryModeClientOnly)
	}
}

// spec: §7.3 lines 377-393 — ValidateRetryPolicy rejects unknown modes
// and negative numeric fields with a structured error citing the field.
func TestValidateRetryPolicyRejectsBadInputs(t *testing.T) {
	cases := []struct {
		name      string
		policy    *session.RetryPolicy
		wantField string
	}{
		{
			name:      "unknown mode",
			policy:    &session.RetryPolicy{Mode: session.RetryMode("yolo")},
			wantField: "mode",
		},
		{
			name:      "negative maxRetries",
			policy:    &session.RetryPolicy{MaxRetries: -1},
			wantField: "maxRetries",
		},
		{
			name:      "negative maxSessionAgeSeconds",
			policy:    &session.RetryPolicy{MaxSessionAgeSeconds: -1},
			wantField: "maxSessionAgeSeconds",
		},
		{
			name:      "negative maxResumeWindowSeconds",
			policy:    &session.RetryPolicy{MaxResumeWindowSeconds: -1},
			wantField: "maxResumeWindowSeconds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := session.ValidateRetryPolicy(tc.policy)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var rpErr *session.RetryPolicyValidationError
			if !errors.As(err, &rpErr) {
				t.Fatalf("expected RetryPolicyValidationError, got %T: %v", err, err)
			}
			if rpErr.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", rpErr.Field, tc.wantField)
			}
			if !strings.Contains(err.Error(), "retryPolicy.") {
				t.Errorf("Error() = %q, want prefix retryPolicy.", err.Error())
			}
		})
	}
}

// ValidateRetryPolicy treats nil and the empty struct as well-formed —
// the gateway uses them to mean "use the deployer defaults".
func TestValidateRetryPolicyAdmitsNilAndEmpty(t *testing.T) {
	if err := session.ValidateRetryPolicy(nil); err != nil {
		t.Errorf("nil policy: got %v, want nil", err)
	}
	if err := session.ValidateRetryPolicy(&session.RetryPolicy{}); err != nil {
		t.Errorf("empty policy: got %v, want nil", err)
	}
}

// spec: §7.3 lines 377-393 — ClampRetryPolicy clamps populated values
// down to the deployer caps, fills unset values with the cap, and folds
// an unknown mode to auto_then_client.
func TestClampRetryPolicyAppliesCaps(t *testing.T) {
	caps := session.RetryPolicyCaps{
		MaxRetries:             3,
		MaxSessionAgeSeconds:   7200,
		MaxResumeWindowSeconds: 900,
	}
	cases := []struct {
		name string
		in   *session.RetryPolicy
		want session.RetryPolicy
	}{
		{
			name: "nil_resolves_to_caps_and_default_mode",
			in:   nil,
			want: session.RetryPolicy{
				Mode:                   session.RetryModeAutoThenClient,
				MaxRetries:             3,
				MaxSessionAgeSeconds:   7200,
				MaxResumeWindowSeconds: 900,
			},
		},
		{
			name: "above_cap_clamps_down",
			in: &session.RetryPolicy{
				MaxRetries:             10,
				MaxSessionAgeSeconds:   100000,
				MaxResumeWindowSeconds: 99999,
			},
			want: session.RetryPolicy{
				Mode:                   session.RetryModeAutoThenClient,
				MaxRetries:             3,
				MaxSessionAgeSeconds:   7200,
				MaxResumeWindowSeconds: 900,
			},
		},
		{
			name: "below_cap_preserved",
			in: &session.RetryPolicy{
				Mode:                   session.RetryModeClientOnly,
				MaxRetries:             1,
				MaxSessionAgeSeconds:   60,
				MaxResumeWindowSeconds: 30,
			},
			want: session.RetryPolicy{
				Mode:                   session.RetryModeClientOnly,
				MaxRetries:             1,
				MaxSessionAgeSeconds:   60,
				MaxResumeWindowSeconds: 30,
			},
		},
		{
			name: "unknown_mode_falls_to_default",
			in:   &session.RetryPolicy{Mode: session.RetryMode("bogus")},
			want: session.RetryPolicy{
				Mode:                   session.RetryModeAutoThenClient,
				MaxRetries:             3,
				MaxSessionAgeSeconds:   7200,
				MaxResumeWindowSeconds: 900,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := session.ClampRetryPolicy(tc.in, caps)
			if got.Mode != tc.want.Mode {
				t.Errorf("Mode = %q, want %q", got.Mode, tc.want.Mode)
			}
			if got.MaxRetries != tc.want.MaxRetries {
				t.Errorf("MaxRetries = %d, want %d", got.MaxRetries, tc.want.MaxRetries)
			}
			if got.MaxSessionAgeSeconds != tc.want.MaxSessionAgeSeconds {
				t.Errorf("MaxSessionAgeSeconds = %d, want %d", got.MaxSessionAgeSeconds, tc.want.MaxSessionAgeSeconds)
			}
			if got.MaxResumeWindowSeconds != tc.want.MaxResumeWindowSeconds {
				t.Errorf("MaxResumeWindowSeconds = %d, want %d", got.MaxResumeWindowSeconds, tc.want.MaxResumeWindowSeconds)
			}
		})
	}
}

// A zero cap field disables that clamp so deployer "unlimited" survives.
func TestClampRetryPolicyZeroCapDisablesClamp(t *testing.T) {
	caps := session.RetryPolicyCaps{} // every cap zero
	in := &session.RetryPolicy{
		MaxRetries:             50,
		MaxSessionAgeSeconds:   100000,
		MaxResumeWindowSeconds: 99999,
	}
	got := session.ClampRetryPolicy(in, caps)
	if got.MaxRetries != 50 || got.MaxSessionAgeSeconds != 100000 || got.MaxResumeWindowSeconds != 99999 {
		t.Errorf("ClampRetryPolicy with zero caps = %+v, want input preserved", got)
	}
}

// The failure lists are cloned so the gateway never aliases the
// caller's slice; subsequent mutations of the input must not leak.
func TestClampRetryPolicyClonesFailureLists(t *testing.T) {
	in := &session.RetryPolicy{
		RetryableFailures:    []string{"pod_evicted"},
		NonRetryableFailures: []string{"workspace_validation_failed"},
	}
	got := session.ClampRetryPolicy(in, session.RetryPolicyCaps{})
	in.RetryableFailures[0] = "MUTATED"
	in.NonRetryableFailures[0] = "MUTATED"
	if got.RetryableFailures[0] != "pod_evicted" {
		t.Errorf("RetryableFailures aliased input: got %q", got.RetryableFailures[0])
	}
	if got.NonRetryableFailures[0] != "workspace_validation_failed" {
		t.Errorf("NonRetryableFailures aliased input: got %q", got.NonRetryableFailures[0])
	}
}
