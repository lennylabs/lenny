// Behavioural test for implement-proposal-build's reverifyDoneSteps input.
//
// It runs the REAL workflow script the way the runtime does — the body wrapped
// in an async function with the sandbox globals injected — and stubs `agent` so
// no subagent, no network, and no git write happens. Stubbing at that seam is
// what makes the test meaningful: every decision under test (skip, re-verify,
// repair, which prompt the repair sends) is made by the script itself, and the
// test only supplies the answers an agent would have returned.
//
// Run: node scripts/test-reverify-done-steps.mjs
import { readFileSync } from "fs";

const SRC = readFileSync(".claude/workflows/implement-proposal-build.js", "utf8").replace(
  /^export\s+const\s+meta/m,
  "const meta",
);

// Two planned steps: S1 is ticked in the checklist, S2 is not.
const PLAN = {
  blastRadius: [{ surface: "pkg/x", why: "t" }],
  steps: [
    { id: "S1", title: "one", work: "w1", targets: ["pkg/a"], tiers: ["unit"], specRefs: ["§1"], checklistStep: "S1", dependsOn: [] },
    { id: "S2", title: "two", work: "w2", targets: ["pkg/b"], tiers: ["unit"], specRefs: ["§2"], checklistStep: "S2", dependsOn: [] },
  ],
  deviations: [],
};

