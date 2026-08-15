// SPDX-License-Identifier: MIT

// Package scope holds the one implementation of the specshift file
// domain. It carries the tracked-tree walk, the read exclusion list the
// gates and the passes share, the per-class register exclusion, the
// per-pass write exclusion list, and the per-file generated-artifact
// rule.
//
// Every pass and every gate reads the domain from here. A gate that
// re-derived the list would drift from the pass that writes the same
// files, and a file inside one domain but outside the other is exactly
// the hole the migration's completeness proof has to close.
//
// The population is the tracked tree, walked with an explicit exclusion
// list rather than an inclusion list of directories. An inclusion list
// is what leaves a carrier ungated, and an ungated carrier means the
// prohibition the citation gates end in is not flat. This is migration
// tooling rather than a platform behavior, so it carries no spec
// citation of its own.
package scope

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Pass names one specshift pass. The write domain differs between the
// name and identifier passes and the anchor and line passes, because a
// reserved phrase in a build or queue record is part of what was written
// at the time while a line citation in the same record is a pointer that
// has to keep resolving.
type Pass string

// The passes specshift carries.
const (
	// Name removes reserved bare noun phrases from prose.
	Name Pass = "name"
	// Identifier rewrites a channel identifier across code, schemas,
	// SDKs, charts, and documentation.
	Identifier Pass = "identifier"
	// Anchor rewrites a retired section anchor to its successor.
	Anchor Pass = "anchor"
	// Line rewrites or retires a line citation.
	Line Pass = "line"
)

// Passes returns every pass name in a stable order.
func Passes() []Pass { return []Pass{Name, Identifier, Anchor, Line} }

// Valid reports whether the name is one specshift carries.
func (p Pass) Valid() bool {
	for _, known := range Passes() {
		if p == known {
			return true
		}
	}
	return false
}

// readExcludedFiles are the tracked files outside the read domain of
// every pass and every gate.
//
// The audit records and the planning documents record findings as they
// were written rather than the current contract. The two citation
// registers are the baselines the citation gates consume, and a gate
// cannot read its own baseline as tree content: the baseline holds a
// copy of the text of every citation it carries, so reading it would
// report each copy under the register's own path and seeding an entry
// for that copy would not converge.
//
// A residual register is absent from this list. Every residual register
// is outside the residual scan's per-class domain, which ReadableForClass
// answers, and it is an ordinary member of the shared read domain: the
// naming lint, the identifier-resolution gate, the citation resolver, and
// the ratchet all read the member and reason text a register carries, and
// every pass may write it. A register listed here would be read by no
// gate and written by no pass.
var readExcludedFiles = []string{
	"BUILD-GAPS.md",
	"TEST-GAPS.md",
	"gateway-runtime-comms.md",
	"gateway-runtime-comms-remediation.md",
	"tests/registers/line-citations.yaml",
	"tests/registers/line-citation-resolution.yaml",
}

// readExcludedPrefix is the staged-proposal tree, excluded for the same
// reason the audit records are.
const readExcludedPrefix = "proposals/"

// testdataSegment names the fixture directories excluded from every read
// and write domain. A gate's own fixture has to present the retired form
// verbatim, its route out of the population is the deletion of the case
// rather than a retirement, and a per-file count seeded for it would
// never fall.
const testdataSegment = "testdata"

// planningRecords are the tracked root-level records the reserved-phrase
// and identifier classes exclude. A reserved phrase in a build or queue
// record is part of what was written at the time and rewriting it would
// edit the record, while a line citation in the same file is a pointer
// that has to keep resolving.
var planningRecords = []string{
	"BUILD-PLAN.md",
	"BUILD-PROGRESS.md",
	"PROPOSAL-QUEUE.md",
}

// PlanningRecords returns those records, so a residual gate for the
// reserved-phrase or identifier class reads the same list the name and
// identifier passes write against instead of restating it.
func PlanningRecords() []string { return append([]string(nil), planningRecords...) }

