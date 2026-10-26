// SPDX-License-Identifier: MIT

package kms_test

import (
	"context"
	"errors"
	"testing"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// spec: 10.2 (Authentication)
// The stub signs with one P-256 key per alias, and its signature
// verifies under its own public key and no other alias's.
func TestStubSignVerifiesUnderPerAliasKey_spec_10_2(t *testing.T) {
	stub := kms.New(t)
	p := stub.AsProvider()
	ctx := context.Background()
	digest := kmstest.Digest("header.payload")
	sig, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, err := p.PublicKey(ctx, pkgkms.AliasJWTSigning)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if !kmstest.VerifyRS(pub, digest, sig) {
		t.Fatal("stub signature does not verify under the alias's public key")
	}
	other, err := p.PublicKey(ctx, pkgkms.AliasJWTVerify)
	if err != nil {
		t.Fatalf("PublicKey(verify): %v", err)
	}
	if kmstest.VerifyRS(other, digest, sig) {
		t.Fatal("two aliases share one stub key")
	}
	if stub.SignCalls() != 1 || stub.PublicKeyCalls() != 2 {
		t.Fatalf("calls = sign %d, public key %d; want 1 and 2", stub.SignCalls(), stub.PublicKeyCalls())
	}
}

// spec: 10.2 (Authentication)
func TestStubSignFaultInjection_spec_10_2(t *testing.T) {
	stub := kms.New(t)
	p := stub.AsProvider()
	ctx := context.Background()
	digest := kmstest.Digest("x")

	stub.SetSignThrottled(true)
	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest); !errors.Is(err, pkgkms.ErrThrottled) {
		t.Errorf("throttled Sign err=%v, want ErrThrottled", err)
	}
	stub.SetSignThrottled(false)

	hard := errors.New("injected hard failure")
	stub.SetSignError(hard)
	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest); !errors.Is(err, hard) || errors.Is(err, pkgkms.ErrThrottled) {
		t.Errorf("hard Sign err=%v, want the injected error", err)
	}
	stub.SetSignError(nil)

	stub.SetPublicKeyError(pkgkms.ErrUnknownKEK)
	if _, err := p.PublicKey(ctx, pkgkms.AliasJWTSigning); !errors.Is(err, pkgkms.ErrUnknownKEK) {
		t.Errorf("PublicKey err=%v, want the injected error", err)
	}
	stub.SetPublicKeyError(nil)

	stub.SetUnavailable(true)
	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest); !errors.Is(err, kms.ErrUnavailable) {
		t.Errorf("unavailable Sign err=%v, want ErrUnavailable", err)
	}
	stub.SetUnavailable(false)

	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest[:31]); !errors.Is(err, pkgkms.ErrInvalidDigest) {
		t.Errorf("31-byte digest err=%v, want ErrInvalidDigest", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Sign(cctx, pkgkms.AliasJWTSigning, digest); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Sign err=%v, want context.Canceled", err)
	}
	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest); err != nil {
		t.Errorf("Sign after clearing faults: %v", err)
	}
}
