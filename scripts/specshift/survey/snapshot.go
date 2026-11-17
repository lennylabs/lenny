// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Kinds of item a snapshot holds.
const (
	KindRequirement = "requirement"
	KindOperation   = "operation"
	KindBlock       = "definition block"
)

// PageInfo is one page of the specification tree.
type PageInfo struct {
	Code, Path, Type, Area, Title, Summary, Status string
}

// Group returns the area a page belongs to for grouping: its area, or for
// a page with none, such as a component or an interface, the folder under
// spec/ that holds it.
func (p PageInfo) Group() string {
	if p.Area != "" {
		return p.Area
	}
	rest := strings.TrimPrefix(p.Path, "spec/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return "root"
}

// Item is one requirement, operation, or definition block, with its text.
type Item struct {
	// Kind is KindRequirement, KindOperation, or KindBlock.
	Kind string
	// ID is the identifier: `POOL-R5`, `ADMPOOLS-createPool`, or a
	// block's anchor such as `setting-minwarm`.
	ID string
	// Anchor is the explicit anchor that marks the item.
	Anchor string
	// File is the page that holds the item, and Code that page's code.
	File, Code string
	// Name is a block's name, and BlockKind its kind.
	Name, BlockKind string
	// Text is the item's text as the page states it.
	Text string
}

// Title returns the requirement's bold title, a block's name, or the
// identifier.
func (it *Item) Title() string {
	if it.Kind == KindBlock {
		return it.Name
	}
	if m := requirementTitleExpr.FindStringSubmatch(it.Text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return it.ID
}

// requirementTitleExpr reads the bold title that follows a requirement's
// identifier.
var requirementTitleExpr = regexp.MustCompile(`\*\*[A-Z][A-Z0-9]*-R\d+\.\s*(.*?)\*\*`)

// key returns the key an item is compared under across snapshots.
func (it *Item) key() string { return it.Kind + "\x00" + it.ID }

// Snapshot is the specification tree at one revision.
type Snapshot struct {
	// Pages maps each code and alias to its page, and ByPath each path.
	Pages  map[string]PageInfo
	ByPath map[string]PageInfo
	// Items maps each item's key to the item.
	Items map[string]*Item
	// Retired maps each retired identifier, and its anchor, to the
	// retirement record.
	Retired map[string]ref.Retired
}

// newSnapshot returns an empty snapshot.
func newSnapshot() *Snapshot {
	return &Snapshot{Pages: map[string]PageInfo{}, ByPath: map[string]PageInfo{}, Items: map[string]*Item{}, Retired: map[string]ref.Retired{}}
}

// sortedItems returns the items of one kind in identifier order.
func (s *Snapshot) sortedItems(kind string) []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].ID, out[j].ID) })
	return out
}

// find returns the item a query names: an identifier, or an anchor in
// any case.
func (s *Snapshot) find(query string) *Item {
	for _, kind := range []string{KindRequirement, KindOperation, KindBlock} {
		if it, ok := s.Items[kind+"\x00"+query]; ok {
			return it
		}
	}
	lower := strings.ToLower(query)
	for _, it := range s.Items {
		if it.Anchor == lower || strings.EqualFold(it.ID, query) {
			return it
		}
	}
	return nil
}

// retirement returns the retirement record of an identifier or anchor.
func (s *Snapshot) retirement(query string) (ref.Retired, bool) {
	if r, ok := s.Retired[query]; ok {
		return r, true
	}
	r, ok := s.Retired[strings.ToLower(query)]
	return r, ok
}

// worktreeSnapshot reads the specification tree of the working tree.
func worktreeSnapshot(ctx context.Context, r repo) (*Snapshot, error) {
	list := func(ctx context.Context) ([]string, error) { return r.worktreeFiles(ctx, "spec") }
	return loadSnapshot(ctx, r.root, list)
}

// revSnapshot reads the specification tree at a commit. An empty commit
// reads as an empty tree.
func revSnapshot(ctx context.Context, r repo, rev, scratch string) (*Snapshot, error) {
	if rev == "" {
		return newSnapshot(), nil
	}
	dir, err := r.extract(ctx, rev, "spec", scratch)
	if err != nil {
		return nil, err
	}
	return loadSnapshot(ctx, dir, scope.DirLister(dir))
}

