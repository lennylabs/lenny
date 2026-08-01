// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/cmd/lenny-test/changegraph"
	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The residual gate is what makes every enumeration the migration works
// from safe to be incomplete. Each enumerated class is defined by a
// deliberately broad predicate rather than by its enumeration, and
// anything that matches the predicate and appears in neither the
// enumeration nor the class's residual register is a residual. A
// residual fails tier 0, named together with the class that should own
// it, so a member no enumeration anticipated is triaged rather than
// absorbed.
//
// Closing a residual means one of two things. The member belongs to the
// class, so it joins the register as in-class and the class's own pass
// or remediation reaches it. Or it does not, so it joins the register as
// an explicit exclusion with a reason. Both are recorded. Narrowing a
// predicate to reach green is the one route the gate does not accept.
//
// Each class reads its own register at tests/registers/residual-<class>.yaml,
// held separately from the register or baseline that drives the class's
// pass, because a pass register is keyed for its rewrite and several of
// them are emptied as that rewrite completes. A residual entry records a
// triage decision instead: a member, its class, an in-class or excluded
// disposition, and a reason. It carries no owner, no opened-at date, no
// expiry, and no blocker, so the shared register contract's expiry and
// blocker ratchet rules do not range over it. An exclusion is permanent,
// because the member never belonged to the class, and an in-class entry
// is retired by the event that takes its member out of the class rather
// than by a date.
//
// The scan for a class ranges over that class's read domain, taken from
// scripts/specshift/scope rather than re-derived here, so a class's
// residual scan, its pass, and its gates read one domain. That domain is
// the tracked tree less the shared read exclusion, which covers the
// staged proposals, the two historical audit records, the two root
// planning documents, and every testdata directory, less the root-level
// planning records for the classes whose passes exclude them, and less
// the registers the class cannot read as tree content. A residual entry
// holds a copy of its member's text, so a scan that read its own
// register would report that copy under the register's own path as a
// further residual and the entry seeded for it would add another copy,
// which does not converge.

// residualRegisterKind and residualRegisterVersion are what a register
// declares. The kind is the gate's own rather than the shared
// exception-register contract's, which is what keeps the shared
// validator's ratchet rules off these entries.
const (
	residualRegisterKind    = "residual-register"
	residualRegisterVersion = 1
)

// The dispositions an entry carries.
const (
	// residualInClass records a member that belongs to the class. Its
	// route out is the class's own pass or remediation.
	residualInClass = "in-class"
	// residualExcluded records a member that never belonged to the class.
	// It is permanent.
	residualExcluded = "excluded"
)

// residualEntry is one triage decision, and one entry of a register.
type residualEntry struct {
	// Member is the member as the class's predicate reads it: the text of
	// a citation, a tracked path, or a call site's key.
	//
	// It is stored with the continuation join of the occurrence it was
	// read from preserved, so a member read across two comment lines is
	// written across two lines here. A wrapped citation folded onto one
	// line would be a citation the form reads, under this file's own
	// path, and this file is an ordinary member of the read domain the
	// citation resolver and the ratchet share. The key the register is
	// keyed by is the stored text with those line breaks joined back into
	// spaces, which residualMemberKey returns.
	Member string `yaml:"member"`
	// Class names the class the entry belongs to. It repeats the class
	// the register is read for, so an entry cannot be moved between
	// registers by a copy that leaves its class behind.
	Class string `yaml:"class"`
	// Disposition is in-class or excluded.
	Disposition string `yaml:"disposition"`
	// Reason is why the member is in the class or outside it.
	Reason string `yaml:"reason"`
}

// String renders the entry for a report.
func (e residualEntry) String() string {
	return fmt.Sprintf("%s (%s, %s)", residualMemberKey(e.Member), e.Class, e.Disposition)
}

// residualMemberKey returns the key a stored member text is held under,
// which is that text with every preserved continuation join folded back
// into the space it stands for. A member is keyed by the text the
// class's predicate reads rather than by the spelling the register
// stores, so the same occurrence read wrapped in one carrier and
// unwrapped in another is one member.
func residualMemberKey(stored string) string {
	return strings.ReplaceAll(stored, "\n", " ")
}

// residualMember is one member the class's broad predicate selected,
// with the site it was first read at. The site is a report aid and is
// outside the key: the same citation text is written in several files,
// and keying on the site would make an entry for one of them.
type residualMember struct {
	// Member is the member's stored text, with any continuation join
	// preserved as a line break, as residualEntry.Member holds it.
	Member string
	Site   string
}

// residualFailure is one member or entry the run reports. Exactly one of
// the three kinds is set.
type residualFailure struct {
	// Class names the class that should own the member.
	Class scope.Class
	// Unclassified is a member the predicate selected that the
	// enumeration does not cover and the register does not carry.
	Unclassified *residualMember
	// StaleExcluded is an exclusion whose member the predicate no longer
	// selects, so the register would otherwise accumulate dead
	// exclusions.
	StaleExcluded *residualEntry
	// StaleInClass is an in-class entry whose member the predicate no
	// longer selects and which survived the run, so an in-class entry
	// cannot outlive the remediation it recorded.
	StaleInClass *residualEntry
}

// String renders the failure for the gate's message.
func (f residualFailure) String() string {
	switch {
	case f.Unclassified != nil:
		return fmt.Sprintf("%s: %s at %s appears in neither the enumeration nor %s",
			f.Class, residualMemberKey(f.Unclassified.Member), f.Unclassified.Site,
			residualRegisterPath(f.Class))
	case f.StaleExcluded != nil:
		return fmt.Sprintf("%s: %s carries an exclusion for %q, which the class's predicate no longer selects",
			f.Class, residualRegisterPath(f.Class), residualMemberKey(f.StaleExcluded.Member))
	case f.StaleInClass != nil:
		return fmt.Sprintf("%s: %s carries an in-class entry for %q, which the class's predicate no longer selects, "+
			"and the run did not remove it", f.Class, residualRegisterPath(f.Class),
			residualMemberKey(f.StaleInClass.Member))
	}
	return fmt.Sprintf("%s: an empty failure", f.Class)
}

// residualReport is what one run measured for one class.
type residualReport struct {
	// Class is the class the report covers.
	Class scope.Class
	// Files is the number of tracked files inside the class's read
	// domain.
	Files int
	// Selected is the number of members the broad predicate selected
	// after the enumeration was subtracted. It reaches zero when the
	// class empties, which is a terminal state rather than a defect.
	Selected int
	// Carried is how many entries the register held when the run loaded
	// it.
	Carried int
	// Held is how many of those the run read again as live members.
	Held int
	// Removed is every in-class entry the run dropped, because its member
	// left the class. Together with Held and the stale failures it
	// accounts for every entry Carried counts.
	Removed []residualEntry
	// Failures is every member or entry the run reports. A non-empty list
	// is the gate's failure.
	Failures []residualFailure
}

// residualClass is one class of the scan: the register it reads, whether
// a member of it can leave, and the predicate that selects its members.
type residualClass struct {
	// Name is the class, as scripts/specshift/scope declares it.
	Name scope.Class
	// Removable reports whether a member can stop matching the class's
	// broad predicate. Every class here but the generated-artifact class
	// has a route out: a citation is rewritten by the line pass, a
	// tracked path gains a glob key in the change graph, and a skip
	// reason is rewritten to open with a category. A generated file keeps
	// matching for as long as it exists, so its in-class entry is
	// permanent for the same reason an exclusion is.
	Removable bool
	// Members returns the members the broad predicate selects over the
	// class's read domain, with the enumeration already subtracted.
	Members func(ctx context.Context, g *residualGate, domain []string) ([]residualMember, error)
}

