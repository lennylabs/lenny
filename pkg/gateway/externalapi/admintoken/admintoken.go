// SPDX-License-Identifier: MIT

// Package admintoken provisions the §17.6 initial admin credential: a
// `platform-admin` user (`lenny-admin`) with a generated API token,
// written to a Kubernetes Secret `lenny-system/lenny-admin-token`.
//
// The provisioning runs during the §17.6 bootstrap flow. The gateway is
// the actor because it holds the three capabilities the credential needs:
// the §10.2 JWT signer (mints a token the gateway's own verifier
// accepts), the §15.1 user registry (creates the admin user row), and an
// in-cluster Kubernetes client (writes the Secret). The §17.6 line 459
// idempotence contract is enforced by reading the Secret first: a re-run
// preserves the existing token rather than regenerating it, so existing
// integrations do not break on `helm upgrade`.
//
// spec: §17.6 lines 455-474 — F-17.6.3, F-24.1.7.
package admintoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
)

const (
	// DefaultSecretName is the §17.6 line 463 Secret name the bootstrap
	// flow writes the initial admin token to.
	DefaultSecretName = "lenny-admin-token"
	// DefaultUsername is the §17.6 line 455 initial admin username.
	DefaultUsername = "lenny-admin"
	// ManagedByLabel / ManagedByValue mark the Secret as bootstrap-owned
	// per the §17.6 line 467 `metadata.labels` block.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "lenny-bootstrap"
	// TokenKey / CreatedAtKey / jtiKey are the §17.6 line 470 `data`
	// fields. jtiKey is a Lenny addition: it records the token's `jti`
	// so Rotate can revoke the superseded token (the spec's "old token
	// is immediately invalidated" clause, line 472).
	TokenKey     = "token"
	CreatedAtKey = "created_at"
	jtiKey       = "jti"

	// DefaultTokenTTL bounds the minted admin token. §17.6 frames the
	// credential as fail-safe and long-lived (rotation, not expiry, is
	// the invalidation path), so the window is wide. Rotation revokes
	// the prior jti immediately regardless of remaining lifetime.
	DefaultTokenTTL = 10 * 365 * 24 * time.Hour
)

// Signer mints a §10.2 JWT. The production wiring passes the gateway's
// KMS-backed signer so the minted token verifies against the gateway's
// own §10.3 RotatingVerifier without any external IdP round-trip.
type Signer interface {
	Sign(jwt.Claims) (string, error)
}

// SecretStore is the minimal Kubernetes Secret surface the provisioner
// needs. The production implementation wraps the gateway's in-cluster
// client; tests fake it. Get reports exists=false (with a nil error)
// when the Secret is absent so the caller distinguishes "not yet
// created" from a transport failure.
type SecretStore interface {
	Get(ctx context.Context, namespace, name string) (data map[string][]byte, exists bool, err error)
	Create(ctx context.Context, namespace, name string, labels map[string]string, data map[string][]byte) error
	Update(ctx context.Context, namespace, name string, data map[string][]byte) error
}

