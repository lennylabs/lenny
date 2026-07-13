// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// readSpecMapPackages returns, per section id, the `packages` list
// recorded in tests/spec-map.json.
func readSpecMapPackages(t *testing.T) map[string][]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Packages []string `json:"packages"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string][]string{}
	for id, sec := range doc.Sections {
		out[id] = sec.Packages
	}
	return out
}

// spec: 25.2 (agent operability architecture overview; the operability
//
//	surface is implemented under pkg/ops, split between the gateway and
//	the lenny-ops service)
//
// diagnosis: A spec-map `packages` entry names the pkg/operability/*
//
//	tree, which does not exist on disk: the §25 operability code lives
//	under pkg/ops/* (pkg/ops/events, pkg/ops/opsaudit, pkg/ops/driftservice,
//	pkg/ops/backup, pkg/ops/mcp, pkg/ops/conventions). A stale
//	pkg/operability reference makes the coverage and impact tooling that
//	reads the `packages` field point at a nonexistent package, so it can
//	neither report coverage for the real package nor tie an edit of it
//	back to its spec section. Repoint the entry at the real pkg/ops/*
//	package.
func TestSpecMapHasNoOperabilityPackageReferences(t *testing.T) {
	t.Parallel()

	stale := []string{}
	for id, pkgs := range readSpecMapPackages(t) {
		for _, p := range pkgs {
			// pkg/operability and pkg/operability/* are the retired
			// names. pkg/gateway/operability is a real package and is
			// intentionally not matched.
			if p == "pkg/operability" || strings.HasPrefix(p, "pkg/operability/") {
				stale = append(stale, id+" → "+p)
			}
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("spec-map.json references the retired pkg/operability tree, which does not exist on disk (the operability surface lives under pkg/ops/*): %s",
			strings.Join(stale, "; "))
	}
}

// spec: 25.2 (agent operability architecture overview; every §25 spec
//
//	section names the pkg/ops package that implements it)
//
// diagnosis: A pkg/ops/* package that the §25 spec-map sections name as
//
//	their implementation is absent from disk. The operability sections
//	map their packages field at pkg/ops/conventions (§25.2 API
//	conventions envelope), pkg/ops/events (§25.5), pkg/ops/opsaudit
//	(§25.9), pkg/ops/driftservice (§25.10), pkg/ops/backup (§25.11), and
//	pkg/ops/mcp (§25.7/§25.12). When one of these directories is missing,
//	the spec-map entry has drifted from the tree (a rename or typo) and
//	the coverage/impact tooling keyed on it resolves nothing. Realign the
//	entry with the package on disk.
func TestOperabilityImplementationPackagesExist(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	// The canonical pkg/ops packages the §25 operability sections are
	// implemented in. Each must exist as a directory on disk.
	want := []string{
		"pkg/ops/conventions",
		"pkg/ops/events",
		"pkg/ops/opsaudit",
		"pkg/ops/driftservice",
		"pkg/ops/backup",
		"pkg/ops/mcp",
	}
	for _, p := range want {
		info, err := os.Stat(filepath.Join(root, p))
		if err != nil || !info.IsDir() {
			t.Errorf("operability implementation package %q does not exist as a directory on disk: %v", p, err)
		}
	}
}
