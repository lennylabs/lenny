// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"

	"github.com/lennylabs/lenny/pkg/kms"
)

// throttleCodes are the AWS KMS error codes that mean a request-rate
// quota refused the call.
var throttleCodes = map[string]bool{
	"ThrottlingException":    true,
	"LimitExceededException": true,
}

// singleAttempt disables the SDK retry for one call, so a throttled Sign
// returns kms.ErrThrottled after one provider request instead of after
// the SDK's backoff retries consume the caller's signing timeout.
//
// spec: §10.2 (Authentication): throttling is backpressure.
func singleAttempt(o *awskms.Options) { o.RetryMaxAttempts = 1 }

// Sign signs the 32-byte digest with ECDSA_SHA_256 under the key the
// alias resolves to and returns the 64-byte r||s signature. AWS KMS
// returns a DER signature, which is converted with left-padding.
//
// spec: §10.2 (Authentication) "Production".
func (p *Provider) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	if err := kms.CheckDigest(digest); err != nil {
		return nil, err
	}
	keyID, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.Sign(ctx, &awskms.SignInput{
		KeyId:            awssdk.String(keyID),
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256,
	}, singleAttempt)
	if err != nil {
		return nil, mapSignError("Sign", err)
	}
	return kms.SignatureFromASN1(out.Signature)
}

// PublicKey returns the P-256 public key of the key the alias resolves
// to. A key whose spec is not ECC_NIST_P256 fails with kms.ErrNotP256.
func (p *Provider) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	keyID, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: awssdk.String(keyID)})
	if err != nil {
		return nil, mapSignError("GetPublicKey", err)
	}
	if out.KeySpec != types.KeySpecEccNistP256 {
		return nil, fmt.Errorf("%w: alias %q has key spec %q", kms.ErrNotP256, alias, out.KeySpec)
	}
	return kms.PublicKeyFromPKIX(out.PublicKey)
}

// mapSignError wraps a throttle in kms.ErrThrottled and a missing key in
// kms.ErrUnknownKEK, keeping the SDK error in the chain.
func mapSignError(op string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && throttleCodes[apiErr.ErrorCode()] {
		return fmt.Errorf("kms/aws: %s: %w: %w", op, kms.ErrThrottled, err)
	}
	var notFound *types.NotFoundException
	if errors.As(err, &notFound) {
		return fmt.Errorf("kms/aws: %s: %w: %w", op, kms.ErrUnknownKEK, err)
	}
	return fmt.Errorf("kms/aws: %s: %w", op, err)
}
