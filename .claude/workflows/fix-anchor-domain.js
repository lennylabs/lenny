// fix-anchor-domain: stop the anchor pass claiming citations it was never given.
//
// THE DEFECT. `specshift -pass anchor` aborts on both confinements with the tree
// unchanged, refusing 1,615 citations across 337 files. Each refusal reads: "the
// citation of §N names a section no specification file of the tree states a
// heading for, and the anchor-move map carries no successor for it."
//
// Those citations are not in the pass's job. Proposal 0064 states the abort
// condition for this pass as a citation naming a RETIRED ANCHOR with no map
// entry, and a map entry whose successor heading does not exist. A retired anchor
// is one the anchor-move map retires. The refused set is a different thing: a
// section that is not in the current tree and never was retired, so nothing ever
// gave it a successor.
//
// Measured composition of the 1,615:
//   1,592 stale Lenny section citations naming a numbering the tree does not use
//         (§3.4 at 160 sites, §3.2 at 117, §15.0 at 104, §12.3.7 at 97,
//          §6.39 at 74). spec/03_high-level-architecture.md carries only
//         "## 3. High-Level Architecture" and no numbered subsection at all.
//      23 regulatory citations: 45 CFR §164.530, §164.312, §164.502, §164.410 in
//         spec/11, spec/12, spec/16 and spec/17. These are HIPAA references. The
//         pass reads them as Lenny section citations and demands a successor.
//
// These are NOT the ~1,500 pre-existing stale citations proposal 0064 measures in
// its §4.6. That population is LINE citations that do not resolve inside the
// section they name, and it is tracked in
// tests/registers/line-citation-resolution.yaml. A grep of that baseline for the
// sections above returns zero. The two populations are related and distinct.
//
// WHAT THE PASS IS BLOCKING. The map holds 11 real entries, the anchors the
// section reductions retired, for example 1541-adapterbinary-protocol and
// internal-messagepart-format, both succeeded by spec/28#2853-intra-pod. The pass
// aborts before writing any of them, so proposal 0064 SPEC-4 cannot run.
//
// THE ASYMMETRY THAT PICKS THE FIX, as in the gloss defect: a pass that declines
// to claim a citation outside its job leaves that citation exactly as it was, and
// a separate gate can still report it. A pass that claims one it cannot resolve
// stops the migration dead. Narrow the domain.
//
// DO NOT WEAKEN THE FAIL-CLOSED GUARANTEE. The stated abort must survive: a
// citation naming an anchor the map RETIRES, with no entry, still aborts, and a
// map entry whose successor heading does not exist still aborts. Those are the
// two cases proposal 0064 assigns to this pass and TEST-1 pins.

export const meta = {
  name: "fix-anchor-domain",
  description:
    "Narrow the specshift anchor pass to the domain proposal 0064 states for it, so a citation naming a section that was never retired stops aborting the run",
  phases: [
    { title: "Diagnose", detail: "read the pass's domain rule and locate where it claims a citation" },
    { title: "Fix", detail: "narrow the domain and pin both surviving abort cases" },
    { title: "Verify", detail: "run both confinements and confirm the 11 real retirements are written" },
  ],
};

const REPO = "/home/ec2-user/lenny";

const CTX = `
Repository: ${REPO}. Work there.

THE DEFECT. \`go run ./scripts/specshift -pass anchor -register tests/spec-anchor-moves.json -only spec/\`
aborts with the tree unchanged, and so does the \`-except spec/\` form. Together
they refuse 1,615 citations across 337 files, each with the message "the citation
of §N names a section no specification file of the tree states a heading for, and
the anchor-move map carries no successor for it".

WHY THAT IS WRONG. Proposal 0064 states this pass's abort condition, in the
register table of its §3, as: the pass "aborts non-zero before any write on a
citation naming a retired anchor with no map entry and on a map entry whose
successor heading does not exist". A RETIRED anchor is one
tests/spec-anchor-moves.json retires. The refused citations name sections that are
not in the tree and were never retired by anything: §3.4, §3.2, §15.0, §12.3.7,
§6.39 name a numbering this specification does not use, and 23 of them are 45 CFR
HIPAA references such as §164.312 and §164.530 sitting in spec/11 and spec/12.

VERIFIED BEFORE THIS TASK WAS WRITTEN, so you do not need to re-establish it,
though you may confirm it:
  - spec/03_high-level-architecture.md carries only "## 3. High-Level Architecture"
    and no numbered subsection, so §3.2 and §3.4 name nothing.
  - grep for those sections in tests/registers/line-citation-resolution.yaml
    returns zero, so this is NOT the pre-existing stale LINE-citation population
    proposal 0064 measures at roughly 1,500 in its §4.6. That is a different class.
  - tests/spec-anchor-moves.json holds 11 entries, the anchors the §4.7 and §15.4
    reductions retired. The pass aborts before writing any of them.

THE FIX DIRECTION. Narrow the pass's domain so it claims a citation only when the
citation names an anchor the map retires. A citation naming a section that is
absent from the tree and that no map entry retires is outside this pass's job and
must not abort it.

DO NOT WEAKEN THE FAIL-CLOSED GUARANTEE. Both stated aborts must survive, and
TEST-1 pins them:
  1. a citation naming a RETIRED anchor for which the map carries no entry;
  2. a map entry whose successor heading does not exist.
If your change would retire either, stop and report that instead.

CONSTRAINTS.
  Do not edit any proposal under proposals/.
  Do not hand-edit anything a migration pass wrote.
  Do not correct the 1,615 citations. They are a pre-existing population and
  correcting them is not this task; the question is only whether this pass should
  be claiming them.
  Match the surrounding code, which documents why a rule exists at length.
`;

