// SPDX-License-Identifier: MIT

package kms_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// spec: 11.7 (KMS round-trips)
// diagnosis: Decrypt(Encrypt(x)) did not equal x. The stub's AES-GCM
//
//	or nonce handling is broken.
func TestStubRoundTrip(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	plain := []byte("hello tenant data")
	ct, err := k.Encrypt("tenants/acme/cmk-1", plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := k.Decrypt("tenants/acme/cmk-1", ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Errorf("round-trip mismatch: %q vs %q", out, plain)
	}
}

// spec: 11.7 (KMS unavailable fault — fail-closed)
// diagnosis: SetUnavailable did not cause subsequent calls to fail.
//
//	The fault-injection toggle is wrong.
func TestStubUnavailableFault(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	k.SetUnavailable(true)
	if _, err := k.Encrypt("tenants/acme/cmk-1", []byte("x")); !errors.Is(err, kms.ErrUnavailable) {
		t.Errorf("encrypt under unavailable: want ErrUnavailable, got %v", err)
	}
	k.SetUnavailable(false)
	if _, err := k.Encrypt("tenants/acme/cmk-1", []byte("x")); err != nil {
		t.Errorf("encrypt after recovery: %v", err)
	}
}

// spec: 11.7 (per-alias deletion → ErrKeyRevoked)
// diagnosis: DeleteAlias did not transition that alias to revoked.
//
//	Subsequent operations should fail closed.
func TestStubDeleteAlias(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	k.DeleteAlias("tenants/acme/cmk-1")
	if _, err := k.Encrypt("tenants/acme/cmk-1", []byte("x")); !errors.Is(err, kms.ErrKeyRevoked) {
		t.Errorf("encrypt under revoked: want ErrKeyRevoked, got %v", err)
	}
	// Other aliases unaffected.
	if _, err := k.Encrypt("tenants/globex/cmk-1", []byte("x")); err != nil {
		t.Errorf("untouched alias: %v", err)
	}
}

// spec: 11.7 (Probe surfaces faults without doing any work)
// diagnosis: Probe returned nil when the alias was denied. The
//
//	probe path is not consulting the deny list.
func TestStubProbe(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	k.DenyAlias("tenants/acme/cmk-1")
	if err := k.Probe("tenants/acme/cmk-1"); !errors.Is(err, kms.ErrAccessDenied) {
		t.Errorf("probe on denied alias: want ErrAccessDenied, got %v", err)
	}
}

// spec: 11.7 (KMS rotation log carries the §16.1 trigger label)
// diagnosis: RotateKey must record Trigger so the lenny_kms_
//
//	rotation_total{trigger=...} metric label assertions
//	have a source of truth.
func TestStubRotationLog(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	if _, err := k.Encrypt("tenants/acme/cmk-1", []byte("a")); err != nil {
		t.Fatalf("seed encrypt: %v", err)
	}
	if _, err := k.RotateKey("tenants/acme/cmk-1", kms.TriggerProactiveRenewal); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := k.RotateKey("tenants/acme/cmk-1", kms.TriggerFaultRateLimited); err != nil {
		t.Fatalf("rotate 2: %v", err)
	}
	rots := k.Rotations()
	if len(rots) != 2 {
		t.Fatalf("Rotations count: want 2; got %d", len(rots))
	}
	if rots[0].Trigger != kms.TriggerProactiveRenewal || rots[1].Trigger != kms.TriggerFaultRateLimited {
		t.Errorf("triggers: got %v / %v", rots[0].Trigger, rots[1].Trigger)
	}
	if rots[0].NewID != 2 || rots[1].NewID != 3 {
		t.Errorf("version IDs: got %d / %d; want 2 / 3", rots[0].NewID, rots[1].NewID)
	}
}

// spec: 11.7 (rotation gate — prior version is decryptable
//
//	inside the in-flight grace window)
// diagnosis: A ciphertext minted under v1 must Decrypt cleanly
//
//	after a rotation to v2, until the gate ceiling passes.
func TestStubDecryptPriorVersionWithinGate(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	ct, err := k.Encrypt("tenants/acme/cmk-1", []byte("old plaintext"))
	if err != nil {
		t.Fatalf("encrypt v1: %v", err)
	}
	if _, err := k.RotateKey("tenants/acme/cmk-1", kms.TriggerManual); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	pt, err := k.Decrypt("tenants/acme/cmk-1", ct)
	if err != nil {
		t.Fatalf("decrypt prior version (gate open): %v", err)
	}
	if string(pt) != "old plaintext" {
		t.Errorf("decrypt result: got %q; want %q", pt, "old plaintext")
	}
}

// spec: 11.7 (rotation gate — prior version rejected after expiry)
// diagnosis: After the §11.7 in-flight gate ceiling passes,
//
//	stale ciphertext must surface as ErrRotationGateExpired.
func TestStubDecryptPriorVersionAfterGate(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	ct, err := k.Encrypt("tenants/acme/cmk-1", []byte("old plaintext"))
	if err != nil {
		t.Fatalf("encrypt v1: %v", err)
	}
	// SetRotationGate(-1) forces an immediate-expiry gate that's
	// then activated by the next RotateKey call. We override
	// AFTER the rotation so the gate is already past expiry.
	if _, err := k.RotateKey("tenants/acme/cmk-1", kms.TriggerManual); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	k.SetRotationGate("tenants/acme/cmk-1", -1)
	_, err = k.Decrypt("tenants/acme/cmk-1", ct)
	if !errors.Is(err, kms.ErrRotationGateExpired) {
		t.Errorf("decrypt after gate: want ErrRotationGateExpired; got %v", err)
	}
}

// spec: 11.7 (ListKeyVersions catalog)
// diagnosis: ListKeyVersions must return the rotation history
//
//	so operability tooling can render the §16.1 metric.
func TestStubListKeyVersions(t *testing.T) {
	t.Parallel()
	k := kms.New(t)
	_, _ = k.Encrypt("tenants/acme/cmk-1", []byte("seed"))
	_, _ = k.RotateKey("tenants/acme/cmk-1", kms.TriggerManual)
	_, _ = k.RotateKey("tenants/acme/cmk-1", kms.TriggerProactiveRenewal)
	versions := k.ListKeyVersions("tenants/acme/cmk-1")
	if len(versions) != 3 {
		t.Fatalf("ListKeyVersions: want 3; got %d", len(versions))
	}
	for i, v := range versions {
		if v.ID != i+1 {
			t.Errorf("versions[%d].ID = %d; want %d", i, v.ID, i+1)
		}
	}
}
