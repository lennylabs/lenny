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
// the site's only tie to the specification fails instead.
//
// The pass fails closed. A straddling range, a path-form citation naming
// a file that does not resolve under spec/, and a stripped
// served-artifact citation whose authoring source keeps no tie are each
// reported for hand correction rather than converted against a guess,
// and the harness leaves the tree byte-identical. A run that retired a
// citation without emitting the anchor that replaces it fails on the
// accounting identity Account states, so a file cannot reach a count of
// zero by having its pointers deleted.
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
		return nil, abortAt(path, found[0], err)
	}
	edits, stripped, err := plan(sections, path, text, found, served)
	if err != nil {
		return nil, err
	}
	after := applyEdits(text, edits)
	// The accounting identity is checked against the text rather than
	// against the plan that produced it, so a conversion that dropped
	// its anchor fails here instead of retiring a pointer.
	if err := Account(text, after, stripped); err != nil {
		return nil, abortAt(path, found[0], err)
	}
	return []byte(after), nil
}

// plan decides, per citation, whether it is stripped or converted, and
// returns the edits together with how many were stripped.
func plan(sections *citation.Resolver, path, text string, found []citation.Citation, served *servedSpans) ([]edit, int, error) {
	edits := make([]edit, 0, len(found))
	stripped := 0
	for _, c := range found {
		if served.covers(c) {
			e, err := stripSite(sections, text, c, served)
			if err != nil {
				return nil, 0, abortAt(path, c, err)
			}
			edits = append(edits, e)
			stripped++
			continue
		}
		anchor, err := anchorFor(sections, c)
		if err != nil {
			return nil, 0, abortAt(path, c, err)
		}
		edits = append(edits, edit{start: c.Offset, end: c.Offset + len(c.Raw), text: anchor})
	}
	return edits, stripped, nil
}

// abortAt reports a site the pass cannot convert, naming the file and
// the line so the operator can hand-correct it. The harness turns the
// abort into a run that leaves the tree byte-identical.
func abortAt(path string, c citation.Citation, cause error) error {
	return &pass.Abort{Path: path, Line: c.Line, Reason: cause.Error()}
}

// checkRegister holds the run to the population the register measured. A
// file carrying a citation the register has no count for, or a count
// that disagrees with what the file carries, aborts: the register is the
// enumeration the migration is proved against, and rewriting a file
// outside it would retire pointers nobody counted.
func (r *Rewriter) checkRegister(path string, found []citation.Citation) error {
	if r.counts == nil {
		return fmt.Errorf("the line pass ran with no register loaded")
	}
	registered, ok := r.counts[path]
	if !ok {
		return abortAt(path, found[0], fmt.Errorf("%s carries no count for this file", r.registerPath))
	}
	if registered != len(found) {
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
func applyEdits(text string, edits []edit) string {
	ordered := append([]edit(nil), edits...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start > ordered[j].start })
	out := text
	for _, e := range ordered {
		out = out[:e.start] + e.text + out[e.end:]
	}
	return out
}
