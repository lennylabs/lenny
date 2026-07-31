// SPDX-License-Identifier: MIT

package line

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// servedKind names where a served client artifact carries the text a
// client reads.
type servedKind int

const (
	// servedDocument is a data artifact whose whole content is served, so
	// every citation it carries sits in text a client reads.
	servedDocument servedKind = iota
	// servedToolSchemas is a Go source whose tool-definition literals
	// become the served MCP tool schemas. Only the text of a tool
	// description or an input schema is served; a comment, an error
	// message, and every other literal of the file are ordinary authoring
	// sites and convert like any other carrier.
	servedToolSchemas
	// servedDescTags is a Go source whose `desc:` struct-tag values are
	// copied verbatim into a generated document an operator reads. Only
	// the text inside a `desc:` value is served; every other literal of
	// the file is an ordinary authoring site and converts.
	servedDescTags
)

// servedArtifacts names the served client artifacts and where each one
// carries served text.
//
// The OpenAPI document is served verbatim to clients and is the source
// the MCP tool inventory is generated from. The MCP tool definitions are
// served as tool schemas. The chart values `desc:` struct tags are
// copied verbatim into the generated chart JSON schema, which is what an
// operator reads. A specification anchor in any of them is a pointer
// into a document the client does not have, so the citation is removed
// rather than converted.
var servedArtifacts = map[string]servedKind{
	"pkg/gateway/externalapi/openapi/openapi.json": servedDocument,
	"pkg/gateway/mcpfabric/mcptools/mcptools.go":   servedToolSchemas,
	"pkg/chart/values/values.go":                   servedDescTags,
}

// ServedArtifacts returns the served client artifacts in a stable order,
// so a caller can report the set the pass strips from.
func ServedArtifacts() []string {
	out := make([]string, 0, len(servedArtifacts))
	for path := range servedArtifacts {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// servedSpans is where one file carries served text. A nil value means
// the file is an ordinary carrier, so every citation in it converts.
type servedSpans struct {
	kind  servedKind
	spans [][2]int
	// src is the parsed Go source of a Go carrier, which is where the
	// tie a strip has to leave standing is read from. It is nil for a
	// data artifact.
	src *goSource
}

// servedSites returns the served spans of a file. A Go served artifact
// the parser cannot read fails rather than being classified, because a
// pass that could not tell a served literal from a comment would convert
// a citation into text a client reads.
func servedSites(path, text string) (*servedSpans, error) {
	kind, ok := servedArtifacts[path]
	if !ok {
		return nil, nil
	}
	if kind == servedDocument {
		return &servedSpans{kind: kind, spans: [][2]int{{0, len(text)}}}, nil
	}
	src, err := parseGo(path, text)
	if err != nil {
		return nil, err
	}
	spans := src.toolSchemaSpans()
	if kind == servedDescTags {
		spans = src.descTagSpans()
	}
	return &servedSpans{kind: kind, spans: spans, src: src}, nil
}

// tieInFile reports where the tie a strip leaves behind is decided. A
// served document and a served tool schema are both decided against the
// authoring source the strip leaves behind, so a section named anywhere
// else in the same file is a surviving tie. A `desc:` struct tag is
// decided against the doc comment of the field it annotates, because the
// generated schema pairs one description with one field and a tie
// standing over another field says nothing about this one.
func (s *servedSpans) tieInFile() bool { return s.kind != servedDescTags }

// covers reports whether the citation sits in served text.
func (s *servedSpans) covers(c citation.Citation) bool {
	if s == nil {
		return false
	}
	for _, span := range s.spans {
		if c.Offset >= span[0] && c.Offset < span[1] {
			return true
		}
	}
	return false
}

// goSource is a parsed Go carrier, which answers where its served text
// sits and which doc comment stands over a given offset.
type goSource struct {
	fset *token.FileSet
	file *ast.File
}

// parseGo parses a Go carrier with its comments attached.
func parseGo(path, src string) (*goSource, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("the served Go artifact does not parse, so a served site cannot be told from an authoring one: %w", err)
	}
	return &goSource{fset: fset, file: file}, nil
}

// offset returns the byte offset of a position.
func (g *goSource) offset(p token.Pos) int { return g.fset.Position(p).Offset }

// span returns the byte span of a node.
func (g *goSource) span(n ast.Node) [2]int { return [2]int{g.offset(n.Pos()), g.offset(n.End())} }

// schemaFields are the tool-definition field names whose value is served
// to a client, which are the tool's description and its input schema.
var schemaFields = map[string]bool{"Description": true, "InputSchema": true}

// schemaVarSuffix ends the name of a variable holding a served tool
// schema, which is how a schema declared away from its tool definition
// is reached.
const schemaVarSuffix = "Schema"

// toolSchemaSpans returns the byte span of every string literal that
// becomes served tool-schema text, which is the value of a tool
// definition's description or input-schema field and the initializer of
// a schema variable. Every other literal of the file, including an error
// message a handler returns, is an ordinary authoring site and converts,
// the same way only a `desc:` value of the chart values source is
// served.
func (g *goSource) toolSchemaSpans() [][2]int {
	var spans [][2]int
	ast.Inspect(g.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && schemaFields[key.Name] {
				spans = append(spans, g.stringLiteralSpansIn(node.Value)...)
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if i < len(node.Values) && strings.HasSuffix(name.Name, schemaVarSuffix) {
					spans = append(spans, g.stringLiteralSpansIn(node.Values[i])...)
				}
			}
		}
		return true
	})
	return spans
}