// Class names one class of the migration's residual scan. A class is a
// broad predicate over the tracked tree together with the register that
// records the members the enumeration does not carry.
type Class string

// The classes the residual scan ranges over.
const (
	// ClassReservedPhrase is the bare reserved noun phrase in prose.
	ClassReservedPhrase Class = "reserved-phrases"
	// ClassIdentifier is the retired channel identifier spelling.
	ClassIdentifier Class = "identifiers"
	// ClassAnchor is the reference into a retired section anchor.
	ClassAnchor Class = "anchors"
	// ClassLineCitations is the line citation into a spec file.
	ClassLineCitations Class = "line-citations"
	// ClassLineCitationResolution is the line citation whose target no
	// longer resolves.
	ClassLineCitationResolution Class = "line-citation-resolution"
	// ClassGeneratedArtifact is the file the generated-artifact rule
	// selects.
	ClassGeneratedArtifact Class = "generated-artifacts"
	// ClassChangeGraphCoverage is the tracked path with no glob key in
	// the change graph.
	ClassChangeGraphCoverage Class = "change-graph-coverage"
	// ClassSkipReason is the skip reason that opens with no category.
	ClassSkipReason Class = "skip-reasons"
)

// classRegisters holds, per class, the registers the class's scan cannot
// read as tree content: its residual register, plus the pass or baseline
// register the class's own gate already excludes.
//
// A residual entry holds a copy of its member's text, so a scan that
// read a residual register would report that copy under the register's
// own path as a further residual, and the entry seeded for that copy
// would add another copy. The seeding does not converge. The same
// argument covers the pass or baseline register the gate consumes.
//
// Every residual register is outside every class's scan, not that class's
// own alone, which residualRegisterOfAnyClass answers. Two classes whose
// predicates overlap, as the two line-citation classes do, otherwise pin
// each other: each register holds a copy of the other's member text, so
// each class reports the copy as a residual of its own, every member is
// triaged twice, and an in-class entry keeps matching after the pass has
// rewritten every carrier site, so no class reaches the empty state its
// rewrite drives toward. The pass or baseline register listed alongside
// stays per class, because it is consumed by one gate.
//
// A path here need not exist yet. Later sub-steps seed the registers of
// the classes whose passes they build, and an exclusion for a register
// that has not landed selects nothing.
var classRegisters = map[Class][]string{
	ClassReservedPhrase: {
		ClassReservedPhrase.ResidualRegister(),
		"tests/registers/reserved-phrase-senses.yaml",
	},
	ClassIdentifier: {
		ClassIdentifier.ResidualRegister(),
		"tests/registers/identifier-senses.yaml",
	},
	// The anchor class excludes the anchor-move map alongside its sense
	// register. The map is keyed by the retired anchor, so it holds a
	// verbatim copy of the text the class's predicate reads, and a scan
	// that opened it would report every retirement the map records as a
	// reference into a retired anchor under the map's own path.
	ClassAnchor: {
		ClassAnchor.ResidualRegister(),
		"tests/registers/anchor-senses.yaml",
		"tests/spec-anchor-moves.json",
	},
	ClassLineCitations: {
		ClassLineCitations.ResidualRegister(),
		"tests/registers/line-citations.yaml",
	},
	ClassLineCitationResolution: {
		ClassLineCitationResolution.ResidualRegister(),
		"tests/registers/line-citation-resolution.yaml",
	},
	// The generated-artifact class is driven by the per-file rule in
	// this package rather than by a register, so it excludes its
	// residual register alone.
	ClassGeneratedArtifact: {
		ClassGeneratedArtifact.ResidualRegister(),
	},
	ClassChangeGraphCoverage: {
		ClassChangeGraphCoverage.ResidualRegister(),
		"tests/registers/change-graph-coverage.yaml",
	},
	ClassSkipReason: {
		ClassSkipReason.ResidualRegister(),
		"tests/registers/skip-reasons.yaml",
	},
}

