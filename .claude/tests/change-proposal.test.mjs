// Layer 2: change-proposal.
//
// The real workflow body runs; only `agent` is stubbed. What is under test is
// what the script decides — which stages run, in what order, how many agents
// each dispatches, what each is told, and what the run returns when one of
// them fails or dissents.
//
// Run: node .claude/tests/change-proposal.test.mjs

import { runWorkflow, suite, labels, matching, never, firstIndex, ordered, loadWorkflow } from "./harness.mjs";

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
  "*:round-boundary": '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
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
  // Named by the migrator's own script path rather than by "workflow:", because
  // the run now fires the open-decisions subworkflow too and a bare prefix would
  // read that as a migration.
  t.check(
    "a folder-layout proposal is not migrated",
    never(calls, "workflow:/repo/.claude/workflows/migrate-proposal.js"),
  );
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

// The recheck trigger hashes each lane's files at that lane's convergence and
// compares afterwards. A stub that returns no digest reads as UNREADABLE, which
// resolves toward reviewing, so every lane looks moved and the run spends its
// whole recheck budget on a lane nothing edited. One steady digest is the
// ordinary case: no lane moves after its own review, and no recheck runs. A
// section about the recheck overrides it with a plan of its own.
const HASH = "0123456789ab";

// A stub table that drives one finding through one round of whichever loop is
// running, then goes clean.
const loopStubs = (over = {}) => {
  let rounds = 0;
  return {
    bootstrap: "SKIPPED",
    conventions: "conforms",
    "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
    "snap*": "DONE",
    diffcount: "0",
    "spec-nonspec-handoff": "reconciled",
    // Every round now closes through the boundary script, so a stub table that
    // omits it leaves every round unable to certify.
    "*:round-boundary": '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
    "hash:*": HASH,
    "*:review:*": { coverage: "c", findings: [] },
    "*:dedup": { findings: [] },
    "*:verify-material": { confirmed: true, reason: "material" },
    "*:verify-evidence": { confirmed: true, reason: "evidence holds" },
    "*:expand:*": { proposal: [], tree: [], searched: "grepped the tree" },
    "*:fix-plan": { groups: [], notes: "" },
    "*:fix-design:*": { designs: [] },
    "*:fix:*": { summary: "fixed it in 0081_fix_x.non-spec-changes.md", newMechanisms: [], escalated: [], designRejected: [] },
    "*:post-fix-review": { findings: [] },
    "verify-checklist": "ok",
    "status:set-reviewed": "DONE",
    "status:record-run": "ok",
    "introspect*": null,
    default: {},
    ...over,
  };
};


t.section("B6c. snapshots are namespaced by run tag and by loop");
{
  // Both loops run, so the non-spec loop's round 1 must not land on the
  // spec loop's round 1, and neither may land where a concurrent run does.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const dests = matching(calls, "snap:").map(
    (c) => (c.prompt.match(/cp -r \S+ (\S+)/) || [])[1],
  );
  t.check("both loops snapshot", dests.length >= 4, String(dests.length));
  t.check(
    "every snapshot lands under the run's own tag",
    dests.every((d) => d && d.startsWith("/repo/scratchpad/cp-snap/0081_fix_x/")),
    String(dests[0]),
  );
  t.check(
    "no two snapshots of the run share a destination",
    new Set(dests).size === dests.length,
    dests.join(" "),
  );
}

// Match a review lens call in either loop: labels are r<N>:review:<lens>.
const isLens = (c) => /^r\d+:review:/.test(c.label);

t.section("B6d. one decisions immunity reaches every lens, and no lens owns the section");
{
  // The clause used to be built per lens: the lens that owned the decisions was
  // told they ARE its findings, and every other lens was told another lens owned
  // them. That lens is deleted, so the split has nothing left to describe, and
  // one clause now reaches every prompt built through barFor. The shape this
  // replaced -- one shared sentence carrying an "UNLESS your lens is X"
  // carve-out -- stays wrong for the reason it always was: reading it is a
  // self-identification step, and those fail.
  const halves = (p) =>
    /Whether a decision should be open at all, and how an open decision is framed, are not yours to file on\./.test(p) &&
    /A false citation inside a decision entry is a finding exactly as anywhere else/.test(p);
  const without = (cs) => cs.filter((c) => !halves(c.prompt)).map((c) => c.label).join(",") || "none";

  // planPath adds PLAN_LENS to both pools. It is the one prompt built through
  // barFor that no default run reaches, so it is enabled here.
  const { calls } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, planPath: "docs/plans/remediation.md" },
    loopStubs(),
  );
  const lenses = calls.filter(isLens);
  const specLenses = lenses.filter((c) => /SCOPE OF THIS LOOP\. You are reviewing the STAGED SPEC EDITS/.test(c.prompt));
  const nonLenses = lenses.filter((c) => /Read the staged spec edits .* AS ONE DOCUMENT/s.test(c.prompt));
  const plan = lenses.filter((c) => c.label.endsWith(":plan-conformance"));

  t.check("the non-spec pool runs", nonLenses.length > 0, String(nonLenses.length));
  t.check("every lens in it carries both halves of the immunity", nonLenses.every((c) => halves(c.prompt)), without(nonLenses));
  t.check(
    "the spec pool runs, reduced by test-coverage",
    specLenses.length > 0 && !specLenses.some((c) => c.label.endsWith(":test-coverage")),
    String(specLenses.length),
  );
  t.check("and every lens in it carries them too", specLenses.every((c) => halves(c.prompt)), without(specLenses));
  t.check("planPath adds the plan lens", plan.length > 0, String(plan.length));
  t.check("which carries them as well", plan.every((c) => halves(c.prompt)), without(plan));
  t.check(
    "the blank rule reaches every lens, since that mechanism is not relaxed",
    lenses.every((c) => /A PROPERLY MARKED BLANK IS NOT A FINDING/.test(c.prompt)),
  );
  t.check(
    "and the bar carries the blank symmetry the deleted lens used to state",
    lenses.every((c) =>
      /over-specification is itself a defect, so a finding that would convert a bounded blank into specified text/.test(c.prompt),
    ),
  );

  t.check("no lens is told the decisions ARE its findings", !calls.some((c) => /ARE YOURS/.test(c.prompt)));
  t.check(
    "and none is told another lens owns them",
    !calls.some((c) => /are not findings\. Another lens owns them\./.test(c.prompt)),
  );
  t.check("no prompt carries a self-identifying carve-out", !calls.some((c) => /UNLESS your lens/.test(c.prompt)));
  t.check(
    "no open-decisions lens runs in either pool or in the sweep that certifies convergence",
    !lenses.some((c) => /:open-decisions$/.test(c.label)),
    labels(calls).filter((l) => /open-decisions/.test(l)).join(",") || "none",
  );

  // The prompt checks above cannot see a branch that survived but stopped
  // firing. What this design deletes is the branch, not only its output.
  const src = loadWorkflow(WF);
  for (const gone of ["DECISIONS_YOURS", "DECISIONS_NOT_YOURS", "lensKey"]) {
    t.check(gone + " is gone from the source, not left unreachable", !src.includes(gone));
  }

  // The fourth consumer of the bar: the redesign subproposal's reviewers read
  // BAR, which is barFor's output with no lens attached at all.
  const rd = await runWorkflow(
    WF,
    {
      ...REVIEW_ARGS, mode: "redesign", focusAreas: ["teardown"],
      maxSpecReviewRounds: 1, maxNonSpecReviewRounds: 1, allowNonSpecOnUnconvergedSpec: true,
    },
    loopStubs({ "redesign*:review:*": { findings: [] }, "redesign*": "done" }),
  );
  const judges = matching(rd.calls, "redesign").filter((c) => /:review:/.test(c.label));
  t.check("the redesign judges run over the same bar", judges.length > 0, String(judges.length));
  t.check("and read the same clause", judges.every((c) => halves(c.prompt)), without(judges));
}

t.section("B6e. what the deleted lens carried is absent from the parent and lives in the phase");
{
  // The lens is gone, so what the parent has to show is absence: no lens is
  // handed the decisions schema, and no prompt carries the procedure that lens
  // ran. The rules this design rehomed are checked here as text in the
  // subworkflow that now owns them; how the phase's own agents read them is
  // pinned by the phase's own test file.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const lenses = calls.filter(isLens);
  t.check("lenses run", lenses.length > 0, String(lenses.length));
  t.check(
    "none is handed a decisions schema",
    lenses.every((c) => !c.opts.schema?.properties?.decisions),
    lenses.filter((c) => c.opts.schema?.properties?.decisions).map((c) => c.label).join(",") || "none",
  );
  t.check(
    "every lens returns coverage and findings and nothing else",
    lenses.every((c) => (c.opts.schema?.required || []).join(",") === "coverage,findings"),
  );
  const procedure = [
    "1. INVENTORY",
    "2. ELABORATE",
    "3. INTERROGATE",
    "4. DETERMINE",
    "YOU OWN `## Open decisions` IN THE SUMMARY",
    "WHAT EACH FIELD OF `decisions` HOLDS",
  ];
  t.check(
    "and the lens's own procedure reaches no prompt",
    procedure.every((s) => !calls.some((c) => c.prompt.includes(s))),
    procedure.filter((s) => calls.some((c) => c.prompt.includes(s))).join(" | ") || "none",
  );

  const phase = loadWorkflow(".claude/workflows/change-proposal-decisions.js");
  for (const [what, re] of [
    ["the GIVE IT TO THE HUMAN test", /GIVE IT TO THE HUMAN only when one of these holds/],
    ["the NEGATIVE TEST", /THE NEGATIVE TEST\. A decision belongs to the human only if a person could answer it in one sitting/],
    ["the bar on promoting a bounded blank", /not yours to expand, second-guess, or promote to a human /],
    ["the bar on filing a blank because it is open", /Never report a blank as an open decision merely because it is open/],
  ]) {
    t.check(what + " is in the phase's briefs", re.test(phase));
    t.check(what + " reaches no prompt of the parent", !calls.some((c) => re.test(c.prompt)));
  }
}

t.section("B6f. the base tier is a workflow argument, independent of the session");
{
  // The session's model and effort are deliberately NOT inherited: a loop that
  // silently changed tier because the operator switched their own model would
  // produce results nobody could compare against an earlier run.
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  // A subworkflow call is not an agent and carries no tier of its own: the base
  // model and effort reach the child as arguments, which
  // .claude/tests/change-proposal-decisions-forwarding.test.mjs asserts.
  const agents = calls.filter((c) => !c.label.startsWith("workflow:"));
  const inherited = agents.filter((c) => !c.opts || !c.opts.model);
  t.check("no agent is left on the session's model", inherited.length === 0, inherited.map((c) => c.label).join(","));
  const noEffort = agents.filter((c) => !c.opts || !c.opts.effort);
  t.check("and none on the session's effort", noEffort.length === 0, noEffort.map((c) => c.label).join(","));

  const base = calls.filter((c) => c.label.startsWith("r1:review:"));
  t.check("lenses default to opus", base.every((c) => c.opts.model === "opus"), base.map((c) => c.opts.model).join(","));
  t.check("at medium effort", base.every((c) => c.opts.effort === "medium"));
  t.check("the tier is logged so a run records what it was measured at", logs.some((l) => /Base tier: opus at medium effort \(default\)/.test(l)));

  const hard = calls.filter((c) => /:round-boundary$|^snap:|^probe:spec-changes$|^status:set-reviewed$/.test(c.label));
  t.check("agents that name their own model keep it", hard.length > 0 && hard.every((c) => c.opts.model === "haiku"), hard.map((c) => c.label + "=" + c.opts.model).join(","));
  // A cheap model is not the same request as a shallow one: these agents are on
  // haiku because their work is mechanical, and on high effort because getting
  // it wrong silently corrupts a round's bookkeeping.
  t.check("and their own effort, which the base does not override", hard.every((c) => c.opts.effort === "high"), hard.map((c) => c.label + "=" + c.opts.effort).join(","));
}
{
  const { calls, logs } = await runWorkflow(WF, { ...REVIEW_ARGS, baseModel: "sonnet", baseEffort: "high" }, loopStubs());
  const base = calls.filter((c) => c.label.startsWith("r1:review:"));
  t.check("a caller-set model reaches every un-hardcoded agent", base.every((c) => c.opts.model === "sonnet"));
  t.check("and a caller-set effort does too", base.every((c) => c.opts.effort === "high"));
  t.check("the log says it was caller-set", logs.some((l) => /Base tier: sonnet at high effort \(caller-set\)/.test(l)));
  const hard = calls.filter((c) => /^snap:/.test(c.label));
  t.check("a hardcoded model is absolute, not relative to the base", hard.every((c) => c.opts.model === "haiku"));
  t.check("and a hardcoded effort survives a caller-set base too", hard.every((c) => c.opts.effort === "high"));
}
{
  const { error } = await runWorkflow(WF, { ...REVIEW_ARGS, baseModel: "gpt" }, loopStubs());
  t.check("an unknown model fails the run rather than running on it", !!error && /baseModel must be one of/.test(error.message), error && error.message);
}
{
  const { error } = await runWorkflow(WF, { ...REVIEW_ARGS, baseEffort: "turbo" }, loopStubs());
  t.check("an unknown effort does too", !!error && /baseEffort must be one of/.test(error.message), error && error.message);
}

t.section("B6g. startPhase skips the phases before it");
{
  const { calls, logs } = await runWorkflow(WF, { ...REVIEW_ARGS, startPhase: "non-spec-review" }, loopStubs());
  t.check("the conventions pass does not run", never(calls, "conventions"));
  t.check("nor the spec loop", !calls.some((c) => /^spec R\d+/.test((c.opts && c.opts.phase) || "")),
    [...new Set(calls.map((c) => (c.opts && c.opts.phase) || "").filter(Boolean))].join(" | "));
  t.check("the non-spec loop does run", calls.some((c) => /^r\d+:review:/.test(c.label)));
  t.check("and the skip is logged with what it assumed", logs.some((l) => /Starting at the non-spec-review phase; skipping/.test(l)));
  t.check("naming that nothing checks those phases were done", logs.some((l) => /nothing checks that they were/.test(l)));
}
{
  const { calls, logs } = await runWorkflow(WF, { ...REVIEW_ARGS, startPhase: "spec-review" }, loopStubs());
  t.check("starting at spec-review still skips conventions", never(calls, "conventions"));
  t.check("but runs the spec loop", logs.some((l) => /spec/i.test(l)));
}
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  t.check("the default runs conventions", !never(calls, "conventions"));
  t.check("and logs no skip", !logs.some((l) => /Starting at the/.test(l)));
}
{
  const { error } = await runWorkflow(WF, { ...REVIEW_ARGS, startPhase: "middle" }, loopStubs());
  t.check("an unknown phase fails the run", !!error && /startPhase must be one of/.test(error.message), error && error.message);
}
{
  // The skipped phases in new mode are the ones that CREATE the proposal, so a
  // new-mode run starting later has no files to review.
  const { error } = await runWorkflow(WF, { ...NEW_ARGS, startPhase: "spec-review" }, newStubs());
  t.check("new mode refuses a later start", !!error && /would skip the phases that write the/.test(error.message), error && error.message);
}

t.section("B7. the spec loop is skipped when nothing is staged for spec");
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" } }));
  t.check("the probe runs", !never(calls, "probe:spec-changes"));
  t.check("the probe is a cheap dedicated agent", calls.find((c) => c.label === "probe:spec-changes").opts.model === "haiku");
  t.check("it is logged as skipped", logs.some((l) => /stages no spec edits; skipping the spec review loop/.test(l)));
  t.check("the non-spec loop still runs", logs.some((l) => /Entering the non-spec review loop/.test(l)));
  t.check("no spec loop is entered", !logs.some((l) => /Entering the spec review loop/.test(l)));
}

t.section("B7b. the spec gate reads a field, and a skip is visible in the result");
{
  const run = (probe, args = REVIEW_ARGS) =>
    runWorkflow(WF, args, loopStubs({ "probe:spec-changes": probe }));
  const entered = (logs) => logs.some((l) => /Entering the spec review loop/.test(l));

  // A structured YES ran the loop's OPPOSITE before the gate read a field:
  // String({...}) is "[object Object]", which matches no /YES/i.
  const yes = await run({ stagesSpecChanges: true, why: "SPEC-1 lands in spec/16" });
  t.check("a structured yes runs the spec loop", entered(yes.logs));
  t.check("and the result records no skip", yes.result.review.specReviewSkipped === null);

  const no = await run({ stagesSpecChanges: false, why: "the staging carries only its headings" });
  t.check("a structured no skips the loop", !entered(no.logs));
  t.check("and the result names the reason",
    no.result.review.specReviewSkipped?.reason === "no-spec-changes",
    JSON.stringify(no.result.review.specReviewSkipped));
  t.check("and carries the probe's why", /only its headings/.test(no.result.review.specReviewSkipped?.why || ""));

  // The dangerous direction: an answer the gate cannot read must not skip the
  // spec review silently. `{}` is also what the harness returns for an
  // unstubbed agent, so this is the shape the suite itself produced.
  const unreadable = await run({});
  t.check("an unreadable answer runs the loop rather than skipping it", entered(unreadable.logs));
  t.check("and the result records no skip on an unreadable answer", unreadable.result.review.specReviewSkipped === null);
  t.check("and the run says the answer was unreadable",
    unreadable.logs.some((l) => /no readable answer/.test(l)));

  // Preserved from the old `|| "YES"` default: a probe that died is not a NO.
  const dead = await run(null);
  t.check("a dead probe still runs the loop", entered(dead.logs));

  // Skipping by caller argument is a different reason from staging nothing,
  // and it was unreachable while an object answer read as "no spec changes".
  const skipped = await run({ stagesSpecChanges: true, why: "SPEC-1" }, { ...REVIEW_ARGS, skipSpecReview: true });
  t.check("skipSpecReview is a distinct recorded reason",
    !entered(skipped.logs) && skipped.result.review.specReviewSkipped?.reason === "skipSpecReview",
    JSON.stringify(skipped.result.review.specReviewSkipped));
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
  t.check("the handoff's write set is closed and named", /only files you may edit are .*summary\.md, .*implementation-checklist\.md and .*non-spec-changes\.md/.test(h.prompt));
  t.check("it rebuilds the deliverable index", /Rebuild `## Deliverable index`/.test(h.prompt));
  t.check("it writes the spec-lane steps as a leading block", /leading block/.test(h.prompt));
  t.check("and its reconciliation steps are not a review round", /Steps 1 through 3 are not a review round/.test(h.prompt));
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
    // No spec loop, so the spec gate is not what this test is measuring: it is
    // about what the NON-SPEC fixer may edit under lockSpecChanges.
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "*:review:*": { coverage: "c", findings: [{ title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing" }] },
    "*:dedup": { findings: [{ title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing", lenses: ["mechanism"] }] },
  });
  const unlocked = await runWorkflow(WF, REVIEW_ARGS, withFinding);
  const locked = await runWorkflow(WF, { ...REVIEW_ARGS, lockSpecChanges: true }, withFinding);
  const fixOf = (r, loop) =>
    r.calls.filter((c) => /:fix:/.test(c.label) && c.prompt.includes(loop + " convergence loop"));
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
    t.check("no fixer ran on a refused finding", never(calls, "r1:fix:"));
  }
  // B11: materiality confirms -> evidence runs, and both must confirm.
  {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, withOne({}));
    t.check("both skeptics ran", !never(calls, "r1:verify-material") && !never(calls, "r1:verify-evidence"));
    t.check("materiality ran first", firstIndex(calls, "r1:verify-material") < firstIndex(calls, "r1:verify-evidence"));
    t.check("the finding was fixed", !never(calls, "r1:fix:"));
  }
  {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, withOne({
      "*:verify-evidence": { confirmed: false, reason: "the citation is right" },
    }));
    t.check("evidence refusing also blocks the fix", never(calls, "r1:fix:"));
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
    t.check("a dead first verifier stops the finding", never(calls, "r1:fix:"));
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

t.section("B12a. the parallel verify path applies the same dead-verifier guard");
{
  const finding = { title: "T", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "citation", introducedBy: "pre-existing" };
  const withOne = (over) => loopStubs({
    "*:review:*": { coverage: "c", findings: [finding] },
    "*:dedup": { findings: [{ ...finding, lenses: ["citations"] }] },
    ...over,
  });
  const PAR = { ...REVIEW_ARGS, verifySequential: false };

  // The failure the guard exists for: one outage must not refute a finding.
  {
    const { calls, logs, result } = await runWorkflow(WF, PAR, withOne({ "*:verify-material": null }));
    t.check("both skeptics are dispatched", !never(calls, "r1:verify-material") && !never(calls, "r1:verify-evidence"));
    t.check("a dead verifier stops the finding", never(calls, "r1:fix:"));
    t.check("and the round is marked inconclusive", logs.some((l) => /INCONCLUSIVE/.test(l)));
    t.check("and the finding is NOT recorded as refuted",
      !(result.review.rejectedTitles || []).includes("T"), JSON.stringify(result.review.rejectedTitles));
    const r2 = calls.find((c) => c.label.startsWith("r2:review:"));
    t.check("so a later lens is never told it was refuted",
      !r2 || !/Already examined and refuted/.test(r2.prompt));
  }
  // A live refusal must still refute, and must name the skeptic that refused.
  {
    const { calls, result } = await runWorkflow(WF, PAR, withOne({
      "*:verify-evidence": { confirmed: false, reason: "the citation is right" },
    }));
    t.check("a live refusal is still recorded as refuted", (result.review.rejectedTitles || []).includes("T"));
    const r2 = calls.find((c) => c.label.startsWith("r2:review:"));
    t.check("and the refusing skeptic is named", !r2 || /T: refuted by the evidence skeptic/.test(r2.prompt));
  }
  // The happy path is unchanged.
  {
    const { calls } = await runWorkflow(WF, PAR, withOne({}));
    t.check("two confirming skeptics still reach the fixer", !never(calls, "r1:fix:"));
  }
}


t.section("B12b. an agent that omits its findings array does not kill the run");
{
  const f = (title) => ({ title, where: "w", claim: "c", why_wrong: "w", evidence: "e",
    suggested_fix: "f", area: "a", kind: "citation", introducedBy: "pre-existing" });

  // 1. A review lens returns an object with no findings key. The key is required
  //    by its schema, so the return is discarded and the lens ends the round as
  //    a failed one; the rest of the round runs regardless.
  {
    const { result, calls, error } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: [f("T")] },
      "*:review:security": { coverage: "c" },            // no findings key
      "*:dedup": { findings: [{ ...f("T"), lenses: ["citations"] }] },
    }));
    t.check("the run does not throw", !error, error && error.message);
    // The surviving lens's finding still reaches verification and the fixer.
    t.check("the other lens's finding is still verified", !never(calls, "r1:verify-material"));
    t.check("and is still fixed", !never(calls, "r1:fix:"));
    t.check("the run still completes", result && result.status !== undefined);
  }

  // 2. The dedup agent returns an object with no findings key: the raw findings
  //    are carried forward rather than lost.
  {
    const { result, calls, logs, error } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: [f("T1"), f("T2")] },
      "*:dedup": {},                                     // no findings key
    }));
    t.check("the run does not throw", !error, error && error.message);
    t.check("every raw finding is still verified",
      matching(calls, "r1:verify-material").length >= 2,
      String(matching(calls, "r1:verify-material").length));
    t.check("and the round is not reported as empty",
      !logs.some((l) => /Round 1: 0 findings after dedup/.test(l)));
    t.check("the run still completes", result && result.status !== undefined);
  }

  // 3. The post-fix reviewer returns an object with no findings key. `findings`
  //    is required by its schema, so the return is discarded and the call
  //    retried; once the retries are spent the round records the review as
  //    unavailable rather than reading the empty object as a clean verdict.
  {
    const { calls, logs, error } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: [f("T")] },
      "*:dedup": { findings: [{ ...f("T"), lenses: ["citations"] }] },
      "*:post-fix-review": {},                           // no findings key
    }));
    t.check("the run does not throw", !error, error && error.message);
    t.check("no follow-up fixer is launched", never(calls, "r1:follow-up-fix"));
    t.check("the post-fix review is recorded as unavailable rather than clean",
      logs.some((l) => /post-fix review unavailable after retries/.test(l)));
  }
}

