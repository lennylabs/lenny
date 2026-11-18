// SPDX-License-Identifier: MIT

package annotation

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Site kinds: where an annotation sits.
const (
	// SiteCode is a function, method, type, or declaration outside a test
	// file.
	SiteCode = "code"
	// SiteTest is a declaration in a test file.
	SiteTest = "test"
	// SitePackage is a package doc comment.
	SitePackage = "package"
)

// Site is the place of one annotation.
type Site struct {
	// Path and Line locate the annotation's label line.
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

// Citation is one annotation of the index with what its items cite.
type Citation struct {
	Site
	// Payload is the annotation's text after the label.
	Payload string
	// Identifiers are the identifier-form tokens the items cite, as
	// Identifiers returns them.
	Identifiers []string
	// Sections are the section numbers the items cite, without the `§`
	// sign, as SectionNumbers returns them.
	Sections []string
}

// TestFunc is one top-level test function of a test file, with what the
// annotations in its doc comment cite.
type TestFunc struct {
	// Dir is the package directory, Path the file, and Line the line of
	// the `func` keyword.
	Dir, Path string
	Line      int
	// Name is the function name.
	Name string
	// Identifiers and Sections are the union of the items of every
	// annotation in the function's doc comment, in order and without
	// duplicates.
	Identifiers []string
	Sections    []string
}

// String renders the test as `path::Name`, the form the harness prints.
func (t TestFunc) String() string { return t.Path + "::" + t.Name }

// Index is every annotation of a set of Go files and every top-level test
// function they declare. It is computed on each run from the tree and
// never committed.
type Index struct {
	// Citations holds the annotations in file and line order.
	Citations []Citation
	// Tests holds the top-level test functions in file and line order,
	// annotated or not.
	Tests []TestFunc
	// Unread names each file the index could not read, with the reason:
	// a file listed but absent, a symbolic link, or a file whose folder
	// resolves outside the repository. The index reads every other file.
	Unread []string
}

// generatedLine is the Go convention for a generated file's header line.
var generatedLine = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// IsGenerated reports whether a Go source carries the generated-file
// header. The Go convention places the header before the first
// non-comment, non-blank text of the file, so a header line is read only
// before the package clause: a generator's own source, which quotes the
// header in a string or a comment below its package clause, is not
// generated.
func IsGenerated(src []byte) bool {
	inBlock := false
	for _, raw := range strings.Split(string(src), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case inBlock:
			inBlock = !strings.Contains(line, "*/")
		case generatedLine.MatchString(line):
			return true
		case line == "" || strings.HasPrefix(line, "//"):
		case strings.HasPrefix(line, "/*"):
			inBlock = !strings.Contains(line[2:], "*/")
		default:
			return false
		}
	}
	return false
}

// CheckedReader returns a reader rooted at root that refuses what
// scope.ReadChecked refuses: a symbolic link, and a file whose folder
// resolves outside the repository.
func CheckedReader(root string) scope.FileReader {
	return func(target string) ([]byte, error) { return scope.ReadChecked(root, target) }
}

// GoFiles returns the Go files a lister reports inside the shared read
// domain of scripts/specshift/scope, which leaves out every testdata
// directory, the change records, and the staged proposals.
func GoFiles(ctx context.Context, list scope.Lister) ([]string, error) {
	domain, err := scope.ReadDomain(ctx, list)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range domain {
		if strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
	}
	return out, nil
}

// BuildIndex indexes the Go files of the read domain that list reports,
// reading each through read.
func BuildIndex(ctx context.Context, list scope.Lister, read scope.FileReader) (*Index, error) {
	files, err := GoFiles(ctx, list)
	if err != nil {
		return nil, err
	}
	return IndexFiles(files, read, nil), nil
}

