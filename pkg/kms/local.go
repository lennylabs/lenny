// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sync"
)

// Local is the in-process KEK Provider for the §4 no-cloud
// development path and for tests. It is the fully implemented
// Provider this package ships; cloud KEK providers (see
// CloudProviderSeam) implement the same interface.
//
// Local derives KEK material deterministically from a single root
// seed plus the alias and KEK version, so a given (seed, alias,
// version) triple always yields the same KEK. A WrappedDEK produced
// by one Local instance unwraps on any other Local instance built
// from the same seed, which is what lets multiple Token Service
// replicas built from the same configured seed share encrypted
// state. Local supports the §4.9.1 KEK rotation procedure through
// RotateKEK: a rotation mints a new version, WrapDEK then stamps the
// new version, and UnwrapDEK still recovers DEKs wrapped under any
// prior version.
//
// Local is a development and test KEK. It holds key material in
// process memory and offers no HSM backing, no audit trail, and no
// access control. Production deployments use a cloud KEK provider.
//
// Local is safe for concurrent use.
type Local struct {
	seed []byte

	mu       sync.RWMutex
	versions map[string]int // alias -> current (highest) KEK version
}

var _ Provider = (*Local)(nil)

// localKEKInfo is the HKDF info string domain-separating Local's KEK
// derivation from any other use of the root seed.
const localKEKInfo = "lenny/kms/local/kek/v1"

// NewLocal returns a Local provider deriving KEK material from seed.
// The seed must be at least 32 bytes; NewLocal returns an error for a
// shorter seed so a weak development seed cannot silently weaken the
// KEK. Every alias starts at KEK version 1 on first use.
func NewLocal(seed []byte) (*Local, error) {
	if len(seed) < DEKSize {
		return nil, fmt.Errorf("kms: local KEK seed must be at least %d bytes, got %d", DEKSize, len(seed))
	}
	return &Local{
		seed:     append([]byte(nil), seed...),
		versions: make(map[string]int),
	}, nil
}

// NewLocalRandom returns a Local provider seeded from crypto/rand. It
// is the zero-configuration development KEK: each process gets a
// distinct seed, so encrypted state does not survive a restart. A
// deployment that needs encrypted state to outlive a process, or to
// be shared across replicas, configures a fixed seed through NewLocal.
func NewLocalRandom() (*Local, error) {
	seed := make([]byte, DEKSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("kms: seed local KEK: %w", err)
	}
	return NewLocal(seed)
}

// kekMaterial derives the 32-byte KEK for (alias, version) from the
// root seed via HKDF-SHA256. Distinct aliases and distinct versions
// yield independent keys; the same triple always yields the same key.
func (l *Local) kekMaterial(alias string, version int) ([]byte, error) {
	salt := []byte(fmt.Sprintf("%s\x00%d", alias, version))
	key, err := hkdf.Key(sha256.New, l.seed, salt, localKEKInfo, DEKSize)
	if err != nil {
		return nil, fmt.Errorf("kms: derive local KEK for %q v%d: %w", alias, version, err)
	}
	return key, nil
}

// currentVersion returns the current KEK version for alias,
// provisioning version 1 on first use.
func (l *Local) currentVersion(alias string) int {
	l.mu.RLock()
	v, ok := l.versions[alias]
	l.mu.RUnlock()
	if ok {
		return v
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if v, ok := l.versions[alias]; ok {
		return v
	}
	l.versions[alias] = 1
	return 1
}

// RotateKEK advances the alias to a fresh KEK version and returns the
// new version number. It implements the §4.9.1 KEK rotation step:
// after a rotation WrapDEK stamps the new version, while UnwrapDEK
// still recovers DEKs wrapped under every prior version, so the
// re-encryption job can run while both versions are live. RotateKEK
// provisions the alias at version 1 first if it has never been used,
// so the first rotation yields version 2.
func (l *Local) RotateKEK(alias string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.versions[alias]
	if v == 0 {
		v = 1
	}
	v++
	l.versions[alias] = v
	return v
}

// WrapDEK encrypts dek under the current KEK version for alias using
// AES-256-GCM. The 12-byte GCM nonce is prepended to the ciphertext
// inside the returned WrappedDEK.Ciphertext.
func (l *Local) WrapDEK(_ context.Context, alias string, dek []byte) (WrappedDEK, error) {
	if len(dek) != DEKSize {
		return WrappedDEK{}, fmt.Errorf("kms: DEK must be %d bytes, got %d", DEKSize, len(dek))
	}
	version := l.currentVersion(alias)
	kek, err := l.kekMaterial(alias, version)
	if err != nil {
		return WrappedDEK{}, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return WrappedDEK{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return WrappedDEK{}, fmt.Errorf("kms: wrap nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, dek, []byte(alias))
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return WrappedDEK{KEKVersion: version, Ciphertext: out}, nil
}

// UnwrapDEK recovers the plaintext DEK from a WrappedDEK. It derives
// the KEK for the version recorded on the WrappedDEK, so a DEK wrapped
// under a superseded version still unwraps. The alias is bound into
// the GCM additional data, so a wrapped DEK does not unwrap under a
// different alias even when both alias KEKs derive from the same seed.
func (l *Local) UnwrapDEK(_ context.Context, alias string, wrapped WrappedDEK) ([]byte, error) {
	if wrapped.KEKVersion < 1 {
		return nil, fmt.Errorf("%w: version %d", ErrUnknownKEKVersion, wrapped.KEKVersion)
	}
	// A version above the current one is not derivable: it names a KEK
	// this provider has never minted.
	l.mu.RLock()
	current := l.versions[alias]
	l.mu.RUnlock()
	if current == 0 {
		// The alias has never been used on this provider. Local can
		// still derive any version deterministically, so treat the
		// first use as provisioning version 1 and continue; a version
		// above 1 then fails below.
		current = 1
	}
	if wrapped.KEKVersion > current {
		return nil, fmt.Errorf("%w: version %d, current %d", ErrUnknownKEKVersion, wrapped.KEKVersion, current)
	}
	kek, err := l.kekMaterial(alias, wrapped.KEKVersion)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	if len(wrapped.Ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("%w: %d bytes", ErrMalformedWrappedDEK, len(wrapped.Ciphertext))
	}
	nonce := wrapped.Ciphertext[:gcm.NonceSize()]
	ct := wrapped.Ciphertext[gcm.NonceSize():]
	dek, err := gcm.Open(nil, nonce, ct, []byte(alias))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnwrap, err)
	}
	if len(dek) != DEKSize {
		return nil, fmt.Errorf("%w: unwrapped %d bytes", ErrUnwrap, len(dek))
	}
	return dek, nil
}

// CurrentKEKVersion reports the KEK version WrapDEK currently stamps
// for alias, provisioning version 1 on first use. It never returns
// ErrUnknownKEK: Local can derive a KEK for any alias.
func (l *Local) CurrentKEKVersion(_ context.Context, alias string) (int, error) {
	return l.currentVersion(alias), nil
}

// newGCM builds an AES-256-GCM AEAD from a 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("kms: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("kms: gcm: %w", err)
	}
	return gcm, nil
}
