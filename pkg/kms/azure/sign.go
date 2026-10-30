// SPDX-License-Identifier: MIT

package azure

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/lennylabs/lenny/pkg/kms"
)

// p256CoordSize is the byte length of one P-256 coordinate.
const p256CoordSize = 32

// Sign signs the 32-byte digest with ES256 under the Key Vault key the
// alias resolves to and returns the 64-byte r||s signature, which is the
// encoding Key Vault returns. The SDK retry is disabled for the call
// (MaxRetries -1), so a throttled Sign returns kms.ErrThrottled after one
// provider request instead of after the SDK's backoff retries consume
// the caller's signing timeout.
//
// spec: §10.2 (Authentication) "Production"; throttling is backpressure.
func (p *Provider) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	if err := kms.CheckDigest(digest); err != nil {
		return nil, err
	}
	ref, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	alg := azkeys.SignatureAlgorithmES256
	ctx = policy.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: -1})
	out, err := p.client.Sign(ctx, ref.name, ref.version, azkeys.SignParameters{
		Algorithm: &alg,
		Value:     digest,
	}, nil)
	if err != nil {
		return nil, mapSignError("Sign", err)
	}
	if err := kms.CheckRawSignature(out.Result); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// PublicKey returns the P-256 public key of the Key Vault key the alias
// resolves to. A key that is not an EC P-256 key fails with
// kms.ErrNotP256.
func (p *Provider) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	ref, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.GetKey(ctx, ref.name, ref.version, nil)
	if err != nil {
		return nil, mapSignError("GetKey", err)
	}
	return publicKeyFromJWK(alias, out.Key)
}

// publicKeyFromJWK builds a P-256 public key from a Key Vault JSON Web
// Key, rejecting any other key type or curve and any point off the curve.
func publicKeyFromJWK(alias string, jwk *azkeys.JSONWebKey) (*ecdsa.PublicKey, error) {
	if jwk == nil || jwk.Kty == nil || jwk.Crv == nil {
		return nil, fmt.Errorf("%w: alias %q returned no EC key", kms.ErrNotP256, alias)
	}
	if *jwk.Kty != azkeys.KeyTypeEC && *jwk.Kty != azkeys.KeyTypeECHSM {
		return nil, fmt.Errorf("%w: alias %q has key type %s", kms.ErrNotP256, alias, *jwk.Kty)
	}
	if *jwk.Crv != azkeys.CurveNameP256 {
		return nil, fmt.Errorf("%w: alias %q has curve %s", kms.ErrNotP256, alias, *jwk.Crv)
	}
	if len(jwk.X) > p256CoordSize || len(jwk.Y) > p256CoordSize {
		return nil, fmt.Errorf("%w: alias %q has an oversized coordinate", kms.ErrNotP256, alias)
	}
	point := make([]byte, 1+2*p256CoordSize)
	point[0] = 0x04 // SEC 1 uncompressed point marker
	copy(point[1+p256CoordSize-len(jwk.X):1+p256CoordSize], jwk.X)
	copy(point[1+2*p256CoordSize-len(jwk.Y):], jwk.Y)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return nil, fmt.Errorf("%w: alias %q: %v", kms.ErrNotP256, alias, err)
	}
	return pub, nil
}

// mapSignError wraps HTTP 429 in kms.ErrThrottled and a missing key in
// kms.ErrUnknownKEK, keeping the SDK error in the chain.
func mapSignError(op string, err error) error {
	var rerr *azcore.ResponseError
	if errors.As(err, &rerr) && rerr.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("kms/azure: %s: %w: %w", op, kms.ErrThrottled, err)
	}
	if isNotFound(err) {
		return fmt.Errorf("kms/azure: %s: %w: %w", op, kms.ErrUnknownKEK, err)
	}
	return fmt.Errorf("kms/azure: %s: %w", op, err)
}
