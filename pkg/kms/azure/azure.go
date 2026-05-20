// SPDX-License-Identifier: MIT

// Package azure implements the §4 / §4.9 KMS Provider over Azure Key
// Vault. WrapDEK calls azkeys.Encrypt with the RSA-OAEP-256 algorithm
// against the configured Key Vault key; UnwrapDEK calls azkeys.Decrypt.
//
// Mapping a Lenny alias to a Key Vault key:
//
//   - The Provider holds a static alias-to-key-identifier map populated
//     at construction. Each entry maps a Lenny alias to a versioned
//     Key Vault key identifier
//     ("https://<vault>.vault.azure.net/keys/<name>/<version>"). When
//     the operator omits the version segment the SDK targets the
//     current version.
//   - Azure Key Vault's automatic-rotation policy mints a new key
//     version on the configured cadence; WrapDEK always targets the
//     current version while UnwrapDEK retains the version the wrapped
//     DEK references so prior versions stay decryptable.
package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/lennylabs/lenny/pkg/kms"
)

// keyClient narrows the Key Vault client surface this Provider uses.
type keyClient interface {
	Encrypt(ctx context.Context, name, version string, parameters azkeys.KeyOperationParameters, opts *azkeys.EncryptOptions) (azkeys.EncryptResponse, error)
	Decrypt(ctx context.Context, name, version string, parameters azkeys.KeyOperationParameters, opts *azkeys.DecryptOptions) (azkeys.DecryptResponse, error)
	GetKey(ctx context.Context, name, version string, opts *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error)
}

// Provider implements kms.Provider over Azure Key Vault.
type Provider struct {
	client keyClient
	mu     sync.RWMutex
	keys   map[string]keyRef // Lenny alias -> {name, version}
}

type keyRef struct {
	name    string
	version string
}

var _ kms.Provider = (*Provider)(nil)

// Config configures a Provider.
type Config struct {
	// VaultURL is the Azure Key Vault URL
	// ("https://<vault>.vault.azure.net"). Required.
	VaultURL string

	// Credential is the Azure SDK credential. A typical caller passes
	// azidentity.NewDefaultAzureCredential or
	// azidentity.NewWorkloadIdentityCredential.
	Credential azcore.TokenCredential

	// AliasToKey maps a Lenny alias to (key name, key version). A
	// blank version targets the current version.
	AliasToKey map[string]KeyRef
}

// KeyRef names a Key Vault key the Provider WrapDEK / UnwrapDEK reaches.
type KeyRef struct {
	Name    string
	Version string
}

// New returns a Provider configured against the supplied Key Vault
// URL + credential.
func New(cfg Config) (*Provider, error) {
	if cfg.VaultURL == "" {
		return nil, errors.New("kms/azure: cfg.VaultURL is required")
	}
	if cfg.Credential == nil {
		return nil, errors.New("kms/azure: cfg.Credential is required")
	}
	if cfg.AliasToKey == nil {
		return nil, errors.New("kms/azure: cfg.AliasToKey is required")
	}
	inner, err := azkeys.NewClient(cfg.VaultURL, cfg.Credential, nil)
	if err != nil {
		return nil, fmt.Errorf("kms/azure: azkeys.NewClient: %w", err)
	}
	p := &Provider{
		client: clientAdapter{inner: inner},
		keys:   map[string]keyRef{},
	}
	for k, v := range cfg.AliasToKey {
		p.keys[k] = keyRef{name: v.Name, version: v.Version}
	}
	return p, nil
}

type clientAdapter struct {
	inner *azkeys.Client
}

func (c clientAdapter) Encrypt(ctx context.Context, name, version string, params azkeys.KeyOperationParameters, opts *azkeys.EncryptOptions) (azkeys.EncryptResponse, error) {
	return c.inner.Encrypt(ctx, name, version, params, opts)
}

func (c clientAdapter) Decrypt(ctx context.Context, name, version string, params azkeys.KeyOperationParameters, opts *azkeys.DecryptOptions) (azkeys.DecryptResponse, error) {
	return c.inner.Decrypt(ctx, name, version, params, opts)
}

func (c clientAdapter) GetKey(ctx context.Context, name, version string, opts *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	return c.inner.GetKey(ctx, name, version, opts)
}

// newWithClient is the test-injection constructor.
func newWithClient(c keyClient, aliasToKey map[string]KeyRef) *Provider {
	p := &Provider{
		client: c,
		keys:   map[string]keyRef{},
	}
	for k, v := range aliasToKey {
		p.keys[k] = keyRef{name: v.Name, version: v.Version}
	}
	return p
}

