// Layer 2: change-proposal.
//
// The real workflow body runs; only `agent` is stubbed. What is under test is
// what the script decides — which stages run, in what order, how many agents
// each dispatches, what each is told, and what the run returns when one of
// them fails or dissents.
//
// Run: node .claude/tests/change-proposal.test.mjs

import { runWorkflow, suite, labels, matching, never, firstIndex, ordered } from "./harness.mjs";

const t = suite("change-proposal");
const WF = ".claude/workflows/change-proposal.js";

const NEW_ARGS = {
  mode: "new",
  problem: "The adapter drops a tracing frame and nothing counts it.",
  context: "spec/16_observability.md:120 names the catalog",
  nextNumber: "0081",
  date: "2026-08-31",
  exemplar: "proposals/0080_fix_x.md",
  repoRoot: "/repo",
  maxReviewRounds: 1,
};

// Enough of a stub table to get through Init/Validate/Draft/Challenge/Write
// and stop at the first review round with nothing found.
const newStubs = (over = {}) => ({
  init: "created",
  "validate:": { verdict: "stands", findings: [{ statement: "s", evidence: "spec/16:1", loadBearing: true }] },
  "validate:consolidate": {
    viable: true,
    restatement: "The adapter drops a tracing frame.",
    title: "Catalog the adapter tracing frame drop counter",
    kind: "fix",
    confirmed: ["the counter is absent"],
    refuted: [],
  },
  "draft:": { viable: true, approach: "add the counter", changes: [], rejected: [], risks: [] },
  "draft:consolidate": {
    viable: true,
    title: "Catalog the adapter tracing frame drop counter",
    kind: "fix",
    problemRestatement: "r",
    decisions: ["d"],
    changes: [{ id: "SPEC-1", title: "catalog row", targets: ["spec/16"], rationale: "r", sketch: "s" }],
    nonGoals: [],
  },
  "challenge:": { verdict: "keep", reasons: "it survives", evidence: [] },
  write: "written",
  conventions: "conforms",
  snap: "DONE",
  diffcount: "0",
  "r1:review:": { coverage: "read it all", findings: [] },
  default: {},
  ...over,
});

t.section("B1. Init creates the directory and its skeletons before anything else");
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const init = calls.find((c) => c.label === "init");
  t.check("an init agent runs", !!init);
  t.check("it runs first", calls[0].label === "init", calls[0] && calls[0].label);
  for (const role of [
    "problem-statement", "summary", "status", "implementation-checklist",
    "spec-changes", "non-spec-changes", "review-log", "deviations",
  ]) {
    t.check("names ." + role + ".md", init.prompt.includes("0081_fix_") && init.prompt.includes("." + role + ".md"));
  }
  t.check("the problem statement is placed verbatim", init.prompt.includes(NEW_ARGS.problem));
  t.check("the caller's citations are seeded as unverified", /marked `unverified`/.test(init.prompt));
  t.check("the status skeleton pins status: Draft", /status: Draft/.test(init.prompt));
  t.check(
    "no agent before Bootstrap names a path outside the proposal directory",
    calls.every((c) => !/\/(spec|pkg|charts|schemas)\//.test(c.prompt.split("HARD CONSTRAINT")[1] || "")),
  );
}

