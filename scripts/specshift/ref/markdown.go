// SPDX-License-Identifier: MIT

package ref

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontMatter is the part of a page's YAML front matter the registry
// reads. The other fields (area, summary, owns, depends-on) belong to the
// conventions and the generators rather than to the registry.
type frontMatter struct {
	Code    string            `yaml:"code"`
	Type    string            `yaml:"type"`
	Status  string            `yaml:"status"`
	Aliases []string          `yaml:"aliases"`
	Retired map[string]string `yaml:"retired"`
}

// parseFrontMatter reads the YAML front matter a page opens with. It
// returns ok false when the page carries none. A page whose front matter
// is not valid YAML returns an error, because the registry would
// otherwise skip a page that meant to declare a code.
func parseFrontMatter(content string) (fm frontMatter, ok bool, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return frontMatter{}, false, nil
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if !strings.HasSuffix(rest, "\n---") {
			return frontMatter{}, false, fmt.Errorf("the front matter has no closing ---")
		}
		end = len(rest) - len("\n---")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return frontMatter{}, false, fmt.Errorf("parse front matter: %w", err)
	}
	return fm, true, nil
}

// mask returns the content with every fenced block, HTML comment, and
// inline code span replaced by spaces. Newlines and byte offsets are
// kept, so a match on the masked text locates the same bytes in the
// original. Links, anchors, and headings written inside example text
// declare nothing and link nowhere, so every reader works on the masked
// text.
func mask(content string) string {
	b := []byte(content)
	blank := func(lo, hi int) {
		for i := lo; i < hi; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for _, f := range fences(content) {
		blank(f.start, f.end)
	}
	for _, m := range commentExpr.FindAllIndex(b, -1) {
		blank(m[0], m[1])
	}
	for _, m := range codeSpanExpr.FindAllIndex(b, -1) {
		blank(m[0], m[1])
	}
	return string(b)
}

var (
	commentExpr  = regexp.MustCompile(`(?s)<!--.*?-->`)
	codeSpanExpr = regexp.MustCompile("`[^`\n]*`")
)

// fence is one fenced block: its byte span including both delimiter
// lines, its info string, its body, and the 1-based line of its opening
// delimiter.
type fence struct {
	start, end int
	info       string
	body       string
	line       int
}

var fenceOpenExpr = regexp.MustCompile("^[ \t]{0,3}(`{3,}|~{3,})[ \t]*([^`]*?)[ \t]*$")

// fences returns the fenced blocks of a document in source order. A
// fence closes on a line of the same character at least as long as its
// opener, so a three-backtick block written inside a four-backtick
// example stays inside the example. An unclosed fence runs to the end
// of the document, as a renderer reads it.
func fences(content string) []fence {
	var out []fence
	lines := strings.SplitAfter(content, "\n")
	offset := 0
	var open *fence
	var delim string
	var body strings.Builder
	for i, l := range lines {
		text := strings.TrimRight(l, "\n")
		if open == nil {
			if m := fenceOpenExpr.FindStringSubmatch(text); m != nil {
				open = &fence{start: offset, info: strings.TrimSpace(m[2]), line: i + 1}
				delim = m[1]
				body.Reset()
			}
			offset += len(l)
			continue
		}
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, delim) && strings.Trim(trimmed, delim[:1]) == "" {
			open.end = offset + len(l)
			open.body = body.String()
			out = append(out, *open)
			open = nil
			offset += len(l)
			continue
		}
		body.WriteString(l)
		offset += len(l)
	}
	if open != nil {
		open.end = len(content)
		open.body = body.String()
		out = append(out, *open)
	}
	return out
}

// lineAt returns the 1-based line of a byte offset.
func lineAt(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
}

// link is one relative Markdown link: the 1-based line it sits on, the
// byte span of its path (the part before any fragment), the path as
// written, and its fragment. A same-page link has an empty path whose
// span sits just before the `#`.
type link struct {
	line     int
	pathLo   int
	pathHi   int
	path     string
	fragment string
}

