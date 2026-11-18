// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"regexp"
	"strings"
)

// Redaction by key removes the value written under a credential key. A
// credential such as a data-store password or a key the chart generates
// has no form of its own, so its key is the only sign of it.
//
// A credential key is one of these, in any case:
//
//   - a key whose name ends in password, passwd, secret-key, api-key,
//     access-key, master-key, client-secret, webhook-secret, or
//     signing-secret, with a hyphen, an underscore, or nothing between the
//     words (adminPassword, POSTGRES_PASSWORD, apiKey, minio-secret-key,
//     oidcClientSecret), or in _pass or -pass (db_pass);
//   - the key secret, pass, pwd, passwd, or pgpass on its own;
//   - Secret Key, Access Key, API Key, or Master Key written with a space,
//     and a quoted key of several words that ends in one of the above
//     ("db password");
//   - the name of a Kubernetes environment entry (- name: POSTGRES_PASSWORD)
//     whose name is one of the above, for the value of that entry.
//
// A longer key that ends in secret, such as existingSecret, secretName,
// or rootCredentialsSecret, names a Kubernetes Secret object in the
// chart's values rather than holding a credential, and keeps its value.
// The environment variable PWD, written in upper case, holds the working
// directory and keeps its value.
//
// The value is found in these spellings:
//
//   - quoted, in YAML or JSON at any depth of JSON escaping
//     ("password": "v", \"secret\": \"v\", \\\"apiKey\\\": \\\"v\\\"),
//     up to the matching closing quote, an escaped quote inside it
//     included;
//   - unquoted YAML (password: v), up to the end of the line, raw or
//     JSON-escaped, or the quote that closes a JSON string holding it;
//   - a YAML block scalar (password: | or >- followed by indented lines),
//     or a plain value on the indented line after an empty one, for every
//     indented line;
//   - a flow sequence (password: [v]), for everything inside the brackets;
//   - unquoted JSON ("apiKey": v), up to a comma, a closing bracket, or
//     white space;
//   - an environment variable or a flag with an equals sign
//     (MINIO_SECRET_KEY=v, --password=v, --from-literal=apiKey=v), up to
//     white space, a quote, or an ampersand, or a comma that starts the
//     next key=value pair of a list such as helm's --set;
//   - a flag and its value after one space (--password v), unless the
//     value starts with a hyphen or is a type name that command help
//     prints after a flag; command help sets a flag's description off by
//     two or more spaces, which this form does not read.
//
// White space around the separator never includes a line break. The
// lines after a key are read only when they are indented further than
// the key, and they stay when the first of them opens a nested mapping
// (secret: followed by defaultMode: 420).
//
// These values stay: a shell reference written exactly ($VAR, ${...},
// $(...)), a placeholder in angle brackets (<password>), true, false,
// null, and none, a value already redacted, and a value of fewer than
// four characters. A documentation example under a credential key, such
// as accessKey: minioadmin, is removed like any other value. The password
// inside a connection string (postgres://user:pass@host) has no key and
// stays.
//
// A value a scenario publishes in its own text (Scenario.PublicValues),
// and the base64 encoding of one, stays when the whole value matches it
// exactly, so a reviewer can see the agent use the published placeholder.

var credentialKey = regexp.MustCompile(`(?i)^(?:--)?(?:` +
	`[a-z0-9_.-]*(?:password|passwd|secret[-_]?key|api[-_]?key|access[-_]?key|master[-_]?key|client[-_]?secret|webhook[-_]?secret|signing[-_]?secret|[_-]pass)` +
	`|secret|pass|pwd|passwd|pgpass` +
	`|(?:secret|access|api|master) key)$`)

// keyHints are the words one of which a credential key contains, which
// gate the scan cheaply.
var keyHints = []string{"pass", "pwd", "secret", "key"}

var (
	exactReference = regexp.MustCompile(`^(?:\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^{}]*\}|\$\([^()]*\)|<[^<>]*>)$`)
	envEntryValue  = regexp.MustCompile(`^(?:\\*["'])?[ \t]*,?(?:[ \t]|\r?\n|\\+[nrt])*(?:-[ \t]+)?(?:\\*["'])?value(?:\\*["'])?[ \t]*:[ \t]*`)
	nextListPair   = regexp.MustCompile(`^,[A-Za-z0-9_.\[\]-]+=`)
	mappingLine    = regexp.MustCompile(`^(?:\\*["'])?[A-Za-z0-9_./-]+(?:\\*["'])?[ \t]*:`)
	blockIndicator = regexp.MustCompile(`^[|>][-+0-9]*[ \t]*$`)
)

// keptWords are values under a credential key that hold no credential.
var keptWords = map[string]bool{"true": true, "false": true, "null": true, "none": true}

