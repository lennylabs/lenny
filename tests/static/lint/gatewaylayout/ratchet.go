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
// returns every row the ratchet refuses. Each base row backs at most one
// current row. A row identical to a base row is backed by it first; a row
// is otherwise accepted only when mapping its paths back through the moves
// (new path to old path) gives a base row that no other current row uses,
// because a move rewrote the row's paths, so a mapped row is refused while
// the row it maps back to remains. A move step adds no row: it routes any
// violation it would create through the seam interface (decision 15 of
// change 0117). The caller passes only the moves that RenameMoves admits.
//
// A package row that gives a package in a directory of the table a part
// other than that directory's is refused besides: a package in transit
// stays outside the layout's directories, and a package of one part does
// not sit in another's. A row present at the merge base is not held to
// that, which keeps the rows of packages that sit today in a directory the
// layout gives to another part.
func Ratchet(base, current Register, moves []Move, table []Dir) []string {
	backing := map[string]int{}
	for _, row := range base.Packages {
		backing[packageKey(row)]++
	}
	for _, row := range base.Imports {
		backing[importKey(row)]++
	}
	exactPkg := make([]bool, len(current.Packages))
	for i, row := range current.Packages {
		if k := packageKey(row); backing[k] > 0 {
			backing[k]--
			exactPkg[i] = true
		}
	}
	exactImp := make([]bool, len(current.Imports))
	for i, row := range current.Imports {
		if k := importKey(row); backing[k] > 0 {
			backing[k]--
			exactImp[i] = true
		}
	}
	var out []string
	for i, row := range current.Packages {
		if exactPkg[i] {
			continue
		}
		where := "package row " + row.Package
		if d, inTable := layoutDir(table, row.Package); inTable && row.Part != d.Part {
			out = append(out, fmt.Sprintf("%s: a package of part %s sits in %s, the directory of part %s; a new row cannot place a package in another part's directory", where, row.Part, d.Path, d.Part))
			continue
		}
		back := row
		back.Package = mapBack(row.Package, moves)
		if k := packageKey(back); back.Package != row.Package && backing[k] > 0 {
			backing[k]--
			continue
		}
		out = append(out, where+": the row is new since the merge base, or maps back to a base row another row already uses; the register only shrinks")
	}
	for i, row := range current.Imports {
		if exactImp[i] {
			continue
		}
		back := row
		back.From, back.To = mapBack(row.From, moves), mapBack(row.To, moves)
		if k := importKey(back); (back.From != row.From || back.To != row.To) && backing[k] > 0 {
			backing[k]--
			continue
		}
		out = append(out, fmt.Sprintf("import row %s -> %s: the row is new since the merge base, or maps back to a base row another row already uses; the register only shrinks", row.From, row.To))
	}
	sort.Strings(out)
	return out
}

// RenameMoves returns the moves a renamed row may map back through: the
// lines the merge-base manifest already holds, whose old path is gone at
// HEAD because the move ran. A line the branch adds to the manifest, or a
// move that has not run, maps no row.
func RenameMoves(moves, baseManifest []Move, existsAtHead func(string) bool) []Move {
	inBase := map[Move]bool{}
	for _, m := range baseManifest {
		inBase[Move{Old: m.Old, New: m.New}] = true
	}
	var out []Move
	for _, m := range moves {
		if inBase[Move{Old: m.Old, New: m.New}] && !existsAtHead(m.Old) {
			out = append(out, m)
		}
	}
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
