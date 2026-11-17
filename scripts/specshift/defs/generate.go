// SPDX-License-Identifier: MIT

package defs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Generate computes every generated file of the tree and returns its
// content by repo-relative path: the catalogs, the pages whose generated
// sections it fills, the folder indexes, and the documentation reference
// pages whose regions it fills. It refuses a tree that fails
// Check, because a catalog built from invalid blocks would list them.
func Generate(t *Tree) (map[string]string, []Problem) {
	if problems := Check(t); len(problems) > 0 {
		return nil, problems
	}
	out := map[string]string{}
	pages := generateCatalogs(t, out)
	generateSections(t, pages, out)
	generateRefPages(t, out)
	problems := generateIndexes(pages, out)
	sortProblems(problems)
	if len(problems) > 0 {
		return nil, problems
	}
	return out, nil
}

// generateCatalogs writes each catalog into out and returns the tree's
// pages with the catalogs as generated, so the catalogs' folder index
// reads their current front matter and titles.
func generateCatalogs(t *Tree, out map[string]string) []*Page {
	pages := map[string]*Page{}
	for _, p := range t.Pages {
		pages[p.Path] = p
	}
	put := func(p string, text string) {
		out[p] = text
		if pg, _ := ParsePage(p, text); pg != nil {
			pages[p] = pg
		}
	}
	for _, c := range catalogs {
		p := catalogPath(c.file)
		put(p, renderCatalog(t, c, frontFor(t, p, c, "catalog")))
	}
	// The catalogs' own index is generated whole by generateIndexes, from
	// the front matter written here.
	indexPath := catalogPath(catalogIndex.file)
	put(indexPath, frontFor(t, indexPath, catalogIndex, "index"))
	list := make([]*Page, 0, len(pages))
	for _, p := range pages {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return list
}

// frontFor returns the front matter of a generated file. The generator
// owns the whole file, front matter included, except the page code: an
// existing file keeps its code, so the code the code registry assigns to a
// catalog survives regeneration.
func frontFor(t *Tree, p string, c catalog, pageType string) string {
	if existing := t.PageByPath(p); existing != nil {
		c.code = existing.Front.Code
	}
	return defaultFrontMatter(c, pageType)
}

// generateSections fills the generated sections of every page that
// carries marker comments, other than the generated catalogs and the
// folder index section, which generateIndexes fills. pages are the tree's
// pages with the catalogs as generated, which the code registry lists.
func generateSections(t *Tree, pages []*Page, out map[string]string) {
	codes := t.Codes()
	for _, p := range t.Pages {
		if strings.HasPrefix(p.Path, CatalogDir+"/") {
			continue
		}
		regions, _ := findRegions(p.Path, p.Body, p.bodyLine)
		var fill []region
		for _, r := range regions {
			if r.name != RegionIndex {
				fill = append(fill, r)
			}
		}
		if len(fill) == 0 {
			continue
		}
		l := linker{fromDir: path.Dir(p.Path), codes: codes}
		body := replaceRegions(p.Body, fill, func(name string) string {
			if name == RegionCodeRegistry {
				return codeRegistry(pages, path.Dir(p.Path))
			}
			return sectionContent(t, p, name, l)
		})
		out[p.Path] = p.FrontText + body
	}
}

// sectionContent returns the content of one generated section of a page.
func sectionContent(t *Tree, p *Page, name string, l linker) string {
	code := p.Front.Code
	var tb table
	switch name {
	case RegionSettingsRead:
		tb = table{header: []string{"Setting", "Default", "Summary"}}
		for _, b := range blocksNaming(t, KindSetting, "read-by", code) {
			tb.add(l.block(b), b.Text("default"), b.Text("summary"))
		}
	case RegionMetricsEmitted:
		tb = table{header: []string{"Metric", "Type", "Summary"}}
		for _, b := range blocksNaming(t, KindMetric, "emitter", code) {
			tb.add(l.block(b), b.Text("type"), b.Text("summary"))
		}
	case RegionEventsEmitted:
		tb = table{header: []string{"Event type", "Summary"}}
		for _, b := range blocksNaming(t, KindEvent, "emitter", code) {
			tb.add(l.block(b), b.Text("summary"))
		}
	case RegionRBACGrants:
		var grants []*Block
		for _, b := range blocksOf(t, KindGrant, KindRBACGrant) {
			if grantUnit(b) == code {
				grants = append(grants, b)
			}
		}
		tb = grantTable(grants, l, false)
	case RegionNetworkRules:
		tb = ruleTable(blocksNaming(t, KindNetworkRule, "unit", code), l, false)
	case RegionDefinitionKinds:
		return SchemaReference(3)
	}
	if len(tb.rows) == 0 {
		return "No definition block names this page yet.\n"
	}
	return tb.render()
}

// codeRegistry renders the code registry: one row per page with its code,
// type, path relative to the specification directory, and summary, read
// from the pages' front matter. The pages at the root of the tree come
// first, then every other page by path. fromDir is the folder of the page
// that carries the table, which its links are relative to.
func codeRegistry(pages []*Page, fromDir string) string {
	sorted := append([]*Page(nil), pages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := sorted[i].Dir() == "", sorted[j].Dir() == ""
		if ri != rj {
			return ri
		}
		return sorted[i].Path < sorted[j].Path
	})
	tb := table{header: []string{"Code", "Type", "Path", "Summary"}}
	for _, p := range sorted {
		rel := strings.TrimPrefix(p.Path, SpecDir+"/")
		tb.add(code(p.Front.Code), p.Front.Type, "["+code(rel)+"]("+relLink(fromDir, p.Path)+")", p.Front.Summary)
	}
	return tb.render()
}

// blocksNaming returns the sorted blocks of a kind whose field lists the
// code.
func blocksNaming(t *Tree, kind, field, code string) []*Block {
	var out []*Block
	for _, b := range blocksOf(t, kind) {
		if contains(b.List(field), code) {
			out = append(out, b)
		}
	}
	return out
}

// Stale compares generated content with the files under root and returns
// the repo-relative paths whose content differs or that do not exist,
// sorted.
func Stale(root string, generated map[string]string) ([]string, error) {
	var stale []string
	for p, want := range generated {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if errors.Is(err, fs.ErrNotExist) {
			stale = append(stale, p)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if !bytes.Equal(got, []byte(want)) {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// Write writes the generated files that differ from the tree under root
// and returns their repo-relative paths, sorted.
func Write(root string, generated map[string]string) ([]string, error) {
	stale, err := Stale(root, generated)
	if err != nil {
		return nil, err
	}
	for _, p := range stale {
		target := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create the folder of %s: %w", p, err)
		}
		if err := os.WriteFile(target, []byte(generated[p]), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
	}
	return stale, nil
}
