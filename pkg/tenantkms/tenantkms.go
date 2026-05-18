// SPDX-License-Identifier: MIT

// Package tenantkms implements the §12.9 per-tenant KMS key lifecycle
// for T4 — Restricted tenants.
//
// The §12.9 model. A tenant with workspaceTier: T4 gets a
// tenant-scoped KMS key so that its workspace artifacts (MinIO
// SSE-KMS, §12.5) and its envelope-encrypted records are wrapped under
// key material no other tenant — and no platform-wide compromise of a
// single shared key — can unwrap. The key is addressed by the
// per-tenant alias tenant:{tenant_id}, the same alias the credential
// store already uses (pkg/gateway/credentialstore/pgstore).
//
// The lifecycle has three transitions:
//
//   - Provision. On tenant create with workspaceTier: T4, or on a T3 →
//     T4 upgrade, the key is provisioned. Provisioning is idempotent:
//     a tenant whose key already exists is left unchanged, so a
//     repeated upgrade or a controller retry does not mint a second
//     key.
//   - Rotate. The §4.9.1 rotation hook advances the tenant key to a
//     fresh version. Records wrapped under a prior version still
//     unwrap, so the re-encryption job can run while both versions
//     are live.
//   - Destroy. On tenant deletion the §12.8 Phase 4a flow destroys the
//     tenant key (AWS KMS ScheduleKeyDeletion, GCP DestroyCryptoKeyVersion,
//     Vault transit key delete). Destroying the key renders every
//     artifact wrapped under it cryptographically unrecoverable — this
//     is the cryptographic-erasure half of §12.9's "Immediate +
//     cryptographic erasure where supported" T4 control. Destroy is
//     also idempotent: a tenant with no key, or an already-destroyed
//     key, is a no-op so a Phase 4a retry is safe.
//
// KeyManager is the lifecycle seam. The kms.Provider interface
// (pkg/kms) covers only WrapDEK/UnwrapDEK — the data path. Key
// provisioning, rotation, and destruction are control-plane operations
// a cloud KMS exposes through separate APIs; KeyManager is the seam
// for them. LocalManager is the in-process implementation over
// kms.Local for the no-cloud development path and for tests; a cloud
// KMS manager implements the same interface.
package tenantkms

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lennylabs/lenny/pkg/kms"
)

// AliasFor returns the §12.9 per-tenant KMS key alias for tenantID. It
// is the tenant:{tenant_id} form the credential store also uses, so a
// T4 tenant's credential records and workspace artifacts wrap under
// the same tenant-scoped key.
func AliasFor(tenantID string) string { return "tenant:" + tenantID }

// WorkspaceTierT4 is the §12.9 workspaceTier value that requires a
// per-tenant KMS key. Only a tenant at this tier gets one.
const WorkspaceTierT4 = "T4"

// Sentinel errors. Callers compare with errors.Is.
var (
	// ErrEmptyTenantID is returned for an empty tenant id. A
	// lifecycle operation never treats an empty id as a wildcard.
	ErrEmptyTenantID = errors.New("tenantkms: tenant ID is required")

	// ErrKeyNotFound is returned by a KeyManager when an operation
	// names a tenant key that was never provisioned. The lifecycle
	// treats it as a no-op for the idempotent Destroy path.
	ErrKeyNotFound = errors.New("tenantkms: tenant key not found")
)

// KeyState is the lifecycle state of a tenant KMS key.
type KeyState string

const (
	// KeyStateActive — the key exists and can wrap and unwrap.
	KeyStateActive KeyState = "active"
	// KeyStatePendingDeletion — the key is scheduled for deletion and
	// is disabled; it can no longer wrap. §12.8 Phase 4a disables a
	// key before scheduling its deletion so the pending window cannot
	// be used to wrap new data.
	KeyStatePendingDeletion KeyState = "pending_deletion"
	// KeyStateDestroyed — the key material is gone; data wrapped under
	// it is cryptographically unrecoverable.
	KeyStateDestroyed KeyState = "destroyed"
)

// KeyInfo is the observable state of a tenant KMS key.
type KeyInfo struct {
	// Alias is the tenant:{tenant_id} key alias.
	Alias string
	// State is the key's lifecycle state.
	State KeyState
	// Version is the current KEK version. WrapDEK stamps it; UnwrapDEK
	// still recovers DEKs wrapped under any prior version.
	Version int
}