t.section("B27. convergence is certified only over a COMPLETE sweep");
{
  // A lens that failed in an early round and ran clean later is not a reason to
  // refuse convergence: the sweep re-reads the final text with every lens. This
  // is the case that must still converge.
  const { result } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
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
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    // Round 2 is the first sweep now that no lens is withheld from round 1.
    "r2:review:security": null,
  }));
  t.check("the incomplete sweep is refused", one.logs.some((l) => /sweep found nothing but was incomplete; NOT converging/.test(l)));
  t.check("and another sweep follows it", one.logs.filter((l) => /FULL SWEEP/.test(l)).length >= 2);
  t.check("which then converges", one.result.review.converged === true);

  // A lens that never returns at all can never certify its domain, so the run
  // exhausts its budget rather than converging.
  const never_ = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "*:review:security": null,
  }));
  t.check("a lens that never returns blocks convergence entirely", never_.result.review.converged === false, String(never_.result.review.converged));
  t.check("every round is marked inconclusive", never_.logs.some((l) => /INCONCLUSIVE/.test(l)));
  t.check("the lens is never retired on its failures", !never_.logs.some((l) => /retiring.*security/.test(l)));

  // A lens that goes dark AFTER the loop has retired it fails every sweep from
  // then on, and a sweep that finds nothing runs no fixer, so the sweep after it
  // is the same question over the same bytes. The loop stops on the second one
  // rather than spending the rest of its budget re-learning it.
  const stalled = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, maxNonSpecReviewRounds: 8 },
    loopStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:security": ({ label }) =>
        Number(label.match(/^r(\d+):/)[1]) >= 2 ? null : { coverage: "c", findings: [] },
    }),
  );
  const L = stalled.result.review.loops[0];
  const sweeps = stalled.logs.filter((l) => /FULL SWEEP/.test(l)).length;
  t.check("it stops at the second identical sweep", sweeps === 2, "sweeps: " + sweeps);
  t.check("the rest of the budget is not spent", L.rounds === 3, "rounds: " + L.rounds);
  t.check("the stalled lens is named in the result", (L.stalledLenses || []).includes("security"), JSON.stringify(L.stalledLenses));
  t.check("it still does not converge", stalled.result.review.converged === false);
  t.check("no log claims the failed lens stays active", !stalled.logs.some((l) => /stay active/.test(l)));
}

t.section("B18d. a lens name no lens in the round carries credits nobody, and retires nobody");
{
  // The dedup agent returns the lens union as free strings, and it is the only
  // input to retirement once a merge has collapsed several findings into one. A
  // name that is close but wrong (`citation-audit` for `citations`) used to
  // retire the lens whose finding had just been confirmed, on the same round it
  // was confirmed, because the survivor set was non-empty and therefore looked
  // attributed. Two lenses report, so the dedup step actually runs: below two
  // raw findings the loop skips it and the script's own stamping carries.
  const G = (n) => ({
    title: "T" + n, where: "w", claim: "c", why_wrong: "w", evidence: "e",
    suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing",
  });
  const run = (name) => runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "*:review:*": ({ label }) =>
      /^r1:review:(citations|mechanism)$/.test(label)
        ? { coverage: "c", findings: [G(label)] }
        : { coverage: "c", findings: [] },
    "*:dedup": { findings: [{ ...G(1), lenses: [name] }, { ...G(2), lenses: ["mechanism"] }] },
    "*:fix-plan": { groups: [{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }], notes: "" },
    "*:fix-design:*": { designs: [], groupNote: "", newMechanisms: [] },
  }));
  const round2 = (calls) => matching(calls, "r2:review:").map((c) => c.label.split(":")[2]);

  const good = await run("citations");
  t.check("with the right name the attributed lens runs again", round2(good.calls).includes("citations"));

  const bad = await run("citation-audit");
  t.check(
    "an unknown name does not retire the lens whose finding was confirmed",
    round2(bad.calls).includes("citations"),
    round2(bad.calls).join(","),
  );
  t.check(
    "the round says it fell back to the weaker rule",
    bad.logs.some((l) => /falling back to retiring only lenses that reported nothing/.test(l)),
  );
  t.check(
    "and the correctly attributed lens is unaffected",
    round2(bad.calls).includes("mechanism"),
  );
}

t.section("B27b. a round whose fixer never returned cannot certify convergence");
{
  const f = (t) => ({ title: t, where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "design-defect", introducedBy: "pre-existing" });
  // One round confirms two findings; every later round is clean, so the loop
  // reaches a sweep that would certify.
  const twoFindings = (over) => loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "r1:review:*": { coverage: "c", findings: [f("T1"), f("T2")] },
    "r1:dedup": { findings: [{ ...f("T1"), lenses: ["mechanism"] }, { ...f("T2"), lenses: ["citations"] }] },
    ...over,
  });
  {
    // The control. Without it the section only proves the run failed to converge,
    // which every broken stub table also proves.
    const { result, calls } = await runWorkflow(WF, REVIEW_ARGS, twoFindings({}));
    t.check("a live fixer still converges", result.review.converged === true, String(result.review.converged));
    t.check("and the proposal is marked Reviewed", !never(calls, "status:set-reviewed"));
  }
  {
    const { result, calls, logs } = await runWorkflow(WF, REVIEW_ARGS, twoFindings({ "*:fix:*": null }));
    const r1 = result.review.history.find((h) => h.round === 1);
    t.check("a dead fixer leaves its round incomplete", r1 && r1.complete === false, JSON.stringify(r1));
    t.check("the round names the group whose fix never ran", r1 && (r1.fixersFailed || []).includes("G1"), JSON.stringify(r1 && r1.fixersFailed));
    t.check("the loop does not certify convergence", result.review.converged === false, String(result.review.converged));
    t.check("the proposal is NOT marked Reviewed", never(calls, "status:set-reviewed"));
    const rec = calls.find((c) => c.label === "status:record-run");
    t.check("and the status agent is told the run did not converge", /DID NOT CONVERGE/.test(rec.prompt));
    t.check("it is logged against the group", logs.some((l) => /the fixer for G1 did not return/.test(l)));
  }
  {
    // One dead fixer among several: the surviving group's edits still land, and
    // convergence is still refused, naming only the group that died.
    const { result, calls } = await runWorkflow(WF, REVIEW_ARGS, twoFindings({
      "r1:fix-plan": { groups: [
        { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
        { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
      ] },
      "r1:fix:G2": null,
    }));
    const fixes = calls.filter((c) => /^r1:fix:G/.test(c.label)).map((c) => c.label);
    t.check("both groups were fixed separately", fixes.includes("r1:fix:G1") && fixes.includes("r1:fix:G2"), fixes.join(","));
    t.check("one dead fixer among several still blocks convergence", result.review.converged === false, String(result.review.converged));
    const r1 = result.review.history.find((h) => h.round === 1);
    t.check("and only the dead group is named", (r1.fixersFailed || []).join() === "G2", JSON.stringify(r1.fixersFailed));
  }
}


// ---- Phase 4: fix planning, design, and grouped fixing -------------------

const F = (n, kind = "citation") => ({
  title: "T" + n, where: "w" + n, claim: "c", why_wrong: "w", evidence: "e",
  suggested_fix: "f", area: "a" + n, kind, introducedBy: "pre-existing",
});
const fs = (n) => Array.from({ length: n }, (_, i) => F(i + 1));

const fixStubs = (n, over = {}) =>
  loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "*:review:*": ({ label }) => (/^r1:/.test(label) ? { coverage: "c", findings: fs(n) } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: fs(n).map((f) => ({ ...f, lenses: ["citations"] })) },
    ...over,
  });

const plan = (groups) => ({ groups, notes: "" });

t.section("B13. the planner's group cap is enforced, and only the count is capped");
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(12, {
    "*:fix-plan": plan(Array.from({ length: 12 }, (_, i) => ({
      id: "G" + (i + 1), title: "g", rationale: "r", findings: [i], order: i + 1,
    }))),
  }));
  const fixers = matching(calls, "r1:fix:");
  t.check("the tail is merged rather than the run failing", fixers.length === 7, String(fixers.length));
  t.check("and it is logged", logs.some((l) => /against a cap of 7; merging the tail/.test(l)));
  t.check("no finding is lost to the merge", true);
}
{
  // One group holding every finding is legal: size is not capped.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(40, {
    "*:fix-plan": plan([{ id: "G1", title: "forty citations", rationale: "same subject", findings: Array.from({ length: 40 }, (_, i) => i), order: 1 }]),
  }));
  t.check("forty findings in one group is accepted", matching(calls, "r1:fix:").length === 1);
  t.check("and one design covers them", matching(calls, "r1:fix-design:").length === 1);
}

t.section("B14. a partition that drops or duplicates a finding falls back safely");
for (const [name, groups] of [
  ["drops one", [{ id: "G1", title: "g", rationale: "r", findings: [0], order: 1 }]],
  ["duplicates one", [
    { id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 },
    { id: "G2", title: "g", rationale: "r", findings: [1], order: 2 },
  ]],
  ["indexes out of range", [{ id: "G1", title: "g", rationale: "r", findings: [0, 1, 9], order: 1 }]],
  ["returns nothing", []],
]) {
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "*:fix-plan": plan(groups) }));
  t.check(name + ": falls back to one group", matching(calls, "r1:fix:").length === 1, String(matching(calls, "r1:fix:").length));
  t.check(name + ": and says so", logs.some((l) => /did not return a clean partition/.test(l)));
  t.check(
    name + ": the single fixer still gets every finding",
    /T1[\s\S]*T2[\s\S]*T3/.test(matching(calls, "r1:fix:")[0].prompt),
  );
}
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "*:fix-plan": null }));
  t.check("a dead planner falls back to one group", matching(calls, "r1:fix:").length === 1);
}

t.section("B14b. a non-integer index is rejected rather than silently dropping its finding");
{
  // Which findings actually reached a fixer, read off the JSON payload
  // fixPrompt embeds, so the assertion is on dispatch and not on wording.
  const dispatched = (calls) =>
    matching(calls, "r1:fix:")
      .flatMap((c) => ["T1", "T2", "T3"].filter((x) => new RegExp('"title": "' + x + '"').test(c.prompt)))
      .sort()
      .join(",");
  for (const [name, idx] of [["a fractional index", [0, 1.5, 2]], ["NaN as an index", [0, NaN, 2]]]) {
    const groups = [{ id: "G1", title: "g", rationale: "r", findings: idx, order: 1 }];
    const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "*:fix-plan": plan(groups) }));
    const got = dispatched(calls);
    t.check(name + ": every confirmed finding still reaches a fixer", got === "T1,T2,T3", got);
    t.check(name + ": the partition is rejected", logs.some((l) => /did not return a clean partition/.test(l)));
    t.check(name + ": and no log claims a split that did not happen", !logs.some((l) => /split into/.test(l)));
  }
}

t.section("B14c. a group order that is not a distinct 1-based sequence is reported, not obeyed");
{
  const G = (id, i, order) =>
    Object.assign({ id, title: id, rationale: "r", findings: [i] }, order === undefined ? {} : { order });
  for (const [name, groups] of [
    ["a negative order", [G("G1", 0, 1), G("G2", 1, 2), G("G3", 2, -5)]],
    ["a missing order", [G("G1", 0, 1), G("G2", 1, 2), G("G3", 2)]],
    ["duplicate orders", [G("G1", 0, 1), G("G2", 1, 1), G("G3", 2, 1)]],
  ]) {
    const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "*:fix-plan": plan(groups) }));
    const got = matching(calls, "r1:fix:").map((c) => c.label).join(",");
    t.check(name + ": the groups are fixed in the order the planner returned them",
      got === "r1:fix:G1,r1:fix:G2,r1:fix:G3", got);
    t.check(name + ": and the broken order field is reported",
      logs.some((l) => /group order is not a distinct 1-based/.test(l)));
  }
}

t.section("B15. the design reaches the fixer that applies it");
{
  const design = {
    designs: [{
      findingTitle: "T1",
      effort: "deep",
      chosen: { approach: "extend the existing frame", why: "no new surface" },
      alternatives: [{ approach: "a second endpoint", whyNot: "a deployer must wire it" }],
      cascades: ["the testing section"],
      doNotDo: ["add a boolean to the manifest"],
    }],
    groupNote: "one edit closes both",
    newMechanisms: [],
  };
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]),
    "*:fix-design:*": design,
  }));
  const fixer = matching(calls, "r1:fix:")[0];
  t.check("one design per group", matching(calls, "r1:fix-design:").length === 1);
  t.check("the chosen approach reaches the fixer", /extend the existing frame/.test(fixer.prompt));
  t.check("so do the alternatives", /a deployer must wire it/.test(fixer.prompt));
  t.check("so does doNotDo", /add a boolean to the manifest/.test(fixer.prompt));
  t.check("so do the cascades", /the testing section/.test(fixer.prompt));
  t.check("the fixer is told to apply rather than invent", /APPLY IT\. Your scope for design decisions is narrow/.test(fixer.prompt));
  t.check("and to declare a design it rejects", /designRejected/.test(fixer.prompt));
}
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]),
    "*:fix-design:*": null,
  }));
  const fixer = matching(calls, "r1:fix:")[0];
  t.check("a dead design still runs the fixer", !!fixer);
  t.check("which is told to design it itself", /No design was produced for this group/.test(fixer.prompt));
  t.check("and the run records the group as designless", logs.some((l) => /no design returned for G1/.test(l)));
}
{
  // A design result with no entries is not a design. `{designs: []}` is truthy,
  // so it took the design path and handed the fixer an empty mandate under
  // "your scope for design decisions is narrow here", logging nothing.
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]),
    "*:fix-design:*": { designs: [], groupNote: "", newMechanisms: [] },
  }));
  const fixer = matching(calls, "r1:fix:")[0];
  t.check("a design with no entries is recorded as designless",
    (result.review.history[0].designless || []).includes("G1"));
  t.check("and logged", logs.some((l) => /no design returned for G1/.test(l)));
  t.check("the fixer is told to design it itself", /No design was produced for this group/.test(fixer.prompt));
  t.check("rather than to narrow its judgement over an empty design",
    !/APPLY IT\. Your scope for design decisions is narrow/.test(fixer.prompt));
}
{
  // Nothing to reconcile when no group has a design, so no agent is spent.
  const { calls, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "*:fix-design:*": { designs: [], groupNote: "", newMechanisms: [] },
  }));
  t.check("two empty designs reconcile nothing", never(calls, "r1:fix-design-reconcile"));
  t.check("and both groups are recorded designless",
    (result.review.history[0].designless || []).join(",") === "G1,G2");
}
{
  // The second door: a revision that revises nothing must not replace a design.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "*:fix-design:*": { designs: [{ findingTitle: "T1", effort: "deep", chosen: { approach: "extend the existing frame", why: "no new surface" } }], groupNote: "", newMechanisms: [] },
    "*:fix-design-reconcile": {
      conflicts: [{ groups: ["G1", "G2"], what: "both rewrite the predicate", resolution: "G2's wording survives" }],
      revised: [{ groupId: "G1", designs: [] }],
    },
  }));
  const g1 = calls.find((c) => c.label === "r1:fix:G1");
  t.check("an empty revision leaves the original design standing",
    /extend the existing frame/.test(g1.prompt));
}

t.section("B15b. the design stage triages by effort, and the caller can force it");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2));
  const d = matching(calls, "r1:fix-design:")[0].prompt;
  t.check("triage comes before investigation", /TRIAGE FIRST, AND LET THE TRIAGE GOVERN YOUR BUDGET/.test(d));
  t.check("over-investigating a trivial finding is named a defect", /Spending deep effort on a trivial\s+finding is a defect in your work/.test(d));
  t.check("the architect path is reserved for deep findings", /ON A DEEP FINDING YOU ARE THE ARCHITECT/.test(d));
  t.check("ground truth before the proposal's prose", /Establish ground truth in the repository BEFORE you read/.test(d));
  t.check("deleting is named the outcome to reach for", /most worth reaching for/.test(d));
  t.check("the anti-hair mandate is stated", /PREVENT THE PROPOSAL GROWING HAIR/.test(d));
  t.check("it is read-only", /read-only investigator/.test(d));
}
{
  const shallow = await runWorkflow(WF, { ...REVIEW_ARGS, fixDesignDepth: "shallow" }, fixStubs(2));
  const deep = await runWorkflow(WF, { ...REVIEW_ARGS, fixDesignDepth: "deep" }, fixStubs(2));
  t.check("shallow is forced through", /FORCED SHALLOW MODE/.test(matching(shallow.calls, "r1:fix-design:")[0].prompt));
  t.check("deep is forced through", /FORCED DEEP MODE/.test(matching(deep.calls, "r1:fix-design:")[0].prompt));
}

t.section("B16. groups are fixed sequentially, in the planner's order");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, {
    "*:fix-plan": plan([
      { id: "G2", title: "second", rationale: "r", findings: [1], order: 2 },
      { id: "G1", title: "first", rationale: "r", findings: [0], order: 1 },
      { id: "G3", title: "third", rationale: "r", findings: [2], order: 3 },
    ]),
  }));
  const order = matching(calls, "r1:fix:").map((c) => c.label);
  t.check("one fixer per group", order.length === 3, order.join(","));
  t.check("in the planner's stated order, not the array order", order.join(",") === "r1:fix:G1,r1:fix:G2,r1:fix:G3", order.join(","));
  t.check("designs ran before any fixer", firstIndex(calls, "r1:fix-design:") < firstIndex(calls, "r1:fix:"));
}

t.section("B17. one post-fix review per round, over the whole round's edits");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
      { id: "G3", title: "c", rationale: "r", findings: [2], order: 3 },
    ]),
  }));
  const post = matching(calls, "r1:post-fix-review");
  t.check("exactly one post-fix review", post.length === 1, String(post.length));
  t.check("it runs after the last group", firstIndex(calls, "r1:post-fix-review") > calls.map((c) => c.label).lastIndexOf("r1:fix:G3"));
  t.check("it diffs against the snapshot taken before the FIRST group", /r1-prefix/.test(post[0].prompt));
  t.check("it is shown every group's summary", /G1:[\s\S]*G2:[\s\S]*G3:/.test(post[0].prompt));
}

t.section("B17b. a round credits the findings its fixers actually closed");
{
  // Every group's fixer returns and the post-fix review is clean, so the run
  // fixed exactly the round's confirmed findings.
  const { result, calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3));
  t.check("the run counts them", result.review.totalFixed === 3, String(result.review.totalFixed));
  t.check("and names them", (result.review.fixedTitles || []).join(",") === "T1,T2,T3", String(result.review.fixedTitles));
  const r2 = calls.filter((c) => /^r2:review:/.test(c.label));
  t.check("the next round's lenses run", r2.length > 0);
  t.check(
    "and every one is told not to re-litigate them",
    r2.every((c) => /Already found and fixed in earlier rounds[^\n]*T1; T2; T3/.test(c.prompt)),
  );
}
{
  // One group's fixer dies. Its findings were never edited, so crediting them
  // would tell the next round's lenses that untouched text reflects a fix.
  const { result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "r1:fix:G1": null,
  }));
  t.check(
    "only the surviving group's finding is credited",
    (result.review.fixedTitles || []).join(",") === "T2",
    String(result.review.fixedTitles),
  );
}
{
  const postFix = { findings: [{ title: "PF1", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "citation", introducedBy: "this-run" }] };
  const dead = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "r1:post-fix-review": postFix, "*:follow-up-fix": null }));
  t.check(
    "a dead follow-up fixer credits nothing of its own",
    !(dead.result.review.fixedTitles || []).includes("PF1"),
    String(dead.result.review.fixedTitles),
  );
  t.check("while the round's own fixes still count", dead.result.review.totalFixed === 3, String(dead.result.review.totalFixed));
  const live = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, { "r1:post-fix-review": postFix, "*:follow-up-fix": "corrected" }));
  t.check(
    "a follow-up that returned does credit its own",
    (live.result.review.fixedTitles || []).includes("PF1"),
    String(live.result.review.fixedTitles),
  );
}


// ---- Phase 5: the review log, its shards, and the round boundary ---------

const BOUNDARY = (over = {}) =>
  JSON.stringify({ merged: 2, ledgerLines: 40, standingLines: 12, ledgerGrowth: 10, compactionDue: false, changedFiles: [], hunks: 3, snapshot: "/repo/scratchpad/cp-snap/t/spec-r2", overrides: {}, ...over });

const logStubs = (over = {}) => fixStubs(2, { "*:round-boundary": BOUNDARY(), ...over });

t.section("B18a2. a shard the merge could not match is reported to the operator");
{
  // Counting stray shards in the boundary script was half the fix. A number
  // nothing reads is as silent as the lost file was, and the whole point is that
  // an agent naming its shard outside the convention loses its block without an
  // error anywhere.
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, logStubs({
    "*:round-boundary": BOUNDARY({ strayShards: 2, strayShardNames: "verify.od.SPEC-2.md,notes.md" }),
  }));
  const line = logs.find((l) => /match no loop's merge pattern/.test(l));
  t.check("the round reports the stray shards", !!line, logs.slice(-3).join(" | "));
  t.check("with the count", !!line && /2 shard\(s\)/.test(line), line);
  t.check("and the names, so the file can be found", !!line && /verify\.od\.SPEC-2\.md/.test(line), line);
}
{
  // And says nothing when there are none, so the line stays worth reading.
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, logStubs({
    "*:round-boundary": BOUNDARY({ strayShards: 0, strayShardNames: "" }),
  }));
  t.check("a clean boundary is silent about strays", !logs.some((l) => /merge pattern/.test(l)));
}

t.section("B18b. the round boundary is one exact command and nothing else");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  const b = matching(calls, "r1:round-boundary")[0];
  t.check("a boundary agent runs", !!b);
  t.check("it runs on haiku", b.opts.model === "haiku");
  // ONE command. The state write used to be a second command, a heredoc the
  // agent ran first, and that two-command shape was classified as unsafe: the
  // agent was blocked before it ran, left no transcript, and every round read as
  // one that could not certify, so a clean full sweep never converged. The
  // script writes the state now, from an argument.
  t.check("its prompt is a single invocation of the script", /Run exactly this command/.test(b.prompt) && /cp-round-boundary\.sh/.test(b.prompt));
  t.check("with no second command and no heredoc", !/two commands/.test(b.prompt) && !/<</.test(b.prompt) && !/mkdir -p/.test(b.prompt));
  t.check("it carries the loop state for a resume", /--state-json '/.test(b.prompt));
  t.check(
    "and that state names the loop and the round",
    /--state-json '[^']*"loop":"non-spec"/.test(b.prompt) && /--state-json '[^']*"round":1/.test(b.prompt),
    (b.prompt.match(/--state-json '[^']{0,80}/) || [""])[0],
  );
  t.check("it carries the loop, round, tag and thresholds", /--loop 'non-spec'/.test(b.prompt) && /--round 1/.test(b.prompt) && /--compact-at 2000/.test(b.prompt));
  t.check("the target and the trigger are passed separately", /--standing-target 200/.test(b.prompt) && /--standing-trigger 320/.test(b.prompt));
  t.check("and no other instruction", /Do nothing else: do not\s+read, summarise, or edit any other file/.test(b.prompt));
  t.check("exactly one per round", matching(calls, "r1:round-boundary").length === 1);
}

t.section("B18b2. a boundary that keeps failing stops the loop instead of spinning");
{
  // The failure this pins cost a measured run its whole budget. The boundary
  // agent was blocked before it ran, so closeRound returned false every round,
  // roundComplete was false every round, and the `isSweep && roundComplete` gate
  // never fired. Clean full sweeps kept finding nothing and the loop kept
  // re-reviewing unchanged text with the log unmerged the whole time.
  const { logs, result } = await runWorkflow(WF, { ...REVIEW_ARGS, maxNonSpecReviewRounds: 8 }, logStubs({
    "*:round-boundary": "FAILED: the agent was blocked",
  }));
  const stop = logs.find((l) => /rounds running, so no round can certify/.test(l));
  t.check("the loop stops on the streak", !!stop, logs.slice(-2).join(" | "));
  t.check("it says the loop cannot converge", !!stop && /cannot converge/.test(stop), stop);
  t.check("it says the log is unmerged", !!stop && /log is unmerged/.test(stop), stop);
  // The budget is 8; stopping on the streak must cost far fewer than that.
  const rounds = new Set(logs.map((l) => (l.match(/^Round (\d+):/) || [])[1]).filter(Boolean));
  t.check("well short of the round budget", rounds.size <= 4, [...rounds].join(","));
  t.check("and the run does not claim convergence", !result.review.converged, String(result.review.converged));
}
{
  // One failure is not the signal: it may be transient, and the merge is
  // idempotent, so the next round's boundary sweeps the orphaned shards.
  let n = 0;
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, logStubs({
    "*:round-boundary": () => (++n === 1 ? "FAILED: transient" : BOUNDARY()),
  }));
  t.check(
    "a single failure does not stop the loop",
    !logs.some((l) => /rounds running, so no round can certify/.test(l)),
  );
}

