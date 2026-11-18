// SPDX-License-Identifier: MIT

package rewrite

import "testing"

// These tests cover the move driver's tooling rather than a platform
// behavior, so they carry no specification annotation.

// TestPathTokensRewritesRegisterForms checks every form a register keyed by
// package path uses: a YAML key, a list entry with a trailing slash, a
// recursive selector, a quoted value, a file under the package, and a
// comma-separated list, and that a longer sibling and a path that only
// shares a prefix are left alone.
func TestPathTokensRewritesRegisterForms(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/session/sessionevents", New: "github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"},
	}
	src := "pkg/gateway/session/sessionevents: 3\n" +
		"  - pkg/gateway/session/sessionevents/\n" +
		"  - pkg/gateway/session/sessionevents/...\n" +
		"    package: \"pkg/gateway/mcpfabric/mcp\"\n" +
		"    file: pkg/gateway/mcpfabric/mcp/server.go\n" +
		"    list: [pkg/gateway/mcpfabric/mcp, pkg/gateway/mcpfabric/mcptools]\n" +
		"    sibling: pkg/gateway/session/sessioneventsx\n" +
		"    other: xpkg/gateway/mcpfabric/mcp\n"
	want := "pkg/gateway/streamproxy/sessionevents: 3\n" +
		"  - pkg/gateway/streamproxy/sessionevents/\n" +
		"  - pkg/gateway/streamproxy/sessionevents/...\n" +
		"    package: \"pkg/gateway/platformtools/mcp\"\n" +
		"    file: pkg/gateway/platformtools/mcp/server.go\n" +
		"    list: [pkg/gateway/platformtools/mcp, pkg/gateway/mcpfabric/mcptools]\n" +
		"    sibling: pkg/gateway/session/sessioneventsx\n" +
		"    other: xpkg/gateway/mcpfabric/mcp\n"
	if got := PathTokens(src, moves); got != want {
		t.Errorf("PathTokens:\n%s\nwant:\n%s", got, want)
	}
}

// TestPathTokensMatchesTheLongestMovedPathOnce checks that a nested move and
// its parent's move in one batch each rewrite their own tokens, and that a
// rewritten token is not rewritten again.
func TestPathTokensMatchesTheLongestMovedPathOnce(t *testing.T) {
	moves := []Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/policy", New: "github.com/lennylabs/lenny/pkg/gateway/policy/policy"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor", New: "github.com/lennylabs/lenny/pkg/policy/interceptor"},
	}
	src := "a: pkg/gateway/policy\nb: pkg/gateway/policy/interceptor/store\nc: pkg/gateway/policy/x.go\n"
	want := "a: pkg/gateway/policy/policy\nb: pkg/policy/interceptor/store\nc: pkg/gateway/policy/policy/x.go\n"
	if got := PathTokens(src, moves); got != want {
		t.Errorf("PathTokens:\n%s\nwant:\n%s", got, want)
	}
}

// TestPathTokensWithNoEffectiveMoveReturnsTheContent checks the empty and
// no-op cases.
func TestPathTokensWithNoEffectiveMoveReturnsTheContent(t *testing.T) {
	src := "pkg/gateway/x: 1\n"
	if got := PathTokens(src, nil); got != src {
		t.Errorf("no moves changed the content: %q", got)
	}
	same := []Move{{Old: "github.com/lennylabs/lenny/pkg/gateway/x", New: "github.com/lennylabs/lenny/pkg/gateway/x"}}
	if got := PathTokens(src, same); got != src {
		t.Errorf("a no-op move changed the content: %q", got)
	}
	if got := PathTokens("", []Move{{Old: "github.com/lennylabs/lenny/pkg/a", New: "github.com/lennylabs/lenny/pkg/b"}}); got != "" {
		t.Errorf("empty content became %q", got)
	}
}
