// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// channelGuardDir holds the docs-tier guards over the communication
// channel section, and channelGuardPrefix is the prefix their file names
// carry.
const (
	channelGuardDir    = "tests/docs"
	channelGuardPrefix = "spec_28_"
)

// modulePath is the module the repository publishes, so an import of one
// of its own packages resolves to the directory that package sits in.
const modulePath = "github.com/lennylabs/lenny/"

// The forms a guard names a file it reads in: a specification path
// written whole, a specification file named by its numbered stem and
// joined to the spec directory at the call site, an import of a package
// of the migration tooling, a proposal document, and a root-level
// markdown record. A tooling package is a Go package the import graph
// reaches, and every other class is a non-Go input with a row in
// tests/non-go-inputs.yaml, so every one's routing is assertable.
//
// The root-level form reads a binding rather than any quoted file name,
// because a guard also writes such a name as an argument to a predicate
// it exercises. An argument names no file the guard reads; a binding
// names the document the guard opens.
var (
	specPathExpr      = regexp.MustCompile(`spec/[A-Za-z0-9_.-]+\.md`)
	specFileStemExpr  = regexp.MustCompile(`"(\d{2}_[a-z0-9-]+\.md)"`)
	toolingImportExpr = regexp.MustCompile(`"` + regexp.QuoteMeta(modulePath) + `(scripts/[A-Za-z0-9_/-]+)"`)
	proposalPathExpr  = regexp.MustCompile(`proposals/[A-Za-z0-9_.-]+\.md`)
	rootDocumentExpr  = regexp.MustCompile(`(?m)^(?:const |var )?\s*[A-Za-z][A-Za-z0-9_]*\s*=\s*"([A-Z][A-Z0-9-]*\.md)"`)
)

// TestChangedSourcesOfTheChannelGuardsSelectTheDocsTier pins the
// selection that the docs-tier guards over the communication channel
// section depend on. Those guards read the section text, the
// spec index, the sections that name a register writer, the shared
// predicates of the migration tooling, the proposal that records who
// creates the section, and the root-level record of what an apply run
// left open, and they hold each derived cell
// to a byte-exact literal so an edit to either side fails the case. That
// only stops drift when an edit to those files selects the docs tier,
// because `--changed` resolves a changed path through the import graph
// and tests/non-go-inputs.yaml, and a `--gate` or `--tier` bound narrows
// the resolved set rather than adding to it.
//
// The files are derived from the guards themselves rather than listed
// here, so a guard added over a specification file, a tooling package,
// or a document that no selection source routes to the docs tier fails
// this case instead of landing unhooked.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestChangedSourcesOfTheChannelGuardsSelectTheDocsTier(t *testing.T) {
	g, err := loadPkgGraph(context.Background(), repoRoot())
	if err != nil {
		t.Fatalf("load the package graph: %v", err)
	}
	inputs, err := readNonGoInputs(repoRoot())
	if err != nil {
		t.Fatalf("read the non-Go input list: %v", err)
	}

	paths, err := channelGuardSources(filepath.Join(repoRoot(), channelGuardDir))
	if err != nil {
		t.Fatalf("read the sources the channel guards hold: %v", err)
	}
	// The section the guards are written over is the one file every one
	// of them reads, so its absence means the derivation read no guard
	// and the case asserts nothing.
	if !containsString(paths, "spec/28_communication-channels.md") {
		t.Fatalf("the guards under %s resolve to %v, which omits the section they hold", channelGuardDir, paths)
	}
	// A guard exercises the shared predicates of the migration tooling
	// against the section text, so a tooling package is the second
	// population the derivation reads. Its absence means the derivation
	// reads the specification paths alone and the routing assertion below
	// passes vacuously over every package a guard imports.
	if !containsPrefixed(paths, "scripts/") {
		t.Fatalf("the guards under %s resolve to %v, which names no tooling package; the derivation reads no such path, so its routing is unasserted", channelGuardDir, paths)
	}
	// The ownership guards hold the record of who creates the section to
	// one proposal document and to the root-level record of what an apply
	// run left open. Both are edited by the very changes those guards
	// exist to catch, so an unrouted edit to either is the failure this
	// case pins; their absence from the derivation would pass it silently.
	if !containsPrefixed(paths, "proposals/") {
		t.Fatalf("the guards under %s resolve to %v, which names no proposal document", channelGuardDir, paths)
	}
	if !containsRootLevelDocument(paths) {
		t.Fatalf("the guards under %s resolve to %v, which names no root-level document", channelGuardDir, paths)
	}
	for _, path := range paths {
		tiers := selectedTiers(g, inputs, path)
		if !containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v, which omits the docs tier that guards it", path, tiers)
		}
	}
}

