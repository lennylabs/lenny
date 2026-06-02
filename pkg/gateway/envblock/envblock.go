// SPDX-License-Identifier: MIT

// Package envblock implements the §14 deployer-configured environment
// variable blocklist applied to a CreateSessionRequest's `env` field. The
// platform ships a default blocklist that operators can extend but not
// reduce in multi-tenant mode; the gateway rejects any env var whose name
// matches a blocklist entry (exact name or `*` glob) with
// 400 ENV_VAR_BLOCKLISTED. spec: §14 lines 47-50, 105. F-14.1.12.
package envblock

import "strings"

// DefaultBlocklist is the §14 platform default env-var blocklist. The
// entries follow the spec's named examples: well-known credential
// variables by exact name plus the credential-suffix globs that catch the
// common provider key/secret/token/password conventions. A deployer
// extends this set via configuration; in multi-tenant mode the default
// cannot be reduced, so it is always merged in first. spec: §14 line 105.
var DefaultBlocklist = []string{
	// Exact names called out in the spec example plus the obvious cloud /
	// provider credential variables.
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GOOGLE_APPLICATION_CREDENTIALS",
	// Credential-suffix globs from the spec example. `*` is a full-string
	// wildcard matching any sequence (including `_`); matching is
	// case-sensitive.
	"*_SECRET_*",
	"*_SECRET",
	"*_KEY",
	"*_TOKEN",
	"*_PASSWORD",
}

// Matcher resolves whether an env-var name is blocked. It holds the
// platform default merged with the deployer extensions; the merge is
// order-preserving so a Match reports the first pattern that fired.
type Matcher struct {
	patterns []string
}

// New returns a Matcher over the §14 platform default blocklist merged
// with the deployer extensions. The default always comes first so an
// operator can extend but never reduce it. A nil extras slice yields the
// default blocklist alone. spec: §14 line 105. F-14.1.12.
func New(extras []string) *Matcher {
	pats := make([]string, 0, len(DefaultBlocklist)+len(extras))
	pats = append(pats, DefaultBlocklist...)
	for _, e := range extras {
		if e != "" {
			pats = append(pats, e)
		}
	}
	return &Matcher{patterns: pats}
}

// Match returns the first blocklist pattern that matches key and true, or
// ("", false) when key is allowed. Matching is case-sensitive and treats
// `*` as a full-string wildcard per §14. F-14.1.12.
func (m *Matcher) Match(key string) (string, bool) {
	for _, p := range m.patterns {
		if globMatch(p, key) {
			return p, true
		}
	}
	return "", false
}

// globMatch reports whether s matches pattern, where `*` is the only
// metacharacter and matches any sequence of zero or more characters
// (including `_`). A pattern with no `*` is an exact, case-sensitive
// comparison. spec: §14 line 105 — "full-string wildcard ... case-sensitive".
func globMatch(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	n := len(parts)
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	if !strings.HasSuffix(s, parts[n-1]) {
		return false
	}
	// Strip the anchored prefix and suffix; when they overlap (prefix +
	// suffix longer than s) the key cannot also satisfy the middle parts.
	rest := s[len(parts[0]):]
	if len(rest) < len(parts[n-1]) {
		return false
	}
	rest = rest[:len(rest)-len(parts[n-1])]
	for i := 1; i < n-1; i++ {
		idx := strings.Index(rest, parts[i])
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(parts[i]):]
	}
	return true
}
