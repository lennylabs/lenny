// SPDX-License-Identifier: MIT

package docgate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/survey"
)

// ErrNoBase reports that the branch has no merge base with the integration
// branch, so there is no diff to read. It is the annotation lint's.
var ErrNoBase = annotation.ErrNoBase

// changesDir is the folder of the change records.
const changesDir = "changes"

// changeFolder matches a change folder's path prefix.
var changeFolder = regexp.MustCompile(`^changes/([0-9]{4}-[^/]+)/`)

// waiverLine matches the line of a Documentation section that waives the
// documentation-impact gate for the pages it names by code:
//
//	No reader-facing behavior changes: POOL, CLAIM
//
// The line may open with a list marker and end with a period. The phrase in
// any other sentence waives nothing, so a waiver always names its pages.
var waiverLine = regexp.MustCompile(`(?m)^[ \t]*(?:[-*][ \t]+)?No reader-facing behavior changes:[ \t]*([A-Z]{2,9}(?:[ \t]*,[ \t]*[A-Z]{2,9})*)[ \t]*\.?[ \t]*$`)

// Waiver is one change folder's statement that the diff changes no
// reader-facing behavior on the pages it names.
type Waiver struct {
	// Folder is the change folder, such as changes/0300-pools.
	Folder string
	// Codes are the page codes the waiver lines name.
	Codes []string
}

// documentationHeading matches the Documentation section of change.md.
var documentationHeading = regexp.MustCompile(`(?m)^##\s+Documentation\s*$`)

// ImpactReport is what the documentation-impact gate found on a branch.
type ImpactReport struct {
	// Base is the merge-base commit, and BaseRef the integration ref.
	Base, BaseRef string
	// Checked are the codes of the landed pages the diff touches.
	Checked []string
	// NotYet are the codes of the pages the diff touches that have not
	// landed, which the gate does not check yet.
	NotYet []string
	// Waivers are the waiver lines of the change folders the diff touches,
	// each naming the page codes it waives.
	Waivers []Waiver
	// Problems name each landed page whose documentation the diff leaves
	// unedited.
	Problems []Problem
}

// touchedPage is a specification page the diff changes items on.
type touchedPage struct {
	path, status string
	items        []string
}

// CheckImpact runs the documentation-impact gate over the working tree at
// root against its merge base with the integration branch. It returns
// ErrNoBase when there is none.
func CheckImpact(ctx context.Context, root string) (ImpactReport, error) {
	var rep ImpactReport
	base, ref, err := annotation.MergeBase(ctx, root)
	if err != nil {
		return rep, err
	}
	rep.Base, rep.BaseRef = base, ref
	sc, err := survey.CompareSpec(ctx, root, base)
	if err != nil {
		return rep, err
	}
	touched := touchedPages(sc)
	if len(touched) == 0 {
		return rep, nil
	}
	pages, err := LoadPages(root)
	if err != nil {
		return rep, err
	}
	docsChanged, err := survey.ChangedFiles(ctx, root, base, DocsDir)
	if err != nil {
		return rep, err
	}
	edited := map[string]bool{}
	for _, p := range docsChanged {
		edited[p] = true
	}
	changed, err := survey.ChangedFiles(ctx, root, base, changesDir)
	if err != nil {
		return rep, err
	}
	if rep.Waivers, err = waivers(root, changed); err != nil {
		return rep, err
	}
	waived := map[string]bool{}
	for _, w := range rep.Waivers {
		for _, c := range w.Codes {
			waived[c] = true
		}
	}
	codes := make([]string, 0, len(touched))
	for c := range touched {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, code := range codes {
		tp := touched[code]
		if !defs.Landed(tp.status) {
			rep.NotYet = append(rep.NotYet, code)
			continue
		}
		rep.Checked = append(rep.Checked, code)
		if waived[code] {
			continue
		}
		naming, anyEdited := describing(pages, code, edited)
		if anyEdited {
			continue
		}
		rep.Problems = append(rep.Problems, missingDocs(code, tp, naming))
	}
	return rep, nil
}

// touchedPages returns the specification pages that hold a changed item,
// before or after the change, by code, with the status the page has after
// the change, or before it when the change removed the page.
func touchedPages(sc *survey.SpecChanges) map[string]*touchedPage {
	out := map[string]*touchedPage{}
	note := func(it *survey.Item) {
		if it == nil || it.Code == "" {
			return
		}
		tp := out[it.Code]
		if tp == nil {
			info, ok := sc.After.Pages[it.Code]
			if !ok {
				info = sc.Before.Pages[it.Code]
			}
			tp = &touchedPage{path: info.Path, status: info.Status}
			out[it.Code] = tp
		}
		for _, id := range tp.items {
			if id == it.ID {
				return
			}
		}
		tp.items = append(tp.items, it.ID)
	}
	for _, c := range sc.Changes {
		note(c.Old)
		note(c.New)
	}
	return out
}

// describing returns the documentation pages whose front matter names a
// code, and whether the diff edits any of them.
func describing(pages []*Page, code string, edited map[string]bool) ([]string, bool) {
	var naming []string
	anyEdited := false
	for _, p := range pages {
		if p.SpecError != "" {
			continue
		}
		for _, c := range p.Codes {
			if c == code {
				naming = append(naming, p.Path)
				anyEdited = anyEdited || edited[p.Path]
				break
			}
		}
	}
	return naming, anyEdited
}

// missingDocs is the finding for a landed page whose documentation the
// diff leaves unedited.
func missingDocs(code string, tp *touchedPage, naming []string) Problem {
	which := "no documentation page names it in its front matter yet"
	if len(naming) > 0 {
		which = "the pages that name it are " + strings.Join(naming, ", ")
	}
	return Problem{Path: tp.path, Message: fmt.Sprintf(
		"the diff changes %s on %s and edits no documentation page whose front matter names %s (%s); "+
			"edit the documentation that describes the change, or add the line `No reader-facing behavior changes: %s` to the Documentation section of the change's change.md",
		strings.Join(tp.items, ", "), code, code, which, code,
	)}
}

// waivers returns the waiver lines in the Documentation sections of the
// change folders the diff touches, one Waiver per folder with every code
// its waiver lines name.
func waivers(root string, changed []string) ([]Waiver, error) {
	seen := map[string]bool{}
	var out []Waiver
	for _, p := range changed {
		m := changeFolder.FindStringSubmatch(p)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		b, err := os.ReadFile(filepath.Join(root, changesDir, m[1], "change.md"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var codes []string
		for _, line := range waiverLine.FindAllStringSubmatch(documentationSection(string(b)), -1) {
			for _, c := range strings.Split(line[1], ",") {
				codes = append(codes, strings.TrimSpace(c))
			}
		}
		if len(codes) > 0 {
			sort.Strings(codes)
			out = append(out, Waiver{Folder: changesDir + "/" + m[1], Codes: codes})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, nil
}

// documentationSection returns the text of the Documentation section of a
// change.md, up to the next level-two heading.
func documentationSection(text string) string {
	loc := documentationHeading.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	rest := text[loc[1]:]
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