// loadSnapshot builds the reference pass's registry over the tree at root
// and reads each registered item's text from its page.
func loadSnapshot(ctx context.Context, root string, list scope.Lister) (*Snapshot, error) {
	t := &ref.Tree{Root: root, List: list}
	committed, err := ref.LoadCommitted(t)
	if err != nil {
		return nil, err
	}
	reg, _, err := ref.Build(ctx, t, committed)
	if err != nil {
		return nil, err
	}
	s := newSnapshot()
	contents := map[string]string{}
	read := func(p string) string {
		if c, ok := contents[p]; ok {
			return c
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			contents[p] = ""
			return ""
		}
		contents[p] = string(b)
		return contents[p]
	}
	for _, p := range reg.Pages {
		info := PageInfo{Code: p.Code, Path: p.Path, Type: p.Type, Status: p.Status}
		if page, _ := defs.ParsePage(p.Path, read(p.Path)); page != nil {
			info.Area, info.Title, info.Summary = page.Front.Area, page.Title, strings.TrimSpace(page.Front.Summary)
		}
		s.Pages[p.Code] = info
		for _, a := range p.Aliases {
			s.Pages[a] = info
		}
		s.ByPath[p.Path] = info
	}
	codeOf := func(file string) string { return s.ByPath[file].Code }
	add := func(it *Item) { s.Items[it.key()] = it }
	for _, id := range reg.Requirements {
		add(&Item{Kind: KindRequirement, ID: id.ID, Anchor: id.Anchor, File: id.File, Code: codeOf(id.File), Text: anchoredText(read(id.File), id.Anchor)})
	}
	for _, id := range reg.Operations {
		add(&Item{Kind: KindOperation, ID: id.ID, Anchor: id.Anchor, File: id.File, Code: codeOf(id.File), Text: anchoredText(read(id.File), id.Anchor)})
	}
	for _, b := range reg.Blocks {
		add(&Item{Kind: KindBlock, ID: b.Anchor, Anchor: b.Anchor, File: b.File, Code: codeOf(b.File), Name: b.Name, BlockKind: b.Kind, Text: blockText(read(b.File), b.Anchor)})
	}
	for _, rt := range reg.Retired {
		s.Retired[rt.ID] = rt
		s.Retired[rt.Anchor] = rt
	}
	return s, nil
}

var (
	// anchorTagExpr matches an explicit anchor tag.
	anchorTagExpr = regexp.MustCompile(`<a\s+id="([^"]+)"\s*(?:/>|>\s*</a>)`)
	// anchorStartExpr matches a line that opens with an explicit anchor.
	anchorStartExpr = regexp.MustCompile(`^\s*<a\s+id="`)
	// headingLineExpr matches a Markdown heading and captures its marks.
	headingLineExpr = regexp.MustCompile(`^(#{1,6})[ \t]`)
	// fenceLineExpr matches a fence and captures its marker and info.
	fenceLineExpr = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")
)

// headingLevel returns the level of a heading line, or 0.
func headingLevel(l string) int {
	if m := headingLineExpr.FindStringSubmatch(l); m != nil {
		return len(m[1])
	}
	return 0
}

// nextNonBlank returns the index of the first non-blank line at or after
// i, or len(lines).
func nextNonBlank(ls []string, i int) int {
	for ; i < len(ls); i++ {
		if strings.TrimSpace(ls[i]) != "" {
			return i
		}
	}
	return len(ls)
}

