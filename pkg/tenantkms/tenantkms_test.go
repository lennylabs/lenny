// SPDX-License-Identifier: MIT

package tenantkms_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §12.9 — the per-tenant KMS key lifecycle for T4 — Restricted
// tenants: provision on create / T3→T4 upgrade, the §4.9.1 rotation
// hook, and destroy-on-erasure for §12.8 Phase 4a cryptographic
// erasure.

// newLifecycle builds a Lifecycle over a LocalManager backed by a
// fixed-seed kms.Local so the test is deterministic.
func newLifecycle(t *testing.T) (*tenantkms.Lifecycle, *kms.Local) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	return tenantkms.New(tenantkms.NewLocalManager(local)), local
}

func TestAliasFor(t *testing.T) {
	if got := tenantkms.AliasFor("acme"); got != "tenant:acme" {
		t.Errorf("AliasFor = %q, want tenant:acme", got)
	}
}

func TestEnsureForTenantProvisionsT4Key(t *testing.T) {
	lc, _ := newLifecycle(t)
	info, err := lc.EnsureForTenant(context.Background(), "acme", tenantkms.WorkspaceTierT4)
	if err != nil {
		t.Fatalf("EnsureForTenant: %v", err)
	}
	if info.Alias != "tenant:acme" {
		t.Errorf("alias = %q, want tenant:acme", info.Alias)
	}
	if info.State != tenantkms.KeyStateActive {
		t.Errorf("state = %q, want active", info.State)
	}
	if info.Version != 1 {
		t.Errorf("version = %d, want 1 on first provision", info.Version)
	}
}

func TestEnsureForTenantSkipsNonT4Tier(t *testing.T) {
	// §12.9: only a T4 tenant gets a tenant-scoped key. A T3 tenant is
	// a no-op.
	lc, _ := newLifecycle(t)
	info, err := lc.EnsureForTenant(context.Background(), "acme", "T3")
	if err != nil {
		t.Fatalf("EnsureForTenant: %v", err)
	}
	if info != (tenantkms.KeyInfo{}) {
		t.Errorf("a non-T4 tenant should yield the zero KeyInfo, got %+v", info)
	}
}

func TestEnsureForTenantIsIdempotent(t *testing.T) {
	// A T3→T4 upgrade or a controller retry must not mint a second
	// key: a second EnsureForTenant returns the existing key unchanged.
	lc, _ := newLifecycle(t)
	ctx := context.Background()
	first, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4)
	if err != nil {
		t.Fatalf("first EnsureForTenant: %v", err)
	}
	second, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4)
	if err != nil {
		t.Fatalf("second EnsureForTenant: %v", err)
	}
	if first.Version != second.Version || second.Version != 1 {
		t.Errorf("a repeated provision must not advance the version: first=%d second=%d",
			first.Version, second.Version)
	}
}

func TestEnsureForTenantRejectsEmptyTenantID(t *testing.T) {
	lc, _ := newLifecycle(t)
	if _, err := lc.EnsureForTenant(context.Background(), "", tenantkms.WorkspaceTierT4); !errors.Is(err, tenantkms.ErrEmptyTenantID) {
		t.Errorf("EnsureForTenant(\"\") error = %v, want ErrEmptyTenantID", err)
	}
}

func TestRotateForTenantAdvancesVersion(t *testing.T) {
	// §4.9.1 rotation hook: rotation advances the key version.
	lc, _ := newLifecycle(t)
	ctx := context.Background()
	if _, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision: %v", err)
	}
	info, err := lc.RotateForTenant(ctx, "acme", tenantkms.WorkspaceTierT4)
	if err != nil {
		t.Fatalf("RotateForTenant: %v", err)
	}
	if info.Version != 2 {
		t.Errorf("version after rotation = %d, want 2", info.Version)
	}
	if info.State != tenantkms.KeyStateActive {
		t.Errorf("state after rotation = %q, want active", info.State)
	}
}

