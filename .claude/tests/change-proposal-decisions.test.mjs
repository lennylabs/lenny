// Behavioural tests for the open-decisions-and-impact-review phase itself.
//
// The parent's suite stubs this subworkflow's RETURN, so nothing there ever
// runs its body: what the parent's tests pin is the seam. This file runs the
// child, with only its agents stubbed, so every join, gate, ordering and
// carry-forward decision under test is made by the script rather than by the
// test. The argument object the parent sends is checked separately, in
// .claude/tests/change-proposal-decisions-forwarding.test.mjs.
//
// The agent labels are the addressing scheme. Every one is `f<firing>:<stage>`,
// with an index where a stage fans out, which is what lets a stub answer one
// item's falsifier differently from another's.

import { loadWorkflow, runWorkflow, suite, matching, never, firstIndex } from "./harness.mjs";

const WF = ".claude/workflows/change-proposal-decisions.js";
const t = suite("change-proposal-decisions");

// The proposal the firings run against, resolved the way the workflow's own
// proposalFiles() resolves it, so an assertion naming a file names the path the
// prompts carry.
const DIR = "/repo/proposals/0099_open_decisions";
const F = (role) => DIR + "/0099_open_decisions." + role + ".md";
const P = {
  root: DIR,
  summary: F("summary"),
  spec: F("spec-changes"),
  nonSpec: F("non-spec-changes"),
  problem: F("problem-statement"),
  log: F("review-log"),
};

// ---- stubs ---------------------------------------------------------------

const OK_COMMIT = { outcome: "committed", sha: "c0ffee1", error: "", outsideProposal: [] };
const NO_DELTA = { files: [], outsideProposal: [] };
const SOME_DELTA = { files: [{ path: "p.md", added: 4, removed: 1 }], outsideProposal: [] };

const verdict = (over) => ({
  falsified: false,
  howConclusive: "none",
  theDispositionIAttacked: "the disposition the phase reached, restated",
  reasoning: "what I looked for and what I found",
  evidence: [],
  ...over,
});
const STANDS = verdict({});
const REFUTES = verdict({ falsified: true, howConclusive: "conclusive", reasoning: "the ground does not say it" });
const UNCERTAIN = verdict({ howConclusive: "partial", reasoning: "a doubt I can articulate and cannot settle" });

// The Apply stage takes a cumulative reading after every agent that ran and
// derives each item's own diff from the pair around it, so a stub that answers
// every reading identically reports every Apply as empty. This grows the file
// list by one per reading, which is one own-diff file per item.
function growingDelta() {
  let n = 0;
  return () => {
    n++;
    return {
      files: Array.from({ length: n }, (_, i) => ({ path: "wrote" + i + ".md", added: 3, removed: 1 })),
      outsideProposal: [],
    };
  };
}

const EMPTY = { coverage: "swept the whole population; nothing in it", decisions: [] };

const base = () => ({
  "*:commit": OK_COMMIT,
  "*:delta:firing": SOME_DELTA,
  "*:delta:apply:*": growingDelta(),
  "*:corpus": { proposals: [] },
  "*:reversal-check": { items: [] },
  "*:human-decisions:*": EMPTY,
  "*:implementor-blanks": EMPTY,
  "*:out-of-scope-defects": EMPTY,
  "*:other-proposals": EMPTY,
  "*:falsify:*": STANDS,
  "*:apply:*": { outcome: "edited", wrote: "the text this item staged", where: [P.summary + " — the section"] },
  "*:cleanup": { outcome: "rewritten", sections: [], relocated: [] },
  "*:verify": { conforms: true, sections: [], defects: [] },
});

const ARGS = (over = {}) => ({
  proposalPath: "proposals/0099_open_decisions",
  repoRoot: "/repo",
  date: "2026-09-04",
  runTag: "decisions-suite",
  firing: 1,
  trigger: "post-non-spec-loop",
  ...over,
});

const fire = (over, stubs) => runWorkflow(WF, ARGS(over), { ...base(), ...stubs });

// One entry as a collector returns it, with every field the schema requires.
const entry = (over = {}) => ({
  id: "",
  decision: "does the gateway retry a refused lease once?",
  home: "summary-open-decisions",
  deliverable: "SPEC-1",
  marker: "",
  groundQuotes: ['spec/04_gateway.md:12 — "the gateway retries a refused lease once"'],
  questionsAsked: ["Q: what does the lease section say / A: it retries once (spec/04_gateway.md)"],
  caseFor: "the spec settles it",
  caseAgainst: "the spec may have drifted from the chart",
  whatWouldFlipIt: "a contrary default in the chart",
  counterfactual: "nothing downstream moves either way",
  cascades: [],
  disposition: "human",
  recommendation: "leave it to the human",
  summaryAction: "unchanged",
  ...over,
});
const found = (...decisions) => ({ coverage: "swept every home named in the brief", decisions });

const promptOf = (calls, label) => (calls.find((c) => c.label === label) || { prompt: "" }).prompt;
const ids = (list) => (list || []).map((x) => x.id).join(",");
const itemById = (res, id) => (res && res.items ? res.items.find((i) => i.id === id) : undefined);

// ==========================================================================
t.section("D1. every firing runs the collectors, each under its own brief");
// ==========================================================================
{
  // Sub-tasks 1 through 4, 7 and 8 run on every firing, whatever the
  // adjudication finds and whether or not anything has changed since the last
  // one. The gate and the write path are the two that run over items, so they
  // are pinned by the sections below rather than here.
  const { result, calls, error } = await fire({}, {});
  t.check("the firing completes", !error && result && result.status === "done", String(error || (result && result.status)));

  const readings = matching(calls, "f1:human-decisions:");
  t.check("sub-task 1 runs three independent adjudicators", readings.length === 3, String(readings.length));
  t.check(
    "over one brief, so the three readings are of the same population",
    new Set(readings.map((c) => c.prompt)).size === 1,
  );
  for (const [name, label] of [
    ["sub-task 2", "f1:implementor-blanks"],
    ["sub-task 3", "f1:out-of-scope-defects"],
    ["sub-task 4", "f1:other-proposals"],
  ]) {
    t.check(name + " runs once", matching(calls, label).length === 1);
  }
  t.check("sub-task 7 runs", matching(calls, "f1:cleanup").length === 1);
  t.check("sub-task 8 runs", matching(calls, "f1:verify").length === 1);
  t.check("and the cleanup runs after the write path", firstIndex(calls, "f1:cleanup") > firstIndex(calls, "f1:commit"));
  t.check("with the verify pass last of the three", firstIndex(calls, "f1:verify") > firstIndex(calls, "f1:cleanup"));

  // Each brief names its own population and no other's, which is the whole
  // reason there are four rather than one merged inventory.
  const POP = [
    ["f1:human-decisions:1", "Your population is EVERY DECISION THIS PROPOSAL LEAVES TO A HUMAN"],
    ["f1:implementor-blanks", "Your population is EVERY DECISION THIS PROPOSAL LEAVES TO THE IMPLEMENTOR"],
    ["f1:out-of-scope-defects", "Your population is EVERY DEFECT THIS PROPOSAL EXPLICITLY CALLS OUT AS OUT OF SCOPE"],
    ["f1:other-proposals", "Your population is EVERY OTHER PROPOSAL ON DISK THIS ONE BEARS ON"],
  ];
  for (const [label, sentence] of POP) {
    t.check(label + " states its own population", promptOf(calls, label).includes(sentence));
    const strays = POP.filter(([l, s]) => l !== label && promptOf(calls, label).includes(s));
    t.check("and carries no other collector's", strays.length === 0, strays.map(([l]) => l).join(","));
  }

  // The rules the design rehomed out of the deleted lens, each in the brief it
  // puts them in and nowhere else in the phase.
  const REHOMED = [
    ["the GIVE IT TO THE HUMAN test", "f1:human-decisions:1", "GIVE IT TO THE HUMAN only when one of these holds"],
    ["the NEGATIVE TEST", "f1:human-decisions:1", "THE NEGATIVE TEST. A decision belongs to the human only if a person could answer it in one sitting"],
    ["the bar on promoting a bounded blank", "f1:implementor-blanks", "not yours to expand, second-guess, or promote to a human "],
    ["the bar on filing a blank because it is open", "f1:implementor-blanks", "Never report a blank as an open decision merely because it is open"],
    ["the STALE GROUND reconciliation", "f1:human-decisions:1", "STALE GROUND. Its citation, the text it quotes"],
    ["the MIS-STATED reconciliation", "f1:human-decisions:1", "MIS-STATED. It asks a question this proposal does not actually face"],
    ["the ORPHANED reconciliation", "f1:human-decisions:1", "ORPHANED. Its subject is a deliverable this proposal no longer stages"],
    ["the MISSING drift", "f1:human-decisions:1", "MISSING. A survivor whose only home is a design item"],
    ["the RESOLVED SINCE drift", "f1:human-decisions:1", "RESOLVED SINCE. A later round answered it"],
  ];
  // The three readings of sub-task 1 share one brief, so the comparison is
  // against the other SUB-TASKS rather than against the other agents.
  const subTaskOf = (label) => label.replace(/^f\d+:/, "").replace(/:\d+$/, "");
  for (const [what, label, text] of REHOMED) {
    t.check(what + " is in " + label + "'s brief", promptOf(calls, label).includes(text));
    const elsewhere = calls
      .filter((c) => subTaskOf(c.label) !== subTaskOf(label) && c.prompt.includes(text))
      .map((c) => c.label);
    t.check("and in no other collector's brief", elsewhere.length === 0, elsewhere.join(","));
  }

  // The drift set is pinned as a set, the way the design states it, so a later
  // edit cannot carry one of the five away unnoticed.
  const DRIFTS = ["MISSING", "RESOLVED SINCE", "STALE GROUND", "MIS-STATED", "ORPHANED"];
  const lost = DRIFTS.filter((d) => !promptOf(calls, "f1:human-decisions:1").includes(d));
  t.check("all five drifts the deleted lens named are still named", lost.length === 0, lost.join(",") || "all");

  // The defaults each brief states, which are what make a blank and an
  // out-of-scope call survive by construction rather than by argument.
  t.check(
    "sub-task 2 defaults to leaving the blank alone",
    promptOf(calls, "f1:implementor-blanks").includes("THE DEFAULT IS TO LEAVE IT ALONE"),
  );
  t.check(
    "and protects a marker against narrowing inside it",
    promptOf(calls, "f1:implementor-blanks").includes("a preference between two ways of bounding one is not your finding"),
  );
  t.check(
    "sub-task 3 defaults to the call standing",
    promptOf(calls, "f1:out-of-scope-defects").includes("THE DEFAULT IS THAT THE CALL IS RIGHT"),
  );

  // A dead collector is a different answer from an empty population: the
  // sub-task's population goes unadjudicated, and the firing says so rather
  // than reporting nothing to do.
  for (const [name, key, line] of [
    ["sub-task 2", "implementor-blanks", "Sub-task 2 (decisions left to the implementor)"],
    ["sub-task 3", "out-of-scope-defects", "Sub-task 3 (out-of-scope defect declarations)"],
    ["sub-task 4", "other-proposals", "Sub-task 4 (impacts on other proposals)"],
  ]) {
    const dead = await fire({}, { ["f1:" + key]: null });
    t.check(
      name + "'s dead collector leaves its population unadjudicated rather than empty",
      (dead.result.unadjudicated || []).includes(key),
      (dead.result.unadjudicated || []).join(","),
    );
    t.check(
      "and says so rather than reporting nothing to do",
      dead.logs.some((l) => l.startsWith(line) && /the population is UNADJUDICATED rather than empty/.test(l)),
    );
    t.check(
      "with the population named in the collection tally",
      dead.logs.some((l) => /^Collected .*UNADJUDICATED: /.test(l) && l.includes(key)),
    );
    t.check(
      "and the dead agent named",
      (dead.result.deadAgents || []).includes("f1:" + key),
      (dead.result.deadAgents || []).join(","),
    );
  }
}

