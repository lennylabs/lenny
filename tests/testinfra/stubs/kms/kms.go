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
	versions    map[string][]KeyVersion // alias → ordered key versions; latest is the current encrypt key
	deleted     map[string]time.Time
	deniedFor   map[string]bool
	unavailable bool
	// gateExpiry tracks the per-alias §11.7 in-flight gate ceiling.
	// When a rotation lands, the previous version is still
	// decryptable until gateExpiry passes. After that, decrypts
	// against the prior version fail with ErrRotationGateExpired.
	gateExpiry map[string]time.Time
	// rotationLog records every rotation for §16.1 metric assertions.
	rotationLog []RotationEvent
}

// KeyVersion is one entry in an alias's version history.
type KeyVersion struct {
	ID        int       // 1-indexed; increments on every RotateKey
	Material  []byte    // 32-byte key bytes; tests usually inspect ID only
	CreatedAt time.Time // when the version was minted (real wall-clock; tests can override via SetClock if added)
}

// RotationTrigger names why a rotation happened. Matches the
// §11.7 / §4.9.2 trigger enum so audit-event assertions can
// pin the value directly.
type RotationTrigger string

const (
	TriggerProactiveRenewal  RotationTrigger = "proactive_renewal"
	TriggerFaultRateLimited  RotationTrigger = "fault_rate_limited"
	TriggerManual            RotationTrigger = "manual"
	TriggerCeilingHit        RotationTrigger = "ceiling_hit"
	TriggerComplianceMandate RotationTrigger = "compliance_mandate"
)

// RotationEvent captures one observed rotation. Tests pull
// rotationLog via Rotations() to assert the §16.1
// lenny_kms_rotation_total{trigger} metric will fire correctly.
type RotationEvent struct {
	Alias     string
	NewID     int
	Trigger   RotationTrigger
	Timestamp time.Time
}

// New returns a KMS stub. New does not start a network listener;
// callers pass the *Stub directly to test code via dependency
// injection.
func New(t testing.TB) *Stub {
	t.Helper()
	return &Stub{
		versions:   make(map[string][]KeyVersion),
		deleted:    make(map[string]time.Time),
		deniedFor:  make(map[string]bool),
		gateExpiry: make(map[string]time.Time),
	}
}

// ErrUnavailable models a KMS-down condition.
var ErrUnavailable = errors.New("kms stub: unavailable")

// ErrKeyRevoked models a deleted key.
var ErrKeyRevoked = errors.New("kms stub: key revoked")

// ErrAccessDenied models an IAM denial.
var ErrAccessDenied = errors.New("kms stub: access denied")

// ErrRotationGateExpired models a decrypt attempt against a prior
// key version after the §11.7 in-flight gate ceiling has passed.
// Production callers see this when stale ciphertext is decrypted
// long after the issuing key was rotated out.
var ErrRotationGateExpired = errors.New("kms stub: rotation grace window expired")

// ErrUnknownAlias models a decrypt against an alias that's never
// had a key minted.
var ErrUnknownAlias = errors.New("kms stub: unknown alias")

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

// Encrypt encrypts plaintext under the alias's current key version.
// The first call to a given alias auto-provisions a fresh 32-byte
// key. The returned blob is the version ID (1 byte) || nonce ||
// ciphertext so Decrypt can pick the right version.
func (s *Stub) Encrypt(alias string, plaintext []byte) ([]byte, error) {
	if err := s.checkFaults(alias); err != nil {
		return nil, err
	}
	ver := s.currentVersion(alias)
	block, _ := aes.NewCipher(ver.Material)
	g, _ := cipher.NewGCM(block)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := g.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+len(nonce)+len(ct))
	out = append(out, byte(ver.ID))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt is the inverse of Encrypt. The version ID embedded in
// the blob picks the key version; if that version is not the
// current one and the §11.7 in-flight gate has expired, returns
// ErrRotationGateExpired.
func (s *Stub) Decrypt(alias string, packed []byte) ([]byte, error) {
	if err := s.checkFaults(alias); err != nil {
		return nil, err
	}
	if len(packed) < 13 { // 1 (version) + 12 (GCM nonce minimum)
		return nil, errors.New("kms stub: ciphertext too short")
	}
	versionID := int(packed[0])
	body := packed[1:]
	ver, err := s.versionByID(alias, versionID)
	if err != nil {
		return nil, err
	}
	// If a rotation happened and the in-flight gate has elapsed,
	// stale ciphertext under an older version is rejected.
	s.mu.Lock()
	gateOK := true
	if !ver.isCurrent {
		if expiry, ok := s.gateExpiry[alias]; ok && !expiry.IsZero() && time.Now().After(expiry) {
			gateOK = false
		}
	}
	s.mu.Unlock()
	if !gateOK {
		return nil, ErrRotationGateExpired
	}
	block, _ := aes.NewCipher(ver.Material)
	g, _ := cipher.NewGCM(block)
	if len(body) < g.NonceSize() {
		return nil, errors.New("kms stub: ciphertext too short")
	}
	nonce, ct := body[:g.NonceSize()], body[g.NonceSize():]
	return g.Open(nil, nonce, ct, nil)
}

