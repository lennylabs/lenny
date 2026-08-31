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
  "*:review:*": { coverage: "read it all", findings: [] },
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



// ---- Phase 3: the two review loops and sequential verification ------------

const REVIEW_ARGS = {
  mode: "review",
  proposalPath: "proposals/0081_fix_x",
  date: "2026-08-31",
  exemplar: "e.md",
  repoRoot: "/repo",
  maxSpecReviewRounds: 4,
  maxNonSpecReviewRounds: 4,
};

// A stub table that drives one finding through one round of whichever loop is
// running, then goes clean.
const loopStubs = (over = {}) => {
  let rounds = 0;
  return {
    bootstrap: "SKIPPED",
    conventions: "conforms",
    "probe:spec-changes": "YES",
    "snap*": "DONE",
    diffcount: "0",
    "spec-nonspec-handoff": "reconciled",
    "*:review:*": { coverage: "c", findings: [] },
    "*:dedup": { findings: [] },
    "*:verify-material": { confirmed: true, reason: "material" },
    "*:verify-evidence": { confirmed: true, reason: "evidence holds" },
    "*:fix": { summary: "fixed it in 0081_fix_x.non-spec-changes.md", newMechanisms: [] },
    "*:post-fix-review": { findings: [] },
    "verify-checklist": "ok",
    "mark-verified": "ok",
    "introspect*": null,
    default: {},
    ...over,
  };
};

// Match a review lens call in either loop: labels are r<N>:review:<lens>.
const isLens = (c) => /^r\d+:review:/.test(c.label);

t.section("B7. the spec loop is skipped when nothing is staged for spec");
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "probe:spec-changes": "NO" }));
  t.check("the probe runs", !never(calls, "probe:spec-changes"));
  t.check("the probe is a cheap dedicated agent", calls.find((c) => c.label === "probe:spec-changes").opts.model === "haiku");
  t.check("it is logged as skipped", logs.some((l) => /stages no spec edits; skipping the spec review loop/.test(l)));
  t.check("the non-spec loop still runs", logs.some((l) => /Entering the non-spec review loop/.test(l)));
  t.check("no spec loop is entered", !logs.some((l) => /Entering the spec review loop/.test(l)));
}

t.section("B8. spec converges before non-spec begins");
{
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const specAt = logs.findIndex((l) => /Entering the spec review loop/.test(l));
  const nonAt = logs.findIndex((l) => /Entering the non-spec review loop/.test(l));
  const handoffAt = logs.findIndex((l) => /Reconciled the deliverable index/.test(l));
  t.check("both loops run", specAt >= 0 && nonAt >= 0, "spec@" + specAt + " nonspec@" + nonAt);
  t.check("spec first", specAt < nonAt);
  t.check("the handoff runs between them", handoffAt > specAt && handoffAt < nonAt, "handoff@" + handoffAt);
}
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const h = calls.find((c) => c.label === "spec-nonspec-handoff");
  t.check("the handoff may edit only the summary and the checklist", /only files you may edit are .*summary\.md and .*implementation-checklist\.md/.test(h.prompt));
  t.check("it rebuilds the deliverable index", /Rebuild `## Deliverable index`/.test(h.prompt));
  t.check("it writes the spec-lane steps as a leading block", /leading block/.test(h.prompt));
  t.check("and is told it is not a review round", /This is not a review round/.test(h.prompt));
}

t.section("B9. each loop tells its lenses and its fixer what it owns");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const lenses = calls.filter(isLens);
  const specLenses = lenses.filter((c) => /SCOPE OF THIS LOOP. You are reviewing the STAGED SPEC EDITS/.test(c.prompt));
  const nonLenses = lenses.filter((c) => /Read the staged spec edits .* AS ONE DOCUMENT/s.test(c.prompt));
  t.check("spec-loop lenses are scoped to the spec staging", specLenses.length > 0);
  t.check("non-spec-loop lenses read both files as one document", nonLenses.length > 0);
  t.check("every lens belongs to exactly one loop", specLenses.length + nonLenses.length === lenses.length, lenses.length + " lenses, " + specLenses.length + "+" + nonLenses.length);
  t.check(
    "spec-loop lenses are told checklist drift is not a finding there",
    specLenses.every((c) => /Drift in them is expected here and is NOT a finding/.test(c.prompt)),
  );
  t.check(
    "test-coverage does not run in the spec loop",
    !specLenses.some((c) => c.label.endsWith(":test-coverage")),
  );
  t.check(
    "but does in the non-spec loop",
    nonLenses.some((c) => c.label.endsWith(":test-coverage")),
  );
}