// IndexFiles indexes the named Go files. A generated file is left out,
// because a generator writes its comments. When keep is not nil, a file
// for which it returns false is left out as well, which lets a caller
// that wants one identifier skip the files that cannot cite it.
//
// A file the reader refuses or cannot find is recorded under Unread, and
// the index reads the rest. A file that does not parse still contributes
// its annotations, read by the scanner, with no declaration and no test
// function: the compiler rather than the index reports the fault.
func IndexFiles(files []string, read scope.FileReader, keep func(path string, src []byte) bool) *Index {
	x := &Index{}
	for _, p := range files {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		test := strings.HasSuffix(p, "_test.go")
		src, err := read(p)
		if err != nil {
			x.Unread = append(x.Unread, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		annotated := bytes.Contains(src, []byte(annotationLabel))
		if !annotated && !test {
			continue
		}
		if IsGenerated(src) || (keep != nil && !keep(p, src)) {
			continue
		}
		x.addFile(p, src, annotated)
	}
	return x
}

// addFile indexes one file's annotations and test functions.
func (x *Index) addFile(p string, src []byte, annotated bool) {
	decls, tests := declarations(p, src)
	var cites []Citation
	if annotated {
		for _, a := range Annotations(Comments(p, src)) {
			s := decls.at(a.Line)
			s.Path, s.Line = p, a.Line
			cites = append(cites, Citation{
				Site:        s,
				Payload:     a.Payload,
				Identifiers: Identifiers(a.Payload),
				Sections:    SectionNumbers(a.Payload),
			})
		}
	}
	x.Citations = append(x.Citations, cites...)
	for _, t := range tests {
		for _, c := range cites {
			if c.Line >= t.docFrom && c.Line <= t.docTo {
				t.fn.Identifiers = appendNew(t.fn.Identifiers, c.Identifiers...)
				t.fn.Sections = appendNew(t.fn.Sections, c.Sections...)
			}
		}
		x.Tests = append(x.Tests, t.fn)
	}
}

// appendNew appends the values not already in list.
func appendNew(list []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, have := range list {
			if have == v {
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	return list
}

// Sites returns the annotations that cite an identifier, compared in
// lower case because an anchor is the lower-cased identifier.
func (x *Index) Sites(id string) []Site {
	var out []Site
	for _, c := range x.Citations {
		for _, cited := range c.Identifiers {
			if strings.EqualFold(cited, id) {
				out = append(out, c.Site)
				break
			}
		}
	}
	return out
}

// Test returns the test function of a package directory by name.
func (x *Index) Test(dir, name string) (TestFunc, bool) {
	for _, t := range x.Tests {
		if t.Dir == dir && t.Name == name {
			return t, true
		}
	}
	return TestFunc{}, false
}

// sectionNumberExpr reads the number that opens a section citation.
var sectionNumberExpr = regexp.MustCompile(`^\d+(?:\.\d+)*`)

// SectionNumbers returns the section numbers an annotation payload cites,
// without the `§` sign, in order and without duplicates. Each item that
// cites a section contributes the number after each top-level `§`, or
// its leading number in the bare form; a `§` inside a parenthesised
// heading is part of that heading.
func SectionNumbers(payload string) []string {
	var out []string
	for _, item := range Items(payload) {
		if !IsSectionCitation(item) {
			continue
		}
		if m := sectionNumberExpr.FindString(item); m != "" {
			out = appendNew(out, m)
		}
		depth := 0
		for i, r := range item {
			switch r {
			case '(':
				depth++
			case ')':
				if depth > 0 {
					depth--
				}
			case '§':
				if depth > 0 {
					continue
				}
				rest := strings.TrimLeft(item[i+utf8.RuneLen(r):], " ")
				if m := sectionNumberExpr.FindString(rest); m != "" {
					out = appendNew(out, m)
				}
			}
		}
	}
	return out
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

// testDecl is a top-level test function with the lines of its doc
// comment.
type testDecl struct {
	fn             TestFunc
	docFrom, docTo int
}

// declarations indexes the doc comments of a Go file and lists its
// top-level test functions. A file that does not parse indexes nothing.
func declarations(p string, src []byte) (declIndex, []testDecl) {
	idx := declIndex{test: strings.HasSuffix(p, "_test.go")}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return idx, nil
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
	var tests []testDecl
	for _, d := range f.Decls {
		switch t := d.(type) {
		case *ast.FuncDecl:
			name := t.Name.Name
			if t.Recv != nil && len(t.Recv.List) > 0 {
				name = receiverName(t.Recv.List[0].Type) + "." + name
			} else if idx.test && IsTestFuncName(name) {
				td := testDecl{fn: TestFunc{Dir: path.Dir(p), Path: p, Line: fset.Position(t.Pos()).Line, Name: name}}
				if t.Doc != nil {
					td.docFrom, td.docTo = fset.Position(t.Doc.Pos()).Line, fset.Position(t.Doc.End()).Line
				}
				tests = append(tests, td)
			}
			add(t.Doc, Site{Decl: name, Kind: kind, Func: true})
		case *ast.GenDecl:
			add(t.Doc, Site{Decl: strings.Join(specNames(t), ", "), Kind: kind})
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
	return idx, tests
}

// IsTestFuncName reports whether a top-level function name in a test file
// names a test that `go test` runs: `Test` followed by nothing or by a
// character that is not a lower-case letter, and not `TestMain`.
func IsTestFuncName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || name == "TestMain" {
		return false
	}
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
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
