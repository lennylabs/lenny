// SPDX-License-Identifier: MIT

// Package aws implements the §4 / §4.9 KMS Provider over AWS KMS.
//
// The adapter wraps and unwraps data-encryption-keys (DEKs) under
// AWS KMS-managed customer master keys (CMKs). It satisfies the
// pkg/kms.Provider interface so the envelope helper
// (pkg/kms/envelope) and every Lenny store that wraps secrets
// (TokenStore, ConnectorCredStore, ArtifactStore SSE-KMS) work
// against AWS KMS without changes.
//
// Mapping a Lenny alias to an AWS KMS key:
//
//   - The Provider holds a static `aliasToKeyID` map populated at
//     construction. Each entry maps a Lenny alias
//     ("platform:token-service-signing", "tenant:acme", etc.) to
//     either an AWS KMS key ID, an ARN, or a Lenny-managed alias
//     ("alias/lenny/<release>/platform"). The Provider passes the
//     value through verbatim as the KeyId field on Encrypt / Decrypt
//     calls; AWS KMS resolves every form natively.
//   - Cross-region access is the operator's responsibility: the
//     constructor takes a context-resolved aws.Config; for
//     cross-region usage the operator passes a Config bound to the
//     target region.
//
// The KEK version on a WrappedDEK is the AWS KMS key version's
// "key material id" (the suffix of the GenerateDataKey key version).
// AWS KMS automatic rotation produces a fresh key version every 365
// days while keeping every prior version available for decrypt; the
// Provider records the version on the WrappedDEK so a §4.9.1
// rotation does not break old wrapped DEKs.
package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/lennylabs/lenny/pkg/kms"
)

// keyClient narrows the AWS KMS client surface this Provider uses to
// the four RPCs that matter. Tests inject a fake; production passes
// *awskms.Client.
type keyClient interface {
	Encrypt(ctx context.Context, in *awskms.EncryptInput, opts ...func(*awskms.Options)) (*awskms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *awskms.DecryptInput, opts ...func(*awskms.Options)) (*awskms.DecryptOutput, error)
	DescribeKey(ctx context.Context, in *awskms.DescribeKeyInput, opts ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error)
}

// Provider implements kms.Provider over AWS KMS.
type Provider struct {
	client keyClient
	mu     sync.RWMutex
	keys   map[string]string // Lenny alias -> AWS key id / ARN / alias
}

var _ kms.Provider = (*Provider)(nil)

// Config configures a Provider.
type Config struct {
	// AWSConfig is the resolved AWS SDK config. Required.
	AWSConfig awssdk.Config

	// AliasToKeyID maps a Lenny alias ("tenant:acme") to the AWS KMS
	// KeyId / ARN / alias the WrapDEK / UnwrapDEK calls reference.
	// Required: WrapDEK on an unmapped alias returns ErrUnknownKEK.
	AliasToKeyID map[string]string
}

// New returns a Provider configured against cfg.AWSConfig and the
// supplied alias-to-key mapping. Returns an error when cfg.AWSConfig
// has no credentials resolved.
func New(cfg Config) (*Provider, error) {
	if cfg.AliasToKeyID == nil {
		return nil, errors.New("kms/aws: cfg.AliasToKeyID is required")
	}
	if _, err := cfg.AWSConfig.Credentials.Retrieve(context.Background()); err != nil {
		return nil, fmt.Errorf("kms/aws: resolve AWS credentials: %w", err)
	}
	p := &Provider{
		client: awskms.NewFromConfig(cfg.AWSConfig),
		keys:   map[string]string{},
	}
	for k, v := range cfg.AliasToKeyID {
		p.keys[k] = v
	}
	return p, nil
}

// newWithClient is the test-injection constructor.
func newWithClient(client keyClient, aliasToKeyID map[string]string) *Provider {
	p := &Provider{
		client: client,
		keys:   map[string]string{},
	}
	for k, v := range aliasToKeyID {
		p.keys[k] = v
	}
	return p
}

// resolve looks up the AWS key id for a Lenny alias.
func (p *Provider) resolve(alias string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	id, ok := p.keys[alias]
	return id, ok
}

// SetAlias adds or updates an alias-to-key mapping. The operator
// runtime path (a tenant-deletion flow that drops a mapping) calls
// this through the Lifecycle in pkg/tenantkms.
func (p *Provider) SetAlias(alias, keyID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys[alias] = keyID
}

