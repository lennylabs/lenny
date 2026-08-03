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

// The three forms a guard names a file it reads in: a specification path
// written whole, a specification file named by its numbered stem and
// joined to the spec directory at the call site, and an import of a
// package of the migration tooling.
var (
	specPathExpr      = regexp.MustCompile(`spec/[A-Za-z0-9_.-]+\.md`)
	specFileStemExpr  = regexp.MustCompile(`"(\d{2}_[a-z0-9-]+\.md)"`)
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
	for _, path := range paths {
		tiers := tiersForChangedPathIn(doc.Globs, path)
		if !containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v, which omits the docs tier that guards it", path, tiers)
		}
	}
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

// containsString reports whether want is in list.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
