// SPDX-License-Identifier: MIT

package anchor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The declaration the anchor-move map carries. It is a driving register
// rather than a residual one (tests/registers/README.md): it is keyed by
// retired anchor for the rewrite it drives, and it is emptied by the
// change that runs the pass over the whole domain.
const (
	mapKind    = "spec-anchor-moves"
	mapVersion = 1
)

// Target names one heading: the repo-relative path of the document that
// carries it and the anchor it is addressed by.
type Target struct {
	File   string `json:"file" yaml:"file"`
	Anchor string `json:"anchor" yaml:"anchor"`
}

// String renders the target in the fragment-link spelling, which is the
// spelling a citation of an unnumbered heading is written in.
func (t Target) String() string { return t.File + "#" + t.Anchor }

// Move is one retired anchor's redirect: the anchor a heading was
// addressed by before the reduction, and the heading the material moved
// to. Those two are the whole entry, which is what the change that
// seeds the map writes.
//
// The map decides the link class on its own, because a link names the
// retired anchor. It scopes the bare-citation class, because an anchor a
// renderer derives from a numbered heading opens with that section's
// digits, so the anchors the map retires state which sections the
// reduction retired. What each citation of such a section means is
// answered per occurrence by the sense register, because a reduction
// carves material out of the anchor it moves.
type Move struct {
	Anchor    string `json:"anchor"`
	Successor Target `json:"successor"`
}

// mapDocument is the anchor-move map as it is written.
type mapDocument struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	// Moves is a pointer so a document that declares no moves block is
	// distinguishable from one that declares an empty list. The first is
	// malformed; the second is a map with no redirect left, which the
	// loader still refuses, because a pass driven by it would report the
	// zero work of a completed migration.
	Moves *[]Move `json:"moves"`
}

// moveMap is the loaded map, indexed by the retired anchor, which is
// what a markdown fragment link names, and by the digits of the section
// each retired anchor addressed, which is what a bare §X.Y citation
// names once its dots are removed.
type moveMap struct {
	path     string
	byAnchor map[string]Move
	// digits holds the leading digit run of every anchor the map
	// retires. A section number a citation writes matches an entry when
	// its dots are removed, which is how the anchors the map carries
	// scope the bare-citation class. An anchor declared by an explicit
	// kramdown attribute opens with no digits and contributes none, so a
	// citation of the section it addressed is left to the residual
	// checks rather than to this pass.
	digits map[string]bool
}

// anchorSectionExpr reads the leading digits of an anchor derived from a
// numbered heading, which are the section's number with its dots
// removed.
var anchorSectionExpr = regexp.MustCompile(`^(\d+)(?:-|$)`)

// loadMoves reads and validates the anchor-move map.
//
// A missing, unreadable, or malformed map is an error rather than an
// empty one. A load that degraded to carrying nothing would rewrite no
// site while the run exited zero, which reads as a completed migration
// rather than as the failure it is, and the change that empties the map
// is the change that would destroy the record of what the run should
// have done.
func loadMoves(path string) (*moveMap, error) {
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("read the anchor-move map %s: %w", path, err)
	}
	var doc mapDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the anchor-move map %s: %w", path, err)
	}
	if doc.Kind != mapKind {
		return nil, fmt.Errorf("anchor-move map %s: expected kind %q, got %q", path, mapKind, doc.Kind)
	}
	if doc.Version != mapVersion {
		return nil, fmt.Errorf("anchor-move map %s: expected version %d, got %d", path, mapVersion, doc.Version)
	}
	if doc.Moves == nil {
		return nil, fmt.Errorf("anchor-move map %s: carries no moves block", path)
	}
	if len(*doc.Moves) == 0 {
		return nil, fmt.Errorf("anchor-move map %s: carries no move", path)
	}
	m := &moveMap{path: path, byAnchor: map[string]Move{}, digits: map[string]bool{}}
	for i, move := range *doc.Moves {
		if err := validateMove(path, i, move); err != nil {
			return nil, err
		}
		if _, ok := m.byAnchor[move.Anchor]; ok {
			return nil, fmt.Errorf("anchor-move map %s: anchor %q is declared twice", path, move.Anchor)
		}
		m.byAnchor[move.Anchor] = move
		if run := anchorSectionExpr.FindStringSubmatch(move.Anchor); run != nil {
			m.digits[run[1]] = true
		}
	}
	return m, nil
}

// validateMove reports the first schema defect in one entry. A defect
// fails the load rather than dropping the entry, because a dropped entry
// leaves its retired anchor unrewritten with the run reporting success.
func validateMove(path string, i int, m Move) error {
	where := fmt.Sprintf("anchor-move map %s: move %d", path, i)
	if strings.TrimSpace(m.Anchor) == "" {
		return fmt.Errorf("%s names no retired anchor", where)
	}
	where = fmt.Sprintf("anchor-move map %s: %q", path, m.Anchor)
	if strings.TrimSpace(m.Successor.File) == "" {
		return fmt.Errorf("%s names no successor file", where)
	}
	if strings.TrimSpace(m.Successor.Anchor) == "" {
		return fmt.Errorf("%s names no successor anchor", where)
	}
	return nil
}

// anchor returns the redirect for a retired anchor.
func (m *moveMap) anchor(a string) (Move, bool) {
	move, ok := m.byAnchor[a]
	return move, ok
}

// retires reports whether the map carries a redirect for the anchor,
// which is what puts a fragment link naming it inside the pass. A link
// into an anchor no entry names is left where it stands: the migration
// retires the anchors the map carries, and a link that already resolved
// to nothing before the run is corrected by hand alongside the
// fragment-link gate.
func (m *moveMap) retires(anchor string) bool {
	_, ok := m.byAnchor[anchor]
	return ok
}

// retiresSection reports whether the map retires an anchor of the
// section a citation names, which is what puts a bare §X.Y citation
// inside the pass and hands it to the sense register. The comparison
// removes the dots, because the anchor a renderer derives from a
// numbered heading spells that number as one run of digits.
//
// A number no retired anchor spells is outside the pass, so a citation
// of another document's own numbering, and a citation of a section this
// migration leaves alone, are read as no site at all.
func (m *moveMap) retiresSection(number string) bool {
	return m.digits[strings.ReplaceAll(number, ".", "")]
}

// successors returns every successor the map names, in the order the
// anchors sort, so the check that holds each to an existing heading
// reports the same failure in the same order on every run.
func (m *moveMap) successors() []Move {
	out := make([]Move, 0, len(m.byAnchor))
	for _, a := range sortedKeys(m.byAnchor) {
		out = append(out, m.byAnchor[a])
	}
	return out
}
