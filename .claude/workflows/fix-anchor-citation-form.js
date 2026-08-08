// fix-anchor-citation-form: stop the anchor pass writing a file path into prose.
//
// THE DEFECT. scripts/specshift/anchor/heading.go citationFor renders the form a
// resolved citation is written in:
//
//     if citation.IsSpecFile(t.File) && heading.Number != "" {
//         return "§" + heading.Number, nil
//     }
//     return t.String(), nil        // <path>#<anchor>
//
// The fallback is right for a fragment link, whose target IS a path and anchor.
// It is wrong for a bare §X.Y citation written in a sentence, which the pass
// rewrites with the same string. Measured over a trial application of the pass,
// 92 sites took a raw path where a section reference belongs:
//
//   before: // The gateway owns schema_version per §15.4.1;
//   after:  // The gateway owns schema_version per spec/15_external-api-surface.md#messageenvelope--unified-message-format;
//
//   before: // newMessageID returns a §15.4.1-shaped message id.
//   after:  // newMessageID returns a spec/15_external-api-surface.md#messageenvelope--unified-message-format-shaped message id.
//
// The second is not merely ugly; the sentence no longer parses.
//
// WHY THE FALLBACK FIRES. §15.4.1's content split three ways when the section was
// reduced. 499 sites resolve to spec/28#2853-intra-pod, whose anchor carries a
// number, so they render as §28.5.3 and read correctly. 92 resolve to headings
// the carve-out kept in spec/15 that carry no number of their own
// (#messageenvelope--unified-message-format, #translation-fidelity-matrix), and
// those take the path.
//
// THE RULE TO WRITE INSTEAD. A heading with no number of its own still sits
// inside a numbered section. Cite that section. #messageenvelope--unified-message-format
// sits under ## 15.4, so a bare citation resolving to it is written §15.4. This is
// the rule §29.1 already fixes for specification prose: cite the surviving parent
// rather than the retired anchor. A fragment link keeps taking the path and
// anchor, because a link target is a path and an anchor.
//
// WHAT MADE THIS HARD TO SEE, and what the verification here must therefore do.
// Two automated checks passed over these 92 sites. A whole-file check that strips
// citation tokens from both sides and compares the remainder is blind by
// construction, because the path IS a citation token and vanishes from both
// sides. A diff check asking whether any non-citation text changed also passes,
// because none did. Neither asks the question that matters: is the citation the
// pass WROTE a well-formed citation for the context it landed in. Verification
// below must check the written form, not only the untouched surroundings.

export const meta = {
  name: "fix-anchor-citation-form",
  description:
    "Render a bare citation resolving to an unnumbered heading as its numbered parent section rather than as a file path",
  phases: [
    { title: "Fix", detail: "cite the nearest numbered ancestor, and test it" },
    { title: "Verify", detail: "apply on a copy and check the written citation form, not only the surroundings" },
  ],
};

const REPO = "/home/ec2-user/lenny";

const CTX = `
Repository: ${REPO}. Work there.

THE DEFECT is in \`scripts/specshift/anchor/heading.go\`, in \`citationFor\`:

    if citation.IsSpecFile(t.File) && heading.Number != "" {
        return "§" + heading.Number, nil
    }
    return t.String(), nil

\`t.String()\` is \`<path>#<anchor>\`. That is the correct rewrite for a fragment
link. It is the wrong rewrite for a bare §X.Y citation in a sentence, and the pass
uses this one function for both. A trial application put a raw file path into
prose at 92 sites, including one where the sentence stops parsing:

    // newMessageID returns a §15.4.1-shaped message id.
 -> // newMessageID returns a spec/15_external-api-surface.md#messageenvelope--unified-message-format-shaped message id.

THE RULE TO IMPLEMENT. A heading carrying no number of its own still sits inside a
numbered section, and that section is what a bare citation should name. The
carve-out headings in spec/15 (#messageenvelope--unified-message-format,
#translation-fidelity-matrix) sit under \`## 15.4\`, so a bare citation resolving
to either is written \`§15.4\`. This matches the rule §29.1 fixes for
specification prose: cite the surviving parent rather than the retired anchor.

A FRAGMENT LINK IS UNCHANGED. A link's target is a path and an anchor and must
stay one. Only the bare-citation rendering changes. If the two forms are produced
by one function today, they have to stop sharing, and the reason belongs in the
commentary.

The headings index already parses every heading of every file in order, so the
nearest preceding numbered heading is available without reading anything new.
Confirm that before assuming it.

CONSTRAINTS.
  Do not weaken any fail-closed behaviour. Two aborts are load-bearing and pinned:
  a citation naming an anchor the map retires with no sense-register entry, and a
  map entry whose successor heading does not exist.
  Do not edit any proposal under proposals/.
  Do not correct citations in the tree by hand; this is a tooling change.
  Match the surrounding code, which documents at length why each rule exists.
  Two test failures are PRE-EXISTING and reproduce at HEAD:
  TestLoadTableReadsTheLandedNamingTable_spec_28_3 and
  TestAMisSeededOccurrenceFailsTheSpecificationConfinedRuns... Do not fix them.
`;

