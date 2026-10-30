// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The fragment-link gate asserts that every intra-repo markdown fragment
// link in spec/ and docs/ resolves to a heading slug that exists or to an
// explicit kramdown anchor attribute that exists. Its domain is a link
// whose target is a tracked .md file or the citing page itself.
//
// The gate is what holds three classes of the channel migration's
// mechanical rewrites: the markdown cross-reference redirects the anchor
// pass writes, the same-page fragments a relocation carries into another
// file and which are rewritten to their file-qualified form, and the
// pre-existing links whose target heading never existed and which are
// hand-corrected. A redirect written to a heading nothing declares, and a
// relocated fragment left in its same-page form, are both invisible to a
// pass that reports only what it rewrote.
//
// The existing scripts/check-markdown-links.sh cannot serve here: it
// resolves the file rather than the fragment, and it exits 0 when
// markdown-link-check is absent, so it is green on a tree it never read.
//
// Two forms sit outside the domain deliberately. A link written to the
// rendered documentation site, which is the .html form the docs/ pages
// use for site-internal navigation, is resolved by the site generator
// against generated output rather than against a markdown heading. An
// absolute URL addresses a document this walk does not hold. Neither is
// a fragment this gate can resolve, and reporting either would be a
// finding no correction in this tree closes.
//
// The anchor-attribute branch of the predicate is not an accommodation.
// The docs/ pages declare their anchors with the kramdown attribute in
// quantity, docs/reference/glossary.md alone carries 75 of them, and
// A page in docs/api links to an anchor its own page declares only
// through a kramdown attribute rather than a heading. A heading-only predicate would
// be red on every attribute-resolved fragment in the tree.

// fragmentLinkPage is the citing side of the domain: the gate reads the
// fragment links written in the tracked markdown of these two trees.
var fragmentLinkPageRoots = []string{"spec/", "docs/"}

// fragmentLinkExpr reads the target of an inline markdown link. The
// label is skipped and the target is read whole, up to the first
// whitespace or parenthesis, which is every intra-repo target the tree
// writes.
var fragmentLinkExpr = regexp.MustCompile(`\]\(([^()\s]*)\)`)

// fragmentAnchorExpr reads the kramdown attribute that gives a heading
// an anchor of its own, in the two positions a page writes it: on the
// heading line, and on the line below it. A page that declares one
// addresses the heading by it as well as by the slug of the heading
// text.
var fragmentAnchorExpr = regexp.MustCompile(`\{:[^}\n]*#([A-Za-z0-9._-]+)[^}\n]*\}`)

// fragmentLinkFailure is one link in the domain whose fragment resolves
// against neither a heading slug nor an anchor attribute of the page it
// addresses. It names the citing file and line, so the correction is
// made at the site rather than searched for.
type fragmentLinkFailure struct {
	// Path is the citing page, repo-relative.
	Path string
	// Line is the 1-based line the link is written on.
	Line int
	// Target is the link target as written.
	Target string
	// Page is the page the fragment is resolved against, repo-relative,
	// which is the citing page itself for the same-page form.
	Page string
	// Fragment is the fragment the link addresses, without its hash.
	Fragment string
}

// String renders one failure for a verdict.
func (f fragmentLinkFailure) String() string {
	return fmt.Sprintf("%s:%d: %q addresses %q in %s, which declares neither a heading with that slug nor that anchor attribute",
		f.Path, f.Line, f.Target, f.Fragment, f.Page)
}

// fragmentLinkReport is one run: the citing pages read, the links inside
// the domain, and the failures.
type fragmentLinkReport struct {
	// Pages is the number of citing pages the run read.
	Pages int
	// Links is the number of links inside the domain the run resolved.
	Links int
	// Failures are the links that resolved against nothing, in file and
	// line order.
	Failures []fragmentLinkFailure
}

// fragmentLinkGate resolves every intra-repo markdown fragment link of
// the citing trees against the anchors the addressed page declares.
type fragmentLinkGate struct {
	list scope.Lister
	read scope.FileReader
}