// residualClasses are the classes this gate ranges over, in a stable
// order. The reserved-phrase, identifier, and anchor classes are absent
// because their broad predicates match a live population until the
// sub-step that seeds their registers lands: a check whose route to
// green is a content change that has not been made yet cannot land here.
func residualClasses() []residualClass {
	return []residualClass{
		{Name: scope.ClassLineCitations, Removable: true, Members: residualCitationMembers},
		{Name: scope.ClassLineCitationResolution, Removable: true, Members: residualCitationMembers},
		{Name: scope.ClassGeneratedArtifact, Removable: false, Members: residualGeneratedMembers},
		{Name: scope.ClassChangeGraphCoverage, Removable: true, Members: residualChangeGraphMembers},
		{Name: scope.ClassSkipReason, Removable: true, Members: residualSkipMembers},
	}
}

// residualRegisterPath returns the class's register.
func residualRegisterPath(c scope.Class) string { return c.ResidualRegister() }

// residualGate is the tier-0 residual gate. Its dependencies are
// injected so a case drives it over a fixture tree.
type residualGate struct {
	// List enumerates the tracked tree.
	List scope.Lister
	// Read reads a repo-relative tracked path.
	Read scope.FileReader
	// Write writes a repo-relative tracked path. It is used for the
	// downward removal of an in-class entry whose member left the class,
	// and nothing else. A gate with no writer reports such an entry
	// instead, so a read-only inspection never certifies a register it
	// could not reconcile.
	Write func(path string, content []byte) error
}

// newResidualGate returns the gate over the tracked tree at root.
func newResidualGate(root string) *residualGate {
	return &residualGate{
		List:  scope.GitLister(root),
		Read:  scope.DirReader(root),
		Write: scope.DirWriter(root),
	}
}

// newResidualGateOver returns the gate with each dependency supplied, so
// a caller drives it over a tree other than a git checkout. A nil writer
// is a read-only run.
func newResidualGateOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *residualGate {
	return &residualGate{List: list, Read: read, Write: write}
}

// RunAll runs every class and returns one report each, in class order. A
// class that could not be inspected fails the whole run: a report of no
// residual for the classes that did run would certify a tree the gate
// only partly opened.
func (g *residualGate) RunAll(ctx context.Context) ([]residualReport, error) {
	var out []residualReport
	for _, c := range residualClasses() {
		rep, err := g.Run(ctx, c)
		if err != nil {
			return out, err
		}
		out = append(out, rep)
	}
	return out, nil
}

// Run computes one class's residual.
//
// A run that could not inspect the tree returns an error rather than an
// empty report: a walk that selected no file over a tree that is not
// empty, and a missing or malformed register, are both states in which a
// report of no residual would certify content the run never opened, or
// would exempt every member at once.
//
// A class whose predicate selects no member over a scan that did select
// files is the terminal state the class's pass or remediation reaches,
// so it is reported green. An in-class entry that outlived its member is
// the register-versus-tree inconsistency that state must not hide, and
// it is removed by the run or reported by it.
func (g *residualGate) Run(ctx context.Context, c residualClass) (residualReport, error) {
	rep := residualReport{Class: c.Name}
	if g.List == nil || g.Read == nil {
		return rep, fmt.Errorf("residual gate: a lister and a reader are required")
	}
	// The class is named on the domain failure as well as on the
	// residual, because a walk root or an exclusion list that selects
	// nothing is reported against the class whose scan it emptied,
	// whichever of the two exclusions emptied it.
	domain, err := scope.ClassReadDomain(ctx, g.List, c.Name)
	if err != nil {
		return rep, fmt.Errorf("residual gate: %s class: %w", c.Name, err)
	}
	carried, order, err := loadResidualRegister(g.Read, c.Name)
	if err != nil {
		return rep, fmt.Errorf("residual gate: %w", err)
	}
	members, err := c.Members(ctx, g, domain)
	if err != nil {
		return rep, fmt.Errorf("residual gate: %s class: %w", c.Name, err)
	}
	rep.Files = len(domain)
	rep.Selected = len(members)
	rep.Carried = len(carried)

	kept := g.compare(c, members, carried, order, &rep)
	// The removal lands on a run that reports nothing else. A run that
	// fails leaves the register byte-identical, so the next run measures
	// the same population and reverting the edit that failed it restores
	// green.
	if len(rep.Failures) == 0 && len(rep.Removed) > 0 {
		if err := writeResidualRegister(g.Write, c.Name, kept); err != nil {
			return rep, fmt.Errorf("residual gate: %w", err)
		}
	}
	return rep, nil
}

// compare measures the selected members against the register, fills in
// the report, and returns the entries a rewritten register holds.
//
// A member the register does not carry is a residual. An entry whose
// member the predicate no longer selects is either dropped, when the
// class has a route out and a writer is configured, or reported.
func (g *residualGate) compare(c residualClass, members []residualMember,
	carried map[string]residualEntry, order []string, rep *residualReport,
) []residualEntry {
	selected := make(map[string]bool, len(members))
	for _, m := range members {
		selected[residualMemberKey(m.Member)] = true
	}
	for _, m := range members {
		if _, ok := carried[residualMemberKey(m.Member)]; !ok {
			held := m
			rep.Failures = append(rep.Failures, residualFailure{Class: c.Name, Unclassified: &held})
		}
	}
	kept := make([]residualEntry, 0, len(carried))
	for _, member := range order {
		entry := carried[member]
		if selected[member] {
			rep.Held++
			kept = append(kept, entry)
			continue
		}
		stale := entry
		switch {
		case entry.Disposition == residualExcluded:
			// An exclusion is permanent, so a member the predicate has
			// stopped selecting is a dead exclusion rather than a
			// remediation, and the register does not accumulate one.
			rep.Failures = append(rep.Failures, residualFailure{Class: c.Name, StaleExcluded: &stale})
			kept = append(kept, entry)
		case !c.Removable || g.Write == nil:
			// A class with no route out has no remediation to record, and
			// a run with no writer cannot record one, so the entry is
			// reported rather than left standing unexplained.
			rep.Failures = append(rep.Failures, residualFailure{Class: c.Name, StaleInClass: &stale})
			kept = append(kept, entry)
		default:
			// The member took the class's own route out, so the entry
			// that recorded it goes with it in the same run.
			rep.Removed = append(rep.Removed, entry)
		}
	}
	return kept
}

// residualCitationMembers selects the members of the two line-citation
// classes. The predicate is any occurrence of a section sigil or a
// section path standing adjacent to a line-number token, with adjacency
// tolerating the continuation join, and with no commitment to a member
// separator, a range spelling, or a comment dialect. The enumeration it
// subtracts is the citation form itself, which is what the line pass
// rewrites, the ratchet counts, and the resolver reads, so the residual
// is the spelling the form misses.
//
// A member is keyed by its text rather than by its site. The same
// spelling is written in several files, and a site-keyed member would
// demand an entry for each of them while the text a register carries is
// the same string. Keying on the text is also what makes the seeding
// converge, because the copy a register holds of a member's text is the
// member already recorded.
//
// The text is recorded with the occurrence's continuation join
// preserved, so a citation read across two comment lines is stored
// across two lines. A residual register is an ordinary member of the
// shared read domain, and a wrapped occurrence folded onto one line
// would stand there as a citation the form reads, which the ratchet
// counts and the resolver reports under the register's own path.
func residualCitationMembers(ctx context.Context, g *residualGate, domain []string) ([]residualMember, error) {
	seen := map[string]residualMember{}
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := g.Read(target)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
		content := string(data)
		read := citation.FindIn(target, content)
		for _, b := range citation.FindBroadIn(target, content) {
			if coveredByCitationForm(b, read) {
				continue
			}
			if _, ok := seen[b.Text]; ok {
				continue
			}
			seen[b.Text] = residualMember{
				Member: strings.Join(b.Lines, "\n"),
				Site:   fmt.Sprintf("%s:%d", target, b.Line),
			}
		}
	}
	return sortedResidualMembers(seen), nil
}