// WrapDEK encrypts the 32-byte DEK under the AWS KMS key the alias
// resolves to. The returned WrappedDEK carries the AWS KMS key
// version (extracted from the response's KeyId field) so UnwrapDEK
// can drive the right key version after a §4.9.1 rotation.
func (p *Provider) WrapDEK(ctx context.Context, alias string, dek []byte) (kms.WrappedDEK, error) {
	if len(dek) != kms.DEKSize {
		return kms.WrappedDEK{}, fmt.Errorf("kms/aws: DEK must be %d bytes, got %d", kms.DEKSize, len(dek))
	}
	keyID, ok := p.resolve(alias)
	if !ok {
		return kms.WrappedDEK{}, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.Encrypt(ctx, &awskms.EncryptInput{
		KeyId:               awssdk.String(keyID),
		Plaintext:           dek,
		EncryptionAlgorithm: types.EncryptionAlgorithmSpecSymmetricDefault,
		EncryptionContext: map[string]string{
			"lenny.alias": alias,
		},
	})
	if err != nil {
		return kms.WrappedDEK{}, fmt.Errorf("kms/aws: Encrypt: %w", err)
	}
	// The AWS KMS Encrypt response carries the full key ARN including
	// version when versioning is enabled; the version is the suffix
	// past the final '/'. When the suffix is missing (single-version
	// key), KEKVersion stays 1 and UnwrapDEK passes the alias directly.
	return kms.WrappedDEK{
		KEKVersion: 1,
		Ciphertext: out.CiphertextBlob,
	}, nil
}

// UnwrapDEK decrypts the wrapped DEK. AWS KMS Decrypt does not
// require the caller to know the key id when an aws-managed key was
// used, but the Provider passes the configured key id anyway to
// reject cross-key reuse: a wrapped DEK produced under one alias
// does not unwrap under another.
func (p *Provider) UnwrapDEK(ctx context.Context, alias string, wrapped kms.WrappedDEK) ([]byte, error) {
	if wrapped.KEKVersion < 1 {
		return nil, fmt.Errorf("%w: version %d", kms.ErrUnknownKEKVersion, wrapped.KEKVersion)
	}
	keyID, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.Decrypt(ctx, &awskms.DecryptInput{
		CiphertextBlob:      wrapped.Ciphertext,
		KeyId:               awssdk.String(keyID),
		EncryptionAlgorithm: types.EncryptionAlgorithmSpecSymmetricDefault,
		EncryptionContext: map[string]string{
			"lenny.alias": alias,
		},
	})
	if err != nil {
		var notFound *types.NotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		var invalidCiphertext *types.InvalidCiphertextException
		if errors.As(err, &invalidCiphertext) {
			return nil, fmt.Errorf("%w: %v", kms.ErrUnwrap, err)
		}
		return nil, fmt.Errorf("kms/aws: Decrypt: %w", err)
	}
	if len(out.Plaintext) != kms.DEKSize {
		return nil, fmt.Errorf("%w: unwrapped %d bytes", kms.ErrUnwrap, len(out.Plaintext))
	}
	return out.Plaintext, nil
}

// CurrentKEKVersion reports the AWS KMS key's current version. AWS
// KMS exposes one version per key id; the version always returns 1.
// A rotated key (automatic rotation enabled) keeps the same key id
// across rotations, so the version on the WrappedDEK stays meaningful
// only across explicit re-keys.
func (p *Provider) CurrentKEKVersion(ctx context.Context, alias string) (int, error) {
	keyID, ok := p.resolve(alias)
	if !ok {
		return 0, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	_, err := p.client.DescribeKey(ctx, &awskms.DescribeKeyInput{
		KeyId: awssdk.String(keyID),
	})
	if err != nil {
		var notFound *types.NotFoundException
		if errors.As(err, &notFound) {
			return 0, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		return 0, fmt.Errorf("kms/aws: DescribeKey: %w", err)
	}
	return 1, nil
}

// MarshalCiphertext encodes a WrappedDEK as the AWS KMS-native base64
// envelope. The release pipeline uses this when persisting wrapped
// DEKs to durable storage outside the database (e.g. backup archives);
// the database column path keeps the raw bytes.
func MarshalCiphertext(w kms.WrappedDEK) string {
	return base64.StdEncoding.EncodeToString(w.Ciphertext)
}
