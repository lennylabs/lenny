// SPDX-License-Identifier: MIT

package anchor

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// siteKind names which of the reference forms a site carries. The
// anchor-move map scopes both classes, because the map states which
// anchors the reduction retires: a link naming a retired anchor and a
// citation of the section a retired anchor addressed are the references
// the reduction invalidated, and every other reference names a
// destination the reduction leaves alone. A link is then resolved by the
// map itself, and a citation per occurrence by the sense register, whose
// missing entry stops the run rather than being passed over.
type siteKind string

const (
	// linkSite is an intra-repo markdown fragment link, in the
	// file-qualified `[...](NN_file.md#anchor)` form or the same-page
	// `[...](#anchor)` form, into an anchor the anchor-move map retires.
	linkSite siteKind = "link"
	// citationSite is a bare section citation of the §X.Y form, in a
	// comment or in prose, naming a section whose anchor the anchor-move
	// map retires.
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
	// successor is the heading the anchor-move map redirects a link
	// site to. It is read while the site is found, because the map is
	// what makes the link a site at all.
	successor Target
}

// fragmentLinkExpr matches a markdown link with a fragment. The first
// group is the destination's path half, which is empty for the same-page
// form, and the second is the fragment.
var fragmentLinkExpr = regexp.MustCompile(`\]\(([^)\s#]*)#([A-Za-z0-9._-]+)\)`)

// bareCitationExpr matches a bare section citation, capturing the whole
// dotted number so a citation of a deeper subsection is read as naming
// that subsection rather than as naming its parent.
var bareCitationExpr = regexp.MustCompile(`§(\d+(?:\.\d+)*)`)

// findSites returns every reference one file carries into an anchor the
// anchor-move map retires, in source order.
//
// The map states the population. It enumerates the anchors this
// reduction retires, so a reference into one of them is a reference the
// reduction invalidated, and a reference into any other heading is one
// the reduction leaves alone. Reading the tree as the population
// instead would pull in every reference that resolves nowhere for
// reasons this migration did not create, which is the hand-authored
// class the fragment-link gate and the residual check own, and would
// stop the run over references no map entry will ever redirect.
//
// A fragment link is a site when the map retires the anchor it names and
// its destination is a tracked markdown document of the tree, which is
// the population the fragment-link gate reads. An absolute URL and a
// link into a file the repository does not carry are outside that
// population, and rewriting one would edit a reference the pass cannot
// check.
//
// A bare section citation is a site when the map retires the anchor of
// the section it names. What an occurrence means is answered by the
// sense register one occurrence at a time, because a reduction carves
// material out of the anchor it moves, and an occurrence the register
// does not answer for stops the run.
//
// A citation written inside a markdown fragment link is not read as a
// bare citation. The pass performs a target-only redirect, and a label
// that names the retiring subsection is corrected by hand in the change
// that makes the reduction, so reading the label here would both rewrite
// a site the pass does not own and shift the occurrence numbering the
// sense register is keyed by.
func findSites(target, text string, tree *headings, moves *moveMap) []site {
	var out []site
	var links []span
	for _, m := range fragmentLinkExpr.FindAllStringSubmatchIndex(text, -1) {
		links = append(links, span{start: labelStart(text, m[0]), end: m[1]})
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
			anchor:    anchor,
			file:      file,
			samePage:  destination == "",
			successor: move.Successor,
		})
	}
	for _, m := range bareCitationExpr.FindAllStringSubmatchIndex(text, -1) {
		number := text[m[2]:m[3]]
		if !moves.retiresSection(number) {
			continue
		}
		if covers(links, m[0]) {
			continue
		}
		out = append(out, site{
			kind:    citationSite,
			start:   m[0],
			end:     m[1],
			line:    lineOf(text, m[0]),
			section: number,
		})
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

// labelStart returns the offset a markdown link begins at, given the
// offset of the `]` that closes its label. The label is the text between
// the brackets on that line, so the search stops at the line start,
// which leaves a destination written with no label spanning its
// destination alone.
func labelStart(text string, closing int) int {
	for at := closing - 1; at >= 0; at-- {
		switch text[at] {
		case '[':
			return at
		case '\n':
			return closing
		}
	}
	return closing
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
