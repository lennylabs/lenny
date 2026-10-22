// Resume replay: a run interrupted part-way and relaunched with the same
// arguments replays every agent call it completed before the stop.
//
// The runtime replays a call from its journal only while every call before it
// was invoked with the same prompt and options in the same order (see the
// journal model in harness.mjs). The defect this file pins: a run stopped by
// the account limit in its non-spec loop was relaunched with identical
// arguments, and the open-decisions firing after the spec loop re-ran live,
// baseline commit included, followed by every non-spec round. The prompts were
// byte-identical. What moved was the ORDER: the firing's sub-task wave invoked
// the corpus inventory after an agent whose latency differs between a live run
// and a replay, beside a reading held behind a nested parallel(), so the two
// were invoked in the other order on the resume and the key chain diverged.
//
// Each case runs the real workflow body twice against the modelled journal:
// once live with latencies that vary by label and a stop part-way, and once as
// the resume. Run: node .claude/tests/resume-replay.test.mjs

import { runJournaled, replayDivergence, suite } from "./harness.mjs";

const t = suite("resume-replay");

// Deterministic latencies that differ by label, so the live run's completion
// order is not its invocation order. `slow` names labels that take longest,
// which is how the measured run's digest agent behaved.
function latencyProfile(seed, slow = []) {
  return (call) => {
    if (slow.some((re) => re.test(call.label))) return 40;
    let h = seed;
    for (const ch of call.label) h = (h * 31 + ch.charCodeAt(0)) % 9973;
    return 2 + (h % 17);
  };
}
const PROFILES = [
  ["digest slow", latencyProfile(7, [/:impact-inputs$/])],
  ["collectors slow", latencyProfile(13, [/:human-decisions:/, /:out-of-scope-defects$/])],
  ["hash spread", latencyProfile(101)],
  ["first designer slow", latencyProfile(29, [/:answer-design:0$/, /:falsify:0$/])],
];

// `makeStubs` builds a fresh table for each run, as a relaunched process starts
// with fresh script state, so a stub that counts its calls starts at zero.
async function interruptAndResume(relPath, args, makeStubs, { latency, stopAt, inline, source }) {
  const first = await runJournaled(relPath, args, makeStubs(), { latency, stopAt, inline, source });
  const second = await runJournaled(relPath, args, makeStubs(), { latency, journal: first.journal, inline, source });
  return { first, second, divergence: replayDivergence(first, second) };
}
const describe = (d) => (d ? "#" + d.index + " " + d.label + (d.other ? " vs " + d.other : "") + ": " + d.why : "");

// ---- the open-decisions subworkflow ---------------------------------------

