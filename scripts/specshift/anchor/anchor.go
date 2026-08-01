// SPDX-License-Identifier: MIT

// Package anchor holds the specshift anchor pass, which rewrites a
// reference into a retired section anchor to the heading the material
// moved to.
//
// Two site classes are rewritten, and each is driven by its own
// register.
//
// An intra-repo markdown fragment link is decided by the anchor-move
// map alone, because the link names the retired anchor and the map
// carries one successor per anchor. Both forms of that link are inside
// the pass, the file-qualified `[...](NN_file.md#anchor)` form and the
// same-page `[...](#anchor)` form, which is the majority form inside a
// specification file. A link is rewritten when the map names its anchor
// and the document it addresses no longer declares that anchor. A link
// into an anchor that document still declares is left as it stands, and
// so is a link into an anchor the map does not name, which covers a link
// that was already broken before this migration and is not this pass's
// to invent a successor for. So is a link whose destination is not a
// tracked markdown document of the tree, which covers an absolute URL
// the fragment-link gate does not read either.
//
// A bare section citation of the §X.Y form, in a comment or in prose
// alike, is decided per occurrence by the sense register. The map cannot
// decide it: a reduction carves material out of the section it moves, so
// a citation of the carved-out material means a heading that stays where
// it is while the map's single successor for that anchor names the
// heading the rest of the material moved to. A token naming a section
// the specification no longer declares is an occurrence the register has
// to answer for, and there are three answers. An entry naming a heading
// redirects the citation there. An entry recording that the occurrence
// cites no specification section, which is what a regulation clause and
// a stale pointer at material this migration never moved are, leaves it
// exactly as it is written. An occurrence with no entry at all aborts
// the run naming the file and the line, with the tree left
// byte-identical, rather than being sent to the map's successor.
// Substituting the successor there would land a canonical-looking
// pointer at a heading that does not define the cited material, and no
// gate over the anchor classes reads meaning: the fragment-link gate
// reads links alone, and the citation resolver and the per-file ratchet
// match the retired line-citation form alone. A recorded occurrence
// whose section the map carries no successor for aborts the same way,
// because the redirect it needs was never written down.
//
// A section number is judged live or retired against the specification's
// own headings. A heading numbered the same way in a testing document or
// a documentation page declares no specification section, so folding it
// in would report a retired section as alive and pass silently over
// every citation of it.
//
// A redirect is held to a heading that exists. Every successor the map
// names and every destination the sense register names is resolved
// against the headings of the tree before any file is written, so an
// entry pointing at a heading nothing declares fails the run rather
// than sending every inbound reference to a page position that does not
// resolve. A missing or malformed map fails the same way, because a run
// with no redirects would rewrite nothing and report the zero work of a
// completed migration, and the change that empties the map is the change
// that would destroy the record.
//
// The walk, the read exclusion, the anchor class's own register
// exclusion, and the per-pass write exclusion all come from the scope
// package rather than from a list here, so the pass writes against the
// same statement of the domain the gates over the anchor classes read.
//
// This is migration tooling rather than a platform behavior, so it
// carries no spec citation of its own.
package anchor

