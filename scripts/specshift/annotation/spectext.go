// SPDX-License-Identifier: MIT

package annotation

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"unicode"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// specDir holds the specification.
const specDir = "spec/"

// QuoteMinWords is the shortest quoted span, in words, that the lint
// compares with the specification. A shorter span is a term or a value
// rather than a quotation.
const QuoteMinWords = 5

// RunMinWords is the shortest run of words, without quotation marks,
// that the lint reads as copied specification text when the run appears
// in the specification word for word.
const RunMinWords = 12

// SpecText is the specification's prose, normalized to lower-case words,
// with an index of every run of RunMinWords consecutive words.
type SpecText struct {
	text    string
	windows map[uint64]bool
	// names holds the normalized headings and bold paragraph labels.
	// Quoting one of them names a place in the specification, which a
	// section citation does, rather than copying its text.
	names map[string]bool
}

// LoadSpecText reads every Markdown page under spec/.
func LoadSpecText(ctx context.Context, list scope.Lister, read scope.FileReader) (*SpecText, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the specification: %w", err)
	}
	var pages []string
	for _, p := range all {
		if strings.HasPrefix(p, specDir) && strings.HasSuffix(p, ".md") {
			src, err := read(p)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", p, err)
			}
			pages = append(pages, string(src))
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("the tree carries no specification page under %s", specDir)
	}
	return NewSpecText(pages...), nil
}

// NewSpecText indexes the given pages.
func NewSpecText(pages ...string) *SpecText {
	s := &SpecText{windows: map[uint64]bool{}, names: map[string]bool{}}
	var b strings.Builder
	b.WriteByte(' ')
	for _, page := range pages {
		for _, m := range specName.FindAllStringSubmatch(page, -1) {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			s.names[strings.Join(Words(sectionNumber.ReplaceAllString(name, "")), " ")] = true
		}
		words := Words(stripMarkup(page))
		for i := 0; i+RunMinWords <= len(words); i++ {
			s.windows[hashWords(words[i:i+RunMinWords])] = true
		}
		b.WriteString(strings.Join(words, " "))
		b.WriteString(" | ")
	}
	s.text = b.String()
	return s
}

// ContainsQuote reports whether the span, normalized, appears in the
// specification word for word. A span that is a heading or a bold
// paragraph label is a name rather than a quotation, and is not
// reported.
func (s *SpecText) ContainsQuote(span string) bool {
	words := Words(span)
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, " ")
	if s.names[joined] {
		return false
	}
	return strings.Contains(s.text, " "+joined+" ")
}

// specName matches a Markdown heading and a bold paragraph label.
var specName = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$|\*\*([^*\n]+)\*\*`)

// sectionNumber matches the section number that opens a numbered
// heading.
var sectionNumber = regexp.MustCompile(`^\s*\d+(?:\.\d+)*\.?\s*`)

// CopiedRun returns the first run of RunMinWords words of the text that
// appears in the specification word for word, and whether there is one.
func (s *SpecText) CopiedRun(text string) (string, bool) {
	words := Words(text)
	for i := 0; i+RunMinWords <= len(words); i++ {
		if s.windows[hashWords(words[i:i+RunMinWords])] {
			return strings.Join(words[i:i+RunMinWords], " "), true
		}
	}
	return "", false
}

// markup matches the parts of a Markdown page that are not prose: a link
// target and an HTML tag such as an anchor.
var markup = regexp.MustCompile(`\]\([^)]*\)|<[^>]+>`)

// stripMarkup removes link targets and tags, so the words of a link's
// text run on into the words around it.
func stripMarkup(page string) string { return markup.ReplaceAllString(page, "]") }

// Words lower-cases the text and splits it into runs of letters and
// digits, so punctuation, Markdown emphasis, and comment wrapping do not
// change a comparison.
func Words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// hashWords hashes a run of words.
func hashWords(words []string) uint64 {
	h := fnv.New64a()
	for _, w := range words {
		_, _ = h.Write([]byte(w))
		_, _ = h.Write([]byte{' '})
	}
	return h.Sum64()
}

// quoted matches a span inside straight or typographic double quotation
// marks.
var quoted = regexp.MustCompile(`"([^"]+)"|“([^”]+)”`)

// QuotedSpans returns the spans of text inside quotation marks that are
// at least QuoteMinWords words long.
func QuotedSpans(text string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(text, -1) {
		span := m[1]
		if span == "" {
			span = m[2]
		}
		if len(Words(span)) >= QuoteMinWords {
			out = append(out, span)
		}
	}
	return out
}
