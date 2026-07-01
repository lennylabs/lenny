// SPDX-License-Identifier: MIT

package admintoken_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
)

// fakeSecrets is an in-memory SecretStore. It records create/update
// calls so a test can assert idempotence and the §17.6 line 470 data
// shape.
type fakeSecrets struct {
	mu        sync.Mutex
	store     map[string]map[string][]byte
	labels    map[string]map[string]string
	creates   int
	updates   int
	getErr    error
	updateErr error
}

func newFakeSecrets() *fakeSecrets {
	return &fakeSecrets{store: map[string]map[string][]byte{}, labels: map[string]map[string]string{}}
}

func key(ns, name string) string { return ns + "/" + name }

func (f *fakeSecrets) Get(_ context.Context, ns, name string) (map[string][]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	d, ok := f.store[key(ns, name)]
	return d, ok, nil
}

func (f *fakeSecrets) Create(_ context.Context, ns, name string, labels map[string]string, data map[string][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	f.store[key(ns, name)] = data
	f.labels[key(ns, name)] = labels
	return nil
}

func (f *fakeSecrets) Update(_ context.Context, ns, name string, data map[string][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates++
	f.store[key(ns, name)] = data
	return nil
}

type recordedRevoke struct {
	tenant, jti string
}

// fakeIssued is an in-memory admintoken.IssuedTokens. It records every
// call so the ordering tests can assert the §13.3 rotation sequence:
// which record path (audited vs non-audited) each mint took, the
// durable-revoke order relative to the Secret patch, and the per-subject
// lock bracketing the whole read-modify-write. Individual call errors can
// be injected to exercise the abort-and-retry failure branches.
type fakeIssued struct {
	mu             sync.Mutex
	recorded       []admintoken.MintedToken // non-audited Record (Provision)
	recordedAudit  []admintoken.MintedToken // RecordWithExchangeAudit (Rotate)
	durableRevoked []recordedRevoke         // DurableRevoke (with audit + gated cache)
	events         []string                 // ordered log: "record"/"record_audit"/"revoke:<jti>"/"lock_enter"/"lock_exit"

	// Injected failures. recordAuditErr fails the audited mint; revokeErr
	// keyed by jti fails DurableRevoke for that jti.
	recordAuditErr error
	revokeErr      map[string]error

	// existing tracks which jtis are still live (not durably revoked), so
	// a retry against an already-revoked jti is a no-op.
	revokedSet map[string]struct{}
}

func newFakeIssued() *fakeIssued {
	return &fakeIssued{revokeErr: map[string]error{}, revokedSet: map[string]struct{}{}}
}

func (f *fakeIssued) Record(_ context.Context, rec admintoken.MintedToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, rec)
	f.events = append(f.events, "record")
	return nil
}

func (f *fakeIssued) RecordWithExchangeAudit(_ context.Context, rec admintoken.MintedToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordAuditErr != nil {
		return f.recordAuditErr
	}
	f.recordedAudit = append(f.recordedAudit, rec)
	f.events = append(f.events, "record_audit")
	return nil
}

func (f *fakeIssued) DurableRevoke(_ context.Context, tenant, jti string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.revokeErr[jti]; err != nil {
		return err
	}
	if _, done := f.revokedSet[jti]; done {
		// Idempotent no-op: already durably revoked.
		return nil
	}
	f.revokedSet[jti] = struct{}{}
	f.durableRevoked = append(f.durableRevoked, recordedRevoke{tenant, jti})
	f.events = append(f.events, "revoke:"+jti)
	return nil
}

func (f *fakeIssued) WithSubjectLock(ctx context.Context, _ string, _ string, fn func(context.Context) error) error {
	f.mu.Lock()
	f.events = append(f.events, "lock_enter")
	f.mu.Unlock()
	err := fn(ctx)
	f.mu.Lock()
	f.events = append(f.events, "lock_exit")
	f.mu.Unlock()
	return err
}

func (f *fakeIssued) snapshotEvents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	copy(out, f.events)
	return out
}

