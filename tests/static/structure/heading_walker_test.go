// SPDX-License-Identifier: MIT

package structure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The heading walker asserts that every in-scope specification heading carries
// an index entry in `spec/README.md` whose anchor resolves. Change 0116 removed
// its second conjunct, a `tests/spec-map.json` key or a
// `tests/spec-map-exceptions.yaml` entry for each heading, when it retired both
// files: the annotation index records which tests cite a section, and the
// coverage view of `lenny-test coverage --spec` lists a heading no test cites.
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

// numberedHeading matches any numbered heading, at any depth and with a
// section number of any number of parts. Which of those headings the walker
// evaluates is decided by inHeadingDomain rather than by the match, because
// the domain at depths below the subsection depends on what the index carries.
var numberedHeading = regexp.MustCompile(`^(#{2,})\s+(\d+(?:\.\d+)*)\s+(.+)$`)

// cardSection matches the number of a §28.5 contract card, which is deeper
// than the index's usual depth and in the domain unconditionally because the
// cards are the citable handles the naming law creates.
var cardSection = regexp.MustCompile(`^28\.5\.\d+$`)

// indexRow matches a markdown link target in the index.
var indexRow = regexp.MustCompile(`\]\(([^)]+)\)`)

// indexLabel matches the section number an index row's link label opens with,
// as in `- [24.19.1 Image Management](...)`. The number is how the walker
// decides whether the index carries a heading, so a row whose anchor does not
// resolve still puts its heading in the domain and is reported there.
var indexLabel = regexp.MustCompile(`\[(\d+(?:\.\d+)*)[ .]`)

// inHeadingDomain reports whether the walker evaluates a heading. The `## N`
// and `### N.M` headings are in the domain because that is the depth the index
// is written to. Below that depth the domain is what the index already
// carries, together with the §28.5 contract cards, because the index does not
// descend uniformly and a gate that demanded it would be asking for a
// different index rather than checking this one.
func inHeadingDomain(marker, number string, indexedNumbers map[string]bool) bool {
	switch {
	case len(marker) <= 3 && strings.Count(number, ".") <= 1:
		return true
	case cardSection.MatchString(number):
		return true
	default:
		return indexedNumbers[number]
	}
}

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

// headingWalkerFinding is an in-scope heading the index does not reach.
type headingWalkerFinding struct {
	File    string
	Number  string
	Title   string
	Anchor  string
	Missing string
}

// runHeadingWalker reports every in-scope heading whose index row is missing or
// does not resolve.
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
	indexedNumbers := map[string]bool{}
	for _, m := range indexLabel.FindAllStringSubmatch(string(body), -1) {
		indexedNumbers[m[1]] = true
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
			m := numberedHeading.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			marker, number, title := m[1], m[2], m[3]
			if !inHeadingDomain(marker, number, indexedNumbers) {
				continue
			}
			inScope++
			anchor := base + "#" + headingSlug(number+" "+title)
			// A top-level section is indexed by its file alone.
			indexResolves := indexed[anchor] ||
				(!strings.Contains(number, ".") && indexed[base])
			report := func(missing string) {
				findings = append(findings, headingWalkerFinding{
					File: f, Number: number, Title: strings.TrimSpace(title), Anchor: anchor,
					Missing: missing,
				})
			}
			if !indexResolves {
				report("no index entry")
			}
		}
	}
	if inScope == 0 {
		return nil, 0, fmt.Errorf("run the heading walker: no in-scope heading was read; "+
			"a report of no finding over an unread tree certifies nothing (%d tracked file(s) listed)",
			len(files))
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Number != findings[j].Number {
			return findings[i].Number < findings[j].Number
		}
		return findings[i].Missing < findings[j].Missing
	})
	return findings, inScope, nil
}

// spec: 28.2 (citable handles), 28.5 (contract cards)
// diagnosis: a specification heading exists that `spec/README.md` does not
// list, or lists behind an anchor that resolves nowhere. A reader working from
// the index cannot reach an unlisted section. Add the index row beneath its
// file's top-level entry with an anchor that resolves.
func TestEverySpecificationHeadingCarriesAnIndexEntry(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	findings, inScope, err := runHeadingWalker(scope.GitLister(root), scope.DirReader(root))
	if err != nil {
		t.Fatalf("heading walker: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s §%s %q has %s (index anchor %q)",
			f.File, f.Number, f.Title, f.Missing, f.Anchor)
	}
	t.Logf("%d in-scope heading(s) read, %d without a resolving index row", inScope, len(findings))
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
		{"15.4.1 Message Format and Binary I/O Requirements", "1541-message-format-and-binary-io-requirements"},
		{"23.1 Why Lenny?", "231-why-lenny"},
	}
	for _, c := range cases {
		if got := headingSlug(c.heading); got != c.want {
			t.Errorf("headingSlug(%q) = %q, want %q", c.heading, got, c.want)
		}
	}
}

