// SPDX-License-Identifier: MIT

package defs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DocsReferenceDir is the repo-relative folder of the documentation's
// reference pages.
const DocsReferenceDir = "docs/reference"

// The generated regions of the documentation's reference pages. Each one
// lists, for readers, the catalog entries of one kind whose owner page has
// landed.
const (
	RegionRefSettings = "reference-settings"
	RegionRefMetrics  = "reference-metrics"
	RegionRefAlerts   = "reference-alerts"
	RegionRefErrors   = "reference-errors"
	RegionRefEvents   = "reference-events"
	RegionRefStates   = "reference-state-machines"
	RegionRefGlossary = "reference-glossary"
)

// referenceRegionNames are the region names a reference page's marker may
// carry.
var referenceRegionNames = []string{
	RegionRefSettings, RegionRefMetrics, RegionRefAlerts, RegionRefErrors,
	RegionRefEvents, RegionRefStates, RegionRefGlossary,
}

// referencePages maps each reference page that lists catalog entries to
// the regions it carries.
var referencePages = map[string][]string{
	"configuration.md":       {RegionRefSettings},
	"metrics.md":             {RegionRefMetrics, RegionRefAlerts},
	"error-catalog.md":       {RegionRefErrors},
	"cloudevents-catalog.md": {RegionRefEvents},
	"state-machines.md":      {RegionRefStates},
	"glossary.md":            {RegionRefGlossary},
}

// RefPage is one page of the documentation's reference section.
type RefPage struct {
	// Path is the repo-relative path, such as docs/reference/metrics.md.
	Path string
	// Content is the whole file.
	Content string
}

// loadRefPages reads the Markdown files directly under the reference
// folder. A tree with no documentation has none.
func loadRefPages(root string) ([]*RefPage, error) {
	matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(DocsReferenceDir), "*.md"))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", DocsReferenceDir, err)
	}
	sort.Strings(matches)
	out := make([]*RefPage, 0, len(matches))
	for _, m := range matches {
		content, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", m, err)
		}
		out = append(out, &RefPage{Path: path.Join(DocsReferenceDir, filepath.Base(m)), Content: string(content)})
	}
	return out, nil
}

// checkRefPages reports malformed markers on every reference page and a
// reference page that lacks a region it carries.
func checkRefPages(t *Tree) []Problem {
	var problems []Problem
	for _, rp := range t.RefPages {
		regions, ps := findNamedRegions(rp.Path, rp.Content, 1, referenceRegionNames)
		problems = append(problems, ps...)
		have := map[string]bool{}
		for _, r := range regions {
			have[r.name] = true
		}
		for _, name := range referencePages[path.Base(rp.Path)] {
			if !have[name] {
				problems = append(problems, Problem{Path: rp.Path, Message: fmt.Sprintf(
					"the reference page needs the generated region <!-- generated:%s --> ... <!-- /generated:%s --> after its hand-written list", name, name,
				)})
			}
		}
	}
	return problems
}

// generateRefPages fills the regions of every reference page that carries
// them.
func generateRefPages(t *Tree, out map[string]string) {
	for _, rp := range t.RefPages {
		regions, _ := findNamedRegions(rp.Path, rp.Content, 1, referenceRegionNames)
		if len(regions) == 0 {
			continue
		}
		out[rp.Path] = replaceRegions(rp.Content, regions, func(name string) string {
			return referenceContent(t, name)
		})
	}
}

// referenceContent returns the content of one reference region: empty when
// no landed page owns an entry of its kind.
func referenceContent(t *Tree, name string) string {
	switch name {
	case RegionRefSettings:
		return refSections(t, landedBlocks(t, KindSetting), 2, "configuration", refSettingsTable)
	case RegionRefMetrics:
		return refSections(t, landedBlocks(t, KindMetric), 2, "metrics", refMetricsTable)
	case RegionRefAlerts:
		return refSections(t, landedBlocks(t, KindAlert), 3, "alerts", refAlertsTable)
	case RegionRefErrors:
		return refSections(t, landedBlocks(t, KindError), 2, "errors", refErrorsTable)
	case RegionRefEvents:
		return refSections(t, landedBlocks(t, KindEvent), 2, "events", refEventsTable)
	case RegionRefStates:
		return refStates(landedBlocks(t, KindState))
	case RegionRefGlossary:
		return refGlossary(t)
	}
	return ""
}

// landedBlocks returns the sorted blocks of the given kinds whose owner
// page has landed. The rows of an area whose pages have not landed stay
// hand-written until the area change replaces them.
func landedBlocks(t *Tree, kindNames ...string) []*Block {
	var out []*Block
	for _, b := range blocksOf(t, kindNames...) {
		if b.Page.Landed() {
			out = append(out, b)
		}
	}
	return out
}