// KeyManager is the §12.9 tenant-key control-plane seam: the
// provisioning, rotation, and destruction operations a cloud KMS
// exposes through APIs distinct from the wrap/unwrap data path.
//
// Implementations must be safe for concurrent use. Every method is
// idempotent so a controller retry — the §12.8 Phase 4a flow resumes
// from a persisted phase — does not double-provision or fail on an
// already-destroyed key.
type KeyManager interface {
	// ProvisionKey creates the KMS key for alias if it does not exist
	// and returns its KeyInfo. A second call for an existing key
	// returns the existing KeyInfo unchanged.
	ProvisionKey(ctx context.Context, alias string) (KeyInfo, error)

	// RotateKey advances the key for alias to a fresh version and
	// returns the updated KeyInfo. It returns ErrKeyNotFound when the
	// alias was never provisioned.
	RotateKey(ctx context.Context, alias string) (KeyInfo, error)

	// DisableKey moves the key for alias to KeyStatePendingDeletion so
	// it can no longer wrap new data. §12.8 Phase 4a disables before
	// scheduling deletion. A second call is a no-op. It returns
	// ErrKeyNotFound when the alias was never provisioned.
	DisableKey(ctx context.Context, alias string) (KeyInfo, error)

	// DestroyKey destroys the key material for alias (AWS KMS
	// ScheduleKeyDeletion, GCP DestroyCryptoKeyVersion, Vault transit
	// delete) and returns the final KeyInfo in KeyStateDestroyed. A
	// second call, or a call for an alias that was never provisioned,
	// returns ErrKeyNotFound; callers treat that as a completed
	// idempotent destroy.
	DestroyKey(ctx context.Context, alias string) (KeyInfo, error)

	// KeyInfoFor returns the current KeyInfo for alias, or
	// ErrKeyNotFound when no key was provisioned.
	KeyInfoFor(ctx context.Context, alias string) (KeyInfo, error)
}

// Lifecycle drives the §12.9 per-tenant KMS key lifecycle over a
// KeyManager. It is the call surface the tenant admin path
// (provision / upgrade), the §4.9.1 rotation hook, and the §12.8
// tenant-deletion controller (Phase 4a destroy) use.
type Lifecycle struct {
	mgr KeyManager
}

// New returns a Lifecycle over the given KeyManager.
func New(mgr KeyManager) *Lifecycle {
	return &Lifecycle{mgr: mgr}
}

// EnsureForTenant provisions the §12.9 per-tenant KMS key for a tenant
// at workspaceTier T4. It is the provision-on-create and the T3 → T4
// upgrade hook.
//
// For a non-T4 tier it is a no-op and returns the zero KeyInfo: only
// T4 tenants get a tenant-scoped key. For a T4 tenant it provisions
// the key idempotently — a tenant whose key already exists keeps it,
// so a repeated upgrade or a controller retry does not mint a second
// key.
func (l *Lifecycle) EnsureForTenant(ctx context.Context, tenantID, workspaceTier string) (KeyInfo, error) {
	if tenantID == "" {
		return KeyInfo{}, ErrEmptyTenantID
	}
	if workspaceTier != WorkspaceTierT4 {
		return KeyInfo{}, nil
	}
	info, err := l.mgr.ProvisionKey(ctx, AliasFor(tenantID))
	if err != nil {
		return KeyInfo{}, fmt.Errorf("tenantkms: provision key for tenant %q: %w", tenantID, err)
	}
	return info, nil
}

// RotateForTenant advances the tenant's §12.9 KMS key to a fresh
// version. It is the §4.9.1 rotation hook for T4 tenant keys. Records
// wrapped under a prior version still unwrap, so the re-encryption job
// runs while both versions are live.
//
// A non-T4 tier is a no-op. A T4 tenant whose key was never
// provisioned is provisioned first, then rotated, so the rotation hook
// is safe to invoke on a tenant whose provision step has not yet run.
func (l *Lifecycle) RotateForTenant(ctx context.Context, tenantID, workspaceTier string) (KeyInfo, error) {
	if tenantID == "" {
		return KeyInfo{}, ErrEmptyTenantID
	}
	if workspaceTier != WorkspaceTierT4 {
		return KeyInfo{}, nil
	}
	alias := AliasFor(tenantID)
	info, err := l.mgr.RotateKey(ctx, alias)
	if errors.Is(err, ErrKeyNotFound) {
		// The key was never provisioned — provision it, then rotate so
		// the caller still observes a fresh post-rotation version.
		if _, perr := l.mgr.ProvisionKey(ctx, alias); perr != nil {
			return KeyInfo{}, fmt.Errorf("tenantkms: provision key for tenant %q before rotate: %w", tenantID, perr)
		}
		info, err = l.mgr.RotateKey(ctx, alias)
	}
	if err != nil {
		return KeyInfo{}, fmt.Errorf("tenantkms: rotate key for tenant %q: %w", tenantID, err)
	}
	return info, nil
}

