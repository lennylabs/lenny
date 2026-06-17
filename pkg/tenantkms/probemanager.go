// SPDX-License-Identifier: MIT

package tenantkms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
)

// ErrControlPlaneUnsupported is returned by a ProviderProbeManager for
// every control-plane operation (ProvisionKey, RotateKey, DisableKey,
// DestroyKey, KeyInfoFor). §12.5 line 301 makes per-tenant key
// provisioning the deployer's responsibility ("Operators MUST provision
// the tenant-specific KMS key … before setting workspaceTier: T4") and
// §12.8 Phase 4a destruction is driven against the cloud KMS console/API.
// The gateway's kms.Provider is the wrap/unwrap data-path seam only, so a
// ProviderProbeManager exposes the §12.5 availability Probe and nothing
// else.
var ErrControlPlaneUnsupported = errors.New(
	"tenantkms: control-plane operations are not supported by the provider-backed probe manager",
)

// ProviderProbeManager is a probe-only KeyManager over a kms.Provider.
// It backs the §12.5 admin-time probe (bullet 1) and the continuous
// probe (bullet 4, STO-021) in the gateway, which resolves a single
// kms.Provider for the §4 envelope data path and reuses it as the
// availability-probe backend so the probe observes the same provider
// credentials a T4 artifact write would use.
//
// Probe runs the spec's "zero-byte encrypt/decrypt round-trip against
// the tenant-scoped key": WrapDEK then UnwrapDEK of a fixed throwaway
// DEK under the tenant:{tenant_id} alias. A round-trip failure (key
// disabled, provider-side outage, IAM grant stripped) returns a non-nil
// error that the Lifecycle maps to the fail-closed
// CLASSIFICATION_CONTROL_VIOLATION admin rejection and the
// T4KmsKeyUnusable alert path.
type ProviderProbeManager struct {
	prov kms.Provider
}

var _ KeyManager = (*ProviderProbeManager)(nil)

// NewProviderProbeManager returns a probe-only KeyManager over prov.
func NewProviderProbeManager(prov kms.Provider) *ProviderProbeManager {
	return &ProviderProbeManager{prov: prov}
}

// NewProviderProbeLifecycle returns a Lifecycle whose probe round-trip
// runs against prov. It is the §12.5 admin-time and continuous-probe
// wiring entry point for the gateway. A nil now selects the
// time.Now().UTC clock the Lifecycle uses for last-probe-success
// stamps.
func NewProviderProbeLifecycle(prov kms.Provider, now func() time.Time) *Lifecycle {
	return NewWithClock(NewProviderProbeManager(prov), now)
}

// Probe implements KeyManager. It performs the §12.5 zero-byte
// encrypt/decrypt round-trip of probeDEK under alias against the
// underlying provider. An unknown alias (the key was never provisioned)
// maps to ErrKeyNotFound; a round-trip whose recovered bytes diverge
// maps to ErrKeyUnavailable; any other provider error is wrapped
// verbatim. Probe is read-only.
//
// spec: §12.5 line 301 (admin-time probe), line 307 (continuous probe).
func (m *ProviderProbeManager) Probe(ctx context.Context, alias string) error {
	wrapped, err := m.prov.WrapDEK(ctx, alias, probeDEK)
	if err != nil {
		if errors.Is(err, kms.ErrUnknownKEK) {
			return fmt.Errorf("%w: alias %q", ErrKeyNotFound, alias)
		}
		return fmt.Errorf("tenantkms: probe wrap %q: %w", alias, err)
	}
	got, err := m.prov.UnwrapDEK(ctx, alias, wrapped)
	if err != nil {
		if errors.Is(err, kms.ErrUnknownKEK) {
			return fmt.Errorf("%w: alias %q", ErrKeyNotFound, alias)
		}
		return fmt.Errorf("tenantkms: probe unwrap %q: %w", alias, err)
	}
	if !bytes.Equal(got, probeDEK) {
		return fmt.Errorf("%w: alias %q probe round-trip mismatch", ErrKeyUnavailable, alias)
	}
	return nil
}

// ProvisionKey implements KeyManager. See ErrControlPlaneUnsupported.
func (m *ProviderProbeManager) ProvisionKey(context.Context, string) (KeyInfo, error) {
	return KeyInfo{}, ErrControlPlaneUnsupported
}

// RotateKey implements KeyManager. See ErrControlPlaneUnsupported.
func (m *ProviderProbeManager) RotateKey(context.Context, string) (KeyInfo, error) {
	return KeyInfo{}, ErrControlPlaneUnsupported
}

// DisableKey implements KeyManager. See ErrControlPlaneUnsupported.
func (m *ProviderProbeManager) DisableKey(context.Context, string) (KeyInfo, error) {
	return KeyInfo{}, ErrControlPlaneUnsupported
}

// DestroyKey implements KeyManager. See ErrControlPlaneUnsupported.
func (m *ProviderProbeManager) DestroyKey(context.Context, string) (KeyInfo, error) {
	return KeyInfo{}, ErrControlPlaneUnsupported
}

// KeyInfoFor implements KeyManager. See ErrControlPlaneUnsupported.
func (m *ProviderProbeManager) KeyInfoFor(context.Context, string) (KeyInfo, error) {
	return KeyInfo{}, ErrControlPlaneUnsupported
}
