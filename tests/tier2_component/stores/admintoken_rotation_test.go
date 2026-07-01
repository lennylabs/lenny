//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component / tier-3 contract / tier-9 security coverage for the
// gateway's in-process admin-credential rotation ordering (C7 of proposal
// 0021, F-CTL-1). It drives the real admintoken.Provisioner against the
// real Postgres-backed issued-token store and audit chain, through an
// adapter that mirrors the production cmd/lenny-gateway adminIssuedTokens.
// The pre-fix Rotate emitted no audit rows and revoked best-effort with no
// durable guarantee; every behavioral test here asserts the corrected
// outcome and fails against pre-fix code.
package stores_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// memSecrets is an in-memory admintoken.SecretStore. The Secret plane is
// Kubernetes in production; the rotation ordering under test is the
// Postgres store interaction, so the Secret is faked here while the token
// store is real. updateErr injects a Secret-patch failure.
type memSecrets struct {
	mu        sync.Mutex
	data      map[string][]byte
	exists    bool
	updates   int
	updateErr error
}

func (m *memSecrets) Get(context.Context, string, string) (map[string][]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.exists {
		return nil, false, nil
	}
	cp := make(map[string][]byte, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, true, nil
}

func (m *memSecrets) Create(_ context.Context, _, _ string, _ map[string]string, data map[string][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = data
	m.exists = true
	return nil
}

func (m *memSecrets) Update(_ context.Context, _, _ string, data map[string][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updates++
	m.data = data
	return nil
}

func (m *memSecrets) currentJTI() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.data["jti"])
}

func (m *memSecrets) prevJTI() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.data["prev_jti"])
}

// recordingCache is the cross-replica revocation cache. It records every
// pushed jti so a test can assert the cache push is gated on the durable
// Postgres write.
type recordingCache struct {
	mu      sync.Mutex
	revoked []string
}

func (c *recordingCache) Revoke(jti string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoked = append(c.revoked, jti)
}

func (c *recordingCache) has(jti string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.revoked {
		if r == jti {
			return true
		}
	}
	return false
}

// storeAdminIssued adapts the real issued-token store to
// admintoken.IssuedTokens, mirroring the production cmd/lenny-gateway
// adminIssuedTokens (which lives in package main and cannot be imported).
// It builds the §16.7 audit payloads and gates the cache push on the
// durable Postgres write, exactly as production does.
type storeAdminIssued struct {
	store *issuedtokenstore.Store
	cache *recordingCache
}

func (a storeAdminIssued) issued(rec admintoken.MintedToken) issuedtokenstore.IssuedToken {
	return issuedtokenstore.IssuedToken{
		JTI: rec.JTI, TenantID: rec.TenantID, Subject: rec.Subject,
		TokenHash: rec.TokenHash, IssuedAt: rec.IssuedAt, ExpiresAt: rec.ExpiresAt,
	}
}

func (a storeAdminIssued) Record(ctx context.Context, rec admintoken.MintedToken) error {
	return a.store.Record(ctx, a.issued(rec))
}

func (a storeAdminIssued) RecordWithExchangeAudit(ctx context.Context, rec admintoken.MintedToken) error {
	payload, _ := json.Marshal(map[string]any{
		"exchange_type": "admin_rotation",
		"caller_sub":    rec.Subject,
		"subject_sub":   rec.Subject,
		"jti":           rec.JTI,
		"policy_result": "allow",
		"timestamp":     rec.IssuedAt,
	})
	_, err := a.store.RecordWithAudit(ctx, a.issued(rec),
		string(auditcatalog.EventTokenExchanged), payload, rec.IssuedAt)
	return err
}

func (a storeAdminIssued) DurableRevoke(ctx context.Context, tenantID, jti string, at time.Time) error {
	payload, _ := json.Marshal(map[string]any{
		"revoked_jti":       jti,
		"revocation_reason": "rotation_replaced",
		"propagation_mode":  "postgres_only",
		"timestamp":         at,
	})
	_, err := a.store.RevokeWithAudit(ctx, tenantID, jti, "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), payload, at)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		return nil // idempotent no-op; no cache push
	}
	if err != nil {
		return err // durable write failed: do NOT push onto the cache
	}
	if a.cache != nil {
		a.cache.Revoke(jti)
	}
	return nil
}

func (a storeAdminIssued) WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error {
	return a.store.WithSubjectLock(ctx, tenantID, subject, fn)
}

