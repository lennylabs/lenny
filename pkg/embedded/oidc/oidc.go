// SPDX-License-Identifier: MIT

// Package oidc is the dev-only identity provider for §17.4 Embedded
// Mode. It issues short-lived HMAC-SHA256 JWTs for a single built-in
// user and refuses to issue or accept any token whose audience claim
// is not dev.local. The signing key rotates per process.
//
// The provider exists so lenny token print can emit a bearer for the
// built-in user. Embedded Mode credentials are insecure; the
// non-suppressible production warning banner printed by lenny up
// states this. The provider must not be reachable outside localhost.
package oidc

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// Audience is the only audience the §17.4 embedded provider issues or
// accepts. A token carrying any other audience is rejected.
const Audience = "dev.local"

// Issuer is the iss claim stamped on every issued token.
const Issuer = "https://lenny.dev.local"

// BuiltInUser is the §17.4 single built-in user the provider issues
// tokens for.
const (
	BuiltInUser   = "alice@dev.local"
	BuiltInTenant = "default"
)

// DefaultTokenTTL is the lifetime of a token minted by Issue when the
// caller does not request a TTL. §17.4 specifies short-lived tokens.
const DefaultTokenTTL = 1 * time.Hour

// Provider is the embedded dev identity provider. Its signing key is
// generated when New is called. NewWithPersistedKey writes the key to
// disk so a separate process can mint tokens the running stack
// accepts; §17.4 rotates the key on every lenny up.
type Provider struct {
	signer *jwt.HMACSigner
	keyID  string
	secret []byte
}

// keyFile is the on-disk format of a persisted signing key.
type keyFile struct {
	KeyID  string `json:"keyId"`
	Secret []byte `json:"secret"`
}

// New constructs a Provider with a freshly generated signing key.
func New() (*Provider, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("embedded oidc: generate signing key: %w", err)
	}
	keyID := "embedded-" + uuid.NewString()
	return providerFromKey(keyID, secret), nil
}

// providerFromKey builds a Provider from an explicit signing key.
func providerFromKey(keyID string, secret []byte) *Provider {
	return &Provider{
		signer: jwt.NewHMACSigner(keyID, secret),
		keyID:  keyID,
		secret: append([]byte{}, secret...),
	}
}

// NewWithPersistedKey constructs a Provider whose signing key is
// persisted at path. §17.4 rotates the embedded OIDC signing key per
// lenny up: when rotate is true, or no key file exists, a fresh key is
// generated and written. When rotate is false and a key file exists,
// the stored key is loaded so a separate process (lenny token print)
// mints tokens the running stack's gateway accepts.
func NewWithPersistedKey(path string, rotate bool) (*Provider, error) {
	if !rotate {
		if p, err := loadProvider(path); err == nil {
			return p, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	p, err := New()
	if err != nil {
		return nil, err
	}
	if err := p.persist(path); err != nil {
		return nil, err
	}
	return p, nil
}

// loadProvider reads a persisted signing key and returns a Provider
// backed by it.
func loadProvider(path string) (*Provider, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kf keyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("embedded oidc: parse key file %s: %w", path, err)
	}
	if len(kf.Secret) == 0 || kf.KeyID == "" {
		return nil, fmt.Errorf("embedded oidc: key file %s is incomplete", path)
	}
	return providerFromKey(kf.KeyID, kf.Secret), nil
}

// persist writes the provider's signing key to path with owner-only
// permissions.
func (p *Provider) persist(path string) error {
	raw, err := json.Marshal(keyFile{KeyID: p.keyID, Secret: p.secret})
	if err != nil {
		return fmt.Errorf("embedded oidc: marshal key file: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("embedded oidc: write key file %s: %w", path, err)
	}
	return nil
}

// Issue mints a short-lived JWT for the built-in user. ttl bounds the
// token lifetime; a non-positive ttl uses DefaultTokenTTL. The token
// carries the platform-admin role so the built-in user can drive the
// admin API in Embedded Mode.
func (p *Provider) Issue(ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	now := time.Now()
	claims := jwt.Claims{
		Issuer:     Issuer,
		Subject:    BuiltInUser,
		Audience:   []string{Audience},
		IssuedAt:   now.Unix(),
		NotBefore:  now.Add(-time.Minute).Unix(),
		Expiry:     now.Add(ttl).Unix(),
		JWTID:      uuid.NewString(),
		TenantID:   BuiltInTenant,
		CallerType: "user",
		Roles:      []auth.Role{auth.RolePlatformAdmin},
	}
	tok, err := p.signer.Sign(claims)
	if err != nil {
		return "", fmt.Errorf("embedded oidc: sign token: %w", err)
	}
	return tok, nil
}

// Verify validates a token issued by this provider and returns its
// claims. A token whose audience is not dev.local is rejected even
// when its signature is valid: §17.4 requires the embedded provider to
// refuse any audience other than dev.local.
func (p *Provider) Verify(token string) (jwt.Claims, error) {
	claims, err := p.signer.Verify(token)
	if err != nil {
		return jwt.Claims{}, fmt.Errorf("embedded oidc: %w", err)
	}
	if !hasAudience(claims.Audience, Audience) {
		return jwt.Claims{}, fmt.Errorf("embedded oidc: token audience %v is not %q", claims.Audience, Audience)
	}
	return claims, nil
}

// KeyID returns the rotating signing key identifier.
func (p *Provider) KeyID() string { return p.keyID }

// hasAudience reports whether want is present in auds.
func hasAudience(auds []string, want string) bool {
	for _, a := range auds {
		if a == want {
			return true
		}
	}
	return false
}
