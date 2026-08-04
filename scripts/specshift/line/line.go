// SPDX-License-Identifier: MIT

// Package line holds the specshift line pass, which retires a citation
// of the line-citation form the citation package reads.
//
// A citation in an ordinary carrier is converted to a single anchor
// citation: the section the citation names, with the qualifier it
// carries, and with no line number and no orphan integer left behind.
// The whole citation is replaced, in every spelling and across a
// continuation join, because a conversion that consumed the head alone
// would leave the remaining members standing as integers the resolver
// does not read and the ratchet does not count.
//
// A citation in a served client artifact is stripped rather than
// converted, because the text of those artifacts is what a client reads
// and a specification anchor is not part of the client contract. The
// artifacts are named in servedArtifacts, and a strip that would delete
// the site's only tie to the specification fails instead. The tie of a
// `desc:` struct tag stands in the doc comment of the field it
// annotates, because the generated schema pairs one description with one
// field. Every other served tie stands anywhere in the authoring source
// the strip leaves behind and is decided against the text the run
// leaves behind.
//
// The pass fails closed. A straddling range, a path-form citation naming
// a file that does not resolve under spec/, a stripped
// served-artifact citation whose authoring source keeps no tie, and a
// conversion whose emitted anchor composes the retired form again with
// the carrier text beside it are each
// reported for hand correction rather than converted against a guess,
// and the harness leaves the tree byte-identical. Every such site of the
// whole walk is reported by one run, so the hand correction works from a
// named population rather than from a plan-and-fix cycle per site. A run
// that retired a citation without emitting the anchor that replaces it
// fails on the accounting identity Account states, so a file cannot
// reach a count of zero by having its pointers deleted.
//
// The write domain, which excludes the historical audit records, the
// staged proposals, and every file the per-file generated-artifact rule
// selects, comes from the scope package rather than from a list here. A
// generated artifact's route to a zero count is the regeneration of its
// source.
//
// This is migration tooling rather than a platform behavior, so it
// carries no spec citation of its own.
package line

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/gate"
	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Rewriter is the line pass. Its tree dependencies are injected so a
// test drives it over a fixture tree.
type Rewriter struct {
	list scope.Lister
	read scope.FileReader

	// registerPath and counts are the driving register: the per-file
	// citation counts the ratchet holds. The pass reads a file only
	// against the count the register carries for it, so a file the
	// enumeration missed aborts the run rather than being rewritten
	// against a count nobody measured.
	registerPath string
	counts       map[string]int

	// confine is the part of the write domain this run may write, nil
	// when the run covers the whole of it. The pass runs no check that
	// reads the register in the direction the walk does not cover, so
	// the confinement narrows nothing here: it is held so the run
	// reports the per-file counts it leaves to the complementary run,
	// which for this pass is the only signal that an entry is
	// uncovered.
	confine *pass.Confinement

	// resolver indexes the sections under spec/. It is built on the
	// first file that carries a citation rather than at construction,
	// because a run over a tree with no citation left needs no index.
	// The harness rewrites one file at a time, so the field needs no
	// lock.
	resolver *citation.Resolver
}

// New returns the line pass over the tree the lister and reader cover.
func New(list scope.Lister, read scope.FileReader) *Rewriter {
	return &Rewriter{list: list, read: read}
}

// Pass names the write domain the pass runs in.
func (r *Rewriter) Pass() scope.Pass { return scope.Line }

// Confine states the part of the write domain this run writes. The pass
// carries no claimed-entry check, so nothing holds a register entry to
// the tree in either direction and the confinement decides which
// per-file counts this run reports as deferred.
func (r *Rewriter) Confine(c *pass.Confinement) { r.confine = c }

// Deferred returns the per-file counts this run left to the
// complementary one, in path order, one entry per file the register
// keys. The register is read from the command line before the walk, so
// the list is derivable at any point of the run.
//
// Nothing else reports these entries. An entry outside the confinement
// is neither consumed nor rejected, and a replayed run over an
// already-rewritten tree finds no citation, plans an empty diff, and
// exits zero, so a count no run covers is visible in this list alone.
func (r *Rewriter) Deferred() []string {
	files := r.DeferredFiles()
	entries := make([]string, 0, len(files))
	for _, target := range files {
		entries = append(entries, fmt.Sprintf("%s %d citation(s)", target, r.counts[target]))
	}
	return entries
}