t.section("B2. Validate dispatches six lenses plus one consolidator");
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const lenses = matching(calls, "validate:").filter((c) => c.label !== "validate:consolidate");
  t.check("six lenses", lenses.length === 6, String(lenses.length));
  t.check(
    "each is a distinct lens",
    new Set(lenses.map((c) => c.label)).size === 6,
    lenses.map((c) => c.label).join(","),
  );
  for (const k of ["premise", "evidence", "prior-art", "scope", "impact", "alternatives"]) {
    t.check("lens " + k + " runs", lenses.some((c) => c.label === "validate:" + k));
  }
  t.check("one consolidator", matching(calls, "validate:consolidate").length === 1);
  t.check("it runs after every lens", ordered(calls, "validate:premise", "validate:consolidate"));
  t.check(
    "only the consolidator may edit the problem statement",
    /only file you may edit is .*problem-statement\.md/.test(calls.find((c) => c.label === "validate:consolidate").prompt),
  );
  t.check(
    "the lenses are read-only",
    lenses.every((c) => /read-only investigator/.test(c.prompt)),
  );
}
{
  // A dead lens must not crash the run or be mistaken for a clean verdict.
  const { result, calls } = await runWorkflow(WF, NEW_ARGS, newStubs({ "validate:scope": null }));
  t.check("a dead lens does not crash the run", !!result, String(result));
  t.check("the consolidator still runs", !never(calls, "validate:consolidate"));
  t.check(
    "and is shown only the lenses that returned",
    !/"lens": *"scope"/.test(calls.find((c) => c.label === "validate:consolidate").prompt),
  );
}
{
  const { result, calls } = await runWorkflow(
    WF, NEW_ARGS,
    newStubs({ "validate:": null, "validate:consolidate": null }),
  );
  t.check("every lens failing is reported, not papered over", result.status === "interrupted", result.status);
  t.check("no draft stance runs", never(calls, "draft:"));
}

t.section("B3. a non-viable validation stops before any design work");
{
  const { result, calls } = await runWorkflow(WF, NEW_ARGS, newStubs({
    "validate:consolidate": { viable: false, whyNotValid: "", whyNotViable: "already solved by §16.1", restatement: "", confirmed: [], refuted: [] },
  }));
  t.check("status not-viable", result.status === "not-viable", result.status);
  t.check("the reason is carried out", /already solved/.test(result.reason || ""));
  t.check("no draft stance runs", never(calls, "draft:"));
  t.check("no write runs", never(calls, "write"));
}

t.section("B4. Draft dispatches six stances plus one consolidator, and Challenge still runs per change");
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const stances = matching(calls, "draft:").filter((c) => c.label !== "draft:consolidate");
  t.check("six stances", stances.length === 6, String(stances.length));
  for (const k of ["minimal", "spec-first", "reuse", "failure-modes", "implementor", "contrarian"]) {
    t.check("stance " + k + " runs", stances.some((c) => c.label === "draft:" + k));
  }
  t.check("one consolidator", matching(calls, "draft:consolidate").length === 1);
  t.check("it runs after every stance", ordered(calls, "draft:minimal", "draft:consolidate"));
  t.check(
    "stances are told to commit rather than hedge",
    stances.every((c) => /commit to YOUR stance rather than hedging/.test(c.prompt)),
  );
  t.check("one challenge per surviving change", matching(calls, "challenge:").length === 1);
  t.check("challenge runs after the consolidator", ordered(calls, "draft:consolidate", "challenge:"));
}
{
  // The contrarian stance arguing for no change must reach the consolidator as
  // an argument to answer, not be silently dropped.
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs({
    "draft:contrarian": { viable: false, whyNotViable: "the frame is already counted upstream", approach: "" },
  }));
  const c = calls.find((x) => x.label === "draft:consolidate").prompt;
  t.check("the consolidator is told a stance dissented", /TAKE THE DISSENT SERIOUSLY/.test(c));
  t.check("and is given its reasoning", /already counted upstream/.test(c));
}
{
  const { result, calls } = await runWorkflow(WF, NEW_ARGS, newStubs({
    "draft:consolidate": { viable: false, whyNotViable: "no change is warranted", title: "", kind: "fix", problemRestatement: "", decisions: [], changes: [], nonGoals: [] },
  }));
  t.check("a consolidator that finds no change needed stops the run", result.status === "not-viable", result.status);
  t.check("no write runs", never(calls, "write"));
}
{
  const { result, calls } = await runWorkflow(WF, NEW_ARGS, newStubs({
    "challenge:": { verdict: "drop", reasons: "an existing surface covers it", evidence: [] },
  }));
  t.check("every change dropped means no change needed", result.status === "no-change-needed", result.status);
  t.check("no write runs", never(calls, "write"));
  t.check("the dropped change is reported with its reason", /existing surface/.test(JSON.stringify(result.dropped || [])));
}