// coveredByCitationForm reports whether the broad occurrence overlaps a
// citation the form itself read. Overlap rather than equality is the
// test, because the two matchers bound a citation differently: the form
// consumes a trailing gloss the broad predicate stops before, and the
// broad predicate consumes a separator run the form does not admit. An
// occurrence that shares any byte with a citation the form read is
// already enumerated.
func coveredByCitationForm(b citation.LocatedBroad, read []citation.Located) bool {
	for _, c := range read {
		if b.Offset < c.Offset+len(c.Raw) && c.Offset < b.End {
			return true
		}
	}
	return false
}

// residualGeneratedMembers selects the members of the generated-artifact
// class. The predicate is the union the per-file generated-artifact rule
// applies: a generation marker in the file header, a generation
// declaration in top-level document metadata for a format that carries
// no comment syntax, and membership in the output set of a named
// producer. The enumeration it subtracts is the marker: a file that
// declares itself generated needs no triage, so what is left is the file
// selected by producer membership alone.
//
// That second disjunct is what makes the class complete. The CRDs under
// charts/lenny/crds/ and their copies under pkg/embedded/crds/ are
// producer output that carries no marker at all, so a marker-only rule
// would classify them as ordinary carriers and direct a pass to write
// them. The producer output sets are read from the producer list rather
// than by running a producer, which is what makes the rule decidable at
// tier 0.
func residualGeneratedMembers(ctx context.Context, g *residualGate, domain []string) ([]residualMember, error) {
	seen := map[string]residualMember{}
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		disjunct, err := scope.Generated(target, g.Read)
		if err != nil {
			return nil, err
		}
		if disjunct == scope.NotGenerated {
			continue
		}
		marker, err := scope.MarkerDeclared(target, g.Read)
		if err != nil {
			return nil, err
		}
		if marker != scope.NotGenerated {
			continue
		}
		seen[target] = residualMember{Member: target, Site: target}
	}
	return sortedResidualMembers(seen), nil
}

// residualDataExts is the documented denylist the change-graph coverage
// class's broad predicate subtracts: the extensions whose content is
// data, configuration, or documentation rather than a language this tree
// builds, renders, or executes from. A path with no extension carries no
// language either, and in this tree names a dotfile, a license, a
// container or make recipe, or a fuzz corpus entry.
//
// The predicate is stated as a denylist because an allowlist of source
// extensions is another enumeration wearing a regular expression: a
// language added to the tree later drops out of the class silently,
// which is the tail the residual exists to catch. A file in a language
// this list does not name is a member and is triaged in the register,
// and widening this list is the predicate narrowing the gate does not
// accept as a route to green.
var residualDataExts = []string{
	"", ".md", ".txt", ".yaml", ".yml", ".json", ".toml", ".cfg", ".xml", ".mod", ".sum",
}

// residualChangeGraphMembers selects the members of the change-graph
// coverage class. The predicate is any tracked file that no glob key in
// the change graph covers, over every tree and every language, less the
// documented data and documentation extensions. A test file is outside
// the predicate for the reason the completeness check states: a test is
// a target of the graph rather than a source of it.
//
// The enumeration it subtracts is the completeness check's own domain,
// which the check holds to a glob key or to its coverage baseline on
// every run of validate-maps. That domain is read from
// cmd/lenny-test/changegraph, which the check reads as well, so a tree
// or an extension that moves moves for both at once and no path is left
// governed by neither. What remains is the source file the check's
// narrowing never reaches, whose route out is the same glob key.
func residualChangeGraphMembers(ctx context.Context, g *residualGate, domain []string) ([]residualMember, error) {
	globs, err := readResidualChangeGraphGlobs(g.Read)
	if err != nil {
		return nil, err
	}
	seen := map[string]residualMember{}
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !residualSourceFile(target) || changegraph.InCheckDomain(target) {
			continue
		}
		if changegraph.CoveredByGlob(target, globs) {
			continue
		}
		seen[target] = residualMember{Member: target, Site: target}
	}
	return sortedResidualMembers(seen), nil
}

// residualSourceFile reports whether a tracked path is one the broad
// predicate ranges over: anything but a test file and anything but the
// data and documentation extensions residualDataExts names.
func residualSourceFile(target string) bool {
	if strings.HasSuffix(target, "_test.go") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(target))
	for _, denied := range residualDataExts {
		if ext == denied {
			return false
		}
	}
	return true
}

// readResidualChangeGraphGlobs returns the change graph's glob keys,
// read through the reading in cmd/lenny-test/changegraph that the
// completeness check and the `--changed` selector read, so a key means
// the same thing to all three.
//
// A missing or malformed graph is an error rather than an empty key set:
// a load that degraded to carrying nothing would report every source
// file in the tree as a residual.
func readResidualChangeGraphGlobs(read scope.FileReader) (map[string]bool, error) {
	data, err := read(changegraph.RelPath)
	if err != nil {
		return nil, fmt.Errorf("read the change graph %s: %w", changegraph.RelPath, err)
	}
	return changegraph.ParseGlobKeys(changegraph.RelPath, data)
}

// residualSkipMembers selects the members of the skip-reason class. The
// predicate is any call whose selected name opens with Skip, in any
// tracked Go file of the class's read domain, that passes an argument
// the classifier would read as a reason and whose reason does not open
// with one of the categories. It commits to no tree and to no spelling
// of the call beyond that opening, because a skip that states nothing a
// reader can act on is the same defect wherever it is written.
//
// The enumeration it subtracts is the skip-reason classifier's own
// domain and call forms, which the classifier holds to the categories or
// to its register on every run. What is left is the skip call the
// classifier's narrowing never reaches, in a tree outside its three or
// under a name outside its two forms.
func residualSkipMembers(ctx context.Context, g *residualGate, domain []string) ([]residualMember, error) {
	seen := map[string]residualMember{}
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(target, ".go") {
			continue
		}
		data, err := g.Read(target)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
		found, err := residualSkipsInFile(target, data)
		if err != nil {
			return nil, err
		}
		for _, m := range found {
			seen[m.Member] = m
		}
	}
	return sortedResidualMembers(seen), nil
}

// residualSkipsInFile returns every skip-named call in one Go file that
// the classifier's domain and call forms leave unread and whose reason
// states no category.
//
// A file that does not parse is an error rather than a file with no
// call: a scan that passed over what it could not read would go green on
// the file a rewrite broke.
func residualSkipsInFile(target string, src []byte) ([]residualMember, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", target, err)
	}
	governed := inSkipDomain(target)
	var out []residualMember
	ordinals := map[string]int{}
	for _, decl := range file.Decls {
		fn := enclosingFuncName(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := residualSkipName(call)
			if !ok {
				return true
			}
			ordinals[fn]++
			if governed && residualSkipEnumerated(name) {
				return true
			}
			if residualSkipStatesCategory(call) {
				return true
			}
			out = append(out, residualMember{
				Member: residualSkipKey(target, fn, ordinals[fn], name),
				Site:   fmt.Sprintf("%s:%d", target, fset.Position(call.Lparen).Line),
			})
			return true
		})
	}
	return out, nil
}

// residualSkipName reports the selected name when the call opens with
// Skip. The predicate is the opening of the name alone, so a spelling
// the classifier's two forms do not carry is still selected.
func residualSkipName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(sel.Sel.Name, skipCallPlain) {
		return "", false
	}
	return sel.Sel.Name, true
}

// residualSkipEnumerated reports whether the classifier's call forms
// already read the name.
func residualSkipEnumerated(name string) bool {
	return name == skipCallPlain || name == skipCallFormatted || strings.HasPrefix(name, skipHelperPrefix)
}

// residualSkipStatesCategory reports whether the call's first argument
// is a string literal opening with one of the categories. A call with no
// argument states no reason to classify and is outside the predicate,
// which is the same answer the classifier gives a bare skip.
func residualSkipStatesCategory(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return true
	}
	reason, ok := constantStringExpr(call.Args[0])
	if !ok {
		return false
	}
	return openedWithSkipCategory(reason)
}

// residualSkipKey renders a skip call site as a member. The site is
// keyed by its file, the function holding it, and its ordinal among the
// skip-named call sites of that function, because the line a call sits
// on moves with any edit above it.
func residualSkipKey(path, function string, ordinal int, name string) string {
	return fmt.Sprintf("%s %s (site %d): %s", path, function, ordinal, name)
}

