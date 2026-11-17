// SPDX-License-Identifier: MIT

// Package docgate holds the gates that keep the documentation under docs/
// in step with the specification, as .claude/rules/doc-content.md states
// under "Documentation and the specification":
//
//	specshift docs [-root <path>] check
//	specshift docs [-root <path>] impact
//
// check runs two gates over every documentation page:
//
//   - front matter: a page whose YAML front matter carries the key spec
//     lists page codes, and each code resolves to a page, or an alias of
//     one, in spec/registry.json. A page whose front matter has no spec
//     key is not checked yet, because front matter is added area by area.
//   - catalog names: on a page in scope, every setting, metric, alert,
//     error code, label, annotation, event type, and CLI command the page
//     names in a code span exists in the catalogs, and a default, limit,
//     or unit the page quotes next to such a name matches the definition
//     block.
//
// A page is in scope once every code its spec list names belongs to a
// page that has landed: its status in the registry is neither placeholder
// nor partial. A page with spec: [] describes no specification page and is
// in scope at once.
//
// The catalog-name gate is conservative. It reads code spans only, outside
// fenced code blocks, and decides what a span names from the span's form
// and its context. A span whose form fixes its kind, such as a metric name
// that starts with lenny_, a key under lenny.dev/, an event type under
// dev.lenny., or a command that starts with lenny-ctl, must exist in the
// catalogs. A span whose form is ambiguous, such as an upper-case name or
// a dotted field path, is classified only when the word beside it or its
// table column names the kind. A name the catalogs do not hold but that a
// numbered section of the specification still defines, and a name only
// the context classifies, is reported as undecided rather than refused,
// and so is a quoted value the gate cannot compare. Undecided findings
// never fail the gate.
//
// impact reads the branch's diff against its merge base with main, found
// as the annotation lint finds it. A requirement, operation, or definition
// block the diff adds, modifies, or retires on a landed specification page
// requires that the diff also edits a documentation page whose front
// matter names that page, or that a change folder the diff touches states
// in the Documentation section of its change.md that no reader-facing
// behavior changes. A page that has not landed is listed as not checked
// yet. The gate skips with a reason when there is no merge base.
//
// The generated reference pages are held by specshift defs generate
// --check, which fills their regions from the catalogs.
//
// The package implements contributor tooling for the specification's own
// structure rather than a platform behavior, so its code and tests carry
// no specification annotation.
package docgate
