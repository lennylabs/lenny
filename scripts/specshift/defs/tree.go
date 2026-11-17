// SPDX-License-Identifier: MIT

package defs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecDir is the repo-relative directory that holds the specification tree.
const SpecDir = "spec"

// Problem is one finding of a check, located by file and line.
type Problem struct {
	// Path is the repo-relative file.
	Path string
	// Line is the 1-based line, or 0 when the problem concerns the file.
	Line int
	// Message says what is wrong.
	Message string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Message)
	}
	return fmt.Sprintf("%s: %s", p.Path, p.Message)
}

// FrontMatter is the part of a page's front matter the package reads.
type FrontMatter struct {
	Code    string `yaml:"code"`
	Type    string `yaml:"type"`
	Area    string `yaml:"area"`
	Summary string `yaml:"summary"`
	Status  string `yaml:"status"`
}

// Page is one page of the specification tree.
type Page struct {
	// Path is the repo-relative path, such as spec/components/gateway/index.md.
	Path string
	// Front is the decoded front matter.
	Front FrontMatter
	// FrontText is the front matter verbatim, from the opening --- line to
	// the closing --- line inclusive, with a trailing newline.
	FrontText string
	// Body is the text after the front matter.
	Body string
	// bodyLine is the file line of the body's first line.
	bodyLine int
	// Title is the text of the first level-one heading, or empty.
	Title string
	// Blocks are the page's definition blocks in file order.
	Blocks []*Block
	// Terms are the owned terms of the page's tables.
	Terms []Term
	// Requirements are the page's requirements.
	Requirements []Requirement
}

// Dir returns the page's folder relative to the specification directory,
// "" for a page at the root of the tree.
func (p *Page) Dir() string {
	dir := path.Dir(strings.TrimPrefix(p.Path, SpecDir+"/"))
	if dir == "." {
		return ""
	}
	return dir
}

// IsPlaceholder reports whether the page is a placeholder.
func (p *Page) IsPlaceholder() bool { return p.Front.Status == "placeholder" }

// Block is one definition block.
type Block struct {
	// Page is the page that holds the block.
	Page *Page
	// Line is the file line of the opening fence.
	Line int
	// Kind is the kind field's value; Schema is nil when it names no kind.
	Kind   string
	Schema *Kind
	// Name is the value of the kind's name field.
	Name string
	// Anchor is the anchor the block's kind and name determine.
	Anchor string
	// fields maps each key to its value node, and keyLines to the key's
	// file line.
	fields   map[string]*yaml.Node
	keyLines map[string]int
	// anchorLine is the file line of the anchor above the block, and
	// anchorFound the anchor it names, empty when there is none.
	anchorLine  int
	anchorFound string
	// problems are the parse problems found while reading the block.
	problems []Problem
}

// Text returns a field's value rendered as text: a scalar as written, a
// sequence joined with ", ", and a mapping as "key: value" pairs.
func (b *Block) Text(field string) string {
	return nodeText(b.fields[field])
}

// List returns a field's value as a list of strings: a sequence's items,
// or a scalar as a one-item list.
func (b *Block) List(field string) []string {
	n := b.fields[field]
	if n == nil {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, nodeText(c))
		}
		return out
	}
	return []string{nodeText(n)}
}

// Has reports whether the block carries a field.
func (b *Block) Has(field string) bool { return b.fields[field] != nil }

// Term is one owned term.
type Term struct {
	Page       *Page
	Line       int
	Anchor     string
	Term       string
	Definition string
}

// Requirement is one numbered requirement.
type Requirement struct {
	Page       *Page
	Line       int
	Anchor     string
	Identifier string
	Title      string
}

// Tree is the specification tree.
type Tree struct {
	// Pages are the pages sorted by path.
	Pages []*Page
	// Problems are the problems found while reading the pages.
	Problems []Problem
}

// PageByPath returns the page at a repo-relative path.
func (t *Tree) PageByPath(p string) *Page {
	for _, pg := range t.Pages {
		if pg.Path == p {
			return pg
		}
	}
	return nil
}