// anchoredText returns the text an explicit anchor marks.
//
// An anchor written inline, as a requirement's is, marks its paragraph
// and the lines that continue it, up to the next heading, the next line
// that opens with an anchor, or the next definition block. An anchor on
// a line of its own above a heading, as an operation's usually is, marks
// that heading's section, up to the next heading of the same or a higher
// level.
func anchoredText(content, anchor string) string {
	ls := strings.Split(content, "\n")
	start := -1
	for i, l := range ls {
		for _, m := range anchorTagExpr.FindAllStringSubmatch(l, -1) {
			if m[1] == anchor {
				start = i
				break
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return ""
	}
	sectionLevel := 0
	headingAt := -1
	if strings.TrimSpace(anchorTagExpr.ReplaceAllString(ls[start], "")) == "" {
		if j := nextNonBlank(ls, start+1); j < len(ls) {
			if lv := headingLevel(ls[j]); lv > 0 {
				sectionLevel, headingAt = lv, j
			}
		}
	}
	out := []string{ls[start]}
	inFence := ""
	for j := start + 1; j < len(ls); j++ {
		l := ls[j]
		if inFence != "" {
			out = append(out, l)
			if t := strings.TrimSpace(l); strings.HasPrefix(t, inFence) && strings.Trim(t, inFence[:1]) == "" {
				inFence = ""
			}
			continue
		}
		if m := fenceLineExpr.FindStringSubmatch(l); m != nil {
			if m[2] == "lenny-def" && sectionLevel == 0 {
				break
			}
			inFence = m[1]
			out = append(out, l)
			continue
		}
		if lv := headingLevel(l); lv > 0 && j != headingAt {
			if sectionLevel == 0 || lv <= sectionLevel {
				break
			}
		}
		if anchorStartExpr.MatchString(l) {
			if sectionLevel == 0 {
				break
			}
			if k := nextNonBlank(ls, j+1); k < len(ls) && strings.TrimSpace(anchorTagExpr.ReplaceAllString(l, "")) == "" {
				if lv := headingLevel(ls[k]); lv > 0 && lv <= sectionLevel {
					break
				}
			}
		}
		out = append(out, l)
	}
	return normalize(strings.Join(out, "\n"))
}

// blockText returns the body of the definition block an anchor names,
// read as the reference pass reads it: the anchor is the block's kind and
// the slug of its name, or of its key when it has no name.
func blockText(content, anchor string) string {
	ls := strings.Split(content, "\n")
	for i := 0; i < len(ls); i++ {
		m := fenceLineExpr.FindStringSubmatch(ls[i])
		if m == nil {
			continue
		}
		end := i + 1
		for end < len(ls) {
			t := strings.TrimSpace(ls[end])
			if strings.HasPrefix(t, m[1]) && strings.Trim(t, m[1][:1]) == "" {
				break
			}
			end++
		}
		body := ls[i+1 : min(end, len(ls))]
		if m[2] == "lenny-def" && blockAnchor(body) == anchor {
			return normalize(strings.Join(body, "\n"))
		}
		i = end
	}
	return ""
}

// blockAnchor returns the anchor of a block body: `<kind>-<slug>`.
func blockAnchor(body []string) string {
	var kind, name, key string
	for _, l := range body {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "kind":
			kind = v
		case "name":
			name = v
		case "key":
			key = v
		}
	}
	if name == "" {
		name = key
	}
	return kind + "-" + defs.Slug(name)
}

// normalize trims trailing space from each line and blank lines from both
// ends, so a change of whitespace alone is not read as a change of text.
func normalize(text string) string {
	ls := strings.Split(text, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Trim(strings.Join(ls, "\n"), "\n")
}

// itemTextIn returns the text of an item in one version of its page.
func itemTextIn(content string, it *Item) string {
	if it.Kind == KindBlock {
		return blockText(content, it.Anchor)
	}
	return anchoredText(content, it.Anchor)
}

// naturalLess orders strings with their digit runs compared as numbers,
// so POOL-R2 sorts before POOL-R10.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
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

func digitRun(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// pageLabel renders a page for a report: its code and path.
func pageLabel(p PageInfo) string {
	if p.Code == "" {
		return "(no page)"
	}
	return fmt.Sprintf("`%s` (%s)", p.Code, p.Path)
}

// specPage reports whether a repository path is a page of the new tree.
func specPage(p string) bool {
	return strings.HasPrefix(p, "spec/") && strings.HasSuffix(p, ".md") && !numberedFile.MatchString(path.Base(p))
}

// numberedFile matches the base name of a numbered file.
var numberedFile = regexp.MustCompile(`^\d{2}_.*\.md$`)
