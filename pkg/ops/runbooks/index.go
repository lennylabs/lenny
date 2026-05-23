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

// Filter is the §25.7 Path A runbook discovery filter. Every set field
// must match; an empty filter matches every runbook.
type Filter struct {
	// Alert matches a runbook whose triggers name the alert.
	Alert string
	// Component matches a runbook listing the health-API component.
	Component string
	// Tag matches a runbook carrying the tag.
	Tag string
	// Symptom matches a runbook any of whose symptom strings contains
	// it as a substring.
	Symptom string
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
	if f.Symptom != "" && !anyContains(fm.Symptoms, f.Symptom) {
		return false
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

func anyContains(list []string, substr string) bool {
	for _, x := range list {
		if strings.Contains(x, substr) {
			return true
		}
	}
	return false
}