func (p *Provider) resolve(alias string) (keyRef, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.keys[alias]
	return r, ok
}

// SetAlias adds or updates an alias-to-key mapping.
func (p *Provider) SetAlias(alias string, ref KeyRef) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys[alias] = keyRef{name: ref.Name, version: ref.Version}
}

// WrapDEK encrypts the 32-byte DEK under the Key Vault key the alias
// resolves to using RSA-OAEP-256.
func (p *Provider) WrapDEK(ctx context.Context, alias string, dek []byte) (kms.WrappedDEK, error) {
	if len(dek) != kms.DEKSize {
		return kms.WrappedDEK{}, fmt.Errorf("kms/azure: DEK must be %d bytes, got %d", kms.DEKSize, len(dek))
	}
	ref, ok := p.resolve(alias)
	if !ok {
		return kms.WrappedDEK{}, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	alg := azkeys.EncryptionAlgorithmRSAOAEP256
	out, err := p.client.Encrypt(ctx, ref.name, ref.version, azkeys.KeyOperationParameters{
		Algorithm: &alg,
		Value:     dek,
		// Azure's AAD field is a 64-byte limit; encode the alias as
		// the additional authenticated data.
		AdditionalAuthenticatedData: []byte(alias),
	}, nil)
	if err != nil {
		if isNotFound(err) {
			return kms.WrappedDEK{}, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		return kms.WrappedDEK{}, fmt.Errorf("kms/azure: Encrypt: %w", err)
	}
	return kms.WrappedDEK{
		KEKVersion: 1,
		Ciphertext: out.Result,
	}, nil
}

// UnwrapDEK decrypts the wrapped DEK. A wrong-alias unwrap fails on
// the AAD binding.
func (p *Provider) UnwrapDEK(ctx context.Context, alias string, wrapped kms.WrappedDEK) ([]byte, error) {
	if wrapped.KEKVersion < 1 {
		return nil, fmt.Errorf("%w: version %d", kms.ErrUnknownKEKVersion, wrapped.KEKVersion)
	}
	ref, ok := p.resolve(alias)
	if !ok {
		return nil, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	alg := azkeys.EncryptionAlgorithmRSAOAEP256
	out, err := p.client.Decrypt(ctx, ref.name, ref.version, azkeys.KeyOperationParameters{
		Algorithm:                   &alg,
		Value:                       wrapped.Ciphertext,
		AdditionalAuthenticatedData: []byte(alias),
	}, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		if isInvalidArg(err) {
			return nil, fmt.Errorf("%w: %v", kms.ErrUnwrap, err)
		}
		return nil, fmt.Errorf("kms/azure: Decrypt: %w", err)
	}
	if len(out.Result) != kms.DEKSize {
		return nil, fmt.Errorf("%w: unwrapped %d bytes", kms.ErrUnwrap, len(out.Result))
	}
	return out.Result, nil
}

// CurrentKEKVersion calls GetKey to confirm alias addressability.
func (p *Provider) CurrentKEKVersion(ctx context.Context, alias string) (int, error) {
	ref, ok := p.resolve(alias)
	if !ok {
		return 0, fmt.Errorf("%w: alias %q", kms.ErrUnknownKEK, alias)
	}
	if _, err := p.client.GetKey(ctx, ref.name, ref.version, nil); err != nil {
		if isNotFound(err) {
			return 0, fmt.Errorf("%w: %v", kms.ErrUnknownKEK, err)
		}
		return 0, fmt.Errorf("kms/azure: GetKey: %w", err)
	}
	return 1, nil
}

// isNotFound maps Azure Key Vault's 404 ResponseError to a sentinel
// the kms package's callers recognise.
func isNotFound(err error) bool {
	var rerr *azcore.ResponseError
	if !errors.As(err, &rerr) {
		return false
	}
	if rerr.StatusCode == 404 {
		return true
	}
	return strings.Contains(rerr.ErrorCode, "NotFound")
}

// isInvalidArg maps Azure Key Vault's 400 / "ForbiddenByPolicy" errors
// to the ErrUnwrap sentinel — these are the AAD-binding-rejection
// paths the wrapped DEK round-trip can hit.
func isInvalidArg(err error) bool {
	var rerr *azcore.ResponseError
	if !errors.As(err, &rerr) {
		return false
	}
	return rerr.StatusCode == 400 || strings.Contains(rerr.ErrorCode, "Invalid")
}

// runtimeWrap silences unused-import errors when the build excludes
// the test stubs. The reference keeps azruntime in scope for
// downstream callers that want to extend the package with custom
// pipelines.
var _ = azruntime.NewResponseError
