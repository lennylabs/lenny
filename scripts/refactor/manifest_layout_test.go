// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

// spec: §4.1 (the gateway is one component internally partitioned into
// subsystem boundaries; each §4.1 subsystem stays cohesive in one group subtree
// so a future per-pod extraction is a directory-subtree move rather than a
// scattered cherry-pick).

// repoRootFromTest returns the repository root relative to the driver package
// directory (scripts/refactor), which is where `go test` runs. The tests that
// validate the committed manifest against the real tree read files under the
// repo root, so they need it resolved from the package location rather than from
// a temp fixture.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

// loadCommittedManifest parses the real committed manifest
// (scripts/refactor/manifest) the driver consumes, so a test validates the
// landed layout against the manifest that is supposed to describe it.
func loadCommittedManifest(t *testing.T, root string) []rewrite.Move {
	t.Helper()
	f, err := os.Open(filepath.Join(root, "scripts", "refactor", "manifest"))
	if err != nil {
		t.Fatalf("open committed manifest: %v", err)
	}
	defer f.Close()
	moves, err := rewrite.ParseManifest(f)
	if err != nil {
		t.Fatalf("parse committed manifest: %v", err)
	}
	return moves
}

// TestCommittedManifestDestinationsExist asserts that every destination in the
// committed manifest names a directory that exists in the tree, so the manifest
// is a faithful description of the landed layout rather than drifting from it.
// A move whose destination directory is missing (for example a manifest entry
// still pointing at a pre-move top-level location while the package moved
// elsewhere) fails here.
func TestCommittedManifestDestinationsExist(t *testing.T) {
	root := repoRootFromTest(t)
	for _, m := range loadCommittedManifest(t, root) {
		dest := filepath.Join(root, filepath.FromSlash(rewrite.RepoRel(m.New)))
		info, err := os.Stat(dest)
		if err != nil {
			t.Errorf("manifest destination %s does not exist: %v", rewrite.RepoRel(m.New), err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("manifest destination %s is not a directory", rewrite.RepoRel(m.New))
		}
	}
}

// TestMCPFabricSubsystemIsOneExtractableSubtree pins the §4.1 subsystem-seam
// invariant that proposal 0020 §2 records for the MCP Fabric subsystem: the
// delegation-orchestration packages, including the delegation-tree lifecycle
// workers §4.1 assigns to MCP Fabric (its extraction trigger is
// lenny_mcp_fabric_active_delegations), stay cohesive within the mcpfabric/
// subtree so a future per-pod extraction is a subtree move. This test would fail
// against the pre-fix layout, which split the delegation-tree lifecycle packages
// into a sibling top-level pkg/gateway/delegationtree/ group.
func TestMCPFabricSubsystemIsOneExtractableSubtree(t *testing.T) {
	root := repoRootFromTest(t)
	gatewayDir := filepath.Join(root, "pkg", "gateway")

	// The pre-fix layout had a top-level pkg/gateway/delegationtree/ group. The
	// corrected layout nests the whole concern under mcpfabric/, so no such
	// top-level directory may exist.
	if _, err := os.Stat(filepath.Join(gatewayDir, "delegationtree")); err == nil {
		t.Errorf("pkg/gateway/delegationtree exists as a top-level group: the delegation-tree " +
			"lifecycle packages must nest under pkg/gateway/mcpfabric/delegationtree so the MCP " +
			"Fabric subsystem stays one extractable subtree (proposal 0020 §2)")
	}

	// The delegation-tree lifecycle packages must live under the mcpfabric/
	// subtree at mcpfabric/delegationtree/<pkg>.
	fabricTree := filepath.Join(gatewayDir, "mcpfabric", "delegationtree")
	for _, pkg := range []string{
		"deadlock", "leasecontrol", "orphancleanup", "resultrollup",
		"treearchive", "treebudget", "treerecovery",
	} {
		if info, err := os.Stat(filepath.Join(fabricTree, pkg)); err != nil || !info.IsDir() {
			t.Errorf("expected delegation-tree package under pkg/gateway/mcpfabric/delegationtree/%s: %v", pkg, err)
		}
	}

	// Every manifest destination that names a §4.1-delegation package must sit
	// inside the mcpfabric/ subtree, so no delegation package escapes the
	// subsystem into a sibling top-level group. This walks the committed manifest
	// rather than a hard-coded list, so a future manifest entry that re-splits a
	// delegation package out of mcpfabric/ is caught.
	for _, m := range loadCommittedManifest(t, root) {
		rel := rewrite.RepoRel(m.New)
		if !isDelegationTreePackage(rel) {
			continue
		}
		if !strings.HasPrefix(rel, "pkg/gateway/mcpfabric/") {
			t.Errorf("delegation-tree package %s is grouped outside the mcpfabric/ subtree; it must "+
				"nest under pkg/gateway/mcpfabric/ so the MCP Fabric subsystem is one extractable subtree", rel)
		}
	}
}

// isDelegationTreePackage reports whether a repo-relative gateway package path
// names one of the §8 delegation-tree lifecycle packages the MCP Fabric
// subsystem orchestrates, identified by its leaf name so the check is
// independent of the group directory it currently sits under.
func isDelegationTreePackage(rel string) bool {
	leaf := rel[strings.LastIndexByte(rel, '/')+1:]
	switch leaf {
	case "deadlock", "leasecontrol", "orphancleanup", "resultrollup",
		"treearchive", "treebudget", "treerecovery":
		return true
	default:
		return false
	}
}
