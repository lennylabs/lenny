// SPDX-License-Identifier: MIT

// Package name holds the specshift name pass, which removes the bare
// reserved noun phrases the naming law names from prose and writes the
// canonical identifier that denotes the mechanism the prose meant.
//
// The pass is driven per occurrence. Every site is resolved from an
// entry in the sense register, keyed by file and by the occurrence's
// position in that file, and a site the register does not carry aborts
// the run with the tree left byte-identical. Substituting a default at
// an unresolved site is what silently converts an ambiguous sentence
// into a precise false one: the result carries a canonical spelling, so
// the naming lint and the identifier-resolution gate both pass it, and
// no gate reads meaning. The reviewer resolves each site into the
// register before the substitution runs rather than after.
//
// An entry names one or more identifiers out of the whole identifier
// space the specification declares, which covers links, channels, and
// registers rather than channels alone, because several sites denote the
// connection rather than one of the conversations it carries. An
// identifier the specification does not declare fails the run rather
// than being written, so a typo in an entry cannot reach the tree. An
// entry naming more than one identifier carries the replacement text
// each identifier sits in, so a site whose sentence denotes two
// mechanisms is written with both at the positions the entry records
// rather than collapsed onto one of them.
//
// The matcher applies the shared continuation join before either
// spelling, so an occurrence wrapped across two consecutive comment
// lines is one site the register resolves rather than two half-sites
// neither the pass nor the lint reads. It is the one join the citation
// matcher applies, over every carrier, so the two read one wrapped
// population. A match the join folded together is held to one comment
// afterwards: both lines of the fold have to open behind the same
// comment marker of the carrier's own format. A number sign, an
// asterisk, and a pair of hyphens are markup in a markdown document, so
// a heading, a list item, or an emphasis run there stands as the author
// wrote it rather than fusing onto the line above it, while the same
// number sign opens a comment in a YAML document and folds. Two
// populations are outside the matcher. A
// markdown anchor identifier is an addressable link target rather than
// prose, so it needs no entry and is left as it stands; rewriting one
// breaks every inbound link, including the untracked links this
// repository cannot see. That exclusion comes from the scope package, so
// the pass writes against the same statement of it the naming lint
// reads, and it reaches the fragment of a link rather than the whole
// destination: a phrase in the path half is a site the register
// resolves. In a Go file the matcher reads the doc comment position the
// scope package states, which is the group the parser attaches to the
// package clause or to a declaration, together with the string literals
// the pinned-literal register names, because those are the naming law's
// positions there. Every other comment holds internal text the law does
// not govern, among them a header block a build constraint detaches from
// the package clause, a comment between the elements of a package-level
// literal, a comment trailing the code it annotates, and an
// implementation comment inside a function body, and so does every
// string literal the pinned-literal register does not name.
//
// That register covers the string literals under tests/tier11_docs/ that
// pin specification prose, a specification heading slug, or an
// intra-spec markdown link. They are positions of this pass because the
// run that rewrites such a sentence in the specification has to rewrite
// the literal that pins it in the same diff; otherwise tier 11 goes red
// with a reader on the sentence and no pass able to write the literal.
// The register names each literal rather than the pass walking every
// string of the file, so the operator-facing help text a tier-11 carrier
// happens to hold stays outside the pass and the enumeration the sense
// register is keyed by stays defined.
//
// The carriers the matcher reads are the ones the naming law names,
// which the scope package states: the specification, the documentation,
// the schemas, a tracked Go file, and a tracked root-level markdown
// document, wherever in the tree it sits, so a Go file under sdks/ is
// read through its doc comments like any other. A carrier outside that
// list, such as a chart values file or a runtime SDK source written in
// another language, holds text the law does not govern, so a phrase
// there is neither a site nor an abort.
//
// The register is held to the tree as well as driving it. An entry no
// site claims fails the run, because a run that skipped it would exit
// zero having written nothing for a site a reviewer had resolved.
//
// The write domain, which excludes the historical audit records, the
// staged proposals, the root build and queue records, and every file the
// per-file generated-artifact rule selects, comes from the scope package
// rather than from a list here. A generated artifact's route out of the
// population is the regeneration of its source.
//
// This is migration tooling rather than a platform behavior, so it
// carries no spec citation of its own.
package name

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Rewriter is the name pass. Its tree dependencies are injected so a
// test drives it over a fixture tree.
type Rewriter struct {
	list scope.Lister
	read scope.FileReader

	// registerPath and senses are the driving register: the per-site
	// senses, keyed by file and by occurrence within that file.
	registerPath string
	senses       map[string]map[int]Entry

	// pinned holds, per tier-11 reconciliation carrier, the string
	// literals the pinned-literal register resolves as pinning
	// specification prose, a heading slug, or an intra-spec link. It is
	// read out of the tree the pass runs over, alongside the identifier
	// space, because both are properties of that tree.
	pinned map[string]map[int]bool

	// confine is the part of the write domain this run may write, nil
	// when the run covers the whole of it. The claimed-entry checks read
	// the register in the direction the walk does not cover, so they
	// hold an entry to the tree only where this run is the run that
	// writes its file.
	confine *pass.Confinement

	// deferred names the register entries the confinement put outside
	// this run, in register order. They are the complementary run's to
	// check, and the run reports them so an entry no run covers is
	// visible rather than silently unconsumed.
	deferred []string

	// declared is the identifier space the specification declares,
	// indexed on the first file the pass reads rather than at
	// construction, because the pass reads the tree it rewrites. The
	// harness rewrites one file at a time, so the field needs no lock.
	declared map[string]bool
}