// ==========================================================================
t.section("D2. a bounded blank and an out-of-scope call survive their sub-task by default");
// ==========================================================================
{
  // The two defaults, exercised rather than read: each item reaches its own
  // falsifier under the brief its disposition calls for, stands under the
  // standing posture, and is not rewritten.
  const blank = entry({
    home: "implementor-blank",
    deliverable: "CODE-3",
    marker: "IMPLEMENTOR'S CHOICE: any buffer size between 4 and 64 KiB",
    decision: "how large is the read buffer?",
    disposition: "implementor",
    recommendation: "the marker bounds the choice; leave it",
    summaryAction: "not-applicable",
  });
  const defect = entry({
    home: "out-of-scope-defect",
    deliverable: "CODE-4",
    marker: "out of scope: the flaky drain test",
    decision: "does this proposal fix the flaky drain test?",
    disposition: "out-of-scope-stands",
    recommendation: "record the defect row; the call is right",
    summaryAction: "unchanged",
  });
  const { result, calls } = await fire(
    {},
    { "f1:implementor-blanks": found(blank), "f1:out-of-scope-defects": found(defect) },
  );
  const b = itemById(result, "marker:code-3:implementor's choice: any buffer size between 4 and 64 kib");
  const d = itemById(result, "marker:code-4:out of scope: the flaky drain test");
  t.check("the blank is collected", !!b, ids(result && result.items));
  t.check("the out-of-scope call is collected", !!d, ids(result && result.items));
  t.check("the blank keeps its implementor disposition", b && b.disposition === "implementor", b && b.disposition);
  t.check("and stands at the gate", b && b.gate === "stands" && b.survives === true, b && b.gate);
  t.check("under the DELEGATION brief", /You are the DELEGATION judge/.test(promptOf(calls, "f1:falsify:0")));
  t.check("which stands unless conclusively falsified", /STANDS UNLESS YOU FALSIFY IT CONCLUSIVELY/.test(promptOf(calls, "f1:falsify:0")));
  t.check(
    "and no Apply rewrites it",
    b && b.apply && b.apply.status === "no-edit-needed",
    b && b.apply && b.apply.status,
  );
  t.check("the out-of-scope call stands too", d && d.disposition === "out-of-scope-stands" && d.gate === "stands", d && d.gate);
  t.check("under the RESIDUAL brief", /You are the RESIDUAL judge/.test(promptOf(calls, "f1:falsify:1")));

  // A disposition outside a brief's own vocabulary is read as the brief being
  // stepped outside, and clamps to that brief's default rather than becoming a
  // new outcome.
  const strayed = await fire({}, { "f1:implementor-blanks": found({ ...blank, disposition: "human" }) });
  const s = itemById(strayed.result, "marker:code-3:implementor's choice: any buffer size between 4 and 64 kib");
  t.check("a disposition the brief may not return clamps to its default", s && s.disposition === "implementor", s && s.disposition);
  t.check(
    "and the clamp is logged",
    strayed.logs.some((l) => /is not one this brief may return; recorded as implementor/.test(l)),
  );

  // `not-applicable` says the summary holds NO entry for the item, which is the
  // opposite of already carrying it. Reading the two as one dropped the item
  // from the Apply queue, and the defect row it needed was written nowhere
  // else: the cleanup pass is a format sweep barred from adding a decision.
  const absent = await fire(
    {},
    { "f1:out-of-scope-defects": found({ ...defect, summaryAction: "not-applicable" }) },
  );
  const a = itemById(absent.result, "marker:code-4:out of scope: the flaky drain test");
  t.check("a defect row the summary does not carry reaches Apply", a && a.apply && a.apply.status !== "no-edit-needed", a && a.apply && a.apply.status);
  t.check(
    "under the brief that writes the row",
    promptOf(absent.calls, "f1:apply:0").includes("## Defects in the shipped tree that this proposal does not stage"),
  );
}

