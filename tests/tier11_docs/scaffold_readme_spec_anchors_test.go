// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the §26.12 scaffolder README pointer.
// This test is NOT under a build tag because it exercises the
// repository state directly — no external infrastructure required.

package tier11_docs_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-ctl/runtimescaffold"
)

// sectionCitation matches a "§X.Y" or "§X.Y.Z" spec-section citation,
// such as the "§26.12" and "§15.4.6" markers the scaffolded README
// carries.
var sectionCitation = regexp.MustCompile(`§(\d+(?:\.\d+)+)`)

// specHeadingNumber matches an ATX heading whose text opens with a
// dotted section number, such as "### 26.12 Adding a new reference
// runtime" or "#### 15.4.6 Conformance Test Suite". It does not match
// the bare top-level heading ("## 26. Reference Runtime Catalog")
// because the period sits directly after the number with no
// intervening space, which is fine: citations this test checks are
// always dotted multi-part numbers, never a bare top-level one.
var specHeadingNumber = regexp.MustCompile(`^#{2,6}\s+(\d+(?:\.\d+)+)\s+\S`)

// specFileForTopLevelSection returns the spec/ file whose name carries
// the two-digit zero-padded prefix for a top-level section number
// (spec files are named spec/NN_slug.md).
func specFileForTopLevelSection(specDir string, topLevel int) (string, error) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return "", fmt.Errorf("read spec dir: %w", err)
	}
	prefix := fmt.Sprintf("%02d_", topLevel)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return filepath.Join(specDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no spec file with prefix %q under %s", prefix, specDir)
}

// specHeadingNumbers reads a spec markdown file and returns the set of
// dotted section numbers its ATX headings declare.
func specHeadingNumbers(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	nums := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := specHeadingNumber.FindStringSubmatch(line); m != nil {
			nums[m[1]] = true
		}
	}
	return nums
}

// diagnosis: the scaffolder's README template (§26.12) cites a spec
// section — most notably §26.12 itself and the §15.4.6 conformance
// suite it points a runtime author at — that no longer exists as a
// live heading. A runtime author who follows the scaffolded README's
// stated onramp lands on a stale reference. The existing scaffolder
// unit test (cmd/lenny-ctl/runtimescaffold) pins that the README's text
// markers are present in the rendered file; it does not resolve those
// section numbers against the spec itself, so a spec renumbering or
// heading rename that orphans "§26.12" or "§15.4.6" would not be
// caught anywhere. This test scaffolds a runtime through the same
// in-process Generate call and resolves every §-cited section number
// in the emitted README against the corresponding spec file's live
// headings.
//
// spec: §26.12 (Adding a new reference runtime — appendix entry for
// maintainer review), §15.4.6 (Conformance Test Suite).
func TestScaffoldedREADMESpecAnchorsResolveToLiveSections(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runtimescaffold.Generate(runtimescaffold.Spec{
		Name:     "anchor-check",
		Language: runtimescaffold.LangGo,
		Template: runtimescaffold.TemplateMinimal,
	}, base, &stdout, &stderr); code != runtimescaffold.ExitOK {
		t.Fatalf("scaffold generate: exit %d, stderr=%q", code, stderr.String())
	}

	readmePath := filepath.Join(base, "anchor-check", "README.md")
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read scaffolded README: %v", err)
	}
	body := string(raw)

	matches := sectionCitation.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("scaffolded README carries no §-prefixed spec-section citation to resolve")
	}

	seen := map[string]bool{}
	for _, m := range matches {
		num := m[1]
		if seen[num] {
			continue
		}
		seen[num] = true

		topLevelStr := num
		if i := strings.Index(num, "."); i >= 0 {
			topLevelStr = num[:i]
		}
		topLevel, err := strconv.Atoi(topLevelStr)
		if err != nil {
			t.Errorf("scaffolded README citation §%s: could not parse top-level section number: %v", num, err)
			continue
		}

		specFile, err := specFileForTopLevelSection(specDir, topLevel)
		if err != nil {
			t.Errorf("scaffolded README cites §%s but no spec file covers top-level section %d: %v",
				num, topLevel, err)
			continue
		}

		headings := specHeadingNumbers(t, specFile)
		if !headings[num] {
			t.Errorf("scaffolded README cites §%s (expected in spec/%s) but no heading in that file declares section %s",
				num, filepath.Base(specFile), num)
		}
	}

	// The finding this test closes names §26.12 and §15.4.6 as the
	// scaffolded README's runtime-author onramp anchors. Confirm both
	// are still present in the rendered README, not just resolvable in
	// the abstract: an edit that dropped the citation entirely would
	// otherwise pass the loop above with zero checks performed for it.
	for _, want := range []string{"26.12", "15.4.6"} {
		if !seen[want] {
			t.Errorf("scaffolded README no longer cites §%s; the runtime-author onramp reference is missing", want)
		}
	}
}
