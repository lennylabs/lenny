// The parent implement-proposal workflow reads almost none of the arguments the
// skill documents: every tuning bound is consumed inside
// implement-proposal-build.js, so the parent's only job for them is to hand
// them across. It once handed across six keys, and a run invoked with
// maxStepAttempts: 7 and a spec review focus reached the child with neither, so
// the documented bound had no effect and the focused per-spec-step reviewer
// could never be switched on through the documented entry point.
//
// The assertion is on the argument object the child is actually called with,
// which the harness records as the subworkflow call's prompt. That is the seam
// the defect lived at, rather than a regex over prompt prose.

import { loadWorkflow, runWorkflow, suite } from "./harness.mjs";

const WORKFLOW = ".claude/workflows/implement-proposal.js";

// One distinct value per argument, so a forwarding line that names the wrong
// key is caught rather than passing on a coincidentally equal value.
const TUNING = {
  maxPlanRounds: 3,
  maxStepAttempts: 7,
  maxDeadAttempts: 9,
  maxReplans: 11,
  replanEvery: 13,
  replanStruggleAttempts: 15,
  maxVerifyRounds: 17,
  maxReviewRounds: 19,
  coverageFloor: 90,
  introspectEvery: 21,
  minUnproductiveRounds: 23,
  maxPhaseOscillations: 25,
  maxFinalGateFailures: 27,
  expensiveTierSeconds: 29,
  leaseTtlHours: 31,
  skipBuild: true,
  specReviewFocus: ["4.7 adapter manifest"],
};

const REQUIRED = {
  proposalPath: "proposals/0099_x",
  date: "2026-09-01",
  repoRoot: "/repo",
};

const STUBS = {
  plan: {
    approved: true,
    alreadyApplied: false,
    statusLine: "Approved",
    specEdits: [],
    nonSpecStaged: [],
    findingIds: [],
  },
  default: {},
};

const OPTS = {
  subworkflows: {
    "implement-proposal-build.js": { steps: [], green: true, reviewClean: true, commits: [] },
  },
};

async function childArgs(args) {
  const { calls, error } = await runWorkflow(WORKFLOW, args, STUBS, OPTS);
  const handoff = calls.filter((c) => c.label.startsWith("workflow:"));
  return { error, handoff };
}

const t = suite("implement-proposal argument forwarding");

const full = await childArgs({ ...REQUIRED, ...TUNING });
t.check("the run reaches the build subworkflow", !full.error, full.error && full.error.message);
t.check("the build subworkflow is invoked exactly once", full.handoff.length === 1);

const sub = JSON.parse(full.handoff.length ? full.handoff[0].prompt : "{}");
for (const [key, value] of Object.entries(TUNING)) {
  t.check(
    "forwards " + key,
    JSON.stringify(sub[key]) === JSON.stringify(value),
    "child received " + JSON.stringify(sub[key]),
  );
}

// The child defaults each bound itself. A parent that re-defaulted them would
// be a second default table to drift, so an argument the caller omitted must
// reach the child omitted.
const bare = await childArgs({ ...REQUIRED });
const bareSub = JSON.parse(bare.handoff.length ? bare.handoff[0].prompt : "{}");
const redefaulted = Object.keys(TUNING).filter((k) => bareSub[k] !== undefined);
t.check(
  "an omitted argument reaches the child omitted rather than re-defaulted",
  redefaulted.length === 0,
  "parent supplied a default for " + redefaulted.join(", "),
);

// Every ARG_CLASS key names an argument the script reads. A key nothing reads
// is a classification left behind by a removed branch, which is how the stale
// specReviewFocus entry outlived the forwarding it was written for.
{
  const src = loadWorkflow(WORKFLOW);
  const registry = src.slice(src.indexOf("ARG_CLASS"));
  const body = registry.slice(registry.indexOf("{"), registry.indexOf("};") + 1);
  const keys = [...body.matchAll(/^\s*([A-Za-z_$][\w$]*)\s*:/gm)].map((m) => m[1]);
  const orphans = keys.filter((k) => !new RegExp("\\binput\\." + k + "\\b").test(src));
  t.check("every ARG_CLASS key names an argument the script reads", orphans.length === 0,
    "unread: " + orphans.join(", "));
}

t.done();