func newProvisioner(t *testing.T, secrets admintoken.SecretStore, rev admintoken.IssuedTokens) (*admintoken.Provisioner, *jwt.HMACSigner, userstore.Store) {
	t.Helper()
	signer := jwt.NewHMACSigner("k", []byte("admin-token-secret"))
	users := userstore.NewMemory()
	p, err := admintoken.New(admintoken.Config{
		Namespace:   "lenny-system",
		AdminTenant: "default",
	}, signer, users, secrets, rev, func() time.Time {
		return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, signer, users
}

// spec: §17.6 lines 455-471 — the first run mints a token, writes the
// Secret with the bootstrap label + data fields, and creates the
// platform-admin user. F-17.6.3.
func TestProvisionFirstRunCreatesSecretAndUser_spec_17_6_455(t *testing.T) {
	secrets := newFakeSecrets()
	p, signer, users := newProvisioner(t, secrets, nil)

	res, err := p.Provision(context.Background())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !res.Created {
		t.Fatal("first run should report Created=true")
	}
	if res.Token == "" {
		t.Fatal("first run should return the minted token")
	}
	// §17.6 line 467 label.
	if got := secrets.labels[key("lenny-system", "lenny-admin-token")][admintoken.ManagedByLabel]; got != admintoken.ManagedByValue {
		t.Errorf("managed-by label = %q, want %q", got, admintoken.ManagedByValue)
	}
	// §17.6 line 470 data fields.
	data := secrets.store[key("lenny-system", "lenny-admin-token")]
	if string(data[admintoken.TokenKey]) != res.Token {
		t.Error("Secret token field does not match returned token")
	}
	if _, err := time.Parse(time.RFC3339, string(data[admintoken.CreatedAtKey])); err != nil {
		t.Errorf("created_at is not RFC3339: %v", err)
	}

	// The minted token verifies and authorizes platform-admin.
	claims, err := signer.Verify(res.Token)
	if err != nil {
		t.Fatalf("minted token did not verify: %v", err)
	}
	if claims.Subject != "lenny-admin" || claims.TenantID != "default" {
		t.Errorf("claims subject/tenant = %q/%q", claims.Subject, claims.TenantID)
	}
	if claims.Typ != auth.TokenUserBearer {
		t.Errorf("claims typ = %q, want user_bearer", claims.Typ)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != auth.RolePlatformAdmin {
		t.Errorf("claims roles = %v, want [platform-admin]", claims.Roles)
	}

	// The admin user row exists with the platform-admin role.
	u, err := users.Get(context.Background(), "default", "lenny-admin")
	if err != nil {
		t.Fatalf("admin user not created: %v", err)
	}
	if len(u.Roles) != 1 || u.Roles[0] != auth.RolePlatformAdmin {
		t.Errorf("admin user roles = %v, want [platform-admin]", u.Roles)
	}
}

// spec: §17.6 line 459 — a re-run preserves the existing token and does
// not regenerate it. F-17.6.3.
func TestProvisionIsIdempotent_spec_17_6_459(t *testing.T) {
	secrets := newFakeSecrets()
	p, _, _ := newProvisioner(t, secrets, nil)

	first, err := p.Provision(context.Background())
	if err != nil {
		t.Fatalf("Provision #1: %v", err)
	}
	firstToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])

	second, err := p.Provision(context.Background())
	if err != nil {
		t.Fatalf("Provision #2: %v", err)
	}
	if second.Created {
		t.Error("re-run should report Created=false")
	}
	if second.Token != "" {
		t.Error("re-run should not echo a token")
	}
	if secrets.creates != 1 {
		t.Errorf("Secret created %d times, want 1", secrets.creates)
	}
	if got := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey]); got != firstToken {
		t.Error("re-run regenerated the token; it must be preserved")
	}
	_ = first
}

