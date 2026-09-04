// The open-decisions-and-impact-review phase is a subworkflow, and the harness
// records a `workflow()` sub-call as one entry whose prompt is the JSON of the
// argument object, returning from a stub table without ever running the child's
// body. That is what makes the seam invisible to both behavioural suites: the
// parent's tests stub the child's return, and the child's own tests assert
// against the child's idea of its arguments. Nothing between them checks that
// the object the parent actually sends is the object the child reads.
//
// This file is that check. Every value is distinct, so a forwarding line that
// names the wrong key is caught rather than passing on a coincidentally equal
// value, and the key set is compared against the child's own argument registry
// rather than against a list restated here.
//
// It follows .claude/tests/implement-proposal-forwarding.test.mjs, which exists
// for the same defect one seam over.

import { loadWorkflow, runWorkflow, suite } from "./harness.mjs";

const WF = ".claude/workflows/change-proposal.js";
const CHILD = ".claude/workflows/change-proposal-decisions.js";
const CHILD_LABEL = "workflow:/repo/" + CHILD;

// One distinct value per argument the parent controls. `lockSpecChanges` is
// carried at true and the tier at a pair no default produces, so a parent that
// dropped either would show it here rather than in a run's cost.
const ARGS = {
  mode: "review",
  proposalPath: "proposals/0081_fix_x",
  date: "2026-08-31",
  exemplar: "e.md",
  repoRoot: "/repo",
  runTag: "s24-forwarding",
  maxSpecReviewRounds: 4,
  maxNonSpecReviewRounds: 4,
  lockSpecChanges: true,
  baseModel: "sonnet",
  baseEffort: "high",
  maxPeriodicFirings: 7,
};

// The refuted premise the run-wide list carries. `rejected` is the one argument
// the parent builds rather than passes through, so it is given a value a
// hard-coded empty array could not fake: a finding both skeptics refuse.
const REFUTED = "the premise the skeptics refused";

const F = {
  title: REFUTED, where: "w", claim: "c", why_wrong: "w", evidence: "e",
  suggested_fix: "f", area: "a", kind: "citation", introducedBy: "pre-existing",
};

const STUBS = {
  bootstrap: "SKIPPED",
  conventions: "conforms",
  "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" },
  "snap*": "DONE",
  diffcount: "0",
  "spec-nonspec-handoff": "reconciled",
  // A steady lane digest, so no recheck runs and the two post-loop firings are
  // the whole set this file reads.
  "hash:*": "0123456789ab",
  "*:round-boundary":
    '{"merged":0,"ledgerLines":10,"ledgerGrowth":0,"compactionDue":false,"changedFiles":[],"hunksKnown":true,"hunks":3,"snapshot":"/repo/snap","overrides":{}}',
  "*:review:*": ({ label }) =>
    (/^r1:/.test(label) ? { coverage: "c", findings: [F] } : { coverage: "c", findings: [] }),
  "*:dedup": { findings: [{ ...F, lenses: ["citations"] }] },
  "*:verify-material": { confirmed: false, reason: "it changes nothing a reader acts on" },
  "*:verify-evidence": { confirmed: true, reason: "evidence holds" },
  "*:expand:*": { proposal: [], tree: [], searched: "grepped the tree" },
  "*:fix-plan": { groups: [], notes: "" },
  "*:fix-design:*": { designs: [] },
  "*:fix:*": { summary: "fixed", newMechanisms: [], escalated: [], designRejected: [] },
  "*:post-fix-review": { findings: [] },
  "verify-checklist": "ok",
  "status:set-reviewed": "DONE",
  "status:record-run": "ok",
  "introspect*": null,
  default: {},
};

// The cross-firing state channel E7 settles: the parent holds the object the
// child returns and hands it back at the next firing.
const CARRIED = { itemRecords: { "id:OD-1": { disposition: "resolve" } }, firings: 1 };
const OPTS = {
  subworkflows: { "change-proposal-decisions.js": { status: "done", phaseState: CARRIED } },
};

const t = suite("change-proposal decisions forwarding");

const { calls, error } = await runWorkflow(WF, ARGS, STUBS, OPTS);
const handoff = calls.filter((c) => c.label === CHILD_LABEL);

t.check("the run reaches the decisions subworkflow", !error, error && error.message);
t.check("both post-loop firings invoke it", handoff.length === 2, String(handoff.length));
t.check(
  "and by script path rather than by name",
  handoff.every((c) => c.label === "workflow:/repo/.claude/workflows/change-proposal-decisions.js"),
  handoff.map((c) => c.label).join(","),
);

const first = JSON.parse(handoff.length ? handoff[0].prompt : "{}");
const second = JSON.parse(handoff.length > 1 ? handoff[1].prompt : "{}");

