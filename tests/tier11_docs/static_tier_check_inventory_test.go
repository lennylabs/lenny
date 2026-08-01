// SPDX-License-Identifier: MIT

// Tier-11 documentation check that reconciles the tier-0 check list in
// TESTING.md §12.0 with the check table the harness actually composes in
// cmd/lenny-test. §12.0 is the contract a contributor reads to learn what
// the static tier runs, and nothing else guards it: a check added to or
// deleted from the harness leaves the documented enumeration behind, as
// happened when the proto no-drift check moved from a shell script to a
// Go test and §12.0 kept naming the retired script and its predicate.
//
// cmd/lenny-test is a main package and cannot be imported, so the check
// names are read out of its syntax tree rather than restated here. That
// keeps the assertion tied to the producer instead of to a copy of it.
//
// These cases carry no `// spec:` annotation. The tier-0 check inventory
// is owned by TESTING.md rather than by a numbered section under spec/.
//
// These tests are NOT under a build tag: they read the repository
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// staticTierSectionHeading opens the TESTING.md section that enumerates
// the tier-0 checks. The section runs to the next heading of the same
// level.
const staticTierSectionHeading = "### 12.0 Tier 0 — Static"

// harnessCheckTableFile holds the check table the static tier composes.
const harnessCheckTableFile = "cmd/lenny-test/cmd_run.go"

// backtickSpan matches a single backtick-quoted span. §12.0 writes every
// command and script path in that form, so a check name is documented
// when some span carries it.
var backtickSpan = regexp.MustCompile("`([^`]+)`")

// checkNameQualifier matches the trailing parenthetical a check name may
// carry to point at the rule it enforces, as in
// "scripts/lint-schema.sh (R-01)". The documentation names the command
// itself, so the qualifier is dropped before matching.
var checkNameQualifier = regexp.MustCompile(`\s+\([^)]*\)$`)

// scriptPathRef matches a scripts/ shell path as §12.0 writes it.
var scriptPathRef = regexp.MustCompile(`^scripts/[A-Za-z0-9._-]+\.sh$`)

// staticTierSection returns the body of the TESTING.md §12.0 section.
func staticTierSection(t *testing.T) string {
	t.Helper()
	body := readRepoFile(t, repoRoot(t), "TESTING.md")
	idx := strings.Index(body, staticTierSectionHeading)
	if idx < 0 {
		t.Fatalf("TESTING.md has no %q heading", staticTierSectionHeading)
	}
	rest := body[idx+len(staticTierSectionHeading):]
	if end := strings.Index(rest, "\n### "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// harnessCheckNames returns the name of every entry in the tier-0 check
// table, read from the syntax tree of the file that declares it. An
// entry is a composite literal of the staticCheck element type carrying
// a `name` field.
func harnessCheckNames(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(repoRoot(t), harnessCheckTableFile)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", harnessCheckTableFile, err)
	}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		arr, ok := lit.Type.(*ast.ArrayType)
		if !ok {
			return true
		}
		if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "staticCheck" {
			return true
		}
		for _, elt := range lit.Elts {
			entry, ok := elt.(*ast.CompositeLit)
			if !ok {
				continue
			}
			if name, ok := compositeStringField(entry, "name"); ok {
				names = append(names, name)
			}
		}
		return true
	})
	if len(names) == 0 {
		t.Fatalf("%s declares no []staticCheck entries with a name field; the reconciliation would pass vacuously", harnessCheckTableFile)
	}
	return names
}

// compositeStringField returns the string literal assigned to field in a
// keyed composite literal.
func compositeStringField(lit *ast.CompositeLit, field string) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		val, ok := kv.Value.(*ast.BasicLit)
		if !ok || val.Kind != token.STRING {
			continue
		}
		s, err := strconv.Unquote(val.Value)
		if err != nil {
			continue
		}
		return s, true
	}
	return "", false
}

// undocumentedChecks returns the check names that no backtick span in
// section carries. The function takes the section text so the same
// assertion runs against a fixture.
func undocumentedChecks(section string, names []string) []string {
	var spans []string
	for _, m := range backtickSpan.FindAllStringSubmatch(section, -1) {
		spans = append(spans, m[1])
	}
	var missing []string
	for _, name := range names {
		want := strings.TrimSpace(checkNameQualifier.ReplaceAllString(name, ""))
		found := false
		for _, span := range spans {
			if strings.Contains(span, want) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	return missing
}

// TestStaticTierDocumentsEveryHarnessCheck pins the TESTING.md §12.0
// enumeration to the check table cmd/lenny-test composes. A check the
// harness runs and §12.0 omits leaves a contributor reading the document
// unaware of a gate their change must clear.
//
// diagnosis: a failure means the tier-0 check table gained a check that
// TESTING.md §12.0 does not enumerate, so the documented static-tier
// contract understates what the tier runs.
func TestStaticTierDocumentsEveryHarnessCheck(t *testing.T) {
	section := staticTierSection(t)
	if missing := undocumentedChecks(section, harnessCheckNames(t)); len(missing) > 0 {
		t.Errorf("TESTING.md §12.0 omits harness check(s) %v; every entry in the cmd/lenny-test tier-0 check table must appear in the §12.0 list", missing)
	}
}

// TestStaticTierNamesNoAbsentScript pins the reverse direction for the
// shell checks: §12.0 must not name a script the repository no longer
// carries. A check reimplemented as a Go test, as the proto no-drift
// check was, leaves the retired script's path behind in the document
// otherwise.
//
// diagnosis: a failure means TESTING.md §12.0 names a scripts/ file that
// does not exist, so the documented static tier describes a check the
// harness cannot run.
func TestStaticTierNamesNoAbsentScript(t *testing.T) {
	root := repoRoot(t)
	for _, m := range backtickSpan.FindAllStringSubmatch(staticTierSection(t), -1) {
		ref := strings.Fields(m[1])
		if len(ref) == 0 || !scriptPathRef.MatchString(ref[0]) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, ref[0])); err != nil {
			t.Errorf("TESTING.md §12.0 names %s, which is absent from the repository: %v", ref[0], err)
		}
	}
}

// TestStaticTierReconciliationDetectsAnOmittedCheck pins the detection
// itself. A reconciliation that passes over any section text would keep
// passing after the documentation drops a check, so the same assertion
// runs over a fixture section built from the live check table with one
// entry removed, and must report exactly that entry.
//
// diagnosis: a failure means undocumentedChecks no longer detects a
// missing entry, so the §12.0 reconciliation above would pass against a
// document that has dropped a check.
func TestStaticTierReconciliationDetectsAnOmittedCheck(t *testing.T) {
	names := harnessCheckNames(t)
	for _, dropped := range names {
		t.Run(dropped, func(t *testing.T) {
			var lines []string
			for _, name := range names {
				if name == dropped {
					continue
				}
				lines = append(lines, "- `"+strings.TrimSpace(checkNameQualifier.ReplaceAllString(name, ""))+"`")
			}
			fixture := strings.Join(lines, "\n")
			missing := undocumentedChecks(fixture, names)
			if len(missing) != 1 || missing[0] != dropped {
				t.Fatalf("fixture omitting %q reported missing %v; want exactly [%s]", dropped, missing, dropped)
			}
		})
	}
}
