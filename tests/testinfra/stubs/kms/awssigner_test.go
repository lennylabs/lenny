// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	pkgkms "github.com/lennylabs/lenny/pkg/kms"
)

// spec: 10.2 (Authentication) "Signing topology"
// diagnosis: a token the in-process signer produces does not verify
// against the key set a process mapped to the server's key loads, so a
// chaos test cannot sign a subject token the gateway and the Token Service
// accept; or the signer reaches the server and is counted or failed by it.
func TestAWSServerSignerVerifiesUnderTheServerKeySet(t *testing.T) {
	s := NewAWSServer(t)
	ks, err := jwt.LoadKeySet(context.Background(), awsProvider(t, s), map[string]string{pkgkms.AliasJWTSigning: s.KeyID()})
	if err != nil {
		t.Fatalf("load key set: %v", err)
	}
	signer := s.Signer(t)
	if signer.KeyID() != ks.Signing().KeyID {
		t.Fatalf("signer kid %q, key set kid %q", signer.KeyID(), ks.Signing().KeyID)
	}
	s.SetFailing(true)
	before := s.SignRequests()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		IssuedAt: time.Now().Unix(), Expiry: time.Now().Add(time.Minute).Unix(), JWTID: "stub-signed",
	})
	if err != nil {
		t.Fatalf("sign with the server failing: %v", err)
	}
	if s.SignRequests() != before {
		t.Fatalf("the in-process signer reached the server")
	}
	c, err := ks.Verify(tok)
	if err != nil || c.Subject != "alice@acme.com" {
		t.Fatalf("verify: claims %+v err %v", c, err)
	}
}
