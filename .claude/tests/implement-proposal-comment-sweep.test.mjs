// Behavioural test for the build subworkflow's comment sweep.
//
// A spec edit can leave a code comment restating the contract it replaced. The
// proposal's review does not look for missed edit sites in code comments, so
// such a comment was caught only when a final reviewer looked past its brief,
// and two runs of one proposal disagreed about it. The sweep runs once, after
// the last checklist step, against the landed spec diff. These checks pin when
// it runs, that it corrects comments alone, that a non-comment site goes to a
// human as a proposed deviation instead, that a commit touching more than
// comments is reverted, and that no failure of the sweep blocks the run.
//
// Run: node .claude/tests/implement-proposal-comment-sweep.test.mjs
import { runWorkflow, suite, labels, matching, never, firstIndex } from "./harness.mjs";

const t = suite("implement-proposal-build: comment sweep");

const BUILD = ".claude/workflows/implement-proposal-build.js";

const specStep = (id, deps) => ({
  id, lane: "spec", title: "s", work: "SPEC-1", targets: ["spec/example.md"],
  tiers: ["static"], checklistStep: id, dependsOn: deps || [],
});
const codeStep = (id, deps) => ({
  id, lane: "code", title: "c", work: "w", targets: ["pkg/example"],
  tiers: ["unit"], checklistStep: id, dependsOn: deps || [],
});
const args = (steps, extra) => ({
  proposalPath: "proposals/0099_fix_x", repoRoot: "/repo", date: "d",
  plan: { blastRadius: [], steps }, ...extra,
});

const COMMENT_SITE = {
  file: "pkg/example/handler.go", line: 42, kind: "comment",
  quote: "// A failure here surfaces as the old code.", contradicts: "spec/example.md, \"Example heading\"",
};
const CODE_SITE = {
  file: "tests/tierN_example/example_test.go", line: 7, kind: "non-comment",
  quote: "want := \"OLD_CODE\"", contradicts: "spec/example.md, \"Example heading\"",
};

const STUBS = (over) => ({
  "checklist-ticks": { ticked: [] },
  baseline: { sha: "base0000" },
  "build:*:base": { sha: "base0000" },
  "spec-targets:*": { files: ["spec/example.md"] },
  "lease-open:*": "{}",
  "lease-release:*": "{}",
  "lease-check:*": { leaseHeld: false },
  "apply:*": { applied: ["SPEC-1"], unappliable: [], deviations: [] },
  "verify:S1:spec": { discrepancies: [] },
  "commit-spec:*": "ok",
  "compile:*": { compiles: true, errors: [], leaseHeld: false },
  "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/example/a.go"], testsAddedOrModified: [] },
  "review:*": { findings: [] },
  verify: { green: true, tiersRun: ["static"], failures: [] },
  "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
  "tick:*": "DONE",
  "compile-guard:*": { clean: true, compiles: true },
  "proposal-edit-audit": { edited: false, commits: [] },
  "sweep:find": { searched: "grep", sites: [COMMENT_SITE, CODE_SITE] },
  "sweep:fix": { commit: "sweep123", edited: [{ file: COMMENT_SITE.file, line: 42, after: "// corrected" }], skipped: [] },
  "sweep:check": { commentOnly: true, offending: [], reverted: "" },
  default: {},
  ...over,
});

// ---- T1: after a spec step lands, the sweep runs once, before Verify -------

