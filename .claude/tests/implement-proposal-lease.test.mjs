// Layer 2: the spec/ write lease, as implement-proposal drives it.
//
// The hook itself is tested by executing it (hook.test.sh). This tests the
// other half: that the run opens a lease before it writes spec/, and releases
// it on EVERY way out. A release that does not happen leaves spec/ writable
// for whatever runs next, and the terminal paths are exactly where that is
// least likely to be noticed, so each one gets its own case.
//
// Run: node .claude/tests/implement-proposal-lease.test.mjs

import { runWorkflow, suite, matching, never, firstIndex } from "./harness.mjs";

const t = suite("implement-proposal: spec lease");
const WF = ".claude/workflows/implement-proposal.js";

const PLAN = (over = {}) => ({
  approved: true,
  alreadyApplied: false,
  statusLine: "- **Status:** Approved for implementation.",
  specEdits: [
    { id: "1.1", targetFile: "spec/04_control-plane.md", subsection: "S1", summary: "a row", method: "authored" },
  ],
  nonSpecStaged: [],
  findingIds: [],
  ...over,
});

const base = (over = {}) => ({
  plan: PLAN(),
  "lease-open": "{}",
  "lease-release": "{}",
  "apply:": { applied: ["1.1"], unappliable: [], deviations: [] },
  "verify:": { discrepancies: [] },
  "verify-aligned": { aligned: true, missing: [] },
  "commit-spec": "committed",
  "mark-and-commit-spec": "recorded",
  ...over,
});

const opened = (calls) => matching(calls, "lease-open").length;
const released = (calls) => matching(calls, "lease-release").length;

const ARGS = {
  proposalPath: "proposals/0081_fix_x",
  repoRoot: "/repo",
  date: "2026-08-31",
  implementCode: false,
};

t.section("C1. the lease opens before anything writes spec/");
{
  const { calls } = await runWorkflow(WF, ARGS, base());
  t.check("a lease is opened", opened(calls) >= 1);
  t.check(
    "before the first apply agent",
    firstIndex(calls, "lease-open") < firstIndex(calls, "apply:"),
    "open@" + firstIndex(calls, "lease-open") + " apply@" + firstIndex(calls, "apply:"),
  );
  const p = calls.find((c) => c.label.startsWith("lease-open")).prompt;
  t.check("it names the proposal", p.includes("0081_fix_x"));
  t.check("it scopes the allow list to the files this run touches", /--allow 'spec\/04_control-plane\.md'/.test(p));
  t.check("it is a dedicated one-command agent", /Do nothing else\./.test(p) && /Run exactly this command/.test(p));
  t.check("it runs on haiku", calls.find((c) => c.label.startsWith("lease-open")).opts.model === "haiku");
}

t.section("C2. the lease is released on every return path");
const paths = [
  ["spec-only (clean run)", {}, "spec-only"],
  ["not-approved", { plan: PLAN({ approved: false, alreadyApplied: false }) }, "not-approved"],
  [
    "spec-unappliable",
    { "apply:": { applied: [], unappliable: [{ id: "1.1", reason: "anchor drifted" }], deviations: [] } },
    "spec-unappliable",
  ],
  [
    "spec-not-clean",
    { "verify:": { discrepancies: [{ title: "d", file: "spec/04_control-plane.md", where: "w", expected: "e", observed: "o", fix: "f" }] } },
    "spec-not-clean",
  ],
  [
    "not-aligned",
    {
      plan: PLAN({ alreadyApplied: true }),
      "verify-aligned": { aligned: false, missing: ["1.1 at its anchor"] },
      "apply-missing": { applied: [], unappliable: [], deviations: [] },
    },
    "not-aligned",
  ],
];
// The invariant is not "a release always happens" -- a path that returns before
// the spec phase has no lease to release, and calling release there would be a
// write with nothing to write about. It is that NO PATH LEAVES ONE OPEN: if the
// run opened a lease, it released it.
for (const [name, over, wantStatus] of paths) {
  const { result, calls } = await runWorkflow(WF, ARGS, base(over));
  t.check(name + ": reaches " + wantStatus, result && result.status === wantStatus, result && result.status);
  t.check(
    name + ": no lease is left open",
    opened(calls) === 0 || released(calls) >= 1,
    "opened=" + opened(calls) + " released=" + released(calls),
  );
  t.check(name + ": no release without an open", released(calls) === 0 || opened(calls) >= 1);
}
t.check(
  "not-approved returns before the spec phase, so it opens nothing at all",
  (await runWorkflow(WF, ARGS, base({ plan: PLAN({ approved: false, alreadyApplied: false }) }))).calls
    .filter((c) => c.label.startsWith("lease-")).length === 0,
);

t.section("C2b. a run that never opens a lease never releases one");
{
  const { calls } = await runWorkflow(WF, ARGS, base({ plan: PLAN({ specEdits: [] }) }));
  t.check("no spec edits means no lease", opened(calls) === 0, String(opened(calls)));
  t.check("and no release call", released(calls) === 0, String(released(calls)));
}

t.section("C2c. the lease closes before the code phase begins");
{
  const { calls } = await runWorkflow(
    WF,
    { ...ARGS, implementCode: true },
    base(),
    { subworkflows: { "implement-proposal-build": { steps: [], commits: [], green: true, reviewClean: true } } },
  );
  const lastRelease = calls.map((c) => c.label).lastIndexOf(
    calls.map((c) => c.label).filter((l) => l.startsWith("lease-release")).slice(-1)[0],
  );
  const build = firstIndex(calls, "workflow:");
  t.check("the build subworkflow ran", build >= 0);
  t.check(
    "a release precedes it, so spec/ is locked while code lands",
    lastRelease >= 0 && lastRelease < build,
    "release@" + lastRelease + " build@" + build,
  );
}

t.section("C13. every prompt names a role file, never a concatenated path");
{
  const { calls } = await runWorkflow(WF, ARGS, base());
  const summaryCarriers = calls.filter((c) => /THE PROPOSAL'S SUMMARY/.test(c.prompt));
  t.check("some agent is given the summary", summaryCarriers.length > 0);
  t.check(
    "it points at the summary role file on a folder proposal",
    summaryCarriers.every((c) => c.prompt.includes("0081_fix_x.summary.md")),
    summaryCarriers[0] && summaryCarriers[0].prompt.slice(0, 200),
  );
}
{
  const { calls } = await runWorkflow(
    WF,
    { ...ARGS, proposalPath: "proposals/0081_fix_x.md" },
    base(),
  );
  const summaryCarriers = calls.filter((c) => /THE PROPOSAL'S SUMMARY/.test(c.prompt));
  t.check(
    "a legacy proposal points at the section instead",
    summaryCarriers.every((c) => /`## Summary` section of .*0081_fix_x\.md/.test(c.prompt)),
    summaryCarriers[0] && summaryCarriers[0].prompt.slice(0, 200),
  );
  t.check("and never invents a .summary.md that does not exist", summaryCarriers.every((c) => !c.prompt.includes(".summary.md")));
}

t.done();
