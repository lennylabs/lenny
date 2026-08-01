// SPDX-License-Identifier: MIT

package anchor

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// siteKind names which of the reference forms a site carries. A link
// naming a retired anchor and a citation of the section a retired anchor
// addressed are the references the reduction invalidated. A link is
// resolved by the anchor-move map itself, and a citation per occurrence
// by the sense register, whose missing entry stops the run rather than
// being passed over.
type siteKind string

const (
	// linkSite is an intra-repo markdown fragment link, in the
	// file-qualified `[...](NN_file.md#anchor)` form or the same-page
	// `[...](#anchor)` form, into an anchor the anchor-move map retires.
	linkSite siteKind = "link"
	// citationSite is a bare section citation of the §X.Y form, in a
	// comment or in prose, naming a section whose anchor the anchor-move
	// map retires, or one no specification file of the tree states a
	// heading for and the map carries no successor for either.
	citationSite siteKind = "citation"
)

// site is one reference the pass reads: the byte span it replaces, the
// line it sits on, and what it names.
type site struct {
	kind  siteKind
	start int
	end   int
	line  int
	// section is the dotted number a bare citation names, which the
	// abort reports when the sense register does not record the
	// occurrence.
	section string
	// samePage records that the link was written in the same-page form,
	// so a redirect that stays on the page keeps that form.
	samePage bool
	// successor is the heading the anchor-move map redirects a link
	// site to. It is read while the site is found, because the map is
	// what carries the redirect.
	successor Target
	// unmapped records a citation of a section no specification file of
	// the tree states and the anchor-move map carries no successor for.
	// The site is unresolvable rather than rewritable, so it stops the
	// run naming the file and the line.
	unmapped bool
}

// linkExpr matches a markdown link, with the fragment its destination
// carries when it carries one. The first group is the destination's path
// half, which is empty for the same-page form, and the second is the
// fragment.
//
// Every link is matched, whether or not its destination carries a
// fragment, because the whole span of a link is the span a bare citation
// is suppressed inside. The label of a fragment-less link names a
// section as often as the label of a fragment link does, and the label
// is hand-corrected in the change that makes the reduction.
//
// The label admits a newline, so a label wrapped across lines is spanned
// whole, and the destination admits a trailing title so a link written
// with one is still read as a link.
var linkExpr = regexp.MustCompile(`\[[^\]]*\]\(([^)\s#]*)(?:#([A-Za-z0-9._-]+))?[^)]*\)`)

// bareCitationExpr matches a bare section citation, capturing the whole
// dotted number so a citation of a deeper subsection is read as naming
// that subsection rather than as naming its parent.
var bareCitationExpr = regexp.MustCompile(`§(\d+(?:\.\d+)*)`)

// findSites returns every reference one file carries that the reduction
// invalidated, in source order.
//
// The anchor-move map decides the population of the link class. The map
// states which anchors the reduction retires, so a link into one of them
// is a site and every other link stands exactly as it is written. A link
// whose anchor its destination document does not declare and the map
// does not retire either is a broken link the tree carried before the
// run, and its correction is the fragment-link gate and the hand
// enumeration that gate reports.
//
// A fragment link is read when its destination is a tracked markdown
// document of the tree, which is the population the fragment-link gate
// reads. An absolute URL and a link into a file the repository does not
// carry are outside that population, and rewriting one would judge a
// reference the pass cannot check.
//
// A bare citation is decided two-sidedly, because no gate over the
// anchor classes reads a §X.Y token: the fragment-link gate reads links
// alone, and the citation resolver and the per-file ratchet match the
// retired line-citation form alone. A citation of a section the map
// retires the anchor of is a site the sense register resolves one
// occurrence at a time, because a reduction carves material out of the
// anchor it moves. A citation of a section a specification file of the
// tree still states a heading for stands as written. A citation of a
// section neither states is a citation of a heading that is gone with no
// successor to send it to, so it stops the run naming the file and the
// line. Deciding the class by the map alone would leave such a citation
// standing while the run exited zero, and the change that empties the
// map would then destroy the record of what the run should have done.
//
// A citation written inside a markdown link is not read as a bare
// citation, whether or not that link's destination carries a fragment.
// The pass performs a target-only redirect, and a label that names the
// retiring subsection is corrected by hand in the change that makes the
// reduction, so reading the label here would both rewrite a site the
// pass does not own and shift the occurrence numbering the sense
// register is keyed by.
func findSites(target, text string, tree *headings, moves *moveMap) []site {
	var out []site
	var links []span
	for _, m := range linkExpr.FindAllStringSubmatchIndex(text, -1) {
		links = append(links, span{start: m[0], end: m[1]})
		if m[4] < 0 {
			continue
		}
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
		move, retired := moves.anchor(anchor)
		if !retired {
			continue
		}
		out = append(out, site{
			kind:      linkSite,
			start:     m[2],
			end:       m[5],
			line:      lineOf(text, m[2]),
			samePage:  destination == "",
			successor: move.Successor,
		})
	}
	for _, m := range bareCitationExpr.FindAllStringSubmatchIndex(text, -1) {
		if covers(links, m[0]) {
			continue
		}
		number := text[m[2]:m[3]]
		s := site{
			kind:    citationSite,
			start:   m[0],
			end:     m[1],
			line:    lineOf(text, m[0]),
			section: number,
		}
		switch {
		case moves.retiresSection(number):
		case tree.declaresSection(number):
			continue
		default:
			s.unmapped = true
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// span is a half-open byte range of one file's text.
type span struct {
	start int
	end   int
}

// covers reports whether any span contains the offset.
func covers(spans []span, offset int) bool {
	for _, s := range spans {
		if offset >= s.start && offset < s.end {
			return true
		}
	}
	return false
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
