// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// channelGuardDir holds the tier-11 guards over the communication
// channel section, and channelGuardPrefix is the prefix their file names
// carry.
const (
	channelGuardDir    = "tests/tier11_docs"
	channelGuardPrefix = "spec_28_"
)

// modulePath is the module the repository publishes, so an import of one
// of its own packages resolves to the directory that package sits in.
const modulePath = "github.com/lennylabs/lenny/"

// The forms a guard names a routed file it reads in: a specification
// path written whole, a specification file named by its numbered stem
// and joined to the spec directory at the call site, and an import of a
// package of the migration tooling. Each of those three classes sits
// under a change-graph key, so each one's routing is assertable.
//
// A guard also reads two documents outside those classes, a proposal
// document and a root-level markdown record. Neither sits under a
// canonical root the change-graph path validator accepts, so neither is
// keyed and neither routes. Those two forms are derived separately below
// and held to that decision rather than to the docs tier.
var (
	specPathExpr      = regexp.MustCompile(`spec/[A-Za-z0-9_.-]+\.md`)
	specFileStemExpr  = regexp.MustCompile(`"(\d{2}_[a-z0-9-]+\.md)"`)
	toolingImportExpr = regexp.MustCompile(`"` + regexp.QuoteMeta(modulePath) + `(scripts/[A-Za-z0-9_/-]+)"`)
	proposalPathExpr  = regexp.MustCompile(`proposals/[A-Za-z0-9_.-]+\.md`)
	rootDocumentExpr  = regexp.MustCompile(`(?m)^(?:const |var )?\s*[A-Za-z][A-Za-z0-9_]*\s*=\s*"([A-Z][A-Z0-9-]*\.md)"`)
)

// TestChangedSourcesOfTheChannelGuardsSelectTheDocsTier pins the
// change-graph routing that the tier-11 guards over the communication
// channel section depend on. Those guards read the section text, the
// spec index, the sections that name a register writer, and the shared
// predicates of the migration tooling, and they hold each derived cell
// to a byte-exact literal so an edit to either side fails the case. That
// only stops drift when an edit to those files selects the docs tier,
// because `--changed` resolves a changed path against the change-graph
// keys and `--max-tier` caps the resolved set rather than adding to it.
//
// The files are derived from the guards themselves rather than listed
// here, so a guard added over a specification file or a tooling package
// whose change-graph key names no docs target fails this case instead of
// landing unhooked.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestChangedSourcesOfTheChannelGuardsSelectTheDocsTier(t *testing.T) {
	globs := readChangeGraphGlobs(t)

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
	for _, path := range paths {
		tiers := tiersForChangedPathIn(globs, path)
		if !containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v, which omits the docs tier that guards it", path, tiers)
		}
	}
}

// TestAGuardSourceOutsideTheDocsTargetsIsReported pins the two halves
// the routing assertion rests on, over a synthetic guard rather than
// over the landed tree: the derivation reads the file a guard names in
// whichever tree it sits, and a path whose change-graph key carries no
// docs target resolves to a tier set without the docs tier.
//
// Without this case the derivation can shrink back to one form and the
// assertion over the landed guards keeps passing, because a path it
// never derives is a path it never resolves.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestAGuardSourceOutsideTheDocsTargetsIsReported(t *testing.T) {
	dir := t.TempDir()
	guard := "package tier11_docs_test\n\n" +
		"import _ \"" + modulePath + "scripts/specshift/unrouted\"\n\n" +
		"const held = \"spec/99_a-section-a-guard-reads.md\"\n\n" +
		"const stem = \"98_a-section-named-by-its-stem.md\"\n"
	if err := os.WriteFile(filepath.Join(dir, channelGuardPrefix+"synthetic_test.go"), []byte(guard), 0o600); err != nil {
		t.Fatalf("write the synthetic guard: %v", err)
	}

	paths, err := channelGuardSources(dir)
	if err != nil {
		t.Fatalf("read the sources of the synthetic guard: %v", err)
	}
	globs := map[string]map[string][]string{
		"tests/": {"docs": []string{"tests/tier11_docs/..."}},
	}
	// Each form a guard names a routed file in has to be derived, and
	// each has to read as unrouted under a graph with no key covering it.
	for _, held := range []string{
		"spec/99_a-section-a-guard-reads.md",
		"spec/98_a-section-named-by-its-stem.md",
		"scripts/specshift/unrouted",
	} {
		if !containsString(paths, held) {
			t.Errorf("the synthetic guard resolves to %v, which omits %s, a source it reads", paths, held)
			continue
		}
		if tiers := tiersForChangedPathIn(globs, held); containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v under a graph with no key covering it, so an uncovered guard source reads as routed", held, tiers)
		}
	}
}

