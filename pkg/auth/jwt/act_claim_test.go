// SPDX-License-Identifier: MIT

package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
)

// rawPayload decodes the payload segment of a compact JWT into a generic
// map, so a test can assert which keys the signer wrote.
func rawPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	return m
}

// spec: 13.3 (Credential Flow) "Lenny JWT claim structure" act, 10.2 (Authentication) "JWT claims" typ
//
// The `act` claim round-trips through Sign and Verify as the flat RFC 8693
// object {sub, tenant_id, session_id, delegation_depth}. A token without an
// actor carries no `act` key at all rather than a null, and a root actor's
// delegation_depth 0 is written rather than dropped. An actor that is not a
// Lenny session omits session_id.
func TestActClaimRoundTrip_spec_13_3(t *testing.T) {
	s := NewHMACSigner("dev-key-1", []byte("test-secret"))
	base := Claims{
		Subject: "alice@acme.com", Audience: []string{"lenny-gateway"},
		Expiry: farFutureExpiry, IssuedAt: farFutureIssued, JWTID: "jti-1",
		TenantID: "acme",
	}
	cases := []struct {
		name    string
		act     *ActorClaim
		wantRaw map[string]any
	}{
		{name: "no actor", act: nil, wantRaw: nil},
		{
			name: "session actor at depth 1",
			act:  &ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_parent", DelegationDepth: 1},
			wantRaw: map[string]any{
				"sub": "bob@acme.com", "tenant_id": "acme",
				"session_id": "sess_parent", "delegation_depth": float64(1),
			},
		},
		{
			name: "root actor that is not a session",
			act:  &ActorClaim{Subject: "bob@acme.com", TenantID: "acme"},
			wantRaw: map[string]any{
				"sub": "bob@acme.com", "tenant_id": "acme", "delegation_depth": float64(0),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.Act = c.act
			in.Typ = auth.TokenUserBearer
			if c.act != nil {
				in.Typ = auth.TokenA2ADelegation
				in.DelegationDepth = c.act.DelegationDepth + 1
			}
			tok, err := s.Sign(in)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			raw := rawPayload(t, tok)
			gotRaw, present := raw["act"]
			switch {
			case c.wantRaw == nil && present:
				t.Fatalf("payload carries act %v, want the key omitted", gotRaw)
			case c.wantRaw != nil && !reflect.DeepEqual(gotRaw, c.wantRaw):
				t.Fatalf("payload act = %v, want %v", gotRaw, c.wantRaw)
			}
			out, err := s.Verify(tok)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !reflect.DeepEqual(out.Act, c.act) {
				t.Errorf("verified Act = %+v, want %+v", out.Act, c.act)
			}
		})
	}
}

// signHS256 signs a hand-built signing input with secret, so a test can
// present a payload the typed Claims struct cannot produce.
func signHS256(signingInput string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// spec: 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// RFC 8693 defines `act` as a JSON object. A payload whose `act` is any
// other JSON type fails verification as malformed, so no verifier admits a
// token whose delegation chain it cannot read.
func TestVerifyRejectsNonObjectActClaim_spec_13_3(t *testing.T) {
	secret := []byte("test-secret")
	s := NewHMACSigner("dev-key-1", secret)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"dev-key-1"}`))
	for _, act := range []string{`"bob@acme.com"`, `["bob@acme.com"]`, `7`} {
		t.Run(act, func(t *testing.T) {
			payload := `{"sub":"alice@acme.com","tenant_id":"acme","exp":4070908800,"act":` + act + `}`
			tok := signHS256(header+"."+base64.RawURLEncoding.EncodeToString([]byte(payload)), secret)
			_, err := s.Verify(tok)
			var verr *VerifyError
			if !errors.As(err, &verr) || verr.Reason != "malformed" {
				t.Fatalf("Verify err = %v, want a malformed VerifyError", err)
			}
		})
	}
	// The same hand-built path admits a well-formed object, so the
	// rejection above is the act type and not the construction.
	payload := `{"sub":"alice@acme.com","tenant_id":"acme","exp":4070908800,"act":{"sub":"bob@acme.com","tenant_id":"acme","delegation_depth":0}}`
	tok := signHS256(header+"."+base64.RawURLEncoding.EncodeToString([]byte(payload)), secret)
	out, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify well-formed act: %v", err)
	}
	if out.Act == nil || out.Act.Subject != "bob@acme.com" {
		t.Errorf("Act = %+v, want sub bob@acme.com", out.Act)
	}
}
