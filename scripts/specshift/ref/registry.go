// SPDX-License-Identifier: MIT

package ref

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// registryVersion is the format version of spec/registry.json.
const registryVersion = 1

// Registry is the generated table of every page code, identifier,
// anchor, and definition block of the specification tree, with the file
// that holds each one and the history of moves. The committed copy is
// the record of where every identifier was, so a later build detects a
// move by comparing with it.
type Registry struct {
	Version      int          `json:"version"`
	Pages        []Page       `json:"pages"`
	Requirements []Identifier `json:"requirements"`
	Operations   []Identifier `json:"operations"`
	Terms        []Term       `json:"terms"`
	Blocks       []Block      `json:"blocks"`
	Retired      []Retired    `json:"retired"`
	Moves        []Move       `json:"moves"`
}

// Page is one page of the new tree, read from its front matter.
type Page struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Type    string   `json:"type"`
	Status  string   `json:"status"`
	Aliases []string `json:"aliases,omitempty"`
}

// Identifier is a requirement (CODE-Rn) or an operation (CODE-<name>)
// with its explicit anchor.
type Identifier struct {
	ID     string `json:"id"`
	Anchor string `json:"anchor"`
	File   string `json:"file"`
}

// Term is an owned term with its explicit anchor.
type Term struct {
	Anchor string `json:"anchor"`
	File   string `json:"file"`
}

// Block is a definition block, keyed by the anchor derived from its kind
// and name.
type Block struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Anchor string `json:"anchor"`
	File   string `json:"file"`
}

// Retired is an identifier a page retires in its front matter, with the
// identifier that succeeds it or the record of its removal.
type Retired struct {
	ID        string `json:"id"`
	Anchor    string `json:"anchor"`
	File      string `json:"file"`
	Successor string `json:"successor,omitempty"`
	Removed   string `json:"removed,omitempty"`
}

// Move records that a page code or an identifier left one file for
// another. A page move is recorded once under the page's code; an
// identifier is recorded only when it moved apart from its page.
type Move struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Finding is one problem the pass reports, at a file and a line.
type Finding struct {
	File    string
	Line    int
	Message string
}

func (f Finding) String() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message)
	}
	return fmt.Sprintf("%s: %s", f.File, f.Message)
}

// emptyRegistry returns a registry with every list present and empty, so
// the encoded file never carries null.
func emptyRegistry() *Registry {
	return &Registry{
		Version:      registryVersion,
		Pages:        []Page{},
		Requirements: []Identifier{},
		Operations:   []Identifier{},
		Terms:        []Term{},
		Blocks:       []Block{},
		Retired:      []Retired{},
		Moves:        []Move{},
	}
}

// Encode returns the registry as the committed file holds it.
func (r *Registry) Encode() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, fmt.Errorf("encode the registry: %w", err)
	}
	return b.Bytes(), nil
}

// LoadCommitted reads the committed registry. A tree with no committed
// registry has no history, so it reads as an empty registry.
func LoadCommitted(t *Tree) (*Registry, error) {
	b, err := scope.ReadChecked(t.Root, RegistryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return emptyRegistry(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegistryPath, err)
	}
	r := emptyRegistry()
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", RegistryPath, err)
	}
	return r, nil
}

// pageDoc is one page of the new tree as the build reads it.
type pageDoc struct {
	path    string
	content string
	fm      frontMatter
}

// builder accumulates a registry and the problems found while building
// it.
type builder struct {
	reg      *Registry
	findings []Finding
	codes    map[string]bool
	codeLine map[string]int       // page path -> line of its code: field
	anchors  map[string][]Finding // anchor -> every place it is registered
}