// New returns the name pass over the tree the lister and reader cover.
func New(list scope.Lister, read scope.FileReader) *Rewriter {
	return &Rewriter{list: list, read: read}
}

// Pass names the write domain the pass runs in.
func (r *Rewriter) Pass() scope.Pass { return scope.Name }

// Confine states the part of the write domain this run writes, which
// the claimed-entry checks hold their entries to.
func (r *Rewriter) Confine(c *pass.Confinement) { r.confine = c }

// Deferred returns the register entries this run left to the
// complementary one, in register order.
func (r *Rewriter) Deferred() []string {
	return append([]string(nil), r.deferred...)
}

// LoadRegister reads and validates the per-site senses that drive the
// pass. A missing or malformed register fails rather than loading as an
// empty one: a run with no senses would rewrite no file and report the
// zero work of a completed migration.
func (r *Rewriter) LoadRegister(path string) error {
	senses, err := loadSenses(path)
	if err != nil {
		return err
	}
	r.registerPath, r.senses = path, senses
	return nil
}

// Rewrite substitutes the registered identifier at every reserved-phrase
// site one file carries.
func (r *Rewriter) Rewrite(ctx context.Context, target string, content []byte) ([]byte, error) {
	if r.senses == nil {
		return nil, fmt.Errorf("the name pass ran with no register loaded")
	}
	// The whole register is checked against the declared identifier
	// space and against the tree before any file is rewritten, so an
	// entry naming an identifier the specification does not declare, and
	// an entry no site in the tree claims, both fail the run rather than
	// the file that happens to carry a site.
	if err := r.checkRegister(ctx); err != nil {
		return nil, err
	}
	text := string(content)
	sites, err := findSites(target, text, r.pinned[target])
	if err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return content, nil
	}
	edits, err := r.plan(target, sites)
	if err != nil {
		return nil, err
	}
	after := splice(text, edits)
	if err := standing(target, after, r.pinned[target]); err != nil {
		return nil, err
	}
	return []byte(after), nil
}