// sortedResidualMembers returns the members in a stable order.
func sortedResidualMembers(seen map[string]residualMember) []residualMember {
	out := make([]residualMember, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return residualMemberKey(out[i].Member) < residualMemberKey(out[j].Member)
	})
	return out
}

// residualRegisterDoc is a register's schema. Entries is a pointer so a
// document that declares no entries block is distinguishable from one
// that declares an empty list; the first is malformed and the second is
// a class whose predicate selects nothing the enumeration misses.
type residualRegisterDoc struct {
	Kind    string           `yaml:"kind"`
	Version int              `yaml:"version"`
	Class   string           `yaml:"class"`
	Entries *[]residualEntry `yaml:"entries"`
}

// loadResidualRegister reads and validates one class's register into its
// entries, returning them by member together with the order they were
// written in.
//
// A missing, unreadable, or malformed register is an error rather than
// an empty set: a load that degraded to carrying nothing would report
// every member of the class at once, and one that degraded to carrying
// everything would exempt the population the gate exists to surface.
func loadResidualRegister(read scope.FileReader, c scope.Class) (map[string]residualEntry, []string, error) {
	path := residualRegisterPath(c)
	if read == nil {
		return nil, nil, fmt.Errorf("read residual register %s: no reader is configured", path)
	}
	data, err := read(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read residual register %s: %w", path, err)
	}
	var doc residualRegisterDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse residual register %s: %w", path, err)
	}
	if doc.Kind != residualRegisterKind {
		return nil, nil, fmt.Errorf("residual register %s: expected kind %q, got %q", path, residualRegisterKind, doc.Kind)
	}
	if doc.Version != residualRegisterVersion {
		return nil, nil, fmt.Errorf("residual register %s: expected version %d, got %d",
			path, residualRegisterVersion, doc.Version)
	}
	if doc.Class != string(c) {
		return nil, nil, fmt.Errorf("residual register %s: declares class %q, and it is read for class %q",
			path, doc.Class, c)
	}
	if doc.Entries == nil {
		return nil, nil, fmt.Errorf("residual register %s: carries no entries block", path)
	}
	carried := map[string]residualEntry{}
	var order []string
	for _, entry := range *doc.Entries {
		if err := validResidualEntry(path, c, entry); err != nil {
			return nil, nil, err
		}
		key := residualMemberKey(entry.Member)
		if _, ok := carried[key]; ok {
			return nil, nil, fmt.Errorf("residual register %s: member %q is declared twice", path, key)
		}
		carried[key] = entry
		order = append(order, key)
	}
	return carried, order, nil
}

// validResidualEntry holds one entry to the schema. A reason is required
// on both dispositions: an entry with no reason records that someone
// looked at the member without recording what they concluded, which is
// the silent widening the register exists to prevent.
func validResidualEntry(path string, c scope.Class, entry residualEntry) error {
	if strings.TrimSpace(entry.Member) == "" {
		return fmt.Errorf("residual register %s: carries an entry with no member", path)
	}
	if err := validResidualMemberText(path, entry.Member); err != nil {
		return err
	}
	if entry.Class != string(c) {
		return fmt.Errorf("residual register %s: member %q declares class %q, and the register is read for class %q",
			path, entry.Member, entry.Class, c)
	}
	if entry.Disposition != residualInClass && entry.Disposition != residualExcluded {
		return fmt.Errorf("residual register %s: member %q carries disposition %q, want %q or %q",
			path, entry.Member, entry.Disposition, residualInClass, residualExcluded)
	}
	if strings.TrimSpace(entry.Reason) == "" {
		return fmt.Errorf("residual register %s: member %q carries no reason", path, entry.Member)
	}
	if strings.ContainsAny(entry.Reason, "\n\r") {
		return fmt.Errorf("residual register %s: member %q carries a reason with a line break, and a reason is one line",
			path, entry.Member)
	}
	return nil
}

// validResidualMemberText holds a stored member text to the spelling the
// register writes. A line break stands for a continuation the class's
// predicate joined, so each line carries content and neither opens nor
// closes on whitespace: a blank or indented line would not survive the
// block scalar it is written in, and the member the next run reads back
// would differ from the one recorded.
func validResidualMemberText(path, member string) error {
	if strings.Contains(member, "\r") {
		return fmt.Errorf("residual register %s: member %q carries a carriage return", path, member)
	}
	for _, line := range strings.Split(member, "\n") {
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("residual register %s: member %q carries an empty line",
				path, residualMemberKey(member))
		}
		if line != strings.TrimSpace(line) {
			return fmt.Errorf("residual register %s: member %q carries a line opening or closing on whitespace",
				path, residualMemberKey(member))
		}
	}
	return nil
}

// writeResidualRegister writes one class's register holding exactly the
// entries given. It is the one writer of the file: the downward removal
// calls it with the entries that survived a run, and the seeding of a
// register calls it with the members the gate itself selected, so the
// file is never hand-assembled from a population stated elsewhere.
//
// Every value is written as a literal block scalar. A member is a copy
// of text the class's predicate reads, and the sibling class scans this
// file as ordinary tree content, so a quoted or wrapped spelling would
// present the sibling with a member that differs from the one recorded
// here by an escape or a line break. The seeding would then record the
// altered copy, whose own written form differs again, and it would not
// converge.
func writeResidualRegister(write func(string, []byte) error, c scope.Class, entries []residualEntry) error {
	path := residualRegisterPath(c)
	if write == nil {
		return fmt.Errorf("rewrite residual register %s: no writer is configured", path)
	}
	sorted := append([]residualEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return residualMemberKey(sorted[i].Member) < residualMemberKey(sorted[j].Member)
	})
	var b strings.Builder
	b.WriteString(residualRegisterHeader(c))
	fmt.Fprintf(&b, "kind: %s\nversion: %d\nclass: %s\n", residualRegisterKind, residualRegisterVersion, c)
	// An empty class declares an empty list rather than an absent one,
	// because a document with no entries block is the malformed register
	// the loader refuses, and a class whose predicate selects nothing is
	// the terminal state the pass and the remediation reach.
	if len(sorted) == 0 {
		b.WriteString("entries: []\n")
	} else {
		b.WriteString("entries:\n")
	}
	for i, entry := range sorted {
		if err := validResidualEntry(path, c, entry); err != nil {
			return fmt.Errorf("rewrite residual register %s: %w", path, err)
		}
		if i > 0 && residualMemberKey(sorted[i-1].Member) == residualMemberKey(entry.Member) {
			return fmt.Errorf("rewrite residual register %s: member %q is written twice",
				path, residualMemberKey(entry.Member))
		}
		b.WriteString("  - member: |-\n" + residualBlockScalar(entry.Member))
		b.WriteString("    class: " + entry.Class + "\n")
		b.WriteString("    disposition: " + entry.Disposition + "\n")
		b.WriteString("    reason: |-\n" + residualBlockScalar(entry.Reason))
	}
	if err := write(path, []byte(b.String())); err != nil {
		return fmt.Errorf("rewrite residual register %s: %w", path, err)
	}
	return nil
}