// helpTypes are the type names command help prints after a flag.
var helpTypes = map[string]bool{"string": true, "strings": true, "stringArray": true, "stringSlice": true, "int": true, "bool": true, "duration": true, "file": true, "path": true}

type valueMode int

const (
	modeYAML valueMode = iota
	modeJSON
	modeAssign
	modeFlag
)

// redactKeyed removes the value under each credential key in b, except a
// value public lists or one of the values that stay.
func redactKeyed(b []byte, public map[string]bool) []byte {
	if !containsFold(b, keyHints) {
		return b
	}
	var spans [][2]int
	for i := 0; i < len(b); {
		if !isIdentByte(b[i]) {
			i++
			continue
		}
		s, e := i, i+identLen(b[i:])
		e = extendSpacedKey(b, s, e)
		i = e
		if !isCredentialKey(b[s:e]) {
			continue
		}
		found, next := keyedValueSpans(b, s, e, public)
		spans = append(spans, found...)
		i = max(i, next)
	}
	return replaceSpans(b, spans)
}

// containsFold reports whether b contains, in any case, one of the
// lower-case words.
func containsFold(b []byte, words []string) bool {
	for i := range b {
		for _, w := range words {
			if lowerASCII(b[i]) == w[0] && hasPrefixFold(b[i:], w) {
				return true
			}
		}
	}
	return false
}

func isCredentialKey(key []byte) bool {
	return string(key) != "PWD" && containsFold(key, keyHints) && credentialKey.Match(key)
}

