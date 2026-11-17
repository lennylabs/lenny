// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"path"
	"sort"
)

// FixResult is what one run of Fix changed.
type FixResult struct {
	// Links is the number of link paths rewritten.
	Links int
	// Files are the files whose links were rewritten, sorted.
	Files []string
	// Unresolved are the links into the new tree that neither the
	// committed registry nor the tree places.
	Unresolved []Finding
}

// Fix rewrites link paths after pages or identifiers move, then writes
// the registry. The committed registry, with its history of moves, says
// where each page and identifier was; the tree says where it is now.
//
// A link is rewritten when its fragment is a registered identifier or
// anchor, in which case it is pointed at the file that registers it now,
// or when the page it reached is a registered page that moved, in which
// case it is pointed at that page's code's current path. A link written
// in a page that itself moved is read from the page's old directory, so
// a link that reached an unmoved file before the move keeps reaching it.
// Every new path is computed from the directory of the file the link is
// written in.
func Fix(ctx context.Context, t *Tree) (FixResult, error) {
	committed, err := LoadCommitted(t)
	if err != nil {
		return FixResult{}, err
	}
	fresh, _, err := Build(ctx, t, committed)
	if err != nil {
		return FixResult{}, err
	}
	f := &fixer{t: t, x: newIndex(fresh), oldCodeAt: pathCodes(committed), oldPathOf: map[string]string{}}
	for _, p := range committed.Pages {
		now, ok := f.x.codePath[p.Code]
		if !ok {
			continue
		}
		if now != p.Path {
			f.oldPathOf[now] = p.Path
			continue
		}
		// A registry rebuilt after the move already places the page at
		// its new path, and its last move record names where it was.
		for i := len(committed.Moves) - 1; i >= 0; i-- {
			if m := committed.Moves[i]; m.ID == p.Code && m.To == now {
				f.oldPathOf[now] = m.From
				break
			}
		}
	}
	files, err := t.Files(ctx)
	if err != nil {
		return FixResult{}, err
	}
	var res FixResult
	for _, file := range files {
		if !inDomain(file) || !isMarkdown(file) {
			continue
		}
		n, err := f.fixFile(file, &res)
		if err != nil {
			return FixResult{}, err
		}
		if n > 0 {
			res.Links += n
			res.Files = append(res.Files, file)
		}
	}
	sort.Slice(res.Unresolved, func(i, j int) bool { return findingLess(res.Unresolved[i], res.Unresolved[j]) })
	if err := writeRegistry(t, fresh); err != nil {
		return FixResult{}, err
	}
	return res, nil
}

// fixer holds what one run of Fix reads.
type fixer struct {
	t         *Tree
	x         *index
	oldCodeAt map[string]string // committed path -> code
	oldPathOf map[string]string // current page path -> committed path
}

// fixFile rewrites the links of one file and returns how many it
// rewrote.
func (f *fixer) fixFile(file string, res *FixResult) (int, error) {
	content, err := f.t.Read(file)
	if err != nil {
		return 0, err
	}
	oldFile := file
	if p, ok := f.oldPathOf[file]; ok {
		oldFile = p
	}
	var edits []edit
	for _, l := range links(content) {
		target, ok := f.target(file, oldFile, l)
		if !ok {
			res.Unresolved = append(res.Unresolved, Finding{File: file, Line: l.line, Message: fmt.Sprintf("link %s: neither the registry nor the tree places its target", displayTarget(l.path, l.fragment))})
			continue
		}
		if target == "" {
			continue
		}
		newPath := ""
		if target != file {
			newPath = relativeTo(path.Dir(file), target)
		}
		if newPath == l.path || (l.path != "" && mustResolve(file, l.path) == target) {
			continue
		}
		edits = append(edits, edit{lo: l.pathLo, hi: l.pathHi, text: newPath})
	}
	if len(edits) == 0 {
		return 0, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].lo < edits[j].lo })
	if err := f.t.Write(file, rewriteSpans(content, edits)); err != nil {
		return 0, fmt.Errorf("write %s: %w", file, err)
	}
	return len(edits), nil
}

// target returns the repository path a link should reach now, "" when
// the link needs no rewrite, and false when the link points into the new
// tree and nothing places its target.
func (f *fixer) target(file, oldFile string, l link) (string, bool) {
	if l.path == "" {
		// A same-page fragment follows its identifier when the identifier
		// moved to another page. Only a page of the new tree links its
		// identifiers this way, as Check reads it; a same-page fragment in
		// any other file, such as a documentation page, a rule, or a
		// numbered file, is a local heading anchor and is left as written.
		if _, isPage := f.x.pageByPath[file]; !isPage {
			return "", true
		}
		if owner, ok := f.x.anchorFile[l.fragment]; ok && owner != file && l.fragment != "" {
			return owner, true
		}
		return "", true
	}
	oldTarget, oldOK := resolve(oldFile, l.path)
	curTarget, curOK := resolve(file, l.path)
	governed := (oldOK && newTreeTarget(oldTarget)) || (curOK && newTreeTarget(curTarget))
	if governed && l.fragment != "" {
		if owner, ok := f.x.anchorFile[l.fragment]; ok {
			return owner, true
		}
	}
	if oldOK {
		if code, ok := f.oldCodeAt[oldTarget]; ok {
			if now, ok := f.x.codePath[code]; ok {
				return now, true
			}
		}
	}
	if curOK && f.t.exists(curTarget) {
		return "", true
	}
	if oldOK && f.t.exists(oldTarget) {
		return oldTarget, true
	}
	if governed {
		return "", false
	}
	return "", true
}

// mustResolve resolves a link path, returning "" when it leaves the
// repository.
func mustResolve(from, linkPath string) string {
	p, _ := resolve(from, linkPath)
	return p
}

// writeRegistry writes the registry to its committed path.
func writeRegistry(t *Tree, r *Registry) error {
	b, err := r.Encode()
	if err != nil {
		return err
	}
	if err := t.Write(RegistryPath, string(b)); err != nil {
		return fmt.Errorf("write %s: %w", RegistryPath, err)
	}
	return nil
}