phase("Fix");

const fix = await agent(
  `${CTX}

Implement the rule.

1. Read \`scripts/specshift/anchor/heading.go\` around \`citationFor\` and its
   commentary, and the callers that render a bare citation versus a fragment link.
   Establish whether one function serves both and where they diverge.
2. Change the bare-citation rendering so a destination inside a specification file
   whose heading carries no number resolves to the nearest enclosing numbered
   heading, written in the §-form. Leave the fragment-link rendering alone.
3. Decide deliberately what happens when there is NO enclosing numbered heading,
   and say why in the commentary. Erroring is defensible for a fail-closed tool;
   silently falling back to the path is what produced this defect. Do not leave
   the case undefined.
4. Rewrite the commentary that currently says a heading with no number "has no
   number to cite either". It is what licensed the defect. State the rule that
   replaces it and why a bare citation and a link take different forms.
5. Update the tests. Add regression cases:
     - a bare citation resolving to an unnumbered spec heading renders as the
       enclosing numbered section, NOT as a path;
     - a fragment link to the same destination still renders as path and anchor;
     - a bare citation resolving to a NUMBERED spec heading still renders in the
       §-form, which is the 499-site majority case and must not regress.
6. Run \`go test ./scripts/specshift/...\` and report, confirming the only failures
   are the two pre-existing ones.`,
  { label: "fix", phase: "Fix" },
);

phase("Verify");

const verify = await agent(
  `${CTX}

The rendering rule is changed:

${fix}

Verify it, and verify it in the way the earlier checks failed to.

1. Both dry runs, reporting exit code, planned file count, and every refusal:
     go run ./scripts/specshift -pass anchor -register tests/spec-anchor-moves.json -only spec/
     go run ./scripts/specshift -pass anchor -register tests/spec-anchor-moves.json -except spec/

2. Apply on an isolated copy made with \`cp -a\` into the scratch directory, never
   on ${REPO}. Do not run \`git checkout\`, \`git stash\` or \`git reset\` against
   ${REPO}; it holds uncommitted work.

3. THE CHECK THAT MATTERS, which two earlier checks missed. Do not only ask
   whether the surrounding text survived. Ask whether the citation the pass WROTE
   is well formed for where it landed. Specifically, over the applied copy:
     a. Count every occurrence the pass introduced of a bare \`spec/<file>.md#<anchor>\`
        appearing OUTSIDE a markdown link, that is not preceded by \`(\` or \`[\`.
        This number must be ZERO. It was 92 before the fix.
     b. Count the bare citations rendered in the §-form, and confirm the 499-site
        §28.5.3 majority is unchanged.
     c. Confirm every fragment link still renders as path and anchor.
     d. Show ten before-and-after pairs, chosen to include the sites that were
        broken: the transcriptstore comments, the migrations, and the
        newMessageID "-shaped message id" sentence, which must now read as a
        section reference and parse as English.
   Then delete the copy.

4. Confirm no non-citation text changed anywhere: for each changed file, strip
   citation tokens from the HEAD version and the applied version and compare the
   remainders. Report the count that differ; it must be zero apart from files this
   tooling change itself edits under scripts/specshift/.

5. Report tier 0 from source, since the checked-in binary is stale:
     go run ./cmd/lenny-test --tier static

Lead the report with the count from 3a. That is the number that decides whether
this is done.`,
  { label: "verify", phase: "Verify" },
);

return { fix, verify };
