// SPDX-License-Identifier: MIT

package gatewaylayout

import (
	"fmt"
	"sort"
	"strings"
)

// Move is one line of the move manifest, by repository-relative paths.
type Move struct {
	Old  string
	New  string
	Step string
}

// RatchetInput is what the ratchets read besides the two registers.
type RatchetInput struct {
	// Moves is every line of the move manifest; a row whose paths a move
	// rewrote maps back through it to its merge-base row.
	Moves []Move
	// Performed is the moves this branch performs: those absent from the
	// merge-base manifest, and those whose old path exists at the merge
	// base and whose new path does not. Only they grant the step exemption.
	Performed []Move
	// Landed is the steps that the change's steps.md records as Landed.
	Landed map[string]bool
}

// Ratchet compares the register with its copy at the merge base and
// returns every row the ratchet refuses. A row the branch did not change
// is accepted. A changed or added row is accepted only when:
//
//   - mapping its paths back through the moves (new path to old path)
//     gives a row of the base register, because a move rewrote the row's
//     paths; or
//   - the step it names is the step of a move this branch performs that
//     lands its package, as a move step that registers a violation of the
//     package it moves.
//
// Either way, a package row that gives a package in a directory of the
// table a part other than that directory's is refused: a package in
// transit stays outside the layout's directories, and a package of one
// part does not sit in another's. A row present at the merge base is not
// held to that, which keeps the rows of packages that sit today in a
// directory the layout gives to another part. Landed reports rows naming a
// landed step separately.
func Ratchet(base, current Register, in RatchetInput, table []Dir) []string {
	baseRows := map[string]bool{}
	for _, row := range base.Packages {
		baseRows[packageKey(row)] = true
	}
	for _, row := range base.Imports {
		baseRows[importKey(row)] = true
	}
	var out []string
	for _, row := range current.Packages {
		key := packageKey(row)
		if baseRows[key] {
			continue
		}
		where := "package row " + row.Package
		if d, inTable := layoutDir(table, row.Package); inTable && row.Part != d.Part {
			out = append(out, fmt.Sprintf("%s: a package of part %s sits in %s, the directory of part %s; a new row cannot place a package in another part's directory", where, row.Part, d.Path, d.Part))
			continue
		}
		back := row
		back.Package = mapBack(row.Package, in.Moves)
		if baseRows[packageKey(back)] || movedAtStep(row.Package, row.Step, in.Performed) {
			continue
		}
		out = append(out, where+": the row is new since the merge base; the register only shrinks, except in the step that moves the package")
	}
	for _, row := range current.Imports {
		if baseRows[importKey(row)] {
			continue
		}
		back := row
		back.From, back.To = mapBack(row.From, in.Moves), mapBack(row.To, in.Moves)
		if baseRows[importKey(back)] || movedAtStep(row.From, row.Step, in.Performed) || movedAtStep(row.To, row.Step, in.Performed) {
			continue
		}
		out = append(out, fmt.Sprintf("import row %s -> %s: the row is new since the merge base; the register only shrinks, except in the step that moves the package", row.From, row.To))
	}
	sort.Strings(out)
	return out
}

// LandedRows returns every row that names a step the change records as
// Landed: a landed step removed its violations, so its rows are gone.
func LandedRows(reg Register, landed map[string]bool) []string {
	var out []string
	for _, row := range reg.Packages {
		if landed[row.Step] {
			out = append(out, fmt.Sprintf("package row %s names step %s, which has landed", row.Package, row.Step))
		}
	}
	for _, row := range reg.Imports {
		if landed[row.Step] {
			out = append(out, fmt.Sprintf("import row %s -> %s names step %s, which has landed", row.From, row.To, row.Step))
		}
	}
	return out
}

// PerformedMoves returns the moves this branch performs, given the
// merge-base manifest and whether a directory exists at the merge base.
// A move absent from the merge-base manifest is this branch's, and so is a
// move whose old path exists at the merge base and whose new path does not.
func PerformedMoves(moves, baseManifest []Move, existsAtBase func(string) bool) []Move {
	inBase := map[string]bool{}
	for _, m := range baseManifest {
		inBase[m.Old+"\x00"+m.New] = true
	}
	var out []Move
	for _, m := range moves {
		if !inBase[m.Old+"\x00"+m.New] || (existsAtBase(m.Old) && !existsAtBase(m.New)) {
			out = append(out, m)
		}
	}
	return out
}

// LandedSteps reads the steps a change's steps.md records as Landed.
func LandedSteps(stepsMD string) map[string]bool {
	landed := map[string]bool{}
	step := ""
	for _, line := range strings.Split(stepsMD, "\n") {
		if rest, ok := strings.CutPrefix(line, "### "); ok {
			step = strings.Fields(rest + " ")[0]
			continue
		}
		if step != "" && strings.TrimSpace(line) == "- status: Landed" {
			landed[step] = true
		}
	}
	return landed
}

func packageKey(r PackageRow) string {
	return strings.Join([]string{"p", r.Package, r.Part, r.Step, r.RemovedBy}, "\x00")
}

func importKey(r ImportRow) string {
	return strings.Join([]string{"i", r.From, r.To, r.Rule, r.Step, r.RemovedBy}, "\x00")
}

// mapBack returns the path a package had before the move whose new path is
// it or holds it, by the longest matching new path, or the path unchanged.
func mapBack(path string, moves []Move) string {
	best := -1
	for i, m := range moves {
		if path == m.New || strings.HasPrefix(path, m.New+"/") {
			if best < 0 || len(m.New) > len(moves[best].New) {
				best = i
			}
		}
	}
	if best < 0 {
		return path
	}
	return moves[best].Old + path[len(moves[best].New):]
}

// movedAtStep reports whether a move at step lands path, or a directory
// that holds it.
func movedAtStep(path, step string, moves []Move) bool {
	if step == "" {
		return false
	}
	for _, m := range moves {
		if m.Step == step && (path == m.New || strings.HasPrefix(path, m.New+"/")) {
			return true
		}
	}
	return false
}
