// fidelity-review: verify that a spec section correctly captures the mechanism
// the code implements and the source analysis established.
//
// WHY THIS EXISTS. Reviewing §28 and §29 by handing an agent the file and asking
// for defects has a known failure: the files are ~1,300 and ~1,600 lines, so each
// pass samples a different subset and the round count never converges. Sixteen
// rounds fixed thirty-seven real defects and did not run dry. Worse, the whole
// class of defect the project actually cares about is invisible to a text review:
// a sentence that cites a real section, reads plausibly, and describes a
// mechanism the code does not implement passes every citation check there is.
//
// THE UNIT is one mechanism: an RPC, a REST route, a register, or a channel.
// That set is enumerable from the protos and the register, so coverage is a
// fraction rather than a feeling.
//
// THE METHOD is ground-truth-first differential. Each checker derives what the
// mechanism does from the implementation and from the source analysis, writes
// that down, derives what the spec text SHOULD say from those two alone, and
// only then reads what it does say. Reading the spec text last is the point: a
// reviewer who reads it first anchors on it and goes looking for confirmation,
// which is how a plausible-but-wrong sentence survives round after round.
// The schema enforces the order by making groundTruth a required field that is
// answered before findings.
//
// FOUR BUCKETS per statement: agrees, misstates, omits, invents. The last two
// are the ones a citation audit structurally cannot produce, and they are where
// the defects that survived the earlier rounds live.
//
// This workflow REPORTS. It applies no fix. Fixes are serialized in a separate
// pass because concurrent edits to one file collide and because a fixer that
// invents claims is a live failure mode on this material.
//
//   Workflow({ scriptPath: ".claude/workflows/fidelity-review.js", args: {
//     repoRoot: "/abs/path",
//     specFiles: ["spec/28_...md", "spec/29_...md"],
//     sourceDocs: ["gateway-runtime-comms.md", ...],
//     mechanisms: [ { name, kind, where } ... ]
//   }})

export const meta = {
  name: "fidelity-review",
  description:
    "Verify a spec section against the code that implements it and the analysis it was written from, one mechanism at a time, deriving ground truth before reading the spec text",
  phases: [
    { title: "Verify", detail: "one checker per mechanism: derive from code and source, then diff against the spec text" },
    { title: "Synthesize", detail: "pool the findings, drop the duplicates, rank" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.repoRoot || !Array.isArray(input.mechanisms)) {
  throw new Error("args.repoRoot and args.mechanisms are required");
}
const repo = input.repoRoot;
const specFiles = input.specFiles || [
  "spec/28_communication-channels.md",
  "spec/29_communication-scenarios.md",
];
const sourceDocs = input.sourceDocs || ["gateway-runtime-comms.md"];
const mechanisms = input.mechanisms;

const FINDING_SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: ["mechanism", "groundTruth", "statementsChecked", "findings"],
  properties: {
    mechanism: { type: "string" },
    groundTruth: {
      type: "object",
      additionalProperties: false,
      required: ["fromCode", "fromSource", "codeEvidence"],
      properties: {
        fromCode: {
          type: "string",
          description:
            "What the mechanism actually does, derived from the proto and both sides of the implementation BEFORE reading the spec text. Who initiates, what fields cross the boundary, what is enforced, what the failure modes are, and what is not enforced.",
        },
        fromSource: {
          type: "string",
          description:
            "What the source analysis established about this mechanism, including any defect it recorded. Empty when the source does not cover it.",
        },
        codeEvidence: {
          type: "array",
          items: { type: "string" },
          description: "file:line for every claim in fromCode. A claim with no evidence line is not ground truth.",
        },
      },
    },
    statementsChecked: {
      type: "integer",
      description: "How many distinct spec statements about this mechanism were examined. Coverage denominator.",
    },
    findings: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["bucket", "specFile", "quote", "actual", "evidence", "kind", "why"],
        properties: {
          bucket: {
            type: "string",
            enum: ["misstates", "omits", "invents"],
            description:
              "misstates: the text describes the mechanism differently from the code or the source. omits: the code or source carries a fact the text drops without stating it or marking it unstated. invents: the text asserts a mechanism neither the code nor the source has.",
          },
          specFile: { type: "string" },
          quote: {
            type: "string",
            description:
              "The offending text quoted VERBATIM, long enough to locate uniquely. Never a line number: insertions and renumbering invalidate those, and this material has already been renumbered.",
          },
          actual: {
            type: "string",
            description: "What the code or the source actually does. For an omits finding, the dropped fact.",
          },
          evidence: {
            type: "array",
            items: { type: "string" },
            description: "file:line in code or source supporting `actual`.",
          },
          kind: {
            type: "string",
            enum: ["fix", "flag"],
            description:
              "fix: a defect in the spec text under review. flag: the spec and the code disagree in a way that predates this text, which the spec section governs and a human settles.",
          },
          severity: { type: "string", enum: ["high", "medium", "low"] },
          why: { type: "string" },
        },
      },
    },
    notes: { type: "string" },
  },
};

