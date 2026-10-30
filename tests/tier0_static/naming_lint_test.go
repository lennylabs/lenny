// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/name"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The naming lint enforces the reserved-word ban the naming law states,
// and only that ban. Two words may not stand as a bare noun phrase
// naming a conversation on this surface: the word the platform uses for
// a resource's phase transitions, and the word it uses for a command
// plane. The ban covers the space-separated spelling and the hyphenated
// compound spelling, a matcher joins two consecutive comment lines
// before it applies either spelling, and either word may stand inside a
// canonical identifier or inside a markdown anchor identifier.
//
// The lint's domain is the specification, the documentation, the
// schemas, the doc comments of every tracked Go file, and the tracked
// root-level contract documents, under the exclusion list and the
// markdown-anchor-identifier exclusion the name pass reads, so the
// lint's domain is exactly what the name pass can write. A site the lint
// reported in a file no pass may write would have no route to green but
// a suppression, which the law does not admit.
//
// Nothing here restates a banned spelling. Every tracked Go file is a
// carrier of the prohibition through its doc comments, so a specimen
// written in this source would be a site of the class the lint reports,
// and the matcher itself lives once in scripts/specshift/name. Each case
// drives the gate over a fixture tree assembled from testdata, which
// sits outside the read domain of every gate and the write domain of
// every pass, so the gate does not read its own input.
//
// spec: §28.1 N3 (reserved-word ban), §28.1 N4 (one identifier per
// channel)

// namingLintFixtures is the fixture root for the cases below.
const namingLintFixtures = "testdata/naming-lint"

// namingLintSite is one site the lint reports, named by the file, the
// line the phrase opens on, and the phrase itself, so a failure states
// the offending member rather than a count.
type namingLintSite struct {
	Path string
	Line int
	Text string
}

// String renders the site for the gate's message.
func (s namingLintSite) String() string {
	return fmt.Sprintf("%s line %d: %q stands as a bare reserved noun phrase", s.Path, s.Line, s.Text)
}

// namingLintReport is one run of the lint: the carriers it opened and
// every site it read in them, in path order and, within a file, in
// source order.
type namingLintReport struct {
	// Carriers is how many files inside the prohibition's domain the run
	// opened. A run that opened none inspected nothing.
	Carriers int
	// Sites is every reserved-phrase site the run read.
	Sites []namingLintSite
}

// namingLint is the tier-0 naming lint. Its tree dependencies are
// injected so a case drives it over a fixture tree.
type namingLint struct {
	// List enumerates the tracked tree.
	List scope.Lister
	// Read reads a repo-relative tracked path.
	Read scope.FileReader
}

// newNamingLint returns the lint over the tracked tree at root.
func newNamingLint(root string) *namingLint {
	return &namingLint{List: scope.GitLister(root), Read: scope.DirReader(root)}
}

// newNamingLintOver returns the lint with each dependency supplied, so a
// case drives it over a tree that is not a git checkout.
func newNamingLintOver(list scope.Lister, read scope.FileReader) *namingLint {
	return &namingLint{List: list, Read: read}
}

// Run reads every carrier of the prohibition and returns the sites it
// carries.
//
// The domain is the name pass's write domain intersected with the
// carrier predicate, both taken from scripts/specshift/scope rather than
// re-derived here, so the lint and the pass that removes the sites read
// one statement of the exclusion list.
//
// A run that opened no carrier returns an error rather than an empty
// report. An exclusion list that happens to cover every path, or a walk
// root pointed at an excluded subtree, otherwise reads as a completed
// inspection: the gate reports green over content it never opened, which
// is indistinguishable from a tree with no site in it.
func (g *namingLint) Run(ctx context.Context) (namingLintReport, error) {
	var rep namingLintReport
	if g.List == nil || g.Read == nil {
		return rep, fmt.Errorf("naming lint: a lister and a reader are required")
	}
	domain, err := scope.WriteDomain(ctx, g.List, scope.Name, g.Read)
	if err != nil {
		return rep, fmt.Errorf("naming lint: %w", err)
	}
	for _, target := range domain {
		if !scope.ReservedPhraseCarrier(target) {
			continue
		}
		content, err := g.Read(target)
		if err != nil {
			return rep, fmt.Errorf("naming lint: read %s: %w", target, err)
		}
		sites, err := name.Sites(target, string(content))
		if err != nil {
			return rep, fmt.Errorf("naming lint: %w", err)
		}
		rep.Carriers++
		for _, s := range sites {
			rep.Sites = append(rep.Sites, namingLintSite{Path: target, Line: s.Line, Text: s.Text})
		}
	}
	if rep.Carriers == 0 {
		return rep, fmt.Errorf("naming lint over %d writable path(s): no carrier of the prohibition was opened", len(domain))
	}
	return rep, nil
}

