// SPDX-License-Identifier: MIT

package sdkwarm

import "testing"

// spec: §5.1 line 24 / §6.1 line 34 — the sdkWarmBlockingPaths glob
// dialect (per-segment path.Match plus cross-segment `**`).
func TestMatch_spec_6_1(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		// Exact, anchored at the workspace root.
		{"CLAUDE.md", "CLAUDE.md", true},
		{"CLAUDE.md", "sub/CLAUDE.md", false}, // pattern anchored; not a deep match
		{"CLAUDE.md", "CLAUDE.mdx", false},
		// `*` stays within a single segment.
		{".claude/*", ".claude/settings.json", true},
		{".claude/*", ".claude/agents/foo.md", false}, // `*` is one segment only
		{".claude/*", ".claude", false},               // needs a child segment
		// `**` crosses segments.
		{".claude/**", ".claude/agents/foo.md", true},
		{".claude/**", ".claude/settings.json", true},
		{".claude/**", ".claude", true}, // `**` matches zero segments
		{"**/CLAUDE.md", "a/b/CLAUDE.md", true},
		{"**/CLAUDE.md", "CLAUDE.md", true}, // leading `**` matches zero segments
		{"**", "anything/at/all", true},
		// `?` and char classes are per-segment.
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file12.txt", false},
		{"[abc].md", "a.md", true},
		{"[abc].md", "d.md", false},
		// Case sensitivity (§5.1 line 105 "case-sensitive").
		{"CLAUDE.md", "claude.md", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// spec: §6.1 lines 34-40 — the gateway demotes when any workspace file
// matches any blocking pattern; an empty pattern list never demotes.
func TestRequiresDemotion_spec_6_1(t *testing.T) {
	t.Run("blocking file present triggers demotion", func(t *testing.T) {
		mp, pat, req := RequiresDemotion(
			[]string{"src/main.go", "CLAUDE.md", "README"},
			DefaultBlockingPaths,
		)
		if !req {
			t.Fatalf("expected demotion required")
		}
		if mp != "CLAUDE.md" || pat != "CLAUDE.md" {
			t.Fatalf("match = (%q, %q), want (CLAUDE.md, CLAUDE.md)", mp, pat)
		}
	})

	t.Run("anchor-stripped path still matches", func(t *testing.T) {
		_, _, req := RequiresDemotion([]string{"./.claude/settings.json"}, DefaultBlockingPaths)
		if !req {
			t.Fatalf("expected demotion for ./.claude/settings.json")
		}
	})

	t.Run("no blocking file leaves SDK warm", func(t *testing.T) {
		_, _, req := RequiresDemotion([]string{"src/main.go", "go.mod"}, DefaultBlockingPaths)
		if req {
			t.Fatalf("did not expect demotion")
		}
	})

	t.Run("empty pattern list disables checking", func(t *testing.T) {
		// §6.1 line 38 — sdkWarmBlockingPaths: [] keeps every pod SDK-warm.
		_, _, req := RequiresDemotion([]string{"CLAUDE.md", ".claude/x"}, nil)
		if req {
			t.Fatalf("empty pattern list must never demote")
		}
	})

	t.Run("empty and blank paths are skipped", func(t *testing.T) {
		_, _, req := RequiresDemotion([]string{"", "  "}, DefaultBlockingPaths)
		if req {
			t.Fatalf("blank paths must not match")
		}
	})
}
