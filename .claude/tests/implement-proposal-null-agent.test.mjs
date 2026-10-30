// Behavioural test for a dead agent in the implement-proposal pipeline.
//
// `agent()` returns null when the subagent never ran: the account hit a usage
// limit, or the call died on a terminal API error after its own retries. The
// step loop in implement-proposal-build.js documents that contract and every
// agent result there is guarded for it, but the plan, build and close results
// were not, so each one turned a dead agent into a bare TypeError with no
// result and no status. The four sites were reproduced as "Cannot read
// properties of null (reading 'approved' / 'steps' / 'closed')"; a fifth, a
// dead plan REVISER, destroyed a plan that had already been produced.
//
// These checks assert behaviour: the returned status object, and the absence of
// the crash. No prompt prose is asserted.
//
// Run: node .claude/tests/implement-proposal-null-agent.test.mjs
import { runWorkflow, suite } from "./harness.mjs";

const t = suite("implement-proposal: a dead agent is a status, not a crash");

const PARENT = ".claude/workflows/implement-proposal.js";
const BUILD = ".claude/workflows/implement-proposal-build.js";

const ARGS = { proposalPath: "proposals/0099_fix_x", date: "2026-09-01", repoRoot: "/repo" };

const OKPLAN = {
  approved: true,
  alreadyApplied: false,
  statusLine: "Approved",
  specEdits: [],
  nonSpecStaged: [],
  findingIds: [],
};

const BUILD_OK = { status: "implemented", green: true, reviewClean: true, steps: [], commits: [] };

t.section("1. the parent's plan agent dies");
{
  const { result, error } = await runWorkflow(PARENT, ARGS, { plan: null, default: {} });
  t.check("no throw", error === null, String(error && error.message));
  t.check("an honest status", !!result && result.status === "aborted", JSON.stringify(result));
  t.check("and it says why", !!result && /plan agent returned no result/.test(result.abortReason || ""));
}

t.section("2. the build subworkflow returns null");
{
  const { result, error } = await runWorkflow(
    PARENT,
    ARGS,
    { plan: OKPLAN, default: {} },
    { subworkflows: { "implement-proposal-build.js": null } },
  );
  t.check("no throw", error === null, String(error && error.message));
  t.check("aborted, as for a throwing subworkflow", !!result && result.status === "aborted", JSON.stringify(result));
}

t.section("3. the closing agent dies after a green build");
{
  const { result, error, logs } = await runWorkflow(
    PARENT,
    ARGS,
    { plan: { ...OKPLAN, findingIds: ["BG-1"] }, "close-findings": null, default: {} },
    { subworkflows: { "implement-proposal-build.js": BUILD_OK } },
  );
  t.check("no throw", error === null, String(error && error.message));
  t.check("the build's result survives", !!result && result.status === "implemented", JSON.stringify(result));
  t.check("nothing is claimed closed", !!result && result.findingsClosed.length === 0);
  t.check("and the operator is told", logs.some((l) => /left OPEN/.test(l)), JSON.stringify(logs));
}

t.section("4. the build subworkflow's planner dies");
{
  const { result, error } = await runWorkflow(BUILD, ARGS, {
    plan: null,
    "plan-critique:r1": { complete: true, gaps: [] },
    default: {},
  });
  t.check("no throw", error === null, String(error && error.message));
  t.check("aborted", !!result && result.status === "aborted", JSON.stringify(result));
  t.check("and not green", !!result && result.green === false && result.steps.length === 0);
}

t.section("5. the plan reviser dies after a good plan");
{
  const P1 = {
    blastRadius: ["pkg/a"],
    deviations: [],
    steps: [
      {
        id: "S1", title: "t", work: "w", targets: ["pkg/a"], tiers: ["unit"],
        specRefs: ["1"], dependsOn: [], lane: "code",
      },
    ],
  };
  const { error, logs } = await runWorkflow(BUILD, ARGS, {
    plan: P1,
    "plan-critique:r1": { complete: false, gaps: ["g"] },
    "plan-revise:r1": null,
    "checklist-ticks": { ticked: [] },
    default: {},
  });
  // The run continues past the plan phase into the step loop, where an
  // under-stubbed baseline guard raises its own unrelated error, so the
  // assertion is on the logs and on the absence of the null dereference rather
  // than on error === null.
  t.check("the previous plan is kept", logs.some((l) => /keeping the previous plan/.test(l)), JSON.stringify(logs));
  t.check("and the build still starts", logs.some((l) => /Build sequence: 1 steps/.test(l)), JSON.stringify(logs));
  t.check("no null deref", !/reading 'steps'/.test(String(error && error.message)), String(error && error.message));
}

t.done();
