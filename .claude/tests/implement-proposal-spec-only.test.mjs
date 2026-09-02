// Behavioural test for spec-only mode across implement-proposal.js and its
// implement-proposal-build.js subworkflow.
//
// The mode used to be a no-op: the parent returned before the subworkflow was
// ever invoked, so a run with implementCode:false made exactly one agent call
// ("plan") and applied nothing, while close-build-gaps.sh relied on it to land
// the spec before building code against it. These checks pin both halves of the
// fix: the parent forwards the mode, and the subworkflow runs the checklist's
// leading spec-lane prefix and stops.
//
// Run: node .claude/tests/implement-proposal-spec-only.test.mjs
import { runWorkflow, suite, labels, matching, never } from "./harness.mjs";

const t = suite("implement-proposal: spec-only mode");

const PARENT = ".claude/workflows/implement-proposal.js";
const BUILD = ".claude/workflows/implement-proposal-build.js";

const PARENT_PLAN = {
  approved: true,
  alreadyApplied: false,
  statusLine: "Approved",
  specEdits: [{ id: "7.1", targetFile: "spec/28_x.md", subsection: "S", summary: "s", method: "authored", command: "" }],
  nonSpecStaged: [],
  findingIds: [],
};

const parentArgs = (implementCode) => ({
  proposalPath: "proposals/0081_fix_x",
  repoRoot: "/repo",
  date: "2026-09-01",
  implementCode,
});

// The stub table the golden case uses for the build workflow, so a spec step
// really applies, verifies and commits under the script's own control.
const BUILD_STUBS = {
  "checklist-ticks": { ticked: [] },
  baseline: { sha: "b" },
  "build:S2:base": { sha: "b" },
  "spec-targets:*": { files: ["spec/16.md"] },
  "lease-open:*": "{}",
  "lease-release:*": "{}",
  // The prefix's exit checks the lease it may be leaking, and fails closed on
  // no answer, so a fixture that ends the prefix must say it was released.
  "lease-check:*": { leaseHeld: false },
  "apply:*": { applied: ["SPEC-1"], unappliable: [], deviations: [] },
  "verify:S1:spec": { discrepancies: [] },
  "commit-spec:*": "ok",
  "compile:*": { compiles: true, errors: [], leaseHeld: false },
  "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/a.go"], testsAddedOrModified: [] },
  "review:*": { findings: [] },
  "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
  "tick:*": "DONE",
  "compile-guard:*": { clean: true, compiles: true },
  "proposal-edit-audit": { edited: false, commits: [] },
  default: {},
};

const specStep = (id, deps) => ({
  id, lane: "spec", title: "s", work: "SPEC-1", targets: ["spec/16.md"],
  tiers: ["static"], checklistStep: id, dependsOn: deps || [],
});
const codeStep = (id, deps) => ({
  id, lane: "code", title: "c", work: "w", targets: ["pkg/a"],
  tiers: ["unit"], checklistStep: id, dependsOn: deps || [],
});
const buildArgs = (steps, extra) => ({
  proposalPath: "proposals/0081_fix_x", repoRoot: "/repo", date: "d",
  plan: { blastRadius: [], steps }, ...extra,
});

// ---- T1: the parent forwards the mode instead of short-circuiting ---------

t.section("T1: implementCode:false reaches the build subworkflow");
{
  const { result, calls } = await runWorkflow(PARENT, parentArgs(false), { plan: PARENT_PLAN }, {
    subworkflows: {
      "implement-proposal-build": { status: "spec-only", steps: [{ step: "S1", lane: "spec" }], commits: ["c1"] },
    },
  });
  const sub = calls.find((c) => c.label === "workflow:/repo/.claude/workflows/implement-proposal-build.js");
  t.check("the build subworkflow is invoked", !!sub, "calls: " + JSON.stringify(labels(calls)));
  t.check("the forwarded args carry specOnly:true", !!sub && JSON.parse(sub.prompt).specOnly === true,
    sub ? sub.prompt : "no subworkflow call");
  t.check('result.status is "spec-only"', result && result.status === "spec-only", JSON.stringify(result));
  t.check("the prefix's commits are reported", !!result && (result.commits || []).includes("c1"), JSON.stringify(result));
}

