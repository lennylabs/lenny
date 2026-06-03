// SPDX-License-Identifier: MIT

// Package runbooks parses the §25.7 operational-runbook front matter
// and applies the §25.7 Path A discovery filter. lenny-ops indexes the
// runbook markdown under docs/runbooks/ and serves the index through
// GET /v1/admin/runbooks; this package is the parsing and matching
// logic behind that index.
package runbooks

import (
	"errors"
	"fmt"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// Trigger links a runbook to an alert (§25.7 front-matter triggers).
type Trigger struct {
	Alert    string `yaml:"alert" json:"alert"`
	Severity string `yaml:"severity" json:"severity,omitempty"`
}

// FrontMatter is the §25.7 runbook front matter parsed for discovery.
type FrontMatter struct {
	// Title is the human runbook title, searched by the `q` full-text
	// filter alongside symptoms and tags (§25.7 line 3143).
	Title      string    `yaml:"title" json:"title,omitempty"`
	Triggers   []Trigger `yaml:"triggers" json:"triggers,omitempty"`
	Components []string  `yaml:"components" json:"components,omitempty"`
	Symptoms   []string  `yaml:"symptoms" json:"symptoms,omitempty"`
	Tags       []string  `yaml:"tags" json:"tags,omitempty"`
	Requires   []string  `yaml:"requires" json:"requires,omitempty"`
	Related    []string  `yaml:"related" json:"related,omitempty"`
}

// Parse extracts and decodes the YAML front matter delimited by `---`
// at the start of a runbook markdown document.
func Parse(markdown []byte) (FrontMatter, error) {
	const delim = "---"
	s := string(markdown)
	if !strings.HasPrefix(s, delim+"\n") && !strings.HasPrefix(s, delim+"\r\n") {
		return FrontMatter{}, errors.New("runbook has no YAML front matter")
	}
	body := s[strings.IndexByte(s, '\n')+1:]
	end := strings.Index(body, "\n"+delim)
	if end < 0 {
		return FrontMatter{}, errors.New("runbook front matter is not terminated by ---")
	}
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(body[:end]), &fm); err != nil {
		return FrontMatter{}, fmt.Errorf("parse runbook front matter: %w", err)
	}
	return fm, nil
}

// Filter is the §25.7 Path A runbook discovery filter (spec lines
// 3140-3143). Every set field must match; an empty filter matches every
// runbook.
type Filter struct {
	// Alert matches a runbook whose triggers name the alert
	// (`?alert=`, against triggers[].alert).
	Alert string
	// Component matches a runbook listing the health-API component
	// (`?component=`, against components[]).
	Component string
	// Tag matches a runbook carrying the tag (`?tag=`, against tags[]).
	Tag string
	// Requires matches a runbook listing the named capability
	// (`?requires=`, against requires[]); the spec use case is "filter
	// to runbooks the agent can execute".
	Requires string
	// Query is the `?q=` full-text filter: every whitespace-separated
	// term must appear as a case-insensitive substring across the
	// runbook's symptoms, tags, and title (spec line 3143).
	Query string
}

// Matches reports whether a runbook with the given front matter
// satisfies the filter.
func Matches(fm FrontMatter, f Filter) bool {
	if f.Alert != "" && !triggersAlert(fm.Triggers, f.Alert) {
		return false
	}
	if f.Component != "" && !contains(fm.Components, f.Component) {
		return false
	}
	if f.Tag != "" && !contains(fm.Tags, f.Tag) {
		return false
	}
	if f.Requires != "" && !contains(fm.Requires, f.Requires) {
		return false
	}
	if f.Query != "" && !matchesQuery(fm, f.Query) {
		return false
	}
	return true
}

// matchesQuery implements the §25.7 `q` filter: a case-insensitive AND
// over the query's whitespace-separated terms, each of which must occur
// as a substring somewhere in the runbook's symptoms, tags, or title.
func matchesQuery(fm FrontMatter, query string) bool {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return true
	}
	var hay strings.Builder
	hay.WriteString(strings.ToLower(fm.Title))
	for _, s := range fm.Symptoms {
		hay.WriteByte('\n')
		hay.WriteString(strings.ToLower(s))
	}
	for _, t := range fm.Tags {
		hay.WriteByte('\n')
		hay.WriteString(strings.ToLower(t))
	}
	haystack := hay.String()
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func triggersAlert(triggers []Trigger, alert string) bool {
	for _, t := range triggers {
		if t.Alert == alert {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
