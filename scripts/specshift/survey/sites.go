// SPDX-License-Identifier: MIT

package survey

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Site kinds.
const (
	// SiteCode is a function, method, type, or declaration outside a test
	// file.
	SiteCode = "code"
	// SiteTest is a declaration in a test file.
	SiteTest = "test"
	// SitePackage is a package doc comment.
	SitePackage = "package"
)

// Site is one annotation that cites an identifier.
type Site struct {
	// Path and Line locate the annotation.
	Path string
	Line int
	// Decl names the declaration whose doc comment carries it: a function
	// `Name`, a method `Type.Name`, a type or a value name, or `package
	// name`. It is empty when the annotation is attached to none.
	Decl string
	// Kind is SiteCode, SiteTest, or SitePackage.
	Kind string
	// Func reports whether Decl is a function or a method.
	Func bool
}

func (s Site) String() string {
	if s.Decl == "" {
		return fmt.Sprintf("%s:%d", s.Path, s.Line)
	}
	return fmt.Sprintf("%s:%d %s", s.Path, s.Line, s.Decl)
}

// generatedExpr is the Go convention for a generated file's header.
var generatedExpr = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// goFiles lists the Go files of the working tree inside the shared read
// domain, which leaves out testdata, the change records, and the staged
// proposals.
func goFiles(ctx context.Context, r repo) ([]string, error) {
	all, err := r.worktreeFiles(ctx, "*.go")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range all {
		if strings.HasSuffix(p, ".go") && scope.Readable(p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// findSites returns, for each wanted identifier, the annotations in the
// working tree's Go files that cite it. Identifiers are compared in lower
// case, because an anchor is the lower-cased identifier.
func findSites(ctx context.Context, r repo, wanted []string) (map[string][]Site, error) {
	out := map[string][]Site{}
	if len(wanted) == 0 {
		return out, nil
	}
	want := map[string]string{} // lower case -> as asked
	needles := make([][]byte, 0, len(wanted))
	for _, w := range wanted {
		want[strings.ToLower(w)] = w
		needles = append(needles, []byte(strings.ToLower(w)))
	}
	files, err := goFiles(ctx, r)
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if !bytes.Contains(src, []byte("spec:")) || generatedExpr.Match(src) || !containsAny(bytes.ToLower(src), needles) {
			continue
		}
		var hits []struct {
			id   string
			line int
		}
		for _, a := range annotation.Annotations(annotation.Comments(p, src)) {
			for _, id := range annotation.Identifiers(a.Payload) {
				if asked, ok := want[strings.ToLower(id)]; ok {
					hits = append(hits, struct {
						id   string
						line int
					}{asked, a.Line})
				}
			}
		}
		if len(hits) == 0 {
			continue
		}
		decls := declarations(p, src)
		for _, h := range hits {
			s := decls.at(h.line)
			s.Path, s.Line = p, h.line
			out[h.id] = append(out[h.id], s)
		}
	}
	return out, nil
}

// containsAny reports whether src contains one of the needles.
func containsAny(src []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(src, n) {
			return true
		}
	}
	return false
}

// declRange is the doc comment of one declaration, by line.
type declRange struct {
	from, to int
	site     Site
}

// declIndex maps a line to the declaration whose doc comment holds it.
type declIndex struct {
	ranges []declRange
	test   bool
}

// declarations indexes the doc comments of a Go file. A file that does not
// parse indexes nothing, and its sites carry no declaration.
func declarations(p string, src []byte) declIndex {
	idx := declIndex{test: strings.HasSuffix(p, "_test.go")}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return idx
	}
	kind := SiteCode
	if idx.test {
		kind = SiteTest
	}
	add := func(doc *ast.CommentGroup, s Site) {
		if doc == nil {
			return
		}
		idx.ranges = append(idx.ranges, declRange{from: fset.Position(doc.Pos()).Line, to: fset.Position(doc.End()).Line, site: s})
	}
	add(f.Doc, Site{Decl: "package " + f.Name.Name, Kind: SitePackage})
	for _, d := range f.Decls {
		switch t := d.(type) {
		case *ast.FuncDecl:
			name := t.Name.Name
			if t.Recv != nil && len(t.Recv.List) > 0 {
				name = receiverName(t.Recv.List[0].Type) + "." + name
			}
			add(t.Doc, Site{Decl: name, Kind: kind, Func: true})
		case *ast.GenDecl:
			names := specNames(t)
			add(t.Doc, Site{Decl: strings.Join(names, ", "), Kind: kind})
			for _, sp := range t.Specs {
				switch s := sp.(type) {
				case *ast.TypeSpec:
					add(s.Doc, Site{Decl: s.Name.Name, Kind: kind})
				case *ast.ValueSpec:
					var vs []string
					for _, n := range s.Names {
						vs = append(vs, n.Name)
					}
					add(s.Doc, Site{Decl: strings.Join(vs, ", "), Kind: kind})
				}
			}
		}
	}
	return idx
}

// specNames returns the names a general declaration declares.
func specNames(d *ast.GenDecl) []string {
	var out []string
	for _, sp := range d.Specs {
		switch s := sp.(type) {
		case *ast.TypeSpec:
			out = append(out, s.Name.Name)
		case *ast.ValueSpec:
			for _, n := range s.Names {
				out = append(out, n.Name)
			}
		}
	}
	return out
}

// receiverName returns the type name of a method receiver.
func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// at returns the declaration whose doc comment holds a line. A line no
// doc comment holds is a site of the file's kind with no declaration.
func (x declIndex) at(line int) Site {
	for _, r := range x.ranges {
		if line >= r.from && line <= r.to {
			return r.site
		}
	}
	if x.test {
		return Site{Kind: SiteTest}
	}
	return Site{Kind: SiteCode}
}

// splitSites separates the code sites, the tests, and the package doc
// comments of one identifier.
func splitSites(sites []Site) (code, tests, packages []Site) {
	for _, s := range sites {
		switch s.Kind {
		case SiteTest:
			tests = append(tests, s)
		case SitePackage:
			packages = append(packages, s)
		default:
			code = append(code, s)
		}
	}
	return code, tests, packages
}

// deciders returns the distinct functions and methods outside test files
// that cite an identifier.
func deciders(sites []Site) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sites {
		if s.Kind != SiteCode || !s.Func {
			continue
		}
		key := s.Path + " " + s.Decl
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// decidersWarning is the number of functions above which the digest lists
// a requirement, so a reviewer checks that its decision is not
// duplicated.
const decidersWarning = 3