// residualBlockScalar renders a value as the body of a literal block
// scalar, one indented line per line of the value. A member written
// across two lines keeps the continuation join the occurrence carried,
// so the stored copy is not a citation the form reads.
func residualBlockScalar(value string) string {
	var b strings.Builder
	for _, line := range strings.Split(value, "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// residualRegisterHeader explains the file to a reader who opens it
// without the gate beside them. It states no section reference and no
// line number of its own, because the sibling class reads this file as
// tree content and a citation written here would be a member of that
// class.
func residualRegisterHeader(c scope.Class) string {
	return fmt.Sprintf(`# SPDX-License-Identifier: MIT
#
# Residual register for the %s class.
#
# Each entry is a member of the class that the enumeration the migration
# works from does not carry. An in-class entry belongs to the class, and
# its route out is the class's own pass or remediation. An excluded entry
# never belonged to the class, and it is permanent.
#
# The residual gate in tests/tier0_static fails tier 0 on a member that
# matches the class's broad predicate and appears in neither the
# enumeration nor this file, so a member no enumeration anticipated is
# triaged here rather than absorbed. The gate removes an in-class entry
# whose member has left the class in the same run, and it never adds an
# entry.
`, c)
}

// residualTree is a fixture tree the gate runs over. It holds whichever
// files a case writes and the registers at the paths the gate reads.
type residualTree struct {
	t    *testing.T
	root string
}

// newResidualTree returns a fixture tree carrying an empty register for
// every class and a change graph holding one key, so a case writes only
// the files it exercises.
func newResidualTree(t *testing.T) *residualTree {
	t.Helper()
	tr := &residualTree{t: t, root: t.TempDir()}
	for _, c := range residualClasses() {
		tr.register(c.Name)
	}
	tr.changeGraph("pkg")
	return tr
}

// file writes a file into the tree.
func (tr *residualTree) file(target, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// register writes one class's register holding the given entries.
func (tr *residualTree) register(c scope.Class, entries ...residualEntry) {
	tr.t.Helper()
	// The writer preserves the mode of an existing file and creates no
	// directory, so the register is placed before it is written.
	tr.file(residualRegisterPath(c), "")
	if err := writeResidualRegister(scope.DirWriter(tr.root), c, entries); err != nil {
		tr.t.Fatalf("seed the fixture register for %s: %v", c, err)
	}
}

// rawRegister writes one class's register verbatim, for a document the
// writer would not produce.
func (tr *residualTree) rawRegister(c scope.Class, body string) {
	tr.t.Helper()
	tr.file(residualRegisterPath(c), body)
}

// removeRegister deletes one class's register from the tree.
func (tr *residualTree) removeRegister(c scope.Class) {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(residualRegisterPath(c)))); err != nil {
		tr.t.Fatalf("remove the fixture register for %s: %v", c, err)
	}
}

// registerText returns one class's register as it stands in the tree.
func (tr *residualTree) registerText(c scope.Class) string {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(residualRegisterPath(c))))
	if err != nil {
		tr.t.Fatalf("read the fixture register for %s: %v", c, err)
	}
	return string(body)
}

// changeGraph writes a change graph holding the given glob keys.
func (tr *residualTree) changeGraph(keys ...string) {
	tr.t.Helper()
	globs := map[string]any{}
	for _, key := range keys {
		globs[key] = []string{"./..."}
	}
	body, err := json.MarshalIndent(map[string]any{"version": 1, "globs": globs}, "", "  ")
	if err != nil {
		tr.t.Fatalf("encode the fixture change graph: %v", err)
	}
	tr.file(changegraph.RelPath, string(body))
}

// run drives one class over the fixture tree.
func (tr *residualTree) run(c residualClass) (residualReport, error) {
	tr.t.Helper()
	g := newResidualGateOver(scope.DirLister(tr.root), scope.DirReader(tr.root), scope.DirWriter(tr.root))
	return g.Run(context.Background(), c)
}

// runReadOnly drives one class with no writer, so an entry the run would
// otherwise remove survives it.
func (tr *residualTree) runReadOnly(c residualClass) (residualReport, error) {
	tr.t.Helper()
	g := newResidualGateOver(scope.DirLister(tr.root), scope.DirReader(tr.root), nil)
	return g.Run(context.Background(), c)
}

// runOK drives one class and fails the test when the run could not
// inspect the tree.
func (tr *residualTree) runOK(c residualClass) residualReport {
	tr.t.Helper()
	rep, err := tr.run(c)
	if err != nil {
		tr.t.Fatalf("run the %s residual scan over the fixture tree: %v", c.Name, err)
	}
	return rep
}

// residualClassByName returns the class of that name.
func residualClassByName(t *testing.T, name scope.Class) residualClass {
	t.Helper()
	for _, c := range residualClasses() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the residual gate carries no %s class", name)
	return residualClass{}
}

// residualFailureTexts renders a report's failures for an assertion
// message.
func residualFailureTexts(rep residualReport) []string {
	out := make([]string, 0, len(rep.Failures))
	for _, f := range rep.Failures {
		out = append(out, f.String())
	}
	return out
}

// residualNames a report's unclassified members.
func residualNames(rep residualReport) []string {
	var out []string
	for _, f := range rep.Failures {
		if f.Unclassified != nil {
			out = append(out, f.Unclassified.Member)
		}
	}
	return out
}

// inClassEntry and excludedEntry build a register entry of each
// disposition.
func inClassEntry(c scope.Class, member string) residualEntry {
	return residualEntry{Member: member, Class: string(c), Disposition: residualInClass, Reason: "the class's pass reaches it"}
}

func excludedEntry(c scope.Class, member string) residualEntry {
	return residualEntry{Member: member, Class: string(c), Disposition: residualExcluded, Reason: "it never belonged to the class"}
}

func TestResidualGateCertifiesTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	reports, err := newResidualGate(root).RunAll(context.Background())
	if err != nil {
		t.Fatalf("run the residual gate over the tracked tree: %v", err)
	}
	if len(reports) != len(residualClasses()) {
		t.Fatalf("the gate reported %d class(es), want %d", len(reports), len(residualClasses()))
	}
	for _, rep := range reports {
		if len(rep.Failures) > 0 {
			t.Errorf("%d member(s) of the %s class are classified by neither the enumeration nor %s:\n  %s",
				len(rep.Failures), rep.Class, residualRegisterPath(rep.Class),
				strings.Join(residualFailureTexts(rep), "\n  "))
		}
		// A scan that read a fraction of the tree reports no residual, so
		// the file count is asserted rather than taken as a clean class.
		// The selected count and the register are not asserted to be
		// non-zero: both reach zero when a class empties, which is the
		// terminal state the pass and the remediation drive toward.
		if rep.Files == 0 {
			t.Errorf("the %s scan read no file; a report of no residual over an unread tree certifies nothing", rep.Class)
		}
		t.Logf("%s: %d file(s) in the domain, %d member(s) selected, %d carried, %d held, %d removed",
			rep.Class, rep.Files, rep.Selected, rep.Carried, rep.Held, len(rep.Removed))
		for _, removed := range rep.Removed {
			t.Logf("%s: removed %s", rep.Class, removed)
		}
	}
}

// residualFixture is one class's residual: the file the case writes, the
// member the scan reports for it, and the route that takes the member
// out of the class.
type residualFixture struct {
	// Class is the class the residual belongs to.
	Class scope.Class
	// Member is what the scan reports.
	Member string
	// Write places the residual in the tree.
	Write func(tr *residualTree)
	// Retire takes the member out of the class on the class's own route
	// out. It is nil for the generated-artifact class, whose members
	// cannot leave: a generated file keeps matching the predicate for as
	// long as it exists.
	Retire func(tr *residualTree)
}

// residualFixtureDir holds the citation a case writes into its fixture
// tree, the carrier that carries it, and the record that quotes it.
//
// The text sits in a fixture rather than in this source because this
// gate reads the tracked tree: a citation written here would be an
// occurrence of the class this gate scans for, reported under this
// file's own path with no route out but the deletion of the case.
// testdata is outside every read domain, so a gate does not report its
// own input.
const residualFixtureDir = "testdata/residual"

// residualCitationCarrier is where a case places the fixture carrier in
// its tree.
const residualCitationCarrier = "pkg/carrier/carrier.go"

// residualFixtureText reads one fixture.
func residualFixtureText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(residualFixtureDir, name))
	if err != nil {
		t.Fatalf("read the residual fixture %s: %v", name, err)
	}
	return string(data)
}

// residualCitationMember is the member the scan reports for the fixture
// carrier. The spelling is outside the enumerated citation grammar,
// which admits no prose between the reference and the keyword, so the
// scan reports it.
func residualCitationMember(t *testing.T) string {
	t.Helper()
	return strings.TrimSuffix(residualFixtureText(t, "citation-member.txt"), "\n")
}