// plan resolves every site of one file against the register.
//
// Every unresolved site is collected before the plan fails, so one run
// names the whole hand-correction population rather than its first
// member. Nothing is written until the plan succeeds, so reporting them
// all still leaves the tree byte-identical.
func (r *Rewriter) plan(target string, sites []site) ([]edit, error) {
	entries := r.senses[target]
	edits := make([]edit, 0, len(sites))
	var aborts []*pass.Abort
	for i, s := range sites {
		occurrence := i + 1
		entry, ok := entries[occurrence]
		if !ok {
			aborts = append(aborts, &pass.Abort{
				Path: target,
				Line: s.line,
				Reason: fmt.Sprintf("occurrence %d of a reserved noun phrase has no entry in %s, so its sense is unresolved",
					occurrence, r.registerPath),
			})
			continue
		}
		edits = append(edits, edit{start: s.start, end: s.end, text: entry.substitution()})
	}
	if err := pass.Aborted(aborts); err != nil {
		return nil, err
	}
	return edits, nil
}

// checkRegister holds the whole register to the identifier space the
// specification declares and to the sites the tree carries. It runs once
// per run.
//
// An identifier no register in the specification declares is refused
// rather than written, because the substitution reads as canonical to
// every gate downstream: the naming lint sees no reserved phrase, and
// the identifier-resolution gate sees one spelling of a name nothing
// else in the tree carries, so a misspelled entry would land as a
// pointer to a mechanism that does not exist. The check ranges over the
// text the pass writes rather than over the entry's identifier list
// alone, so an identifier carried only by a replacement is held to the
// same declaration.
func (r *Rewriter) checkRegister(ctx context.Context) error {
	if r.declared != nil {
		return nil
	}
	tracked, err := trackedPaths(ctx, r.list)
	if err != nil {
		return err
	}
	// The pinned-literal register is read before any site is enumerated,
	// because the literals it resolves are positions of the enumeration
	// the sense register's occurrence numbers are keyed by.
	if err := r.loadPinned(tracked); err != nil {
		return err
	}
	declared, err := declaredIdentifiers(ctx, r.list, r.read)
	if err != nil {
		return err
	}
	if err := r.checkClaimed(ctx, tracked); err != nil {
		return err
	}
	var undeclared []string
	for _, target := range sortedKeys(r.senses) {
		entries := r.senses[target]
		for _, occurrence := range sortedOccurrences(entries) {
			for _, id := range entries[occurrence].written() {
				if !declared[id] {
					undeclared = append(undeclared, fmt.Sprintf("%s occurrence %d names %s", target, occurrence, id))
				}
			}
		}
	}
	if len(undeclared) > 0 {
		return fmt.Errorf("%s names %d identifier(s) the specification declares nowhere: %s",
			r.registerPath, len(undeclared), strings.Join(undeclared, "; "))
	}
	r.declared = declared
	return nil
}

// checkClaimed reports every register entry no site in the tree claims,
// which is an entry keyed to a file the tree does not carry, to a file
// outside the pass's write domain, or to an occurrence number above the
// count of sites its file carries.
//
// The register drives the pass in one direction and is checked in both.
// An entry the walk never reaches is written nowhere and, without this
// check, the run exits zero having reported the completed migration it
// never performed, which is the failure the loader already refuses at
// file granularity for a register that carries no entry. An off-by-one
// enumeration and a misspelled path are the two ways an entry lands in
// that position.
//
// An entry whose file the run's confinement does not cover is deferred
// rather than checked, because this run neither reaches its site nor
// writes its file, and the complementary run checks it. The filter is
// the confinement rather than the set of files this run planned sites
// for: an entry whose file the confinement covers and whose sites a
// prior run over the same confinement already consumed still fails,
// which is what makes a replayed run loud rather than a silent no-op.
func (r *Rewriter) checkClaimed(ctx context.Context, tracked map[string]bool) error {
	var unclaimed []string
	r.deferred = nil
	for _, target := range sortedKeys(r.senses) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("check the reserved-phrase sense register against the tree: %w", err)
		}
		if !r.confine.Covers(target) {
			for _, occurrence := range sortedOccurrences(r.senses[target]) {
				r.deferred = append(r.deferred, fmt.Sprintf("%s occurrence %d", target, occurrence))
			}
			continue
		}
		reason, err := r.unclaimedReason(target, tracked)
		if err != nil {
			return err
		}
		if reason == "" {
			continue
		}
		for _, occurrence := range sortedOccurrences(r.senses[target]) {
			unclaimed = append(unclaimed, fmt.Sprintf("%s occurrence %d (%s)", target, occurrence, reason))
		}
	}
	if len(unclaimed) > 0 {
		return fmt.Errorf("%s carries %d entr(ies) no site in the tree claims, so the run would report a substitution it never performed: %s",
			r.registerPath, len(unclaimed), strings.Join(unclaimed, "; "))
	}
	return nil
}

