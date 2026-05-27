// SPDX-License-Identifier: MIT

package session

import (
	"fmt"
	"sort"
)

// RetryPolicy is the §7.3 client-supplied retry policy carried on
// CreateSession. Each field is independently optional: the gateway
// fills missing values from the deployer defaults at admission and
// clamps every populated value against the deployer caps so a client
// cannot grow its budget past the platform's published bounds.
//
// The §7.3 line 381 worked example:
//
//	{
//	  "mode": "auto_then_client",
//	  "maxRetries": 2,
//	  "retryableFailures": ["pod_evicted", "node_lost", "runtime_crash"],
//	  "nonRetryableFailures": ["workspace_validation_failed", "setup_command_failed"],
//	  "maxSessionAgeSeconds": 7200,
//	  "maxResumeWindowSeconds": 900
//	}
//
// spec: §7.3 lines 377-393.
type RetryPolicy struct {
	// Mode governs whether the gateway runs the auto-retry path before
	// surfacing the failure to the client. The §7.3 worked example pins
	// "auto_then_client" as the default; "client_only" disables the
	// auto-retry chain so every failure surfaces immediately as
	// awaiting_client_action.
	Mode RetryMode `json:"mode,omitempty"`

	// MaxRetries is the §7.3 auto-retry budget: the number of automatic
	// pod-recovery attempts the gateway runs before declaring the
	// session awaiting_client_action. Zero (the wire default) selects
	// the deployer's DefaultMaxRetries.
	MaxRetries int `json:"maxRetries,omitempty"`

	// RetryableFailures enumerates the §7.3 failure classes the auto-
	// retry path applies to. An empty slice selects the §7.3 line 384
	// platform default. Unknown values are preserved verbatim so a
	// later platform version can extend the catalog without a client-
	// side migration.
	RetryableFailures []string `json:"retryableFailures,omitempty"`

	// NonRetryableFailures enumerates the §7.3 failure classes that
	// bypass the auto-retry path and go straight to awaiting_client_action.
	// An empty slice selects the §7.3 line 385 platform default.
	NonRetryableFailures []string `json:"nonRetryableFailures,omitempty"`

	// MaxSessionAgeSeconds is the §7.3 per-session total-lifetime cap.
	// The gateway clamps the value against the deployer's matching cap
	// (typically the watchdog's MaxSessionAgeSeconds) so the platform
	// bound is the upper limit. Zero selects the deployer default.
	MaxSessionAgeSeconds int `json:"maxSessionAgeSeconds,omitempty"`

	// MaxResumeWindowSeconds is the §7.3 line 404 resume-window timer
	// that bounds how long a session may sit in resume_pending while
	// the gateway tries to allocate a fresh warm pod before falling
	// back to awaiting_client_action. Zero selects the deployer default.
	MaxResumeWindowSeconds int `json:"maxResumeWindowSeconds,omitempty"`
}

// RetryMode is the closed §7.3 enum on RetryPolicy.Mode.
type RetryMode string

const (
	// RetryModeAutoThenClient is the §7.3 line 382 default: the
	// gateway runs up to maxRetries automatic recovery attempts before
	// surfacing the failure to the client via awaiting_client_action.
	RetryModeAutoThenClient RetryMode = "auto_then_client"
	// RetryModeClientOnly disables auto-retry: every failure surfaces
	// to the client immediately as awaiting_client_action.
	RetryModeClientOnly RetryMode = "client_only"
)

// DefaultRetryMode is the §7.3 line 382 worked-example default.
const DefaultRetryMode = RetryModeAutoThenClient

// AllRetryModes returns the closed §7.3 mode enum in declaration order.
func AllRetryModes() []RetryMode {
	return []RetryMode{RetryModeAutoThenClient, RetryModeClientOnly}
}

// IsValid reports whether m is a known §7.3 retry mode.
func (m RetryMode) IsValid() bool {
	for _, v := range AllRetryModes() {
		if m == v {
			return true
		}
	}
	return false
}

// Resolve returns m when it is valid; otherwise the §7.3 default.
func (m RetryMode) Resolve() RetryMode {
	if m.IsValid() {
		return m
	}
	return DefaultRetryMode
}