// ==========================================================================
t.section("D3. sub-task 1's join is script-side: unanimity, or the human's");
// ==========================================================================
{
  const OD = "OD-7";
  const key = "id:" + OD;
  const q = { id: OD, decision: "does the lease survive a gateway restart?" };

  // Three adjudicators disposing of one item differently. The join needs no
  // clause for a split: an item is `resolve` only when all three resolve it to
  // one answer, so anything else is the human's by construction.
  const split = await fire({}, {
    "f1:human-decisions:1": found(entry({ ...q, disposition: "resolve", answer: "it survives", summaryAction: "withdrawn" })),
    "f1:human-decisions:2": found(entry({ ...q, disposition: "human", summaryAction: "updated" })),
    "f1:human-decisions:3": found(entry({ ...q, disposition: "implementor", summaryAction: "unchanged" })),
  });
  const s = itemById(split.result, key);
  t.check("an item three adjudicators dispose differently reaches the human", s && s.disposition === "human", s && s.disposition);
  t.check("recorded as a split", s && s.agreement === "split", s && s.agreement);
  t.check(
    "with all three positions kept",
    s && (s.readings || []).map((r) => r.disposition).sort().join(",") === "human,implementor,resolve",
    s && (s.readings || []).map((r) => r.disposition).join(","),
  );
  t.check(
    "and the falsifier is briefed on the disposition the join reached",
    /You are the HUMAN-QUESTION judge/.test(promptOf(split.calls, "f1:falsify:0")),
  );

  // Three that all resolve, to substantively different answers. Picking one is
  // what this phase does not do; the alternatives are recorded instead.
  const divergent = await fire({}, {
    "f1:human-decisions:1": found(entry({ ...q, disposition: "resolve", answer: "the lease survives the restart" })),
    "f1:human-decisions:2": found(entry({ ...q, disposition: "resolve", answer: "the lease is revoked and re-minted" })),
    "f1:human-decisions:3": found(entry({ ...q, disposition: "resolve", answer: "the lease expires with the pod" })),
  });
  const dv = itemById(divergent.result, key);
  t.check("three resolves to differing answers reach the human", dv && dv.disposition === "human", dv && dv.disposition);
  t.check("recorded as a divergent resolve", dv && dv.agreement === "divergent-resolve", dv && dv.agreement);
  t.check("with the three answers recorded as alternatives", dv && (dv.alternatives || []).length === 3, dv && String((dv.alternatives || []).length));
  t.check(
    "and none of them picked and applied",
    divergent.result && divergent.result.applied.length === 0,
    ids(divergent.result && divergent.result.applied),
  );

  // The join folds case and whitespace and nothing else. It borrowed the key
  // normalizer once, which truncates at 160 characters, so three answers that
  // agreed for a paragraph and contradicted each other at its end joined as
  // one and were applied without the human seeing either.
  const prefix =
    "the lease is revoked at gateway restart and re-minted by the successor replica from the pod's " +
    "stored nonce, exactly as the staged text for SPEC-2 now states it, and ";
  const longTail = await fire({}, {
    "f1:human-decisions:1": found(entry({ ...q, disposition: "resolve", answer: prefix + "the old lease id is retired" })),
    "f1:human-decisions:2": found(entry({ ...q, disposition: "resolve", answer: prefix + "the old lease id is reused" })),
    "f1:human-decisions:3": found(entry({ ...q, disposition: "resolve", answer: prefix + "the old lease id is retired" })),
  });
  const lt = itemById(longTail.result, key);
  t.check("the shared prefix is longer than the key normalizer's bound", prefix.length > 160, String(prefix.length));
  t.check("answers that diverge past that bound still reach the human", lt && lt.disposition === "human", lt && lt.disposition);
  t.check("recorded as a divergent resolve", lt && lt.agreement === "divergent-resolve", lt && lt.agreement);
  t.check(
    "and none of them applied",
    longTail.result && longTail.result.applied.length === 0,
    ids(longTail.result && longTail.result.applied),
  );

  // A `human` item whose home is anywhere but the summary is not in the summary
  // yet, whatever its readings said the summary entry needs. The migration out
  // of a staged change file's `## Open decisions for review` is the case the
  // design names, and it is the Apply stage that writes it.
  const staged = { ...q, home: "staged-open-decisions", disposition: "human", summaryAction: "not-applicable" };
  const migrate = await fire({}, {
    "f1:human-decisions:1": found(entry(staged)),
    "f1:human-decisions:2": found(entry(staged)),
    "f1:human-decisions:3": found(entry(staged)),
  });
  const mg = itemById(migrate.result, key);
  t.check("an entry still in a staged change file reaches Apply", mg && mg.apply && mg.apply.status !== "no-edit-needed", mg && mg.apply && mg.apply.status);
  t.check(
    "under the brief that migrates it into the summary",
    promptOf(migrate.calls, "f1:apply:0").includes("MIGRATE it: write the entry in the summary, delete it there"),
  );

  // The control: unanimity on one answer is the only route to `resolve`.
  const agreed = await fire({}, {
    "f1:human-decisions:*": found(entry({ ...q, disposition: "resolve", answer: "the lease survives the restart", summaryAction: "withdrawn" })),
  });
  const ag = itemById(agreed.result, key);
  t.check("three resolves to one answer do resolve", ag && ag.disposition === "resolve", ag && ag.disposition);
  t.check("recorded as unanimous", ag && ag.agreement === "unanimous-resolve", ag && ag.agreement);

  // A dead adjudicator leaves two readings, and two readings cannot be
  // unanimous, so the item cannot reach `resolve` however the survivors read it.
  const oneDead = await fire({}, {
    "f1:human-decisions:1": found(entry({ ...q, disposition: "resolve", answer: "the lease survives the restart" })),
    "f1:human-decisions:2": null,
    "f1:human-decisions:3": found(entry({ ...q, disposition: "resolve", answer: "the lease survives the restart" })),
  });
  const dd = itemById(oneDead.result, key);
  t.check("a dead adjudicator leaves two readings", dd && (dd.readings || []).length === 2, dd && String((dd.readings || []).length));
  t.check("so the item cannot reach resolve", dd && dd.disposition === "human", dd && dd.disposition);
  t.check("recorded as incomplete readings", dd && dd.agreement === "incomplete-readings", dd && dd.agreement);
  t.check(
    "and the shortfall is logged rather than absorbed",
    oneDead.logs.some((l) => /2\/3 adjudicators returned; no item can be unanimous on fewer than three readings/.test(l)),
  );
  t.check(
    "with the dead reading named",
    (oneDead.result.deadAgents || []).includes("f1:human-decisions:2"),
    (oneDead.result.deadAgents || []).join(","),
  );
  t.check(
    "all three populations still swept",
    oneDead.calls.filter((c) => c.label === "f1:human-decisions:2").length === 4,
    "retries",
  );

  // All three dead is not two dead: there is no reading at all, so sub-task 1's
  // population is unadjudicated rather than swept clean.
  const allDead = await fire({}, { "f1:human-decisions:*": null });
  t.check(
    "three dead adjudicators leave the population unadjudicated",
    (allDead.result.unadjudicated || []).includes("human-decisions"),
    (allDead.result.unadjudicated || []).join(","),
  );
  t.check(
    "reported as unadjudicated rather than as a population with nothing in it",
    allDead.logs.some((l) => /all three adjudicators returned nothing; the population is UNADJUDICATED/.test(l)),
  );
  t.check(
    "and no shortfall line claims a partial reading",
    !allDead.logs.some((l) => /adjudicators returned; no item can be unanimous/.test(l)),
  );
  t.check(
    "with all three named dead",
    [1, 2, 3].every((n) => (allDead.result.deadAgents || []).includes("f1:human-decisions:" + n)),
    (allDead.result.deadAgents || []).join(","),
  );
}

// ==========================================================================
t.section("D4. the gate: one falsifier per item, asymmetric defaults, a script-side tally");
// ==========================================================================
{
  // Four items in one firing, one per disposition the gate briefs differently:
  // a resolution, a human decision, an implementor blank, and an out-of-scope
  // call. Their order in `items` is the order the collectors run, which is the
  // order the falsifier indices follow.
  const RESOLVE = entry({
    id: "OD-1",
    decision: "which timeout does the adapter use?",
    disposition: "resolve",
    answer: "thirty seconds, as the chart already sets",
    // `updated` rather than `withdrawn`, so this item's route through the
    // report is the gate's refusal rather than the refused withdrawal D11
    // covers: the two reasons are different and both are pinned.
    summaryAction: "updated",
  });
  const HUMAN = entry({ id: "OD-2", decision: "does this proposal widen to the CLI?", disposition: "human", summaryAction: "added" });
  const BLANK = entry({
    home: "implementor-blank", deliverable: "CODE-3", marker: "IMPLEMENTOR'S CHOICE: the retry jitter",
    decision: "how much jitter?", disposition: "implementor", summaryAction: "not-applicable",
  });
  const DEFECT = entry({
    home: "out-of-scope-defect", deliverable: "CODE-4", marker: "out of scope: the drain race",
    decision: "does this proposal fix the drain race?", disposition: "out-of-scope-stands", summaryAction: "added",
  });
  const population = {
    "f1:human-decisions:*": found(RESOLVE, HUMAN),
    "f1:implementor-blanks": found(BLANK),
    "f1:out-of-scope-defects": found(DEFECT),
  };
  const K = { resolve: "id:OD-1", human: "id:OD-2", blank: "marker:code-3:implementor's choice: the retry jitter", defect: "marker:code-4:out of scope: the drain race" };

  const run = await fire({}, {
    ...population,
    // Its own falsifier per item, briefed on its own disposition: the
    // resolution is conclusively refuted, the human decision is left with an
    // articulated doubt, and the other two stand.
    "f1:falsify:0": REFUTES,
    "f1:falsify:1": UNCERTAIN,
  });
  const r = run.result;
  t.check("every item gets its own falsifier", matching(run.calls, "f1:falsify:").length === 4, String(matching(run.calls, "f1:falsify:").length));
  for (const [i, id] of [[0, K.resolve], [1, K.human], [2, K.blank], [3, K.defect]]) {
    const p = promptOf(run.calls, "f1:falsify:" + i);
    const others = Object.values(K).filter((x) => x !== id && p.includes('"id": "' + x + '"'));
    t.check("falsifier " + i + " holds only " + id, p.includes('"id": "' + id + '"') && others.length === 0, others.join(","));
  }
  t.check("the GROUND judge reads the resolution", /You are the GROUND judge/.test(promptOf(run.calls, "f1:falsify:0")));
  t.check("and is told an uncertain verdict sets the item aside", /`partial` sets the item aside exactly as `conclusive` does/.test(promptOf(run.calls, "f1:falsify:0")));

  const res = itemById(r, K.resolve);
  const hum = itemById(r, K.human);
  t.check("a conclusively falsified disposition is refuted", res && res.gate === "refuted", res && res.gate);
  t.check("and is not applied", !(r.applied || []).some((a) => a.id === K.resolve), ids(r.applied));
  t.check(
    "a refutation naming no replacement sets the item aside rather than substituting one",
    res && res.survives === false && res.disposition === "resolve",
    res && res.disposition,
  );
  t.check(
    "with the refutation recorded against it",
    res && res.falsification && res.falsification.falsified === true && res.falsification.howConclusive === "conclusive",
  );
  t.check(
    "and its entry still listed for the human",
    (r.decisionsLeftToHuman || []).some((d) => d.id === K.resolve && /the gate refuted the resolution/.test(d.reason)),
    ids(r.decisionsLeftToHuman),
  );
  t.check("a human decision whose falsifier is uncertain STANDS", hum && hum.gate === "stands", hum && hum.gate);
  t.check("and is applied", (r.applied || []).some((a) => a.id === K.human), ids(r.applied));

  // The same doubt under the other posture: a `resolve` creates text nothing
  // else reviews, so an uncertain falsifier refutes it.
  const uncertainResolve = await fire({}, { ...population, "f1:falsify:0": UNCERTAIN });
  const ur = itemById(uncertainResolve.result, K.resolve);
  t.check("a resolve whose falsifier is uncertain is refuted", ur && ur.gate === "refuted", ur && ur.gate);
  t.check(
    "and not applied",
    !(uncertainResolve.result.applied || []).some((a) => a.id === K.resolve),
    ids(uncertainResolve.result.applied),
  );
  t.check(
    "which is logged as the posture rather than as a refutation found",
    uncertainResolve.logs.some((l) => /on an uncertain verdict under a posture that needs support/.test(l)),
  );

  // The tally is the script's. An Apply that reports a set-aside item as its
  // own business changes nothing about what is applied.
  const overreaching = await fire({}, {
    ...population,
    "f1:falsify:0": REFUTES,
    "f1:apply:*": {
      outcome: "edited",
      wrote: "I also resolved OD-1 while I was in the file",
      where: [P.summary + " — the section"],
      note: "the survivors are OD-1, OD-2 and both markers",
    },
  });
  const ov = overreaching.result;
  t.check(
    "an Apply reporting a different survivor set does not change what is applied",
    !(ov.applied || []).some((a) => a.id === K.resolve),
    ids(ov.applied),
  );
  t.check(
    "and the count of Apply agents is the script's own",
    matching(overreaching.calls, "f1:apply:").length === (ov.applied || []).length + (ov.failedItems || []).length,
    String(matching(overreaching.calls, "f1:apply:").length),
  );

  // A dead agent costs its own item and no other's, on either stage.
  const deadFalsifier = await fire({}, { ...population, "f1:falsify:1": null });
  const df = itemById(deadFalsifier.result, K.human);
  t.check("a dead falsifier leaves its own item unadjudicated", df && df.gate === "unadjudicated", df && df.gate);
  t.check("and unapplied", !(deadFalsifier.result.applied || []).some((a) => a.id === K.human), ids(deadFalsifier.result.applied));
  t.check(
    "while the rest proceed",
    (deadFalsifier.result.applied || []).some((a) => a.id === K.resolve),
    ids(deadFalsifier.result.applied),
  );
  t.check(
    "and the dead falsifier is named",
    (deadFalsifier.result.deadAgents || []).includes("f1:falsify:1"),
    (deadFalsifier.result.deadAgents || []).join(","),
  );

  const deadApply = await fire({}, { ...population, "f1:apply:0": null });
  const da = deadApply.result;
  t.check(
    "a dead Apply leaves its own item unapplied",
    (da.failedItems || []).some((x) => x.id === K.resolve && /returned nothing after retries/.test(x.reason)),
    ids(da.failedItems),
  );
  t.check("while the rest proceed", (da.applied || []).some((a) => a.id === K.human), ids(da.applied));
  t.check(
    "and it is logged as this item's loss",
    deadApply.logs.some((l) => /is NOT applied and the rest proceed/.test(l)),
  );
}

