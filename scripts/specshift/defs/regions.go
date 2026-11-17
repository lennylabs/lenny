// SPDX-License-Identifier: MIT

package defs

import (
	"fmt"
	"regexp"
	"strings"
)

// The names of the generated sections a page may carry between marker
// comments, such as <!-- generated:settings-read --> and
// <!-- /generated:settings-read -->.
const (
	RegionSettingsRead    = "settings-read"
	RegionMetricsEmitted  = "metrics-emitted"
	RegionEventsEmitted   = "events-emitted"
	RegionRBACGrants      = "rbac-grants"
	RegionNetworkRules    = "network-rules"
	RegionIndex           = "index"
	RegionDefinitionKinds = "definition-kinds"
	RegionCodeRegistry    = "code-registry"
)

// ConventionsPath is the repo-relative path of the conventions page, which
// carries the code registry and the definition-kinds reference as
// generated sections.
const ConventionsPath = SpecDir + "/conventions.md"

// conventionsRegions are the generated sections the conventions page
// carries, so the code registry and the list of definition-block kinds
// it states are always the ones the front matter and the schemas give.
var conventionsRegions = []string{RegionCodeRegistry, RegionDefinitionKinds}

// regionNames are the region names a marker may carry.
var regionNames = []string{
	RegionSettingsRead, RegionMetricsEmitted, RegionEventsEmitted,
	RegionRBACGrants, RegionNetworkRules, RegionIndex, RegionDefinitionKinds,
	RegionCodeRegistry,
}

var (
	regionOpen  = regexp.MustCompile(`^<!-- generated:([a-z0-9-]+) -->$`)
	regionClose = regexp.MustCompile(`^<!-- /generated:([a-z0-9-]+) -->$`)
)

// region is one generated section of a body: the indexes of its opening
// and closing marker lines.
type region struct {
	name        string
	open, close int
}

// findRegions locates the marker pairs of a specification page's body
// outside code fences. It reports an unknown name, a nested or unclosed
// opening marker, a closing marker with no opening, and a name used twice.
// bodyLine is the file line of the body's first line.
func findRegions(path, body string, bodyLine int) ([]region, []Problem) {
	return findNamedRegions(path, body, bodyLine, regionNames)
}

// findNamedRegions is findRegions over a given set of region names, so a
// documentation page and a specification page each accept only their own.
func findNamedRegions(path, body string, bodyLine int, names []string) ([]region, []Problem) {
	lines := strings.Split(body, "\n")
	var out []region
	var problems []Problem
	at := func(i int, format string, args ...any) {
		problems = append(problems, Problem{Path: path, Line: bodyLine + i, Message: fmt.Sprintf(format, args...)})
	}
	seen := map[string]bool{}
	openAt, openName := -1, ""
	for i := 0; i < len(lines); i++ {
		if m := fenceOpen.FindStringSubmatch(lines[i]); m != nil {
			i = fenceEnd(lines, i, m[1])
			continue
		}
		t := strings.TrimSpace(lines[i])
		if m := regionOpen.FindStringSubmatch(t); m != nil {
			switch {
			case !contains(names, m[1]):
				at(i, "unknown generated section %q; the sections are %s", m[1], strings.Join(names, ", "))
			case openAt >= 0:
				at(i, "generated section %q opens inside %q", m[1], openName)
			default:
				openAt, openName = i, m[1]
			}
			continue
		}
		if m := regionClose.FindStringSubmatch(t); m != nil {
			if openAt < 0 || m[1] != openName {
				at(i, "closing marker for %q has no matching opening marker", m[1])
				continue
			}
			if seen[openName] {
				at(openAt, "generated section %q appears twice on the page", openName)
			} else {
				out = append(out, region{name: openName, open: openAt, close: i})
			}
			seen[openName] = true
			openAt, openName = -1, ""
		}
	}
	if openAt >= 0 {
		at(openAt, "generated section %q has no closing marker", openName)
	}
	return out, problems
}

// replaceRegions returns the body with each region's content replaced by
// what fill returns for its name. The markers stay, and the content sits
// between them with one blank line on either side. Empty content leaves
// one blank line between the markers.
func replaceRegions(body string, regions []region, fill func(name string) string) string {
	if len(regions) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	var sb strings.Builder
	last := 0
	for _, r := range regions {
		sb.WriteString(strings.Join(lines[last:r.open+1], "\n"))
		sb.WriteString("\n\n")
		if content := strings.TrimRight(fill(r.name), "\n"); content != "" {
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
		last = r.close
	}
	sb.WriteString(strings.Join(lines[last:], "\n"))
	return sb.String()
}

// regionBlock renders a complete marked region, for appending to a page
// that has none.
func regionBlock(name, content string) string {
	return "<!-- generated:" + name + " -->\n\n" + strings.TrimRight(content, "\n") + "\n\n<!-- /generated:" + name + " -->\n"
}

// unitRegions returns, for each page code, the regions the blocks of the
// tree fill on that code's page.
func unitRegions(t *Tree) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(code, region string) {
		if out[code] == nil {
			out[code] = map[string]bool{}
		}
		out[code][region] = true
	}
	for _, b := range t.Blocks() {
		switch b.Kind {
		case KindSetting:
			for _, c := range b.List("read-by") {
				add(c, RegionSettingsRead)
			}
		case KindMetric:
			for _, c := range b.List("emitter") {
				add(c, RegionMetricsEmitted)
			}
		case KindEvent:
			for _, c := range b.List("emitter") {
				add(c, RegionEventsEmitted)
			}
		case KindRBACGrant:
			add(b.Text("unit"), RegionRBACGrants)
		case KindNetworkRule:
			add(b.Text("unit"), RegionNetworkRules)
		}
	}
	return out
}

// checkRegions reports malformed markers on every page, a conventions page
// that lacks the code registry or the definition-kinds section, and a page
// that a block names (by read-by, emitter, or unit) but that lacks the
// section the block fills. A placeholder page is exempt from the last
// rule, because its content has not been written yet.
func checkRegions(t *Tree) []Problem {
	var problems []Problem
	needed := unitRegions(t)
	for _, p := range t.Pages {
		regions, ps := findRegions(p.Path, p.Body, p.bodyLine)
		problems = append(problems, ps...)
		if p.IsPlaceholder() {
			continue
		}
		have := map[string]bool{}
		for _, r := range regions {
			have[r.name] = true
		}
		if p.Path == ConventionsPath {
			for _, name := range conventionsRegions {
				if !have[name] {
					problems = append(problems, Problem{Path: p.Path, Line: 0, Message: fmt.Sprintf(
						"the conventions page needs the generated section <!-- generated:%s --> ... <!-- /generated:%s -->", name, name,
					)})
				}
			}
		}
		for _, name := range regionNames {
			if needed[p.Front.Code][name] && !have[name] {
				problems = append(problems, Problem{Path: p.Path, Line: 0, Message: fmt.Sprintf(
					"definition blocks name %s, so the page needs the generated section <!-- generated:%s --> ... <!-- /generated:%s -->",
					p.Front.Code, name, name,
				)})
			}
		}
	}
	return problems
}
