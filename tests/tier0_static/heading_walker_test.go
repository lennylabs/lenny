// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The heading walker asserts that every in-scope specification heading carries
// both a `tests/spec-map.json` key, or a `tests/spec-map-exceptions.yaml` entry
// under a stated reason class, and an index entry in `spec/README.md` whose
// anchor resolves.
//
// The two conjuncts answer different questions about the same heading. The
// spec-map key names the tests, packages, and schemas that encode the section,
// so a section written with no coverage and no recorded reason for the absence
// is caught where it is written. A heading whose implementation is pending
// takes the `pending-implementation` reason class, which carries the open item
// whose closure retires the entry in `blocker` and the date the entry was
// written in `opened_at`, so the exemption is temporary by construction and the
// change that closes the blocker replaces the entry with a key.
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

const (
	specIndexPath               = "spec/README.md"
	specMapPath                 = "tests/spec-map.json"
	specMapExceptionsPath       = "tests/spec-map-exceptions.yaml"
	reasonPendingImplementation = "pending-implementation"
)

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

// headingWalkerFinding is one conjunct an in-scope heading does not satisfy.
type headingWalkerFinding struct {
	File    string
	Number  string
	Title   string
	Anchor  string
	Missing string
}

// specMapException is one `tests/spec-map-exceptions.yaml` entry, read for
// the fields the walker's second conjunct names.
type specMapException struct {
	Section  string `yaml:"section"`
	Reason   string `yaml:"reason"`
	Blocker  string `yaml:"blocker"`
	OpenedAt string `yaml:"opened_at"`
}

// headingCoverage is what the spec map and its exception register say about
// the sections they carry, keyed by section number.
type headingCoverage struct {
	keys       map[string]bool
	exceptions map[string]specMapException
}

