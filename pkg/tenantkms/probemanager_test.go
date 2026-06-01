// SPDX-License-Identifier: MIT

package tenantkms_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// fakeProvider is a kms.Provider double for the §12.5 ProviderProbeManager
// tests. WrapErr / UnwrapErr inject provider-side failures; a nil pair
// performs a faithful round-trip (the returned WrappedDEK echoes the DEK
// so UnwrapDEK recovers it) so the success path is exercised without a
// real KMS.
type fakeProvider struct {
	wrapErr   error
	unwrapErr error
	wrapped   []byte // captured plaintext, echoed back by UnwrapDEK
	corrupt   bool   // when true, UnwrapDEK returns altered bytes
}

func (f *fakeProvider) WrapDEK(_ context.Context, _ string, dek []byte) (kms.WrappedDEK, error) {
	if f.wrapErr != nil {
		return kms.WrappedDEK{}, f.wrapErr
	}
	f.wrapped = append([]byte(nil), dek...)
	return kms.WrappedDEK{KEKVersion: 1, Ciphertext: f.wrapped}, nil
}

func (f *fakeProvider) UnwrapDEK(_ context.Context, _ string, w kms.WrappedDEK) ([]byte, error) {
	if f.unwrapErr != nil {
		return nil, f.unwrapErr
	}
	out := append([]byte(nil), w.Ciphertext...)
	if f.corrupt && len(out) > 0 {
		out[0] ^= 0xff
	}
	return out, nil
}

func (f *fakeProvider) CurrentKEKVersion(context.Context, string) (int, error) { return 1, nil }

// spec: §12.5 line 301 / line 307 — the ProviderProbeManager backs the
// admin-time and continuous probe over the gateway's resolved
// kms.Provider.

func TestProviderProbeManagerRoundTripSucceeds_spec_12_5_307(t *testing.T) {
	mgr := tenantkms.NewProviderProbeManager(&fakeProvider{})
	if err := mgr.Probe(context.Background(), tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("Probe round-trip: %v", err)
	}
}

func TestProviderProbeManagerLocalRandomSucceeds_spec_12_5_307(t *testing.T) {
	// kms.Local derives a KEK for any alias, so the dev-mode provider
	// always satisfies the probe — the local KMS is always available.
	prov, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	mgr := tenantkms.NewProviderProbeManager(prov)
	if err := mgr.Probe(context.Background(), tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("Probe over kms.Local: %v", err)
	}
}

func TestProviderProbeManagerUnknownKEKMapsToKeyNotFound_spec_12_5_301(t *testing.T) {
	mgr := tenantkms.NewProviderProbeManager(&fakeProvider{wrapErr: kms.ErrUnknownKEK})
	err := mgr.Probe(context.Background(), tenantkms.AliasFor("bob"))
	if !errors.Is(err, tenantkms.ErrKeyNotFound) {
		t.Fatalf("Probe unknown KEK = %v, want ErrKeyNotFound", err)
	}
}

func TestProviderProbeManagerWrapErrorIsFailClosed_spec_12_5_301(t *testing.T) {
	// A transport-layer provider error must surface as a non-nil probe
	// result so the Lifecycle maps it to CLASSIFICATION_CONTROL_VIOLATION.
	boom := errors.New("kms backend unreachable")
	mgr := tenantkms.NewProviderProbeManager(&fakeProvider{wrapErr: boom})
	if err := mgr.Probe(context.Background(), tenantkms.AliasFor("acme")); err == nil {
		t.Fatal("Probe with backend error returned nil, want fail-closed error")
	}
}

func TestProviderProbeManagerRoundTripMismatchUnavailable_spec_12_5_307(t *testing.T) {
	mgr := tenantkms.NewProviderProbeManager(&fakeProvider{corrupt: true})
	err := mgr.Probe(context.Background(), tenantkms.AliasFor("acme"))
	if !errors.Is(err, tenantkms.ErrKeyUnavailable) {
		t.Fatalf("Probe round-trip mismatch = %v, want ErrKeyUnavailable", err)
	}
}

func TestProviderProbeManagerControlPlaneUnsupported_spec_12_5_301(t *testing.T) {
	mgr := tenantkms.NewProviderProbeManager(&fakeProvider{})
	ctx := context.Background()
	alias := tenantkms.AliasFor("acme")
	ops := map[string]func() error{
		"ProvisionKey": func() error { _, e := mgr.ProvisionKey(ctx, alias); return e },
		"RotateKey":    func() error { _, e := mgr.RotateKey(ctx, alias); return e },
		"DisableKey":   func() error { _, e := mgr.DisableKey(ctx, alias); return e },
		"DestroyKey":   func() error { _, e := mgr.DestroyKey(ctx, alias); return e },
		"KeyInfoFor":   func() error { _, e := mgr.KeyInfoFor(ctx, alias); return e },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, tenantkms.ErrControlPlaneUnsupported) {
			t.Errorf("%s = %v, want ErrControlPlaneUnsupported", name, err)
		}
	}
}

func TestProviderProbeLifecycleStampsLastSuccess_spec_12_5_307(t *testing.T) {
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	lc := tenantkms.NewProviderProbeLifecycle(&fakeProvider{}, func() time.Time { return now })
	if err := lc.ProbeAvailability(context.Background(), "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("ProbeAvailability: %v", err)
	}
	got, ok := lc.LastProbeSuccess("acme")
	if !ok || !got.Equal(now) {
		t.Fatalf("LastProbeSuccess = %s ok=%v, want %s", got, ok, now)
	}
}
