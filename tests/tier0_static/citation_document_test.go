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

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The citation-document gate asserts that every §-number written in the
// tree names a section some document declares, and that a reader can
// tell which document from the line alone.
//
// The population it closes over was three separate defects wearing one
// shape. A number belonging to a proposal, `§3.4`, sent a reader to
// spec/03, which carries no numbered subsections at all. A number
// belonging to the testing model, `§12.9.8`, sent the same reader to
// spec/12, which does not declare it either. A number that
// over-specified, `§27.5.4`, named a real section and one component
// more. All three read as specification citations because the §-form
// says nothing about which document is meant, so a reader who followed
// one and found nothing concluded the reference was stale. None of them
// was stale.
//
// What the gate enforces is therefore narrower than "the citation is
// correct", which no gate can check: it enforces that the citation
// resolves somewhere a reader can reach. A number the specification
// declares resolves. A number written behind the name of the document
// that declares it resolves, which is why `TESTING.md §12.9.8` and
// `proposal 0013 §3.10` pass where the bare forms do not. A number
// belonging to an external standard resolves in that standard, and this
// tree neither declares nor may correct 45 CFR §164.312.
//
// The gate starts green over an empty register, which is the point. The
// three populations were closed before it landed, so its whole job is
// that they do not grow back, and an entry in its register is a claim
// that some further form is legitimate rather than a record of debt.
//
// Two exclusions are not accommodations. The ledgers, the proposals and
// the queue record what was true when they were written, so a reference
// inside one is a historical statement and rewriting it would change the
// record rather than fix a pointer. The style rules and the review
// tooling quote the bare form in order to describe it, and a gate that
// read them would report the description as the defect.

// citationDocumentRegisterPath records the occurrences a reader has
// judged legitimate that the resolution rules do not reach.
const citationDocumentRegisterPath = "tests/registers/citation-document-senses.yaml"

// citationRef matches a §-number. The negative lookbehind keeps it off a
// word character so a token such as `x§1` is not read as a citation, and
// the number is taken greedily so `§27.5.4` is one reference rather than
// `§27.5` followed by a stray component.
var citationRef = regexp.MustCompile(`(?:^|[^\w])§(\d+(?:\.\d+)*)`)

// specHeading matches a numbered heading, which is how a document
// declares a section.
var specHeading = regexp.MustCompile(`^#{1,6}\s+(\d+(?:\.\d+)*)[.\s]`)

// externalStandard names a document outside this repository. Its
// sections are not ours to declare and not ours to correct, so a line
// citing one is resolved by the standard rather than by the tree.
var externalStandard = regexp.MustCompile(
	`(?i)\b(RFC\s*\d+|CFR|ISO\s*\d+|NIST|SP\s*800|OAuth|JSON[- ]RPC|SemVer|OCSF` +
		`|OpenSLO|CloudEvents|W3C|IETF|HIPAA|SOC\s*2|PCI[- ]DSS)\b`,
)

// qualifyingDocument names a document this repository does hold, written
// ahead of the number it declares. Only text before the number counts: a
// document named after it is a different reference on the same line.
//
// Any markdown document named ahead of the number qualifies it, which is
// the general form of what `TESTING.md §12.9.8` does: the reader is told
// where to look before being given the number. A proposal is named either
// by the word or by its four-digit identifier, since `addedBy: "0013
// §3.10"` says as much as "proposal 0013 §3.10" and spelling the word out
// in a struct literal would add nothing.
var qualifyingDocument = regexp.MustCompile(
	`(?i)(\b[\w.-]+\.md\b|\bproposals?\b|\b0\d{3}(_[a-z][\w-]*)?\b)`,
)

// linkedToDocument matches the tail of `[§13.0](../TESTING.md#130-...)`
// as seen from just after the number. The link target names the document,
// so a reader following it arrives where the section is declared.
var linkedToDocument = regexp.MustCompile(`^[^\]]*\]\([^)]*\.md`)

// linkPrecedes matches a markdown link closing just before the number, as
// in `[Security Principles](security-principles) §1.7`. The section
// belongs to the page just linked, where it does resolve, and rewriting
// it would repoint a reader inside a document that declares it.
var linkPrecedes = regexp.MustCompile(`\]\([^)]*\)[^§]{0,40}§?$`)

// citationDocumentExcluded are the paths whose §-numbers describe the
// problem rather than commit it, or state what was true when written.
var citationDocumentExcluded = []string{
	"TESTING.md",
	"BUILD-GAPS.md",
	"BUILD-PROGRESS.md",
	"TEST-GAPS.md",
	"PROPOSAL-QUEUE.md",
	"proposals/",
	".claude/",
	"dist/",
	"tests/registers/",
	"scripts/review-migration-diff/",
	"tests/tier0_static/citation_document_test.go",
	// A changelog entry states what a release contained. Its references
	// age with the release rather than with the tree.
	"CHANGELOG.md",
}

