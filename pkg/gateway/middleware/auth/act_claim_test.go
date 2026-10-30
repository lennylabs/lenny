// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: 13.3 (Credential Flow) "Lenny JWT claim structure" act, 15 (External API Surface) "Shared Adapter Types" CallerIdentity.Act
//
// The RFC 8693 `act` claim of a verified bearer reaches handlers as
// Principal.Act, the source of the §15 CallerIdentity.Act projection. An
// a2a_delegation bearer names its delegating actor; a bearer without the
// claim resolves to a nil Act.
func TestBearerCarriesActClaim_spec_13_3(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	act := &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1}
	childTok, err := signer.Sign(jwt.Claims{
		TenantID: "acme", Subject: "alice@acme.com", Typ: pkgauth.TokenA2ADelegation,
		DelegationDepth: 2, Act: act, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign child token: %v", err)
	}
	plainTok, err := signer.Sign(jwt.Claims{
		TenantID: "acme", Subject: "alice@acme.com", Typ: pkgauth.TokenUserBearer,
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign plain token: %v", err)
	}

	for _, tc := range []struct {
		name    string
		token   string
		wantAct *jwt.ActorClaim
	}{
		{"a2a_delegation bearer", childTok, act},
		{"bearer without act", plainTok, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner, got := captureHandler()
			h := Wrap(inner, Options{Verifier: signer, MultiTenant: true, Registry: permissiveRegistry{}})
			req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
			}
			if !reflect.DeepEqual(got.Act, tc.wantAct) {
				t.Errorf("Principal.Act = %+v, want %+v", got.Act, tc.wantAct)
			}
		})
	}
}

// spec: 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// copyActor returns an independent copy, so editing the principal's actor
// does not reach the verified claims.
func TestCopyActorIsIndependent_spec_13_3(t *testing.T) {
	if copyActor(nil) != nil {
		t.Fatal("copyActor(nil) is non-nil")
	}
	src := &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme"}
	dst := copyActor(src)
	dst.Subject = "carol@acme.com"
	if src.Subject != "bob@acme.com" {
		t.Errorf("source Subject = %q after editing the copy", src.Subject)
	}
}