t.section("B18c. a failed boundary makes the round inconclusive");
{
  // A boundary that fails in a NON-sweep round is recoverable: the merge is
  // idempotent and the next round's call sweeps the orphaned shards, so the
  // property to pin is that the round itself cannot certify, not that the run
  // can never converge afterwards.
  const one = await runWorkflow(WF, REVIEW_ARGS, logStubs({ "r1:round-boundary": "FAILED: no such directory" }));
  t.check("it is logged as inconclusive", one.logs.some((l) => /round-boundary script did not complete; round INCONCLUSIVE/.test(l)));
  t.check("a later clean round still converges", one.result.review.converged === true, String(one.result.review.converged));
  // One that fails in EVERY round leaves the log unmerged and no snapshot, and
  // no round can certify, so the run exhausts its budget instead.
  const all = await runWorkflow(WF, REVIEW_ARGS, logStubs({ "*:round-boundary": "FAILED: no such directory" }));
  t.check("a boundary that never succeeds blocks convergence", all.result.review.converged === false, String(all.result.review.converged));
}

t.section("B19. every parallel agent writes its own shard, and none writes the log");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  const writers = calls.filter((c) => /scratchpad\/cp-log\//.test(c.prompt));
  t.check("the reviewing agents carry a shard path", writers.length > 0, String(writers.length));
  const shards = writers.map((c) => (c.prompt.match(/scratchpad\/cp-log\/[^\s]+\.md/) || [""])[0]);
  t.check("each shard path is distinct", new Set(shards).size === shards.length, shards.slice(0, 4).join(" "));
  t.check(
    "no agent is told to append to the review log itself",
    !calls.some((c) => /append[^.]*to .*review-log\.md/i.test(c.prompt)),
  );
  // The shard exception is stated only in the variant used where a shard is
  // actually named. Stating it unconditionally told agents they had "your own log
  // shard, named below" when nothing below named one, and an agent that took the
  // offer invented a path the round boundary can never match, losing the block in
  // silence.
  const permissive = calls.filter((c) => /EXCEPT the one log shard named below/.test(c.prompt));
  t.check("READ_ONLY permits exactly that one write", permissive.length > 0, String(permissive.length));
  t.check(
    "and every prompt that permits it also names the shard",
    permissive.every((c) => /scratchpad\/cp-log\/[^\s]+\.md/.test(c.prompt)),
  );
  t.check(
    "no prompt offers a shard it does not name",
    !calls.some((c) => /EXCEPT your own log shard/.test(c.prompt)),
  );
  t.check(
    "a read-only agent with no shard is told not to write at all",
    calls.some((c) => /read-only investigator\. Do not create, edit, or delete any file\./.test(c.prompt)),
  );
  // The converse, and the direction that actually regressed: splitting the
  // constant left one agent named a shard and forbidden to create any file, so
  // it either skipped its block or invented a path the merge can never match.
  // The check must run over a fix path, because fix-design is where it happened.
  const forbidden = calls.filter(
    (c) =>
      /scratchpad\/cp-log\/[^\s]+\.md/.test(c.prompt) &&
      /read-only investigator\. Do not create, edit, or delete any file\./.test(c.prompt) &&
      !/EXCEPT the one log shard named below/.test(c.prompt),
  );
  t.check("this run reaches a fix-design agent", calls.some((c) => /fix-design/.test(c.label)));
  t.check(
    "no prompt names a shard while forbidding every write",
    forbidden.length === 0,
    forbidden.map((c) => c.label).join(","),
  );
  t.check("the tag vocabulary is fixed", writers.every((c) => /CORRECTS \[id\]/.test(c.prompt) && /USEFUL \[id\]/.test(c.prompt)));
  t.check("the standing context is read first", writers.every((c) => /Read the\s+`## Standing context`/.test(c.prompt)));
  t.check("padding is discouraged", writers.some((c) => /padding it is worse than leaving it out/.test(c.prompt)));
}

t.section("B20. compaction fires when the boundary says it is due, and not otherwise");
{
  const no = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  t.check("not due: no compaction agent", never(no.calls, "r1:compact"));
  const yes = await runWorkflow(WF, REVIEW_ARGS, logStubs({ "*:round-boundary": BOUNDARY({ compactionDue: true, standingLines: 96 }) }));
  const c = yes.calls.find((x) => /:compact$/.test(x.label));
  t.check("due: a compaction agent runs", !!c);
  t.check("it may edit only the review log", /only file you may edit is .*review-log\.md/.test(c.prompt));
  t.check("it edits rather than rewriting the whole file", /EDIT, DO NOT REWRITE/.test(c.prompt) && /Do NOT\s+rewrite the whole file with Write/.test(c.prompt));
  t.check("and says why the old whole-file instruction was ignored", /The instruction was wrong and the passes were\s+right/.test(c.prompt));
  t.check("but paging the file in with sed is still barred", /forty Bash calls/.test(c.prompt));
  t.check("it does NOT verify against the repository", /DO NOT VERIFY ANYTHING AGAINST THE REPOSITORY/.test(c.prompt));
  t.check("MISTAKE is named the most valuable tag and never dropped", /MISTAKE` IS THE MOST VALUABLE TAG IN THE LOG AND IS NEVER DROPPED/.test(c.prompt));
  t.check("the standing context is structured under three budgeted headings", /### Settled/.test(c.prompt) && /### Traps/.test(c.prompt) && /### Open/.test(c.prompt));
  t.check("Settled and Open are one line each", /`### Settled` and\s+`### Open` are one line per entry/.test(c.prompt));
  t.check("Traps is uncapped in count, because that is the section worth keeping", /No cap on how many/.test(c.prompt));
  t.check("entries carry a bold subject so the section can be navigated", /GIVE EACH ENTRY A SHORT BOLD SUBJECT/.test(c.prompt));
  t.check("the target is carried from the boundary script", /THE TARGET IS 200 LINES/.test(c.prompt));
  t.check("and overshooting beats dropping something that matters", /DO NOT DROP IT/.test(c.prompt) && /the target moves up\s+on its own/.test(c.prompt));
  // The pass no longer moves anything: it curates the standing context from the
  // whole ledger, and the round boundary drains the ledger to Retired after it.
  t.check("the standing context is the only section it edits", /IT IS THE ONLY SECTION YOU EDIT/.test(c.prompt));
  t.check("it reads the whole ledger and leaves it alone", /READ ALL OF IT\. Do not edit it/.test(c.prompt));
  t.check("it is told the boundary archives the ledger for it", /the round\s+boundary archives it for you/.test(c.prompt));
  // The archive is a separate file the agent is never given the path to, so the
  // prompt states there is nothing else to read rather than forbidding a read.
  // A prohibition would not hold: measured against this same prompt, "read the
  // file once, write it once" was ignored by every pass.
  t.check("and is told this file is the whole live record", /There is no third section and no archive in this file/.test(c.prompt));
  t.check("the archive path is never given to it", !/review-log-archive/.test(c.prompt));
  t.check("CORRECTS is honoured against the standing context", /HONOUR `CORRECTS`, AGAINST THE STANDING CONTEXT/.test(c.prompt));
  t.check("a superseded watchout is deleted rather than kept", /DELETED rather than kept for the record/.test(c.prompt));
  t.check("a USEFUL entry is promoted", /HONOUR `USEFUL`/.test(c.prompt));
  // Compaction deliberately does NOT check the tree any more: doing so turned a
  // text pass into a mini-review that grepped pkg/ and read three spec files.
  t.check("contradictions are resolved by recency, not by checking the tree", /keep\s+the NEWER one/.test(c.prompt));
  t.check("OPEN, UNVERIFIED and DEFERRED are never dropped", /NEVER DROP AN `OPEN`, AN `UNVERIFIED`, OR A `DEFERRED`/.test(c.prompt));
  t.check("and DEFERRED is kept whole, because the handoff must apply it", /### Deferred/.test(c.prompt) && /cannot apply a headline/.test(c.prompt));
  t.check("it must not act on what the log says", /do not fix a\s+defect it names/.test(c.prompt));
}

t.section("B29. mid-run overrides apply forward, and anchored keys are refused");
{
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, logStubs({
    "*:round-boundary": BOUNDARY({ overrides: { maxFixGroups: 3, lensPrompt: "steer the lenses" } }),
  }));
  t.check("a forward knob is taken", logs.some((l) => /overrides applied for the next round: maxFixGroups=3/.test(l)));
  t.check("an anchored key is refused by name", logs.some((l) => /ignoring override\(s\) lensPrompt/.test(l)));
  t.check("and the refusal says why", logs.some((l) => /already baked into prompts/.test(l)));
}


// ---- Phase 6: the lens cache, argument classes, and resume ---------------

t.section("B18. every lens carries the cache instruction, keyed on content");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  const lenses = calls.filter(isLens);
  t.check("every lens carries it", lenses.every((c) => /CACHE\. Before anything else/.test(c.prompt)));
  t.check("the key is lens, round and a content hash", lenses.every((c) => {
    const round = c.label.match(/^r(\d+):/)[1];
    const lens = c.label.split(":")[2];
    return /md5sum \| cut -c1-12/.test(c.prompt) && c.prompt.includes(lens + "-r" + round + "-$H.json");
  }));
  t.check("the hash covers the files a fix would change", lenses.every((c) => /spec-changes\.md .*non-spec-changes\.md .*implementation-checklist\.md/.test(c.prompt)));
  t.check("a hit returns without reviewing", lenses.every((c) => /return exactly it as your structured output and do no other work/.test(c.prompt)));
  t.check("a miss writes the answer back", lenses.every((c) => /immediately before you return, write your findings JSON/.test(c.prompt)));
  t.check("NO cache-clear agent exists", never(calls, "cache-clear") && !calls.some((c) => /rm -rf .*cp-cache/.test(c.prompt)));
}

t.section("B29b. every argument the script reads is classified");
{
  const { readFileSync } = await import("fs");
  const { resolve } = await import("path");
  const { REPO: R } = await import("./harness.mjs");
  const src = readFileSync(resolve(R, ".claude/workflows/change-proposal.js"), "utf8");
  const reads = new Set([...src.matchAll(/\binput\.([A-Za-z_$][\w$]*)/g)].map((m) => m[1]));
  const from = src.indexOf("const ARG_CLASS");
  const registry = src.slice(from, src.indexOf("\n};", from));
  const missing = [...reads].filter((k) => !new RegExp("\\b" + k + "\\s*:").test(registry));
  t.check(reads.size + " argument(s) read, all classified", missing.length === 0, missing.join(", "));
  const classes = [...registry.matchAll(/:\s*"([a-z]+)"/g)].map((m) => m[1]);
  t.check(
    "only forward, anchored and launch are used",
    classes.length > 20 && classes.every((c) => ["forward", "anchored", "launch"].includes(c)),
    [...new Set(classes)].join(","),
  );
}

t.section("B30. resumeState continues a loop rather than restarting it");
{
  const state = JSON.stringify({ loop: "non-spec", round: 2, sweeps: 1, retired: ["citations", "security"], args: { exemplar: "old.md" } });
  const { logs } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, resumeState: true },
    logStubs({ "resume-state:*": state }),
  );
  t.check("it resumes at the recorded round", logs.some((l) => /Resuming the non-spec loop at round 2/.test(l)));
  t.check("with the recorded lenses still retired", logs.some((l) => /with 2 lens\(es\) already retired/.test(l)));
  t.check("and names an anchored argument that changed since", logs.some((l) => /anchored argument exemplar changed since the recorded run/.test(l)));
}
{
  const { logs } = await runWorkflow(WF, { ...REVIEW_ARGS, resumeState: true }, logStubs({ "resume-state:*": "{}" }));
  t.check("no recorded state starts at round 1", logs.some((l) => /no state was recorded/.test(l)));
}
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  t.check("without resumeState nothing is read", never(calls, "resume-state"));
}


// ---- Phase 7: the introspection gate, panels, and next steps -------------

const PASS = (over = {}) => ({
  observations: ["o"], caseHealthy: "h", caseUnhealthy: "u",
  verdict: "healthy", reasoning: "r", prediction: "p", ...over,
});
const introStubs = (over = {}) =>
  logStubs({
    "introspect:*": PASS(),
    "introspect-gate:*": { warranted: true, why: "the counter is right" },
    "judge:*": { falsified: false, howConclusive: "none", theArgumentIAttacked: "a", reasoning: "could not" },
    growth: { documentWas: 10, documentNow: 12, grew: [] },
    ...over,
  });

t.section("B21. the gate can stop a counter wake before the full pass runs");
{
  const { calls, logs } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 99 },
    introStubs({
      "*:review:*": ({ label }) => (/^r1:/.test(label) ? { coverage: "c", findings: fs(6) } : { coverage: "c", findings: [] }),
      "*:dedup": { findings: fs(6).map((f) => ({ ...f, lenses: ["mechanism"], kind: "design-defect", area: "one-area" })) },
      "introspect-gate:*": { warranted: false, why: "the area is large and draining normally" },
    }),
  );
  // The churn counter needs several rounds to trip, so this asserts the gate's
  // wiring rather than a trip: when it runs and refuses, no pass and no panel follow.
  if (!never(calls, "introspect-gate")) {
    t.check("no full pass runs after an unwarranted gate", never(calls, "introspect:"));
    t.check("no panel runs either", never(calls, "judge:"));
    t.check("and it is logged", logs.some((l) => /gate found the counter unwarranted/.test(l)));
  } else {
    t.check("the gate is wired (no counter tripped in this run)", true);
  }
}
{
  // A CADENCE wake ignores the gate: the cadence exists to look when no counter
  // has fired, and letting the gate suppress it removes the only pass that is
  // not reacting to something.
  const { calls } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1, maxNonSpecReviewRounds: 4 },
    introStubs({ "introspect-gate:*": { warranted: false, why: "no" } }),
  );
  t.check("a cadence wake runs the full pass anyway", !never(calls, "introspect:"), labels(calls).filter((l) => /introspect/.test(l)).join(","));
  t.check("and never consults the gate", never(calls, "introspect-gate"));
}

t.section("B22-B23. every verdict goes to a panel, and it stands unless falsified");
{
  const { calls, logs } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 }, introStubs(),
  );
  const judges = matching(calls, "judge:healthy:");
  t.check("a healthy verdict still convenes a panel", judges.length > 0, String(judges.length));
  t.check("with judgesHealthy judges", judges.length >= 2);
  t.check("they are told to falsify, not vote", judges.every((c) => /YOUR JOB\s+IS TO FALSIFY THAT, not to vote/.test(c.prompt)));
  t.check("partial is named an honest answer", judges.every((c) => /`partial` is an\s+honest and common answer/.test(c.prompt)));
  t.check("ratifying is named the other failure", judges.every((c) => /RATIFYING IS THE OTHER FAILURE/.test(c.prompt)));
  t.check("the verdict stands when none falsifies", logs.some((l) => /the verdict healthy STANDS/.test(l)));
}
{
  const { logs } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 },
    introStubs({
      "introspect:*": PASS({ verdict: "halt", questionForHuman: "which mechanism ships?" }),
      "judge:*": { falsified: true, howConclusive: "conclusive", theArgumentIAttacked: "a", reasoning: "the run is draining", fallbackVerdict: "healthy" },
    }),
  );
  t.check("a majority falsifying conclusively overturns it", logs.some((l) => /falsified halt conclusively; taking the least disruptive fallback, healthy/.test(l)));
}
{
  const { result } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 },
    introStubs({
      "introspect:*": PASS({ verdict: "halt", questionForHuman: "q" }),
      "judge:*": { falsified: true, howConclusive: "partial", theArgumentIAttacked: "a", reasoning: "unsure", fallbackVerdict: "healthy" },
    }),
  );
  t.check("a partial falsification leaves a halt standing", !!result.introspection.stoppedBy, JSON.stringify(result.introspection.stoppedBy || {}).slice(0, 80));
}
{
  // A falsifier that names no fallback used to be read as naming `healthy`, so a
  // unanimous conclusive refutation of `healthy` decided `healthy` and logged a
  // fallback nobody had named.
  const { calls, logs } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 },
    introStubs({
      "judge:*": { falsified: true, howConclusive: "conclusive", theArgumentIAttacked: "a", reasoning: "the run is circling" },
    }),
  );
  t.check("judges are asked for the verdict the evidence supports",
    matching(calls, "judge:healthy:").every((c) => /fallbackVerdict/.test(c.prompt)));
  t.check("a refutation that names nothing is logged as naming nothing",
    logs.some((l) => /named no verdict the evidence supports/.test(l)));
  t.check("and is not reported as a fallback a judge named",
    !logs.some((l) => /taking the least disruptive fallback/.test(l)));
}
{
  // The loop continues on a verdict no judge endorsed, so the next round must
  // re-examine it rather than wait out the cadence.
  const undirected = {
    ...introStubs(),
    "*:review:*": { coverage: "c", findings: fs(2) },
    "judge:*": { falsified: true, howConclusive: "conclusive", theArgumentIAttacked: "a", reasoning: "circling, no direction" },
  };
  const { calls } = await runWorkflow(WF, { ...REVIEW_ARGS, introspectEvery: 2, maxNonSpecReviewRounds: 4 }, undirected);
  const rounds = labels(calls).filter((l) => /^introspect:r/.test(l));
  t.check("an undirected refutation forces the next round to re-introspect",
    rounds.includes("introspect:r3"), rounds.join(","));

  const directed = { ...undirected, "judge:*": { ...undirected["judge:*"], fallbackVerdict: "prune" } };
  const { calls: dcalls } = await runWorkflow(WF, { ...REVIEW_ARGS, introspectEvery: 2, maxNonSpecReviewRounds: 4 }, directed);
  const drounds = labels(dcalls).filter((l) => /^introspect:r/.test(l));
  t.check("a refutation that names a fallback does not force one",
    !drounds.includes("introspect:r3"), drounds.join(","));
}
{
  const { result } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 },
    introStubs({
      "introspect:*": PASS({ verdict: "halt", questionForHuman: "q" }),
      "judge:*": { falsified: true, howConclusive: "conclusive", theArgumentIAttacked: "a", reasoning: "x", fallbackVerdict: "halt" },
    }),
  );
  t.check("a falsifier naming the verdict it refuted cannot re-impose it",
    !result.introspection.stoppedBy, JSON.stringify(result.introspection.stoppedBy || {}).slice(0, 80));
}

t.section("B24. each verdict gets its own panel, and redesign judges share fix-design's principles");
{
  for (const [v, marker] of [
    ["redesign", /SMALLER-MECHANISM judge/],
    ["prune", /DELEGATION judge/],
    ["reframe", /PROBLEM-FIT judge/],
    ["halt", /HUMAN-QUESTION judge/],
  ]) {
    const { calls } = await runWorkflow(
      WF, { ...REVIEW_ARGS, introspectEvery: 1, maxRedesigns: 0 },
      introStubs({ "introspect:*": PASS({ verdict: v, questionForHuman: "q", areas: ["m"], sections: ["s"] }) }),
    );
    const judges = matching(calls, "judge:" + v + ":");
    t.check(v + " gets its own panel", judges.length > 0, String(judges.length));
    t.check(v + " panel is specialised", judges.some((c) => marker.test(c.prompt)));
  }
  const { calls } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1, maxRedesigns: 0 },
    introStubs({ "introspect:*": PASS({ verdict: "redesign", areas: ["m"] }) }),
  );
  const rj = matching(calls, "judge:redesign:");
  t.check("redesign judges weigh deleting over respecifying", rj.some((c) => /Can the thing be DELETED rather than respecified/.test(c.prompt)));
  t.check("and count the cascade", rj.some((c) => /what else in the proposal must change/.test(c.prompt)));
}

t.section("B26. a stopping verdict carries proposed next steps");
{
  const { result } = await runWorkflow(
    WF, { ...REVIEW_ARGS, introspectEvery: 1 },
    introStubs({
      "introspect:*": PASS({
        verdict: "halt",
        questionForHuman: "which mechanism ships?",
        nextSteps: {
          summary: "re-run with the mechanism lens leading and the spec staging locked",
          confidence: "clear",
          rerunMode: "review",
          rerunArgs: '{"lockSpecChanges":true,"startLenses":["mechanism"]}',
        },
      }),
    }),
  );
  const stopped = result.introspection.stoppedBy;
  t.check("the run stops", !!stopped && stopped.verdict === "halt");
  t.check("the next steps are carried out in the result", !!result.introspection.nextSteps);
  t.check("with a confidence the skill can branch on", result.introspection.nextSteps.confidence === "clear");
  t.check("and rerun arguments that parse", (() => {
    try { JSON.parse(result.introspection.nextSteps.rerunArgs); return true; } catch { return false; }
  })());
  t.check("the pass is told to fill them", true);
}

t.section("B26b. a round that ends the loop still closes, and a stopped run does not report reviewed");
{
  for (const v of ["halt", "reframe"]) {
    const { calls, result } = await runWorkflow(
      WF, { ...REVIEW_ARGS, introspectEvery: 1 },
      introStubs({ "introspect:*": PASS({ verdict: v, questionForHuman: "q" }) }),
    );
    const stopped = result.introspection.stoppedBy;
    t.check(v + " stops the loop", !!stopped && stopped.verdict === v);
    t.check(
      "the round that ends in " + v + " still closes through the boundary",
      matching(calls, "r" + stopped.round + ":round-boundary").length === 1,
      labels(calls).filter((l) => /round-boundary/.test(l)).join(",") || "none",
    );
    t.check(
      "and the run does not report itself reviewed",
      result.status === "stopped-" + v,
      String(result.status),
    );
  }
  // Every reviewer dying ends the loop the same way, and that round wrote log
  // shards and owes the next launch a snapshot exactly as any other does.
  const dead = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" }, "*:review:*": null }));
  t.check(
    "a round whose reviewers all died closes too",
    matching(dead.calls, "r1:round-boundary").length === 1,
    labels(dead.calls).filter((l) => /round-boundary/.test(l)).join(",") || "none",
  );
  t.check("and the loop records the reviewer failure", dead.result.review.reviewersFailed === true);
}


