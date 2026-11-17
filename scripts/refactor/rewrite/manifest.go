// SPDX-License-Identifier: MIT

package rewrite

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ParseManifest reads the move manifest (scripts/refactor/manifest): one
// move per non-comment, non-blank line, two or three tab-separated fields
// (<old import path>\t<new import path>[\t<step>]). Lines beginning with '#'
// and blank lines are skipped. It returns the moves in longest-old-path-first order, so
// the driver and the audit process a strict-prefix sibling pair (for example
// pkg/gateway/mcptools before pkg/gateway/mcp) with the longer path first; the
// boundary-anchored rewrites are order-independent, but the deterministic order
// keeps the driver's logs stable and re-runnable.
func ParseManifest(r io.Reader) ([]Move, error) {
	var moves []Move
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(raw, "\t")
		// Tolerate trailing-space noise but require exactly two non-empty fields.
		cleaned := make([]string, 0, 2)
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if f != "" {
				cleaned = append(cleaned, f)
			}
		}
		if len(cleaned) != 2 && len(cleaned) != 3 {
			return nil, fmt.Errorf("manifest line %d: expected <old>\\t<new>[\\t<step>], got %q", line, raw)
		}
		m := Move{Old: cleaned[0], New: cleaned[1]}
		if len(cleaned) == 3 {
			m.Step = cleaned[2]
		}
		moves = append(moves, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	sortMovesLongestFirst(moves)
	return moves, nil
}

// sortMovesLongestFirst orders moves by descending old-path length, breaking
// ties lexically. Longest-first guarantees that when one old path is a strict
// prefix of another, the longer is handled first.
func sortMovesLongestFirst(moves []Move) {
	sort.SliceStable(moves, func(i, j int) bool {
		if len(moves[i].Old) != len(moves[j].Old) {
			return len(moves[i].Old) > len(moves[j].Old)
		}
		return moves[i].Old < moves[j].Old
	})
}
