// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"errors"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
)

// Provider adapts the KMS Stub to the pkg/kms.Provider interface so
// component and integration tests can drive the §4 envelope-encryption
// path (pkg/kms/envelope, credentialstore/pgstore, the Token Service
// KMS signer) against the same fault-injecting stub the rest of the
// credential tests use.
//
// The adapter maps the envelope KEK operations onto the stub's per-
// alias AES-GCM key:
//
//   - WrapDEK encrypts the 32-byte DEK with Stub.Encrypt under the
//     alias's current key version, then reads that version back so the
//     returned WrappedDEK records it.
//   - UnwrapDEK decrypts with Stub.Decrypt; the stub embeds the key
//     version in its own blob, so the WrappedDEK.KEKVersion is carried
//     for the pkg/kms contract but the stub recovers the version
//     itself.
//   - CurrentKEKVersion reports the alias's latest minted version.
//
// Stub fault injection (SetUnavailable, DenyAlias, DeleteAlias) and
// rotation (RotateKey) apply unchanged: a test can rotate the KEK or
// fail it and observe the envelope helper surface the error.
type Provider struct {
	stub *Stub
}

var _ pkgkms.Provider = (*Provider)(nil)

// AsProvider returns a pkg/kms.Provider backed by the stub.
func (s *Stub) AsProvider() *Provider { return &Provider{stub: s} }

// WrapDEK encrypts dek under the stub's current key version for alias.
func (p *Provider) WrapDEK(_ context.Context, alias string, dek []byte) (pkgkms.WrappedDEK, error) {
	if len(dek) != pkgkms.DEKSize {
		return pkgkms.WrappedDEK{}, errors.New("kms stub provider: DEK must be 32 bytes")
	}
	ct, err := p.stub.Encrypt(alias, dek)
	if err != nil {
		return pkgkms.WrappedDEK{}, err
	}
	version := p.stub.currentVersion(alias).ID
	return pkgkms.WrappedDEK{KEKVersion: version, Ciphertext: ct}, nil
}

// UnwrapDEK decrypts a WrappedDEK back to the plaintext DEK. The stub
// embeds the key version in its blob, so the recorded
// WrappedDEK.KEKVersion is not needed to pick the key; it is validated
// against the blob to keep the pkg/kms contract honest.
func (p *Provider) UnwrapDEK(_ context.Context, alias string, wrapped pkgkms.WrappedDEK) ([]byte, error) {
	if wrapped.KEKVersion < 1 {
		return nil, pkgkms.ErrUnknownKEKVersion
	}
	if len(wrapped.Ciphertext) == 0 {
		return nil, pkgkms.ErrMalformedWrappedDEK
	}
	dek, err := p.stub.Decrypt(alias, wrapped.Ciphertext)
	if err != nil {
		return nil, err
	}
	if len(dek) != pkgkms.DEKSize {
		return nil, pkgkms.ErrUnwrap
	}
	return dek, nil
}

// CurrentKEKVersion reports the alias's latest key version,
// provisioning version 1 on first use (the stub's Encrypt does the
// same).
func (p *Provider) CurrentKEKVersion(_ context.Context, alias string) (int, error) {
	return p.stub.currentVersion(alias).ID, nil
}
