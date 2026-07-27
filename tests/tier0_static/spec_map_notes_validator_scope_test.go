// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// validatorScopeDenials match the ways a spec-map `notes` sentence tells
// a reader that the `lenny-test validate-maps` orphan sweep skips a
// population of test files. A section whose coverage rests on in-package
// tests uses one of these phrasings to explain why it enumerates those
// tests by hand, so a stale denial tells the reader the enumeration is
// unenforced when the validator does enforce it.
var validatorScopeDenials = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(does not|do not|doesn't|never|cannot|can't|could not|will not|won't)\s+(walk|walks|sweep|sweeps|visit|visits|scan|scans|reach|reaches|catch|catches|see|sees)`),
	regexp.MustCompile(`(?i)(walks|sweeps|visits|scans|covers)\s+only`),
	regexp.MustCompile(`(?i)only\s+(walks|sweeps|visits|scans|covers)`),
	regexp.MustCompile(`(?i)and above only`),
}

// splitNotesSentences splits a notes field into sentences so a claim is
// evaluated against the clause that carries it rather than against the
// whole paragraph. A boundary is a period followed by whitespace and
// then a capital letter, a backtick, or a section sign, which leaves
// file names (`playground_test.go`), section numbers (`§27.3.1`), and
// abbreviations (`BUILD-GAPS.md`) intact.
func splitNotesSentences(notes string) []string {
	sentences := []string{}
	start := 0
	for i := 0; i < len(notes); i++ {
		if notes[i] != '.' {
			continue
		}
		j := i + 1
		if j >= len(notes) || !isNotesSpace(notes[j]) {
			continue
		}
		for j < len(notes) && isNotesSpace(notes[j]) {
			j++
		}
		if j >= len(notes) {
			break
		}
		r, _ := utf8.DecodeRuneInString(notes[j:])
		if !unicode.IsUpper(r) && r != '`' && r != '§' {
			continue
		}
		if s := strings.TrimSpace(notes[start:j]); s != "" {
			sentences = append(sentences, s)
		}
		start = j
		i = j - 1
	}
	if s := strings.TrimSpace(notes[start:]); s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

func isNotesSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// describesValidatorScope reports whether a sentence is talking about
// the reach of the spec-map orphan sweep, either by naming the
// subcommand or by naming the tier boundary the sweep once stopped at.
func describesValidatorScope(sentence string) bool {
	lower := strings.ToLower(sentence)
	return strings.Contains(lower, "validate-maps") ||
		strings.Contains(lower, "tests/tier2_component and above")
}

// validatorSweepsInPackageTests reports whether the test-files-mapped
// check in cmd/lenny-test still composes the in-package half of the
// orphan sweep with the tier-directory half. The behavior itself is
// pinned by the harness's own unit tests; this reads the wiring so the
// prose check below cannot outlive the scope it describes.
func validatorSweepsInPackageTests(t *testing.T, root string) bool {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, filepath.Join(root, "cmd", "lenny-test"), nil, 0)
	if err != nil {
		t.Fatalf("parse cmd/lenny-test: %v", err)
	}
	declFound, sweepFound := false, false
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "validateTestFilesMapped" || fn.Body == nil {
					continue
				}
				declFound = true
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if ident, ok := call.Fun.(*ast.Ident); ok &&
						strings.Contains(strings.ToLower(ident.Name), "inpackage") {
						sweepFound = true
					}
					return true
				})
			}
		}
	}
	if !declFound {
		t.Fatalf("cmd/lenny-test declares no validateTestFilesMapped function; the spec-map orphan sweep moved or was renamed, so re-derive what scope the tests/spec-map.json notes may claim before updating this guard")
	}
	return sweepFound
}

// spec: TESTING.md §5 (spec traceability) — "`tests/spec-map.json` maps
//
//	every spec section to the tests, packages, migrations, and chart
//	templates that encode it", and validation confirms "Every test
//	function with a `// spec:` annotation appears in the spec map under
//	each section it lists".
//
// The `notes` field is the human-readable record of what a section's
// coverage rests on, and several §27 sections use it to explain why they
// enumerate in-package tests by hand. Those explanations assert a scope
// for `lenny-test validate-maps`. The sweep gained its in-package half
// (the annotated test files of every package a section claims, subject
// to the waivers in tests/spec-map-inpackage-pending.txt) after those
// notes were written, so a note still claiming the sweep stops at the
// tier directories tells a reader an enforced enumeration is unenforced,
// and invites the next author to drop the enumeration or to add a
// redundant hand-rolled guard. This check holds the notes to the scope
// the validator has.
//
// diagnosis: A tests/spec-map.json `notes` field says `lenny-test
//
//	validate-maps` skips a class of test files that it now sweeps.
//	Rewrite the sentence named in the failure to describe the current
//	scope: the sweep walks tests/tier2_component and above plus the
//	annotated in-package test files of every package a section claims,
//	and it honors tests/spec-map-inpackage-pending.txt. If the in-package
//	half was deliberately removed from cmd/lenny-test instead, this guard
//	fails first with a message naming that, and the notes become correct
//	again.
func TestSpecMapNotesDescribeTheValidatorScopeItHas(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	if !validatorSweepsInPackageTests(t, root) {
		t.Fatal("cmd/lenny-test validateTestFilesMapped no longer composes the in-package orphan sweep; the spec-map notes that describe the sweep as tier-directory-only are correct again, so this guard needs re-deriving against the validator's current scope")
	}

	notes := readSpecMapNotes(t)
	sections := make([]string, 0, len(notes))
	for id := range notes {
		if strings.TrimSpace(notes[id]) == "" {
			continue
		}
		sections = append(sections, id)
	}
	sort.Strings(sections)

	for _, id := range sections {
		for _, sentence := range splitNotesSentences(notes[id]) {
			if !describesValidatorScope(sentence) {
				continue
			}
			for _, denial := range validatorScopeDenials {
				if match := denial.FindString(sentence); match != "" {
					t.Errorf("spec-map section %s notes claim a narrower validate-maps scope than the validator has (matched %q): %s", id, match, sentence)
					break
				}
			}
		}
	}
}