// The fixture tree below carries one in-scope subsection heading with a
// resolving index row, so a case varies the index or the headings alone.
const (
	headingWalkerFixtureSection = "28.9"
	headingWalkerFixtureFile    = "spec/28_communication-channels.md"
	headingWalkerFixtureSpec    = "## 28 Communication Channels\n\n" +
		"### 28.9 Worked subsection\n\nProse.\n"
	headingWalkerFixtureIndex = "# Specification\n\n" +
		"- [28. Communication Channels](28_communication-channels.md)\n" +
		"  - [28.9 Worked subsection](28_communication-channels.md#289-worked-subsection)\n"
)

// headingWalkerTree is a fixture tree the walker reads in place of the
// repository, so a case states one defect and nothing else.
type headingWalkerTree struct {
	t    *testing.T
	root string
}

// newHeadingWalkerTree writes the fixture tree with the heading and its index
// row both present.
func newHeadingWalkerTree(t *testing.T) *headingWalkerTree {
	t.Helper()
	tr := &headingWalkerTree{t: t, root: t.TempDir()}
	tr.write(headingWalkerFixtureFile, headingWalkerFixtureSpec)
	tr.write(specIndexPath, headingWalkerFixtureIndex)
	return tr
}

// write puts a file into the fixture tree.
func (tr *headingWalkerTree) write(target, body string) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// remove deletes a file from the fixture tree.
func (tr *headingWalkerTree) remove(target string) {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(target))); err != nil {
		tr.t.Fatalf("remove %s: %v", target, err)
	}
}

// run drives the walker over the fixture tree.
func (tr *headingWalkerTree) run() ([]headingWalkerFinding, int, error) {
	tr.t.Helper()
	return runHeadingWalker(scope.DirLister(tr.root), scope.DirReader(tr.root))
}

// spec: 28.1 (N8, a citation names a heading), 28.2 (citable handles)
// diagnosis: the walker's index conjunct no longer holds. Either a subsection
// the index does not list, or lists behind an anchor that resolves nowhere,
// passes, in which case a reader working from the index cannot reach the
// section and nothing reports it, or a listed subsection is reported, in which
// case the gate is red on an index that satisfies it.
func TestHeadingWalkerRequiresAResolvingIndexRow(t *testing.T) {
	t.Parallel()
	const indexWithoutTheSubsection = "# Specification\n\n" +
		"- [28. Communication Channels](28_communication-channels.md)\n"
	cases := []struct {
		name  string
		index string
		want  string
	}{
		{
			name:  "a subsection whose index row resolves passes",
			index: headingWalkerFixtureIndex,
		},
		{
			name:  "a subsection the index does not list fails",
			index: indexWithoutTheSubsection,
			want:  "no index entry",
		},
		{
			name: "a subsection whose index anchor resolves nowhere fails",
			index: indexWithoutTheSubsection +
				"  - [28.9 Worked subsection](28_communication-channels.md#289-worked-section)\n",
			want: "no index entry",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newHeadingWalkerTree(t)
			tr.write(specIndexPath, c.index)
			findings, _, err := tr.run()
			if err != nil {
				t.Fatalf("run the heading walker over the fixture tree: %v", err)
			}
			var reported []string
			for _, f := range findings {
				// The finding names the heading, which is how an author
				// reading the failure knows which index row to write.
				if f.Number != headingWalkerFixtureSection || f.Title != "Worked subsection" {
					t.Errorf("finding %q does not name §%s %q",
						f.Missing, headingWalkerFixtureSection, "Worked subsection")
				}
				reported = append(reported, f.Missing)
			}
			switch {
			case c.want == "" && len(reported) > 0:
				t.Errorf("§%s reported %v, want no finding", headingWalkerFixtureSection, reported)
			case c.want != "" && len(reported) != 1:
				t.Errorf("§%s reported %v, want exactly %q", headingWalkerFixtureSection, reported, c.want)
			case c.want != "" && reported[0] != c.want:
				t.Errorf("§%s reported %q, want %q", headingWalkerFixtureSection, reported[0], c.want)
			}
		})
	}
}