var (
	inlineLinkExpr = regexp.MustCompile(`!?\[(?:[^\[\]]|\[[^\[\]]*\])*\]\(([^()\s]*)(?:\s+"[^"]*")?\)`)
	refDefExpr     = regexp.MustCompile(`(?m)^[ \t]{0,3}\[[^\]]+\]:[ \t]*(\S+)`)
	schemeExpr     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// links returns the relative links of a document: inline links and
// reference definitions, outside fenced blocks, comments, and code spans.
// An absolute URL, a scheme, a root-relative path, and a bare fragment
// are left out, because none of them is resolved against the tree. A
// same-page fragment is kept with an empty path, because a fragment whose
// identifier moved to another page needs a path.
func links(content string) []link {
	masked := mask(content)
	var out []link
	add := func(lo, hi int) {
		raw := content[lo:hi]
		if raw == "" || raw == "#" || strings.HasPrefix(raw, "/") || schemeExpr.MatchString(raw) {
			return
		}
		p, frag, _ := strings.Cut(raw, "#")
		out = append(out, link{
			line:     lineAt(content, lo),
			pathLo:   lo,
			pathHi:   lo + len(p),
			path:     p,
			fragment: frag,
		})
	}
	for _, m := range inlineLinkExpr.FindAllStringSubmatchIndex(masked, -1) {
		if masked[m[0]] == '!' {
			continue // an image embeds a file rather than linking a page
		}
		add(m[2], m[3])
	}
	for _, m := range refDefExpr.FindAllStringSubmatchIndex(masked, -1) {
		lo, hi := m[2], m[3]
		if strings.HasPrefix(content[lo:hi], "<") {
			continue
		}
		add(lo, hi)
	}
	return out
}

// decodePath undoes percent-encoding in a link path. A path that does
// not decode is returned as written.
func decodePath(p string) string {
	if d, err := url.PathUnescape(p); err == nil {
		return d
	}
	return p
}

// anchorExpr matches an explicit anchor, the form the conventions fix
// for identifiers and owned terms: `<a id="pool-r5"></a>`.
var anchorExpr = regexp.MustCompile(`<a\s+id="([^"]+)"\s*(?:/>|>\s*</a>)`)

// explicitAnchor is one explicit anchor with its line and the text that
// follows it, which names the identifier when the anchor marks one.
type explicitAnchor struct {
	id      string
	line    int
	context string
}

// explicitAnchors returns the explicit anchors of a document outside
// example text. The context of each anchor is the rest of its line and
// the next non-blank line, read from the original content, because the
// identifier an operation anchor marks is usually written in a code span
// on the heading that follows it.
func explicitAnchors(content string) []explicitAnchor {
	masked := mask(content)
	lines := strings.Split(content, "\n")
	var out []explicitAnchor
	for _, m := range anchorExpr.FindAllStringSubmatchIndex(masked, -1) {
		ln := lineAt(content, m[0])
		lineEnd := strings.IndexByte(content[m[1]:], '\n')
		ctx := content[m[1]:]
		if lineEnd >= 0 {
			ctx = content[m[1] : m[1]+lineEnd]
		}
		for i := ln; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "" {
				ctx += "\n" + lines[i]
				break
			}
		}
		out = append(out, explicitAnchor{id: masked[m[2]:m[3]], line: ln, context: ctx})
	}
	return out
}

var (
	headingExpr      = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	headingAttrExpr  = regexp.MustCompile(`\{:?[ \t]*#([A-Za-z0-9_.:-]+)[^}]*\}`)
	headingNumberExp = regexp.MustCompile(`^(\d+(?:\.\d+)*)\.?(?:[ \t]|$)`)
)

// heading is one Markdown heading outside example text.
type heading struct {
	level  int
	title  string
	number string
	line   int
	anchor string
	attrs  []string
}

// headings returns the headings of a document with the anchors a
// renderer gives them: the slug of the text and every `{#id}` attribute.
func headings(content string) []heading {
	var out []heading
	rawLines := strings.Split(content, "\n")
	for i, l := range strings.Split(mask(content), "\n") {
		m := headingExpr.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		raw := rawLines[i]
		title := strings.TrimSpace(headingExpr.FindStringSubmatch(raw)[2])
		h := heading{level: len(m[1]), line: i + 1}
		for _, a := range headingAttrExpr.FindAllStringSubmatch(title, -1) {
			h.attrs = append(h.attrs, a[1])
		}
		h.title = strings.TrimSpace(headingAttrExpr.ReplaceAllString(title, ""))
		if n := headingNumberExp.FindStringSubmatch(h.title); n != nil {
			h.number = n[1]
		}
		h.anchor = headingSlug(h.title)
		out = append(out, h)
	}
	return out
}

// headingSlug returns the anchor a renderer derives from a heading's
// text: lower-cased, with letters, digits, hyphens, and underscores kept,
// spaces written as hyphens, and every other character dropped. It is the
// rule the anchor pass reads the numbered files with.
func headingSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// headingAnchors returns every heading anchor of a document.
func headingAnchors(content string) map[string]bool {
	out := map[string]bool{}
	for _, h := range headings(content) {
		out[h.anchor] = true
		for _, a := range h.attrs {
			out[a] = true
		}
	}
	return out
}

// blockSlug returns the anchor slug of a definition block's name, as the
// conventions define it: letters and digits kept in lower case, every other
// run collapsed to one hyphen, and leading and trailing hyphens trimmed.
func blockSlug(name string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pending && b.Len() > 0 {
				b.WriteByte('-')
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	return b.String()
}

// defBlock is the part of a `lenny-def` block the registry reads.
type defBlock struct {
	kind, name string
	line       int
}

// defBlocks returns the `lenny-def` blocks of a document with their
// `kind:` and `name:` lines. A block that names its subject with `key:`
// rather than `name:`, as the label and annotation blocks of the migration
// spike do, is read by its key. The full block grammar belongs to the block
// parser; the registry needs only the two fields its anchor derives from.
func defBlocks(content string) []defBlock {
	var out []defBlock
	for _, f := range fences(content) {
		if f.info != "lenny-def" {
			continue
		}
		b := defBlock{line: f.line}
		key := ""
		for _, l := range strings.Split(f.body, "\n") {
			if k, v, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "kind":
					b.kind = v
				case "name":
					b.name = v
				case "key":
					key = v
				}
			}
		}
		if b.name == "" {
			b.name = key
		}
		out = append(out, b)
	}
	return out
}

// rewriteSpans replaces byte spans of content, given as [lo, hi) with
// their replacement, in one pass. The spans do not overlap.
func rewriteSpans(content string, edits []edit) string {
	var b bytes.Buffer
	pos := 0
	for _, e := range edits {
		b.WriteString(content[pos:e.lo])
		b.WriteString(e.text)
		pos = e.hi
	}
	b.WriteString(content[pos:])
	return b.String()
}

// edit replaces content[lo:hi] with text.
type edit struct {
	lo, hi int
	text   string
}
