// SPDX-License-Identifier: MIT

package docgate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
)

// The kinds of name the catalog-name gate reads.
const (
	kindSetting = "setting"
	kindMetric  = "metric"
	kindAlert   = "alert"
	kindError   = "error code"
	kindLabel   = "label or annotation"
	kindEvent   = "event type"
	kindCLI     = "CLI command"
)

// cliDir is the folder of the specification pages whose operations are CLI
// commands, named by their command words.
const cliDir = defs.SpecDir + "/interfaces/cli/"

// Catalog is every name the definition blocks define, and the text of the
// numbered sections that have not migrated yet.
type Catalog struct {
	// names maps each block name to its blocks.
	names map[string][]*defs.Block
	// commands are the command words of the CLI operations.
	commands map[string]bool
	// numbered is the text of the numbered files, which still define the
	// names of the areas that have not migrated.
	numbered string
}

// NewCatalog builds the catalog from a loaded tree and the numbered files
// under root.
func NewCatalog(root string, t *defs.Tree) (*Catalog, error) {
	c := &Catalog{names: map[string][]*defs.Block{}, commands: map[string]bool{}}
	for _, b := range t.Blocks() {
		if b.Name == "" {
			continue
		}
		if b.Kind == defs.KindOperation {
			if strings.HasPrefix(b.Page.Path, cliDir) {
				c.commands[strings.Join(strings.Fields(b.Name), " ")] = true
			}
			continue
		}
		c.names[b.Name] = append(c.names[b.Name], b)
	}
	matches, err := filepath.Glob(filepath.Join(root, defs.SpecDir, "[0-9][0-9]_*.md"))
	if err != nil {
		return nil, fmt.Errorf("list the numbered files: %w", err)
	}
	sort.Strings(matches)
	var sb strings.Builder
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", m, err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	c.numbered = sb.String()
	return c, nil
}

// inNumbered reports whether the numbered sections name a string, bounded
// on both sides by a character that cannot continue a name.
func (c *Catalog) inNumbered(name string) bool {
	if name == "" {
		return false
	}
	text := c.numbered
	for {
		i := strings.Index(text, name)
		if i < 0 {
			return false
		}
		before, after := byte(' '), byte(' ')
		if i > 0 {
			before = text[i-1]
		}
		if j := i + len(name); j < len(text) {
			after = text[j]
		}
		if !nameByte(before) && !nameByte(after) {
			return true
		}
		text = text[i+1:]
	}
}