// hasPrefixFold reports whether b starts with the lower-case word w in
// any case.
func hasPrefixFold(b []byte, w string) bool {
	if len(b) < len(w) {
		return false
	}
	for i := 0; i < len(w); i++ {
		if lowerASCII(b[i]) != w[i] {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// extendSpacedKey extends a key such as "Secret" over a following " Key".
func extendSpacedKey(b []byte, s, e int) int {
	switch strings.ToLower(string(b[s:e])) {
	case "secret", "access", "api", "master":
	default:
		return e
	}
	if e+4 <= len(b) && b[e] == ' ' && strings.EqualFold(string(b[e+1:e+4]), "key") && (e+4 == len(b) || !isIdentByte(b[e+4])) {
		return e + 4
	}
	return e
}

// keyedValueSpans finds the value of the credential key at b[s:e] and
// returns the ranges to redact and the offset where scanning resumes.
func keyedValueSpans(b []byte, s, e int, public map[string]bool) ([][2]int, int) {
	if p, mode, ok := envEntry(b, s, e); ok {
		return valueSpans(b, s, p, mode, public)
	}
	k, quoted := quoteBefore(b, s)
	p := e
	if quoted {
		// A quote before the key with no matching quote after it opens
		// a JSON string that holds YAML, such as "password: ...".
		if n, ok := quoteAt(b, p); ok && n == k {
			p += n + 1
		} else {
			quoted = false
		}
	} else if n, ok := quoteAt(b, p); ok && p+n+1 < len(b) && b[skipBlank(b, p+n+1)] == ':' {
		// A quoted key of several words ("db password") ends in the
		// credential word.
		p += n + 1
		quoted = true
	}
	if strings.HasPrefix(string(b[s:e]), "--") && !quoted {
		switch {
		case p < len(b) && b[p] == '=':
			return valueSpans(b, s, p+1, modeAssign, public)
		case p+1 < len(b) && (b[p] == ' ' || b[p] == '\t'):
			// Command help sets a flag's description off by two or more
			// spaces; a command line passes the value after one.
			if b[p+1] == ' ' || b[p+1] == '\t' || b[p+1] == '-' {
				return nil, e
			}
			return valueSpans(b, s, p+1, modeFlag, public)
		}
		return nil, e
	}
	p = skipBlank(b, p)
	if p >= len(b) || (b[p] != ':' && b[p] != '=') {
		return nil, e
	}
	mode := modeYAML
	switch {
	case b[p] == '=':
		mode = modeAssign
	case quoted:
		mode = modeJSON
	}
	return valueSpans(b, s, skipBlank(b, p+1), mode, public)
}

// envEntry reports whether the key at b[s:e] is the name of a Kubernetes
// environment entry followed by its value, and where the value starts.
func envEntry(b []byte, s, e int) (int, valueMode, bool) {
	j := s
	if n, ok := quoteEndingAt(b, j); ok {
		j -= n + 1
	}
	for j > 0 && (b[j-1] == ' ' || b[j-1] == '\t') {
		j--
	}
	if j == 0 || b[j-1] != ':' {
		return 0, 0, false
	}
	j--
	quoted := false
	if n, ok := quoteEndingAt(b, j); ok {
		j -= n + 1
		quoted = true
	}
	if j < 4 || !strings.EqualFold(string(b[j-4:j]), "name") || (j > 4 && isIdentByte(b[j-5])) {
		return 0, 0, false
	}
	window := b[e:min(len(b), e+256)]
	m := envEntryValue.FindIndex(window)
	if m == nil {
		return 0, 0, false
	}
	if quoted {
		return e + m[1], modeJSON, true
	}
	return e + m[1], modeYAML, true
}

// valueSpans parses the value that starts at p, for the key that starts
// at s, and returns the ranges to redact and where scanning resumes.
func valueSpans(b []byte, s, p int, mode valueMode, public map[string]bool) ([][2]int, int) {
	if k, ok := quoteAt(b, p); ok {
		vs := p + k + 1
		ve := closingQuote(b, vs, k, b[p+k])
		return judge(b, vs, ve, mode, public), ve
	}
	if bytes.HasPrefix(b[p:], []byte(Redacted)) {
		return nil, p + len(Redacted)
	}
	if mode == modeYAML || mode == modeJSON {
		if p < len(b) && b[p] == '[' {
			return flowSequence(b, p, public)
		}
		if lineEnd(b, p) == p || blockIndicator.Match(b[p:lineEnd(b, p)]) {
			return indentedBlock(b, s, lineEnd(b, p), public)
		}
	}
	if ve, ok := referenceEnd(b, p); ok {
		return judge(b, p, ve, mode, public), ve
	}
	ve := p
	switch mode {
	case modeYAML:
		ve = lineEnd(b, p)
		for ve > p && (b[ve-1] == ' ' || b[ve-1] == '\t') {
			ve--
		}
	case modeJSON, modeFlag:
		for ve < len(b) && !bytes.ContainsRune([]byte(" \t\r\n\"'\\,}]"), rune(b[ve])) {
			if mode == modeFlag && b[ve] == ',' {
				break
			}
			ve++
		}
	case modeAssign:
		for ve < len(b) && !bytes.ContainsRune([]byte(" \t\r\n\"'\\&"), rune(b[ve])) {
			if b[ve] == ',' && nextListPair.Match(b[ve:min(len(b), ve+128)]) {
				break
			}
			ve++
		}
	}
	return judge(b, p, ve, mode, public), ve
}

// referenceEnd returns the end of a reference that starts at p and can
// hold white space, $(...), ${...}, or <...>, when it closes on the same
// line, so the value is judged whole.
func referenceEnd(b []byte, p int) (int, bool) {
	var closer byte
	switch {
	case p+1 < len(b) && b[p] == '$' && b[p+1] == '(':
		closer = ')'
	case p+1 < len(b) && b[p] == '$' && b[p+1] == '{':
		closer = '}'
	case p < len(b) && b[p] == '<':
		closer = '>'
	default:
		return 0, false
	}
	c := bytes.IndexByte(b[p:lineEnd(b, p)], closer)
	if c < 0 {
		return 0, false
	}
	return p + c + 1, true
}

// judge returns the range b[vs:ve] to redact, or none when the value is
// one that stays.
func judge(b []byte, vs, ve int, mode valueMode, public map[string]bool) [][2]int {
	v := string(b[vs:ve])
	switch {
	case len(v) < 4, v == Redacted, public[v], keptWords[strings.ToLower(v)], exactReference.MatchString(v):
		return nil
	case mode == modeFlag && helpTypes[v]:
		return nil
	}
	return [][2]int{{vs, ve}}
}

// flowSequence redacts the elements of a flow sequence that starts at p,
// unless every element is one that stays.
func flowSequence(b []byte, p int, public map[string]bool) ([][2]int, int) {
	end := p
	for end < len(b) && breakLen(b, end) == 0 {
		end++
	}
	if bytes.HasPrefix(b[p:end], []byte(Redacted)) {
		return nil, p + len(Redacted)
	}
	if bytes.HasPrefix(b[p+1:end], []byte(Redacted+"]")) {
		return nil, p + 1 + len(Redacted)
	}
	c := bytes.IndexByte(b[p:end], ']')
	if c < 0 {
		return [][2]int{{p + 1, end}}, end
	}
	c += p
	for _, el := range strings.Split(string(b[p+1:c]), ",") {
		el = strings.Trim(strings.TrimSpace(el), `"'\`)
		if judge([]byte(el), 0, len(el), modeJSON, public) != nil {
			return [][2]int{{p + 1, c}}, c
		}
	}
	return nil, c
}

// indentedBlock redacts the lines after the line that ends at eol that
// are indented further than the key at s: a block scalar's lines, or a
// plain value continued on the next line. Lines that open a nested
// mapping hold no value of the key and stay.
func indentedBlock(b []byte, s, eol int, public map[string]bool) ([][2]int, int) {
	col := s - lineStart(b, s)
	start, end := -1, eol
	bl := breakLen(b, eol)
	if bl == 0 {
		return nil, eol
	}
	for pos := eol + bl; pos < len(b); {
		le := lineEnd(b, pos)
		content := pos + indentLen(b[pos:le])
		if content == le || content-pos <= col {
			break
		}
		if start < 0 {
			if mappingLine.Match(b[content:le]) {
				return nil, eol
			}
			start = content
		}
		end = le
		bl = breakLen(b, le)
		if bl == 0 {
			break
		}
		pos = le + bl
	}
	if start < 0 {
		return nil, eol
	}
	return judge(b, start, end, modeYAML, public), end
}

// lineEnd returns the offset of the end of the line that holds p: a raw
// line break, a JSON-escaped one, or a quote that closes a JSON string,
// which a JSON delimiter or the end of the text follows.
func lineEnd(b []byte, p int) int {
	for j := p; j < len(b); j++ {
		switch b[j] {
		case '\n', '\r':
			return j
		case '\\':
			if breakLen(b, j) > 0 {
				return j
			}
		case '"':
			if j+1 == len(b) || bytes.IndexByte([]byte(",}]:"), b[j+1]) >= 0 {
				return j
			}
		}
	}
	return len(b)
}

// breakLen returns the length of the line break at b[j:]: raw, or
// JSON-escaped at any depth.
func breakLen(b []byte, j int) int {
	if j >= len(b) {
		return 0
	}
	if b[j] == '\n' {
		return 1
	}
	if b[j] == '\r' {
		if j+1 < len(b) && b[j+1] == '\n' {
			return 2
		}
		return 1
	}
	return lineBreakLen(b[j:])
}

// lineStart returns the offset of the start of the line that holds s.
// A JSON string's opening quote, after a colon, a comma, or an opening
// bracket, also starts a line, so YAML inside a JSON string is measured
// from its own first column.
func lineStart(b []byte, s int) int {
	for j := s; j > 0; j-- {
		if b[j-1] == '\n' || b[j-1] == '\r' || (b[j-1] == 'n' && j > 1 && b[j-2] == '\\') {
			return j
		}
		if b[j-1] == '"' && j > 1 && bytes.IndexByte([]byte(":,[{"), b[j-2]) >= 0 {
			return j
		}
	}
	return 0
}

func indentLen(b []byte) int {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return n
}

// quoteAt reports whether b[p:] starts with a quote preceded by k
// backslashes, and returns k.
func quoteAt(b []byte, p int) (int, bool) {
	k := 0
	for p+k < len(b) && b[p+k] == '\\' {
		k++
	}
	if p+k < len(b) && (b[p+k] == '"' || b[p+k] == '\'') {
		return k, true
	}
	return 0, false
}

// quoteBefore reports whether the key at s is preceded by a quote, and
// returns the number of backslashes before that quote.
func quoteBefore(b []byte, s int) (int, bool) {
	return quoteEndingAt(b, s)
}

// quoteEndingAt reports whether b[:j] ends with a quote, and returns the
// number of backslashes before it.
func quoteEndingAt(b []byte, j int) (int, bool) {
	if j == 0 || (b[j-1] != '"' && b[j-1] != '\'') {
		return 0, false
	}
	k := 0
	for j-2-k >= 0 && b[j-2-k] == '\\' {
		k++
	}
	return k, true
}

// closingQuote returns the offset of the quote that closes a value opened
// by q after k backslashes: the next q preceded by exactly k backslashes.
// A quote with more backslashes before it is escaped inside the value. An
// unclosed value ends at the end of the line.
func closingQuote(b []byte, vs, k int, q byte) int {
	for j := vs; j < len(b); j++ {
		if b[j] == '\n' || b[j] == '\r' {
			return j
		}
		if b[j] != q {
			continue
		}
		n := 0
		for j-1-n >= vs && b[j-1-n] == '\\' {
			n++
		}
		if n == k {
			return j - k
		}
	}
	return len(b)
}

func skipBlank(b []byte, p int) int {
	for p < len(b) && (b[p] == ' ' || b[p] == '\t') {
		p++
	}
	return p
}

func identLen(b []byte) int {
	n := 0
	for n < len(b) && isIdentByte(b[n]) {
		n++
	}
	return n
}

// isIdentByte reports whether c can appear in a key name or a flag.
func isIdentByte(c byte) bool {
	return isURLByte(c) || c == '.'
}
