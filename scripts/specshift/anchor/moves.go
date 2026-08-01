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
// retired anchor. It scopes the bare-citation class without deciding it:
// the entry states the section the retired anchor addressed, so the map
// states which sections were retired, while a reduction carves material
// out of the anchor it moves, so the per-occurrence sense register
// answers what each citation inside that class means.
type Move struct {
	Anchor string `json:"anchor"`
	// Section is the number of the section the retired anchor addressed,
	// in the dotted spelling a citation writes it in, and it is the
	// empty string for an anchor of an unnumbered heading. It is a
	// pointer so an entry that declares the field empty is
	// distinguishable from one that omits it: the first states that the
	// anchor addressed no numbered section, and the second is a map that
	// has not said, which would drop every bare citation of the section
	// out of the pass with the run reporting the zero work of a
	// completed migration.
	//
	// The field is declared rather than derived from the anchor's
	// spelling. An anchor a renderer derives from a numbered heading
	// opens with the section's digits, but an anchor declared by an
	// explicit kramdown attribute carries none, so a rule that read the
	// spelling would scope the citation class by how the map's author
	// wrote each anchor.
	Section   *string `json:"section"`
	Successor Target  `json:"successor"`
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
// names.
type moveMap struct {
	path     string
	byAnchor map[string]Move
	// sections holds every section number the map's entries declare, in
	// the dotted spelling a citation writes. The map states which
	// sections this migration retired as well as which anchors, and the
	// citation class is scoped by it the way the link class is scoped by
	// the anchor.
	sections map[string]bool
}

// anchorSectionExpr reads the leading digits of an anchor derived from a
// numbered heading, which are the section's number with its dots
// removed. It cross-checks a declared section against the anchor that
// carries it; the section itself is declared rather than read from here,
// because an anchor declared by an explicit kramdown attribute carries
// no such run while still addressing a numbered section.
var anchorSectionExpr = regexp.MustCompile(`^(\d+)(?:-|$)`)

// sectionNumberExpr matches the dotted spelling of a section number,
// which is the spelling a bare §X.Y citation writes.
var sectionNumberExpr = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

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
	m := &moveMap{path: path, byAnchor: map[string]Move{}, sections: map[string]bool{}}
	for i, move := range *doc.Moves {
		if err := validateMove(path, i, move); err != nil {
			return nil, err
		}
		if _, ok := m.byAnchor[move.Anchor]; ok {
			return nil, fmt.Errorf("anchor-move map %s: anchor %q is declared twice", path, move.Anchor)
		}
		m.byAnchor[move.Anchor] = move
		if section := strings.TrimSpace(*move.Section); section != "" {
			m.sections[section] = true
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
	return validateSection(where, m)
}

// validateSection holds the section an entry declares to the anchor that
// carries it.
//
// The section scopes the bare-citation class, so an entry that leaves it
// unstated narrows that class silently: every bare citation of the
// section would fall outside the pass, the run would rewrite the links
// alone and exit zero, and the change that empties the map would destroy
// the record of what the run should have done. An entry therefore states
// the section, with the empty string for an anchor of an unnumbered
// heading, and an anchor a renderer derived from a numbered heading is
// held to a section its digits spell.
func validateSection(where string, m Move) error {
	if m.Section == nil {
		return fmt.Errorf("%s declares no section field, and every entry states the section its retired anchor addressed, with the empty string for an anchor of an unnumbered heading", where)
	}
	section := strings.TrimSpace(*m.Section)
	digits := anchorSectionExpr.FindStringSubmatch(m.Anchor)
	if section == "" {
		if digits != nil {
			return fmt.Errorf("%s opens with the digits of a numbered heading and declares no section, so every bare citation of that section would fall outside the pass", where)
		}
		return nil
	}
	if !sectionNumberExpr.MatchString(section) {
		return fmt.Errorf("%s declares the section %q, and a section is written in the dotted spelling a citation writes it in", where, section)
	}
	if digits != nil && digits[1] != strings.ReplaceAll(section, ".", "") {
		return fmt.Errorf("%s declares the section %s, which the anchor's own digits do not spell", where, section)
	}
	return nil
}

// anchor returns the redirect for a retired anchor.
func (m *moveMap) anchor(a string) (Move, bool) {
	move, ok := m.byAnchor[a]
	return move, ok
}

// retiresSection reports whether the map declares the section a citation
// names as retired, which is what puts a bare §X.Y citation inside the
// class the sense register resolves. A number no entry declares names a
// section this migration did not move, so the pass leaves every citation
// of it as it stands, in the way it leaves a link into an anchor no
// entry names.
func (m *moveMap) retiresSection(number string) bool {
	return m.sections[number]
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
