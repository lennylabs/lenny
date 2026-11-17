// SPDX-License-Identifier: MIT

package docgate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// DocsDir is the repo-relative folder of the documentation.
const DocsDir = "docs"

// Problem is one finding, located by file and line.
type Problem = defs.Problem

// Page is one documentation page.
type Page struct {
	// Path is the repo-relative path.
	Path string
	// HasSpec reports whether the front matter carries the spec key.
	HasSpec bool
	// Codes are the page codes the spec key lists.
	Codes []string
	// SpecLine is the file line of the spec key, or 1.
	SpecLine int
	// SpecError says why the spec key is malformed, or is empty.
	SpecError string
	// Body is the text after the front matter, and BodyLine the file line
	// of its first line.
	Body     string
	BodyLine int
}

// skippedDirs are folders under docs/ that hold build output or
// dependencies rather than pages.
var skippedDirs = map[string]bool{"_site": true, "node_modules": true, "vendor": true, ".jekyll-cache": true}

// LoadPages reads every Markdown page under root/docs, sorted by path. A
// tree with no docs folder has none.
func LoadPages(root string) ([]*Page, error) {
	var out []*Page
	dir := filepath.Join(root, DocsDir)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		out = append(out, ParsePage(filepath.ToSlash(rel), string(content)))
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ParsePage reads one documentation page's front matter and body.
func ParsePage(relPath, content string) *Page {
	p := &Page{Path: relPath, Body: content, BodyLine: 1, SpecLine: 1}
	if !strings.HasPrefix(content, "---\n") {
		return p
	}
	lines := strings.SplitAfter(content, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return p
	}
	p.Body = strings.Join(lines[end+1:], "")
	p.BodyLine = end + 2
	readSpec(p, strings.Join(lines[1:end], ""))
	return p
}

// readSpec reads the spec key of a page's front matter. The YAML starts
// on the file's second line.
func readSpec(p *Page, yamlText string) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		// A page that names no specification page is not checked yet, so
		// invalid YAML is a finding only when the spec key is present.
		for i, l := range strings.Split(yamlText, "\n") {
			if strings.HasPrefix(l, "spec:") {
				p.HasSpec, p.SpecLine = true, i+2
				p.SpecError = fmt.Sprintf("the front matter is not valid YAML: %v", err)
			}
		}
		return
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "spec" {
			continue
		}
		p.HasSpec, p.SpecLine = true, m.Content[i].Line+1
		v := m.Content[i+1]
		if v.Kind != yaml.SequenceNode {
			p.SpecError = "spec must be a list of specification page codes, such as spec: [POOL, CLAIM], or spec: [] for a page that describes none"
			return
		}
		for _, c := range v.Content {
			if c.Kind != yaml.ScalarNode || c.Value == "" {
				p.SpecError = "each entry of spec must be a specification page code"
				return
			}
			p.Codes = append(p.Codes, c.Value)
		}
		return
	}
}

// Statuses maps every page code and alias of spec/registry.json to the
// page's status.
type Statuses map[string]string

// LoadStatuses reads the committed registry under root. A tree with no
// registry has no page, so no code resolves.
func LoadStatuses(root string) (Statuses, error) {
	reg, err := ref.LoadCommitted(&ref.Tree{Root: root})
	if err != nil {
		return nil, err
	}
	out := Statuses{}
	for _, p := range reg.Pages {
		out[p.Code] = p.Status
		for _, a := range p.Aliases {
			out[a] = p.Status
		}
	}
	return out, nil
}

// InScope reports whether the catalog-name gate applies to a page: its
// spec key is well formed and every code it lists names a landed page.
func (p *Page) InScope(st Statuses) bool {
	if !p.HasSpec || p.SpecError != "" {
		return false
	}
	for _, c := range p.Codes {
		status, ok := st[c]
		if !ok || !defs.Landed(status) {
			return false
		}
	}
	return true
}

// CheckFrontMatter reports, for every page that carries the spec key, a
// key that is not a list of codes and a code that spec/registry.json does
// not register.
func CheckFrontMatter(pages []*Page, st Statuses) []Problem {
	var out []Problem
	for _, p := range pages {
		if !p.HasSpec {
			continue
		}
		if p.SpecError != "" {
			out = append(out, Problem{Path: p.Path, Line: p.SpecLine, Message: p.SpecError})
			continue
		}
		for _, c := range p.Codes {
			if _, ok := st[c]; !ok {
				out = append(out, Problem{Path: p.Path, Line: p.SpecLine, Message: fmt.Sprintf(
					"spec names %q, which %s does not register as a page code or an alias", c, ref.RegistryPath,
				)})
			}
		}
	}
	return out
}
