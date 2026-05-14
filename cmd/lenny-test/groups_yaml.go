// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// groupsDoc mirrors the relevant subset of tests/groups.yaml. We
// only read the fields we use to resolve tiersForGroup; the file
// has more shape (descriptions, additional_checks) that is
// preserved on the YAML side.
type groupsDoc struct {
	Version int                       `yaml:"version"`
	Groups  map[string]groupSelectors `yaml:"groups"`
}

type groupSelectors struct {
	Description string        `yaml:"description"`
	Selectors   groupSelector `yaml:"selectors"`
}

type groupSelector struct {
	Tiers           []string       `yaml:"tiers"`
	SpecSections    []string       `yaml:"spec_sections"`
	MaxTier         string         `yaml:"max_tier"`
	Include         []groupInclude `yaml:"include"`
	AdditionalCheck []string       `yaml:"additional_checks"`
}

type groupInclude struct {
	Tier   string `yaml:"tier"`
	Subset string `yaml:"subset"`
}

// loadGroupsYAML parses tests/groups.yaml. Returns an empty doc
// when the file is missing.
func loadGroupsYAML() (groupsDoc, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot(), "tests", "groups.yaml"))
	if err != nil {
		return groupsDoc{}, err
	}
	var doc groupsDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return doc, err
	}
	return doc, nil
}

// tiersForGroupFromYAML resolves a group name through groups.yaml
// (when the YAML defines the named group). Returns an empty plan
// when the group is not in the YAML; callers fall back to the
// hard-coded mapping in tiersForGroup.
func tiersForGroupFromYAML(name string) []tierPlan {
	doc, err := loadGroupsYAML()
	if err != nil {
		return nil
	}
	g, ok := doc.Groups[name]
	if !ok {
		return nil
	}
	sel := g.Selectors
	if len(sel.Tiers) == 0 && len(sel.Include) == 0 {
		return nil
	}
	// Build the plan by walking the selectors block.
	plan := []tierPlan{}
	seen := map[string]int{} // tier name → index into plan
	for _, t := range sel.Tiers {
		seen[t] = len(plan)
		plan = append(plan, tierPlan{name: t})
	}
	// `include` entries add tiers (or extend the subset list of an
	// existing tier). Subset strings can be comma-separated.
	for _, inc := range sel.Include {
		subsets := splitNonEmpty(inc.Subset)
		if idx, ok := seen[inc.Tier]; ok {
			plan[idx].subsets = append(plan[idx].subsets, subsets...)
			continue
		}
		seen[inc.Tier] = len(plan)
		plan = append(plan, tierPlan{name: inc.Tier, subsets: subsets})
	}
	// Apply max_tier when present.
	if sel.MaxTier != "" {
		bound := indexOf(allTiers(), sel.MaxTier)
		filtered := []tierPlan{}
		for _, p := range plan {
			if i := indexOf(allTiers(), p.name); i >= 0 && i <= bound {
				filtered = append(filtered, p)
			}
		}
		plan = filtered
	}
	return plan
}
