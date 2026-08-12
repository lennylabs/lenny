// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The heading walker asserts that every in-scope specification heading carries
// an index entry in `spec/README.md` whose anchor resolves.
//
// The index is how a reader finds a section without knowing which file holds
// it, and it is the only surface that lists the specification as a whole. A
// heading absent from it is unreachable by that route, and the absence is
// invisible: nothing else in the tree reads the index, so a section can be
// written, cited and implemented while never appearing there. Forty-two
// subsection headings were missing when this gate landed, forty of them the
// whole of the build-sequence file's phase list.
//
// In scope are the `## N` and `### N.M` headings under `spec/`, which is the
// depth the index is written to, together with the §28.5 contract cards. A
// deeper heading is in scope only where the index already carries one at that
// depth, because the index does not descend uniformly and a gate that demanded
// it would be asking for a different index rather than checking this one.
//
// The anchor is checked as well as the entry. An index row whose target does
// not resolve is worse than a missing row: it tells a reader the section exists
// and sends them nowhere, and the fragment-link gate reads the row as a link
// like any other, so the two gates agree on what resolving means.

const specIndexPath = "spec/README.md"

// indexedHeading matches a heading the index is written to: a section or a
// subsection, numbered.
var indexedHeading = regexp.MustCompile(`^(#{2,3})\s+(\d+(?:\.\d+)?)\s+(.+)$`)

// cardHeading matches a §28.5 contract card, which is deeper than the index's
// usual depth and carried deliberately because the cards are the citable
// handles the naming law creates.
var cardHeading = regexp.MustCompile(`^#{3,4}\s+(28\.5\.\d+)\s+(.+)$`)

// indexRow matches a markdown link target in the index.
var indexRow = regexp.MustCompile(`\]\(([^)]+)\)`)

// headingSlug renders a heading the way GitHub anchors it: punctuation is
// dropped and every remaining space becomes one hyphen. The spaces a dropped
// character leaves behind are significant, so "Phase 0 — Bootstrap" anchors as
// "phase-0--bootstrap" with two hyphens, and collapsing them produces a slug
// that resolves nowhere.
func headingSlug(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	s = regexp.MustCompile("[`*_\\[\\]()]").ReplaceAllString(s, "")
	s = regexp.MustCompile(`[^\w\s-]`).ReplaceAllString(s, "")
	return strings.Trim(strings.ReplaceAll(s, " ", "-"), "-")
}

// headingWalkerFinding is one heading the index does not reach.
type headingWalkerFinding struct {
	File    string
	Number  string
	Title   string
	Anchor  string
	Missing string
}

// runHeadingWalker reports every in-scope heading with no resolving index entry.
func runHeadingWalker(list scope.Lister, read scope.FileReader) ([]headingWalkerFinding, int, error) {
	files, err := list(context.Background())
	if err != nil {
		return nil, 0, fmt.Errorf("list the tracked tree: %w", err)
	}
	body, err := read(specIndexPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", specIndexPath, err)
	}
	indexed := map[string]bool{}
	for _, m := range indexRow.FindAllStringSubmatch(string(body), -1) {
		indexed[m[1]] = true
	}

	var findings []headingWalkerFinding
	inScope := 0
	for _, f := range files {
		if !strings.HasPrefix(f, "spec/") || !strings.HasSuffix(f, ".md") || strings.HasSuffix(f, "README.md") {
			continue
		}
		base := f[strings.LastIndex(f, "/")+1:]
		src, err := read(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			var number, title string
			switch {
			case indexedHeading.MatchString(line):
				m := indexedHeading.FindStringSubmatch(line)
				number, title = m[2], m[3]
			case cardHeading.MatchString(line):
				m := cardHeading.FindStringSubmatch(line)
				number, title = m[1], m[2]
			default:
				continue
			}
			inScope++
			anchor := base + "#" + headingSlug(number+" "+title)
			if indexed[anchor] {
				continue
			}
			// A top-level section is indexed by its file alone.
			if !strings.Contains(number, ".") && indexed[base] {
				continue
			}
			findings = append(findings, headingWalkerFinding{
				File: f, Number: number, Title: strings.TrimSpace(title), Anchor: anchor,
				Missing: "no index entry",
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Number < findings[j].Number
	})
	return findings, inScope, nil
}

// spec: 28.2 (citable handles), 28.5 (contract cards)
// diagnosis: a specification heading exists that `spec/README.md` does not
// list, so a reader working from the index cannot reach that section. Add the
// index row beneath its file's top-level entry, with an anchor that resolves.
func TestEverySpecificationHeadingCarriesAnIndexEntry(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	findings, inScope, err := runHeadingWalker(scope.GitLister(root), scope.DirReader(root))
	if err != nil {
		t.Fatalf("heading walker: %v", err)
	}
	if inScope == 0 {
		t.Fatalf("the walk found no in-scope heading; a walker that read nothing certifies nothing")
	}
	for _, f := range findings {
		t.Errorf("%s §%s %q has %s in %s (expected anchor %q)",
			f.File, f.Number, f.Title, f.Missing, specIndexPath, f.Anchor)
	}
	t.Logf("%d in-scope heading(s) read, %d without an index entry", inScope, len(findings))
}

// spec: 28.2 (citable handles)
// diagnosis: the walker's slug no longer matches the anchors GitHub generates,
// so it would report a correct index row as missing, or accept one that
// resolves nowhere.
func TestHeadingWalkerSlugMatchesTheRenderedAnchor(t *testing.T) {
	t.Parallel()
	cases := []struct{ heading, want string }{
		{"17.1 Kubernetes Resources", "171-kubernetes-resources"},
		{"18.3 Phase 0 — Bootstrap the infrastructure repo", "183-phase-0--bootstrap-the-infrastructure-repo"},
		{"17.4 Local Development Mode (`lenny-dev`)", "174-local-development-mode-lenny-dev"},
		{"28.5.3 Intra-pod", "2853-intra-pod"},
		{"23.1 Why Lenny?", "231-why-lenny"},
	}
	for _, c := range cases {
		if got := headingSlug(c.heading); got != c.want {
			t.Errorf("headingSlug(%q) = %q, want %q", c.heading, got, c.want)
		}
	}
}