// newAdminProvisioner wires an admintoken.Provisioner against the real
// issued-token store for tenant. It returns the provisioner, the fake
// Secret, the signer (to decode jtis from tokens), and the recording cache.
func newAdminProvisioner(t *testing.T, store *issuedtokenstore.Store, tenant string) (*admintoken.Provisioner, *memSecrets, *jwt.HMACSigner, *recordingCache) {
	t.Helper()
	signer := jwt.NewHMACSigner("k", []byte("admin-token-secret"))
	cache := &recordingCache{}
	secrets := &memSecrets{}
	p, err := admintoken.New(admintoken.Config{
		Namespace:   "lenny-system",
		AdminTenant: tenant,
	}, signer, userstore.NewMemory(), secrets,
		storeAdminIssued{store: store, cache: cache}, time.Now)
	if err != nil {
		t.Fatalf("admintoken.New: %v", err)
	}
	return p, secrets, signer, cache
}

func jtiOf(t *testing.T, signer *jwt.HMACSigner, token string) string {
	t.Helper()
	claims, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	return claims.JWTID
}

// spec: §13.3 line 599 (gateway-mediated admin-credential rotation
// ordering), §16.7 line 672/673 (token.exchanged admin_rotation,
// token.revoked rotation_replaced).
// diagnosis: a rotation must emit exactly one token.exchanged row carrying
// exchange_type=admin_rotation for the new token and one token.revoked row
// carrying revocation_reason=rotation_replaced for the superseded token. A
// failure means the gateway's in-process admin rotation does not produce
// the §16.7-mandated rotation audit rows, so an admin credential rotation
// is unauditable. Pre-fix Rotate emitted NO audit rows at all, so this
// contract assertion fails against it.
func TestAdminRotationEmitsExchangeAndRevokeAudit(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	chain := auditstore.New(pg.Router(t))

	p, secrets, signer, _ := newAdminProvisioner(t, store, tenant)

	first, err := p.Rotate(ctx) // first rotation on empty state: mint only
	if err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	firstJTI := jtiOf(t, signer, first.Token)

	second, err := p.Rotate(ctx) // supersedes firstJTI
	if err != nil {
		t.Fatalf("second Rotate: %v", err)
	}
	secondJTI := jtiOf(t, signer, second.Token)
	if secondJTI == firstJTI {
		t.Fatal("rotation reused the prior jti")
	}
	if got := secrets.prevJTI(); got != firstJTI {
		t.Errorf("Secret prev_jti = %q, want the superseded jti %q", got, firstJTI)
	}

	rows, err := chain.Rows(ctx, tenant)
	if err != nil {
		t.Fatalf("chain.Rows: %v", err)
	}
	var exchanged, revoked int
	for _, r := range rows {
		switch r.EventType {
		case string(auditcatalog.EventTokenExchanged):
			exchanged++
			var d struct {
				ExchangeType string `json:"exchange_type"`
			}
			if err := json.Unmarshal(r.Payload, &d); err != nil {
				t.Fatalf("decode token.exchanged payload: %v", err)
			}
			if d.ExchangeType != "admin_rotation" {
				t.Errorf("token.exchanged exchange_type=%q, want admin_rotation", d.ExchangeType)
			}
		case string(auditcatalog.EventTokenRevoked):
			revoked++
			var d struct {
				Reason     string `json:"revocation_reason"`
				RevokedJTI string `json:"revoked_jti"`
			}
			if err := json.Unmarshal(r.Payload, &d); err != nil {
				t.Fatalf("decode token.revoked payload: %v", err)
			}
			if d.Reason != "rotation_replaced" {
				t.Errorf("token.revoked revocation_reason=%q, want rotation_replaced", d.Reason)
			}
			if d.RevokedJTI != firstJTI {
				t.Errorf("token.revoked revoked_jti=%q, want the superseded jti %q", d.RevokedJTI, firstJTI)
			}
		}
	}
	// Two mints (two rotations) => two token.exchanged rows; one supersession
	// => one token.revoked row.
	if exchanged != 2 {
		t.Errorf("token.exchanged rows=%d, want 2", exchanged)
	}
	if revoked != 1 {
		t.Errorf("token.revoked rows=%d, want 1", revoked)
	}
}

// spec: §13.3 line 599, §17.6 line 507 ("immediately invalidated, not a
// grace period").
// diagnosis: after a rotation the superseded token must be durably revoked
// in Postgres (revoked_at set), so a §12.2 rehydration keeps rejecting it
// after any cache eviction, and the cross-replica cache is pushed only
// after the durable write. A failure means the old admin token silently
// becomes valid again after a cache eviction (the exact fail-open the
// pre-fix best-effort, non-durable revoke allowed).
func TestAdminRotationDurablyRevokesOldTokenAndGatesCache(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	p, _, signer, cache := newAdminProvisioner(t, store, tenant)

	first, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	oldJTI := jtiOf(t, signer, first.Token)

	if _, err := p.Rotate(ctx); err != nil {
		t.Fatalf("second Rotate: %v", err)
	}

	// The superseded token is durably revoked in Postgres.
	got, err := store.Get(ctx, tenant, oldJTI)
	if err != nil {
		t.Fatalf("Get old token: %v", err)
	}
	if !got.Revoked() {
		t.Fatal("superseded token not durably revoked (would validate after a cache eviction)")
	}
	if got.RevokedReason != "rotation_replaced" {
		t.Errorf("revoked_reason=%q, want rotation_replaced", got.RevokedReason)
	}
	// The cross-replica cache holds the revocation, pushed after the durable
	// write committed.
	if !cache.has(oldJTI) {
		t.Error("revocation cache does not hold the superseded jti after a successful durable revoke")
	}
}

