// Behavioural test for implement-proposal-build's reverifyDoneSteps input.
//
// It runs the REAL workflow script the way the runtime does — the body wrapped
// in an async function with the sandbox globals injected — and stubs `agent` so
// no subagent, no network, and no git write happens. Stubbing at that seam is
// what makes the test meaningful: every decision under test (skip, re-verify,
// repair, which prompt the repair sends) is made by the script itself, and the
// test only supplies the answers an agent would have returned.
//
// Run: node .claude/tests/implement-proposal-reverify.test.mjs
import { loadWorkflow, REPO } from "./harness.mjs";
import { readFileSync } from "fs";
import { resolve } from "path";

const SRC = loadWorkflow(".claude/workflows/implement-proposal-build.js");

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

// Section 5 covered the stuck-finding judges under the retired contract: a
// boolean `stuck` and a unanimous-high vote. That mechanism was replaced by
// three verdicts and a medium-or-better agreement, and the behaviour now has
// its own suite in scripts/test-stuck-judging.mjs, which also covers the case
// the old design could not detect. Keeping a second copy here would only let
// the two drift.

console.log("\n6. accepted divergences reach the FINAL review lenses and nothing else");
{
  const DIVS = ["REG-1 stages the revocation row WIRED; the tree has no caller so it is UNWIRED"];
  const runFinal = (accepted) => {
    const calls = [];
    const agent = async (prompt, opts = {}) => {
      const label = opts.label || "";
      calls.push({ label, prompt });
      if (label === "plan") return { blastRadius: [], steps: [{ id: "S1", title: "t", work: "w", targets: ["pkg/a"], tiers: ["unit"], specRefs: [], checklistStep: "S1", dependsOn: [] }], deviations: [] };
      if (label.startsWith("plan-critique")) return { complete: true, gaps: [] };
      if (label === "checklist-ticks") return { ticked: ["S1"] };
      if (label.startsWith("baseline") || label.endsWith(":base")) return { sha: "deadbeef" };
      if (label.startsWith("review:")) return { findings: [] };
      if (label.startsWith("verify") || label.startsWith("selfverify")) return { green: true, tiersRun: ["unit"] };
      if (label.startsWith("guard") || label.includes("compile")) return { clean: true, compiles: true };
      if (label === "proposal-edit-audit") return { edited: false, commits: [] };
      return {};
    };
    const logs = [];
    const fn = new Function("args","agent","parallel","pipeline","phase","log","workflow","budget",
      "return (async () => {\n" + SRC + "\n})();");
    return fn({ proposalPath: "p.md", repoRoot: "/repo", date: "d", acceptedDivergences: accepted },
      agent, async (t) => Promise.all(t.map((f) => f())), async (i) => i, () => {},
      (m) => logs.push(String(m)), async () => ({}), { total: null, spent: () => 0, remaining: () => Infinity })
      .then((r) => ({ result: r, calls, logs }));
  };
  {
    const { calls, logs } = await runFinal(DIVS);
    const finals = calls.filter((c) => /^review:(conformance|invariants|completeness):r/.test(c.label));
    check("the final lenses ran", finals.length >= 3, finals.length + " final reviewers");
    check("every final lens carries the divergence", finals.length > 0 && finals.every((c) => c.prompt.includes(DIVS[0])));
    check("they are told it is adjudicated", finals.every((c) => /ALREADY ADJUDICATED/.test(c.prompt)));
    check("and told the rest is still fully in scope", finals.every((c) => /no gentler with it/.test(c.prompt)));
    check("logged the seeding", logs.some((l) => /1 accepted divergence\(s\) seeded/.test(l)));
  }
  {
    const { calls, logs } = await runFinal(undefined);
    check("absent by default: no block anywhere", !calls.some((c) => /ALREADY ADJUDICATED/.test(c.prompt)));
    check("absent by default: nothing logged", !logs.some((l) => /accepted divergence/.test(l)));
  }
  {
    const { calls } = await runFinal("a single string divergence");
    const finals = calls.filter((c) => /^review:(conformance|invariants|completeness):r/.test(c.label));
    check("a string argument normalises", finals.length > 0 && finals.every((c) => c.prompt.includes("a single string divergence")));
  }
}

console.log("\n7. a caller-supplied plan replaces the planning phase");
{
  const SUPPLIED = { blastRadius: [], steps: [
    { id: "S20", title: "supplied", work: "w", targets: ["pkg/a"], tiers: ["unit"], specRefs: [], checklistStep: "S20", dependsOn: [] },
  ]};
  const runPlan = (plan) => {
    const calls = [];
    const agent = async (prompt, opts = {}) => {
      const label = opts.label || "";
      calls.push({ label, prompt });
      if (label === "plan") return { blastRadius: [], steps: [{ id: "DERIVED", title: "d", work: "w", targets: [], tiers: [], specRefs: [], checklistStep: "DERIVED", dependsOn: [] }] };
      if (label.startsWith("plan-critique")) return { complete: true, gaps: [] };
      if (label === "checklist-ticks") return { ticked: [] };
      if (label.startsWith("baseline") || label.endsWith(":base")) return { sha: "deadbeef" };
      if (label.startsWith("review:")) return { findings: [] };
      if (label.startsWith("build:")) return { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1" };
      if (label.startsWith("verify") || label.startsWith("selfverify")) return { green: true, tiersRun: ["unit"] };
      if (label.startsWith("guard") || label.includes("compile")) return { clean: true, compiles: true };
      if (label === "proposal-edit-audit") return { edited: false, commits: [] };
      return {};
    };
    const logs = [];
    const fn = new Function("args","agent","parallel","pipeline","phase","log","workflow","budget",
      "return (async () => {\n" + SRC + "\n})();");
    return fn({ proposalPath: "p.md", repoRoot: "/repo", date: "d", plan },
      agent, async (t) => Promise.all(t.map((f) => f())), async (i) => i, () => {},
      (m) => logs.push(String(m)), async () => ({}), { total: null, spent: () => 0, remaining: () => Infinity })
      .then((r) => ({ result: r, calls, logs }));
  };
  {
    const { calls, logs } = await runPlan(SUPPLIED);
    check("no planner agent ran", !calls.some((c) => c.label === "plan"));
    check("no plan-critique ran", !calls.some((c) => c.label.startsWith("plan-critique")));
    check("the supplied step is the one built", calls.some((c) => c.label.startsWith("build:S20")));
    check("the derived step is NOT built", !calls.some((c) => c.label.includes("DERIVED")));
    check("logged the skip", logs.some((l) => /supplied by the caller/.test(l)));
  }
  {
    const { calls } = await runPlan(undefined);
    check("absent: the planner still runs", calls.some((c) => c.label === "plan"));
    check("absent: the derived step is built", calls.some((c) => c.label.startsWith("build:DERIVED")));
  }
  {
    const { calls } = await runPlan({ steps: [] });
    check("an empty supplied plan falls back to planning", calls.some((c) => c.label === "plan"));
  }
}

console.log(failures === 0 ? "\nAll checks passed.\n" : "\n" + failures + " check(s) FAILED.\n");
process.exit(failures ? 1 : 0);
