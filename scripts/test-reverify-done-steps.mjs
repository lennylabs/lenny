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
console.log(failures === 0 ? "\nAll checks passed.\n" : "\n" + failures + " check(s) FAILED.\n");
process.exit(failures ? 1 : 0);