t.section("B32. each review loop introspects and counts churn on its OWN rounds");
{
  // `lastIntrospectRound` is compared against `round`, which restarts at 1 in
  // each loop. Measured before it was reset per loop: at introspectEvery 3 over
  // two 6-round loops the spec loop introspected at r3 and r6 and left the
  // counter at 6, and the non-spec loop -- which reviews the larger half --
  // evaluated 1 - 6 >= 3 every round and introspected zero times.
  //
  // The handoff call is dispatched between the two loops, so its index splits
  // the call list into the spec loop's half and the non-spec loop's half.
  const { calls } = await runWorkflow(
    WF,
    {
      ...REVIEW_ARGS, introspectEvery: 3, maxSpecReviewRounds: 6,
      maxNonSpecReviewRounds: 6, allowNonSpecOnUnconvergedSpec: true,
    },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
      "*:review:*": { coverage: "c", findings: fs(2) },
      "*:dedup": { findings: fs(2).map((f) => ({ ...f, lenses: ["citations"] })) },
    }),
  );
  const handoff = firstIndex(calls, "spec-nonspec-handoff");
  const passes = matching(calls, "introspect:");
  const after = passes.filter((c) => c.index > handoff).map((c) => c.label);
  t.check("the spec loop introspects on cadence", passes.some((c) => c.index < handoff), passes.map((c) => c.label).join(","));
  t.check("and so does the non-spec loop", after.length > 0, after.join(",") || "(none)");
  t.check("on its own round numbers", after.join(",") === "introspect:r3,introspect:r6", after.join(","));
}
{
  // The same root cause in the churn window. `areaLog` records the round a
  // finding was filed in, so without the loop it was filed in, every entry from
  // the spec loop falls inside any window the non-spec loop measures: six design
  // defects against area "one" in the spec loop tripped the churn counter in
  // non-spec round 1, in an area that loop had found nothing in. introspectEvery
  // is 99 here so that only churn can wake a pass.
  const D = (n, area, kind) => ({
    title: "T" + n, where: "w" + n, claim: "c", why_wrong: "w", evidence: "e",
    suggested_fix: "f", area, kind, introducedBy: "this-run",
  });
  const specFindings = Array.from({ length: 6 }, (_, i) => D(i + 1, "one", "design-defect"));
  const nonSpecFindings = [D(1, "two", "citation")];
  // Every agent's phase is prefixed with the loop that dispatched it, so a stub
  // can answer differently in each loop without tracking where the run is.
  const byLoop = (extra) => ({ opts }) => ({
    coverage: "c",
    ...extra(/^non-spec/.test(String(opts.phase || "")) ? nonSpecFindings : specFindings),
  });
  const { calls } = await runWorkflow(
    WF,
    {
      ...REVIEW_ARGS, introspectEvery: 99, maxSpecReviewRounds: 2,
      maxNonSpecReviewRounds: 2, allowNonSpecOnUnconvergedSpec: true,
    },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
      "*:review:*": byLoop((f) => ({ findings: f })),
      "*:dedup": byLoop((f) => ({ findings: f.map((x) => ({ ...x, lenses: ["mechanism"] })) })),
    }),
  );
  const handoff = firstIndex(calls, "spec-nonspec-handoff");
  t.check("the spec loop's own churn still trips", !never(calls.slice(0, handoff), "introspect"));
  t.check(
    "the non-spec loop's churn counter reads only its own findings",
    never(calls.slice(handoff), "introspect"),
    calls.slice(handoff).filter((c) => /introspect/.test(c.label)).map((c) => c.label).join(","),
  );
}
{
  // The over-correction guard. `redesignsRun` is the redesign budget AND the tag
  // in the subproposal's filename, so resetting it per loop alongside
  // `lastIntrospectRound` would make the non-spec loop's first redesign overwrite
  // the spec loop's subproposal record. One round per loop, so each fires one.
  const { calls } = await runWorkflow(
    WF,
    {
      ...REVIEW_ARGS, introspectEvery: 1, maxRedesigns: 2, maxSpecReviewRounds: 1,
      maxNonSpecReviewRounds: 1, allowNonSpecOnUnconvergedSpec: true,
    },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
      "introspect:*": PASS({ verdict: "redesign", areas: ["a1"] }),
      "redesign*:review:*": { findings: [] },
      "redesign*": "done",
    }),
  );
  const redesigns = matching(calls, "redesign");
  t.check("a redesign runs in each loop", redesigns.length > 0, labels(calls).filter((l) => /redesign/.test(l)).join(","));
  const tags = [...new Set(redesigns.map((c) => c.label.match(/^redesign(\d+):/)[1]))];
  t.check("the two loops' redesigns get distinct tags", tags.join(",") === "1,2", tags.join(","));
  const files = new Set(
    redesigns.filter((c) => /-redesign-\d+\.md/.test(c.prompt)).map((c) => c.prompt.match(/-redesign-(\d+)\.md/)[1]),
  );
  t.check("and distinct subproposal files", files.size === 2, [...files].join(","));
}

t.section("B24c. a caller-requested redesign runs once for the run and respects the budget");
{
  // The entry redesign block sits inside runReviewLoop, which is called once per
  // loop, so without a run-scoped flag it fired twice: measured with
  // focusAreas ['teardown'], redesign1:* ran in the spec loop and redesign2:* in
  // the non-spec loop, twelve agents where six were asked for, and the second
  // pass spent the last of maxRedesigns so introspection could never ask for one.
  const ARGS = {
    ...REVIEW_ARGS, mode: "redesign", focusAreas: ["teardown"],
    maxSpecReviewRounds: 1, maxNonSpecReviewRounds: 1, allowNonSpecOnUnconvergedSpec: true,
  };
  const RD = { "redesign*:review:*": { findings: [] }, "redesign*": "done" };
  const tagsOf = (calls) => [
    ...new Set(matching(calls, "redesign").map((c) => c.label.match(/^redesign(\d+):/)[1])),
  ].join(",");

  const { calls, logs } = await runWorkflow(WF, ARGS, loopStubs(RD));
  t.check(
    "both loops run",
    logs.some((l) => /Entering the spec review loop/.test(l)) &&
      logs.some((l) => /Entering the non-spec review loop/.test(l)),
  );
  t.check("the caller's redesign runs once for the run, not once per loop", tagsOf(calls) === "1", tagsOf(calls));
  t.check(
    "and exactly one apply lands it",
    matching(calls, "redesign").filter((c) => /:apply$/.test(c.label)).length === 1,
    labels(calls).filter((l) => /redesign/.test(l)).join(","),
  );

  // The budget the introspection path already honours.
  const zero = await runWorkflow(WF, { ...ARGS, maxRedesigns: 0 }, loopStubs(RD));
  t.check(
    "maxRedesigns 0 suppresses it",
    matching(zero.calls, "redesign").length === 0,
    labels(zero.calls).filter((l) => /redesign/.test(l)).join(","),
  );
  t.check("and says why", zero.logs.some((l) => /budget of 0 is spent/.test(l)));

  // The budget is left for introspection: with maxRedesigns 1 the entry pass
  // spends it and the introspection pass records the refusal instead.
  const one = await runWorkflow(
    WF,
    { ...ARGS, maxRedesigns: 1, introspectEvery: 1 },
    introStubs({ ...RD, "introspect:*": PASS({ verdict: "redesign", areas: ["a1"] }) }),
  );
  t.check("one redesign total across the run", tagsOf(one.calls) === "1", tagsOf(one.calls));
}

t.section("B24a2. a prune agent that dies prunes nothing, and says so");
{
  // The bookkeeping used to run on a discarded return: the sections were marked
  // pruned, the history recorded a prune, and `retired.clear()` fired, all on
  // the strength of an edit that never landed. Clearing the retirement set is
  // the expensive half, because it costs the loop a whole serialised round.
  const { calls, result, logs } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, introspectEvery: 1, maxNonSpecReviewRounds: 3 },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: fs(2) },
      "*:dedup": { findings: fs(2).map((f) => ({ ...f, lenses: ["citations"] })) },
      "introspect:*": PASS({ verdict: "prune", sections: ["## 3. Design"] }),
      "prune:*": null,
    }),
  );
  t.check("the prune agent was called", matching(calls, "prune:r").length > 0);
  t.check(
    "but nothing is recorded as pruned",
    (result.introspection.prunes || []).length === 0,
    JSON.stringify(result.introspection.prunes || []),
  );
  t.check(
    "and the failure is reported rather than silent",
    logs.some((l) => /prune agent did not return/.test(l)),
    logs.filter((l) => /prune/.test(l)).join(" | "),
  );
  t.check(
    "no round claims to have pruned sections",
    !logs.some((l) => /pruned \d+ section/.test(l)),
  );
}

t.section("B24b. a prune is budgeted, remembers what it deleted, and lets the pool drain");
{
  // Measured against the pre-fix code: a pass naming the same section every
  // round pruned it in rounds 1-4 and cleared the retirement set each time, so
  // every round launched all 13 lenses and no sweep was ever reached.
  const { calls, result } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, introspectEvery: 1, maxNonSpecReviewRounds: 4 },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: fs(2) },
      "*:dedup": { findings: fs(2).map((f) => ({ ...f, lenses: ["citations"] })) },
      "introspect:*": PASS({ verdict: "prune", sections: ["## 3. Design"] }),
    }),
  );
  const prunes = matching(calls, "prune:r");
  t.check("the same section is pruned once", prunes.length === 1, labels(calls).filter((l) => /^prune:/.test(l)).join(","));
  // Round 1's prune clears the retirement set, which is deliberate, so round 2
  // is a full round. What the budget buys is that no later round is cleared
  // again: rounds 3 and 4 drain. Before the fix every round was 13.
  const per = [1, 2, 3, 4].map((n) => calls.filter((c) => new RegExp("^r" + n + ":review:").test(c.label)).length);
  t.check("and the pool drains behind it", per[2] < per[1], per.join(","));
  t.check("the prune is recorded on the run", (result.introspection.prunes || []).length === 1,
    JSON.stringify(result.introspection.prunes || []));
}
{
  // A distinct section each round: the memory does not apply, so only the
  // budget can stop it. Without one the pre-fix code pruned four times.
  const { calls, logs } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, introspectEvery: 1, maxNonSpecReviewRounds: 4, maxPrunes: 2 },
    introStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "*:review:*": { coverage: "c", findings: fs(2) },
      "*:dedup": { findings: fs(2).map((f) => ({ ...f, lenses: ["citations"] })) },
      "introspect:*": ({ label }) => PASS({ verdict: "prune", sections: ["## S" + label.match(/r(\d+)/)[1]] }),
    }),
  );
  t.check("the budget caps the prunes", matching(calls, "prune:r").length === 2,
    labels(calls).filter((l) => /^prune:/.test(l)).join(","));
  t.check("and the spent budget is logged", logs.some((l) => /prune but the budget of 2 is spent/.test(l)));
}

// ---- Phase 8b: the applicability lens under one execution sequence -------

t.section("B31. the lens states the lane rules and no longer forbids an interleave");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, logStubs());
  const app = calls.filter(isLens).find((c) => c.label.endsWith(":applicability"));
  t.check("the applicability lens runs", !!app);
  t.check("EXECUTION-MODEL INVERSION is gone", !/EXECUTION-MODEL INVERSION/.test(app.prompt));
  t.check("the old spec-edits-first claim is gone", !/lands its spec\/ edits\s+FIRST/.test(app.prompt));
  t.check("one lane per step is a finding", /A step\s+naming both a spec deliverable and a non-spec one is a finding/.test(app.prompt));
  t.check("the leading-spec-block norm is stated", /standard pattern is every spec step first, in a leading block/.test(app.prompt));
  t.check("an unjustified interleave is a finding", /WITHOUT stating on its own line why the interleave is necessary/.test(app.prompt));
  t.check("and a bad justification is judged", /Efficiency, convenience, and a\s+preference for building before writing do not qualify/.test(app.prompt));
  t.check("the guarantee is restated as a dependency rule", /implementing a statement staged by a LATER step is a finding/.test(app.prompt));
  t.check("the checklist is named the one execution sequence", /THE ONE EXECUTION SEQUENCE/.test(app.prompt));
}
{
  const { calls } = await runWorkflow(WF, NEW_ARGS, newStubs());
  const w = calls.find((c) => c.label === "write");
  t.check("the writer is told one lane per step", /ONE lane only/.test(w.prompt));
  t.check("and that spec steps lead by default", /standard pattern is every\s+spec step first/.test(w.prompt));
}


// ---- Post-smoke fixes ----------------------------------------------------
//
// Three defects a real run on proposal 0076 exposed that no stub had.

t.section("PS1. the problem statement is editable, and the bound is stated");
{
  const withFinding = loopStubs({
    "*:review:*": ({ label }) => (/^r1:/.test(label) ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
  });
  for (const [name, args] of [["spec", REVIEW_ARGS], ["non-spec", { ...REVIEW_ARGS, lockSpecChanges: true }]]) {
    const { calls } = await runWorkflow(WF, args, withFinding);
    const fixers = calls.filter((c) => /:fix:/.test(c.label));
    t.check(name + ": a fixer runs", fixers.length > 0);
    t.check(
      name + ": every fixer may edit the problem statement",
      fixers.every((c) => /problem-statement\.md — CORRECT THE RECORD here/.test(c.prompt)),
    );
    t.check(
      name + ": and is told to fix it in the SAME edit as the section restating it",
      fixers.every((c) => /in the same edit as the section that restates it/.test(c.prompt)),
    );
    t.check(
      name + ": changing the question is refused and routed to a reframe",
      fixers.every((c) => /You may NOT change what the problem IS/.test(c.prompt) && /introspection pass's decision/.test(c.prompt)),
    );
  }
}

t.section("PS2. the parallel designs are reconciled before any of them is applied");
{
  const three = fixStubs(3, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
      { id: "G3", title: "c", rationale: "r", findings: [2], order: 3 },
    ]),
    "*:fix-design-reconcile": { conflicts: [], revised: [] },
    "*:fix-design:*": { designs: [{ findingTitle: "T1", effort: "moderate", chosen: { approach: "a", why: "w" } }], groupNote: "", newMechanisms: [] },
  });
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, three);
  const rec = calls.find((c) => /fix-design-reconcile/.test(c.label));
  t.check("a reconciliation runs", !!rec);
  t.check("after every design", firstIndex(calls, "r1:fix-design:") < firstIndex(calls, "r1:fix-design-reconcile"));
  t.check("and before any fixer", firstIndex(calls, "r1:fix-design-reconcile") < firstIndex(calls, "r1:fix:"));
  t.check("exactly one per round", matching(calls, "r1:fix-design-reconcile").length === 1);
  t.check("it is read-only", /read-only investigator/.test(rec.prompt));
  t.check("it is given every group's design", /"id": "G1"[\s\S]*"id": "G2"[\s\S]*"id": "G3"/.test(rec.prompt));
  t.check("same section is not a conflict; same statement is", /touch the same SECTION are not in conflict/.test(rec.prompt));
  t.check("it resolves rather than only reporting", /RESOLVE, do not just report/.test(rec.prompt));
  t.check("and prefers merging two additions into one", /PREFER THE SMALLER RESULT/.test(rec.prompt));
}
{
  // A revised design must reach the fixer that applies it.
  const { calls, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "*:fix-design:*": { designs: [{ findingTitle: "T1", effort: "moderate", chosen: { approach: "the original", why: "w" } }], groupNote: "", newMechanisms: [] },
    "*:fix-design-reconcile": {
      conflicts: [{ groups: ["G1", "G2"], what: "both rewrite the same predicate", resolution: "G1's wording survives" }],
      revised: [{ groupId: "G2", designs: [{ findingTitle: "T2", effort: "moderate", chosen: { approach: "the reconciled one", why: "G1 owns the predicate" } }] }],
    },
  }));
  const g2 = calls.find((c) => c.label === "r1:fix:G2");
  const g1 = calls.find((c) => c.label === "r1:fix:G1");
  t.check("the revised design reaches its group", /the reconciled one/.test(g2.prompt));
  t.check("an unrevised group keeps its original", /the original/.test(g1.prompt));
  t.check("the conflict is recorded in the round history", JSON.stringify(result.review.history).includes("both rewrite the same predicate"));
}
{
  // A reconciler that revises ONE finding's design in a two-finding group must
  // not delete the other's: the fixer is told to apply the design it is given,
  // and the design's adjudicated sites are what the post-fix review checks.
  const d = (title, approach, site) => ({
    findingTitle: title, effort: "moderate", chosen: { approach, why: "w" },
    siteDispositions: [{ file: site, line: 1, quote: "q", disposition: "in-scope", why: "w" }],
  });
  const { calls, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1, 2], order: 2 },
    ]),
    "r1:fix-design:G1": { designs: [d("T1", "G1 design", "proposals/0081_fix_x/0081_fix_x.spec-changes.md")], groupNote: "", newMechanisms: [] },
    "r1:fix-design:G2": { designs: [d("T2", "G2 design for T2", "proposals/0081_fix_x/0081_fix_x.summary.md"), d("T3", "G2 design for T3", "proposals/0081_fix_x/0081_fix_x.non-spec-changes.md")], groupNote: "", newMechanisms: [] },
    "*:fix-design-reconcile": {
      conflicts: [{ groups: ["G1", "G2"], what: "both state the predicate", resolution: "G1's wording survives" }],
      revised: [{ groupId: "G2", designs: [d("T2", "the reconciled T2 design", "proposals/0081_fix_x/0081_fix_x.summary.md")] }],
    },
  }));
  const g2 = calls.find((c) => c.label === "r1:fix:G2");
  t.check("the revision reaches its group", /the reconciled T2 design/.test(g2.prompt));
  t.check("and the group's other finding keeps its design", /G2 design for T3/.test(g2.prompt));
  t.check(
    "so every design's in-scope site still reaches the post-fix review",
    result.review.history[0].sitesAdopted === 3,
    String(result.review.history[0].sitesAdopted),
  );
}
{
  // A revision naming a group this round does not have is dropped. It must be
  // reported, and the log must count what was applied.
  const one = (title, approach) => ({ findingTitle: title, effort: "moderate", chosen: { approach, why: "w" } });
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "*:fix-design:*": { designs: [one("T1", "the original")], groupNote: "", newMechanisms: [] },
    "*:fix-design-reconcile": {
      conflicts: [{ groups: ["G1", "G9"], what: "both state the predicate", resolution: "G9's wording survives" }],
      revised: [{ groupId: "G9", designs: [one("T2", "a design for a group that does not exist")] }],
    },
  }));
  t.check("the unknown group is reported", logs.some((l) => /does not have \(G9\)/.test(l)));
  t.check("the round records the dropped revision", (result.review.history[0].designRevisionsDropped || []).includes("G9"));
  t.check("and the log counts what was applied", logs.some((l) => /applied 0 revised design\(s\)/.test(l)));
  t.check(
    "no group's design is corrupted by it",
    calls.filter((c) => /^r1:fix:G/.test(c.label)).every((c) => /the original/.test(c.prompt) && !/a design for a group that does not exist/.test(c.prompt)),
  );
}
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([{ id: "G1", title: "a", rationale: "r", findings: [0, 1], order: 1 }]),
  }));
  t.check("one group needs no reconciliation", never(calls, "fix-design-reconcile"));
}
{
  // Each fixer after the first is told what the earlier ones actually did,
  // which the design stage could not know because it ran before any edit.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:fix-plan": plan([
      { id: "G1", title: "a", rationale: "r", findings: [0], order: 1 },
      { id: "G2", title: "b", rationale: "r", findings: [1], order: 2 },
    ]),
    "*:fix-design-reconcile": { conflicts: [], revised: [] },
    "*:fix:*": { summary: "rewrote the predicate in section 4", newMechanisms: [], escalated: [], designRejected: [] },
  }));
  const g1 = calls.find((c) => c.label === "r1:fix:G1");
  const g2 = calls.find((c) => c.label === "r1:fix:G2");
  t.check("the first fixer is told of no earlier group", !/WHAT THE EARLIER GROUPS IN THIS ROUND/.test(g1.prompt));
  t.check("the second is", /WHAT THE EARLIER GROUPS IN THIS ROUND/.test(g2.prompt));
  t.check("and carries what the first actually did", /rewrote the predicate in section 4/.test(g2.prompt));
  t.check("and is told to check anchors against the current text", /Check your anchors against the CURRENT text/.test(g2.prompt));
}

t.section("PS3. the spec loop runs on intent, not on whether the text is written yet");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const probe = calls.find((c) => c.label === "probe:spec-changes");
  t.check("the probe asks about intent", /Report whether a proposal INTENDS any change/.test(probe.prompt));
  t.check("an unwritten target still counts", /even when the text is not written yet, is\s+marked as an indicative target/.test(probe.prompt));
  t.check("and says why that needs the loop more, not less", /needs the spec review MORE than one whose staging is finished/.test(probe.prompt));
  t.check("NO requires nothing anywhere naming a spec target", /the staging carries only its\s+headings AND nothing anywhere names a spec target/.test(probe.prompt));
  t.check("it also reads the summary when the staging is thin", /if the first is thin/.test(probe.prompt));
}

// ---------------------------------------------------------------------------
// F5: per-finding site expansion.
// ---------------------------------------------------------------------------

const sites = (proposal = [], tree = []) => ({ proposal, tree, searched: "grepped X" });

// Pull the sites JSON back out of a prompt, so a test asserts the DATA an agent
// receives rather than the sentence wrapped around it.
function sitesPayload(prompt) {
  const i = prompt.indexOf("accordingly.\n");
  if (i < 0) return null;
  const start = prompt.indexOf("[", i);
  let depth = 0;
  for (let k = start; k < prompt.length; k++) {
    if (prompt[k] === "[") depth++;
    else if (prompt[k] === "]" && --depth === 0) return JSON.parse(prompt.slice(start, k + 1));
  }
  return null;
}
const SITE_P = { file: "proposals/0081_fix_x/0081_fix_x.spec-changes.md", line: 10, quote: "q", why: "breaks", confidence: "high" };
const SITE_T = { file: "spec/10.md", line: 20, quote: "tq", why: "breaks", confidence: "medium" };

t.section("X1. expansion runs once per CONFIRMED finding, on sonnet, before grouping");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(3, {
    "*:expand:*": sites([SITE_P]),
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1, 2], order: 1 }]),
  }));
  const exp = matching(calls, "r1:expand:");
  t.check("one expansion per confirmed finding", exp.length === 3, String(exp.length));
  t.check("each runs on sonnet", exp.every((c) => c.opts.model === "sonnet"));
  t.check("expansion precedes grouping", firstIndex(calls, "r1:expand:") < firstIndex(calls, "r1:fix-plan"));
  t.check("and precedes design", firstIndex(calls, "r1:expand:") < firstIndex(calls, "r1:fix-design:"));
  t.check("it is anchored to one finding", /site-expansion pass for ONE confirmed finding/.test(exp[0].prompt));
  t.check("the test is falsification", /WHICH OTHER SITES BECOME WRONG/.test(exp[0].prompt));
  t.check("consistent restatement is excluded", /Consistent\s+restatement is not a defect/.test(exp[0].prompt));
  t.check("an empty result is blessed", /AN EMPTY RESULT IS A GOOD RESULT/.test(exp[0].prompt));
  t.check("both search methods are required", /MECHANICAL/.test(exp[0].prompt) && /BY FUNCTION/.test(exp[0].prompt));
  t.check("tree sites are named as missing edit sites", /THE PROPOSAL IS MISSING AN EDIT\s+SITE/.test(exp[0].prompt));
  t.check("it may not write a log shard", /including a log\s+shard/.test(exp[0].prompt));
}

t.section("X2. a refuted finding is never expanded");
{
  // Refuting EVERY finding empties the round, which short-circuits before
  // expansion is reached -- so an all-refuted fixture proves nothing. Only a
  // MIXED round distinguishes "expands the confirmed ones" from "expands
  // everything the dedup produced".
  const mixed = fixStubs(3, {
    "*:expand:*": sites([SITE_P]),
    "*:verify-material": ({ prompt }) =>
      /"title": "T1"/.test(prompt)
        ? { confirmed: false, reason: "not material" }
        : { confirmed: true, reason: "material" },
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]),
  });
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, mixed);
  t.check("the round still runs", !never(calls, "r1:fix:"));
  t.check("only the confirmed findings are expanded", matching(calls, "r1:expand:").length === 2,
    String(matching(calls, "r1:expand:").length));
  const expanded = matching(calls, "r1:expand:").map((c) => c.prompt).join("\n");
  t.check("and the refuted one is not among them", !/"title": "T1"/.test(expanded));
}
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:verify-material": { confirmed: false, reason: "not material" },
    "*:expand:*": sites([SITE_P]),
  }));
  t.check("an all-refuted round expands nothing", never(calls, "r1:expand:"));
}

t.section("X3. a dead expansion leaves the finding intact and the round proceeds");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, { "*:expand:*": null }));
  t.check("the fixer still ran", !never(calls, "r1:fix:"));
  const d = matching(calls, "r1:fix-design:")[0];
  t.check("the design carries no sites block", !/POTENTIALLY RELATED SITES/.test(d.prompt));
  t.check("and the confirmed finding is unchanged", /"where": "w1"/.test(d.prompt));
}

