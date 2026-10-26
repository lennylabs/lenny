// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"math/big"
	"strings"
	"testing"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// awsProvider resolves the real AWS KMS adapter against s, as a binary
// started with s.Args() and s.Env() does.
func awsProvider(t *testing.T, s *AWSServer) pkgkms.Provider {
	t.Helper()
	for _, kv := range s.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	p, err := providerflags.Resolve(context.Background(), providerflags.Options{
		Provider:   providerflags.ProviderAWS,
		AWSRegion:  "us-east-1",
		AliasToKey: map[string]string{pkgkms.AliasJWTSigning: s.KeyID(), "tenant:acme": s.KeyID()},
	})
	if err != nil {
		t.Fatalf("resolve aws provider: %v", err)
	}
	return p
}

// spec: 10.2 (Authentication) "Signing topology"
// diagnosis: the stub's AWS KMS protocol does not match what the real AWS
// adapter sends or parses, so a binary started on --kms-provider=aws
// against it cannot load its key set or sign, and the cross-process KMS
// outage tests fail for a harness reason.
func TestAWSServerServesTheRealAdapter(t *testing.T) {
	s := NewAWSServer(t)
	p := awsProvider(t, s)
	ctx := context.Background()

	pub, err := p.PublicKey(ctx, pkgkms.AliasJWTSigning)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if !pub.Equal(s.PublicKey()) {
		t.Fatal("PublicKey returned a key other than the stub's signing key")
	}
	digest := sha256.Sum256([]byte("payload"))
	sig, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest[:])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatalf("Sign returned a signature that does not verify under the stub key (%d bytes)", len(sig))
	}
	dek := make([]byte, pkgkms.DEKSize)
	wrapped, err := p.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if got, err := p.UnwrapDEK(ctx, "tenant:acme", wrapped); err != nil || len(got) != pkgkms.DEKSize {
		t.Fatalf("UnwrapDEK = %d bytes, %v", len(got), err)
	}
	if n := s.SignRequests(); n != 1 {
		t.Errorf("SignRequests = %d, want 1", n)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: SetFailing does not fail Sign hard, or the adapter reports the
// stub's outage as throttling, so the chaos test cannot trip the signing
// breaker through it; or the stub does not recover after SetFailing(false).
func TestAWSServerFailingSignIsHardFailure(t *testing.T) {
	s := NewAWSServer(t)
	p := awsProvider(t, s)
	ctx := context.Background()
	digest := sha256.Sum256([]byte("payload"))

	s.SetFailing(true)
	_, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest[:])
	if err == nil {
		t.Fatal("Sign succeeded against a failing stub")
	}
	if errors.Is(err, pkgkms.ErrThrottled) {
		t.Fatalf("Sign error %v is throttling, want a hard failure", err)
	}
	if n := s.SignRequests(); n != 1 {
		t.Errorf("SignRequests = %d after one failed Sign, want 1 (no retry)", n)
	}
	s.SetFailing(false)
	if _, err := p.Sign(ctx, pkgkms.AliasJWTSigning, digest[:]); err != nil {
		t.Fatalf("Sign after recovery: %v", err)
	}
}

// spec: 10.2 (Authentication) "Signing topology"
// diagnosis: the flags render the aliases as separate --kms-alias flags or
// in a form ParseAliases rejects, so a binary keeps only one alias and a
// tenant KEK or the signing key resolves to nothing.
func TestAWSServerArgsRenderOneAliasFlag(t *testing.T) {
	s := NewAWSServer(t)
	var aliasFlags []string
	for _, a := range s.Args("acme", "default") {
		if v, ok := strings.CutPrefix(a, "--kms-alias="); ok {
			aliasFlags = append(aliasFlags, v)
		}
	}
	if len(aliasFlags) != 1 {
		t.Fatalf("Args rendered %d --kms-alias flags, want 1", len(aliasFlags))
	}
	m, err := providerflags.ParseAliases(aliasFlags[0])
	if err != nil {
		t.Fatalf("ParseAliases: %v", err)
	}
	for _, alias := range []string{pkgkms.AliasJWTSigning, "tenant:acme", "tenant:default"} {
		if m[alias] != s.KeyID() {
			t.Errorf("alias %s maps to %q, want %q", alias, m[alias], s.KeyID())
		}
	}
}