// refGroup is one section of a reference region: the blocks one area,
// one component, or one interface owns.
type refGroup struct {
	title  string
	blocks []*Block
}

// refSections renders blocks grouped by owner, one heading and one table
// per group, with groups ordered by title. noun follows the group's title
// in its heading, as the hand-written sections of the page name theirs.
func refSections(t *Tree, blocks []*Block, level int, noun string, render func(*Tree, []*Block) table) string {
	if len(blocks) == 0 {
		return ""
	}
	byKey := map[string]*refGroup{}
	var groups []*refGroup
	for _, b := range blocks {
		key, title := groupOf(t, b.Page)
		g := byKey[key]
		if g == nil {
			g = &refGroup{title: title}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.blocks = append(g.blocks, b)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].title) < strings.ToLower(groups[j].title)
	})
	var parts []string
	for _, g := range groups {
		tb := render(t, g.blocks)
		parts = append(parts, strings.Repeat("#", level)+" "+g.title+" "+noun+"\n\n"+tb.render())
	}
	return strings.Join(parts, "\n")
}

// groupOf returns the key and the reader-facing title of the section a
// page's blocks are listed under: the page's capability area, named by the
// area's index page; for a page with no area inside a folder of its own,
// such as a gateway part, that folder's index page; and otherwise the page
// itself.
func groupOf(t *Tree, p *Page) (string, string) {
	if p.Front.Area != "" {
		var best *Page
		for _, q := range t.Pages {
			if q.Front.Type == "index" && q.Front.Area == p.Front.Area && (best == nil || len(q.Path) < len(best.Path)) {
				best = q
			}
		}
		if best != nil {
			return "area:" + p.Front.Area, indexTitle(best)
		}
		return "area:" + p.Front.Area, folderTitle(p.Front.Area)
	}
	parts := splitPath(p.Dir())
	if len(parts) >= 2 {
		folder := strings.Join(parts[:2], "/")
		if idx := t.PageByPath(path.Join(SpecDir, folder, IndexFile)); idx != nil {
			return "folder:" + folder, indexTitle(idx)
		}
	}
	return "page:" + p.Path, pageTitle(p)
}

// indexTitle returns an index page's level-one heading, or the title its
// folder's name gives when the page has none yet.
func indexTitle(p *Page) string {
	if p.Title != "" {
		return p.Title
	}
	return folderTitle(p.Dir())
}

// componentNames returns the titles of the pages a list of codes names,
// for a reader who never sees a code.
func componentNames(t *Tree, values []string) string {
	codes := t.Codes()
	out := make([]string, 0, len(values))
	for _, v := range values {
		if p, ok := codes[v]; ok {
			out = append(out, pageTitle(p))
			continue
		}
		out = append(out, v)
	}
	return strings.Join(out, ", ")
}

var (
	plainLink   = regexp.MustCompile(`!?\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\([^()\s]*(?:\s+"[^"]*")?\)`)
	plainAnchor = regexp.MustCompile(`<a\s+id="[^"]*"\s*(?:/>|>\s*</a>)`)
)

// plain returns specification prose for a documentation page: each link
// reduced to its text, because a link into the specification does not
// resolve on the documentation site, and explicit anchors removed.
func plain(s string) string {
	s = plainAnchor.ReplaceAllString(s, "")
	return strings.TrimSpace(plainLink.ReplaceAllString(s, "$1"))
}

// capitalize upper-cases the first letter of a value.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func refSettingsTable(t *Tree, blocks []*Block) table {
	tb := table{header: []string{"Field", "Type", "Default", "Unit", "Scopes", "Read by", "Description", "Validation"}}
	for _, b := range blocks {
		def := code(b.Text("default"))
		if pairs := mappingPairs(b, "defaults-by-tier"); len(pairs) > 0 {
			tiers := make([]string, 0, len(pairs))
			values := map[string]string{}
			for _, p := range pairs {
				tiers = append(tiers, p[0])
				values[p[0]] = p[1]
			}
			sortTiers(tiers)
			byTier := make([]string, 0, len(tiers))
			for _, tier := range tiers {
				byTier = append(byTier, code(values[tier])+" ("+tier+")")
			}
			def += "; by capacity tier: " + strings.Join(byTier, ", ")
		}
		tb.add(code(b.Name), b.Text("type"), def, b.Text("unit"), b.Text("scopes"),
			componentNames(t, b.List("read-by")), plain(b.Text("summary")), settingValidation(b))
	}
	return tb
}

