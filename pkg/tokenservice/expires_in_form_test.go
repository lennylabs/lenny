// SPDX-License-Identifier: MIT

package tokenservice

import (
	"net/url"
	"testing"
)

// TestParseFormExpiresInRejectsNonInteger pins the expires_in form field
// of POST /v1/oauth/token: absent yields no requested lifetime, an
// integer (surrounding space trimmed) is returned, and a non-integer is
// an error the endpoint answers as invalid_request.
//
// spec: 10.2 (Authentication) "JWT claims", 4.3 (Token Service)
func TestParseFormExpiresInRejectsNonInteger(t *testing.T) {
	if got, err := parseFormExpiresIn(url.Values{}); err != nil || got != nil {
		t.Errorf("absent expires_in = %v, %v; want nil, nil", got, err)
	}
	if got, err := parseFormExpiresIn(url.Values{"expires_in": {" 600 "}}); err != nil || got == nil || *got != 600 {
		t.Errorf("expires_in=600 = %v, %v; want 600", got, err)
	}
	for _, bad := range []string{"ten", "60.5", ""} {
		if got, err := parseFormExpiresIn(url.Values{"expires_in": {bad}}); err == nil {
			t.Errorf("expires_in=%q = %v, want an error", bad, *got)
		}
	}
}
