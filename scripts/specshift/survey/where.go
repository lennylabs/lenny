// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// historyLimit bounds the commits read to find the last change to an
// identifier's text.
const historyLimit = 500

// Where prints what the tree holds about an identifier, a page code, or a
// definition block's anchor.
func Where(ctx context.Context, root, query string, out io.Writer) error {
	r := repo{root: root}
	query = strings.TrimSpace(query)
	snap, err := worktreeSnapshot(ctx, r)
	if err != nil {
		return err
	}
	docs, err := loadDocs(ctx, r)
	if err != nil {
		return err
	}
	if page, ok := snap.Pages[query]; ok {
		return wherePage(ctx, r, snap, docs, query, page, out)
	}
	it := snap.find(query)
	if it == nil {
		if rt, ok := snap.retirement(query); ok {
			return whereRetired(ctx, r, snap, query, rt.File, rt.Successor, rt.Removed, out)
		}
		return fmt.Errorf("%s is not a page code, requirement, operation, or definition block of the specification tree; `specshift ref lookup` resolves anchors and numbered sections", query)
	}
	page := snap.ByPath[it.File]
	fmt.Fprintf(out, "%s (%s)\n", it.ID, it.Kind)
	fmt.Fprintf(out, "Page: %s %s", page.Code, page.Path)
	if page.Title != "" {
		fmt.Fprintf(out, " (%s)", page.Title)
	}
	fmt.Fprintf(out, "\nAnchor: %s#%s\n", it.File, it.Anchor)
	fmt.Fprintf(out, "\nText:\n%s\n", indent(it.Text))
	sites, err := findSites(ctx, r, []string{it.ID})
	if err != nil {
		return err
	}
	printSites(out, sites[it.ID])
	printDocs(out, docs, page.Code)
	issues, err := openIssues(ctx, r, page.Code, it.ID)
	if err != nil {
		return err
	}
	printIssues(out, fmt.Sprintf("Open issues under issues/%s/ that name %s", page.Code, it.ID), issues)
	last, err := lastTextChange(ctx, r, it)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nLast change to its text:\n  %s\n", last)
	return nil
}

// wherePage prints a page: its summary, its requirements and operations,
// the code that names its code, the documentation that describes it, its
// open issues, and the last commit to its file.
func wherePage(ctx context.Context, r repo, snap *Snapshot, docs *DocIndex, query string, page PageInfo, out io.Writer) error {
	fmt.Fprintf(out, "%s (page", query)
	if query != page.Code {
		fmt.Fprintf(out, ", alias of %s", page.Code)
	}
	fmt.Fprintf(out, ")\nPage: %s", page.Path)
	if page.Title != "" {
		fmt.Fprintf(out, " (%s)", page.Title)
	}
	fmt.Fprintf(out, "\nType: %s", page.Type)
	if page.Area != "" {
		fmt.Fprintf(out, ", area %s", page.Area)
	}
	if page.Status != "" {
		fmt.Fprintf(out, ", status %s", page.Status)
	}
	fmt.Fprintf(out, "\n\nSummary:\n%s\n", indent(page.Summary))
	var held []string
	for _, kind := range []string{KindRequirement, KindOperation, KindBlock} {
		for _, it := range snap.sortedItems(kind) {
			if it.File == page.Path {
				held = append(held, it.ID)
			}
		}
	}
	fmt.Fprintln(out, "\nRequirements, operations, and definition blocks:")
	if len(held) == 0 {
		fmt.Fprintln(out, "  none yet")
	} else {
		fmt.Fprintf(out, "  %s\n", strings.Join(held, ", "))
	}
	sites, err := findSites(ctx, r, []string{page.Code})
	if err != nil {
		return err
	}
	printSites(out, sites[page.Code])
	printDocs(out, docs, page.Code)
	issues, err := openIssues(ctx, r, page.Code, "")
	if err != nil {
		return err
	}
	printIssues(out, fmt.Sprintf("Open issues under issues/%s/", page.Code), issues)
	log, err := r.git(ctx, "log", "-1", "--date=short", commitFormat, "--", page.Path)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "\nLast change to the page:")
	if cs := parseCommits(log); len(cs) > 0 {
		fmt.Fprintf(out, "  %s %s %s\n", cs[0].Short, cs[0].Date, cs[0].Subject)
	} else {
		fmt.Fprintln(out, "  not committed yet")
	}
	return nil
}

