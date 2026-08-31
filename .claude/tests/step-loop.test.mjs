// Layer 2: the per-step build loop, after the conformance-before-tests
// restructure, plus the deterministic diff classifier it depends on.
//
// Run: node .claude/tests/step-loop.test.mjs

import { runWorkflow, suite, matching, never, firstIndex, ordered, labels } from "./harness.mjs";
import { classify } from "../tools/classify-diff.mjs";

const t = suite("step loop");
const WF = ".claude/workflows/implement-proposal-build.js";
const STEP = {
  id: "S1", title: "the counter", work: "emit it", targets: ["pkg/adapter"],
  tiers: ["unit", "component"], specRefs: ["16.1"], checklistStep: "S1", dependsOn: [],
};
const ARGS = (over = {}) => ({
  proposalPath: "proposals/0081_fix_x", repoRoot: "/repo", date: "2026-08-31",
  plan: { blastRadius: [], steps: [STEP] }, maxStepAttempts: 8, ...over,
});
const FINDING = [{ title: "F1", where: "pkg/a.go:1", divergence: "d", fix: "f" }];

const base = (over = {}) => ({
  "checklist-ticks": { ticked: [] },
  "baseline": { sha: "base0" },
  "build:S1:base": { sha: "base0" },
  "compile:*": { compiles: true, errors: [], leaseHeld: false },
  "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/a.go"], testsAddedOrModified: [] },
  "review:*": { findings: [] },
  "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
  "tick:*": "DONE",
  "compile-guard:*": { clean: true, compiles: true },
  "proposal-edit-audit": { edited: false, commits: [] },
  default: {},
  ...over,
});

t.section("C3. conformance runs before any test, and a finding stops the tests");
{
  let n = 0;
  const { calls } = await runWorkflow(WF, ARGS(), base({
    "review:*": () => (++n <= 2 ? { findings: FINDING } : { findings: [] }),
  }));
  const firstReview = firstIndex(calls, "review:S1");
  const firstVerify = firstIndex(calls, "verify:S1");
  t.check("the review runs before the first verify", firstReview >= 0 && firstReview < firstVerify, "review@" + firstReview + " verify@" + firstVerify);
  t.check("a compile gate runs before the review", firstIndex(calls, "compile:S1") < firstReview);
  const r1Verify = calls.filter((c) => c.label === "verify:S1:r1");
  t.check("no tests run on the round the review found something", r1Verify.length === 0, String(r1Verify.length));
}
{
  // With findings on every round the tests never run at all.
  const { result, calls, logs } = await runWorkflow(WF, ARGS({ maxStepAttempts: 3 }), base({
    "review:*": { findings: FINDING },
  }));
  t.check("no verify agent ever runs", never(calls, "verify:S1:r"), labels(calls).filter((l) => /verify/.test(l)).join(","));
  t.check("the step aborts on conformance, not on tests", result.status === "step-stuck");
  t.check("and says the tests never ran", logs.some((l) => /the tests never ran: the step never got past conformance/.test(l)));
}

t.section("C4. a test failure returns through conformance before the tests re-run");
{
  let v = 0;
  const { calls } = await runWorkflow(WF, ARGS(), base({
    "verify:*": ({ label }) => (label.endsWith(":final") ? { green: true, tiersRun: ["unit"], failures: [] } : (++v === 1 ? { green: false, tiersRun: ["unit"], failures: ["TestX failed"] } : { green: true, tiersRun: ["unit"], failures: [] })),
  }));
  const seq = labels(calls).filter((l) => /^(review|verify):S1/.test(l));
  t.check("the sequence is review, verify, review, verify", /^review.*verify.*review.*verify/s.test(seq.join(" ")), seq.join(" "));
  t.check("a failing test round is followed by a conformance re-check", seq.filter((l) => l.startsWith("review")).length >= 2);
}