t.section("T1: the sweep runs after the checklist and before the final gate");
{
  const { result, calls } = await runWorkflow(BUILD, args([specStep("S1"), codeStep("S2", ["S1"])]), STUBS());
  const L = labels(calls);
  t.check("the find agent ran once", matching(calls, "sweep:find").length === 1, JSON.stringify(L));
  t.check("it ran after the last checklist step", firstIndex(calls, "sweep:find") > firstIndex(calls, "tick:S2"), JSON.stringify(L));
  t.check("it ran before the whole-change verify", firstIndex(calls, "sweep:find") < L.indexOf("verify"), JSON.stringify(L));
  t.check("it ran before the final review", firstIndex(calls, "sweep:find") < firstIndex(calls, "review:conformance"), JSON.stringify(L));
  const find = calls.find((c) => c.label === "sweep:find");
  t.check("the find agent reads the landed spec diff from the baseline", /git diff base0000\.\.HEAD -- spec\//.test(find.prompt), find.prompt);
  t.check("the find agent is told it is read-only", /read-only/.test(find.prompt), find.prompt);
  t.check("the result reports a committed sweep", result && result.commentSweep && result.commentSweep.status === "committed",
    JSON.stringify(result && result.commentSweep));
  t.check("the result carries the sweep commit", result.commentSweep.commit === "sweep123", JSON.stringify(result.commentSweep));
}

// ---- T2: only comment sites reach the fixer; the rest go to a human --------

t.section("T2: a non-comment site is recorded as a proposed deviation, never edited");
{
  const { result, calls } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS());
  const fix = calls.find((c) => c.label === "sweep:fix");
  t.check("the fixer ran", !!fix, JSON.stringify(labels(calls)));
  t.check("the fixer was given the comment site", fix && fix.prompt.includes(COMMENT_SITE.file), fix && fix.prompt);
  t.check("the fixer was NOT given the non-comment site", fix && !fix.prompt.includes(CODE_SITE.file), fix && fix.prompt);
  t.check("the fixer is limited to comment lines", fix && /EDIT COMMENT LINES ONLY/.test(fix.prompt), fix && fix.prompt);
  t.check("the fixer must run tier 0", fix && /tests\/tier0_static/.test(fix.prompt), fix && fix.prompt);
  const dev = calls.find((c) => c.label === "deviation:proposed:sweep");
  t.check("a proposed deviation was recorded", !!dev, JSON.stringify(labels(calls)));
  t.check("it names the non-comment site", dev && dev.prompt.includes(CODE_SITE.file), dev && dev.prompt);
  t.check("the result lists the non-comment site", (result.commentSweep.nonCommentSites || []).some((s) => s.file === CODE_SITE.file),
    JSON.stringify(result.commentSweep));
}

// ---- T3: a commit that touched more than comments is reverted --------------

t.section("T3: the independent check reverts a commit with non-comment lines");
{
  const { result, calls, logs } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS({
    "sweep:check": { commentOnly: false, offending: ["+\treturn nil"], reverted: "rev456" },
  }));
  const check = calls.find((c) => c.label === "sweep:check");
  t.check("the check reads the sweep commit", check && check.prompt.includes("sweep123"), check && check.prompt);
  t.check("the check decides with classify-diff.mjs", check && /classify-diff\.mjs sweep123~1\.\.sweep123 --json/.test(check.prompt), check && check.prompt);
  t.check("the check is told to revert on a non-comment line", check && /git revert --no-edit sweep123/.test(check.prompt), check && check.prompt);
  t.check("the check runs on its own small model", check && check.opts.model === "haiku", JSON.stringify(check && check.opts));
  t.check("the result says reverted", result.commentSweep.status === "reverted", JSON.stringify(result.commentSweep));
  t.check("the revert SHA is reported", result.commentSweep.reverted === "rev456", JSON.stringify(result.commentSweep));
  t.check("the revert is logged", logs.some((l) => /was reverted/.test(l)), logs.join(" | "));
  t.check("the run still reached the final gate", labels(calls).includes("verify"), JSON.stringify(labels(calls)));
}

// ---- T4: nothing found means no fixer and no deviation ---------------------

t.section("T4: an empty find result ends the sweep quietly");
{
  const { result, calls } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS({ "sweep:find": { searched: "grep", sites: [] } }));
  t.check("the fixer never ran", never(calls, "sweep:fix"), JSON.stringify(labels(calls)));
  t.check("no deviation was recorded", never(calls, "deviation:proposed:sweep"), JSON.stringify(labels(calls)));
  t.check("the result says there was nothing to correct", result.commentSweep.status === "no-comment-sites", JSON.stringify(result.commentSweep));
}

// ---- T5: a dead sweep agent does not block the run -------------------------

