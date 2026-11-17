// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"io"
)

// LinksInto prints every page-level link into the page a code or alias
// names: each relative link in the domain whose path reaches that page and
// that carries no fragment. When a placeholder becomes a full page, each
// of these links can move to the identifier or term anchor it means, as
// "Page status" in spec/conventions.md states.
func LinksInto(ctx context.Context, t *Tree, code string, out io.Writer) error {
	committed, err := LoadCommitted(t)
	if err != nil {
		return err
	}
	reg, _, err := Build(ctx, t, committed)
	if err != nil {
		return err
	}
	x := newIndex(reg)
	target, ok := x.codePath[code]
	if !ok {
		return fmt.Errorf("%s is not a registered page code or alias", code)
	}
	files, err := t.Files(ctx)
	if err != nil {
		return err
	}
	var found []Finding
	for _, f := range files {
		if !inDomain(f) || !isMarkdown(f) {
			continue
		}
		content, err := t.Read(f)
		if err != nil {
			return err
		}
		for _, l := range links(content) {
			if l.fragment != "" || l.path == "" {
				continue
			}
			if p, ok := resolve(f, l.path); ok && p == target {
				found = append(found, Finding{File: f, Line: l.line, Message: l.path})
			}
		}
	}
	fmt.Fprintf(out, "%s (%s): %d page-level link(s)\n", code, target, len(found))
	for _, f := range found {
		fmt.Fprintf(out, "  %s\n", f)
	}
	return nil
}