const DWF = ".claude/workflows/change-proposal-decisions.js";
const DIR = "/repo/proposals/0099_open_decisions";
const DARGS = {
  proposalPath: "proposals/0099_open_decisions",
  repoRoot: "/repo",
  date: "2026-09-04",
  runTag: "replay-suite",
  firing: 1,
  trigger: "post-spec-loop",
};
const entry = (over) => ({
  id: "",
  decision: "does the gateway retry a refused lease once?",
  home: "summary-open-decisions",
  deliverable: "SPEC-1",
  marker: "",
  groundQuotes: ['spec/example.md, "Lease retries" — "the gateway retries a refused lease once"'],
  questionsAsked: ["Q: what does the lease section say / A: it retries once"],
  caseFor: "the spec settles it",
  caseAgainst: "the chart may disagree",
  whatWouldFlipIt: "a contrary default in the chart",
  counterfactual: "nothing downstream moves",
  cascades: [],
  disposition: "human",
  recommendation: "leave it to the human",
  summaryAction: "unchanged",
  ...over,
});
const found = (...decisions) => ({ coverage: "swept every home named in the brief", decisions });
const STANDS = { falsified: false, howConclusive: "none", theDispositionIAttacked: "x", reasoning: "y", evidence: [] };
const REFUTE_TO_RESOLVE = {
  falsified: true, howConclusive: "conclusive", theDispositionIAttacked: "human",
  reasoning: "the shipped spec settles it", evidence: 'spec/example.md, "Gate"', fallbackDisposition: "resolve",
};
const DESIGN = {
  answerable: true, answerKey: "equality", answer: "The gate compares for equality.",
  authority: 'spec/example.md, "Gate"', why: "it is settled", where: [DIR + "/x.spec-changes.md — SPEC-1"],
};
function decisionStubs() {
  let reading = 0;
  return {
    "*:commit": { outcome: "committed", sha: "c0ffee1", error: "", outsideProposal: [] },
    "*:delta:firing": { files: [{ path: "p.md", added: 4, removed: 1 }], outsideProposal: [] },
    "*:delta:apply:*": () => ({ files: [{ path: "w" + ++reading + ".md", added: 3, removed: 1 }], outsideProposal: [] }),
    "*:corpus": { proposals: [{ proposal: "0098_fix_neighbour", status: "Draft", date: "2026-09-01", dateSource: "drafted" }] },
    "*:reversal-check": { items: [] },
    "*:triage": { humanDecisions: true, outOfScopeDefects: true, why: "the diff could hold either kind" },
    "*:impact-inputs": "0123456789ab\n",
    "*:human-decisions:*": found(
      entry({ id: "OD-1", decision: "does the gate stay equality?" }),
      entry({ id: "OD-2", decision: "which timeout applies?", disposition: "human" }),
    ),
    "*:out-of-scope-defects": found(entry({
      id: "OOS-1", decision: "the drain leaks a lease", home: "out-of-scope-defect", disposition: "out-of-scope-stands",
    })),
    "*:other-proposals": found(entry({
      id: "IMP-1", decision: "0098 restates the fence", home: "other-proposal", disposition: "impact-row",
      recommendation: "| 0098 | restates the fence | draft; revise |",
    })),
    "f1:falsify:0": REFUTE_TO_RESOLVE,
    "f1:falsify:1": REFUTE_TO_RESOLVE,
    "*:falsify:*": STANDS,
    "*:answer-design:*": DESIGN,
    "*:falsify-answer:*": STANDS,
    "*:apply:*": { outcome: "edited", recordWritten: true, where: [DIR + "/x.summary.md — the section"] },
    "*:cleanup": { outcome: "rewritten", sections: [], relocated: [] },
    "*:verify": { conforms: true, sections: [], defects: [] },
    default: {},
  };
}

t.section("R1. an open-decisions firing interrupted part-way replays its whole completed prefix");
for (const [name, latency] of PROFILES) {
  for (const stopLabel of ["f1:commit", "f1:apply:1", "f1:verify"]) {
    const { first, second, divergence } = await interruptAndResume(
      DWF, DARGS, decisionStubs, { latency, stopAt: (c) => c.label === stopLabel },
    );
    t.check(
      name + ", stopped at " + stopLabel + ": the interrupted run reached the stop",
      first.stopIndex > 0, String(first.stopIndex),
    );
    t.check(
      name + ", stopped at " + stopLabel + ": every call before the stop replays, byte-identical",
      divergence === null, describe(divergence),
    );
    t.check(
      name + ", stopped at " + stopLabel + ": the first live call of the resume is the stopped one",
      second.firstMiss === first.stopIndex, second.firstMiss + " vs " + first.stopIndex,
    );
  }
}
{
  // The wave this defect came from runs every collector, the corpus inventory,
  // and the answer designer with its falsifier, so a pass that never reached
  // them would prove nothing about them.
  const { first } = await interruptAndResume(DWF, DARGS, decisionStubs, {
    latency: PROFILES[0][1], stopAt: (c) => c.label === "f1:verify",
  });
  const before = first.calls.slice(0, first.stopIndex).map((c) => c.label);
  for (const l of ["f1:impact-inputs", "f1:corpus", "f1:human-decisions:1", "f1:out-of-scope-defects",
    "f1:other-proposals", "f1:answer-design:0", "f1:answer-design:1", "f1:falsify-answer:0", "f1:commit"]) {
    t.check("the replayed prefix covers " + l, before.includes(l), before.join(","));
  }
}

t.section("R2. a later firing, with records to check and a triage, replays too");
{
  const once = await runJournaled(DWF, DARGS, decisionStubs(), { latency: PROFILES[0][1] });
  const args2 = {
    ...DARGS, firing: 2, trigger: "post-non-spec-loop", phaseState: once.result.phaseState, humanReadings: 3,
  };
  for (const [name, latency] of PROFILES) {
    const { first, divergence } = await interruptAndResume(
      DWF, args2, decisionStubs, { latency, stopAt: (c) => c.label === "f2:commit" },
    );
    t.check(name + ": the second firing ran its reversal check and three readings before the stop",
      first.calls.slice(0, first.stopIndex).filter((c) => /^f2:(reversal-check|human-decisions:\d)$/.test(c.label)).length === 4,
      first.calls.slice(0, first.stopIndex).map((c) => c.label).join(","));
    t.check(name + ": every call before the stop replays", divergence === null, describe(divergence));
  }
}

