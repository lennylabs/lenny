// SPDX-License-Identifier: MIT

// Package pagegate holds the gates that read the text of the
// specification tree's pages, and the gate that holds the contribution
// blocks against the chart.
//
// The pages are the ones the defs package reads: the Markdown files under
// spec/ whose front matter carries a code. A numbered file joins them
// once its sections are successor stubs, which StubbedNumberedFile
// decides; until the stub format lands, no numbered file is in scope.
//
// The text gates are:
//
//   - the gloss rule: a borrowed term's row in a page's Terms table holds
//     no number and no normative keyword in its gloss, and exactly one
//     link;
//   - walkthroughs: a page of type walkthrough holds no normative keyword;
//   - documentation style: a page holds none of the banned words and
//     phrases of .claude/rules/doc-style.md that a pattern can find, and
//     no heading that names a count, except that the headings of a
//     numbered file's successor stubs keep the wording their citations
//     reach them by;
//   - review comments: a capability page whose area has landed (no page
//     under its spec/capabilities/<area>/ folder is a placeholder), and a
//     page in no area whose status is neither placeholder nor partial,
//     hold no src, judgment, or unresolved review comment.
//
// A normative keyword is a BCP 14 keyword in upper case, or one of the
// lower-case words "must", "shall", and "should", as the conventions page
// states under "Normative language".
//
// Sizes reports a page over its word budget as a warning rather than a
// problem: about 6,000 words for a content page and about 1,500 for an
// index. A catalog has no budget, because it is generated.
//
// CheckContributions holds each grant, rbac-grant, and network-rule block
// against the chart as `helm template` renders it: the Role or ClusterRole
// bound to the unit's ServiceAccount carries the grant, and a
// NetworkPolicy that selects the unit's pods allows the traffic. The unit
// register maps a unit's page code to its ServiceAccount.
//
// The package implements contributor tooling for the specification's own
// structure rather than a platform behavior, so its code and tests carry
// no specification annotation.
package pagegate