// residualCitationSource is the carrier carrying that citation, and
// residualCitationRetired is the same carrier after the citation is
// rewritten, which is the route the line pass takes a member out of the
// class by.
func residualCitationSource(t *testing.T) string {
	t.Helper()
	return residualFixtureText(t, "citation-carrier.go.txt")
}

func residualCitationRetired(t *testing.T) string {
	t.Helper()
	return residualFixtureText(t, "citation-retired.go.txt")
}

// residualCitationRecord is a record quoting the citation, which a case
// places in an excluded file.
func residualCitationRecord(t *testing.T) string {
	t.Helper()
	return residualFixtureText(t, "citation-record.md.txt")
}

// The producer output a case writes, which carries no generation marker
// of its own, so the marker enumeration does not cover it and the
// producer disjunct is what selects it.
const (
	residualGeneratedMember = "charts/lenny/crds/lenny.dev_widgets.yaml"
	residualGeneratedSource = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.lenny.dev\n"
	residualGeneratedMarked = "# Code generated by controller-gen. DO NOT EDIT.\n" + residualGeneratedSource
)

// The source file a case writes outside the completeness check's trees
// and extensions, and the glob key that covers it.
const (
	residualChangeGraphMember = "dist/tool.rb"
	residualChangeGraphSource = "# a packaging formula\nclass Tool\nend\n"
	residualChangeGraphKey    = "dist"
)

// The skip call site a case writes outside the classifier's trees, and
// the member the scan reports for it.
const residualSkipCarrier = "migrations/carrier_test.go"

// residualSkipMember is the member the scan reports for that site.
var residualSkipMember = residualSkipKey(residualSkipCarrier, "TestCarrier", 1, skipCallPlain)

// residualFixtures returns one fixture per class, in class order.
func residualFixtures(t *testing.T) []residualFixture {
	t.Helper()
	return []residualFixture{
		{
			Class:  scope.ClassLineCitations,
			Member: residualCitationMember(t),
			Write:  func(tr *residualTree) { tr.file(residualCitationCarrier, residualCitationSource(tr.t)) },
			Retire: func(tr *residualTree) { tr.file(residualCitationCarrier, residualCitationRetired(tr.t)) },
		},
		{
			Class:  scope.ClassLineCitationResolution,
			Member: residualCitationMember(t),
			Write:  func(tr *residualTree) { tr.file(residualCitationCarrier, residualCitationSource(tr.t)) },
			Retire: func(tr *residualTree) { tr.file(residualCitationCarrier, residualCitationRetired(tr.t)) },
		},
		{
			Class:  scope.ClassGeneratedArtifact,
			Member: residualGeneratedMember,
			Write:  func(tr *residualTree) { tr.file(residualGeneratedMember, residualGeneratedSource) },
		},
		{
			Class:  scope.ClassChangeGraphCoverage,
			Member: residualChangeGraphMember,
			Write:  func(tr *residualTree) { tr.file(residualChangeGraphMember, residualChangeGraphSource) },
			Retire: func(tr *residualTree) { tr.changeGraph("pkg", residualChangeGraphKey) },
		},
		{
			Class:  scope.ClassSkipReason,
			Member: residualSkipMember,
			Write: func(tr *residualTree) {
				tr.file(residualSkipCarrier, skipCarrierSource(skipCarrierFunc{
					Name:       "TestCarrier",
					Statements: []string{`t.Skip("the bundle is absent")`},
				}))
			},
			Retire: func(tr *residualTree) {
				tr.file(residualSkipCarrier, skipCarrierSource(skipCarrierFunc{
					Name:       "TestCarrier",
					Statements: []string{fmt.Sprintf("t.Skip(%q)", skipreason.Categories[0]+" the bundle is absent")},
				}))
			},
		},
	}
}

// assertResidualNames fails the test unless the report names exactly the
// member given.
func assertResidualNames(t *testing.T, rep residualReport, member string) {
	t.Helper()
	named := residualNames(rep)
	if len(named) != 1 || named[0] != member {
		t.Fatalf("the %s scan reported %v, want the one member %q:\n  %s",
			rep.Class, named, member, strings.Join(residualFailureTexts(rep), "\n  "))
	}
}

func TestResidualGateFailsOnAResidualInEachClassAndNamesIt(t *testing.T) {
	t.Parallel()
	for _, fixture := range residualFixtures(t) {
		t.Run(string(fixture.Class), func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			fixture.Write(tr)
			c := residualClassByName(t, fixture.Class)

			rep := tr.runOK(c)
			assertResidualNames(t, rep, fixture.Member)
			// The gate names the class alongside the member, because the
			// remedy is an entry in that class's register.
			if !strings.Contains(rep.Failures[0].String(), string(fixture.Class)) {
				t.Errorf("the failure %q names no class", rep.Failures[0])
			}
		})
	}
}

func TestResidualGateAcceptsAMemberRegisteredInEachDisposition(t *testing.T) {
	t.Parallel()
	for _, fixture := range residualFixtures(t) {
		for _, disposition := range []string{residualInClass, residualExcluded} {
			t.Run(string(fixture.Class)+"/"+disposition, func(t *testing.T) {
				t.Parallel()
				tr := newResidualTree(t)
				fixture.Write(tr)
				entry := inClassEntry(fixture.Class, fixture.Member)
				if disposition == residualExcluded {
					entry = excludedEntry(fixture.Class, fixture.Member)
				}
				tr.register(fixture.Class, entry)
				c := residualClassByName(t, fixture.Class)

				rep := tr.runOK(c)
				if len(rep.Failures) > 0 {
					t.Errorf("a member registered as %s was reported:\n  %s",
						disposition, strings.Join(residualFailureTexts(rep), "\n  "))
				}
				// The member is held rather than passed over, so a run
				// that read nothing cannot produce this result.
				if rep.Selected != 1 || rep.Held != 1 {
					t.Errorf("the scan selected %d member(s) and held %d entr(ies), want one of each",
						rep.Selected, rep.Held)
				}
			})
		}
	}
}

func TestResidualGateFailsOnAnEntryWithNoReason(t *testing.T) {
	t.Parallel()
	tr := newResidualTree(t)
	fixture := residualFixtures(t)[0]
	fixture.Write(tr)
	// The writer refuses an entry with no reason, so the document is
	// written verbatim: a triage decision with no reason recorded is what
	// the case exercises.
	tr.rawRegister(fixture.Class, "kind: residual-register\nversion: 1\nclass: "+string(fixture.Class)+
		"\nentries:\n  - member: |-\n      "+fixture.Member+"\n    class: "+string(fixture.Class)+
		"\n    disposition: excluded\n    reason: \"\"\n")

	_, err := tr.run(residualClassByName(t, fixture.Class))
	if err == nil {
		t.Fatal("an entry carrying no reason was accepted")
	}
	if !strings.Contains(err.Error(), "no reason") {
		t.Errorf("the failure %q does not name the missing reason", err)
	}
}

func TestResidualGateFailsOnAnExclusionThePredicateNoLongerSelects(t *testing.T) {
	t.Parallel()
	for _, fixture := range residualFixtures(t) {
		if fixture.Retire == nil {
			continue
		}
		t.Run(string(fixture.Class), func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			fixture.Write(tr)
			tr.register(fixture.Class, excludedEntry(fixture.Class, fixture.Member))
			fixture.Retire(tr)
			c := residualClassByName(t, fixture.Class)

			before := tr.registerText(fixture.Class)
			rep := tr.runOK(c)
			if len(rep.Failures) != 1 || rep.Failures[0].StaleExcluded == nil {
				t.Fatalf("a dead exclusion was accepted:\n  %s", strings.Join(residualFailureTexts(rep), "\n  "))
			}
			// An exclusion is permanent, so the run reports it rather
			// than dropping it, and it leaves the register standing.
			if got := tr.registerText(fixture.Class); got != before {
				t.Errorf("the run rewrote the register on a failure:\n%s", got)
			}
		})
	}
}