// ---- the parent, with the subworkflow run inline ---------------------------

const CWF = ".claude/workflows/change-proposal.js";
const NEW_ARGS = {
  mode: "new",
  problem: "The adapter drops a tracing frame and nothing counts it.",
  context: "spec/example.md, 'Metric catalog' names the catalog",
  nextNumber: "0081",
  date: "2026-08-31",
  exemplar: "proposals/0080_fix_x.md",
  repoRoot: "/repo",
  runTag: "replay-parent",
  maxSpecReviewRounds: 2,
  maxNonSpecReviewRounds: 3,
};
const FINDING = (title) => ({
  title, severity: "high", kind: "defect", provenance: "staged",
  location: "x.non-spec-changes.md", evidence: "it says so", remedy: "fix it", lens: "",
});
function parentStubs() {
  return {
    ...decisionStubs(),
    init: "created",
    "validate:": { verdict: "stands", findings: [{ statement: "s", evidence: "spec/example.md", loadBearing: true }] },
    "validate:consolidate": {
      viable: true, restatement: "The adapter drops a tracing frame.",
      title: "Catalog the adapter tracing frame drop counter", kind: "fix", confirmed: ["absent"], refuted: [],
    },
    "draft:": { viable: true, approach: "add the counter", changes: [], rejected: [], risks: [] },
    "draft:consolidate": {
      viable: true, title: "Catalog the adapter tracing frame drop counter", kind: "fix",
      problemRestatement: "r", decisions: ["d"],
      changes: [{ id: "SPEC-1", title: "catalog row", targets: ["spec/example.md"], rationale: "r", sketch: "s" }],
      nonGoals: [],
    },
    "challenge:": { verdict: "keep", reasons: "it survives", evidence: [] },
    write: "written",
    conventions: "conforms",
    snap: "DONE",
    diffcount: "0",
    "hash:*": "0123456789abcdef0123456789abcdef  -\n",
    "save-state:decisions:check*": "",
    "save-state:decisions:join": "OK 1 1",
    "*:round-boundary":
      '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
    // Each review lens finds one defect in the first non-spec round and nothing
    // after, so the loop runs a verify, a fix, and a second round.
    "*:review:*": (call) => ({
      coverage: "read it all",
      findings: /^non-spec R1: /.test(String(call.opts.phase || "")) ? [FINDING("defect from " + call.label)] : [],
    }),
    "*:dedup": { findings: [FINDING("one merged defect")] },
    "*:verify": { first: true, firstReason: "material", second: true, secondReason: "the evidence holds", conforms: true, sections: [], defects: [] },
    "*:fix-plan": { groups: [{ id: "G1", findings: ["one merged defect"], files: ["x.non-spec-changes.md"] }] },
    "*:fix-design:*": { design: "edit it" },
    "*:fix:*": { summary: "fixed", filesChanged: ["x.non-spec-changes.md"] },
    "*:post-fix-review": { coverage: "read it", findings: [] },
    default: {},
  };
}

t.section("R3. change-proposal stopped in its non-spec loop replays the spec loop, firing 1, and the rounds before the stop");
for (const [name, latency] of PROFILES) {
  // The stop lands in the second non-spec round, after firing 1, its state
  // save, and a whole non-spec round with a verify and a fix have completed.
  const stopAt = (c) => /^non-spec R2: /.test(String(c.opts.phase || ""));
  const { first, divergence } = await interruptAndResume(CWF, NEW_ARGS, parentStubs, { latency, stopAt, inline: true });
  const before = first.calls.slice(0, first.stopIndex === -1 ? first.calls.length : first.stopIndex).map((c) => c.label);
  t.check(name + ": the interrupted run reached its non-spec loop", first.stopIndex > 0,
    String(first.stopIndex) + " :: " + before.slice(-8).join(","));
  t.check(name + ": firing 1 ran inline before the stop", before.includes("f1:commit") && before.includes("f1:corpus"),
    before.filter((l) => l.startsWith("f1:")).join(","));
  t.check(name + ": the state save ran before the stop", before.some((l) => l.startsWith("save-state:decisions:")),
    before.filter((l) => l.startsWith("save-state")).join(","));
  t.check(name + ": every call before the stop replays, byte-identical", divergence === null, describe(divergence));
}

