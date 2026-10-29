// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// treeRecoveryFailedDoc names one documented symbol of §8.10 tree recovery
// whose doc comment states what happens to an unrecovered node. An empty
// symbol names the package doc; a dotted symbol names a struct field.
type treeRecoveryFailedDoc struct {
	file   string
	symbol string
}

var treeRecoveryFailedDocs = []treeRecoveryFailedDoc{
	{"pkg/delegation/recovery/recovery.go", ""},
	{"pkg/delegation/recovery/recovery.go", "OutcomeFailed"},
	{"pkg/gateway/mcpfabric/delegationtree/treerecovery/treerecovery.go", "Summary.Failed"},
	{"pkg/gateway/sessionserver/treerecovery.go", "recoverDelegationTree"},
}

// docOf returns the doc comment text of symbol in f.
func docOf(f *ast.File, symbol string) string {
	if symbol == "" {
		return f.Doc.Text()
	}
	typeName, field, isField := strings.Cut(symbol, ".")
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !isField && d.Name.Name == symbol {
				return d.Doc.Text()
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if text, ok := specDoc(spec, typeName, field, isField); ok {
					return text
				}
			}
		}
	}
	return ""
}

// specDoc returns the doc of a const or var named name, or of the field of
// the struct type named name.
func specDoc(spec ast.Spec, name, field string, isField bool) (string, bool) {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		for _, n := range s.Names {
			if !isField && n.Name == name {
				return s.Doc.Text(), true
			}
		}
	case *ast.TypeSpec:
		st, ok := s.Type.(*ast.StructType)
		if !isField || s.Name.Name != name || !ok {
			return "", false
		}
		for _, fl := range st.Fields.List {
			for _, n := range fl.Names {
				if n.Name == field {
					return fl.Doc.Text(), true
				}
			}
		}
	}
	return "", false
}

// spec: §8.10 (Delegation Tree Recovery)
//
// Tree recovery fails an unrecovered node only while its locked row is still
// in resume_pending, and Summary.Failed counts every node not recovered,
// including a node that already left resume_pending and was not written. The
// doc comment of each symbol that states the outcome of an unrecovered node
// names the resume_pending condition and does not state that every
// unrecovered node is marked terminally failed.
func TestTreeRecoveryFailedDocsNameResumePendingCondition_spec_8_10(t *testing.T) {
	root := repoRoot(t)
	for _, d := range treeRecoveryFailedDocs {
		path := filepath.Join(root, d.file)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", d.file, err)
		}
		doc := strings.Join(strings.Fields(docOf(f, d.symbol)), " ")
		name := d.file + " " + d.symbol
		if doc == "" {
			t.Errorf("%s: no doc comment found", name)
			continue
		}
		if !strings.Contains(doc, "resume_pending") {
			t.Errorf("%s: doc does not name the resume_pending condition on the failed write: %q", name, doc)
		}
		if strings.Contains(doc, "marked terminally failed and the traversal continues") ||
			strings.Contains(doc, "number marked terminally failed") ||
			strings.Contains(doc, "could not be recovered and is marked") ||
			strings.Contains(doc, "brought to a known state (recovered, failed, or still running)") {
			t.Errorf("%s: doc states every unrecovered node is marked terminally failed: %q", name, doc)
		}
	}
}
