// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spec: §13.3 (Credential Flow)
//
// The step (1a) re-read refuses a revoked caller token, listed after the
// subject and the actor, with a SourceRevokedError whose reason is
// caller_token_revoked, reports a revoked subject before a revoked caller,
// and does not apply the impersonation predicate to the caller token.
func TestRecheckRefusesARevokedCallerTokenOnly(t *testing.T) {
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	sources := []SourceToken{
		{Role: SourceSubjectToken, JTI: "subject"},
		{Role: SourceCallerToken, JTI: "caller"},
	}
	cases := []struct {
		name     string
		rows     []recheckRow
		wantRole SourceRole
	}{
		{"revoked caller", []recheckRow{{jti: "subject"}, {jti: "caller", revokedAt: &at}}, SourceCallerToken},
		{"revoked subject before a revoked caller", []recheckRow{{jti: "caller", revokedAt: &at}, {jti: "subject", revokedAt: &at}}, SourceSubjectToken},
		{"impersonation caller passes", []recheckRow{{jti: "subject"}, {jti: "caller", impersonation: true}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := lockAndRecheck(context.Background(), &recheckTx{rows: &recheckRows{rows: tc.rows}}, "acme", sources)
			if tc.wantRole == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			var re *SourceRevokedError
			if !errors.As(err, &re) || re.Role != tc.wantRole {
				t.Fatalf("err = %v, want a SourceRevokedError naming %s", err, tc.wantRole)
			}
		})
	}
	if got := (&SourceRevokedError{Role: SourceCallerToken}).Reason(); got != "caller_token_revoked" {
		t.Errorf("reason = %q, want caller_token_revoked", got)
	}
}