t.section("C6. the final gate runs over the finished tree, and its failures route back");
// The gate exists because a step that took several rounds reached its final
// state through SCOPED runs, so no single pass has covered the tree as it now
// stands. A step whose first attempt was clean already ran the full set, and
// correctly gets no second one.
{
  const { calls } = await runWorkflow(WF, ARGS(), base());
  t.check("a first-try clean step needs no extra gate", never(calls, "verify:S1:final"));
}
{
  // One conformance finding forces attempt 2, whose verify is scoped, so the
  // gate is owed.
  let n = 0;
  let f = 0;
  const { calls, logs } = await runWorkflow(WF, ARGS(), base({
    "review:*": () => (++n <= 1 ? { findings: FINDING } : { findings: [] }),
    "verify:*": ({ label }) => {
      if (label.endsWith(":final")) {
        return ++f === 1
          ? { green: false, tiersRun: ["unit", "component"], failures: ["component tier broke"] }
          : { green: true, tiersRun: ["unit", "component"], failures: [] };
      }
      return { green: true, tiersRun: ["unit"], failures: [] };
    },
  }));
  t.check("a scoped step owes a final gate", matching(calls, "verify:S1:final").length >= 1, String(matching(calls, "verify:S1:final").length));
  t.check("a failing gate is followed by another attempt and another gate", matching(calls, "verify:S1:final").length === 2, String(matching(calls, "verify:S1:final").length));
  t.check("its failures reach the next fixer", calls.some((c) => /^build:S1/.test(c.label) && /component tier broke/.test(c.prompt)));
  t.check("it is named a scoping miss", logs.some((l) => /Every one of these is a scoping miss and is recorded/.test(l)));
  t.check("and the step ticks only after a passing gate", ordered(calls, "verify:S1:final", "tick:"));
}
{
  let n = 0;
  const { result } = await runWorkflow(WF, ARGS({ maxStepAttempts: 4 }), base({
    "review:*": () => (++n <= 1 ? { findings: FINDING } : { findings: [] }),
    "verify:*": ({ label }) =>
      label.endsWith(":final")
        ? { green: false, tiersRun: ["unit"], failures: ["still broken"] }
        : { green: true, tiersRun: ["unit"], failures: [] },
  }));
  t.check("a gate that never passes records its misses", (result.gateMisses || []).length > 0, String((result.gateMisses || []).length));
  t.check("each miss names the failures", (result.gateMisses || [])[0] && (result.gateMisses[0].failures || []).join().includes("still broken"));
  t.check("each miss names its step and attempt", (result.gateMisses || [])[0] && result.gateMisses[0].step === "S1" && typeof result.gateMisses[0].attempt === "number");
  t.check("and the step never ticks", result.status === "step-stuck", result.status);
}

t.section("C7. the scoping block is grounded in a real diff and a cost ledger");
{
  let n = 0;
  const { calls } = await runWorkflow(WF, ARGS(), base({
    "review:*": () => (++n <= 1 ? { findings: FINDING } : { findings: [] }),
  }));
  const scoped = calls.filter((c) => /^verify:S1:r/.test(c.label) && /ONLY the tiers this fix warrants/.test(c.prompt));
  t.check("a fix round is scoped", scoped.length > 0, String(scoped.length));
  const p = scoped[0].prompt;
  t.check("it runs the classifier over a real range", /classify-diff\.mjs HEAD~1\.\.HEAD/.test(p));
  t.check("and reads the diff itself", /read `git diff HEAD~1\.\.HEAD` YOURSELF/.test(p));
  t.check("the fixer's report is adversarial, not the subject", /the diff wins/.test(p));
  t.check("a disagreement is reportable on its own", /a fixer that changed more than it said is worth\s+knowing about/.test(p));
  t.check("the script is authoritative where it fires", /AUTHORITATIVE where it\s+fires/.test(p));
  t.check("the class table is carried", /comment-only \| 0/.test(p) && /security \(auth, isolation, egress, credentials\) \| \+ 9/.test(p));
  t.check("going below the table is limited to three classes", /only for comment-only, doc-only, and rename-local/.test(p));
  t.check("an expensive tier needs a named hunk", /No named hunk, no run/.test(p));
  t.check("the ledger is read before deciding", /scratchpad\/test-times/.test(p));
  t.check("and written after each tier, by this same agent", /Do not spawn a separate step for that/.test(p));
  t.check("skips are recorded with reasons", /every tier you skipped, and the\s+reason for each/.test(p));
}

