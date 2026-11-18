// SPDX-License-Identifier: MIT

// Package survey reads the specification structure, the code's
// annotations, the documentation's front matter, and the issue records
// together, for the helpers that report on them:
//
//	specshift digest [-root <path>] [-base <rev>] [-stdout] [-force] <step>
//	specshift where [-root <path>] <identifier | code | anchor>
//	specshift since [-root <path>] <YYYY-MM-DD | commit>
//
// digest writes changes/<folder>/digests/<step>.md for a step's branch:
// the requirements, operations, and definition blocks the branch adds,
// modifies, and retires against its merge base with main, with the old
// and new text of each, their code sites and tests from the `// spec:`
// annotations, the documentation pages whose front matter names a
// touched page, the catalog names the documentation still uses after the
// branch removed them, and the requirements cited from more than three
// functions. It leaves headed placeholders for the gate results, the
// review outcomes, and the defaults applied, which the session fills in.
//
// where prints one identifier's text, its page, its code sites and
// tests, the documentation pages that describe its page, the open issues
// that name it, and the last commit that changed its text.
//
// since lists the requirements and operations added, modified, and
// retired since a date or a commit, and the Go packages changed since
// then, grouped by the area of the pages their doc.go names.
//
// CheckCoverage is the coverage gate of change 0116, which the static
// tier runs as TestEveryChangedRequirementHasACitingTest. For each
// requirement the working tree adds, or whose text it modifies, against
// its merge base with main, it finds the test functions whose doc-comment
// annotation cites the requirement directly and the open or planned issue
// records that list it under `requirements`. A requirement with neither
// fails the gate. A requirement that moves with its text unchanged, a
// retired requirement, an operation, a definition block, and a numbered
// section are outside it.
//
// A snapshot of the specification at a commit is read by extracting the
// commit's spec/ directory and building the reference pass's registry
// over it, so the identifiers each command compares are the ones the
// committed registry would record. The working tree is read in place,
// including files not yet committed.
//
// These commands are contributor tooling over the specification
// structure of change 0115 and carry no specification annotation.
package survey