// TestNamingLintReportsNoBareReservedNounPhraseInTheTree is the gate.
// Every site it reports is a violation of the reserved-word ban, named
// with its file, its line, and its own words, so the report is the
// hand-correction list rather than a count.
//
// diagnosis: a file in the specification, the documentation, the
// schemas, a Go doc comment, or a tracked root-level contract document
// carries one of the two reserved words as a bare noun phrase naming a
// conversation. The route to green is the canonical identifier of the
// mechanism the sentence denotes, resolved through
// tests/registers/reserved-phrase-senses.yaml and written by the
// specshift name pass. Widening the matcher, suppressing the site, or
// registering an exception is not a route to green.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintReportsNoBareReservedNounPhraseInTheTree(t *testing.T) {
	t.Parallel()

	rep := runNamingLint(t, newNamingLint(schematest.RepoRoot(t)))
	for _, s := range rep.Sites {
		t.Errorf("%s", s)
	}
}

// TestNamingLintPassesTheNormativeStatementOfN3 holds the section that
// states the rule to the rule. §28.1 sits inside the prohibition's own
// domain, so it describes the two reserved words rather than quoting
// them, and no exclusion covers it. A lint that read a specimen out of
// the statement of the law would be red in the file that states it, with
// no correction available.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesTheNormativeStatementOfN3(t *testing.T) {
	t.Parallel()

	const target = "spec/28_communication-channels.md"
	root := schematest.RepoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, target))
	if err != nil {
		t.Fatalf("read the naming law: %v", err)
	}
	sites, err := name.Sites(target, string(content))
	if err != nil {
		t.Fatalf("read the sites of %s: %v", target, err)
	}
	for _, s := range sites {
		t.Errorf("%s", namingLintSite{Path: target, Line: s.Line, Text: s.Text})
	}
}

// TestNamingLintPassesTheAgentFacingStatementOfTheNamingLaw pins the
// other document the naming law points at. §28.1 states that the
// literal spellings are held outside the prohibition's domain, in the
// lint's matcher and in the agent-facing naming rules under .claude, so
// that document carries every spelling the ban covers, written out. It
// sits outside the carrier predicate, which admits the specification,
// the documentation, the schemas, a tracked Go file, and a tracked
// root-level markdown document, so the lint never opens it.
//
// The accept has to be the domain's doing. The case therefore drives the
// same landed body from a path inside the domain and requires the run to
// report it, so a matcher that had stopped selecting, or a walk that had
// stopped reading, cannot produce the accept above.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesTheAgentFacingStatementOfTheNamingLaw(t *testing.T) {
	t.Parallel()

	const target = ".claude/rules/channel-naming.md"
	body, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), filepath.FromSlash(target)))
	if err != nil {
		t.Fatalf("read the agent-facing naming rules: %v", err)
	}
	if scope.ReservedPhraseCarrier(target) {
		t.Fatalf("the carrier predicate admits %s, so the specimen it holds is a site the lint reports", target)
	}

	outside := newNamingLintTree(t)
	outside.clean()
	outside.file(target, string(body))
	assertNamingLintSites(t, runNamingLint(t, outside.lint()))

	inside := newNamingLintTree(t)
	inside.clean()
	inside.file("docs/reference/channel-naming.md", string(body))
	if len(runNamingLint(t, inside.lint()).Sites) == 0 {
		t.Fatalf("the same body inside the domain reported no site, so the accept above is the matcher reading nothing")
	}

	assertMatcherReadsEverySpecimen(t, string(body))
}

// reservedSpecimenHeading opens the section of the agent-facing naming
// rules that writes the banned spellings out, and reservedSpecimenAfter
// opens the section after it. The specimen table runs between the two.
const (
	reservedSpecimenHeading = "## Reserved spellings"
	reservedSpecimenAfter   = "\n## "
)

// codeSpan matches one backtick-quoted span, which is how the specimen
// table writes each spelling.
var codeSpan = regexp.MustCompile("`([^`\n]+)`")

