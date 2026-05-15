// SPDX-License-Identifier: MIT

package main

// Canonical file paths the harness reads and writes.
//
// Centralizing these here keeps the path strings out of flag
// defaults, validators, and reporters where they'd otherwise drift.
// Every reference to one of these locations should use the
// constant instead of a bare literal.
//
// Paths are repo-relative; absolute paths are constructed via
// filepath.Join(repoRoot(), ...) at the call site so they stay
// portable across the harness's working directory.

const (
	// resultsDir holds verdict files and the history log.
	resultsDir = "tests/results"

	// latestVerdictFile is the canonical "most recent verdict"
	// path under resultsDir. PR comments and CI artifacts point at
	// this file.
	latestVerdictFile = resultsDir + "/latest.json"

	// verdictHistoryFile accumulates one-line JSON summaries per
	// run per §21.2. Append-only; the §21.2 root-cause analyzer
	// reads it.
	verdictHistoryFile = "history.jsonl"

	// coverProfileFile is where Go coverage profiles land when
	// LENNY_COVER_PROFILE is unset.
	coverProfileFile = resultsDir + "/cover.out"

	// specMapFile maps every spec section to its tests, packages,
	// and other traceability artifacts.
	specMapFile = "tests/spec-map.json"

	// specMapExceptionsFile records sections that intentionally
	// have no tests (non-normative, post-v1, etc.).
	specMapExceptionsFile = "tests/spec-map-exceptions.yaml"

	// changeGraphFile maps source paths to the tiers that
	// exercise them; the --changed selector walks this graph.
	changeGraphFile = "tests/change-graph.json"

	// groupsFile defines named test-selection groups (pr-fast,
	// nightly, phase-N-gate).
	groupsFile = "tests/groups.yaml"

	// groupsSubsetsFile defines the subset names referenced from
	// groups.yaml's include: clauses.
	groupsSubsetsFile = "tests/groups.subsets.yaml"

	// flakeBudgetFile lists quarantined tests per §21.4.
	flakeBudgetFile = "tests/flake-budget.yaml"

	// parityMatrixFile lists every cloud capability and the
	// providers it is validated against.
	parityMatrixFile = "tests/tier6_e2e_cloud/parity-matrix.yaml"
)