// DestroyForTenant destroys the tenant's §12.9 KMS key. It is the
// §12.8 Phase 4a cryptographic-erasure step: the key is first disabled
// (so the pending-deletion window cannot wrap new data) and then
// destroyed, rendering every artifact wrapped under it cryptographically
// unrecoverable.
//
// Destroy is idempotent and never fails the deletion job: a tenant
// with no key (a non-T4 tenant, or a T4 tenant whose key was never
// provisioned) and an already-destroyed key both return a
// KeyStateDestroyed KeyInfo with done=true and a nil error. §12.8
// Phase 4a is explicit that a failed KMS cleanup is surfaced in the
// erasure receipt but does not block the remaining phases — a
// transport error from the KeyManager is returned so the controller
// can record it, but ErrKeyNotFound is absorbed as a completed
// destroy.
func (l *Lifecycle) DestroyForTenant(ctx context.Context, tenantID string) (KeyInfo, error) {
	if tenantID == "" {
		return KeyInfo{}, ErrEmptyTenantID
	}
	alias := AliasFor(tenantID)

	// §12.8 Phase 4a: disable before scheduling deletion so no new
	// encrypt/decrypt call can use the key during the pending window.
	if _, err := l.mgr.DisableKey(ctx, alias); err != nil && !errors.Is(err, ErrKeyNotFound) {
		return KeyInfo{}, fmt.Errorf("tenantkms: disable key for tenant %q: %w", tenantID, err)
	}

	info, err := l.mgr.DestroyKey(ctx, alias)
	if errors.Is(err, ErrKeyNotFound) {
		// No key, or already destroyed — the cryptographic-erasure
		// post-condition already holds.
		return KeyInfo{Alias: alias, State: KeyStateDestroyed}, nil
	}
	if err != nil {
		return KeyInfo{}, fmt.Errorf("tenantkms: destroy key for tenant %q: %w", tenantID, err)
	}
	return info, nil
}

// LocalManager is the in-process KeyManager over kms.Local for the
// §12.9 no-cloud development path and for tests. It tracks per-alias
// lifecycle state and delegates version management to the embedded
// kms.Local provider so a wrapped DEK produced before a rotation still
// unwraps afterward.
//
// LocalManager is a development and test KeyManager: it holds key
// state in process memory and offers no HSM backing or audit trail. A
// production deployment uses a cloud KMS KeyManager.
//
// LocalManager is safe for concurrent use.
type LocalManager struct {
	local *kms.Local

	mu   sync.Mutex
	keys map[string]KeyInfo
}

var _ KeyManager = (*LocalManager)(nil)

// NewLocalManager returns a LocalManager over the given kms.Local
// provider. The same kms.Local instance must back both the manager and
// the envelope helper that wraps records, so a tenant key the manager
// rotates or destroys is the key the data path uses.
func NewLocalManager(local *kms.Local) *LocalManager {
	return &LocalManager{local: local, keys: map[string]KeyInfo{}}
}

// ProvisionKey implements KeyManager. It provisions the alias at KEK
// version 1 on first call and is a no-op on a repeat call.
func (m *LocalManager) ProvisionKey(ctx context.Context, alias string) (KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if info, ok := m.keys[alias]; ok {
		return info, nil
	}
	version, err := m.local.CurrentKEKVersion(ctx, alias)
	if err != nil {
		return KeyInfo{}, err
	}
	info := KeyInfo{Alias: alias, State: KeyStateActive, Version: version}
	m.keys[alias] = info
	return info, nil
}

// RotateKey implements KeyManager. It advances the alias to a fresh
// KEK version via kms.Local.RotateKEK so DEKs wrapped under the prior
// version still unwrap.
func (m *LocalManager) RotateKey(_ context.Context, alias string) (KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.keys[alias]
	if !ok {
		return KeyInfo{}, ErrKeyNotFound
	}
	if info.State == KeyStateDestroyed {
		return KeyInfo{}, fmt.Errorf("tenantkms: cannot rotate destroyed key %q", alias)
	}
	info.Version = m.local.RotateKEK(alias)
	info.State = KeyStateActive
	m.keys[alias] = info
	return info, nil
}

// DisableKey implements KeyManager. It moves an active key to
// KeyStatePendingDeletion and is a no-op for a key already past
// active.
func (m *LocalManager) DisableKey(_ context.Context, alias string) (KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.keys[alias]
	if !ok {
		return KeyInfo{}, ErrKeyNotFound
	}
	if info.State == KeyStateActive {
		info.State = KeyStatePendingDeletion
		m.keys[alias] = info
	}
	return info, nil
}

// DestroyKey implements KeyManager. It marks the alias destroyed. A
// repeat call, or a call for an alias that was never provisioned,
// returns ErrKeyNotFound.
func (m *LocalManager) DestroyKey(_ context.Context, alias string) (KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.keys[alias]
	if !ok || info.State == KeyStateDestroyed {
		return KeyInfo{}, ErrKeyNotFound
	}
	info.State = KeyStateDestroyed
	m.keys[alias] = info
	return info, nil
}

// KeyInfoFor implements KeyManager.
func (m *LocalManager) KeyInfoFor(_ context.Context, alias string) (KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.keys[alias]
	if !ok {
		return KeyInfo{}, ErrKeyNotFound
	}
	return info, nil
}
