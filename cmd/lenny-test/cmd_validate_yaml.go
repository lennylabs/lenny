// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The three YAML configuration files under tests/ — groups.yaml,
// groups.subsets.yaml, and spec-map-exceptions.yaml — drive
// selector resolution, subset expansion, and the spec-coverage
// exemption list. A typo in any of them silently misroutes a CI
// run. These validators give validate-maps the same teeth on YAML
// that it already has on the JSON traceability files.

func validateGroupsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("groups.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	// Selectors uses yaml.Node so unknown-but-valid keys (cloud_subset,
	// load_scenarios, additional_checks, exclude, include_all_tiers, …)
	// do not trip the empty-selector check. We only need to confirm the
	// block exists and has at least one mapping entry.
	var doc struct {
		Version int `yaml:"version"`
		Groups  map[string]struct {
			Description string `yaml:"description"`
			Selectors   yaml.Node `yaml:"selectors"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("groups.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("groups.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Groups) == 0 {
		return newResult("groups.yaml", false, "groups: section is empty")
	}
	knownTiers := map[string]bool{}
	for _, t := range allTiers() {
		knownTiers[t] = true
	}
	var problems []string
	for name, g := range doc.Groups {
		if !isPhaseGate(name) && g.Description == "" {
			problems = append(problems, fmt.Sprintf("%s: missing description", name))
		}
		// Selectors must exist and be a non-empty mapping.
		if g.Selectors.Kind == 0 {
			problems = append(problems, fmt.Sprintf("%s: missing selectors block", name))
			continue
		}
		if g.Selectors.Kind != yaml.MappingNode || len(g.Selectors.Content) == 0 {
			problems = append(problems, fmt.Sprintf("%s: selectors block is empty", name))
			continue
		}
		// Walk the selector keys and verify tier-named values are
		// known. We pair-iterate Content (keys at even indices,
		// values at odd indices) per yaml.v3 mapping representation.
		for i := 0; i+1 < len(g.Selectors.Content); i += 2 {
			k := g.Selectors.Content[i]
			v := g.Selectors.Content[i+1]
			switch k.Value {
			case "max_tier", "tier":
				if v.Value != "" && !knownTiers[v.Value] {
					problems = append(problems, fmt.Sprintf("%s: unknown %s %q", name, k.Value, v.Value))
				}
			case "tiers":
				for _, t := range v.Content {
					if !knownTiers[t.Value] {
						problems = append(problems, fmt.Sprintf("%s: unknown tier %q", name, t.Value))
					}
				}
			case "include":
				for j, item := range v.Content {
					tier := mappingValue(item, "tier")
					if tier == "" {
						problems = append(problems, fmt.Sprintf("%s: include[%d] missing tier", name, j))
					} else if !knownTiers[tier] {
						problems = append(problems, fmt.Sprintf("%s: include[%d] unknown tier %q", name, j, tier))
					}
				}
			}
		}
	}
	if len(problems) > 0 {
		return newResult("groups.yaml", false, summarizeProblems(problems))
	}
	return newResult("groups.yaml", true, fmt.Sprintf("%d group(s); every selector resolves", len(doc.Groups)))
}

func validateGroupsSubsetsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	// Subsets carry their contents through one of several slots:
	//   tests:        []string of paths, or the scalar `all`
	//   scenarios:    same shape as tests, for tier 7 load
	//   scenarios_ref: name of another subset (delegation)
	//   runtimes:     []string of runtime ids (tier 10 conformance)
	//   run:          Go regexp matched against test names (tier 4)
	// We accept yaml.Node for the polymorphic slots and only verify
	// at least one is populated.
	var doc struct {
		Version int `yaml:"version"`
		Subsets map[string]struct {
			Tier         string    `yaml:"tier"`
			Tests        yaml.Node `yaml:"tests"`
			Scenarios    yaml.Node `yaml:"scenarios"`
			ScenariosRef string    `yaml:"scenarios_ref"`
			Runtimes     []string  `yaml:"runtimes"`
			Run          string    `yaml:"run"`
		} `yaml:"subsets"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Subsets) == 0 {
		return newResult("groups.subsets.yaml", false, "subsets: section is empty")
	}
	knownTiers := map[string]bool{}
	for _, t := range allTiers() {
		knownTiers[t] = true
	}
	var problems []string
	for name, s := range doc.Subsets {
		if s.Tier == "" {
			problems = append(problems, fmt.Sprintf("%s: missing tier", name))
		} else if !knownTiers[s.Tier] {
			problems = append(problems, fmt.Sprintf("%s: unknown tier %q", name, s.Tier))
		}
		// A subset declares its contents through one of three slots:
		// tests (most tiers), scenarios (tier 7 load), or scenarios_ref
		// (delegates to another subset). Each may carry a sequence of
		// paths or the scalar sentinel `all`.
		hasTests := s.Tests.Kind == yaml.SequenceNode && len(s.Tests.Content) > 0
		hasTests = hasTests || (s.Tests.Kind == yaml.ScalarNode && s.Tests.Value != "")
		hasScenarios := s.Scenarios.Kind == yaml.SequenceNode && len(s.Scenarios.Content) > 0
		hasScenarios = hasScenarios || (s.Scenarios.Kind == yaml.ScalarNode && s.Scenarios.Value != "")
		if !hasTests && !hasScenarios && s.ScenariosRef == "" && len(s.Runtimes) == 0 && s.Run == "" {
			problems = append(problems, fmt.Sprintf("%s: no tests/scenarios/runtimes/run/scenarios_ref", name))
		}
	}
	if len(problems) > 0 {
		return newResult("groups.subsets.yaml", false, summarizeProblems(problems))
	}
	return newResult("groups.subsets.yaml", true,
		fmt.Sprintf("%d subset(s); every entry has a tier and at least one content slot", len(doc.Subsets)))
}

func validateSpecMapExceptionsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	var doc struct {
		Version    int `yaml:"version"`
		Exceptions []struct {
			Section       string `yaml:"section"`
			Reason        string `yaml:"reason"`
			Justification string `yaml:"justification"`
		} `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Exceptions) == 0 {
		return newResult("spec-map-exceptions.yaml", true, "no exceptions defined")
	}
	allowedReasons := map[string]bool{
		"non-normative":     true,
		"indirect-coverage": true,
		"meta":              true,
		"deferred":          true,
		"empty":             true,
		"post-v1":           true,
		"anti-feature":      true,
	}
	var problems []string
	seen := map[string]int{}
	for i, e := range doc.Exceptions {
		if e.Section == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d]: missing section", i))
			continue
		}
		if prev, dup := seen[e.Section]; dup {
			problems = append(problems, fmt.Sprintf("exceptions[%d]: section %q already declared at exceptions[%d]", i, e.Section, prev))
		}
		seen[e.Section] = i
		if e.Reason == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): missing reason", i, e.Section))
		} else if !allowedReasons[e.Reason] {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): unknown reason %q", i, e.Section, e.Reason))
		}
		if strings.TrimSpace(e.Justification) == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): missing justification", i, e.Section))
		}
	}
	if len(problems) > 0 {
		return newResult("spec-map-exceptions.yaml", false, summarizeProblems(problems))
	}
	return newResult("spec-map-exceptions.yaml", true,
		fmt.Sprintf("%d exception(s); every entry has a reason and a justification", len(doc.Exceptions)))
}

// mappingValue returns the scalar value of `key` inside a YAML
// mapping node, or empty string when the key is absent or the value
// is not scalar.
func mappingValue(node *yaml.Node, key string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func summarizeProblems(problems []string) string {
	preview := problems
	if len(preview) > 5 {
		preview = append(append([]string{}, preview[:5]...),
			fmt.Sprintf("... (%d more)", len(problems)-5))
	}
	return fmt.Sprintf("%d issue(s): %s", len(problems), strings.Join(preview, "; "))
}

// yamlPaths returns the canonical paths to the three YAML config
// files under tests/.
func yamlPaths(root string) (groups, subsets, exceptions string) {
	groups = filepath.Join(root, "tests", "groups.yaml")
	subsets = filepath.Join(root, "tests", "groups.subsets.yaml")
	exceptions = filepath.Join(root, "tests", "spec-map-exceptions.yaml")
	return
}