// newFragmentLinkGate returns the gate over a tracked tree. Membership
// comes from the git index, because an untracked file is a target no
// reader of the repository has.
func newFragmentLinkGate(root string) *fragmentLinkGate {
	return &fragmentLinkGate{list: scope.GitLister(root), read: scope.DirReader(root)}
}

// newFragmentLinkGateOver returns the gate over an injected tree, which
// is how the cases below run it over a fixture.
func newFragmentLinkGateOver(list scope.Lister, read scope.FileReader) *fragmentLinkGate {
	return &fragmentLinkGate{list: list, read: read}
}

// Run resolves the domain and reports every link that resolves against
// nothing.
//
// A run that inspected no link is an error rather than a clean report: a
// predicate that selects nothing, a citing tree that moved, and a walk
// that read no file all produce an empty failure list, and none of them
// certifies a link.
func (g *fragmentLinkGate) Run(ctx context.Context) (fragmentLinkReport, error) {
	var rep fragmentLinkReport
	if g.list == nil || g.read == nil {
		return rep, fmt.Errorf("run the fragment-link gate: a lister and a reader are required")
	}
	tracked, err := g.list(ctx)
	if err != nil {
		return rep, fmt.Errorf("run the fragment-link gate: %w", err)
	}
	markdown := map[string]bool{}
	for _, p := range tracked {
		if filepath.Ext(p) == ".md" {
			markdown[p] = true
		}
	}
	if len(markdown) == 0 {
		return rep, fmt.Errorf("run the fragment-link gate: the tracked tree holds no markdown document")
	}
	pages := make([]string, 0, len(markdown))
	for p := range markdown {
		if fragmentLinkCitingPage(p) {
			pages = append(pages, p)
		}
	}
	if len(pages) == 0 {
		return rep, fmt.Errorf("run the fragment-link gate: no citing page under %s", strings.Join(fragmentLinkPageRoots, " or "))
	}
	sort.Strings(pages)

	anchors := map[string]map[string]bool{}
	declared := func(target string) (map[string]bool, error) {
		if held, ok := anchors[target]; ok {
			return held, nil
		}
		content, err := g.read(target)
		if err != nil {
			return nil, fmt.Errorf("read %s to index the anchors it declares: %w", target, err)
		}
		held := fragmentLinkAnchors(string(content))
		anchors[target] = held
		return held, nil
	}

	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return rep, fmt.Errorf("run the fragment-link gate: %w", err)
		}
		content, err := g.read(page)
		if err != nil {
			return rep, fmt.Errorf("read %s to resolve its fragment links: %w", page, err)
		}
		rep.Pages++
		for _, link := range fragmentLinksIn(page, string(content), markdown) {
			rep.Links++
			held, err := declared(link.Page)
			if err != nil {
				return rep, err
			}
			if held[link.Fragment] {
				continue
			}
			rep.Failures = append(rep.Failures, link)
		}
	}
	if rep.Links == 0 {
		return rep, fmt.Errorf("run the fragment-link gate: %d citing page(s) carry no fragment link; a report of no failure over a population of nothing certifies nothing", rep.Pages)
	}
	sort.Slice(rep.Failures, func(i, j int) bool {
		if rep.Failures[i].Path != rep.Failures[j].Path {
			return rep.Failures[i].Path < rep.Failures[j].Path
		}
		return rep.Failures[i].Line < rep.Failures[j].Line
	})
	return rep, nil
}

// fragmentLinkCitingPage reports whether a tracked markdown document is
// one the gate reads links from.
func fragmentLinkCitingPage(target string) bool {
	for _, root := range fragmentLinkPageRoots {
		if strings.HasPrefix(target, root) {
			return true
		}
	}
	return false
}

