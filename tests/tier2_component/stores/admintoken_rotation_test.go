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
	admintokenreclaimer "github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken/reclaimer"
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
		// spec: §16.7 line 672 — mirrors the Token Service emitter's
		// policy_result="accepted" for a successful exchange.
		"policy_result": "accepted",
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
// exchange_type=admin_rotation and policy_result=accepted for the new token
// and one token.revoked row carrying revocation_reason=rotation_replaced for
// the superseded token. A failure means the gateway's in-process admin
// rotation does not produce the §16.7-mandated rotation audit rows, or emits
// a policy_result outside the §16.7 enum, so an admin credential rotation is
// unauditable or mis-classified. Pre-fix Rotate emitted NO audit rows at all, so this
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
				PolicyResult string `json:"policy_result"`
			}
			if err := json.Unmarshal(r.Payload, &d); err != nil {
				t.Fatalf("decode token.exchanged payload: %v", err)
			}
			if d.ExchangeType != "admin_rotation" {
				t.Errorf("token.exchanged exchange_type=%q, want admin_rotation", d.ExchangeType)
			}
			// spec: §16.7 line 672 — policy_result is (accepted |
			// rejected:<reason>); a successful admin rotation mirrors the
			// Token Service emitter and carries "accepted", never the
			// out-of-enum "allow" the pre-fix adapter wrote.
			if d.PolicyResult != "accepted" {
				t.Errorf("token.exchanged policy_result=%q, want accepted", d.PolicyResult)
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

// crashingIssued wraps storeAdminIssued and fails the DurableRevoke of a single
// targeted jti, simulating a process crash (or hard revoke failure) after the
// Secret patch commits but before the in-request durable revoke of that jti
// commits. Targeting a single jti rather than every revoke lets the crash be
// placed precisely on the post-patch revoke while leaving the earlier
// revoke-before-overwrite step (which targets a different, already-handled jti)
// free to commit — the sequence the two-crash-then-retry test exercises. With
// crashJTI empty, every revoke passes through to the real store.
type crashingIssued struct {
	inner    storeAdminIssued
	mu       sync.Mutex
	crashJTI string
}

func (c *crashingIssued) Record(ctx context.Context, rec admintoken.MintedToken) error {
	return c.inner.Record(ctx, rec)
}

func (c *crashingIssued) RecordWithExchangeAudit(ctx context.Context, rec admintoken.MintedToken) error {
	return c.inner.RecordWithExchangeAudit(ctx, rec)
}

func (c *crashingIssued) DurableRevoke(ctx context.Context, tenantID, jti string, at time.Time) error {
	c.mu.Lock()
	crash := c.crashJTI != "" && c.crashJTI == jti
	c.mu.Unlock()
	if crash {
		return errors.New("simulated crash between Secret patch and durable revoke")
	}
	return c.inner.DurableRevoke(ctx, tenantID, jti, at)
}

func (c *crashingIssued) WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error {
	return c.inner.WithSubjectLock(ctx, tenantID, subject, fn)
}

// crashOn arms the wrapper to fail the durable revoke of exactly jti (the
// post-patch revoke of the read-time current token). Passing "" disarms it.
func (c *crashingIssued) crashOn(jti string) {
	c.mu.Lock()
	c.crashJTI = jti
	c.mu.Unlock()
}

// newReclaimer wires the production admintoken/reclaimer.Reclaimer over the
// same fake Secret the provisioner patches and the same real store adapter the
// in-handler revoke uses, so the sweep exercises the real durable-revoke +
// audit + idempotency path against real Postgres. A zero interval falls back
// to the default; the tests call Sweep directly rather than Run.
func newReclaimer(t *testing.T, secrets admintokenreclaimer.SecretReader, revoker admintokenreclaimer.Revoker, tenant string) *admintokenreclaimer.Reclaimer {
	t.Helper()
	r, err := admintokenreclaimer.New(admintokenreclaimer.Config{
		Namespace:  "lenny-system",
		SecretName: admintoken.DefaultSecretName,
		Tenant:     tenant,
	}, secrets, revoker, time.Now)
	if err != nil {
		t.Fatalf("reclaimer.New: %v", err)
	}
	return r
}

// spec: §13.3 lines 601-603 (named predecessor and leader-gated reclaimer),
// §16.7 line 673 (token.revoked rotation_replaced), §17.6.
// diagnosis: a crash after the Secret patch but before the in-request durable
// revoke leaves the prior admin token live and named in prev_jti. The
// leader-gated reclaimer sweep MUST durably revoke that named predecessor on
// its next pass, so the superseded credential stops validating within the
// sweep interval rather than validating indefinitely (§13.3 line 601 crash
// window). A failure means the crash-recovery surface does not close the
// window and the orphaned admin token validates until its ~10-year TTL.
func TestReclaimerRevokesOrphanedPredecessorAfterCrash(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	signer := jwt.NewHMACSigner("k", []byte("admin-token-secret"))
	cache := &recordingCache{}
	secrets := &memSecrets{}
	crash := &crashingIssued{inner: storeAdminIssued{store: store, cache: cache}}
	p, err := admintoken.New(admintoken.Config{Namespace: "lenny-system", SecretName: admintoken.DefaultSecretName, AdminTenant: tenant},
		signer, userstore.NewMemory(), secrets, crash, time.Now)
	if err != nil {
		t.Fatalf("admintoken.New: %v", err)
	}

	// Establish an admin token (first rotation on empty state: mint only).
	first, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	orphanJTI := jtiOf(t, signer, first.Token)

	// A second rotation crashes after the Secret patch but before the
	// in-request durable revoke of orphanJTI commits: the Secret is patched
	// (naming orphanJTI in prev_jti) and Rotate returns an error, leaving
	// orphanJTI live-and-named.
	crash.crashOn(orphanJTI)
	if _, err := p.Rotate(ctx); err == nil {
		t.Fatal("Rotate must surface the crash on the post-patch revoke")
	}
	if got := secrets.prevJTI(); got != orphanJTI {
		t.Fatalf("prev_jti = %q, want the orphaned predecessor %q named after the crash", got, orphanJTI)
	}
	// The predecessor is still live in Postgres (the crash window is open).
	got, err := store.Get(ctx, tenant, orphanJTI)
	if err != nil {
		t.Fatalf("Get orphan: %v", err)
	}
	if got.Revoked() {
		t.Fatal("precondition: orphan must be live after the crash (the sweep has not run yet)")
	}

	// Run the leader-gated reclaimer sweep once. It reads prev_jti and durably
	// revokes the orphan through a fresh, non-crashing store adapter (the sweep
	// runs on a healthy replica after the crash, so its durable revoke commits).
	recl := newReclaimer(t, secrets, storeAdminIssued{store: store, cache: cache}, tenant)
	reclaimed, err := recl.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !reclaimed {
		t.Fatal("Sweep did not reclaim the named predecessor")
	}

	// The orphan is now durably revoked with rotation_replaced.
	after, err := store.Get(ctx, tenant, orphanJTI)
	if err != nil {
		t.Fatalf("Get orphan after sweep: %v", err)
	}
	if !after.Revoked() {
		t.Fatal("reclaimer sweep did not durably revoke the orphaned predecessor (crash window stays open)")
	}
	if after.RevokedReason != "rotation_replaced" {
		t.Errorf("revoked_reason=%q, want rotation_replaced", after.RevokedReason)
	}
	if !cache.has(orphanJTI) {
		t.Error("reclaimer sweep did not push the orphan onto the cross-replica cache after the durable revoke")
	}

	// The sweep is idempotent: a second pass over the same still-named
	// predecessor is a no-op durable revoke (already revoked), not a
	// double-emit or an error.
	if _, err := recl.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep must be idempotent, got: %v", err)
	}
}