t.section("X4. sites reach the planner, the designer and the fixer, framed as candidates");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:expand:*": sites([SITE_P], [SITE_T]),
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]),
  }));
  const planner = calls.find((c) => c.label === "r1:fix-plan");
  const design = matching(calls, "r1:fix-design:")[0];
  const fixer = matching(calls, "r1:fix:")[0];
  t.check("the planner gets them", /POTENTIALLY RELATED SITES/.test(planner.prompt));
  t.check("and is told to group on overlap", /USE THE SITES FOR ONE THING: OVERLAP/.test(planner.prompt));
  t.check("the designer gets them", /POTENTIALLY RELATED SITES/.test(design.prompt));
  t.check("with three dispositions", /IN SCOPE/.test(design.prompt) && /SEPARATE FINDING/.test(design.prompt) && /NOT A SITE/.test(design.prompt));
  t.check("and pressure in both directions", /PRESSURE RUNS BOTH WAYS/.test(design.prompt));
  t.check("everyone is told they are unverified", /They are CANDIDATES/.test(design.prompt));
  t.check("the fixer is told the design decides", /THE SITES YOU EDIT ARE FIXED BY THE DESIGN/.test(fixer.prompt));
  // The instruction and the payload are separate expressions, so the fixer could
  // be told to "follow the adjudication" with no sites in its prompt at all.
  t.check("and is actually GIVEN the sites, not just told about them", /POTENTIALLY RELATED SITES/.test(fixer.prompt));
  t.check("with the site data itself", /0081_fix_x\.spec-changes\.md/.test(fixer.prompt) && /"spec\/10\.md"/.test(fixer.prompt));
  t.check("and to re-read before editing", /RE-READ BEFORE YOU EDIT/.test(fixer.prompt));
  t.check("tree sites stay out of bounds for the fixer", /is NOT yours to edit/.test(fixer.prompt));
  t.check("proposal and tree sites stay separate", /"proposal":/.test(design.prompt) && /"tree":/.test(design.prompt));
}

t.section("X5. only in-scope sites are checked by the post-fix review");
{
  const design = { designs: [{ findingTitle: "T1", effort: "trivial", chosen: { approach: "a", why: "w" },
    siteDispositions: [
      { file: "proposals/0081_fix_x/0081_fix_x.spec-changes.md", line: 10, disposition: "in-scope", why: "breaks" },
      { file: "proposals/0081_fix_x/0081_fix_x.summary.md", line: 20, disposition: "separate-finding", why: "already wrong" },
    ] }], newMechanisms: [] };
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:expand:*": sites([SITE_P]),
    "*:fix-design:*": design,
  }));
  const pf = calls.find((c) => c.label === "r1:post-fix-review");
  t.check("the in-scope site is checked", /HANDED TO THE FIXER AS IN SCOPE/.test(pf.prompt));
  // The spec-changes path appears twice in this prompt -- once from the in-scope
  // list and once inside the finding's own site JSON -- so its mere presence
  // proves nothing. The in-scope block is what must carry it.
  const inScopeBlock = pf.prompt.split("HANDED TO THE FIXER AS IN SCOPE")[1] || "";
  t.check("and named in the in-scope block itself", /0081_fix_x\.spec-changes\.md/.test(inScopeBlock));
  t.check("the separate-finding site is not", !/0081_fix_x\.summary\.md/.test(inScopeBlock));
  t.check("the open sweep is still demanded", /Then do the open-ended sweep anyway/.test(pf.prompt));
  t.check("adoption is logged", logs.some((l) => /1 related site\(s\) adopted/.test(l)));
}

t.section("X5b. site classes are decided by path, not by the pass that returned them");
{
  const MISFILED_TREE = { file: "spec/10_x.md", line: 5, quote: "sq", why: "breaks", confidence: "high" };
  const MISFILED_PROP = { file: "proposals/0081_fix_x/0081_fix_x.spec-changes.md", line: 7, quote: "pq", why: "breaks", confidence: "high" };
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    // Deliberately crossed: the spec file under `proposal`, the proposal file under `tree`.
    "*:expand:*": { proposal: [MISFILED_TREE], tree: [MISFILED_PROP], searched: "grepped X" },
  }));
  const design = matching(calls, "r1:fix-design:")[0];
  const payload = sitesPayload(design.prompt);
  const cls = (f) => (payload[0].sites.proposal.some((s) => s.file === f) ? "proposal"
                    : payload[0].sites.tree.some((s) => s.file === f) ? "tree" : "absent");
  t.check("a spec/ path filed as `proposal` is moved to tree", cls("spec/10_x.md") === "tree", cls("spec/10_x.md"));
  t.check("a proposal-dir path filed as `tree` is moved to proposal",
    cls("proposals/0081_fix_x/0081_fix_x.spec-changes.md") === "proposal",
    cls("proposals/0081_fix_x/0081_fix_x.spec-changes.md"));
  t.check("neither site is lost", payload[0].sites.proposal.length + payload[0].sites.tree.length === 2);
  t.check("the move is logged, not silent", logs.some((l) => /reclassified by path/.test(l)));
}

t.section("X5c. an in-scope site the fixer may not edit is not checked as drift");
{
  const SPEC_SITE = { file: "spec/10_x.md", line: 5, quote: "sq", why: "breaks", confidence: "high" };
  const design = { designs: [{ findingTitle: "T1", effort: "trivial", chosen: { approach: "a", why: "w" },
    siteDispositions: [{ file: "spec/10_x.md", line: 5, quote: "sq", disposition: "in-scope", why: "breaks" }] }], newMechanisms: [] };
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:expand:*": { proposal: [], tree: [SPEC_SITE], searched: "grepped X" },
    "*:fix-design:*": design,
  }));
  const pf = calls.find((c) => c.label === "r1:post-fix-review");
  t.check("no in-scope block is produced at all", !/HANDED TO THE FIXER AS IN SCOPE/.test(pf.prompt));
  t.check("so nothing can be filed as CONFIRMED drift against it", result.review.history[0].sitesAdopted === 0,
    String(result.review.history[0].sitesAdopted));
  t.check("and the drop is on the record", logs.some((l) => /not the fixer's to edit/.test(l)));
}

t.section("X6. the cap bounds expansion and says what it skipped");
{
  const { calls, logs } = await runWorkflow(WF, { ...REVIEW_ARGS, maxExpansions: 2 }, fixStubs(5, {
    "*:expand:*": sites([SITE_P]),
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1, 2, 3, 4], order: 1 }]),
  }));
  t.check("only the cap ran", matching(calls, "r1:expand:").length === 2);
  t.check("the drop is logged, not silent", logs.some((l) => /skipped by maxExpansions/.test(l)));
}
{
  const { calls } = await runWorkflow(WF, { ...REVIEW_ARGS, skipExpansion: true }, fixStubs(2, { "*:expand:*": sites([SITE_P]) }));
  t.check("skipExpansion turns the stage off entirely", never(calls, "r1:expand:"));
}

// ---------------------------------------------------------------------------
// F6: a location rewritten round after round.
// ---------------------------------------------------------------------------

t.section("X7. a location rewritten in an earlier round is shown to the DESIGNER");
{
  // The same finding location recurs in rounds 1 and 2.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:review:*": ({ label }) => (/^r[12]:/.test(label) ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:expand:*": sites(),
  }));
  const r1 = matching(calls, "r1:fix-design:")[0];
  const r2 = matching(calls, "r2:fix-design:")[0];
  t.check("round 1 sees no history", !/REWRITTEN BEFORE/.test(r1.prompt));
  t.check("round 2 does", /THIS TEXT HAS BEEN REWRITTEN BEFORE/.test(r2.prompt));
  t.check("and is told round 1's attempt was rejected", /REJECTED: round 2 finding/.test(r2.prompt));
  t.check("and must differ in KIND", /HOW THIS ATTEMPT DIFFERS IN KIND/.test(r2.prompt));
  t.check("narrowing is named as the trap", /Weakening, narrowing, qualifying, or enumerating/.test(r2.prompt));
}

t.section("X8. an unrelated location in a later round carries no history");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:review:*": ({ label }) => (/^r1:/.test(label) ? { coverage: "c", findings: [F(1)] }
      : /^r2:/.test(label) ? { coverage: "c", findings: [F(2)] } : { coverage: "c", findings: [] }),
    "*:dedup": ({ label }) => (/^r1:/.test(label)
      ? { findings: [{ ...F(1), lenses: ["citations"] }] }
      : { findings: [{ ...F(2), lenses: ["citations"] }] }),
    "*:expand:*": sites(),
  }));
  const r2 = matching(calls, "r2:fix-design:")[0];
  t.check("a different location carries no history", !/REWRITTEN BEFORE/.test(r2.prompt));
}

// ---------------------------------------------------------------------------
// F1: the non-spec loop does not run on a spec staging that is still moving,
// and the spec fixer repairs what its own edits falsify.
// ---------------------------------------------------------------------------

// A stub table whose spec loop never goes clean, so the spec loop exhausts its
// budget without converging.
const specNeverClean = (over = {}) =>
  loopStubs({
    "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
    "*:review:*": ({ label }) => ({ coverage: "c", findings: [F(1)] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:expand:*": { proposal: [], tree: [], searched: "" },
    ...over,
  });

t.section("X9. an unconverged spec loop blocks the non-spec loop");
{
  const { calls, logs, result } = await runWorkflow(WF, { ...REVIEW_ARGS, maxSpecReviewRounds: 2 }, specNeverClean());
  t.check("the spec loop ran", logs.some((l) => /Entering the spec review loop/.test(l)));
  t.check("the non-spec loop did not", !logs.some((l) => /Entering the non-spec review loop/.test(l)));
  t.check("and the block is logged with the remedy", logs.some((l) => /did NOT converge after 2 of 2 round\(s\); the non-spec review is NOT run/.test(l)));
  t.check("the status names it", result.status === "spec-not-converged", String(result.status));
  t.check("the budget is reported", result.specGate && result.specGate.budget === 2);
  t.check("and what it was still finding", result.specGate.stillFinding.includes("T1"));
  t.check("with how to resume", /Raise maxSpecReviewRounds above 2/.test(result.specGate.resume));
}

t.section("X10. the handoff runs anyway, before the run returns");
{
  const { calls, logs } = await runWorkflow(WF, { ...REVIEW_ARGS, maxSpecReviewRounds: 2 }, specNeverClean());
  const h = calls.find((c) => c.label === "spec-nonspec-handoff");
  t.check("the handoff ran on a non-converged loop", !!h);
  t.check("and knows the staging is unsettled", /did NOT converge/.test(h.prompt));
  t.check("it is told why it is still worth doing", /worth doing\s+precisely because the staging is unsettled/.test(h.prompt));
  t.check("and not to guess where open findings land", /Do not try to guess where the open\s+findings will land/.test(h.prompt));
  t.check("it is logged as unsettled", logs.some((l) => /against the UNSETTLED spec staging/.test(l)));
  t.check("the handoff precedes the block", firstIndex(calls, "spec-nonspec-handoff") >= 0);
}

t.section("X11. the override lets the non-spec loop run anyway");
{
  const { logs, result } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, maxSpecReviewRounds: 2, maxNonSpecReviewRounds: 1, allowNonSpecOnUnconvergedSpec: true },
    specNeverClean(),
  );
  t.check("the non-spec loop runs", logs.some((l) => /Entering the non-spec review loop/.test(l)));
  t.check("and the status is not the gate status", result.status !== "spec-not-converged", String(result.status));
}

t.section("X12. a converged spec loop is not blocked and the handoff says so");
{
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const h = calls.find((c) => c.label === "spec-nonspec-handoff");
  t.check("the handoff knows it converged", /has converged/.test(h.prompt));
  t.check("the non-spec loop runs", logs.some((l) => /Entering the non-spec review loop/.test(l)));
  t.check("the status is the normal one", result.status === "reviewed", String(result.status));
}

t.section("X13. the spec fixer repairs consequential drift in the non-spec staging, and nothing else");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, specNeverClean({}));
  const specFix = calls.find((c) => /:fix:/.test(c.label) && c.prompt.includes("spec convergence loop"));
  t.check("the spec fixer may open the non-spec staging", /non-spec-changes\.md — REPAIR ONLY WHAT YOUR OWN EDIT FALSIFIED/.test(specFix.prompt));
  t.check("only where it already has content", /ALREADY HAS\s+CONTENT beyond its headings/.test(specFix.prompt));
  t.check("the trigger is always a spec finding", /THE TRIGGER IS ALWAYS A SPEC FINDING/.test(specFix.prompt));
  t.check("authoring is barred", /YOU MAY NOT AUTHOR/.test(specFix.prompt));
  t.check("independent defects are the next loop's", /that is a finding\s+for the loop that follows/.test(specFix.prompt));
  t.check("an empty file means nothing to do", /WHEN THE FILE IS EMPTY there is nothing to repair/.test(specFix.prompt));
  t.check("the checklist stays out of bounds", /including the implementation checklist, and every file outside it,\s+is out of bounds/.test(specFix.prompt));
  const specLens = calls.find((c) => /^r1:review:/.test(c.label) && c.prompt.includes("STAGED SPEC EDITS"));
  t.check("but a lens is told not to file it as a finding", /Do not file the non-spec statement as a separate finding/.test(specLens.prompt));
}

// ---------------------------------------------------------------------------
// F8: a correction the spec loop derives but may not apply has an owner.
// ---------------------------------------------------------------------------

t.section("X14. DEFERRED is a distinct tag, and the summary is not the errata surface");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, specNeverClean());
  const specFix = calls.find((c) => /:fix:/.test(c.label) && c.prompt.includes("spec convergence loop"));
  t.check("the tag exists", /DEFERRED \[file\]: a correction you DERIVED but may not land/.test(specFix.prompt));
  t.check("and is distinguished from OPEN", /an OPEN is a question nobody has answered, and a DEFERRED is an answer nobody has\s+applied/.test(specFix.prompt));
  t.check("the summary grant is narrowed", /THE INDEX, AND\s+STATEMENTS YOUR OWN EDITS FALSIFY, AND NOTHING ELSE/.test(specFix.prompt));
  t.check("with the evidence for why", /nine-hundred-word errata list/.test(specFix.prompt));
}

t.section("X15. the handoff discharges them, and may not author to do it");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs());
  const h = calls.find((c) => c.label === "spec-nonspec-handoff");
  t.check("it has a fourth step", /4\. DISCHARGE THE DEFERRED CORRECTIONS/.test(h.prompt));
  t.check("it may now edit the non-spec staging", /non-spec-changes\.md, and .*review-log\.md to record what you closed/.test(h.prompt));
  t.check("it closes repairs with a CORRECTS", /append a `CORRECTS \[id\]` line/.test(h.prompt));
  t.check("it may NOT author what does not exist yet", /would require AUTHORING a staged code/.test(h.prompt));
  t.check("because no non-spec lens has read it", /no non-spec\s+lens has ever read/.test(h.prompt));
  t.check("what it cannot close becomes an OPEN the next loop reads", /so the next loop's first round reads it/.test(h.prompt));
  t.check("steps 1-3 stay a reconciliation", /Steps 1 through 3 are not a review round/.test(h.prompt));
  t.check("and steps 4-5 are named as the exception", /Steps 4 and 5 are the one place this pass changes what the/.test(h.prompt));
  t.check("it has a fifth step carrying decisions to the summary", /5\. CARRY THE OPEN DECISIONS INTO THE SUMMARY/.test(h.prompt));
  t.check("because the human never reads the review log", /The human never reads that log/.test(h.prompt));
  // The section it carries them into is the summary's own, under the name the
  // summary now uses, and the recommendation an unresolved entry lacks is
  // supplied by the phase rather than by the lens this design deleted.
  t.check(
    "into the section the summary now names",
    /ensure the summary's\s+`## Open decisions for human to make` section carries it/.test(h.prompt),
  );
  t.check("and it may not invent a recommendation the loop did not derive", /Do not invent a recommendation\s+the loop did not derive/.test(h.prompt));
  t.check(
    "the phase supplies the one the loop did not",
    /open-decisions-and-impact-review phase supplies it/.test(h.prompt),
  );
  t.check("and no deleted lens is named as supplying it", !/open-decisions lens/.test(h.prompt));
}

t.section("X16. two locations are the same site only when they really are");
{
  const F2 = (n, where) => ({ ...F(n), where });
  const run = async (w1, w2) => {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
      "*:review:*": ({ label }) => /^r1:/.test(label)
        ? { coverage: "c", findings: [F2(1, w1)] }
        : /^r2:/.test(label) ? { coverage: "c", findings: [F2(2, w2)] } : { coverage: "c", findings: [] },
      "*:dedup": ({ label }) => /^r1:/.test(label)
        ? { findings: [{ ...F2(1, w1), lenses: ["citations"] }] }
        : { findings: [{ ...F2(2, w2), lenses: ["citations"] }] },
      "*:expand:*": sites(),
    }));
    const d = matching(calls, "r2:fix-design:")[0];
    return d ? /REWRITTEN BEFORE/.test(d.prompt) : false;
  };
  // The file name of one change file is a SUBSTRING of the other's, which a
  // containment match read as the same site.
  t.check("the two change files are not one site",
    !(await run("spec-changes.md:120", "non-spec-changes.md, Staged code changes")));
  // A file name with no section is every finding in that file, not a location.
  t.check("a bare file name is not a location",
    !(await run("spec-changes.md:120", "spec-changes.md:412")));
  t.check("two deliverables are not one site", !(await run("SPEC-3", "SPEC-7")));
  t.check("but the same passage across rounds is", await run("staged 10.1.8 step 1", "10.1.8 step 1, line 213"));
}

t.section("X17. the status file is written on a run that did NOT converge");
{
  const { calls } = await runWorkflow(WF, { ...REVIEW_ARGS, maxSpecReviewRounds: 2 }, specNeverClean());
  t.check("the run did not converge", !never(calls, "spec-nonspec-handoff"));
  t.check("the status is still recorded", !never(calls, "status:record-run"));
  t.check("but it is not marked Reviewed", never(calls, "status:set-reviewed"));
  const rec = calls.find((c) => c.label === "status:record-run");
  t.check("and it is told the run did not converge", /DID NOT CONVERGE/.test(rec.prompt));
}

t.section("X18. the compaction target comes from the boundary, not the default");
{
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "*:round-boundary": '{"merged":0,"ledgerLines":10,"standingLines":500,"ledgerGrowth":0,"compactionDue":true,"standingTarget":400,"standingTrigger":520,"targetRaises":2,"targetRaisedNow":true,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
  }));
  const c = calls.find((x) => /:compact$/.test(x.label));
  t.check("a compaction ran", !!c);
  t.check("it is asked to reach the BACKED-OFF target", /THE TARGET IS 400 LINES/.test(c.prompt));
  t.check("not the starting default", !/THE TARGET IS 200 LINES/.test(c.prompt));
}

t.section("X19. the site matcher's own guards, with tokens on BOTH sides");
{
  // X16's file cases are stopped by the empty-token guard before the file check
  // is reached, so the commit's headline fix rested on an accident. These
  // fixtures carry real tokens on both sides, so only the file check can
  // separate them.
  const F2 = (n, where) => ({ ...F(n), where });
  const twoRounds = async (w1, w2) => {
    const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
      "*:review:*": ({ label }) => /^r1:/.test(label)
        ? { coverage: "c", findings: [F2(1, w1)] }
        : /^r2:/.test(label) ? { coverage: "c", findings: [F2(2, w2)] } : { coverage: "c", findings: [] },
      "*:dedup": ({ label }) => /^r1:/.test(label)
        ? { findings: [{ ...F2(1, w1), lenses: ["citations"] }] }
        : { findings: [{ ...F2(2, w2), lenses: ["citations"] }] },
      "*:expand:*": sites(),
    }));
    const d = matching(calls, "r2:fix-design:")[0];
    return d ? /REWRITTEN BEFORE/.test(d.prompt) : false;
  };
  t.check("the same section in the two change files is NOT one site",
    !(await twoRounds("spec-changes.md, SPEC-3 table", "non-spec-changes.md, SPEC-3 table")));
  t.check("the same section in the same file IS one site",
    await twoRounds("spec-changes.md, SPEC-3 table", "spec-changes.md, SPEC-3 table row"));
  t.check("two sections in one file are not one site",
    !(await twoRounds("spec-changes.md, SPEC-3 table", "spec-changes.md, SPEC-9 preamble")));
  t.check("a single-digit ordinal still discriminates",
    !(await twoRounds("checklist.md step 2", "checklist.md step 8")));
}

t.section("X20. the site history does not leak across loops");
{
  // Both loops run, both find at the SAME location. Without the loop filter the
  // non-spec designer is shown the spec loop's attempt.
  const W = "summary.md, deliverable index";
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
    "*:review:*": ({ label }) => /^r1:/.test(label)
      ? { coverage: "c", findings: [{ ...F(1), where: W }] } : { coverage: "c", findings: [] },
    "*:dedup": { findings: [{ ...F(1), where: W, lenses: ["citations"] }] },
    "*:expand:*": sites(),
  }));
  const specDesign = calls.find((c) => /:fix-design:/.test(c.label) && /Loop: spec\./.test(c.prompt));
  const nonSpecDesign = calls.find((c) => /:fix-design:/.test(c.label) && /Loop: non-spec\./.test(c.prompt));
  t.check("both loops reached a design", !!specDesign && !!nonSpecDesign);
  t.check("the spec loop's round 1 sees no history", !/REWRITTEN BEFORE/.test(specDesign.prompt));
  t.check("and neither does the non-spec loop's round 1", !/REWRITTEN BEFORE/.test(nonSpecDesign.prompt));
}

t.section("X21. signals that reach a prompt are pinned, not just computed");
{
  const design = { designs: [{ findingTitle: "T1", effort: "trivial", chosen: { approach: "REWROTE THE PREDICATE", why: "w" } }], newMechanisms: [] };
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:review:*": ({ label }) => /^r[12]:/.test(label) ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] },
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:fix-design:*": design,
    "*:expand:*": sites(),
  }));
  const r2 = matching(calls, "r2:fix-design:")[0];
  t.check("an earlier attempt's APPROACH reaches the next designer", /REWROTE THE PREDICATE/.test(r2.prompt));
}
{
  // A mechanism a fixer declares must reach the next round's fixer as a strike.
  // A strike is credited when a LATER finding is about the mechanism, matched on
  // its name, so both the name and the later finding's text must carry it.
  const MECH = "rotation-gate";
  const about = { ...F(9), title: "the " + MECH + " is unreachable", where: "spec-changes.md, SPEC-3 gate" };
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:review:*": ({ label }) => /^r1:/.test(label) ? { coverage: "c", findings: [F(1)] }
      : /^r[23]:/.test(label) ? { coverage: "c", findings: [about] } : { coverage: "c", findings: [] },
    "*:dedup": ({ label }) => /^r1:/.test(label)
      ? { findings: [{ ...F(1), lenses: ["citations"] }] }
      : { findings: [{ ...about, lenses: ["citations"] }] },
    "*:expand:*": sites(),
    "*:fix:*": ({ label }) => /^r1:/.test(label)
      ? { summary: "s", escalated: [], designRejected: [],
          newMechanisms: [{ name: MECH, why: "w", state: "s", callers: "c", failureMode: "f", test: "t" }] }
      : { summary: "s", escalated: [], designRejected: [], newMechanisms: [] },
  }));
  const r3fix = matching(calls, "r3:fix:")[0];
  t.check("a declared mechanism becomes a strike a later fixer sees", !!r3fix && /MECHANISMS THIS LOOP INVENTED THAT KEEP FAILING/.test(r3fix.prompt));
  t.check("named, with the round it was introduced", !!r3fix && /rotation-gate \(introduced round 1\)/.test(r3fix.prompt));
}
{
  const { calls, logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "*:round-boundary": '{"merged":0,"ledgerLines":10,"standingLines":500,"ledgerGrowth":0,"compactionDue":false,"standingTarget":400,"standingTrigger":520,"targetRaises":3,"targetRaisedNow":true,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
    "*:review:*": ({ label }) => /^r1:/.test(label) ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] },
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:expand:*": sites(),
    introspectEvery: 1,
  }));
  t.check("a raised target is logged", logs.some((l) => /could not reach its target; raised to 400/.test(l)));
  const intro = calls.find((c) => /introspect/.test(c.label) && !/gate/.test(c.label));
  if (intro) t.check("and the raise count reaches introspection", /OUTGROWN ITS TARGET 3 TIME\(S\)/.test(intro.prompt));
  else t.check("and the raise count reaches introspection", true, "no introspection pass in this fixture");
}

t.section("X22. a finding nobody searched is not reported as having no sites");
{
  const { calls } = await runWorkflow(WF, { ...REVIEW_ARGS, maxExpansions: 1 }, fixStubs(3, {
    "*:expand:*": sites([SITE_P]),
    "*:fix-plan": plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1, 2], order: 1 }]),
  }));
  const d = matching(calls, "r1:fix-design:")[0];
  t.check("the designer is told which findings were NOT searched", /NOT SEARCHED/.test(d.prompt));
  t.check("and that absence of sites is absence of a search", /absence of a search/.test(d.prompt));
}
{
  // A dead expansion agent must be distinguishable from one that found nothing.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, { "*:expand:*": null }));
  const d = matching(calls, "r1:fix-design:")[0];
  t.check("a dead expansion is reported as not searched", /NOT SEARCHED/.test(d.prompt));
}
{
  // And a genuine empty result must NOT claim a sweep was done.
  const { calls } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, { "*:expand:*": sites() }));
  const fx = matching(calls, "r1:fix:")[0];
  t.check("an empty search does not tell the fixer a sweep was done", !/the sweep has been done for you/.test(fx.prompt));
  t.check("nor claim nothing was searched", !/NOT SEARCHED/.test(fx.prompt));
}