t.section("B9b. lockSpecChanges governs what the non-spec fixer may edit");
{
  const withFinding = loopStubs({
    "*:review:*": { coverage: "c", findings: [{ title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing" }] },
    "*:dedup": { findings: [{ title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing", lenses: ["mechanism"] }] },
  });
  const unlocked = await runWorkflow(WF, REVIEW_ARGS, withFinding);
  const locked = await runWorkflow(WF, { ...REVIEW_ARGS, lockSpecChanges: true }, withFinding);
  const fixOf = (r, loop) =>
    r.calls.filter((c) => /:fix$/.test(c.label) && c.prompt.includes(loop + " convergence loop"));
  const uf = fixOf(unlocked, "non-spec");
  const lf = fixOf(locked, "non-spec");
  t.check("a non-spec fixer runs in both", uf.length > 0 && lf.length > 0, uf.length + "/" + lf.length);
  t.check("unlocked: the fixer may touch the spec staging", uf.every((c) => /spec-changes\.md — permitted, but PREFER/.test(c.prompt)));
  t.check("locked: it is told the spec staging is LOCKED", lf.every((c) => /spec-changes\.md is LOCKED for this run/.test(c.prompt)));
  t.check("locked: and given the escalation route", lf.every((c) => /recording an open decision/.test(c.prompt)));
  t.check("the run echoes which it was", locked.result.review.lockSpecChanges === true && unlocked.result.review.lockSpecChanges === false);
}

t.section("B10-B12. verification is sequential and short-circuits");
{
  const finding = { title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "citation", introducedBy: "pre-existing" };
  const withOne = (over) => loopStubs({
    "*:review:*": { coverage: "c", findings: [finding] },
    "*:dedup": { findings: [{ ...finding, lenses: ["citations"] }] },
    ...over,
  });

  // B10: materiality refuses -> evidence is never asked.
  {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, withOne({
      "*:verify-material": { confirmed: false, reason: "style only" },
    }));
    t.check("materiality ran", !never(calls, "r1:verify-material"));
    t.check("evidence was NEVER called", never(calls, "r1:verify-evidence"));
    t.check("no fixer ran on a refused finding", never(calls, "r1:fix"));
  }
  // B11: materiality confirms -> evidence runs, and both must confirm.
  {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, withOne({}));
    t.check("both skeptics ran", !never(calls, "r1:verify-material") && !never(calls, "r1:verify-evidence"));
    t.check("materiality ran first", firstIndex(calls, "r1:verify-material") < firstIndex(calls, "r1:verify-evidence"));
    t.check("the finding was fixed", !never(calls, "r1:fix"));
  }
  {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, withOne({
      "*:verify-evidence": { confirmed: false, reason: "the citation is right" },
    }));
    t.check("evidence refusing also blocks the fix", never(calls, "r1:fix"));
  }
  // B12: the order is configurable.
  {
    const { calls } = await runWorkflow(
      WF,
      { ...REVIEW_ARGS, verifyOrder: ["evidence", "material"] },
      withOne({ "*:verify-evidence": { confirmed: false, reason: "bad citation" } }),
    );
    t.check("evidence ran first", !never(calls, "r1:verify-evidence"));
    t.check("materiality was never asked", never(calls, "r1:verify-material"));
  }
  // A dead verifier is not a refusal.
  {
    const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, withOne({ "*:verify-material": null }));
    t.check("a dead first verifier stops the finding", never(calls, "r1:fix"));
    t.check("and the round is marked inconclusive", logs.some((l) => /INCONCLUSIVE/.test(l)));
  }
  // A refused finding records which skeptic refused it.
  {
    const { result } = await runWorkflow(WF, REVIEW_ARGS, withOne({
      "*:verify-material": { confirmed: false, reason: "style only" },
    }));
    t.check("the run reports it as rejected", (result.review.rejectedTitles || []).includes("T"));
  }
}

t.section("B27. convergence is certified only over a COMPLETE sweep");
{
  // A lens that failed in an early round and ran clean later is not a reason to
  // refuse convergence: the sweep re-reads the final text with every lens. This
  // is the case that must still converge.
  const { result } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": "NO",
    "r1:review:security": null,
  }));
  t.check("a lens that failed early and recovered still converges", result.review.converged === true, String(result.review.converged));
}
{
  // A sweep in which a lens FAILED is incomplete and must not certify. A dropped
  // lens contributes zero findings and is indistinguishable from a satisfied
  // one, so counting it would let an outage certify a proposal. The loop's
  // answer is to re-run rather than to accept: a single failed sweep is
  // followed by another, and only a complete one converges.
  const one = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": "NO",
    "r3:review:security": null,
  }));
  t.check("the incomplete sweep is refused", one.logs.some((l) => /sweep found nothing but was incomplete; NOT converging/.test(l)));
  t.check("and another sweep follows it", one.logs.filter((l) => /FULL SWEEP/.test(l)).length >= 2);
  t.check("which then converges", one.result.review.converged === true);

  // A lens that never returns at all can never certify its domain, so the run
  // exhausts its budget rather than converging.
  const never_ = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": "NO",
    "*:review:security": null,
  }));
  t.check("a lens that never returns blocks convergence entirely", never_.result.review.converged === false, String(never_.result.review.converged));
  t.check("every round is marked inconclusive", never_.logs.some((l) => /INCONCLUSIVE/.test(l)));
  t.check("the lens is never retired on its failures", !never_.logs.some((l) => /retiring.*security/.test(l)));
}

t.done();