// spec: §13.3 line 599.
// diagnosis: a Secret-patch failure must leave the old token LIVE and NOT
// durably revoked (a clean retry rather than a dead-token lockout): the
// durable revoke runs only after a successful patch. A failure means a
// patch failure locks the operator out (old token revoked, new token never
// installed in the Secret).
func TestAdminRotationSecretPatchFailureLeavesOldTokenLive(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	p, secrets, signer, cache := newAdminProvisioner(t, store, tenant)

	first, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	oldJTI := jtiOf(t, signer, first.Token)

	// Fail the next Secret patch.
	secrets.mu.Lock()
	secrets.updateErr = errors.New("secret patch failed")
	secrets.mu.Unlock()

	if _, err := p.Rotate(ctx); err == nil {
		t.Fatal("Rotate must fail when the Secret patch fails")
	}

	// The old token is still live (not durably revoked), so the operator can
	// retry with the current credential.
	got, err := store.Get(ctx, tenant, oldJTI)
	if err != nil {
		t.Fatalf("Get old token: %v", err)
	}
	if got.Revoked() {
		t.Fatal("old token revoked despite the Secret patch failing (dead-token lockout)")
	}
	if cache.has(oldJTI) {
		t.Error("cache holds a revocation the durable store did not apply (fail-open)")
	}
}

// spec: §13.3 line 605 (per-subject serialization).
// diagnosis: two concurrent rotations for the same subject must leave no
// live admin token beyond the current one. The per-subject session-scoped
// advisory lock serializes the non-atomic Secret read-modify-write so a
// concurrent rotation cannot drop the other's successor jti through the
// Secret's blind full-map replace. A failure means a concurrent rotation
// leaves a superseded token live-and-unnamed, validating indefinitely.
func TestAdminRotationConcurrentLeavesNoLiveSupersededToken(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	p, secrets, signer, _ := newAdminProvisioner(t, store, tenant)

	// Collect every jti this test ever mints; at the end exactly one (the
	// Secret's current jti) may be live and every other must be durably
	// revoked. A concurrent lost-update on the non-atomic Secret
	// read-modify-write would drop a successor jti named nowhere, leaving it
	// live and unrevoked, which this end-state check catches.
	var mintMu sync.Mutex
	var minted []string
	recordMint := func(tok string) {
		mintMu.Lock()
		minted = append(minted, jtiOf(t, signer, tok))
		mintMu.Unlock()
	}

	// Establish an initial admin token.
	first, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("initial Rotate: %v", err)
	}
	recordMint(first.Token)

	const rounds = 6
	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		results := make(chan string, 2)
		errs := make(chan error, 2)
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := p.Rotate(ctx)
				if err != nil {
					errs <- err
					return
				}
				results <- res.Token
			}()
		}
		wg.Wait()
		close(errs)
		close(results)
		for err := range errs {
			t.Fatalf("concurrent Rotate: %v", err)
		}
		for tok := range results {
			recordMint(tok)
		}
	}

	// After all rotations, only the Secret's current jti may be live.
	currentJTI := secrets.currentJTI()
	if currentJTI == "" {
		t.Fatal("Secret has no current jti after concurrent rotations")
	}
	cur, err := store.Get(ctx, tenant, currentJTI)
	if err != nil {
		t.Fatalf("Get current token: %v", err)
	}
	if cur.Revoked() {
		t.Fatal("the Secret's current token is durably revoked (concurrent rotations dropped the successor)")
	}

	// Every minted jti other than the current one must be durably revoked.
	// A live non-current token is a successor a concurrent rotation dropped
	// unnamed — the exact leak the per-subject advisory lock closes.
	for _, jti := range minted {
		if jti == currentJTI {
			continue
		}
		tok, err := store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get minted jti %q: %v", jti, err)
		}
		if !tok.Revoked() {
			t.Errorf("superseded jti %q is still live after concurrent rotations (dropped successor)", jti)
		}
	}
}

// startIssuedStore brings up a Postgres container with the production
// migrations and returns the issued-token store plus the raw handle.
func startIssuedStore(t *testing.T) (*issuedtokenstore.Store, *containers.Postgres) {
	t.Helper()
	_, pg := startStore(t)
	return issuedtokenstore.New(pg.Pool), pg
}
