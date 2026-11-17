// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// unmappedGroup is the group of packages whose doc.go names no page code.
const unmappedGroup = "unmapped"

// packageChange is one Go package the working tree changed.
type packageChange struct {
	dir     string
	codes   []string
	removed bool
}

// Since prints the requirements and operations added, modified, and
// retired since a date or a commit, and the Go packages changed since
// then grouped by area.
func Since(ctx context.Context, root, arg string, out io.Writer) error {
	r := repo{root: root}
	base, ok, err := r.resolveSince(ctx, arg)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "specshift-since-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if ok {
		fmt.Fprintf(out, "Since %s (%s, %s), compared with the working tree.\n", base.Short, base.Date, base.Subject)
	} else {
		fmt.Fprintf(out, "Since %s, which is before the first commit, so everything in the working tree is new.\n", arg)
	}
	before, err := revSnapshot(ctx, r, base.Hash, scratch)
	if err != nil {
		return err
	}
	after, err := worktreeSnapshot(ctx, r)
	if err != nil {
		return err
	}
	for _, s := range []struct{ kind, heading string }{
		{KindRequirement, "Requirements"},
		{KindOperation, "Operations"},
	} {
		cs := compare(before, after, s.kind)
		fmt.Fprintf(out, "\n%s:\n", s.heading)
		if len(cs) == 0 {
			fmt.Fprintln(out, "  none added, modified, or retired")
			continue
		}
		for _, change := range []string{Added, Modified, Retired} {
			var rows []string
			for _, c := range cs {
				if c.Change != change {
					continue
				}
				rows = append(rows, sinceRow(c))
			}
			if len(rows) == 0 {
				continue
			}
			fmt.Fprintf(out, "  %s:\n", strings.ToUpper(change[:1])+change[1:])
			for _, row := range rows {
				fmt.Fprintf(out, "    %s\n", row)
			}
		}
	}
	pkgs, err := changedPackages(ctx, r, base.Hash, after)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nGo packages changed: %d\n", len(pkgs))
	groups := map[string][]string{}
	for _, p := range pkgs {
		label := p.dir
		if len(p.codes) > 0 {
			label += " (" + strings.Join(p.codes, ", ") + ")"
		}
		if p.removed {
			label += " (removed)"
		}
		seen := map[string]bool{}
		for _, code := range p.codes {
			g := after.Pages[code].Group()
			if !seen[g] {
				seen[g] = true
				groups[g] = append(groups[g], label)
			}
		}
		if len(seen) == 0 {
			groups[unmappedGroup] = append(groups[unmappedGroup], label)
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		if g != unmappedGroup {
			names = append(names, g)
		}
	}
	sort.Strings(names)
	if _, ok := groups[unmappedGroup]; ok {
		names = append(names, unmappedGroup)
	}
	for _, g := range names {
		title := g
		if g == unmappedGroup {
			title = "unmapped (the package's doc.go names no page code yet)"
		}
		fmt.Fprintf(out, "  %s:\n", title)
		for _, label := range groups[g] {
			fmt.Fprintf(out, "    %s\n", label)
		}
	}
	return nil
}

// sinceRow renders one changed item on a line.
func sinceRow(c ItemChange) string {
	it := c.Item()
	row := fmt.Sprintf("%s %s (%s)", it.ID, it.Title(), it.File)
	if c.Moved() {
		row += ", moved from " + c.Old.File
	}
	if c.Change == Retired && c.HasRecord {
		if c.Record.Successor != "" {
			row += ", successor " + c.Record.Successor
		} else if c.Record.Removed != "" {
			row += ", " + c.Record.Removed
		}
	}
	return row
}

// changedPackages returns the Go packages, by directory, whose files the
// working tree adds, modifies, or deletes relative to base, with the page
// codes each package's doc.go names.
func changedPackages(ctx context.Context, r repo, base string, snap *Snapshot) ([]packageChange, error) {
	files, err := r.changedFiles(ctx, base, "*.go")
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && scope.Readable(f) {
			dirs[path.Dir(f)] = true
		}
	}
	var out []packageChange
	for dir := range dirs {
		pc := packageChange{dir: dir}
		docPath := path.Join(dir, "doc.go")
		src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(docPath)))
		if err != nil && base != "" {
			if old, ok := r.show(ctx, base, docPath); ok {
				src, err = []byte(old), nil
			}
		}
		if err == nil {
			pc.codes = pageCodes(docPath, src, snap)
		}
		pc.removed = !hasGoFiles(filepath.Join(r.root, filepath.FromSlash(dir)))
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out, nil
}

// pageCodes returns the registered page codes the annotations of a doc.go
// name.
func pageCodes(p string, src []byte, snap *Snapshot) []string {
	var out []string
	for _, a := range annotation.Annotations(annotation.Comments(p, src)) {
		for _, id := range annotation.Identifiers(a.Payload) {
			if k, _ := annotation.Classify(id); k != annotation.PageCode {
				continue
			}
			if _, ok := snap.Pages[id]; ok {
				out = append(out, id)
			}
		}
	}
	return dedupe(out)
}

// hasGoFiles reports whether a directory holds a Go file.
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}