// stringLiteralSpansIn returns the byte span of every string literal
// inside the node.
func (g *goSource) stringLiteralSpansIn(n ast.Node) [][2]int {
	var spans [][2]int
	ast.Inspect(n, func(node ast.Node) bool {
		if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			spans = append(spans, g.span(lit))
		}
		return true
	})
	return spans
}

// descTagKey opens the struct-tag value the chart schema generator
// copies verbatim into the generated document.
const descTagKey = `desc:"`

// descTagSpans returns the byte span of the value of every `desc:`
// struct tag in the source. The key is read out of the tag literal as it
// is written, which is the raw-quoted form the convention uses, so an
// offset inside the literal is an offset in the file. A tag written as
// an interpreted string escapes its inner quotes and so carries no
// `desc:"` run; it is left an ordinary carrier and its citation
// converts.
func (g *goSource) descTagSpans() [][2]int {
	var spans [][2]int
	ast.Inspect(g.file, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		lit := field.Tag.Value
		key := strings.Index(lit, descTagKey)
		if key < 0 {
			return true
		}
		open := key + len(descTagKey)
		width := strings.IndexByte(lit[open:], '"')
		if width < 0 {
			return true
		}
		start := g.offset(field.Tag.Pos())
		spans = append(spans, [2]int{start + open, start + open + width})
		return true
	})
	return spans
}

// docRun returns the doc comment standing over the declaration the
// offset sits in, empty when that declaration carries none.
//
// The tie a strip leaves behind is read from the enclosing declaration
// rather than from the source line above the citation, because a served
// string literal spans several lines and the declaration's doc comment
// stands above the declaration rather than above the literal. The walk
// stops at the innermost declaration containing the offset, so a comment
// on an enclosing type is not read as the tie of a field whose only
// carrier the strip empties.
func (g *goSource) docRun(offset int) string {
	for _, decl := range g.file.Decls {
		if !within(g.span(decl), offset) {
			continue
		}
		switch d := decl.(type) {
		case *ast.FuncDecl:
			return commentText(d.Doc)
		case *ast.GenDecl:
			return g.genDeclDoc(d, offset)
		}
	}
	return ""
}

// genDeclDoc returns the doc comment of the innermost part of a
// declaration containing the offset. A struct field owns its own doc
// comment, a spec of a parenthesized group owns its own, and a
// single-spec declaration carries its doc comment on the declaration.
func (g *goSource) genDeclDoc(d *ast.GenDecl, offset int) string {
	for _, spec := range d.Specs {
		if !within(g.span(spec), offset) {
			continue
		}
		if field := g.fieldContaining(spec, offset); field != nil {
			return commentText(field.Doc)
		}
		if doc := specDoc(spec); doc != nil {
			return commentText(doc)
		}
		if d.Lparen.IsValid() {
			return ""
		}
	}
	return commentText(d.Doc)
}

// fieldContaining returns the innermost struct field of the node whose
// span contains the offset.
func (g *goSource) fieldContaining(n ast.Node, offset int) *ast.Field {
	var found *ast.Field
	ast.Inspect(n, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok || !within(g.span(field), offset) {
			return true
		}
		found = field
		return true
	})
	return found
}