phase("Diagnose");

const diag = await agent(
  `${CTX}

Establish how the pass decides a citation is its business. Change nothing.

1. Read the anchor pass implementation under \`scripts/specshift/\`. Find where it
   selects the citations it will rewrite and where it produces the refusal message
   quoted above. Cite file:line.
2. State the predicate it currently applies, precisely, and contrast it with the
   predicate proposal 0064 states. Say exactly where the two diverge.
3. Determine whether the pass can tell "an anchor the map retires" from "a section
   absent from the tree". It has the map, and it can read the tree's headings, so
   say what information it already has and whether the narrower predicate is
   computable from it.
4. Find every test that pins the current behaviour, in
   \`scripts/specshift/run_test.go\` and elsewhere, and say for each whether it
   asserts one of the two abort cases proposal 0064 keeps, or asserts the broader
   behaviour that is the defect. Name the TEST-1 cases if they exist yet.
5. Report how many of the 1,615 refusals would remain refusals under the narrower
   predicate. Measure it.

Return a plain report with file:line citations.`,
  { label: "diagnose", phase: "Diagnose" },
);

phase("Fix");

const fix = await agent(
  `${CTX}

The diagnosis:

${diag}

Narrow the pass's domain accordingly.

Write the commentary in the voice of the surrounding code: state that a citation
naming a section the tree does not carry is not evidence of a retired anchor,
because nothing retired it, and that claiming it would stop a migration on a
population the pass was never given. Name both surviving abort cases in the same
comment so a later reader does not widen the predicate back.

Update the tests: a test asserting the broad behaviour is rewritten to the
narrowed rule with its intent preserved. ADD regression cases covering the three
real shapes:
  - a Lenny-style citation naming a section absent from the tree (§3.4), which
    must NOT abort and must be left untouched;
  - a regulatory citation (§164.312), which must NOT abort and must be left
    untouched;
  - a citation naming an anchor the map DOES retire but with no map entry, which
    MUST still abort.
Also keep a case for a map entry whose successor heading does not exist, which
must still abort.

Then run \`go test ./scripts/specshift/...\` and report. Note that two failures,
TestLoadTableReadsTheLandedNamingTable_spec_28_3 and
TestAMisSeededOccurrenceFailsTheSpecificationConfinedRuns..., are PRE-EXISTING and
reproduce at HEAD; do not try to fix them, just confirm they are the only ones
besides any you introduce.`,
  { label: "fix", phase: "Fix" },
);

phase("Verify");

const verify = await agent(
  `${CTX}

The pass is narrowed:

${fix}

Verify on the real tree, WITHOUT applying anything permanent.

1. Run both dry runs and report exit code and planned file count:
     go run ./scripts/specshift -pass anchor -register tests/spec-anchor-moves.json -only spec/
     go run ./scripts/specshift -pass anchor -register tests/spec-anchor-moves.json -except spec/
   Report every remaining refusal. Zero refusals is the goal, but a refusal that is
   one of the two legitimate abort cases is a correct result and must be reported
   as such rather than worked around.

2. Confirm the pass now plans the real work: the map's 11 retired anchors, among
   them 1541-adapterbinary-protocol, internal-messagepart-format and
   protocol-reference--message-schemas, all succeeded by
   spec/28_communication-channels.md#2853-intra-pod. Report which files it plans to
   rewrite and confirm each planned rewrite targets one of those 11.

3. THE CONTENT CHECK, which a sibling pass failed and which is why this step
   exists. On an isolated copy of the tree made with \`cp -a\` into the scratch
   directory, never on ${REPO}, apply the pass and inspect the diff. For every
   rewritten line, confirm the ONLY change is the anchor within the link target,
   and that no surrounding word, link text, or sentence was altered or deleted.
   Report the counts: lines changed, lines where only the anchor changed, lines
   where anything else changed. The last number must be ZERO. Show eight
   representative before-and-after pairs. Then DELETE the copy. Do not run
   \`git checkout\`, \`git stash\` or \`git reset\` against ${REPO}.

4. Confirm the 1,615 previously-refused citations are untouched by the pass in
   that trial: they should appear nowhere in the diff.

5. Report tier 0 from source, which is stale as a checked-in binary:
     go run ./cmd/lenny-test --tier static

Return a plain report. Lead with the two dry-run exit codes and the count of lines
where something other than the anchor changed.`,
  { label: "verify", phase: "Verify" },
);

return { diagnosis: diag, fix, verify };