// TestTheDocumentsTheChannelGuardsReadOutsideTheKeyedRootsSelectNoTier
// pins the standing decision about the two documents the channel guards
// read that no change-graph key covers: the proposal that records who
// creates the section, and the root-level record of what an apply run
// left open. Both sit outside the canonical roots the change-graph path
// validator accepts, so keying them would fail the static tier, and
// neither is keyed. An edit that touches one of them alone therefore
// selects no tier and fires none of the guards that read it.
//
// The case asserts that dependence rather than leaving it silent: the
// documents are derived from the guards, each is held to an empty tier
// set, and a graph keyed on either one is held to fail the validator
// that rejects it. A later change that brings one of those documents
// under a key fails here, which is the point at which the routing
// assertion above should grow to cover it.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers); TESTING.md
// §5 (tests/change-graph.json maps source packages, schemas,
// migrations, and chart templates to the tests that exercise them)
func TestTheDocumentsTheChannelGuardsReadOutsideTheKeyedRootsSelectNoTier(t *testing.T) {
	globs := readChangeGraphGlobs(t)

	documents, err := channelGuardUnkeyedDocuments(filepath.Join(repoRoot(), channelGuardDir))
	if err != nil {
		t.Fatalf("read the unkeyed documents the channel guards hold: %v", err)
	}
	// Both forms have to be derived, or the assertions below run over a
	// short list and say nothing about the class they omit.
	if !containsPrefixed(documents, "proposals/") {
		t.Fatalf("the guards under %s resolve to %v, which names no proposal document", channelGuardDir, documents)
	}
	if !containsRootLevelDocument(documents) {
		t.Fatalf("the guards under %s resolve to %v, which names no root-level document", channelGuardDir, documents)
	}
	for _, document := range documents {
		if tiers := tiersForChangedPathIn(globs, document); len(tiers) != 0 {
			t.Errorf("a change to %s selects %v; it is keyed now, so the docs-tier routing assertion has to cover it", document, tiers)
		}
		if res := validateChangeGraphPathsOverKey(t, document); res.ok {
			t.Errorf("a change graph keyed on %s passes the path validator, so the key it was denied is now available to it", document)
		}
	}
}

// validateChangeGraphPathsOverKey runs the change-graph path validator
// over a graph carrying key alone, so a caller can assert whether that
// key is one the validator accepts.
func validateChangeGraphPathsOverKey(t *testing.T, key string) checkResult {
	t.Helper()
	graph := map[string]any{
		"globs": map[string]any{
			key: map[string]any{"docs": []string{"tests/tier11_docs/..."}},
		},
	}
	body, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("encode the probe change graph: %v", err)
	}
	path := filepath.Join(t.TempDir(), "change-graph.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the probe change graph: %v", err)
	}
	return validateChangeGraphPaths(path, repoRoot())
}

// readChangeGraphGlobs returns the committed change graph's glob table.
func readChangeGraphGlobs(t *testing.T) map[string]map[string][]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(), changeGraphFile))
	if err != nil {
		t.Fatalf("read %s: %v", changeGraphFile, err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", changeGraphFile, err)
	}
	return doc.Globs
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

// channelGuardSources returns the tracked paths under a change-graph key
// that the tier-11 guards over the communication channel section read,
// sorted and deduplicated.
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
	})
}

// channelGuardUnkeyedDocuments returns the documents those same guards
// read that no change-graph key covers, sorted and deduplicated.
//
// The root-level form reads a binding rather than any quoted file name,
// because a guard also writes such a name as an argument to a predicate
// it exercises. An argument names no file the guard reads; a binding
// names the document the guard opens.
func channelGuardUnkeyedDocuments(dir string) ([]string, error) {
	return channelGuardPaths(dir, func(text string, found map[string]bool) {
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
