// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	pkgkms "github.com/lennylabs/lenny/pkg/kms"
)

// Signer returns a §10.2 signer over the server's signing key, so a test
// can sign a token every process mapped to KeyID verifies without asking
// a gateway or the Token Service to mint it. Its Sign calls do not reach
// the server, so they are not counted by SignRequests and SetFailing does
// not fail them.
//
// spec: §10.2 (Authentication) "Signing topology".
func (s *AWSServer) Signer(t testing.TB) *jwt.RemoteSigner {
	t.Helper()
	pub := &s.key.PublicKey
	jwk, err := jwt.ECPublicJWK(pub)
	if err != nil {
		t.Fatalf("kms stub: signing key jwk: %v", err)
	}
	kid, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("kms stub: signing key thumbprint: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(awsKeyProvider{key: s.key}, jwt.KeySetEntry{KeyID: kid, PublicKey: pub})
	if err != nil {
		t.Fatalf("kms stub: remote signer: %v", err)
	}
	return signer
}

// awsKeyProvider signs digests with the server's key in process. Only
// Sign and PublicKey are implemented; the embedded nil Provider panics on
// any other call, which jwt.RemoteSigner never makes.
type awsKeyProvider struct {
	pkgkms.Provider
	key *ecdsa.PrivateKey
}

// Sign returns the raw r||s P-256 signature of digest.
func (p awsKeyProvider) Sign(_ context.Context, _ string, digest []byte) ([]byte, error) {
	r, sv, err := ecdsa.Sign(rand.Reader, p.key, digest)
	if err != nil {
		return nil, err
	}
	sig := make([]byte, pkgkms.SignatureSize)
	r.FillBytes(sig[:pkgkms.SignatureSize/2])
	sv.FillBytes(sig[pkgkms.SignatureSize/2:])
	return sig, nil
}

// PublicKey returns the server's signing public key.
func (p awsKeyProvider) PublicKey(context.Context, string) (*ecdsa.PublicKey, error) {
	return &p.key.PublicKey, nil
}
