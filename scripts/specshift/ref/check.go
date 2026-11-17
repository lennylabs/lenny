// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"sort"
)

// Check builds the registry and returns every problem with the links
// into the new specification tree and with the registry itself:
//
//   - a link into spec/ that does not resolve;
//   - a fragment on a link into a new-tree page that is not an
//     identifier or term anchor registered on that page;
//   - a link that targets a heading anchor on another new-tree page;
//   - a link with a fragment into a page whose status is placeholder;
//   - a duplicate code, anchor, or block, and a code that differs from
//     another only by a one-letter suffix;
//   - a retired identifier with no successor or removed record, or whose
//     successor is not registered, and an identifier or code the
//     committed registry holds that left the tree with no retired record.
//
// Links into the numbered files keep the rules of the existing citation
// gates and are not checked here.
func Check(ctx context.Context, t *Tree) ([]Finding, error) {
	committed, err := LoadCommitted(t)
	if err != nil {
		return nil, err
	}
	reg, findings, err := Build(ctx, t, committed)
	if err != nil {
		return nil, err
	}
	x := newIndex(reg)
	retiredFindings, err := checkRetired(t, x)
	if err != nil {
		return nil, err
	}
	findings = append(findings, retiredFindings...)
	findings = append(findings, checkVanished(committed, x)...)
	linkFindings, err := checkLinks(ctx, t, x)
	if err != nil {
		return nil, err
	}
	findings = append(findings, linkFindings...)
	sort.Slice(findings, func(i, j int) bool { return findingLess(findings[i], findings[j]) })
	return findings, nil
}

// checkRetired reports a retirement whose successor is not registered.
// A retirement with neither a successor nor a removed record is reported
// by the build.
func checkRetired(t *Tree, x *index) ([]Finding, error) {
	var out []Finding
	for _, r := range x.reg.Retired {
		if r.Successor != "" && !x.resolves(r.Successor) {
			content, err := t.Read(r.File)
			if err != nil {
				return nil, err
			}
			out = append(out, Finding{File: r.File, Line: frontMatterLine(content, r.ID), Message: fmt.Sprintf("retired identifier %s names successor %s, which is not registered", r.ID, r.Successor)})
		}
	}
	return out, nil
}

// checkVanished reports a code, requirement, or operation the committed
// registry holds that the tree no longer declares, retires, or keeps as
// an alias. Numbers are never reused, so an identifier leaves only
// through a retirement that names its successor or records its removal.
func checkVanished(committed *Registry, x *index) []Finding {
	var out []Finding
	for _, p := range committed.Pages {
		if _, ok := x.codePath[p.Code]; !ok {
			out = append(out, Finding{File: RegistryPath, Message: fmt.Sprintf("code %s (%s) left the tree with no alias on another page", p.Code, p.Path)})
		}
	}
	for _, ids := range [][]Identifier{committed.Requirements, committed.Operations} {
		for _, id := range ids {
			if _, ok := x.idAnchor[id.ID]; !ok {
				out = append(out, Finding{File: id.File, Message: fmt.Sprintf("identifier %s left the tree with no retired record naming a successor or its removal", id.ID)})
			}
		}
	}
	return out
}

// checkLinks reports the links in the domain that break a link rule of
// the new tree.
func checkLinks(ctx context.Context, t *Tree, x *index) ([]Finding, error) {
	files, err := t.Files(ctx)
	if err != nil {
		return nil, err
	}
	var out []Finding
	headingCache := map[string]map[string]bool{}
	for _, f := range files {
		if !inDomain(f) || !isMarkdown(f) {
			continue
		}
		content, err := t.Read(f)
		if err != nil {
			return nil, err
		}
		for _, l := range links(content) {
			msg, err := checkLink(t, x, f, l, headingCache)
			if err != nil {
				return nil, err
			}
			if msg != "" {
				out = append(out, Finding{File: f, Line: l.line, Message: msg})
			}
		}
	}
	return out, nil
}

// checkLink returns the problem with one link, or "" when it has none.
func checkLink(t *Tree, x *index, from string, l link, headingCache map[string]map[string]bool) (string, error) {
	if l.path == "" {
		// A same-page fragment is a local anchor unless the page is in
		// the new tree and the fragment is an identifier registered on
		// another page.
		if _, isPage := x.pageByPath[from]; !isPage {
			return "", nil
		}
		if owner, ok := x.anchorFile[l.fragment]; ok && owner != from {
			return fmt.Sprintf("link #%s: the identifier is registered on %s, not on this page", l.fragment, owner), nil
		}
		return "", nil
	}
	target, ok := resolve(from, l.path)
	if !ok || !newTreeTarget(target) {
		return "", nil
	}
	shown := displayTarget(l.path, l.fragment)
	if !t.exists(target) {
		return fmt.Sprintf("link %s does not resolve: %s does not exist", shown, target), nil
	}
	page, isPage := x.pageByPath[target]
	if l.fragment == "" || !isPage || target == from {
		return "", nil
	}
	if page.Status == "placeholder" {
		return fmt.Sprintf("link %s carries a fragment into %s, which is a placeholder", shown, page.Code), nil
	}
	owner, registered := x.anchorFile[l.fragment]
	if registered && owner == target {
		return "", nil
	}
	if registered {
		return fmt.Sprintf("link %s: #%s is registered on %s, not on %s", shown, l.fragment, owner, target), nil
	}
	anchors, cached := headingCache[target]
	if !cached {
		content, err := t.Read(target)
		if err != nil {
			return "", err
		}
		anchors = headingAnchors(content)
		headingCache[target] = anchors
	}
	if anchors[l.fragment] {
		return fmt.Sprintf("link %s targets a heading anchor on another page; link an identifier or term anchor", shown), nil
	}
	return fmt.Sprintf("link %s: #%s is not a registered identifier or term anchor", shown, l.fragment), nil
}