// ==========================================================================
t.section("D5. the write path: sequential, each after the first told what the earlier ones wrote");
// ==========================================================================
{
  const A = entry({ id: "OD-1", decision: "which timeout?", disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const B = entry({ id: "OD-2", decision: "does it widen to the CLI?", disposition: "human", summaryAction: "added" });
  const population = { "f1:human-decisions:*": found(A, B) };

  const run = await fire({}, {
    ...population,
    "f1:apply:0": { outcome: "edited", wrote: "Thirty seconds, from the chart default.", where: [P.spec + " — SPEC-1"] },
    "f1:apply:1": { outcome: "edited", wrote: "The CLI question, stated for the human.", where: [P.summary + " — open decisions"] },
  });
  const order = run.calls.map((c) => c.label).filter((l) => /^f1:(apply|delta:apply):/.test(l));
  t.check(
    "the Apply agents run one after another, each with its own reading taken between",
    order.join(" ") === "f1:apply:0 f1:delta:apply:0 f1:apply:1 f1:delta:apply:1",
    order.join(" "),
  );
  const first = promptOf(run.calls, "f1:apply:0");
  const second = promptOf(run.calls, "f1:apply:1");
  t.check("the first Apply is told of no earlier one", !first.includes("WHAT THE EARLIER APPLIES IN THIS FIRING ALREADY DID"));
  t.check("the second is", second.includes("WHAT THE EARLIER APPLIES IN THIS FIRING ALREADY DID"));
  t.check("and is told what the first wrote", second.includes("Thirty seconds, from the chart default."), "the earlier Apply's own text");
  t.check("and where it wrote it", second.includes(P.spec + " — SPEC-1"));
  t.check("each holds one item", first.includes("YOU HOLD ONE ITEM"));
  t.check("and both are applied", (run.result.applied || []).length === 2, ids(run.result.applied));

  // Git is the evidence. An Apply that reports an edit the diff does not carry
  // is a failed item, which is not detectable from the agent's own report.
  const empty = await fire({}, { ...population, "*:delta:apply:*": NO_DELTA });
  t.check(
    "an Apply whose git diff is empty is a failed item",
    (empty.result.failedItems || []).length === 2 &&
      (empty.result.failedItems || []).every((x) => /the diff under the proposal pathspec is empty/.test(x.reason)),
    ids(empty.result.failedItems),
  );
  t.check("and nothing is recorded as applied", (empty.result.applied || []).length === 0, ids(empty.result.applied));
  t.check("the prompt says so before the agent writes", first.includes("GIT IS THE EVIDENCE"));

  // The missing evidence and the missing edit are different answers.
  const noEvidence = await fire({}, { ...population, "*:delta:apply:*": null });
  t.check(
    "a dead change-detection agent fails the item for the missing evidence rather than the missing edit",
    (noEvidence.result.failedItems || []).some((x) => /no git evidence/.test(x.reason)),
    (noEvidence.result.failedItems || []).map((x) => x.reason).join(" | "),
  );
}

// ==========================================================================
t.section("D6. the baseline commit: before Apply, the proposal directory alone");
// ==========================================================================
{
  const A = entry({ id: "OD-1", decision: "which timeout?", disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const population = { "f1:human-decisions:*": found(A) };

  const run = await fire({}, population);
  const commit = promptOf(run.calls, "f1:commit");
  t.check("the commit runs before any Apply", firstIndex(run.calls, "f1:commit") < firstIndex(run.calls, "f1:apply:"));
  t.check("and after the collectors, so it commits what they read", firstIndex(run.calls, "f1:commit") > firstIndex(run.calls, "f1:human-decisions:"));
  t.check("it stages under the proposal pathspec", commit.includes("git add -A -- " + P.root));
  t.check("and commits under it", commit.includes("-- " + P.root));
  t.check("the pathspec is stated as the whole scope", commit.includes("THE PATHSPEC IS THE WHOLE SCOPE"));
  t.check("and the sweeping forms are barred", commit.includes("never `git commit -a`") && commit.includes("never `git checkout`"));

  // A firing that cannot take its baseline cannot tell what it wrote from what
  // was already there, so it stops rather than authoring onto an unusable one.
  const failed = await fire({}, {
    ...population,
    "f1:commit": { outcome: "failed", error: "fatal: cannot lock ref HEAD", outsideProposal: [] },
  });
  t.check("a failed commit aborts the firing", failed.result && failed.result.status === "aborted", failed.result && failed.result.status);
  t.check(
    "and reports why",
    /cannot lock ref HEAD/.test((failed.result && failed.result.abortReason) || ""),
    failed.result && failed.result.abortReason,
  );
  t.check("no Apply runs", never(failed.calls, "f1:apply:"));
  t.check("no cleanup runs", never(failed.calls, "f1:cleanup"));
  t.check("no verify runs", never(failed.calls, "f1:verify"));
  t.check("and the abort is logged", failed.logs.some((l) => /Baseline commit FAILED/.test(l)));

  // A firing may follow a stage that changed nothing.
  const emptyCommit = await fire({}, {
    ...population,
    "f1:commit": { outcome: "empty", sha: "c0ffee1", error: "", outsideProposal: [] },
  });
  t.check("an empty commit is not a failure", emptyCommit.result && emptyCommit.result.status === "done", emptyCommit.result && emptyCommit.result.status);
  t.check("the firing proceeds to Apply", !never(emptyCommit.calls, "f1:apply:"));
  t.check("and says HEAD is the baseline", emptyCommit.logs.some((l) => /nothing to commit under .*; HEAD is the baseline/.test(l)));

  // Another actor's changes are none of this run's business.
  const outside = await fire({}, {
    ...population,
    "f1:commit": { ...OK_COMMIT, outsideProposal: ["pkg/gateway/lease.go"] },
    "f1:delta:firing": { files: [{ path: "p.md", added: 1, removed: 0 }], outsideProposal: ["docs/guide.md"] },
  });
  t.check(
    "a change outside the proposal is left uncommitted and reported",
    (outside.result.outsideProposal || []).includes("pkg/gateway/lease.go"),
    (outside.result.outsideProposal || []).join(","),
  );
  t.check(
    "and both readings of it are kept rather than the later one winning",
    (outside.result.outsideProposal || []).includes("docs/guide.md"),
    (outside.result.outsideProposal || []).join(","),
  );
  t.check("it is logged", outside.logs.some((l) => /Left uncommitted, outside the proposal/.test(l)));
}

// ==========================================================================
t.section("D7. cross-firing state: contested, carried forward, reworded");
// ==========================================================================
{
  const OD = { id: "OD-1", decision: "which timeout does the adapter use?" };
  const RESOLVED = entry({ ...OD, disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const first = await fire({ firing: 1 }, {
    "f1:human-decisions:*": found(RESOLVED),
    "f1:apply:0": { outcome: "edited", wrote: "The adapter waits thirty seconds.", where: [P.spec + " — SPEC-1"] },
  });
  const rec = first.result.phaseState.itemRecords["id:OD-1"];
  t.check("firing 1 records what it applied", rec && rec.applyStatus === "applied", rec && rec.applyStatus);
  t.check("with the text it wrote", rec && rec.wrote === "The adapter waits thirty seconds.", rec && rec.wrote);
  t.check("and where", rec && (rec.where || []).join(",") === P.spec + " — SPEC-1", rec && (rec.where || []).join(","));

  const carriedState = () => JSON.parse(JSON.stringify(first.result.phaseState));

  // A record restored from a resumed run, or written by an older shape of this
  // file, can arrive without `unmatchedAt`. Indexing it threw a TypeError that
  // nulled the whole firing, so one malformed record cost every item in it.
  {
    const malformed = carriedState();
    for (const k of Object.keys(malformed.itemRecords)) delete malformed.itemRecords[k].unmatchedAt;
    const out = await runWorkflow(WF, ARGS({ firing: 2, phaseState: malformed }), {
      ...base(),
      "f2:human-decisions:*": found(entry({ ...OD, id: "OD-9", disposition: "human" })),
    });
    t.check("a record with no unmatchedAt does not null the firing", !!out.result, String(out.result));
    t.check(
      "and the malformed record is normalised rather than dropped",
      !!(out.result && out.result.phaseState && out.result.phaseState.itemRecords),
    );
  }

  // The reversal. The tree is read rather than an agent asked to re-judge, and
  // an item the loop reverted is the human's from here on.
  const reverted = await runWorkflow(WF, ARGS({ firing: 2, phaseState: carriedState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: "id:OD-1", state: "absent", nowCarries: "the question, open again" }] },
    "f2:human-decisions:*": found(RESOLVED),
  });
  const rv = itemById(reverted.result, "id:OD-1");
  t.check("an applied item the loop reverted is CONTESTED", rv && rv.gate === "contested", rv && rv.gate);
  t.check("routed to the human", rv && rv.disposition === "human", rv && rv.disposition);
  t.check("and never re-applied", never(reverted.calls, "f2:apply:"), "an Apply ran");
  t.check("nor re-adjudicated", never(reverted.calls, "f2:falsify:"), "a falsifier ran");
  t.check(
    "with both positions recorded",
    (reverted.result.contested || []).some(
      (c) => c.id === "id:OD-1" && c.appliedAtFiring === 1 && c.contestedAtFiring === 2 && /open again/.test(c.nowCarries),
    ),
    JSON.stringify(reverted.result.contested),
  );
  t.check(
    "and it is listed for the human as contested",
    (reverted.result.decisionsLeftToHuman || []).some((d) => d.id === "id:OD-1" && /CONTESTED/.test(d.reason)),
    ids(reverted.result.decisionsLeftToHuman),
  );
  t.check("which is logged", reverted.logs.some((l) => /CONTESTED: id:OD-1 was applied at firing 1/.test(l)));

  // And it stays contested: insisting is the loop this rule exists to prevent.
  const third = await runWorkflow(WF, ARGS({ firing: 3, phaseState: JSON.parse(JSON.stringify(reverted.result.phaseState)) }), {
    ...base(),
    "f3:human-decisions:*": found(RESOLVED),
  });
  t.check("a contested item is still contested a firing later", (third.result.contested || []).some((c) => c.id === "id:OD-1"));
  t.check("and still not re-applied", never(third.calls, "f3:apply:"), "an Apply ran");

  // Nothing touched it: the disposition carries forward and costs no agent.
  const untouched = await runWorkflow(WF, ARGS({ firing: 2, phaseState: carriedState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: "id:OD-1", state: "present", nowCarries: "" }] },
    "f2:human-decisions:*": found(RESOLVED),
  });
  const un = itemById(untouched.result, "id:OD-1");
  t.check("an item nothing has touched carries its disposition forward", un && un.disposition === "resolve", un && un.disposition);
  t.check("with the firing that reached it named", un && un.carried && un.carried.fromFiring === 1, JSON.stringify(un && un.carried));
  t.check("it is not re-gated", never(untouched.calls, "f2:falsify:"), "a falsifier ran");
  t.check("nor re-applied", never(untouched.calls, "f2:apply:"), "an Apply ran");
  t.check("and the carry is logged", untouched.logs.some((l) => /1 item\(s\) carried forward untouched/.test(l)));

  // Reworded rather than reverted. The match is on the identifier, so the
  // rewrite still finds its record.
  const reworded = await runWorkflow(WF, ARGS({ firing: 2, phaseState: carriedState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: "id:OD-1", state: "present", nowCarries: "" }] },
    "f2:human-decisions:*": found(
      entry({
        id: "OD-1",
        decision: "what timeout does the adapter apply to a refused lease?",
        marker: "a wholly rewritten marker line",
        disposition: "human",
        recommendation: "a rewritten recommendation",
        summaryAction: "updated",
      }),
    ),
  });
  const rw = itemById(reworded.result, "id:OD-1");
  t.check("a reworded entry still matches its record", rw && !!rw.carried, JSON.stringify(rw && rw.carried));
  t.check(
    "and carries the earlier disposition rather than the reworded one",
    rw && rw.disposition === "resolve",
    rw && rw.disposition,
  );
  t.check("so it is not re-adjudicated", never(reworded.calls, "f2:falsify:"), "a falsifier ran");

  // An UNSTAMPED item keys on its deliverable plus the marker text as first
  // recorded, so the same match holds where `lockSpecChanges` forbids a stamp.
  // Without the pin a reworded line mints a new key, and the same substance is
  // contested under the old one and re-applied under the new one at once.
  const DRAIN = entry({
    home: "out-of-scope-defect",
    deliverable: "CODE-4",
    marker: "out of scope: the drain race",
    decision: "is the drain race right to be out of scope?",
    disposition: "out-of-scope-wrong",
    recommendation: "the call is wrong; the proposal must specify the drain order",
    summaryAction: "added",
  });
  const DRAIN_KEY = "marker:code-4:out of scope: the drain race";
  const REWORDED = { ...DRAIN, marker: "out of scope for now: the drain race" };
  const markerFirst = await fire({ firing: 1 }, {
    "f1:out-of-scope-defects": found(DRAIN),
    "f1:apply:0": { outcome: "edited", wrote: "The drain order is specified.", where: [P.spec + " — CODE-4"] },
  });
  t.check(
    "an unstamped item is keyed on its deliverable and marker",
    !!markerFirst.result.phaseState.itemRecords[DRAIN_KEY],
    Object.keys(markerFirst.result.phaseState.itemRecords).join(","),
  );
  const markerState = () => JSON.parse(JSON.stringify(markerFirst.result.phaseState));

  const markerCarried = await runWorkflow(WF, ARGS({ firing: 2, phaseState: markerState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: DRAIN_KEY, state: "present", nowCarries: "" }] },
    "f2:out-of-scope-defects": found(REWORDED),
  });
  const mc = itemById(markerCarried.result, DRAIN_KEY);
  t.check("a reworded marker line matches the key it was first given", mc && !!mc.carried, ids(markerCarried.result.items));
  t.check("so it is not re-gated", never(markerCarried.calls, "f2:falsify:"), "a falsifier ran");
  t.check("nor re-applied", never(markerCarried.calls, "f2:apply:"), "an Apply ran");

  const markerReverted = await runWorkflow(WF, ARGS({ firing: 2, phaseState: markerState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: DRAIN_KEY, state: "absent", nowCarries: "nothing about the drain order" }] },
    "f2:out-of-scope-defects": found(REWORDED),
  });
  const mr = itemById(markerReverted.result, DRAIN_KEY);
  t.check("and a reworded line the loop reversed is CONTESTED", mr && mr.gate === "contested", mr && mr.gate);
  t.check("routed to the human", mr && mr.disposition === "human", mr && mr.disposition);
  t.check("never re-adjudicated", never(markerReverted.calls, "f2:falsify:"), "a falsifier ran");
  t.check("and never re-applied under a second key", never(markerReverted.calls, "f2:apply:"), "an Apply ran");

  // The pin is tight enough that a second declaration under the same
  // deliverable is its own item rather than the first one reworded.
  const OTHER = { ...DRAIN, marker: "out of scope: the lease leak", decision: "is the lease leak right to be out of scope?" };
  const markerOther = await runWorkflow(WF, ARGS({ firing: 2, phaseState: markerState() }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: DRAIN_KEY, state: "present", nowCarries: "" }] },
    "f2:out-of-scope-defects": found(OTHER),
  });
  const mo = itemById(markerOther.result, "marker:code-4:out of scope: the lease leak");
  t.check("a different marker under the same deliverable is a different item", !!mo, ids(markerOther.result.items));
  t.check("adjudicated afresh under its own key", mo && !mo.carried && mo.gate === "stands", mo && mo.gate);

  // A record with no verdict is unmatched work rather than a disposition to
  // carry. The gate never reached the item, so a later firing must re-run it;
  // carrying the non-verdict forward would freeze it unadjudicated for the run.
  const died = await fire({ firing: 1 }, { "f1:human-decisions:*": found(RESOLVED), "f1:falsify:0": null });
  const dd = itemById(died.result, "id:OD-1");
  t.check("an item whose falsifier died is left unadjudicated", dd && dd.gate === "unadjudicated", dd && dd.gate);
  const retried = await runWorkflow(WF, ARGS({ firing: 2, phaseState: JSON.parse(JSON.stringify(died.result.phaseState)) }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: "id:OD-1", state: "present", nowCarries: "" }] },
    "f2:human-decisions:*": found(RESOLVED),
    "f2:apply:0": { outcome: "edited", wrote: "The adapter waits thirty seconds.", where: [P.spec + " — SPEC-1"] },
  });
  const rt = itemById(retried.result, "id:OD-1");
  t.check("the next firing gates it rather than carrying the non-verdict", matching(retried.calls, "f2:falsify:").length === 1, String(matching(retried.calls, "f2:falsify:").length));
  t.check("and it is adjudicated there", rt && rt.gate === "stands", rt && rt.gate);
  t.check("and applied", (retried.result.applied || []).some((a) => a.id === "id:OD-1"), ids(retried.result.applied));
}

