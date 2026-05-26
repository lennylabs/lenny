// SPDX-License-Identifier: MIT

package idempotency

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMaxKeyLengthMatchesSpec(t *testing.T) {
	// §11.5 caps the key at 128 characters.
	if MaxKeyLength != 128 {
		t.Errorf("MaxKeyLength: want 128 per §11.5, got %d", MaxKeyLength)
	}
}

func TestTTLMatchesSpec(t *testing.T) {
	// §11.5 retains records for 24 hours.
	if TTL != 24*time.Hour {
		t.Errorf("TTL: want 24h per §11.5, got %v", TTL)
	}
}

func TestKeyValidateAcceptsCanonical(t *testing.T) {
	cases := []Key{
		{TenantID: "acme", Value: "abc-123"},
		{TenantID: "acme", Value: strings.Repeat("a", MaxKeyLength)},
		{TenantID: "globex", Value: "550e8400-e29b-41d4-a716-446655440000"},
	}
	for _, k := range cases {
		if err := k.Validate(); err != nil {
			t.Errorf("Key(%+v).Validate() = %v, want nil", k, err)
		}
	}
}

func TestKeyValidateRejectsOverLengthValue(t *testing.T) {
	k := Key{TenantID: "acme", Value: strings.Repeat("a", MaxKeyLength+1)}
	err := k.Validate()
	var tle *KeyTooLongError
	if !errors.As(err, &tle) {
		t.Fatalf("expected *KeyTooLongError, got %v", err)
	}
	if tle.Length != MaxKeyLength+1 || tle.Max != MaxKeyLength {
		t.Errorf("error fields: %+v", tle)
	}
}

func TestKeyValidateRejectsMissingFields(t *testing.T) {
	for _, k := range []Key{{}, {TenantID: "acme"}, {Value: "x"}} {
		if err := k.Validate(); err == nil {
			t.Errorf("Key(%+v) should reject", k)
		}
	}
}

// spec: §11.5 line 277 — the 128-character cap is measured in runes,
// so a 128-rune multi-byte key is admissible even though its byte
// length exceeds 128. Closes F-11.5.11.
func TestKeyValidateRuneLengthAcceptsMultibyteUpToCap(t *testing.T) {
	// 128 Cyrillic letters; each is 2 UTF-8 bytes, so the byte length
	// is 256 — well over the byte-count cap the prior implementation
	// applied. Validate must accept it.
	value := strings.Repeat("я", MaxKeyLength)
	if got := utf8RuneCount(value); got != MaxKeyLength {
		t.Fatalf("test fixture: want %d runes, got %d", MaxKeyLength, got)
	}
	k := Key{TenantID: "acme", Value: value}
	if err := k.Validate(); err != nil {
		t.Errorf("128-rune multibyte key: Validate = %v, want nil", err)
	}
}

// spec: §11.5 line 277 — 129 runes (one over the cap) must be rejected
// regardless of how many UTF-8 bytes they encode to. Closes F-11.5.11.
func TestKeyValidateRuneLengthRejectsOverCapMultibyte(t *testing.T) {
	value := strings.Repeat("я", MaxKeyLength+1)
	k := Key{TenantID: "acme", Value: value}
	err := k.Validate()
	var tle *KeyTooLongError
	if !errors.As(err, &tle) {
		t.Fatalf("expected *KeyTooLongError, got %v", err)
	}
	if tle.Length != MaxKeyLength+1 {
		t.Errorf("Length = %d, want %d (rune count, not byte count)", tle.Length, MaxKeyLength+1)
	}
}

// utf8RuneCount mirrors utf8.RuneCountInString without importing the
// package in the test file (the spec test only needs it to assert the
// fixture is sized in runes).
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func TestHashBodyIsStable(t *testing.T) {
	a := HashBody([]byte(`{"input":"hello"}`))
	b := HashBody([]byte(`{"input":"hello"}`))
	if a != b {
		t.Errorf("HashBody must be stable for identical input")
	}
	c := HashBody([]byte(`{"input":"world"}`))
	if a == c {
		t.Errorf("HashBody must differ for different input")
	}
	// SHA-256 hex digest is 64 chars.
	if len(a) != 64 {
		t.Errorf("HashBody output: want 64 hex chars, got %d", len(a))
	}
}

// testNow is the deterministic anchor every idempotency test uses
// in place of time.Now(). DetectReuse and IsExpired accept the
// caller's "now" as an explicit argument, so a fixed value
// disconnects the suite from wall-clock without losing semantics.
var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestDetectReuseStoreNewWhenNoPrior(t *testing.T) {
	action, err := DetectReuse(Record{}, HashBody([]byte("body")), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionStoreNew {
		t.Errorf("want store_new, got %q", action)
	}
}

func TestDetectReuseReplayWhenMatch(t *testing.T) {
	stored := Record{
		Key:      Key{TenantID: "acme", Value: "abc"},
		BodyHash: HashBody([]byte("body")),
		StoredAt: testNow,
	}
	action, err := DetectReuse(stored, HashBody([]byte("body")), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != ActionReplay {
		t.Errorf("want replay, got %q", action)
	}
}

func TestDetectReuseRejectsKeyReusedWithDifferentBody(t *testing.T) {
	stored := Record{
		Key:      Key{TenantID: "acme", Value: "abc"},
		BodyHash: HashBody([]byte("original-body")),
		StoredAt: testNow,
	}
	action, err := DetectReuse(stored, HashBody([]byte("different-body")), testNow)
	if action != ActionReject {
		t.Errorf("want reject, got %q", action)
	}
	var re *ReuseError
	if !errors.As(err, &re) {
		t.Fatalf("expected *ReuseError, got %T", err)
	}
	if re.Code() != 422 {
		t.Errorf("Code: want 422, got %d", re.Code())
	}
	if re.ErrorCode() != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("ErrorCode: want IDEMPOTENCY_KEY_REUSED, got %q", re.ErrorCode())
	}
}

func TestDetectReuseTreatsExpiredAsNoPrior(t *testing.T) {
	stored := Record{
		Key:      Key{TenantID: "acme", Value: "abc"},
		BodyHash: HashBody([]byte("body")),
		StoredAt: testNow.Add(-25 * time.Hour), // outside the 24h TTL
	}
	action, err := DetectReuse(stored, HashBody([]byte("different-body")), testNow)
	if err != nil {
		t.Fatalf("expired key reused with different body should not error, got %v", err)
	}
	if action != ActionStoreNew {
		t.Errorf("expired record should be treated as no-prior; want store_new, got %q", action)
	}
}

func TestRecordIsExpired(t *testing.T) {
	now := testNow
	fresh := Record{StoredAt: now.Add(-1 * time.Hour)}
	expired := Record{StoredAt: now.Add(-25 * time.Hour)}
	if fresh.IsExpired(now) {
		t.Errorf("1h-old record must not be expired")
	}
	if !expired.IsExpired(now) {
		t.Errorf("25h-old record must be expired")
	}
}

func TestRecordZeroStoredAtIsNotExpired(t *testing.T) {
	// A zero StoredAt means "no record at all"; IsExpired returns
	// false so DetectReuse falls through to store_new via the
	// empty-key branch rather than the expired branch.
	if (Record{}).IsExpired(testNow) {
		t.Errorf("zero Record must not report IsExpired")
	}
}
