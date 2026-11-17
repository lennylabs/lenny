// SPDX-License-Identifier: MIT

package annotation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// Rule names one refusal of the lint.
type Rule string

const (
	// InFunctionBody is an annotation inside a function body.
	InFunctionBody Rule = "annotation inside a function body"
	// OnStructField is an annotation on a struct field.
	OnStructField Rule = "annotation on a struct field"
	// SectionInTestName is a section number in a test name.
	SectionInTestName Rule = "section number in a test name"
	// QuotedSpecText is specification text copied into a comment.
	QuotedSpecText Rule = "specification text in a comment"
	// UnresolvedIdentifier is an identifier citation the registry does
	// not carry.
	UnresolvedIdentifier Rule = "identifier citation that does not resolve"
	// RetiredIdentifier is a citation of an identifier the registry
	// records as retired; the finding names its successor.
	RetiredIdentifier Rule = "citation of a retired identifier"
)

// Finding is one refusal at one line.
type Finding struct {
	Path   string
	Line   int
	Rule   Rule
	Detail string
}

// String renders the finding as `path:line: rule: detail`.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Rule, f.Detail)
}

// ChangedFile is one Go file with the lines a branch adds or changes.
type ChangedFile struct {
	Path string
	Src  []byte
	// Lines holds the 1-based numbers of the added or changed lines.
	Lines map[int]bool
}

// Lint holds the annotation rule on the changed lines of Go files.
//
// A nil Registry skips the identifier check and a nil Spec skips the
// quoted-text check; the caller reports why each was skipped.
type Lint struct {
	Registry *Registry
	Spec     *SpecText
}

// Check lints one changed file and returns its findings in line order.
func (l Lint) Check(f ChangedFile) ([]Finding, error) {
	if len(f.Lines) == 0 {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.Path, f.Src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	var out []Finding
	out = append(out, l.placement(fset, file, f)...)
	if strings.HasSuffix(f.Path, "_test.go") {
		out = append(out, testNames(fset, file, f)...)
	}
	if l.Spec != nil {
		out = append(out, l.quotes(fset, file, f)...)
	}
	if l.Registry != nil {
		out = append(out, l.identifiers(f)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, nil
}

// span is a source range between two positions, exclusive at both ends.
type span struct{ from, to token.Pos }

// placement refuses an annotation inside a function body or inside a
// struct type's field list, which covers a field's doc comment and its
// trailing comment.
func (l Lint) placement(fset *token.FileSet, file *ast.File, f ChangedFile) []Finding {
	var bodies, structs []span
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.FuncDecl:
			if t.Body != nil {
				bodies = append(bodies, span{t.Body.Lbrace, t.Body.Rbrace})
			}
		case *ast.FuncLit:
			bodies = append(bodies, span{t.Body.Lbrace, t.Body.Rbrace})
		case *ast.StructType:
			structs = append(structs, span{t.Fields.Opening, t.Fields.Closing})
		}
		return true
	})
	var out []Finding
	for _, group := range file.Comments {
		for _, c := range group.List {
			line := fset.Position(c.Slash).Line
			if !f.Lines[line] {
				continue
			}
			if _, ok := Payload(CommentText(c.Text)); !ok {
				continue
			}
			switch {
			case within(c.Slash, bodies):
				out = append(out, Finding{
					f.Path, line, InFunctionBody,
					"cite the requirement in the doc comment of the function that makes the decision",
				})
			case within(c.Slash, structs):
				out = append(out, Finding{
					f.Path, line, OnStructField,
					"cite the requirement on the type or the function that makes the decision",
				})
			}
		}
	}
	return out
}

// within reports whether pos lies strictly inside one of the spans.
func within(pos token.Pos, spans []span) bool {
	for _, s := range spans {
		if pos > s.from && pos < s.to {
			return true
		}
	}
	return false
}

// sectionInName matches a section number in a test function's name:
// `_spec_4_7`, `Spec16_5`, a finding number such as `_F_7_3_21`, or any
// two numbers joined by an underscore.
var sectionInName = regexp.MustCompile(`(?i)spec_?\d|\d+_\d+|§`)

// sectionInSubtest matches a section number in a subtest's name. It is
// narrower than the function-name match, because a subtest name is free
// text in which a dotted number is usually a version or a value.
var sectionInSubtest = regexp.MustCompile(`(?i)§\s*\d|\bspec[ _]?\d+(?:[._]\d+)+`)

// testFuncPrefixes are the prefixes `go test` runs.
var testFuncPrefixes = []string{"Test", "Benchmark", "Fuzz", "Example"}

