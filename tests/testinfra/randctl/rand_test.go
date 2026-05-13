// SPDX-License-Identifier: MIT

package randctl_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/randctl"
)

// spec: 10 (TESTING.md §10)
// diagnosis: Two calls to randctl.New(t) within the same test produced
//
//	different streams. The seed is derived from t.Name(); same
//	name must yield the same stream.
func TestNewSameNameSameStream(t *testing.T) {
	t.Parallel()
	r1 := randctl.New(t)
	r2 := randctl.New(t)
	for i := 0; i < 32; i++ {
		a := r1.Uint64()
		b := r2.Uint64()
		if a != b {
			t.Fatalf("same-name streams diverged at i=%d: %x vs %x", i, a, b)
		}
	}
}

// spec: 10
// diagnosis: Different test names produced the same stream. The seed
//
//	derivation must spread distinct names to distinct seeds.
func TestNewDifferentNamesDifferStreams(t *testing.T) {
	t.Parallel()
	t.Run("alpha", func(t *testing.T) {
		t.Parallel()
		rA := randctl.New(t)
		t.Run("beta", func(t *testing.T) {
			t.Parallel()
			rB := randctl.New(t)
			// Compare on the first draw; collision probability is ~2^-64.
			if rA.Uint64() == rB.Uint64() {
				t.Errorf("distinct-name streams collided on first draw")
			}
		})
	})
}

// spec: 10
// diagnosis: NewSeeded did not reproduce. A captured seed pair must
//
//	regenerate the same stream byte-for-byte.
func TestNewSeededReproduces(t *testing.T) {
	t.Parallel()
	const a, b uint64 = 0xdeadbeef, 0xfeedfacecafef00d
	r1 := randctl.NewSeeded(a, b)
	r2 := randctl.NewSeeded(a, b)
	for i := 0; i < 32; i++ {
		if r1.Uint64() != r2.Uint64() {
			t.Fatalf("seeded streams diverged at i=%d", i)
		}
	}
}

// spec: 10
// diagnosis: IntN(0) or IntN(-1) did not panic. IntN must panic on
//
//	non-positive bounds (crypto/rand semantics).
func TestIntNPanicsOnNonPositive(t *testing.T) {
	t.Parallel()
	r := randctl.New(t)
	for _, n := range []int{0, -1, -1000} {
		n := n
		t.Run("", func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Errorf("IntN(%d) did not panic", n)
				}
			}()
			r.IntN(n)
		})
	}
}

// spec: 10
// diagnosis: HexN produced a string of unexpected length. n bytes hex-
//
//	encoded is 2n characters.
func TestHexNLength(t *testing.T) {
	t.Parallel()
	r := randctl.New(t)
	for _, n := range []int{0, 4, 16, 32} {
		got := randctl.HexN(r, n)
		if len(got) != 2*n {
			t.Errorf("HexN(r, %d) length = %d, want %d", n, len(got), 2*n)
		}
	}
}

// spec: 10
// diagnosis: CryptoRand returned crypto/rand failures. Smoke-only: we
//
//	just confirm the interface methods don't panic.
func TestCryptoRandSmokeOk(t *testing.T) {
	t.Parallel()
	r := randctl.CryptoRand()
	buf := make([]byte, 16)
	if _, err := r.Read(buf); err != nil {
		t.Fatalf("CryptoRand.Read: %v", err)
	}
	_ = r.Uint64()
	_ = r.IntN(100)
}