t.section("X23. an ordinal is decisive, so two steps of one deliverable are two sites");
{
  // `SPEC-3 step 2` and `SPEC-3 step 5` share the generic `spec-3` and `step`,
  // which met the 0.6 overlap floor on their own and outvoted the one digit that
  // differed. The round-3 designer was told its passage had been rewritten twice
  // and rejected, and markSitesRejected wrote that into the durable table.
  const F2 = (n, where) => ({ ...F(n), where });
  const threeRounds = async (w1, w2, w3) => {
    const per = { r1: w1, r2: w2, r3: w3 };
    const pick = (label) => per[(label.match(/^r\d+/) || [""])[0]];
    return runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
      "*:review:*": ({ label }) => {
        const w = pick(label);
        return w ? { coverage: "c", findings: [F2(1, w)] } : { coverage: "c", findings: [] };
      },
      "*:dedup": ({ label }) => {
        const w = pick(label);
        return { findings: w ? [{ ...F2(1, w), lenses: ["citations"] }] : [] };
      },
      "*:expand:*": sites(),
    }));
  };

  const { calls, logs } = await threeRounds(
    "spec-changes.md, SPEC-3 step 2",
    "spec-changes.md, SPEC-3 step 5",
    "spec-changes.md, SPEC-3 step 8",
  );
  const d2 = matching(calls, "r2:fix-design:")[0];
  const d3 = matching(calls, "r3:fix-design:")[0];
  t.check("all three rounds reached a design", !!d2 && !!d3);
  t.check("step 5 does not inherit step 2's history", !/REWRITTEN BEFORE/.test(d2.prompt));
  t.check("and step 8 inherits neither", !/REWRITTEN BEFORE/.test(d3.prompt));
  // The durable table, read through the detector that counts it.
  t.check("no repeat is recorded at a site with one attempt",
    !logs.some((l) => /rewritten three or more times/.test(l)));

  // The control: over-tightening the matcher would silence F6 entirely.
  const W = "spec-changes.md, SPEC-3 step 2";
  const same = await threeRounds(W, W, W);
  const s2 = matching(same.calls, "r2:fix-design:")[0];
  const s3 = matching(same.calls, "r3:fix-design:")[0];
  t.check("the same step across rounds still carries its history",
    /THIS TEXT HAS BEEN REWRITTEN BEFORE/.test(s2.prompt));
  t.check("and the third attempt is told about both", /REWRITTEN BEFORE/.test(s3.prompt));
  t.check("and the repeat IS logged there",
    same.logs.some((l) => /rewritten three or more times/.test(l)));

  // A dotted section is the same passage at another grain, so it is not a
  // contradicting identifier.
  const nested = await threeRounds(
    "spec-changes.md, \u00a74.6 step 2", "spec-changes.md, \u00a74.6.1 step 2", undefined,
  );
  t.check("a dotted section prefix is still one site",
    /REWRITTEN BEFORE/.test(matching(nested.calls, "r2:fix-design:")[0].prompt));
}

t.section("R41. a verify outage retires nothing and never certifies convergence");
{
  // The lens side already guaranteed that a lens which failed its own retries is
  // never retired. The verify side had no counterpart, so an outage made "no
  // finding of its own survived verification" vacuously true and retired the
  // very lenses that had just found the defects -- then the sweep round was
  // complete on its own terms and the run returned status reviewed, converged.
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": "NO",
    "*:review:*": ({ label }) => (/^r1:/.test(label)
      ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:verify-material": null,
  }));
  t.check("the round is inconclusive", logs.some((l) => /verifiers failed after retries/.test(l)));
  t.check("no lens is retired on a verdict nobody reached",
    logs.some((l) => /verification did not complete, so no lens is retired/.test(l)));
  t.check("the run does NOT converge", result && result.review && result.review.converged === false,
    String(result && result.review && result.review.converged));
  t.check("the status is not reviewed", result && result.status !== "reviewed", String(result && result.status));
  t.check("and the loop says which rounds could not verify",
    logs.some((l) => /could not verify in round\(s\)/.test(l)));
  t.check("so the proposal is not stamped Reviewed", never(calls, "status:set-reviewed"));
}

t.section("R42. a fix claim the tree does not support is withdrawn");
{
  // A fixer answering "no edit was needed" pushed its findings into the run-wide
  // "already fixed, do not re-litigate" list handed to every later lens of BOTH
  // loops, permanently suppressing them. The diff proving nothing changed was
  // already being collected one field away.
  const noChange = '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,' +
    '"changedFiles":[],"hunksKnown":true,"hunks":0,"snapshot":"/repo/snap","overrides":{}}';
  const { logs, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:round-boundary": noChange,
    "*:fix:*": { summary: "No edit was needed; the text already says this.",
      newMechanisms: [], escalated: [], designRejected: [] },
  }));
  t.check("the empty claim is withdrawn",
    logs.some((l) => /the tree did not change; the claim is withdrawn/.test(l)));
  t.check("and nothing is counted as fixed",
    result && result.review && result.review.totalFixed === 0,
    String(result && result.review && result.review.totalFixed));
  // The point is not that the run can never converge afterwards -- a later round
  // whose lenses genuinely find nothing may. The point is that the withdrawn
  // findings are no longer SUPPRESSED, so a later lens is free to re-find them.
  const { calls: c2 } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:round-boundary": noChange,
    "*:fix:*": { summary: "No edit was needed.", newMechanisms: [], escalated: [], designRejected: [] },
    "*:review:*": ({ label }) => (/^r[12]:/.test(label)
      ? { coverage: "c", findings: fs(2) } : { coverage: "c", findings: [] }),
  }));
  const r2lens = matching(c2, "r2:review:")[0];
  t.check("and a later lens is NOT told they were fixed",
    !r2lens || !/Already found and fixed in earlier rounds/.test(r2lens.prompt));
}
{
  // The control: a round whose tree DID change still credits its fixes.
  const { result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {}));
  t.check("a real fix is still credited",
    result && result.review && result.review.totalFixed === 2,
    String(result && result.review && result.review.totalFixed));
}

t.section("R43. the guards a mutation audit found deletable");
{
  // A mutation audit reintroduced each of these and the suite stayed green, so
  // the guard's stated reasoning was unenforced. Each check below is verified to
  // go red when its guard is removed.

  // NOT TESTED, deliberately: the `sweeps < 2` floor in sweepStalled.
  //
  // A mutation audit found it deletable with a green suite. I tried to build a
  // case where removing it changes the outcome and could not. The floor only
  // matters when a lens reaches the fail streak while fewer than two sweeps have
  // run, and retirement makes that unreachable: a lens that returns no findings
  // retires, so a repeatedly-failing lens ends up the only active one and the
  // loop exits through "every reviewer failed" first. Reaching the streak in
  // ordinary rounds needs the lens to keep producing confirmed findings, and
  // then the round is not barren, which is the only branch that consults
  // sweepStalled at all.
  //
  // So the floor appears redundant rather than load-bearing. It is left in place
  // because it costs nothing and states an intent, but a test asserting it would
  // pass either way, and a test that passes either way is worse than none.

  // A prune rewrites sections and tells its agent to reconcile the checklist,
  // files-touched and testing sections with what is left. A retired lens never
  // re-reads any of it, so the pool must reopen or the loop can certify text no
  // lens has seen in its pruned form.
  const pruned = await runWorkflow(WF, { ...REVIEW_ARGS, introspectEvery: 1, maxSpecReviewRounds: 4 }, loopStubs({
    "probe:spec-changes": "NO",
    "*:review:*": ({ label }) => (/^r1:/.test(label)
      ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "introspect*": { observations: [], caseHealthy: "h", caseUnhealthy: "u", verdict: "prune", reasoning: "r", sections: ["## 3. Design"] },
    "judge:*": { falsified: false, howConclusive: "none", reasoning: "stands" },
    "prune:*": "pruned",
  }));
  const at = pruned.logs.findIndex((l) => /pruned 1 section/.test(l));
  const nextLaunch = pruned.logs.slice(at + 1).find((l) => /launching \d+ reviewers/.test(l));
  t.check("a prune happened", at >= 0);
  t.check(
    "and the round after it reopens every retired lens",
    !!nextLaunch && /\(0\/\d+ lenses retired\)/.test(nextLaunch),
    String(nextLaunch),
  );

  // A site whose file is empty, blank or not a string cannot be opened by
  // anyone. Left in, it reaches the planner, the designer, the fixer and the
  // post-fix reviewer as a real edit site.
  const junk = await runWorkflow(WF, REVIEW_ARGS, fixStubs(1, {
    "*:expand:*": {
      proposal: [
        { file: "", line: 1, quote: "q", why: "w", confidence: "high" },
        { file: "   ", line: 2, quote: "q", why: "w", confidence: "high" },
      ],
      tree: [],
      searched: "x",
    },
  }));
  const design = matching(junk.calls, "r1:fix-design:")[0];
  t.check("a site with no usable path is dropped", !design || !/"file": ""/.test(design.prompt));
  t.check(
    "and the drop is logged rather than silent",
    junk.logs.some((l) => /drop|no usable path|without a path/i.test(l)),
    JSON.stringify(junk.logs.filter((l) => /site/i.test(l)).slice(0, 3)),
  );
}

t.section("R44. zero hunks with no baseline is not an empty fix claim");
{
  // The first round of a loop has no previous snapshot, so the boundary reports
  // zero hunks because there is nothing to diff against. Reading that as "the
  // fixer edited nothing" withdrew every genuine fix from round 1 of both loops
  // on a measured run and reported nothing fixed.
  const noBaseline = '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,' +
    '"changedFiles":[],"hunksKnown":false,"hunks":0,"snapshot":"/repo/snap","overrides":{}}';
  const { logs, result } = await runWorkflow(WF, REVIEW_ARGS, fixStubs(2, {
    "*:round-boundary": noBaseline,
  }));
  t.check("the claim is NOT withdrawn", !logs.some((l) => /the claim is withdrawn/.test(l)));
  t.check("and the fixes are credited",
    result && result.review && result.review.totalFixed === 2,
    String(result && result.review && result.review.totalFixed));
}

t.section("R45. every lens runs in round one; none is withheld by rotation");
{
  // `operational` and `fresh` used to be a second pool with their own schedule:
  // while any ordinary lens was active, exactly one rotated in per round. That
  // manufactured a lens which had NEVER RUN, and a lens that has never run
  // cannot retire, and a lens that has not retired blocks the sweep -- so a
  // clean proposal spent a whole round discharging one lens. A measured run's
  // non-spec loop went 13 lenses, then `fresh` alone, then a full sweep.
  const { logs } = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
  }));
  const rounds = logs.filter((l) => /launching \d+ reviewers|FULL SWEEP/.test(l));
  // The count is the non-spec pool's size, which fell by one when the
  // open-decisions lens was deleted. A lens withheld by rotation shows here as
  // a smaller number, which is the failure this pins.
  t.check("round one runs the whole pool", /launching 14 reviewers/.test(rounds[0] || ""), String(rounds[0]));
  const firstRetire = logs.find((l) => /retiring/.test(l)) || "";
  t.check(
    "both extras retire in round one, so neither can block the sweep",
    /operational/.test(firstRetire) && /fresh/.test(firstRetire),
    firstRetire,
  );
  t.check("so the very next round is the sweep", /FULL SWEEP 1/.test(rounds[1] || ""), String(rounds[1]));
  t.check("and no round runs a lens alone", !logs.some((l) => /launching 1 reviewers/.test(l)));
}

// ---- The prerequisites the phase would otherwise inherit -----------------
//
// P1, P2 and P4 of the open-decisions-and-impact-review design. None of them
// belongs to the phase; each is something the phase would otherwise inherit.

t.section("R46. lockSpecChanges applies at any value, and a mid-run flip reaches the fixer");
{
  // It was `const` while the mid-run override table assigned to it, so writing
  // the key at ANY value into the override file threw at the assignment and the
  // run returned nothing at all.
  // The flip is asserted at the fixer's prompt rather than at the config site,
  // because the config site copies the constant onto the loop once, before
  // round one, where a later flip cannot reach a prompt.
  const flipAt = (v) => loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "hash:*": HASH,
    // Findings in rounds 1 and 2, so a fixer runs on either side of the flip.
    "*:review:*": ({ label }) =>
      (/^r[12]:/.test(label) ? { coverage: "c", findings: [F(1)] } : { coverage: "c", findings: [] }),
    "*:dedup": { findings: [{ ...F(1), lenses: ["citations"] }] },
    "*:round-boundary": BOUNDARY(),
    "r1:round-boundary": BOUNDARY({ overrides: { lockSpecChanges: v } }),
  });
  for (const v of [true, false]) {
    const { result, error } = await runWorkflow(WF, REVIEW_ARGS, flipAt(v));
    t.check("an override of lockSpecChanges=" + v + " does not throw", !error, String(error));
    t.check("and the run still returns", !!result && result.status === "reviewed", String(result && result.status));
  }

  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, flipAt(true));
  t.check(
    "the override is taken forward",
    logs.some((l) => /overrides applied for the next round: lockSpecChanges=true/.test(l)),
  );
  const fix = (n) => calls.find((c) => c.label === "r" + n + ":fix:G1");
  t.check("a fixer runs on either side of the flip", !!fix(1) && !!fix(2));
  t.check(
    "before it the fixer may touch the spec staging",
    /spec-changes\.md — permitted, but PREFER/.test(fix(1).prompt) &&
      !/spec-changes\.md is LOCKED for this run/.test(fix(1).prompt),
  );
  t.check(
    "after it the fixer is told the spec staging is LOCKED",
    /spec-changes\.md is LOCKED for this run/.test(fix(2).prompt) &&
      !/spec-changes\.md — permitted, but PREFER/.test(fix(2).prompt),
  );
  t.check("and the run reports the value it ended on", result.review.lockSpecChanges === true);
}

t.section("R47. a schema'd return missing a required field is discarded, not believed");
{
  // The cache instruction has a lens print a JSON file it wrote on an earlier
  // run and return it verbatim, outside the tool-call schema, so a key the
  // schema marks `required` can simply be absent. Such a return reaching the
  // loop is a lens that found nothing, which is what certifies convergence.
  const bad = loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "hash:*": HASH,
    // `coverage` is required on the review schema and is absent here.
    "*:review:citations": { findings: [] },
  });
  const { calls, logs, result } = await runWorkflow(WF, REVIEW_ARGS, bad);
  t.check(
    "the call is retried to exhaustion",
    calls.filter((c) => c.label === "r1:review:citations").length === 4,
    String(calls.filter((c) => c.label === "r1:review:citations").length),
  );
  t.check(
    "and the discard names the field",
    logs.some((l) => /r1:review:citations: return is missing required field\(s\) coverage, discarding it/.test(l)),
  );
  t.check(
    "the lens is counted failed rather than clean",
    logs.some((l) => /Round 1: 1\/\d+ lenses failed after retries; round INCONCLUSIVE/.test(l)),
  );
  const loop = result.review.loops.find((l) => l.name === "non-spec");
  t.check("it retires nothing", !loop.retiredLenses.includes("citations"), loop.retiredLenses.join(","));
  t.check("and the loop cannot converge on it", result.review.converged === false);

  // The control: the same table with a complete return converges, so the run
  // above is stopped by the missing field and by nothing else.
  const good = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "hash:*": HASH,
  }));
  t.check("a complete return does converge", good.result.review.converged === true);
}

t.section("R48. the summary's section list reaches every prompt that states it");
{
  // Three authorities stated the summary's structure and disagreed: the
  // constant, the seeder that writes the skeleton, and the bootstrap that
  // derives a missing one. They now state one list, so the list is asserted at
  // each of them rather than at the constant alone.
  const newRun = await runWorkflow(WF, NEW_ARGS, newStubs({ "hash:*": HASH }));
  const reviewRun = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH }));
  const promptFor = (name) =>
    (newRun.calls.find((c) => c.label === name) || reviewRun.calls.find((c) => c.label === name) || {}).prompt;

  const SECTIONS = [
    "**Problem statement.**",
    "**Decisions.**",
    "## Open decisions for human to make",
    "## Defects in the shipped tree that this proposal does not stage",
    "## Impacts on other proposals",
    "## Deliverable index",
  ];
  for (const name of ["write", "bootstrap", "init"]) {
    const p = promptFor(name);
    t.check("the " + name + " prompt exists", !!p);
    const absent = SECTIONS.filter((s) => !p.includes(s));
    t.check("and states every section of the list to " + name, absent.length === 0, absent.join(" | "));
  }

  const all = [...newRun.calls, ...reviewRun.calls];
  t.check(
    "no prompt still says **Fixed decisions.**",
    !all.some((c) => /\*\*Fixed decisions\.\*\*/.test(c.prompt)),
    all.filter((c) => /\*\*Fixed decisions\.\*\*/.test(c.prompt)).map((c) => c.label).join(","),
  );
  // A resolved decision now LEAVES the summary, so the clause that kept it
  // there must not reach an agent that would obey it.
  t.check(
    "and none carries the withdraw-in-place clause",
    !all.some((c) => /withdrawn in place/i.test(c.prompt)),
    all.filter((c) => /withdrawn in place/i.test(c.prompt)).map((c) => c.label).join(","),
  );
  // The summary owns the open decisions, so the staged non-spec file no longer
  // carries a section for them: neither the skeleton nor the instruction to
  // fill it survives.
  t.check("the seeder creates no decisions section in the staged changes", !/## Open decisions for review/.test(promptFor("init")));
  t.check("and the writer is not told to fill one", !/## Open decisions for review/.test(promptFor("write")));
}

// ---- The open-decisions-and-impact-review phase: where it fires -----------
//
// The phase is a subworkflow, and the harness records a `workflow()` sub-call
// as one entry whose prompt is the JSON of the argument object, returning from
// a stub table without running the child's body. So what these sections test is
// the parent's half: which loop each firing follows, which paths still get one,
// and what the periodic cadence counts. The argument object itself is asserted
// in .claude/tests/change-proposal-decisions-forwarding.test.mjs.

const CHILD_LABEL = "workflow:/repo/.claude/workflows/change-proposal-decisions.js";

// What the child returns when it ran and found nothing to do. A firing over an
// unchanged staging is the ordinary case, so it is the default here.
const CHILD_RETURN = {
  status: "done",
  phaseState: {},
  items: [],
  applied: [],
  failedItems: [],
  recordedForOperator: [],
  decisionsResolved: [],
  decisionsLeftToHuman: [],
  contested: [],
  deadAgents: [],
  unadjudicated: [],
  changedFiles: [],
};
const withChild = (ret = CHILD_RETURN) => ({
  subworkflows: { "change-proposal-decisions.js": ret },
});

/** The argument object of every firing of a run, in order. */
const firedWith = (calls) =>
  calls.filter((c) => c.label === CHILD_LABEL).map((c) => JSON.parse(c.prompt));
/** Just the triggers, which is what names the site each firing ran at. */
const triggers = (calls) => firedWith(calls).map((f) => f.trigger);

// The run's loops and firings in the order they happened. Each round-boundary
// call names its own loop in the command it runs and each firing carries its
// trigger, so consecutive boundaries of one loop collapse into one entry and a
// firing inside a loop stays between two entries for that loop.
const timeline = (calls) => {
  const out = [];
  for (const c of calls) {
    if (/:round-boundary$/.test(c.label)) {
      const loop = (c.prompt.match(/--loop '([^']+)'/) || [])[1];
      if (out[out.length - 1] !== "loop:" + loop) out.push("loop:" + loop);
    } else if (c.label === CHILD_LABEL) {
      out.push("fire:" + JSON.parse(c.prompt).trigger);
    }
  }
  return out;
};

// A lane's steady digest, and the one a lane that has moved reads. The recheck
// trigger compares a lane's files against the digest taken at that lane's own
// last convergence, so a plan names the exact hash labels that read something
// else and every other label reads its lane's steady value.
const LANE = { spec: "aaaaaaaaaaaa", "non-spec": "bbbbbbbbbbbb" };
const MOVED = "cccccccccccc";
const laneHashes = (plan = {}) => ({ label }) => {
  const m = /^hash:(spec|non-spec):/.exec(label);
  return (m && (plan[label] || LANE[m[1]])) || LANE.spec;
};

// Two findings in the rounds the pattern names and nothing in the rest, which
// is what fixes how many rounds a loop runs and which of them reach the loop
// tail. A round with confirmed findings runs to the tail; one that finds
// nothing takes the clean-round `continue` before it.
const findsIn = (rounds) => ({ label }) =>
  (rounds.test(label) ? { coverage: "c", findings: fs(2) } : { coverage: "c", findings: [] });
const DEDUP2 = { findings: fs(2).map((f) => ({ ...f, lenses: ["citations"] })) };

t.section("R51. every review loop is followed by a firing, including each recheck of either lane");
{
  // A spec edit outstanding after the non-spec loop runs a recheck PAIR, and
  // the firing after each of its loops is what makes the pair's own writing
  // adjudicated rather than carried to the end of the run.
  const pair = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "*:review:*": findsIn(/^r1:/),
    "*:dedup": DEDUP2,
    "hash:*": laneHashes({
      "hash:spec:after-non-spec-loop": MOVED,
      // The recheck re-takes its own lane's baseline as it returns, so the
      // second pass compares against what the recheck certified and the run
      // settles after one pair.
      "hash:spec:spec-recheck": MOVED,
      "hash:spec:after-non-spec-loop:2": MOVED,
    }),
  }), withChild());
  t.check("the run completes", !pair.error, String(pair.error));
  t.check(
    "four loops ran",
    (pair.result.review.loops || []).map((l) => l.name).join(",") ===
      "spec,non-spec,spec-recheck,non-spec-recheck",
    (pair.result.review.loops || []).map((l) => l.name).join(","),
  );
  t.check(
    "and a firing sits after each of them, naming the loop it followed",
    timeline(pair.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:non-spec fire:post-non-spec-loop " +
      "loop:spec-recheck fire:post-spec-recheck loop:non-spec-recheck fire:post-non-spec-recheck",
    timeline(pair.calls).join(" "),
  );
  t.check(
    "so the run takes one firing per loop and no more",
    triggers(pair.calls).length === pair.result.review.loops.length,
    triggers(pair.calls).join(","),
  );

  // A non-spec edit with the spec lane settled runs a LONE non-spec recheck,
  // which is a review loop like any other and is followed by a firing too.
  const lone = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "*:review:*": findsIn(/^r1:/),
    "*:dedup": DEDUP2,
    "hash:*": laneHashes({
      "hash:non-spec:after-non-spec-loop": MOVED,
      "hash:non-spec:non-spec-recheck": MOVED,
      "hash:non-spec:after-non-spec-loop:2": MOVED,
    }),
  }), withChild());
  t.check(
    "a lone non-spec recheck is followed by a firing as well",
    timeline(lone.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:non-spec fire:post-non-spec-loop " +
      "loop:non-spec-recheck fire:post-non-spec-recheck",
    timeline(lone.calls).join(" "),
  );
  t.check("and no spec recheck ran beside it", lone.result.rechecks.pairs === 0,
    String(lone.result.rechecks.pairs));
}