// citationDocumentSense is one occurrence a reader has excused, keyed by
// file and by the 1-based position of the occurrence among the
// unresolved references that file carries, in source order.
type citationDocumentSense struct {
	File       string `yaml:"file"`
	Occurrence int    `yaml:"occurrence"`
	Reason     string `yaml:"reason"`
}

// citationDocumentRegister is the register as written. Entries is a
// pointer so a document declaring no entries block is distinguishable
// from one declaring an empty list. A register the gate could not parse
// would excuse nothing and report every occurrence, which reads as a
// tree-wide regression rather than as the broken register it is, so both
// forms are refused explicitly.
type citationDocumentRegister struct {
	Kind    string                   `yaml:"kind"`
	Version int                      `yaml:"version"`
	Entries *[]citationDocumentSense `yaml:"entries"`
}

// citationDocumentFinding is one §-number that names no document a
// reader can reach.
type citationDocumentFinding struct {
	File       string
	Line       int
	Occurrence int
	Section    string
	Text       string
}

// citationDocumentReport is what one run of the gate saw.
type citationDocumentReport struct {
	FilesRead   int
	Refs        int
	Excused     int
	Findings    []citationDocumentFinding
	StaleExcuse []citationDocumentSense
}

// citationDocumentGate resolves every citation in the tracked tree.
type citationDocumentGate struct {
	list scope.Lister
	read scope.FileReader
}

func newCitationDocumentGate(root string) *citationDocumentGate {
	return &citationDocumentGate{list: scope.GitLister(root), read: scope.DirReader(root)}
}

// inDomain reports whether the path's citations are the gate's to judge.
func citationDocumentInDomain(p string) bool {
	// A testdata tree is fixture material rather than source. A citation
	// there is input to some other gate, often deliberately unresolvable so
	// that gate can be seen to report it, and its route out is the deletion
	// of the case rather than a correction. The go tool ignores testdata by
	// the same convention.
	if p == "testdata" || strings.HasPrefix(p, "testdata/") || strings.Contains(p, "/testdata/") {
		return false
	}
	for _, ex := range citationDocumentExcluded {
		if p == ex || strings.HasPrefix(p, ex) {
			return false
		}
	}
	return true
}

