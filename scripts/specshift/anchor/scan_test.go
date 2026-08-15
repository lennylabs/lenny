// SPDX-License-Identifier: MIT

package anchor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// These cases hold the scan the residual check over the anchor class
// drives. The check subtracts the references the pass reads from its own
// broad predicate, so what the scan answers decides which references are
// triaged: a scan that reported none would triage every reference of the
// tree, and one that reported a reference the pass does not rewrite
// would leave that reference unregistered and unwritten.
//
// The fixture tree sits under testdata, which is outside every class's
// read domain. A retired anchor or a citation of a retired section
// written in this source would be a member of the class the residual
// gate scans for, standing under this file's own path.

// anchorFixtureTree is the tree the cases list and read. It carries two
// specification documents, a carrier holding one reference of each form
// the pass reads, and a map retiring the anchor both name.
const anchorFixtureTree = "testdata/tree"

// anchorFixtureMap is where that tree holds its anchor-move map, and
// anchorFixtureCarrier is the document carrying the references.
const (
	anchorFixtureMap     = "tests/spec-anchor-moves.json"
	anchorFixtureCarrier = "docs/carrier.md"
)

// anchorFixture reads one fixture body.
func anchorFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", fixture, err)
	}
	return body
}

// anchorTreeLister lists the fixture tree.
func anchorTreeLister() scope.Lister { return scope.DirLister(anchorFixtureTree) }

// anchorTreeReader reads the fixture tree.
func anchorTreeReader() scope.FileReader { return scope.DirReader(anchorFixtureTree) }

// anchorReaderWithMap reads the fixture tree with its anchor-move map
// replaced by the named fixture, so a case drives the scan over a map
// the tree does not carry without a tree of its own for each one.
func anchorReaderWithMap(t *testing.T, fixture string) scope.FileReader {
	t.Helper()
	body := anchorFixture(t, fixture)
	read := anchorTreeReader()
	return func(target string) ([]byte, error) {
		if target == anchorFixtureMap {
			return body, nil
		}
		return read(target)
	}
}

// anchorCarrierText reads the carrier the references stand in.
func anchorCarrierText(t *testing.T) string {
	t.Helper()
	body, err := anchorTreeReader()(anchorFixtureCarrier)
	if err != nil {
		t.Fatalf("read the fixture carrier: %v", err)
	}
	return string(body)
}

// TestNewScanRefusesAMapItCannotRead pins the fail-closed rule the scan
// opens with. A scan driven by a map it could not read carries no
// retirement, so it reports that the pass rewrites nothing and the
// residual check triages every reference of the tree as a member no pass
// reaches. Failing names the map instead.
//
// The two nil dependencies are the same failure reached from the caller:
// a scan with no lister or no reader cannot index the tree the link form
// resolves against, and a scan that carried on would read no link at all
// while reporting a clean class.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// is redirected through the anchor-move map)
func TestNewScanRefusesAMapItCannotRead(t *testing.T) {
	t.Parallel()
	missing := func(target string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: target, Err: fs.ErrNotExist}
	}
	for name, c := range map[string]struct {
		List scope.Lister
		Read scope.FileReader
	}{
		"no lister":                     {List: nil, Read: anchorTreeReader()},
		"no reader":                     {List: anchorTreeLister(), Read: nil},
		"a map the tree does not carry": {List: anchorTreeLister(), Read: missing},
		"a document that is not JSON":   {List: anchorTreeLister(), Read: anchorReaderWithMap(t, "moves-not-json.txt")},
		"a document of another kind":    {List: anchorTreeLister(), Read: anchorReaderWithMap(t, "moves-other-kind.json.txt")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			scan, err := NewScan(context.Background(), c.List, c.Read, anchorFixtureMap)
			if err == nil {
				t.Fatalf("%s was admitted, and the scan reports %d retired anchor(s)", name, len(scan.Anchors()))
			}
		})
	}
}

// TestNewScanAdmitsAMapThatRetiresNothing pins the one map state the
// pass refuses and the scan admits. The map is what records a
// retirement, so a map that retires nothing states a class with no
// member: that is the tree before the reduction the map describes has
// landed, and the tree after the change that empties it. Refusing it
// would leave the class unchecked in both.
//
// The second case holds the index the scan skips in that state. The
// index covers the markdown documents of the class read domain, and a
// tree carrying none fails to build one. A scan that reached the index
// anyway would fail on a tree whose class the map already states is
// empty.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// is redirected through the anchor-move map)
func TestNewScanAdmitsAMapThatRetiresNothing(t *testing.T) {
	t.Parallel()
	t.Run("the class carries no member", func(t *testing.T) {
		t.Parallel()
		scan, err := NewScan(context.Background(), anchorTreeLister(),
			anchorReaderWithMap(t, "moves-empty.json.txt"), anchorFixtureMap)
		if err != nil {
			t.Fatalf("a map retiring nothing was refused: %v", err)
		}
		if !scan.Empty() {
			t.Errorf("the scan reports %d retired anchor(s) out of a map that declares none", len(scan.Anchors()))
		}
		if got := scan.Anchors(); len(got) != 0 {
			t.Errorf("the scan names the retired anchors %v out of a map that declares none", got)
		}
		if refs := scan.References(anchorFixtureCarrier, anchorCarrierText(t)); refs != nil {
			t.Errorf("the scan reports %d reference(s) the pass rewrites while the map retires nothing", len(refs))
		}
	})
	t.Run("no heading of the tree is indexed", func(t *testing.T) {
		t.Parallel()
		// The lister names the map alone, so the class read domain
		// carries no markdown document and the index cannot be built.
		// The scan returning at all is what proves it was skipped.
		list := func(context.Context) ([]string, error) { return []string{anchorFixtureMap}, nil }
		if _, err := NewScan(context.Background(), list,
			anchorReaderWithMap(t, "moves-empty.json.txt"), anchorFixtureMap); err != nil {
			t.Fatalf("the scan indexed a tree it had no reference to read in: %v", err)
		}
	})
}

