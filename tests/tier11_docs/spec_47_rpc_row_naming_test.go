// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the §4.7 adapter RPC tables and the Go
// doc comments that map a client method onto a row of those tables.
//
// A client method that speaks one of the §4.7 RPCs documents which row it
// carries, in the form "the §4.7 table names <RPC>". When a row is renamed in
// the spec, the comment keeps naming the retired row and sends a reader to a
// row the table no longer carries. The compiler does not read comments, the
// tier-11 retirement sweeps do not read `pkg/`, and no doc gate reads a Go
// comment against the table, so the stale mapping stands with every tier
// green.
//
// This check reads the row names out of the §4.7 tables and holds every such
// comment under the library and binary roots to them.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (runtime adapter RPC tables).

package tier11_docs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specTableRowNameRE captures the backticked identifier a §4.7 table row
// carries in its first column, which is the RPC that row states.
var specTableRowNameRE = regexp.MustCompile("^\\|\\s*`([A-Za-z][A-Za-z0-9_]*)`\\s*\\|")

// specRowClaimRE captures the RPC name a comment claims the §4.7 table
// carries. The name is read whether it is backticked or bare, because both
// forms are written and both send the reader to the same table.
var specRowClaimRE = regexp.MustCompile("§4\\.7 table names\\s+`?([A-Za-z][A-Za-z0-9_]*)`?")

// specSectionRPCRows returns the RPC names the §4.7 tables carry, across both
// the Gateway → Adapter and the Adapter → Gateway direction.
func specSectionRPCRows(t *testing.T, root string) map[string]bool {
	t.Helper()
	body := specSection(t, filepath.Join(root, "spec", "04_system-components.md"), "### 4.7 ")
	rows := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if m := specTableRowNameRE.FindStringSubmatch(line); m != nil {
			rows[m[1]] = true
		}
	}
	return rows
}

// goCommentClaimRoots are the directories whose Go comments map a method onto
// a §4.7 row: the libraries that speak the RPCs and the binaries that wire
// them. The reader-facing roots the retirement sweeps walk do not include
// either, which is why a renamed row survives in a client's own comment.
var goCommentClaimRoots = []string{"pkg", "cmd", "sdks"}

// specRowClaim is one comment site claiming a §4.7 row name.
type specRowClaim struct {
	file string
	line int
	rpc  string
}

// specRowClaims returns every comment site under the given roots that names a
// §4.7 table row. Two consecutive comment lines are joined before the claim is
// read, so a phrase wrapped across a comment boundary is one site rather than
// none.
func specRowClaims(t *testing.T, root string, roots []string) []specRowClaim {
	t.Helper()
	claims := []specRowClaim{}
	for _, rel := range roots {
		err := filepath.WalkDir(filepath.Join(root, rel), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file, _ := filepath.Rel(root, path)
			lines := strings.Split(string(body), "\n")
			for i, line := range lines {
				if !strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				joined := line
				if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "//") {
					joined = line + " " + strings.TrimPrefix(strings.TrimSpace(lines[i+1]), "//")
				}
				m := specRowClaimRE.FindStringSubmatch(joined)
				if m == nil {
					continue
				}
				// A phrase read once on its own line and again as the tail of
				// the preceding line's join is one site; report it once, at
				// the line the phrase completes on.
				if n := len(claims); n > 0 && claims[n-1].file == file &&
					claims[n-1].line == i && claims[n-1].rpc == m[1] {
					claims[n-1].line = i + 1
					continue
				}
				claims = append(claims, specRowClaim{file: file, line: i + 1, rpc: m[1]})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}
	return claims
}

// spec: 4.7
// diagnosis: a Go comment maps a client method onto a §4.7 RPC row the table
//
//	does not carry. The §4.7 tables are the normative statement of the adapter
//	RPC set, so a comment naming a row that was renamed or removed documents a
//	contract the spec contradicts and points a reader at a table entry that
//	does not exist. Read the row name out of the §4.7 table and name that row,
//	or, when the row is genuinely gone, delete the mapping with the surface it
//	described.
func TestGoCommentsNameOnlyLiveSpec47RPCRows(t *testing.T) {
	root := repoRoot(t)
	rows := specSectionRPCRows(t, root)
	if !rows["Shutdown"] {
		t.Fatalf("the §4.7 tables yielded %d row name(s) and no Shutdown row; the row extraction "+
			"no longer reads the tables, so every stale claim below would pass", len(rows))
	}
	claims := specRowClaims(t, root, goCommentClaimRoots)
	if len(claims) == 0 {
		t.Fatalf("no Go comment names a §4.7 table row; the claim reader no longer sees the " +
			"comments it holds to the tables")
	}
	for _, claim := range claims {
		if !rows[claim.rpc] {
			t.Errorf("%s:%d says the §4.7 table names %s, and the §4.7 tables carry no such RPC row; "+
				"name the row the table states", claim.file, claim.line, claim.rpc)
		}
	}
}