const METHOD = `
You are verifying that a specification section correctly captures a mechanism this
repository implements. The repository root is ${repo}. Work there.

THE MECHANISM YOU OWN is named below. Confine yourself to it. Another checker owns
every other mechanism, so a defect outside yours is not yours to report.

WORK IN THIS ORDER, AND DO NOT DEPART FROM IT. The order is the method, not
bookkeeping. Steps 1 and 2 complete before you open any specification file.

STEP 1 — DERIVE GROUND TRUTH FROM THE CODE.
  Read the implementation. For an RPC that means the proto message definitions,
  the server handler, and the code that calls it. For a REST route it means the
  registered handler and what it does. For a register it means every read and
  write of the underlying key or column. Establish, with a file:line for each:
  who initiates; what fields cross the boundary and what they mean; the ordering
  and concurrency constraints the code actually enforces; the failure modes and
  what each produces; and what the code notably does NOT enforce or handle.
  Do not open ${specFiles.join(" or ")} during this step.

STEP 2 — DERIVE INTENT FROM THE SOURCE ANALYSIS.
  Read what ${sourceDocs.join(" and ")} established about this mechanism,
  including any defect or gap it recorded. The source is roughly 297 commits
  stale, so it is a record of intent rather than an authority on current
  behaviour. Where it disagrees with the code, note both.

STEP 3 — DERIVE WHAT THE SPECIFICATION SHOULD SAY.
  From steps 1 and 2 alone, before reading the specification text, write down what
  a correct specification of this mechanism would state.

STEP 4 — NOW READ THE SPECIFICATION TEXT AND DIFF.
  Find every statement about your mechanism in ${specFiles.join(" and ")}. Search
  by identifier, by RPC name, by message name, and by paraphrase, because a
  statement that names the mechanism only by a noun phrase still counts. Compare
  each against step 3 and sort it into one of four outcomes: it agrees; it
  MISSTATES the mechanism; it OMITS something the code or source carries; or it
  INVENTS a mechanism neither has. Report the last three. Count everything you
  examined in statementsChecked, including the ones that agree.

WHY THE ORDER MATTERS. A reviewer who reads the specification text first anchors
on it and reads the code looking for confirmation, which is how a plausible but
wrong sentence survives many review rounds. Deriving independently and diffing
makes the discrepancy fall out instead of depending on you noticing it.

WHAT COUNTS AS A FINDING.
  A statement that cites a real section and still describes the mechanism wrongly
  is a finding; citation correctness is not the subject of this review.
  A fact the code enforces that the specification drops silently is an OMITS
  finding. A fact the specification marks unstated, or that the specification
  deliberately does not carry because the section governs and the code diverges,
  is not.
  A mechanism the specification asserts that you cannot find in the code or the
  source is an INVENTS finding, and it is the highest-severity class here.
  A claim of silence is itself a claim. When the specification says it does not
  state something and the code plainly does it, that is a finding.

FIX VERSUS FLAG. The specification governs where it and the code disagree, so a
divergence that predates this text is a flag for a human, never a silent
correction. A defect in the text under review is a fix. When unsure, flag.

QUOTE, DO NOT CITE LINES. Anchor every finding with verbatim quoted text. This
material has been renumbered and re-wrapped, so line numbers go stale mid-review.

Report honestly. A mechanism whose specification text is correct is a valuable
result: return zero findings with your groundTruth and statementsChecked filled
in. Do not manufacture a finding to look thorough.
`;

phase("Verify");

const results = await parallel(
  mechanisms.map((m) => () =>
    agent(
      `${METHOD}

THE MECHANISM YOU OWN: ${m.name}
KIND: ${m.kind}
WHERE TO START LOOKING: ${m.where}
${m.notes ? "NOTES: " + m.notes : ""}`,
      {
        label: `verify:${m.name}`,
        phase: "Verify",
        schema: FINDING_SCHEMA,
      },
    ),
  ),
);

const ok = results.filter(Boolean);
const all = ok.flatMap((r) => (r.findings || []).map((f) => ({ ...f, mechanism: r.mechanism })));
const checked = ok.reduce((n, r) => n + (r.statementsChecked || 0), 0);

log(
  `${ok.length}/${mechanisms.length} mechanisms verified, ${checked} spec statements examined, ${all.length} findings`,
);

if (all.length === 0) {
  return { mechanisms: ok.length, statementsChecked: checked, findings: [], groundTruth: ok };
}

phase("Synthesize");

const synth = await agent(
  `Below are findings from independent checkers, each of which verified one mechanism
against the code that implements it and the analysis it was written from, in
${repo}.

Your job is to pool them for a human reader. Do not re-review the specification and
do not add findings of your own.

1. Drop exact duplicates, and merge findings that are the same defect reached from
   two mechanisms. Merging keeps the strongest evidence from each.
2. Spot-check the highest-severity INVENTS and MISSTATES findings by opening the
   evidence file:line. A checker that misread the code produces a confident wrong
   finding, which costs more than a missed one here. Drop what does not hold and
   say which you dropped.
3. Rank by severity, then by blast radius: a defect in a normative rule other
   sections rest on outranks a defect in one trace step.
4. Separate the fix findings from the flag findings. The flags go to a human
   because the specification governs where it and the code disagree.

Return a plain report. Lead with the count of findings that survived your check,
by bucket. Then the fix findings, ranked, each with its mechanism, its verbatim
quote, what the code actually does, and its evidence. Then the flags. Close with
the mechanisms that came back clean, named, so the reader knows what was covered
rather than only what was broken.

FINDINGS:
${JSON.stringify(all, null, 1)}`,
  { label: "synthesize", phase: "Synthesize" },
);

return {
  mechanisms: ok.length,
  statementsChecked: checked,
  rawFindingCount: all.length,
  report: synth,
  findings: all,
};