function run({ reverifyDoneSteps, reverifyFindings }) {
  const calls = [];
  let reviewRound = 0;

  const agent = async (prompt, opts = {}) => {
    const label = opts.label || "";
    calls.push({ label, prompt });

    if (label === "plan") return JSON.parse(JSON.stringify(PLAN));
    if (label.startsWith("plan-critique")) return { complete: true, gaps: [] };
    if (label === "checklist-ticks") return { ticked: ["S1"] };
    if (label.startsWith("baseline") || label.endsWith(":base")) return { sha: "deadbeef" };
    if (label.startsWith("review:")) {
      // The re-verify pass answers with the injected findings; every later
      // review (after a repair) comes back clean so the loop can converge.
      const isReverify = label.endsWith(":reverify");
      if (isReverify) return { findings: reverifyFindings };
      return { findings: [] };
    }
    if (label.startsWith("build:")) return { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1" };
    if (label.startsWith("verify") || label.startsWith("selfverify")) return { green: true, tiersRun: ["unit"] };
    if (label.startsWith("tick:")) return "DONE";
    if (label.startsWith("guard") || label.includes("compile")) return { clean: true, compiles: true };
    if (label === "proposal-edit-audit") return { edited: false, commits: [] };
    if (label.startsWith("drift")) return { drifted: false, reasons: [] };
    // Anything unhandled: a permissive default so the script can reach its end.
    return {};
  };

  const parallel = async (thunks) => Promise.all(thunks.map((t) => t()));
  const pipeline = async (items, ...stages) => {
    const out = [];
    for (const it of items) {
      let v = it;
      for (const st of stages) v = await st(v, it, 0);
      out.push(v);
    }
    return out;
  };

  const logs = [];
  const fn = new Function(
    "args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
    "return (async () => {\n" + SRC + "\n})();",
  );
  return fn(
    { proposalPath: "proposals/p.md", repoRoot: "/repo", date: "2026-01-01", reverifyDoneSteps },
    agent, parallel, pipeline, () => {}, (m) => logs.push(String(m)),
    async () => ({}), { total: null, spent: () => 0, remaining: () => Infinity },
  ).then((result) => ({ result, calls, logs }));
}

const FINDING = [{ title: "diverged", where: "pkg/a:1 vs §1", divergence: "d", fix: "change the code" }];
let failures = 0;
const check = (name, cond, detail) => {
  if (cond) console.log("  PASS  " + name);
  else { console.log("  FAIL  " + name + (detail ? "  :: " + detail : "")); failures++; }
};

console.log("\n1. flag OFF (the default): a ticked step is skipped, never reviewed");
{
  const { calls, logs } = await run({ reverifyDoneSteps: false, reverifyFindings: [] });
  const s1Reviews = calls.filter((c) => c.label.startsWith("review:S1"));
  check("no review agent ran for the ticked step", s1Reviews.length === 0, s1Reviews.length + " ran");
  check("logged as skipped", logs.some((l) => /Step S1: already present in the tree/.test(l)));
  check("the unticked step still ran", calls.some((c) => c.label.startsWith("build:S2")));
}

console.log("\n2. flag ON, re-verify clean: reviewed by both lenses, then skipped");
{
  const { calls, logs } = await run({ reverifyDoneSteps: true, reverifyFindings: [] });
  const rv = calls.filter((c) => c.label.startsWith("review:S1") && c.label.endsWith(":reverify"));
  check("both lenses ran on the ticked step", rv.length === 2, rv.length + " ran");
  check("no implement/fix agent ran for it", !calls.some((c) => c.label.startsWith("build:S1") && !c.label.endsWith(":base")));
  check("logged as re-verified clean", logs.some((l) => /Step S1: re-verified clean/.test(l)));

  // The defect this guards: a re-verify pointed at `git diff <base>..HEAD`
  // reads an EMPTY diff, because a ticked step's commits landed in an earlier
  // run and sit behind this run's base. The reviewer would report clean having
  // looked at nothing, so the whole feature would pass vacuously.
  const rvPrompt = calls.find((c) => c.label === "review:S1:conformance:reverify").prompt;
  // The prompt may MENTION git diff while explaining why there is none; what it
  // must not do is instruct the reviewer to read one.
  check("the re-verify is not told to read a diff",
    !/read ONLY this step's diff/.test(rvPrompt) && !/git diff \w+\.\.HEAD/.test(rvPrompt),
    (rvPrompt.match(/read ONLY this step's diff|git diff \w+\.\.HEAD/) || [""])[0]);
  check("the re-verify is told to read the current tree", /CURRENT STATE OF THE TREE/.test(rvPrompt));
  check("the re-verify names the step's targets", rvPrompt.includes("pkg/a"));
  check("the re-verify says the step came from an earlier run", /EARLIER run/.test(rvPrompt));

  // The in-loop path must be untouched: a step this run built IS reviewed by diff.
  const { calls: c2 } = await run({ reverifyDoneSteps: false, reverifyFindings: [] });
  const loopPrompt = c2.find((c) => /^review:S2:conformance:r/.test(c.label)).prompt;
  check("a freshly built step is still reviewed by diff", /git diff deadbeef\.\.HEAD/.test(loopPrompt));
}

console.log("\n3. flag ON, re-verify finds a divergence: repaired through the normal loop");
{
  const { calls, logs, result } = await run({ reverifyDoneSteps: true, reverifyFindings: FINDING });
  check("a fix agent ran for the ticked step", calls.some((c) => c.label.startsWith("build:S1") && !c.label.endsWith(":base")));
  const first = calls.find((c) => c.label.startsWith("build:S1") && !c.label.endsWith(":base"));
  check(
    "the repair uses the FIX prompt, not the fresh-implement prompt",
    first && !/^Implement one step of a build sequence/.test(first.prompt),
    first ? first.prompt.slice(0, 46) : "no build agent",
  );
  check("the repair prompt carries the re-verify findings", first && first.prompt.includes("diverged"));
  check(
    "the repair prompt says the step came from an earlier run",
    first && /implemented by an earlier run/.test(first.prompt),
  );
  check("it was re-reviewed after the fix", calls.some((c) => c.label.startsWith("review:S1") && !c.label.endsWith(":reverify")));
  check("the tiers were re-run after the fix", calls.some((c) => c.label.startsWith("verify") || c.label.startsWith("selfverify")));
  check("logged as repairing (one finding per lens)", logs.some((l) => /Step S1: re-verify found 2 finding\(s\); repairing/.test(l)));
  check(
    "reported in reverifyRepaired",
    result && Array.isArray(result.reverifyRepaired) && result.reverifyRepaired.some((r) => r.id === "S1"),
    JSON.stringify(result && result.reverifyRepaired),
  );
}

if (failures) { const { logs } = await run({ reverifyDoneSteps: true, reverifyFindings: FINDING }); console.log("\n  --- S1 log lines ---"); logs.filter((l) => /S1/.test(l)).forEach((l) => console.log("   " + l.slice(0, 130))); }
console.log("\n4. tier scoping: full on the first implementation, scoped on a fix, full again at the end");
{
  // S2 is unticked, so it is a fresh step: attempt 1 is an initial implementation.
  const { calls } = await run({ reverifyDoneSteps: false, reverifyFindings: [] });
  const v1 = calls.find((c) => c.label === "verify:S2:r1");
  check("attempt 1 runs the FULL tier set", v1 && /each higher tier this step must run/.test(v1.prompt));
  check("attempt 1 is not scoped", v1 && !/ONLY those of the step's higher tiers/.test(v1.prompt));
  check("a first-try-correct step needs no final gate", !calls.some((c) => c.label === "verify:S2:final"));
}
{
  // A ticked step with findings: its first attempt is a FIX, so it takes the
  // scoped path, and the final gate must then run before it is marked done.
  const { calls, logs } = await run({ reverifyDoneSteps: true, reverifyFindings: FINDING });
  const v1 = calls.find((c) => c.label === "verify:S1:r1");
  check("a fix attempt runs only the tiers the fix could affect",
    v1 && /ONLY those of the step's higher tiers/.test(v1.prompt));
  check("the fix attempt is told the skipped tiers get re-run",
    v1 && /not the final gate/.test(v1.prompt));
  check("the fixer itself is scoped too",
    calls.some((c) => c.label.startsWith("build:S1") && !c.label.endsWith(":base") &&
      /TESTS FOR THIS ATTEMPT ARE SCOPED TO THE FIX/.test(c.prompt)));
  const fg = calls.find((c) => c.label === "verify:S1:final");
  check("the final full gate runs before the step is marked done", !!fg);
  check("the final gate skips nothing", fg && /Skip none of them/.test(fg.prompt));
  check("logged the final gate", logs.some((l) => /full tier pass as the final gate/.test(l)));
}

console.log("\n5. stuck-finding introspection: unanimous high confidence suppresses, anything less does not");
{
  // A step whose reviews never go clean, so the loop reaches attempt 5.
  const STUCK = [{ title: "row lands UNWIRED where the proposal says WIRED", where: "x:1", divergence: "d", fix: "f" }];
  const runStuck = (verdicts) => {
    const calls = [];
    let judged = 0;
    const agent = async (prompt, opts = {}) => {
      const label = opts.label || "";
      calls.push({ label, prompt });
      if (label === "plan") return { blastRadius: [], steps: [{ id: "S1", title: "t", work: "w", targets: ["pkg/a"], tiers: ["unit"], specRefs: [], checklistStep: "S1", dependsOn: [] }], deviations: [] };
      if (label.startsWith("plan-critique")) return { complete: true, gaps: [] };
      if (label === "checklist-ticks") return { ticked: [] };
      if (label.startsWith("baseline") || label.endsWith(":base")) return { sha: "deadbeef" };
      if (label.startsWith("stuck:")) return verdicts[judged++ % verdicts.length];
      if (label.startsWith("review:")) return { findings: STUCK };
      if (label.startsWith("build:")) return { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1" };
      if (label.startsWith("verify") || label.startsWith("selfverify")) return { green: true, tiersRun: ["unit"] };
      if (label.startsWith("guard") || label.includes("compile")) return { clean: true, compiles: true };
      if (label === "proposal-edit-audit") return { edited: false, commits: [] };
      return {};
    };
    const parallel = async (t) => Promise.all(t.map((f) => f()));
    const logs = [];
    const fn = new Function("args","agent","parallel","pipeline","phase","log","workflow","budget",
      "return (async () => {\n" + SRC + "\n})();");
    return fn({ proposalPath: "p.md", repoRoot: "/repo", date: "d", maxStepAttempts: 6 },
      agent, parallel, async (i) => i, () => {}, (m) => logs.push(String(m)),
      async () => ({}), { total: null, spent: () => 0, remaining: () => Infinity })
      .then((result) => ({ result, calls, logs }));
  };

  const HIGH = { stuck: true, confidence: "high", findingTitle: "row lands UNWIRED where the proposal says WIRED", whyCodeIsRight: "no caller", whyProposalIsWrong: "REG-1 says WIRED", reasoning: "r" };
  const MED  = { stuck: true, confidence: "medium", findingTitle: "x", whyCodeIsRight: "", whyProposalIsWrong: "", reasoning: "r" };
  const NOT  = { stuck: false, confidence: "high", findingTitle: "", whyCodeIsRight: "", whyProposalIsWrong: "", reasoning: "r" };

  {
    const { calls, logs, result } = await runStuck([HIGH]);
    check("judges fire only at the introspect interval", calls.filter((c) => c.label.startsWith("stuck:")).length === 3,
      calls.filter((c) => c.label.startsWith("stuck:")).length + " judge calls");
    check("three lenses, not one", new Set(calls.filter((c) => c.label.startsWith("stuck:")).map((c) => c.label.split(":")[2])).size === 3);
    check("unanimous high confidence records it", (result.stuckFindings || []).length === 1, JSON.stringify(result.stuckFindings || []));
    check("logged the agreement", logs.some((l) => /THREE JUDGES AGREE/.test(l)));
    const later = calls.filter((c) => c.label.startsWith("review:") && /ALREADY JUDGED UNRESOLVABLE/.test(c.prompt));
    check("later review rounds are told to ignore it", later.length > 0, later.length + " reviews carried the note");
    check("a commit records it", calls.some((c) => c.label.startsWith("record-stuck:")));
  }
  {
    const { result, logs } = await runStuck([HIGH, HIGH, MED]);
    check("one MEDIUM blocks suppression", (result.stuckFindings || []).length === 0);
    check("logged the non-agreement", logs.some((l) => /did not reach unanimous high confidence/.test(l)));
  }
  {
    const { result } = await runStuck([HIGH, HIGH, NOT]);
    check("one dissent blocks suppression", (result.stuckFindings || []).length === 0);
  }
}

console.log(failures === 0 ? "\nAll checks passed.\n" : "\n" + failures + " check(s) FAILED.\n");
process.exit(failures ? 1 : 0);
