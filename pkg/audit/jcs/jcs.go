// SPDX-License-Identifier: MIT

// Package jcs implements the RFC 8785 JSON Canonicalization Scheme used
// by the §11.7 audit hash chain. The chain hashes the canonical form of
// each event payload (`payload_canonical_json`) so that two byte
// representations of the same logical JSON value produce the same hash.
//
// The canonicalization, per §11.7 item 3 line 364, applies:
//   - lexicographic key order at every nesting level (UTF-16 code-unit
//     order, the RFC 8785 ordering),
//   - UTF-8 NFC normalization of string values,
//   - no insignificant whitespace, and
//   - numbers in shortest canonical (ECMAScript Number::toString) form.
//
// spec: §11.7 item 3 line 364 (RFC 8785 JCS).
package jcs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// Canonicalize returns the RFC 8785 canonical form of a JSON value. The
// input must be a single JSON value; trailing data is an error. Numbers
// are parsed with full precision preserved through the decoder and
// re-emitted in ECMAScript canonical form, so the output is stable under
// the key reordering and whitespace changes a Postgres `jsonb` round
// trip introduces.
//
// spec: §11.7 item 3 line 364.
func Canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("jcs: parse: %w", err)
	}
	if dec.More() {
		return nil, errors.New("jcs: trailing data after JSON value")
	}
	var b strings.Builder
	if err := writeValue(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// writeValue emits one JSON value in canonical form.
func writeValue(b *strings.Builder, v interface{}) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeString(b, t)
	case json.Number:
		s, err := canonicalNumber(string(t))
		if err != nil {
			return err
		}
		b.WriteString(s)
	case []interface{}:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]interface{}:
		return writeObject(b, t)
	default:
		return fmt.Errorf("jcs: unsupported value type %T", v)
	}
	return nil
}

// writeObject emits a JSON object with keys ordered by their UTF-16
// code-unit sequence (the RFC 8785 ordering) after NFC normalization.
func writeObject(b *strings.Builder, m map[string]interface{}) error {
	type member struct {
		key string
		val interface{}
	}
	members := make([]member, 0, len(m))
	for k, v := range m {
		members = append(members, member{key: norm.NFC.String(k), val: v})
	}
	sort.Slice(members, func(i, j int) bool {
		return lessUTF16(members[i].key, members[j].key)
	})
	b.WriteByte('{')
	for i, mem := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(b, mem.key)
		b.WriteByte(':')
		if err := writeValue(b, mem.val); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// lessUTF16 compares two strings by their UTF-16 code-unit sequence, the
// ordering RFC 8785 mandates for object keys. For Basic Multilingual
// Plane characters this matches code-point order; supplementary
// characters are compared by their surrogate-pair code units so a key
// in the 0xE000..0xFFFF range sorts after a supplementary-plane key, as
// the scheme requires.
func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// writeString emits a JSON string with NFC-normalized content and the
// minimal RFC 8785 escaping: the two mandatory escapes (`"` and `\`),
// the short escapes for the control characters that have them, and
// `\u00xx` for the remaining C0 control characters. Every other
// character, including non-ASCII, is emitted as literal UTF-8.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range norm.NFC.String(s) {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// canonicalNumber returns the ECMAScript Number::toString form of a JSON
// number token. The token is parsed as an IEEE-754 double (the JSON
// number model RFC 8785 canonicalizes against) and re-emitted in the
// shortest round-tripping form, so `1.0`, `1`, and `1e0` all canonicalize
// to `1` and survive a Postgres `jsonb` numeric round trip identically.
func canonicalNumber(token string) (string, error) {
	f, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return "", fmt.Errorf("jcs: parse number %q: %w", token, err)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("jcs: non-finite number %q", token)
	}
	return es6Number(f), nil
}

// es6Number implements the ECMAScript Number::toString algorithm for a
// finite double, the number serialization RFC 8785 references. It
// recovers the shortest decimal digit string and decimal exponent from
// strconv's scientific form and applies the standard positional /
// exponential selection rules.
func es6Number(f float64) string {
	if f == 0 {
		// Covers both +0 and -0; ECMAScript renders both as "0".
		return "0"
	}
	neg := false
	if f < 0 {
		neg = true
		f = -f
	}
	// strconv 'e' with precision -1 yields the shortest round-tripping
	// mantissa and a signed decimal exponent, e.g. "1.5e+03".
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	eIdx := strings.IndexByte(sci, 'e')
	mantissa := sci[:eIdx]
	exp, _ := strconv.Atoi(sci[eIdx+1:])
	digits := strings.Replace(mantissa, ".", "", 1)
	k := len(digits) // number of significant digits
	n := exp + 1     // position of the decimal point relative to digits

	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		var m string
		if k == 1 {
			m = digits
		} else {
			m = digits[:1] + "." + digits[1:]
		}
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		out = m + "e" + sign + strconv.Itoa(e)
	}
	if neg {
		return "-" + out
	}
	return out
}
