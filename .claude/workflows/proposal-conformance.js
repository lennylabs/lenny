// proposal-conformance: check what landed in the tree against what a proposal staged.
//
// WHY THIS EXISTS. Proposals 0064, 0065, 0066 and 0067 were implemented across
// many commits, several of them after failed applications and restarts. The
// question this answers is not whether the specification matches the code, which
// it will not while the remediation is in flight, and not whether the prose reads
// well. It is whether the implementation of those four proposals was faithful and
// complete: did each staged deliverable land, land with the content that was
// staged, land in the place it was staged for, and did anything land that no
// proposal staged.
//
// THE UNIT is one staged deliverable, a sub-step under a proposal's "Proposed
// changes" heading. That set is enumerable from the proposals, so coverage is a
// fraction rather than a feeling.
//
// THE METHOD is ground-truth-first differential, as in fidelity-review.js, with
// the proposal replacing the code as ground truth. The checker reads the staged
// sub-step and writes down what it requires BEFORE looking at what is in the
// tree. Reading the tree first anchors the reader on what was done and makes an
// omission invisible, because nothing on the page is wrong; the requirement is
// simply not there. Every miss found so far in this project was of that form.
//
// FIVE BUCKETS: absent, altered, misplaced, unstaged, and stale-record. The
// unstaged bucket catches implementation overreach, which no review that starts
// from the tree will ever surface.
//
// ORDERED CONTAINMENT, NOT EQUALITY. Where a proposal stages literal text, the
// landed text must carry what was staged in order. It need not match line for
// line: a correct application of a later pass rewrites anchors and citations in
// text an earlier proposal staged. A line-for-line gate on this material was
// already tried and was red against every correct execution.
//
// This workflow REPORTS. It applies no fix.
//
//   Workflow({ scriptPath: ".claude/workflows/proposal-conformance.js", args: {
//     repoRoot: "/abs/path",
//     deliverables: [ { proposal, step, where, notes } ... ],
//     crossChecks: [ { name, instruction } ... ]     // optional
//   }})

export const meta = {
  name: "proposal-conformance",
  description:
    "Check each staged deliverable of a proposal against what actually landed in the tree, deriving the requirement before reading the tree",
  phases: [
    { title: "Conformance", detail: "one checker per staged deliverable" },
    { title: "Cross-checks", detail: "consistency between proposals and their amendments" },
    { title: "Synthesize", detail: "pool, verify, rank" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.repoRoot || !Array.isArray(input.deliverables)) {
  throw new Error("args.repoRoot and args.deliverables are required");
}
const repo = input.repoRoot;
const deliverables = input.deliverables;
const crossChecks = input.crossChecks || [];

const SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: ["deliverable", "requirement", "landed", "requirementsChecked", "findings"],
  properties: {
    deliverable: { type: "string" },
    requirement: {
      type: "string",
      description:
        "What the staged sub-step requires, enumerated as a checklist, written BEFORE looking at the tree. Every distinct obligation gets its own line: each file to create or edit, each register to seed, each command to run, each gate to add, each literal text block to land, and every ordering constraint the sub-step states.",
    },
    landed: {
      type: "string",
      description: "What is actually in the tree for this deliverable, and where.",
    },
    requirementsChecked: {
      type: "integer",
      description: "How many distinct obligations from the checklist were verified against the tree.",
    },
    status: {
      type: "string",
      enum: ["conformant", "partial", "not-implemented"],
      description:
        "not-implemented is a legitimate outcome for a deliverable later in the sequence, and is not a defect. Say so plainly rather than reporting its obligations as findings.",
    },
    findings: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["bucket", "obligation", "quote", "actual", "evidence", "why"],
        properties: {
          bucket: {
            type: "string",
            enum: ["absent", "altered", "misplaced", "unstaged", "stale-record"],
            description:
              "absent: the sub-step requires it and the tree does not carry it. altered: it landed with content differing in substance from what was staged. misplaced: it landed in a different file, section or position than staged. unstaged: the tree carries content this sub-step's application introduced that no proposal staged. stale-record: the proposal's own record of what it did, such as its files-touched list or a register it seeds, disagrees with the tree.",
          },
          obligation: {
            type: "string",
            description: "The obligation from the checklist this finding is about.",
          },
          quote: {
            type: "string",
            description:
              "The staged text, quoted verbatim from the proposal, that states the obligation. For an unstaged finding, quote the landed text instead and say which proposal was searched for it.",
          },
          actual: { type: "string", description: "What the tree carries instead." },
          evidence: {
            type: "array",
            items: { type: "string" },
            description:
              "file:line in the proposal for the staged side and in the tree for the landed side. Both sides get a citation.",
          },
          severity: { type: "string", enum: ["high", "medium", "low"] },
          why: {
            type: "string",
            description:
              "Why this is a conformance defect rather than a permitted deviation. A deviation the applier recorded deliberately, in a commit message or in the proposal's own deviations record, is not a finding; say so and leave it out.",
          },
        },
      },
    },
    notes: { type: "string" },
  },
};

