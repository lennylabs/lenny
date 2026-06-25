// SPDX-License-Identifier: MIT

// Package devauth is the in-process dev token signer for §17.4 Embedded
// Mode. It mints short-lived HMAC-SHA256 bearers for a single built-in
// user from a persisted signing key, and the in-cluster gateway trusts
// that key as a second §10.2 verifier through its dev-mode
// --bearer-trust-hmac-key-file hook. Every minted token carries the
// dev.local audience; the gateway rejects any token whose audience claim
// is not dev.local even when it is signed by the trusted dev key, so the
// §17.4 audience-rejection control holds at the in-cluster gateway.
//
// The signer exists so `lenny token print` and `lenny session new` can
// emit a bearer the running stack's gateway accepts without a standalone
// identity-provider process. Embedded Mode credentials are insecure; the
// non-suppressible production warning banner printed by `lenny up` states
// this. The persisted key file is written in the
// jwt.LoadHMACKeyFile-readable format the gateway's
// --bearer-trust-hmac-key-file flag loads.
//
// spec: §17.4 (the CLI mints the dev bearer from a persisted dev key and
// the in-cluster gateway trusts it in dev mode through
// --bearer-trust-hmac-key-file), §10.2 (Bearer verifier).
package devauth

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

// Audience is the only audience the §17.4 dev token signer issues. The
// in-cluster gateway's dev-mode bearer-trust verifier rejects any token
// whose audience claim is not this value, so the §17.4 audience-rejection
// control is retained even though the signing key is trusted.
const Audience = "dev.local"

// Issuer is the iss claim stamped on every minted token.
const Issuer = "https://lenny.dev.local"

// BuiltInUser and BuiltInTenant identify the §17.4 single built-in user
// the dev signer mints tokens for.
const (
	BuiltInUser   = "alice@dev.local"
	BuiltInTenant = "default"
)

// DefaultTokenTTL is the lifetime of a token minted by Issue when the
// caller does not request a TTL. §17.4 specifies short-lived tokens.
const DefaultTokenTTL = 1 * time.Hour

// Signer is the in-process dev token signer. Its signing key is generated
// when New is called. NewWithPersistedKey writes the key to disk in the
// jwt.LoadHMACKeyFile-readable format so the in-cluster gateway loads the
// same key as a §10.2 verifier; §17.4 rotates the key on every `lenny up`.
type Signer struct {
	signer *jwt.HMACSigner
	keyID  string
	secret []byte
}

// keyFile is the on-disk format of a persisted signing key. The field
// names mirror jwt.LoadHMACKeyFile so the gateway's
// --bearer-trust-hmac-key-file flag reads the same file the signer wrote.
type keyFile struct {
	KeyID  string `json:"keyId"`
	Secret []byte `json:"secret"`
}

// New constructs a Signer with a freshly generated signing key.
func New() (*Signer, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("embedded devauth: generate signing key: %w", err)
	}
	keyID := "embedded-" + uuid.NewString()
	return signerFromKey(keyID, secret), nil
}

// signerFromKey builds a Signer from an explicit signing key.
func signerFromKey(keyID string, secret []byte) *Signer {
	return &Signer{
		signer: jwt.NewHMACSigner(keyID, secret),
		keyID:  keyID,
		secret: append([]byte{}, secret...),
	}
}

// NewWithPersistedKey constructs a Signer whose signing key is persisted
// at path. §17.4 rotates the embedded dev signing key per `lenny up`:
// when rotate is true, or no key file exists, a fresh key is generated and
// written. When rotate is false and a key file exists, the stored key is
// loaded so a separate process (`lenny token print`, `lenny session new`)
// mints tokens the running stack's gateway accepts.
func NewWithPersistedKey(path string, rotate bool) (*Signer, error) {
	if !rotate {
		if s, err := loadSigner(path); err == nil {
			return s, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	s, err := New()
	if err != nil {
		return nil, err
	}
	if err := s.persist(path); err != nil {
		return nil, err
	}
	return s, nil
}

// loadSigner reads a persisted signing key and returns a Signer backed by
// it.
func loadSigner(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kf keyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("embedded devauth: parse key file %s: %w", path, err)
	}
	if len(kf.Secret) == 0 || kf.KeyID == "" {
		return nil, fmt.Errorf("embedded devauth: key file %s is incomplete", path)
	}
	return signerFromKey(kf.KeyID, kf.Secret), nil
}

// persist writes the signer's signing key to path with owner-only
// permissions, in the jwt.LoadHMACKeyFile-readable format.
func (s *Signer) persist(path string) error {
	raw, err := json.Marshal(keyFile{KeyID: s.keyID, Secret: s.secret})
	if err != nil {
		return fmt.Errorf("embedded devauth: marshal key file: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("embedded devauth: write key file %s: %w", path, err)
	}
	return nil
}

// Issue mints a short-lived JWT for the built-in user. ttl bounds the
// token lifetime; a non-positive ttl uses DefaultTokenTTL. The token
// carries the platform-admin role so the built-in user can drive the
// admin API in Embedded Mode, and the dev.local audience so it verifies
// at the in-cluster gateway's dev-mode bearer-trust verifier.
func (s *Signer) Issue(ttl time.Duration) (string, error) {
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
	tok, err := s.signer.Sign(claims)
	if err != nil {
		return "", fmt.Errorf("embedded devauth: sign token: %w", err)
	}
	return tok, nil
}

// Verify validates a token minted by this signer and returns its claims.
// A token whose audience is not dev.local is rejected even when its
// signature is valid, mirroring the in-cluster gateway's dev-mode
// audience pin: §17.4 requires the dev token path to refuse any audience
// other than dev.local.
func (s *Signer) Verify(token string) (jwt.Claims, error) {
	claims, err := s.signer.Verify(token)
	if err != nil {
		return jwt.Claims{}, fmt.Errorf("embedded devauth: %w", err)
	}
	if !hasAudience(claims.Audience, Audience) {
		return jwt.Claims{}, fmt.Errorf("embedded devauth: token audience %v is not %q", claims.Audience, Audience)
	}
	return claims, nil
}

// KeyID returns the rotating signing key identifier.
func (s *Signer) KeyID() string { return s.keyID }

// hasAudience reports whether want is present in auds.
func hasAudience(auds []string, want string) bool {
	for _, a := range auds {
		if a == want {
			return true
		}
	}
	return false
}