// spec: §13.3 line 603 (the sweep targets only the single named predecessor).
// diagnosis: the reclaimer sweep must NOT revoke the about-to-be-installed
// successor when it fires mid-rotation (the new token is recorded but the
// Secret is not yet patched, so the Secret's prev_jti still names the prior
// predecessor, not the successor). A failure means a sweep firing during a
// rotation revokes the successor before it is installed, handing the operator
// a dead-on-arrival token and locking them out — the exact lockout the
// persist-Secret-before-revoke ordering exists to prevent.
func TestReclaimerMidRotationDoesNotRevokeSuccessor(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	p, secrets, signer, cache := newAdminProvisioner(t, store, tenant)

	// Establish an admin token so the Secret exists with a current jti and an
	// empty prev_jti slot.
	first, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	installedJTI := jtiOf(t, signer, first.Token)

	// Simulate mid-rotation: the successor token is recorded in Postgres
	// (RecordWithExchangeAudit) but the Secret is NOT yet patched, so its
	// prev_jti is still empty. The sweep reads the Secret, sees no named
	// predecessor, and does nothing — it never reaches the recorded-but-not-
	// installed successor.
	successorJTI := "successor-recorded-not-installed"
	now := time.Now().UTC()
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: successorJTI, TenantID: tenant, Subject: admintoken.DefaultUsername,
		TokenHash: []byte("successor-hash"), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record successor: %v", err)
	}

	recl := newReclaimer(t, secrets, storeAdminIssued{store: store, cache: cache}, tenant)
	reclaimed, err := recl.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if reclaimed {
		t.Fatal("Sweep reclaimed something when prev_jti is empty (mid-rotation successor must be untouched)")
	}

	// The recorded-but-not-installed successor is still live.
	succ, err := store.Get(ctx, tenant, successorJTI)
	if err != nil {
		t.Fatalf("Get successor: %v", err)
	}
	if succ.Revoked() {
		t.Fatal("reclaimer sweep revoked the about-to-be-installed successor (dead-on-arrival lockout)")
	}
	// The currently installed token is also untouched.
	cur, err := store.Get(ctx, tenant, installedJTI)
	if err != nil {
		t.Fatalf("Get installed: %v", err)
	}
	if cur.Revoked() {
		t.Fatal("reclaimer sweep revoked the currently installed admin token")
	}
}

