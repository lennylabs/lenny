// SPDX-License-Identifier: MIT

// Package kms is the §4 KMS abstraction. It defines the two families of
// key operation Lenny delegates to a key-management service: key-
// encryption-key (KEK) wrap and unwrap for envelope encryption, and
// asymmetric ES256 signing for Lenny-issued JWTs.
//
// The §4 / §12.9 envelope model. T4 Restricted data, such as OAuth
// refresh tokens, user-supplied API keys, and credential pool secrets, is
// stored under AES-256-GCM envelope encryption. Each record carries its
// own random 32-byte data-encryption-key (DEK). The DEK is wrapped by a
// KEK that lives in KMS, and only the wrapped DEK is stored alongside the
// ciphertext. To read a record the holder asks KMS to unwrap the DEK,
// decrypts the record with the recovered DEK, and discards the plaintext
// DEK. A database dump exposes only wrapped DEKs and ciphertext.
//
// The §10.2 signing model. Every Lenny-issued JWT is signed with ES256
// under the key the platform:jwt-signing alias names. On a cloud provider
// the private key stays inside the KMS: the signer sends the SHA-256
// digest of the JWS signing input to the KMS Sign operation, and every
// verifier loads the public keys once at startup through PublicKey.
//
// A key is addressed by an alias string. The deployment maps each alias
// to a provider key reference through the --kms-alias flag
// (pkg/kms/providerflags).
//
// This package ships Local, a deterministic in-process provider for the
// no-cloud development path and for tests. The cloud providers (AWS KMS,
// GCP Cloud KMS, and Azure Key Vault) live in pkg/kms/aws, pkg/kms/gcp,
// and pkg/kms/azure and implement the same Provider interface.
package kms

import (
	"context"
	"crypto/ecdsa"
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

	// ErrThrottled is returned by Sign and PublicKey when the provider
	// refused the request because a request-rate quota is exhausted (AWS
	// ThrottlingException or LimitExceededException, GCP
	// ResourceExhausted, Azure HTTP 429). It is retryable backpressure:
	// the §10.2 signer answers it with a retryable 503 and never counts
	// it toward the signing circuit breaker.
	ErrThrottled = errors.New("kms: request throttled by the provider")

	// ErrInvalidDigest is returned by Sign when the digest is not a
	// 32-byte SHA-256 digest.
	ErrInvalidDigest = errors.New("kms: sign digest must be a 32-byte SHA-256 digest")

	// ErrNotP256 is returned by PublicKey when the key the alias names is
	// not an ECDSA P-256 key, and by Sign when the provider returns a
	// signature that is not a P-256 ECDSA signature.
	ErrNotP256 = errors.New("kms: key is not an ECDSA P-256 key")
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

// Provider is the §4 KMS seam. Implementations must be safe for
// concurrent use. It carries two operation families.
//
// KEK operations. WrapDEK, UnwrapDEK, and CurrentKEKVersion wrap and
// unwrap 32-byte data-encryption-keys under a KEK. The envelope helper
// (pkg/kms/envelope) generates a random DEK, calls WrapDEK to obtain the
// storable form, encrypts the record with the DEK, and discards the
// plaintext DEK; on read it calls UnwrapDEK. The §4.9.1 re-encryption
// job, the tenant KMS probe (pkg/tenantkms), and the re-key tooling
// (pkg/kms/rekey) call the same operations. A KEK operation never sees
// record plaintext.
//
// Asymmetric operations. Sign and PublicKey back the §10.2 ES256 JWT
// signer. Sign takes the SHA-256 digest of the JWS signing input and
// returns the signature; PublicKey returns the public key every verifier
// loads into its key set at startup. On a cloud provider the private key
// never leaves the KMS.
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

	// Sign returns the ES256 signature of a 32-byte SHA-256 digest under
	// the key the alias names, as the 64-byte JWS r||s encoding (RFC 7518
	// section 3.4). It returns ErrInvalidDigest for a digest of any other
	// length, ErrUnknownKEK for an alias with no key, and ErrThrottled
	// when the provider refused the request on a rate quota. A cloud
	// provider makes exactly one provider request per call, so a
	// throttle surfaces at once instead of after SDK retries.
	Sign(ctx context.Context, alias string, digest []byte) ([]byte, error)

	// PublicKey returns the P-256 public key of the key the alias names.
	// It returns ErrNotP256 when the key is not an ECDSA P-256 key and
	// ErrUnknownKEK for an alias with no key.
	PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error)
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
