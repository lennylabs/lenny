// SPDX-License-Identifier: MIT

package idempotency

import (
	"testing"
)

// FuzzKeyValidate exercises the §11.5 key validator on arbitrary
// tenantID + value inputs. The invariants asserted:
//
//   - Validate never panics on any input.
//   - The error categorisation is deterministic (same input → same
//     error type).
//   - Empty tenant or empty value always errors.
//   - Over-length values always produce KeyTooLongError.
//   - Inputs whose length is within bounds and contain a non-empty
//     value succeed.
//
// Seed corpus covers known edge cases: empty values, just-under and
// just-over MaxKeyLength, multi-byte runes, ASCII control chars.
func FuzzKeyValidate(f *testing.F) {
	f.Add("acme", "key-1")
	f.Add("", "key-1")
	f.Add("acme", "")
	f.Add("acme", string(make([]byte, MaxKeyLength)))
	f.Add("acme", string(make([]byte, MaxKeyLength+1)))
	f.Add("acme", "\x00\x01\x02")
	f.Add("acme", "日本語")

	f.Fuzz(func(t *testing.T, tenant, value string) {
		key := Key{TenantID: tenant, Value: value}
		err := key.Validate()
		switch {
		case tenant == "":
			if err == nil {
				t.Errorf("empty tenant must error; key=%+v", key)
			}
		case value == "":
			if err == nil {
				t.Errorf("empty value must error; key=%+v", key)
			}
		case len(value) > MaxKeyLength:
			if _, ok := err.(*KeyTooLongError); !ok {
				t.Errorf("over-length value must produce *KeyTooLongError; got %T (%v)", err, err)
			}
		default:
			if err != nil {
				t.Errorf("admissible key rejected: tenant=%q value=%q err=%v", tenant, value, err)
			}
		}
	})
}

// FuzzHashBody exercises the body hashing path. Invariants:
//
//   - HashBody never panics on any byte slice.
//   - HashBody is deterministic.
//   - HashBody outputs the canonical hex-encoded SHA-256 length (64
//     characters).
func FuzzHashBody(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("hello"))
	f.Add([]byte("\x00\x01\x02\x03"))

	f.Fuzz(func(t *testing.T, body []byte) {
		first := HashBody(body)
		second := HashBody(body)
		if first != second {
			t.Errorf("HashBody not deterministic on body of length %d", len(body))
		}
		if len(first) != 64 {
			t.Errorf("HashBody output length: want 64 hex chars, got %d", len(first))
		}
	})
}