import (
	"context"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Rewriter is the anchor pass. Its tree dependencies are injected so a
// test drives it over a fixture tree.
type Rewriter struct {
	list scope.Lister
	read scope.FileReader

	// moves is the driving register: the redirect for each retired
	// anchor, keyed by anchor for the link form and by section number
	// for the bare citation form.
	moves *moveMap

	// senses are the per-occurrence destinations of the bare citations
	// the map alone cannot decide. They are read out of the tree the
	// pass runs over, as the redirect targets are, because both are
	// properties of that tree.
	senses map[string]map[int]Sense

	// tree indexes the anchors the markdown documents of the tree
	// declare. It is built on the first file the pass reads rather than
	// at construction, because the pass reads the tree it rewrites. The
	// harness rewrites one file at a time, so the fields need no lock.
	tree *headings
}

// New returns the anchor pass over the tree the lister and reader cover.
func New(list scope.Lister, read scope.FileReader) *Rewriter {
	return &Rewriter{list: list, read: read}
}

// Pass names the write domain the pass runs in.
func (r *Rewriter) Pass() scope.Pass { return scope.Anchor }

// LoadRegister reads and validates the anchor-move map that drives the
// pass. A missing or malformed map fails rather than loading as an empty
// one: a run with no redirects would rewrite no file and report the zero
// work of a completed migration.
func (r *Rewriter) LoadRegister(path string) error {
	moves, err := loadMoves(path)
	if err != nil {
		return err
	}
	r.moves = moves
	return nil
}

// Rewrite redirects every reference into a retired anchor one file
// carries.
func (r *Rewriter) Rewrite(ctx context.Context, target string, content []byte) ([]byte, error) {
	if r.moves == nil {
		return nil, fmt.Errorf("the anchor pass ran with no anchor-move map loaded")
	}
	// Every redirect is held to a heading of the tree before any file is
	// rewritten, so an entry whose successor heading does not exist
	// fails the run rather than the file that happens to carry a
	// reference to it.
	if err := r.checkRegisters(ctx); err != nil {
		return nil, err
	}
	text := string(content)
	sites := findSites(target, text, r.moves, r.tree)
	if len(sites) == 0 {
		return content, nil
	}
	edits, err := r.plan(target, sites)
	if err != nil {
		return nil, err
	}
	return []byte(splice(text, edits)), nil
}

// plan resolves every site of one file against the register that decides
// its class.
//
// A link is decided by the map, which named its anchor as retired and
// carries the successor to write in its place.
//
// A citation is decided by the sense register, which is read first
// because it is what says whether the occurrence cites a specification
// section at all. An occurrence the register does not record aborts the
// run, and so does a recorded occurrence whose section the map carries
// no successor for: in the first case what the citation means was never
// resolved, and in the second the redirect it needs was never written
// down. Passing over either would leave a citation of a section that is
// gone while the run exited zero, which reads as the completed migration
// it is not, and the change that empties the map is the change that
// destroys the record of what the run should have done.
//
// Every unresolved site is collected before the plan fails, so one run
// names the whole hand-correction population rather than its first
// member. Nothing is written until the plan succeeds, so reporting them
// all still leaves the tree byte-identical.
//
// Every citation site takes an occurrence number, so the sense
// register's numbering of a file is the position of the citation among
// every citation of a retired section that file carries, whether or not
// the map happens to carry a redirect for it.
func (r *Rewriter) plan(target string, sites []site) ([]edit, error) {
	edits := make([]edit, 0, len(sites))
	var aborts []*pass.Abort
	citations := 0
	for _, s := range sites {
		if s.kind == linkSite {
			move, _ := r.moves.anchor(s.anchor)
			edits = append(edits, edit{start: s.start, end: s.end, text: linkTarget(target, s, move.Successor)})
			continue
		}
		citations++
		// The register is read before the map, because it is the register
		// that says whether the token is a specification citation at all.
		// A section-sign token naming a regulation clause has no successor
		// in any map, so a run that reported the missing map entry first
		// could never be completed.
		sense, ok := r.senses[target][citations]
		if !ok {
			aborts = append(aborts, &pass.Abort{Path: target, Line: s.line, Reason: unresolvedReason(s, citations)})
			continue
		}
		if !sense.rewrites() {
			continue
		}
		if !s.mapped {
			aborts = append(aborts, &pass.Abort{Path: target, Line: s.line, Reason: r.unmappedReason(s)})
			continue
		}
		written, err := r.tree.citationFor(sense.target())
		if err != nil {
			return nil, fmt.Errorf("%s: %s occurrence %d: %w", senseRegisterPath, target, citations, err)
		}
		edits = append(edits, edit{start: s.start, end: s.end, text: written})
	}
	if err := pass.Aborted(aborts); err != nil {
		return nil, err
	}
	return edits, nil
}

// unresolvedReason states why a citation the sense register does not
// record stops the run, naming the occurrence and the register that
// resolves it.
func unresolvedReason(s site, occurrence int) string {
	return fmt.Sprintf("occurrence %d of a citation naming the retired §%s has no entry in %s, so what it means is unresolved",
		occurrence, s.section, senseRegisterPath)
}

// unmappedReason states why a citation the anchor-move map does not
// carry stops the run, naming the section the citation points at and the
// map that records no successor for it.
func (r *Rewriter) unmappedReason(s site) string {
	return fmt.Sprintf("the citation of §%s names a section the specification no longer declares, and %s carries no successor for it",
		s.section, r.moves.path)
}

// checkRegisters indexes the tree's headings, loads the sense register
// out of the tree, and holds every redirect either register names to a
// heading that exists. It runs once per run.
//
// A successor heading nothing declares is refused rather than written,
// because the rewritten reference reads as resolved to every reader: the
// link renders, and the citation carries a canonical anchor. The
// fragment-link gate would catch the link half after the fact, and
// nothing would catch the citation half at all, so the redirect is
// checked before the run writes rather than after.
func (r *Rewriter) checkRegisters(ctx context.Context) error {
	if r.tree != nil {
		return nil
	}
	tree, err := newHeadings(ctx, r.list, r.read)
	if err != nil {
		return err
	}
	senses, err := r.loadSenseRegister()
	if err != nil {
		return err
	}
	var missing []string
	for _, move := range r.moves.successors() {
		if _, ok := tree.lookup(move.Successor); !ok {
			missing = append(missing, fmt.Sprintf("%s: %q redirects to %s", r.moves.path, move.Anchor, move.Successor))
		}
	}
	for _, target := range sortedKeys(senses) {
		for _, occurrence := range sortedOccurrences(senses[target]) {
			sense := senses[target][occurrence]
			if !sense.rewrites() {
				continue
			}
			if _, ok := tree.lookup(sense.target()); !ok {
				missing = append(missing, fmt.Sprintf("%s: %s occurrence %d cites %s",
					senseRegisterPath, target, occurrence, sense.Destination))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d redirect(s) name a heading no document of the tree declares: %s",
			len(missing), strings.Join(missing, "; "))
	}
	r.tree, r.senses = tree, senses
	return nil
}

// loadSenseRegister reads the sense register out of the tree. A missing
// or malformed register fails rather than loading as an empty one, for
// the reason the map does: the run would abort at the first bare
// citation of the tree, reporting a register that has not been seeded
// while one had been.
func (r *Rewriter) loadSenseRegister() (map[string]map[int]Sense, error) {
	data, err := r.read(senseRegisterPath)
	if err != nil {
		return nil, fmt.Errorf("read the anchor sense register %s: %w", senseRegisterPath, err)
	}
	return loadSenses(data)
}