// residualRegisterDir and residualRegisterPrefix compose the path of a
// class's residual register. The convention is stated once here, so the
// gate that reads a register and the exclusion that keeps the class from
// reading it as tree content name the same file.
const (
	residualRegisterDir    = "tests/registers/"
	residualRegisterPrefix = "residual-"
)

// ResidualRegister returns the repo-relative path of the class's
// residual register, which records the members of the class the
// enumeration does not carry.
//
// The register need not exist yet. Each one is seeded by the sub-step
// that builds the class's residual check, and an exclusion for a
// register that has not landed selects nothing.
func (c Class) ResidualRegister() string {
	return residualRegisterDir + residualRegisterPrefix + string(c) + ".yaml"
}

// residualRegisterOfAnyClass reports whether the tracked path is the
// residual register of some class. Every one of them is outside the
// residual scan domain of every class, because a register holds a
// verbatim copy of each member's text and a class whose predicate
// overlaps another's would read those copies as its own members.
func residualRegisterOfAnyClass(target string) bool {
	for c := range classRegisters {
		if target == c.ResidualRegister() {
			return true
		}
	}
	return false
}

// Classes returns every class name in a stable order.
func Classes() []Class {
	names := make([]Class, 0, len(classRegisters))
	for c := range classRegisters {
		names = append(names, c)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Valid reports whether the name is one of the classes.
func (c Class) Valid() bool {
	_, ok := classRegisters[c]
	return ok
}

// Registers returns the registers the class's own scan excludes.
func (c Class) Registers() []string {
	return append([]string(nil), classRegisters[c]...)
}

// passClasses maps each pass to the class its sites belong to, so the
// pass and the residual scan that follows it read one domain.
var passClasses = map[Pass]Class{
	Name:       ClassReservedPhrase,
	Identifier: ClassIdentifier,
	Anchor:     ClassAnchor,
	Line:       ClassLineCitations,
}

// PassClass returns the class of the pass.
func PassClass(p Pass) (Class, error) {
	c, ok := passClasses[p]
	if !ok {
		return "", fmt.Errorf("class of pass %q: unknown pass", p)
	}
	return c, nil
}

// planningExcludedClasses are the classes whose scan additionally
// excludes the root-level planning records, matching the write domain of
// the name and identifier passes.
var planningExcludedClasses = map[Class]bool{
	ClassReservedPhrase: true,
	ClassIdentifier:     true,
}

// registerDir holds the test-infrastructure registers, and
// registerFileExt is the extension each of them carries. Every register
// under it is keyed by a tracked path or carries one inside a member,
// and a run that renames a file invalidates those keys.
const (
	registerDir     = "tests/registers/"
	registerFileExt = ".yaml"
)

// mapRegisters are the path-keyed test-infrastructure registers that sit
// outside the register directory: the tier selection map and the
// specification map, both keyed by tracked path.
var mapRegisters = []string{
	"tests/change-graph.json",
	"tests/spec-map.json",
}

// pathKeyedRegisterRule states the membership rule in the form an error
// message names it, so a message reports the rule rather than a snapshot
// of the tree.
var pathKeyedRegisterRule = append([]string{registerDir + "*" + registerFileExt}, mapRegisters...)

// PathKeyedRegisterRule returns that statement of the rule.
func PathKeyedRegisterRule() []string { return append([]string(nil), pathKeyedRegisterRule...) }

// KeyWritable reports whether a tracked path is a path-keyed
// test-infrastructure register, which the run that renames a file rekeys.
//
// Membership is derived from where a register sits rather than from an
// enumeration of registers, so a register added later joins the rekey
// domain by construction. A hand-written enumeration leaves a register
// added after it was written unrekeyed: the run renames a file the
// register is keyed by, the old key silently survives, and the gate that
// owns the register fails on the new path with no permitted route back
// to green, because every register is rewritten downward and never
// widened.
//
// Membership is also independent of the site-rewrite domain. The two
// citation baselines are outside that domain, because a citation gate
// cannot read its own baseline as tree content, and they still take the
// key rewrite: without it the ratchet fires on a rename that changed no
// citation and every baselined non-resolving citation under the old path
// reappears as a resolver failure. Every other register is an ordinary
// domain member and takes the key rewrite alongside its site rewrite.
func KeyWritable(target string) bool {
	if strings.HasPrefix(target, registerDir) && strings.HasSuffix(target, registerFileExt) {
		return true
	}
	for _, r := range mapRegisters {
		if target == r {
			return true
		}
	}
	return false
}

// Readable reports whether a tracked path is inside the read domain the
// passes and the gates share.
func Readable(p string) bool {
	if strings.HasPrefix(p, readExcludedPrefix) {
		return false
	}
	if hasSegment(p, testdataSegment) {
		return false
	}
	for _, f := range readExcludedFiles {
		if p == f {
			return false
		}
	}
	return true
}

// ReadableForClass reports whether a tracked path is inside the read
// domain of one class. That domain is the shared read domain less three
// further exclusions: the root-level planning records, for the
// reserved-phrase and identifier classes, every class's residual
// register, and the pass or baseline register the class's own gate
// consumes. The last two are the registers a class's predicate would
// otherwise match as tree content. The generated-artifact
// rule is not applied here: a generated artifact is inside a gate's read
// domain and carries a per-file count, and its route to zero is the
// regeneration of its source.
//
// The residual scan for a class ranges over this domain, so the scan,
// the exclusion, and the pass's own denylist cannot drift apart.
func ReadableForClass(c Class, target string) (bool, error) {
	if !c.Valid() {
		return false, fmt.Errorf("class read domain: unknown class %q", c)
	}
	if !Readable(target) {
		return false, nil
	}
	if excludesPlanningRecord(c, target) {
		return false, nil
	}
	if residualRegisterOfAnyClass(target) {
		return false, nil
	}
	for _, reg := range classRegisters[c] {
		if target == reg {
			return false, nil
		}
	}
	return true, nil
}

// excludesPlanningRecord reports whether the class excludes the named
// root-level planning record. The reserved-phrase and identifier classes
// do, and their passes take the same exclusion, so the pass and the
// residual scan that follows it agree on the record.
func excludesPlanningRecord(c Class, target string) bool {
	if !planningExcludedClasses[c] {
		return false
	}
	for _, rec := range planningRecords {
		if target == rec {
			return true
		}
	}
	return false
}

// Writable reports whether the pass may write the tracked path. The
// site-rewrite domain is the shared read domain, less the root-level
// planning records for the name and identifier passes, less every file
// the per-file generated-artifact rule selects. It fails rather than
// answering when the generated-artifact rule cannot read the file,
// because a pass that wrote a file it could not classify would write a
// derived artifact.
//
// The class register exclusion is deliberately absent here. That
// exclusion belongs to the residual scan, which reads no residual
// register and no register its own gate consumes as tree content. It is
// not a write exclusion: a
// class's sense register and residual register are ordinary members of
// the shared read domain, so the naming lint, the identifier-resolution
// gate, and the citation gates all read them and report the sites they
// carry. Folding the residual scan's exclusion into the write domain
// would leave those sites with no pass able to rewrite them, which is a
// readable file with no route to a zero count.
//
// This answer governs site rewriting alone. The key rewrite over the
// path-keyed registers runs through KeyWritable, so a register excluded
// from reading is still rekeyed by the run that renames a file.
func Writable(p Pass, target string, read FileReader) (bool, error) {
	c, err := PassClass(p)
	if err != nil {
		return false, err
	}
	if !Readable(target) {
		return false, nil
	}
	if excludesPlanningRecord(c, target) {
		return false, nil
	}
	disjunct, err := Generated(target, read)
	if err != nil {
		return false, fmt.Errorf("write domain for %s: %w", target, err)
	}
	return disjunct == NotGenerated, nil
}

// reservedPhrasePrefixes are the directories the naming law's
// reserved-phrase prohibition covers whole.
var reservedPhrasePrefixes = []string{"spec/", "docs/", "schemas/"}

// ReservedPhraseCarrier reports whether a tracked path is a carrier of
// the reserved-phrase prohibition, which is the specification, the
// documentation, the schemas, a tracked Go file, and a tracked
// root-level markdown document.
//
// The domain is narrower than the write domain, which is the whole
// tracked tree less the read, planning, and generated exclusions. A
// carrier outside this list holds text the prohibition does not govern,
// among them chart values, chart templates, the runtime scaffold
// templates, and the runtime SDK sources of the languages other than
// Go, and reading a site in one would abort a fail-closed pass at a site
// the register is never seeded for. A tracked Go file under sdks/ is a
// carrier through its doc comments like any other tracked Go file,
// including its test files.
//
// The predicate is held here rather than in the pass, so the pass that
// writes the sites and the lint that reads them share one statement of
// the domain. It answers for a whole file; the in-file position the
// prohibition covers in a Go file is the doc comment, which the pass
// applies on top of this answer.
func ReservedPhraseCarrier(target string) bool {
	for _, prefix := range reservedPhrasePrefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	if filepath.Ext(target) == ".go" {
		return true
	}
	return !strings.Contains(target, "/") && filepath.Ext(target) == ".md"
}

// markdownAnchorExprs match the two markdown anchor identifiers the
// naming law's reserved-phrase prohibition places outside its matcher,
// which are the kramdown attribute that declares an anchor and the
// fragment of an intra-repo markdown link.
//
// Each expression carries one capture group naming the span the
// prohibition does not reach. The group is the whole attribute for the
// kramdown form, whose text exists only to declare the identifier, and
// the fragment alone for the link form. The path half of a link
// destination is ordinary prose the register resolves like any other
// site, so an expression spanning the whole destination would leave a
// phrase there with no pass able to write it and no lint able to report
// it.
var markdownAnchorExprs = []*regexp.Regexp{
	regexp.MustCompile(`(\{:[^}\n]*#[A-Za-z0-9._-]+[^}\n]*\})`),
	regexp.MustCompile(`\]\([^)\n]*(#[A-Za-z0-9._-]+)\)`),
}

// MarkdownAnchorIdentifiers returns the byte span of every markdown
// anchor identifier a text carries, each as a half-open [lo, hi) pair.
// An anchor identifier is an addressable link target rather than prose,
// so a reserved noun phrase inside one needs no sense-register entry and
// is left as it stands: rewriting one breaks every inbound link,
// including the untracked links this repository cannot see.
//
// The exclusion is held here rather than in the pass, so the name pass
// that writes the reserved-phrase sites and the naming lint that reads
// them share one statement of it, as they share ReservedPhraseCarrier.
// A lint that re-derived the exclusion would report a site the pass
// cannot write, or pass over one the pass would abort at.
func MarkdownAnchorIdentifiers(text string) [][2]int {
	var out [][2]int
	for _, expr := range markdownAnchorExprs {
		for _, m := range expr.FindAllStringSubmatchIndex(text, -1) {
			if len(m) < 4 || m[2] < 0 {
				continue
			}
			out = append(out, [2]int{m[2], m[3]})
		}
	}
	return out
}

// GoDocCommentSpans returns the byte span of every doc comment of a Go
// carrier, each as a half-open [lo, hi) pair over the content, in source
// order. A doc comment is the group the parser attaches to the package
// clause or to a declaration, which is the position the naming law's
// reserved-phrase prohibition governs in a Go file.
//
// Every other comment of the file is outside the prohibition: a header
// prose block a build constraint separates from the package clause by a
// blank line, the prose written between the elements of a package-level
// composite literal, a comment trailing the code it annotates, and an
// implementation comment inside a function body alike. A phrase in one
// of those is neither substituted nor demanded of the sense register.
//
// The position rule is held here rather than in the pass, so the name
// pass that writes the reserved-phrase sites of a Go carrier and the
// naming lint that reads them share one statement of it, as they share
// ReservedPhraseCarrier and MarkdownAnchorIdentifiers. A lint that
// re-derived the position would report a site the pass cannot write, and
// a pass reading a wider position than the lint would demand a register
// entry at a site the law does not govern and shift the occurrence
// numbering the register is keyed by for every later site of the file.
//
// A file the parser cannot read fails rather than being read whole or
// skipped. Reading it whole would rewrite an implementation comment and
// an operator-facing literal the law does not govern, and skipping it
// would leave every doc comment it carries with no writer.
func GoDocCommentSpans(target, content string) ([][2]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target, content, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("read the doc comments of %s: %w", target, err)
	}
	var out [][2]int
	for c := range attachedDocComments(file) {
		lo := fset.Position(c.Pos()).Offset
		out = append(out, [2]int{lo, lo + len(c.Text)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, nil
}

// attachedDocComments returns the set of comments the parser attaches to
// the package clause or to a declaration as its documentation.
func attachedDocComments(file *ast.File) map[*ast.Comment]bool {
	out := map[*ast.Comment]bool{}
	add := func(group *ast.CommentGroup) {
		if group == nil {
			return
		}
		for _, c := range group.List {
			out[c] = true
		}
	}
	add(file.Doc)
	ast.Inspect(file, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			add(decl.Doc)
		case *ast.GenDecl:
			add(decl.Doc)
		case *ast.TypeSpec:
			add(decl.Doc)
		case *ast.ValueSpec:
			add(decl.Doc)
		case *ast.ImportSpec:
			add(decl.Doc)
		case *ast.Field:
			add(decl.Doc)
		}
		return true
	})
	return out
}

// hasSegment reports whether a slash-separated path carries the named
// directory segment. Matching a segment rather than a substring keeps a
// file named `testdata.go` inside the domain.
func hasSegment(p, segment string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

// FileReader reads a repo-relative, slash-separated tracked path.
type FileReader func(target string) ([]byte, error)

// DirReader returns a FileReader rooted at dir.
func DirReader(dir string) FileReader {
	return func(target string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(target)))
	}
}

// DirWriter returns a writer rooted at dir that preserves each file's
// existing mode. The passes and the gates write through it, so a file
// rewritten by a pass and a baseline rewritten by a gate keep the mode
// they were committed with.
func DirWriter(dir string) func(target string, content []byte) error {
	return func(target string, content []byte) error {
		full := filepath.Join(dir, filepath.FromSlash(target))
		mode := os.FileMode(0o644)
		if info, err := os.Stat(full); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(full, content, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	}
}

// DirRemover returns a remover rooted at dir. A path that is already
// absent is reported as removed, so the rollback of a move whose write
// never landed is not itself a failure.
func DirRemover(dir string) func(target string) error {
	return func(target string) error {
		err := os.Remove(filepath.Join(dir, filepath.FromSlash(target)))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", target, err)
		}
		return nil
	}
}

// Lister reports the tracked paths of a tree, repo-relative and
// slash-separated.
type Lister func(ctx context.Context) ([]string, error)

// GitLister lists the tracked tree with `git ls-files`. Membership comes
// from the index rather than from a filesystem walk, because an
// untracked file is outside every remedy the passes and the gates name.
func GitLister(root string) Lister {
	return func(ctx context.Context) ([]string, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
		}
		var paths []string
		for _, raw := range bytes.Split(out, []byte{0}) {
			if len(raw) == 0 {
				continue
			}
			paths = append(paths, string(raw))
		}
		return paths, nil
	}
}

