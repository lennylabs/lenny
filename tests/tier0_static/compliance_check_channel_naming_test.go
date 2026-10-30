// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// complianceFullPath is the file that declares and registers the
// Full-level conformance checks the external-adapter suite runs.
const complianceFullPath = "cmd/lenny-compliance/full.go"

// runtimeOpsCheckFunc is the Go symbol of the check that drives the
// capability handshake on CH-RUNTIMEOPS. The naming law gives a channel
// one identifier on every carrier it appears on, the Go symbol among
// them, and the §28.3 naming table fixes RuntimeOps as that channel's
// canonical go-symbol spelling.
const runtimeOpsCheckFunc = "checkRuntimeOpsHandshake"

// retiredChannelStem is the go-symbol spelling the §28.3 naming table
// retires for CH-RUNTIMEOPS. It is composed rather than written whole so
// this file does not itself reintroduce the spelling it forbids.
var retiredChannelStem = "Life" + "cycle"

// complianceFullDecls returns the names of the top-level functions
// cmd/lenny-compliance/full.go declares, and the registration entries of
// the Full-level battery keyed by case name and valued by the identifier
// each entry calls.
func complianceFullDecls(t *testing.T) (declared map[string]bool, registered map[string]string) {
	t.Helper()

	root := schematest.RepoRoot(t)
	path := filepath.Join(root, filepath.FromSlash(complianceFullPath))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", complianceFullPath, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, body, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", complianceFullPath, err)
	}

	declared = map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		declared[fn.Name.Name] = true
	}

	// The battery is a slice of anonymous structs whose fields are the
	// case name, the spec section, and the check function. Reading the
	// registration rather than the declaration list is what ties a case
	// name to the symbol that serves it.
	registered = map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		arr, ok := lit.Type.(*ast.ArrayType)
		if !ok {
			return true
		}
		if _, ok := arr.Elt.(*ast.StructType); !ok {
			return true
		}
		for _, elt := range lit.Elts {
			entry, ok := elt.(*ast.CompositeLit)
			if !ok || len(entry.Elts) != 3 {
				continue
			}
			name, ok := entry.Elts[0].(*ast.BasicLit)
			if !ok || name.Kind != token.STRING {
				continue
			}
			fn, ok := entry.Elts[2].(*ast.Ident)
			if !ok {
				continue
			}
			unquoted, err := strconv.Unquote(name.Value)
			if err != nil {
				t.Fatalf("unquote case name %s in %s: %v", name.Value, complianceFullPath, err)
			}
			registered[unquoted] = fn.Name
		}
		return true
	})
	return declared, registered
}

// spec: 15.4.6 (Full-level test categories), 28.1 (channel naming law), 28.3 (naming table)
// diagnosis: a Full-level conformance check is registered under a symbol
//
//	that either does not exist or still carries the channel
//	spelling the §28.3 naming table retires. A missing symbol
//	means the registration and the declarations in
//	cmd/lenny-compliance/full.go have drifted. A retired spelling
//	means the check was renamed on some carriers and not on the
//	Go symbol, so a search for the channel no longer returns the
//	check that exercises it.
func TestComplianceFullChecksNamedForTheirChannel(t *testing.T) {
	t.Parallel()

	declared, registered := complianceFullDecls(t)
	if len(registered) == 0 {
		t.Fatalf("no Full-level check registrations found in %s, so this test asserts nothing", complianceFullPath)
	}

	found := false
	for caseName, fn := range registered {
		if !declared[fn] {
			t.Errorf("case %q registers %s, which %s does not declare", caseName, fn, complianceFullPath)
		}
		if strings.Contains(fn, retiredChannelStem) {
			t.Errorf("case %q registers %s, whose name carries the go-symbol spelling the §28.3 naming table retires for CH-RUNTIMEOPS", caseName, fn)
		}
		if fn == runtimeOpsCheckFunc {
			found = true
		}
	}
	if !found {
		t.Errorf("no Full-level case registers %s, so the CH-RUNTIMEOPS capability handshake check is absent or named for something other than its channel", runtimeOpsCheckFunc)
	}
}

// spec: 15.4.6 (Full-level test categories), 28.1 (channel naming law)
// diagnosis: tests/spec-map.json names a check symbol in
//
//	cmd/lenny-compliance/full.go that the file does not declare.
//	A rename that moved the symbol without rewriting the
//	references leaves the spec map pointing at nothing, and the
//	section it indexes is covered by a test the harness cannot
//	resolve.
func TestSpecMapComplianceCheckReferencesResolve(t *testing.T) {
	t.Parallel()

	declared, _ := complianceFullDecls(t)

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read tests/spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse tests/spec-map.json: %v", err)
	}

	prefix := complianceFullPath + "::"
	references := 0
	for id, section := range doc.Sections {
		for _, ref := range section.Tests {
			if !strings.HasPrefix(ref, prefix) {
				continue
			}
			references++
			symbol := strings.TrimPrefix(ref, prefix)
			if !declared[symbol] {
				t.Errorf("section %s names %s, which %s does not declare", id, ref, complianceFullPath)
			}
			if strings.Contains(symbol, retiredChannelStem) {
				t.Errorf("section %s names %s, whose symbol carries the spelling the §28.3 naming table retires for CH-RUNTIMEOPS", id, ref)
			}
		}
	}
	if references == 0 {
		t.Fatalf("tests/spec-map.json names no %s symbol, so this test asserts nothing", complianceFullPath)
	}
}