// ---- T2: the other value still forwards, so the mode cannot go dead again -

t.section("T2: implementCode:true forwards specOnly:false");
{
  const { result, calls } = await runWorkflow(PARENT, parentArgs(true), { plan: PARENT_PLAN }, {
    subworkflows: {
      "implement-proposal-build": { status: "implemented", green: true, reviewClean: true, steps: [], commits: [] },
    },
  });
  const sub = calls.find((c) => c.label === "workflow:/repo/.claude/workflows/implement-proposal-build.js");
  t.check("the forwarded args carry specOnly:false", !!sub && JSON.parse(sub.prompt).specOnly === false,
    sub ? sub.prompt : "no subworkflow call");
  t.check('result.status is "implemented"', result && result.status === "implemented", JSON.stringify(result));
}

// ---- T3: the build workflow runs the spec prefix and stops ----------------

t.section("T3: spec prefix applies, then the first code step stops the run");
{
  const { result, calls } = await runWorkflow(
    BUILD, buildArgs([specStep("S1"), codeStep("S2", ["S1"])], { specOnly: true }), BUILD_STUBS);
  t.check('result.status is "spec-only"', result && result.status === "spec-only", JSON.stringify(result));
  t.check("stopped at the first code step", result && result.stoppedAt === "S2", JSON.stringify(result));
  t.check("the spec step applied", matching(calls, "apply:S1").length === 1, JSON.stringify(labels(calls)));
  t.check("the spec step committed", matching(calls, "commit-spec:S1").length === 1, JSON.stringify(labels(calls)));
  t.check("the code step never built", never(calls, "build:S2"), JSON.stringify(labels(calls)));
  t.check("no whole-change verify ran", calls.every((c) => c.label !== "verify"), JSON.stringify(labels(calls)));
  t.check("no compile guard ran", never(calls, "compile-guard:"), JSON.stringify(labels(calls)));
  t.check("the lease is released", matching(calls, "lease-release:S1").length === 1, JSON.stringify(labels(calls)));
  const open = calls.find((c) => c.label === "lease-open:S1");
  t.check("the lease is opened with a TTL", !!open && open.prompt.includes("--ttl-hours"), open ? open.prompt : "no lease-open");
}

// ---- T4: an interleaved spec step is reported, not skipped ----------------

t.section("T4: a spec step behind the stopping step is reported");
{
  const { result, calls } = await runWorkflow(
    BUILD,
    buildArgs([specStep("S1"), codeStep("S2", ["S1"]), specStep("S3", ["S2"])], { specOnly: true }),
    BUILD_STUBS);
  t.check('result.status is "spec-only-incomplete"', result && result.status === "spec-only-incomplete", JSON.stringify(result));
  t.check("stopped at S2", result && result.stoppedAt === "S2", JSON.stringify(result));
  t.check("S3 is named as behind", result && JSON.stringify(result.specStepsBehind) === '["S3"]', JSON.stringify(result));
  t.check("the code step never built", never(calls, "build:S2"), JSON.stringify(labels(calls)));
}

// ---- T5: a wholly-spec checklist stops before Verify ----------------------

t.section("T5: an all-spec checklist does not fall into Verify");
{
  const { result, calls } = await runWorkflow(
    BUILD, buildArgs([specStep("S1"), specStep("S1b", ["S1"])], { specOnly: true }), BUILD_STUBS);
  t.check('result.status is "spec-only"', result && result.status === "spec-only", JSON.stringify(result));
  t.check("both spec steps committed", matching(calls, "commit-spec:").length === 2, JSON.stringify(labels(calls)));
  t.check("no compile guard ran", never(calls, "compile-guard:"), JSON.stringify(labels(calls)));
  t.check("no whole-change verify ran", calls.every((c) => c.label !== "verify"), JSON.stringify(labels(calls)));
}

t.done();
