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
//   - the secretPatterns in redact.go: a bearer token, the value of a
//     --token flag, a kubeconfig or Secret key, a token variable, a
//     private key (a truncated one included), an Anthropic credential
//     such as the Claude OAuth token, and an Authorization: Basic header;
//   - the value under a credential key such as password (redact_keys.go).
//
// The scenarios' placeholder keys (sk-ant-journey-<nonce>) match no form
// and stay in the records, so the reviewer can follow how the agent
// handled the key. Under a credential key they stay only as a scenario's
// published value; under a token key or after "Bearer" they are removed
// by that pattern. Long base64 strings that decode to no
// credential, such as the signatures of the agent's thinking blocks, stay
// as well.
//
// The search does not cover a token split by a line break that indents
// the next line, a third layer of base64, or hex encoding.

// formDepth is how many layers of base64 encoding Redact looks through,
// so the base64 of a Secret holding a base64-encoded kubeconfig is still
// recognised.
const formDepth = 2

// minEncodedLen is the shortest base64 string decoded in search of a
// credential: 20 characters encode 15 bytes, shorter than any token the
// forms recognise.
const minEncodedLen = 20

// minWrapLen is the number of token characters, counted on both sides of
// a line break, at which the break is taken to wrap a value rather than
// end one. The base64 command wraps at 76 characters and PEM at 64, and a
// terminal wraps a long token at its width. A line joined this way can
// put the last word of the line before a credential inside its
// redaction; clipping the span at the break instead would leave the tail
// of a wrapped token in clear.
const minWrapLen = 40

var encodedRun = regexp.MustCompile(`[A-Za-z0-9+/_-]{20,}={0,2}`)

// redactByForm replaces every credential recognised by its form, looking
// through depth layers of base64. A value public lists stays under a
// credential key.
func redactByForm(b []byte, depth int, public map[string]bool) []byte {
	b = replaceSpans(b, credentialSpans(b, depth, public))
	for _, p := range secretPatterns {
		if containsFold(b, p.hints) {
			b = p.re.ReplaceAll(b, []byte(p.with))
		}
	}
	return redactKeyed(b, public)
}

// credentialSpans returns the byte ranges of b that hold a JWT or a
// base64 string encoding a credential. It searches a view of b with the
// wrapping line breaks removed (unwrap), so a token split across lines,
// in plain text or inside a JSON-escaped string, is found whole.
func credentialSpans(b []byte, depth int, public map[string]bool) [][2]int {
	text, at := unwrap(b)
	spans := jwtSpans(text)
	if depth > 0 {
		for _, m := range encodedRun.FindAllIndex(text, -1) {
			if encodesCredential(text[m[0]:m[1]], depth, public) {
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
		// The header is checked before the tail, so a long dotted chain
		// with no header costs one pass rather than one pass per segment.
		if e < len(text) && text[e] == '.' {
			if start := headerStart(text[a:e]); start >= 0 {
				if end, ok := jwtTail(text, e); ok {
					spans = append(spans, [2]int{a + start, end})
					a = end
					continue
				}
			}
		}
		a = e
	}
	return spans
}

// maxHeaderLen bounds the encoded length of a JWT header the search
// recognises. A header with an x5c certificate chain runs to a few
// kilobytes.
const maxHeaderLen = 16 << 10

// maxExtraSegments is how many dotted segments past the signature a token
// can carry: an encrypted token (JWE) has five segments in all.
const maxExtraSegments = 2

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
	for i := 0; i < maxExtraSegments && end+1 < len(text) && text[end] == '.' && isURLByte(text[end+1]); i++ {
		end += 1 + urlRunLen(text[end+1:])
	}
	return end, true
}

// headerStart returns the offset in run at which a JWT header begins, or
// -1. Text can run into a token without a separator, such as a JSON
// escape (\neyJ...), a line that unwrap joined to it, or the bytes a
// misaligned base64 decoding yields, so every offset is tried. A header
// is a JSON object, so the first three bytes it decodes to are an opening
// brace and a quote or white space; that cheap check runs before the
// whole segment is decoded. The header runs from its offset to the end
// of run, so only the last maxHeaderLen characters are tried.
func headerStart(run []byte) int {
	for k := max(0, len(run)-maxHeaderLen); k+4 <= len(run); k++ {
		if run[k] == 'e' && opensJSONObject(run[k:k+4]) && isJWTHeader(run[k:]) {
			return k
		}
	}
	return -1
}

// opensJSONObject reports whether four base64url characters decode to an
// opening brace followed by a quote or white space.
func opensJSONObject(quad []byte) bool {
	var raw [3]byte
	if _, err := base64.RawURLEncoding.Decode(raw[:], quad); err != nil {
		return false
	}
	return raw[0] == '{' && (raw[1] == '"' || raw[1] == ' ' || raw[1] == '\n' || raw[1] == '\r' || raw[1] == '\t')
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
func encodesCredential(s []byte, depth int, public map[string]bool) bool {
	s = bytes.TrimRight(s, "=")
	for off := 0; off < 4 && len(s)-off >= minEncodedLen; off++ {
		raw, ok := decodeLenient(s[off:])
		if !ok {
			continue
		}
		if !bytes.Equal(redactByForm(raw, depth-1, public), raw) {
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
// a JSON string), that sits between token characters as wraps states.
// When b holds no wrapping break, unwrap returns b and
// a nil offset table.
func unwrap(b []byte) ([]byte, []int) {
	var text []byte
	var at []int
	piece := 0
	for i := 0; i < len(b); {
		if n := lineBreakLen(b[i:]); n > 0 && wraps(piece, b[i+n:]) {
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

// wraps reports whether a line break after a run of piece token
// characters, followed by next, splits one value: both sides hold token
// characters and together they hold at least minWrapLen. A terminal that
// wraps a token begun late on a line leaves a short run before the break
// and a long one after it.
func wraps(piece int, next []byte) bool {
	if piece == 0 || len(next) == 0 || !isTokenByte(next[0]) {
		return false
	}
	need := minWrapLen - piece
	n := 0
	for n < need && n < len(next) && isTokenByte(next[n]) {
		n++
	}
	return n >= need
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