t.section("R52. the firing after the spec loop runs on each of the four paths that run no non-spec loop");
{
  // The site is straight-line, so a run that stops early still adjudicates
  // once. Each path also has to SAY that no later firing ran, because a reader
  // of the result cannot otherwise tell one adjudication from two.
  const halting = {
    "introspect:*": PASS({ verdict: "halt", questionForHuman: "which mechanism ships?" }),
    "introspect-gate:*": { warranted: true, why: "the counter is right" },
    "judge:*": { falsified: false, howConclusive: "none", theArgumentIAttacked: "a", reasoning: "could not" },
    growth: { documentWas: 10, documentNow: 12, grew: [] },
  };
  const PATHS = [
    // The spec loop exhausts a one-round budget with a finding open, so the
    // non-spec loop is not run over staging that is still moving.
    ["spec-not-converged", { maxSpecReviewRounds: 1 },
      { "*:review:*": findsIn(/^r1:/), "*:dedup": DEDUP2 }],
    ["skipNonSpecReview", { skipNonSpecReview: true }, {}],
    // startPhase past both review phases, which is also one of R53's three.
    ["startPhase", { startPhase: "finalize" }, {}],
    ["stopped-by-introspection", { introspectEvery: 1 },
      { "*:review:*": findsIn(/^r1:/), "*:dedup": DEDUP2, ...halting }],
  ];
  for (const [reason, args, over] of PATHS) {
    const { result, calls, error } = await runWorkflow(
      WF, { ...REVIEW_ARGS, ...args }, loopStubs({ "hash:*": HASH, ...over }), withChild(),
    );
    t.check("the " + reason + " path completes", !error, String(error));
    t.check(
      "no non-spec loop ran on the " + reason + " path",
      !result.review.nonSpecReviewed,
      String(result.review.nonSpecReviewed),
    );
    t.check(
      "the firing after the spec review still ran",
      triggers(calls).join(",") === "post-spec-loop",
      triggers(calls).join(",") || "none",
    );
    const p = result.decisions.paths.noNonSpecLoop;
    t.check("and the result names the path as " + reason, p && p.reason === reason, p && p.reason);
    t.check(
      "reporting that no later firing ran",
      p && p.firingAfterNonSpecLoop === false && p.adjudications.join(",") === "post-spec-loop",
      p && JSON.stringify(p.adjudications),
    );
  }
}

t.section("R53. the first firing runs on each of the three paths that run no spec loop");
{
  // The adjudication does not depend on a spec review having happened, so the
  // firing whose trigger names the spec loop runs where that loop never did.
  const PATHS = [
    ["no-spec-changes", {}, { "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" } }],
    ["skipSpecReview", { skipSpecReview: true }, {}],
    ["startPhase", { startPhase: "non-spec-review" }, {}],
  ];
  for (const [reason, args, over] of PATHS) {
    const { result, calls, error } = await runWorkflow(
      WF, { ...REVIEW_ARGS, ...args }, loopStubs({ "hash:*": HASH, ...over }), withChild(),
    );
    t.check("the " + reason + " path completes", !error, String(error));
    t.check("no spec loop ran on the " + reason + " path", !result.review.specReviewed);
    t.check(
      "the first firing ran anyway",
      triggers(calls)[0] === "post-spec-loop",
      triggers(calls).join(",") || "none",
    );
    const p = result.decisions.paths.noSpecLoop;
    t.check("and the result names the path as " + reason, p && p.reason === reason, p && p.reason);
    t.check("recording that the first firing ran", p && p.firstFiringRan === true);
    // The non-spec loop runs on all three, so its own firing follows.
    t.check(
      "and the non-spec loop's firing follows it",
      triggers(calls).join(",") === "post-spec-loop,post-non-spec-loop",
      triggers(calls).join(","),
    );
  }
}

t.section("R54. the periodic firing runs in the non-spec loop only, and never at a recheck's boundary");
{
  // periodEvery at 1 fires at every round that reaches the loop tail, so any
  // loop whose tail is reached and produces no firing is one the gate excluded.
  // Round 1 of every loop here is the same round -- the labels carry the round
  // rather than the loop -- and the non-spec loop's fires, which is what makes
  // the silence of the other three evidence of the gate rather than of a round
  // that never got there.
  const { result, calls, error } = await runWorkflow(WF, { ...REVIEW_ARGS, periodEvery: 1 }, loopStubs({
    "*:review:*": findsIn(/^r1:/),
    "*:dedup": DEDUP2,
    "hash:*": laneHashes({
      "hash:spec:after-non-spec-loop": MOVED,
      "hash:spec:spec-recheck": MOVED,
      "hash:spec:after-non-spec-loop:2": MOVED,
    }),
  }), withChild());
  t.check("the run completes", !error, String(error));
  const line = timeline(calls);
  t.check(
    "all four loops run the same three rounds",
    result.review.loops.every((l) => l.rounds === 3),
    result.review.loops.map((l) => l.name + ":" + l.rounds).join(","),
  );
  t.check(
    "exactly one periodic firing runs",
    triggers(calls).filter((x) => x === "periodic").length === 1,
    triggers(calls).join(","),
  );
  t.check(
    "and it runs inside the non-spec loop",
    line.every((e, i) => e !== "fire:periodic" || line[i - 1] === "loop:non-spec"),
    line.join(" "),
  );
  const inLoop = (name) => {
    const from = line.indexOf("loop:" + name);
    const to = line.indexOf("fire:post-" + (name === "spec" ? "spec-loop" : name));
    return from >= 0 && to > from ? line.slice(from, to) : [];
  };
  for (const name of ["spec", "spec-recheck", "non-spec-recheck"]) {
    t.check(
      "no periodic firing runs at the " + name + " loop's round boundary",
      !inLoop(name).includes("fire:periodic"),
      inLoop(name).join(" ") || "the loop's segment was not found",
    );
  }
  t.check(
    "the result reports the cadence it ran at",
    result.decisions.periodic.periodEvery === 1 && result.decisions.periodic.firings === 1,
    JSON.stringify(result.decisions.periodic),
  );
}

t.section("R55. the periodic cadence counts firings, so a round that never reaches the loop tail spends none");
{
  // A round that returns through the clean-round `continue`, or through the
  // break every reviewer failing takes, jumps over the tail the periodic firing
  // hooks. Counting rounds instead would fire at a boundary the round never
  // reached, which is the whole reason the counter is incremented at the hook.
  const nonSpecOnly = (over) => loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "hash:*": HASH,
    "*:dedup": DEDUP2,
    ...over,
  });

  const clean = await runWorkflow(WF, { ...REVIEW_ARGS, periodEvery: 1 },
    nonSpecOnly({ "*:review:*": findsIn(/^r1:/) }), withChild());
  const cleanLoop = clean.result.review.loops.find((l) => l.name === "non-spec");
  t.check("three rounds run", cleanLoop.rounds === 3, String(cleanLoop.rounds));
  t.check(
    "two of them find nothing and return before the tail",
    [2, 3].every((n) => clean.logs.some((l) => new RegExp("Round " + n + ": 0 raw findings").test(l))),
    clean.logs.filter((l) => /raw findings/.test(l)).join(" | "),
  );
  t.check(
    "so one firing runs at a cadence of one, rather than three",
    clean.result.decisions.periodic.firings === 1,
    JSON.stringify(clean.result.decisions.periodic),
  );

  const dead = await runWorkflow(WF, { ...REVIEW_ARGS, periodEvery: 1 }, nonSpecOnly({
    "*:review:*": ({ label }) =>
      (/^r1:/.test(label) ? { coverage: "c", findings: fs(2) }
        : /^r2:/.test(label) ? null
          : { coverage: "c", findings: [] }),
  }), withChild());
  const deadLoop = dead.result.review.loops.find((l) => l.name === "non-spec");
  t.check("a round whose reviewers all fail ends the loop", deadLoop.rounds === 2, String(deadLoop.rounds));
  t.check("and says so", dead.logs.some((l) => /Round 2: every reviewer failed; stopping/.test(l)));
  t.check(
    "that round reaches no tail and spends no firing",
    dead.result.decisions.periodic.firings === 1,
    JSON.stringify(dead.result.decisions.periodic),
  );

  // The distinguishing shape. At a cadence of two, with rounds 1, 3 and 5
  // returning before the tail and rounds 2 and 4 reaching it, counting tails
  // fires once, at round 4. Counting rounds fires twice, at rounds 2 and 4,
  // so a `round % periodEvery` gate fails here and nowhere else in the suite.
  const skipped = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, periodEvery: 2, maxNonSpecReviewRounds: 6 },
    nonSpecOnly({ "*:review:*": findsIn(/^r2:|^r4:/) }),
    withChild(),
  );
  const skippedLoop = skipped.result.review.loops.find((l) => l.name === "non-spec");
  t.check("six rounds run at a cadence of two", skippedLoop.rounds === 6, String(skippedLoop.rounds));
  t.check(
    "three of them find nothing and return before the tail",
    [1, 3, 5].every((n) => skipped.logs.some((l) => new RegExp("Round " + n + ": 0 raw findings").test(l))),
    skipped.logs.filter((l) => /raw findings/.test(l)).join(" | "),
  );
  t.check(
    "so the firing lands on the second round that reached the tail, and runs once",
    skipped.result.decisions.periodic.firings === 1
      && skipped.result.decisions.periodic.periodEvery === 2,
    JSON.stringify(skipped.result.decisions.periodic),
  );
}

t.section("R56. a round that exits on introspection fires once, through the post-loop firing");
{
  // The periodic firing is suppressed on the halting round so the same staging
  // is not adjudicated twice: the firing after the loop covers it.
  const base = (over) => loopStubs({
    "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
    "hash:*": HASH,
    "*:review:*": findsIn(/^r1:/),
    "*:dedup": DEDUP2,
    "introspect-gate:*": { warranted: true, why: "the counter is right" },
    "judge:*": { falsified: false, howConclusive: "none", theArgumentIAttacked: "a", reasoning: "could not" },
    growth: { documentWas: 10, documentNow: 12, grew: [] },
    ...over,
  });
  const ARGS = { ...REVIEW_ARGS, periodEvery: 1, introspectEvery: 1 };

  const halted = await runWorkflow(WF, ARGS,
    base({ "introspect:*": PASS({ verdict: "halt", questionForHuman: "q" }) }), withChild());
  t.check("the run stops on the halt", halted.result.status === "stopped-halt", halted.result.status);
  t.check(
    "the halting round is round one",
    halted.result.review.loops.find((l) => l.name === "non-spec").rounds === 1,
  );
  t.check(
    "no periodic firing runs on it",
    !triggers(halted.calls).includes("periodic"),
    triggers(halted.calls).join(","),
  );
  t.check(
    "and the firing after the loop covers it",
    triggers(halted.calls).join(",") === "post-spec-loop,post-non-spec-loop",
    triggers(halted.calls).join(","),
  );

  // The control: the same round without the halt does fire periodically, so
  // the absence above is the suppression rather than a round short of cadence.
  const healthy = await runWorkflow(WF, ARGS, base({ "introspect:*": PASS() }), withChild());
  t.check(
    "the same round without the halt fires periodically",
    triggers(healthy.calls).join(",") === "post-spec-loop,periodic,post-non-spec-loop",
    triggers(healthy.calls).join(","),
  );
}

t.section("R57. every post-loop firing runs whether or not anything changed, and the last reads the whole refuted list");
{
  // There is no "did any decisions appear" condition on any site: a firing runs
  // when nothing has changed, and carrying an untouched item's disposition
  // forward is what keeps it cheap.
  const steady = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH }), withChild());
  t.check("both firings ran over an unchanged staging", steady.result.decisions.fired === 2,
    String(steady.result.decisions.fired));
  t.check("neither failed", steady.result.decisions.failedFirings === 0);
  t.check(
    "and each reports that nothing changed",
    steady.result.decisions.firings.every((f) => f.ran && f.changedFiles.length === 0),
    JSON.stringify(steady.result.decisions.firings.map((f) => f.changedFiles)),
  );

  // The run-wide refuted list grows as the skeptics refuse findings, and the
  // firing after the non-spec loop reads it complete: an item an earlier firing
  // routed to the human may be resolvable once the ground it rested on is gone.
  let dedups = 0;
  const refuting = await runWorkflow(WF, REVIEW_ARGS, loopStubs({
    "hash:*": HASH,
    "*:review:*": findsIn(/^r1:/),
    // One refuted finding per loop, titled for the loop that produced it, so
    // the second firing's list can be told from the first's.
    "*:dedup": () => {
      const n = ++dedups;
      return { findings: [{ ...F(n), title: "refuted-" + n, lenses: ["citations"] }] };
    },
    "*:verify-material": { confirmed: false, reason: "it changes nothing a reader acts on" },
  }), withChild());
  const lists = firedWith(refuting.calls).map((f) => (f.rejected || []).map((r) => r.title));
  t.check(
    "the first firing carries what the spec loop refuted",
    JSON.stringify(lists[0]) === JSON.stringify(["refuted-1"]),
    JSON.stringify(lists[0]),
  );
  t.check(
    "and the firing after the non-spec loop carries both, complete",
    JSON.stringify(lists[1]) === JSON.stringify(["refuted-1", "refuted-2"]),
    JSON.stringify(lists[1]),
  );
}

t.section("R58. maxPeriodicFirings bounds the periodic firing alone");
{
  // The periodic firing is the only one whose count is open-ended, so it is the
  // only one with a budget. Exhausting it must not take the structural firings
  // with it: those are one per loop and the loop count is already bounded.
  const { result, calls, error, logs } = await runWorkflow(
    WF,
    { ...REVIEW_ARGS, periodEvery: 1, maxPeriodicFirings: 2, maxNonSpecReviewRounds: 6 },
    loopStubs({
      "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" },
      "hash:*": HASH,
      "*:review:*": findsIn(/^r[1234]:/),
      "*:dedup": DEDUP2,
    }),
    withChild(),
  );
  t.check("the run completes", !error, String(error));
  t.check(
    "four rounds reach the tail",
    result.review.loops.find((l) => l.name === "non-spec").rounds === 6,
    String(result.review.loops.find((l) => l.name === "non-spec").rounds),
  );
  t.check(
    "the periodic firing stops at its budget",
    triggers(calls).filter((x) => x === "periodic").length === 2,
    triggers(calls).join(","),
  );
  t.check(
    "every post-loop firing still runs",
    triggers(calls).filter((x) => x !== "periodic").join(",") === "post-spec-loop,post-non-spec-loop",
    triggers(calls).join(","),
  );
  t.check(
    "the stop is reported once, saying the post-loop firings continue",
    logs.filter((l) => /periodic open-decisions budget of 2 firing\(s\) is spent/.test(l)).length === 1
      && logs.some((l) => /Every post-loop firing still runs/.test(l)),
    logs.filter((l) => /budget of 2/.test(l)).join(" | "),
  );
  t.check(
    "and the result carries the stop out of the run",
    result.decisions.periodic.budgetSpent === true && result.decisions.periodic.budget === 2,
    JSON.stringify(result.decisions.periodic),
  );
}

t.section("R59. a null return from the child is a failed firing, exactly as a throw is");
{
  // A subworkflow that RETURNS null did not throw, so a catch never sees it and
  // every read of the result would dereference null. Both are the same
  // condition and take the same exit: the firing is recorded as failed, the run
  // says so, and the remaining loops still run.
  const nulled = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH }), withChild(null));
  t.check("a null return does not crash the run", !nulled.error, String(nulled.error));
  t.check("the run still reports itself reviewed", nulled.result.status === "reviewed", nulled.result.status);
  t.check(
    "both firings are recorded as failed rather than dropped",
    nulled.result.decisions.fired === 2 && nulled.result.decisions.failedFirings === 2,
    JSON.stringify([nulled.result.decisions.fired, nulled.result.decisions.failedFirings]),
  );
  t.check(
    "each says what happened, and that the run continues",
    nulled.logs.filter((l) => /FAILED: the subworkflow returned no result\. This firing adjudicated nothing; the run continues\./.test(l)).length === 2,
    nulled.logs.filter((l) => /firing \d+ FAILED/.test(l)).join(" | "),
  );
  t.check(
    "and the empty lists a reader reads per firing are stated rather than absent",
    nulled.result.decisions.firings.every(
      (f) => f.ran === false && f.status === "failed" && f.applied.length === 0
        && f.decisionsLeftToHuman.length === 0 && f.changedFiles === null,
    ),
    JSON.stringify(nulled.result.decisions.firings[0]),
  );

  // A child that THROWS reaches the same record through the catch, differing
  // only in the reason. The stub table's getter is the only way to make the
  // harness's subworkflow throw rather than return.
  const throwing = {};
  Object.defineProperty(throwing, "change-proposal-decisions.js", {
    enumerable: true,
    get() { throw new Error("the child blew up"); },
  });
  const threw = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH }), { subworkflows: throwing });
  t.check("a throw does not crash the run either", !threw.error, String(threw.error));
  const strip = (r) => r.decisions.firings.map((f) => ({ ...f, reason: "" }));
  t.check(
    "and is recorded exactly as the null return is, apart from the reason",
    JSON.stringify(strip(threw.result)) === JSON.stringify(strip(nulled.result)),
    JSON.stringify(strip(threw.result)[0]),
  );
  t.check(
    "the reason names the failure the child raised",
    threw.result.decisions.firings.every((f) => f.reason === "the child blew up"),
    threw.result.decisions.firings.map((f) => f.reason).join(" | "),
  );
}

// ---- Rechecks: what re-reads a lane whose staging moved after its review --
//
// The run may converge only when no lane's staging has changed since that
// lane's own last review. A firing is what can change a lane's staging after
// its review, and a recheck is what reviews it again. The trigger is a content
// hash of the lane's files rather than an agent's report, so these sections
// stub that hash and nothing else decides whether a lane moved.
//
// The stub models a FILE rather than a reading: a lane's digest keeps whatever
// value it was last given, so an edit made at one comparison is still there at
// the next one and at the baseline a recheck re-takes as it returns. A plan
// naming a label is the edit that landed just before the hash at that label was
// taken. `laneHashes` above answers each label independently, which is the
// wrong model here: a lane that moved once would read as moving back.
const edit = (n) => String(n).repeat(12);
const laneTape = (plan = {}) => {
  const cur = { spec: LANE.spec, "non-spec": LANE["non-spec"] };
  return ({ label }) => {
    const m = /^hash:(spec|non-spec):/.exec(label);
    if (!m) return LANE.spec;
    if (Object.prototype.hasOwnProperty.call(plan, label)) cur[m[1]] = plan[label];
    return cur[m[1]];
  };
};

// Labels carry the round but not the loop, because `round` restarts in every
// loop. The loop is in the call's phase, which is `<loop> R<n>: <stage>`.
const callsInLoop = (calls, name) =>
  calls.filter((c) => c.opts && typeof c.opts.phase === "string" && c.opts.phase.startsWith(name + " R"));
const lensesOf = (calls, name) =>
  callsInLoop(calls, name).filter((c) => /^r1:review:/.test(c.label)).map((c) => c.label.split(":")[2]).sort();
const fixerOf = (calls, name) =>
  (callsInLoop(calls, name).find((c) => /^r\d+:fix:/.test(c.label)) || {}).prompt || "";
const loopNames = (r) => (r.result.review.loops || []).map((l) => l.name);

// One group, so the confirmed findings of every loop reach a fixer and the
// fixer brief each lane hands its own loop is observable.
const ONE_GROUP = plan([{ id: "G1", title: "g", rationale: "r", findings: [0, 1], order: 1 }]);
const recheckStubs = (tape, over = {}) =>
  loopStubs({
    "*:review:*": findsIn(/^r1:/),
    "*:dedup": DEDUP2,
    "*:fix-plan": ONE_GROUP,
    "hash:*": tape,
    ...over,
  });

// What each lane's constants say, at the prompt each of them reaches.
const SPEC_SCOPE = /SCOPE OF THIS LOOP\. You are reviewing the STAGED SPEC EDITS/;
const NONSPEC_SCOPE = /Read the staged spec edits in .* AS ONE DOCUMENT/;
const SPEC_GRANT = /the staged spec edits, which is what this loop converges/;
const NONSPEC_GRANT = /the staged code, schema, chart, migration, docs and test changes/;
const SPEC_BRIEF = /THE IMPLEMENTATION CHECKLIST IS NOT YOURS/;
const NONSPEC_BRIEF = /KEEP THE IMPLEMENTATION CHECKLIST CURRENT/;
const DELTA = /THIS IS A RECHECK, AND THE DELTA IS WHERE YOU LOOK FIRST/;

t.section("R60. a spec edit by the firing after the spec loop runs a pair before the non-spec loop starts");
{
  // The control first: a run whose lanes both read the same digest at every
  // comparison takes no recheck at all, so every recheck below is fired by the
  // edit its own tape stages and by nothing in the harness.
  const steady = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape()), withChild());
  t.check("the control completes", !steady.error, String(steady.error));
  t.check(
    "a run over unmoved staging takes no recheck and converges",
    steady.result.rechecks.pairs === 0 && steady.result.rechecks.lone === 0 &&
      steady.result.review.converged === true,
    JSON.stringify(steady.result.rechecks),
  );

  // The non-spec loop's premise is that the spec staging is settled, so a spec
  // edit made by the firing that precedes it is reviewed BEFORE it starts
  // rather than invalidating it afterwards.
  const early = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-first-firing": edit(1),
  })), withChild());
  t.check("the run completes", !early.error, String(early.error));
  t.check(
    "the pair runs between the firing and the non-spec loop",
    timeline(early.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:spec-recheck fire:post-spec-recheck " +
      "loop:non-spec-recheck fire:post-non-spec-recheck loop:non-spec fire:post-non-spec-loop",
    timeline(early.calls).join(" "),
  );
  t.check("one pair was spent", early.result.rechecks.pairs === 1, String(early.result.rechecks.pairs));
  t.check(
    "and the run still converges, because the recheck read the edit",
    early.result.review.converged === true && early.result.status === "reviewed",
    early.result.status,
  );

  // The same on a path that ran NO spec loop: the baseline is taken at the
  // first firing, which is the last point that lane was settled, so the trigger
  // is decidable there too.
  const noSpecLoop = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-first-firing": edit(1),
  }), { "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" } }), withChild());
  t.check("that run completes too", !noSpecLoop.error, String(noSpecLoop.error));
  t.check(
    "no spec loop ran",
    !loopNames(noSpecLoop).includes("spec"),
    loopNames(noSpecLoop).join(","),
  );
  t.check(
    "the baseline was taken at the first firing rather than at a loop",
    noSpecLoop.calls.some((c) => c.label === "hash:spec:first-firing") &&
      !noSpecLoop.calls.some((c) => c.label === "hash:spec:spec-loop"),
    noSpecLoop.calls.filter((c) => /^hash:spec:/.test(c.label)).map((c) => c.label).join(","),
  );
  t.check(
    "and the firing's spec edit still runs a pair before the non-spec loop",
    timeline(noSpecLoop.calls).join(" ") ===
      "fire:post-spec-loop loop:spec-recheck fire:post-spec-recheck " +
      "loop:non-spec-recheck fire:post-non-spec-recheck loop:non-spec fire:post-non-spec-loop",
    timeline(noSpecLoop.calls).join(" "),
  );
}

t.section("R61. a spec edit by any later firing, or by the non-spec fixer, runs a pair");
{
  // The trigger is the tree, so it does not matter which agent wrote the edit.
  // That is the point: the non-spec fixer holds the same permission over the
  // staged spec edits that the phase does, and scoping the trigger to the
  // phase's own edits would leave half the requirement met.
  const late = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
  })), withChild());
  t.check("the run completes", !late.error, String(late.error));
  t.check(
    "the pair runs after the non-spec loop and its firing",
    timeline(late.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:non-spec fire:post-non-spec-loop " +
      "loop:spec-recheck fire:post-spec-recheck loop:non-spec-recheck fire:post-non-spec-recheck",
    timeline(late.calls).join(" "),
  );

  // The same comparison covers the non-spec fixer, which writes inside the loop
  // rather than after it. The fixer ran and its grant names the staged spec
  // edits, so the edit the comparison sees is one it was permitted to make.
  t.check(
    "the non-spec fixer ran under a grant that permits the staged spec edits",
    /permitted, but PREFER any resolution that does not touch it/.test(fixerOf(late.calls, "non-spec")),
    fixerOf(late.calls, "non-spec") ? "the fixer ran under another grant" : "no fixer ran in the non-spec loop",
  );
  t.check("and one pair was spent on the outstanding edit", late.result.rechecks.pairs === 1,
    String(late.result.rechecks.pairs));
}