// Codes returns the page of each registered code. A code two pages carry
// maps to the first by path; the check reports the duplicate.
func (t *Tree) Codes() map[string]*Page {
	out := map[string]*Page{}
	for _, p := range t.Pages {
		if _, ok := out[p.Front.Code]; !ok {
			out[p.Front.Code] = p
		}
	}
	return out
}

// Blocks returns every block of the tree in path and line order.
func (t *Tree) Blocks() []*Block {
	var out []*Block
	for _, p := range t.Pages {
		out = append(out, p.Blocks...)
	}
	return out
}

// LoadTree reads every page under root/spec. A Markdown file is a page
// when it opens with YAML front matter that carries a code; any other file
// is outside the tree.
func LoadTree(root string) (*Tree, error) {
	t := &Tree{}
	specRoot := filepath.Join(root, SpecDir)
	err := filepath.WalkDir(specRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		page, problems := ParsePage(filepath.ToSlash(rel), string(content))
		t.Problems = append(t.Problems, problems...)
		if page != nil {
			t.Pages = append(t.Pages, page)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("walk %s: %w", specRoot, err)
	}
	sort.Slice(t.Pages, func(i, j int) bool { return t.Pages[i].Path < t.Pages[j].Path })
	return t, nil
}

// splitFrontMatter splits a file into its front matter and its body. It
// reports ok false when the file does not open with a front matter block.
func splitFrontMatter(content string) (front, yamlText, body string, bodyLine int, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", content, 1, false
	}
	lines := strings.SplitAfter(content, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			front = strings.Join(lines[:i+1], "")
			if !strings.HasSuffix(front, "\n") {
				front += "\n"
			}
			yamlText = strings.Join(lines[1:i], "")
			body = strings.Join(lines[i+1:], "")
			return front, yamlText, body, i + 2, true
		}
	}
	return "", "", content, 1, false
}

// ParsePage parses one file. It returns a nil page for a file that is not
// a page of the tree.
func ParsePage(relPath, content string) (*Page, []Problem) {
	front, yamlText, body, bodyLine, ok := splitFrontMatter(content)
	if !ok {
		return nil, nil
	}
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(yamlText), &fm); err != nil {
		return nil, []Problem{{Path: relPath, Line: 1, Message: fmt.Sprintf("front matter is not valid YAML: %v", err)}}
	}
	if fm.Code == "" {
		return nil, nil
	}
	// The grammar of a code is the reference tool's to enforce, with the
	// rest of the code registry; this package needs only the set of codes.
	p := &Page{Path: relPath, Front: fm, FrontText: front, Body: body, bodyLine: bodyLine}
	return p, scanBody(p)
}

var (
	anchorLine      = regexp.MustCompile(`^\s*<a id="([^"]*)"></a>\s*$`)
	termRow         = regexp.MustCompile(`^\|\s*<a id="([^"]+)"></a>\s*([^|]*?)\s*\|\s*(.*?)\s*\|\s*$`)
	requirementLine = regexp.MustCompile(`<a id="([a-z0-9]+-r[0-9]+)"></a>\*\*([A-Z]{2,9}-R[0-9]+)\.\s*(.*?)\*\*`)
	fenceOpen       = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")
)

// scanBody walks the body once, outside other code fences, collecting the
// title, the definition blocks, the owned terms, and the requirements.
func scanBody(p *Page) []Problem {
	lines := strings.Split(p.Body, "\n")
	var problems []Problem
	lastNonBlank, lastNonBlankLine := "", 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		fileLine := p.bodyLine + i
		if m := fenceOpen.FindStringSubmatch(line); m != nil {
			end := fenceEnd(lines, i, m[1])
			if m[2] == "lenny-def" {
				b := parseBlock(p, fileLine, lines[i+1:end])
				b.anchorFound, b.anchorLine = precedingAnchor(lastNonBlank), lastNonBlankLine
				p.Blocks = append(p.Blocks, b)
				problems = append(problems, b.problems...)
				if end == len(lines) {
					problems = append(problems, Problem{Path: p.Path, Line: fileLine, Message: "the lenny-def block has no closing fence"})
				}
			}
			i = end
			lastNonBlank, lastNonBlankLine = "", 0
			continue
		}
		scanLine(p, line, fileLine)
		if strings.TrimSpace(line) != "" {
			lastNonBlank, lastNonBlankLine = line, fileLine
		}
	}
	return problems
}