func TestRotateForTenantProvisionsWhenKeyAbsent(t *testing.T) {
	// The rotation hook is safe to invoke on a T4 tenant whose
	// provision step has not yet run: it provisions, then rotates.
	lc, _ := newLifecycle(t)
	info, err := lc.RotateForTenant(context.Background(), "acme", tenantkms.WorkspaceTierT4)
	if err != nil {
		t.Fatalf("RotateForTenant on an unprovisioned tenant: %v", err)
	}
	if info.Version != 2 {
		t.Errorf("version = %d, want 2 (provision at v1, then rotate)", info.Version)
	}
}

func TestRotateForTenantSkipsNonT4(t *testing.T) {
	lc, _ := newLifecycle(t)
	info, err := lc.RotateForTenant(context.Background(), "acme", "T3")
	if err != nil {
		t.Fatalf("RotateForTenant: %v", err)
	}
	if info != (tenantkms.KeyInfo{}) {
		t.Errorf("a non-T4 rotation should be a no-op, got %+v", info)
	}
}

func TestDestroyForTenantDestroysKey(t *testing.T) {
	// §12.8 Phase 4a: the tenant key is destroyed for cryptographic
	// erasure.
	lc, _ := newLifecycle(t)
	ctx := context.Background()
	if _, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision: %v", err)
	}
	info, err := lc.DestroyForTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DestroyForTenant: %v", err)
	}
	if info.State != tenantkms.KeyStateDestroyed {
		t.Errorf("state = %q, want destroyed", info.State)
	}
}

func TestDestroyForTenantIsIdempotent(t *testing.T) {
	// §12.8 Phase 4a is re-run on a controller restart. A second
	// destroy — and a destroy for a tenant that never had a key —
	// must succeed as a no-op so the deletion job is not blocked.
	lc, _ := newLifecycle(t)
	ctx := context.Background()
	if _, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := lc.DestroyForTenant(ctx, "acme"); err != nil {
		t.Fatalf("first DestroyForTenant: %v", err)
	}
	info, err := lc.DestroyForTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("second DestroyForTenant must be a no-op: %v", err)
	}
	if info.State != tenantkms.KeyStateDestroyed {
		t.Errorf("state = %q, want destroyed on the idempotent re-run", info.State)
	}
}

func TestDestroyForTenantNoKeyIsNoOp(t *testing.T) {
	// A non-T4 tenant has no per-tenant key; Phase 4a destroying it is
	// a no-op that reports the cryptographic-erasure post-condition.
	lc, _ := newLifecycle(t)
	info, err := lc.DestroyForTenant(context.Background(), "never-provisioned")
	if err != nil {
		t.Fatalf("DestroyForTenant on an unprovisioned tenant: %v", err)
	}
	if info.State != tenantkms.KeyStateDestroyed {
		t.Errorf("state = %q, want destroyed", info.State)
	}
}

func TestDestroyForTenantDisablesBeforeDestroying(t *testing.T) {
	// §12.8 Phase 4a: the key is disabled before deletion is
	// scheduled, so the pending window cannot wrap new data.
	mgr := newRecordingManager(t)
	lc := tenantkms.New(mgr)
	ctx := context.Background()
	if _, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := lc.DestroyForTenant(ctx, "acme"); err != nil {
		t.Fatalf("DestroyForTenant: %v", err)
	}
	if len(mgr.calls) < 2 || mgr.calls[len(mgr.calls)-2] != "Disable" || mgr.calls[len(mgr.calls)-1] != "Destroy" {
		t.Errorf("destroy call order = %v, want a Disable immediately before Destroy", mgr.calls)
	}
}

