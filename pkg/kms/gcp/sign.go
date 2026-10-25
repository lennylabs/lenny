// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"crypto/ecdsa"
	"encoding/pem"
	"fmt"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/kms"
)

// noRetry disables the client's default retry for one call, so a
// throttled or unavailable Sign returns after one provider request
// instead of after the client's backoff retries consume the caller's
// signing timeout.
//
// spec: §10.2 (Authentication): throttling is backpressure.
var noRetry = gax.WithRetry(func() gax.Retryer { return nil })

// Sign signs the 32-byte SHA-256 digest under the CryptoKeyVersion the
// alias resolves to and returns the 64-byte r||s signature. Cloud KMS
// returns a DER signature, which is converted with left-padding.
//
// spec: §10.2 (Authentication) "Production".
func (p *Provider) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	if err := kms.CheckDigest(digest); err != nil {
		return nil, err
	}
	keyName, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:   keyName,
		Digest: &kmspb.Digest{Digest: &kmspb.Digest_Sha256{Sha256: digest}},
	}, noRetry)
	if err != nil {
		return nil, mapSignError("AsymmetricSign", err)
	}
	return kms.SignatureFromASN1(out.GetSignature())
}

// PublicKey returns the P-256 public key of the CryptoKeyVersion the
// alias resolves to. A version whose algorithm is not
// EC_SIGN_P256_SHA256 fails with kms.ErrNotP256.
func (p *Provider) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	keyName, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: keyName})
	if err != nil {
		return nil, mapSignError("GetPublicKey", err)
	}
	if out.GetAlgorithm() != kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256 {
		return nil, fmt.Errorf("%w: alias %q has algorithm %s", kms.ErrNotP256, alias, out.GetAlgorithm())
	}
	block, _ := pem.Decode([]byte(out.GetPem()))
	if block == nil {
		return nil, fmt.Errorf("kms/gcp: GetPublicKey: alias %q returned no PEM block", alias)
	}
	return kms.PublicKeyFromPKIX(block.Bytes)
}

// mapSignError wraps ResourceExhausted in kms.ErrThrottled and NotFound
// in kms.ErrUnknownKEK, keeping the gRPC error in the chain.
func mapSignError(op string, err error) error {
	switch status.Code(err) {
	case codes.ResourceExhausted:
		return fmt.Errorf("kms/gcp: %s: %w: %w", op, kms.ErrThrottled, err)
	case codes.NotFound:
		return fmt.Errorf("kms/gcp: %s: %w: %w", op, kms.ErrUnknownKEK, err)
	}
	return fmt.Errorf("kms/gcp: %s: %w", op, err)
}
