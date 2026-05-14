// SPDX-License-Identifier: MIT

// Package randctl provides a deterministic seeded RNG keyed by the test
// name. UUIDs, nonces, ULIDs, and bucketing seeds derived from this RNG
// reproduce exactly across runs, eliminating the second most common
// source of flaky distributed-systems tests (the first is uncontrolled
// time; see timectl).
//
// Production code reads randomness from a registry that defaults to
// crypto/rand and points at this package only in tests.
//
// See TESTING.md §10 (Service and cluster provisioning — Time and
// randomness control).
package randctl

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"sync"
	"testing"
)

// Rand is the abstraction every randomness-reading component depends on.
// Tests inject a deterministic implementation; production uses CryptoRand.
type Rand interface {
	// Read fills b with random bytes. Returns len(b) and a nil error
	// (mirroring crypto/rand's io.Reader contract).
	Read(b []byte) (int, error)
	// Uint64 returns a 64-bit unsigned integer.
	Uint64() uint64
	// IntN returns a non-negative pseudo-random number in [0, n).
	// Panics if n <= 0.
	IntN(n int) int
}

// New returns a deterministic Rand whose stream is keyed by the test name
// (t.Name()). Two calls to New within the same test, or two different
// tests with the same name, produce the same stream; different test
// names produce independent streams.
//
// The deterministic stream is intended for test fixtures. Do not use
// this RNG for cryptographic operations under test.
func New(t *testing.T) Rand {
	t.Helper()
	return NewSeeded(deriveSeed(t.Name()))
}

// NewSeeded constructs a Rand from a raw 64-bit seed pair. Useful when
// callers want to override the test-name seeding or replay a failure
// from a captured seed. The two halves of the seed are XOR'd into a
// single int64 since math/rand's Source is 63-bit.
func NewSeeded(seed1, seed2 uint64) Rand {
	src := rand.NewSource(int64(seed1 ^ seed2))
	return &pcgRand{r: rand.New(src)}
}

// CryptoRand returns a Rand backed by crypto/rand. Production-side code
// uses this; tests don't.
func CryptoRand() Rand { return cryptoRandImpl{} }

// deriveSeed turns a test name into a stable (seed1, seed2) pair using
// the first 128 bits of SHA-256(name). Different names always produce
// different seeds; the same name always reproduces.
func deriveSeed(name string) (uint64, uint64) {
	h := sha256.Sum256([]byte(name))
	return binary.LittleEndian.Uint64(h[0:8]), binary.LittleEndian.Uint64(h[8:16])
}

type pcgRand struct {
	mu sync.Mutex
	r  *rand.Rand
}

func (p *pcgRand) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range b {
		b[i] = byte(p.r.Uint64())
	}
	return len(b), nil
}

func (p *pcgRand) Uint64() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.r.Uint64()
}

func (p *pcgRand) IntN(n int) int {
	if n <= 0 {
		panic("randctl: IntN called with n <= 0")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.r.Intn(n)
}

type cryptoRandImpl struct{}

func (cryptoRandImpl) Read(b []byte) (int, error) {
	// import as needed to keep the package light
	return cryptoReader(b)
}

func (cryptoRandImpl) Uint64() uint64 {
	var b [8]byte
	if _, err := cryptoReader(b[:]); err != nil {
		panic("randctl: crypto/rand read failed: " + err.Error())
	}
	return binary.LittleEndian.Uint64(b[:])
}

func (cryptoRandImpl) IntN(n int) int {
	if n <= 0 {
		panic("randctl: IntN called with n <= 0")
	}
	return int(cryptoUint64() % uint64(n))
}

// HexN draws n hex-encoded bytes from r. Useful for generating
// ULID/UUID-like identifiers in tests.
func HexN(r Rand, n int) string {
	buf := make([]byte, n)
	if _, err := r.Read(buf); err != nil {
		panic("randctl: HexN: " + err.Error())
	}
	return hex.EncodeToString(buf)
}
