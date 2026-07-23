// SPDX-License-Identifier: MIT

package sessionserver

import "testing"

// TestParseRetryAfterHeader covers the numeric-seconds Retry-After parsing the
// create-and-start service reads off the recorder for a non-2xx envelope. The
// end-to-end pool-exhaustion path exercises only a valid positive value, so
// this pins the empty, zero, negative, and non-numeric branches that decide
// whether ServiceError.RetryAfterSeconds carries a backoff hint or stays zero.
// spec: §15.1 line 1138 (Retry-After delta-seconds form); §7.1 create-and-start
// atomicity (the §4.9 CREDENTIAL_POOL_EXHAUSTED rejection carries no header).
func TestParseRetryAfterHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty header carries no hint", "", 0},
		{"valid positive delta-seconds", "5", 5},
		{"zero is a valid non-negative value", "0", 0},
		{"negative is rejected as unset", "-3", 0},
		{"non-numeric is rejected as unset", "abc", 0},
		{"trailing junk is rejected as unset", "12x", 0},
		{"http-date form is intentionally unsupported", "Wed, 21 Oct 2026 07:28:00 GMT", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfterHeader(tc.in); got != tc.want {
				t.Errorf("parseRetryAfterHeader(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestServiceErrorError verifies the ServiceError.Error() rendering the typed
// service error exposes to a non-HTTP caller (the §15.2 MCP tool and the §15
// single-shot adapter binder), which formats the §16.3 code and message.
// spec: §15.2.1 rule 3 line 1384 (shared error envelope).
func TestServiceErrorError(t *testing.T) {
	err := &ServiceError{Code: "SESSION_CREATION_FAILED", Message: "warm pool exhausted"}
	if got, want := err.Error(), "SESSION_CREATION_FAILED: warm pool exhausted"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// An empty message still renders the code and separator, so a caller
	// logging the error never loses the code.
	empty := &ServiceError{Code: "INTERNAL_ERROR"}
	if got, want := empty.Error(), "INTERNAL_ERROR: "; got != want {
		t.Errorf("Error() empty-message = %q, want %q", got, want)
	}
}