// DirLister lists every ordinary file under dir, repo-relative to dir.
// It stands in for the git index over a fixture tree.
func DirLister(dir string) Lister {
	return func(ctx context.Context) ([]string, error) {
		var paths []string
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return fmt.Errorf("relativize %s: %w", p, err)
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", dir, err)
		}
		return paths, nil
	}
}

// ReadDomain returns the tracked paths inside the read domain, sorted.
//
// It fails on a domain that selected nothing over a tree that is not
// empty, as well as on an empty tree. A walk root pointed at an excluded
// subtree, or an exclusion list that happens to cover every path the
// walk produced, otherwise yields an empty domain that every caller
// reads as a completed inspection: a pass reports an empty diff and a
// gate reports green over content neither of them opened.
func ReadDomain(ctx context.Context, list Lister) ([]string, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked tree: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("list tracked tree: no tracked path found")
	}
	domain := make([]string, 0, len(all))
	for _, p := range all {
		if Readable(p) {
			domain = append(domain, p)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("read domain over %d tracked path(s): the exclusion list selected zero files", len(all))
	}
	sort.Strings(domain)
	return domain, nil
}

// ClassReadDomain returns the tracked paths inside one class's read
// domain, sorted. It is the domain the residual scan for that class
// ranges over.
func ClassReadDomain(ctx context.Context, list Lister, c Class) ([]string, error) {
	readable, err := ReadDomain(ctx, list)
	if err != nil {
		return nil, err
	}
	domain := make([]string, 0, len(readable))
	for _, target := range readable {
		ok, err := ReadableForClass(c, target)
		if err != nil {
			return nil, err
		}
		if ok {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("%s class read domain over %d readable path(s): the exclusion list selected zero files",
			c, len(readable))
	}
	return domain, nil
}

// KeyWriteDomain returns the tracked path-keyed registers the pass
// rekeys through the key channel rather than through its site rewrite,
// sorted. A register the pass already site-rewrites is absent, because
// its key rewrite runs alongside that rewrite in one diff entry.
//
// It carries the same zero-inspection guard the read and write domains
// do. A channel that selected nothing reports a completed rekey over
// registers it never opened, and a rename whose keys never move makes the
// citation ratchet fire on a file that changed no citation while every
// baselined non-resolving citation under the old path re-surfaces as a
// resolver failure, with the run that caused it having exited clean.
func KeyWriteDomain(ctx context.Context, list Lister, p Pass, read FileReader) ([]string, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("key-write domain: unknown pass %q", p)
	}
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked tree: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("list tracked tree: no tracked path found")
	}
	domain := make([]string, 0, len(pathKeyedRegisterRule))
	for _, target := range all {
		if !KeyWritable(target) {
			continue
		}
		site, err := Writable(p, target, read)
		if err != nil {
			return nil, err
		}
		if !site {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("%s pass key-write domain over %d tracked path(s): no path-keyed register (%s) is outside the site-rewrite domain",
			p, len(all), strings.Join(pathKeyedRegisterRule, ", "))
	}
	sort.Strings(domain)
	return domain, nil
}

// WriteDomain returns the tracked paths the pass may write, sorted. It
// carries the same zero-inspection guard ReadDomain does, over its own
// filtered result: a pass whose write domain collapses to nothing aborts
// rather than reporting the empty diff of a completed migration.
func WriteDomain(ctx context.Context, list Lister, p Pass, read FileReader) ([]string, error) {
	readable, err := ReadDomain(ctx, list)
	if err != nil {
		return nil, err
	}
	domain := make([]string, 0, len(readable))
	for _, target := range readable {
		ok, err := Writable(p, target, read)
		if err != nil {
			return nil, err
		}
		if ok {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("%s pass write domain over %d readable path(s): the exclusion list selected zero files",
			p, len(readable))
	}
	return domain, nil
}

// RepoRoot returns the git top level containing dir.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
