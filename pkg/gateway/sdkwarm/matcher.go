// SPDX-License-Identifier: MIT

// Package sdkwarm holds the gateway-side §6.1 SDK-warm demotion decision:
// given a session's workspace plan and the runtime's sdkWarmBlockingPaths,
// it decides whether the gateway must demote a pre-connected SDK-warm pod
// to pod-warm before the workspace is materialized.
//
// spec: §6.1 lines 34-40 — "if the workspace plan includes files matching
// any of these glob patterns, the gateway sets requiresDemotion: true on
// the ClaimOpts and the adapter calls the DemoteSDK RPC". Patterns follow
// Go path.Match (per segment) extended with `**` (cross-segment), matched
// case-sensitively against relative workspace paths (§5.1 line 24).
package sdkwarm

import (
	"path"
	"strings"
)

// DefaultBlockingPaths is the §5.1 line 24 / §6.1 line 34 default
// sdkWarmBlockingPaths list applied when a preConnect runtime declares
// none. It matches the project-config files that must be present at
// session start and therefore cannot be served by a pre-connected SDK.
var DefaultBlockingPaths = []string{"CLAUDE.md", ".claude/*"}

// RequiresDemotion reports whether any workspace path matches any blocking
// pattern. When it returns true the gateway must call DemoteSDK before
// materializing the workspace (§6.1 line 34); matchedPath / matchedPattern
// name the first match for the audit/observability trail. An empty pattern
// list (§6.1 line 38, "disables demotion-path checking entirely") never
// demotes.
func RequiresDemotion(paths, patterns []string) (matchedPath, matchedPattern string, requires bool) {
	for _, p := range paths {
		clean := normalize(p)
		if clean == "" {
			continue
		}
		for _, pat := range patterns {
			pat = normalize(pat)
			if pat == "" {
				continue
			}
			if Match(pat, clean) {
				return clean, pat, true
			}
		}
	}
	return "", "", false
}

// normalize strips a leading "./" or "/" so a pattern and a path that
// differ only by an anchor prefix still compare equal. The §6.1 paths are
// relative to the workspace root.
func normalize(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

// Match reports whether name matches pattern under the §5.1 line 24 glob
// dialect: each `/`-delimited segment is matched with Go path.Match (so
// `*`, `?`, and `[...]` stay within a single segment), and `**` matches
// zero or more whole segments. Matching is case-sensitive.
func Match(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// `**` matches zero or more segments. Collapse the rest and
			// try every split point; an `**` in trailing position matches
			// whatever remains, including nothing.
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
