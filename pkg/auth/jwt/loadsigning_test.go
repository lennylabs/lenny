// SPDX-License-Identifier: MIT

package jwt_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// spec: 10.2 (Authentication) "Signing topology", "Verifier key set and rotation"
//
// LoadSigning makes one PublicKey fetch, and the breaker-wrapped signer
// it returns signs under the key set's signing kid, so its tokens verify
// against the key set it returned.
func TestLoadSigningBuildsSignerFromOneKeySetFetch_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	if got := stub.PublicKeyCalls(); got != 1 {
		t.Errorf("PublicKey calls = %d, want 1", got)
	}
	if signer.KeyID() != ks.Signing().KeyID {
		t.Errorf("signer kid %q, key set signing kid %q", signer.KeyID(), ks.Signing().KeyID)
	}
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(), JWTID: "jti-1",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := ks.Verify(tok); err != nil {
		t.Errorf("key set refused the signer's token: %v", err)
	}
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
//
// A key set that cannot load yields no signer and no key set, and the
// provider error stays inspectable, so a caller's startup exit names it.
func TestLoadSigningReturnsKeySetLoadError_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	boom := errors.New("kms endpoint refused the connection")
	stub.SetPublicKeyError(boom)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), nil, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the provider error", err)
	}
	if ks != nil || signer != nil {
		t.Errorf("key set %v and signer %v returned on failure, want nil", ks, signer)
	}
}