t.section("T5: a find agent that dies leaves the run going");
{
  const { result, calls, logs } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS({ "sweep:find": null }));
  t.check("the fixer never ran", never(calls, "sweep:fix"), JSON.stringify(labels(calls)));
  t.check("the result says the find failed", result.commentSweep.status === "find-failed", JSON.stringify(result.commentSweep));
  t.check("the failure is logged", logs.some((l) => /find agent returned no result/.test(l)), logs.join(" | "));
  t.check("the run still reached the final gate", labels(calls).includes("verify"), JSON.stringify(labels(calls)));
  t.check("the run still finished", result.status === "implemented", "status=" + result.status);
}

t.section("T5b: a fixer that dies leaves the run going");
{
  const { result, calls } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS({ "sweep:fix": null }));
  t.check("the check never ran", never(calls, "sweep:check"), JSON.stringify(labels(calls)));
  t.check("the result says the fix failed", result.commentSweep.status === "fix-failed", JSON.stringify(result.commentSweep));
  t.check("the run still finished", result.status === "implemented", "status=" + result.status);
}

// ---- T6: no landed spec edit, no sweep ---------------------------------------

t.section("T6: a code-only checklist does not sweep");
{
  const { result, calls } = await runWorkflow(BUILD, args([codeStep("S1")]), STUBS());
  t.check("no sweep agent ran", never(calls, "sweep:"), JSON.stringify(labels(calls)));
  t.check("the result says why", result.commentSweep.ran === false && /no spec step landed/.test(result.commentSweep.reason),
    JSON.stringify(result.commentSweep));
}

// ---- T7: the switch turns it off ---------------------------------------------

t.section("T7: sweepComments:false skips the sweep");
{
  const { result, calls } = await runWorkflow(BUILD, args([specStep("S1")], { sweepComments: false }), STUBS());
  t.check("no sweep agent ran", never(calls, "sweep:"), JSON.stringify(labels(calls)));
  t.check("the result says it was disabled", /sweepComments:false/.test(result.commentSweep.reason), JSON.stringify(result.commentSweep));
}

// ---- T8: spec-only mode stops before the sweep -------------------------------

t.section("T8: spec-only mode does not sweep");
{
  const { calls } = await runWorkflow(BUILD, args([specStep("S1"), codeStep("S2", ["S1"])], { specOnly: true }), STUBS());
  t.check("no sweep agent ran", never(calls, "sweep:"), JSON.stringify(labels(calls)));
}

// ---- T9: the final review is told a sweep commit is expected -----------------

t.section("T9: the final reviewers do not read a sweep commit as scope creep");
{
  const { calls } = await runWorkflow(BUILD, args([specStep("S1")]), STUBS());
  const reviewers = matching(calls, "review:").filter((c) => /^review:(conformance|invariants|completeness):/.test(c.label));
  t.check("the final reviewers ran", reviewers.length === 3, JSON.stringify(labels(calls)));
  t.check("every final reviewer is told about `comments:` commits",
    reviewers.every((c) => /begins `comments:` comes from this run's comment sweep/.test(c.prompt)),
    reviewers.map((c) => c.label).join(","));
}

// ---- T10: the parent forwards the sweep's result ----------------------------

t.section("T10: implement-proposal returns the sweep result");
{
  const PARENT = ".claude/workflows/implement-proposal.js";
  const sweep = { ran: true, status: "committed", commit: "sweep123" };
  const { result } = await runWorkflow(PARENT,
    { proposalPath: "proposals/0099_fix_x", repoRoot: "/repo", date: "d", implementCode: true },
    { plan: { approved: true, alreadyApplied: false, statusLine: "Approved", specEdits: [], nonSpecStaged: [], findingIds: [] } },
    { subworkflows: { "implement-proposal-build": { status: "implemented", green: true, reviewClean: true, steps: [], commits: [], commentSweep: sweep } } });
  t.check("commentSweep is in the result", result && result.commentSweep && result.commentSweep.commit === "sweep123",
    JSON.stringify(result));
}

t.done();