// reservedSpecimens returns every spelling the agent-facing naming rules
// write out, read off the table rows of the section that carries them.
//
// The spellings are read from the document rather than restated here.
// Every tracked Go file is a carrier of the prohibition through its doc
// comments, so a specimen written in this source would be a site of the
// class the lint reports. Reading them also makes the document and the
// matcher one statement of the ban: a spelling added to the table that
// the matcher does not recognize fails the case below, and so does a
// matcher that stops recognizing one the table carries.
//
// A section that yields no row is an error rather than an empty answer,
// because a loop over no specimen checks the matcher against nothing.
func reservedSpecimens(body string) ([]string, error) {
	start := strings.Index(body, reservedSpecimenHeading)
	if start < 0 {
		return nil, fmt.Errorf("the naming rules carry no section %q", reservedSpecimenHeading)
	}
	section := body[start+len(reservedSpecimenHeading):]
	if end := strings.Index(section, reservedSpecimenAfter); end >= 0 {
		section = section[:end]
	}
	var specimens []string
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
			specimens = append(specimens, m[1])
		}
	}
	if len(specimens) == 0 {
		return nil, fmt.Errorf("the specimen table states no spelling")
	}
	sort.Strings(specimens)
	return specimens, nil
}

// assertMatcherReadsEverySpecimen holds the matcher to the specimen the
// naming law points at. §28.1 describes the two reserved words instead
// of writing them, and states that the literal spellings are held in the
// lint's matcher and in the agent-facing naming rules. The two are one
// statement of the ban only if the matcher reads every spelling the
// document writes out, so each specimen is driven through the matcher on
// its own, from a path inside the prohibition's domain.
//
// Driving the whole document at once does not answer this. One spelling
// the matcher had stopped reading would leave the other three reporting,
// and the run would still be non-empty.
//
// spec: §28.1 N3 (reserved-word ban)
func assertMatcherReadsEverySpecimen(t *testing.T, body string) {
	t.Helper()

	specimens, err := reservedSpecimens(body)
	if err != nil {
		t.Fatalf("read the specimens the naming rules write out: %v", err)
	}
	// The prohibition covers two reserved words in two spellings each.
	if len(specimens) != 4 {
		t.Fatalf("the specimen table states %d spelling(s) (%v), and the ban covers two words in two spellings",
			len(specimens), specimens)
	}
	const target = "docs/reference/specimen.md"
	for _, specimen := range specimens {
		sites, err := name.Sites(target, "The runtime opens the "+specimen+".\n")
		if err != nil {
			t.Errorf("read the sites of the %q specimen: %v", specimen, err)
			continue
		}
		if len(sites) != 1 {
			t.Errorf("the matcher read %d site(s) in the %q specimen, and the specimen is one site", len(sites), specimen)
			continue
		}
		if !strings.EqualFold(sites[0].Text, specimen) {
			t.Errorf("the matcher read %q where the naming rules write %q, so the two state different bans",
				sites[0].Text, specimen)
		}
	}
}

// TestNamingLintFailsABareReservedNounPhraseInSpecProse pins the case
// the ban is written for: the space-separated spelling standing as a
// bare noun phrase in specification prose. The site is named with its
// file and its line, because the report is what a hand correction works
// from.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintFailsABareReservedNounPhraseInSpecProse(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("docs/fixture-alpha.md", "spec-space-separated.md.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()), "docs/fixture-alpha.md line 3")
}

// TestNamingLintFailsTheHyphenatedCompoundSpelling pins the second
// spelling the ban covers. The worked case is a build-sequence sentence,
// because that file carries the compound spelling and no space-separated
// occurrence, so a matcher written to the space-separated spelling alone
// passes it.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintFailsTheHyphenatedCompoundSpelling(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("docs/fixture-beta.md", "spec-hyphenated.md.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()), "docs/fixture-beta.md line 3")
}

// TestNamingLintFailsAPhraseWrappedAcrossTwoCommentLines pins the
// continuation join. A phrase whose two words fall on either side of a
// comment boundary is one site, so the lint reads the joined population
// the name pass writes. A matcher applied line by line reads neither
// line as a site, and the occurrence would be invisible to the pass and
// to the lint alike. The worked case is a proto comment, which is where
// the schemas carry a wrapped occurrence.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintFailsAPhraseWrappedAcrossTwoCommentLines(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("schemas/lenny-example.proto", "schema-wrapped.proto.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()), "schemas/lenny-example.proto line 5")
}