// RetryPolicyCaps is the deployer-supplied set of upper bounds the
// gateway applies to a client-supplied RetryPolicy. Each field is the
// hard ceiling: a client value above the cap is clamped down; a
// zero/unset client value falls through to the cap as the effective
// value.
//
// spec: §7.3 line 377 — "Retry policy is set per session by the
// client, bounded by deployer caps".
type RetryPolicyCaps struct {
	// MaxRetries is the deployer's upper bound on RetryPolicy.MaxRetries.
	MaxRetries int
	// MaxSessionAgeSeconds is the deployer's upper bound on
	// RetryPolicy.MaxSessionAgeSeconds (mirrors the watchdog's same-named
	// platform-wide cap).
	MaxSessionAgeSeconds int
	// MaxResumeWindowSeconds is the deployer's upper bound on
	// RetryPolicy.MaxResumeWindowSeconds.
	MaxResumeWindowSeconds int
}

// ValidateRetryPolicy is the gateway-side admission check. It rejects
// negative values with a structured error citing the §7.3 field; the
// caller maps the error to its 400 envelope. Modes are restricted to
// the closed enum; unknown values reject.
//
// spec: §7.3 lines 377-393.
func ValidateRetryPolicy(p *RetryPolicy) error {
	if p == nil {
		return nil
	}
	if p.Mode != "" && !p.Mode.IsValid() {
		return &RetryPolicyValidationError{Field: "mode", Reason: fmt.Sprintf("unknown mode %q; allowed: %v", p.Mode, modeStrings())}
	}
	if p.MaxRetries < 0 {
		return &RetryPolicyValidationError{Field: "maxRetries", Reason: "must be non-negative"}
	}
	if p.MaxSessionAgeSeconds < 0 {
		return &RetryPolicyValidationError{Field: "maxSessionAgeSeconds", Reason: "must be non-negative"}
	}
	if p.MaxResumeWindowSeconds < 0 {
		return &RetryPolicyValidationError{Field: "maxResumeWindowSeconds", Reason: "must be non-negative"}
	}
	return nil
}

// ClampRetryPolicy returns the effective per-session policy after
// applying caps. A nil input yields the deployer default policy (the
// cap values themselves act as the effective values when the client
// supplied nothing). A zero cap field skips that clamp so deployer
// "unlimited" semantics survive.
//
// The mode is normalised to DefaultRetryMode when unset or unknown,
// matching RetryMode.Resolve().
//
// Failure lists are not clamped — they are advisory category lists the
// auto-retry path consults; the deployer admission gates entries via
// the runtime-registry policy at registration time, not here.
//
// spec: §7.3 lines 377-393.
func ClampRetryPolicy(p *RetryPolicy, caps RetryPolicyCaps) RetryPolicy {
	out := RetryPolicy{Mode: DefaultRetryMode}
	if p != nil {
		out = *p
		out.RetryableFailures = append([]string(nil), p.RetryableFailures...)
		out.NonRetryableFailures = append([]string(nil), p.NonRetryableFailures...)
	}
	out.Mode = out.Mode.Resolve()
	if caps.MaxRetries > 0 {
		if out.MaxRetries <= 0 || out.MaxRetries > caps.MaxRetries {
			out.MaxRetries = caps.MaxRetries
		}
	}
	if caps.MaxSessionAgeSeconds > 0 {
		if out.MaxSessionAgeSeconds <= 0 || out.MaxSessionAgeSeconds > caps.MaxSessionAgeSeconds {
			out.MaxSessionAgeSeconds = caps.MaxSessionAgeSeconds
		}
	}
	if caps.MaxResumeWindowSeconds > 0 {
		if out.MaxResumeWindowSeconds <= 0 || out.MaxResumeWindowSeconds > caps.MaxResumeWindowSeconds {
			out.MaxResumeWindowSeconds = caps.MaxResumeWindowSeconds
		}
	}
	return out
}

// RetryPolicyValidationError is returned by ValidateRetryPolicy. It
// carries the offending field so the §15.1 error envelope can surface
// details.field per the §7.3 admission contract.
type RetryPolicyValidationError struct {
	Field  string
	Reason string
}

func (e *RetryPolicyValidationError) Error() string {
	return fmt.Sprintf("retryPolicy.%s: %s", e.Field, e.Reason)
}

func modeStrings() []string {
	out := make([]string, 0, len(AllRetryModes()))
	for _, m := range AllRetryModes() {
		out = append(out, string(m))
	}
	sort.Strings(out)
	return out
}
