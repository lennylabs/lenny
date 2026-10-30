// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"errors"
	"testing"
)

// rawExtras encodes each value as a JSON string under its claim name,
// as Verify populates Claims.Extras from a token payload.
func rawExtras(t *testing.T, claims map[string]string) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(claims))
	for name, v := range claims {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = b
	}
	return out
}

type stubVerifier struct {
	claims Claims
	err    error
}

func (s stubVerifier) Verify(string) (Claims, error) { return s.claims, s.err }

// TestTenantClaimVerifierResolvesConfiguredClaim pins the wrapper's
// tenant selection: the configured claim replaces Claims.TenantID, an
// absent configured claim yields an empty TenantID even when the token
// carries `tenant_id`, an empty claim name reads `tenant_id`, and an
// inner rejection passes through unchanged.
// spec: 10.2 (Authentication: `tenant_id` extraction)
func TestTenantClaimVerifierResolvesConfiguredClaim(t *testing.T) {
	alt := Claims{TenantID: "globex"}
	alt.Extras = rawExtras(t, map[string]string{"acme_tenant": "acme"})

	cases := []struct {
		name  string
		inner Claims
		claim string
		want  string
	}{
		{"configured claim wins over tenant_id", alt, "acme_tenant", "acme"},
		{"absent configured claim clears tenant_id", Claims{TenantID: "acme"}, "acme_tenant", ""},
		{"empty claim name reads tenant_id", Claims{TenantID: "acme"}, "", "acme"},
		{"default claim name reads tenant_id", Claims{TenantID: "acme"}, "tenant_id", "acme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewTenantClaimVerifier(stubVerifier{claims: tc.inner}, tc.claim)
			got, err := v.Verify("token")
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got.TenantID != tc.want {
				t.Errorf("TenantID = %q, want %q", got.TenantID, tc.want)
			}
		})
	}

	sentinel := errors.New("signature_invalid")
	if _, err := NewTenantClaimVerifier(stubVerifier{err: sentinel}, "acme_tenant").Verify("token"); !errors.Is(err, sentinel) {
		t.Errorf("inner rejection = %v, want %v", err, sentinel)
	}
}

// TestNewTenantClaimVerifierRejectsNilInner pins the constructor's
// programming-error guard.
// spec: 10.2 (Authentication: `tenant_id` extraction)
func TestNewTenantClaimVerifierRejectsNilInner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewTenantClaimVerifier(nil) did not panic")
		}
	}()
	NewTenantClaimVerifier(nil, "tenant_id")
}
