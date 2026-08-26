// Behavioural test for the build subworkflow's stuck-judging.
//
// Runs the REAL workflow body with `agent` stubbed, so every decision under
// test is made by the script. Covers the case that motivated the redesign: a
// finding the loop never closes, where the reviewer is RIGHT and the code is
// genuinely incomplete. The old mechanism could not detect it, because its
// only non-resolvable verdict was "the proposal is wrong".
//
// Run: node scripts/test-stuck-judging.mjs
import { readFileSync } from "fs";

const SRC = readFileSync(".claude/workflows/implement-proposal-build.js", "utf8").replace(
  /^export\s+const\s+meta/m,
  "const meta",
);

const STEP = {
  id: "S21", title: "docs", work: "w", targets: ["TEST-GAPS.md"],
  tiers: ["static"], specRefs: [], checklistStep: "S21", dependsOn: [],
};

// verdicts: what each of the three lenses returns when polled.
// cleanAfterCall: the review call index after which findings stop, so a
// scenario can let judging fire at attempt 2 and still let the step finish.
// Two review lenses run per attempt, so 4 means "clean from attempt 3".
function run({ verdicts, cleanAfterCall = 0, minUnproductiveRounds, maxStepAttempts = 7 }) {
  const calls = [];
  const logs = [];
  let reviewRound = 0;
  const agent = async (prompt, opts = {}) => {
    const label = opts.label || "";
    calls.push({ label, prompt });
    if (label === "checklist-ticks") return { ticked: [] };
    if (label.startsWith("baseline") || label.endsWith(":base")) return { sha: "base0000" };
    if (label.startsWith("stuck:")) {
      const lens = label.split(":")[2];
      return verdicts[lens];
    }
    if (label.startsWith("review:")) {
      reviewRound++;
      if (cleanAfterCall && reviewRound > cleanAfterCall) return { findings: [] };
      return { findings: [{ title: "T-4.4.21 is left OPEN where the proposal closes it", detail: "d" }] };
    }
    if (label.startsWith("build:")) {
      return { implemented: true, testsPassed: true, tiersRun: ["static"],
               commit: "c" + calls.length, filesChanged: ["PROPOSAL-QUEUE.md"], testsAddedOrModified: [] };
    }
    if (label.startsWith("verify") || label.startsWith("selfverify")) return { green: true, tiersRun: ["static"] };
    if (label.startsWith("guard") || label.includes("compile")) return { clean: true, compiles: true };
    if (label === "proposal-edit-audit") return { edited: false, commits: [] };
    return {};
  };
  const fn = new Function("args","agent","parallel","pipeline","phase","log","workflow","budget",
    "return (async () => {\n" + SRC + "\n})();");
  return fn({ proposalPath: "p.md", repoRoot: "/repo", date: "d",
              plan: { blastRadius: [], steps: [STEP] }, introspectEvery: 2, maxStepAttempts,
              minUnproductiveRounds },
    agent, async (t) => Promise.all(t.map((f) => f())), async (i) => i, () => {},
    (m) => logs.push(String(m)), async () => ({}),
    { total: null, spent: () => 0, remaining: () => Infinity })
    .then((result) => ({ result, calls, logs }));
}

let failures = 0;
const check = (n, c, d) => { if (c) console.log("  PASS  " + n);
  else { console.log("  FAIL  " + n + (d ? "  :: " + d : "")); failures++; } };

const V = (verdict, confidence) => ({ verdict, confidence, findingTitle: "T-4.4.21 is left OPEN where the proposal closes it",
  roundsMovedIt: "no round touched TEST-GAPS.md", outstandingWork: "write the tier-4 case", reasoning: "r" });

console.log("\n1. the case that motivated this: loop not closing a real finding");
{
  const { result, calls, logs } = await run({ verdicts: {
    motion: V("unproductive", "high"), "remaining-work": V("unproductive", "medium"), forecast: V("unproductive", "medium") } });
  check("the step did NOT tick", result.status === "step-stuck", "status=" + result.status);
  const rec = (result.stuckFindings || [])[0];
  check("recorded as unproductive", rec && rec.kind === "unproductive", JSON.stringify(rec && rec.kind));
  check("carries the outstanding work", rec && /tier-4 case/.test(rec.outstandingWork || ""));
  check("logged that it is NOT set aside", logs.some((l) => /NOT set aside/.test(l)));
  check("the finding was never suppressed to the fixer",
    !calls.some((c) => c.label.startsWith("build:") && /ALREADY JUDGED UNRESOLVABLE/.test(c.prompt)));
}

console.log("\n2. medium confidence is now enough (it was not before)");
{
  const { result } = await run({ verdicts: {
    motion: V("unproductive", "medium"), "remaining-work": V("unproductive", "medium"), forecast: V("unproductive", "medium") } });
  check("three mediums reach a verdict", result.status === "step-stuck");
}

