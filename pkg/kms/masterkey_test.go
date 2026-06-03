// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// spec: §17.4 line 163 — the file-backed master key persists, so a
// second load returns the identical seed (the property that lets
// encrypted state survive a restart).
func TestLoadOrCreateMasterKey_persistsAcrossLoads_spec_17_4_163(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kms", "master.key")
	first, err := LoadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(first) < DEKSize {
		t.Fatalf("seed is %d bytes, want >= %d", len(first), DEKSize)
	}
	second, err := LoadOrCreateMasterKey(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second load returned a different seed; the key did not persist")
	}
}

// The created key file is mode 0600 (owner-only) so the master key is
// not world-readable on a shared host.
func TestLoadOrCreateMasterKey_fileMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := LoadOrCreateMasterKey(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("master key mode = %v, want 0600", perm)
	}
}

// A short pre-existing key is a fatal misconfiguration rather than a
// silent regeneration that would orphan ciphertext.
func TestLoadOrCreateMasterKey_rejectsShortKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("seed short key: %v", err)
	}
	if _, err := LoadOrCreateMasterKey(path); err == nil {
		t.Fatal("expected an error for a short master key, got nil")
	}
}

func TestLoadOrCreateMasterKey_emptyPath(t *testing.T) {
	if _, err := LoadOrCreateMasterKey(""); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

// spec: §17.4 line 163 — a DEK wrapped by one process unwraps in a
// second process built from the same key file, which is the whole point
// of a persisted master key (NewLocalRandom cannot do this).
func TestNewLocalFromKeyFile_unwrapsAcrossInstances_spec_17_4_163(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	ctx := context.Background()
	const alias = "tenant:acme"

	p1, err := NewLocalFromKeyFile(path)
	if err != nil {
		t.Fatalf("first provider: %v", err)
	}
	dek := make([]byte, DEKSize)
	for i := range dek {
		dek[i] = byte(i)
	}
	wrapped, err := p1.WrapDEK(ctx, alias, dek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	// A second provider built from the same file (simulating a restart).
	p2, err := NewLocalFromKeyFile(path)
	if err != nil {
		t.Fatalf("second provider: %v", err)
	}
	got, err := p2.UnwrapDEK(ctx, alias, wrapped)
	if err != nil {
		t.Fatalf("unwrap after restart: %v", err)
	}
	if string(got) != string(dek) {
		t.Fatal("unwrapped DEK differs from the original; the master key did not persist")
	}
}

// A fresh random provider cannot unwrap a DEK from a different seed —
// the regression the file-backed key exists to fix.
func TestNewLocalRandom_doesNotUnwrapForeignDEK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	ctx := context.Background()
	const alias = "tenant:acme"
	persisted, err := NewLocalFromKeyFile(path)
	if err != nil {
		t.Fatalf("persisted provider: %v", err)
	}
	dek := make([]byte, DEKSize)
	wrapped, err := persisted.WrapDEK(ctx, alias, dek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	random, err := NewLocalRandom()
	if err != nil {
		t.Fatalf("random provider: %v", err)
	}
	if _, err := random.UnwrapDEK(ctx, alias, wrapped); err == nil {
		t.Fatal("a random provider unexpectedly unwrapped a foreign-seed DEK")
	}
}