const METHOD = `
You are checking whether one staged deliverable of an approved proposal was
implemented faithfully. The repository is ${repo}. Work there.

WHAT IS AND IS NOT THE SUBJECT. The subject is conformance between the proposal
and the tree. The code that implements the platform is mid-remediation and is NOT
a reference point: a divergence between the specification and the platform code is
expected right now and is not a finding here. Do not report one. Equally, prose
quality is not the subject. The subject is whether what the proposal staged is
what landed.

WORK IN THIS ORDER. Steps 1 and 2 complete before you look at the tree.

STEP 1 — READ THE STAGED SUB-STEP IN FULL.
  Read it end to end, including any table, register seed, literal text block, or
  command line it carries. Read the surrounding design sections it refers to when
  it depends on them.

STEP 2 — WRITE THE REQUIREMENT AS A CHECKLIST.
  Enumerate every distinct obligation: each file to create or edit, each register
  to seed and with what, each command to run, each gate to add, each literal text
  block to land, each heading and index row, and every ordering constraint stated.
  This checklist is your denominator. Do not look at the tree while writing it.

STEP 3 — NOW VERIFY EACH OBLIGATION AGAINST THE TREE, ONE AT A TIME.
  For each, find what landed and compare. Reading the tree only now is the point:
  a reviewer who starts from the tree sees text that is coherent and reads it as
  correct, so an obligation that simply is not there stays invisible. Every miss
  found in this project so far was of that form.

STEP 4 — LOOK FOR WHAT LANDED THAT NOTHING STAGED.
  Search the files this sub-step touched for content its application introduced
  that no proposal stages. Implementation overreach is invisible to any review
  that starts from the tree, because the extra content is usually plausible.

ORDERED CONTAINMENT, NOT EQUALITY. Where the proposal stages literal text, the
landed text must carry what was staged, in order and in substance. It need NOT
match line for line. A later pass legitimately rewrites anchors, citations and
cross-references inside text an earlier proposal staged, and a heading may have
been renumbered. Judge substance. A line-for-line comparison on this material has
already been tried and was red against every correct execution of it.

NOT-IMPLEMENTED IS A LEGITIMATE ANSWER. Several deliverables sit later in a
sequence that is still in flight. When a sub-step has not been applied at all, set
status to not-implemented, say so plainly, and do not enumerate its obligations as
findings. Report a partial application as partial, and there the unmet obligations
ARE findings, because a half-applied step is how a miss hides.

A RECORDED DEVIATION IS NOT A DEFECT. The appliers recorded deviations
deliberately, in commit messages and in the proposals' own records. When you find
a difference, check whether it was recorded as a deliberate deviation before
reporting it, and leave it out when it was. Say in your notes which you excluded
that way.

CITE BOTH SIDES. Every finding carries a proposal-side citation for the staged
obligation and a tree-side citation for what landed. Quote the staged text
verbatim.

Report honestly. A deliverable that landed correctly is a valuable result: return
zero findings with your requirement checklist and requirementsChecked filled in.
Do not manufacture a finding to look thorough.
`;

phase("Conformance");

const results = await parallel(
  deliverables.map((d) => () =>
    agent(
      `${METHOD}

THE DELIVERABLE YOU OWN: ${d.proposal} ${d.step}
WHERE IT IS STAGED: ${d.where}
${d.notes ? "NOTES: " + d.notes : ""}`,
      {
        label: `conform:${d.proposal}-${d.step}`,
        phase: "Conformance",
        schema: SCHEMA,
      },
    ),
  ),
);

const ok = results.filter(Boolean);
const all = ok.flatMap((r) =>
  (r.findings || []).map((f) => ({ ...f, deliverable: r.deliverable, status: r.status })),
);
const checked = ok.reduce((n, r) => n + (r.requirementsChecked || 0), 0);

log(
  `${ok.length}/${deliverables.length} deliverables checked, ${checked} obligations verified, ${all.length} findings`,
);

let crossFindings = [];
if (crossChecks.length) {
  phase("Cross-checks");
  const cross = await parallel(
    crossChecks.map((c) => () =>
      agent(
        `${METHOD}

THIS IS A CROSS-PROPOSAL CHECK rather than a single deliverable. Use the same
ground-truth-first order: derive what the proposals require of each other before
reading the tree.

THE CHECK: ${c.name}
${c.instruction}`,
        { label: `cross:${c.name}`, phase: "Cross-checks", schema: SCHEMA },
      ),
    ),
  );
  crossFindings = cross
    .filter(Boolean)
    .flatMap((r) => (r.findings || []).map((f) => ({ ...f, deliverable: r.deliverable })));
  log(`${crossFindings.length} cross-proposal findings`);
}

const pooled = all.concat(crossFindings);
if (pooled.length === 0) {
  return { deliverables: ok.length, obligationsChecked: checked, findings: [], detail: ok };
}

phase("Synthesize");

const synth = await agent(
  `Below are conformance findings from independent checkers, each of which owned one
staged deliverable of proposals 0064, 0065, 0066 or 0067 in ${repo} and checked it
against the tree.

Pool them for a human reader. Do not re-review the proposals and do not add
findings of your own.

1. Drop exact duplicates and merge findings that are one defect reached from two
   deliverables, keeping the strongest evidence from each.
2. Spot-check every high-severity finding by opening both citations, the staged
   side in the proposal and the landed side in the tree. A checker that misread a
   staged obligation produces a confident wrong finding, which costs more here
   than a missed one. Drop what does not hold and say which you dropped and why.
3. Discard any finding that is really a specification-versus-platform-code
   divergence rather than a proposal-versus-tree one. That is not the subject of
   this review and the platform code is mid-remediation.
4. Rank by severity, then by blast radius: a missing obligation that a later
   deliverable depends on outranks an isolated one.

Return a plain report. Lead with the counts by bucket that survived. Then the
findings, ranked, each naming its deliverable, the staged obligation quoted, what
the tree carries, and both citations. Then a short section listing the
deliverables that came back conformant and those that are legitimately not yet
implemented, so the reader sees the coverage rather than only the defects.

FINDINGS:
${JSON.stringify(pooled, null, 1)}`,
  { label: "synthesize", phase: "Synthesize" },
);

return {
  deliverables: ok.length,
  obligationsChecked: checked,
  rawFindingCount: pooled.length,
  report: synth,
  findings: pooled,
  detail: ok.map((r) => ({ deliverable: r.deliverable, status: r.status, checked: r.requirementsChecked })),
};
