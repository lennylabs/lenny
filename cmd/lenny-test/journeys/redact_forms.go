// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
)

// Redaction by form finds credentials the harness was never told about.
// A trial that installs Lenny mints the platform-admin token inside the
// cluster, and the agent can read it from a Secret, so the harness cannot
// know the value in advance. The forms recognised here are:
//
//   - a JSON Web Token: two or more base64url segments after a header
//     segment that decodes to a JSON object with an "alg" member;
//   - a base64 string (standard or URL alphabet) whose decoding holds a
//     credential by form, which is how a Kubernetes Secret's data shows a
//     token, a kubeconfig, or a private key;
//   - the secretPatterns in redact.go: a bearer token, a kubeconfig or
//     Secret key, a token variable, a private key, and an Anthropic
//     credential such as the Claude OAuth token.
//
// The scenarios' placeholder keys (sk-ant-journey-<nonce>) match none of
// these forms and stay in the records, because the postconditions and the
// reviewer look for them. Long base64 strings that decode to no
// credential, such as the signatures of the agent's thinking blocks, stay
// as well.

// formDepth is how many layers of base64 encoding Redact looks through,
// so the base64 of a Secret holding a base64-encoded kubeconfig is still
// recognised.
const formDepth = 2

// minEncodedLen is the shortest base64 string decoded in search of a
// credential: 20 characters encode 15 bytes, shorter than any token the
// forms recognise.
const minEncodedLen = 20

// minWrapLen is the shortest run of token characters that a line break
// is taken to wrap rather than end. The base64 command wraps at 76
// characters and PEM at 64, and a terminal wraps a long token at its
// width, so a run this long followed by a line break and more token
// characters is read as one value.
const minWrapLen = 40

// maxHeaderSkip bounds how far into a base64url run the JWT search looks
// for the header's first character. Text that runs into a token without a
// separator, such as a JSON escape (\neyJ...) or the bytes a misaligned
// base64 decoding yields, puts a short prefix before the header.
const maxHeaderSkip = 256

var encodedRun = regexp.MustCompile(`[A-Za-z0-9+/_-]{20,}={0,2}`)

// redactByForm replaces every credential recognised by its form, looking
// through depth layers of base64.
func redactByForm(b []byte, depth int) []byte {
	b = replaceSpans(b, credentialSpans(b, depth))
	for _, p := range secretPatterns {
		b = p.re.ReplaceAll(b, []byte(p.with))
	}
	return b
}

// credentialSpans returns the byte ranges of b that hold a JWT or a
// base64 string encoding a credential. It searches a view of b with the
// wrapping line breaks removed (unwrap), so a token split across lines,
// in plain text or inside a JSON-escaped string, is found whole.
func credentialSpans(b []byte, depth int) [][2]int {
	text, at := unwrap(b)
	spans := jwtSpans(text)
	if depth > 0 {
		for _, m := range encodedRun.FindAllIndex(text, -1) {
			if encodesCredential(text[m[0]:m[1]], depth) {
				spans = append(spans, [2]int{m[0], m[1]})
			}
		}
	}
	if at == nil {
		return spans
	}
	for i, s := range spans {
		spans[i] = [2]int{at[s[0]], at[s[1]-1] + 1}
	}
	return spans
}

// jwtSpans returns the ranges of text that hold a JWT: a header segment,
// a dot, a non-empty payload segment, a dot, and the signature segment,
// with any further dotted segments of an encrypted token.
func jwtSpans(text []byte) [][2]int {
	var spans [][2]int
	for a := 0; a < len(text); {
		if !isURLByte(text[a]) {
			a++
			continue
		}
		e := a + urlRunLen(text[a:])
		if end, ok := jwtTail(text, e); ok {
			if start := headerStart(text[a:e]); start >= 0 {
				spans = append(spans, [2]int{a + start, end})
				a = end
				continue
			}
		}
		a = e
	}
	return spans
}

// jwtTail checks that a header ending at e is followed by a payload and a
// signature segment, and returns where the token ends.
func jwtTail(text []byte, e int) (int, bool) {
	if e >= len(text) || text[e] != '.' {
		return 0, false
	}
	p := e + 1 + urlRunLen(text[e+1:])
	if p == e+1 || p >= len(text) || text[p] != '.' {
		return 0, false
	}
	end := p + 1 + urlRunLen(text[p+1:])
	for end+1 < len(text) && text[end] == '.' && isURLByte(text[end+1]) {
		end += 1 + urlRunLen(text[end+1:])
	}
	return end, true
}

// headerStart returns the offset in run at which a JWT header begins, or
// -1. A header is a JSON object, so its encoding starts with "ey" (an
// opening brace and a quote) or "ew" (a brace and white space).
func headerStart(run []byte) int {
	for k := 0; k+1 < len(run) && k <= maxHeaderSkip; k++ {
		if run[k] == 'e' && (run[k+1] == 'y' || run[k+1] == 'w') && isJWTHeader(run[k:]) {
			return k
		}
	}
	return -1
}