func nameByte(b byte) bool {
	return b == '_' || b == '.' || b == '-' || b == '/' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// histogramSuffixes are the series suffixes of a histogram or a summary.
var histogramSuffixes = []string{"_bucket", "_sum", "_count"}

// lookup returns the blocks a name resolves to: the blocks of that name,
// or for a histogram or summary series the metric it belongs to.
func (c *Catalog) lookup(name string) []*defs.Block {
	if bs := c.names[name]; len(bs) > 0 {
		return bs
	}
	for _, s := range histogramSuffixes {
		base, ok := strings.CutSuffix(name, s)
		if !ok {
			continue
		}
		for _, b := range c.names[base] {
			if t := b.Text("type"); b.Kind == defs.KindMetric && (t == "histogram" || t == "summary") {
				return []*defs.Block{b}
			}
		}
	}
	return nil
}

// command reports whether a lenny-ctl invocation names a catalog command:
// some leading run of its command words is a CLI operation's name.
func (c *Catalog) command(words []string) bool {
	for k := len(words); k > 0; k-- {
		if c.commands[strings.Join(words[:k], " ")] {
			return true
		}
	}
	return false
}

// numberedCommand reports whether the numbered sections name a lenny-ctl
// invocation's command: some leading run of at least two of its words, or
// its one word, follows lenny-ctl there. A trailing word may be a
// positional argument rather than a command word, so a shorter run counts.
func (c *Catalog) numberedCommand(words []string) bool {
	for k := len(words); k >= min(2, len(words)) && k > 0; k-- {
		if c.inNumbered("lenny-ctl " + strings.Join(words[:k], " ")) {
			return true
		}
	}
	return false
}

var (
	metricForm  = regexp.MustCompile(`^lenny_[a-z0-9]+(?:_[a-z0-9]+)+$`)
	labelForm   = regexp.MustCompile(`^(?:[a-z0-9-]+\.)*lenny\.dev/[A-Za-z0-9._/-]+$`)
	eventForm   = regexp.MustCompile(`^dev\.lenny\.[a-z0-9_.]+$`)
	cliForm     = regexp.MustCompile(`^lenny-ctl\s+(.*)$`)
	commandWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

	// The forms a span takes when only its context fixes its kind.
	weakForms = map[string]*regexp.Regexp{
		kindSetting: regexp.MustCompile(`^(?:[a-z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)*|--[a-z][a-z0-9-]*)$`),
		kindMetric:  regexp.MustCompile(`^[a-z][a-z0-9_]+$`),
		kindAlert:   regexp.MustCompile(`^[A-Z][A-Za-z0-9]+$`),
		kindError:   regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+$`),
		kindLabel:   regexp.MustCompile(`^[a-z0-9.-]+/[A-Za-z0-9._-]+$`),
		kindEvent:   regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$`),
	}

	// contextWords maps the word beside a span to the kind it names.
	contextWords = map[string]string{
		"setting": kindSetting, "field": kindSetting, "flag": kindSetting, "parameter": kindSetting,
		"metric": kindMetric, "alert": kindAlert, "error": kindError, "code": kindError,
		"label": kindLabel, "annotation": kindLabel, "event": kindEvent,
	}
	wordAfter  = regexp.MustCompile(`^\s+([A-Za-z]+)`)
	wordBefore = regexp.MustCompile(`([A-Za-z]+)\s+$`)

	// headerKinds maps a table column header to the kind its cells name.
	headerKinds = map[string]string{
		"setting": kindSetting, "field": kindSetting, "parameter": kindSetting, "flag": kindSetting, "key": kindSetting,
		"metric": kindMetric, "alert": kindAlert, "code": kindError, "error": kindError, "error code": kindError,
		"label": kindLabel, "annotation": kindLabel, "event": kindEvent, "event type": kindEvent,
	}
)

// singular strips a plural s from a context word.
func singular(w string) string {
	w = strings.ToLower(w)
	if len(w) > 3 && strings.HasSuffix(w, "s") {
		return w[:len(w)-1]
	}
	return w
}

// Span is one code span of a page, with what surrounds it.
type Span struct {
	// Text is the span's content, trimmed.
	Text string
	// Line is the file line.
	Line int
	// Before and After are the text of the line or the table cell on
	// either side of the span.
	Before, After string
	// Header is the header of the table column that holds the span, or
	// empty in prose.
	Header string
	// Column is the index of the table column that holds the span, or -1
	// in prose.
	Column int
	// Row maps each column header of the span's table row to its cell,
	// or is nil in prose.
	Row map[string]string
}

// classify returns the kind a span names, the name it names, and whether
// its form alone fixes the kind. An empty kind means the gate reads the
// span as naming nothing it checks.
func classify(s Span) (kind, name string, strong bool) {
	text := s.Text
	if m := cliForm.FindStringSubmatch(text); m != nil {
		return kindCLI, text, true
	}
	if i := strings.IndexAny(text, "{="); i > 0 {
		text = text[:i]
	}
	switch {
	case metricForm.MatchString(text):
		return kindMetric, text, true
	case labelForm.MatchString(text):
		return kindLabel, text, true
	case eventForm.MatchString(text):
		return kindEvent, text, true
	}
	hint := headerKinds[strings.ToLower(strings.Trim(s.Header, "` "))]
	if m := wordAfter.FindStringSubmatch(s.After); hint == "" && m != nil {
		hint = contextWords[singular(m[1])]
	}
	if m := wordBefore.FindStringSubmatch(s.Before); hint == "" && m != nil {
		hint = contextWords[singular(m[1])]
	}
	if hint != "" && weakForms[hint].MatchString(text) {
		return hint, text, false
	}
	return "", text, false
}

