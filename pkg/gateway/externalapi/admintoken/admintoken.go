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
	// is immediately invalidated" clause).
	TokenKey     = "token"
	CreatedAtKey = "created_at"
	jtiKey       = "jti"
	// prevJtiKey durably names the single predecessor token a rotation
	// supersedes, written into the Secret in the same patch that installs
	// the new jti. It is the §13.3 "named predecessor" slot the
	// leader-gated reclaimer sweep reads to durably revoke a predecessor
	// orphaned by a crash between the Secret patch and the durable revoke.
	// A single slot is overwritable, so Rotate durably revokes whatever
	// this slot currently names before overwriting it (the §13.3
	// revoke-before-overwrite rule), so no predecessor is ever named
	// nowhere. spec: §13.3 (named predecessor, revoke-before-overwrite).
	prevJtiKey = "prev_jti"

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

// IssuedTokens records minted admin tokens and durably revokes superseded
// ones with the §16.7 rotation audit coverage. The production wiring
// passes an adapter over the §13.3 issued-token store: recording at mint
// makes the token revocable, and the durable revoke marks the prior token
// revoked so the gateway rejects it on the next request (the §17.6
// "immediately invalidated, not a grace period" guarantee).
//
// The adapter owns the §16.7-owned audit vocabulary (`token.exchanged`
// with `exchange_type: admin_rotation`, `token.revoked` with
// `revocation_reason: rotation_replaced`, `propagation_mode`), so this
// package passes domain data and stays free of the audit schema.
//
// Optional: a nil store leaves the token un-revocable, so a rotated token
// then lapses only at its own expiry. The degraded behavior is acceptable
// for dev/in-memory deployments that have no durable token store.
//
// spec: §13.3 (gateway-mediated admin-credential rotation ordering and
// mandatory exchange/revoke audit, lines 587/599), §16.7 (token.exchanged
// exchange_type=admin_rotation, line 672; token.revoked rotation_replaced,
// line 673).
type IssuedTokens interface {
	// Record persists a minted token with no audit row. Provision's
	// bootstrap first mint uses it: the initial credential issuance is not
	// a token exchange, so no `token.exchanged` row is emitted.
	Record(ctx context.Context, rec MintedToken) error
	// RecordWithExchangeAudit persists a minted token and writes the §16.7
	// `token.exchanged{exchange_type: admin_rotation}` audit row in one
	// transaction. Rotate's mint step uses it. It does not revoke the
	// prior token, so the §13.3 persist-Secret-before-revoke ordering is
	// preserved.
	RecordWithExchangeAudit(ctx context.Context, rec MintedToken) error
	// DurableRevoke durably revokes jti with revocation_reason=
	// rotation_replaced, writes the §16.7 `token.revoked` audit row in the
	// same transaction, and only then pushes the jti onto the cross-replica
	// revocation cache, so a durable-write failure never leaves the cache
	// holding a revocation the authoritative store lacks. It returns nil
	// when jti names no live token (already revoked or absent), so a retry
	// is idempotent.
	DurableRevoke(ctx context.Context, tenantID, jti string, at time.Time) error
	// WithSubjectLock runs fn under a per-subject session-scoped Postgres
	// advisory lock so the whole non-atomic rotation read-modify-write
	// (Secret patch plus the separate store transactions) serializes for
	// one subject and two concurrent rotations cannot drop a successor jti
	// through the Secret's blind full-map replace. spec: §13.3 line 605.
	WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error
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
	m, err := p.mintForProvision(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := p.secrets.Create(ctx, p.cfg.Namespace, p.cfg.SecretName,
		map[string]string{ManagedByLabel: ManagedByValue},
		secretData(m.token, m.jti, "", m.createdAt)); err != nil {
		return Result{}, fmt.Errorf("admintoken: create secret: %w", err)
	}
	return Result{Created: true, Token: m.token}, nil
}

// Rotate generates a fresh admin token, patches the Secret with it, and
// durably revokes the superseded token. When no Secret exists yet Rotate
// provisions one (so an operator can rotate before the first bootstrap has
// run). The old token stops validating as soon as the revocation
// propagates; there is no grace period (§13.3 line 599, §17.6 line 507).
//
// The whole read-modify-write runs under a per-subject session-scoped
// Postgres advisory lock (§13.3 line 605) so two concurrent rotations
// cannot interleave the K8s Secret patch and the separate store
// transactions and drop a successor jti through the Secret's blind
// full-map replace. With no store wired (dev/in-memory), the lock is a
// pass-through and the durable-revoke steps are no-ops.
//
// spec: §13.3 (gateway-mediated admin-credential rotation ordering, lines
// 599/601/605), §16.7 (token.exchanged exchange_type=admin_rotation, line
// 672; token.revoked rotation_replaced, line 673), §17.6 — F-17.6.3.
func (p *Provisioner) Rotate(ctx context.Context) (Result, error) {
	if err := p.ensureUser(ctx); err != nil {
		return Result{}, err
	}
	if p.issued == nil {
		// No durable store: rotate without a lock or durable revoke.
		return p.rotateLocked(ctx)
	}
	var res Result
	err := p.issued.WithSubjectLock(ctx, p.cfg.AdminTenant, p.cfg.Username, func(ctx context.Context) error {
		var rerr error
		res, rerr = p.rotateLocked(ctx)
		return rerr
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// rotateLocked runs the §13.3 rotation sequence. The caller holds the
// per-subject advisory lock (or none, for the store-less dev path), so
// this body is the atomic read-modify-write the lock protects:
//
//  1. Read the live Secret.
//  2. Revoke-before-overwrite: durably revoke the jti the live Secret's
//     prevJtiKey already names (a prior crash's orphaned predecessor), and
//     abort before the patch when that revoke fails, so the slot keeps
//     naming the still-unrevoked predecessor for the next attempt or the
//     leader-gated sweep (§13.3 revoke-before-overwrite).
//  3. Mint and record the new token with its token.exchanged audit row
//     (no revoke yet), preserving persist-Secret-before-revoke.
//  4. Patch the Secret to {current jti, prevJti: read-time current jti}.
//  5. Durably revoke the read-time current jti with rotation_replaced only
//     after the patch succeeds.
func (p *Provisioner) rotateLocked(ctx context.Context) (Result, error) {
	existing, exists, err := p.secrets.Get(ctx, p.cfg.Namespace, p.cfg.SecretName)
	if err != nil {
		return Result{}, fmt.Errorf("admintoken: read secret: %w", err)
	}

	if exists {
		// Step 2: revoke-before-overwrite. The prevJtiKey slot is
		// overwritten by the patch below, so any unrevoked predecessor it
		// names must be durably revoked first, otherwise a sequential
		// crash-then-retry rotation would leave that predecessor named
		// nowhere and the sweep could never reach it (§13.3).
		if prevJTI := string(existing[prevJtiKey]); prevJTI != "" {
			if rerr := p.durableRevoke(ctx, prevJTI); rerr != nil {
				return Result{}, fmt.Errorf("admintoken: revoke orphaned predecessor before overwrite: %w", rerr)
			}
		}
	}

	// Step 3: mint + record + token.exchanged audit (no revoke yet).
	m, err := p.mintForRotation(ctx)
	if err != nil {
		return Result{}, err
	}

	if !exists {
		// No prior Secret: create one with an empty predecessor slot. An
		// operator may rotate before the first bootstrap has run.
		if err := p.secrets.Create(ctx, p.cfg.Namespace, p.cfg.SecretName,
			map[string]string{ManagedByLabel: ManagedByValue},
			secretData(m.token, m.jti, "", m.createdAt)); err != nil {
			return Result{}, fmt.Errorf("admintoken: create secret: %w", err)
		}
		return Result{Created: true, Token: m.token}, nil
	}

	// Step 4: patch the Secret, naming the read-time current jti as the
	// predecessor the reclaimer sweep durably revokes if the durable revoke
	// below does not commit (a crash between patch and revoke).
	prevJTI := string(existing[jtiKey])
	if err := p.secrets.Update(ctx, p.cfg.Namespace, p.cfg.SecretName,
		secretData(m.token, m.jti, prevJTI, m.createdAt)); err != nil {
		return Result{}, fmt.Errorf("admintoken: patch secret: %w", err)
	}

	// Step 5: durably revoke the read-time current jti, only after the
	// patch succeeded. The old token is durably invalidated with no grace
	// period. A failure here surfaces to the caller so the operator retries;
	// the predecessor stays named in prevJtiKey for the sweep in the
	// meantime, so the token is never left live-and-unnamed.
	if prevJTI != "" {
		if err := p.durableRevoke(ctx, prevJTI); err != nil {
			return Result{}, fmt.Errorf("admintoken: revoke superseded token after patch: %w", err)
		}
	}
	return Result{Created: true, Token: m.token}, nil
}

// durableRevoke durably revokes jti with revocation_reason=
// rotation_replaced through the audited store path, which writes the §16.7
// token.revoked row and gates the cross-replica cache push on the durable
// Postgres write. A nil store (dev/in-memory) or an empty jti is a no-op.
func (p *Provisioner) durableRevoke(ctx context.Context, jti string) error {
	if p.issued == nil || jti == "" {
		return nil
	}
	return p.issued.DurableRevoke(ctx, p.cfg.AdminTenant, jti, p.now().UTC())
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

// mintedToken carries a freshly-signed admin token and the metadata a
// later record step needs. sign builds it without recording, so the two
// record paths (non-audited Provision, audited Rotate) diverge on the
// record call only.
type mintedToken struct {
	token     string
	jti       string
	createdAt time.Time
	record    MintedToken
}

// sign signs a §10.2 user_bearer JWT for the admin user without recording
// it. The token carries roles=[platform-admin] so it authorizes the §15.1
// admin surface, iss/aud matching the gateway's expected claims, and a
// random jti so a later rotation can revoke it precisely. The caller then
// records the token through Record (Provision) or RecordWithExchangeAudit
// (Rotate).
func (p *Provisioner) sign() (mintedToken, error) {
	now := p.now().UTC()
	jti, err := randomJTI()
	if err != nil {
		return mintedToken{}, err
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
	token, err := p.signer.Sign(claims)
	if err != nil {
		return mintedToken{}, fmt.Errorf("admintoken: sign token: %w", err)
	}
	sum := sha256.Sum256([]byte(token))
	return mintedToken{
		token:     token,
		jti:       jti,
		createdAt: now,
		record: MintedToken{
			TenantID:  p.cfg.AdminTenant,
			Subject:   p.cfg.Username,
			JTI:       jti,
			TokenHash: sum[:],
			IssuedAt:  now,
			ExpiresAt: now.Add(p.cfg.TokenTTL),
		},
	}, nil
}

// mintForProvision signs and records the §17.6 bootstrap first token
// through the non-audited Record path. The bootstrap first mint is not a
// token exchange (there is no prior token to replace), so §13.3 line 587
// scopes no mandatory `token.exchanged` audit row to it; recording it
// keeps the issued-token row a later Rotate revokes precisely. A record
// failure aborts the mint: an un-recorded token could not be revoked,
// which would silently break the §17.6 rotation guarantee.
//
// spec: §13.3 line 587 (audit coverage scoped to exchanges), §17.6.
func (p *Provisioner) mintForProvision(ctx context.Context) (mintedToken, error) {
	m, err := p.sign()
	if err != nil {
		return mintedToken{}, err
	}
	if p.issued != nil {
		if rerr := p.issued.Record(ctx, m.record); rerr != nil {
			return mintedToken{}, fmt.Errorf("admintoken: record token: %w", rerr)
		}
	}
	return m, nil
}

// mintForRotation signs and records the rotated token through the audited
// path, emitting the §16.7 `token.exchanged{exchange_type: admin_rotation}`
// audit row in the same transaction as the issued-token INSERT. It does
// not revoke the prior token, so the §13.3 persist-Secret-before-revoke
// ordering is preserved: Rotate patches the Secret, then durably revokes.
//
// spec: §13.3 line 599, §16.7 line 672.
func (p *Provisioner) mintForRotation(ctx context.Context) (mintedToken, error) {
	m, err := p.sign()
	if err != nil {
		return mintedToken{}, err
	}
	if p.issued != nil {
		if rerr := p.issued.RecordWithExchangeAudit(ctx, m.record); rerr != nil {
			return mintedToken{}, fmt.Errorf("admintoken: record rotated token: %w", rerr)
		}
	}
	return m, nil
}

// PredecessorJTI reports the single predecessor jti a rotation named in the
// Secret's prevJtiKey slot, or "" when the slot is absent or empty. The
// §13.3 leader-gated reclaimer sweep reads it to durably revoke a predecessor
// orphaned by a crash between the Secret patch and the in-request durable
// revoke, without duplicating the prevJtiKey string outside this package.
//
// spec: §13.3 (named predecessor and leader-gated reclaimer, line 603).
func PredecessorJTI(data map[string][]byte) string {
	return string(data[prevJtiKey])
}

// secretData builds the §17.6 line 470 Secret `data` map. prevJTI is the
// predecessor the new token supersedes; it is empty on a first Secret
// creation (no predecessor) and carries the read-time current jti on a
// rotation, so the §13.3 reclaimer sweep can name the predecessor.
func secretData(token, jti, prevJTI string, createdAt time.Time) map[string][]byte {
	return map[string][]byte{
		TokenKey:     []byte(token),
		CreatedAtKey: []byte(createdAt.Format(time.RFC3339)),
		jtiKey:       []byte(jti),
		prevJtiKey:   []byte(prevJTI),
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