// Build reads the specification tree and returns its registry, with the
// moves found against the committed registry appended to the committed
// history, and the problems the build met.
func Build(ctx context.Context, t *Tree, committed *Registry) (*Registry, []Finding, error) {
	docs, findings, err := readPages(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	b := &builder{reg: emptyRegistry(), findings: findings, codes: map[string]bool{}, codeLine: map[string]int{}, anchors: map[string][]Finding{}}
	for _, d := range docs {
		b.codes[d.fm.Code] = true
		for _, a := range d.fm.Aliases {
			b.codes[a] = true
		}
	}
	for _, d := range docs {
		b.addPage(d)
	}
	b.reportCodeGrammar()
	b.reportDuplicateCodes()
	b.reportDuplicateAnchors()
	b.reportSuffixCodes()
	b.reportReservedCodes()
	b.sort()
	b.reg.Moves = appendMoves(committed, b.reg)
	return b.reg, b.findings, nil
}

// readPages returns every page of the new tree: each Markdown file under
// spec/ other than a numbered file whose front matter names a code.
func readPages(ctx context.Context, t *Tree) ([]pageDoc, []Finding, error) {
	files, err := t.Files(ctx)
	if err != nil {
		return nil, nil, err
	}
	var docs []pageDoc
	var findings []Finding
	for _, p := range files {
		if !strings.HasPrefix(p, specDir) || isNumbered(p) || !isMarkdown(p) {
			continue
		}
		content, err := t.Read(p)
		if err != nil {
			return nil, nil, err
		}
		fm, ok, err := parseFrontMatter(content)
		if err != nil {
			findings = append(findings, Finding{File: p, Line: 1, Message: err.Error()})
			continue
		}
		if !ok || fm.Code == "" {
			continue
		}
		docs = append(docs, pageDoc{path: p, content: content, fm: fm})
	}
	return docs, findings, nil
}

// addPage registers one page and everything it declares.
func (b *builder) addPage(d pageDoc) {
	aliases := append([]string(nil), d.fm.Aliases...)
	sort.Strings(aliases)
	b.reg.Pages = append(b.reg.Pages, Page{Code: d.fm.Code, Path: d.path, Type: d.fm.Type, Status: d.fm.Status, Aliases: aliases})
	b.codeLine[d.path] = frontMatterLine(d.content, "code")

	blockAnchors := b.addBlocks(d)
	retired := b.addRetired(d)
	for _, a := range explicitAnchors(d.content) {
		if blockAnchors[a.id] || retired[a.id] {
			// The anchor written above a block, or kept in the body for a
			// retired identifier, is the anchor the block or the
			// retirement already registers.
			delete(blockAnchors, a.id)
			continue
		}
		b.addAnchor(d.path, a)
	}
	b.reportBareRequirements(d)
}

// addBlocks registers the definition blocks of a page and returns their
// anchors. The blocks are read by the definition-block parser of
// scripts/specshift/defs, so the registry names each block by its kind's
// name field (name, key, or type) and anchors it as defs.AnchorFor does.
// An operation block registers the operation identifier <CODE>-<name>,
// whose anchor carries the page code, so two interface pages may each
// define an operation with the same name.
func (b *builder) addBlocks(d pageDoc) map[string]bool {
	out := map[string]bool{}
	page, _ := defs.ParsePage(d.path, d.content)
	if page == nil {
		return out
	}
	for _, blk := range page.Blocks {
		switch {
		case blk.Kind == "":
			b.findings = append(b.findings, Finding{File: d.path, Line: blk.Line, Message: "definition block has no kind: line"})
			continue
		case blk.Schema == nil:
			b.findings = append(b.findings, Finding{File: d.path, Line: blk.Line, Message: fmt.Sprintf("definition block kind %q is not a defined kind", blk.Kind)})
			continue
		case blk.Anchor == "":
			b.findings = append(b.findings, Finding{File: d.path, Line: blk.Line, Message: fmt.Sprintf("definition block of kind %s has no %s: line", blk.Kind, blk.Schema.NameField)})
			continue
		}
		if blk.Kind == defs.KindOperation {
			b.reg.Operations = append(b.reg.Operations, Identifier{ID: d.fm.Code + "-" + blk.Name, Anchor: blk.Anchor, File: d.path})
		} else {
			b.reg.Blocks = append(b.reg.Blocks, Block{Kind: blk.Kind, Name: blk.Name, Anchor: blk.Anchor, File: d.path})
		}
		b.claim(blk.Anchor, d.path, blk.Line)
		out[blk.Anchor] = true
	}
	return out
}

// addRetired registers the identifiers a page retires in its front
// matter and returns their anchors.
func (b *builder) addRetired(d pageDoc) map[string]bool {
	out := map[string]bool{}
	for id, record := range d.fm.Retired {
		line := frontMatterLine(d.content, id)
		r := Retired{ID: id, Anchor: strings.ToLower(id), File: d.path}
		record = strings.TrimSpace(record)
		switch {
		case record == "":
			b.findings = append(b.findings, Finding{File: d.path, Line: line, Message: fmt.Sprintf("retired identifier %s has no successor or removed record", id)})
		case strings.HasPrefix(strings.ToLower(record), "removed"):
			r.Removed = record
		default:
			r.Successor = record
		}
		b.reg.Retired = append(b.reg.Retired, r)
		b.claim(r.Anchor, d.path, line)
		out[r.Anchor] = true
	}
	return out
}

// frontMatterLine returns the line of the front matter that names key,
// or 1 when none does.
func frontMatterLine(content, key string) int {
	for i, l := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), key+":") {
			return i + 1
		}
	}
	return 1
}