// spec: §13.3 (gateway-mediated admin-credential rotation ordering, lines
// 599/601), §16.7 (token.exchanged admin_rotation, token.revoked
// rotation_replaced) — rotation mints a new token through the AUDITED
// record path, patches the Secret naming the read-time current jti as the
// predecessor, then durably revokes the prior token. F-17.6.3.
//
// This asserts the corrected outcome: pre-fix Rotate recorded the rotated
// token through the non-audited Record path (no token.exchanged) and
// best-effort-revoked with no audit and no durable guarantee. A pre-fix
// implementation records zero audited tokens and performs zero
// DurableRevoke calls, so this test fails against it.
func TestRotateMintsAndRevokesPrevious_spec_13_3(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, signer, _ := newProvisioner(t, secrets, rev)

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	oldToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])
	oldClaims, err := signer.Verify(oldToken)
	if err != nil {
		t.Fatalf("verify old token: %v", err)
	}

	res, err := p.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !res.Created {
		t.Error("rotation should report Created=true")
	}
	data := secrets.store[key("lenny-system", "lenny-admin-token")]
	newToken := string(data[admintoken.TokenKey])
	if newToken == oldToken {
		t.Error("rotation did not change the token")
	}
	if secrets.updates != 1 {
		t.Errorf("Secret updated %d times, want 1", secrets.updates)
	}
	// The Secret now names the read-time current jti as the predecessor so
	// the reclaimer sweep can revoke it after a crash.
	if got := string(data["prev_jti"]); got != oldClaims.JWTID {
		t.Errorf("prev_jti = %q, want the old token's jti %q", got, oldClaims.JWTID)
	}
	// The previous jti was durably revoked (audited, cache gated on durable
	// write), replacing the pre-fix best-effort unaudited revoke.
	if len(rev.durableRevoked) != 1 {
		t.Fatalf("durable revocations = %d, want 1", len(rev.durableRevoked))
	}
	if rev.durableRevoked[0].jti != oldClaims.JWTID {
		t.Errorf("revoked jti = %q, want the old token's jti %q", rev.durableRevoked[0].jti, oldClaims.JWTID)
	}
	// Provision recorded through the non-audited path; Rotate recorded
	// through the AUDITED path (the corrected outcome).
	if len(rev.recorded) != 1 {
		t.Fatalf("non-audited records = %d, want 1 (Provision only)", len(rev.recorded))
	}
	if len(rev.recordedAudit) != 1 {
		t.Fatalf("audited records = %d, want 1 (Rotate emits token.exchanged)", len(rev.recordedAudit))
	}
	if rev.recordedAudit[0].JTI == oldClaims.JWTID {
		t.Error("rotated token reused the prior jti")
	}
}

// spec: §13.3 line 599 — the whole rotation read-modify-write runs under
// the per-subject advisory lock, and the durable revoke of the superseded
// token happens AFTER the Secret patch (persist-Secret-before-revoke). The
// event order pins both invariants against the pre-fix code, which held no
// lock and revoked (best-effort) after the patch with no audit.
func TestRotateOrderingUnderLock_spec_13_3_599(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, signer, _ := newProvisioner(t, secrets, rev)

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	oldToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])
	oldClaims, _ := signer.Verify(oldToken)

	if _, err := p.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	events := rev.snapshotEvents()
	// Expected order: record (Provision), then the Rotate critical section
	// bracketed by the lock: record_audit (mint), then revoke of the prior
	// jti — and the whole thing between lock_enter/lock_exit.
	want := []string{
		"record",
		"lock_enter",
		"record_audit",
		"revoke:" + oldClaims.JWTID,
		"lock_exit",
	}
	if len(events) != len(want) {
		t.Fatalf("event order = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event[%d] = %q, want %q (full: %v)", i, events[i], want[i], events)
		}
	}
}

// spec: §13.3 line 601 (revoke-before-overwrite) — when the live Secret's
// prev_jti already names an orphaned predecessor (a prior crash between
// patch and revoke), the next Rotate durably revokes that predecessor
// BEFORE overwriting the prev_jti slot, so it is never named nowhere. This
// behavior is absent from pre-fix code, which had no prev_jti slot and no
// revoke-before-overwrite step.
func TestRotateRevokesOrphanedPredecessorBeforeOverwrite_spec_13_3_601(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, _, _ := newProvisioner(t, secrets, rev)

	// Seed a Secret whose prev_jti names an orphaned predecessor "jti_A"
	// (as a crash between patch and revoke would leave it), with a live
	// current token "jti_B".
	secrets.store[key("lenny-system", "lenny-admin-token")] = map[string][]byte{
		admintoken.TokenKey:     []byte("old-token-bytes"),
		admintoken.CreatedAtKey: []byte(time.Now().UTC().Format(time.RFC3339)),
		"jti":                   []byte("jti_B"),
		"prev_jti":              []byte("jti_A"),
	}

	if _, err := p.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Both the orphaned predecessor jti_A and the read-time current jti_B
	// are durably revoked, and jti_A is revoked before the patch (before
	// jti_B, which is revoked after the patch).
	revokedJTIs := map[string]int{}
	for i, r := range rev.durableRevoked {
		revokedJTIs[r.jti] = i
	}
	if _, ok := revokedJTIs["jti_A"]; !ok {
		t.Fatal("orphaned predecessor jti_A was not durably revoked (would validate indefinitely)")
	}
	if _, ok := revokedJTIs["jti_B"]; !ok {
		t.Fatal("read-time current jti_B was not durably revoked")
	}
	if revokedJTIs["jti_A"] >= revokedJTIs["jti_B"] {
		t.Errorf("jti_A must be revoked before jti_B (revoke-before-overwrite): order indices A=%d B=%d",
			revokedJTIs["jti_A"], revokedJTIs["jti_B"])
	}
}

