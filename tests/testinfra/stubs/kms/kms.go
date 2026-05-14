// SPDX-License-Identifier: MIT

// Package kms is a minimal KMS stub for tier-2 component and tier-4
// integration tests. It models the envelope-encryption surface
// Lenny relies on: a per-tenant CMK alias resolves to a 32-byte key
// material; Encrypt/Decrypt round-trip ciphertext under XSalsa20-Poly1305-
// equivalent semantics (we use AES-GCM with the same per-tenant key
// for simplicity; the real KMS adapter abstracts the algorithm).
//
// The stub also models KMS faults the gateway must handle gracefully:
//
//   - SetUnavailable(true) → every call returns ErrUnavailable
//   - DenyAlias(alias) → that alias returns ErrAccessDenied
//   - DeletedAt(alias) → that alias returns ErrKeyRevoked
//
// Production-grade behavior (regional replication, asymmetric keys,
// HSM-backed signing) is intentionally absent. This stub is for
// tests that need a KMS to exist, not for tests that need to
// validate KMS-vendor parity.
package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"
)

// Stub is the in-process KMS.
type Stub struct {
	mu          sync.Mutex
	keys        map[string][]byte // alias → 32-byte key
	deleted     map[string]time.Time
	deniedFor   map[string]bool
	unavailable bool
}

// New returns a KMS stub. New does not start a network listener;
// callers pass the *Stub directly to test code via dependency
// injection.
func New(t testing.TB) *Stub {
	t.Helper()
	return &Stub{
		keys:      make(map[string][]byte),
		deleted:   make(map[string]time.Time),
		deniedFor: make(map[string]bool),
	}
}

// ErrUnavailable models a KMS-down condition.
var ErrUnavailable = errors.New("kms stub: unavailable")

// ErrKeyRevoked models a deleted key.
var ErrKeyRevoked = errors.New("kms stub: key revoked")

// ErrAccessDenied models an IAM denial.
var ErrAccessDenied = errors.New("kms stub: access denied")

// SetUnavailable toggles the fault-injection flag.
func (s *Stub) SetUnavailable(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unavailable = v
}

// DenyAlias makes the given alias return ErrAccessDenied.
func (s *Stub) DenyAlias(alias string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deniedFor[alias] = true
}

// DeleteAlias marks the alias as revoked at the current time.
func (s *Stub) DeleteAlias(alias string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted[alias] = time.Now()
}

// Encrypt encrypts plaintext under the alias's key. The first call
// to a given alias auto-provisions a fresh 32-byte key. Returns
// (ciphertext || nonce) so Decrypt can split.
func (s *Stub) Encrypt(alias string, plaintext []byte) ([]byte, error) {
	if err := s.checkFaults(alias); err != nil {
		return nil, err
	}
	key := s.keyFor(alias)
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := g.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ct...), nil
}

// Decrypt is the inverse of Encrypt.
func (s *Stub) Decrypt(alias string, packed []byte) ([]byte, error) {
	if err := s.checkFaults(alias); err != nil {
		return nil, err
	}
	key := s.keyFor(alias)
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	if len(packed) < g.NonceSize() {
		return nil, errors.New("kms stub: ciphertext too short")
	}
	nonce, ct := packed[:g.NonceSize()], packed[g.NonceSize():]
	return g.Open(nil, nonce, ct, nil)
}

// Probe models the §11.7 KMS probe (the gateway pings the KMS to
// detect key-unavailable conditions). Returns nil when the alias is
// reachable.
func (s *Stub) Probe(alias string) error {
	return s.checkFaults(alias)
}

func (s *Stub) checkFaults(alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return ErrUnavailable
	}
	if s.deniedFor[alias] {
		return ErrAccessDenied
	}
	if _, ok := s.deleted[alias]; ok {
		return ErrKeyRevoked
	}
	return nil
}

func (s *Stub) keyFor(alias string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[alias]; ok {
		return k
	}
	// Deterministic per-alias key from a fixed salt + alias so two
	// runs with the same alias produce the same key. This means
	// tests can encrypt in one process and decrypt in another (e.g.,
	// for cached fixtures).
	h := sha256.Sum256([]byte("lenny-kms-stub-salt:" + alias))
	key := h[:]
	s.keys[alias] = key
	return key
}