// addAnchor classifies one explicit anchor and registers it: a
// requirement when it reads `<code>-r<n>` for a registered code, an
// operation when it reads `<code>-<name>` and the identifier is written
// beside it, and otherwise an owned term.
func (b *builder) addAnchor(file string, a explicitAnchor) {
	b.claim(a.id, file, a.line)
	prefix, rest, ok := strings.Cut(a.id, "-")
	code := strings.ToUpper(prefix)
	if ok && rest != "" && b.codes[code] {
		if m := requirementSuffixExpr.FindStringSubmatch(rest); m != nil {
			b.reg.Requirements = append(b.reg.Requirements, Identifier{ID: code + "-R" + m[1], Anchor: a.id, File: file})
			return
		}
		if name, found := operationName(code, rest, a.context); found {
			b.reg.Operations = append(b.reg.Operations, Identifier{ID: code + "-" + name, Anchor: a.id, File: file})
			return
		}
	}
	b.reg.Terms = append(b.reg.Terms, Term{Anchor: a.id, File: file})
}

var requirementSuffixExpr = regexp.MustCompile(`^r(\d+)$`)

// operationName returns the operation name an anchor marks, spelled as
// the text beside the anchor writes it, when that text names the
// identifier `CODE-<name>`.
func operationName(code, rest, context string) (string, bool) {
	expr := regexp.MustCompile(`(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(code) + `-([A-Za-z0-9_.]+)`)
	for _, m := range expr.FindAllStringSubmatch(context, -1) {
		if strings.ToLower(m[1]) == rest {
			return m[1], true
		}
	}
	return "", false
}

// bareRequirementExpr matches a requirement written at the start of a
// line, after an optional list marker.
var bareRequirementExpr = regexp.MustCompile(`^[ \t]*(?:[-*][ \t]+)?\*\*([A-Z][A-Z0-9]*-R\d+)\.`)

// reportBareRequirements reports a requirement written without the
// explicit anchor the conventions require, because no link can reach it.
func (b *builder) reportBareRequirements(d pageDoc) {
	for i, l := range strings.Split(mask(d.content), "\n") {
		if m := bareRequirementExpr.FindStringSubmatch(l); m != nil {
			b.findings = append(b.findings, Finding{File: d.path, Line: i + 1, Message: fmt.Sprintf("requirement %s has no explicit anchor <a id=%q></a>", m[1], strings.ToLower(m[1]))})
		}
	}
}

// claim records one registration of an anchor.
func (b *builder) claim(anchor, file string, line int) {
	b.anchors[anchor] = append(b.anchors[anchor], Finding{File: file, Line: line})
}

// CodeExpr is the grammar of a page code: two to nine upper-case letters,
// with no digit and no hyphen, as the conventions page states under
// "Identifiers".
var CodeExpr = regexp.MustCompile(`^[A-Z]{2,9}$`)

// reportCodeGrammar reports a code or an alias that breaks the grammar,
// because an identifier built on it would not parse as <CODE>-<suffix>.
func (b *builder) reportCodeGrammar() {
	for _, p := range b.reg.Pages {
		for _, c := range append([]string{p.Code}, p.Aliases...) {
			if !CodeExpr.MatchString(c) {
				b.findings = append(b.findings, Finding{File: p.Path, Line: b.codeLine[p.Path], Message: fmt.Sprintf("code %s is not two to nine upper-case letters", c)})
			}
		}
	}
}

// reportDuplicateCodes reports a code that two pages declare, and an
// alias that is also a code or another page's alias.
func (b *builder) reportDuplicateCodes() {
	seen := map[string]string{}
	note := func(code, file string) {
		if prev, ok := seen[code]; ok {
			b.findings = append(b.findings, Finding{File: file, Line: b.codeLine[file], Message: fmt.Sprintf("code %s is also registered by %s", code, prev)})
			return
		}
		seen[code] = file
	}
	for _, p := range b.reg.Pages {
		note(p.Code, p.Path)
	}
	for _, p := range b.reg.Pages {
		for _, a := range p.Aliases {
			note(a, p.Path)
		}
	}
}

// reportDuplicateAnchors reports an anchor registered more than once:
// two requirements, terms, operations, or blocks with one anchor, on one
// page or on two.
func (b *builder) reportDuplicateAnchors() {
	for anchor, places := range b.anchors {
		if len(places) < 2 {
			continue
		}
		for _, p := range places[1:] {
			b.findings = append(b.findings, Finding{File: p.File, Line: p.Line, Message: fmt.Sprintf("anchor #%s is also registered at %s:%d", anchor, places[0].File, places[0].Line)})
		}
	}
}

// reportSuffixCodes reports a code that differs from another only by a
// one-letter suffix, such as DERIVE beside DERIVED, because the two read
// as the same code.
func (b *builder) reportSuffixCodes() {
	owner := map[string]string{}
	for _, p := range b.reg.Pages {
		owner[p.Code] = p.Path
		for _, a := range p.Aliases {
			owner[a] = p.Path
		}
	}
	for code, file := range owner {
		if len(code) < 3 {
			continue
		}
		short := code[:len(code)-1]
		if other, ok := owner[short]; ok && other != file {
			b.findings = append(b.findings, Finding{File: file, Line: b.codeLine[file], Message: fmt.Sprintf("code %s differs from %s (%s) only by a suffix", code, short, other)})
		}
	}
}

// ReservedCodes holds the issue codes that no specification page
// carries, each with the subject its issues cover. issues/README.md
// ("Codes outside the specification") assigns them. A page that took one
// would share its folder under issues/, so the registry refuses it.
var ReservedCodes = map[string]string{
	"GATE": "the merge gate, the test harness, and the gate registers",
}

// IsReservedCode reports whether code is an issue code that no page
// carries.
func IsReservedCode(code string) bool {
	_, ok := ReservedCodes[code]
	return ok
}

// reportReservedCodes reports a page code or alias that is a reserved
// issue code, or that differs from one only by a one-letter suffix, as
// reportSuffixCodes reads two page codes.
func (b *builder) reportReservedCodes() {
	for _, p := range b.reg.Pages {
		for _, c := range append([]string{p.Code}, p.Aliases...) {
			for reserved, subject := range ReservedCodes {
				switch {
				case c == reserved:
					b.findings = append(b.findings, Finding{File: p.Path, Line: b.codeLine[p.Path], Message: fmt.Sprintf("code %s is reserved for the issues about %s (issues/README.md)", c, subject)})
				case len(c) > 1 && (c[:len(c)-1] == reserved || reserved[:len(reserved)-1] == c):
					b.findings = append(b.findings, Finding{File: p.Path, Line: b.codeLine[p.Path], Message: fmt.Sprintf("code %s differs from the reserved issue code %s only by a suffix", c, reserved)})
				}
			}
		}
	}
}

// sort orders every list so the encoded registry is stable.
func (b *builder) sort() {
	r := b.reg
	sort.Slice(r.Pages, func(i, j int) bool {
		if r.Pages[i].Code != r.Pages[j].Code {
			return r.Pages[i].Code < r.Pages[j].Code
		}
		return r.Pages[i].Path < r.Pages[j].Path
	})
	for _, ids := range [][]Identifier{r.Requirements, r.Operations} {
		sort.Slice(ids, func(i, j int) bool {
			if ids[i].ID != ids[j].ID {
				return naturalLess(ids[i].ID, ids[j].ID)
			}
			return ids[i].File < ids[j].File
		})
	}
	sort.Slice(r.Terms, func(i, j int) bool {
		if r.Terms[i].Anchor != r.Terms[j].Anchor {
			return r.Terms[i].Anchor < r.Terms[j].Anchor
		}
		return r.Terms[i].File < r.Terms[j].File
	})
	sort.Slice(r.Blocks, func(i, j int) bool {
		if r.Blocks[i].Anchor != r.Blocks[j].Anchor {
			return r.Blocks[i].Anchor < r.Blocks[j].Anchor
		}
		return r.Blocks[i].File < r.Blocks[j].File
	})
	sort.Slice(r.Retired, func(i, j int) bool { return naturalLess(r.Retired[i].ID, r.Retired[j].ID) })
	sort.Slice(b.findings, func(i, j int) bool { return findingLess(b.findings[i], b.findings[j]) })
}

// findingLess orders findings by file and line.
func findingLess(a, b Finding) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Message < b.Message
}

// naturalLess orders strings with their digit runs compared as numbers,
// so POOL-R2 sorts before POOL-R10.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digitPrefix(a), digitPrefix(b)
		if da != "" && db != "" {
			if len(da) != len(db) {
				return len(da) < len(db)
			}
			if da != db {
				return da < db
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digitPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// index is a registry read for lookup: where every code and anchor is.
type index struct {
	reg        *Registry
	pageByPath map[string]Page
	codePath   map[string]string // code or alias -> page path
	aliasOf    map[string]string // alias -> code
	anchorFile map[string]string // anchor -> file, for every registered anchor
	idAnchor   map[string]string // requirement or operation ID -> anchor
	retired    map[string]Retired
}

// newIndex indexes a registry.
func newIndex(r *Registry) *index {
	x := &index{
		reg:        r,
		pageByPath: map[string]Page{},
		codePath:   map[string]string{},
		aliasOf:    map[string]string{},
		anchorFile: map[string]string{},
		idAnchor:   map[string]string{},
		retired:    map[string]Retired{},
	}
	for _, p := range r.Pages {
		x.pageByPath[p.Path] = p
		x.codePath[p.Code] = p.Path
		for _, a := range p.Aliases {
			x.codePath[a] = p.Path
			x.aliasOf[a] = p.Code
		}
	}
	for _, ids := range [][]Identifier{r.Requirements, r.Operations} {
		for _, id := range ids {
			x.anchorFile[id.Anchor] = id.File
			x.idAnchor[id.ID] = id.Anchor
		}
	}
	for _, t := range r.Terms {
		x.anchorFile[t.Anchor] = t.File
	}
	for _, blk := range r.Blocks {
		x.anchorFile[blk.Anchor] = blk.File
	}
	for _, rt := range r.Retired {
		x.anchorFile[rt.Anchor] = rt.File
		x.idAnchor[rt.ID] = rt.Anchor
		x.retired[rt.ID] = rt
	}
	return x
}

// resolves reports whether a name is a registered code, alias,
// identifier, or anchor.
func (x *index) resolves(name string) bool {
	if _, ok := x.codePath[name]; ok {
		return true
	}
	if _, ok := x.idAnchor[name]; ok {
		return true
	}
	_, ok := x.anchorFile[strings.ToLower(name)]
	return ok
}

// identities returns the file of every identifier and anchor the
// registry holds, keyed by the name a move record uses: the identifier
// for a requirement, an operation, or a retirement, and the anchor for a
// term or a block.
func identities(r *Registry) map[string]string {
	out := map[string]string{}
	for _, ids := range [][]Identifier{r.Requirements, r.Operations} {
		for _, id := range ids {
			out[id.ID] = id.File
		}
	}
	for _, t := range r.Terms {
		out[t.Anchor] = t.File
	}
	for _, blk := range r.Blocks {
		out[blk.Anchor] = blk.File
	}
	for _, rt := range r.Retired {
		out[rt.ID] = rt.File
	}
	return out
}

// appendMoves returns the committed history of moves with a record
// appended for every page and identifier the fresh registry places in
// another file than the committed one does. An identifier that moved
// with its page is covered by the page's record.
func appendMoves(committed, fresh *Registry) []Move {
	moves := append([]Move{}, committed.Moves...)
	oldPage := map[string]string{} // code -> committed path
	oldCode := map[string]string{} // committed path -> code
	for _, p := range committed.Pages {
		oldPage[p.Code] = p.Path
		oldCode[p.Path] = p.Code
	}
	newPage := map[string]string{}
	for _, p := range fresh.Pages {
		newPage[p.Code] = p.Path
	}
	var added []Move
	for _, p := range fresh.Pages {
		if from, ok := oldPage[p.Code]; ok && from != p.Path {
			added = append(added, Move{ID: p.Code, From: from, To: p.Path})
		}
	}
	before := identities(committed)
	for id, to := range identities(fresh) {
		from, ok := before[id]
		if !ok || from == to {
			continue
		}
		if code, ok := oldCode[from]; ok && newPage[code] == to {
			continue
		}
		added = append(added, Move{ID: id, From: from, To: to})
	}
	sort.Slice(added, func(i, j int) bool {
		if added[i].ID != added[j].ID {
			return naturalLess(added[i].ID, added[j].ID)
		}
		return added[i].From < added[j].From
	})
	return append(moves, added...)
}

// pathCodes returns, for each path, the code of the page a committed registry and its
// history of moves place at a path: the page there when the registry was
// committed, or a page a move record says left that path.
func pathCodes(committed *Registry) map[string]string {
	out := map[string]string{}
	codes := map[string]bool{}
	for _, p := range committed.Pages {
		codes[p.Code] = true
	}
	for _, m := range committed.Moves {
		if codes[m.ID] {
			out[m.From] = m.ID
		}
	}
	for _, p := range committed.Pages {
		out[p.Path] = p.Code
	}
	return out
}

// displayTarget renders a link target for a message.
func displayTarget(p, fragment string) string {
	if fragment == "" {
		return p
	}
	return p + "#" + fragment
}