// DeferredFiles returns the files those counts are keyed to, in path
// order. The register carries one entry per file, so the two lists are
// the same length.
func (r *Rewriter) DeferredFiles() []string {
	return r.confine.Exclude(sortedCounts(r.counts))
}

// sortedCounts returns the files the register keys, in path order.
func sortedCounts(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for target := range counts {
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

// LoadRegister reads and validates the per-file counts that drive the
// pass. A missing or malformed register fails rather than loading as an
// empty one: a run with no counts would rewrite no file and report a
// completed migration.
func (r *Rewriter) LoadRegister(path string) error {
	counts, err := gate.LoadRatchetBaseline(fileReader, path)
	if err != nil {
		return fmt.Errorf("load the line pass register: %w", err)
	}
	if len(counts) == 0 {
		return fmt.Errorf("the line pass register %s carries no per-file count", path)
	}
	r.registerPath, r.counts = path, counts
	return nil
}

// fileReader reads a register named on the command line, which is a
// filesystem path rather than a tracked-tree path: the register is
// outside the read domain, so it is not reachable through the tree
// reader.
func fileReader(path string) ([]byte, error) {
	return os.ReadFile(filepath.FromSlash(path))
}

// Rewrite converts or strips every citation one file carries.
func (r *Rewriter) Rewrite(ctx context.Context, path string, content []byte) ([]byte, error) {
	text := string(content)
	found := citation.Find(text)
	if len(found) == 0 {
		return content, nil
	}
	if err := r.checkRegister(path, found); err != nil {
		return nil, err
	}
	sections, err := r.sections(ctx)
	if err != nil {
		return nil, err
	}
	served, err := servedSites(path, text)
	if err != nil {
		return nil, pass.Aborted([]*pass.Abort{abortAt(path, found[0], err)})
	}
	edits, strips, err := plan(sections, path, text, found, served)
	if err != nil {
		return nil, err
	}
	after, err := applyEdits(text, edits)
	if err != nil {
		return nil, pass.Aborted([]*pass.Abort{abortAt(path, found[0], err)})
	}
	if err := fileTies(path, after, strips); err != nil {
		return nil, err
	}
	if err := reformed(path, after); err != nil {
		return nil, err
	}
	// The accounting identity is checked against the text rather than
	// against the plan that produced it, so a conversion that dropped
	// its anchor fails here instead of retiring a pointer.
	if err := Account(text, after, len(strips)); err != nil {
		return nil, pass.Aborted([]*pass.Abort{abortAt(path, found[0], err)})
	}
	return []byte(after), nil
}

// plan decides, per citation, whether it is stripped or converted, and
// returns the edits together with the strips it planned.
//
// Every site the pass cannot convert is collected before the plan fails,
// so one run names the whole hand-correction population rather than its
// first member. Nothing is written until the plan succeeds, so reporting
// them all still leaves the tree byte-identical.
// A strip widens its span over the punctuation that introduced the
// citation, so the bound each site is planned inside runs from the end of
// the edit planned before it to the start of the citation planned after
// it. Two citations separated by nothing but a separator would otherwise
// widen into one another and yield overlapping spans.
func plan(sections *citation.Resolver, path, text string, found []citation.Citation, served *servedSpans) ([]edit, []strip, error) {
	edits := make([]edit, 0, len(found))
	var strips []strip
	var aborts []*pass.Abort
	planned := 0
	for i, c := range found {
		next := len(text)
		if i+1 < len(found) {
			next = found[i+1].Offset
		}
		if served.covers(c) {
			e, s, err := stripSite(sections, text, c, served, span{lo: planned, hi: next})
			if err != nil {
				aborts = append(aborts, abortAt(path, c, err))
				continue
			}
			edits = append(edits, e)
			strips = append(strips, s)
			planned = e.end
			continue
		}
		anchor, err := anchorFor(sections, c)
		if err != nil {
			aborts = append(aborts, abortAt(path, c, err))
			continue
		}
		edits = append(edits, edit{start: c.Offset, end: c.Offset + len(c.Raw), text: anchor})
		planned = c.Offset + len(c.Raw)
	}
	if len(aborts) > 0 {
		return nil, nil, pass.Aborted(aborts)
	}
	return edits, strips, nil
}

// fileTies holds every strip whose tie is decided against the whole
// authoring source to the tie it has to leave standing in the rewritten
// file, and reports all of the ones that keep none.
func fileTies(path, after string, strips []strip) error {
	var aborts []*pass.Abort
	for _, s := range strips {
		if !s.fileTie {
			continue
		}
		if err := fileTie(after, s); err != nil {
			aborts = append(aborts, abortAt(path, s.site, err))
		}
	}
	return pass.Aborted(aborts)
}

// reformed reports every citation of the retired form the rewritten
// text still carries.
//
// The pass converts or strips every citation it read, so the rewritten
// text is required to carry none. A citation standing in it is one the
// replacement itself composed, out of the anchor the conversion emitted
// and the carrier text beside the citation it replaced. Two spellings
// compose that way: a citation whose bare-word gloss ran to a trailing
// colon, whose anchor then stands directly against that colon and reads
// the integer opening the next comment line as a member; and a citation
// followed by a separator word and a further parenthesized reference,
// whose anchor and qualifier then absorb the words behind the separator
// and read that reference as the keyword and its member.
//
// The site is reported for hand correction rather than written, because
// both halves of the retirement break otherwise. The file holds a
// non-zero count after the pass has run over it, so it never reaches
// the zero the migration exits on; and a second run over the rewritten
// file converts the composed citation, whose span now covers the
// carrier's own prose, so the prose is deleted. The accounting identity
// does not catch it: the composed citation is counted on both sides of
// the identity, so the accounting balances.
func reformed(path, after string) error {
	var aborts []*pass.Abort
	for _, c := range citation.Find(after) {
		aborts = append(aborts, abortAt(path, c, fmt.Errorf(
			"converting this file composes the retired form again out of the emitted anchor and the text beside it, as %q, so the site is left for hand correction",
			c.Text,
		)))
	}
	return pass.Aborted(aborts)
}

// abortAt reports a site the pass cannot convert, naming the file and
// the line so the operator can hand-correct it. The harness turns the
// abort into a run that leaves the tree byte-identical.
func abortAt(path string, c citation.Citation, cause error) *pass.Abort {
	return &pass.Abort{Path: path, Line: c.Line, Reason: cause.Error()}
}

// checkRegister holds the run to the population the register measured. A
// file carrying a citation the register has no count for, and a file
// carrying more citations than the register counts, each abort: the
// register is the enumeration the migration is proved against, and
// rewriting a site outside it would retire a pointer nobody counted.
//
// A count below the registered one is the retirement the register
// absorbs downward, so it is rewritten rather than aborted. Holding the
// pass to an equality would stop it on every file whose citations a hand
// correction retired before the run, which is the state the tree is in
// between a reported straddling range and the re-run that converts the
// rest of the file.
func (r *Rewriter) checkRegister(path string, found []citation.Citation) error {
	if r.counts == nil {
		return fmt.Errorf("the line pass ran with no register loaded")
	}
	registered, ok := r.counts[path]
	if !ok {
		return abortAt(path, found[0], fmt.Errorf("%s carries no count for this file", r.registerPath))
	}
	if len(found) > registered {
		return abortAt(path, found[0], fmt.Errorf("the file carries %d citation(s) where %s carries %d",
			len(found), r.registerPath, registered))
	}
	return nil
}

// sections returns the section index, building it on first use.
func (r *Rewriter) sections(ctx context.Context) (*citation.Resolver, error) {
	if r.resolver != nil {
		return r.resolver, nil
	}
	resolver, err := citation.NewResolver(ctx, r.list, r.read)
	if err != nil {
		return nil, fmt.Errorf("line pass: %w", err)
	}
	r.resolver = resolver
	return resolver, nil
}

// edit is one replacement in a file, given as a byte span and the text
// that replaces it. A strip carries empty text.
type edit struct {
	start int
	end   int
	text  string
}

// applyEdits splices every edit into the text. The edits are applied
// from the end so an earlier edit does not move a later span.
//
// Every span is measured against the original text, so two spans that
// overlap cannot both be spliced: the second splice indexes into a string
// the first already shortened and cuts bytes belonging to neither span,
// which in a UTF-8 carrier cuts a character in half. The overlap is
// invisible to every check downstream, because the citations are gone
// either way and the accounting balances, so it is refused here rather
// than written.
func applyEdits(text string, edits []edit) (string, error) {
	ordered := append([]edit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start > ordered[j].start })
	out := text
	limit := len(text)
	for _, e := range ordered {
		if e.end > limit {
			return "", fmt.Errorf("two rewrites of this file cover the same bytes, at %d-%d and again from %d, so neither can be written",
				e.start, e.end, limit)
		}
		out = out[:e.start] + e.text + out[e.end:]
		limit = e.start
	}
	return out, nil
}
