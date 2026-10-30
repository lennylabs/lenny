// Behavioural test for the approval gate in implement-proposal.js.
//
// The gate used to read `if (!plan.approved && !plan.alreadyApplied)`, which
// let a fact about the tree stand in for a human decision: a run stubbed with
// {approved:false, alreadyApplied:true, statusLine:"Draft - do not implement"}
// did not stop, entered implement-proposal-build.js and returned
// "implemented-not-green", so a Draft or Implemented proposal whose spec edits
// an interrupted earlier run had already landed got its full code blast radius
// built. This is the only approval check the code lane has: the build
// subworkflow never reads a status, and the spec-lease hook's own refusal
// covers spec/ alone. The rule these checks pin is the one
// .claude/skills/implement-proposal/SKILL.md already states, that the status
// must be "Approved" and anything else stops the run.
//
// Run: node .claude/tests/implement-proposal-gate.test.mjs
import { runWorkflow, loadWorkflow, suite, labels, never } from "./harness.mjs";

const t = suite("implement-proposal: approval gate");

const PARENT = ".claude/workflows/implement-proposal.js";

const ARGS = {
  proposalPath: "proposals/0099_test_x",
  repoRoot: "/repo",
  date: "2026-09-01",
  implementCode: true,
};

const PLAN = (o) => ({
  approved: false,
  alreadyApplied: false,
  statusLine: "Draft - do not implement",
  specEdits: [],
  nonSpecStaged: [],
  findingIds: [],
  ...o,
});

const run = (plan) =>
  runWorkflow(PARENT, ARGS, { plan }, {
    subworkflows: {
      "implement-proposal-build": { status: "implemented", green: true, reviewClean: true, steps: [], commits: [] },
    },
  });

const SUB = "workflow:/repo/.claude/workflows/implement-proposal-build.js";

// ---- T1: landed spec edits do not substitute for approval ----------------

t.section("T1: unapproved with the spec edits already in the tree is refused");
{
  const { result, calls } = await run(PLAN({ alreadyApplied: true }));
  t.check('result.status is "not-approved"', result && result.status === "not-approved", JSON.stringify(result));
  t.check("the build subworkflow never ran", never(calls, SUB), JSON.stringify(labels(calls)));
}

// ---- T2: the plain refusal still refuses ---------------------------------

t.section("T2: unapproved with no landed spec edits is refused");
{
  const { result, calls } = await run(PLAN({}));
  t.check('result.status is "not-approved"', result && result.status === "not-approved", JSON.stringify(result));
  t.check("the build subworkflow never ran", never(calls, SUB), JSON.stringify(labels(calls)));
}

// ---- T3 and T4: an approved proposal still runs, landed edits or not ------

for (const alreadyApplied of [false, true]) {
  t.section("T" + (alreadyApplied ? 4 : 3) + ": approved with alreadyApplied:" + alreadyApplied + " proceeds");
  const { result, calls } = await run(PLAN({ approved: true, alreadyApplied, statusLine: "Approved" }));
  t.check("the build subworkflow ran", !never(calls, SUB), JSON.stringify(labels(calls)));
  t.check('result.status is not "not-approved"', result && result.status !== "not-approved", JSON.stringify(result));
}

// ---- T5: the schema describes the four-state model -----------------------
//
// The status used to be a prose bullet and `.status.md` now carries Draft,
// Reviewed, Approved and Implemented only, so a schema description naming a
// bullet or an "Applied to spec" state sends the plan agent back to the
// mechanism proposal-status.mjs was written to retire. Scoped to the PLAN
// schema block: the prompt legitimately tells the agent not to parse a Status
// bullet, and a comment lower down explains which state the checklist replaced.

t.section("T5: the plan schema names no retired status mechanism");
{
  const src = loadWorkflow(PARENT);
  const schema = src.slice(src.indexOf("const PLAN = {"), src.indexOf("statusLine: { type: \"string\" }"));
  t.check("the schema block was located", schema.length > 0 && schema.includes("alreadyApplied"), "block: " + schema);
  t.check("no \"Status bullet\" description", !/Status bullet/.test(schema), schema);
  t.check("nothing is described as beginning a status bullet", !/begins "/.test(schema), schema);
  t.check("alreadyApplied is described as non-gating", schema.includes("does not affect the approval gate"), schema);
}

t.done();