// spec: §13.3 line 603 (the sweep never revokes a flow-2 self-rotated token
// for the same platform-admin subject).
// diagnosis: the lenny-admin subject is a platform-admin eligible for the
// general /v1/oauth/token self-rotation grant, which mints a live token for
// the same subject WITHOUT patching the Secret. The reclaimer sweep, which
// targets only the single jti the Secret names in prev_jti, MUST leave that
// self-rotated token live. A failure means the sweep revokes a legitimately
// self-rotated admin token within one sweep interval — a whole-subject sweep's
// exact defect, which the named-predecessor design avoids.
func TestReclaimerLeavesFlow2SelfRotatedTokenAlive(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	p, secrets, _, cache := newAdminProvisioner(t, store, tenant)

	// Two bootstrap-Secret rotations: the first mints, the second supersedes it
	// and names the first as prev_jti, then the in-handler revoke durably
	// revokes the first. After this, prev_jti names the (already revoked) first
	// jti and the Secret's current jti is the second.
	if _, err := p.Rotate(ctx); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	if _, err := p.Rotate(ctx); err != nil {
		t.Fatalf("second Rotate: %v", err)
	}

	// A flow-2 general /v1/oauth/token self-rotation mints a NEW live token for
	// the same lenny-admin subject via RecordWithRotationAudit, without patching
	// the Secret. Its jti is therefore never written into prev_jti.
	selfRotatedJTI := "flow2-self-rotated-live"
	now := time.Now().UTC()
	if err := store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: selfRotatedJTI, TenantID: tenant, Subject: admintoken.DefaultUsername,
		TokenHash: []byte("flow2-hash"), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record flow-2 token: %v", err)
	}

	// The sweep runs. prev_jti names the already-revoked first bootstrap jti, so
	// the sweep's durable revoke is an idempotent no-op; the flow-2 token, named
	// nowhere in the Secret, is untouched.
	recl := newReclaimer(t, secrets, storeAdminIssued{store: store, cache: cache}, tenant)
	if _, err := recl.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	selfRotated, err := store.Get(ctx, tenant, selfRotatedJTI)
	if err != nil {
		t.Fatalf("Get flow-2 token: %v", err)
	}
	if selfRotated.Revoked() {
		t.Fatal("reclaimer sweep revoked a flow-2 self-rotated lenny-admin token (whole-subject sweep defect)")
	}
	// Sanity: the Secret's current bootstrap token is also live.
	cur, err := store.Get(ctx, tenant, secrets.currentJTI())
	if err != nil {
		t.Fatalf("Get current bootstrap token: %v", err)
	}
	if cur.Revoked() {
		t.Fatal("current bootstrap admin token unexpectedly revoked")
	}
}