// TestNamingLintFailsABareReservedNounPhraseInEveryDomainN3Names walks
// the domains the prohibition covers beyond the specification: a
// documentation page, a description string in a schemas document, the
// doc comment of a tracked Go file, and the two tracked root-level
// contract documents. A lint scoped to spec/ alone is green on every one
// of them.
//
// The Go carrier holds a second occurrence in a comment inside a
// function body, which the naming law does not govern and no pass may
// write, so the case pins that the lint reports the doc comment and
// stops there.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintFailsABareReservedNounPhraseInEveryDomainN3Names(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		domain  string
		target  string
		fixture string
		want    string
	}{
		{"a documentation page", "docs/reference/adapter-contract.md", "docs-page.md.txt", "docs/reference/adapter-contract.md line 3"},
		{"a schemas description string", "schemas/lenny-example.json", "schema-description.json.txt", "schemas/lenny-example.json line 8"},
		{"a Go doc comment", "pkg/adapter/handler.go", "go-doc-comment.go.txt", "pkg/adapter/handler.go line 5"},
		{"the root README", "README.md", "root-contract.md.txt", "README.md line 3"},
		{"the root testing document", "TESTING.md", "root-contract.md.txt", "TESTING.md line 3"},
	} {
		t.Run(c.domain, func(t *testing.T) {
			t.Parallel()
			tr := newNamingLintTree(t)
			tr.clean()
			tr.fixture(c.target, c.fixture)
			assertNamingLintSites(t, runNamingLint(t, tr.lint()), c.want)
		})
	}
}

// TestNamingLintPassesAReservedWordInsideACanonicalIdentifier pins the
// accept N3 states outright: either word may stand inside a canonical
// identifier. The canonical identifiers are what the pass writes over
// every site it removes, so a lint that reported one would be red on the
// text of its own remedy and the migration would have no terminal state.
//
// spec: §28.1 N3 (reserved-word ban), §28.1 N4 (one identifier per
// channel)
func TestNamingLintPassesAReservedWordInsideACanonicalIdentifier(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("spec/28_communication-channels.md", "identifiers.md.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()))
}

// TestNamingLintPassesAnUnrelatedBoundSense pins the other side of the
// matcher. The reserved words are ordinary English outside the noun
// phrase the ban names, and the tree carries several hundred bound
// senses against a banned population an order of magnitude smaller. The
// worked cases are the cloud-storage prose and the in-fence command
// arguments of the deployment-topology section, which no rewrite may
// touch: the arguments are the vendor's own flags.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesAnUnrelatedBoundSense(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("spec/17_deployment-topology.md", "bound-senses.md.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()))
}

// TestNamingLintPassesAMarkdownAnchorIdentifier pins the exclusion N3
// places outside the matcher, in both of its forms: the kramdown
// attribute that declares an anchor, and the fragment of an intra-repo
// link. An anchor identifier is an addressable link target rather than
// prose, and rewriting one breaks every inbound link, including the
// untracked links this repository cannot see.
//
// The case is required rather than incidental, because an anchor
// identifier carries the hyphenated compound spelling verbatim: a
// matcher built to the compound spelling and to no exclusion is red on a
// site the naming law exempts, with the anchor-redirect map as its only
// route out.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesAMarkdownAnchorIdentifier(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("docs/api/internal.md", "anchor-identifiers.md.txt")
	assertNamingLintSites(t, runNamingLint(t, tr.lint()))
}

// TestNamingLintPassesAFileTheExclusionListNames pins the read
// exclusion. Each of these records a finding, a plan, or a queued item
// as it was written rather than the current contract, so a reserved
// phrase in one is part of the record and no pass may rewrite it. A lint
// that read them would report a population with no route to green.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesAFileTheExclusionListNames(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"BUILD-GAPS.md",
		"TEST-GAPS.md",
		"BUILD-PLAN.md",
		"BUILD-PROGRESS.md",
		"PROPOSAL-QUEUE.md",
		"gateway-runtime-comms.md",
		"gateway-runtime-comms-remediation.md",
		"proposals/0001_example.md",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			tr := newNamingLintTree(t)
			tr.clean()
			tr.fixture(target, "spec-space-separated.md.txt")
			assertNamingLintSites(t, runNamingLint(t, tr.lint()))
		})
	}
}

// TestNamingLintPassesAFixtureUnderTestdata pins the fixture exclusion.
// A gate's own fixture exists to carry the form the gate rejects, in
// either spelling, so a lint that read one would report its own input as
// a defect and would put that file inside its read domain while every
// pass's write domain excludes it. That is the writerless site the
// shared domain exists to prevent, and its only route out would be the
// deletion of the case.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintPassesAFixtureUnderTestdata(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		target  string
		fixture string
	}{
		{"the space-separated spelling", "tests/tier0_static/testdata/naming-lint/carrier.md", "spec-space-separated.md.txt"},
		{"the hyphenated compound spelling", "docs/testdata/carrier.md", "spec-hyphenated.md.txt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := newNamingLintTree(t)
			tr.clean()
			tr.fixture(c.target, c.fixture)
			assertNamingLintSites(t, runNamingLint(t, tr.lint()))
		})
	}
}

