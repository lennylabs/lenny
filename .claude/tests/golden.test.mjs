// Layer 3: golden prompt digests.
//
// The failure this catches is invisible to layer 2 and to review. A refactor
// drops `HARD CONSTRAINT: the only file you may edit is …` from one prompt out
// of forty: no behavioural test asserts that specific prompt, and the diff is
// large enough that a reader does not notice. The digest makes it a one-line
// failure.
//
// The digest is deliberately LOSSY. It records, per agent call, the stage, the
// label, the schema, and which invariant blocks are present -- never the prose
// around them. So it fails on structure and does not break on every wording
// edit, which is what keeps it from being re-recorded reflexively.
//
// Run:    node .claude/tests/golden.test.mjs
// Accept: node .claude/tests/golden.test.mjs --record   (review the diff!)

import { readFileSync, writeFileSync, existsSync, mkdirSync } from "fs";
import { resolve } from "path";
import { runWorkflow, suite, REPO } from "./harness.mjs";

const RECORD = process.argv.includes("--record");
const DIR = resolve(REPO, ".claude/tests/golden");
if (!existsSync(DIR)) mkdirSync(DIR, { recursive: true });

// The blocks whose PRESENCE is the contract. Losing one silently is the class
// this layer exists for.
const BLOCKS = [
  ["file-ownership", /HARD CONSTRAINT|only file you may edit|only files you may edit|You may edit ONLY/],
  ["read-only", /read-only investigator|You are READ-ONLY|You are read-only/],
  ["log-shard", /THE REVIEW LOG carries/],
  ["cache", /CACHE\. Before anything else/],
  ["evidence", /Verify every claim directly against/],
  ["finding-bar", /REPORT ONLY REAL ERRORS/],
  ["loop-scope", /SCOPE OF THIS LOOP/],
  ["deviations", /DEVIATIONS ALREADY ACCEPTED|WHAT AN IMPLEMENTATION ALREADY LEARNED/],
  ["summary", /THE PROPOSAL'S SUMMARY/],
  ["rules", /code-best-practices\.md/],
  ["preflight", /ENVIRONMENT PREFLIGHT/],
  ["memory-safe", /MEMORY-SAFE/],
  ["branch-safety", /BRANCH SAFETY/],
  ["one-command", /Run exactly (this command|these two commands)/],
  ["scoping", /ONLY the tiers this fix warrants/],
  ["caller-prompt", /ADDITIONAL INSTRUCTION FROM THE CALLER/],
];

function digest(calls) {
  return calls.map((c) => ({
    label: c.label,
    phase: (c.opts && c.opts.phase) || "",
    model: (c.opts && c.opts.model) || "",
    schema: (c.opts && c.opts.schema && Object.keys(c.opts.schema.properties || {}).sort().join("+")) || "",
    blocks: BLOCKS.filter(([, re]) => re.test(c.prompt)).map(([n]) => n),
  }));
}

const t = suite("golden");

const CASES = [
  {
    name: "change-proposal-review",
    wf: ".claude/workflows/change-proposal.js",
    args: { mode: "review", proposalPath: "proposals/0081_fix_x", date: "d", exemplar: "e.md", repoRoot: "/repo", maxSpecReviewRounds: 4, maxNonSpecReviewRounds: 4 },
    stubs: {
      bootstrap: "SKIPPED", conventions: "ok", "probe:spec-changes": { stagesSpecChanges: true, why: "SPEC-1" }, "snap*": "DONE",
      "*:review:*": { coverage: "c", findings: [] },
      "*:round-boundary": '{"merged":0,"ledgerLines":1,"compactionDue":false,"changedFiles":[],"hunks":0,"snapshot":"/s","overrides":{}}',
      "spec-nonspec-handoff": "ok", "introspect*": null, growth: { documentWas: 1, documentNow: 1, grew: [] },
      default: {},
    },
  },
  {
    // A round that confirms a finding, so the plan, design, fix and post-fix
    // prompts are in the digest. Without it the whole fix path is unrecorded,
    // and a constraint dropped from the fixer is invisible to this layer.
    name: "change-proposal-fixing",
    wf: ".claude/workflows/change-proposal.js",
    args: { mode: "review", proposalPath: "proposals/0081_fix_x", date: "d", exemplar: "e.md", repoRoot: "/repo", maxSpecReviewRounds: 3, maxNonSpecReviewRounds: 3 },
    stubs: (() => {
      const F = { title: "T1", where: "w", claim: "c", why_wrong: "w", evidence: "e", suggested_fix: "f", area: "a", kind: "citation", introducedBy: "pre-existing" };
      return {
        bootstrap: "SKIPPED", conventions: "ok", "probe:spec-changes": { stagesSpecChanges: false, why: "headings only" }, "snap*": "DONE",
        "*:review:*": ({ label }) => (/^r1:/.test(label) ? { coverage: "c", findings: [F] } : { coverage: "c", findings: [] }),
        "*:dedup": { findings: [{ ...F, lenses: ["citations"] }] },
        "*:verify-material": { confirmed: true, reason: "m" },
        "*:verify-evidence": { confirmed: true, reason: "e" },
        "*:fix-plan": { groups: [{ id: "G1", title: "g", rationale: "r", findings: [0], order: 1 }], notes: "" },
        "*:fix-design:*": { designs: [{ findingTitle: "T1", effort: "trivial", chosen: { approach: "a", why: "w" } }], groupNote: "", newMechanisms: [] },
        "*:fix": { summary: "fixed", newMechanisms: [], escalated: [], designRejected: [] },
        "*:post-fix-review": { findings: [] },
        "*:round-boundary": '{"merged":0,"ledgerLines":1,"compactionDue":false,"changedFiles":[],"hunks":1,"snapshot":"/s","overrides":{}}',
        "introspect*": null, growth: { documentWas: 1, documentNow: 1, grew: [] }, default: {},
      };
    })(),
  },
  {
    name: "implement-proposal-build",
    wf: ".claude/workflows/implement-proposal-build.js",
    args: {
      proposalPath: "proposals/0081_fix_x", repoRoot: "/repo", date: "d",
      plan: { blastRadius: [], steps: [
        { id: "S1", lane: "spec", title: "s", work: "SPEC-1", targets: ["spec/16.md"], tiers: ["static"], checklistStep: "S1", dependsOn: [] },
        { id: "S2", lane: "code", title: "c", work: "w", targets: ["pkg/a"], tiers: ["unit"], checklistStep: "S2", dependsOn: ["S1"] },
      ] },
    },
    stubs: {
      "checklist-ticks": { ticked: [] }, baseline: { sha: "b" }, "build:S2:base": { sha: "b" },
      "spec-targets:*": { files: ["spec/16.md"] }, "lease-open:*": "{}", "lease-release:*": "{}",
      "apply:*": { applied: ["SPEC-1"], unappliable: [], deviations: [] },
      "verify:S1:spec": { discrepancies: [] }, "commit-spec:*": "ok",
      "compile:*": { compiles: true, errors: [] },
      "lease-check:*": { leaseHeld: false },
      "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/a.go"], testsAddedOrModified: [] },
      "review:*": { findings: [] }, "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
      "tick:*": "DONE", "compile-guard:*": { clean: true, compiles: true },
      "proposal-edit-audit": { edited: false, commits: [] }, default: {},
    },
  },
];

for (const c of CASES) {
  const { calls } = await runWorkflow(c.wf, c.args, c.stubs);
  const got = JSON.stringify(digest(calls), null, 2) + "\n";
  const path = resolve(DIR, c.name + ".json");
  if (RECORD || !existsSync(path)) {
    writeFileSync(path, got);
    t.check(c.name + ": recorded (" + calls.length + " calls)", true);
    continue;
  }
  const want = readFileSync(path, "utf8");
  if (got === want) {
    t.check(c.name + ": " + calls.length + " prompt digests unchanged", true);
  } else {
    const g = got.split("\n");
    const w = want.split("\n");
    const at = g.findIndex((l, i) => l !== w[i]);
    t.check(
      c.name + ": prompt digests changed",
      false,
      "first difference at line " + (at + 1) + "\n    was: " + (w[at] || "(end)") + "\n    now: " + (g[at] || "(end)") +
        "\n    If this is intended, re-record with --record and review the diff in the commit.",
    );
  }
}

t.done();