// spec: §13.3 lines 603-604 (revoke-before-overwrite plus the sweep closing
// the last-named predecessor).
// diagnosis: two crash-then-retry rotations straddling the sweep window must
// leave NEITHER superseded token live. Rotation A crashes leaving predecessor
// jti_A orphaned and named; the operator retries with rotation B, which
// durably revokes jti_A before overwriting prev_jti (revoke-before-overwrite),
// then itself crashes leaving jti_B orphaned and named. The sweep then closes
// jti_B. A failure means an orphan escapes both the revoke-before-overwrite
// step and the sweep, validating indefinitely under the ~10-year admin TTL —
// the indefinite-validation leak the two rules jointly close.
func TestReclaimerTwoCrashRetryRotationsRevokeBothPredecessors(t *testing.T) {
	t.Parallel()
	store, pg := startIssuedStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	signer := jwt.NewHMACSigner("k", []byte("admin-token-secret"))
	cache := &recordingCache{}
	secrets := &memSecrets{}
	crash := &crashingIssued{inner: storeAdminIssued{store: store, cache: cache}}
	p, err := admintoken.New(admintoken.Config{Namespace: "lenny-system", SecretName: admintoken.DefaultSecretName, AdminTenant: tenant},
		signer, userstore.NewMemory(), secrets, crash, time.Now)
	if err != nil {
		t.Fatalf("admintoken.New: %v", err)
	}

	// Establish jti_A as the current admin token.
	rA, err := p.Rotate(ctx)
	if err != nil {
		t.Fatalf("Rotate A: %v", err)
	}
	jtiA := jtiOf(t, signer, rA.Token)

	// Rotation A' patches the Secret to {jti_B, prev_jti: jti_A} then crashes on
	// its post-patch revoke of jti_A, leaving jti_A orphaned and named.
	crash.crashOn(jtiA)
	if _, err := p.Rotate(ctx); err == nil {
		t.Fatal("rotation A' must surface the crash on the post-patch revoke of jti_A")
	}
	jtiB := secrets.currentJTI()
	if secrets.prevJTI() != jtiA {
		t.Fatalf("after crash A': prev_jti=%q want jti_A=%q", secrets.prevJTI(), jtiA)
	}

	// The operator retries with rotation B. Its revoke-before-overwrite step
	// (step 2) durably revokes jti_A — the predecessor the live Secret names —
	// BEFORE overwriting prev_jti; the crash is armed only on jti_B, so this step
	// commits and closes the first orphan. B then mints jti_C, patches to
	// {jti_C, prev_jti: jti_B}, and crashes on its post-patch revoke of jti_B,
	// leaving jti_B orphaned and named. This is the real rotateLocked sequence,
	// driven end to end rather than modeled out of band.
	crash.crashOn(jtiB)
	if _, err := p.Rotate(ctx); err == nil {
		t.Fatal("rotation B must surface the crash on the post-patch revoke of jti_B")
	}
	if secrets.prevJTI() != jtiB {
		t.Fatalf("after crash B: prev_jti=%q want jti_B=%q", secrets.prevJTI(), jtiB)
	}

	// jti_A is durably revoked by rotation B's revoke-before-overwrite step;
	// jti_B is still live-and-named after B's crash.
	aTok, err := store.Get(ctx, tenant, jtiA)
	if err != nil {
		t.Fatalf("Get jti_A: %v", err)
	}
	if !aTok.Revoked() {
		t.Fatal("jti_A not revoked by revoke-before-overwrite (first orphan leaked)")
	}
	bTok, err := store.Get(ctx, tenant, jtiB)
	if err != nil {
		t.Fatalf("Get jti_B: %v", err)
	}
	if bTok.Revoked() {
		t.Fatal("precondition: jti_B must be live-and-named before the sweep runs")
	}

	// The sweep closes the last-named predecessor jti_B.
	recl := newReclaimer(t, secrets, storeAdminIssued{store: store, cache: cache}, tenant)
	if _, err := recl.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	// Neither superseded token is live after the sweep.
	bAfter, err := store.Get(ctx, tenant, jtiB)
	if err != nil {
		t.Fatalf("Get jti_B after sweep: %v", err)
	}
	if !bAfter.Revoked() {
		t.Fatal("sweep did not close the last-named predecessor jti_B (indefinite-validation leak)")
	}
	if bAfter.RevokedReason != "rotation_replaced" {
		t.Errorf("jti_B revoked_reason=%q, want rotation_replaced", bAfter.RevokedReason)
	}
}