// commandWords returns the command words of a lenny-ctl invocation: the
// words after lenny-ctl up to the first flag, placeholder, or value.
func commandWords(invocation string) []string {
	m := cliForm.FindStringSubmatch(invocation)
	if m == nil {
		return nil
	}
	var words []string
	for _, w := range strings.Fields(m[1]) {
		if !commandWord.MatchString(w) {
			break
		}
		words = append(words, w)
	}
	return words
}

// Result is what the catalog-name gate found on the pages in scope.
type Result struct {
	// Problems fail the gate.
	Problems []Problem
	// Undecided are the names and values the gate could not decide.
	Undecided []Problem
	// Pages is the number of pages the gate read.
	Pages int
}

// CheckNames runs the catalog-name gate over the pages in scope.
func CheckNames(pages []*Page, st Statuses, c *Catalog) Result {
	var r Result
	for _, p := range pages {
		if !p.InScope(st) {
			continue
		}
		r.Pages++
		for _, s := range Spans(p) {
			checkSpan(&r, p, s, c)
		}
	}
	sortProblems(r.Problems)
	sortProblems(r.Undecided)
	return r
}

// checkSpan decides one span.
func checkSpan(r *Result, p *Page, s Span, c *Catalog) {
	kind, name, strong := classify(s)
	if kind == "" {
		// A name the catalogs define is checked whatever its form, so a
		// value quoted beside it is compared too.
		if bs := c.lookup(name); len(bs) > 0 {
			checkValues(r, p, s, bs)
		}
		return
	}
	at := func(list *[]Problem, format string, args ...any) {
		*list = append(*list, Problem{Path: p.Path, Line: s.Line, Message: fmt.Sprintf(format, args...)})
	}
	if kind == kindCLI {
		words := commandWords(name)
		switch {
		case len(words) == 0 || c.command(words):
		case c.numberedCommand(words):
			at(&r.Undecided, "the %s `%s` is not in the catalogs yet; a numbered section of the specification still defines it", kind, name)
		default:
			at(&r.Problems, "the %s `%s` is not in the catalogs, and no section of the specification defines it", kind, name)
		}
		return
	}
	if bs := c.lookup(name); len(bs) > 0 {
		checkValues(r, p, s, bs)
		return
	}
	switch {
	case c.inNumbered(name) || c.inNumbered(shortEventType(kind, name)):
		at(&r.Undecided, "the %s `%s` is not in the catalogs yet; a numbered section of the specification still defines it", kind, name)
	case strong:
		at(&r.Problems, "the %s `%s` is not in the catalogs, and no section of the specification defines it", kind, name)
	default:
		at(&r.Undecided, "`%s` reads as %s from its context, and neither the catalogs nor a numbered section define it", name, article(kind))
	}
}

// article returns a kind with its indefinite article.
func article(kind string) string {
	if strings.ContainsRune("aeiou", rune(kind[0])) {
		return "an " + kind
	}
	return "a " + kind
}

// shortEventType returns an event type without the dev.lenny. prefix, the
// form the numbered sections name most event types in, or "" for any
// other name.
func shortEventType(kind, name string) string {
	if kind != kindEvent {
		return ""
	}
	short, ok := strings.CutPrefix(name, "dev.lenny.")
	if !ok {
		return ""
	}
	return short
}

// sortProblems orders findings by path and line.
func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Path != ps[j].Path {
			return ps[i].Path < ps[j].Path
		}
		return ps[i].Line < ps[j].Line
	})
}