// fragmentLinksIn returns the fragment links of one page that sit inside
// the domain, which is a link whose target is a tracked markdown
// document or the citing page itself.
//
// The links are read from the lines outside every fenced code block: a
// link written inside a fence is example text, and a page documenting
// how a link is written would otherwise be reported for the example it
// shows.
func fragmentLinksIn(page, content string, markdown map[string]bool) []fragmentLinkFailure {
	var out []fragmentLinkFailure
	for _, line := range citation.ProseLines(content) {
		for _, m := range fragmentLinkExpr.FindAllStringSubmatch(line.Text, -1) {
			target := m[1]
			hash := strings.Index(target, "#")
			if hash < 0 {
				continue
			}
			file, fragment := target[:hash], target[hash+1:]
			if file == "" {
				out = append(out, fragmentLinkFailure{
					Path: page, Line: line.Number, Target: target,
					Page: page, Fragment: fragment,
				})
				continue
			}
			if strings.Contains(file, ":") || strings.HasPrefix(file, "//") {
				// An absolute URL, a mail address, or any other scheme
				// addresses a document this walk does not hold.
				continue
			}
			resolved := path.Clean(path.Join(path.Dir(page), file))
			if !markdown[resolved] {
				// The .html form the docs/ pages use for site-internal
				// navigation lands here, together with a target that is
				// neither markdown nor tracked.
				continue
			}
			out = append(out, fragmentLinkFailure{
				Path: page, Line: line.Number, Target: target,
				Page: resolved, Fragment: fragment,
			})
		}
	}
	return out
}

// fragmentLinkAnchors returns every anchor one markdown document
// declares: the slug of each heading, and each explicit kramdown anchor
// attribute, in either of the two positions a page writes the attribute.
func fragmentLinkAnchors(content string) map[string]bool {
	out := map[string]bool{}
	heading := map[int]bool{}
	for _, h := range citation.AllHeadings(content) {
		heading[h.Line] = true
		out[fragmentLinkSlug(h.Title)] = true
		for _, m := range fragmentAnchorExpr.FindAllStringSubmatch(h.Title, -1) {
			out[m[1]] = true
		}
	}
	for _, line := range citation.ProseLines(content) {
		if heading[line.Number] {
			continue
		}
		trimmed := strings.TrimSpace(line.Text)
		if !strings.HasPrefix(trimmed, "{:") {
			continue
		}
		for _, m := range fragmentAnchorExpr.FindAllStringSubmatch(trimmed, -1) {
			out[m[1]] = true
		}
	}
	delete(out, "")
	return out
}

// fragmentLinkSlug returns the anchor a renderer derives from a
// heading's text: the text lowercased, with every character that is not
// a letter, a digit, a hyphen, or an underscore dropped, and every space
// written as a hyphen.
func fragmentLinkSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fragmentTree is a fixture tracked tree the gate runs over. It holds
// whichever pages a case writes.
//
// The fixtures are written here rather than under testdata/ because a
// case's whole input is a handful of markdown lines, and a fixture
// tree's own paths, spec/ and docs/, are what the citing-page predicate
// selects on.
type fragmentTree struct {
	t    *testing.T
	root string
}

// newFragmentTree returns an empty fixture tree.
func newFragmentTree(t *testing.T) *fragmentTree {
	t.Helper()
	return &fragmentTree{t: t, root: t.TempDir()}
}

// page writes one markdown document into the fixture tree.
func (tr *fragmentTree) page(target, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the fixture directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write the fixture page %s: %v", target, err)
	}
}

// run runs the gate over the fixture tree.
func (tr *fragmentTree) run() (fragmentLinkReport, error) {
	tr.t.Helper()
	return newFragmentLinkGateOver(scope.DirLister(tr.root), scope.DirReader(tr.root)).Run(context.Background())
}

// runOK runs the gate over the fixture tree and fails the case when the
// run itself errors.
func (tr *fragmentTree) runOK() fragmentLinkReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the fragment-link gate over the fixture tree: %v", err)
	}
	return rep
}