// What the first firing must carry, key by key. Every value is distinct, so a
// line that reads `runTag: date` or `baseEffort: baseModel` fails here.
const EXPECTED = {
  proposalPath: "/repo/proposals/0081_fix_x",
  repoRoot: "/repo",
  date: "2026-08-31",
  runTag: "s24-forwarding",
  firing: 1,
  trigger: "post-spec-loop",
  rejected: [{ title: REFUTED, refutedBy: "material", reason: "it changes nothing a reader acts on" }],
  phaseState: {},
  lockSpecChanges: true,
  baseModel: "sonnet",
  baseEffort: "high",
  maxPeriodicFirings: 7,
};

for (const [key, value] of Object.entries(EXPECTED)) {
  t.check(
    "forwards " + key,
    JSON.stringify(first[key]) === JSON.stringify(value),
    "child received " + JSON.stringify(first[key]),
  );
}

// The test's own discipline, checked rather than trusted: two keys sharing a
// value would make a mis-named forwarding line pass every assertion above.
{
  const seen = new Map();
  const shared = [];
  for (const [k, v] of Object.entries(EXPECTED)) {
    const s = JSON.stringify(v);
    if (seen.has(s)) shared.push(seen.get(s) + "/" + k);
    seen.set(s, k);
  }
  t.check("no two arguments carry the same value", shared.length === 0, shared.join(", "));
}

// The identity of the firing, which is the one part of the object that must
// differ between two calls of the same run.
t.check("the second firing is numbered", second.firing === 2, String(second.firing));
t.check("and names its own trigger", second.trigger === "post-non-spec-loop", String(second.trigger));
t.check(
  "the state the child returned is handed back at the next firing",
  JSON.stringify(second.phaseState) === JSON.stringify(CARRIED),
  JSON.stringify(second.phaseState),
);
t.check(
  "and the refuted list has grown by the time the last firing reads it",
  (second.rejected || []).length > (first.rejected || []).length,
  JSON.stringify((second.rejected || []).map((r) => r.title)),
);

// The seam itself: the key set the parent sends against the argument registry
// the child declares. A key the parent stops sending, or one the child adds
// and nothing forwards, is a silent default rather than an error at either end.
{
  const src = loadWorkflow(CHILD);
  const registry = src.slice(src.indexOf("const ARG_CLASS"));
  const body = registry.slice(registry.indexOf("{"), registry.indexOf("};") + 1);
  const declared = [...body.matchAll(/^\s*([A-Za-z_$][\w$]*)\s*:/gm)].map((m) => m[1]);
  const sent = Object.keys(first);
  t.check(
    "every argument the child declares is forwarded",
    declared.every((k) => sent.includes(k)),
    declared.filter((k) => !sent.includes(k)).join(", "),
  );
  t.check(
    "and the parent sends nothing the child does not declare",
    sent.every((k) => declared.includes(k)),
    sent.filter((k) => !declared.includes(k)).join(", "),
  );
  t.check(
    "the assertions above cover the whole registry",
    declared.every((k) => Object.prototype.hasOwnProperty.call(EXPECTED, k)),
    declared.filter((k) => !Object.prototype.hasOwnProperty.call(EXPECTED, k)).join(", "),
  );
  // Every declared argument is one the child actually reads, which is the check
  // that caught a classification left behind by a removed branch one seam over.
  const orphans = declared.filter((k) => !new RegExp("\\binput\\." + k + "\\b").test(src));
  t.check("every declared argument is read by the child", orphans.length === 0, orphans.join(", "));
}

// The trigger is the other half of the seam. The parent names the site it fired
// from and the child validates that name against its own list, throwing before
// it spawns anything if the name is unknown. `pre-spec-loop` was added to the
// parent and not to the child: every firing threw, the parent caught it,
// recorded a failed firing, and ran on as though the phase had found nothing.
// Neither behavioural suite could see it, because the parent's stubs the child's
// return and the child's own tests only ever pass triggers the child knows.
t.section("F2. every trigger the parent can fire is one the child accepts");
{
  const parent = loadWorkflow(WF);
  const child = loadWorkflow(CHILD);

  const sent = [...parent.matchAll(/fireDecisionsPhase\("([a-z-]+)"\)/g)].map((m) => m[1]);
  t.check("the parent fires from more than one site", sent.length > 1, sent.join(", "));

  const listed = child.slice(child.indexOf("const TRIGGERS = ["));
  const accepted = [...listed.slice(0, listed.indexOf("]")).matchAll(/"([a-z-]+)"/g)].map((m) => m[1]);
  t.check("the child declares its accepted triggers", accepted.length > 0, accepted.join(", "));

  const unknown = [...new Set(sent)].filter((tr) => !accepted.includes(tr));
  t.check(
    "every trigger the parent sends is one the child accepts",
    unknown.length === 0,
    unknown.join(", ") || "none",
  );

  // The reverse is a weaker signal but still worth naming: a trigger the child
  // accepts and no site sends is a name that will never appear in a log.
  const unused = accepted.filter((tr) => tr !== "unspecified" && !sent.includes(tr));
  t.check("and the child accepts no trigger no site sends", unused.length === 0, unused.join(", ") || "none");
}

t.done();