// ==========================================================================
t.section("D8. the corpus is gathered once per run; sub-task 4 re-runs at every firing");
// ==========================================================================
{
  const CORPUS = {
    proposals: [
      { proposal: "0050_earlier", status: "Approved", date: "2026-08-20", dateSource: "approved" },
      { proposal: "0051_other", status: "Draft", date: "2026-08-29", dateSource: "commit" },
    ],
  };
  const ROW_ONE = entry({
    home: "other-proposal", deliverable: "0050_earlier", marker: "0050's SPEC-2 deliverable",
    decision: "what does this staging do to 0050?", disposition: "impact-row",
    recommendation: "0050_earlier (Approved, 2026-08-20) — its SPEC-2 loses its subject",
    affectsProposals: ["0050_earlier (Approved, 2026-08-20, approved) — SPEC-2 loses its subject — changes with the choice: no"],
    changesWithChoice: false, summaryAction: "added",
  });
  const ROW_TWO = entry({
    home: "other-proposal", deliverable: "0051_other", marker: "0051's CODE-1 deliverable",
    decision: "what does this staging do to 0051?", disposition: "impact-row",
    recommendation: "0051_other (Draft, 2026-08-29) — its CODE-1 is invalidated by the change staged this firing",
    affectsProposals: ["0051_other (Draft, 2026-08-29, commit) — CODE-1 invalidated — changes with the choice: yes"],
    changesWithChoice: true, summaryAction: "added",
  });

  const one = await fire({ firing: 1 }, { "f1:corpus": CORPUS, "f1:other-proposals": found(ROW_ONE) });
  t.check("firing 1 gathers the inventory", matching(one.calls, "f1:corpus").length === 1);
  t.check("and hands it to sub-task 4", promptOf(one.calls, "f1:other-proposals").includes("0050_earlier — status Approved — 2026-08-20 (approved)"));
  t.check("it is carried on the phase state", (one.result.phaseState.corpus || []).length === 2, String((one.result.phaseState.corpus || []).length));

  const two = await runWorkflow(WF, ARGS({ firing: 2, phaseState: JSON.parse(JSON.stringify(one.result.phaseState)) }), {
    ...base(),
    "f2:other-proposals": found(ROW_TWO),
  });
  t.check("firing 2 gathers no inventory of its own", never(two.calls, "f2:corpus"), "the corpus agent ran again");
  t.check("and says it is reusing the run's", two.logs.some((l) => /Corpus inventory: reusing 2 row\(s\) gathered earlier in this run/.test(l)));
  t.check("while the assessment itself re-runs", matching(two.calls, "f2:other-proposals").length === 1);
  t.check(
    "against the staging as it stands at this firing",
    promptOf(two.calls, "f2:other-proposals").includes("THIS FIRING RE-DERIVES THE WHOLE SECTION"),
  );
  t.check(
    "and an earlier firing's rows are not treated as evidence",
    promptOf(two.calls, "f2:other-proposals").includes("an earlier firing's rows are not evidence"),
  );
  const row = itemById(two.result, "marker:0051_other:0051's code-1 deliverable");
  t.check("the new firing's row is adjudicated fresh", row && row.gate === "stands", row && row.gate);
  t.check(
    "and staged, so the impacts section reflects this firing rather than the first",
    promptOf(two.calls, "f2:apply:0").includes("its CODE-1 is invalidated by the change staged this firing"),
  );
  t.check(
    "under the brief that owns that section",
    /That section is the ONLY place this proposal asserts anything about another proposal's continued validity/.test(
      promptOf(two.calls, "f2:apply:0"),
    ),
  );

  // A row's key is the other proposal and the line being read, both of which
  // are stable across firings, while its content is a function of a staging
  // that is not. So a row that came back DIFFERENT is a new claim about another
  // proposal and goes through the gate and the write path again, rather than
  // leaving the earlier firing's row standing in the section.
  const ROW_ONE_KEY = "marker:0050_earlier:0050's spec-2 deliverable";
  const ROW_ONE_V2 = {
    ...ROW_ONE,
    recommendation: "0050_earlier (Approved, 2026-08-20) — its SPEC-2 is invalidated by the change staged this firing",
  };
  const redone = await runWorkflow(WF, ARGS({ firing: 2, phaseState: JSON.parse(JSON.stringify(one.result.phaseState)) }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: ROW_ONE_KEY, state: "present", nowCarries: "" }] },
    "f2:other-proposals": found(ROW_ONE_V2),
  });
  const rd = itemById(redone.result, ROW_ONE_KEY);
  t.check("a re-derived row that changed is adjudicated afresh", rd && !rd.carried && rd.gate === "stands", rd && rd.gate);
  t.check("through its own falsifier", matching(redone.calls, "f2:falsify:").length === 1, String(matching(redone.calls, "f2:falsify:").length));
  t.check("and written again", matching(redone.calls, "f2:apply:").length === 1, String(matching(redone.calls, "f2:apply:").length));
  t.check(
    "so the section carries this firing's row rather than the first firing's",
    promptOf(redone.calls, "f2:apply:0").includes("its SPEC-2 is invalidated by the change staged this firing"),
  );

  // The counterpart: a sweep that re-derived the same row has found nothing
  // new, so the row carries forward and costs neither a falsifier nor an Apply.
  const same = await runWorkflow(WF, ARGS({ firing: 2, phaseState: JSON.parse(JSON.stringify(one.result.phaseState)) }), {
    ...base(),
    "f2:reversal-check": { items: [{ id: ROW_ONE_KEY, state: "present", nowCarries: "" }] },
    "f2:other-proposals": found(ROW_ONE),
  });
  const sm = itemById(same.result, ROW_ONE_KEY);
  t.check("an unchanged row carries forward untouched", sm && !!sm.carried, JSON.stringify(sm && sm.carried));
  t.check("with no falsifier", never(same.calls, "f2:falsify:"), "a falsifier ran");
  t.check("and no Apply", never(same.calls, "f2:apply:"), "an Apply ran");
}