// testNames refuses a section number in the name of a test function or
// of a subtest a changed line declares.
func testNames(fset *token.FileSet, file *ast.File, f ChangedFile) []Finding {
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.FuncDecl:
			line := fset.Position(t.Name.Pos()).Line
			if t.Recv == nil && f.Lines[line] && isTestFunc(t.Name.Name) && sectionInName.MatchString(t.Name.Name) {
				out = append(out, Finding{
					f.Path, line, SectionInTestName,
					fmt.Sprintf("%s: name the behavior and cite the requirement in the doc comment", t.Name.Name),
				})
			}
		case *ast.CallExpr:
			sel, ok := t.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" || len(t.Args) == 0 {
				return true
			}
			lit, ok := t.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			line := fset.Position(lit.Pos()).Line
			name, err := strconv.Unquote(lit.Value)
			if err == nil && f.Lines[line] && sectionInSubtest.MatchString(name) {
				out = append(out, Finding{
					f.Path, line, SectionInTestName,
					fmt.Sprintf("subtest %q: name the behavior", name),
				})
			}
		}
		return true
	})
	return out
}

// isTestFunc reports whether a function name is one `go test` runs.
func isTestFunc(name string) bool {
	for _, p := range testFuncPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// quotes refuses specification text copied into a comment group that a
// changed line touches: a quoted span of QuoteMinWords words or more
// that appears in the specification, or a run of RunMinWords words on
// the changed lines that appears there word for word.
func (l Lint) quotes(fset *token.FileSet, file *ast.File, f ChangedFile) []Finding {
	var out []Finding
	for _, group := range file.Comments {
		lines := groupLines(fset, group)
		first := 0
		var whole, changed []string
		for _, cl := range lines {
			whole = append(whole, cl.Text)
			if f.Lines[cl.Line] {
				if first == 0 {
					first = cl.Line
				}
				changed = append(changed, cl.Text)
			}
		}
		if first == 0 {
			continue
		}
		reported := false
		for _, q := range QuotedSpans(strings.Join(whole, " ")) {
			if l.Spec.ContainsQuote(q) {
				out = append(out, Finding{
					f.Path, first, QuotedSpecText,
					fmt.Sprintf("%q is quoted from the specification; cite the identifier instead", q),
				})
				reported = true
				break
			}
		}
		if reported {
			continue
		}
		if run, ok := l.Spec.CopiedRun(strings.Join(changed, " ")); ok {
			out = append(out, Finding{
				f.Path, first, QuotedSpecText,
				fmt.Sprintf("%q is copied from the specification; cite the identifier instead", run),
			})
		}
	}
	return out
}

// groupLines returns the comment lines of a comment group with their
// line numbers.
func groupLines(fset *token.FileSet, group *ast.CommentGroup) []CommentLine {
	var out []CommentLine
	for _, c := range group.List {
		line := fset.Position(c.Slash).Line
		if strings.HasPrefix(c.Text, "//") {
			out = append(out, CommentLine{Line: line, Text: strings.TrimPrefix(c.Text, "//")})
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(c.Text, "/*"), "*/")
		for i, part := range strings.Split(body, "\n") {
			out = append(out, CommentLine{Line: line + i, Text: part})
		}
	}
	return out
}

// identifiers refuses an identifier citation, in an annotation a changed
// line touches, that the registry does not carry.
func (l Lint) identifiers(f ChangedFile) []Finding {
	var out []Finding
	for _, a := range Annotations(Comments(f.Path, f.Src)) {
		if !touches(a, f.Lines) {
			continue
		}
		for _, id := range Identifiers(a.Payload) {
			if l.Registry.Has(id) {
				continue
			}
			kind, _ := Classify(id)
			if rt, ok := l.Registry.Retired(id); ok {
				out = append(out, Finding{f.Path, a.Line, RetiredIdentifier, retiredDetail(id, kind, rt)})
				continue
			}
			out = append(out, Finding{
				f.Path, a.Line, UnresolvedIdentifier,
				fmt.Sprintf("%s (%s) is not in %s", id, kind, RegistryPath),
			})
		}
	}
	return out
}

// retiredDetail names what replaced a retired identifier: the successor
// to cite instead, or the record of its removal.
func retiredDetail(id string, kind IdentifierKind, rt ref.Retired) string {
	if rt.Successor != "" {
		return fmt.Sprintf("%s (%s) is retired in %s; cite its successor %s", id, kind, rt.File, rt.Successor)
	}
	return fmt.Sprintf("%s (%s) is retired in %s with no successor (%s); remove the citation", id, kind, rt.File, rt.Removed)
}

// touches reports whether a changed line falls inside the annotation.
func touches(a Annotation, lines map[int]bool) bool {
	for n := a.Line; n <= a.EndLine; n++ {
		if lines[n] {
			return true
		}
	}
	return false
}
