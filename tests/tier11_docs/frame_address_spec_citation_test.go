// SPDX-License-Identifier: MIT

// Tier-11 consistency check between a `// spec:` citation and the section it
// names, for the frame-addressing rule.
//
// §28.5.3 states how a session-scoped JSON Lines frame is addressed: the
// adapter and the gateway populate the per-session identifier on every frame
// in the set, and a frame that carries none resolves against the receiving
// stream's own binding when the pod holds at most one slot and is otherwise
// rejected. §4.6.1 is the warm pool controller's pod lifecycle and states
// nothing about frame addressing.
//
// The harness maps a test to its spec sections through the `// spec:`
// annotation, so a citation that names §4.6.1 for an addressing behavior
// registers that behavior against the warm pool controller and leaves a
// reviewer unable to trace it. The two sections are adjacent in a reader's
// mind because both talk about pods, which is what makes the substitution easy
// to write and hard to see.
//
// The check scans the tracked Go sources for a citation entry that names
// §4.6.1 and describes an addressing behavior in its parenthetical. Every
// legitimate §4.6.1 citation in the tree describes a claim, a hold, a queue,
// or an occupancy projection, so the addressing vocabulary below is the
// discriminator.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.6.1 (warm pool controller — pod lifecycle), 28.5.3 (intra-pod frame
// addressing)

package tier11_docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// warmPoolCitation matches one citation entry naming §4.6.1 together with the
// parenthetical that describes what the citing code takes from it.
var warmPoolCitation = regexp.MustCompile(`§?4\.6\.1\s*\(([^)]*)\)`)

// addressingVocabulary is the wording that marks a parenthetical as describing
// the §28.5.3 frame-addressing rule rather than a warm-pool behavior. A
// citation that names §4.6.1 and uses any of these words is citing the wrong
// section.
var addressingVocabulary = []string{
	"frame",
	"envelope",
	"session-scoped",
	"tracing",
	"addressed",
	"absent address",
}

// trackedGoFiles returns every tracked Go file in the repository, excluding the
// fixture trees, which carry deliberately malformed citations.
func trackedGoFiles(t testing.TB, root string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "-z", "*.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list tracked go files: %v", err)
	}

	var files []string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "" || strings.Contains(rel, "testdata/") {
			continue
		}
		files = append(files, rel)
	}
	if len(files) == 0 {
		t.Fatal("no tracked go files found")
	}
	return files
}

// spec: 4.6.1, 28.5.3
// diagnosis: a `// spec:` citation names §4.6.1 for a frame-addressing
//
//	behavior. §4.6.1 is the warm pool controller's pod lifecycle; the rule that
//	populates the per-session identifier on every session-scoped frame and
//	resolves or rejects a frame that carries none is §28.5.3. The citation
//	registers the behavior against the wrong section in the test-to-spec map
//	and misdirects a reviewer tracing the behavior to its source. Rewrite the
//	entry to §28.5.3, keeping the parenthetical as written.
func TestFrameAddressingCitesTheIntraPodSection(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range trackedGoFiles(t, root) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		// A citation wraps across comment lines, so the comment markers and
		// the line breaks are folded away before the entry is matched.
		folded := strings.ReplaceAll(string(body), "\n", " ")
		folded = strings.ReplaceAll(folded, "//", " ")

		for _, m := range warmPoolCitation.FindAllStringSubmatch(folded, -1) {
			parenthetical := strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
			for _, word := range addressingVocabulary {
				if strings.Contains(parenthetical, word) {
					t.Errorf("%s cites §4.6.1 for a frame-addressing behavior (%q); §28.5.3 states that rule and §4.6.1 is the warm pool controller", rel, m[1])
					break
				}
			}
		}
	}
}