// settingValidation states a setting's accepted range or values.
func settingValidation(b *Block) string {
	if b.Has("values") {
		return "One of " + codes(b.List("values")) + "."
	}
	n := b.fields["range"]
	if n == nil {
		return ""
	}
	if n.Kind == yaml.SequenceNode && len(n.Content) == 2 {
		return "From " + code(nodeText(n.Content[0])) + " to " + code(nodeText(n.Content[1])) + "."
	}
	return plain(nodeText(n))
}

func refMetricsTable(t *Tree, blocks []*Block) table {
	tb := table{header: []string{"Metric", "Type", "Labels", "Unit", "Emitted by", "Description"}}
	for _, b := range blocks {
		labels := codes(b.List("labels"))
		if labels == "" {
			labels = "none"
		}
		tb.add(code(b.Name), capitalize(b.Text("type")), labels, b.Text("unit"),
			componentNames(t, b.List("emitter")), plain(b.Text("summary")))
	}
	return tb
}

func refAlertsTable(_ *Tree, blocks []*Block) table {
	tb := table{header: []string{"Alert", "Expression / Condition", "For", "Severity", "Description"}}
	for _, b := range blocks {
		tb.add(code(b.Name), plain(b.Text("expr")), b.Text("for"), capitalize(b.Text("severity")), plain(b.Text("summary")))
	}
	return tb
}

func refErrorsTable(_ *Tree, blocks []*Block) table {
	tb := table{header: []string{"Code", "HTTP", "Category", "Retryable", "Description"}}
	for _, b := range blocks {
		retryable := ""
		if b.Has("retryable") {
			retryable = "No"
			if b.Text("retryable") == "true" {
				retryable = "Yes"
			}
		}
		desc := plain(b.Text("summary"))
		if msg := b.Text("message"); msg != "" {
			desc += " Message: " + code(msg) + "."
		}
		tb.add(code(b.Name), b.Text("status"), b.Text("category"), retryable, desc)
	}
	return tb
}

func refEventsTable(t *Tree, blocks []*Block) table {
	tb := table{header: []string{"`type`", "Description", "`data`", "Emitted by"}}
	for _, b := range blocks {
		tb.add(code(b.Name), plain(b.Text("summary")), plain(b.Text("payload")), componentNames(t, b.List("emitter")))
	}
	return tb
}

// refStates renders one section per state machine: its summary, its
// states, and its transitions.
func refStates(blocks []*Block) string {
	var parts []string
	for _, b := range blocks {
		var sb strings.Builder
		sb.WriteString("## " + capitalize(b.Name) + "\n\n")
		sb.WriteString(plain(b.Text("summary")) + " The states belong to the " + b.Text("object") + ".\n\n")
		sb.WriteString("States, in lifecycle order: " + codes(b.List("states")) + ".")
		if initial := b.Text("initial"); initial != "" {
			sb.WriteString(" A new " + b.Text("object") + " starts in " + code(initial) + ".")
		}
		if terminal := b.List("terminal"); len(terminal) > 0 {
			sb.WriteString(" Terminal states: " + codes(terminal) + ".")
		}
		sb.WriteString("\n\n")
		tb := table{header: []string{"From", "To", "Trigger"}}
		if n := b.fields["transitions"]; n != nil && n.Kind == yaml.SequenceNode {
			for _, tr := range n.Content {
				tb.add(codes(listOf(mappingValue(tr, "from"))), code(nodeText(mappingValue(tr, "to"))), plain(nodeText(mappingValue(tr, "on"))))
			}
		}
		sb.WriteString(tb.render())
		parts = append(parts, sb.String())
	}
	return strings.Join(parts, "\n")
}

// refGlossary renders the owned terms of the landed pages alphabetically,
// under one heading per initial letter, in the layout of the hand-written
// glossary: a heading per term with its anchor, then its definition.
func refGlossary(t *Tree) string {
	var terms []Term
	for _, p := range t.Pages {
		if p.Landed() {
			terms = append(terms, p.Terms...)
		}
	}
	if len(terms) == 0 {
		return ""
	}
	sort.SliceStable(terms, func(i, j int) bool {
		a, b := strings.ToLower(terms[i].Term), strings.ToLower(terms[j].Term)
		if a != b {
			return a < b
		}
		return terms[i].Anchor < terms[j].Anchor
	})
	var sb strings.Builder
	letter := ""
	for i, term := range terms {
		if term.Term == "" {
			continue
		}
		first := strings.ToUpper(term.Term[:1])
		if first != letter {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("## " + first + "\n")
			letter = first
		}
		sb.WriteString("\n### " + term.Term + "\n{: #" + term.Anchor + " }\n\n" + plain(term.Definition) + "\n")
	}
	return sb.String()
}