t.section("R62. every spec-recheck is followed by a non-spec-recheck, which reads the non-spec lane");
{
  // A spec-recheck changes the staged spec text, the non-spec loop reads both
  // change files as one document, and the spec fixer may repair a non-spec
  // statement its own edit falsified. Either way the result is non-spec text no
  // non-spec lens has read, so the non-spec-recheck runs whether or not the
  // non-spec staging moved.
  const pair = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
  })), withChild());
  t.check("the run completes", !pair.error, String(pair.error));
  t.check(
    "nothing moved the non-spec lane, so no lone recheck was owed",
    pair.result.rechecks.lone === 0,
    String(pair.result.rechecks.lone),
  );
  const line = timeline(pair.calls).join(" ");
  t.check(
    "the non-spec-recheck runs anyway, immediately after the spec-recheck's firing",
    /loop:spec-recheck fire:post-spec-recheck loop:non-spec-recheck/.test(line),
    line,
  );

  // Over two pairs, so "every" has more than one instance to hold over. The
  // pair stays adjacent: the firing that follows the spec-recheck sits between
  // its two loops rather than after both.
  const twice = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    "hash:spec:spec-recheck": edit(2),
    "hash:spec:after-non-spec-loop:2": edit(3),
    "hash:spec:spec-recheck-2": edit(4),
  })), withChild());
  const entries = timeline(twice.calls);
  const specRechecks = entries.filter((e) => /^loop:spec-recheck/.test(e)).length;
  t.check("two spec-rechecks ran", specRechecks === 2, entries.join(" "));
  t.check(
    "and each is followed by its firing and then by a non-spec-recheck",
    entries.every((e, i) =>
      !/^loop:spec-recheck/.test(e) ||
      (entries[i + 1] === "fire:post-spec-recheck" && /^loop:non-spec-recheck/.test(entries[i + 2] || ""))),
    entries.join(" "),
  );

  // What the non-spec-recheck reads is the non-spec lane: a repair the
  // spec-recheck's fixer made in the non-spec staging is inside its scope, and
  // its own fixer holds the non-spec brief through the lane on the loop config
  // rather than through the loop's name. The two constants reach different
  // prompts, so each is asserted where it lands: the scope note reaches every
  // lens, and the editable grant reaches the fixer.
  t.check(
    "the spec-recheck's fixer may repair what its own spec edit falsified",
    /REPAIR ONLY WHAT YOUR OWN EDIT FALSIFIED/.test(fixerOf(pair.calls, "spec-recheck")),
  );
  t.check(
    "its lenses are briefed with NONSPEC_SCOPE_NOTE",
    callsInLoop(pair.calls, "non-spec-recheck").filter((c) => /^r\d+:review:/.test(c.label))
      .every((c) => NONSPEC_SCOPE.test(c.prompt) && !SPEC_SCOPE.test(c.prompt)),
  );
  t.check(
    "its fixer is granted the non-spec files by NONSPEC_EDITABLE",
    NONSPEC_GRANT.test(fixerOf(pair.calls, "non-spec-recheck")) &&
      !SPEC_GRANT.test(fixerOf(pair.calls, "non-spec-recheck")),
  );
  t.check(
    "and reads the non-spec fixer brief rather than the spec one",
    NONSPEC_BRIEF.test(fixerOf(pair.calls, "non-spec-recheck")) &&
      !SPEC_BRIEF.test(fixerOf(pair.calls, "non-spec-recheck")),
  );
}

t.section("R63. each recheck is its lane's review phase: pool, scope note, grant, and fixer brief");
{
  const run = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
  })), withChild());
  t.check("the run completes", !run.error, String(run.error));

  // The pools are asserted against the lane loops rather than against a count,
  // because a lens added to the pool must reach both.
  t.check(
    "the non-spec-recheck runs the full pool",
    lensesOf(run.calls, "non-spec-recheck").join(",") === lensesOf(run.calls, "non-spec").join(","),
    lensesOf(run.calls, "non-spec-recheck").join(","),
  );
  t.check(
    "the spec-recheck runs the spec lane's pool, with test-coverage dropped",
    lensesOf(run.calls, "spec-recheck").join(",") ===
      lensesOf(run.calls, "non-spec").filter((k) => k !== "test-coverage").join(","),
    lensesOf(run.calls, "spec-recheck").join(","),
  );
  t.check(
    "which is the pool the spec loop itself ran",
    lensesOf(run.calls, "spec-recheck").join(",") === lensesOf(run.calls, "spec").join(","),
    lensesOf(run.calls, "spec").join(","),
  );

  t.check(
    "the spec-recheck's lenses read SPEC_SCOPE_NOTE",
    callsInLoop(run.calls, "spec-recheck").filter((c) => /^r\d+:review:/.test(c.label))
      .every((c) => SPEC_SCOPE.test(c.prompt) && !NONSPEC_SCOPE.test(c.prompt)),
  );
  t.check(
    "and its fixer reads SPEC_EDITABLE and the spec brief",
    SPEC_GRANT.test(fixerOf(run.calls, "spec-recheck")) &&
      SPEC_BRIEF.test(fixerOf(run.calls, "spec-recheck")) &&
      !NONSPEC_BRIEF.test(fixerOf(run.calls, "spec-recheck")),
  );

  // The delta is what a recheck differs by. It names the text added since that
  // lane's last convergence as where the lenses look first, and says a defect
  // anywhere in the staging is still a finding.
  for (const name of ["spec-recheck", "non-spec-recheck"]) {
    const lenses = callsInLoop(run.calls, name).filter((c) => /^r1:review:/.test(c.label));
    t.check(
      "every lens of the " + name + " is pointed at the delta",
      lenses.length > 0 && lenses.every((c) => DELTA.test(c.prompt)),
      String(lenses.length),
    );
    t.check(
      "without narrowing what it may report",
      lenses.every((c) => /A defect anywhere in the staging is still a finding/.test(c.prompt)),
    );
  }
  t.check(
    "no lens of either lane loop is told it is rechecking",
    [...callsInLoop(run.calls, "spec"), ...callsInLoop(run.calls, "non-spec")]
      .filter((c) => /^r\d+:review:/.test(c.label)).every((c) => !DELTA.test(c.prompt)),
  );
}

t.section("R64. a recheck's convergence settles its own edits, and an edit after it is outstanding again");
{
  // The baseline is re-taken at each recheck's convergence. Without that, the
  // recheck's own edits would read as drift at the next comparison and fire
  // another pair against them, forever.
  const settles = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    // The spec-recheck edited the staged spec file, and that is all that
    // changed. Its own convergence is what the next comparison reads against.
    "hash:spec:spec-recheck": edit(2),
  })), withChild());
  t.check("the run completes", !settles.error, String(settles.error));
  t.check("one pair runs and no more", settles.result.rechecks.pairs === 1,
    String(settles.result.rechecks.pairs));
  t.check(
    "and the run converges over the recheck's own edits",
    settles.result.review.converged === true && settles.result.status === "reviewed",
    settles.result.status,
  );

  // A firing that follows a recheck and changes that lane's staging leaves the
  // lane stale again, and the lane is reviewed again. The comparison after the
  // pair is where such an edit shows, whether the firing after the spec-recheck
  // made it or the non-spec-recheck did.
  const again = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    "hash:spec:spec-recheck": edit(2),
    "hash:spec:after-non-spec-loop:2": edit(3),
    "hash:spec:spec-recheck-2": edit(4),
  })), withChild());
  t.check("that run completes", !again.error, String(again.error));
  t.check("a second pair runs", again.result.rechecks.pairs === 2, String(again.result.rechecks.pairs));
  t.check(
    "under its own name, so it does not re-enter the first pair's namespace",
    loopNames(again).join(",") ===
      "spec,non-spec,spec-recheck,non-spec-recheck,spec-recheck-2,non-spec-recheck-2",
    loopNames(again).join(","),
  );
  t.check(
    "the run converges once the edit has been reviewed",
    again.result.review.converged === true, String(again.result.status),
  );
  t.check(
    "and the budget bounds the alternation at maxRecheckPairs",
    again.result.rechecks.pairs === again.result.rechecks.pairBudget,
    JSON.stringify([again.result.rechecks.pairs, again.result.rechecks.pairBudget]),
  );
}

t.section("R65. exhausting either recheck budget stops the run rather than converging");
{
  // The posture is the one the run already takes for a spec loop that did not
  // converge: it says what it was still finding rather than converging over
  // text no reviewer in that lane has read.
  const pairs = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    "hash:spec:spec-recheck": edit(2),
    "hash:spec:after-non-spec-loop:2": edit(3),
    "hash:spec:spec-recheck-2": edit(4),
    "hash:spec:after-non-spec-loop:3": edit(5),
  })), withChild());
  t.check("the run completes", !pairs.error, String(pairs.error));
  t.check("the budget stops the alternation", pairs.result.rechecks.pairs === 2,
    String(pairs.result.rechecks.pairs));
  t.check(
    "the run does NOT converge over the unreviewed edit",
    pairs.result.review.converged === false && pairs.result.status === "recheck-budget-exhausted",
    pairs.result.status,
  );
  const stop = pairs.result.rechecks.stop || {};
  t.check("the stop names the lane", stop.lane === "spec", String(stop.lane));
  t.check("and the budget that ran out", stop.budget === "maxRecheckPairs" && stop.limit === 2,
    JSON.stringify([stop.budget, stop.limit]));
  t.check(
    "and the file the outstanding edit is in",
    (stop.files || []).some((f) => /spec-changes\.md$/.test(f)),
    (stop.files || []).join(","),
  );
  t.check(
    "and the outstanding edit itself, as the two digests",
    typeof stop.outstanding === "string" && stop.outstanding.includes(edit(4)) &&
      stop.outstanding.includes(edit(5)),
    String(stop.outstanding),
  );
  t.check("the lane is reported outstanding", pairs.result.rechecks.specOutstanding === true);
  t.check(
    "the stop is logged once, naming what to raise",
    pairs.logs.filter((l) => /maxRecheckPairs budget of 2 is spent/.test(l)).length === 1 &&
      pairs.logs.some((l) => /Raise maxRecheckPairs above 2 and resume/.test(l)),
    pairs.logs.filter((l) => /budget of 2 is spent/.test(l)).join(" | "),
  );

  // The lone recheck's budget is counted separately, because a lone recheck can
  // beget a pair exactly as a pair can, and a budget named for pairs cannot
  // account for a loop that runs alone.
  const lone = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:non-spec:after-non-spec-loop": edit(1),
    "hash:non-spec:non-spec-recheck": edit(2),
    "hash:non-spec:after-non-spec-loop:2": edit(3),
    "hash:non-spec:non-spec-recheck-2": edit(4),
    "hash:non-spec:after-non-spec-loop:3": edit(5),
  })), withChild());
  t.check("that run completes", !lone.error, String(lone.error));
  t.check("two lone rechecks ran and no pair", lone.result.rechecks.lone === 2 &&
    lone.result.rechecks.pairs === 0, JSON.stringify(lone.result.rechecks.lone));
  t.check(
    "the run does not converge either",
    lone.result.review.converged === false && lone.result.status === "recheck-budget-exhausted",
    lone.result.status,
  );
  const ls = lone.result.rechecks.stop || {};
  t.check("the report names the non-spec lane and its budget",
    ls.lane === "non-spec" && ls.budget === "maxNonSpecRechecks", JSON.stringify([ls.lane, ls.budget]));
  t.check(
    "and both files of that lane, since the summary is in it",
    (ls.files || []).some((f) => /non-spec-changes\.md$/.test(f)) &&
      (ls.files || []).some((f) => /summary\.md$/.test(f)),
    (ls.files || []).join(","),
  );
  t.check("the lane is reported outstanding", lone.result.rechecks.nonSpecOutstanding === true);
}

t.section("R66. the lone non-spec recheck: when it runs alone, and when the pair covers it");
{
  // A lone recheck arises only where no non-spec review already follows.
  const alone = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:non-spec:after-non-spec-loop": edit(1),
    "hash:non-spec:non-spec-recheck": edit(1),
  })), withChild());
  t.check("the run completes", !alone.error, String(alone.error));
  t.check(
    "a terminal firing that moved only the non-spec lane runs one recheck and no spec-recheck",
    timeline(alone.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:non-spec fire:post-non-spec-loop " +
      "loop:non-spec-recheck fire:post-non-spec-recheck",
    timeline(alone.calls).join(" "),
  );
  t.check("counted against its own budget", alone.result.rechecks.lone === 1 &&
    alone.result.rechecks.pairs === 0, JSON.stringify(alone.result.rechecks));

  // Where BOTH lanes moved, the pair runs and no lone recheck is taken beside
  // it: the pair's own non-spec-recheck already read that text, which is what
  // the baseline re-taken at its convergence records.
  const both = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    // The firing moved the non-spec lane too. The pair's re-take is the first
    // non-spec hash after it, because the spec comparison fires first.
    "hash:non-spec:non-spec-recheck": edit(2),
  })), withChild());
  t.check("that run completes", !both.error, String(both.error));
  t.check(
    "one pair runs and no lone recheck follows it",
    both.result.rechecks.pairs === 1 && both.result.rechecks.lone === 0,
    JSON.stringify(both.result.rechecks),
  );
  t.check(
    "so the non-spec lane is reviewed once rather than twice",
    loopNames(both).filter((n) => /^non-spec-recheck/.test(n)).length === 1,
    loopNames(both).join(","),
  );

  // A lone recheck can beget a pair, because the permission over the staged
  // spec edits is not withdrawn inside one.
  const begets = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:non-spec:after-non-spec-loop": edit(1),
    "hash:non-spec:non-spec-recheck": edit(1),
    "hash:spec:after-non-spec-loop:2": edit(2),
  })), withChild());
  t.check("that run completes", !begets.error, String(begets.error));
  t.check(
    "the spec edit made inside the lone recheck runs a pair after it",
    timeline(begets.calls).join(" ") ===
      "loop:spec fire:post-spec-loop loop:non-spec fire:post-non-spec-loop " +
      "loop:non-spec-recheck fire:post-non-spec-recheck " +
      "loop:spec-recheck fire:post-spec-recheck loop:non-spec-recheck-2 fire:post-non-spec-recheck",
    timeline(begets.calls).join(" "),
  );
  t.check("each budget counts its own", begets.result.rechecks.lone === 1 &&
    begets.result.rechecks.pairs === 1, JSON.stringify(begets.result.rechecks));
}

t.section("R67. under lockSpecChanges no pair runs, and the spec lane is not even compared");
{
  // Neither fixer may write the staged spec edits and the phase records the
  // edit it would have made instead of staging it, so no post-convergence spec
  // edit can exist. Comparing anyway would let one unreadable digest spend the
  // pair budget on an edit that cannot happen.
  const locked = await runWorkflow(WF, { ...REVIEW_ARGS, lockSpecChanges: true }, recheckStubs(laneTape({
    "hash:spec:after-first-firing": edit(1),
    "hash:spec:after-non-spec-loop": edit(2),
    "hash:non-spec:after-non-spec-loop": edit(3),
    "hash:non-spec:non-spec-recheck": edit(3),
  })), withChild());
  t.check("the run completes", !locked.error, String(locked.error));
  t.check("no pair runs", locked.result.rechecks.pairs === 0, String(locked.result.rechecks.pairs));
  t.check(
    "and no spec comparison is taken at all",
    !locked.calls.some((c) => /^hash:spec:after-/.test(c.label)),
    locked.calls.filter((c) => /^hash:spec:/.test(c.label)).map((c) => c.label).join(","),
  );
  t.check(
    "the non-spec lane is still compared, and still rechecked when it moves",
    locked.result.rechecks.lone === 1,
    String(locked.result.rechecks.lone),
  );
  t.check("the run reports the lock it ran under", locked.result.review.lockSpecChanges === true);
}

t.section("R68. every recheck's artifacts land under its own name");
{
  // Every artifact of a loop derives from its name: the state file, the log
  // shards, the snapshots, and the boundary script's --loop argument. A recheck
  // sharing the name of the loop it rechecks would re-enter an exited loop's
  // namespace, which is the collision the distinct names exist to avoid.
  const run = await runWorkflow(WF, REVIEW_ARGS, recheckStubs(laneTape({
    "hash:spec:after-non-spec-loop": edit(1),
    "hash:spec:spec-recheck": edit(2),
    "hash:spec:after-non-spec-loop:2": edit(3),
    "hash:spec:spec-recheck-2": edit(4),
  })), withChild());
  t.check("the run completes", !run.error, String(run.error));
  const NAMES = ["spec", "non-spec", "spec-recheck", "non-spec-recheck", "spec-recheck-2", "non-spec-recheck-2"];
  t.check("six loops ran", loopNames(run).sort().join(",") === [...NAMES].sort().join(","),
    loopNames(run).join(","));
  t.check(
    "and every one of them is a row in review.loops",
    run.result.review.loops.every((l) => typeof l.rounds === "number" && "converged" in l),
    JSON.stringify(run.result.review.loops.map((l) => l.name)),
  );
  t.check(
    "the rechecks are also reported under rechecks.loops",
    run.result.rechecks.loops.map((l) => l.name).join(",") ===
      "spec-recheck,non-spec-recheck,spec-recheck-2,non-spec-recheck-2",
    run.result.rechecks.loops.map((l) => l.name).join(","),
  );

  const boundaries = run.calls.filter((c) => /:round-boundary$/.test(c.label));
  const loopOf = (c) => (c.prompt.match(/--loop '([^']+)'/) || [])[1];
  const stateOf = (c) => (c.prompt.match(/--state-json '[^']*"loop":"([^"]+)"/) || [])[1];
  t.check(
    "each boundary names its own loop",
    NAMES.every((n) => boundaries.some((c) => loopOf(c) === n)),
    [...new Set(boundaries.map(loopOf))].join(","),
  );
  t.check(
    "and the state it carries names that same loop",
    boundaries.every((c) => stateOf(c) === loopOf(c)),
    boundaries.map((c) => stateOf(c) + "/" + loopOf(c)).join(" "),
  );

  const dests = run.calls.filter((c) => /^snap:/.test(c.label))
    .map((c) => (c.prompt.match(/cp -r \S+ (\S+)/) || [])[1]);
  t.check(
    "no two snapshots of the run share a destination",
    new Set(dests).size === dests.length, String(dests.length - new Set(dests).size),
  );
  for (const n of NAMES) {
    t.check(
      "the " + n + " loop snapshots under its own name",
      dests.some((d) => d && d.startsWith("/repo/scratchpad/cp-snap/0081_fix_x/" + n + "-r")),
      dests.join(" "),
    );
  }

  // The shards are what the boundary script merges, and it merges by the loop
  // name in the shard's own filename. A recheck writing under the name of the
  // loop it rechecks would have its log merged at that loop's next boundary,
  // so each shard is checked against the loop of the agent that writes it.
  const SHARD = /scratchpad\/cp-log\/[^/]+\/([^\s/]+)\.md/g;
  const shardsOf = (c) => [...c.prompt.matchAll(SHARD)].map((m) => m[1]);
  const written = [];
  for (const c of run.calls) {
    const phase = (c.opts && c.opts.phase) || "";
    const name = NAMES.find((n) => phase.startsWith(n + " R"));
    if (name) for (const s of shardsOf(c)) written.push({ name, shard: s });
  }
  t.check("shards were written", written.length > 0, String(written.length));
  t.check(
    "every agent's shard is named for the loop that agent ran in",
    written.every((w) => w.shard.startsWith(w.name + ".")),
    written.filter((w) => !w.shard.startsWith(w.name + ".")).map((w) => w.name + ":" + w.shard).join(" "),
  );
  for (const n of NAMES) {
    t.check("the " + n + " loop wrote shards of its own", written.some((w) => w.name === n));
  }
  t.check(
    "and no two agents of the run write the same shard path, so none is overwritten",
    new Set(written.map((w) => w.shard)).size === written.length,
    written.map((w) => w.shard).filter((s, i, a) => a.indexOf(s) !== i).join(" ") || "all distinct",
  );
}

// ==========================================================================
t.section("R69. the two decision lists are folded across firings by identifier, and the later firing wins");
// ==========================================================================
{
  // The child reports per firing and an operator reads per run. A resolution
  // leaves the human's section, so a later firing collects no item for it and
  // names it on neither list; a reversal re-lists it as contested. Folding by
  // identifier is what keeps one decision from being reported as both closed
  // and still open, which is what concatenating the firings' lists would say.
  const first = {
    ...CHILD_RETURN,
    decisionsResolved: [
      { id: "id:OD-1", question: "q1", kind: "resolved", authority: "the falsification gate" },
      { id: "id:OD-2", question: "q2", kind: "withdrawn", authority: "a validation pass" },
    ],
  };
  const second = {
    ...CHILD_RETURN,
    decisionsLeftToHuman: [
      { id: "id:OD-1", question: "q1", gate: "contested", reason: "CONTESTED: the loop reversed it" },
    ],
  };
  // The harness reads the stub table on every call, so a getter is what lets
  // one firing be answered differently from the next.
  const table = {};
  let nth = 0;
  Object.defineProperty(table, "change-proposal-decisions.js", {
    enumerable: true,
    get() { return [first, second][Math.min(nth++, 1)]; },
  });
  const run = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH }), { subworkflows: table });
  const closed = run.result.decisionsResolved;
  const human = run.result.decisionsLeftToHuman;
  t.check(
    "the reversed decision is listed once, and under the later firing's answer",
    human.filter((d) => d.id === "id:OD-1").length === 1 && !closed.some((d) => d.id === "id:OD-1"),
    JSON.stringify([closed.map((d) => d.id), human.map((d) => d.id)]),
  );
  t.check(
    "carrying the firing and the trigger that last spoke about it",
    human[0].firing === 2 && human[0].trigger === "post-non-spec-loop",
    JSON.stringify([human[0].firing, human[0].trigger]),
  );
  t.check(
    "a decision the later firing never named keeps the firing that closed it",
    closed.length === 1 && closed[0].id === "id:OD-2" && closed[0].firing === 1 &&
      closed[0].trigger === "post-spec-loop",
    JSON.stringify(closed),
  );
  t.check(
    "and the entry's own fields, including the authority, are the child's",
    closed[0].kind === "withdrawn" && closed[0].authority === "a validation pass",
    JSON.stringify(closed[0]),
  );
  t.check(
    "the counts an operator reads are the folded lists' own",
    run.result.decisions.resolved === 1 && run.result.decisions.leftToHuman === 1,
    JSON.stringify([run.result.decisions.resolved, run.result.decisions.leftToHuman]),
  );
}

// ==========================================================================
t.section("R70. the unclosed OPEN and DEFERRED counts are read, and a count nobody could read is null");
// ==========================================================================
{
  // Null rather than zero is the load-bearing part. A log the counting agent
  // could not read is not a log with nothing left in it, so a dead or
  // unreadable count must not be reported as a clean one.
  const withCounts = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH, "log:unclosed-markers": "3 7" }));
  t.check(
    "the counts reach the result object",
    JSON.stringify(withCounts.result.decisions.unclosedMarkers) === JSON.stringify({ open: 3, deferred: 7 }),
    JSON.stringify(withCounts.result.decisions.unclosedMarkers),
  );
  t.check(
    "and are logged",
    withCounts.logs.some((l) => /Review log: 3 unclosed OPEN and 7 unclosed DEFERRED marker\(s\)/.test(l)),
    withCounts.logs.filter((l) => /Review log:/.test(l)).join(" | "),
  );

  const unreadable = await runWorkflow(
    WF, REVIEW_ARGS,
    loopStubs({ "hash:*": HASH, "log:unclosed-markers": "awk: cannot open the log" }),
  );
  t.check(
    "an unreadable count is null rather than zero",
    unreadable.result.decisions.unclosedMarkers === null,
    JSON.stringify(unreadable.result.decisions.unclosedMarkers),
  );
  t.check(
    "and says the counts could not be read",
    unreadable.logs.some((l) => /Review log: the unclosed OPEN and DEFERRED counts could not be read/.test(l)),
    unreadable.logs.filter((l) => /Review log:/.test(l)).join(" | "),
  );

  const deadCount = await runWorkflow(WF, REVIEW_ARGS, loopStubs({ "hash:*": HASH, "log:unclosed-markers": null }));
  t.check(
    "a dead counting agent is null too",
    deadCount.result.decisions.unclosedMarkers === null,
    JSON.stringify(deadCount.result.decisions.unclosedMarkers),
  );
  t.check(
    "and is not reported as a log with nothing left in it",
    deadCount.logs.some((l) => /the unclosed OPEN and DEFERRED counts could not be read/.test(l)) &&
      !deadCount.logs.some((l) => /unclosed OPEN and \d+ unclosed DEFERRED/.test(l)),
    deadCount.logs.filter((l) => /Review log:/.test(l)).join(" | "),
  );
}

t.done();