// whereRetired prints a retired identifier and where its successor is.
func whereRetired(ctx context.Context, r repo, snap *Snapshot, query, file, successor, removed string, out io.Writer) error {
	fmt.Fprintf(out, "%s (retired)\nPage: %s\n", query, file)
	if removed != "" {
		fmt.Fprintf(out, "Removed: %s\n", removed)
	}
	if successor != "" {
		fmt.Fprintf(out, "Successor: %s\n", successor)
	}
	sites, err := findSites(ctx, r, []string{query})
	if err != nil {
		return err
	}
	if len(sites[query]) > 0 {
		fmt.Fprintln(out, "\nAnnotations that still cite the retired identifier:")
		for _, s := range sites[query] {
			fmt.Fprintf(out, "  %s\n", s)
		}
	}
	if successor != "" && snap.find(successor) != nil {
		fmt.Fprintf(out, "\nRun `specshift where %s` for the successor.\n", successor)
	}
	return nil
}

// printSites prints the code sites and the tests of an identifier.
func printSites(out io.Writer, sites []Site) {
	code, tests, packages := splitSites(sites)
	section := func(title string, ss []Site) {
		fmt.Fprintf(out, "\n%s:\n", title)
		if len(ss) == 0 {
			fmt.Fprintln(out, "  none found by annotation")
			return
		}
		for _, s := range ss {
			fmt.Fprintf(out, "  %s\n", s)
		}
	}
	if len(packages) > 0 {
		section("Packages whose doc comment names it", packages)
	}
	section("Code sites", code)
	section("Tests", tests)
}

// printDocs prints the documentation pages that name a page code.
func printDocs(out io.Writer, docs *DocIndex, code string) {
	fmt.Fprintf(out, "\nDocumentation pages whose front matter names %s:\n", code)
	pages := docs.ByCode[code]
	if len(pages) == 0 {
		fmt.Fprintln(out, "  none")
	}
	for _, p := range pages {
		fmt.Fprintf(out, "  %s\n", p)
	}
	fmt.Fprintf(out, "  (%s)\n", docs.coverageNote())
}

// printIssues prints a list of open issues.
func printIssues(out io.Writer, title string, issues []Issue) {
	fmt.Fprintf(out, "\n%s:\n", title)
	if len(issues) == 0 {
		fmt.Fprintln(out, "  none")
		return
	}
	for _, is := range issues {
		fmt.Fprintf(out, "  %s %s (%s)\n", is.ID, is.Path, is.Status)
	}
}

// lastTextChange returns the last commit that changed an item's text on
// its current page, or says that the working tree changes it.
//
// It reads the commits that touched the page, newest first, and returns
// the first whose text of the item differs from its parent's. Before a
// page moved, the item's history is on another path, so the commit that
// added the page is reported for an item that arrived with it.
func lastTextChange(ctx context.Context, r repo, it *Item) (string, error) {
	head, inHead := r.show(ctx, "HEAD", it.File)
	if !inHead || itemTextIn(head, it) != it.Text {
		return "uncommitted: the working tree changes it", nil
	}
	out, err := r.git(ctx, "log", fmt.Sprintf("-%d", historyLimit), "--date=short", commitFormat, "HEAD", "--", it.File)
	if err != nil {
		return "", err
	}
	for _, c := range parseCommits(out) {
		now, _ := r.show(ctx, c.Hash, it.File)
		before, _ := r.show(ctx, c.Hash+"^", it.File)
		if itemTextIn(now, it) != itemTextIn(before, it) {
			return fmt.Sprintf("%s %s %s", c.Short, c.Date, c.Subject), nil
		}
	}
	return fmt.Sprintf("not found in the last %d commits to %s", historyLimit, it.File), nil
}

// indent indents each line of a text by two spaces.
func indent(text string) string {
	if strings.TrimSpace(text) == "" {
		return "  (empty)"
	}
	ls := strings.Split(text, "\n")
	for i, l := range ls {
		ls[i] = "  " + l
	}
	return strings.Join(ls, "\n")
}
