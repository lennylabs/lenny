// SPDX-License-Identifier: MIT

package anchor

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// siteKind names which of the two reference forms a site carries. The
// forms are driven by different registers: a link names the retired
// anchor, which the map decides on its own, and a bare citation names
// the retired section, whose destination the map cannot decide because
// a reduction carves material out of the anchor it moves.
type siteKind string

const (
	// linkSite is an intra-repo markdown fragment link, in the
	// file-qualified `[...](NN_file.md#anchor)` form or the same-page
	// `[...](#anchor)` form.
	linkSite siteKind = "link"
	// citationSite is a bare section citation of the §X.Y form, in a
	// comment or in prose.
	citationSite siteKind = "citation"
)

// site is one reference the pass reads: the byte span it replaces, the
// line it sits on, and what it names.
type site struct {
	kind    siteKind
	start   int
	end     int
	line    int
	anchor  string
	section string
	// file is the document a link's fragment resolves against, which is
	// the citing page for the same-page form.
	file string
	// samePage records that the link was written in the same-page form,
	// so a redirect that stays on the page keeps that form.
	samePage bool
	// mapped records that the anchor-move map carries the redirect for
	// what the site names. A site the map does not carry names a
	// destination no heading of the tree declares, which the pass reports
	// rather than leaving in place.
	mapped bool
}

// fragmentLinkExpr matches a markdown link with a fragment. The first
// group is the destination's path half, which is empty for the same-page
// form, and the second is the fragment.
var fragmentLinkExpr = regexp.MustCompile(`\]\(([^)\s#]*)#([A-Za-z0-9._-]+)\)`)

// bareCitationExpr matches a bare section citation, capturing the whole
// dotted number so a citation of a deeper subsection is read as naming
// that subsection rather than as naming its parent.
var bareCitationExpr = regexp.MustCompile(`§(\d+(?:\.\d+)*)`)

// findSites returns every reference one file carries that names a
// retired anchor or a retired section, in source order.
//
// The heading index decides what is retired, rather than the
// anchor-move map: a reference whose destination the tree still declares
// is one the reduction left alone and stands, and every other reference
// names a destination that is gone. A reference the map carries is
// redirected, and a reference it does not carry is a site all the same,
// so the pass reports it rather than passing over a reference into a
// destination that resolves nowhere.
//
// A link whose destination is not a tracked markdown document of the
// tree is not a site: an absolute URL and a link into a file the
// repository does not carry are both outside the population the
// fragment-link gate reads, and rewriting one would edit a reference the
// pass cannot check.
func findSites(target, text string, moves *moveMap, tree *headings) []site {
	var out []site
	for _, m := range fragmentLinkExpr.FindAllStringSubmatchIndex(text, -1) {
		destination, anchor := text[m[2]:m[3]], text[m[4]:m[5]]
		if strings.Contains(destination, "://") {
			continue
		}
		file := target
		if destination != "" {
			file = path.Join(path.Dir(target), destination)
		}
		if !tree.carries(file) {
			continue
		}
		_, mapped := moves.anchor(anchor)
		if !mapped && tree.declaresAnchor(file, anchor) {
			continue
		}
		out = append(out, site{
			kind:     linkSite,
			start:    m[2],
			end:      m[5],
			line:     lineOf(text, m[2]),
			anchor:   anchor,
			file:     file,
			samePage: destination == "",
			mapped:   mapped,
		})
	}
	for _, m := range bareCitationExpr.FindAllStringSubmatchIndex(text, -1) {
		number := text[m[2]:m[3]]
		_, mapped := moves.section(number)
		if !mapped && tree.declaresSection(number) {
			continue
		}
		out = append(out, site{
			kind:    citationSite,
			start:   m[0],
			end:     m[1],
			line:    lineOf(text, m[0]),
			section: number,
			mapped:  mapped,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// lineOf returns the 1-based line the offset sits on.
func lineOf(text string, offset int) int {
	return strings.Count(text[:offset], "\n") + 1
}

// linkTarget returns the destination a redirected link is written with.
//
// A link whose successor sits on the citing page keeps the same-page
// form when it was written in it, so a page's internal links stay
// internal. Every other redirect is written as the path from the citing
// page's directory to the successor's file, which is the form the
// file-qualified links of the tree are written in.
func linkTarget(citing string, s site, successor Target) string {
	if s.samePage && successor.File == citing {
		return "#" + successor.Anchor
	}
	return relative(citing, successor.File) + "#" + successor.Anchor
}

// relative returns the path from the citing file's directory to the
// target file.
func relative(citing, target string) string {
	from := directorySegments(citing)
	to := directorySegments(target)
	shared := 0
	for shared < len(from) && shared < len(to) && from[shared] == to[shared] {
		shared++
	}
	segments := make([]string, 0, len(from)-shared+len(to)-shared+1)
	for range from[shared:] {
		segments = append(segments, "..")
	}
	segments = append(segments, to[shared:]...)
	segments = append(segments, path.Base(target))
	return path.Join(segments...)
}

// directorySegments returns the directory of a repo-relative path as its
// segments. A file at the root of the repository sits in no directory
// and yields none, so a link written from one climbs out of nothing: the
// path from a root-level carrier to spec/28_communication-channels.md is
// that path itself.
func directorySegments(target string) []string {
	dir := path.Dir(target)
	if dir == "." || dir == "" {
		return nil
	}
	return strings.Split(dir, "/")
}

// edit is one rewrite in a file, given as a byte span of the source and
// the text that replaces it.
type edit struct {
	start int
	end   int
	text  string
}

// splice writes every edit into the text. The edits arrive in source
// order and no two sites overlap, because each covers one match of one
// matcher, so the result is assembled in one forward pass.
func splice(text string, edits []edit) string {
	var out strings.Builder
	at := 0
	for _, e := range edits {
		out.WriteString(text[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.WriteString(text[at:])
	return out.String()
}