t.section("B5. Write fills the role files, not one document");
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const w = calls.find((c) => c.label === "write");
  t.check("a write agent runs", !!w);
  for (const role of ["summary", "spec-changes", "non-spec-changes", "implementation-checklist", "problem-statement", "status"]) {
    t.check("write names ." + role + ".md", w.prompt.includes("." + role + ".md"));
  }
  t.check("it is scoped to the proposal directory", /only files you may edit are the six named below, all inside/.test(w.prompt));
  t.check("spec staging is separated from the rest", /the staged SPEC edits and nothing else/.test(w.prompt));
  t.check("an empty spec-changes file is explicitly valid", /do not invent a spec edit to fill it/.test(w.prompt));
  t.check("the checklist carries the one-lane rule", /ONE lane only/.test(w.prompt));
  t.check("and the leading-spec-block norm", /standard pattern is every\s+spec step first/.test(w.prompt));
  t.check("the deliverable index is named as the resolver of ids", /ONLY place a deliverable id resolves/.test(w.prompt));
  t.check("it does not touch the status", /leave it alone. The status is Draft/.test(w.prompt));
}

t.section("B6. Bootstrap migrates a legacy proposal, then backfills");
{
  const { calls } = await runWorkflow(
    WF,
    { mode: "review", proposalPath: "proposals/0076_fix_y.md", date: "2026-08-31", exemplar: "e.md", repoRoot: "/repo", maxReviewRounds: 1 },
    newStubs(),
    { subworkflows: { "migrate-proposal": { status: "migrated", dir: "proposals/0076_fix_y" } } },
  );
  t.check("the migrator subworkflow is invoked", !never(calls, "workflow:"));
  t.check("it is invoked by path, not by name", calls.find((c) => c.label.startsWith("workflow:")).label.includes("migrate-proposal.js"));
  const boot = calls.find((c) => c.label === "bootstrap");
  t.check("bootstrap runs after it", !!boot && firstIndex(calls, "workflow:") < firstIndex(calls, "bootstrap"));
  t.check(
    "and works on the MIGRATED directory, not the legacy path",
    boot.prompt.includes("proposals/0076_fix_y/0076_fix_y.summary.md"),
    (boot.prompt.match(/0076_fix_y[^\s]*/) || [])[0],
  );
  t.check("a complete proposal is a no-op", /change NOTHING and reply SKIPPED/.test(boot.prompt));
  t.check("an inferred order must be marked", /note on its\s+line that the order is inferred/.test(boot.prompt));
}
{
  const { result, calls } = await runWorkflow(
    WF,
    { mode: "review", proposalPath: "proposals/0076_fix_y.md", date: "2026-08-31", exemplar: "e.md", repoRoot: "/repo", maxReviewRounds: 1 },
    newStubs(),
    { subworkflows: { "migrate-proposal": { status: "lost-content", reason: "3 lines lost" } } },
  );
  t.check("a failed migration stops the run", result.status === "migration-failed", result.status);
  t.check("no bootstrap or review follows", never(calls, "bootstrap") && never(calls, "r1:review:"));
  t.check("the migrator's reason is carried out", /3 lines lost/.test(result.reason || ""));
}
{
  const { calls } = await runWorkflow(
    WF,
    { mode: "review", proposalPath: "proposals/0076_fix_y", date: "2026-08-31", exemplar: "e.md", repoRoot: "/repo", maxReviewRounds: 1 },
    newStubs(),
  );
  t.check("a folder-layout proposal is not migrated", never(calls, "workflow:"));
  t.check("bootstrap still runs to backfill", !never(calls, "bootstrap"));
}

t.section("B6b. snapshots and diffs span the directory, not one file");
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const snaps = matching(calls, "snap:");
  t.check("a snapshot is taken", snaps.length > 0);
  t.check("it copies the directory recursively", snaps.every((c) => /cp -r /.test(c.prompt)), snaps[0] && snaps[0].prompt.slice(0, 120));
  t.check("it is a dedicated one-command agent", snaps.every((c) => /Do nothing else\./.test(c.prompt)));
  const dc = calls.filter((c) => c.label === "diffcount");
  if (dc.length) t.check("the hunk count diffs recursively", dc.every((c) => /diff -ru /.test(c.prompt)));
}

t.done();
