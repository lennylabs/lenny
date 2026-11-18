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

// Ratchet compares the register with its copy at the merge base and
// returns every row the ratchet refuses. A row the branch did not change
// is accepted. A changed or added row is accepted only when mapping its
// paths back through the moves (new path to old path) gives a row of the
// base register, because a move rewrote the row's paths. A move step adds
// no row: it routes any violation it would create through the seam
// interface (decision 15 of change 0117).
//
// A package row that gives a package in a directory of the table a part
// other than that directory's is refused besides: a package in transit
// stays outside the layout's directories, and a package of one part does
// not sit in another's. A row present at the merge base is not held to
// that, which keeps the rows of packages that sit today in a directory the
// layout gives to another part.
func Ratchet(base, current Register, moves []Move, table []Dir) []string {
	baseRows := map[string]bool{}
	for _, row := range base.Packages {
		baseRows[packageKey(row)] = true
	}
	for _, row := range base.Imports {
		baseRows[importKey(row)] = true
	}
	var out []string
	for _, row := range current.Packages {
		if baseRows[packageKey(row)] {
			continue
		}
		where := "package row " + row.Package
		if d, inTable := layoutDir(table, row.Package); inTable && row.Part != d.Part {
			out = append(out, fmt.Sprintf("%s: a package of part %s sits in %s, the directory of part %s; a new row cannot place a package in another part's directory", where, row.Part, d.Path, d.Part))
			continue
		}
		back := row
		back.Package = mapBack(row.Package, moves)
		if baseRows[packageKey(back)] {
			continue
		}
		out = append(out, where+": the row is new since the merge base; the register only shrinks")
	}
	for _, row := range current.Imports {
		if baseRows[importKey(row)] {
			continue
		}
		back := row
		back.From, back.To = mapBack(row.From, moves), mapBack(row.To, moves)
		if baseRows[importKey(back)] {
			continue
		}
		out = append(out, fmt.Sprintf("import row %s -> %s: the row is new since the merge base; the register only shrinks", row.From, row.To))
	}
	sort.Strings(out)
	return out
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
