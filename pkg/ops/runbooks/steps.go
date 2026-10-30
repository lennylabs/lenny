// SPDX-License-Identifier: MIT

package runbooks

import (
	"strconv"
	"strings"
)

// AccessPath is one access-level variant of a §25.7 runbook step: the
// command form for a particular caller capability (lenny-ctl, the
// admin API, or kubectl).
type AccessPath struct {
	// Access is the caller capability: "lenny-ctl", "api", or "kubectl".
	Access string `json:"access"`
	// Method and Path are populated for the "api" access form.
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	// Requires names a capability the path additionally needs, e.g.
	// "cluster-access" for a kubectl path.
	Requires string `json:"requires,omitempty"`
	// Commands are the shell or API lines for the path.
	Commands []string `json:"commands,omitempty"`
}

// Step is one §25.7 runbook step: a titled diagnosis or remediation
// step with the access-path variants the indexer extracted.
type Step struct {
	ID    string       `json:"id"`
	Title string       `json:"title"`
	Paths []AccessPath `json:"paths"`
}

// ParseSteps extracts the §25.7 structured steps from a runbook
// markdown body. Each `### ` heading begins a step; each
// `<!-- access: ... -->` comment begins an access path whose commands
// are the lines of the fenced code block that follows. Headings with
// no access comment (decision and escalation prose) are skipped.
func ParseSteps(markdown []byte) []Step {
	lines := strings.Split(string(markdown), "\n")
	var steps []Step
	var cur *Step
	var path *AccessPath
	inFence := false

	flushPath := func() {
		if cur != nil && path != nil {
			cur.Paths = append(cur.Paths, *path)
		}
		path = nil
	}
	flushStep := func() {
		flushPath()
		// A heading is a step only if it carried access paths; prose
		// headings (Decision, Escalation) are skipped. Step IDs are
		// sequential over the kept steps.
		if cur != nil && len(cur.Paths) > 0 {
			cur.ID = stepID(len(steps) + 1)
			steps = append(steps, *cur)
		}
		cur = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "### "):
			flushStep()
			inFence = false
			cur = &Step{Title: strings.TrimSpace(line[4:])}
		case strings.HasPrefix(trimmed, "<!-- access:") && strings.HasSuffix(trimmed, "-->"):
			flushPath()
			inFence = false
			if cur != nil {
				p := parseAccessComment(trimmed)
				path = &p
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence = !inFence
		case inFence && path != nil && trimmed != "":
			path.Commands = append(path.Commands, trimmed)
		}
	}
	flushStep()
	return steps
}

func stepID(n int) string {
	return "step-" + strconv.Itoa(n)
}

// parseAccessComment decodes a `<!-- access: <access> key=value ... -->`
// marker into an AccessPath. The first token after `access:` is the
// access level; the remaining `key=value` tokens populate method,
// path, and requires.
func parseAccessComment(comment string) AccessPath {
	inner := strings.TrimSpace(comment)
	inner = strings.TrimPrefix(inner, "<!--")
	inner = strings.TrimSuffix(inner, "-->")
	inner = strings.TrimSpace(inner)
	inner = strings.TrimPrefix(inner, "access:")

	var p AccessPath
	for i, tok := range strings.Fields(inner) {
		if i == 0 {
			p.Access = tok
			continue
		}
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch key {
		case "method":
			p.Method = value
		case "path":
			p.Path = value
		case "requires":
			p.Requires = value
		}
	}
	return p
}
