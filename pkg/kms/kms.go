// SPDX-License-Identifier: MIT

// Package kms is the §4 KMS abstraction for envelope encryption. It
// defines the key-encryption-key (KEK) operation Lenny's encryption-at-
// rest model depends on: a KMS-backed KEK wraps a per-record data-
// encryption-key (DEK), and no plaintext DEK is ever persisted.
//
// The §4 / §12.9 model. T4 Restricted data — OAuth refresh tokens,
// user-supplied API keys, credential pool secrets — is stored under
// AES-256-GCM envelope encryption. Each record carries its own random
// 32-byte DEK. The DEK is wrapped (encrypted) by a KEK that lives in
// KMS; only the wrapped DEK is stored alongside the ciphertext. To
// read a record the holder asks KMS to unwrap the DEK, decrypts the
// record with the recovered DEK, and discards the plaintext DEK. A
// database dump exposes only wrapped DEKs and ciphertext.
//
// Provider is the KEK seam. WrapDEK and UnwrapDEK are the two KEK
// operations the envelope helper (pkg/kms/envelope) calls. A KEK is
// addressed by an alias string; the §4.9.1 rotation procedure rotates
// the key material behind an alias and stamps the version that wrapped
// each record so any prior version can still unwrap.
//
// This package ships one fully implemented Provider: Local, a
// deterministic in-process KEK for the no-cloud development path and
// for tests. Cloud KEK providers (AWS KMS, GCP Cloud KMS, Azure Key
// Vault) implement the same Provider interface; §4 specifies the
// envelope model and the cloud KMS integrations operationally
// (EKS/GKE/AKS encryption configuration) but does not pin a v1 cloud
// KMS client API, so the cloud providers are a documented seam — see
// CloudProviderSeam.
package kms

import (
	"context"
	"errors"
)

// Sentinel errors. Callers compare with errors.Is.
var (
	// ErrUnknownKEK is returned when an alias names a KEK the provider
	// has no key material for.
	ErrUnknownKEK = errors.New("kms: unknown KEK alias")

	// ErrUnknownKEKVersion is returned by UnwrapDEK when the wrapped
	// DEK names a KEK version the provider does not have. A wrapped
	// DEK from a future version, or from a version pruned past the
	// §4.9.1 90-day retention window, fails this way.
	ErrUnknownKEKVersion = errors.New("kms: unknown KEK version")

	// ErrUnwrap is returned when a wrapped DEK fails authenticated
	// decryption under the named KEK. A wrapped DEK that was tampered
	// with, truncated, or produced by a different KEK fails this way.
	ErrUnwrap = errors.New("kms: DEK unwrap failed")

	// ErrMalformedWrappedDEK is returned when a wrapped DEK is too
	// short or otherwise structurally invalid before any cryptographic
	// check runs.
	ErrMalformedWrappedDEK = errors.New("kms: malformed wrapped DEK")
)

// WrappedDEK is a data-encryption-key encrypted under a KEK. It is the
// only form of a DEK that is ever persisted. KEKVersion records which
// version of the KEK alias produced Ciphertext, so UnwrapDEK can pick
// the right key material after a §4.9.1 KEK rotation.
type WrappedDEK struct {
	// KEKVersion is the 1-indexed version of the KEK alias that
	// wrapped this DEK. Version 0 is never valid.
	KEKVersion int

	// Ciphertext is the wrapped (encrypted) DEK bytes. Its internal
	// structure is provider-private; callers treat it as opaque and
	// hand it back to UnwrapDEK verbatim.
	Ciphertext []byte
}

// Provider is the §4 KMS KEK seam. A Provider wraps and unwraps data-
// encryption-keys under a KEK addressed by an alias. Implementations
// must be safe for concurrent use.
//
// A Provider never sees record plaintext. It only ever handles 32-byte
// DEKs. The envelope helper (pkg/kms/envelope) is the sole caller; it
// generates a random DEK, calls WrapDEK to obtain the storable form,
// encrypts the record with the DEK under AES-256-GCM, and discards the
// plaintext DEK. On read it calls UnwrapDEK to recover the DEK.
type Provider interface {
	// WrapDEK encrypts a 32-byte data-encryption-key under the current
	// version of the KEK named by alias. The returned WrappedDEK
	// records the KEK version so UnwrapDEK can recover it after a KEK
	// rotation. WrapDEK rejects a DEK whose length is not 32 bytes.
	WrapDEK(ctx context.Context, alias string, dek []byte) (WrappedDEK, error)

	// UnwrapDEK recovers the plaintext data-encryption-key from a
	// WrappedDEK. It selects the KEK version recorded on the
	// WrappedDEK so a DEK wrapped under a prior KEK version still
	// unwraps after a §4.9.1 rotation. It returns ErrUnknownKEK,
	// ErrUnknownKEKVersion, ErrUnwrap, or ErrMalformedWrappedDEK on
	// failure.
	UnwrapDEK(ctx context.Context, alias string, wrapped WrappedDEK) ([]byte, error)

	// CurrentKEKVersion reports the version number WrapDEK stamps for
	// the alias. The §4.9.1 re-encryption job reads this to find rows
	// still wrapped under a superseded version. It returns
	// ErrUnknownKEK when the alias has no key material.
	CurrentKEKVersion(ctx context.Context, alias string) (int, error)
}

// DEKSize is the byte length of a data-encryption-key. AES-256-GCM
// (§4) uses a 256-bit key.
const DEKSize = 32

// CloudProviderSeam documents the cloud-KMS extension point.
//
// §4 requires cloud-managed deployments to enable envelope encryption
// through the cloud provider's KMS (AWS KMS on EKS, GCP Cloud KMS on
// GKE, Azure Key Vault on AKS) and specifies that integration
// operationally — the cluster encryption configuration and the
// per-tenant key lifecycle. It does not pin a v1 Go client API for
// calling those KMS services directly from the Token Service.
//
// A cloud KEK provider satisfies the seam by implementing Provider:
//
//   - WrapDEK calls the cloud KMS Encrypt operation against the KEK
//     resource the alias maps to (an AWS KMS key ARN, a GCP Cloud KMS
//     CryptoKey resource name, an Azure Key Vault key identifier),
//     passing the 32-byte DEK as plaintext and recording the KMS key
//     version on the returned WrappedDEK.
//   - UnwrapDEK calls the cloud KMS Decrypt operation, selecting the
//     recorded key version.
//   - CurrentKEKVersion reads the alias's primary/current key version
//     from the cloud KMS.
//
// The cloud provider maps a Lenny alias (for example "tenant:acme" or
// "platform:token-service-signing") to the cloud KMS resource through
// deployment configuration. Until a cloud provider is wired, Local is
// the implemented Provider; it is the no-cloud development KEK and the
// test KEK, and it satisfies the same Provider contract a cloud
// provider must satisfy.
const CloudProviderSeam = "cloud KEK providers implement kms.Provider; see the doc comment"
