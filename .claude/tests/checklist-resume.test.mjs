// Behavioural test for the startup resume reconciliation in
// implement-proposal-build: which file the run reads the ticked checklist boxes
// from, and therefore whether a resumed run skips the steps an earlier run
// already landed.
//
// The stub for `checklist-ticks` does not assert the prompt text. It parses the
// path out of the grep command the prompt names and then answers the way a
// POSIX grep against the real filesystem would: a directory yields nothing
// (`grep` prints "Is a directory" and exits 2), and a file yields the ids on its
// `[x]` lines. A prompt that names the proposal directory therefore fails here
// for the same reason it fails in production, and the sibling review-log file in
// the fixture catches a read that recurses into the directory instead.
//
// Run: node .claude/tests/checklist-resume.test.mjs

import { mkdtempSync, mkdirSync, writeFileSync, statSync, readFileSync } from "fs";
import { tmpdir } from "os";
import { resolve } from "path";
import { runWorkflow, suite, never, matching } from "./harness.mjs";

const t = suite("checklist resume");
const WF = ".claude/workflows/implement-proposal-build.js";
const STEP = {
  id: "S1", title: "the counter", work: "emit it", targets: ["pkg/adapter"],
  tiers: ["unit"], specRefs: ["16.1"], checklistStep: "S1", dependsOn: [],
};
const TICKED_LINE = "- [x] **S1 · code** — done\n";

// A folder proposal with a ticked checklist and a review log that quotes a
// checklist line of its own, so a recursive read of the directory returns two
// answers where the checklist alone returns one.
function fixture() {
  const root = mkdtempSync(resolve(tmpdir(), "checklist-resume-"));
  const dir = root + "/proposals/0081_fix_x";
  mkdirSync(dir, { recursive: true });
  writeFileSync(dir + "/0081_fix_x.implementation-checklist.md", TICKED_LINE);
  writeFileSync(dir + "/0081_fix_x.review-log.md", "- [ ] **S1 · code** — a quoted line\n");
  writeFileSync(root + "/proposals/0081_fix_x.md", TICKED_LINE);
  return root;
}

// What `grep -nE '^- \[[ x]\] \*\*S' <path>` reports, without the recursion a
// host's grep shim may add.
function grepTicks(path) {
  let st;
  try {
    st = statSync(path);
  } catch {
    return [];
  }
  if (st.isDirectory()) return [];
  return readFileSync(path, "utf8")
    .split("\n")
    .map((line) => /^- \[x\] \*\*(\S+)/.exec(line))
    .filter(Boolean)
    .map((m) => m[1]);
}

const base = (over = {}) => ({
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

// The `checklist-ticks` answer comes from the path the prompt itself names.
const ticksFromPrompt = ({ prompt }) => {
  const m = /grep -nE '[^']+' ([^\s`]+)/.exec(prompt);
  return { ticked: m ? grepTicks(m[1]) : [] };
};

async function resume(proposalPath, repoRoot) {
  return runWorkflow(
    WF,
    { proposalPath, repoRoot, date: "2026-08-31", plan: { blastRadius: [], steps: [STEP] }, maxStepAttempts: 8 },
    // A glob key rather than the exact label: the harness only calls a stub
    // value when it matched by glob or prefix.
    base({ "checklist-tick*": ticksFromPrompt }),
  );
}

t.section("a folder proposal's ticked step is read from the checklist file");
{
  const repo = fixture();
  const { result, calls, logs } = await resume("proposals/0081_fix_x", repo);
  t.check("the ticked step is not rebuilt", never(calls, "build:S1"), matching(calls, "build:S1").map((c) => c.label).join(","));
  t.check("it takes the already-done branch", logs.some((l) => /Step S1: already present in the tree; nothing to build/.test(l)), logs.join(" | "));
  t.check("and is recorded as skipped", (result.skippedSteps || []).some((s) => s.id === "S1"), JSON.stringify(result.skippedSteps));
  t.check(
    "exactly one id came back, so the read did not recurse over the directory",
    logs.some((l) => /Checklist: 1 step\(s\) already ticked/.test(l)),
    logs.filter((l) => /^Checklist:/.test(l)).join(" | "),
  );
}

t.section("a legacy single-file proposal still resumes");
{
  const repo = fixture();
  const { result, calls, logs } = await resume("proposals/0081_fix_x.md", repo);
  t.check("the ticked step is not rebuilt", never(calls, "build:S1"), matching(calls, "build:S1").map((c) => c.label).join(","));
  t.check("it takes the already-done branch", logs.some((l) => /Step S1: already present in the tree; nothing to build/.test(l)), logs.join(" | "));
  t.check("and is recorded as skipped", (result.skippedSteps || []).some((s) => s.id === "S1"), JSON.stringify(result.skippedSteps));
}

t.done();