// declaredSections reads every section number the specification declares.
func (g *citationDocumentGate) declaredSections(ctx context.Context, files []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, f := range files {
		if !strings.HasPrefix(f, "spec/") || !strings.HasSuffix(f, ".md") {
			continue
		}
		body, err := g.read(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if m := specHeading.FindStringSubmatch(line); m != nil {
				n := strings.TrimSuffix(m[1], ".")
				out[n] = true
				// A top-level number is declared by every section
				// beneath it, so §15 resolves wherever §15.4 does.
				if i := strings.Index(n, "."); i > 0 {
					out[n[:i]] = true
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no specification section was read; the walk selected no spec/ file")
	}
	return out, nil
}

// resolves reports whether the occurrence at m names a document a reader
// can reach from the line.
func resolvesToADocument(line string, start, end int, section string, declared, selfDeclared map[string]bool, externalFile bool) bool {
	if declared[section] {
		return true
	}
	if externalStandard.MatchString(line) {
		return true
	}
	// A file citing an external standard carries that standard's numbering
	// throughout: a HIPAA table names 45 CFR in its heading and then writes
	// §164.312 down a column, and a token-exchange test names RFC 8693 once
	// and then writes §2.2.1 in its own prose. The file-wide reading applies
	// only where the number's top-level component names no specification
	// section, so it cannot excuse a stale §6.5 in a file that happens to
	// mention OAuth.
	if externalFile && !declared[topLevelOf(section)] {
		return true
	}
	if qualifyingDocument.MatchString(line[:start]) {
		return true
	}
	// A page citing a section of its own numbering resolves for the reader
	// who is already on it: `All of §3.1–§3.3 above` in the security
	// principles page points up the same page.
	if selfDeclared[section] {
		return true
	}
	if linkPrecedes.MatchString(line[:start]) {
		return true
	}
	return linkedToDocument.MatchString(line[end:])
}

// isCommentLine reports whether the line opens with a comment marker in
// any of the languages this tree is written in.
func isCommentLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "--")
}

// topLevelOf returns a section number's first component.
func topLevelOf(section string) string {
	if i := strings.Index(section, "."); i > 0 {
		return section[:i]
	}
	return section
}

// Run walks the tree and reports every unresolved citation.
func (g *citationDocumentGate) Run(ctx context.Context) (*citationDocumentReport, error) {
	files, err := g.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the tracked tree: %w", err)
	}
	declared, err := g.declaredSections(ctx, files)
	if err != nil {
		return nil, err
	}
	excused, err := g.loadRegister()
	if err != nil {
		return nil, err
	}
	seen := map[string]map[int]bool{}

	rep := &citationDocumentReport{}
	for _, f := range files {
		if !citationDocumentInDomain(f) {
			continue
		}
		body, err := g.read(f)
		if err != nil {
			// A listed path the reader cannot open is not a citation
			// finding; a binary or removed file is skipped rather than
			// reported as a defect it cannot be.
			continue
		}
		if !strings.Contains(string(body), "§") {
			continue
		}
		externalFile := externalStandard.Match(body)
		// The sections the citing file declares itself, so a page pointing
		// within its own numbering is read as the self-reference it is.
		selfDeclared := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			if m := specHeading.FindStringSubmatch(line); m != nil {
				selfDeclared[strings.TrimSuffix(m[1], ".")] = true
			}
		}
		rep.FilesRead++
		occurrence := 0
		// A document named once at the head of a comment block qualifies
		// the numbers written further down it. `// Proposal 0013 §3.3,
		// §3.5,` continuing onto `// §3.6, and §3.10 each add one` is one
		// sentence, and a reader has been told where to look. The carry
		// ends at a blank line or at anything that is not a comment, so it
		// cannot reach past the block that established it.
		blockQualified := false
		for n, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || !isCommentLine(trimmed) {
				blockQualified = false
			}
			for _, m := range citationRef.FindAllStringSubmatchIndex(line, -1) {
				section := line[m[2]:m[3]]
				rep.Refs++
				if blockQualified || resolvesToADocument(line, m[2], m[3], section, declared, selfDeclared, externalFile) {
					continue
				}
				occurrence++
				if excused[f][occurrence] {
					rep.Excused++
					if seen[f] == nil {
						seen[f] = map[int]bool{}
					}
					seen[f][occurrence] = true
					continue
				}
				rep.Findings = append(rep.Findings, citationDocumentFinding{
					File: f, Line: n + 1, Occurrence: occurrence,
					Section: section, Text: strings.TrimSpace(line),
				})
			}
			if isCommentLine(trimmed) && qualifyingDocument.MatchString(line) {
				blockQualified = true
			}
		}
	}
	// An excuse whose occurrence no longer exists has outlived the site
	// it explained. Reporting it keeps the register from accumulating
	// entries that excuse nothing, which is how a register stops
	// describing the tree.
	for f, occs := range excused {
		for occ := range occs {
			if !seen[f][occ] {
				rep.StaleExcuse = append(rep.StaleExcuse, citationDocumentSense{File: f, Occurrence: occ})
			}
		}
	}
	sort.Slice(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].File != rep.Findings[j].File {
			return rep.Findings[i].File < rep.Findings[j].File
		}
		return rep.Findings[i].Line < rep.Findings[j].Line
	})
	sort.Slice(rep.StaleExcuse, func(i, j int) bool {
		if rep.StaleExcuse[i].File != rep.StaleExcuse[j].File {
			return rep.StaleExcuse[i].File < rep.StaleExcuse[j].File
		}
		return rep.StaleExcuse[i].Occurrence < rep.StaleExcuse[j].Occurrence
	})
	return rep, nil
}

// loadRegister reads the sense register into file → occurrence.
func (g *citationDocumentGate) loadRegister() (map[string]map[int]bool, error) {
	body, err := g.read(citationDocumentRegisterPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", citationDocumentRegisterPath, err)
	}
	var doc citationDocumentRegister
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", citationDocumentRegisterPath, err)
	}
	if doc.Kind != "citation-document-senses" || doc.Version != 1 {
		return nil, fmt.Errorf("%s declares kind %q version %d, want citation-document-senses version 1",
			citationDocumentRegisterPath, doc.Kind, doc.Version)
	}
	if doc.Entries == nil {
		return nil, fmt.Errorf("%s declares no entries block; an absent block and an empty one "+
			"are not the same claim", citationDocumentRegisterPath)
	}
	out := map[string]map[int]bool{}
	for i, e := range *doc.Entries {
		if e.File == "" || e.Occurrence < 1 || strings.TrimSpace(e.Reason) == "" {
			return nil, fmt.Errorf("%s entry %d needs a file, a 1-based occurrence and a reason",
				citationDocumentRegisterPath, i)
		}
		if out[e.File] == nil {
			out[e.File] = map[int]bool{}
		}
		out[e.File][e.Occurrence] = true
	}
	return out, nil
}