func isJWTHeader(seg []byte) bool {
	raw, err := base64.RawURLEncoding.DecodeString(string(seg))
	if err != nil {
		return false
	}
	var h map[string]json.RawMessage
	if json.Unmarshal(raw, &h) != nil {
		return false
	}
	_, ok := h["alg"]
	return ok
}

// encodesCredential reports whether s, a base64 string in either
// alphabet, decodes to text that holds a credential by form. The string
// may start with characters that are not part of the encoding, such as
// the n of a JSON escape or a word run into it, so the decoding is tried
// at each of the four alignments.
func encodesCredential(s []byte, depth int) bool {
	s = bytes.TrimRight(s, "=")
	for off := 0; off < 4 && len(s)-off >= minEncodedLen; off++ {
		raw, ok := decodeLenient(s[off:])
		if !ok {
			continue
		}
		if !bytes.Equal(redactByForm(raw, depth-1), raw) {
			return true
		}
	}
	return false
}

// decodeLenient decodes unpadded base64 in the standard or the URL
// alphabet, dropping a trailing character that cannot complete a byte.
func decodeLenient(s []byte) ([]byte, bool) {
	norm := make([]byte, len(s))
	for i, c := range s {
		switch c {
		case '-':
			c = '+'
		case '_':
			c = '/'
		}
		norm[i] = c
	}
	if len(norm)%4 == 1 {
		norm = norm[:len(norm)-1]
	}
	raw := make([]byte, base64.RawStdEncoding.DecodedLen(len(norm)))
	n, err := base64.RawStdEncoding.Decode(raw, norm)
	return raw[:n], err == nil
}

// unwrap returns b with each wrapping line break removed, and the offset
// in b of each byte of the result. A wrapping line break is a newline,
// raw or JSON-escaped (\n, \r\n, or the doubly escaped \\n of JSON inside
// a JSON string), that follows at least minWrapLen token characters and
// precedes another. When b holds no wrapping break, unwrap returns b and
// a nil offset table.
func unwrap(b []byte) ([]byte, []int) {
	var text []byte
	var at []int
	piece := 0
	for i := 0; i < len(b); {
		if n := lineBreakLen(b[i:]); n > 0 && piece >= minWrapLen && i+n < len(b) && isTokenByte(b[i+n]) {
			if at == nil {
				text, at = make([]byte, 0, len(b)), make([]int, 0, len(b))
				for j := 0; j < i; j++ {
					text, at = append(text, b[j]), append(at, j)
				}
			}
			i += n
			piece = 0
			continue
		}
		if isTokenByte(b[i]) && b[i] != '=' {
			piece++
		} else {
			piece = 0
		}
		if at != nil {
			text, at = append(text, b[i]), append(at, i)
		}
		i++
	}
	if at == nil {
		return b, nil
	}
	return text, at
}

// lineBreakLen returns the length of the newline at the start of b: a raw
// \n or \r\n, or a JSON escape of either with any number of backslashes.
func lineBreakLen(b []byte) int {
	switch {
	case len(b) > 0 && b[0] == '\n':
		return 1
	case len(b) > 1 && b[0] == '\r' && b[1] == '\n':
		return 2
	}
	n := escapedLen(b, 'r')
	if m := escapedLen(b[n:], 'n'); m > 0 {
		return n + m
	}
	return 0
}

// escapedLen returns the length of one or more backslashes followed by
// c at the start of b, or 0.
func escapedLen(b []byte, c byte) int {
	i := 0
	for i < len(b) && b[i] == '\\' {
		i++
	}
	if i == 0 || i >= len(b) || b[i] != c {
		return 0
	}
	return i + 1
}

// replaceSpans replaces each range of b with Redacted, merging ranges
// that overlap.
func replaceSpans(b []byte, spans [][2]int) []byte {
	if len(spans) == 0 {
		return b
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	out := make([]byte, 0, len(b))
	prev := 0
	for i := 0; i < len(spans); {
		start, end := spans[i][0], spans[i][1]
		for i++; i < len(spans) && spans[i][0] <= end; i++ {
			end = max(end, spans[i][1])
		}
		out = append(append(out, b[prev:start]...), Redacted...)
		prev = end
	}
	return append(out, b[prev:]...)
}

func urlRunLen(b []byte) int {
	n := 0
	for n < len(b) && isURLByte(b[n]) {
		n++
	}
	return n
}

// isURLByte reports whether c belongs to the base64url alphabet.
func isURLByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// isTokenByte reports whether c can appear in a JWT or a base64 string
// of either alphabet.
func isTokenByte(c byte) bool {
	return isURLByte(c) || c == '+' || c == '/' || c == '=' || c == '.'
}
