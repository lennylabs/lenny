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

func TestDetectReuseStoreNewWhenNoPrior(t *testing.T) {
	action, err := DetectReuse(Record{}, HashBody([]byte("body")), time.Now())
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
		StoredAt: time.Now(),
	}
	action, err := DetectReuse(stored, HashBody([]byte("body")), time.Now())
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
		StoredAt: time.Now(),
	}
	action, err := DetectReuse(stored, HashBody([]byte("different-body")), time.Now())
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
		StoredAt: time.Now().Add(-25 * time.Hour), // outside the 24h TTL
	}
	action, err := DetectReuse(stored, HashBody([]byte("different-body")), time.Now())
	if err != nil {
		t.Fatalf("expired key reused with different body should not error, got %v", err)
	}
	if action != ActionStoreNew {
		t.Errorf("expired record should be treated as no-prior; want store_new, got %q", action)
	}
}

func TestRecordIsExpired(t *testing.T) {
	now := time.Now()
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
	if (Record{}).IsExpired(time.Now()) {
		t.Errorf("zero Record must not report IsExpired")
	}
}