func TestResidualGateRemovesAnInClassEntryWhoseMemberLeftTheClass(t *testing.T) {
	t.Parallel()
	for _, fixture := range residualFixtures(t) {
		if fixture.Retire == nil {
			continue
		}
		t.Run(string(fixture.Class), func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			fixture.Write(tr)
			tr.register(fixture.Class, inClassEntry(fixture.Class, fixture.Member))
			// The route out is the class's own: the citation is
			// rewritten by the pass, the path gains a glob key, and the
			// skip reason gains a category.
			fixture.Retire(tr)
			c := residualClassByName(t, fixture.Class)

			rep := tr.runOK(c)
			if len(rep.Failures) > 0 {
				t.Errorf("the remediation that emptied the class turned the gate red:\n  %s",
					strings.Join(residualFailureTexts(rep), "\n  "))
			}
			if len(rep.Removed) != 1 || rep.Removed[0].Member != fixture.Member {
				t.Fatalf("the run removed %v, want the entry for %q", rep.Removed, fixture.Member)
			}
			if strings.Contains(tr.registerText(fixture.Class), fixture.Member) {
				t.Errorf("the register still carries %q:\n%s", fixture.Member, tr.registerText(fixture.Class))
			}
			// The second run reads the rewritten register and stays
			// green, so the removal is recorded rather than repeated.
			again := tr.runOK(c)
			if len(again.Failures) > 0 || len(again.Removed) > 0 {
				t.Errorf("the run after the removal reported %d failure(s) and %d removal(s)",
					len(again.Failures), len(again.Removed))
			}
		})
	}
}

func TestResidualGateFailsOnAnInClassEntryThatSurvivesItsMember(t *testing.T) {
	t.Parallel()
	for _, fixture := range residualFixtures(t) {
		if fixture.Retire == nil {
			continue
		}
		t.Run(string(fixture.Class), func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			fixture.Write(tr)
			tr.register(fixture.Class, inClassEntry(fixture.Class, fixture.Member))
			fixture.Retire(tr)
			c := residualClassByName(t, fixture.Class)

			// A run with no writer cannot remove the entry, so it
			// reports it: an in-class entry does not outlive the
			// remediation it recorded, and a read-only inspection never
			// certifies a register it could not reconcile.
			rep, err := tr.runReadOnly(c)
			if err != nil {
				t.Fatalf("run the %s residual scan: %v", c.Name, err)
			}
			if len(rep.Failures) != 1 || rep.Failures[0].StaleInClass == nil {
				t.Fatalf("an in-class entry outlived its member unreported:\n  %s",
					strings.Join(residualFailureTexts(rep), "\n  "))
			}
			if !strings.Contains(rep.Failures[0].String(), fixture.Member) {
				t.Errorf("the failure %q does not name the member", rep.Failures[0])
			}
		})
	}
}

func TestResidualGateHoldsAGeneratedArtifactEntryAcrossRuns(t *testing.T) {
	t.Parallel()
	tr := newResidualTree(t)
	tr.file(residualGeneratedMember, residualGeneratedSource)
	tr.register(scope.ClassGeneratedArtifact, inClassEntry(scope.ClassGeneratedArtifact, residualGeneratedMember))
	c := residualClassByName(t, scope.ClassGeneratedArtifact)

	// No member of this class leaves it: a generated file keeps matching
	// the predicate for as long as it exists, so its in-class entry is
	// permanent and the class is not required to empty.
	before := tr.registerText(scope.ClassGeneratedArtifact)
	for run := 1; run <= 3; run++ {
		rep := tr.runOK(c)
		if len(rep.Failures) > 0 || len(rep.Removed) > 0 {
			t.Fatalf("run %d reported %d failure(s) and %d removal(s):\n  %s",
				run, len(rep.Failures), len(rep.Removed), strings.Join(residualFailureTexts(rep), "\n  "))
		}
		if rep.Held != 1 {
			t.Fatalf("run %d held %d entr(ies), want the one the register carries", run, rep.Held)
		}
	}
	if got := tr.registerText(scope.ClassGeneratedArtifact); got != before {
		t.Errorf("the register was rewritten across runs:\n%s", got)
	}
}

func TestResidualGateReportsAProducerOutputWithNoMarker(t *testing.T) {
	t.Parallel()
	tr := newResidualTree(t)
	tr.file(residualGeneratedMember, residualGeneratedSource)
	// A second producer output that declares itself generated is what
	// the marker enumeration covers, so the case cannot pass through a
	// scan that reports every producer output.
	marked := "charts/lenny/crds/lenny.dev_gadgets.yaml"
	tr.file(marked, residualGeneratedMarked)
	c := residualClassByName(t, scope.ClassGeneratedArtifact)

	rep := tr.runOK(c)
	assertResidualNames(t, rep, residualGeneratedMember)
}

func TestResidualGateReadsNoRegisterAsTreeContent(t *testing.T) {
	t.Parallel()
	fixture := residualFixtures(t)[0]
	for name, place := range map[string]func(tr *residualTree){
		"its own residual register": func(tr *residualTree) {
			tr.register(fixture.Class, inClassEntry(fixture.Class, residualCitationMember(tr.t)))
		},
		// Another class's residual register is excluded for the same
		// reason. The two line-citation classes select the same text, so
		// each register holds a copy of the other's members: a class that
		// read its sibling's register would triage every member twice and
		// would keep selecting it after the pass had rewritten every
		// carrier site, leaving the entry that recorded it unremovable.
		"another class's residual register": func(tr *residualTree) {
			tr.register(scope.ClassLineCitationResolution,
				inClassEntry(scope.ClassLineCitationResolution, residualCitationMember(tr.t)))
		},
		"the baseline its gate excludes": func(tr *residualTree) {
			tr.file("tests/registers/line-citations.yaml",
				"kind: line-citation-count-baseline\nversion: 1\nfiles: []\n# "+residualCitationMember(tr.t)+"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			// A carrier with no citation keeps the class's domain
			// selecting files, so the case measures the register rather
			// than an empty scan.
			tr.file(residualCitationCarrier, residualCitationRetired(tr.t))
			place(tr)
			c := residualClassByName(t, fixture.Class)

			rep := tr.runOK(c)
			if len(rep.Failures) > 0 {
				t.Errorf("a member's text inside a register was read as tree content:\n  %s",
					strings.Join(residualFailureTexts(rep), "\n  "))
			}
		})
	}
}

func TestResidualGateReadsNoExcludedFile(t *testing.T) {
	t.Parallel()
	fixture := residualFixtures(t)[0]
	// The last entry is the control. The same sentence in an ordinary
	// carrier is reported, so a case that reports nothing cannot pass
	// through a scan that reads no file at all.
	for _, target := range []struct {
		Path     string
		Excluded bool
	}{
		{Path: "BUILD-GAPS.md", Excluded: true},
		{Path: "TEST-GAPS.md", Excluded: true},
		{Path: "gateway-runtime-comms.md", Excluded: true},
		{Path: "gateway-runtime-comms-remediation.md", Excluded: true},
		{Path: "proposals/0065_a-staged-proposal.md", Excluded: true},
		{Path: "pkg/carrier/testdata/fixture.go", Excluded: true},
		{Path: "docs/record.md"},
	} {
		t.Run(target.Path, func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			tr.file(residualCitationCarrier, residualCitationRetired(tr.t))
			tr.file(target.Path, residualCitationRecord(t))
			c := residualClassByName(t, fixture.Class)

			rep := tr.runOK(c)
			if target.Excluded {
				if len(rep.Failures) > 0 {
					t.Errorf("an occurrence in %s was reported:\n  %s", target.Path,
						strings.Join(residualFailureTexts(rep), "\n  "))
				}
				return
			}
			assertResidualNames(t, rep, residualCitationMember(t))
		})
	}
}