// spec: §13.3 line 601 — Rotate aborts BEFORE patching the Secret when the
// revoke of the orphaned predecessor fails, so the prev_jti slot keeps
// naming the still-unrevoked predecessor for the next attempt or the
// sweep, and the Secret is not overwritten to lose the predecessor's name.
func TestRotateAbortsWhenOrphanRevokeFails_spec_13_3_601(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	rev.revokeErr["jti_A"] = errors.New("durable store unreachable")
	p, _, _ := newProvisioner(t, secrets, rev)

	secrets.store[key("lenny-system", "lenny-admin-token")] = map[string][]byte{
		admintoken.TokenKey:     []byte("old-token-bytes"),
		admintoken.CreatedAtKey: []byte(time.Now().UTC().Format(time.RFC3339)),
		"jti":                   []byte("jti_B"),
		"prev_jti":              []byte("jti_A"),
	}

	if _, err := p.Rotate(context.Background()); err == nil {
		t.Fatal("Rotate must fail when the orphaned-predecessor revoke fails")
	}
	// The Secret must NOT have been patched: prev_jti still names jti_A.
	data := secrets.store[key("lenny-system", "lenny-admin-token")]
	if got := string(data["prev_jti"]); got != "jti_A" {
		t.Errorf("prev_jti = %q, want jti_A preserved (Rotate must abort before overwriting)", got)
	}
	if secrets.updates != 0 {
		t.Errorf("Secret updated %d times, want 0 (abort before patch)", secrets.updates)
	}
}

// spec: §13.3 line 599 — a Secret-patch failure after the audited mint
// leaves the old token live and NOT durably revoked (a clean retry rather
// than a dead-token lockout): the durable revoke of the read-time current
// token runs only after a successful patch.
func TestRotatePatchFailureLeavesOldTokenLive_spec_13_3_599(t *testing.T) {
	secrets := newFakeSecrets()
	secrets.updateErr = errors.New("secret patch failed")
	rev := newFakeIssued()
	p, signer, _ := newProvisioner(t, secrets, rev)

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	oldToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])
	oldClaims, _ := signer.Verify(oldToken)

	if _, err := p.Rotate(context.Background()); err == nil {
		t.Fatal("Rotate must fail when the Secret patch fails")
	}
	// The old token's jti was NOT durably revoked: a patch failure must not
	// strand the operator on a dead token.
	for _, r := range rev.durableRevoked {
		if r.jti == oldClaims.JWTID {
			t.Fatal("old token revoked despite the Secret patch failing (dead-token lockout)")
		}
	}
}

// Rotate with no existing Secret provisions one fresh (an operator
// rotating before the first bootstrap). spec: §17.6.
func TestRotateWithoutExistingSecretCreates(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, _, _ := newProvisioner(t, secrets, rev)

	res, err := p.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !res.Created {
		t.Error("Rotate on empty state should report Created=true")
	}
	if secrets.creates != 1 || secrets.updates != 0 {
		t.Errorf("creates=%d updates=%d, want 1/0", secrets.creates, secrets.updates)
	}
	if len(rev.durableRevoked) != 0 {
		t.Errorf("no prior token existed; durable revocations=%d, want 0", len(rev.durableRevoked))
	}
	// The first mint on an empty state records through the AUDITED path
	// (Rotate always uses the exchange audit; only Provision's bootstrap
	// first mint is non-audited).
	if len(rev.recordedAudit) != 1 {
		t.Errorf("audited records = %d, want 1", len(rev.recordedAudit))
	}
}

