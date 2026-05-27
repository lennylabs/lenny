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

// spec: §7.3 lines 384-388 — ClassifyFailure recognises the three
// platform-default retryable causes and the two non-retryable causes
// when retryPolicy supplies no overrides.
func TestClassifyFailureUsesPlatformDefaults(t *testing.T) {
	cases := []struct {
		reason string
		want   session.FailureClassification
	}{
		{"pod_evicted", session.FailureRetryable},
		{"node_lost", session.FailureRetryable},
		{"runtime_crash", session.FailureRetryable},
		{"workspace_validation_failed", session.FailureNonRetryable},
		{"setup_command_failed", session.FailureNonRetryable},
		{"sigkill", session.FailureUnknown},
		{"", session.FailureUnclassified},
	}
	for _, tc := range cases {
		if got := session.ClassifyFailure(tc.reason, nil); got != tc.want {
			t.Errorf("ClassifyFailure(%q, nil) = %s, want %s", tc.reason, got, tc.want)
		}
	}
}

// spec: §7.3 line 382 — client_only forces every classifiable cause
// to NonRetryable so the gateway surfaces the failure to the client
// immediately as awaiting_client_action.
func TestClassifyFailureClientOnlyModeForcesNonRetryable(t *testing.T) {
	p := &session.RetryPolicy{Mode: session.RetryModeClientOnly}
	if got := session.ClassifyFailure("pod_evicted", p); got != session.FailureNonRetryable {
		t.Errorf("client_only pod_evicted = %s, want %s", got, session.FailureNonRetryable)
	}
	if got := session.ClassifyFailure("workspace_validation_failed", p); got != session.FailureNonRetryable {
		t.Errorf("client_only workspace_validation_failed = %s, want %s", got, session.FailureNonRetryable)
	}
	if got := session.ClassifyFailure("custom_reason", p); got != session.FailureUnknown {
		t.Errorf("client_only custom = %s, want %s (unknown stays unknown)", got, session.FailureUnknown)
	}
}

// spec: §7.3 line 384-385 — per-session lists override the platform
// defaults, not augment them. A retryPolicy that names only
// retryableFailures keeps the platform-default nonRetryableFailures.
func TestClassifyFailurePerSessionOverrides(t *testing.T) {
	p := &session.RetryPolicy{RetryableFailures: []string{"custom_retry"}}
	if got := session.ClassifyFailure("custom_retry", p); got != session.FailureRetryable {
		t.Errorf("custom_retry with override = %s, want %s", got, session.FailureRetryable)
	}
	// Platform default no longer matches because the override replaced
	// the list.
	if got := session.ClassifyFailure("pod_evicted", p); got != session.FailureUnknown {
		t.Errorf("pod_evicted with retryable override = %s, want %s", got, session.FailureUnknown)
	}
	// Platform-default non-retryable list still applies (no override).
	if got := session.ClassifyFailure("workspace_validation_failed", p); got != session.FailureNonRetryable {
		t.Errorf("workspace_validation_failed with retryable override = %s, want %s", got, session.FailureNonRetryable)
	}
}

// spec: §7.3 lines 384/385 — DefaultRetryableFailures /
// DefaultNonRetryableFailures expose the worked-example platform
// defaults so callers can echo them on the response without depending
// on the classifier internals.
func TestDefaultRetryableFailuresMatchSpec(t *testing.T) {
	wantR := []string{"pod_evicted", "node_lost", "runtime_crash"}
	gotR := session.DefaultRetryableFailures()
	if !slicesEqual(gotR, wantR) {
		t.Errorf("DefaultRetryableFailures = %v, want %v", gotR, wantR)
	}
	wantN := []string{"workspace_validation_failed", "setup_command_failed"}
	gotN := session.DefaultNonRetryableFailures()
	if !slicesEqual(gotN, wantN) {
		t.Errorf("DefaultNonRetryableFailures = %v, want %v", gotN, wantN)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FailureClassification.String returns the lower_snake label so log
// lines / audit rows surface the disposition uniformly.
func TestFailureClassificationString(t *testing.T) {
	cases := []struct {
		c    session.FailureClassification
		want string
	}{
		{session.FailureUnclassified, "unclassified"},
		{session.FailureRetryable, "retryable"},
		{session.FailureNonRetryable, "non_retryable"},
		{session.FailureUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}