// specDoc returns the doc comment a declaration spec carries.
func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		return s.Doc
	case *ast.TypeSpec:
		return s.Doc
	}
	return nil
}

// commentText returns the text of a comment group, empty when there is
// none.
func commentText(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	return doc.Text()
}

// within reports whether the offset falls inside the span.
func within(span [2]int, offset int) bool { return offset >= span[0] && offset < span[1] }

// strip is one removed served citation together with the tie the run has
// to leave standing in its place. A strip emits no anchor, so the
// section it named has to stay named somewhere: a removal that leaves no
// tie reads to both the ratchet and the resolver as a retirement.
type strip struct {
	site   citation.Citation
	number string
	// fileTie records that the tie stands anywhere in the authoring
	// source the strip leaves behind rather than in the doc comment of
	// one declaration, which is how a served document and a served tool
	// schema hold one. Whether it stands there is decided against the
	// text the run leaves behind, so the caller decides it.
	fileTie bool
}

// stripSite returns the edit that removes a served citation, together
// with the tie the strip has to leave standing, and fails when the site
// keeps no tie. The tie of a `desc:` struct tag is decided here against
// the doc comment of the field the tag annotates. Every other served tie
// is decided by the caller against the rewritten file.
//
// What the strip removes is the citation's reference-and-members run
// rather than the whole citation. A conversion replaces the whole
// citation because the anchor it writes says what the citation said. A
// strip writes nothing in its place, and the served text is the client
// contract, so the gloss written against the last member has to stay: a
// gloss is the description's own prose in every served dialect, and
// removing it with the pointer empties or truncates the description a
// client reads. The span is held inside bounds so it cannot reach the
// citation written beside it.
func stripSite(sections *citation.Resolver, text string, c citation.Citation, served *servedSpans, within span) (edit, strip, error) {
	number, err := anchorNumber(sections, c)
	if err != nil {
		return edit{}, strip{}, err
	}
	record := strip{site: c, number: number, fileTie: served.tieInFile()}
	if !record.fileTie && !anchorOf(number).MatchString(served.src.docRun(c.Offset)) {
		return edit{}, strip{}, fmt.Errorf(
			"stripping the served citation would delete the site's only tie, because the doc comment over its declaration names no §%s", number,
		)
	}
	start, end := stripSpan(text, c.Offset, c.MembersEnd)
	return within.clamp(start, end), record, nil
}

// span is the bound a widened strip is held inside, which runs from the
// end of the edit planned before it to the start of the citation planned
// after it.
type span struct {
	lo int
	hi int
}

// clamp holds a widened strip inside the bound, so two strips of one run
// never overlap and no strip reaches the citation beside it. The widening
// crosses blanks and a separator, and two citations separated by nothing
// but a separator each widen over the blanks between them, which yields
// two spans computed against the same original text that overlap. Splicing
// overlapping spans cuts bytes that belong to neither, so the bound is
// applied where the span is computed and the splice checks the result.
func (s span) clamp(start, end int) edit {
	return edit{start: max(start, s.lo), end: min(max(end, s.lo), s.hi)}
}

// fileTie reports whether the section a stripped citation named is still
// named in the authoring source the run leaves behind.
func fileTie(after string, s strip) error {
	if anchorOf(s.number).MatchString(after) {
		return nil
	}
	return fmt.Errorf(
		"stripping the served citation would delete the file's only tie, because nothing else in it names §%s", s.number,
	)
}

// anchorOf returns the expression matching a reference to one section,
// bounded so §17.9.1 does not match §17.9.10.
func anchorOf(number string) *regexp.Regexp {
	return regexp.MustCompile(`§` + regexp.QuoteMeta(number) + `(?:[^0-9.]|\.[^0-9]|$)`)
}

// stripSpan widens the span a strip removes over the punctuation that
// existed only to introduce the citation, which is the `spec:` label
// standing in front of it, a bracket pair wrapping it, and a dash or
// colon separating it from the text it introduced when the citation
// opened the value. Leaving that punctuation behind is the residue that
// makes a served string read as a fragment.
func stripSpan(text string, start, end int) (int, int) {
	left := trimLeft(text, start)
	if cut, ok := cutLabel(text, left); ok {
		left = trimLeft(text, cut)
	}
	right := trimRight(text, end)
	if l, r, ok := cutBrackets(text, left, right); ok {
		return collapse(text, l, r)
	}
	if opensClause(text, left) {
		right = trimRight(text, cutSeparator(text, right))
	}
	return collapse(text, left, right)
}

