// SPDX-License-Identifier: MIT

package anchor

import (
	"context"
	"fmt"
	"sort"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Scan answers, for one tree, what the anchor-move map retires and which
// references of a file the pass reads there.
//
// It exists for the residual check over the anchor class, which subtracts
// the references the pass reads from its own broad predicate and triages
// what is left. The subtraction runs against this package rather than
// against a second statement of the two reference forms, because a check
// that re-derived them would report a reference the pass already rewrites,
// or pass over one the pass never reaches.
type Scan struct {
	moves *moveMap
	// tree indexes the anchors the markdown documents of the class read
	// domain declare, which is what decides whether a link destination is a
	// document of this tree. It is nil when the map retires nothing, since
	// no file then carries a reference the pass reads and the index would
	// answer no question.
	tree *headings
}

// NewScan reads the anchor-move map through the given reader and, when the
// map retires anything, indexes the headings of the tree.
//
// A missing or malformed map fails rather than scanning as an empty one: a
// scan driven by a map it could not read would report no reference the pass
// handles and would triage every reference in the tree as a residual. A map
// that retires nothing is admitted, for the reason decodeMoves states.
func NewScan(ctx context.Context, list scope.Lister, read scope.FileReader, path string) (*Scan, error) {
	if list == nil || read == nil {
		return nil, fmt.Errorf("scan the anchor references of the tree: a lister and a reader are required")
	}
	data, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("read the anchor-move map %s: %w", path, err)
	}
	moves, err := decodeMoves(path, data)
	if err != nil {
		return nil, err
	}
	s := &Scan{moves: moves}
	if s.Empty() {
		return s, nil
	}
	tree, err := newHeadings(ctx, list, read)
	if err != nil {
		return nil, err
	}
	s.tree = tree
	return s, nil
}

// Empty reports whether the map retires nothing, which is the state of a
// tree whose reduction has not landed and of one whose anchor pass has run
// to completion.
func (s *Scan) Empty() bool { return len(s.moves.byAnchor) == 0 }

// Anchors returns every retired anchor the map records, sorted, which is
// the text a reference into a retired anchor is written with.
func (s *Scan) Anchors() []string {
	out := make([]string, 0, len(s.moves.byAnchor))
	for a := range s.moves.byAnchor {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// RetiresSection reports whether the map retires the anchor of the section
// the dotted number names, which is what makes a citation of that section a
// reference the reduction invalidated.
func (s *Scan) RetiresSection(number string) bool { return s.moves.retiresSection(number) }

// References returns the byte span of every reference the pass reads in one
// file, in source order, each as a half-open [lo, hi) pair.
//
// The two forms are the ones findSites reads: a markdown fragment link into
// a retired anchor whose destination is a document of this tree, and a bare
// section citation, outside every link, of a section whose anchor the map
// retires. A caller subtracting these spans from a wider predicate is left
// with the references the pass does not reach, which are the members its
// register triages.
func (s *Scan) References(target, text string) [][2]int {
	if s.tree == nil {
		return nil
	}
	found := findSites(target, text, s.tree, s.moves)
	out := make([][2]int, 0, len(found))
	for _, site := range found {
		out = append(out, [2]int{site.start, site.end})
	}
	return out
}