// unclaimedReason reports why no site of one file claims its entries, or
// the empty string when every entry of that file has a site.
func (r *Rewriter) unclaimedReason(target string, tracked map[string]bool) (string, error) {
	if !tracked[target] {
		return "the tree carries no such file", nil
	}
	writable, err := scope.Writable(scope.Name, target, r.read)
	if err != nil {
		return "", fmt.Errorf("check %s against the reserved-phrase sense register: %w", target, err)
	}
	if !writable {
		return "the file is outside the pass's write domain", nil
	}
	content, err := r.read(target)
	if err != nil {
		return "", fmt.Errorf("read %s for the reserved-phrase sense register check: %w", target, err)
	}
	sites, err := findSites(target, string(content), r.pinned[target])
	if err != nil {
		return "", err
	}
	entries := r.senses[target]
	for _, occurrence := range sortedOccurrences(entries) {
		if occurrence > len(sites) {
			return fmt.Sprintf("the file carries %d reserved-phrase site(s)", len(sites)), nil
		}
	}
	return "", nil
}

// trackedPaths returns the tracked tree as a set, so the register check
// asks about a path without walking the list per entry.
func trackedPaths(ctx context.Context, list scope.Lister) (map[string]bool, error) {
	if list == nil {
		return nil, fmt.Errorf("check the reserved-phrase sense register against the tree: a lister is required")
	}
	paths, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("check the reserved-phrase sense register against the tree: %w", err)
	}
	tracked := make(map[string]bool, len(paths))
	for _, p := range paths {
		tracked[p] = true
	}
	return tracked, nil
}

// standing reports every reserved-phrase site the rewritten text still
// carries.
//
// The pass substitutes at every site it read, so the rewritten text is
// required to carry none. A site standing in it is one the substitution
// itself composed, out of the identifier written at a site and the
// carrier text beside it. It is reported for hand correction rather than
// written, because the file would otherwise leave the pass with a site
// the naming lint reads and no further pass writes, and a second run
// over it would substitute against an entry keyed for a different
// occurrence.
func standing(target, after string, pinned map[int]bool) error {
	sites, err := findSites(target, after, pinned)
	if err != nil {
		return err
	}
	var aborts []*pass.Abort
	for _, s := range sites {
		aborts = append(aborts, &pass.Abort{
			Path: target,
			Line: s.line,
			Reason: fmt.Sprintf("substituting in this file composes a reserved noun phrase again out of an emitted identifier and the text beside it, as %q, so the site is left for hand correction",
				s.text),
		})
	}
	return pass.Aborted(aborts)
}

// edit is one substitution in a file, given as a byte span of the source
// and the text that replaces it.
type edit struct {
	start int
	end   int
	text  string
}

// splice writes every edit into the text. The edits arrive in source
// order and no two sites overlap, because each covers one match of the
// matcher, so the result is assembled in one forward pass.
func splice(text string, edits []edit) string {
	var out strings.Builder
	at := 0
	for _, e := range edits {
		out.WriteString(text[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.WriteString(text[at:])
	return out.String()
}

// sortedKeys returns the register's file keys in a stable order, so a
// run reports the same failures in the same order.
func sortedKeys(senses map[string]map[int]Entry) []string {
	out := make([]string, 0, len(senses))
	for target := range senses {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

// sortedOccurrences returns one file's occurrence numbers in order.
func sortedOccurrences(entries map[int]Entry) []int {
	out := make([]int, 0, len(entries))
	for occurrence := range entries {
		out = append(out, occurrence)
	}
	sort.Ints(out)
	return out
}
