// SPDX-License-Identifier: MIT

package admintoken_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/admintoken"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// fakeSecrets is an in-memory SecretStore. It records create/update
// calls so a test can assert idempotence and the §17.6 line 470 data
// shape.
type fakeSecrets struct {
	mu      sync.Mutex
	store   map[string]map[string][]byte
	labels  map[string]map[string]string
	creates int
	updates int
	getErr  error
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
	f.updates++
	f.store[key(ns, name)] = data
	return nil
}

type recordedRevoke struct {
	tenant, jti, reason string
}

type fakeRevoker struct {
	mu      sync.Mutex
	revoked []recordedRevoke
}

func (f *fakeRevoker) Revoke(_ context.Context, tenant, jti, reason string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, recordedRevoke{tenant, jti, reason})
	return nil
}

func newProvisioner(t *testing.T, secrets admintoken.SecretStore, rev admintoken.Revoker) (*admintoken.Provisioner, *jwt.HMACSigner, userstore.Store) {
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

// spec: §17.6 line 472 — rotation mints a new token, patches the Secret,
// and revokes the superseded token's jti immediately. F-17.6.3.
func TestRotateMintsAndRevokesPrevious_spec_17_6_472(t *testing.T) {
	secrets := newFakeSecrets()
	rev := &fakeRevoker{}
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
	newToken := string(secrets.store[key("lenny-system", "lenny-admin-token")][admintoken.TokenKey])
	if newToken == oldToken {
		t.Error("rotation did not change the token")
	}
	if secrets.updates != 1 {
		t.Errorf("Secret updated %d times, want 1", secrets.updates)
	}
	// The previous jti was revoked.
	if len(rev.revoked) != 1 {
		t.Fatalf("revocations = %d, want 1", len(rev.revoked))
	}
	if rev.revoked[0].jti != oldClaims.JWTID {
		t.Errorf("revoked jti = %q, want the old token's jti %q", rev.revoked[0].jti, oldClaims.JWTID)
	}
	if rev.revoked[0].reason != "admin_token_rotated" {
		t.Errorf("revoke reason = %q", rev.revoked[0].reason)
	}
}

// Rotate with no existing Secret provisions one fresh (an operator
// rotating before the first bootstrap). spec: §17.6 line 472.
func TestRotateWithoutExistingSecretCreates(t *testing.T) {
	secrets := newFakeSecrets()
	rev := &fakeRevoker{}
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
	if len(rev.revoked) != 0 {
		t.Errorf("no prior token existed; revocations=%d, want 0", len(rev.revoked))
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