t.section("R4. the split verifier's second skeptics replay whatever order the first ones finished in");
{
  // Three findings reach verification. The first skeptic on the FIRST finding
  // is the slowest, so live its second skeptic is invoked last; a replay
  // invokes all three at once.
  const stubs = () => ({
    ...parentStubs(),
    "*:dedup": { findings: [FINDING("first merged defect"), FINDING("second merged defect"), FINDING("third merged defect")] },
    "*:verify-material": { confirmed: true, reason: "material" },
    "*:verify-evidence": { confirmed: false, reason: "the citation does not say it" },
  });
  const base = latencyProfile(3);
  const latency = (c) => (/verify-material/.test(c.label) && c.prompt.includes("first merged defect") ? 40 : base(c));
  const stopAt = (c) => /^non-spec R2: /.test(String(c.opts.phase || ""));
  const { first, divergence } = await interruptAndResume(
    CWF, { ...NEW_ARGS, verifyMode: "split" }, stubs, { latency, stopAt, inline: true },
  );
  const before = first.calls.slice(0, first.stopIndex === -1 ? first.calls.length : first.stopIndex).map((c) => c.label);
  t.check("both skeptics ran on each finding before the stop",
    before.filter((l) => /^r1:verify-(material|evidence)$/.test(l)).length >= 6,
    before.filter((l) => /verify/.test(l)).join(","));
  t.check("every call before the stop replays", divergence === null, describe(divergence));
}

t.section("R5. reading the decisions state back retries its slices in rounds that replay");
{
  // Two slices, neither of which ever verifies, so both are retried. The first
  // slice's reader is the slow one, so live its retry is invoked last.
  const stubs = () => ({
    ...parentStubs(),
    "resume-state:decisions:meta": '{"chunks":[{"len":10,"sum":100},{"len":12,"sum":120}]}',
    "resume-state:decisions:*": "CP_SLICE_BEGIN not the slice CP_SLICE_END",
  });
  const base = latencyProfile(5);
  const latency = (c) => (/^resume-state:decisions:\d/.test(c.label) && / 0 \d+\n/.test(c.prompt) ? 40 : base(c));
  const stopAt = (c) => c.label === "f1:commit";
  const { first, divergence } = await interruptAndResume(
    CWF, { ...NEW_ARGS, resumeState: true }, stubs, { latency, stopAt, inline: true },
  );
  const before = first.calls.slice(0, first.stopIndex).map((c) => c.label);
  t.check("both slices were retried before the stop",
    before.filter((l) => /^resume-state:decisions:\d:retry\d$/.test(l)).length >= 2,
    before.filter((l) => l.startsWith("resume-state")).join(","));
  t.check("every call before the stop replays", divergence === null, describe(divergence));
}

t.section("R6. implement-proposal-build stopped part-way replays the steps before the stop");
{
  // The build's parallel stages each invoke one agent per slot, so it carried
  // none of the orderings fixed above. This holds it there.
  const args = {
    proposalPath: "proposals/0081_fix_x", repoRoot: "/repo", date: "d",
    plan: { blastRadius: [], steps: [
      { id: "S1", lane: "spec", title: "s", work: "SPEC-1", targets: ["spec/example.md"], tiers: ["static"], checklistStep: "S1", dependsOn: [] },
      { id: "S2", lane: "code", title: "c", work: "w", targets: ["pkg/a"], tiers: ["unit"], checklistStep: "S2", dependsOn: ["S1"] },
    ] },
  };
  const stubs = () => ({
    "checklist-ticks": { ticked: [] }, baseline: { sha: "b" }, "build:S2:base": { sha: "b" },
    "spec-targets:*": { files: ["spec/example.md"] }, "lease-open:*": "{}", "lease-release:*": "{}",
    "apply:*": { applied: ["SPEC-1"], unappliable: [], deviations: [] },
    "verify:S1:spec": { discrepancies: [] }, "commit-spec:*": "ok",
    "compile:*": { compiles: true, errors: [] },
    "lease-check:*": { leaseHeld: false },
    "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/a.go"], testsAddedOrModified: [] },
    "review:*": { findings: [] }, "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
    "tick:*": "DONE", "compile-guard:*": { clean: true, compiles: true },
    "proposal-edit-audit": { edited: false, commits: [] }, default: {},
  });
  for (const [name, latency] of PROFILES) {
    for (const stopLabel of ["verify:S2:r1", "sweep:find"]) {
      const { first, divergence } = await interruptAndResume(
        ".claude/workflows/implement-proposal-build.js", args, stubs,
        { latency, stopAt: (c) => c.label === stopLabel },
      );
      t.check(name + ", stopped at " + stopLabel + ": the interrupted build reached the stop",
        first.stopIndex > 0, String(first.stopIndex));
      t.check(name + ", stopped at " + stopLabel + ": every call before the stop replays",
        divergence === null, describe(divergence));
    }
  }
}

