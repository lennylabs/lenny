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
