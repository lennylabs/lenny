// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// docsDir is the documentation tree.
const docsDir = "docs"

// DocIndex is the documentation pages and the specification pages their
// front matter names.
type DocIndex struct {
	// Pages is the number of documentation pages read.
	Pages int
	// WithSpec is the number of pages whose front matter carries `spec`.
	WithSpec int
	// ByCode maps each code to the documentation pages that name it.
	ByCode map[string][]string
	// pagePaths are the documentation pages, sorted.
	pagePaths []string
}

// docFront is the part of a documentation page's front matter the index
// reads.
type docFront struct {
	Spec *[]string `yaml:"spec"`
}

// frontMatterText returns the YAML of a file's front matter, and false
// when the file has none.
func frontMatterText(content string) (string, bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", false
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// loadDocs reads the front matter of every documentation page of the
// working tree.
func loadDocs(ctx context.Context, r repo) (*DocIndex, error) {
	files, err := r.worktreeFiles(ctx, docsDir)
	if err != nil {
		return nil, err
	}
	x := &DocIndex{ByCode: map[string][]string{}}
	for _, p := range files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		x.Pages++
		x.pagePaths = append(x.pagePaths, p)
		b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		fm, ok := frontMatterText(string(b))
		if !ok {
			continue
		}
		var f docFront
		if yaml.Unmarshal([]byte(fm), &f) != nil || f.Spec == nil {
			continue
		}
		x.WithSpec++
		for _, code := range *f.Spec {
			x.ByCode[code] = append(x.ByCode[code], p)
		}
	}
	sort.Strings(x.pagePaths)
	for code := range x.ByCode {
		sort.Strings(x.ByCode[code])
	}
	return x, nil
}

// coverageNote states how many documentation pages carry `spec` front
// matter, because front matter is added area by area and a page without
// it cannot be found from the specification page it describes.
func (x *DocIndex) coverageNote() string {
	return pluralf(x.WithSpec, "documentation page", "documentation pages") + " of " + itoa(x.Pages) +
		" name in front matter the specification pages they describe. Front matter is added area by area, so a page without it is not found here."
}

// Mention is one line of a documentation page that names something.
type Mention struct {
	Path string
	Line int
	Text string
}

// mentions returns the lines of the documentation pages that name a
// catalog name, matched as a whole word.
func mentions(r repo, docs *DocIndex, name string) []Mention {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	expr := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_-])`)
	var out []Mention
	for _, p := range docs.pagePaths {
		b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		for i, l := range strings.Split(string(b), "\n") {
			if expr.MatchString(l) {
				out = append(out, Mention{Path: p, Line: i + 1, Text: strings.TrimSpace(l)})
			}
		}
	}
	return out
}

// issuesDir is the folder of the issue records.
const issuesDir = "issues"

// Issue is one open issue record.
type Issue struct {
	Path, ID, Status string
}

// issueFront is the part of an issue's front matter the helpers read.
type issueFront struct {
	ID           string   `yaml:"id"`
	Status       string   `yaml:"status"`
	Requirements []string `yaml:"requirements"`
}

// openIssues returns the open and planned issues under issues/<code>/ that
// name an identifier in their front matter or their text. An empty
// identifier returns every open issue under the code.
func openIssues(ctx context.Context, r repo, code, id string) ([]Issue, error) {
	if code == "" {
		return nil, nil
	}
	files, err := r.worktreeFiles(ctx, issuesDir+"/"+code)
	if err != nil {
		return nil, err
	}
	var word *regexp.Regexp
	if id != "" {
		word = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9-])` + regexp.QuoteMeta(id) + `($|[^A-Za-z0-9])`)
	}
	var out []Issue
	for _, p := range files {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		var f issueFront
		if fm, ok := frontMatterText(string(b)); ok {
			_ = yaml.Unmarshal([]byte(fm), &f)
		}
		if strings.EqualFold(f.Status, "closed") {
			continue
		}
		if word != nil && !word.Match(b) {
			continue
		}
		status := f.Status
		if status == "" {
			status = "open"
		}
		issueID := f.ID
		if issueID == "" {
			issueID = strings.TrimSuffix(filepath.Base(p), ".md")
		}
		out = append(out, Issue{Path: p, ID: issueID, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].ID, out[j].ID) })
	return out, nil
}

// pluralf renders a count with the singular or plural noun.
func pluralf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

// itoa renders an integer.
func itoa(n int) string { return strconv.Itoa(n) }
