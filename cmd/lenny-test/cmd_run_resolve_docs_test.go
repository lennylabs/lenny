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

// The forms a guard names a file it reads in: a specification path
// written whole, a specification file named by its numbered stem and
// joined to the spec directory at the call site, a proposal document
// written whole, a root-level markdown document bound to a name, and an
// import of a package of the migration tooling.
//
// A guard over a proposal document holds the record of who creates a
// specification file to one document, so the document it reads is a
// source of the guard on the same terms as the section text is, and it
// has to select the docs tier the same way. The same guard reads the
// durable record of what an apply run left open, which is a root-level
// document, so that form is derived too.
//
// The root-level form reads a binding rather than any quoted file name,
// because a guard also writes such a name as an argument to a predicate
// it exercises. An argument names no file the guard reads and routes
// nothing; a binding names the document the guard opens.
var (
	specPathExpr      = regexp.MustCompile(`spec/[A-Za-z0-9_.-]+\.md`)
	specFileStemExpr  = regexp.MustCompile(`"(\d{2}_[a-z0-9-]+\.md)"`)
	proposalPathExpr  = regexp.MustCompile(`proposals/[A-Za-z0-9_.-]+\.md`)
	rootDocumentExpr  = regexp.MustCompile(`(?m)^(?:const |var )?\s*[A-Za-z][A-Za-z0-9_]*\s*=\s*"([A-Z][A-Z0-9-]*\.md)"`)
	toolingImportExpr = regexp.MustCompile(`"` + regexp.QuoteMeta(modulePath) + `(scripts/[A-Za-z0-9_/-]+)"`)
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
// here, so a guard added over a file whose change-graph key names no
// docs target fails this case instead of landing unhooked.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestChangedSourcesOfTheChannelGuardsSelectTheDocsTier(t *testing.T) {
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
	// A guard holds the record of who creates that section to one
	// proposal document, so a proposal path is the second population the
	// derivation reads. Its absence means the derivation reads the spec
	// paths alone and the routing assertion below passes vacuously over
	// every document a guard reads outside spec/.
	if !containsPrefixed(paths, "proposals/") {
		t.Fatalf("the guards under %s resolve to %v, which names no proposal document; the derivation reads no such path, so its routing is unasserted", channelGuardDir, paths)
	}
	// A guard also holds that record against the durable record of what
	// an apply run left open, which is a root-level document rather than
	// a path under a directory the graph already keys. Its absence means
	// the derivation reads no such document, so the routing assertion
	// below passes vacuously over that whole file class.
	if !containsRootLevelDocument(paths) {
		t.Fatalf("the guards under %s resolve to %v, which names no root-level document; the derivation reads no such path, so its routing is unasserted", channelGuardDir, paths)
	}
	for _, path := range paths {
		tiers := tiersForChangedPathIn(doc.Globs, path)
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
// Without this case the derivation can shrink back to the spec paths
// alone and the assertion over the landed guards keeps passing, because
// a path it never derives is a path it never resolves.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestAGuardSourceOutsideTheDocsTargetsIsReported(t *testing.T) {
	dir := t.TempDir()
	guard := "package tier11_docs_test\n\n" +
		"const held = \"proposals/9999_fix_a-document-a-guard-reads.md\"\n\n" +
		"const record = \"A-RECORD-A-GUARD-READS.md\"\n"
	if err := os.WriteFile(filepath.Join(dir, channelGuardPrefix+"synthetic_test.go"), []byte(guard), 0o600); err != nil {
		t.Fatalf("write the synthetic guard: %v", err)
	}

	paths, err := channelGuardSources(dir)
	if err != nil {
		t.Fatalf("read the sources of the synthetic guard: %v", err)
	}
	globs := map[string]map[string][]string{
		"spec/": {"docs": []string{"tests/tier11_docs/..."}},
	}
	// Both forms a guard names a document it reads in: a path under a
	// directory, and a root-level document. Each has to be derived, and
	// each has to read as unrouted under a graph with no key covering it.
	for _, held := range []string{
		"proposals/9999_fix_a-document-a-guard-reads.md",
		"A-RECORD-A-GUARD-READS.md",
	} {
		if !containsString(paths, held) {
			t.Errorf("the synthetic guard resolves to %v, which omits %s, a document it reads", paths, held)
			continue
		}
		if tiers := tiersForChangedPathIn(globs, held); containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v under a graph with no key covering it, so an uncovered guard source reads as routed", held, tiers)
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

// channelGuardSources returns the tracked paths the tier-11 guards over
// the communication channel section read, sorted and deduplicated.
func channelGuardSources(dir string) ([]string, error) {
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
		text := string(source)
		for _, m := range specPathExpr.FindAllString(text, -1) {
			found[m] = true
		}
		for _, m := range specFileStemExpr.FindAllStringSubmatch(text, -1) {
			found["spec/"+m[1]] = true
		}
		for _, m := range proposalPathExpr.FindAllString(text, -1) {
			found[m] = true
		}
		for _, m := range rootDocumentExpr.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
		for _, m := range toolingImportExpr.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
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