// ==========================================================================
t.section("D9. sub-task 7's cleanup: the listed sections, in order, and nothing else");
// ==========================================================================
{
  const { calls } = await fire({}, {});
  const p = promptOf(calls, "f1:cleanup");
  t.check("the cleanup states the whole job", p.includes("CARRYING EXACTLY THE SECTIONS BELOW, IN THIS ORDER, AND NOTHING ELSE"));

  const LIST = [
    "`# Summary: <title>`",
    "`## Summary`",
    "`## Goals`",
    "`## Non-goals`",
    "`## Open decisions for human to make`",
    "`## Defects in the shipped tree that this proposal does not stage`",
    "`## Impacts on other proposals`",
    "`## Deliverable index`",
  ];
  for (const h of LIST) t.check("the list carries " + h, p.includes(h));
  const at = LIST.map((h) => p.indexOf(h));
  t.check("in that order", at.every((x, i) => i === 0 || (x > at[i - 1] && x >= 0)), at.join(","));
  t.check("`## Deliverable index` last", at[at.length - 1] === Math.max(...at));

  const PARTS = ["`**Problem statement.**`", "`**What changes.**`", "`**Decisions.**`", "`**Watch out for.**`"];
  for (const part of PARTS) t.check("`## Summary` holds " + part, p.includes(part));
  const partAt = PARTS.map((x) => p.indexOf(x));
  t.check("in that order", partAt.every((x, i) => i === 0 || x > partAt[i - 1]), partAt.join(","));

  t.check("the index is preserved line for line", p.includes("`## Deliverable index` IS NOT YOURS TO MAINTAIN"));
  t.check("in last position", p.includes("in LAST position"));
  t.check("with no line added, removed, reworded or reordered", p.includes("Do not add a line, remove a line, reword a line, reorder it, or move it above another section"));

  t.check("`**Problem statement.**` survives", p.includes("`**Problem statement.**` SURVIVES AND IS NOT REWRITTEN"));
  t.check("and an edit restating what the problem IS is refused", p.includes("you may not restate what the problem IS") && p.includes("an edit that makes one is refused"));

  t.check("no `### Retired` block survives inside the decisions section", p.includes("NO `### Retired` OR EQUIVALENT BLOCK SURVIVES INSIDE `## Open decisions for human to make`"));
  t.check("because a resolved or withdrawn decision LEAVES that section", p.includes("LEAVES that section"));

  t.check("unlisted content is relocated", p.includes("Content the list does not name is RELOCATED rather than deleted"));
  t.check("and deleting it is not an option the pass has", p.includes("Deleting content is not an option this pass has"));
  t.check("each kind naming its own destination", p.includes("A BLOCK OF CONFIRMED SHIPPED-TREE DEFECTS is promoted UNCHANGED") && p.includes("PROSE ABOUT ANOTHER PROPOSAL merges into `## Impacts on other proposals`"));
  t.check("and anything unplaceable becomes an OPEN line rather than a deletion", p.includes("record it as an `OPEN` line in " + P.log));

  t.check("the identifiers survive a rewrite", p.includes("PRESERVE THE IDENTIFIERS"));
  t.check("it is a format pass rather than a review", p.includes("THIS IS A FORMAT PASS, NOT A REVIEW"));
  t.check("its grant is the summary and the review log", p.includes(P.summary + " — the whole file") && p.includes(P.log + " — where a relocated correction"));
  t.check("and the staged change files are out of bounds to it", p.includes("the staged change files, the problem statement and the implementation checklist included"));
}
{
  const dead = await fire({}, { "f1:cleanup": null });
  t.check("a dead cleanup agent is reported rather than read as a clean pass", dead.result.summaryCleanup === null, JSON.stringify(dead.result.summaryCleanup));
  t.check("and logged", dead.logs.some((l) => /the summary is NOT conformed by this firing/.test(l)));
}

