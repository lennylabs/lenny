// SPDX-License-Identifier: MIT

package changegraph_test

import (
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/changegraph"
)

// The change graph and the tracked source domain of the completeness
// check are owned by TESTING.md rather than by a numbered section under
// spec/, so these cases carry no spec annotation: the harness would
// attribute an annotated failure to a platform section this package
// implements nothing of.
//
// The cases pin the boundary the completeness check and the tier-0
// residual scan divide between them. Both read this package, so a tree
// or an extension that moves here moves for both at once, and neither a
// path governed by both nor a path governed by neither can appear.

func TestSourceDomainCarriesTheTreesAndExtensionsTheCheckWalks(t *testing.T) {
	t.Parallel()
	trees := changegraph.SourceTrees()
	if len(trees) == 0 || len(changegraph.SourceExts()) == 0 {
		t.Fatalf("the source domain is empty, so the completeness check would inspect nothing")
	}
	// The accessor hands out a copy. A caller that mutated the returned
	// slice would otherwise narrow the domain of every other reader.
	trees[0] = "mutated"
	if changegraph.SourceTrees()[0] == "mutated" {
		t.Errorf("SourceTrees returned the backing slice, so a caller can narrow the shared domain")
	}
}

func TestInCheckDomainGovernsASourceFileUnderASourceTreeAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"pkg/gateway/server.go", true},
		{"cmd/lenny-test/main.go", true},
		{"scripts/lint-schema.sh", true},
		{"tests/testinfra/containers/run.go", true},
		{"pkg/gateway/server_test.go", false},
		{"pkg/gateway/values.yaml", false},
		{"schemas/lenny-tokenservice.proto", false},
		{"deploy/terraform/cloud/aws/main.tf", false},
		{"dist/brew/lenny.rb", false},
		{"close-build-gaps.sh", false},
		{"pkgsomething/other.go", false},
	} {
		if got := changegraph.InCheckDomain(tc.path); got != tc.want {
			t.Errorf("InCheckDomain(%q) is %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestGlobPrefixReadsEverySpellingOfAKeyAsItsDirectory(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]string{
		"pkg/gateway/...": "pkg/gateway",
		"pkg/gateway/":    "pkg/gateway",
		"pkg/gateway":     "pkg/gateway",
		"scripts/lint.sh": "scripts/lint.sh",
	} {
		if got := changegraph.GlobPrefix(key); got != want {
			t.Errorf("GlobPrefix(%q) is %q, want %q", key, got, want)
		}
	}
}

func TestCoveredByGlobCoversAPathAndItsAncestorsAlone(t *testing.T) {
	t.Parallel()
	keys := map[string]bool{"pkg/gateway": true, "scripts/lint.sh": true}
	for p, want := range map[string]bool{
		"pkg/gateway/server.go":         true,
		"pkg/gateway/mcp/tools/tool.go": true,
		"scripts/lint.sh":               true,
		"pkg/controller/pool.go":        false,
		"pkg/gatewayadjacent/x.go":      false,
		"":                              false,
	} {
		if got := changegraph.CoveredByGlob(p, keys); got != want {
			t.Errorf("CoveredByGlob(%q) is %v, want %v", p, got, want)
		}
	}
}

func TestParseGlobKeysFailsClosedOnAGraphItCannotRead(t *testing.T) {
	t.Parallel()
	keys, err := changegraph.ParseGlobKeys("graph.json", []byte(`{"globs":{"pkg/...":["./..."]}}`))
	if err != nil {
		t.Fatalf("parse a well-formed graph: %v", err)
	}
	if !keys["pkg"] {
		t.Errorf("the key set %v does not carry the graph's one key", keys)
	}
	// A graph that carries no globs block, or that does not parse, is an
	// error rather than an empty key set: a load that degraded to
	// carrying nothing reports every tracked source path as uncovered.
	for name, body := range map[string]string{
		"a document that is not JSON": "{",
		"a document with no globs":    `{"version":1}`,
	} {
		if _, err := changegraph.ParseGlobKeys("graph.json", []byte(body)); err == nil {
			t.Errorf("%s was read as a key set", name)
		}
	}
}
