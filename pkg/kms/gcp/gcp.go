// SPDX-License-Identifier: MIT

// Package gcp implements the §4 / §4.9 KMS Provider over GCP Cloud
// KMS. It satisfies the pkg/kms.Provider interface against Cloud KMS
// CryptoKeyVersions: WrapDEK calls cloudkms.Encrypt against the
// configured CryptoKey, UnwrapDEK calls cloudkms.Decrypt.
//
// Mapping a Lenny alias to a Cloud KMS resource:
//
//   - The Provider holds a static alias-to-resource map populated at
//     construction. Each entry maps a Lenny alias to a full Cloud
//     KMS CryptoKey resource name
//     ("projects/<project>/locations/<region>/keyRings/<ring>/cryptoKeys/<key>").
//   - Cloud KMS automatic rotation produces new CryptoKeyVersions on
//     the configured rotation cadence; the WrapDEK call always
//     targets the CryptoKey (which resolves to the current primary
//     version), while UnwrapDEK passes the recorded resource name
//     so prior versions remain decryptable.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	cloudkms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/kms"
)

// keyClient narrows the Cloud KMS client surface this Provider uses.
type keyClient interface {
	Encrypt(ctx context.Context, req *kmspb.EncryptRequest, opts ...option.ClientOption) (*kmspb.EncryptResponse, error)
	Decrypt(ctx context.Context, req *kmspb.DecryptRequest, opts ...option.ClientOption) (*kmspb.DecryptResponse, error)
	GetCryptoKey(ctx context.Context, req *kmspb.GetCryptoKeyRequest, opts ...option.ClientOption) (*kmspb.CryptoKey, error)
}

// Provider implements kms.Provider over GCP Cloud KMS.
type Provider struct {
	client keyClient
	mu     sync.RWMutex
	keys   map[string]string // Lenny alias -> CryptoKey resource name
}

var _ kms.Provider = (*Provider)(nil)

// Config configures a Provider.
type Config struct {
	// AliasToKeyName maps a Lenny alias to a fully-qualified
	// CryptoKey resource name. Required.
	AliasToKeyName map[string]string

	// ClientOptions are passed verbatim to cloudkms.NewKeyManagementClient.
	// A typical caller sets option.WithCredentialsFile or
	// option.WithUserAgent here.
	ClientOptions []option.ClientOption
}

// clientAdapter wraps *cloudkms.KeyManagementClient to satisfy the
// internal keyClient interface (its methods take option.ClientOption
// for per-call overrides which the official client does not, so the
// adapter discards them).
type clientAdapter struct {
	inner *cloudkms.KeyManagementClient
}

func (c *clientAdapter) Encrypt(ctx context.Context, req *kmspb.EncryptRequest, _ ...option.ClientOption) (*kmspb.EncryptResponse, error) {
	return c.inner.Encrypt(ctx, req)
}

func (c *clientAdapter) Decrypt(ctx context.Context, req *kmspb.DecryptRequest, _ ...option.ClientOption) (*kmspb.DecryptResponse, error) {
	return c.inner.Decrypt(ctx, req)
}

func (c *clientAdapter) GetCryptoKey(ctx context.Context, req *kmspb.GetCryptoKeyRequest, _ ...option.ClientOption) (*kmspb.CryptoKey, error) {
	return c.inner.GetCryptoKey(ctx, req)
}

// New returns a Provider configured against ctx-resolved Cloud KMS
// credentials and the supplied alias-to-key mapping.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.AliasToKeyName == nil {
		return nil, errors.New("kms/gcp: cfg.AliasToKeyName is required")
	}
	inner, err := cloudkms.NewKeyManagementClient(ctx, cfg.ClientOptions...)
	if err != nil {
		return nil, fmt.Errorf("kms/gcp: cloudkms.NewKeyManagementClient: %w", err)
	}
	return newWithClient(&clientAdapter{inner: inner}, cfg.AliasToKeyName), nil
}

// newWithClient is the test-injection constructor.
func newWithClient(c keyClient, aliasToKeyName map[string]string) *Provider {
	p := &Provider{
		client: c,
		keys:   map[string]string{},
	}
	for k, v := range aliasToKeyName {
		p.keys[k] = v
	}
	return p
}

func (p *Provider) resolve(alias string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.keys[alias]
	return v, ok
}

// SetAlias adds or updates an alias-to-key mapping.
func (p *Provider) SetAlias(alias, keyName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys[alias] = keyName
}

// WrapDEK encrypts the 32-byte DEK under the CryptoKey the alias
// resolves to.
func (p *Provider) WrapDEK(ctx context.Context, alias string, dek []byte) (kms.WrappedDEK, error) {
	if len(dek) != kms.DEKSize {
		return kms.WrappedDEK{}, fmt.Errorf("kms/gcp: DEK must be %d bytes, got %d", kms.DEKSize, len(dek))
	}
	keyName, ok := p.resolve(alias)
	if !ok {
		return kms.WrappedDEK{}, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:                        keyName,
		Plaintext:                   dek,
		AdditionalAuthenticatedData: []byte("lenny.alias=" + alias),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return kms.WrappedDEK{}, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		return kms.WrappedDEK{}, fmt.Errorf("kms/gcp: Encrypt: %w", err)
	}
	return kms.WrappedDEK{
		KEKVersion: 1,
		Ciphertext: out.Ciphertext,
	}, nil
}

// UnwrapDEK decrypts the wrapped DEK. The AAD binding rejects a
// wrapped DEK under a different alias.
func (p *Provider) UnwrapDEK(ctx context.Context, alias string, wrapped kms.WrappedDEK) ([]byte, error) {
	if wrapped.KEKVersion < 1 {
		return nil, fmt.Errorf("%w: version %d", kms.ErrUnknownKEKVersion, wrapped.KEKVersion)
	}
	keyName, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	out, err := p.client.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:                        keyName,
		Ciphertext:                  wrapped.Ciphertext,
		AdditionalAuthenticatedData: []byte("lenny.alias=" + alias),
	})
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound:
			return nil, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		case codes.InvalidArgument, codes.FailedPrecondition:
			return nil, fmt.Errorf("%w: %v", kms.ErrUnwrap, err)
		}
		return nil, fmt.Errorf("kms/gcp: Decrypt: %w", err)
	}
	if len(out.Plaintext) != kms.DEKSize {
		return nil, fmt.Errorf("%w: unwrapped %d bytes", kms.ErrUnwrap, len(out.Plaintext))
	}
	return out.Plaintext, nil
}

// CurrentKEKVersion calls GetCryptoKey to confirm the alias is still
// addressable. Cloud KMS rotates CryptoKeyVersions under the same
// CryptoKey resource name, so the version is logically 1 from the
// caller's perspective; the §4.9.1 re-encryption job uses this only
// to fail fast on a deleted CryptoKey.
func (p *Provider) CurrentKEKVersion(ctx context.Context, alias string) (int, error) {
	keyName, ok := p.resolve(alias)
	if !ok {
		return 0, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	if _, err := p.client.GetCryptoKey(ctx, &kmspb.GetCryptoKeyRequest{Name: keyName}); err != nil {
		if status.Code(err) == codes.NotFound {
			return 0, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		return 0, fmt.Errorf("kms/gcp: GetCryptoKey: %w", err)
	}
	return 1, nil
}
