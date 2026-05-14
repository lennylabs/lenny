// SPDX-License-Identifier: MIT

package idempotency

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestPropertyHashBodyDeterministic — HashBody is deterministic and
// returns 64 hex characters.
//
// spec: 11.5 (canonical body hash)
// diagnosis: A non-deterministic hash or wrong length means the
//
//	canonicalisation step drifted.
func TestPropertyHashBodyDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		body := rapid.SliceOf(rapid.Byte()).Draw(rt, "body")
		a := HashBody(body)
		b := HashBody(body)
		if a != b {
			rt.Errorf("HashBody not deterministic on body of length %d", len(body))
		}
		if len(a) != 64 {
			rt.Errorf("HashBody length: want 64, got %d", len(a))
		}
	})
}

// TestPropertyHashBodyDistinctOnDistinctInput — two distinct byte
// slices either return distinct hashes OR collide (a 256-bit hash
// collision is astronomically unlikely). We only assert: identical
// input → identical hash (the contrapositive).
//
// spec: 11.5 (collision-resistant hash)
// diagnosis: A property violation here is dramatic — same body, two
//
//	hashes — and means the hash isn't actually canonical.
func TestPropertyHashBodyContrapositive(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		body := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(rt, "body")
		clone := append([]byte(nil), body...)
		if HashBody(body) != HashBody(clone) {
			rt.Errorf("HashBody changed on byte-equivalent clone")
		}
	})
}

// TestPropertyRecordIsExpiredMonotonic — a Record's IsExpired result
// is monotonic in time: once expired, always expired (for fixed
// StoredAt). The reverse for not-yet-expired is also true.
//
// spec: 11.5 (24-hour TTL)
// diagnosis: A non-monotonic expiry decision would let an expired
//
//	record un-expire at a later check.
func TestPropertyRecordIsExpiredMonotonic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		storedAt := time.Unix(rapid.Int64Range(0, 1<<32).Draw(rt, "storedAt"), 0).UTC()
		offsetA := time.Duration(rapid.Int64Range(0, int64(48*time.Hour)).Draw(rt, "offsetA"))
		extra := time.Duration(rapid.Int64Range(0, int64(48*time.Hour)).Draw(rt, "extra"))
		rec := Record{StoredAt: storedAt}
		nowA := storedAt.Add(offsetA)
		nowB := nowA.Add(extra)
		if rec.IsExpired(nowA) && !rec.IsExpired(nowB) {
			rt.Errorf("IsExpired un-expired: expired at %v then not expired at %v", nowA, nowB)
		}
	})
}

// TestPropertyKeyValidateRejectsOverlong — Validate always rejects
// keys longer than MaxKeyLength.
//
// spec: 11.5 (key length cap)
// diagnosis: Accepting an over-length key is a security-relevant bug
//
//	(potential DoS via unbounded memory in the store).
func TestPropertyKeyValidateRejectsOverlong(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(MaxKeyLength+1, MaxKeyLength*4).Draw(rt, "len")
		value := string(make([]byte, n))
		key := Key{TenantID: "acme", Value: value}
		if err := key.Validate(); err == nil {
			rt.Errorf("over-length key (%d bytes) was accepted", n)
		}
	})
}