// spec: §13.3 line 587, §17.6 — the bootstrap Provision() first mint is
// NOT a token exchange: it records through the non-audited Record path and
// emits no token.exchanged audit row (no exchange_type: admin_rotation).
// This pins the corrected scoping: the audited record path is reserved for
// Rotate, so a regression that routes Provision through the audited path
// (asserting a rotation that did not occur) fails here.
func TestProvisionUsesNonAuditedRecordPath_spec_13_3_587(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, _, _ := newProvisioner(t, secrets, rev)

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(rev.recorded) != 1 {
		t.Errorf("non-audited records = %d, want 1", len(rev.recorded))
	}
	if len(rev.recordedAudit) != 0 {
		t.Errorf("audited records = %d, want 0 (bootstrap first mint is not an exchange)", len(rev.recordedAudit))
	}
	if len(rev.durableRevoked) != 0 {
		t.Errorf("durable revocations = %d, want 0 (nothing to revoke on first mint)", len(rev.durableRevoked))
	}
}

// spec: §13.3 line 599 — a durable-revoke failure AFTER the Secret patch
// surfaces to the caller so the operator retries; the Secret already holds
// the new token (the operator is not locked out) and the predecessor stays
// named in prev_jti so the reclaimer sweep can finish the revoke. This pins
// the corrected outcome against the pre-fix best-effort revoke, which
// discarded the error (`_ = p.issued.Revoke(...)`) and reported success.
func TestRotatePostPatchRevokeFailureSurfaces_spec_13_3_599(t *testing.T) {
	secrets := newFakeSecrets()
	rev := newFakeIssued()
	p, signer, _ := newProvisioner(t, secrets, rev)

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	oldToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])
	oldClaims, _ := signer.Verify(oldToken)
	// Fail the post-patch durable revoke of the read-time current jti.
	rev.revokeErr[oldClaims.JWTID] = errors.New("durable store unreachable")

	if _, err := p.Rotate(context.Background()); err == nil {
		t.Fatal("Rotate must surface the post-patch durable-revoke failure, not swallow it")
	}
	// The Secret was patched (the operator holds the new token) and prev_jti
	// names the still-unrevoked predecessor for the sweep.
	data := secrets.store[key("lenny-system", "lenny-admin-token")]
	if string(data[admintoken.TokenKey]) == oldToken {
		t.Error("Secret still holds the old token; the patch should have committed before the revoke")
	}
	if got := string(data["prev_jti"]); got != oldClaims.JWTID {
		t.Errorf("prev_jti = %q, want the unrevoked predecessor %q named for the sweep", got, oldClaims.JWTID)
	}
}

// spec: §17.6 — with no durable store wired (dev/in-memory), Rotate still
// rotates the Secret without a lock or a durable revoke. The degraded
// no-store posture is acceptable for deployments that have no issued-token
// store; the rotated token then lapses only at its own expiry.
func TestRotateWithoutStoreRotatesSecret(t *testing.T) {
	secrets := newFakeSecrets()
	p, signer, _ := newProvisioner(t, secrets, nil) // nil IssuedTokens

	if _, err := p.Provision(context.Background()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	oldToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])

	res, err := p.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate with no store: %v", err)
	}
	if res.Token == oldToken {
		t.Error("Rotate did not change the token")
	}
	if secrets.updates != 1 {
		t.Errorf("Secret updated %d times, want 1", secrets.updates)
	}
	// The rotated token still verifies.
	if _, err := signer.Verify(res.Token); err != nil {
		t.Errorf("rotated token did not verify: %v", err)
	}
}

// New rejects missing required dependencies.
func TestNewValidatesDeps(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("s"))
	users := userstore.NewMemory()
	secrets := newFakeSecrets()
	cases := []struct {
		name string
		cfg  admintoken.Config
		sig  admintoken.Signer
	}{
		{"no signer", admintoken.Config{Namespace: "ns", AdminTenant: "t"}, nil},
		{"no namespace", admintoken.Config{AdminTenant: "t"}, signer},
		{"no tenant", admintoken.Config{Namespace: "ns"}, signer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := admintoken.New(tc.cfg, tc.sig, users, secrets, nil, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