// TestScanReadsTheReferencesThePassRewrites pins what the residual check
// subtracts. The two forms are the ones the pass reads: a markdown
// fragment link into a retired anchor whose destination is a document of
// this tree, and a bare section citation, outside every link, of a
// section whose anchor the map retires. A subtraction against a wider
// answer would leave a reference the pass rewrites registered as a
// residual, and one against a narrower answer would leave a reference no
// pass reaches unregistered.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// is redirected through the anchor-move map)
func TestScanReadsTheReferencesThePassRewrites(t *testing.T) {
	t.Parallel()
	scan, err := NewScan(context.Background(), anchorTreeLister(), anchorTreeReader(), anchorFixtureMap)
	if err != nil {
		t.Fatalf("scan the fixture tree: %v", err)
	}
	if scan.Empty() {
		t.Fatal("the fixture map retires nothing, so the case pins no reference")
	}
	anchors := scan.Anchors()
	if len(anchors) != 1 {
		t.Fatalf("the scan names %v as retired, want the one anchor the map records", anchors)
	}

	content := anchorCarrierText(t)
	refs := scan.References(anchorFixtureCarrier, content)
	if len(refs) != 2 {
		t.Fatalf("the scan reads %d reference(s) of the carrier, want the link and the bare citation", len(refs))
	}
	if refs[0][1] > refs[1][0] {
		t.Errorf("the reference spans %v and %v overlap or stand out of source order", refs[0], refs[1])
	}
	if link := content[refs[0][0]:refs[0][1]]; !strings.Contains(link, anchors[0]) {
		t.Errorf("the first reference spans %q, which names no retired anchor", link)
	}
	cited := content[refs[1][0]:refs[1][1]]
	number, ok := strings.CutPrefix(cited, "§")
	if !ok {
		t.Fatalf("the second reference spans %q, which is no bare section citation", cited)
	}
	if !scan.RetiresSection(number) {
		t.Errorf("the scan read a citation of section %s, whose anchor the map does not retire", number)
	}
	// A deeper subsection of the same number is a section of its own, and
	// nothing retired it. A membership test that read the prefix would
	// send every citation under a retired section to one successor.
	if deeper := number + ".9"; scan.RetiresSection(deeper) {
		t.Errorf("the scan reports section %s as retired, and the map records no anchor for it", deeper)
	}
}

// TestScanReadsNoReferenceOfADocumentOutsideTheTree pins the population
// the link form is read over. A destination the tree does not carry is a
// reference the pass cannot check, and rewriting one would judge a
// document this repository does not hold.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// is redirected through the anchor-move map)
func TestScanReadsNoReferenceOfADocumentOutsideTheTree(t *testing.T) {
	t.Parallel()
	scan, err := NewScan(context.Background(), anchorTreeLister(), anchorTreeReader(), anchorFixtureMap)
	if err != nil {
		t.Fatalf("scan the fixture tree: %v", err)
	}
	// The carrier's own text is read under a path one directory deeper
	// than the carrier stands at, so its relative destination resolves to
	// a document the tree does not carry and the link is left alone. The
	// bare citation is read there like anywhere else, because the map
	// alone decides that form.
	refs := scan.References("elsewhere/deep/carrier.md", anchorCarrierText(t))
	if len(refs) != 1 {
		t.Fatalf("the scan reads %d reference(s) under a path whose link resolves outside the tree, "+
			"want the bare citation alone", len(refs))
	}
}

// TestLoadMovesRefusesTheEmptyMapTheScanAdmits pins the split between
// the two callers of the map. The pass refuses a map with no redirect,
// because a run that rewrites nothing reports the zero work of a
// completed migration, while the scan admits it for the reason the
// residual check states.
//
// spec: §28.1 (N8, the citation rule: a reference into a retired anchor
// is redirected through the anchor-move map)
func TestLoadMovesRefusesTheEmptyMapTheScanAdmits(t *testing.T) {
	t.Parallel()
	body := anchorFixture(t, "moves-empty.json.txt")

	decoded, err := decodeMoves(anchorFixtureMap, body)
	if err != nil {
		t.Fatalf("the decoder refused a map that declares an empty list of moves: %v", err)
	}
	if len(decoded.byAnchor) != 0 {
		t.Errorf("the decoder read %d redirect(s) out of an empty list", len(decoded.byAnchor))
	}

	path := filepath.Join(t.TempDir(), "spec-anchor-moves.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write the fixture map: %v", err)
	}
	if _, err := loadMoves(path); err == nil {
		t.Error("the loader admitted a map with no redirect, so the pass would report a completed migration")
	} else if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the loader refused the map because it could not read it: %v", err)
	}
}