// spec: 28.1 (N8, a citation names a heading), 28.2 (citable handles)
// diagnosis: the walker reports green over a tree in which it read no heading
// at all, so a selector that stops matching, or a walk whose specification
// files move out from under it, certifies the index rather than checking it.
func TestHeadingWalkerFailsWhenItReadsNoHeading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(tr *headingWalkerTree)
	}{
		{
			name:   "a tree that carries no specification file",
			mutate: func(tr *headingWalkerTree) { tr.remove(headingWalkerFixtureFile) },
		},
		{
			name: "a specification file that carries no numbered heading",
			mutate: func(tr *headingWalkerTree) {
				tr.write(headingWalkerFixtureFile, "# Communication Channels\n\nProse.\n")
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newHeadingWalkerTree(t)
			c.mutate(tr)
			_, inScope, err := tr.run()
			if err == nil {
				t.Fatalf("the walker certified %s, reading %d in-scope heading(s)", c.name, inScope)
			}
			if !strings.Contains(err.Error(), "no in-scope heading was read") {
				t.Errorf("error %q does not state that the walk read no heading", err.Error())
			}
		})
	}
}

// spec: 28.1 (N8, a citation names a heading), 28.2 (citable handles)
// diagnosis: the walker's domain selector no longer matches the depths the
// index is written to. Either a deeper heading the index carries is skipped, in
// which case a section a reader can reach from the index goes unchecked,
// or a deeper heading the index does not carry is evaluated, in which case the
// gate demands an index the specification does not have.
func TestHeadingWalkerDomainFollowsTheDepthsTheIndexCarries(t *testing.T) {
	t.Parallel()
	const (
		baseInDomain = 2 // `## 28` and `### 28.9`, both indexed.
		deeperSpec   = headingWalkerFixtureSpec +
			"\n#### 28.9.1 Worked sub-subsection\n\nProse.\n"
		deeperRow = "    - [28.9.1 Worked sub-subsection]" +
			"(28_communication-channels.md#2891-worked-sub-subsection)\n"
		cardSpec = headingWalkerFixtureSpec +
			"\n#### 28.5.1 Gateway-to-pod\n\nProse.\n"
	)
	cases := []struct {
		name        string
		spec        string
		index       string
		wantInScope int
		wantNumber  string
		wantMissing string
	}{
		{
			name:        "a deeper heading the index carries passes",
			spec:        deeperSpec,
			index:       headingWalkerFixtureIndex + deeperRow,
			wantInScope: baseInDomain + 1,
		},
		{
			name:        "a deeper heading whose index row does not resolve fails",
			spec:        deeperSpec,
			index:       headingWalkerFixtureIndex + "    - [28.9.1 Worked sub-subsection](28_communication-channels.md#28.9.1)\n",
			wantInScope: baseInDomain + 1,
			wantNumber:  "28.9.1",
			wantMissing: "no index entry",
		},
		{
			name:        "a deeper heading the index does not carry is outside the domain",
			spec:        deeperSpec,
			index:       headingWalkerFixtureIndex,
			wantInScope: baseInDomain,
		},
		{
			name:        "a contract card heading with no index row fails",
			spec:        cardSpec,
			index:       headingWalkerFixtureIndex,
			wantInScope: baseInDomain + 1,
			wantNumber:  "28.5.1",
			wantMissing: "no index entry",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newHeadingWalkerTree(t)
			tr.write(headingWalkerFixtureFile, c.spec)
			tr.write(specIndexPath, c.index)
			findings, inScope, err := tr.run()
			if err != nil {
				t.Fatalf("run the heading walker over the fixture tree: %v", err)
			}
			if inScope != c.wantInScope {
				t.Errorf("the walk read %d in-domain heading(s), want %d", inScope, c.wantInScope)
			}
			var reported []string
			for _, f := range findings {
				reported = append(reported, fmt.Sprintf("§%s: %s", f.Number, f.Missing))
			}
			want := []string(nil)
			if c.wantMissing != "" {
				want = []string{fmt.Sprintf("§%s: %s", c.wantNumber, c.wantMissing)}
			}
			if strings.Join(reported, "; ") != strings.Join(want, "; ") {
				t.Errorf("the walk reported %v, want %v", reported, want)
			}
		})
	}
}