// scanLine collects the title, an owned term, or a requirement from one
// line outside a code fence.
func scanLine(p *Page, line string, fileLine int) {
	if p.Title == "" && strings.HasPrefix(line, "# ") {
		p.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		p.Title = headingAttribute.ReplaceAllString(p.Title, "")
	}
	if m := termRow.FindStringSubmatch(line); m != nil {
		p.Terms = append(p.Terms, Term{Page: p, Line: fileLine, Anchor: m[1], Term: m[2], Definition: m[3]})
		return
	}
	for _, m := range requirementLine.FindAllStringSubmatch(line, -1) {
		p.Requirements = append(p.Requirements, Requirement{Page: p, Line: fileLine, Anchor: m[1], Identifier: m[2], Title: strings.TrimSpace(m[3])})
	}
}

// headingAttribute matches a trailing kramdown attribute such as {#terms}.
var headingAttribute = regexp.MustCompile(`\s*\{#[^}]*\}\s*$`)

// fenceEnd returns the index of the line that closes the fence opened at
// line i, or len(lines) when the fence is never closed.
func fenceEnd(lines []string, i int, marker string) int {
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if len(t) >= len(marker) && strings.Trim(t, marker[:1]) == "" && strings.HasPrefix(t, marker) {
			return j
		}
	}
	return len(lines)
}

// precedingAnchor returns the id of an anchor line, or "".
func precedingAnchor(line string) string {
	if m := anchorLine.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// parseBlock decodes the body lines of one block. fenceLine is the file
// line of the opening fence, so the block's first body line is
// fenceLine+1.
func parseBlock(p *Page, fenceLine int, body []string) *Block {
	b := &Block{Page: p, Line: fenceLine, fields: map[string]*yaml.Node{}, keyLines: map[string]int{}}
	at := func(line int, format string, args ...any) {
		b.problems = append(b.problems, Problem{Path: p.Path, Line: line, Message: fmt.Sprintf(format, args...)})
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(body, "\n")), &doc); err != nil {
		at(fenceLine, "the lenny-def block is not valid YAML: %v", err)
		return b
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		at(fenceLine, "the lenny-def block is not a YAML mapping")
		return b
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		line := fenceLine + key.Line
		if _, dup := b.fields[key.Value]; dup {
			at(line, "field %q appears twice", key.Value)
			continue
		}
		b.fields[key.Value] = val
		b.keyLines[key.Value] = line
	}
	b.Kind = nodeText(b.fields["kind"])
	b.Schema, _ = KindByName(b.Kind)
	if b.Schema != nil {
		b.Name = nodeText(b.fields[b.Schema.NameField])
		if Slug(b.Name) != "" {
			b.Anchor = AnchorFor(b.Schema.Name, b.Name, p.Front.Code)
		}
	}
	return b
}

// lineOf returns the file line of a field, or of the block's fence when
// the field is absent.
func (b *Block) lineOf(field string) int {
	if l, ok := b.keyLines[field]; ok {
		return l
	}
	return b.Line
}

// nodeText renders a YAML value as one line of text.
func nodeText(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.Join(strings.Fields(n.Value), " ")
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			parts = append(parts, nodeText(c))
		}
		return strings.Join(parts, ", ")
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, nodeText(n.Content[i])+": "+nodeText(n.Content[i+1]))
		}
		return strings.Join(parts, ", ")
	case yaml.AliasNode:
		return nodeText(n.Alias)
	}
	return ""
}

// Slug lower-cases a name and replaces each run of characters other than
// letters and digits with a hyphen, trimming hyphens at either end.
func Slug(name string) string {
	var sb strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingHyphen && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			pendingHyphen = false
			sb.WriteRune(r)
			continue
		}
		pendingHyphen = true
	}
	return sb.String()
}

// AnchorFor returns the anchor of a block: <kind>-<slug>, or for an
// operation the operation identifier <code>-<name> slugged.
func AnchorFor(kind, name, code string) string {
	if kind == KindOperation {
		return Slug(code + "-" + name)
	}
	return kind + "-" + Slug(name)
}