func TestCryptographicErasureRendersDataUnrecoverable(t *testing.T) {
	// End-to-end §12.9: a record envelope-encrypted under the tenant
	// key cannot be decrypted once the key is destroyed and the
	// process is restarted (a fresh Local without the seed).
	seed := bytes.Repeat([]byte{0x33}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	lc := tenantkms.New(tenantkms.NewLocalManager(local))
	ctx := context.Background()
	alias := tenantkms.AliasFor("acme")
	if _, err := lc.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Encrypt a T4 record under the tenant key.
	cipher, err := envelope.New(local, alias)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	plaintext := []byte("phi: patient record")
	sealed, err := cipher.Seal(ctx, plaintext)
	if err != nil {
		t.Fatalf("Cipher.Seal: %v", err)
	}
	// Sanity: the live key decrypts it.
	got, err := cipher.Open(ctx, sealed)
	if err != nil {
		t.Fatalf("Cipher.Open before destroy: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch before destroy")
	}

	// §12.8 Phase 4a destroys the tenant key.
	if _, err := lc.DestroyForTenant(ctx, "acme"); err != nil {
		t.Fatalf("DestroyForTenant: %v", err)
	}

	// After destruction, a process restart drops the key seed. A
	// fresh Local with a different seed cannot unwrap the DEK — the
	// ciphertext is cryptographically unrecoverable.
	otherSeed := bytes.Repeat([]byte{0x77}, kms.DEKSize)
	freshLocal, err := kms.NewLocal(otherSeed)
	if err != nil {
		t.Fatalf("kms.NewLocal (fresh): %v", err)
	}
	freshCipher, err := envelope.New(freshLocal, alias)
	if err != nil {
		t.Fatalf("envelope.New (fresh): %v", err)
	}
	if _, err := freshCipher.Open(ctx, sealed); err == nil {
		t.Fatal("decrypting a T4 record after the tenant key is destroyed must fail")
	}
}

func TestDestroyForTenantRejectsEmptyTenantID(t *testing.T) {
	lc, _ := newLifecycle(t)
	if _, err := lc.DestroyForTenant(context.Background(), ""); !errors.Is(err, tenantkms.ErrEmptyTenantID) {
		t.Errorf("DestroyForTenant(\"\") error = %v, want ErrEmptyTenantID", err)
	}
}

func TestLocalManagerRotateRejectsDestroyedKey(t *testing.T) {
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, "tenant:acme"); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if _, err := mgr.DestroyKey(ctx, "tenant:acme"); err != nil {
		t.Fatalf("DestroyKey: %v", err)
	}
	if _, err := mgr.RotateKey(ctx, "tenant:acme"); err == nil {
		t.Error("rotating a destroyed key must fail")
	}
}

// recordingManager wraps a LocalManager and records the order of
// lifecycle calls so a test can assert the §12.8 Phase 4a
// disable-before-destroy ordering.
type recordingManager struct {
	inner *tenantkms.LocalManager
	calls []string
}

func newRecordingManager(t *testing.T) *recordingManager {
	t.Helper()
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	return &recordingManager{inner: tenantkms.NewLocalManager(local)}
}

func (m *recordingManager) ProvisionKey(ctx context.Context, alias string) (tenantkms.KeyInfo, error) {
	m.calls = append(m.calls, "Provision")
	return m.inner.ProvisionKey(ctx, alias)
}

func (m *recordingManager) RotateKey(ctx context.Context, alias string) (tenantkms.KeyInfo, error) {
	m.calls = append(m.calls, "Rotate")
	return m.inner.RotateKey(ctx, alias)
}

func (m *recordingManager) DisableKey(ctx context.Context, alias string) (tenantkms.KeyInfo, error) {
	m.calls = append(m.calls, "Disable")
	return m.inner.DisableKey(ctx, alias)
}

func (m *recordingManager) DestroyKey(ctx context.Context, alias string) (tenantkms.KeyInfo, error) {
	m.calls = append(m.calls, "Destroy")
	return m.inner.DestroyKey(ctx, alias)
}

func (m *recordingManager) KeyInfoFor(ctx context.Context, alias string) (tenantkms.KeyInfo, error) {
	return m.inner.KeyInfoFor(ctx, alias)
}