// fragmentFailureTexts renders a report's failures for an assertion
// message.
func fragmentFailureTexts(rep fragmentLinkReport) []string {
	out := make([]string, 0, len(rep.Failures))
	for _, f := range rep.Failures {
		out = append(out, f.String())
	}
	return out
}

// spec: §28.1 (N8, the citation rule: a citation names a heading, so
// every intra-repo markdown fragment link in spec/ and docs/ addresses a
// heading that exists)
func TestFragmentLinkGateCertifiesTheTree(t *testing.T) {
	t.Parallel()
	rep, err := newFragmentLinkGate(schematest.RepoRoot(t)).Run(context.Background())
	if err != nil {
		t.Fatalf("run the fragment-link gate over the tracked tree: %v", err)
	}
	if len(rep.Failures) > 0 {
		t.Errorf("%d intra-repo markdown fragment link(s) address a heading that does not exist:\n  %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "\n  "))
	}
	t.Logf("%d citing page(s) read, %d fragment link(s) resolved", rep.Pages, rep.Links)
}

// spec: §28.1 (N8, the citation rule: a file-qualified fragment link
// addressing a heading of the page it names resolves)
func TestFragmentLinkGatePassesAFileQualifiedLinkToAHeading(t *testing.T) {
	t.Parallel()
	tr := newFragmentTree(t)
	tr.page("spec/28_communication-channels.md", "# 28. Communication Channels\n\n## 28.3 The Register\n\nText.\n")
	tr.page("docs/reference/channels.md", "# Channels\n\nSee [the register](../../spec/28_communication-channels.md#283-the-register).\n")

	rep := tr.runOK()
	if len(rep.Failures) > 0 {
		t.Errorf("the gate reported %d failure(s) on a link to a heading that exists: %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "; "))
	}
	if rep.Links != 1 {
		t.Errorf("the gate resolved %d link(s), want the one the fixture writes", rep.Links)
	}
}

// spec: §28.1 (N8, the citation rule: a fragment link addressing an
// anchor a page declares with the kramdown attribute resolves, which is
// how the docs/ pages address most of their anchors)
func TestFragmentLinkGatePassesALinkResolvedByAnAnchorAttribute(t *testing.T) {
	t.Parallel()
	// The worked case is the docs/api/internal.md same-page link, whose
	// link resolves only through the standalone attribute that page
	// writes further down the page. The heading above that attribute carries a
	// different text, so a heading-only predicate is red on the link.
	tr := newFragmentTree(t)
	tr.page("docs/api/internal.md", strings.Join([]string{
		"# Internal API",
		"",
		"See [the message table](#runtimeops-messages) below.",
		"",
		"### Message Table For The Operations Conversation",
		"{: #runtimeops-messages }",
		"",
		"Text.",
		"",
	}, "\n"))

	rep := tr.runOK()
	if len(rep.Failures) > 0 {
		t.Errorf("the gate reported %d failure(s) on a link an anchor attribute resolves: %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "; "))
	}
	if rep.Links != 1 {
		t.Errorf("the gate resolved %d link(s), want the one the fixture writes", rep.Links)
	}
}

// spec: §28.1 (N8, the citation rule: a fragment link addressing a slug
// no heading and no anchor attribute declares is reported by file and
// line)
func TestFragmentLinkGateFailsOnASlugNothingDeclares(t *testing.T) {
	t.Parallel()
	tr := newFragmentTree(t)
	tr.page("spec/08_recursive-delegation.md", "# 8. Delegation\n\n### 8.5 Delegation Tools\n\nText.\n")
	tr.page("spec/09_mcp-integration.md", strings.Join([]string{
		"# 9. MCP",
		"",
		"See [the inventory](08_recursive-delegation.md#85-platform-tool-inventory).",
		"",
	}, "\n"))

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the link whose target heading does not exist: %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Path != "spec/09_mcp-integration.md" || got.Line != 3 {
		t.Errorf("the failure names %s:%d, want the citing site and the line the link is written on", got.Path, got.Line)
	}
	if got.Page != "spec/08_recursive-delegation.md" || got.Fragment != "85-platform-tool-inventory" {
		t.Errorf("the failure addresses %q in %s, want the missing slug in the page the link names", got.Fragment, got.Page)
	}
	if rendered := got.String(); !strings.Contains(rendered, "spec/09_mcp-integration.md") ||
		!strings.Contains(rendered, "3") || !strings.Contains(rendered, "85-platform-tool-inventory") {
		t.Errorf("the rendered failure %q does not name the citing file, its line, and the fragment", rendered)
	}
}

// spec: §28.1 (N8, the citation rule: a same-page fragment left behind
// by a relocation, whose heading now lives in another file, is reported)
func TestFragmentLinkGateFailsOnARelocatedSamePageFragment(t *testing.T) {
	t.Parallel()
	// This is the class the migration rests on the gate alone: a block
	// carrying a same-page link is moved into another file, the heading
	// it addressed stays behind, and neither the link nor its target was
	// edited, so no pass reports the break.
	tr := newFragmentTree(t)
	tr.page("spec/15_external-api-surface.md", "# 15. External API\n\n#### MessageEnvelope\n\nText.\n")
	tr.page("spec/28_communication-channels.md", strings.Join([]string{
		"# 28. Communication Channels",
		"",
		"The envelope is described under [MessageEnvelope](#messageenvelope).",
		"",
	}, "\n"))

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the relocated same-page fragment: %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Path != "spec/28_communication-channels.md" || got.Page != "spec/28_communication-channels.md" {
		t.Errorf("the failure reads %+v, want the same-page form resolved against the page that carries it", got)
	}
	if got.Fragment != "messageenvelope" {
		t.Errorf("the failure addresses %q, want the fragment the relocated block carried", got.Fragment)
	}
}

// spec: §28.1 (N8, the citation rule: a link the site generator resolves
// and a link into another document are outside the gate's domain)
func TestFragmentLinkGateIgnoresTheRenderedSiteFormAndAbsoluteURLs(t *testing.T) {
	t.Parallel()
	tr := newFragmentTree(t)
	tr.page("spec/28_communication-channels.md", "# 28. Communication Channels\n\n## 28.3 The Register\n\nText.\n")
	tr.page("docs/reference/channels.md", strings.Join([]string{
		"# Channels",
		"",
		"See [the register](../../spec/28_communication-channels.md#283-the-register).",
		"",
		"See [the guide](../runtime-author-guide/publishing.html#nothing-declares-this).",
		"",
		"See [the matrix](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#nothing-declares-this).",
		"",
	}, "\n"))

	rep := tr.runOK()
	if len(rep.Failures) > 0 {
		t.Errorf("the gate reported %d failure(s) on links outside its domain: %s",
			len(rep.Failures), strings.Join(fragmentFailureTexts(rep), "; "))
	}
	if rep.Links != 1 {
		t.Errorf("the gate resolved %d link(s), want only the intra-repo markdown link of the three the fixture writes", rep.Links)
	}
}

// spec: §28.1 (N8, the citation rule: a run that resolved no link
// certifies no citation, so it fails rather than reporting green)
func TestFragmentLinkGateFailsWhenItResolvesNoLink(t *testing.T) {
	t.Parallel()
	tr := newFragmentTree(t)
	tr.page("spec/28_communication-channels.md", "# 28. Communication Channels\n\n## 28.3 The Register\n\nText.\n")
	tr.page("docs/reference/channels.md", "# Channels\n\nSee [the register](../../spec/28_communication-channels.md).\n")

	rep, err := tr.run()
	if err == nil {
		t.Fatalf("the gate reported %d page(s) and %d link(s) with no error over a tree carrying no fragment link", rep.Pages, rep.Links)
	}
	if !strings.Contains(err.Error(), "no fragment link") {
		t.Errorf("the error %q does not state that the run resolved no fragment link", err)
	}
}