// loadHeadingCoverage reads the spec map and its exception register. A file
// that is missing or malformed is an error rather than an empty result,
// because a walker that read neither would certify every heading as covered.
func loadHeadingCoverage(read scope.FileReader) (headingCoverage, error) {
	cov := headingCoverage{keys: map[string]bool{}, exceptions: map[string]specMapException{}}

	mapBody, err := read(specMapPath)
	if err != nil {
		return cov, fmt.Errorf("read %s: %w", specMapPath, err)
	}
	var specMap struct {
		Sections map[string]json.RawMessage `json:"sections"`
	}
	if err := json.Unmarshal(mapBody, &specMap); err != nil {
		return cov, fmt.Errorf("parse %s: %w", specMapPath, err)
	}
	for section := range specMap.Sections {
		cov.keys[section] = true
	}

	exBody, err := read(specMapExceptionsPath)
	if err != nil {
		return cov, fmt.Errorf("read %s: %w", specMapExceptionsPath, err)
	}
	var register struct {
		Exceptions []specMapException `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(exBody, &register); err != nil {
		return cov, fmt.Errorf("parse %s: %w", specMapExceptionsPath, err)
	}
	for _, entry := range register.Exceptions {
		cov.exceptions[entry.Section] = entry
	}
	return cov, nil
}

// coverageFinding states which part of the second conjunct a heading fails,
// and is empty when the heading satisfies it.
func (c headingCoverage) coverageFinding(number string) string {
	if c.keys[number] {
		return ""
	}
	entry, ok := c.exceptions[number]
	if !ok {
		return "no " + specMapPath + " key and no " + specMapExceptionsPath + " entry"
	}
	if strings.TrimSpace(entry.Reason) == "" {
		return "a " + specMapExceptionsPath + " entry under no reason class"
	}
	if entry.Reason != reasonPendingImplementation {
		return ""
	}
	switch {
	case strings.TrimSpace(entry.Blocker) == "" && strings.TrimSpace(entry.OpenedAt) == "":
		return "a " + reasonPendingImplementation + " entry with no blocker and no opened_at"
	case strings.TrimSpace(entry.Blocker) == "":
		return "a " + reasonPendingImplementation + " entry with no blocker"
	case strings.TrimSpace(entry.OpenedAt) == "":
		return "a " + reasonPendingImplementation + " entry with no opened_at"
	}
	return ""
}

// runHeadingWalker reports every in-scope heading that fails either conjunct.
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
	coverage, err := loadHeadingCoverage(read)
	if err != nil {
		return nil, 0, err
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
			if missing := coverage.coverageFinding(number); missing != "" {
				report(missing)
			}
		}
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
// list, or that neither `tests/spec-map.json` nor `tests/spec-map-exceptions.yaml`
// covers. A reader working from the index cannot reach an unlisted section, and
// an uncovered heading records neither the tests that encode it nor a reason
// for their absence. Add the index row beneath its file's top-level entry with
// an anchor that resolves, and the spec-map key, or an exceptions entry under
// the reason class that fits, which for a pending implementation is
// `pending-implementation` with its `blocker` and `opened_at`.
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
		t.Errorf("%s §%s %q has %s (index anchor %q)",
			f.File, f.Number, f.Title, f.Missing, f.Anchor)
	}
	t.Logf("%d in-scope heading(s) read, %d unsatisfied conjunct(s)", inScope, len(findings))
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
// resolving index row, so the only conjunct a case varies is the spec-map
// coverage of that heading.
const (
	headingWalkerFixtureSection = "28.9"
	headingWalkerFixtureFile    = "spec/28_communication-channels.md"
	headingWalkerFixtureSpec    = "## 28 Communication Channels\n\n" +
		"### 28.9 Worked subsection\n\nProse.\n"
	headingWalkerFixtureIndex = "# Specification\n\n" +
		"- [28. Communication Channels](28_communication-channels.md)\n" +
		"  - [28.9 Worked subsection](28_communication-channels.md#289-worked-subsection)\n"
	headingWalkerFixtureKeyed = `{"version": 1, "sections": {"28": {"title": "Communication Channels"},` +
		` "28.9": {"title": "Worked subsection"}}}`
	headingWalkerFixtureUnkeyed     = `{"version": 1, "sections": {"28": {"title": "Communication Channels"}}}`
	headingWalkerFixtureNoException = "version: 1\nexceptions: []\n"
)

// headingWalkerTree is a fixture tree the walker reads in place of the
// repository, so a case states one defect and nothing else.
type headingWalkerTree struct {
	t    *testing.T
	root string
}

// newHeadingWalkerTree writes the fixture tree with the given spec map and
// exception register, and with the heading and index row both present.
func newHeadingWalkerTree(t *testing.T, specMap, exceptions string) *headingWalkerTree {
	t.Helper()
	tr := &headingWalkerTree{t: t, root: t.TempDir()}
	tr.write(headingWalkerFixtureFile, headingWalkerFixtureSpec)
	tr.write(specIndexPath, headingWalkerFixtureIndex)
	tr.write(specMapPath, specMap)
	tr.write(specMapExceptionsPath, exceptions)
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

// pendingException renders an exception entry for the fixture heading with
// the given reason class, blocker, and opened_at, any of which may be empty.
func pendingException(reason, blocker, openedAt string) string {
	entry := "version: 1\nexceptions:\n  - section: \"" + headingWalkerFixtureSection + "\"\n"
	if reason != "" {
		entry += "    reason: " + reason + "\n"
	}
	entry += "    justification: The heading is written and its tests are not.\n"
	if blocker != "" {
		entry += "    blocker: " + blocker + "\n"
	}
	if openedAt != "" {
		entry += "    opened_at: " + openedAt + "\n"
	}
	return entry
}

// spec: 28.1 (N8, a citation names a heading), 28.2 (citable handles)
// diagnosis: the walker's spec-map conjunct no longer holds. Either a heading
// with neither a spec-map key nor a well-formed exception entry passes, in
// which case a section can be written with no recorded coverage and no
// recorded reason for its absence, or a covered heading is reported, in which
// case the gate is red on a tree that satisfies it.
func TestHeadingWalkerRequiresSpecMapCoverageOrAnException(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		specMap    string
		exceptions string
		want       string
	}{
		{
			name:       "a heading with a spec-map key passes",
			specMap:    headingWalkerFixtureKeyed,
			exceptions: headingWalkerFixtureNoException,
		},
		{
			name:       "a heading with a well-formed pending-implementation entry passes",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException(reasonPendingImplementation, "R12", "2026-08-15"),
		},
		{
			name:       "a heading under another stated reason class passes",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException("non-normative", "", ""),
		},
		{
			name:       "a heading with neither a key nor an entry fails",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: headingWalkerFixtureNoException,
			want:       "no " + specMapPath + " key and no " + specMapExceptionsPath + " entry",
		},
		{
			name:       "an entry under no reason class fails",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException("", "R12", "2026-08-15"),
			want:       "a " + specMapExceptionsPath + " entry under no reason class",
		},
		{
			name:       "a pending-implementation entry with no blocker fails",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException(reasonPendingImplementation, "", "2026-08-15"),
			want:       "a " + reasonPendingImplementation + " entry with no blocker",
		},
		{
			name:       "a pending-implementation entry with no opened_at fails",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException(reasonPendingImplementation, "R12", ""),
			want:       "a " + reasonPendingImplementation + " entry with no opened_at",
		},
		{
			name:       "a pending-implementation entry with neither field fails",
			specMap:    headingWalkerFixtureUnkeyed,
			exceptions: pendingException(reasonPendingImplementation, "", ""),
			want:       "a " + reasonPendingImplementation + " entry with no blocker and no opened_at",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newHeadingWalkerTree(t, c.specMap, c.exceptions)
			findings, inScope, err := tr.run()
			if err != nil {
				t.Fatalf("run the heading walker over the fixture tree: %v", err)
			}
			if inScope == 0 {
				t.Fatalf("the walk found no in-scope heading; a walker that read nothing certifies nothing")
			}
			var reported []string
			for _, f := range findings {
				if f.Number != headingWalkerFixtureSection {
					t.Errorf("unexpected finding on §%s: %s", f.Number, f.Missing)
					continue
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
// diagnosis: the walker certifies the tree while reading no coverage, so every
// heading passes the spec-map conjunct by default. A missing or malformed spec
// map or exception register must stop the walk instead.
func TestHeadingWalkerFailsOnAnUnreadableCoverageRegister(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(tr *headingWalkerTree)
		wantErr string
	}{
		{
			name:    "a missing spec map",
			mutate:  func(tr *headingWalkerTree) { tr.remove(specMapPath) },
			wantErr: "read " + specMapPath,
		},
		{
			name:    "a malformed spec map",
			mutate:  func(tr *headingWalkerTree) { tr.write(specMapPath, "{\"sections\": [") },
			wantErr: "parse " + specMapPath,
		},
		{
			name:    "a missing exception register",
			mutate:  func(tr *headingWalkerTree) { tr.remove(specMapExceptionsPath) },
			wantErr: "read " + specMapExceptionsPath,
		},
		{
			name: "a malformed exception register",
			mutate: func(tr *headingWalkerTree) {
				tr.write(specMapExceptionsPath, "exceptions:\n  - section: \"28.9\"\n   reason: x\n")
			},
			wantErr: "parse " + specMapExceptionsPath,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newHeadingWalkerTree(t, headingWalkerFixtureKeyed, headingWalkerFixtureNoException)
			c.mutate(tr)
			_, _, err := tr.run()
			if err == nil {
				t.Fatalf("the walker certified the tree with %s", c.name)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not name %q", err.Error(), c.wantErr)
			}
		})
	}
}