// spaceBytes are the horizontal blanks the strip crosses. A newline is
// not one: a strip never joins two lines.
const spaceBytes = " \t"

// trimLeft moves the offset back over horizontal blanks.
func trimLeft(text string, at int) int {
	for at > 0 && strings.IndexByte(spaceBytes, text[at-1]) >= 0 {
		at--
	}
	return at
}

// trimRight moves the offset forward over horizontal blanks.
func trimRight(text string, at int) int {
	for at < len(text) && strings.IndexByte(spaceBytes, text[at]) >= 0 {
		at++
	}
	return at
}

// citationLabel is the label a carrier writes in front of a citation.
const citationLabel = "spec:"

// cutLabel reports the offset of a `spec:` label standing in front of
// the citation.
func cutLabel(text string, at int) (int, bool) {
	if at < len(citationLabel) {
		return at, false
	}
	if !strings.EqualFold(text[at-len(citationLabel):at], citationLabel) {
		return at, false
	}
	return at - len(citationLabel), true
}

// brackets are the delimiter pairs a carrier wraps a citation in.
var brackets = map[byte]byte{'(': ')', '[': ']'}

// cutBrackets reports the span widened over a bracket pair that wraps
// the citation and nothing else.
func cutBrackets(text string, left, right int) (int, int, bool) {
	if left == 0 || right >= len(text) {
		return left, right, false
	}
	closer, ok := brackets[text[left-1]]
	if !ok || text[right] != closer {
		return left, right, false
	}
	return left - 1, right + 1, true
}

// separators are the marks that introduce the text a citation opened,
// each written against a blank so a hyphenated word is not read as one.
var separators = []string{"—", "–", "-", ":", "/", ","}

// cutSeparator reports the offset behind a separator standing between
// the citation and the text it introduced.
func cutSeparator(text string, at int) int {
	for _, sep := range separators {
		if !strings.HasPrefix(text[at:], sep) {
			continue
		}
		behind := at + len(sep)
		if behind < len(text) && strings.IndexByte(spaceBytes, text[behind]) >= 0 {
			return behind
		}
	}
	return at
}

// closers are the marks a blank must not stand in front of once the
// citation between them is gone.
const closers = ".,;:)]}\"`\n"

// opensClause reports whether the offset stands at the opening of the
// run the citation sat in, which is the start of a string literal, the
// start of a comment body, or the byte behind an opening bracket. A
// citation that opened a bracketed clause introduces the text behind it
// the same way a citation opening a value does, so the separator it wrote
// against that text is residue once the citation is gone. Leaving the
// separator behind renders a summary as `(— stop admitting new
// sessions)` in a document a client reads.
func opensClause(text string, at int) bool {
	if at == 0 {
		return true
	}
	if text[at-1] == '"' || text[at-1] == '`' {
		return true
	}
	if _, ok := brackets[text[at-1]]; ok {
		return true
	}
	return commentOpening.MatchString(text[lineStart(text, at):at])
}

// commentOpening matches the run from the start of a line to the body of
// a comment in the dialects the carriers use.
var commentOpening = regexp.MustCompile(`^[ \t]*(?:(?://|#|--|\*)[ \t]*)?$`)

// lineStart returns the offset of the start of the line the offset sits
// on.
func lineStart(text string, at int) int {
	if i := strings.LastIndexByte(text[:at], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// duplicated are the marks that read as a repetition once the citation
// between them is gone. A citation written as its own sentence behind
// the sentence it annotates carries the second of the two periods.
const duplicated = ".,;"

// collapse closes the blanks the removal would double up, the mark it
// would repeat, and the blank it would leave standing in front of a
// closing mark.
func collapse(text string, left, right int) (int, int) {
	if left > 0 && right < len(text) && text[right] == text[left-1] && strings.IndexByte(duplicated, text[right]) >= 0 {
		return left, right + 1
	}
	if left > 0 && text[left-1] == ' ' && right < len(text) {
		if text[right] == ' ' {
			return left, right + 1
		}
		if strings.IndexByte(closers, text[right]) >= 0 {
			return left - 1, right
		}
	}
	if opensClause(text, left) {
		return left, trimRight(text, right)
	}
	return left, right
}
