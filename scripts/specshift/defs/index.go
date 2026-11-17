// SPDX-License-Identifier: MIT

package defs

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// IndexFile is the name of a folder's index page.
const IndexFile = "index.md"

// folders returns every folder of the tree below the specification
// directory, as paths relative to it: each page's folder and that folder's
// ancestors. The specification directory itself is not a folder of the
// tree, because its README.md is the root map.
func folders(pages []*Page) []string {
	set := map[string]bool{}
	for _, p := range pages {
		for dir := p.Dir(); dir != ""; dir = parentDir(dir) {
			set[dir] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func parentDir(dir string) string {
	parent := path.Dir(dir)
	if parent == "." {
		return ""
	}
	return parent
}

// indexRows returns the page table of one folder: every page in the
// folder other than its index, and the index page of every subfolder,
// sorted by link path.
func indexRows(folder string, pages []*Page, byPath map[string]*Page, all []string) table {
	type row struct{ link, title, code, summary string }
	var rows []row
	for _, p := range pages {
		if p.Dir() == folder && path.Base(p.Path) != IndexFile {
			rows = append(rows, row{path.Base(p.Path), pageTitle(p), p.Front.Code, p.Front.Summary})
		}
	}
	for _, sub := range all {
		if parentDir(sub) != folder {
			continue
		}
		link := path.Base(sub) + "/" + IndexFile
		r := row{link, folderTitle(sub), "", ""}
		if p := byPath[path.Join(SpecDir, sub, IndexFile)]; p != nil {
			r.code = p.Front.Code
			r.summary = p.Front.Summary
			if p.Title != "" {
				r.title = p.Title
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].link < rows[j].link })
	tb := table{header: []string{"Page", "Code", "Summary"}}
	for _, r := range rows {
		code := ""
		if r.code != "" {
			code = "`" + r.code + "`"
		}
		tb.add("["+r.title+"]("+r.link+")", code, strings.Join(strings.Fields(r.summary), " "))
	}
	return tb
}

// folderTitle derives a title from a folder's name:
// runtimes-and-pools gives "Runtimes and pools".
func folderTitle(folder string) string {
	name := strings.ReplaceAll(path.Base(folder), "-", " ")
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// renderIndexTable renders a folder's page table, or a sentence when the
// folder holds no other page.
func renderIndexTable(tb table) string {
	if len(tb.rows) == 0 {
		return "This folder holds no other page yet.\n"
	}
	return tb.render()
}

// generateIndexes writes the index of every folder into out. Every index
// page keeps its own text, including the hand-written sections that point
// at the numbered sections a placeholder still draws on, and its page table
// goes into its index section, which is appended under a Pages heading when
// the page has none.
func generateIndexes(pages []*Page, out map[string]string) []Problem {
	byPath := map[string]*Page{}
	for _, p := range pages {
		byPath[p.Path] = p
	}
	all := folders(pages)
	var problems []Problem
	for _, folder := range all {
		indexPath := path.Join(SpecDir, folder, IndexFile)
		p := byPath[indexPath]
		if p == nil {
			problems = append(problems, Problem{Path: indexPath, Message: fmt.Sprintf(
				"folder %s holds pages but no index.md with front matter carrying a code", path.Join(SpecDir, folder),
			)})
			continue
		}
		tb := indexRows(folder, pages, byPath, all)
		out[indexPath] = fillIndexSection(p, currentText(p, out), renderIndexTable(tb))
	}
	return problems
}

// fillIndexSection puts a folder table into a page's index section, or
// appends the section when the page has none.
func fillIndexSection(p *Page, text, content string) string {
	front, _, body, bodyLine, ok := splitFrontMatter(text)
	if !ok {
		front, body, bodyLine = p.FrontText, p.Body, p.bodyLine
	}
	regions, _ := findRegions(p.Path, body, bodyLine)
	for _, r := range regions {
		if r.name == RegionIndex {
			return front + replaceRegions(body, []region{r}, func(string) string { return content })
		}
	}
	return front + strings.TrimRight(body, "\n") + "\n\n## Pages\n\n" + regionBlock(RegionIndex, content)
}

// currentText returns a page's text as generation has left it so far.
func currentText(p *Page, out map[string]string) string {
	if text, ok := out[p.Path]; ok {
		return text
	}
	return p.FrontText + p.Body
}