// spec: 28.4 (citation resolution), 29.2 (reference resolution)
// diagnosis: a §-number was written that names no section any document
// this repository holds declares, so a reader following it lands nowhere.
// Either the number is wrong, or the document that declares it has to be
// named on the line: `TESTING.md §12.9.8` and `proposal 0013 §3.10`
// resolve where the bare forms do not.
func TestEveryCitationNamesADocumentAReaderCanReach(t *testing.T) {
	t.Parallel()
	rep, err := newCitationDocumentGate(schematest.RepoRoot(t)).Run(context.Background())
	if err != nil {
		t.Fatalf("citation-document gate: %v", err)
	}
	if rep.FilesRead == 0 || rep.Refs == 0 {
		t.Fatalf("the walk read %d file(s) and %d citation(s); a gate that read nothing "+
			"certifies nothing", rep.FilesRead, rep.Refs)
	}
	for _, f := range rep.Findings {
		t.Errorf("%s:%d cites §%s, which no document this repository holds declares "+
			"(occurrence %d in the file)\n    %s",
			f.File, f.Line, f.Section, f.Occurrence, f.Text)
	}
	for _, s := range rep.StaleExcuse {
		t.Errorf("%s excuses %s occurrence %d, which the walk no longer finds; remove the entry",
			citationDocumentRegisterPath, s.File, s.Occurrence)
	}
	t.Logf("%d file(s) carrying a citation, %d citation(s) read, %d excused by %s",
		rep.FilesRead, rep.Refs, rep.Excused, citationDocumentRegisterPath)
}

// spec: 28.4 (citation resolution)
// diagnosis: the gate stopped reporting a citation that names no
// document, so the populations it was built to hold could grow back
// unseen.
func TestCitationDocumentGateReportsAnUnreachableCitation(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"4.6.1": true, "15": true}
	cases := []struct {
		line    string
		resolve bool
		why     string
	}{
		{"// spec: §4.6.1 (a declared section)", true, "the specification declares it"},
		{"// the §3.4 recycle disposition", false, "a proposal's number, written bare"},
		{"// per proposal 0013 §3.10 the anchor", true, "the proposal is named ahead of it"},
		{"// TESTING.md §12.9.8 credential battery", true, "the testing model is named ahead of it"},
		{"// 45 CFR §164.312 technical safeguards", true, "an external standard's section"},
		{"// see [§13.0](../TESTING.md#130-tier-0)", true, "the link target names the document"},
		{"// a newly invented §99.1 pointer", false, "nothing declares it"},
		{"// §12.9.8 battery, TESTING.md below", false, "the document is named after the number"},
	}
	for _, c := range cases {
		m := citationRef.FindStringSubmatchIndex(c.line)
		if m == nil {
			t.Fatalf("no citation found in %q", c.line)
		}
		got := resolvesToADocument(c.line, m[2], m[3], c.line[m[2]:m[3]], declared, nil, false)
		if got != c.resolve {
			t.Errorf("resolves(%q) = %v, want %v (%s)", c.line, got, c.resolve, c.why)
		}
	}
}

// spec: 28.4 (citation resolution)
// diagnosis: the gate accepted a register it could not act on, so it
// would run over a tree excusing nothing while reporting success.
func TestCitationDocumentGateRefusesAnUnusableRegister(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"no entries block", "kind: citation-document-senses\nversion: 1\n"},
		{"wrong kind", "kind: something-else\nversion: 1\nentries: []\n"},
		{"wrong version", "kind: citation-document-senses\nversion: 2\nentries: []\n"},
		{"entry with no reason", "kind: citation-document-senses\nversion: 1\nentries:\n  - file: a.go\n    occurrence: 1\n"},
		{"entry with no occurrence", "kind: citation-document-senses\nversion: 1\nentries:\n  - file: a.go\n    reason: r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(citationDocumentRegisterPath)), 0o755); err != nil {
				t.Fatalf("seed the register directory: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, citationDocumentRegisterPath), []byte(c.body), 0o644); err != nil {
				t.Fatalf("seed the register: %v", err)
			}
			g := &citationDocumentGate{list: scope.DirLister(root), read: scope.DirReader(root)}
			if _, err := g.loadRegister(); err == nil {
				t.Fatalf("the gate accepted a register it cannot act on")
			}
		})
	}
}