func TestResidualGateStoresAWrappedCitationWithItsContinuationJoin(t *testing.T) {
	t.Parallel()
	tr := newResidualTree(t)
	tr.file(residualCitationCarrier, residualFixtureText(t, "citation-wrapped.go.txt"))
	c := residualClassByName(t, scope.ClassLineCitations)

	rep := tr.runOK(c)
	if len(rep.Failures) != 1 || rep.Failures[0].Unclassified == nil {
		t.Fatalf("a wrapped citation was reported as %v, want the one unclassified member",
			residualFailureTexts(rep))
	}
	member := rep.Failures[0].Unclassified.Member
	if !strings.Contains(member, "\n") {
		t.Fatalf("the member %q carries no line break, so the wrap it was read across is lost", member)
	}
	// The case pins nothing unless the folded spelling is one the
	// citation form reads. The wrap is what keeps the occurrence out of
	// the enumeration, and folding it back is what would make the stored
	// copy a citation of its own.
	folded := residualMemberKey(member)
	if len(citation.FindIn(residualCitationCarrier, folded)) == 0 {
		t.Fatalf("the folded member %q is read by no citation form", folded)
	}

	tr.register(scope.ClassLineCitations, inClassEntry(scope.ClassLineCitations, member))
	body := tr.registerText(scope.ClassLineCitations)
	if !strings.Contains(body, "      §17.2 \"canonical\n      enumeration\" (line 40\n") {
		t.Errorf("the register does not carry the member across two lines:\n%s", body)
	}
	register := residualRegisterPath(scope.ClassLineCitations)
	if read := citation.FindIn(register, body); len(read) > 0 {
		t.Errorf("the stored member stands as the citation %q, which the ratchet counts and the resolver reports under %s",
			read[0].Raw, register)
	}
	// The stored spelling reads back as the same member, so the entry
	// holds the site rather than being reported again beside it.
	again := tr.runOK(c)
	if len(again.Failures) > 0 || again.Held != 1 {
		t.Errorf("the run after the entry landed held %d entr(ies) and reported:\n  %s",
			again.Held, strings.Join(residualFailureTexts(again), "\n  "))
	}
}

func TestResidualGateSelectsEverySourceLanguageOutsideTheCompletenessCheck(t *testing.T) {
	t.Parallel()
	tr := newResidualTree(t)
	// Each of these is a tracked source file in a language the
	// completeness check does not read, under a tree it does not walk,
	// covered by no glob key. A predicate written as a list of source
	// extensions carries the languages the tree happened to hold when it
	// was written and drops every one added later.
	members := []string{
		"deploy/terraform/cloud/aws/main.tf",
		"dist/scaffold/go.Makefile.tmpl",
		"dist/tool.rb",
		"dist/web/app.css",
		"dist/web/index.html",
		"schemas/service.proto",
	}
	for _, target := range members {
		tr.file(target, "a source file\n")
	}
	// Data, configuration, and documentation carry no language a tier is
	// selected from, and the denylist subtracts them.
	for _, target := range []string{
		"docs/guide.md", "deploy/values.yaml", "deploy/values.yml", "compose/config.json",
		"hack/boilerplate.txt", "dist/config.toml", "dist/policy.xml", "dist/plugin.cfg",
		"dist/go.mod", "dist/go.sum", "dist/Dockerfile",
	} {
		tr.file(target, "data\n")
	}
	// The completeness check governs a source file under its own trees,
	// so the residual subtracts it whether or not a glob key covers it.
	tr.file("cmd/tool/main.go", "package main\n")
	tr.file("scripts/build.sh", "echo build\n")
	// A test file is a target of the graph rather than a source of it.
	tr.file("dist/tool_test.go", "package dist\n")
	// A glob key covers the fixture tree's own package.
	tr.file("pkg/carrier/carrier.go", "package carrier\n")
	c := residualClassByName(t, scope.ClassChangeGraphCoverage)

	rep := tr.runOK(c)
	named := residualNames(rep)
	sort.Strings(named)
	if strings.Join(named, "\n") != strings.Join(members, "\n") {
		t.Errorf("the scan reported\n  %s\nwant\n  %s", strings.Join(named, "\n  "), strings.Join(members, "\n  "))
	}
}

func TestResidualGateFailsOnAMalformedOrMissingRegister(t *testing.T) {
	t.Parallel()
	fixture := residualFixtures(t)[0]
	for name, place := range map[string]func(tr *residualTree){
		"a register that is absent":   func(tr *residualTree) { tr.removeRegister(fixture.Class) },
		"a document that is not YAML": func(tr *residualTree) { tr.rawRegister(fixture.Class, "\tnot: [a document\n") },
		"a document of another kind": func(tr *residualTree) {
			tr.rawRegister(fixture.Class, "kind: exception-register\nversion: 1\nentries: []\n")
		},
		"a document of another version": func(tr *residualTree) {
			tr.rawRegister(fixture.Class, "kind: residual-register\nversion: 2\nclass: line-citations\nentries: []\n")
		},
		"a document declaring another class": func(tr *residualTree) {
			tr.rawRegister(fixture.Class, "kind: residual-register\nversion: 1\nclass: anchors\nentries: []\n")
		},
		"a document with no entries block": func(tr *residualTree) {
			tr.rawRegister(fixture.Class, "kind: residual-register\nversion: 1\nclass: line-citations\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newResidualTree(t)
			fixture.Write(tr)
			place(tr)

			if _, err := tr.run(residualClassByName(t, fixture.Class)); err == nil {
				t.Fatalf("%s certified the class", name)
			}
		})
	}
}

func TestResidualGateFailsOnAScanThatSelectedNoFile(t *testing.T) {
	t.Parallel()
	// The tree is not empty and holds one register, which the class
	// cannot read as tree content, so the class's own scan selects
	// nothing. Both a citation class and the skip-reason class are
	// covered, because the exclusion that empties the scan is the
	// per-class register exclusion in either case.
	for _, name := range []scope.Class{scope.ClassLineCitations, scope.ClassSkipReason} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			tr := &residualTree{t: t, root: t.TempDir()}
			tr.register(name)

			_, err := tr.run(residualClassByName(t, name))
			if err == nil {
				t.Fatal("a scan that selected no file certified the class")
			}
			if !strings.Contains(err.Error(), string(name)) {
				t.Errorf("the failure %q does not name the class", err)
			}
		})
	}
}

func TestResidualGateAcceptsAClassWhosePredicateSelectsNoMember(t *testing.T) {
	t.Parallel()
	fixture := residualFixtures(t)[0]
	c := residualClassByName(t, fixture.Class)

	t.Run("an empty register", func(t *testing.T) {
		t.Parallel()
		tr := newResidualTree(t)
		tr.file(residualCitationCarrier, residualCitationRetired(tr.t))

		rep := tr.runOK(c)
		if len(rep.Failures) > 0 || rep.Selected != 0 {
			t.Errorf("an emptied class over %d file(s) selected %d member(s) and reported:\n  %s",
				rep.Files, rep.Selected, strings.Join(residualFailureTexts(rep), "\n  "))
		}
		if rep.Files == 0 {
			t.Error("the scan selected no file, which is the failure the zero-file case covers")
		}
	})

	t.Run("a register still carrying an in-class entry", func(t *testing.T) {
		t.Parallel()
		tr := newResidualTree(t)
		tr.file(residualCitationCarrier, residualCitationRetired(tr.t))
		tr.register(fixture.Class, inClassEntry(fixture.Class, fixture.Member))

		// The register and the tree disagree: the class is empty and the
		// entry records a member the predicate no longer selects. A run
		// that can rewrite the register removes it, and a run that
		// cannot reports it.
		rep, err := tr.runReadOnly(c)
		if err != nil {
			t.Fatalf("run the %s residual scan: %v", c.Name, err)
		}
		if len(rep.Failures) != 1 || rep.Failures[0].StaleInClass == nil {
			t.Fatalf("an emptied class certified a register that still carries an in-class entry:\n  %s",
				strings.Join(residualFailureTexts(rep), "\n  "))
		}
	})
}