// ==========================================================================
t.section("D10. sub-task 8's verify: read-only, over what the firing claims");
// ==========================================================================
{
  const A = entry({ id: "OD-1", decision: "which timeout?", disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const run = await fire({}, {
    "f1:human-decisions:*": found(A),
    "f1:cleanup": {
      outcome: "rewritten",
      sections: ["# Summary: x", "## Summary", "## Goals", "## Non-goals", "## Open decisions for human to make"],
      relocated: ["a nine-hundred-word errata list — DEFERRED entries in the review log"],
    },
    "f1:verify": { conforms: false, sections: [], defects: ["1 — the deliverable index was dropped — summary.md — restore it"] },
  });
  const p = promptOf(run.calls, "f1:verify");
  t.check("the verify pass reports and does not fix", p.includes("REPORT, DO NOT FIX"));
  t.check("editing nothing, the review log included", p.includes("You edit nothing, the review log included"));
  t.check("check 1 catches a dropped or reordered index", /1\. `## Deliverable index` is present, is LAST, and is unchanged by this firing/.test(p));
  t.check("check 2 catches a dropped identifier", /2\. Every entry under `## Open decisions for human to make` carries the stable identifier/.test(p));
  t.check("check 3 catches a surviving Retired block", /3\. No `### Retired` or equivalent block survives/.test(p));
  t.check("check 4 catches a withdrawal naming no authority", /4\. Every decision that left that section names the authority/.test(p));
  t.check("check 5 catches a restated problem statement", /5\. `\*\*Problem statement\.\*\*` is present and still says what the change repairs/.test(p));
  t.check("check 6 catches a preamble the entries do not support", /6\. A preamble on `## Open decisions for human to make`/.test(p));
  t.check("check 7 catches an unlisted heading and content that left without arriving", /7\. Every listed heading is present, in the listed order, and no heading the list does not name survives/.test(p));
  t.check(
    "it is handed the cleanup's own report, so a pass that dropped the index is checkable",
    p.includes('"a nine-hundred-word errata list — DEFERRED entries in the review log"'),
  );
  t.check("and what the phase says it resolved", p.includes('"decisionsResolved"'));
  t.check("anchored on the baseline commit's diff", p.includes("git diff -- " + P.root));
  t.check("the verdict is returned rather than acted on", run.result.verification && run.result.verification.conforms === false);
  t.check("with the defect logged", run.logs.some((l) => /the deliverable index was dropped/.test(l)));
  t.check("and nothing re-applied because of it", matching(run.calls, "f1:apply:").length === 1, String(matching(run.calls, "f1:apply:").length));

  const dead = await fire({}, { "f1:verify": null });
  t.check("a dead verify leaves the firing UNVERIFIED rather than verified", dead.result.verification === null);
  t.check("and says so", dead.logs.some((l) => /this firing's output is UNVERIFIED/.test(l)));
}

// ==========================================================================
t.section("D11. what leaves the summary, and what a withdrawal must name");
// ==========================================================================
{
  // A resolution the gate passed and the write path landed: the answer is
  // staged, the entry leaves the human's list, and it is returned as closed.
  const A = entry({ id: "OD-1", decision: "which timeout?", disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const run = await fire({}, {
    "f1:human-decisions:*": found(A),
    "f1:apply:0": { outcome: "edited", wrote: "The adapter waits thirty seconds.", where: [P.spec + " — SPEC-1"] },
  });
  const r = run.result;
  const closed = (r.decisionsResolved || []).find((d) => d.id === "id:OD-1");
  t.check("a decision the phase resolves is returned as resolved", closed && closed.kind === "resolved", closed && closed.kind);
  t.check("and is no longer listed for the human", !(r.decisionsLeftToHuman || []).some((d) => d.id === "id:OD-1"), ids(r.decisionsLeftToHuman));
  t.check("naming the authority that settled it", closed && /the open-decisions-and-impact-review phase, firing 1/.test(closed.authority), closed && closed.authority);
  t.check("with its own falsifier named in that authority", closed && /attacked by this item's own falsifier/.test(closed.authority));
  t.check("and its ground cited", closed && (closed.citation || []).length > 0, JSON.stringify(closed && closed.citation));
  const applyPrompt = promptOf(run.calls, "f1:apply:0");
  t.check("its answer is staged into the proposal", applyPrompt.includes("You are staging an ANSWER"));
  t.check("and the entry removed from the human's section", applyPrompt.includes("REMOVE the item's entry from `## Open decisions for human to make`"));
  t.check("with no retired block left behind it", applyPrompt.includes("no `### Retired` or equivalent block replaces it"));
  // A resolved entry leaves both homes, so an item collected from a staged
  // change file's own section is deleted there too, and the section with it.
  t.check(
    "and, where it was found in a staged change file, deleted there as well",
    applyPrompt.includes("delete its entry there as well, and delete that section once it is empty"),
  );

  // A withdrawal the phase's own gate stands behind is recorded as such.
  const W = entry({ id: "OD-2", decision: "does this widen to the CLI?", disposition: "human", summaryAction: "withdrawn" });
  const withdrawn = await fire({}, { "f1:human-decisions:*": found(W) });
  const wd = (withdrawn.result.decisionsResolved || []).find((d) => d.id === "id:OD-2");
  t.check("a withdrawal the gate stands behind is returned as withdrawn", wd && wd.kind === "withdrawn", wd && wd.kind);
  t.check("with its authority", wd && wd.authority.length > 0, wd && wd.authority);
  t.check(
    "and its Apply is told to take the entry out rather than write one back",
    promptOf(withdrawn.calls, "f1:apply:0").includes(
      "REMOVE the entry from `## Open decisions for human to make` rather than ensuring one is there",
    ),
  );

  // A resolution the join downgraded to the human is not a withdrawal this
  // phase took. A reading that resolves an item is told the entry then leaves
  // the summary, so all three readings report the entry as withdrawn while the
  // join left the item listed with their three answers beside it.
  const R = { id: "OD-3", decision: "which backoff does the adapter use?" };
  const rd = (answer) => entry({ ...R, disposition: "resolve", answer, summaryAction: "withdrawn" });
  const divergent = await fire({}, {
    "f1:human-decisions:1": found(rd("a fixed one-second backoff")),
    "f1:human-decisions:2": found(rd("an exponential backoff to one minute")),
    "f1:human-decisions:3": found(rd("no backoff at all")),
  });
  const dr = divergent.result;
  t.check(
    "a resolution the join downgraded is not reported as a withdrawn decision",
    !(dr.decisionsResolved || []).some((d) => d.id === "id:OD-3"),
    ids(dr.decisionsResolved),
  );
  const stillOpen = (dr.decisionsLeftToHuman || []).find((d) => d.id === "id:OD-3");
  t.check("it is left to the human", !!stillOpen, ids(dr.decisionsLeftToHuman));
  t.check(
    "with the readings' three answers recorded as alternatives",
    stillOpen && (stillOpen.alternatives || []).length === 3,
    stillOpen && String((stillOpen.alternatives || []).length),
  );
  t.check("and the reason says where they are", stillOpen && /recorded as alternatives/.test(stillOpen.reason), stillOpen && stillOpen.reason);
  t.check(
    "and its Apply keeps the entry rather than taking it out",
    !promptOf(divergent.calls, "f1:apply:0").includes("THIS ITEM'S ENTRY LEAVES THE SECTION"),
  );

  // One that names nobody is refused. A withdrawal's whole record is the
  // absence of an entry, so one nothing settled is indistinguishable from an
  // entry a fixer dropped.
  const unauthorised = await fire({}, { "f1:human-decisions:*": found(W), "f1:falsify:0": null });
  const ur = unauthorised.result;
  t.check("a withdrawal naming no authority is refused", !(ur.decisionsResolved || []).some((d) => d.id === "id:OD-2"), ids(ur.decisionsResolved));
  t.check(
    "and the decision stays the human's",
    (ur.decisionsLeftToHuman || []).some((d) => d.id === "id:OD-2" && /withdrawal naming no authority is refused/.test(d.reason)),
    ids(ur.decisionsLeftToHuman),
  );
  t.check("with the refusal logged", unauthorised.logs.some((l) => /withdrawal REFUSED, it names no authority/.test(l)));
}

// ==========================================================================
t.section("D12. lockSpecChanges: a resolution needing the spec staging is recorded, not written");
// ==========================================================================
{
  const A = entry({ id: "OD-1", decision: "which timeout?", disposition: "resolve", answer: "thirty seconds", summaryAction: "withdrawn" });
  const locked = await fire({ lockSpecChanges: true }, {
    "f1:human-decisions:*": found(A),
    "f1:apply:0": {
      outcome: "blocked",
      wrote: "",
      where: [],
      note: "the answer belongs in the staged spec edits, which are locked for this run",
    },
  });
  const p = promptOf(locked.calls, "f1:apply:0");
  t.check("the Apply's grant does not open the staged spec edits", !p.includes(P.spec + " — the staged spec edits"), "the spec file was granted");
  t.check("and says they are locked by the operator", p.includes(P.spec + " is LOCKED for this run by the operator"));
  t.check("telling the agent to report blocked with the edit it would have made", p.includes("report `blocked`, state in `note` the edit you would have made"));
  t.check("the collectors are told too", promptOf(locked.calls, "f1:human-decisions:1").includes("THE STAGED SPEC CHANGES ARE LOCKED for this run by the operator"));
  const rec = (locked.result.recordedForOperator || []).find((x) => x.id === "id:OD-1");
  t.check("the resolution is recorded for the operator", rec && rec.status === "recorded", rec && rec.status);
  t.check("carrying the edit it could not make", rec && /staged spec edits, which are locked/.test(rec.reason), rec && rec.reason);
  t.check("it is not counted as applied", !(locked.result.applied || []).some((x) => x.id === "id:OD-1"), ids(locked.result.applied));
  t.check("nor as a failure", !(locked.result.failedItems || []).some((x) => x.id === "id:OD-1"), ids(locked.result.failedItems));
  t.check("and it is logged as recorded", locked.logs.some((l) => /is RECORDED for the operator/.test(l)));
  t.check(
    "nor as a withdrawn decision",
    !(locked.result.decisionsResolved || []).some((x) => x.id === "id:OD-1"),
    ids(locked.result.decisionsResolved),
  );
  t.check(
    "and the decision is still the human's",
    (locked.result.decisionsLeftToHuman || []).some((x) => x.id === "id:OD-1"),
    ids(locked.result.decisionsLeftToHuman),
  );

  // The control: unlocked, the same resolution is granted the file it needs.
  const open = await fire({}, { "f1:human-decisions:*": found(A) });
  t.check(
    "with the lock off the same Apply is granted the staged spec edits",
    promptOf(open.calls, "f1:apply:0").includes(P.spec + " — the staged spec edits"),
  );
  t.check(
    "and the collectors are told nothing about a lock",
    !promptOf(open.calls, "f1:human-decisions:1").includes("THE STAGED SPEC CHANGES ARE LOCKED"),
  );
}

// ==========================================================================
t.section("D13. the parts the deleted lens carried, in the homes this phase gives them");
// ==========================================================================
{
  // The parent's B6e pins that these left the parent. This section pins that
  // they arrived, because text that moves without an assertion following it is
  // coverage lost rather than coverage relocated.
  const { calls } = await fire({}, {});
  const p1 = promptOf(calls, "f1:human-decisions:1");
  const sch = (calls.find((c) => c.opts && c.opts.schema && c.opts.schema.properties && c.opts.schema.properties.decisions) || { opts: {} }).opts.schema;
  t.check("a collector is handed the decisions schema", !!sch);
  const item = (sch && sch.properties.decisions.items) || { required: [], properties: {} };

  // The receipts. The order of work is stated in prose and back-filling a field
  // to justify a conclusion already reached is what the schema still catches,
  // so a required field dropped here is the enforcement half going quiet.
  // `cascades` required is what makes the settled-decision exception considered
  // rather than optional, and `summaryAction` required is what makes the
  // summary reconciliation a reported act rather than a silent one.
  const REQUIRED = [
    "groundQuotes", "questionsAsked", "caseFor", "caseAgainst", "whatWouldFlipIt",
    "counterfactual", "cascades", "disposition", "summaryAction",
  ];
  const absent = REQUIRED.filter((k) => !(item.required || []).includes(k));
  t.check("every receipt field is required of an entry", absent.length === 0, absent.join(","));
  t.check(
    "and the two evidence arrays must be non-empty",
    (item.properties.groundQuotes || {}).minItems === 1 && (item.properties.questionsAsked || {}).minItems === 1,
    JSON.stringify([(item.properties.groundQuotes || {}).minItems, (item.properties.questionsAsked || {}).minItems]),
  );

  // The size budget. The first version of this schema carried its field guidance
  // as descriptions, serialised to roughly 7.7k, and the API refused every call
  // it was attached to; the lens failed twelve times across three rounds and
  // never ran. A schema that cannot be sent enforces nothing. The move grew it
  // along that same axis, since both enums gained values, so the budget is
  // absolute rather than a delta against a sibling schema.
  const bytes = JSON.stringify(sch || {}).length;
  t.check("the decisions schema stays sendable", bytes < 2000, bytes + " bytes (budget 2000)");
  t.check("because the field guidance is in the brief instead", p1.includes("WHAT EACH FIELD OF `decisions` HOLDS"));

  // The order of work, which is the other half of the same enforcement.
  const STEPS = ["1. INVENTORY", "2. ELABORATE", "3. INTERROGATE", "4. DETERMINE"];
  const at = STEPS.map((step) => p1.indexOf(step));
  t.check("the four steps are all stated", at.every((i) => i >= 0), at.join(","));
  t.check("in order", at[0] < at[1] && at[1] < at[2] && at[2] < at[3], at.join(","));
  t.check("with the test applied only after them", p1.indexOf("THE TEST, applied to each decision in this order.") > at[3]);
  t.check("and step 3 asks the question that would kill the answer", /KILL the answer you are drifting\s+toward/.test(p1));
  t.check("and treats an unanswerable one as a result", /a result rather than a gap/.test(p1));

  // The settled-decision scope, and the one exception that keeps a cascade from
  // being reopened as a decision of its own.
  t.check("a settled decision is out of the population", p1.includes("A SETTLED DECISION IS NOT YOURS"));
  t.check("with cascade as the only exception", p1.includes("THE ONE EXCEPTION IS CASCADE"));
  t.check("which never becomes a decision of its own", /never becomes a decision of its own/.test(p1));

  // The status-and-recency rule, in sub-task 4's brief, with the counterfactual
  // gate that decides whether a row is even a question. How the corpus is
  // gathered and handed over is D8's subject and is not repeated here.
  const p4 = promptOf(calls, "f1:other-proposals");
  t.check("an implemented proposal is not affected", p4.includes("An `Implemented` proposal is already in the tree and is not affected"));
  t.check("a draft may be invalidated freely", /`Draft` may be invalidated\s+freely/.test(p4));
  t.check("and a recently reviewed one warrants care", /last reviewed within fourteen\s+days warrants care/.test(p4));
  t.check("a row is a question only where the choice changes it", /whether choosing differently would change\s+that effect/.test(p4));
  t.check("otherwise it is a row", /a row rather than a question for a human/.test(p4));
  t.check("and a commit date is declared as one", /rather than when it was\s+reviewed/.test(p4));
  // base() stubs an empty corpus, so this run takes the fallback branch, which
  // is where the instruction to read the statuses directly lives.
  t.check("with the fallback naming the tool to read a status with", p4.includes("proposal-status.mjs <proposal> --json"));
}

t.done();
