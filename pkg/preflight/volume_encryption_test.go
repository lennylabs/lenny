// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §12.9 line 1050 — global.devMode exempts the volume-encryption
// check entirely, regardless of attestation or prober.
func TestVolumeEncryptionCheckDevModeExempt_spec_12_9_1050(t *testing.T) {
	d := VolumeEncryptionCheck{DevMode: true}.Decide(context.Background())
	if !d.Passed {
		t.Fatalf("dev mode should exempt; got reject: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "devMode") {
		t.Errorf("reason should cite devMode exemption: %q", d.Reason)
	}
}

// spec: §12.9 line 1050 — with no prober the check cannot be performed; the
// posture is non-blocking. An unattested install passes with a WARNING that
// records the attestation obligation; an attested install passes cleanly.
func TestVolumeEncryptionCheckAttestationGate_spec_12_9_1050(t *testing.T) {
	unattested := VolumeEncryptionCheck{}.Decide(context.Background())
	if !unattested.Passed {
		t.Fatalf("unattested cannot-perform must be non-blocking: %s", unattested.Reason)
	}
	if !strings.HasPrefix(unattested.Reason, "WARNING") {
		t.Errorf("unattested cannot-perform should warn: %q", unattested.Reason)
	}
	if !strings.Contains(unattested.Reason, "attestVolumeEncryption") {
		t.Errorf("reason should point at the attestation value: %q", unattested.Reason)
	}

	attested := VolumeEncryptionCheck{Attested: true}.Decide(context.Background())
	if !attested.Passed {
		t.Fatalf("attested install should pass: %s", attested.Reason)
	}
	if strings.HasPrefix(attested.Reason, "WARNING") {
		t.Errorf("attested pass should not warn: %q", attested.Reason)
	}
}

// spec: §12.9 line 1050 — a prober that reports every volume encrypted
// passes; an unencrypted volume fails closed even when attested (the
// attestation only covers the "cannot be performed" case).
func TestVolumeEncryptionCheckProbed_spec_12_9_1050(t *testing.T) {
	allEncrypted := VolumeEncryptionProbeFunc(func(context.Context) ([]VolumeEncryptionResult, error) {
		return []VolumeEncryptionResult{
			{Name: "postgres", Encrypted: true, Detail: "gp3-encrypted"},
			{Name: "redis", Encrypted: true, Detail: "gp3-encrypted"},
		}, nil
	})
	if d := (VolumeEncryptionCheck{Prober: allEncrypted}).Decide(context.Background()); !d.Passed {
		t.Fatalf("all-encrypted should pass: %s", d.Reason)
	}

	oneUnencrypted := VolumeEncryptionProbeFunc(func(context.Context) ([]VolumeEncryptionResult, error) {
		return []VolumeEncryptionResult{
			{Name: "postgres", Encrypted: true},
			{Name: "redis", Encrypted: false, Detail: "standard"},
		}, nil
	})
	// Attested true must not rescue a positively-detected unencrypted volume.
	d := VolumeEncryptionCheck{Prober: oneUnencrypted, Attested: true}.Decide(context.Background())
	if d.Passed {
		t.Error("a detected unencrypted volume must fail closed despite attestation")
	}
	if !strings.Contains(d.Reason, "redis") {
		t.Errorf("reason should name the unencrypted volume: %q", d.Reason)
	}
}

// spec: §12.9 line 1050 — a prober error or an empty result set routes
// through the attestation gate (cannot be performed).
func TestVolumeEncryptionCheckProbeError_spec_12_9_1050(t *testing.T) {
	failing := VolumeEncryptionProbeFunc(func(context.Context) ([]VolumeEncryptionResult, error) {
		return nil, errors.New("cloud provider API unavailable")
	})
	d := VolumeEncryptionCheck{Prober: failing}.Decide(context.Background())
	if !d.Passed || !strings.HasPrefix(d.Reason, "WARNING") {
		t.Errorf("probe error without attestation should be a non-blocking warning: passed=%v %q", d.Passed, d.Reason)
	}
	if d := (VolumeEncryptionCheck{Prober: failing, Attested: true}).Decide(context.Background()); !d.Passed {
		t.Error("probe error with attestation should pass cleanly")
	}

	empty := VolumeEncryptionProbeFunc(func(context.Context) ([]VolumeEncryptionResult, error) {
		return nil, nil
	})
	if d := (VolumeEncryptionCheck{Prober: empty}).Decide(context.Background()); !d.Passed {
		t.Error("empty probe result should be non-blocking")
	}
}