console.log("\n3. a proposal defect is still set aside, and the loop continues");
{
  const { result, calls, logs } = await run({ cleanAfterCall: 4, verdicts: {
    motion: V("unresolvable", "high"), "remaining-work": V("unresolvable", "medium"), forecast: V("unresolvable", "high") } });
  const rec = (result.stuckFindings || [])[0];
  check("recorded as unresolvable", rec && rec.kind === "unresolvable");
  check("the fixer was told to leave it alone",
    calls.some((c) => c.label.startsWith("build:") && /ALREADY JUDGED UNRESOLVABLE/.test(c.prompt)));
  check("the step still finished", result.status !== "step-stuck", "status=" + result.status);
}

console.log("\n4. disagreement keeps the loop running");
{
  const { result, logs } = await run({ cleanAfterCall: 4, verdicts: {
    motion: V("unproductive", "high"), "remaining-work": V("resolvable", "high"), forecast: V("unproductive", "high") } });
  check("no verdict reached", logs.some((l) => /no agreed verdict/.test(l)));
  check("nothing recorded", (result.stuckFindings || []).length === 0);
}

console.log("\n5. low confidence does not count");
{
  const { result, logs } = await run({ cleanAfterCall: 4, verdicts: {
    motion: V("unproductive", "low"), "remaining-work": V("unproductive", "low"), forecast: V("unproductive", "low") } });
  check("no verdict reached on three lows", logs.some((l) => /no agreed verdict/.test(l)));
  check("nothing recorded", (result.stuckFindings || []).length === 0);
}

console.log("\n6. the judges are actually shown the round log");
{
  const { calls } = await run({ verdicts: {
    motion: V("resolvable", "high"), "remaining-work": V("resolvable", "high"), forecast: V("resolvable", "high") } });
  const j = calls.find((c) => c.label.startsWith("stuck:"));
  check("a judge ran", !!j);
  check("the prompt carries THE ROUND LOG", j && /THE ROUND LOG:/.test(j.prompt));
  check("the log names a real round with its commit", j && /ROUND 1/.test(j.prompt) && /commit: c/.test(j.prompt));
  check("the log carries the round's finding", j && /T-4\.4\.21 is left OPEN/.test(j.prompt));
  check("the judge is told to read the commits first", j && /git show --stat/.test(j.prompt));
}

console.log("\n7. the unproductive floor: a pattern, not a bad round or two");
{
  // introspectEvery 2 fires the judges at attempt 2, when only 2 consecutive
  // rounds have tried and failed. The floor is 5, so the verdict is refused
  // even though all three judges returned it at high confidence. Capped at 3
  // attempts so the run ends before the rounds legitimately reach the floor.
  const { result, logs } = await run({ maxStepAttempts: 3, verdicts: {
    motion: V("unproductive", "high"), "remaining-work": V("unproductive", "high"), forecast: V("unproductive", "high") } });
  check("logged why it was refused", logs.some((l) => /only 2 consecutive round\(s\)/.test(l)));
  check("no unproductive record was made",
    !(result.stuckFindings || []).some((f) => f.kind === "unproductive"),
    JSON.stringify((result.stuckFindings || []).map((f) => f.kind)));
  check("it aborted on the ordinary cap, not on the judges",
    logs.some((l) => /design-conformance divergences outstanding/.test(l)),
    logs.filter((l) => /stuck \(/.test(l)).join(" | "));
}

console.log("\n8. the floor is met once the rounds add up");
{
  const { result, calls } = await run({ minUnproductiveRounds: 2, verdicts: {
    motion: V("unproductive", "high"), "remaining-work": V("unproductive", "medium"), forecast: V("unproductive", "medium") } });
  check("the step stops", result.status === "step-stuck");
  const rec = (result.stuckFindings || [])[0];
  check("records how many rounds failed", rec && rec.consecutiveFailedRounds === 2,
    "got " + (rec && rec.consecutiveFailedRounds));
  const j = calls.find((c) => c.label.startsWith("stuck:"));
  check("the judges were told the count and the floor", j && /2 consecutive round\(s\)/.test(j.prompt) &&
    /requires at least 2 such rounds/.test(j.prompt));
}

console.log("\n9. below the floor the judges are told not to return it");
{
  const { calls } = await run({ verdicts: {
    motion: V("resolvable", "high"), "remaining-work": V("resolvable", "high"), forecast: V("resolvable", "high") } });
  const j = calls.find((c) => c.label.startsWith("stuck:"));
  check("the brief forbids it at 2 of 5", j && /you may NOT return it/.test(j.prompt));
  check("and says the floor is not for unresolvable", j && /does not apply to .unresolvable./.test(j.prompt));
}

console.log(failures === 0 ? "\nAll checks passed.\n" : "\n" + failures + " check(s) FAILED.\n");
process.exit(failures ? 1 : 0);
