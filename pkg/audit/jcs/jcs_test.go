// SPDX-License-Identifier: MIT

package jcs

import (
	"encoding/json"
	"testing"
)

// canon is a test helper that canonicalizes raw and fails on error.
func canon(t *testing.T, raw string) string {
	t.Helper()
	b, err := Canonicalize([]byte(raw))
	if err != nil {
		t.Fatalf("Canonicalize(%q): %v", raw, err)
	}
	return string(b)
}

// TestCanonicalizeKeyOrder_spec_11_7_364 — object keys are emitted in
// lexicographic order regardless of input order, so two byte forms of
// the same object hash identically. spec: §11.7 item 3 line 364.
func TestCanonicalizeKeyOrder_spec_11_7_364(t *testing.T) {
	t.Parallel()
	a := canon(t, `{"b":1,"a":2,"c":3}`)
	b := canon(t, `{"c":3,"a":2,"b":1}`)
	if a != b {
		t.Fatalf("key order not canonical: %q != %q", a, b)
	}
	if a != `{"a":2,"b":1,"c":3}` {
		t.Errorf("got %q", a)
	}
}

// TestCanonicalizeNestedKeyOrder_spec_11_7_364 — ordering applies at
// every nesting level. spec: §11.7 item 3 line 364.
func TestCanonicalizeNestedKeyOrder_spec_11_7_364(t *testing.T) {
	t.Parallel()
	got := canon(t, `{"z":{"y":1,"x":2},"a":[{"d":1,"c":2}]}`)
	want := `{"a":[{"c":2,"d":1}],"z":{"x":2,"y":1}}`
	if got != want {
		t.Errorf("nested order: got %q want %q", got, want)
	}
}

// TestCanonicalizeWhitespaceStripped_spec_11_7_364 — insignificant
// whitespace is removed. spec: §11.7 item 3 line 364.
func TestCanonicalizeWhitespaceStripped_spec_11_7_364(t *testing.T) {
	t.Parallel()
	got := canon(t, "{\n  \"a\" : 1,\n  \"b\" : [ 1, 2 ]\n}")
	if got != `{"a":1,"b":[1,2]}` {
		t.Errorf("whitespace: got %q", got)
	}
}

// TestCanonicalizeNumbers_spec_11_7_364 — numbers are emitted in
// ECMAScript shortest form, so trailing zeros and exponent spellings of
// the same value canonicalize identically. This is what makes the hash
// stable across a Postgres jsonb numeric round trip. spec: §11.7 line 364.
func TestCanonicalizeNumbers_spec_11_7_364(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`1`, `1`},
		{`1.0`, `1`},
		{`1.50`, `1.5`},
		{`100`, `100`},
		{`1e2`, `100`},
		{`1E2`, `100`},
		{`0.1`, `0.1`},
		{`-0`, `0`},
		{`-2.5`, `-2.5`},
		{`1e21`, `1e+21`},
		{`1e20`, `100000000000000000000`},
		{`1e-6`, `0.000001`},
		{`1e-7`, `1e-7`},
		{`0`, `0`},
	}
	for _, c := range cases {
		if got := canon(t, c.in); got != c.want {
			t.Errorf("number %q: got %q want %q", c.in, got, c.want)
		}
	}
}

// TestCanonicalizeNumberFormEquivalence_spec_11_7_364 — the same numeric
// value written three ways produces one canonical form, mirroring what a
// jsonb column does to the stored payload. spec: §11.7 line 364.
func TestCanonicalizeNumberFormEquivalence_spec_11_7_364(t *testing.T) {
	t.Parallel()
	a := canon(t, `{"x":1.50}`)
	b := canon(t, `{"x":1.5}`)
	c := canon(t, `{"x":15e-1}`)
	if a != b || b != c {
		t.Errorf("numeric forms diverge: %q %q %q", a, b, c)
	}
}

// TestCanonicalizeNFC_spec_11_7_364 — string values are NFC-normalized,
// so the precomposed and decomposed spellings of an accented character
// canonicalize identically. spec: §11.7 item 3 line 364.
func TestCanonicalizeNFC_spec_11_7_364(t *testing.T) {
	t.Parallel()
	// U+00E9 (é) vs U+0065 U+0301 (e + combining acute).
	precomposed := canon(t, `{"name":"café"}`)
	decomposed := canon(t, `{"name":"café"}`)
	if precomposed != decomposed {
		t.Fatalf("NFC not applied: %q != %q", precomposed, decomposed)
	}
}

// TestCanonicalizeIdempotent_spec_11_7_364 — canonicalizing an already
// canonical value is a no-op, which guarantees the hash is stable when a
// stored payload_canonical_json is re-canonicalized on read.
// spec: §11.7 item 3 line 364.
func TestCanonicalizeIdempotent_spec_11_7_364(t *testing.T) {
	t.Parallel()
	once := canon(t, `{"b":1,"a":[3,2,1],"c":"x"}`)
	twice := canon(t, once)
	if once != twice {
		t.Errorf("not idempotent: %q != %q", once, twice)
	}
}

// TestCanonicalizeStringEscaping_spec_11_7_364 — control characters use
// the minimal escapes and non-ASCII is emitted literally.
// spec: §11.7 item 3 line 364.
func TestCanonicalizeStringEscaping_spec_11_7_364(t *testing.T) {
	t.Parallel()
	got := canon(t, `{"s":"a\tb\nc d\"e"}`)
	want := `{"s":"a\tb\nc d\"e"}`
	if got != want {
		t.Errorf("escaping: got %q want %q", got, want)
	}
}

// TestCanonicalizeRejectsTrailingData — a payload with trailing tokens is
// rejected rather than silently truncated.
func TestCanonicalizeRejectsTrailingData(t *testing.T) {
	t.Parallel()
	if _, err := Canonicalize([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Error("trailing data should error")
	}
}

// TestCanonicalizeScalarsAndArrays — top-level scalars and arrays
// canonicalize without an enclosing object.
func TestCanonicalizeScalarsAndArrays(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`null`, `null`},
		{`true`, `true`},
		{`false`, `false`},
		{`"x"`, `"x"`},
		{`[3,1,2]`, `[3,1,2]`}, // arrays preserve element order
		{`[]`, `[]`},
		{`{}`, `{}`},
	}
	for _, c := range cases {
		if got := canon(t, c.in); got != c.want {
			t.Errorf("scalar %q: got %q want %q", c.in, got, c.want)
		}
	}
}

// TestCanonicalizeMatchesEncodingJSONForSortedAscii — for an ASCII-keyed
// object Go's encoding/json (which sorts map keys bytewise) agrees with
// the canonicalizer, a cheap cross-check on the ordering rule.
func TestCanonicalizeMatchesEncodingJSONForSortedAscii(t *testing.T) {
	t.Parallel()
	in := `{"gamma":1,"alpha":2,"beta":3}`
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	ref, _ := json.Marshal(m) // encoding/json sorts string keys
	if got := canon(t, in); got != string(ref) {
		t.Errorf("ascii ordering: got %q want %q", got, string(ref))
	}
}
