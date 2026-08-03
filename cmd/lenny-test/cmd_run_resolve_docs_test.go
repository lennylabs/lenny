// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestChangedSpecAndReservedPhraseSourcesSelectTheDocsTier pins the
// change-graph routing that the tier-11 guards over the communication
// channel section depend on. Those guards read the section text, the
// spec index, the sections that name a register writer, and the shared
// reserved-phrase predicate, and they hold each derived cell to a
// byte-exact literal so an edit to either side fails the case. That
// only stops drift when an edit to those files selects the docs tier,
// because `--changed` resolves a changed path against the change-graph
// keys and `--max-tier` caps the resolved set rather than adding to it.
// A key edit that drops the docs target unhooks the guards silently,
// so this case fails instead.
//
// spec: 28.1 (channel naming law), 28.3 (channel registers)
func TestChangedSpecAndReservedPhraseSourcesSelectTheDocsTier(t *testing.T) {
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

	paths := []string{
		"spec/28_communication-channels.md",
		"spec/README.md",
		"spec/04_system-components.md",
		"spec/07_session-lifecycle.md",
		"spec/12_storage-architecture.md",
		"scripts/specshift/scope/scope.go",
	}
	for _, path := range paths {
		tiers := tiersForChangedPathIn(doc.Globs, path)
		if !containsString(tiers, "docs") {
			t.Errorf("a change to %s selects %v, which omits the docs tier that guards it", path, tiers)
		}
	}
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
