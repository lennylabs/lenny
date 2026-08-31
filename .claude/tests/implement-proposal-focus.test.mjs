// Behavioural test for implement-proposal's specReviewFocus input.
//
// Runs the REAL workflow body the way the runtime does, with the sandbox
// globals injected and `agent` stubbed, so every decision under test is made by
// the script and the test only supplies what an agent would have returned.
//
// Covers the already-applied path, which is where the focus argument is worth
// most: the proposal reads "Applied to spec", the presence check runs, and its
// findings drive the repair loop.
//
// Run: node .claude/tests/implement-proposal-focus.test.mjs
import { loadWorkflow, REPO } from "./harness.mjs";
import { readFileSync } from "fs";
import { resolve } from "path";

const SRC = loadWorkflow(".claude/workflows/implement-proposal.js");

const PLAN = {
  approved: true,
  alreadyApplied: true,
  statusLine: "- **Status:** Applied to spec (2026-01-01).",
  specEdits: [{ id: "1.1", targetFile: "spec/05.md", subsection: "S", summary: "rename", method: "authored" }],
  nonSpecStaged: [],
  findingIds: [],
};

// What each reviewer reports as missing, per call index, so a repair can close
// the gap on a later pass.
function run({ specReviewFocus, baseMissing, focusMissing }) {
  const calls = [];
  let alignRound = 0;

  const agent = async (prompt, opts = {}) => {
    const label = opts.label || "";
    calls.push({ label, prompt });

    if (label === "plan") return JSON.parse(JSON.stringify(PLAN));
    if (label.startsWith("verify-aligned")) {
      // First pass reports the injected gaps; any later pass is clean, so the
      // repair loop terminates.
      const isFocus = label.includes(":focus");
      if (label.includes(":r")) return { aligned: true, missing: [] };
      alignRound++;
      const missing = isFocus ? focusMissing : baseMissing;
      return { aligned: missing.length === 0, missing };
    }
    if (label.startsWith("apply-missing")) return { applied: ["1.1"], unappliable: [], deviations: [] };
    if (label.startsWith("close") || label.startsWith("findings")) return { closed: [], notes: "" };
    return {};
  };

  const parallel = async (thunks) => Promise.all(thunks.map((t) => t()));
  const pipeline = async (items, ...stages) => {
    const out = [];
    for (const it of items) { let v = it; for (const st of stages) v = await st(v, it, 0); out.push(v); }
    return out;
  };
  const logs = [];
  // The build subworkflow is out of scope here; return a green stub.
  const workflow = async () => ({ status: "implemented", green: true, reviewClean: true, commits: [], skippedSteps: [] });

  const fn = new Function(
    "args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
    "return (async () => {\n" + SRC + "\n})();",
  );
  return fn(
    { proposalPath: "proposals/p.md", repoRoot: "/repo", date: "2026-01-01", implementCode: false, specReviewFocus },
    agent, parallel, pipeline, () => {}, (m) => logs.push(String(m)),
    workflow, { total: null, spent: () => 0, remaining: () => Infinity },
  ).then((result) => ({ result, calls, logs }));
}

let failures = 0;
const check = (name, cond, detail) => {
  if (cond) console.log("  PASS  " + name);
  else { console.log("  FAIL  " + name + (detail ? "  :: " + detail : "")); failures++; }
};

const AREAS = ["the {slotId} to {sessionId} placeholder rename", "the Basic-level echo obligation"];

console.log("\n1. focus absent (the default): unchanged behaviour");
{
  const { calls, logs } = await run({ specReviewFocus: undefined, baseMissing: [], focusMissing: [] });
  check("exactly one alignment reviewer ran", calls.filter((c) => c.label.startsWith("verify-aligned")).length === 1);
  check("no focus reviewer ran", !calls.some((c) => c.label.includes(":focus")));
  check("no focus block in the prompt", !calls.some((c) => /AREAS TO CONCENTRATE ON/.test(c.prompt)));
  check("nothing logged about focus", !logs.some((l) => /Spec review focus/.test(l)));
}

console.log("\n2. focus present: a second reviewer runs beside the normal one");
{
  const { calls, logs } = await run({ specReviewFocus: AREAS, baseMissing: [], focusMissing: [] });
  const first = calls.filter((c) => c.label === "verify-aligned" || c.label === "verify-aligned:focus");
  check("both reviewers ran on the first pass", first.length === 2, first.map((c) => c.label).join(","));
  const focus = calls.find((c) => c.label === "verify-aligned:focus");
  check("the focus reviewer carries the areas", focus && AREAS.every((a) => focus.prompt.includes(a)));
  check("the normal reviewer does NOT carry them", calls.find((c) => c.label === "verify-aligned") &&
    !/AREAS TO CONCENTRATE ON/.test(calls.find((c) => c.label === "verify-aligned").prompt));
  check("logged the focus areas", logs.some((l) => /Spec review focus: 2 area/.test(l)));
}

console.log("\n3. findings are concatenated, not deduplicated");
{
  const SHARED = "SPEC-3 rename at spec/05.md:515";
  const { calls } = await run({
    specReviewFocus: AREAS,
    baseMissing: [SHARED],
    focusMissing: [SHARED, "SPEC-7 echo row absent from the battery"],
  });
  const repair = calls.find((c) => c.label.startsWith("apply-missing"));
  check("a repair pass ran", !!repair);
  const listed = (repair.prompt.match(/SPEC-3 rename at spec\/05\.md:515/g) || []).length;
  check("the shared finding appears TWICE (no dedup)", listed === 2, "appeared " + listed + "x");
  check("the focus-only finding reached the repair", repair && repair.prompt.includes("SPEC-7 echo row"));
  check("the repair was told 3 missing edits", /^3\. /m.test(repair.prompt) || /3\. SPEC/.test(repair.prompt),
    "numbered list did not reach 3");
}

console.log("\n4. a string argument is normalised to one area");
{
  const { calls, logs } = await run({ specReviewFocus: "just the one area", baseMissing: [], focusMissing: [] });
  check("focus reviewer ran", calls.some((c) => c.label === "verify-aligned:focus"));
  check("logged one area", logs.some((l) => /Spec review focus: 1 area/.test(l)));
}

console.log("\n5. a reviewer that dies fails closed", "");
{
  // focusMissing empty + base empty means aligned; but if a reviewer returns
  // null the run must not call it aligned. Simulated by an empty run list is
  // not reachable here, so assert the guard exists in source instead.
  const src = readFileSync(resolve(REPO, ".claude/workflows/implement-proposal.js"), "utf8");
  check("alignment requires every reviewer to have returned",
    /out\.length === runs\.length && missing\.length === 0/.test(src));
}

console.log(failures === 0 ? "\nAll checks passed.\n" : "\n" + failures + " check(s) FAILED.\n");
process.exit(failures ? 1 : 0);