// selectedTiers returns the tiers a change to one source selects. A
// tooling package is named by its directory, so it stands for a change to
// a file in that directory.
func selectedTiers(g *pkgGraph, inputs *nonGoInputs, source string) []string {
	if _, ok := g.dirToPkg[source]; ok {
		source += "/doc.go"
	}
	sel := selectChanged([]string{source}, g, inputs, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth})
	return planNames(sel.plan())
}

// TestAGuardSourceOutsideTheDocsTargetsIsReported pins the two halves
// the routing assertion rests on, over a synthetic guard rather than
// over the landed tree: the derivation reads the file a guard names in
// whichever tree it sits, and a path that no package owns and no row of
// the non-Go input list routes to the docs tier resolves to a tier set
// without the docs tier.
//
// Without this case the derivation can shrink back to one form and the
// assertion over the landed guards keeps passing, because a path it
// never derives is a path it never resolves.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestAGuardSourceOutsideTheDocsTargetsIsReported(t *testing.T) {
	dir := t.TempDir()
	guard := "package docs_test\n\n" +
		"import _ \"" + modulePath + "scripts/specshift/unrouted\"\n\n" +
		"const held = \"spec/99_a-section-a-guard-reads.md\"\n\n" +
		"const stem = \"98_a-section-named-by-its-stem.md\"\n\n" +
		"const staged = \"proposals/0099_a-proposal-a-guard-reads.md\"\n\n" +
		"const record = \"A-RECORD-A-GUARD-READS.md\"\n"
	if err := os.WriteFile(filepath.Join(dir, channelGuardPrefix+"synthetic_test.go"), []byte(guard), 0o600); err != nil {
		t.Fatalf("write the synthetic guard: %v", err)
	}

	paths, err := channelGuardSources(dir)
	if err != nil {
		t.Fatalf("read the sources of the synthetic guard: %v", err)
	}
	g := newPkgGraph()
	g.addPackage(moduleImportPath+"/tests/docs", "tests/docs", nil, nil, nil)
	inputs := &nonGoInputs{Inputs: []nonGoInput{{Paths: []string{"tests/**"}, Runs: []string{"docs"}, Reason: "a row that covers none of the sources"}}}
	// Each form a guard names a routed file in has to be derived, and
	// each has to read as unrouted under a graph with no key covering it.
	for _, held := range []string{
		"spec/99_a-section-a-guard-reads.md",
		"spec/98_a-section-named-by-its-stem.md",
		"scripts/specshift/unrouted",
		"proposals/0099_a-proposal-a-guard-reads.md",
		"A-RECORD-A-GUARD-READS.md",
	} {
		if !containsString(paths, held) {
			t.Errorf("the synthetic guard resolves to %v, which omits %s, a source it reads", paths, held)
			continue
		}
		if tiers := selectedTiers(g, inputs, held); containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v with no source routing it, so an uncovered guard source reads as routed", held, tiers)
		}
	}
}

// containsRootLevelDocument reports whether list carries a root-level
// markdown document.
func containsRootLevelDocument(list []string) bool {
	for _, s := range list {
		if !strings.Contains(s, "/") && strings.HasSuffix(s, ".md") {
			return true
		}
	}
	return false
}

// channelGuardSources returns the tracked paths that the docs-tier guards
// over the communication channel section read, sorted and deduplicated.
func channelGuardSources(dir string) ([]string, error) {
	return channelGuardPaths(dir, func(text string, found map[string]bool) {
		for _, m := range specPathExpr.FindAllString(text, -1) {
			found[m] = true
		}
		for _, m := range specFileStemExpr.FindAllStringSubmatch(text, -1) {
			found["spec/"+m[1]] = true
		}
		for _, m := range toolingImportExpr.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
		for _, m := range proposalPathExpr.FindAllString(text, -1) {
			found[m] = true
		}
		for _, m := range rootDocumentExpr.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
	})
}

// channelGuardPaths applies collect to the source of every channel guard
// under dir and returns the sorted, deduplicated result.
func channelGuardPaths(dir string, collect func(text string, found map[string]bool)) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), channelGuardPrefix) || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		collect(string(source), found)
	}
	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// containsPrefixed reports whether list carries a path under prefix.
func containsPrefixed(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// containsString reports whether want is in list.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
