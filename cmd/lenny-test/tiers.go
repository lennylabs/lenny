// SPDX-License-Identifier: MIT

package main

// allTiers returns the tier names in execution order. The order is the gate
// hierarchy from TESTING.md §3: lower-numbered tiers gate higher ones.
func allTiers() []string {
	return []string{
		"static",
		"unit",
		"component",
		"contract",
		"integration",
		"e2e_kind",
		"e2e_cloud",
		"load",
		"chaos",
		"security",
		"conformance",
		"docs",
	}
}

// tiersForGroup returns the tier plan implied by a named group.
//
// Phase 0 implementation: hard-coded mapping for the canonical groups. Phase 1
// reads tests/groups.yaml at runtime and resolves subsets from
// tests/groups.subsets.yaml.
func tiersForGroup(name string) []tierPlan {
	switch name {
	case "pr-fast":
		return []tierPlan{
			{name: "static", notes: "changed-only when implemented"},
			{name: "unit", notes: "changed-only when implemented"},
			{name: "component", notes: "changed-only when implemented"},
		}
	case "pr":
		return []tierPlan{
			{name: "static"},
			{name: "unit"},
			{name: "component"},
			{name: "contract"},
			{name: "integration"},
			{name: "e2e_kind", subsets: []string{"critical-path"}},
			{name: "security", subsets: []string{"critical"}},
			{name: "chaos", subsets: []string{"core"}},
			{name: "conformance", subsets: []string{"bundled-runtimes"}},
			{name: "docs"},
		}
	case "nightly":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"critical-path-rotated"},
			"load":        {"per-phase-baseline"},
			"conformance": {"reference-catalog"},
		})
	case "weekly":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"full-non-sandbox"},
			"load":        {"full-system"},
			"conformance": {"reference-catalog"},
		})
	case "pre-release":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"full"},
			"load":        {"full-system"},
			"chaos":       {"full"},
			"security":    {"full", "pentest"},
			"conformance": {"reference-catalog"},
		})
	case "phase-0-gate":
		return []tierPlan{
			{name: "static"},
			{name: "docs"},
		}
	case "phase-1-gate":
		return []tierPlan{
			{name: "static", notes: "schemas validate, contract tests compile, examples round-trip"},
			{name: "unit", notes: "state-machine packages: Session, TaskRecord, Sandbox, SandboxClaim"},
		}
	case "phase-1.5-gate":
		return []tierPlan{
			{name: "static", notes: "lint-schema.sh (R-01) + lint-queries.sh (R-02) + schema validation"},
			{name: "component", subsets: []string{"migrations"}, notes: "migration framework: round-trip, idempotency, rollback, regression"},
		}
	}

	// Phase 0 stub: every other phase-<N>-gate is recognized so the CLI does
	// not error, but resolution is deferred to Phase 1.
	if isPhaseGate(name) {
		return []tierPlan{
			{name: "static", notes: "phase-0-stub: full phase-gate resolution pending"},
		}
	}
	return nil
}

func isPhaseGate(name string) bool {
	const prefix = "phase-"
	const suffix = "-gate"
	if len(name) < len(prefix)+len(suffix) {
		return false
	}
	return name[:len(prefix)] == prefix && name[len(name)-len(suffix):] == suffix
}

func tiersAll(subsets map[string][]string) []tierPlan {
	plans := []tierPlan{}
	for _, t := range allTiers() {
		p := tierPlan{name: t}
		if s, ok := subsets[t]; ok {
			p.subsets = s
		}
		plans = append(plans, p)
	}
	return plans
}