// TestNamingLintFailsRatherThanReportingZeroSitesOnASeededTree is the
// inertness case. A matcher that compiles and selects nothing, a domain
// that opens no carrier, and a walk that reads no file all report the
// same green as a clean tree. The case seeds one known violation into a
// tree that is otherwise clean and requires the run to report it, so the
// green the gate reports over the repository is the green of a tree with
// no site in it.
//
// The run over a tree whose domain opens no carrier at all fails with an
// error rather than reporting no site, for the same reason.
//
// spec: §28.1 N3 (reserved-word ban)
func TestNamingLintFailsRatherThanReportingZeroSitesOnASeededTree(t *testing.T) {
	t.Parallel()

	tr := newNamingLintTree(t)
	tr.clean()
	tr.fixture("docs/fixture-alpha.md", "spec-space-separated.md.txt")
	rep := runNamingLint(t, tr.lint())
	if len(rep.Sites) == 0 {
		t.Fatalf("the run reported no site over a tree seeded with a known violation, so it read no site the tree carries")
	}
	if rep.Carriers == 0 {
		t.Fatalf("the run opened no carrier, so it inspected nothing")
	}

	empty := newNamingLintTree(t)
	empty.file("charts/lenny/values.yaml", "replicas: 2\n")
	if _, err := empty.lint().Run(context.Background()); err == nil {
		t.Fatalf("a run that opened no carrier of the prohibition reported no site instead of failing")
	}
}

// namingLintTree is a fixture tracked tree the lint runs over.
type namingLintTree struct {
	t    *testing.T
	root string
}

// newNamingLintTree returns an empty fixture tree.
func newNamingLintTree(t *testing.T) *namingLintTree {
	t.Helper()
	return &namingLintTree{t: t, root: t.TempDir()}
}

// clean writes a carrier that holds no site, so a tree whose only
// seeded file is excluded still opens a carrier and the run is a
// completed inspection rather than an empty one.
func (tr *namingLintTree) clean() {
	tr.t.Helper()
	tr.file("spec/01_overview.md", "## 1. Overview\n\nThe gateway mediates every session.\n")
}

// fixture writes a fixture body into the tree at a repo-relative path.
func (tr *namingLintTree) fixture(target, fixture string) {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(namingLintFixtures, fixture))
	if err != nil {
		tr.t.Fatalf("read the naming-lint fixture %s: %v", fixture, err)
	}
	tr.file(target, string(body))
}

// file writes one file into the tree.
func (tr *namingLintTree) file(target, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the fixture directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write the fixture file %s: %v", target, err)
	}
}

// lint returns the gate over the fixture tree. The tree is not a git
// checkout, so the walk is the directory lister that stands in for the
// index.
func (tr *namingLintTree) lint() *namingLint {
	tr.t.Helper()
	return newNamingLintOver(scope.DirLister(tr.root), scope.DirReader(tr.root))
}

// runNamingLint runs the gate and fails the case on a run that could not
// inspect the tree.
func runNamingLint(t *testing.T, g *namingLint) namingLintReport {
	t.Helper()
	rep, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("run the naming lint: %v", err)
	}
	return rep
}

// assertNamingLintSites fails the case unless the run reported exactly
// the expected sites, each named by its file and the line the phrase
// opens on. A site the run reported and the case did not expect is
// reported in full, so an over-wide matcher names what it read.
func assertNamingLintSites(t *testing.T, rep namingLintReport, want ...string) {
	t.Helper()
	got := make([]string, 0, len(rep.Sites))
	detail := map[string]string{}
	for _, s := range rep.Sites {
		key := fmt.Sprintf("%s line %d", s.Path, s.Line)
		got = append(got, key)
		detail[key] = s.String()
	}
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if strings.Join(got, "; ") == strings.Join(sorted, "; ") {
		return
	}
	for _, key := range got {
		t.Errorf("the run reported %s", detail[key])
	}
	t.Fatalf("the run reported %d site(s) (%s) and the case expects %d (%s)",
		len(got), strings.Join(got, "; "), len(sorted), strings.Join(sorted, "; "))
}