// ---- negative controls ------------------------------------------------------
//
// The model has to be able to fail. Each script below has one of the orderings
// the fixes removed, and each must diverge on the resume.

t.section("N1. negative control: the pre-fix wave (a chained slot beside a nested parallel) diverges");
{
  const source = `
    const wave = await parallel([
      () => parallel([() => agent("read the decisions", { label: "f1:human-decisions:1" })]),
      () => agent("collect the out-of-scope defects", { label: "f1:out-of-scope-defects" }),
      () => agent("digest the impact inputs", { label: "f1:impact-inputs" })
        .then(() => agent("inventory the corpus", { label: "f1:corpus" }))
        .then(() => agent("sweep the other proposals", { label: "f1:other-proposals" })),
    ]);
    await agent("commit the baseline", { label: "f1:commit" });
    await agent("apply one item", { label: "f1:apply:0" });
    return wave;
  `;
  const { first, second, divergence } = await interruptAndResume("inline", {}, () => ({ default: "ok" }), {
    source, latency: PROFILES[0][1], stopAt: (c) => c.label === "f1:apply:0",
  });
  t.check("the interrupted run completed the wave and the commit", first.stopIndex === 6, String(first.stopIndex));
  t.check("the resume diverges inside the wave", divergence !== null && divergence.index < first.stopIndex, describe(divergence));
  t.check("so the commit re-runs live, as the measured resume's did",
    second.calls.some((c) => c.label === "f1:commit" && !c.replayed));
}

t.section("N2. negative control: agents run beside the main flow diverge");
{
  const source = `
    const save = (async () => {
      await agent("write part 0", { label: "save-state:decisions:0" });
      await agent("check the parts", { label: "save-state:decisions:check" });
      await agent("join the parts", { label: "save-state:decisions:join" });
    })();
    for (let i = 1; i <= 4; i++) await agent("main step " + i, { label: "main:" + i });
    await save;
    await agent("after the save", { label: "tail" });
  `;
  const latency = (c) => (c.label.startsWith("save-state") ? 15 : 10);
  const { first, divergence } = await interruptAndResume("inline", {}, () => ({ default: "ok" }), {
    source, latency, stopAt: (c) => c.label === "tail",
  });
  t.check("the interrupted run completed the main flow and the save", first.stopIndex === 7, String(first.stopIndex));
  t.check("the resume diverges where the save interleaves", divergence !== null, describe(divergence));
}

t.section("N3. negative control: a follow-on call chained to its own slot's first call diverges");
{
  const source = `
    await parallel([0, 1, 2].map((i) => () =>
      agent("design " + i, { label: "answer-design:" + i })
        .then(() => agent("falsify " + i, { label: "falsify-answer:" + i }))));
    await agent("apply", { label: "apply" });
  `;
  const latency = (c) => (c.label.startsWith("answer-design:") ? 30 - 10 * Number(c.label.slice(-1)) : 3);
  const { first, divergence } = await interruptAndResume("inline", {}, () => ({ default: "ok" }), {
    source, latency, stopAt: (c) => c.label === "apply",
  });
  t.check("the interrupted run completed every slot", first.stopIndex === 6, String(first.stopIndex));
  t.check("the resume diverges at the first follow-on call", divergence !== null, describe(divergence));
}

t.section("N4. positive control: the same orderings rewritten as barriers replay");
{
  const source = `
    const designs = await parallel([0, 1, 2].map((i) => () => agent("design " + i, { label: "answer-design:" + i })));
    await parallel(designs.map((d, i) => () => agent("falsify " + i, { label: "falsify-answer:" + i })));
    await agent("write part 0", { label: "save-state:decisions:0" });
    for (let i = 1; i <= 4; i++) await agent("main step " + i, { label: "main:" + i });
    await agent("apply", { label: "apply" });
  `;
  const latency = (c) => (c.label.startsWith("answer-design:") ? 30 - 10 * Number(c.label.slice(-1)) : 3);
  const { divergence } = await interruptAndResume("inline", {}, () => ({ default: "ok" }), {
    source, latency, stopAt: (c) => c.label === "apply",
  });
  t.check("every call before the stop replays", divergence === null, describe(divergence));
}

t.done();