// RotateKey mints a new version for the alias. Trigger names
// why the rotation happened; the value lands on the rotation
// log so tests can assert the §16.1 metric label.
//
// The §11.7 in-flight gate ceiling defaults to 5 minutes (the
// §11.7 documented grace window); callers can override via
// SetRotationGate before calling RotateKey.
func (s *Stub) RotateKey(alias string, trigger RotationTrigger) (KeyVersion, error) {
	if err := s.checkFaults(alias); err != nil {
		return KeyVersion{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.versions[alias]
	prevID := 0
	if len(existing) > 0 {
		prevID = existing[len(existing)-1].ID
	}
	newVer := KeyVersion{
		ID:        prevID + 1,
		Material:  deriveKey(alias, prevID+1),
		CreatedAt: time.Now(),
	}
	s.versions[alias] = append(existing, newVer)
	if prevID > 0 {
		// On rotation, start the in-flight grace window unless one
		// is already set. Tests can pre-set via SetRotationGate.
		if _, set := s.gateExpiry[alias]; !set {
			s.gateExpiry[alias] = time.Now().Add(5 * time.Minute)
		}
	}
	s.rotationLog = append(s.rotationLog, RotationEvent{
		Alias:     alias,
		NewID:     newVer.ID,
		Trigger:   trigger,
		Timestamp: newVer.CreatedAt,
	})
	return newVer, nil
}

// ListKeyVersions returns every version minted for the alias, in
// chronological order. Tests can assert the rotation history
// matches expectations.
func (s *Stub) ListKeyVersions(alias string) []KeyVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KeyVersion, len(s.versions[alias]))
	copy(out, s.versions[alias])
	return out
}

// SetRotationGate overrides the default 5-minute in-flight grace
// window for the alias. Passing 0 disables the gate (every prior
// version is decryptable indefinitely). Passing -1 forces every
// prior version to be immediately stale.
func (s *Stub) SetRotationGate(alias string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case d < 0:
		s.gateExpiry[alias] = time.Now().Add(-time.Second)
	case d == 0:
		delete(s.gateExpiry, alias)
	default:
		s.gateExpiry[alias] = time.Now().Add(d)
	}
}

// Rotations returns the rotation history across every alias.
// Useful for asserting the §16.1 metric label distribution.
func (s *Stub) Rotations() []RotationEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RotationEvent, len(s.rotationLog))
	copy(out, s.rotationLog)
	return out
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

// currentVersion returns (and auto-provisions) the latest version
// for the alias. The first call mints version 1.
func (s *Stub) currentVersion(alias string) KeyVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vs, ok := s.versions[alias]; ok && len(vs) > 0 {
		return vs[len(vs)-1]
	}
	v := KeyVersion{
		ID:        1,
		Material:  deriveKey(alias, 1),
		CreatedAt: time.Now(),
	}
	s.versions[alias] = []KeyVersion{v}
	return v
}

// stubVersion is currentVersion's return type extended with an
// isCurrent flag the Decrypt path needs to decide whether the
// rotation gate applies.
type stubVersion struct {
	KeyVersion
	isCurrent bool
}

// versionByID looks up a specific historic version by its 1-indexed
// ID. Returns ErrUnknownAlias when the alias is empty, or a wrapped
// error when the version is out of range.
func (s *Stub) versionByID(alias string, id int) (stubVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs, ok := s.versions[alias]
	if !ok || len(vs) == 0 {
		return stubVersion{}, ErrUnknownAlias
	}
	if id < 1 || id > len(vs) {
		return stubVersion{}, errors.New("kms stub: unknown key version")
	}
	v := vs[id-1]
	return stubVersion{KeyVersion: v, isCurrent: id == vs[len(vs)-1].ID}, nil
}

// deriveKey computes a deterministic 32-byte key for the
// (alias, version) pair. Same input → same bytes across runs, so
// cached fixtures keep working.
func deriveKey(alias string, version int) []byte {
	h := sha256.Sum256([]byte("lenny-kms-stub-salt:" + alias + ":v" + intToString(version)))
	return h[:]
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