t.section("C5. the detectors wake the judges and do not stop the step");
{
  const { calls, logs } = await runWorkflow(WF, ARGS({ maxStepAttempts: 12, introspectEvery: 3 }), base({
    "review:*": { findings: FINDING },
    "stuck:*": { verdict: "resolvable", confidence: "high", reasoning: "converging" },
  }));
  t.check("the judges are woken", !never(calls, "stuck:"));
  t.check("a resolvable verdict does not stop the step", logs.some((l) => /the loop continues/.test(l)));
  t.check("the step runs to its attempt cap, not to a detector", logs.some((l) => /after 12 attempt\(s\)/.test(l)));
}
{
  const { result, calls } = await runWorkflow(WF, ARGS({ maxStepAttempts: 12, introspectEvery: 3 }), base({
    "review:*": { findings: FINDING },
    "stuck:*": { verdict: "unresolvable", confidence: "high", findingTitle: "F1", whyCodeIsRight: "it is", whyProposalIsWrong: "it is not", roundsMovedIt: "none", reasoning: "r" },
  }));
  t.check("an unresolvable finding is set aside, not a stop", (result.stuckFindings || []).some((f) => f.kind === "unresolvable"));
  t.check("and the next fixer is told to leave it alone", calls.some((c) => /^build:S1/.test(c.label) && /ALREADY JUDGED UNRESOLVABLE/.test(c.prompt)));
}

t.section("C2b. a code step refuses to run while a spec lease is held");
{
  const { result, calls } = await runWorkflow(WF, ARGS(), base({
    "compile:*": { compiles: true, errors: [], leaseHeld: true },
  }));
  t.check("the run stops", result.status === "lease-leaked", result.status);
  t.check("it names the step", (result.reason || "").includes("S1"));
  t.check("and says spec/ is writable", /spec\/ is writable/.test(result.reason || ""));
  t.check("no review or test runs under the leak", never(calls, "review:S1") && never(calls, "verify:S1"));
}

t.section("C0. a step that does not compile is not reviewed");
{
  let c = 0;
  const { calls } = await runWorkflow(WF, ARGS(), base({
    "compile:*": () => (++c === 1 ? { compiles: false, errors: ["pkg/a.go:3: undefined x"], leaseHeld: false } : { compiles: true, errors: [], leaseHeld: false }),
  }));
  t.check("no reviewer sees the broken tree", firstIndex(calls, "review:S1") > firstIndex(calls, "compile:S1:r2"));
  t.check("the build error reaches the next fixer", calls.some((x) => /^build:S1/.test(x.label) && /undefined x/.test(x.prompt)));
}

// ---- T7-T9: the classifier, executed --------------------------------------

const mk = (f, adds, dels = []) =>
  "diff --git a/" + f + " b/" + f + "\n--- a/" + f + "\n+++ b/" + f + "\n" +
  adds.map((l) => "+" + l).join("\n") + (dels.length ? "\n" + dels.map((l) => "-" + l).join("\n") : "");

t.section("T7. comment-only, including block comments and code-like comment text");
t.check("line comments", classify(mk("pkg/a.go", ["// a note", "", "// another"])).classes.includes("comment-only"));
t.check("block comments", classify(mk("pkg/a.go", ["/* multi", "   line */"])).classes.includes("comment-only"));
t.check("a comment containing code text is still a comment", classify(mk("pkg/a.go", ["// x := 1 is what we used to do"])).classes.includes("comment-only"));
t.check("a removed comment counts too", classify(mk("pkg/a.go", [], ["// gone"])).classes.includes("comment-only"));

t.section("T8. one logic line alongside a comment edit is NOT comment-only");
t.check("the case this exists for", !classify(mk("pkg/a.go", ["// a note", "x := 1"])).classes.includes("comment-only"));
t.check("trailing code after a block comment close", !classify(mk("pkg/a.go", ["/* c */ x := 1"])).classes.includes("comment-only"));
t.check("a bare statement", !classify(mk("pkg/a.go", ["return nil"])).classes.includes("comment-only"));
t.check("and it says so", /none of comment-only/.test(classify(mk("pkg/a.go", ["return nil"])).reason));

t.section("T9. doc-only and test-only are decided by path; a mixed diff falls through");
t.check("prose", classify(mk("docs/a.md", ["p"])).classes.includes("doc-only"));
t.check("an operator page is separated", classify(mk("docs/runbooks/a.md", ["p"])).classes.includes("doc-only-operator"));
t.check("a Go test", classify(mk("tests/tier1/a_test.go", ["x := 1"])).classes.includes("test-only"));
t.check("a mixed code+doc diff claims no cheap class", classify(mk("pkg/a.go", ["// c"]) + "\n" + mk("docs/b.md", ["p"])).classes.length === 0);
t.check("an empty diff is its own answer", classify("").classes.includes("empty"));

t.done();