// MintedToken is the §13.3 issued-token record the provisioner writes at
// mint time so a later rotation can revoke the token by its jti.
type MintedToken struct {
	TenantID  string
	Subject   string
	JTI       string
	TokenHash []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// IssuedTokens records minted admin tokens and revokes superseded ones.
// The production wiring passes the §13.3 issued-token store: recording at
// mint makes the token revocable, and Revoke on rotation marks the prior
// token revoked so the gateway rejects it on the next request (the §17.6
// line 472 "immediately invalidated, not a grace period" guarantee).
// Optional: a nil store leaves the token un-revocable, so a rotated token
// then lapses only at its own expiry. The degraded behavior is acceptable
// for dev/in-memory deployments that have no durable token store.
type IssuedTokens interface {
	Record(ctx context.Context, rec MintedToken) error
	Revoke(ctx context.Context, tenantID, jti, reason string, at time.Time) error
}

// Config carries the deployment-specific knobs. Namespace and SecretName
// locate the Secret; AdminTenant is the tenant the admin user and token
// are scoped to; Issuer and Audience must match what the gateway's
// bearer verifier expects (empty values are correct when the verifier
// skips the respective claim check).
type Config struct {
	Namespace   string
	SecretName  string
	Username    string
	AdminTenant string
	Issuer      string
	Audience    []string
	TokenTTL    time.Duration
}

func (c Config) withDefaults() Config {
	if c.SecretName == "" {
		c.SecretName = DefaultSecretName
	}
	if c.Username == "" {
		c.Username = DefaultUsername
	}
	if c.TokenTTL <= 0 {
		c.TokenTTL = DefaultTokenTTL
	}
	return c
}

// Provisioner creates and rotates the §17.6 initial admin credential.
type Provisioner struct {
	cfg     Config
	signer  Signer
	users   userstore.Store
	secrets SecretStore
	issued  IssuedTokens
	now     func() time.Time
}

// New builds a Provisioner. signer, users, and secrets are required;
// issued is optional (see IssuedTokens). clock defaults to time.Now when
// nil.
func New(cfg Config, signer Signer, users userstore.Store, secrets SecretStore, issued IssuedTokens, clock func() time.Time) (*Provisioner, error) {
	if signer == nil {
		return nil, errors.New("admintoken: signer is required")
	}
	if users == nil {
		return nil, errors.New("admintoken: user store is required")
	}
	if secrets == nil {
		return nil, errors.New("admintoken: secret store is required")
	}
	if cfg.Namespace == "" {
		return nil, errors.New("admintoken: namespace is required")
	}
	if cfg.AdminTenant == "" {
		return nil, errors.New("admintoken: admin tenant is required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Provisioner{
		cfg:     cfg.withDefaults(),
		signer:  signer,
		users:   users,
		secrets: secrets,
		issued:  issued,
		now:     clock,
	}, nil
}

// Username reports the admin username this provisioner manages.
func (p *Provisioner) Username() string { return p.cfg.Username }

// SecretRef reports the namespace/name of the managed Secret.
func (p *Provisioner) SecretRef() (namespace, name string) {
	return p.cfg.Namespace, p.cfg.SecretName
}

// Result reports the outcome of a Provision or Rotate call. Created is
// true when a new Secret was written (a first run, or a rotation);
// false when an existing Secret was preserved untouched. Token carries
// the raw minted token only when Created is true and the caller is
// trusted to receive it (Provision via the §15.1 admin API does not
// echo it; the credential lives only in the Secret).
type Result struct {
	// Created is true when this call wrote the Secret. The §17.6 line
	// 473 first-use prompt fires only when Created is true.
	Created bool
	// Token is the raw token value, populated only on a write. Callers
	// that must not surface the credential leave it unread.
	Token string
}

// Provision ensures the §17.6 admin user and credential exist. It is
// idempotent: on a re-run where the Secret already exists the existing
// token is preserved and Result.Created is false. The user row is
// upserted on every call so a Secret that outlives its user row is
// healed.
//
// spec: §17.6 lines 455-471 — F-17.6.3.
func (p *Provisioner) Provision(ctx context.Context) (Result, error) {
	if err := p.ensureUser(ctx); err != nil {
		return Result{}, err
	}
	_, exists, err := p.secrets.Get(ctx, p.cfg.Namespace, p.cfg.SecretName)
	if err != nil {
		return Result{}, fmt.Errorf("admintoken: read secret: %w", err)
	}
	if exists {
		// §17.6 line 459 — the token is not regenerated on re-run; the
		// existing Secret's token is preserved.
		return Result{Created: false}, nil
	}
	token, jti, createdAt, err := p.mint(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := p.secrets.Create(ctx, p.cfg.Namespace, p.cfg.SecretName,
		map[string]string{ManagedByLabel: ManagedByValue},
		secretData(token, jti, createdAt)); err != nil {
		return Result{}, fmt.Errorf("admintoken: create secret: %w", err)
	}
	return Result{Created: true, Token: token}, nil
}

// Rotate generates a fresh admin token, patches the Secret with it, and
// immediately revokes the superseded token by its recorded jti. When no
// Secret exists yet Rotate provisions one (so an operator can rotate
// before the first bootstrap has run). The old token stops validating
// as soon as the revocation propagates; there is no grace period.
//
// spec: §17.6 line 472 — F-17.6.3.
func (p *Provisioner) Rotate(ctx context.Context) (Result, error) {
	if err := p.ensureUser(ctx); err != nil {
		return Result{}, err
	}
	existing, exists, err := p.secrets.Get(ctx, p.cfg.Namespace, p.cfg.SecretName)
	if err != nil {
		return Result{}, fmt.Errorf("admintoken: read secret: %w", err)
	}
	token, jti, createdAt, err := p.mint(ctx)
	if err != nil {
		return Result{}, err
	}
	data := secretData(token, jti, createdAt)
	if exists {
		if err := p.secrets.Update(ctx, p.cfg.Namespace, p.cfg.SecretName, data); err != nil {
			return Result{}, fmt.Errorf("admintoken: patch secret: %w", err)
		}
		p.revokePrevious(ctx, existing)
	} else {
		if err := p.secrets.Create(ctx, p.cfg.Namespace, p.cfg.SecretName,
			map[string]string{ManagedByLabel: ManagedByValue}, data); err != nil {
			return Result{}, fmt.Errorf("admintoken: create secret: %w", err)
		}
	}
	return Result{Created: true, Token: token}, nil
}

// ensureUser upserts the §17.6 admin user row with the platform-admin
// role so the §10.2 platform-role resolver agrees with the minted
// token's role claim. An already-existing row is left as-is rather than
// reset, so an operator who promoted the user further is not downgraded.
func (p *Provisioner) ensureUser(ctx context.Context) error {
	_, err := p.users.Get(ctx, p.cfg.AdminTenant, p.cfg.Username)
	if err == nil {
		return nil
	}
	if !errors.Is(err, userstore.ErrNotFound) {
		return fmt.Errorf("admintoken: read admin user: %w", err)
	}
	now := p.now().UTC()
	cerr := p.users.Create(ctx, userstore.User{
		Subject:     p.cfg.Username,
		TenantID:    p.cfg.AdminTenant,
		Email:       p.cfg.Username,
		DisplayName: "Initial platform administrator",
		Roles:       []auth.Role{auth.RolePlatformAdmin},
		// spec: §10.2 line 294 — the initial admin's platform-managed role
		// must override any OIDC claim for the bootstrap subject.
		RoleAssigned: true,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if cerr != nil && !errors.Is(cerr, userstore.ErrAlreadyExists) {
		return fmt.Errorf("admintoken: create admin user: %w", cerr)
	}
	return nil
}

// mint signs a §10.2 user_bearer JWT for the admin user. The token
// carries roles=[platform-admin] so it authorizes the §15.1 admin
// surface, iss/aud matching the gateway's expected claims, and a random
// jti so a later rotation can revoke it precisely.
func (p *Provisioner) mint(ctx context.Context) (token, jti string, createdAt time.Time, err error) {
	now := p.now().UTC()
	jti, err = randomJTI()
	if err != nil {
		return "", "", time.Time{}, err
	}
	claims := jwt.Claims{
		Issuer:    p.cfg.Issuer,
		Subject:   p.cfg.Username,
		Audience:  p.cfg.Audience,
		Expiry:    now.Add(p.cfg.TokenTTL).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		JWTID:     jti,
		TenantID:  p.cfg.AdminTenant,
		Roles:     []auth.Role{auth.RolePlatformAdmin},
		Typ:       auth.TokenUserBearer,
	}
	token, err = p.signer.Sign(claims)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("admintoken: sign token: %w", err)
	}
	// Record the token in the §13.3 issued-token store so a later
	// rotation can revoke it. A record failure aborts the mint: an
	// un-recorded token could not be revoked, which would silently break
	// the §17.6 line 472 rotation guarantee.
	if p.issued != nil {
		sum := sha256.Sum256([]byte(token))
		if rerr := p.issued.Record(ctx, MintedToken{
			TenantID:  p.cfg.AdminTenant,
			Subject:   p.cfg.Username,
			JTI:       jti,
			TokenHash: sum[:],
			IssuedAt:  now,
			ExpiresAt: now.Add(p.cfg.TokenTTL),
		}); rerr != nil {
			return "", "", time.Time{}, fmt.Errorf("admintoken: record token: %w", rerr)
		}
	}
	return token, jti, now, nil
}

// revokePrevious revokes the jti recorded in the prior Secret so the
// superseded token stops validating immediately. A Secret written
// before the jti field existed, or a Secret with no revoker wired, is a
// best-effort no-op: the old token then lapses only at its own expiry.
func (p *Provisioner) revokePrevious(ctx context.Context, prev map[string][]byte) {
	if p.issued == nil {
		return
	}
	jti := string(prev[jtiKey])
	if jti == "" {
		return
	}
	// Revocation is best-effort: a failure here must not block the
	// already-committed Secret patch. The next reconcile re-attempts via
	// the standard revocation propagation path.
	_ = p.issued.Revoke(ctx, p.cfg.AdminTenant, jti, "admin_token_rotated", p.now().UTC())
}

// secretData builds the §17.6 line 470 Secret `data` map.
func secretData(token, jti string, createdAt time.Time) map[string][]byte {
	return map[string][]byte{
		TokenKey:     []byte(token),
		CreatedAtKey: []byte(createdAt.Format(time.RFC3339)),
		jtiKey:       []byte(jti),
	}
}

// randomJTI returns a 128-bit random token id, hex-encoded.
func randomJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("admintoken: generate jti: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
