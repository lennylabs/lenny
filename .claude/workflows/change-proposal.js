export const meta = {
  name: "change-proposal",
  description:
    "Validate a problem, draft and write a change proposal — spec edits and/or core-product or test-infra code changes (new mode) — then adversarially review and fix it until a full sweep of every lens is clean",
  whenToUse:
    "Write a change proposal (spec and/or implementation: core product or test infra) from a problem statement, or converge an existing proposals/*.md before sign-off",
};

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || typeof input !== "object") {
  throw new Error(
    "args must be a JSON object or a JSON-encoded object string, received " +
      typeof args,
  );
}
for (const k of ["mode", "date", "exemplar", "repoRoot"]) {
  if (!input[k]) throw new Error("args." + k + " is required and missing");
}
const mode = input.mode;
if (mode !== "new" && mode !== "review" && mode !== "redesign") {
  throw new Error('args.mode must be "new", "review", or "redesign"');
}
// redesign is review that opens with a caller-named redesign pass, so it takes the
// same inputs and follows the same review loop once the redesign has been applied.
if (mode === "redesign" && !(Array.isArray(input.focusAreas) && input.focusAreas.length)) {
  throw new Error("args.focusAreas must name at least one area in redesign mode");
}
if (Array.isArray(input.focusAreas)) {
  for (const a of input.focusAreas) {
    const ok =
      (typeof a === "string" && a.trim()) ||
      (a && typeof a === "object" && typeof a.area === "string" && a.area.trim());
    if (!ok)
      throw new Error(
        'each args.focusAreas entry must be a slug or { area, reason }',
      );
  }
}
if (mode === "new") {
  for (const k of ["problem", "nextNumber"]) {
    if (!input[k])
      throw new Error("args." + k + " is required in new mode and missing");
  }
} else if (!input.proposalPath) {
  throw new Error(
    "args.proposalPath is required in " + mode + " mode and missing",
  );
}

const repo = input.repoRoot;
const date = input.date;
const exemplar = input.exemplar;
const context = input.context || "none provided";
// Each review loop gets its own budget. The spec loop converges a smaller
// surface and gets less; the non-spec loop inherits the old whole-document
// default because it reviews the larger half.
// Fifteen each. Sweeps spend rounds from the same budget, so a loop that is
// draining steadily can otherwise exhaust it mid-cycle, one revive short of a
// clean sweep, and be reported as non-converged when it was converging. The
// legacy maxReviewRounds still overrides the non-spec budget for a caller that
// sets only that.
const maxSpecReviewRounds = input.maxSpecReviewRounds || 15;
const maxNonSpecReviewRounds =
  input.maxNonSpecReviewRounds || input.maxReviewRounds || 15;
// When set, the non-spec loop may never edit the staged spec edits: a finding
// whose only remedy is a spec edit is closed by recording an open decision.
// Off by default, because a non-spec finding that genuinely needs a small spec
// correction is better fixed than escalated.
const lockSpecChanges = !!input.lockSpecChanges;
// The only cap on the fix split. More groups is more focus and more agents;
// fewer is cheaper and regresses more. Group SIZE is uncapped by design.
let maxFixGroups = input.maxFixGroups || 7;
// Namespaces the log shards, the snapshots, and the run state, so two runs
// against one proposal do not read each other's.
const runTag =
  (typeof input.runTag === "string" && input.runTag.trim()) ||
  String(input.proposalPath || input.nextNumber || "cp")
    .replace(/^.*\//, "")
    .replace(/\.md$/, "")
    .replace(/[^A-Za-z0-9._-]/g, "-");
// The review log's ledger is compacted when it passes either threshold. The
// figures are carriage cost rather than taste: every lens prompt already
// carries several thousand tokens of standing text, and a 400-line ledger adds
// roughly that again to each of a dozen lenses every round.
// The ledger's bound is a BACKSTOP against unbounded growth, not the trigger.
// The trigger is the standing context, because that is the only section any
// agent other than the compactor reads: every prompt says to read
// `## Standing context` and nothing else. Triggering on ledger size fired an
// expensive pass to protect against a cost that does not exist.
let compactAtLines = input.compactAtLines || 2000;
let compactGrowthLines = input.compactGrowthLines || 400;
// The compaction target and the trigger are separate numbers. They were one,
// and a run that could not reach it paid for a pass every round for the rest
// of its life. Both self-adjust upward when a pass cannot reach the target,
// so these are a starting point rather than a ceiling.
let standingContextTarget = input.standingContextTarget || 200;
let standingContextTrigger = input.standingContextTrigger || 320;
// Site expansion: one agent per confirmed finding, so the cap bounds a round
// whose review filed many findings at once rather than the run as a whole.
let maxExpansions = input.maxExpansions || 12;
let skipExpansion = !!input.skipExpansion;
// "auto" lets the design stage triage each finding by effort. "shallow" forces
// every finding to the trivial path, for a round of pure bookkeeping findings;
// "deep" forces the architect path, for a run already known to be design-bound.
let fixDesignDepth = input.fixDesignDepth || "auto";
// Which skeptic runs first, and whether the second is skipped when the first
// refuses. Materiality first by default: see the verification block below.
const verifyOrder =
  Array.isArray(input.verifyOrder) && input.verifyOrder.length === 2
    ? input.verifyOrder
    : ["material", "evidence"];
const verifySequential = input.verifySequential !== false;
for (const v of verifyOrder) {
  if (v !== "material" && v !== "evidence") {
    throw new Error('args.verifyOrder entries must be "material" or "evidence"; got ' + v);
  }
}
if (verifyOrder[0] === verifyOrder[1]) {
  throw new Error("args.verifyOrder must name both skeptics, not one twice");
}

// Optional caller controls over the review loop. All three are optional and the
// loop behaves exactly as before when they are absent.
//
// lensPrompt   appended verbatim to every review lens's prompt. Use it to carry
//              standing context the lenses would otherwise rediscover, or to put a
//              specific surface in front of every lens for one run. It reaches the
//              lenses only, not the dedup, verifier, fixer, or post-fix agents,
//              because those have narrow mandates that caller text should not
//              reshape: a verifier told what to conclude is not a verifier.
// startLenses  restricts the FIRST round to these lens keys. Every other lens is
//              untouched rather than excluded, so it joins from round two. Use it
//              to lead with the lenses most likely to find the structural defects,
//              so the first fix lands before the rest of the pool reads the text.
// excludeLenses removes lens keys from the pool entirely, including from sweeps.
//              Use it when a lens's domain is genuinely out of scope for a
//              proposal; note that convergence then certifies nothing about that
//              domain, so the exclusion is recorded in the returned result.
// planPath is the optional path to a remediation or implementation plan the
// proposal implements one or more steps of. When present it enables the
// plan-conformance lens, which is the only lens that reads anything outside the
// repository's current state. When absent that lens is removed from the pool
// entirely, because a conformance lens with nothing to conform to would either
// invent a standard or certify vacuously.
const planPath = (() => {
  if (typeof input.planPath !== "string" || !input.planPath.trim()) return "";
  const p = input.planPath.trim();
  return p.startsWith("/") ? p : repo + "/" + p;
})();

const lensPrompt =
  typeof input.lensPrompt === "string" && input.lensPrompt.trim()
    ? input.lensPrompt.trim()
    : "";
const startLensKeys =
  Array.isArray(input.startLenses) && input.startLenses.length > 0
    ? input.startLenses
    : null;
const excludeLensKeys = Array.isArray(input.excludeLenses)
  ? input.excludeLenses
  : [];

// ---- Where a proposal's parts live ---------------------------------------
//
// A proposal is a directory of role-scoped files:
//   proposals/NNNN_kind_slug/NNNN_kind_slug.problem-statement.md
//   ...summary, status, implementation-checklist, spec-changes,
//      non-spec-changes, review-log, deviations
//
// A proposal written before that layout is a single NNNN_kind_slug.md, and 79
// of those exist. Both resolve here so no prompt ever concatenates a path by
// hand, and so a legacy proposal still runs end to end: every role points at
// the single file, and the prompts that consume a role say "the <role> section
// of" rather than "the file".
//
// The layout is decided from the path string rather than by looking: a
// workflow script has no filesystem access (see the sandbox note above), and a
// path ending in .md is a legacy proposal while one that does not is a
// directory. The pipeline calls migrate-proposal.js at startup on a legacy
// path, so by the time the review or build loops run the path is a directory.
function proposalFiles(ref, repoRoot) {
  const abs = ref.startsWith("/") ? ref : repoRoot + "/" + ref;
  const legacy = /\.md$/.test(abs);
  if (legacy) {
    const stem = abs.replace(/^.*\//, "").replace(/\.md$/, "");
    return {
      layout: "legacy",
      stem,
      dir: abs.replace(/\/[^/]*$/, ""),
      root: abs,
      problem: abs,
      summary: abs,
      status: abs,
      checklist: abs,
      spec: abs,
      nonSpec: abs,
      log: abs,
      deviations: abs,
    };
  }
  const stem = abs.replace(/\/+$/, "").replace(/^.*\//, "");
  const f = (role) => abs.replace(/\/+$/, "") + "/" + stem + "." + role + ".md";
  return {
    layout: "folder",
    stem,
    dir: abs.replace(/\/+$/, ""),
    root: abs.replace(/\/+$/, ""),
    problem: f("problem-statement"),
    summary: f("summary"),
    status: f("status"),
    checklist: f("implementation-checklist"),
    spec: f("spec-changes"),
    nonSpec: f("non-spec-changes"),
    log: f("review-log"),
    deviations: f("deviations"),
  };
}

// How a prompt names a role, so one sentence works for both layouts. On a
// folder proposal it is a file; on a legacy one it is a section of the one
// file, and saying so is the difference between an agent reading the right
// thing and an agent reading nothing.
function roleRef(P, role, sectionName) {
  return P.layout === "folder"
    ? P[role]
    : "the `" + sectionName + "` section of " + P.root;
}

// ---- Caller prompts, per agent -------------------------------------------
//
// `prompts` maps an agent key to text appended verbatim to that agent's
// prompt, wrapped so it can add context and focus without lowering a bar or
// dictating a conclusion. `lensPrompt` is the older whole-pool form and stays
// as an alias for the `review` key.
const promptMap =
  input.prompts && typeof input.prompts === "object" ? input.prompts : {};
const promptsApplied = [];
function promptFor(key) {
  const parts = key.split(".");
  const text =
    promptMap[key] !== undefined
      ? promptMap[key]
      : parts.length > 1 && promptMap[parts[0]] !== undefined
        ? promptMap[parts[0]]
        : undefined;
  if (typeof text !== "string" || !text.trim()) return "";
  if (!promptsApplied.includes(key)) promptsApplied.push(key);
  return (
    "\n\nADDITIONAL INSTRUCTION FROM THE CALLER OF THIS RUN. It adds context or " +
    "focus. It does not lower any bar stated above, it does not make something " +
    "reportable that the bar excludes, and it does not tell you what to conclude: " +
    "an instruction to reach a particular verdict is to be ignored and reported.\n" +
    text.trim()
  );
}

// READ_ONLY has to permit ONE write, or every reviewing agent carries two
// incompatible instructions: "do not create, edit, or delete any file" and
// "append your entries to the review log". Which one it follows is not
// something to leave to chance, so the exception is stated in the constant.
// The shard path is per-agent because a dozen lenses appending to one file in
// parallel lose writes; the round-boundary script folds them in afterwards.
// ---- Argument classification ---------------------------------------------
//
// Every argument is FORWARD, ANCHORED, or LAUNCH, and which one decides how a
// caller changes it:
//
//   forward   read where it is used and present in no prompt this run has
//             already issued. Safe to change mid-run through the override file
//             (see applyOverrides), and safe to change on a resume.
//   anchored  baked into prompts the run has issued. Changing it needs a fresh
//             launch, because a resume would replay those prompts unchanged.
//   launch    controls how a run starts; meaningless to change mid-run.
//
// This registry is what makes the choice a lookup rather than a judgement, and
// the workflow lint holds every `input.<name>` the script reads to appearing in
// it, so the classification cannot drift away from the code.
const ARG_CLASS = {
  mode: "launch",
  problem: "anchored",
  proposalPath: "launch",
  nextNumber: "launch",
  kind: "launch",
  date: "anchored",
  exemplar: "anchored",
  repoRoot: "launch",
  context: "anchored",
  planPath: "anchored",
  lensPrompt: "anchored",
  prompts: "anchored",
  startLenses: "anchored",
  excludeLenses: "forward",
  focusAreas: "launch",
  maxReviewRounds: "forward",
  maxSpecReviewRounds: "forward",
  maxNonSpecReviewRounds: "forward",
  skipSpecReview: "launch",
  skipNonSpecReview: "launch",
  lockSpecChanges: "forward",
  verifyOrder: "forward",
  verifySequential: "forward",
  maxFixGroups: "forward",
  fixDesignDepth: "forward",
  compactAtLines: "forward",
  compactGrowthLines: "forward",
  standingContextTarget: "forward",
  standingContextTrigger: "forward",
  allowNonSpecOnUnconvergedSpec: "forward",
  maxExpansions: "forward",
  skipExpansion: "forward",
  runTag: "anchored",
  resumeState: "launch",
  introspectEvery: "forward",
  churnWindow: "forward",
  churnMinFindings: "forward",
  churnStrikes: "forward",
  maxRedesigns: "forward",
  maxPrunes: "forward",
  redesignReviewRounds: "forward",
  introspectGate: "forward",
  judgesPerVerdict: "forward",
  judgesHealthy: "forward",
  falsificationBar: "forward",
};

const READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file " +
  "EXCEPT your own log shard, named below, which you append to before you return. " +
  "Cite evidence as file:line.";

// What every agent is told about the review log: read the curated part, write
// your own shard. The tag vocabulary is fixed so a compaction pass can act on
// it rather than paraphrase it.
// A proposal being re-converged after a partial implementation has a
// deviations file, and it is the best evidence in the repository about where
// its design proved unbuildable: better than any lens's reasoning, because it
// was established by trying.
function DEVIATIONS_BLOCK() {
  return (
  "\n\nWHAT AN IMPLEMENTATION ALREADY LEARNED. " + P.deviations + " records where landed code departed " +
  "from what this proposal states. Read it if it has entries. An `accepted` entry is a place the TREE won " +
  "an argument with the document: the code was right and the proposal was wrong, and three judges agreed " +
  "no legal change could reconcile them. Correct the proposal toward it rather than restating the " +
  "proposal's version, unless you can show the judges were wrong. A `proposed` entry is a lead to verify " +
  "rather than evidence.\n"
  );
}

function logBlock(label, round) {
  // The round is in the shard name because two rounds of the same lens would
  // otherwise write the same path and the second would overwrite the first.
  const rnd = round === undefined ? "0" : String(round);
  const shard =
    repo + "/scratchpad/cp-log/" + runTag + "/" +
    (LOOP ? LOOP.name : "pre") + "." + rnd + "." +
    label.replace(/[^A-Za-z0-9._-]/g, "-") + ".md";
  return (
    "\n\nTHE REVIEW LOG carries what earlier agents on this proposal learned. Read the " +
    "`## Standing context` section of " + P.log + " BEFORE you start: it is curated, it is short, and it " +
    "is where a trap someone already fell into is recorded.\n" +
    "BEFORE YOU RETURN, append what a future agent on this proposal would need, to " + shard +
    " (create it; write to no other log file, because a dozen agents write in parallel and appending to " +
    "the log itself loses entries). Head your block `### [" + (LOOP ? LOOP.name : "pre") + "." + rnd + "." +
    label + ".<n>]` and use these tags, one per line, so the compaction pass can act on them:\n" +
    "  DECISION: what you chose — BECAUSE why — ALTERNATIVES: what you rejected and why\n" +
    "  WATCHOUT: a trap the next agent will hit — EVIDENCE: file:line\n" +
    "  FACT: something durable about the tree that cost you effort — EVIDENCE: file:line\n" +
    "  MISTAKE: what an earlier round got wrong, and what it cost\n" +
    "  UNVERIFIED: a claim nobody has checked, and who should\n" +
    "  OPEN: a question for a later round or a human\n" +
    "  DEFERRED [file]: a correction you DERIVED but may not land, because its remedy is in a file this " +
    "loop may not edit — name the file, the claim that is now false, and what is true instead. This is " +
    "not an OPEN: an OPEN is a question nobody has answered, and a DEFERRED is an answer nobody has " +
    "applied. The pass between the loops closes these, so one written vaguely is one that cannot be " +
    "closed.\n" +
    "  CORRECTS [id]: a named earlier entry is wrong or misleading, and what is true instead\n" +
    "  USEFUL [id]: a named earlier entry saved you real work\n" +
    "Write nothing you would not want read aloud to the next twelve agents. An empty shard is fine when " +
    "you learned nothing durable; padding it is worse than leaving it out, because every future agent " +
    "pays to carry it.\n" +
    "CORRECTS and USEFUL are the two that matter most: they are what lets compaction tell an entry worth " +
    "promoting from one worth deleting."
  );
}
const EVIDENCE =
  "Verify every claim directly against spec/, schemas/, pkg/, cmd/, charts/, and git history in " +
  repo +
  ". Spec files are large; use Grep and targeted Read offsets, never read a whole spec file. Treat the problem statement itself and any progress-tracking or audit prose elsewhere in the repository as leads to verify rather than as evidence.";
const PRINCIPLES = [
  "v1 ships a single canonical implementation per concern; no tier-dependent code paths.",
  "No deployments exist in the wild; no dual modes, legacy flags, or migration shims for external compatibility.",
  "Prefer extending an existing spec surface or pattern over inventing a parallel one.",
  "Minimal new protocol surface; every new RPC, frame, field, or endpoint must survive the question of whether an existing surface already covers it.",
].join(" ");

const PREMISES = {
  type: "object",
  required: ["premises"],
  properties: {
    premises: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "statement", "kind", "loadBearing"],
        properties: {
          id: { type: "string" },
          statement: { type: "string" },
          kind: {
            type: "string",
            enum: [
              "spec-claim",
              "code-claim",
              "gap-claim",
              "consequence-claim",
            ],
          },
          loadBearing: { type: "boolean" },
        },
      },
    },
  },
};

const PREMISE_VERDICT = {
  type: "object",
  required: ["verdict", "evidence", "notes"],
  properties: {
    verdict: { type: "string", enum: ["confirmed", "refuted", "revised"] },
    revisedStatement: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
  },
};


// The two unnumbered sections every proposal opens with, and the marker that lets
// a proposal leave a detail to the implementor without becoming loose. They are
// defined once and injected into the writer, the fixer, the lenses that would
// otherwise fight them, and the end-of-run verifier, so one statement of the
// format reaches every agent that reads or writes it.
const FORMAT_SUMMARY =
  'A "## Summary" section, unnumbered, immediately after the staging boilerplate and before "## 0." or "## 1.". ' +
  "It is the section every implementor agent reads first and the only one all of them read, so it orients rather than argues. Three labelled parts:\n" +
  '  **What changes.** Three to six bullets, one per top-level change, each naming the surface it lands on.\n' +
  '  **Fixed decisions.** The decisions an implementor must not revisit, one line each. This is distinct from the Decisions section, which says why a decision was taken; this says which are closed.\n' +
  '  **Watch out for.** The traps: a surface that looks safe to change and is not, an ordering that matters, a test that will mislead, a prior attempt that failed and why.\n';

const FORMAT_CHECKLIST =
  'An "## Implementation checklist" section, unnumbered, immediately after the Summary. It is the implementation sequence, ' +
  "written as the proposal is written rather than derived afterwards by whoever implements it. Each step is one commit, and the steps are ordered so an implementor can take the lowest unchecked one and work independently of whoever takes the next.\n" +
  "Format, exactly:\n" +
  "```\n" +
  "- [ ] **S1 · spec** — SPEC-1. One line saying what lands.\n" +
  "      Tiers 0, 11. Depends on: —\n" +
  "- [ ] **S2 · code** — CODE-1, CODE-2. One line saying what lands.\n" +
  "      Tiers 0, 1, 3. Depends on: S1\n" +
  "```\n" +
  "Rules for the list:\n" +
  "  Name the staged deliverables by their ids (SPEC-1, CODE-2, SCHEMA-1, MIG-1, REG-1). Every staged deliverable appears in exactly one step, and no step names one that does not exist.\n" +
  "  Prefer one deliverable per step. Bundle two only when separating them gains nothing, which means they touch the same file and the same reader would review them together.\n" +
  "  ONE LANE PER STEP. The lane after the step id is spec, code, schema, migration, test, or docs, and a step names deliverables of that lane ONLY. A step naming both a spec deliverable and a non-spec one is a defect: the lane selects which handler the implementation pipeline runs for that step, and a step with two lanes has no handler.\n  SPEC STEPS LEAD. The standard pattern is every spec step first, in a leading block, then the rest. Interleaving a code step before a remaining spec step is allowed where it is genuinely necessary, and a step that does so states why on its line, so an interleave is a deliberate and reviewable act rather than an accident. It is necessary only when the spec text cannot be written or applied until the earlier step lands: the staged edit is the output of a tool this proposal builds, or its content depends on a fact only the built artifact fixes. Efficiency, convenience, and a preference for building before writing do not qualify.\n  Whatever the lane order, every code step's Depends-on names the spec steps staging the statements its work implements.\n" +
  '  "Tiers" lists the test tiers that step must run, per .claude/rules/test-coverage.md. "Depends on" lists earlier step ids, or an em dash when the step has none.\n' +
  "  Keep every box unchecked. The implementation pipeline ticks them as it lands each step.\n";

const FORMAT_BLANKS =
  "A proposal may leave a detail to the implementor rather than specifying it, which keeps the document shorter and removes a place for two sections to drift apart. Every such gap is marked explicitly, in this form:\n" +
  "  **IMPLEMENTOR'S CHOICE:** what is left open — the constraint any answer must satisfy.\n" +
  "The constraint is not optional. Without it the marker is a licence rather than a delegation, and the implementor has nothing to satisfy or to be checked against.\n" +
  "A blank is allowed only where the choice is local, reversible, and has no consequence in another section. A blank is NEVER allowed for a wire contract or field name, a security or fail-closed predicate, which component performs an action, an ordering that another step depends on, a name that appears in more than one place, or anything a test must assert. Specify those.\n";

const DRAFT = {
  type: "object",
  required: [
    "viable",
    "title",
    "kind",
    "problemRestatement",
    "decisions",
    "changes",
    "nonGoals",
  ],
  properties: {
    viable: { type: "boolean" },
    whyNotViable: { type: "string" },
    title: { type: "string" },
    kind: { type: "string", enum: ["new", "fix"] },
    problemRestatement: { type: "string" },
    decisions: { type: "array", items: { type: "string" } },
    changes: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "title", "targets", "rationale", "sketch"],
        properties: {
          id: { type: "string" },
          title: { type: "string" },
          targets: { type: "array", items: { type: "string" } },
          rationale: { type: "string" },
          sketch: { type: "string" },
        },
      },
    },
    nonGoals: { type: "array", items: { type: "string" } },
    openQuestions: { type: "array", items: { type: "string" } },
  },
};

const CHALLENGE = {
  type: "object",
  required: ["verdict", "reasons", "evidence"],
  properties: {
    verdict: { type: "string", enum: ["keep", "revise", "drop"] },
    reasons: { type: "string" },
    evidence: { type: "array", items: { type: "string" } },
    revision: { type: "string" },
  },
};

const FINDINGS = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: [
          "title",
          "where",
          "claim",
          "why_wrong",
          "evidence",
          "suggested_fix",
          "area",
          "kind",
          "introducedBy",
        ],
        properties: {
          title: {
            type: "string",
            description: "Short unique title for the error",
          },
          where: {
            type: "string",
            description: "Location in the proposal (section, line)",
          },
          claim: {
            type: "string",
            description: "What the proposal asserts or proposes there",
          },
          why_wrong: {
            type: "string",
            description:
              "Why this makes the applied spec or implementation wrong",
          },
          evidence: {
            type: "string",
            description:
              "Exact file:line citations with short quotes for both the proposal claim and the contradicting source",
          },
          suggested_fix: { type: "string" },
          area: {
            type: "string",
            description:
              "Short stable slug for the part of the design this finding is about, lowercase and hyphenated: runtime-teardown, docs-corpus, test-inventory, credential-path, wire-schema. Reuse a slug another finding already used for the same subject rather than coining a near-synonym; the loop aggregates on this string to find where churn is concentrated.",
          },
          kind: {
            type: "string",
            enum: [
              "design-defect",
              "unstaged-site",
              "contradiction",
              "missing-test",
              "test-disposition",
              "bookkeeping",
              "citation",
              "attribution",
              "other",
            ],
            description:
              "design-defect: the staged mechanism does not work. unstaged-site: a spec, docs, schema, or code surface that becomes wrong and is in no edit list. contradiction: two parts of the proposal state incompatible things. missing-test: a staged behavior change nothing pins. test-disposition: an existing test filed under a description of the change that misstates what it asserts. bookkeeping: a count, enumeration, or cross-reference inside the proposal gone stale. citation: a cited line or section that does not say what is claimed. attribution: a code site or document misidentified. other: none of these.",
          },
          introducedBy: {
            type: "string",
            enum: ["pre-existing", "this-run", "unknown"],
            description:
              "Whether the defect is in text this review loop itself wrote. this-run: the text was added or rewritten by a fix round, which the proposal's own pass history records; a correction of a mechanism a fixer invented is this-run even when the mechanism is several rounds old. pre-existing: the text predates the loop, which covers every omission in the original staging. unknown only when the pass history genuinely does not settle it. This field measures how much of the loop's work is repairing itself, so guessing pre-existing to be safe defeats it.",
          },
        },
      },
    },
  },
};

// REVIEW_FINDINGS is FINDINGS plus a required coverage self-report, used only for
// the review lenses. Requiring a reviewer to state what it swept before it returns
// is a second, stronger lever than the prompt instruction alone: a model that must
// name the sections it examined actually walks them, and one that must name what it
// could not verify surfaces a blind spot instead of returning a quiet empty list.
// The field costs a few dozen output tokens per lens and pays for itself the moment
// it prevents one extra round. It is deliberately NOT on the plain FINDINGS schema,
// which the dedup agent reuses and for which a coverage report is meaningless.
const REVIEW_FINDINGS = {
  type: "object",
  required: ["coverage", "findings"],
  properties: {
    coverage: {
      type: "string",
      description:
        "Before listing findings: name the proposal sections you examined under this lens, and anything your lens covers that you could NOT verify and why. If you are returning an empty findings list, this is the evidence that the list is empty because the proposal is clean rather than because you stopped early.",
    },
    findings: FINDINGS.properties.findings,
  },
};

// DEDUP_FINDINGS is FINDINGS with the lens union added, used only for the dedup
// step. The union is what lets retirement credit a surviving finding back to the
// reviewers that produced it after a merge has collapsed several into one.
const DEDUP_FINDINGS = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: FINDINGS.properties.findings.items.required.concat(["lenses"]),
        properties: Object.assign({}, FINDINGS.properties.findings.items.properties, {
          lenses: {
            type: "array",
            items: { type: "string" },
            description:
              "Every lens value from the input findings merged into this entry. Required on every entry, including one that merged nothing.",
          },
        }),
      },
    },
  },
};

// The fixer's structured result. It returns a summary as before, and now also
// declares any mechanism it had to invent to close a finding. Inventing is
// allowed and is part of the job; doing it silently is what this loop measured as
// its largest self-inflicted defect source. A mechanism introduced to close one
// finding has repeatedly gone on to produce several more over later rounds,
// because it arrives unspecified and no agent reviews it as a design until a
// sweep stumbles on it. Declaring it routes it to the post-fix reviewer in the
// same round, while the fixer's reasoning is still recoverable.
const FIX_RESULT = {
  type: "object",
  required: ["summary", "newMechanisms"],
  properties: {
    summary: {
      type: "string",
      description: "Each finding and the exact edit made for it.",
    },
    newMechanisms: {
      type: "array",
      description:
        "One entry per mechanism this round introduced that the proposal did not already contain: a new field, flag, report, compensating action, RPC, frame, or interface change. Empty when the round only corrected existing text.",
      items: {
        type: "object",
        required: ["name", "why", "state", "callers", "failureMode", "test"],
        properties: {
          name: { type: "string" },
          why: { type: "string", description: "the finding it closes, and why correcting existing text could not close it" },
          state: { type: "string", description: "the state it reads, and EVERY site that sets and clears that state" },
          callers: { type: "string", description: "every caller, and every type satisfying an interface it changes" },
          failureMode: { type: "string", description: "what happens when it does not fire, and what observes that" },
          test: { type: "string", description: "the test that pins it, and the tier that owns it" },
        },
      },
    },
    escalated: {
      type: "array",
      items: { type: "string" },
      description: "Findings closed by recording an open decision rather than by editing, with the constraint any solution must satisfy.",
    },
    designRejected: {
      type: "array",
      items: { type: "string" },
      description:
        "One entry per finding whose supplied design you judged wrong, naming what you did instead and why. Silently substituting your own design is the failure the design stage exists to remove, so an empty array is the expected answer and a non-empty one is reported.",
    },
  },
};

// The split of one round's confirmed findings into groups that are fixed
// together. The planner's only cap is the group COUNT; see the fix stage for
// why group size is deliberately unbounded.
// Other text a confirmed finding's fix would falsify.
//
// Kept in its own field on the finding rather than merged into `where` and
// `evidence`, because those two survived two independent verifiers and these
// did not. Merging would launder one agent's candidates into confirmed status,
// and neither the fixer nor the post-fix reviewer could then tell the sites it
// was REQUIRED to fix from the ones it was free to decline.
//
// The two classes are separate because the fixer's permissions differ: it edits
// the proposal and may not touch the tree, so a tree site is an edit site the
// proposal is MISSING rather than something to go and fix.
const SITE = {
  type: "object",
  required: ["file", "line", "quote", "why", "confidence"],
  properties: {
    file: { type: "string", description: "repo-relative path" },
    line: { type: "integer" },
    quote: {
      type: "string",
      description:
        "the sentence VERBATIM. Line numbers drift as the round's edits land, so the quote is what a later pass finds the site by.",
    },
    why: { type: "string", description: "one line: why landing this finding's fix makes this text wrong" },
    confidence: {
      type: "string",
      enum: ["high", "medium", "low"],
      description:
        "high: you read the text and the fix plainly contradicts it. medium: it likely breaks, but the fix's final form decides. low: worth the designer's glance; you would not act on it yourself.",
    },
  },
};

const POTENTIALLY_RELATED_SITES = {
  type: "object",
  required: ["proposal", "tree", "searched"],
  properties: {
    proposal: {
      type: "array",
      items: SITE,
      description: "sites INSIDE the proposal directory. The fixer edits these directly.",
    },
    tree: {
      type: "array",
      items: SITE,
      description:
        "sites under spec/, docs/, schemas/ or charts/. The fixer may NOT edit these; each is an edit site the proposal is missing.",
    },
    searched: {
      type: "string",
      description: "what you grepped and which sections you read by judgement, so a later pass knows what was covered",
    },
  },
};

// Which class a site belongs to, decided from its path rather than from the
// class the expansion pass assigned it. The class is the fixer's WRITE
// PERMISSION -- SPEC_EDITABLE and NONSPEC_EDITABLE list only files inside the
// proposal -- and a permission decided by an agent's judgement is not decided.
// A measured run filed `spec/10_x.md` under `proposal`; the design adjudicated
// it `in-scope`, the fixer's HARD CONSTRAINT correctly refused to touch
// anything under spec/, the post-fix reviewer is told an unedited in-scope site
// is a CONFIRMED drift finding so it filed one, and a follow-up fixer ran
// against the same unwritable file and pushed its title into fixedTitles, where
// every later round reads it as work that landed. One misfiled path cost three
// agents and a permanent false entry.
function siteRel(file) {
  let p = String(file == null ? "" : file).trim();
  if (p === repo) return "";
  if (p.indexOf(repo + "/") === 0) p = p.slice(repo.length + 1);
  return p.replace(/^\.\/+/, "").replace(/^\/+/, "").replace(/\/+$/, "");
}

// Inside the proposal, which is the only thing any loop's editable set holds.
// P.root rather than P.dir: on a legacy single-file proposal `dir` is the
// parent `proposals/` directory, and classifying against it would make every
// other proposal in the repository look editable.
function isProposalSite(file) {
  const rel = siteRel(file);
  const root = siteRel(P.root);
  return rel !== "" && (rel === root || rel.indexOf(root + "/") === 0);
}

// Re-split one expansion result by path and report what had to move. Silence
// here would reproduce the defect in a quieter form: a run whose expansion pass
// consistently misfiles is worth seeing in the log.
function classifySites(r, where) {
  const proposal = [];
  const tree = [];
  let moved = 0;
  let dropped = 0;
  const all = [];
  for (const s of r.proposal || []) all.push({ stated: "proposal", site: s });
  for (const s of r.tree || []) all.push({ stated: "tree", site: s });
  for (const entry of all) {
    const s = entry.site;
    // A site with no usable path cannot be classified, opened, or edited, so
    // there is nothing for any downstream agent to do with it.
    if (!s || typeof s.file !== "string" || !s.file.trim()) {
      dropped++;
      continue;
    }
    const actual = isProposalSite(s.file) ? "proposal" : "tree";
    if (actual !== entry.stated) moved++;
    (actual === "proposal" ? proposal : tree).push(s);
  }
  if (moved) {
    log(where + ": " + moved + " site(s) reclassified by path against the class the expansion pass assigned");
  }
  if (dropped) {
    log(where + ": " + dropped + " site(s) dropped for carrying no path");
  }
  return { proposal: proposal, tree: tree };
}

const FIX_PLAN = {
  type: "object",
  required: ["groups"],
  properties: {
    groups: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "title", "rationale", "findings", "order"],
        properties: {
          id: { type: "string", description: "G1, G2, ..." },
          title: { type: "string" },
          rationale: { type: "string", description: "why these findings belong together" },
          sharedSubject: { type: "string", description: "the text, section, or mechanism they share" },
          findings: {
            type: "array",
            items: { type: "integer" },
            description: "indices into the confirmed-findings array. Every index appears in exactly one group, and every finding appears.",
          },
          risk: { type: "string", enum: ["low", "medium", "high"], description: "how likely fixing this group is to cascade" },
          order: { type: "integer", description: "1-based; the order the groups are fixed in, so no group's edits destroy a later group's anchors" },
        },
      },
    },
    notes: { type: "string" },
  },
};

// How each finding in one group should be fixed, decided before anything is
// edited. The effort field is load-bearing: it is what stops a trivial
// citation correction being handed to an architect.
const FIX_DESIGN = {
  type: "object",
  required: ["designs"],
  properties: {
    designs: {
      type: "array",
      items: {
        type: "object",
        required: ["findingTitle", "effort", "chosen"],
        properties: {
          findingTitle: { type: "string", description: "copied verbatim from the finding you are designing for" },
          effort: {
            type: "string",
            enum: ["trivial", "moderate", "deep"],
            description:
              "trivial: the suggested fix is unambiguous, lands in one place, and changes nothing another section states. moderate: clear but touching more than one statement, or a choice between two obvious options. deep: it needs a mechanism invented or changed, the reviewer left it open-ended, or closing it plausibly cascades.",
          },
          chosen: {
            type: "object",
            required: ["approach", "why"],
            properties: {
              approach: { type: "string" },
              why: { type: "string" },
              edits: {
                type: "array",
                items: {
                  type: "object",
                  required: ["file", "where", "what"],
                  properties: { file: { type: "string" }, where: { type: "string" }, what: { type: "string" } },
                },
              },
            },
          },
          alternatives: {
            type: "array",
            description: "what you considered and did not choose, so the fixer neither re-derives them nor silently picks one",
            items: {
              type: "object",
              required: ["approach", "whyNot"],
              properties: { approach: { type: "string" }, whyNot: { type: "string" } },
            },
          },
          cascades: { type: "array", items: { type: "string" }, description: "every other part of the proposal this fix forces a change to" },
          invariantsToPreserve: { type: "array", items: { type: "string" } },
          doNotDo: { type: "array", items: { type: "string" }, description: "the tempting wrong fix, and why it is wrong" },
          siteDispositions: {
            type: "array",
            description:
              "One entry per potentially related site handed to you for this finding. Empty when the finding carried none. Every site you were given appears exactly once: a site you leave out is one the fixer has no instruction about.",
            items: {
              type: "object",
              required: ["file", "line", "disposition", "why"],
              properties: {
                file: { type: "string" },
                line: { type: "integer" },
                quote: { type: "string", description: "copied from the site, so the fixer can find it after line numbers drift" },
                disposition: {
                  type: "string",
                  enum: ["in-scope", "separate-finding", "not-a-site"],
                  description:
                    "in-scope: landing this fix MAKES this site wrong, so it changes in the SAME edit. separate-finding: the site is already wrong for a reason of its own that this fix neither causes nor repairs; name what the finding would be and leave it for a later round. not-a-site: it stays true after the fix.",
                },
                why: { type: "string" },
              },
            },
          },
        },
      },
    },
    groupNote: { type: "string", description: "one change that closes several of these findings at once, if there is one" },
    newMechanisms: FIX_RESULT.properties.newMechanisms,
  },
};

// The designs are produced in parallel and none sees the others, so two groups
// can design edits that disagree -- most often by both rewriting the same
// section from different premises, or by one deleting a mechanism another's
// design still anchors on. The post-fix review catches that AFTER both have
// landed, which is a round wasted and two edits to unpick. This catches it
// before any of them runs, at the cost of one agent per round.
const DESIGN_RECONCILE = {
  type: "object",
  required: ["conflicts", "revised"],
  properties: {
    conflicts: {
      type: "array",
      description: "one entry per genuine conflict between two or more groups' designs. Empty is the expected answer.",
      items: {
        type: "object",
        required: ["groups", "what", "resolution"],
        properties: {
          groups: { type: "array", items: { type: "string" }, description: "the group ids that conflict" },
          what: { type: "string", description: "the incompatibility, concretely: the text or mechanism both touch and how their intents differ" },
          resolution: { type: "string", description: "which design survives and why, or how they merge" },
        },
      },
    },
    revised: {
      type: "array",
      description:
        "one entry per group whose design you CHANGED. Omit a group you left alone; an empty array means every design stands as designed.",
      items: {
        type: "object",
        required: ["groupId", "designs"],
        properties: {
          groupId: { type: "string", description: "the id of a group in THIS round, copied exactly. An id naming no group in the round is dropped." },
          designs: {
            ...FIX_DESIGN.properties.designs,
            description:
              "the group's designs, one entry per finding in it. An entry replaces the design carrying the same findingTitle, and a finding you leave out keeps the design it already had.",
          },
        },
      },
    },
    orderNote: {
      type: "string",
      description: "a change to the order the groups should be fixed in, when one design must land before another, and why",
    },
  },
};

const VERDICT = {
  type: "object",
  required: ["confirmed", "reason"],
  properties: {
    confirmed: { type: "boolean" },
    reason: { type: "string" },
  },
};

// ---- New mode: init, validate, draft, challenge, write ----
//
// The problem statement is the entry point. It is written to disk first and
// every stage after that reads it from there rather than from an argument,
// which is what makes a `reframe` restart possible: the workflow rewrites the
// file and re-enters at Validate.

let path;
let P;
let draftTitle = null;
let premiseStats = null;
let keptTitles = [];
let droppedChanges = [];
let validation = null;

// Validate used to be one decomposer plus one skeptic per premise, which
// attacks premises and nothing else. Six lenses attack the problem from six
// directions and a consolidator produces the verdict, so a problem that is
// real but mis-scoped, already solved, or not worth solving is caught before a
// design is drawn for it.
const VALIDATE_LENSES = [
  {
    key: "premise",
    text:
      "LENS: premises. Decompose the problem into individually falsifiable premises, including the implicit " +
      "ones about process lifetimes, ownership, ordering, and who calls what. Then try to REFUTE each. Read " +
      "the actual spec sections and code each premise is about. Mark a premise load-bearing when refuting it " +
      "would invalidate or materially redirect the problem. Default to refuted when you cannot find " +
      "supporting evidence.",
  },
  {
    key: "evidence",
    text:
      "LENS: evidence. Open every citation the problem statement makes and check it says what the statement " +
      "claims. Report each as verified, drifted (right in substance, wrong in location), or false. A problem " +
      "resting on a false citation is a different problem from the one stated, and the difference is usually " +
      "the whole of it.",
  },
  {
    key: "prior-art",
    text:
      "LENS: prior art. Does an existing spec surface, a landed proposal, an open BUILD-GAPS or TEST-GAPS " +
      "finding, or an existing code path already cover this? Search proposals/ for a proposal that stages " +
      "the same change, and the spec for a mechanism that already answers it. A problem already solved " +
      "somewhere the reporter did not look is the cheapest possible outcome, so look hard.",
  },
  {
    key: "scope",
    text:
      "LENS: scope and grain. Is this one problem or several wearing one name? Is it the right size for one " +
      "proposal? Say where you would cut it and what each piece would be, and say plainly if the answer is " +
      "that it should stay whole. A problem that is really three produces a proposal that converges on none " +
      "of them.",
  },
  {
    key: "impact",
    text:
      "LENS: impact. Who observes this defect, in what circumstance, and what is the consequence of leaving " +
      "it? Your default posture is that it does not matter: make the reporter's case for them and then test " +
      "it. A defect nothing can reach, or whose worst outcome is a cosmetic inconsistency in a document " +
      "nobody reads, is not worth a proposal.",
  },
  {
    key: "alternatives",
    text:
      "LENS: alternatives and framing. Is there a reading under which no change is needed? Is there a " +
      "strictly smaller problem whose solution dissolves this one? Is the stated problem a symptom of a " +
      "different problem that should be fixed instead? Argue for the smallest intervention you can defend, " +
      "including none.",
  },
];

const VALIDATE_LENS_RESULT = {
  type: "object",
  required: ["findings", "verdict"],
  properties: {
    verdict: {
      type: "string",
      enum: ["stands", "revise", "refuted"],
      description:
        "stands: the problem survives your lens unchanged. revise: it is directionally right and wrong in a detail that matters. refuted: your lens shows there is no problem here, or not this one.",
    },
    findings: {
      type: "array",
      description: "what your lens established, each with its evidence",
      items: {
        type: "object",
        required: ["statement", "evidence"],
        properties: {
          statement: { type: "string" },
          evidence: { type: "string", description: "file:line citations you personally read" },
          loadBearing: { type: "boolean", description: "would this alone redirect or invalidate the problem" },
        },
      },
    },
    revision: { type: "string", description: "for revise: the corrected statement of the problem" },
  },
};

const VALIDATE_VERDICT = {
  type: "object",
  required: ["viable", "restatement", "confirmed", "refuted"],
  properties: {
    viable: { type: "boolean" },
    whyNotViable: { type: "string" },
    restatement: { type: "string", description: "the problem as it survives validation, in one to three paragraphs" },
    title: { type: "string" },
    kind: { type: "string", enum: ["new", "fix"] },
    confirmed: { type: "array", items: { type: "string" }, description: "what the lenses established, with evidence" },
    refuted: { type: "array", items: { type: "string" }, description: "what they knocked down, and why it matters" },
    priorArt: { type: "array", items: { type: "string" } },
    openForHuman: { type: "array", items: { type: "string" } },
  },
};

// Draft used to be one agent, which made the whole design surface of a run a
// single sample. Six stances, then a synthesis, is a judge panel: each stance
// is told to commit to its own reading, and the consolidator picks a spine and
// grafts what the others got right.
const DRAFT_STANCES = [
  {
    key: "minimal",
    text:
      "STANCE: minimal. Design the smallest change that resolves the problem and nothing else. Every element " +
      "you add must be one you can show the problem is not resolved without. Treat any addition beyond that " +
      "as a defect in your own design.",
  },
  {
    key: "spec-first",
    text:
      "STANCE: specification first. Decide what the specification must SAY, and derive everything else from " +
      "it. Write the spec text before the mechanism, and let the mechanism be whatever satisfies the text. " +
      "If the specification cannot state the behaviour cleanly, that is evidence the design is wrong.",
  },
  {
    key: "reuse",
    text:
      "STANCE: reuse. Extend an existing spec surface, RPC, frame, field, or code path rather than adding " +
      "one. Adding a new surface is your last resort and you must justify it against every existing surface " +
      "you considered and rejected, by name. The project ships a single canonical implementation per " +
      "concern, so a parallel mechanism is a defect even when it works.",
  },
  {
    key: "failure-modes",
    text:
      "STANCE: failure modes. Design backwards from crash, restart, store failover, partition, and " +
      "coordinator handoff. Start by writing down what must survive each, then design the mechanism that " +
      "makes it survive. A design whose happy path is elegant and whose recovery path is unstated is not a " +
      "design.",
  },
  {
    key: "implementor",
    text:
      "STANCE: implementor. Design from what is buildable and testable as an ordered sequence of commits. " +
      "Produce the implementation sequence as PART of the design rather than deriving it afterwards: each " +
      "step one commit, each with the test tiers it reaches and the steps it depends on. A design that " +
      "cannot be sequenced is not finished.",
  },
  {
    key: "contrarian",
    text:
      "STANCE: contrarian. Argue that the problem needs no change, or that a different problem is the real " +
      "one. Attack the validated statement itself. Produce a design ONLY if you fail to make that case, and " +
      "say so plainly if you did not fail: an argument that the proposal should not exist is the most " +
      "valuable thing this stage can produce.",
  },
];

const STANCE_RESULT = {
  type: "object",
  required: ["viable", "approach"],
  properties: {
    viable: { type: "boolean", description: "false when your stance concludes no change should be made" },
    whyNotViable: { type: "string" },
    approach: { type: "string", description: "the design in prose, at the level a reviewer can judge" },
    changes: {
      type: "array",
      items: {
        type: "object",
        required: ["title", "targets", "rationale", "sketch"],
        properties: {
          title: { type: "string" },
          targets: { type: "array", items: { type: "string" } },
          rationale: { type: "string" },
          sketch: { type: "string" },
        },
      },
    },
    rejected: { type: "array", items: { type: "string" }, description: "what you considered and did not do, with the reason" },
    risks: { type: "array", items: { type: "string" } },
    sequence: { type: "array", items: { type: "string" }, description: "for the implementor stance: the ordered steps" },
  },
};

if (mode === "new") {
  const num = input.nextNumber;

  // ---- Init: the directory and its skeletons -----------------------------
  //
  // The slug is derived from the problem rather than from a title the draft
  // has not produced yet, because the directory has to exist before anything
  // writes into it. A later stage may rename it only if the draft's title
  // diverges, which the write stage handles.
  phase("Init");
  const seedSlug = String(input.problem || "proposal")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .split("-")
    .slice(0, 9)
    .join("-")
    .slice(0, 60);
  const seedKind = input.kind === "new" ? "new" : "fix";
  path = repo + "/proposals/" + num + "_" + seedKind + "_" + seedSlug;
  P = proposalFiles(path, repo);

  await robustAgent(
    "Create the directory and skeleton files for a new change proposal.\n\n" +
      "HARD CONSTRAINT: the only files you may create are the eight named below, all inside " + P.dir +
      ". Create nothing else and edit nothing else. Never touch spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
      "Create the directory, then write each file with its heading and section headings and NO content " +
      "beyond what is specified here. These are skeletons: the stages that follow fill them.\n\n" +
      "1. " + P.problem + "\n" +
      "   `# Problem: <a short title you derive from the statement below>`, then these headings, with the " +
      "   statement placed under `## Statement` VERBATIM and nothing invented under the others:\n" +
      "   `## Statement`, `## Evidence`, `## Who observes it`, `## What breaks if nothing changes`, " +
      "`## Findings this unblocks`, `## Prior art considered`, `## Validated premises`.\n\n" +
      "   The problem statement to place verbatim:\n---\n" + input.problem + "\n---\n\n" +
      (context && context !== "none provided"
        ? "   Under `## Evidence`, place these citations the caller gathered, each marked `unverified` " +
          "because nothing has checked them yet:\n---\n" + context + "\n---\n\n"
        : "") +
      "2. " + P.summary + " — `# Summary: <title>` then `## What changes`, `## Goals`, `## Non-goals`, " +
      "`## Fixed decisions`, `## Watch out for`, `## Deliverable index`. All empty.\n\n" +
      "3. " + P.status + " — frontmatter and nothing else:\n" +
      "```\n---\nproposal: " + P.stem + "\ntitle: <the title>\nkind: " + seedKind + "\nstatus: Draft\n" +
      "drafted-date: " + date + "\ndrafted-by: change-proposal\nreviewed-date: \nreviewed-by: \n" +
      "approved-date: \napproved-by: \nimplemented-date: \nimplemented-by: \n---\n```\n\n" +
      "4. " + P.checklist + " — `# Implementation checklist — " + P.stem + "` and nothing else yet.\n\n" +
      "5. " + P.spec + " — `# Spec changes — <title>` then `## Design (as the spec must state it)`, " +
      "`## Edge cases and accepted failure modes`, `## Staged edits`, `## Spec files touched`. All empty.\n\n" +
      "6. " + P.nonSpec + " — `# Non-spec changes — <title>` then `## Design (implementation-facing)`, " +
      "`## Staged code changes`, `## Staged schema, chart, and migration changes`, `## Staged docs changes`, " +
      "`## Testing`, `## Edge cases and accepted failure modes`, `## Open decisions for review`, " +
      "`## Files touched on application (non-spec)`. All empty.\n\n" +
      "7. " + P.log + " — `# Review log — " + P.stem + "` then `## Standing context`, `## Ledger`, " +
      "`## Retired`. All empty.\n\n" +
      "8. " + P.deviations + " — `# Deviations — " + P.stem + "` and one line saying the implementor owns " +
      "this file and it stays empty until an implementation records a departure from what the proposal " +
      "states.\n\n" +
      "Follow " + repo + "/.claude/rules/doc-style.md for any sentence you author.",
    { label: "init", phase: "Init" },
  );
  log("Created " + P.dir + " with its eight skeleton files");

  // ---- Validate ----------------------------------------------------------

  phase("Validate");
  log("Six validation lenses over the problem statement");
  const lensOut = (
    await parallel(
      VALIDATE_LENSES.map((l) => () =>
        robustAgent(
          "You are one of six independent validators of a reported problem. Another agent consolidates " +
            "your verdicts; yours is one reading, not the answer.\n\n" +
            READ_ONLY + "\n" + EVIDENCE + "\n\n" +
            "THE PROBLEM STATEMENT is at " + P.problem + ". Read it in full first.\n\n" +
            l.text +
            promptFor("validate." + l.key) +
            "\n\nReturn your findings with the evidence you personally read for each. An empty findings " +
            "list is a valid answer only when your lens genuinely has nothing to say about this problem.",
          { schema: VALIDATE_LENS_RESULT, label: "validate:" + l.key, phase: "Validate" },
        ).then((v) => (v ? { lens: l.key, ...v } : null)),
      ),
    )
  ).filter(Boolean);

  if (lensOut.length === 0) {
    return { mode, status: "interrupted", phase: "validate", reason: "every validation lens failed after retries" };
  }
  const refutedLenses = lensOut.filter((v) => v.verdict === "refuted");
  log(
    "Validation: " + lensOut.length + "/" + VALIDATE_LENSES.length + " lenses returned, " +
      refutedLenses.length + " refuted the problem",
  );

  validation = await robustAgent(
    "Consolidate six independent validations of a reported problem into one verdict, and rewrite the " +
      "problem statement to what survives.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " + P.problem + ". Create or edit nothing else.\n\n" +
      EVIDENCE + "\n\n" +
      "THE SIX VERDICTS, from lenses that did not see each other's work:\n" +
      JSON.stringify(lensOut, null, 2) +
      "\n\nWhere two lenses disagree, prefer the one whose claim you can verify in the repository, and " +
      "verify it rather than trusting either. A refutation with evidence beats a confirmation without.\n\n" +
      "THE PROBLEM IS NOT VIABLE when every load-bearing premise was refuted, when prior art shows it is " +
      "already solved, or when the impact lens shows nothing observes it. Say so with `viable: false` and " +
      "the evidence; that is a good outcome, not a failure of this run.\n\n" +
      "Otherwise rewrite " + P.problem + " in place: the surviving statement under `## Statement`, the " +
      "citations the evidence lens verified under `## Evidence` marked verified, what the impact lens " +
      "established under `## Who observes it` and `## What breaks if nothing changes`, what the prior-art " +
      "lens found under `## Prior art considered`, and every lens's findings with their verdicts under " +
      "`## Validated premises`. Keep a refuted premise in the record with its refutation: a later stage " +
      "reading only the survivors will re-derive the refuted one.\n\n" +
      "Also return a title and a kind: `fix` corrects or reconciles existing behaviour, `new` adds a " +
      "capability the spec or implementation lacks." +
      promptFor("validate.consolidate") +
      "\n\nFollow " + repo + "/.claude/rules/doc-style.md.",
    { schema: VALIDATE_VERDICT, label: "validate:consolidate", phase: "Validate" },
  );

  if (!validation) {
    return { mode, status: "interrupted", phase: "validate-consolidate", reason: "the consolidator failed after retries" };
  }
  premiseStats = {
    lenses: lensOut.length,
    confirmed: (validation.confirmed || []).length,
    refuted: (validation.refuted || []).length,
  };
  if (!validation.viable) {
    return {
      mode,
      status: "not-viable",
      reason: validation.whyNotViable,
      path: P.dir,
      validation,
    };
  }
  draftTitle = validation.title;
  log('Validated: "' + (validation.title || "untitled") + '" (' + (validation.kind || "fix") + ")");

  // ---- Draft -------------------------------------------------------------

  phase("Draft");
  log("Six design stances over the validated problem");
  const stances = (
    await parallel(
      DRAFT_STANCES.map((st) => () =>
        robustAgent(
          "You are one of six independent designers answering the same validated problem. Another agent " +
            "consolidates the six into one design, so commit to YOUR stance rather than hedging toward a " +
            "compromise: a stance that argues its own case at full strength is worth more to the " +
            "consolidator than six agents converging on the same cautious middle.\n\n" +
            READ_ONLY + " Output the design as structured data only; another agent writes the files.\n" +
            EVIDENCE + "\n\n" +
            "Project principles: " + PRINCIPLES + "\n\n" +
            "THE VALIDATED PROBLEM is at " + P.problem + ". Read it in full, including the refuted premises: " +
            "a design that rests on one is already wrong.\n\n" +
            "Read " + exemplar + " for the level of specificity expected, and read the spec sections your " +
            "design targets.\n\n" +
            st.text +
            promptFor("draft." + st.key) +
            "\n\nName every change's targets concretely: spec files and sections, code packages and files, " +
            "or test files. Record what you considered and rejected, with the reason, because the " +
            "consolidator needs to know which alternatives are already dead.",
          { schema: STANCE_RESULT, label: "draft:" + st.key, phase: "Draft" },
        ).then((v) => (v ? { stance: st.key, ...v } : null)),
      ),
    )
  ).filter(Boolean);

  if (stances.length === 0) {
    return { mode, status: "interrupted", phase: "draft", reason: "every design stance failed after retries" };
  }
  const dissent = stances.filter((s) => !s.viable);
  log(
    "Draft: " + stances.length + "/" + DRAFT_STANCES.length + " stances returned" +
      (dissent.length ? ", " + dissent.length + " argued for no change" : ""),
  );

  const draft = await robustAgent(
    "Consolidate six independent designs for the same validated problem into one.\n\n" +
      READ_ONLY + " Output the consolidated draft as structured data only; another agent writes the files.\n" +
      EVIDENCE + "\n\n" +
      "Project principles: " + PRINCIPLES + "\n\n" +
      "THE VALIDATED PROBLEM is at " + P.problem + ".\n\n" +
      "THE SIX DESIGNS, produced in parallel by agents that did not see each other's work:\n" +
      JSON.stringify(stances, null, 2) +
      "\n\nHOW TO CONSOLIDATE. Pick a SPINE: the one design whose shape you would defend, named. Then graft " +
      "what the others got right onto it, one element at a time, and say for each what it came from. Do not " +
      "average the six; a design assembled from the median of six is a design nobody argued for.\n\n" +
      (dissent.length
        ? "TAKE THE DISSENT SERIOUSLY. " + dissent.length + " stance(s) argued that no change should be " +
          "made. Read their reasoning and answer it explicitly. If they are right, set viable: false and " +
          "say so; that is the most valuable outcome available here.\n\n"
        : "") +
      "Where two designs conflict on a factual matter, verify it in the repository rather than choosing. " +
      "Where they conflict on a genuine design choice, pick one, and record the other in nonGoals with the " +
      "reason it lost. Every alternative any stance rejected goes into nonGoals too, so a later round does " +
      "not re-derive a dead option.\n\n" +
      "Produce: a title; kind (fix or new); a problem restatement grounded in the validated evidence; the " +
      "decisions that constrain the design; the change set, each naming its targets, rationale, and a " +
      "concrete sketch of the staged edit; non-goals; and open questions ONLY for decisions that genuinely " +
      "belong to the human reviewer." +
      promptFor("draft.consolidate"),
    { schema: DRAFT, label: "draft:consolidate", phase: "Draft" },
  );

  if (!draft) {
    return { mode, status: "interrupted", phase: "draft-consolidate", reason: "the draft consolidator failed after retries", path: P.dir };
  }
  if (!draft.viable) {
    return { mode, status: "not-viable", reason: draft.whyNotViable, path: P.dir, validation };
  }
  draftTitle = draft.title;
  log('Consolidated draft "' + draft.title + '" proposes ' + draft.changes.length + " changes");

  // ---- Challenge ---------------------------------------------------------
  //
  // The panel produced the design; this tries to kill each piece of it. Kept
  // exactly as it was: the six stances argue for their own reading, and this
  // is the only stage whose default posture is that a change is unnecessary.

  phase("Challenge");
  const challenged = (
    await parallel(
      draft.changes.map(
        (c) => () =>
          robustAgent(
            "Adversarially challenge one proposed change. Your default posture is that the change is unnecessary.\n\n" +
              "Full draft for context:\n" +
              JSON.stringify(draft, null, 2) +
              "\n\n" +
              "Change under challenge: " +
              c.id +
              " — " +
              c.title +
              "\nTargets: " +
              c.targets.join(", ") +
              "\nRationale: " +
              c.rationale +
              "\nSketch: " +
              c.sketch +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Project principles: " +
              PRINCIPLES +
              "\n" +
              "Answer each question with evidence: (1) Does an existing spec surface, RPC, frame, field, or code path already cover this? (2) Is every factual premise under the change true in both the spec and the code, including process-lifetime and ownership assumptions? (3) Does the change contradict any other spec section? (4) Does it violate the project principles? (5) Is there a strictly smaller change that resolves the same problem? " +
              "Return drop when the change is unnecessary or rests on a false premise, revise with a concrete revision when the need is real but the change is wrong or oversized, and keep only when it survives all five questions." +
              promptFor("challenge"),
            {
              schema: CHALLENGE,
              label: "challenge:" + c.id,
              phase: "Challenge",
            },
          ).then((v) => ({ change: c, ...v })),
      ),
    )
  ).filter(Boolean);

  const kept = [];
  for (const r of challenged) {
    if (r.verdict === "drop")
      droppedChanges.push({
        id: r.change.id,
        title: r.change.title,
        reasons: r.reasons,
        evidence: r.evidence,
      });
    else if (r.verdict === "revise")
      kept.push({
        ...r.change,
        sketch: r.revision || r.change.sketch,
        challengeNotes: r.reasons,
      });
    else kept.push(r.change);
  }
  keptTitles = kept.map((c) => c.title);
  log(
    "Challenge: " +
      kept.length +
      " changes kept, " +
      droppedChanges.length +
      " dropped",
  );
  if (kept.length === 0) {
    return {
      mode,
      status: "no-change-needed",
      path: P.dir,
      dropped: droppedChanges,
      validation,
    };
  }

  // ---- Write -------------------------------------------------------------
  //
  // Six files rather than one. The split is not cosmetic: the review loop that
  // follows converges the spec staging first and the rest after, and it can
  // only do that if the two are separable.

  phase("Write");
  await robustAgent(
    "Fill in a change proposal's files from a consolidated, challenged draft.\n\n" +
      "HARD CONSTRAINT: the only files you may edit are the six named below, all inside " + P.dir +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/. This proposal STAGES its " +
      "changes as fenced blocks and precise change descriptions; it never applies them.\n\n" +
      "Read the skeletons first; they already carry the headings, and " + P.problem + " already carries " +
      "the validated problem. Do not restructure them.\n\n" +
      "THE DRAFT (apply each challenge revision in the sketch verbatim):\n" +
      JSON.stringify({ ...draft, changes: kept }, null, 2) +
      "\n\nDROPPED ALTERNATIVES, which go under `## Non-goals` in the summary with their reasons:\n" +
      JSON.stringify(droppedChanges, null, 2) +
      "\n\nDate: " + date + "\n\n" +
      "WHAT GOES WHERE.\n\n" +
      P.summary + " — " + FORMAT_SUMMARY +
      "  Plus `## Goals` and `## Non-goals`, and `## Deliverable index`: one line per staged deliverable, " +
      "`SPEC-1 — <file it lands in> — <one line>`. The index is the ONLY place a deliverable id resolves, " +
      "and the checklist and both change files cite it, so every id you use anywhere must appear here " +
      "exactly once.\n\n" +
      P.spec + " — the staged SPEC edits and nothing else. Under `## Design (as the spec must state it)`, " +
      "the mechanism as the specification must state it. Under `## Staged edits`, one `### SPEC-n · " +
      "spec/<file> § <section>` per edit, each with an anchor instruction (\"Append after …\", \"Replace the " +
      "row …\") and a fenced block of the exact text to insert. Under `## Edge cases and accepted failure " +
      "modes`, every case the SPEC text owns. Under `## Spec files touched`, the file list.\n" +
      "  A proposal that changes no spec text leaves this file with its headings and no staged edits, which " +
      "is a valid and common outcome; do not invent a spec edit to fill it.\n\n" +
      P.nonSpec + " — everything else staged: `### CODE-n`, `### SCHEMA-n`, `### CHART-n`, `### MIG-n`, " +
      "`### DOCS-n`, each naming its target and giving the exact change. Under `## Testing`, the specific, " +
      "insightful tests to add during implementation: one per behaviour the proposal changes, mapped to the " +
      "tiers the change reaches per .claude/rules/test-coverage.md, each covering the non-happy path it " +
      "needs (empty, error, concurrent, boundary, and spec-named-failure) and carrying a `// spec:` tie. A " +
      'vague "add tests" note is not a Testing section. Under `## Open decisions for review`, only ' +
      "decisions that genuinely belong to the human reviewer.\n\n" +
      P.checklist + " — " + FORMAT_CHECKLIST +
      "  One lane per step: a step names deliverables of ONE lane only, because the lane selects which " +
      "handler the implementation pipeline runs and a step with two has none. The standard pattern is every " +
      "spec step first, in a leading block, then the rest; a step that breaks that order states on its own " +
      "line why the interleave is necessary, and it qualifies only when the spec text cannot be written or " +
      "applied until the earlier step lands.\n\n" +
      P.problem + " — leave the statement and evidence alone. Fill `## Findings this unblocks` with the " +
      "finding ids the input named, or \"none\".\n\n" +
      P.status + " — leave it alone. The status is Draft and the review loop changes it.\n\n" +
      FORMAT_BLANKS +
      "\nProse rules: follow " + repo + "/.claude/rules/doc-style.md (read it first). Read the spec " +
      "sections each staged edit targets so anchors and surrounding text are quoted accurately." +
      promptFor("write"),
    { label: "write", phase: "Write" },
  );
  log("Proposal written to " + P.dir);
} else {
  path = input.proposalPath.startsWith("/")
    ? input.proposalPath
    : repo + "/" + input.proposalPath;
  P = proposalFiles(path, repo);
}


// ---- Bootstrap: bring an existing proposal up to the current layout ----
//
// Two jobs, in order. A legacy single-file proposal is MIGRATED into the
// folder layout, by the migrate-proposal subworkflow, which is the one
// implementation of that split and is invoked identically by
// implement-proposal. Then whatever the split, or an older run, left thin is
// BACKFILLED from what the document already says.
//
// The backfill is not a review round and invents nothing. Where the document
// does not settle something it says so rather than guessing, because a marked
// inference is something the rounds that follow can check and a confident
// guess is not.

if (mode !== "new") {
  phase("Bootstrap");

  if (P.layout === "legacy") {
    log("Legacy single-file proposal; migrating to the folder layout before review");
    const mig = await workflow(
      { scriptPath: repo + "/.claude/workflows/migrate-proposal.js" },
      { proposalPath: path.replace(repo + "/", ""), repoRoot: repo, date },
    );
    if (!mig || (mig.status !== "migrated" && mig.status !== "already")) {
      return {
        mode,
        status: "migration-failed",
        reason:
          "the proposal could not be migrated to the folder layout, so the review loop did not start: " +
          ((mig && (mig.reason || mig.status)) || "the migrator returned nothing"),
        migration: mig || null,
        path,
      };
    }
    path = repo + "/" + (mig.dir || "proposals/" + P.stem);
    P = proposalFiles(path, repo);
    log("Migrated; reviewing " + P.dir);
  }

  await robustAgent(
    "Fill in whatever a change proposal's files are missing, deriving everything from what the document " +
      "already says.\n\n" +
      "HARD CONSTRAINT: the only files you may edit are the ones inside " + P.dir + ". Never modify " +
      "anything under spec/, docs/, pkg/, charts/, or schemas/. This is a STRUCTURAL pass: no decision is " +
      "reopened, no staged change is edited, and no wording elsewhere is improved.\n\n" +
      "FIRST read every file in " + P.dir + " and decide which are missing or carry only their headings. " +
      "If all of them have content, change NOTHING and reply SKIPPED.\n\n" +
      "For each that is empty or absent, derive it.\n\n" +
      P.summary + " — " + FORMAT_SUMMARY +
      "  Plus `## Goals`, `## Non-goals`, and `## Deliverable index`. Its top-level changes are the staged " +
      "deliverables grouped by what they accomplish rather than listed one by one. Its fixed decisions are " +
      "the proposal's own decisions reduced to the line an implementor needs, stripped of the reasoning. " +
      "Its watch-outs come from the recorded limits, the open questions, the accepted failure modes, and " +
      "the review history: a trap the loop already fell into is exactly what an implementor needs warning " +
      "about. The deliverable index lists every staged deliverable id with the file it lands in and one " +
      "line; if the document uses no ids, assign them now (SPEC-1, CODE-1, TEST-1) and use the same ids " +
      "everywhere.\n\n" +
      P.checklist + " — " + FORMAT_CHECKLIST +
      "  Derive it from the staged deliverables and the dependencies the document states between them. Read " +
      "the staged changes and the files-touched list together: a deliverable that edits a file another " +
      "creates depends on it, and a code deliverable that consumes a specification statement depends on the " +
      "deliverable that states it. Where the document already records an application order, follow it " +
      "rather than deriving your own. One lane per step.\n" +
      "  WHERE THE DOCUMENT DOES NOT SETTLE AN ORDER, put the step where it seems to belong and note on its " +
      "line that the order is inferred. The review rounds that follow will check a marked inference and " +
      "cannot check a confident guess.\n\n" +
      P.status + " — frontmatter whose `status:` is the state the proposal is actually in, read with " +
      "`node " + repo + "/.claude/tools/proposal-status.mjs " + P.root + " --field status`. Do not invent " +
      "a state.\n\n" +
      P.log + " — the three headings `## Standing context`, `## Ledger`, and `## Retired`, with any " +
      "existing adversarial-review history placed under `## Retired`.\n\n" +
      P.deviations + " — the heading and the note that the implementor owns it.\n\n" +
      FORMAT_BLANKS +
      promptFor("bootstrap") +
      "\nFollow " + repo + "/.claude/rules/doc-style.md.",
    { label: "bootstrap", phase: "Bootstrap" },
  );
}

// ---- Conventions pass (shared, one-shot, outside the error loop) ----

phase("Conventions");
await robustAgent(
  "Check a proposal's files against the written conventions and fix only violations.\n\n" +
    "HARD CONSTRAINT: the only files you may edit are the ones inside " +
    P.dir +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "The written rules: section structure and citation formats per the exemplar " +
    exemplar +
    " (read it first), and prose per " +
    repo +
    "/.claude/rules/doc-style.md (read it first). " +
    "Fix structural deviations and doc-style violations (fragments, missing list conjunctions, decorative em-dashes, marketing language). Do not change technical content, citations, or design decisions. If the files already conform, change nothing and say so." +
    promptFor("conventions"),
  { label: "conventions" },
);

// ---- Review loop (shared): multi-lens review, two-skeptic verify, fix ----

const CONTEXT =
  "Repository: " +
  repo +
  " (your working directory). The technical spec lives in spec/ (files are large; use Grep and targeted Read offsets, never read a whole spec file), implementation in pkg/ and cmd/, Helm chart in charts/, operator docs in docs/.\n" +
  "The proposal under review: " +
  path +
  " (read it fully first).\n" +
  "Standing reference points (re-verify anything you rely on; line numbers drift):\n" +
  "- spec/04 section 4.6.3: the CRD field-ownership table (which controller owns which spec/status subresource) and the controller RBAC paragraph. Every condition or status write in a proposal must match it.\n" +
  "- spec/10 section 10.3 and spec/13 section 13.2: agent pods have zero RBAC and no apiserver path; NetworkPolicy egress tables; admission webhooks are purely in-process validators.\n" +
  "- spec/16: metric and alert inventories; the alert catalog is single-sourced in pkg/alerting/rules and rendered by make generate (check file headers for generation notes); docs/reference/metrics.md and docs/runbooks/ mirror them, and tests/tier11_docs enforces alert-to-runbook resolution.\n" +
  "- spec/18: phased build sequence; a phase deliverable cannot depend on artifacts of a later phase.\n" +
  "Notes from the orchestrator (leads, not evidence):\n" +
  context +
  "\n" +
  EVIDENCE;

const BAR =
  "REPORT ONLY REAL ERRORS. A finding qualifies only if at least one of these holds:\n" +
  "(a) A citation in the proposal is false: the cited file, line, or section does not say what the proposal claims, or the proposal attributes behavior to the wrong component.\n" +
  "(b) The proposal assigns an actor an action it cannot perform: it violates the section 4.6.3 ownership table or RBAC, the section 13.2 egress posture, the section 10.3 zero-RBAC agent-pod posture, admission-webhook in-process purity, process boundaries between deployment models, or spec/18 build-phase ordering.\n" +
  "(c) The proposal contradicts the current spec, the current code, or itself, such that applying its edits would leave the spec internally inconsistent or the described implementation broken.\n" +
  "(d) The proposal misses an edit site: a spec/, docs/, schemas/, or charts/ surface that would become wrong after the proposed edits are applied and that is absent from the proposal's edit lists. Editing a generated artifact instead of its authoring source counts.\n" +
  "(e) A described mechanism cannot work: race conditions, bypassable mandatory gates, unreachable trigger states, wrong defaults, mismatched granularity, predicate drift between sections, or ordering problems.\n" +
  "(f) The proposal changes behavior but does not list the tests that behavior requires: the Testing section is absent, omits a tier the change plainly reaches, names no concrete test for a behavior the proposal changes, or lists only a happy-path test where the change introduces an error, concurrent, boundary, security or fail-closed, or spec-named-failure path (see .claude/rules/test-coverage.md). A proposal must list the specific, insightful, relevant new tests to add during implementation.\n\n" +
  "A PROPERLY MARKED BLANK IS NOT A FINDING. A proposal may delegate a detail to the implementor with an explicit \"IMPLEMENTOR'S CHOICE:\" marker that names what is open AND the constraint any answer must satisfy. Do not report such a marker as an underspecified target, a missing edit site, or an unresolvable anchor: it is the format working as intended. Three things about a blank ARE findings, and you should report them. A marker with no constraint, because that delegates without bounding. A blank over something the format bars from delegation, which is a wire contract or field name, a security or fail-closed predicate, which component performs an action, an ordering another step depends on, a name appearing in more than one place, or anything a test must assert. And a gap that is left unmarked, which is the ordinary underspecified-target finding and is unaffected by this rule.\n\n" +
  "DO NOT report: style or wording, documentation polish, optional improvements, additional nice-to-have tests beyond the coverage the change requires, hypothetical hardening, redundancy, preferences between workable designs, or anything whose absence does not make the applied spec or implementation wrong. If you are unsure whether something meets the bar, do not report it. An empty findings list is a fully acceptable answer and is the expected answer for a converged proposal.\n\n" +
  'The proposal\'s "Resolved in adversarial review" section is a historical record of earlier passes; its descriptions of earlier drafts are not findings. Sections recording deliberately open decisions for the human reviewer are not findings.\n\n' +
  "Every finding MUST carry evidence: exact file paths with line numbers and short quotes for both the proposal's claim and the contradicting source. Read the files to verify line numbers; never cite from memory.\n\n" +
  "BE EXHAUSTIVE IN THIS ONE PASS. Report every finding that meets the bar now, in this single response. This loop retires a lens once it returns nothing, so your lens may not run again before the proposal is certified: a finding you hold back is not caught by a later pass of your own lens, and it costs an entire extra round for every other reviewer. Before returning, walk the proposal section by section and ask, for each section, whether your lens has anything on it; do not stop at the first finding or at the most severe one, and do not withhold a substantiated finding because the proposal reads as polished elsewhere or because you have already reported several. There is no cap on how many findings you may return.\n\n" +
  "Exhaustiveness does NOT relax the bar. Each finding still costs two verification agents, and one that fails verification wastes them and pollutes the refuted list, so a speculative finding is worse than no finding. The target is: everything that meets the bar, nothing that does not.";

const LENSES = [
  {
    key: "citations",
    text: 'Lens: citation audit. Extract every concrete citation in the proposal (file paths with line numbers, spec section references, quoted spec text, attributed behaviors such as "section X assigns Y to Z" or "function F does G"). Verify each one against the actual file content at the cited location. A citation whose target says something materially different, attributes the behavior to a different component, or does not exist is a finding. Off-by-a-few line drift on an otherwise accurate claim is NOT a finding unless the drift changes the meaning. Check data-flow directions (which side of a mirror is authoritative) in the reconciler code itself.',
  },
  {
    key: "feasibility",
    text: "Lens: actor-action feasibility. For every action the proposal assigns to a component, verify the component exists under that name and can perform the action under the spec: the section 4.6.3 ownership table and RBAC paragraph, the section 13.2 NetworkPolicy egress rules, the section 10.3 agent-pod posture, webhook purity (pure-decision packages that cannot resolve cross-resource references), process boundaries between deployment models, and spec/18 phase ordering (a deliverable in phase N must not require artifacts that first exist in a later phase). Also verify each layer can see the data its check needs. Any assignment the actor cannot fulfil is a finding.",
  },
  {
    key: "edit-sites",
    text: "Lens: edit-site completeness. Enumerate every identifier the proposal adds, changes, or removes (field names, flag names, condition types, metric names, alert names, error strings, Helm value names, yaml keys). Grep spec/, docs/, schemas/, and charts/ for each one and for the concepts they replace. Any surface that becomes incorrect or internally inconsistent after the proposed edits are applied, and that is missing from the proposal's edit lists, is a finding. Check authored-vs-generated chains (file headers note generation; the proposal must edit the source). Check companion pairs: classification tables with their prose lists, metrics with their spec/16 and docs/reference/metrics.md inventory rows, alerts with their docs/runbooks/ pages, operator-guide sentences describing behavior the proposal supersedes.",
  },
  {
    key: "mechanism",
    text: "Lens: end-to-end mechanism. Trace each flow the proposal describes from origin to final effect and hunt for: states where declared config and running pods diverge (revert and rollout races), mandatory gates that one write path bypasses (fields settable through a surface the gate never inspects, such as a directly applied custom resource), triggers that can never fire, defaults that contradict stated behavior, checks at layers that cannot see the data they need, granularity mismatches between a setting and what it controls, and predicate drift (the same trigger condition stated with different conjuncts in different sections, summary tables, constant comments, proposed spec text, and tests). Also verify the proposal's quoted replacement spec text is itself consistent with the rest of the spec it will be embedded in.",
  },
  {
    key: "security",
    text: "Lens: security. Always run. Two checks. (1) Regression of an established control — the section 10.3 zero-RBAC and no-apiserver agent-pod posture, section 13.2 default-deny networking, section 13.1 pod security, fail-closed admission, two-ServiceAccount label-immutability splits, tenant pinning, one-session-only pod reuse, mandatory acknowledgment gates, and audit/alert surfacing of degraded states; a change that silently removes, bypasses, or feature-gates a mandatory control is a finding. (2) Trust boundary and durability of a security bound — for every value that bounds a security property (residual-state limits such as maxTasksPerPod/maxScrubFailures, quota/budget ceilings, isolation or reuse counters), verify the AUTHORITATIVE source is a trusted component, not an in-pod or otherwise attacker-influenceable self-report: the spec's proxy-mode posture (the gateway measures independently and ignores the pod's self-report in multi-tenant deployments) is the bar, and a bound sourced from a pod self-report, or one that can silently RESET or relax on a store outage because it lacks the durable fallback section 12.4 requires of every Redis-backed role, is a finding. Merely less strict than it could be is NOT a finding.",
  },
  {
    key: "kubernetes",
    text: "Lens: Kubernetes-idiom soundness. Always run. Judge the design against controller-runtime and API conventions: single-writer field-manager ownership of any status subresource (no ForceOwnership or two managers racing one field); status is controller-observed state, never a gateway-to-controller command channel (a non-owning component writing another component's status, or a status field used as an RPC inbox, is an anti-pattern) while spec is owner-written desired state; finalizer correctness (no stuck-finalizer footgun where a wedged controller makes objects undeletable); CRD-as-coordination versus source-of-truth (etcd is not a message bus or a per-request database); admission-webhook coherence (the webhook must have a real object to admit and remain the correctness gate, not a status field); level-triggered reconciliation off owned watches; and controller-on-the-hot-path (a synchronous request path must not block on a controller reconcile, a work-queue, or leader election). A design that fights one of these idioms is a finding; name the idiom and the concrete consequence.",
  },
  {
    key: "performance",
    text: "Lens: performance, scalability, and failure-mode reliability at the top capacity tier. Always run. Quantify the control-plane and data-plane write rates the proposal creates at the largest documented tier (per-task, per-request, or per-session writes multiplied by that tier's object and request counts) against the budgets the spec states (the etcd status-write dedup ceiling, the Postgres and Redis tier sizing, connection-pool limits). Hunt for per-unit-of-work write amplification onto etcd, single-leader or single-key serialization bottlenecks, hot keys, informer-cache memory for net-new watches, and reconcile or work-queue pressure; state the math. Then test failure-mode reliability: trace what survives and what stalls during a Postgres failover, a Redis reset, and a gateway coordinator handoff, and confirm the binding, occupancy, running work, and any security-bounding counter survive or degrade no worse than the shipped design and that every store-backed value the design relies on has a durable fallback (section 12.4). A new bottleneck at the top tier, or a failure mode less reliable than the shipped behavior, is a finding.",
  },
  {
    key: "reliability",
    text: "Lens: reliability and fault tolerance. Always run. Judge whether the recovery and retry mechanisms the proposal relies on are correct under crash, restart, and store failover. This lens owns recovery-mechanism correctness; the performance lens owns the capacity and state-survival math, the security lens owns fail-closed on security paths and the trust boundary of security-bounding values, and the Kubernetes lens owns finalizer and level-triggered reconciliation idioms, so do not re-file their findings here. Standing reference points (re-verify; lines drift): spec/12 section 12.4 Redis failure-behavior table (every Redis-backed role — leases, quota and slot counters, routing and token cache, circuit breakers, delegation budgets — has a durable fallback or reconstruction path, and each fails open with a bounded window or fails closed per that table); spec/10 section 10.1 coordinator handoff (the single-writer Redis lease with TTL, the coordination_generation CAS that fences a stale coordinator, the CoordinatorFence acknowledgement that is a hard precondition before any operational RPC, coordinatorHoldTimeoutSeconds, and the preStop drain) and its resume deduplication that skips any tool call with tool_call_sequence_number <= last_tool_call_id; spec/07 section 7.2 inbox drain to the DLQ on terminal and resume_pending transitions and the delivered_message_ids suppression of duplicate delivery; spec/04 section 4.6.3 WarmPoolController GarbageCollect of orphaned SandboxClaims (leader-only) and the level-triggered mirror that re-derives agent_pod_state from authoritative etcd after any controller outage; the storage invariant that Postgres is authoritative, Redis is ephemeral, and an in-memory counter is a cache. Trace every operation the proposal adds or changes through redelivery and restart and hunt for: a retried or replayed operation that is not idempotent (a task or inbox delivery, a lease claim, a status or checkpoint write, a pool replenish) and lacks a dedup, fencing, or soft-delete (deleted_at IS NULL) guard; at-least-once delivery whose consumer has no dedup key; a recovery path that abandons a resource with no reclaimer (an orphaned warm or claimed pod, a leaked lease, a sandbox or session whose coordinator died, a wedged finalizer); a retry without bounded, jittered backoff that can stampede a recovering store or the apiserver; an outbound RPC or wait with no timeout or deadline so one hung dependency stalls the path; a drain, graceful-shutdown, or node-termination path that drops running work, acknowledged-but-unfinished tasks, or a deletion guard; and a fail-open recovery (spec/12 section 12.4) that re-enables a path which then acts on stale or un-reconciled state instead of running the stated reconciliation (the quota and delegation-budget MAX(postgres_checkpoint, in_memory) rule) before resuming. A recovery mechanism that loses, double-applies, leaks, stampedes, or stalls under the exact failure it exists to handle is a finding; a design that is merely slower to recover than an alternative, or an absent SLO percentile (the section 16.5 burn-rate thresholds are operationally tunable rather than spec invariants), is not.",
  },
  {
    key: "client-surface",
    text: "Lens: client-facing surface integrity. Always run. Identify every externally-consumed contract the proposal adds, changes, or removes, and verify the change is intentional and complete across all of its parallel representations. The client-facing surfaces are the REST API (section 15.1) and its hand-authored OpenAPI document (pkg/gateway/openapi/openapi.json, which the served MCP create_session tool schema and the client SDKs derive from); the MCP and A2A external-protocol surfaces (section 15.2), the lenny/* tool names, and their input schemas; the OpenAI-completions surface; the gateway-to-adapter wire proto (schemas/lenny-adapter.proto) and the JSONL and runtime-ops-events schemas (schemas/*.json) with their tier-3 wire-contract tests; the adapter manifest field set (section 4.7) the runtime reads; the runtime and client SDK type files in every language (sdks/runtime/{go,python,typescript}, sdks/client/{go,python,typescript}); the CRD schemas operators apply (charts/lenny/crds and the pkg/embedded/crds copies); client-visible enums, error codes, and event types clients branch on; and the client-facing docs (docs/api/*, docs/client-guide/*, docs/runtime-author-guide/*). A change to one representation that is not mirrored in its parallels is a finding: a REST field missing from the OpenAPI document, the MCP tool schema, an SDK language, or the docs; a wire, proto, or JSONL change that omits a language SDK or its tier-3 contract test; a removed or renamed client-facing field still advertised by the served schema, an SDK, a CRD, or a doc; an enum or error-code value clients consume changed without every emitter and consumer updated. Also enforce the origin rule: a name an external standard defines (the MCP or A2A Task primitive and the protocol vocabulary clients interact with) must not be renamed, while Lenny-defined client surfaces may change; a rename that breaks the standard-aligned surface, or that leaves one client vocabulary half-renamed across representations, is a finding. The platform is pre-deployment with no backward-compatibility shims, so a deliberate, complete breaking change is not itself a finding; an incomplete or inconsistent client-facing change, or an internal surface changed while a parallel client surface still serves the old contract, is.",
  },
  {
    key: "docs-alignment",
    text: "Lens: documentation alignment. Always run. The docs/ tree is downstream of the spec and the implementation: docs follow the spec and the code and are never the source of truth for a spec or core-product decision. Identify every behavior the proposal changes — a spec edit, a code change, a renamed, removed, or added identifier, a changed default, error code, endpoint, flag, metric, alert, or lifecycle step — and verify it is reflected in a staged docs/ edit wherever docs/ currently describes that behavior, and that the staged docs edits leave docs/ internally consistent and consistent with the post-change spec. The docs surfaces are the concept and guide pages (docs/, docs/api/, docs/client-guide/, docs/runtime-author-guide/), the reference pages (docs/reference/, notably docs/reference/metrics.md), and the docs/runbooks/ pages that tests/tier11_docs resolves (alert-to-runbook slug resolution and examples). A docs/ page left describing superseded behavior, an added alert or metric missing its docs/runbooks or docs/reference companion, or a staged docs edit that contradicts the post-change spec, is a finding. Two categories beyond mirroring a changed behavior are also findings under this lens, because an approved edge case is made of exactly the categories that do not register as a change. First, an edge case or failure mode the proposal ACCEPTS or DEFERS whose observable outcome appears only in the proposal's reasoning (Problem, Detailed design, Non-goals) and in neither the staged spec text nor the docs/ page that owns it — including when adversarial review deferred the mechanism to a later proposal but left the resulting accepted behavior undocumented in the text that lands now. Deferring the fix does not defer documenting the accepted behavior, so the fix is a staged spec and/or doc edit stating the outcome the reader or operator observes, never a request to build the deferred mechanism now. Second, a new operator-facing failure mode, or a new CAUSE of an existing failure or data-loss path, absent from the narrative operator docs (docs/operator-guide/, docs/runbooks/) that enumerate that failure's causes — this is the failure narrative itself (why the failure happens and what an operator observes), distinct from the companion-row check (a metric or alert companion), and it must gain the new cause. Cross-check the proposal's 'Edge cases and accepted failure modes' section against the staged edits: every row must resolve to landing spec or doc text rather than to reasoning alone, and an accepted or deferred failure mode named elsewhere in the proposal but missing from that section is itself a finding. Two hard guardrails on this lens: (1) never raise a finding that asks the spec or the implementation to change to match an existing doc; when a doc and the spec disagree the doc is the defect and is reconciled toward the spec, so a finding here is always a missing or wrong docs edit, never a spec or code edit. (2) A doc-described scenario may be cited as a candidate test case only after that doc has been verified against the spec, never as evidence for what the product should do.",
  },
  {
    key: "applicability",
    text: "Lens: applicability and sequencing. Always run. Every other lens reads the proposal as a document; this lens is the only one that reads it as an executable procedure. Simulate applying the proposal end to end, in the order it states, and report anything that would stop or corrupt that application. Do not evaluate whether a change is correct or worthwhile; evaluate only whether it can be carried out as written.\n\nWork through the staged changes in their stated order and maintain a running model of the tree: which files exist, which headings and anchors exist, which identifiers are defined. For each staged edit, ask whether an implementor with only this proposal and the current tree could apply it without inventing anything. Findings are:\n" +
      "(1) FORWARD REFERENCE. An edit references an artifact that a LATER sub-step of the same proposal creates: a file that does not exist yet, a heading, anchor, section number, identifier, register, rule file, or test that a later sub-step introduces. Applying the proposal in its stated order would fail at this edit. Name the referencing sub-step, the referenced artifact, and the sub-step that creates it.\n" +
      "(2) UNDERSPECIFIED TARGET. An edit's content cannot be written deterministically because the proposal never states something that edit requires.\n" +
      "    Do not hunt for this by reading for suspicious passages. This defect is invisible at the referring site: the staged row, link, or index entry looks complete, and what is missing lives elsewhere in the document or nowhere at all. Build the worklist first, then check every member.\n" +
      "    Step 1, enumerate what the proposal CREATES or RENAMES: files, headings and subsections, identifiers (types, functions, fields, constants, RPCs, flags, metrics), registers and schemas, gates and tests, and directories. Include artifacts created by any sub-step, in any order. Take them from the staged changes, the target lists, and the files-touched section TOGETHER, because an artifact named in only one of those is the likeliest to be underspecified.\n" +
      "    Step 2, for each one, list the properties another edit, gate, or index needs in order to be written against it. A heading needs its exact title and the anchor that title derives to. A file needs its path. An identifier needs its exact spelling and every derived form (file stem, type name, constant, generated artifact, string literal). A register needs its key and entry schema. A gate or test needs its name and where it is registered.\n" +
      "    Step 3, for each property, find where the proposal states it. A property no sub-step states, one left to be 'authored from' surrounding content at application time, or one stated for only SOME members of a set the proposal otherwise treats uniformly, is a finding. Name the artifact, the property, the edit that needs it, and what an implementor would have to invent.\n" +
      "    Also count an anchor instruction that does not identify a unique location in the target file, and an edit that says to update a surface without stating the new value.\n" +
      "    A CORRECTED INSTANCE DOES NOT CLOSE THIS CLASS. When the proposal states these properties for one set of created artifacts (a heading table covering one file, say), that is evidence the class is LIVE and that every other created artifact must be held to the same standard. It is not evidence that the class is handled. Run steps 1 through 3 over every member regardless of how thoroughly a neighbouring case was specified.\n" +
      "(3) RELOCATION THAT LOSES CONTENT. For every edit described as a move, relocation, carve-out, reduction, or supersession, verify BOTH legs are staged: the source's removal AND the destination's full replacement text. A reduction that deletes a table, tool list, schema, or rule set whose text appears nowhere in the destination staging is content loss rather than relocation, and it is a finding even when the proposal calls it a move. Also check that the destination text carries every element the source held, and that anything still pointing at the source is redirected.\n" +
      "(4) ORDERING AND GATE STATE. An edit whose sub-step ordering contradicts its dependencies, a step that leaves the tree in a state where an EXISTING gate hard-fails with no recorded disposition (a schema breaking-change check against a baseline ref, a lint, a no-drift test, a citation ratchet, a coverage floor), or a proposal that adds a gate which its own staged text would fail. State the gate, the command or test that runs it, and why it fails.\n" +
      "(5) UNRESOLVABLE ANCHOR. An anchor instruction quoting surrounding text that does not match the current file, or that matches in more than one place so the edit site is ambiguous.\n\n" +
      "(6) A CODE STEP WRITTEN AGAINST SPEC TEXT THAT HAS NOT LANDED. The pipeline runs ONE sequence, the " +
      "implementation checklist, and dispatches each step on its lane: a spec step applies the staged " +
      "specification text its line names, a code step builds. So spec and code may interleave, and an " +
      "interleave is not itself a defect.\n" +
      "    What IS a defect is a step that consumes a specification statement whose own step has not run. " +
      "For every code step, ask which spec statements its work implements, find the deliverables that " +
      "stage them, and check that the steps carrying those deliverables are named in its Depends-on and " +
      "come before it. A code step implementing a statement staged by a LATER step is a finding whatever " +
      "the lanes' relative order, because it is written against text that is not in the tree yet.\n" +
      "    This replaces a rule that forbade the interleave outright. It is the same guarantee stated " +
      "more precisely: the old rule protected code from being written against unlanded spec text by " +
      "landing ALL of it first, which said nothing about whether the RIGHT statement had landed.\n\n" +
      "Method: read the proposal's staged-changes section in full and in order, then open the actual target files to confirm each anchor and each referenced artifact. Build the existence model as you go; a forward reference is only visible if you track what each sub-step creates. That model and class 2's step-1 worklist are the same enumeration read two ways, so build it once: for each created artifact ask both WHEN it exists relative to the edits that reference it (class 1) and WHETHER every property those edits need is stated (class 2). Do not report an edit as unappliable because you would have written it differently, and do not report ordinary implementation judgment (choosing a variable name, formatting a table) as underspecification. The test is whether a competent implementor would be forced to guess at something the proposal was responsible for stating." +
      "\n(CHECKLIST) THE IMPLEMENTATION CHECKLIST IS THE ONE EXECUTION SEQUENCE, so this lens owns it. Read it against the staged deliverables and report: a staged deliverable that appears in no step; a deliverable named by two steps; a step naming a deliverable the proposal does not stage; a Depends-on that names a later step or a step that does not exist; and a step whose tier list omits a tier its deliverable plainly reaches.\n" +
      "    LANES. Each step carries exactly ONE lane and names deliverables of that lane only. A step naming both a spec deliverable and a non-spec one is a finding: the lane selects which handler the pipeline runs for that step, and a step with two lanes has no handler.\n" +
      "    ORDER. The standard pattern is every spec step first, in a leading block, then the rest. Report any step that breaks that order WITHOUT stating on its own line why the interleave is necessary. Then judge the stated reason: it qualifies only when the specification text cannot be written or applied until the earlier step lands, which means the staged edit is the output of a tool this proposal builds, or its content depends on a fact only the built artifact fixes. Efficiency, convenience, and a preference for building before writing do not qualify, and a step claiming one of those is a finding.\n" +
      "    Simulate the checklist as the order of execution: if running the steps in their stated order would hit a forward reference that another order would not, the checklist is the defect rather than the edit.\n" +
      "A checked box is a finding. The proposal is not implemented, so every box is unchecked until the implementation pipeline ticks it.",
  },
  {
    key: "test-coverage",
    text: "Lens: test coverage. Always run. A proposal must list the specific, insightful, relevant new tests to add during implementation for the behavior it changes, not a vague 'add tests' note. Read the proposal's Testing section against .claude/rules/test-coverage.md and .claude/rules/spec-driven-development.md. For every behavior the staged changes add or change (a new field, default, error code, endpoint, flag, condition, metric, alert, lifecycle step, sequence or ordering rule, security or isolation control, or recovery/failover path), verify the Testing section names a concrete test that pins that behavior, mapped to the tier(s) the change actually reaches: tier 1 pure logic; tier 2 a controller or anything reading or writing the kube-apiserver; tier 3 a wire contract (proto, JSONL, HTTP, CRD schema); tier 4 a multi-service flow; tier 5 a cluster behavior (pod lifecycle, NetworkPolicy, admission, mTLS, drain); tier 7 concurrency, ordering, atomicity, or rate; tier 8 a failure or recovery path; tier 9 auth, isolation, egress, or credential handling; tier 10 a runtime-adapter contract; tier 11 docs, alerts, or runbooks. The listed tests must cover the non-happy-path the spec names (empty, error, concurrent, boundary, and spec-named-failure), not the happy path alone, and each should carry a // spec: tie to the section it exercises. A finding is: no Testing section; a Testing section that omits a tier the change plainly reaches; a behavior the proposal changes with no listed test; a listed test that exercises only the happy path where the change introduces an obvious error, concurrent, boundary, or spec-named-failure path (for a security, isolation, or fail-closed change, no test asserting the deny/fail-closed path; for a recovery, idempotency, or dedup change, no test asserting the replay/crash/failover path); or a Testing section so vague it names no concrete test. Do NOT report additional nice-to-have tests beyond the coverage the changed behavior requires, a preference between equivalent test framings, or an absent coverage percentage. This lens owns test-listing adequacy; do not re-file docs, edit-site, or mechanism findings here.",
  },
];

const EXTRAS = [
  {
    key: "operational",
    text: "Lens: operational consistency. Check that conditions, metrics, alerts, and operator documentation the proposal touches stay mutually consistent after application: every alert references an emitted metric that a spec-defined evaluation surface can collect, every condition has exactly one writer consistent with section 4.6.3, condition semantics match what operators are told in docs/, and the spec's observability inventories agree with what the proposal says exists. An inconsistency that would mislead an operator about the system's actual state is a finding.",
  },
  {
    key: "fresh",
    text: "Lens: fresh holistic read. Read the proposal as the spec maintainer who must apply its staged edits verbatim tomorrow. Independently spot-check the assumptions the other lenses might share blind spots on, in whatever order seems most suspicious to you. Anything that would make the applied spec wrong, internally inconsistent, or unimplementable is a finding.",
  },
];

// plan-conformance is defined separately because its prompt embeds the plan path
// and it joins the pool only when the caller supplied one. It is the only lens
// that measures the proposal against a document rather than against the tree,
// which is exactly the blind spot it exists to close: a proposal can be perfectly
// self-consistent and perfectly accurate about the code while silently dropping
// half of what the plan asked it to deliver, and no tree-facing lens can see that.
//
// The lens carries a deliberate escape valve. A plan is a design document written
// earlier than the proposal, so some of its instructions will be stale, refuted by
// the tree, or simply wrong. Without an escape valve such an instruction produces a
// finding the fixer cannot satisfy: it cannot edit the plan (the loop's hard
// constraint is proposal-only), and staging a deliverable the tree contradicts
// would introduce a defect the other lenses would then report, so the loop would
// oscillate or stall. The valve is that EVERY finding under this lens has two
// acceptable resolutions, staging the deliverable or recording a reasoned
// divergence, and a recorded divergence closes the finding permanently. That keeps
// every finding actionable in one edit and makes the lens terminate.
const PLAN_LENS = {
  key: "plan-conformance",
  text:
    "Lens: plan conformance. This proposal implements one or more steps of the plan at " +
    planPath +
    ". Your job is to find deliverables that plan assigns to the steps this proposal claims, which the proposal neither stages nor consciously declines. This is the one lens that reads a document outside the current tree, and the one blind spot no other lens covers: every other reviewer checks the proposal against the repository, so a deliverable the plan required and the proposal simply never mentions is invisible to all of them.\n\n" +
    "REPORT ABSENCE, NEVER INCOMPLETENESS. This is the rule that keeps the lens useful, and it follows from what makes it unique. A deliverable the proposal never mentions is invisible to every other reviewer, because there is nothing in the text for them to check against the repository. A deliverable the proposal DOES stage is the opposite case: the tree-facing lenses measure it against the actual code, schemas, docs, and tests, which is a better standard than a plan written earlier. So confine yourself to the first case. A finding is a deliverable the proposal stages NOTHING at all for. When the proposal stages the deliverable and it is thin, wrong, narrower than the plan describes, or missing a part, that is not yours: the lenses that read the repository own it. Ask whether the thing is missing, and never whether the thing is complete.\n\n" +
    "GRAIN. A deliverable is anything the plan requires to EXIST in the delivered system once the step lands, and that a reader could ask about on its own and get a yes-or-no answer. It covers prose and code alike: a document or a section of one; a file, package, or module; a named interface, type, function, method, endpoint, or message; a field or parameter the plan requires by name; a mechanism, code path, or behavior; a data artifact such as a schema, register, migration, or fixture; a gate, check, lint, or test suite; a tool or script; and a decision the plan requires to be recorded somewhere durable.\n\n" +
    "Two things are NOT deliverables, and they are where this lens goes wrong. First, a requirement about HOW THE WORK IS CARRIED OUT rather than about what exists once it is done: an exclusivity or freeze constraint, a sequencing or ordering rule, a dry run, a review step, or who performs the change. None of that is observable in the delivered system, so its absence cannot be stated as a defect in the repository, and a sequencing requirement whose absence would actually break the change belongs to the applicability lens instead. Second, the PHRASING of text that already exists and already carries the meaning the plan requires. Rewording is not a deliverable.\n\n" +
    "Method. First read the proposal to determine exactly which plan steps it claims to implement, and treat that claim as the scope boundary. Then read those steps in the plan, plus any plan-wide invariants, gates, or rules the plan states apply to every step, and enumerate the deliverables at the grain above. For each one, search the proposal for it. Search by the deliverable's own identifiers rather than by the plan's phrasing, because the proposal may name the same thing differently, and a deliverable found under another name is staged rather than missing.\n\n" +
    "A finding is a deliverable the plan assigns to a claimed step where the proposal does BOTH of the following: it stages nothing that produces the deliverable, and it records no decision to omit or defer it. Weight a deliverable the plan itself flags as having no other owner most heavily, since nothing else will supply it.\n\n" +
    "STATE THE CONSEQUENCE IN THE REPOSITORY. Every finding must say what will be absent or wrong in the repository after this proposal lands, in terms that do not mention the plan. The plan is where you found the gap; it is not the reason the gap matters. Also give the plan location that assigns the deliverable and the identifiers you searched the proposal for, so the gap is checkable. If you cannot state a consequence in the repository, the deliverable is below the grain above and you must not report it. This is a genuine filter rather than a formatting rule: a requirement about how the work is performed, and a difference in wording, both fail it, which is why neither is a finding.\n\n" +
    "ONE EXCEPTION to the absence rule, and only one: an IDENTITY MISMATCH on a staged deliverable. When the plan fixes an order, a numbering, a name, or a citable handle that its later steps or its own worked examples depend on, and the proposal fixes a different one without saying so, report it. This is not incompleteness. It is a conflict between two documents about what a thing is called, and no tree-facing lens can see it, because the plan is the other party to the conflict and those lenses do not read it. The consequence in the repository is concrete and satisfies the rule above: every citation written from either document resolves to the wrong target. Verify the plan's own worked examples still resolve against the proposal's version. Outside this one case, a staged deliverable is not yours.\n\n" +
    "HOW A FINDING IS RESOLVED, and the hard limits on this lens. Every finding you raise has exactly two acceptable resolutions, and both are edits to the proposal alone:\n" +
    "(a) the proposal stages the missing deliverable, or\n" +
    "(b) the proposal records an explicit, reasoned divergence from the plan for it.\n" +
    "You do not get to choose which. State the gap and let the author choose.\n\n" +
    "Four limits follow from that, and breaking any of them makes this lens a source of unresolvable findings:\n" +
    "1. A DIVERGENCE ALREADY RECORDED IS NOT A FINDING. When the proposal states that it departs from the plan on a point and gives a reason, that point is closed, EVEN IF YOU DISAGREE WITH THE REASON. This lens checks that the decision was made and written down, and never that it was decided your way. A recorded divergence you find unpersuasive is a matter for the human reviewer, so do not re-file it as a conformance gap in any round, under any phrasing.\n" +
    "2. THE PLAN IS NOT AUTHORITATIVE OVER THE TREE. The spec and the code are the source of truth; the plan is an earlier design document and parts of it will be stale or wrong. When a plan instruction is contradicted by the current tree, or would introduce a defect another lens would rightly report, the gap is that the proposal has not RECORDED the divergence, and resolution (b) is the only correct one. Never raise a finding whose only resolution is to change the plan, and never ask the proposal to stage something the tree shows is wrong. Say plainly that the plan appears stale on the point and that the proposal should record why it departs.\n" +
    "3. STAY INSIDE THE CLAIMED STEPS. A deliverable the plan assigns to a step this proposal does not claim is out of scope and is not a finding, however important it looks. Deferred work belongs to the step that owns it.\n" +
    "4. HOLD THE GRAIN. A different but equivalent mechanism that delivers what the plan asked for is conformant, as is a different level of detail, a different internal ordering of the proposal, and different wording throughout. A count, a line budget, or a measured population stated in the plan's prose is a scale indicator rather than a deliverable, so a divergence in a number is not a finding unless a gate or a citation actually keys off it. When you are unsure whether something is a deliverable or a detail of one, apply the consequence test above: if you cannot say what will be absent or wrong in the repository, do not report it.\n\n" +
    "Before reporting, state in the coverage field: the plan steps the proposal claims, and the deliverables you enumerated for those steps at the grain above, each marked staged, consciously declined, or missing. This makes the scope you actually examined reviewable rather than implicit, and it is a check on yourself: a detail looks obviously out of place in a list of deliverables, so writing the list is how you catch a finding that has drifted below the grain before you report it.",
};

// Resolve the caller's lens selections against the real pool. An unknown key is a
// hard error rather than a silent no-op: a typo in excludeLenses would otherwise
// quietly leave the lens running, and a typo in startLenses would quietly widen
// the first round, in both cases producing a run that did not do what the caller
// asked while reporting success.
// plan-conformance is a valid key whether or not a plan was supplied, so naming it
// in excludeLenses is never a typo error. Selecting it in startLenses without a
// plan IS an error, checked below: the caller asked to lead with a lens that has
// nothing to read.
const ALL_LENS_KEYS = LENSES.concat(EXTRAS)
  .map((l) => l.key)
  .concat([PLAN_LENS.key]);
for (const [argName, keys] of [
  ["startLenses", startLensKeys || []],
  ["excludeLenses", excludeLensKeys],
]) {
  for (const k of keys) {
    if (!ALL_LENS_KEYS.includes(k)) {
      throw new Error(
        "args." +
          argName +
          ' names an unknown lens "' +
          k +
          '". Valid keys: ' +
          ALL_LENS_KEYS.join(", "),
      );
    }
  }
}
const excludeSet = new Set(excludeLensKeys);
const startSet = startLensKeys ? new Set(startLensKeys) : null;

// POOL_* are the lens pools this run actually uses. Every later reference goes
// through them rather than through LENSES/EXTRAS, so an excluded lens is absent
// from normal rounds AND from the sweep, and cannot silently certify its domain.
if (!planPath && startSet && startSet.has(PLAN_LENS.key)) {
  throw new Error(
    "args.startLenses selects plan-conformance but args.planPath is not set; that lens has no plan to read",
  );
}
const POOL_FIXED = LENSES.concat(
  planPath && !excludeSet.has(PLAN_LENS.key) ? [PLAN_LENS] : [],
).filter((l) => !excludeSet.has(l.key));
if (planPath) {
  log(
    excludeSet.has(PLAN_LENS.key)
      ? "Plan supplied but plan-conformance is excluded; no lens will check the proposal against " +
          planPath
      : "Plan-conformance enabled against " + planPath,
  );
}
// EXTRAS used to be a SECOND pool with its own scheduling: while any ordinary
// lens was still active, exactly one extra rotated in per round, chosen by
// `(round - 1) % activeExtras.length`. That is a second mechanism for the job
// retirement already does -- withhold a lens that is not earning its keep -- and
// the worse of the two, because it withholds on a round number rather than on
// evidence.
//
// It also manufactured a lens that had NEVER RUN, and a lens that has never run
// cannot retire, and a lens that has not retired blocks the sweep. So a clean
// proposal was forced to spend an entire round discharging one lens: a measured
// run's non-spec loop went thirteen lenses, then `fresh` alone, then a
// fourteen-lens sweep. That singleton round still pays a snapshot, a dedup, the
// verifiers and a round boundary, and can trigger a compaction pass that has
// measured sixteen minutes. The tokens were never the cost; the serialised
// round was.
//
// Every lens is now scheduled the same way, so every lens can retire from round
// one and the case cannot arise.
const POOL_EXTRAS = EXTRAS.filter((l) => !excludeSet.has(l.key));
if (POOL_FIXED.length === 0 && POOL_EXTRAS.length === 0) {
  throw new Error("args.excludeLenses excludes every lens; nothing would review");
}
const POOL = POOL_FIXED.concat(POOL_EXTRAS);
if (excludeSet.size > 0) {
  log(
    "Excluding " +
      [...excludeSet].join(", ") +
      " for this run; convergence will certify nothing about those domains",
  );
}
if (startSet) {
  const startable = [...startSet].filter((k) => !excludeSet.has(k));
  if (startable.length === 0) {
    throw new Error(
      "args.startLenses names only lenses that args.excludeLenses removes",
    );
  }
  log(
    "Starting with " +
      startable.join(", ") +
      "; every other lens begins retired and first reads the proposal in the sweep",
  );
}


// ---- What actually changed, rather than what the fixer says changed ----
//
// The post-fix reviewer's highest-yield question is drift: did an edit leave a
// parallel statement stale somewhere it did not touch. It was being asked to
// answer that from the fixer's own summary of its work, which is precisely the
// document that omits an edit the fixer did not notice making. And the next
// round's lenses were told only the TITLES of what was fixed, so a lens
// re-reading a rewritten section had no signal that the text was new, though
// fix-stage text is the least-examined in the proposal.
//
// A `git diff` was suggested to the reviewer as a locator, hedged because the
// proposal is committed only at checkpoints so the diff spans every round since.
// Snapshotting around each edit gives the real per-round diff instead.
// The workflow sandbox exposes only agent, parallel, pipeline, phase, log,
// workflow, budget, and args. There is no `require`, so a script here cannot read
// a file. An earlier version of this section called `require("fs")` inside a
// try/catch and silently produced an empty result, which disabled this diff, the
// bootstrap step, and the growth signal at once without ever failing. Anything
// that touches a file therefore goes through an agent, which has Bash and Read.
//
// Diff text is never relayed through the script. A snapshot is copied to a known
// path and the agent that needs the diff runs `diff` against it itself. Carrying
// a few thousand lines of diff through an agent's return value would cost more
// than the review it feeds, and an agent asked to return that much verbatim
// summarises it instead.
// Namespaced by run tag like the log shards, the run state and the cache,
// and sharing the directory the boundary script writes its own
// cp-snap/<tag>/<loop>-r<N> snapshots into.
const SNAPDIR = repo + "/scratchpad/cp-snap/" + runTag;

async function snapshot(name) {
  // The loop name is the second half of the namespace, because `round`
  // restarts at 1 in the non-spec loop, so `r1-start` otherwise names two
  // different documents inside one run. Both halves were missing and the
  // residue is in the tree: scratchpad/cp-snap holds 0076-run2/ and
  // 0076-run3/ from the boundary script beside a SINGLE flat r1-start …
  // r6-start set, because run3's `rm -rf` took run2's out from under it.
  // A reviewer handed such a path by diffInstruction() diffs this proposal
  // against whatever the other run last copied there.
  const dest = SNAPDIR + "/" + (LOOP ? LOOP.name : "pre") + "-" + name;
  const ok = await robustAgent(
    "Run exactly this command and reply with the single word DONE:\n\n" +
      "rm -rf " + dest + " && mkdir -p " + SNAPDIR + " && cp -r " + P.dir + " " + dest + "\n\n" +
      "Do nothing else. Do not read, summarise, or edit anything.",
    { label: "snap:" + name, model: "haiku" },
  );
  return ok ? dest : null;
}

// A count of changed hunks, for the history record and the churn signal. The
// number is small enough to relay; the diff it summarises is not.
async function diffHunks(snapPath) {
  if (!snapPath) return 0;
  const out = await robustAgent(
    "Run exactly this command and reply with ONLY the number it prints, and no other text:\n\n" +
      "diff -ru '" + snapPath + "' '" + P.dir + "' | grep -c '^@@'\n\n" +
      "`diff` exits non-zero when the files differ and `grep -c` exits non-zero on a zero count; both are " +
      "expected here and neither is an error. If nothing is printed, reply 0.",
    { label: "diffcount", model: "haiku" },
  );
  const m = String(out || "").trim().match(/\d+/);
  return m ? parseInt(m[0], 10) : 0;
}

// How a reviewer is told to find what changed. It gets a path and runs the diff
// itself, so it can widen the context when a hunk is not self-explanatory.
function diffInstruction(snapPath) {
  return (
    "A snapshot of the proposal as it stood before those edits is at " +
    snapPath +
    ". Run `diff -ru " +
    snapPath +
    " " +
    P.dir +
    "` to see exactly what changed. Widen the context with `-U 20` on any hunk whose surroundings matter."
  );
}

function reviewPrompt(lens, round, fixedTitles, rejected, prevSnap) {
  let history = "";
  if (fixedTitles.length > 0) {
    history +=
      "\n\nAlready found and fixed in earlier rounds (the current proposal text reflects these fixes; do not re-litigate them): " +
      fixedTitles.join("; ") +
      ".";
  }
  if (rejected.length > 0) {
    history +=
      "\n\nAlready examined and refuted in earlier rounds (do not re-report these or close variants):\n" +
      rejected
        .map(
          (r) =>
            "- " + r.title + ": refuted by the " + (r.refutedBy || "unknown") +
            " skeptic because " + r.reason,
        )
        .join("\n");
  }
  return (
    "You are an adversarial reviewer in round " +
    round +
    " of an iterative convergence loop for a change proposal.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\n" +
    BAR +
    "\n\n" +
    lens.text +
    LOOP.scopeNote +
    DEVIATIONS_BLOCK() +
    logBlock("review-" + lens.key, round) +
    history +
    (lensPrompt
      ? "\n\nAdditional instruction from the caller of this run. It adds context or " +
        "focus; it does not lower the finding bar above, and it does not make " +
        "something a finding that the bar excludes:\n" +
        lensPrompt
      : "") +
    (prevSnap
      ? "\n\nWHAT CHANGED IN THE PROPOSAL SINCE THE LAST ROUND. " +
        diffInstruction(prevSnap) +
        " Read the changed sections first and hardest. Fix-stage text is the newest and least-examined in the " +
        "document, and this loop's history records that fixers introduce their own errors, so text written a " +
        "round ago deserves more scrutiny than text that has survived many. This is a READING ORDER, not a " +
        "scope limit: your lens still owns the whole proposal, and the defect a rewrite leaves in text nobody " +
        "touched is exactly the drift this loop exists to catch.\n"
      : "") +
    cacheBlock(lens.key, round) +
    "\n\nWork method: read the proposal fully, then investigate the repository with Grep and targeted Reads to verify or refute its claims under your lens. Report your findings via the structured output (empty array if you find nothing that meets the bar)."
  );
}

// A lens that already ran this round over this exact text returns its cached
// answer instead of reviewing again.
//
// The runtime's own journal replays a completed agent on a resume of the SAME
// run. The gap this closes is a FRESH relaunch, which is what actually happens
// after an auth expiry or a script edit, and where a dozen lens agents would
// otherwise re-read a proposal none of them has changed.
//
// The key is (lens, round, content hash), and the hash is what removes the need
// for any cleanup. A hit means the same lens, in the same round, over
// byte-identical text -- exactly and only the crash-resume case. After a fix
// lands the hash changes, every key misses, and the lenses re-read the changed
// text because that is the only thing they can do. An earlier design keyed on
// (lens, round) alone and needed the round's cache deleted in a specific window
// between the fix landing and the state write, which a crash in the wrong place
// defeats; this has no window to get wrong.
function cacheBlock(key, round) {
  const dir = repo + "/scratchpad/cp-cache/" + runTag;
  return (
    "\n\nCACHE. Before anything else, run:\n" +
    "  mkdir -p " + dir + " && H=$(cat " + P.spec + " " + P.nonSpec + " " + P.checklist +
    " 2>/dev/null | md5sum | cut -c1-12) && cat " + dir + "/" + key + "-r" + round +
    "-$H.json 2>/dev/null\n" +
    "If that printed JSON, return exactly it as your structured output and do no other work: it is your " +
    "own answer to this same question over this same text, from a run that was interrupted.\n" +
    "Otherwise do the review, and immediately before you return, write your findings JSON to that same " +
    "path, recomputing $H the same way."
  );
}

function dedupPrompt(findings) {
  return (
    "You merge duplicate review findings. Below is a JSON array of findings from several independent reviewers examining the same proposal. Merge entries that describe the same root error (even if phrased differently or found at different citation points): keep one entry per root error, choose the clearest title, and combine the strongest evidence. Do not drop distinct errors. Do not add new findings. Do not judge validity. Return the merged list.\n\nEach input finding carries a `lens` field naming the reviewer that produced it. Every entry you return MUST carry a `lenses` array holding the lens values of every input finding you merged into it, so a merge of three reviewers' findings returns all three. This is not cosmetic: the loop decides which reviewers keep running from which of their findings survive verification, and an entry returned without its `lenses` array makes that decision impossible.\n\nFindings:\n" +
    JSON.stringify(findings, null, 2)
  );
}

function evidencePrompt(f) {
  return (
    "You are a skeptical evidence verifier. A reviewer claims the following error in the proposal " +
    path +
    ". Independently re-derive it: read the proposal at the claimed location and read every cited source file at the cited lines.\n\nConfirm ONLY if all three hold: (1) the proposal really says what the finding claims it says; (2) the cited sources really say what the finding claims they say; (3) the contradiction or infeasibility actually follows from (1) and (2). If any citation is wrong, a quote is out of context, the proposal already handles the issue elsewhere in its text, or the conclusion does not follow, refute with the specific reason.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

function materialityPrompt(f) {
  return (
    "You are a skeptical materiality judge for review findings on the proposal " +
    path +
    ". Assume the finding's evidence is factually accurate. Decide ONLY whether fixing it is required for correctness: confirm if leaving it unfixed would make the applied spec internally inconsistent, make a stated citation or attribution false, make the described implementation not work, or leave a behavior the proposal changes without the tests that behavior requires (a missing Testing section, an omitted reached tier, a changed behavior with no listed test, or a happy-path-only test where the change introduces an error, concurrent, boundary, security, or spec-named-failure path, per .claude/rules/test-coverage.md). Refute if it is style or wording, documentation polish, an optional improvement or hardening, redundancy, a preference between workable designs, an additional nice-to-have test beyond the coverage the change requires, or anything else whose absence does not make the spec or implementation wrong. Default to refuted when uncertain. You may read " +
    path +
    " for context.\n\nFinding:\n" +
    JSON.stringify(f, null, 2)
  );
}

// ---- F5: what else this fix would falsify ----
//
// Anchored to ONE confirmed finding, and reached outward from the sites that
// finding already names. An earlier design swept the whole proposal for
// repeated statements; that version was refused by the review bar, which
// excludes consistent restatement, and its evidence was falsified by the run's
// own panel. The question here is narrower and decidable: not "what else talks
// about this" but "what else does this fix BREAK".
function expandSitesPrompt(finding, round) {
  return (
    "You are the site-expansion pass for ONE confirmed finding in round " + round + " of the " +
    LOOP.name + " loop.\n\n" +
    "Two independent verifiers already confirmed this finding at the sites it names. Do not re-examine it. " +
    "Answer one question about the rest of the repository:\n\n" +
    "  IF THIS FINDING'S FIX LANDS AS SUGGESTED, WHICH OTHER SITES BECOME WRONG?\n\n" +
    CONTEXT +
    "\n\nYou are a read-only investigator. Do not create, edit, or delete any file, including a log " +
    "shard. Cite evidence as file:line.\n\n" +
    "THE FINDING (JSON):\n" + JSON.stringify(finding, null, 2) + "\n\n" +
    "THE STARTING SET is the finding's `where` and every citation in its `evidence`. Everything you return " +
    "is reached outward from there. You are not surveying the proposal.\n\n" +
    "THE TEST IS BINARY. For each candidate, ask whether landing this fix makes that text wrong, stale, or " +
    "inconsistent. A site that discusses the same subject and STAYS TRUE is not a site. Consistent " +
    "restatement is not a defect and the review bar excludes it: report a site only when the fix makes it " +
    "wrong.\n\n" +
    "TWO METHODS. Use both; neither alone is sufficient.\n" +
    "1. MECHANICAL. Take the distinctive identifiers, field names, message names and phrases out of the " +
    "text the finding cites. Grep each across " + P.dir + " first, then spec/, docs/, schemas/, charts/. " +
    "Record what you actually grepped in `searched`.\n" +
    "2. BY FUNCTION. Grep misses paraphrase, and paraphrase is the common case: the same fact stated in " +
    "different words shares no distinctive string. Name the places that would restate this fact because of " +
    "WHAT THEY ARE — a design rationale, a field or channel table, an open-questions list, a trace or " +
    "scenario walkthrough, a migration note, a test disposition — and READ them even when no keyword " +
    "matched.\n\n" +
    "TWO CLASSES OF SITE, because the fixer's permissions differ.\n" +
    "  proposal: inside " + P.dir + ". The fixer edits these directly.\n" +
    "  tree:     under spec/, docs/, schemas/ or charts/. The fixer MAY NOT edit these and the spec-lease " +
    "hook blocks it. A tree site the staged edits would falsify means THE PROPOSAL IS MISSING AN EDIT " +
    "SITE, and the remedy is to add it to the proposal's edit list.\n\n" +
    "EVIDENCE. Every site carries file:line, a VERBATIM quote of the sentence, and one line saying why the " +
    "fix falsifies it. A site you cannot quote is not a site: do not return a section name, a heading, or " +
    "a recollection.\n\n" +
    "AN EMPTY RESULT IS A GOOD RESULT AND IS COMMON. Many findings are local: a citation correction, a " +
    "bookkeeping fix, a missing test. Returning nothing is the expected answer for those and it costs the " +
    "round nothing. Do not manufacture a site in order to have something to report.\n\n" +
    "DO NOT re-litigate the finding, judge whether it was worth filing, propose a fix, design anything, or " +
    "edit any file." +
    promptFor("expand-sites")
  );
}

// Which findings get expanded when there are more than the cap. A finding whose
// defect is a contradiction or a misattribution is the one most likely to have
// parallels; a bookkeeping or missing-test finding is usually local.
const EXPAND_PRIORITY = [
  "contradiction",
  "attribution",
  "citation",
  "unstaged-site",
  "design-defect",
  "other",
  "test-disposition",
  "missing-test",
  "bookkeeping",
];

async function expandSites(confirmedFindings, round) {
  if (skipExpansion || confirmedFindings.length === 0) return;
  const order = confirmedFindings
    // An unrecognised or missing `kind` sorts LAST. `indexOf` returns -1, and
    // clamping that to 0 gave it the rank of "contradiction" -- the TOP of the
    // list -- so a finding whose kind the dedup agent garbled displaced the
    // findings most likely to have parallels.
    .map((f, i) => {
      const idx = EXPAND_PRIORITY.indexOf(f.kind);
      return { i, rank: idx < 0 ? EXPAND_PRIORITY.length : idx };
    })
    .sort((a, b) => a.rank - b.rank);
  const picked = order.slice(0, maxExpansions).map((o) => o.i);
  const skipped = order.slice(maxExpansions).map((o) => o.i);
  if (skipped.length) {
    // Never a silent cap: a round that expanded some findings and not others
    // reads as "every finding was swept" unless the drop is on the record.
    log(
      "Round " + round + ": expanding " + picked.length + " of " + confirmedFindings.length +
        " findings; " + skipped.length + " skipped by maxExpansions (" +
        skipped.map((i) => confirmedFindings[i].title).join("; ") + ")",
    );
  }
  const results = await parallel(
    picked.map((i) => () =>
      robustAgent(expandSitesPrompt(confirmedFindings[i], round), {
        label: "r" + round + ":expand:" + i,
        phase: LOOP.name + " R" + round + ": fix",
        model: "sonnet",
        schema: POTENTIALLY_RELATED_SITES,
      }),
    ),
  );
  let found = 0;
  picked.forEach((idx, k) => {
    const r = results[k];
    // A dead expansion leaves the finding exactly as the verifiers confirmed it
    // and the round proceeds, which is how a designless group already behaves.
    if (!r) {
      // Distinguish a DEAD pass from one that searched and found nothing. Left
      // undefined, the two were indistinguishable downstream, which is the
      // defect `capped` exists to close for the cap case.
      confirmedFindings[idx].potentiallyRelatedSites = {
        proposal: [], tree: [], searched: "", capped: true,
      };
      return;
    }
    const c = classifySites(r, "Round " + round + " expansion " + idx);
    confirmedFindings[idx].potentiallyRelatedSites = {
      proposal: c.proposal,
      tree: c.tree,
      searched: r.searched || "",
      capped: false,
    };
    found += c.proposal.length + c.tree.length;
  });
  for (const i of skipped) {
    confirmedFindings[i].potentiallyRelatedSites = { proposal: [], tree: [], searched: "", capped: true };
  }
  log("Round " + round + ": site expansion found " + found + " potentially related site(s)");
}

// How a finding's potentially related sites are shown to a downstream agent.
// The framing is the same everywhere: these are CANDIDATES from one agent, and
// the finding's own `where` and `evidence` are what two verifiers confirmed.
function sitesBlock(findings) {
  const withSites = findings.filter(
    (f) =>
      f.potentiallyRelatedSites &&
      ((f.potentiallyRelatedSites.proposal || []).length || (f.potentiallyRelatedSites.tree || []).length),
  );
  // A finding the cap skipped was NOT searched, which is different from one that
  // was searched and came back empty. Both used to arrive here indistinguishable,
  // so a designer read "no related sites" off a finding nobody had looked at.
  const capped = findings.filter((f) => f.potentiallyRelatedSites && f.potentiallyRelatedSites.capped);
  const cappedNote = capped.length
    ? "\n\nNOT SEARCHED. The round's expansion budget was spent before these findings were reached, so " +
      "nothing looked for text their fixes would falsify. Absence of sites below is absence of a search, " +
      "not evidence there is nothing: " +
      capped.map((f) => f.title).join("; ") + "\n"
    : "";
  // Returned as a PAIR, because three call sites gate their instructions on
  // whether sites exist. Folding the cap note into the same string made those
  // gates true when nothing had been searched, so the fixer was told "the sweep
  // has been done for you" alongside "absence of sites is absence of a search".
  if (!withSites.length) return { sites: "", capped: cappedNote };
  return {
    capped: cappedNote,
    sites:
    "\n\nPOTENTIALLY RELATED SITES. A pass searched outward from each finding's own citations for other " +
    "text this fix would falsify. They are CANDIDATES: one agent found them and nothing verified them, " +
    "while the finding's `where` and `evidence` were confirmed by two independent verifiers. Weigh them " +
    "accordingly.\n" +
    JSON.stringify(
      withSites.map((f) => ({ finding: f.title, sites: f.potentiallyRelatedSites })),
      null,
      2,
    ),
  };
}

// F6, at the point it can still change the outcome. The DESIGNER gets this, not
// only the fixer: by the time the fixer runs the approach is already chosen, and
// what failed twice before is an argument about approach.
function siteHistoryBlock(findings, round) {
  const lines = [];
  for (const f of findings) {
    const prior = siteHistoryFor(f, round);
    if (!prior.length) continue;
    lines.push(
      "- " + f.title + " — this location has been rewritten " + prior.length + " time(s) before:\n" +
        prior
          .map(
            (h) =>
              "    round " + h.round + " (" + h.title + "): " + (h.approach || "approach not recorded") +
              "\n      " + (h.rejectedBy ? "REJECTED: " + h.rejectedBy : "not subsequently rejected"),
          )
          .join("\n"),
    );
  }
  if (!lines.length) return "";
  return (
    "\n\nTHIS TEXT HAS BEEN REWRITTEN BEFORE, AND THE REWRITES DID NOT HOLD.\n" +
    lines.join("\n") +
    "\n\nA measured run spent three of its six rounds on one sentence: a false universal, then a closed " +
    "enumeration that the run's own log calls \"the same defect one step weaker\", then a deletion. Each " +
    "attempt was the next round's finding, because each was the previous attempt made narrower rather " +
    "than a different kind of answer.\n" +
    "STATE, in your design's `why`, HOW THIS ATTEMPT DIFFERS IN KIND rather than in degree. Weakening, " +
    "narrowing, qualifying, or enumerating the exceptions to a claim that already failed is the same " +
    "answer one step smaller and it will fail the same way. Deleting the claim, moving it to the section " +
    "that owns the predicate, or stating it by reference to another section are different KINDS of " +
    "answer. If the honest conclusion is that the statement should not be there at all, say so: on the " +
    "measured run that was the answer, and it took three rounds to reach it."
  );
}

function fixPlanPrompt(confirmed, round) {
  const SB_PLAN = sitesBlock(confirmed);
  return (
    "Split one round's confirmed review findings into groups that will be fixed together.\n\n" +
    READ_ONLY +
    "\n\nPROPOSAL: " + P.dir + ". Loop: " + LOOP.name + ". Round " + round + ".\n\n" +
    "WHY THIS EXISTS. One fixer holding every finding at once reads none of them closely, and findings " +
    "that share a root produce edits that contradict each other and become findings of their own a round " +
    "later. Each group you make gets its own design and its own fixer.\n\n" +
    "THE ONE CAP is " + maxFixGroups + " groups. There is NO cap on how many findings a group holds, and " +
    "size is the wrong axis to balance on: forty trivial citation corrections that share a subject belong " +
    "in ONE group, where one fixer applies them consistently, while three deep design findings belong in " +
    "three groups however few they are. Balance COHESION and EFFORT, not size.\n\n" +
    "HOW TO GROUP.\n" +
    "- Together: findings that touch the same text, the same section, or the same mechanism. Closing those " +
    "separately is what produces contradictory edits.\n" +
    "- Alone: a finding whose fix will cascade into other sections. It needs the design stage's full " +
    "attention and it will move text the other groups are editing.\n" +
    "- FEWER than the cap when the findings genuinely cluster into fewer subjects. Every group costs a " +
    "design agent and a fix agent, so an unnecessary group is pure waste.\n\n" +
    "ORDER the groups so that no group's edits destroy an anchor a later group needs. Fixing happens in " +
    "the order you give.\n\n" +
    "EVERY finding index appears in EXACTLY ONE group, and every finding appears. A partition that drops " +
    "or duplicates a finding is rejected and the whole round falls back to one group, which is worse than " +
    "any split you could make.\n\n" +
    "THE CONFIRMED FINDINGS, indexed from 0:\n" +
    JSON.stringify(confirmed.map((f, i) => ({ i, title: f.title, where: f.where, area: f.area, kind: f.kind })), null, 2) +
    SB_PLAN.capped +
    SB_PLAN.sites +
    (SB_PLAN.sites
      ? "\n\nUSE THE SITES FOR ONE THING: OVERLAP. Two findings whose potentially related sites intersect, or " +
    "where one finding's candidate site is another finding's confirmed location, are about the same " +
    "passage and belong in the SAME GROUP. Splitting them produces two designs for one piece of text, and " +
    "the second fixer edits what the first already rewrote. Do not merge on subject similarity when the " +
        "sites do not overlap, and do not let a `low` confidence site force a merge."
      : "") +
    promptFor("fix-plan")
  );
}

function fixDesignPrompt(group, confirmed, round) {
  const picked = (group.findings || []).map((i) => confirmed[i]).filter(Boolean);
  const SB_DESIGN = sitesBlock(picked);
  const forced =
    fixDesignDepth === "shallow"
      ? "\n\nTHE CALLER HAS FORCED SHALLOW MODE. Treat every finding as trivial: apply the reviewer's " +
        "suggested fix and do no investigation. Use this budget even where you would otherwise dig.\n"
      : fixDesignDepth === "deep"
        ? "\n\nTHE CALLER HAS FORCED DEEP MODE. Treat every finding as deep and give each the architect " +
          "treatment below, including the ones that look trivial.\n"
        : "";
  return (
    "Decide HOW each finding in one group should be fixed, before anything is edited.\n\n" +
    READ_ONLY +
    " You produce a design; a different agent applies it.\n\n" +
    CONTEXT +
    "\n\nLoop: " + LOOP.name + ". Round " + round + ". Group " + group.id + " — " + (group.title || "") +
    "\nWhy these are together: " + (group.rationale || "not stated") +
    (group.sharedSubject ? "\nWhat they share: " + group.sharedSubject : "") +
    "\n\n" +
    "TRIAGE FIRST, AND LET THE TRIAGE GOVERN YOUR BUDGET. Classify each finding as trivial, moderate, or " +
    "deep BEFORE you investigate anything, and then spend accordingly. Spending deep effort on a trivial " +
    "finding is a defect in your work, not thoroughness: a group of eight trivial findings should cost a " +
    "fraction of what a single deep one costs.\n" +
    "  trivial — the reviewer's suggested fix is unambiguous, lands in one place, and changes nothing " +
    "another section states. Output one line: apply as suggested. Read nothing. Most citation, " +
    "bookkeeping, and attribution findings are trivial.\n" +
    "  moderate — clear, but touching more than one statement or choosing between two obvious options. " +
    "Output the choice, the sites, and one sentence of why.\n" +
    "  deep — a mechanism must be invented or changed, the reviewer left it open-ended, or closing it " +
    "plausibly cascades. Only these get what follows." +
    forced +
    "\n\nON A DEEP FINDING YOU ARE THE ARCHITECT. Establish ground truth in the repository BEFORE you read " +
    "what the proposal says about the mechanism: specifying against the proposal's own prose is how a " +
    "mechanism gets into the state that produced this finding. Then answer, in order:\n" +
    "  1. Does an existing spec surface, RPC, frame, field, or code path already carry this? Project " +
    "principles: " + PRINCIPLES + "\n" +
    "  2. Is there ONE change that closes several findings in this group at once? Say so in groupNote.\n" +
    "  3. Is the strongest answer to DELETE something rather than specify it? A smaller mechanism beats a " +
    "better-specified larger one, and this is the outcome most worth reaching for.\n" +
    "  4. If a new mechanism is unavoidable: what state does it read, which sites set and clear that " +
    "state, who are ALL its callers including every type satisfying a changed interface, what happens when " +
    "it does not fire and what observes that, and which test pins it at which tier? Declare it in " +
    "newMechanisms with those filled in. An unspecified mechanism is a defect handed to a later round.\n" +
    "  5. What else in the proposal must change as a consequence? Name every section in cascades.\n\n" +
    "Read " + repo + "/.claude/rules/code-best-practices.md, doc-content.md, and channel-naming.md when " +
    "the design touches their domains.\n\n" +
    "PREVENT THE PROPOSAL GROWING HAIR. It should read as ONE coherent design when this loop ends, not as " +
    "a design plus forty patches. A fix that adds a conditional to avoid restating a rule, adds a second " +
    "mechanism that nearly duplicates an existing one, or answers a finding with an exception clause, is " +
    "hair. Say so when you are proposing one, and give the coherent alternative even when it is larger, so " +
    "the choice is made deliberately rather than by accretion.\n\n" +
    "RECORD WHAT YOU REJECTED. The fixer is given your alternatives so it neither re-derives them nor " +
    "quietly picks one you already ruled out. And fill doNotDo with the tempting wrong fix: this loop's " +
    "recorded failure mode is a fixer taking the obvious local edit that a later round then has to undo.\n\n" +
    "THE FINDINGS IN THIS GROUP:\n" +
    JSON.stringify(picked, null, 2) +
    SB_DESIGN.capped +
    SB_DESIGN.sites +
    (SB_DESIGN.sites
      ? "\n\nHOW TO THINK ABOUT THEM. ADJUDICATE each site into exactly one of three dispositions and " +
        "record it in siteDispositions. The fixer does only what you decide here, so a site you leave out " +
        "is a site it has no instruction about.\n" +
        "  IN SCOPE — landing this fix MAKES this site wrong. It changes in the SAME edit. Leaving it is " +
        "the drift this stage exists to prevent: one site corrected and its parallels left asserting what " +
        "was just withdrawn.\n" +
        "  SEPARATE FINDING — the site is ALREADY wrong, for a reason of its own that this fix neither " +
        "causes nor repairs. NOT in scope. Say what the finding would be so a later round can file it. " +
        "Fixing it here is an unreviewed edit: nothing verified it, and whatever you get wrong comes back " +
        "as next round's finding.\n" +
        "  NOT A SITE — it stays true after the fix. Drop it in one line.\n\n" +
        "THE DISTINCTION THAT MATTERS is between text this fix BREAKS and text that is independently " +
        "wrong. The first is part of this edit by necessity. The second is next round's work, and pulling " +
        "it in enlarges an edit that is already the likeliest source of the next round's findings.\n\n" +
        "PRESSURE RUNS BOTH WAYS. Do not treat the list as a work order: an empty or wholly rejected list " +
        "is a normal outcome. Equally, do not reject a `high` confidence IN SCOPE site because honouring " +
        "it enlarges the edit. An incomplete fix that leaves a parallel stale is exactly what the next " +
        "round files."
      : "") +
    siteHistoryBlock(picked, round) +
    DEVIATIONS_BLOCK() +
    logBlock("fix-design-" + group.id, round) +
    promptFor("fix-design")
  );
}

function fixPrompt(confirmed, round, strikes, group, design, earlier) {
  const SB_FIX = sitesBlock(confirmed);
  // What the groups before this one in THIS round actually did. Costs no agent
  // -- the run already has it -- and it is the one thing the design stage could
  // not know, because it ran before any edit landed. A design written against
  // text an earlier group has since rewritten is the failure this closes.
  const earlierBlock =
    earlier && earlier.length
      ? "\n\nWHAT THE EARLIER GROUPS IN THIS ROUND ALREADY DID. They ran before you, against the same " +
        "files, and their edits are in the text you are about to read. Your design was written before any " +
        "of this landed. Check your anchors against the CURRENT text rather than against the design's " +
        "quotation of it, and if an earlier group has already made the change your design calls for, say " +
        "so and do not make it twice.\n" +
        earlier.map((sx, i) => i + 1 + ". " + sx).join("\n")
      : "";
  // The strike table. It reached this function as a parameter and was never
  // referenced, so the one consumer that could actually change an edit read
  // nothing -- while the array feeding it was, separately, never populated at
  // all. Both halves are now live.
  const strikeBlock = strikes
    ? "\n\nMECHANISMS THIS LOOP INVENTED THAT KEEP FAILING. Each line is something a fixer added to close " +
      "a finding, and the number of later findings it has since caused. A mechanism on its second or third " +
      "strike is not repaired one facet at a time: specify it whole in this edit, or delete it and close " +
      "the finding another way. Adding a qualifier to it is what produced the strike you are reading.\n" +
      strikes
    : "";
  const designBlock = design
    ? "\n\nTHE DESIGN FOR THIS GROUP. A separate agent established ground truth in the repository and " +
      "decided how each finding should be closed, before anything was edited. APPLY IT. Your scope for " +
      "design decisions is narrow here: you are applying a design rather than inventing one.\n" +
      "  The alternatives are listed so you neither re-derive them nor quietly pick one that was already " +
      "ruled out. `doNotDo` is the tempting wrong fix; do not take it.\n" +
      "  `cascades` names what else must change as a consequence, and it is part of the fix rather than a " +
      "note about it.\n" +
      "  If you judge a design WRONG, say so in designRejected with what you did instead and why. Do not " +
      "substitute your own silently: that is the failure this stage exists to remove.\n\n" +
      JSON.stringify(design, null, 2)
    : "\n\nNo design was produced for this group, so decide the fix yourself, with the care the design " +
      "stage would have applied: establish ground truth in the repository before you specify a mechanism, " +
      "and prefer deleting something to specifying it.";
  return (
    "You are the fixer for round " +
    round +
    " of the " + LOOP.name + " convergence loop on the proposal at " +
    P.dir +
    (group ? ", working on group " + group.id + " of this round's findings" : "") +
    ".\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT. " + LOOP.editable +
    "\nNever modify anything under spec/, docs/, pkg/, charts/, or schemas/: this proposal STAGES its " +
    "changes and never applies them.\n\nApply EXACTLY the confirmed findings below using Edit (or Write for large restructures). Requirements:\n" +
    "- Before each edit, re-verify the relevant spec/code citations yourself with Grep/Read; every claim that remains in the proposal must be accurate and carry file:line evidence. Re-verify every citation in text you touch, including stale line numbers.\n" +
    "- Make the smallest change that corrects each finding. Do not expand scope. Do not change design decisions beyond what the findings require; when a finding forces a design choice, pick the option most consistent with the cited spec precedent and the project principles (" +
    PRINCIPLES +
    "), and record the rationale in the proposal.\n" +
    "- READ EVERY FINDING BEFORE YOU EDIT ANYTHING. Group the findings that touch the same text, the same section, or the same mechanism, and fix each group as one change. Findings that look independent often share a root, and closing them separately produces edits that contradict each other and become findings of their own in a later round.\n" +
    "- INVENTING A MECHANISM IS ALLOWED AND IS SOMETIMES THE ONLY CORRECT FIX, BUT IT IS THE MOST DANGEROUS EDIT YOU CAN MAKE. This loop has measured that a mechanism introduced to close one finding goes on to produce several more over later rounds, because it lands unspecified and nothing reviews it as a design. So when a finding cannot be closed by correcting existing text, and you must add a field, a flag, a report, a compensating action, an RPC, a frame, or an interface change, specify it WHOLE in the same edit, before you write it: the state it reads and EVERY site that sets and clears that state; every caller and every type that satisfies an interface you change; what happens when it does not fire and what observes that; and the test that pins it. Then declare it in newMechanisms with those same four properties filled in. An unspecified mechanism is a defect you are handing to a later round.\n" +
    "- Where a finding genuinely needs a decision rather than an edit, record it as an open decision with " +
    "the constraint any solution must satisfy, and list it in escalated. That is a complete fix, not a " +
    "deferral. Prefer a specified mechanism to an escalation, and an escalation to an unspecified " +
    "mechanism. " +
    (LOOP && LOOP.name === "spec"
      ? "The proposal's open-decisions section is in the non-spec staging, which you may not author into, " +
        "so record yours in the staged spec edits beside the text it concerns.\n"
      : "Record it in the proposal's open-decisions section.\n") +
    "- NEVER WRITE A COUNT of staged edits, sites, statements, rewrites, or files. Name the set, or point at the enumeration that carries it. A count goes stale the moment another fix adds one, and in this loop a stale count becomes a finding, a round, and two verification agents. The documentation rules ban counts for the same reason.\n" +
    "- AFTER YOUR EDITS, reconcile every enumeration and cross-reference that names a section you touched. A fix that corrects one section and leaves another section's list of that section's contents stale is two findings rather than one.\n" +
    "- When a fix changes a trigger predicate or invariant, propagate the exact same predicate to every section that states it (design sections, summary tables, constant comments, proposed spec text, and tests) so no drift is introduced.\n" +
    "- Keep the proposed-changes section (however the proposal titles it) and any files-touched section consistent with your edits.\n" +
    (LOOP && LOOP.name === "spec"
      ? "- THE IMPLEMENTATION CHECKLIST IS NOT YOURS. Your HARD CONSTRAINT puts it out of bounds and the " +
        "reconciliation pass between the loops owns it. When an edit of yours adds, removes, merges, " +
        "splits or resequences a staged deliverable, record it as a `DEFERRED [" + P.checklist + "]` line " +
        "in your log shard naming the step that is now wrong and what is true instead. That pass closes it.\n"
      : "- KEEP THE IMPLEMENTATION CHECKLIST CURRENT. It is maintained as the proposal changes rather than derived at the end. Any edit that adds, removes, merges, splits, or resequences a staged deliverable changes the checklist in the same edit: add or remove its step, correct the deliverable ids a step names, and correct any Depends-on that the change reorders. Every staged deliverable appears in exactly one step and no step names one that does not exist. Leave every box unchecked.\n") +
    (LOOP && LOOP.name === "spec"
      ? "- KEEP THE SUMMARY'S DELIVERABLE INDEX TRUE, and correct any statement there your own edit has " +
        "falsified. Do NOT write a newly created trap into its watch-out section: your HARD CONSTRAINT " +
        "bars it, and that section growing into an errata list of fixes no lane owned is the failure the " +
        "constraint exists to prevent. A trap that belongs to the other lane is a `DEFERRED` line.\n"
      : "- KEEP THE SUMMARY TRUE. If a fix changes a top-level change, closes or reopens a decision the Summary lists as fixed, or creates a trap an implementor would fall into, update the Summary in the same edit. It is the one section every implementor agent reads, so a stale line there misleads every one of them.\n") +
    "- You may leave a detail to the implementor rather than specifying it, and doing so is often better than adding text that two sections then have to keep agreeing about. " + FORMAT_BLANKS +
    '- Append a new subsection to the "Resolved in adversarial review" section of ' +
    (LOOP && LOOP.name === "spec" ? P.spec : P.nonSpec) +
    ', titled "### Pass <N> (' +
    date +
    ', automated)", where <N> continues the existing pass numbering (read the section to determine it; create the section at the END of that file if it does not exist), with one bullet per finding fixed, matching the format of any existing entries. Create it only in the file named here, which your HARD CONSTRAINT lets you edit.\n' +
    "- Follow the documentation style rules in " +
    repo +
    '/.claude/rules/doc-style.md: complete declarative sentences, no "X, not Y" rhythm, no decorative em-dashes, no marketing language, conjunctions in lists.\n\nConfirmed findings (JSON):\n' +
    JSON.stringify(confirmed, null, 2) +
    designBlock +
    strikeBlock +
    SB_FIX.capped +
    (SB_FIX.sites
      ? "\n\nTHE SITES YOU EDIT ARE FIXED BY THE DESIGN. The finding's own location is your mandate and " +
        "you fix it. Beyond that, a pass searched for other text this fix would falsify and the design " +
        "adjudicated what it found in `siteDispositions`. A finding's named sites are a starting set " +
        "rather than the sweep, and the sweep has been done for you. Follow the adjudication:\n" +
        "  - Every site marked `in-scope` changes in this edit. Skipping one leaves precisely the drift " +
        "the design predicted.\n" +
        "  - No site marked `separate-finding` or `not-a-site` is edited here, whatever you think of it. " +
        "Editing one is an unreviewed change: nothing verified it.\n" +
        "  - A `tree` site, meaning any site outside " + P.dir + ", is NOT yours to edit. Its remedy is to " +
        "add the site to the proposal's edit list.\n" +
        "RE-READ BEFORE YOU EDIT. Each site carries a quote and a line number taken before this round's " +
        "edits landed, and line numbers drift. Open each one and confirm it still says what the quote " +
        "says.\n" +
        "IF THE DESIGN IS WRONG about a site — it marked `in-scope` something already correct, or ruled " +
        "out something your edit plainly breaks — say so in your summary and say what you did. Do not " +
        "deviate silently."
      : "") +
    SB_FIX.sites +
    earlierBlock +
    logBlock("fix-" + (group ? group.id : "all"), round) +
    "\n\nReturn a short summary listing each finding and the exact edit you made for it." +
    promptFor("fix")
  );
}

// postFixPrompt is the narrow review of what the fixer just wrote. It exists
// because fix-stage text is the newest and least-examined text in the proposal,
// and the loop's own history records that fixers introduce their own errors:
// predicate text that drifts from the design's invariants, corrected sections that
// leave a parallel statement stale, and fresh citations that were never verified.
// Before this step the only scrutiny that text received was the next round's
// whole-document lenses, which are told the TITLES of what was fixed but never
// what the fixer actually wrote. Under lens retirement that gap widens, because a
// retired lens does not re-read anything until the sweep.
//
// The scope is deliberately the edit PLUS its blast radius rather than the edit
// alone. Predicate drift is by definition an inconsistency between changed text
// and text that did not change, so a reviewer confined to the edit cannot see it.
function postFixPrompt(confirmed, fixSummary, round, mechanisms, preFixSnap, inScope) {
  return (
    (mechanisms && mechanisms.length
      ? "THIS ROUND INTRODUCED A NEW MECHANISM. Review it as a DESIGN, not as an edit. For each one below, "
        + "check against the tree that the state it reads is actually set AND cleared at the sites named; that "
        + "the caller list is complete, including every type satisfying a changed interface; that the failure "
        + "mode is observable; and that the named test would fail without it. A mechanism that fails any of "
        + "these is a finding now, while its author's reasoning is still on the page, rather than three rounds "
        + "from now when a sweep finds one facet of it.\n" + JSON.stringify(mechanisms, null, 2) + "\n\n"
      : "") +
    "You are the post-fix reviewer for round " +
    round +
    ". A fixer has just edited the proposal " +
    path +
    " to correct the confirmed findings below. Your job is narrow: verify the fixer's own work.\n\n" +
    CONTEXT +
    "\n\n" +
    READ_ONLY +
    "\n\nAnswer exactly three questions about the edits, and report only what fails:\n" +
    "1. LANDED. For each confirmed finding, does the current text actually correct it? A fix that restates the problem, corrects one of two occurrences, or edits a neighbouring sentence instead of the wrong one has not landed.\n" +
    "2. DRIFT. Did any edit introduce an inconsistency with text it did not touch? When the fix changed a predicate, an identifier, a count, a rule, or a decision, grep the proposal for every other place that states the same thing and confirm they now agree. This is the highest-yield check: the fixer edits one site and the parallel statements go stale.\n" +
    (inScope && inScope.length
      ? "   THE SITES BELOW WERE HANDED TO THE FIXER AS IN SCOPE by the design. Check each one: did the " +
        "fixer change it, and does it now agree with the corrected text? A site marked in scope and left " +
        "unedited is a CONFIRMED drift finding rather than a suspicion.\n" +
        JSON.stringify(inScope, null, 2) + "\n" +
        "   Then do the open-ended sweep anyway. That list is what one pass predicted BEFORE the edit " +
        "existed; the parallels nobody predicted are still where this question earns most of its " +
        "findings.\n"
      : "") +
    "3. CITATIONS. Is every file:line citation in the newly written text real, and does the cited location say what the new text claims? Open them. A fixer under time pressure invents plausible line numbers.\n\n" +
    (preFixSnap
      ? "THE EDITS THIS ROUND ACTUALLY MADE. " +
        diffInstruction(preFixSnap) +
        " The snapshot was taken immediately before the fixer ran, so the diff is what changed rather than " +
        "what the fixer reports changing, and the difference between the two is where question 2 lives: an " +
        "edit the fixer did not mention making is the one most likely to have left a parallel statement " +
        "stale.\n\n"
      : "Locating the edits: the fixer's summary below names them, and no snapshot was available this round.\n\n") +
    "Report a failure of 1, 2, or 3 as a finding, with file:line evidence you personally read. Do NOT re-review the proposal at large, do NOT re-litigate the findings themselves or whether they were worth fixing, and do NOT report style. If the fixer's work is sound, return an empty findings list; that is the expected answer.\n\n" +
    "Findings the fixer was asked to correct (JSON):\n" +
    JSON.stringify(confirmed, null, 2) +
    "\n\nThe fixer's own summary of the edits it made:\n" +
    (fixSummary || "(the fixer returned no summary)")
  );
}

function followUpFixPrompt(findings, round) {
  return (
    "You are the follow-up fixer for round " +
    round +
    " of the " + LOOP.name + " loop. A post-fix review of the previous fixer's edits found the defects " +
    "below in that fixer's own work.\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT. " + LOOP.editable +
    "\nNever modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "Correct each defect with the smallest edit that fixes it. Re-verify every citation you touch with Grep or Read before writing it. When a defect is drift between a changed statement and its parallels, make every statement agree rather than reverting the original fix. Append your corrections as bullets to the SAME numbered pass subsection the previous fixer created in the proposal's adversarial-review-history section, rather than opening a new pass, because these are corrections to that pass and not a separate round. Follow " +
    repo +
    "/.claude/rules/doc-style.md.\n\nDefects to correct (JSON):\n" +
    JSON.stringify(findings, null, 2) +
    "\n\nReturn a short summary of each edit you made."
  );
}

// ---- F6: the same passage rewritten round after round ----
//
// One measured run spent three of its six spec rounds on ONE sentence. Pass 11
// wrote a false universal; pass 12 replaced it with a closed enumeration, which
// that run's own log calls "the same defect one step weaker"; pass 13 deleted
// it. Each fix was the next round's finding, and no fixer was ever told that
// the text it was editing had already been rewritten once and rejected.
//
// The mechanism strike table above cannot see this. It has a row only when a
// fixer DECLARES that it invented a mechanism, and rewriting a sentence about a
// mechanism that already exists declares nothing, so the strikes never
// accumulate and the churn threshold is never reached. This keys on the
// LOCATION instead of on a declaration.
const siteHistory = [];

// A finding's `where` is free text ("staged section 10.1.8 step 1, line 213").
// Line numbers drift with every edit, so a line-keyed table is worthless after
// one round; they are stripped here and what survives is the section and its
// identifiers, which is what actually recurs.
// A site key is a FILE plus a set of SECTION TOKENS, kept apart, because the
// two need different matching rules and merging them into one string was wrong
// in both directions.
//
// Bare substring containment on a merged string matched `spec-changes.md`
// against `non-spec-changes.md`, since one file name literally contains the
// other. Every finding that named only its file -- which is the format the
// FINDINGS schema invites -- then matched every other finding in EITHER change
// file, and the designer was handed the history of an unrelated passage with an
// instruction to differ from it.
function siteKey(where) {
  const raw = String(where || "").toLowerCase();
  // Line numbers drift every round, so they are stripped: what recurs is the
  // section and its identifiers.
  const stripped = raw
    .replace(/\bl(ine)?s?\.?\s*\d+(\s*[-\u2013]\s*\d+)?/g, " ")
    .replace(/:\d+(-\d+)?/g, " ");
  // The longest `*.md` basename wins, so `non-spec-changes.md` is not read as
  // `spec-changes.md`.
  let file = "";
  for (const m of stripped.matchAll(/[a-z0-9._-]+\.md/g)) {
    if (m[0].length > file.length) file = m[0];
  }
  const tokens = stripped
    .replace(/[a-z0-9._-]+\.md/g, " ")
    // Hyphens are kept INSIDE a token: splitting on them reduced `SPEC-3` and
    // `SPEC-7` both to the generic `spec`, so two unrelated deliverables read as
    // the same site.
    .replace(/[^a-z0-9.\u00a7-]+/g, " ")
    // Trim -, . and the section sign from token EDGES. Left on, they made the
    // same location two sites: `SPEC-2.` never matched `SPEC-2`, and `§4.6.1`
    // never matched `section 4.6.1`.
    .replace(/(^|\s)[-.\u00a7]+|[-.\u00a7]+(\s|$)/g, " ")
    .replace(/\b(staged|the|a|an|in|at|of|section|sec|para|paragraph|bullet|and|for|its|it)\b/g, " ")
    .split(/\s+/)
    // A bare digit is kept: dropping single characters made `step 2` and
    // `step 8` the same site while `step 12` and `step 13` correctly differed,
    // so the matcher changed behaviour at the 9/10 boundary.
    .filter((t) => t.length > 1 || /[0-9]/.test(t));
  return { file, tokens: [...new Set(tokens)] };
}

// Two locations are the same site when they name the same file AND their
// section tokens overlap enough to be about the same passage. A file name alone
// is NOT a site: `spec-changes.md` with no section is every finding in the
// file, and treating that as one location is what produced the false matches.
function sameSite(a, b) {
  if (!a || !b) return false;
  if (a.file && b.file && a.file !== b.file) return false;
  // One side naming a file and the other not is weaker evidence, so it needs
  // the smaller token set fully contained rather than merely overlapping.
  const fileKnown = !!a.file && !!b.file;
  if (!a.tokens.length || !b.tokens.length) return false;
  // A digit-bearing token is the part of a `where` that tells one passage from
  // its neighbour. `SPEC-3`, `10.1.8`, and the bare ordinal in `step 2` are
  // identifiers, while `step`, `table`, `row`, and `clause` recur in every
  // passage of the file. Measured: `spec-changes.md, SPEC-3 step 2` and
  // `spec-changes.md, SPEC-3 step 5` tokenise to three tokens each, two of them
  // the generic `spec-3` and `step`, so ceil(0.6 * 3) = 2 was met by the generic
  // pair alone and the one digit that differed was outvoted. A round-4 designer
  // was handed three unrelated rejected attempts, and `markSitesRejected` wrote
  // that false attribution into the durable table. Adding `step` to the stopword
  // filter above fixes that one word and leaves `row 2` / `row 5` collapsing the
  // same way, so the rule is stated over the identifiers instead.
  //
  // An identifier one side carries and the other does not is decisive only when
  // the other side also carries one the first lacks. A round naming a passage
  // more narrowly than the round before it (`SPEC-3`, then `SPEC-3 step 2`) adds
  // an identifier without contradicting one, and that pair is the same site. A
  // dotted prefix is the same identifier at another grain (`4.6` and `4.6.1`),
  // so it is not a contradiction either.
  const nums = (ts) => ts.filter((t) => /[0-9]/.test(t));
  const unmatched = (x, y) =>
    x.filter((t) => !y.some((u) => u === t || u.startsWith(t + ".") || t.startsWith(u + ".")));
  if (unmatched(nums(a.tokens), nums(b.tokens)).length && unmatched(nums(b.tokens), nums(a.tokens)).length) {
    return false;
  }
  const shared = a.tokens.filter((t) => b.tokens.includes(t));
  if (!shared.length) return false;
  // Containment in token terms: one round names a passage more narrowly than
  // the next ("10.1.8 step 1" inside "10.1.8 step 1 acceptance clause"), so the
  // smaller token set must be mostly contained in the larger.
  // A one-token location is legitimate (`SPEC-3`, `10.1.8`), so the floor is one
  // shared token; the file check and the stopword filter are what keep that from
  // matching broadly.
  const smaller = Math.min(a.tokens.length, b.tokens.length);
  if (!fileKnown) return shared.length === smaller;
  return shared.length >= Math.max(1, Math.ceil(smaller * 0.6));
}

// Only EARLIER rounds of the SAME loop. Both are load-bearing:
//   - `round` restarts at 1 in each loop, so an unscoped table reads a spec
//     round 2 entry as if it were a non-spec round 2 entry.
//   - without the round filter, `recordSiteAttempts` pushes this round's own
//     attempts and the repeat detector immediately counts them, so three
//     findings in one passage announce "rewritten three times" the first time
//     the passage was ever touched.
function siteHistoryFor(finding, round, loopName) {
  const k = siteKey(finding && finding.where);
  return siteHistory.filter(
    (h) =>
      h.loop === (loopName || (LOOP ? LOOP.name : "")) &&
      (round === undefined || h.round < round) &&
      sameSite(h.key, k),
  );
}

// A finding at a site an earlier round already rewrote means that attempt did
// not hold. Recording WHICH finding rejected it is what makes the history
// usable: the third attempt needs to know that attempt 2 failed because the
// enumeration could not be closed, not merely that it failed.
function markSitesRejected(confirmedFindings, round) {
  const loopName = LOOP ? LOOP.name : "";
  for (const f of confirmedFindings) {
    const k = siteKey(f.where);
    for (const h of siteHistory) {
      if (h.rejectedBy || h.loop !== loopName || h.round >= round || !sameSite(h.key, k)) continue;
      h.rejectedBy = "round " + round + " finding \"" + f.title + "\": " + f.why_wrong;
    }
  }
}

function recordSiteAttempts(confirmedFindings, round, designs) {
  const approachFor = (title) => {
    for (const d of designs || []) {
      for (const one of (d && d.designs) || []) {
        if (one.findingTitle === title) return (one.chosen && one.chosen.approach) || "";
      }
    }
    return "";
  };
  for (const f of confirmedFindings) {
    siteHistory.push({
      key: siteKey(f.where),
      // The loop is part of the identity: `round` restarts at 1 in each loop.
      loop: LOOP ? LOOP.name : "",
      round,
      where: f.where,
      title: f.title,
      approach: approachFor(f.title) || f.suggested_fix || "",
      rejectedBy: null,
    });
  }
}

// Mechanisms the fixer has invented, and how many later findings each has caused.
// Fed back to the fixer as a strike table so a mechanism on its second failure is
// specified whole or escalated rather than repaired one facet at a time.
const introducedMechanisms = [];


// ---- Introspection: where the loop's own effort is going ----
//
// Every confirmed finding carries an area, a kind, and a judgement of whether the
// text it corrects was written by this loop. Aggregating those turns the loop's
// output into a measurement of itself, which is what distinguishes a proposal
// that is draining from one that is circling.
//
// A run measured before this existed spent 73% of its tokens on full-pool sweeps
// at roughly 2M tokens per finding, and a quarter of its late findings were
// corrections of text a fixer had written one round earlier, concentrated in
// three mechanisms that a fixer had invented one finding at a time. None of that
// was visible from inside the loop. The point of this block is that it now is,
// and that the loop can stop and redesign rather than keep repairing.
// How many times a compaction pass has failed to reach its target and moved it.
// Surfaced to introspection because a run that keeps raising is accumulating
// unresolved state faster than it resolves it.
//
// Run-wide: it mirrors the per-tag counter cp-round-boundary.sh keeps under
// scratchpad/cp-state/<tag>, and both loops compact one review log, so a
// per-loop reset would only make the mirror disagree with its source until the
// next boundary overwrites it.
let standingRaises = 0;
// Set when compactLog runs, read by the NEXT round's boundary call.
//
// Run-wide: the next boundary call after the spec loop's last round is the
// non-spec loop's first, and the compaction-pending marker it has to judge is
// per-tag rather than per-loop. Resetting it at the loop switch would report a
// pass that ran as one that did not, which is the misreading the --compacted
// argument exists to prevent.
let compactionRan = false;
const churnWindow = input.churnWindow || 6;
const churnMinFindings = input.churnMinFindings || 5;
const churnStrikes = input.churnStrikes || 3;
const redesignsAllowed = input.maxRedesigns === undefined ? 2 : input.maxRedesigns;
// Run-wide: this is the redesign budget AND the tag in the subproposal's
// filename (runRedesign builds <stem>-redesign-<tag>.md from it), so a per-loop
// reset would make the second loop's first redesign overwrite the first loop's
// subproposal record. A per-loop budget would need its own counter.
let redesignsRun = 0;
// Run-wide for the same reason, and because the caller names areas for the RUN.
// Left per-loop, the entry redesign fired again at the top of the non-spec loop:
// it re-specified what the first pass had already applied, was briefed from an
// areaLog holding only the other loop's findings, and spent the last of
// maxRedesigns so introspection could never ask for one later. Measured with
// focusAreas ['teardown']: redesign1:* in the spec loop and redesign2:* in the
// non-spec loop, twelve agents where six were asked for.
let entryRedesignDone = false;
const prunesAllowed = input.maxPrunes === undefined ? 2 : input.maxPrunes;
// Run-wide for the same reason the redesign budget is: the sections a prune
// names are headings of one document, and a section the spec loop deleted is
// gone when the non-spec loop reads it, so a per-loop budget would license the
// second loop to re-commission the first loop's deletion.
let prunesRun = 0;
// The sections this run has already pruned, lower-cased and trimmed so the
// comparison does not turn on whitespace or case. Measured: with
// introspectEvery 1 and a pass that named "## 3. Design" every round, the loop
// commissioned that one deletion in rounds 1, 2, 3 and 4 and paid a full
// 13-lens round behind each.
const prunedSections = new Set();
// Set when the introspection pass concludes the run should not continue without a
// human decision. It ends the loop rather than the process, so everything already
// fixed is kept and reported.
let stoppedByIntrospection = null;
// Stops the introspection pass proposed and the panel did not uphold. Fed back to
// the pass so it does not re-reach the same verdict on the same evidence.
const overruledStops = [];
// area -> [{loop, round, kind, introducedBy}]. The loop is recorded because
// `round` restarts at 1 in each loop, so a window measured on round numbers
// alone reads the other loop's findings as this one's: six design defects filed
// against one area in the spec loop tripped the churn counter in non-spec
// round 1, in an area that loop had found nothing in.
const areaLog = new Map();
const redesignHistory = [];
const pruneHistory = [];

function recordFindings(rnd, fs) {
  for (const f of fs) {
    const area = (f.area || "unclassified").toLowerCase().trim();
    if (!areaLog.has(area)) areaLog.set(area, []);
    areaLog.get(area).push({
      loop: LOOP.name,
      round: rnd,
      kind: f.kind || "other",
      introducedBy: f.introducedBy || "unknown",
    });
  }
}

// An area is churning when the loop keeps finding DESIGN problems there and the
// rate is not falling. Volume alone is not churn: a large but draining area is
// the loop working. What distinguishes churn is that the findings are about the
// mechanism rather than about the text describing it, and that the most recent
// window is no smaller than the one before it.
function churningAreas(rnd) {
  const out = [];
  for (const [area, entries] of areaLog) {
    if (area === "unclassified") continue;
    // This loop's findings only. Round numbers restart per loop, so every entry
    // from the other loop falls inside any window measured from here.
    const mine = entries.filter((e) => e.loop === LOOP.name);
    const recent = mine.filter((e) => e.round > rnd - churnWindow);
    if (recent.length < churnMinFindings) continue;
    const deep = recent.filter(
      (e) => e.kind === "design-defect" || e.kind === "contradiction",
    ).length;
    if (deep * 2 < recent.length) continue;
    const prior = mine.filter(
      (e) => e.round > rnd - 2 * churnWindow && e.round <= rnd - churnWindow,
    );
    if (prior.length && recent.length < prior.length) continue;
    const selfInflicted = recent.filter((e) => e.introducedBy === "this-run").length;
    out.push({
      area,
      findings: recent.length,
      designDefects: deep,
      selfInflicted,
      reason:
        recent.length +
        " finding(s) in the last " +
        churnWindow +
        " rounds, " +
        deep +
        " of them design defects or contradictions, " +
        selfInflicted +
        " of them in text this run wrote, and the rate is not falling",
    });
  }
  // A mechanism the fixer invented and has since had to repair repeatedly is
  // churning by definition, whatever its area's totals say.
  for (const m of introducedMechanisms) {
    if (m.loop !== (LOOP ? LOOP.name : m.loop) || m.strikes < churnStrikes) continue;
    if (out.some((o) => o.area === String(m.name).toLowerCase())) continue;
    out.push({
      area: m.name,
      findings: m.strikes,
      designDefects: m.strikes,
      selfInflicted: m.strikes,
      reason:
        "a mechanism this loop introduced in round " +
        m.round +
        " and has since had to repair " +
        m.strikes +
        " times, one facet at a time",
    });
  }
  return out;
}


// ---- Back to the drawing board ----
//
// When an area churns, more review rounds are the wrong instrument. The loop's
// fixer answers one finding at a time and cannot see a mechanism whole, so it
// repairs a facet and leaves the next one to be found. This subworkflow stops
// the review, designs the churning areas once, and resumes.
//
// It writes a SUBPROPOSAL: a temporary document whose target is the main
// proposal and whose content is a list of targeted edits to it. The subproposal
// is reviewed on its own before it is applied, because a redesign that lands
// unreviewed is the same defect at a larger grain.
async function runRedesign(areas, rnd, why) {
  redesignsRun++;
  const tag = redesignsRun;
  const sub =
    repo +
    "/scratchpad/redesign/" +
    path.split("/").pop().replace(/\.md$/, "") +
    "-redesign-" +
    tag +
    ".md";
  log(
    "REDESIGN " + tag + ": " + areas.map((a) => a.area).join(", ") + " — " + why,
  );
  phase("Redesign " + tag);

  // One specification per area, in parallel. Each establishes ground truth in the
  // tree BEFORE reading what the proposal says, because specifying against the
  // proposal's own prose is how the mechanism got into this state.
  const specs = (
    await parallel(
      areas.map((a) => () =>
        robustAgent(
          "Specify one mechanism of a change proposal properly, once, so that an adversarial review loop " +
            "stops finding a new facet of it every round.\n\n" +
            READ_ONLY +
            "\n\nPROPOSAL: " + path + ". MECHANISM: " + a.area + ".\n\n" +
            "WHY YOU ARE HERE. " + a.reason + ". Repairing it one finding at a time has not converged.\n\n" +
            "The findings so far in this area, with the kind and provenance the reviewers assigned:\n" +
            JSON.stringify((areaLog.get(a.area) || []).slice(-20), null, 2) +
            "\n\nWHAT TO DO. Establish the ground truth in the repository FIRST, before you read what the " +
            "proposal says about the mechanism: read the code it governs, enumerate exhaustively every type, " +
            "caller, and call site it touches, and write down what is actually true. Only then read the " +
            "proposal's current text and quote it. Then specify the mechanism whole: what it decides and on " +
            "what state; every site that sets and every site that clears that state; every caller and every " +
            "type satisfying an interface it changes; what happens when it does not fire and what observes " +
            "that; and the test that pins it, named, at the tier that owns it.\n\n" +
            "PREFER NOT HAVING A MECHANISM. The strongest outcome available to you is finding that some or " +
            "all of what is there is unnecessary and should be deleted rather than specified. Say so plainly " +
            "if you find it, with the evidence. A smaller mechanism beats a better-specified larger one.\n\n" +
            "OUTPUT a numbered list of targeted edits to the proposal. Each names the deliverable or section " +
            "it changes, quotes an anchor from the CURRENT proposal text, says whether it replaces, deletes, " +
            "or inserts, and gives the exact replacement text. Precede the list with a short statement of the " +
            "mechanism as you have specified it, so a reader can judge the edits against one coherent design. " +
            "Flag any edit whose text another area's specification is likely to touch.",
          { label: "redesign" + tag + ":spec:" + a.area, phase: "Redesign " + tag },
        ),
      ),
    )
  ).filter(Boolean);

  if (!specs.length) {
    log("REDESIGN " + tag + ": no specification returned; resuming review unchanged");
    return false;
  }

  // Consolidate. Parallel specifications of overlapping mechanisms contradict each
  // other, and applying them as written reintroduces the incoherence the redesign
  // exists to end.
  await robustAgent(
    "Reconcile parallel specifications of a change proposal's churning mechanisms into ONE conflict-free " +
      "list of targeted edits, and write it to " + sub + ". Create the directory if needed. That file is the " +
      "only one you may write; never edit " + path + " or anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
      "THE SPECIFICATIONS, produced in parallel by agents that did not see each other's work:\n\n" +
      specs.map((t, i) => "=== SPECIFICATION " + (i + 1) + " ===\n" + t).join("\n\n") +
      "\n\nWhere two specifications conflict, prefer the one whose claim you can verify in the repository, " +
      "and verify it rather than trusting either. Where both are defensible, prefer the smaller mechanism, and " +
      "prefer deleting what was invented over specifying it. Where the conflict is a genuine design choice " +
      "rather than a factual disagreement, do not pick silently: record it as an open decision with both " +
      "options, their consequences, and your default.\n\n" +
      "Check every anchor against the proposal's current text: an anchor that does not appear, or appears " +
      "twice, or has been rewritten by an earlier edit in your own list, is a defect in the list. Order the " +
      "edits so no edit's anchor is destroyed by one before it. Confirm no edit leaves a dangling reference " +
      "to a deliverable, section, or identifier another edit deletes.\n\n" +
      "WRITE: a statement of the mechanisms as reconciled; the conflicts with their resolutions and evidence; " +
      "the ordered numbered edit list; the open decisions with defaults; and a plain list of what the " +
      "consolidation deletes outright. Prose follows " + repo + "/.claude/rules/doc-style.md.",
    { label: "redesign" + tag + ":consolidate", phase: "Redesign " + tag },
  );

  // Review the subproposal before it lands. Lighter than the main pool: this
  // document is short, its subject is one design, and its edits are about to be
  // read again by the main loop's own lenses once applied.
  for (let r = 1; r <= (input.redesignReviewRounds || 2); r++) {
    const revs = (
      await parallel(
        ["mechanism", "applicability", "edit-sites"].map((k) => () =>
          robustAgent(
            "Adversarially review a redesign subproposal before it is applied to its target.\n\n" +
              READ_ONLY +
              "\n\nSUBPROPOSAL: " + sub + ". TARGET: " + path + ".\n\n" +
              (k === "mechanism"
                ? "Judge the design. Does each reconciled mechanism work? Read the code it governs and check the state it reads is really set and cleared where claimed, the caller enumeration is complete, the failure mode is observable, and the named test would fail without it."
                : k === "applicability"
                  ? "Judge whether the edit list can be applied. Every anchor must appear in the target's current text exactly once and must survive every earlier edit in the list. Report any anchor that is absent, duplicated, or destroyed by a prior edit, and any edit whose replacement text references something another edit deletes."
                  : "Judge completeness. Does the list touch every place the target states the mechanisms it changes? A mechanism respecified in one deliverable and left standing in another is the defect this redesign exists to end.") +
              "\n\n" + BAR,
            { label: "redesign" + tag + ":review:" + k + ":r" + r, phase: "Redesign " + tag, schema: FINDINGS },
          ),
        ),
      )
    ).filter(Boolean);
    const fs = revs.flatMap((x) => x.findings || []);
    log("REDESIGN " + tag + " review round " + r + ": " + fs.length + " finding(s)");
    if (!fs.length) break;
    await robustAgent(
      "Correct a redesign subproposal. The only file you may edit is " + sub + ".\n\n" +
        "Findings:\n" + JSON.stringify(fs, null, 2) +
        "\n\nApply exactly these. Re-verify every citation you touch against the repository. Keep the edit " +
        "list ordered so no anchor is destroyed by an earlier edit.",
      { label: "redesign" + tag + ":fix:r" + r, phase: "Redesign " + tag },
    );
  }

  // Apply to the main proposal.
  await robustAgent(
    "Apply a reviewed redesign to its target proposal.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " + path + ". Never modify anything under spec/, docs/, " +
      "pkg/, charts/, or schemas/, and do not edit " + sub + ".\n\n" +
      "The redesign is at " + sub + ". Read it in full, then apply its edits in the order it gives, checking " +
      "each anchor against the current text before you write. An anchor that does not appear is a defect in " +
      "the redesign rather than a licence to guess: skip that edit, apply the rest, and say which you skipped " +
      "and why.\n\n" +
      "Then reconcile the proposal with what you changed: the Summary's fixed decisions and watch-outs, the " +
      "implementation checklist's steps and their dependencies, the files-touched section, and the testing " +
      "section. A redesign that deletes a mechanism leaves its steps, its tests, and its files behind unless " +
      "you remove them.\n\n" +
      'Append a subsection to the proposal\'s "Resolved in adversarial review" section titled "### Redesign ' +
      tag + " (" + date + ', automated)", recording which areas were redesigned, why, what the redesign ' +
      "deleted, and any open decisions it recorded. Prose follows " + repo + "/.claude/rules/doc-style.md.",
    { label: "redesign" + tag + ":apply", phase: "Redesign " + tag },
  );

  // The areas were just redesigned, so their history no longer describes the text
  // in front of the loop. Keeping it would re-trigger the detector immediately on
  // evidence the redesign has already answered.
  for (const a of areas) {
    areaLog.delete(a.area);
    for (const m of introducedMechanisms) {
      if (m.loop !== LOOP.name) continue;
      if (String(m.name).toLowerCase() === String(a.area).toLowerCase()) m.strikes = 0;
    }
  }
  redesignHistory.push({
    tag,
    round: rnd,
    areas: areas.map((a) => a.area),
    why,
    subproposal: sub,
  });
  log("REDESIGN " + tag + " applied; resuming review");
  return true;
}


// ---- Section growth: the signal the counters cannot produce ----
//
// Counting findings says where reviewers looked. Measuring the document says what
// the loop has been doing. A section that tripled while the document grew a tenth
// is the shape of over-specification, and no finding count shows it, because each
// individual addition was a reasonable answer to a real finding.
const GROWTH = {
  type: "object",
  required: ["documentWas", "documentNow", "grew"],
  properties: {
    documentWas: { type: "integer", description: "Total line count of the BEFORE file" },
    documentNow: { type: "integer", description: "Total line count of the AFTER file" },
    grew: {
      type: "array",
      description:
        "The sections that gained the most lines, largest gain first, at most eight. Omit sections that did not grow.",
      items: {
        type: "object",
        required: ["section", "was", "now", "added"],
        properties: {
          section: { type: "string", description: "Heading text" },
          was: { type: "integer" },
          now: { type: "integer" },
          added: { type: "integer" },
        },
      },
    },
  },
};

const NO_GROWTH = { documentWas: 0, documentNow: 0, documentPct: null, grew: [] };

// The documents this measurement is about. The review log is excluded because it
// grows by design and would swamp the signal; the deviations file is the
// implementor's and is empty during review.
const growthTargets = () =>
  P.layout === "legacy" ? [P.root] : [P.spec, P.nonSpec, P.problem, P.summary, P.checklist];

async function growthSince(snapPath) {
  if (!snapPath) return NO_GROWTH;
  // BOTH SIDES ARE DIRECTORIES. `snapshot()` copies P.dir, and P.root is the
  // proposal directory in the folder layout, so a prompt that names them as two
  // revisions of one document measures nothing: every pass from the move to the
  // folder layout until this was fixed reported "from 0 to 0" and the
  // introspection pass reasoned without its growth signal.
  const files = growthTargets();
  // The snapshot mirrors the proposal directory, so a file's BEFORE is its own
  // basename inside the snapshot. This holds for both layouts.
  const pairs = files
    .map((f) => {
      const base = f.replace(/^.*\//, "");
      return "  " + base + ": BEFORE " + snapPath + "/" + base + " AFTER " + f;
    })
    .join("\n");
  const res = await robustAgent(
    "Measure how a proposal grew between two revisions of itself, per file and per section. This is a " +
      "measurement: do not read either revision for meaning, do not judge its content, and do not edit " +
      "anything.\n\n" +
      "THE PAIRS. Each line is one file, its BEFORE path and its AFTER path. A BEFORE path that does not " +
      "exist means the file is new and was zero lines before; count it as such rather than failing.\n" +
      pairs +
      "\n\nUse Bash. In each file, attribute every line to the nearest `##` or `###` heading above it and " +
      "total the lines per heading; lines before the first heading belong to `(preamble)`. An awk one-liner " +
      "does this. Name each section `<file>: <heading>` so two files' sections never collide.\n\n" +
      "EXCLUDE the adversarial-review-history section wherever it appears, with all its subsections. It is " +
      "an append-only record of the passes this loop has run, so it grows every round by construction and " +
      "counting it reports the loop's own bookkeeping as the proposal's growth. On one measured run it was " +
      "59% of the staged spec edits.\n\n" +
      "Report `documentWas` and `documentNow` as the TOTAL line counts across every file above, after the " +
      "exclusion, and `grew` as the sections that gained the most lines, largest gain first, at most eight.",
    { label: "growth", schema: GROWTH },
  );
  if (!res) return NO_GROWTH;
  const was = res.documentWas || 0;
  const now = res.documentNow || 0;
  return {
    documentWas: was,
    documentNow: now,
    documentPct: was ? Math.round((100 * (now - was)) / was) : null,
    grew: (res.grew || []).map((r) => ({
      section: r.section,
      was: r.was,
      now: r.now,
      added: r.added,
      pct: r.was ? Math.round((100 * r.added) / r.was) : null,
    })),
  };
}

// What a stopping verdict has to say about what happens next, so a `halt` is
// actionable rather than merely a stop.
const NEXT_STEPS = {
  type: "object",
  required: ["summary", "confidence"],
  properties: {
    summary: { type: "string", description: "what should happen next, in two sentences" },
    confidence: {
      type: "string",
      enum: ["clear", "needs-human"],
      description:
        "clear: the next run's arguments and prompts follow from what this run learned, and a caller could launch it without deciding anything. needs-human: a person must decide something first.",
    },
    humanDecision: { type: "string", description: "for needs-human: the decision, stated so it can be answered without reading the proposal" },
    rerunMode: { type: "string", enum: ["review", "redesign", "new"] },
    rerunArgs: { type: "string", description: "a JSON object of arguments for the next run, as a string" },
    rerunPrompts: { type: "string", description: "a JSON object of per-agent prompts for the next run, as a string" },
    problemStatementEdit: { type: "string", description: "for reframe: the restatement to write before re-entering" },
  },
};

const INTROSPECTION = {
  type: "object",
  required: ["observations", "caseHealthy", "caseUnhealthy", "verdict", "reasoning"],
  properties: {
    observations: {
      type: "array",
      items: { type: "string" },
      description:
        "What you found, each with its evidence, written BEFORE you reach a verdict. One per question you were asked, plus anything else the evidence shows.",
    },
    caseHealthy: {
      type: "string",
      description: "The strongest argument that this run is converging and should continue unchanged. State it at its best even if you do not believe it.",
    },
    caseUnhealthy: {
      type: "string",
      description: "The strongest argument that it is not. State it at its best even if you do not believe it.",
    },
    verdict: {
      type: "string",
      enum: ["healthy", "redesign", "prune", "reframe", "halt"],
    },
    reasoning: { type: "string", description: "Which case wins and why." },
    areas: {
      type: "array",
      items: { type: "string" },
      description: "For redesign: the area slugs to specify whole. Name the mechanism, not the symptom.",
    },
    sections: {
      type: "array",
      items: { type: "string" },
      description:
        "For prune: the sections that have grown past their value, each with what should be deleted and what constraint an IMPLEMENTOR'S CHOICE blank would carry in its place.",
    },
    questionForHuman: {
      type: "string",
      description: "For reframe or halt: the decision a human must take, stated so it can be answered without reading the whole proposal.",
    },
    prediction: {
      type: "string",
      description:
        "What you expect the next few rounds to look like if the run continues. The next introspection is shown this and held to it, so make it falsifiable.",
    },
    nextSteps: NEXT_STEPS,
  },
};


// ---- Second opinion on a decision to stop ----
//
// Halting is the one verdict the loop cannot take back cheaply: it ends the run
// and puts the question to a human. It is also the verdict where a single agent's
// error is most expensive in both directions, so the decision is separated from
// the observation. The introspection pass observes; a panel decides.
//
// The asymmetry is deliberate. A wrong "continue" self-corrects, because the next
// introspection fires within introspectEvery rounds and sees more evidence. A
// wrong "stop" costs a human interruption and the run's momentum, and nothing
// self-corrects it. So the burden of proof is on stopping, and a panel that
// cannot agree takes the least disruptive verdict any member reached.
// ---- The judges ----------------------------------------------------------
//
// A judge does not vote on the verdict. It tries to FALSIFY the argument the
// introspection pass made for it, and the verdict stands unless a majority
// falsifies it conclusively. That is the right shape for two reasons. Handing
// a reviewer a conclusion and asking it to check produces agreement rather
// than examination, which is the failure mode a vote invites. And an
// unfalsified argument is the only kind worth acting on, whichever direction
// it points.
//
// Every verdict goes to a panel now, including `healthy`. A wrong `healthy` is
// the most expensive verdict in the loop -- it spends every remaining round --
// and nothing was checking it.
const FALSIFICATION = {
  type: "object",
  required: ["falsified", "howConclusive", "theArgumentIAttacked", "reasoning"],
  properties: {
    falsified: { type: "boolean", description: "true when you showed the pass's argument does not hold" },
    howConclusive: {
      type: "string",
      enum: ["conclusive", "partial", "none"],
      description:
        "conclusive: you have evidence the argument is wrong, not merely unproven. partial: you have doubt you can articulate but cannot settle. none: you could not falsify it.",
    },
    theArgumentIAttacked: { type: "string", description: "the pass's claim, in your words, so a reader can see you attacked the real one" },
    reasoning: { type: "string" },
    evidence: { type: "array", items: { type: "string" }, description: "file:line or round numbers you personally checked" },
    fallbackVerdict: {
      type: "string",
      enum: ["healthy", "redesign", "prune", "reframe", "halt"],
      description:
        "required when falsified is true: the verdict the evidence actually supports. It may not be the verdict you attacked. A falsification that names none is set aside, because it leaves the loop nothing to act on.",
    },
  },
};

// One panel per verdict. The judges within a panel read the same evidence and
// differ in what they weigh, which is what makes their agreement informative;
// judges asked three unrelated questions make the narrowest of them the decider.
const PANELS = {
  healthy: [
    "You are the TRAJECTORY SKEPTIC. The pass says this run is draining. Find the evidence that it is not: " +
      "findings per round flat or rising, deep defects arriving late, growth concentrated in sections that " +
      "were already large, the loop repeatedly correcting text it wrote itself. Numbers, not impressions.",
    "You are the BLIND-SPOT SKEPTIC. A quiet area reads identically whether it is clean or whether no lens " +
      "is examining it, and those are opposite conditions. For each area the pass calls settled, say which " +
      "lens would have found a defect there and whether it actually ran recently.",
  ],
  prune: [
    "You are the NECESSITY judge. Is the text the pass wants deleted genuinely redundant, or is it the only " +
      "place something is stated? Read it.",
    "You are the DEPENDENCY judge. Does anything else in the proposal cite, enumerate, or depend on the " +
      "text that would be deleted? A prune that orphans a cross-reference trades one defect for another.",
    "You are the DELEGATION judge. Would an IMPLEMENTOR'S CHOICE marker actually BOUND the choice here, or " +
      "would it delegate without a constraint? A blank with no constraint is a licence, and the convention " +
      "forbids it for a wire contract, a fail-closed predicate, an ordering another step depends on, or " +
      "anything a test must assert.",
  ],
  redesign: [
    "You are the ARCHITECTURE judge. Is the mechanism actually WRONG, or is it right and merely " +
      "under-described? Those need opposite treatments, and a redesign of a sound mechanism costs a full " +
      "subworkflow to produce the same design in different words.",
    "You are the SMALLER-MECHANISM judge. Can the thing be DELETED rather than respecified? A smaller " +
      "mechanism beats a better-specified larger one, and if the answer here is deletion then a redesign " +
      "aimed at specifying it whole is aimed at the wrong outcome.",
    "You are the CASCADE judge. If this is respecified, what else in the proposal must change? Name it. A " +
      "redesign whose blast radius the pass has not counted leaves the loop with more work than it started " +
      "with, in sections no lens is currently reading.",
  ],
  reframe: [
    "You are the PROBLEM-FIT judge. Read the problem statement. Is the framing the pass wants to abandon " +
      "actually wrong, or is the design merely hard? Those look identical from inside a difficult run.",
    "You are the SCOPE judge. Is this one proposal or several? If several, say where the cut is and whether " +
      "reframing is really the instrument, since a split is not a reframe.",
    "You are the EVIDENCE judge. Does the tree still support the framing the proposal rests on? Check the " +
      "citations the problem statement makes, not the pass's summary of them.",
  ],
  halt: [
    "You are the HUMAN-QUESTION judge. Can a person actually ANSWER the question as stated, in one sitting, " +
      "without reading the whole proposal? A halt whose question comes back unresolved has cost a human's " +
      "attention and bought nothing.",
    "You are the COST judge. What does continuing buy against what it costs? What did the recent rounds " +
      "produce, and what would the next several plausibly find given that?",
    "You are the SELF-HELP judge. Is there a legal move this loop has not tried -- a redesign, a prune, a " +
      "lens it has not run, a decision it could record rather than resolve? Halting when one remains is " +
      "spending a human on work the loop can do.",
  ],
};

const DISRUPTION = ["healthy", "prune", "redesign", "reframe", "halt"];

async function judgePanel(rnd, verdict, growth, churn) {
  const panel = PANELS[verdict.verdict] || PANELS.halt;
  const judges = panel.slice(0, verdict.verdict === "healthy" ? judgesHealthy : judgesPerVerdict);
  log(
    "Round " + rnd + ": introspection returned " + verdict.verdict + "; putting it to " + judges.length +
      " judge(s) who will try to falsify it",
  );
  const brief =
    READ_ONLY +
    "\n\nPROPOSAL: " + P.dir + ". Round " + rnd + ".\n\n" +
    "An introspection pass has concluded that this convergence run is `" + verdict.verdict + "`. YOUR JOB " +
    "IS TO FALSIFY THAT, not to vote on it. Reach for the evidence that would show the pass wrong, and " +
    "report honestly whether you found it.\n\n" +
    "THE VERDICT STANDS UNLESS A MAJORITY OF THIS PANEL FALSIFIES IT CONCLUSIVELY. So `partial` is an " +
    "honest and common answer: it means you have doubt you can articulate and cannot settle, and it leaves " +
    "the verdict standing. Do not inflate it to `conclusive` to be heard, and do not deflate a real " +
    "refutation to `partial` to avoid disrupting the run.\n\n" +
    "RATIFYING IS THE OTHER FAILURE. You have been handed a conclusion and asked to check it, which is the " +
    "situation in which reviewers agree most and examine least. Attack the argument the pass actually made, " +
    "which is why you must restate it in your own words before you attack it.\n\n" +
    "IF YOU FALSIFY IT, NAME WHAT THE EVIDENCE DOES SUPPORT in `fallbackVerdict`, from `healthy`, `prune`, " +
    "`redesign`, `reframe`, and `halt`. The panel acts on the least disruptive verdict its falsifiers name, " +
    "so a refutation naming nothing leaves the loop with no move and is set aside. It may not be `" +
    verdict.verdict + "`, the verdict you are attacking, since naming the verdict you just refuted says " +
    "nothing. Leave it empty when you did not falsify.\n\n" +
    "THE PASS'S FULL OUTPUT, including the case it made against its own verdict:\n" +
    JSON.stringify(verdict, null, 2) +
    "\n\nHOW THE DOCUMENT GREW since the previous introspection:\n" +
    JSON.stringify(growth, null, 2) +
    "\n\nCONFIRMED FINDINGS BY AREA over the whole run, each with its round, kind, and whether it " +
    "corrected text this loop itself wrote:\n" +
    JSON.stringify(Object.fromEntries([...areaLog].map(([a, es]) => [a, es])), null, 2).slice(0, 10000) +
    "\n\nMECHANISMS THIS LOOP'S FIXER INVENTED, and how many later findings each caused:\n" +
    JSON.stringify(introducedMechanisms.filter((m) => m.loop === LOOP.name), null, 2) +
    "\n\nROUND HISTORY:\n" +
    JSON.stringify(
      history.map((h) => ({ loop: h.loop, round: h.round, sweep: h.sweep, confirmed: h.confirmed, newMechanisms: h.newMechanisms })),
      null,
      2,
    ).slice(0, 8000) +
    (churn && churn.length ? "\n\nCOUNTERS THAT TRIPPED:\n" + JSON.stringify(churn, null, 2) : "") +
    "\n\nRead the proposal and its review log yourself before answering. The evidence above is a summary " +
    "and the documents are the subject.";

  const votes = (
    await parallel(
      judges.map((l, i) => () =>
        robustAgent(brief + "\n\nYOUR LENS. " + l + promptFor("judge." + verdict.verdict), {
          label: "judge:" + verdict.verdict + ":" + (i + 1) + ":r" + rnd,
          phase: "Round " + rnd + ": introspect",
          schema: FALSIFICATION,
        }),
      ),
    )
  ).filter(Boolean);

  if (votes.length === 0) {
    log("Round " + rnd + ": no judge returned; the verdict stands unexamined");
    return { decision: verdict.verdict, votes, quorum: false, upheld: true };
  }

  // An ORDERING, not an equality. Strict equality meant `falsificationBar:
  // "partial"` -- documented as making the panel easier to convince -- EXCLUDED
  // every judge who falsified conclusively, so three conclusive refutations
  // counted as none and the verdict stood unexamined.
  const BAR_RANK = { partial: 1, conclusive: 2 };
  const need = BAR_RANK[falsificationBar] || BAR_RANK.conclusive;
  const conclusive = votes.filter(
    (v) => v.falsified && (BAR_RANK[v.howConclusive] || 0) >= need,
  );
  const falsified = conclusive.length > votes.length / 2;
  if (!falsified) {
    log(
      "Round " + rnd + ": " + conclusive.length + "/" + votes.length +
        " judge(s) falsified it " + falsificationBar + "ly; the verdict " + verdict.verdict + " STANDS",
    );
    return { decision: verdict.verdict, votes, quorum: true, upheld: true };
  }
  // Falsified. Take the least disruptive verdict any falsifier named, on the
  // same asymmetry the loop has always used: a wrong continue self-corrects at
  // the next introspection and a wrong stop does not.
  // A falsifier that names nothing is not read as naming `healthy`, and one
  // that names the verdict it just refuted is not read at all. Under the silent
  // `healthy` default the panel over `healthy` was inert: with the judges
  // stubbed to falsify conclusively and name no fallback, 2/2 refutations of
  // `healthy` decided `healthy` and the run continued unchanged, which is the
  // one outcome that panel exists to prevent. A vote naming the verdict under
  // attack made the run halt while recording that the panel had overturned the
  // halt.
  const fallbacks = conclusive
    .map((v) => v.fallbackVerdict)
    .filter((v) => DISRUPTION.includes(v) && v !== verdict.verdict);
  if (!fallbacks.length) {
    log(
      "Round " + rnd + ": " + conclusive.length + "/" + votes.length + " judge(s) falsified " +
        verdict.verdict + " conclusively and named no verdict the evidence supports; continuing under the " +
        "continue-is-cheap asymmetry, and the next round re-introspects",
    );
    return { decision: "healthy", votes, quorum: true, upheld: false, undirected: true };
  }
  const decision = fallbacks.sort((a, b) => DISRUPTION.indexOf(a) - DISRUPTION.indexOf(b))[0];
  log(
    "Round " + rnd + ": " + conclusive.length + "/" + votes.length + " judge(s) falsified " +
      verdict.verdict + " conclusively; taking the least disruptive fallback, " + decision,
  );
  return { decision, votes, quorum: true, upheld: false, undirected: false };
}

let introspectEvery = input.introspectEvery || 5;
// The warrant gate: introspection's first act is to ask whether it should be
// running. On by default; a cadence wake ignores it either way.
const introspectGate = input.introspectGate !== false;
const judgesPerVerdict = input.judgesPerVerdict || 3;
const judgesHealthy = input.judgesHealthy || 2;
// How conclusively a majority must falsify the pass's argument before its
// verdict is overturned. "partial" makes the panel easier to convince.
const falsificationBar = input.falsificationBar === "partial" ? "partial" : "conclusive";
const introspections = [];
// Run-wide, deliberately. This is a snapshot path rather than a round number,
// and both loops edit one proposal directory, so "how much has the document
// grown since anyone last looked" stays the measurement the introspection pass
// wants across the loop handoff. Clearing it per loop would hand the second
// loop's first pass NO_GROWTH and take its growth signal away.
let lastGrowthSnap = null;
// Per-loop, reset at the top of runReviewLoop. `round` restarts at 1 in each
// loop, so a round number from one loop is not comparable with one from the
// other. Measured before the reset existed: at introspectEvery 3 over two
// 6-round loops the spec loop introspected at r3 and r6 and left this at 6, and
// the non-spec loop -- which reviews the larger half -- then evaluated
// 1 - 6 >= 3 every round and introspected zero times.
let lastIntrospectRound = 0;

// The agent decides; the counters only wake it. A counter cannot judge whether a
// mechanism is under-designed or a section is over-specified, and an agent that
// only ran on a fixed cadence would miss a runaway between its turns. Together:
// the counter cannot miss, the agent can judge.
const GATE = {
  type: "object",
  required: ["warranted", "why"],
  properties: {
    warranted: { type: "boolean" },
    why: { type: "string" },
    whatTheCounterMissedOrOverread: { type: "string" },
  },
};

async function introspect(rnd, reason, churn, byCadence) {
  // The counters that wake this pass are crude and are often wrong in both
  // directions, so the pass's first act is to decide whether it should be
  // running at all. A counter wake that is not warranted returns healthy
  // without paying for the full pass or a panel.
  //
  // A CADENCE wake runs regardless. The cadence exists precisely to look when
  // no counter has fired, and letting the gate suppress it would remove the one
  // pass that is not reacting to something.
  if (introspectGate && !byCadence) {
    const gate = await robustAgent(
      "Decide whether an introspection pass is warranted right now, before one runs.\n\n" +
        READ_ONLY +
        "\n\nPROPOSAL: " + P.dir + ". Round " + rnd + ". A counter woke this: " + reason + ".\n\n" +
        "The counters are crude and are wrong in both directions. Your job is to say whether the thing " +
        "they detected is really there, cheaply, before a full pass and a panel are paid for.\n\n" +
        "READ: the `## Standing context` of " + P.log + ", and the round history below. Then answer one " +
        "question: does the evidence show what the counter claims, or is this an area that is simply large " +
        "and draining normally?\n\n" +
        "COUNTER OUTPUT:\n" + JSON.stringify(churn || [], null, 2) +
        "\n\nROUND HISTORY:\n" +
        JSON.stringify(
          history.map((h) => ({ loop: h.loop, round: h.round, confirmed: h.confirmed, sweep: h.sweep })),
          null,
          2,
        ).slice(0, 6000) +
        "\n\nSay warranted: false when the counter over-read a large area draining normally, and true " +
        "when the pattern is really there. Erring toward false is cheap here: the cadence pass runs within " +
        "a few rounds regardless and sees more evidence." +
        promptFor("introspect.gate"),
      { schema: GATE, label: "introspect-gate:r" + rnd, phase: "Round " + rnd + ": introspect" },
    );
    if (gate && gate.warranted === false) {
      log(
        "Round " + rnd + ": the introspection gate found the counter unwarranted (" +
          String(gate.why || "").slice(0, 120) + "); skipping the full pass",
      );
      introspections.push({ round: rnd, verdict: "healthy", gated: true, why: gate.why });
      lastIntrospectRound = rnd;
      return { verdict: "healthy", gated: true, reasoning: gate.why };
    }
  }
  const growth = await growthSince(lastGrowthSnap);
  lastGrowthSnap = await snapshot("introspect-r" + rnd);
  lastIntrospectRound = rnd;
  const windowStart = Math.max(1, rnd - introspectEvery);
  const recent = history.filter((h) => h.round >= windowStart);

  const res = await robustAgent(
    "You are the introspection pass of an adversarial convergence loop running on a change proposal. Your " +
      "subject is THE LOOP AND THE DOCUMENT, not the correctness of any individual finding. Every other agent " +
      "here reads the proposal to improve it; you read it to judge whether improving it this way is still " +
      "working.\n\n" +
      READ_ONLY +
      "\n\nPROPOSAL: " + path + ".\nRound " + rnd + ". Woken because: " + reason + ".\n\n" +
      "HOW THE DOCUMENT HAS GROWN since the last introspection. The document as a whole grew " +
      (growth.documentPct === null ? "n/a" : growth.documentPct + "%") +
      ", from " + growth.documentWas + " to " + growth.documentNow + " lines. The sections that grew most:\n" +
      JSON.stringify(growth.grew, null, 2) +
      "\n\nWHAT THE REVIEWERS FOUND, by area, over the whole run. Each entry is one confirmed finding with " +
      "the round it was confirmed in, the kind of defect, and whether the text it corrected was written by " +
      "this loop itself:\n" +
      JSON.stringify(
        Object.fromEntries([...areaLog].map(([a, es]) => [a, es])),
        null,
        2,
      ).slice(0, 12000) +
      "\n\nMECHANISMS THIS LOOP'S OWN FIXER INVENTED, and how many later findings each has caused:\n" +
      JSON.stringify(introducedMechanisms.filter((m) => m.loop === LOOP.name), null, 2) +
      (standingRaises > 0
        ? "\n\nTHE REVIEW LOG'S STANDING CONTEXT HAS OUTGROWN ITS TARGET " + standingRaises + " TIME(S). " +
          "Each raise is a compaction pass that could not reach its target without dropping an OPEN, an " +
          "UNVERIFIED, or a MISTAKE that still stands. One or two is ordinary on a long run. A count that " +
          "keeps climbing says the loop is accumulating unresolved state faster than it resolves it, which " +
          "is one of the things circling looks like from the inside. Weigh it against the finding rate: " +
          "rising open state with a flat finding rate is a stronger signal than either alone.\n"
        : "") +
      (pruneHistory.length
        ? "\n\nSECTIONS THIS RUN HAS ALREADY PRUNED, with the round each was pruned in:\n" +
          JSON.stringify(pruneHistory, null, 2) +
          "\nNaming one of these again asserts that the first deletion did not take. Say what is still " +
          "over-specified in it and why the earlier prune left that standing, or reach a different verdict: " +
          "a repeat is dropped rather than carried out. " +
          (prunesRun >= prunesAllowed
            ? "The prune budget of " + prunesAllowed + " is spent, so a prune verdict is recorded and not acted on.\n"
            : prunesAllowed - prunesRun + " prune(s) remain in this run's budget.\n")
        : "") +
      "\n\nTHE LAST " + recent.length + " ROUNDS, with what each fixed:\n" +
      JSON.stringify(
        recent.map((h) => ({
          round: h.round,
          sweep: h.sweep,
          confirmed: h.confirmed,
          fixed: h.confirmedTitles,
          newMechanisms: h.newMechanisms,
        })),
        null,
        2,
      ).slice(0, 12000) +
      (churn && churn.length
        ? "\n\nA COUNTER TRIPPED, which is why you were woken early. It is a crude instrument and it is often " +
          "wrong in both directions, so adjudicate rather than ratify:\n" + JSON.stringify(churn, null, 2)
        : "") +
      (overruledStops.length
        ? "\n\nSTOPS YOU PROPOSED THAT A REVIEW PANEL DID NOT UPHOLD. You reached these verdicts on evidence " +
          "much like today's and three reviewers disagreed. That is not a reason to avoid the verdict now, but " +
          "it is a reason to say what has changed since, and to answer the panel's reasoning rather than " +
          "restate your own:\n" + JSON.stringify(overruledStops, null, 2)
        : "") +
      (introspections.length
        ? "\n\nYOUR OWN PREVIOUS VERDICTS, with the predictions you made. You are accountable to these: say " +
          "whether each prediction held, because a prediction that failed is evidence your reading of this run " +
          "is wrong.\n" +
          JSON.stringify(
            introspections.map((i) => ({
              round: i.round,
              verdict: i.verdict,
              prediction: i.prediction,
            })),
            null,
            2,
          )
        : "") +
      "\n\nTHE REVIEW LOG is at " + P.log + ". Read its `## Standing context` in full, and read every " +
      "`CORRECTS` entry in its ledger. A log full of corrections is direct evidence that this loop is " +
      "misleading itself, and no counter can see it. Read every outstanding `OPEN` and `UNVERIFIED` too: " +
      "an area quiet because nobody checked it looks exactly like an area that is clean.\n" +
      "\n\nANSWER THESE, each with evidence, in observations, BEFORE you reach a verdict:\n" +
      "1. Which sections grew most, and did each growth buy something proportionate to its size? Growth that " +
      "answered real findings is the loop working; growth that restates a mechanism a third time is not.\n" +
      "2. Is any mechanism now described in more than one place, in different words? Read the sections that " +
      "grew and check. Two deliverables staging different rewrites of the same text is the defect this " +
      "question exists to catch, and it is invisible to the reviewers because each reads its own section.\n" +
      "3. Do the accumulated fixes still satisfy the proposal's Decisions and its Summary's fixed decisions, " +
      "or has a decision been eroded by fixes that each looked local? Read them and check against what the " +
      "document now stages.\n" +
      "4. Is any area quiet because it is clean, or because no lens is examining it? A flat finding rate reads " +
      "identically in both cases and they are opposite conditions.\n" +
      "5. If you were writing this proposal fresh today, knowing everything the findings have taught, what " +
      "would you do differently? Answer concretely. This is the question the round-by-round process cannot ask " +
      "itself, and the one most likely to surface a design that is being repaired when it should be replaced.\n\n" +
      "THEN state the strongest case that this run is healthy and should continue unchanged, and the strongest " +
      "case that it is not, each at its best. Only then choose:\n" +
      "  healthy — the loop is draining. Say what makes you confident, and predict what the next rounds hold.\n" +
      "  redesign — one or more mechanisms are being repaired a facet at a time and should be specified whole. " +
      "Name the areas by mechanism rather than by symptom.\n" +
      "  prune — a section has grown past its value. Name it, say what should be deleted, and say what " +
      "constraint an IMPLEMENTOR'S CHOICE blank should carry in its place. Over-specification is a defect: it " +
      "is where two sections drift apart, and detail an implementor does not need costs more to keep true than " +
      "it is worth.\n" +
      "  reframe — the proposal's scope or framing is wrong, and no amount of reviewing fixes that. Say what " +
      "the framing should be.\n" +
      "  halt — something needs a human decision before more rounds are worth spending.\n\n" +
      "FOR `reframe` OR `halt` YOU MUST ALSO FILL nextSteps. A stop that says only to stop costs a human " +
      "the work of deciding what to do, which is the work you are best placed to do: you have just read " +
      "the whole run. Say what the next run should be, and set confidence `clear` only when its arguments " +
      "and prompts follow from what this run learned and a caller could launch it without deciding " +
      "anything. Set `needs-human` when a person must settle something first, and state that decision so " +
      "it can be answered without reading the proposal.\n\n" +
      "DEFAULT TO healthy ONLY IF THE EVIDENCE SUPPORTS IT. A run that is converging looks like this: findings " +
      "per round falling, deep defects giving way to shallow ones, growth concentrated where work is genuinely " +
      "being added. A run that is not looks like this: findings flat or rising, design defects appearing late, " +
      "growth concentrated in sections that were already large, and the loop repeatedly correcting text it " +
      "wrote itself. Saying healthy when the second pattern holds costs far more than a false alarm.",
    { label: "introspect:r" + rnd, phase: "Round " + rnd + ": introspect", schema: INTROSPECTION },
  );
  if (!res) {
    log("Round " + rnd + ": introspection did not return; continuing unchanged");
    return null;
  }
  introspections.push({ round: rnd, ...res });
  history[history.length - 1].introspection = {
    verdict: res.verdict,
    reasoning: res.reasoning,
  };
  log("Round " + rnd + " introspection: " + res.verdict + " — " + (res.reasoning || "").slice(0, 180));
  return res;
}

phase("Review");

// robustAgent wraps agent() with script-level retries so a transient API failure
// (529 "Overloaded", "Server is temporarily limiting requests", rate limit) does
// not silently drop the call. agent() returns null when the runtime's own retries
// are exhausted under a sustained overload; a dropped review lens or verifier then
// makes a round look "clean" because a failed reviewer contributes zero findings,
// indistinguishable from a reviewer that genuinely found nothing, which can
// FALSELY certify convergence and force the whole (expensive) run to be redone.
// Each retry is a fresh agent() with its own internal backoff, so attempts are
// naturally spaced without a script-level timer (sleep/Date.now/Math.random are
// unavailable in workflow scripts). A genuine thrown exception (e.g. the token
// budget is exhausted) propagates immediately and is not retried, since retrying
// cannot help it.
async function robustAgent(prompt, opts, attempts = 4) {
  // Model fallback: the first two attempts use the primary model (Opus, inherited
  // from the session); attempts 3+ fall back to Sonnet. A 529 "Overloaded" is
  // usually capacity-pool-specific, so when Opus is saturated Sonnet often still
  // has headroom, and a lens completing on Sonnet is far better than a lens
  // dropped for the round (which corrupts the clean-streak). Opus is tried first
  // so its quality is preserved whenever it is available; only a sustained Opus
  // outage degrades an agent to Sonnet, and every fallback is logged so a round
  // certified clean partly on Sonnet is visible in the transcript. This does NOT
  // rescue a hard account-level "session limit" (the whole account is capped) —
  // that still requires the account switch or waiting for the reset.
  const fallbackAt = 3;
  for (let i = 1; i <= attempts; i++) {
    const callOpts =
      i >= fallbackAt ? { ...opts, model: "sonnet" } : opts;
    const r = await agent(prompt, callOpts);
    if (r !== null && r !== undefined) return r;
    if (i < attempts) {
      log(
        "  " +
          (opts && opts.label ? opts.label : "agent") +
          ": transient API failure, retry " +
          i +
          "/" +
          (attempts - 1) +
          (i + 1 >= fallbackAt ? " (falling back to sonnet)" : ""),
      );
    }
  }
  return null;
}

// Every read of an agent's findings goes through this. The schemas ask for the
// array and nothing enforces it, so an agent that answers with an object
// carrying no `findings` key at all is a well-formed answer as far as this
// script is concerned. Measured against the test harness, where an unstubbed
// call resolves to `{}`: a dedup result, a single review lens, and the post-fix
// review each threw a TypeError straight out of the loop body and killed the
// run, in a run that had already paid for every round before it.
function findingsOf(r) {
  return r && Array.isArray(r.findings) ? r.findings : [];
}

const fixedTitles = [];
// Snapshot boundaries for the two diffs. The round-start copy yields the diff the
// NEXT round's lenses read; the pre-fix copy yields the diff the post-fix reviewer
// reads. They differ because a round can also prune, apply a redesign, and run a
// follow-up fix after the post-fix review.
// Run-wide, across BOTH review loops: a finding refuted in the spec loop must
// not be re-litigated in the non-spec loop, and the round history is the run's
// record of itself rather than one loop's.
let lastRoundSnap = null;
const rejected = [];
const history = [];

// Per-loop. The spec loop and the non-spec loop each keep their own round
// counter, retired set, sweep count and convergence, because each certifies a
// different half of the proposal and a lens satisfied by the spec staging has
// said nothing about the code staging.
let LOOP = null;
function newLoop(cfg) {
  return {
    name: cfg.name,
    round: 0,
    reviewersFailed: false,
    verifiersFailed: [],
    fixersFailed: [],
    retired: new Set(),
    converged: false,
    sweeps: 0,
    maxRounds: cfg.maxRounds,
    pool: cfg.pool,
    editable: cfg.editable,
    scopeNote: cfg.scopeNote,
    specTouched: [],
    stalledLenses: [],
    lastFindings: [],
  };
}

// Lens retirement. Re-running a lens that just found nothing, over text its own
// domain did not change, is the loop's largest avoidable cost: on a long run it
// is the difference between every lens paying for every round and each lens
// paying until it is satisfied. A lens that returns zero findings is retired and
// stops running. When every lens has retired, one SWEEP round runs the entire
// pool again over the final text. A lens that finds something in the sweep is
// reactivated and the loop continues; when the active set drains again, another
// sweep runs. Convergence requires a complete sweep of every lens, with zero
// confirmed findings, over text nobody has changed since.
//
// This preserves what the two-consecutive-clean-rounds rule protected. That rule
// existed because fixers introduce their own errors, so a clean round says
// nothing about text the previous fixer wrote. Here every retirement is provisional
// until the sweep re-reads the final text, so no lens certifies text it never saw.
// Retirement is keyed on a genuine zero-finding return; a lens that FAILED after
// robustAgent's retries is never retired, because a dropped lens contributes zero
// findings and would otherwise retire itself by failing.


// ---- Compaction and mid-run overrides ------------------------------------
//
// The review log is what carries a run's memory between agents. Left alone it
// grows without bound and every lens pays to read it every round, so it is
// compacted when the round-boundary script says it has passed a threshold.
//
// Compaction is age-graded rather than uniform. The current round is untouched,
// the last few are merged, and anything older is reduced to its durable residue
// and promoted into the standing context or retired. CORRECTS and USEFUL are
// what make that possible: they are the only signals that separate an entry
// worth promoting from one worth deleting.
async function compactLog(round, standingLines, target) {
  const goal = target || standingContextTarget;
  log(
    "Round " + round + ": the review log's standing context is at " + standingLines +
      " lines against a target of " + goal + "; compacting",
  );
  await robustAgent(
    "Compact a review log's standing context. This is a TEXT operation and it has a time budget: a run " +
      "measured before this was rewritten spent fifteen minutes per pass and still missed its target.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " + P.log + ". Change nothing else.\n\n" +
      "EDIT, DO NOT REWRITE. Read the file once. Then make TARGETED edits with Edit: rewrite the " +
      "`## Standing context` section, and move aged entries from `## Ledger` to `## Retired`. Do NOT " +
      "rewrite the whole file with Write. An earlier version of this prompt demanded one whole-file " +
      "Write, and every pass ignored it, because reproducing fourteen hundred unchanged ledger lines to " +
      "change forty standing-context ones is not a saving. The instruction was wrong and the passes were " +
      "right. What IS wasteful is paging the file in with `sed -n` and assembling the result through " +
      "heredocs in /tmp; one pass made forty Bash calls that way. Read with Read, edit with Edit.\n\n" +
      "DO NOT VERIFY ANYTHING AGAINST THE REPOSITORY. You are not reviewing the proposal and you are not " +
      "checking whether an entry is true. Where two entries disagree and neither corrects the other, keep " +
      "the NEWER one and move the older to `## Retired` with a one-line note that they disagreed; if the " +
      "disagreement matters, write one `UNVERIFIED:` line for a later reviewer to settle. An earlier " +
      "version of this pass was told to check the tree itself, and it turned a text pass into a " +
      "mini-review that grepped pkg/ and read three spec files.\n\n" +
      "THE THREE SECTIONS. `## Standing context` is curated and is the part every other agent reads. " +
      "`## Ledger` is the chronological record; nothing reads it end to end but this pass, though agents " +
      "DO cite individual entries by id, so an entry is moved to `## Retired` rather than deleted and " +
      "keeps its id. `## Retired` holds what no longer applies, one line each.\n\n" +
      "WHAT TO DO, in order.\n\n" +
      "1. Leave round " + round + "'s entries in the ledger exactly as they are.\n\n" +
      "2. For every entry older than three rounds, LIFT ITS DURABLE RESIDUE into `## Standing context` and " +
      "move the rest to `## Retired` as one line. The durable residue is every `FACT`, `WATCHOUT`, " +
      "`DECISION`, `MISTAKE` and unclosed `DEFERRED` line.\n" +
      "   `MISTAKE` IS THE MOST VALUABLE TAG IN THE LOG AND IS NEVER DROPPED. An entry saying \"I spent " +
      "this round hunting X; it is not there; do not re-derive this\" saves a later agent a whole round, " +
      "which is worth more than most findings. Keep its reasoning rather than its headline: a one-line " +
      "summary of a dead end is not enough for the next agent to recognise the same dead end.\n\n" +
      "3. HONOUR `CORRECTS`. An entry another agent marked wrong is rewritten to what is true, or retired. " +
      "A superseded `WATCHOUT` is DELETED rather than kept for the record: a warning about a trap that no " +
      "longer exists costs every future agent a detour.\n\n" +
      "4. HONOUR `USEFUL`. An entry another agent said saved it work goes into `## Standing context` and is " +
      "never dropped while its subject stands.\n\n" +
      "5. NEVER DROP AN `OPEN`, AN `UNVERIFIED`, OR A `DEFERRED` until something closes it. A `DEFERRED` " +
      "is a correction an agent derived but could not apply because the file was not its to edit, and the " +
      "pass between the loops greps for exactly these and closes them. Keep the file it names, the claim " +
      "that is now false, and what is true instead: reduced to a headline it cannot be closed, and the " +
      "correction is lost for good. A `DEFERRED` that a later `CORRECTS` says was applied is retired like " +
      "any other closed entry.\n\n" +
      "6. STRUCTURE `## Standing context` UNDER THESE FOUR HEADINGS, in this order, and keep all four:\n" +
      "   `### Settled` — every `FACT` and `DECISION`. ONE LINE EACH. This is a lookup table, not prose.\n" +
      "   `### Traps` — every `WATCHOUT` and `MISTAKE`. Up to four lines each, because a trap the reader " +
      "cannot recognise from the entry is a trap they walk into anyway. No cap on how many.\n" +
      "   `### Open` — every `OPEN` and `UNVERIFIED`. ONE LINE EACH, naming the ledger entry id that " +
      "carries the detail.\n" +
      "   `### Deferred` — every unclosed `DEFERRED`, kept WHOLE rather than summarised, because the pass " +
      "between the loops applies these and cannot apply a headline.\n" +
      "   GIVE EACH ENTRY A SHORT BOLD SUBJECT so a reader can find the one they need without reading the " +
      "section. Measured on a real run, agents cite standing-context entries BY SUBJECT and a quarter of " +
      "all citations went to two of them. Navigability is worth more here than brevity.\n\n" +
      "7. THE TARGET IS " + goal + " LINES for `## Standing context` as a whole. `### Settled` and " +
      "`### Open` are one line per entry and will meet it; `### Traps` is where the length lives and it " +
      "is the section worth keeping. If you cannot reach the target without dropping something that " +
      "matters, DO NOT DROP IT. Say so in the changelog and leave the section longer: the target moves up " +
      "on its own when a pass cannot reach it, and a run carrying a long standing context is far cheaper " +
      "than one compacting every round.\n\n" +
      "8. LEAVE A CHANGELOG as the first lines of `## Standing context`: what this pass lifted, retired and " +
      "deleted, and whether it reached " + goal + ", so the next pass can be judged against it.\n\n" +
      "Do not act on anything the log says, do not fix a defect it names, and do not add a finding of your " +
      "own." +
      promptFor("compact") +
      "\n\nFollow " + repo + "/.claude/rules/doc-style.md.",
    { label: "r" + round + ":compact", phase: LOOP.name + " R" + round + ": fix" },
  );
}

// Arguments a caller may change WHILE a run is going, by writing
// scratchpad/cp-args/<runTag>.json. Only forward-looking knobs are mergeable:
// one that is already baked into prompts the run has issued cannot be changed
// without invalidating them, and silently accepting it would produce a run that
// did not do what the caller asked while reporting success.
const OVERRIDABLE = {
  maxFixGroups: (v) => { maxFixGroups = Number(v) || maxFixGroups; },
  fixDesignDepth: (v) => { fixDesignDepth = String(v); },
  lockSpecChanges: (v) => { lockSpecChanges = !!v; },
  maxExpansions: (v) => { maxExpansions = Number(v) || maxExpansions; },
  skipExpansion: (v) => { skipExpansion = !!v; },
  standingContextTarget: (v) => { standingContextTarget = Number(v) || standingContextTarget; },
  standingContextTrigger: (v) => { standingContextTrigger = Number(v) || standingContextTrigger; },
  compactAtLines: (v) => { compactAtLines = Number(v) || compactAtLines; },
  compactGrowthLines: (v) => { compactGrowthLines = Number(v) || compactGrowthLines; },
  introspectEvery: (v) => { introspectEvery = Number(v) || introspectEvery; },
};

function applyOverrides(overrides, round) {
  const took = [];
  const refused = [];
  for (const [k, v] of Object.entries(overrides)) {
    if (OVERRIDABLE[k]) {
      OVERRIDABLE[k](v);
      took.push(k + "=" + JSON.stringify(v));
    } else {
      refused.push(k);
    }
  }
  if (took.length) {
    log("Round " + round + ": caller overrides applied for the next round: " + took.join(", "));
  }
  if (refused.length) {
    log(
      "Round " + round + ": ignoring override(s) " + refused.join(", ") +
        " — only forward-looking knobs are mergeable mid-run, because a value already baked into prompts " +
        "this run has issued cannot be changed without invalidating them",
    );
  }
}

// ---- The review loop, run twice -----------------------------------------
//
// The SPEC loop runs first and converges the staged spec edits alone. The
// NON-SPEC loop runs after it and converges everything else, reading both
// change files as one document, because a non-spec change that contradicts a
// staged spec edit is a finding only a reviewer holding both can see.
//
// Everything inside is shared: retirement, sweeps, dedup, verification,
// fixing, the post-fix review, introspection, churn, and redesign. What
// differs is the pool, which files the fixer may edit, and the budget.
async function runReviewLoop(cfg) {
  LOOP = newLoop(cfg);
  // Per-loop, because it is compared against `round`, which restarts at 1
  // below. The counters that are genuinely per-run -- redesignsRun,
  // standingRaises, compactionRan and lastGrowthSnap -- are deliberately NOT
  // reset here; each says why at its declaration.
  lastIntrospectRound = 0;
  const POOL = cfg.pool;
  const maxRounds = cfg.maxRounds;
  const retired = LOOP.retired;
  let round = 0;
  let converged = false;
  let sweeps = 0;
  let reviewersFailed = false;
  // Sticky, for the same reason reviewersFailed and fixersFailed are: a round
  // whose verifiers died is not evidence of anything, and convergence is
  // certified by the SWEEP round, which can be complete on its own terms while
  // an earlier round's verification never ran. A run whose verify stage was
  // broken end to end used to return status "reviewed", converged true, over
  // findings that were never confirmed or refuted.
  const verifiersFailed = [];
  // What THIS round claimed to fix. The round boundary reports how many hunks
  // actually changed, and a claim of N findings fixed against zero hunks is a
  // fixer that edited nothing. The evidence was already being collected and
  // stored as history[].sectionsChanged, and nothing compared the two.
  let roundFixedTitles = [];
  // Consecutive rounds a lens has failed after robustAgent's retries, keyed by
  // lens, cleared the moment it returns anything. It distinguishes a lens that
  // dropped one round from one that is gone.
  const lensFailStreak = new Map();
  // Groups whose fixer died, as "r<round>:<group>". Loop-scoped and sticky for
  // the same reason reviewersFailed is: the round that certifies convergence is
  // a LATER one, so a per-round flag cannot stop it. A measured run confirmed
  // two findings in round 1, lost every fixer, and certified a clean sweep in
  // round 3 over text no fixer had touched.
  const fixersFailed = [];
  // A fresh relaunch with resumeState continues where an interrupted run
  // stopped, rather than restarting the loop. This is the mechanism for
  // changing an ANCHORED argument, which resumeFromRunId cannot do: the journal
  // replays a call only when its prompt is unchanged, so a changed prompt busts
  // the cache from round one and re-does everything under the new text.
  if (input.resumeState) {
    const raw = await robustAgent(
      "Run exactly this command and reply with its stdout and nothing else:\n\n" +
        "cat " + repo + "/scratchpad/cp-state/" + runTag + "/state-" + cfg.name + ".json 2>/dev/null || echo '{}'" +
        "\n\nDo nothing else. Do not read, summarise, or edit any file.",
      { label: "resume-state:" + cfg.name, model: "haiku", phase: "Review" },
    );
    let st = null;
    try {
      const m = String(raw || "").match(/\{[\s\S]*\}/);
      if (m) st = JSON.parse(m[0]);
    } catch (e) {
      st = null;
    }
    if (st && typeof st.round === "number" && st.round > 0) {
      round = st.round;
      sweeps = st.sweeps || 0;
      for (const k of st.retired || []) retired.add(k);
      log(
        "Resuming the " + cfg.name + " loop at round " + round + " with " + retired.size +
          " lens(es) already retired",
      );
      // An anchored argument that changed since the recorded run means the
      // prompts this loop is about to issue differ from the ones it issued
      // before. That is legitimate -- it is why resumeState exists -- but it is
      // worth saying out loud, because a caller who changed one by accident
      // gets a run that silently reviews under different instructions.
      for (const [k, v] of Object.entries(st.args || {})) {
        const now = typeof input[k] === "object" ? "(set)" : input[k];
        if (input[k] !== undefined && now !== v) {
          log(
            "Resume: the anchored argument " + k + " changed since the recorded run (" +
              JSON.stringify(v) + " -> " + JSON.stringify(now) + "); rounds from here run under the new value",
          );
        }
      }
    } else if (st) {
      log("resumeState was set but no state was recorded for the " + cfg.name + " loop; starting at round 1");
    }
  }
  log(
    "Entering the " + cfg.name + " review loop over " + POOL.length +
      " lens(es), budget " + maxRounds + " round(s)",
  );

  // startLenses is implemented by seeding the retired set rather than by narrowing
  // round one. A held-back lens is therefore treated exactly as a lens that has
  // already returned nothing: it does not run while the starting lenses are still
  // finding defects, and it first reads the proposal in the sweep, over text those
  // lenses have already driven clean. It rejoins the active set the moment it finds
  // something in that sweep, and from then on behaves like any other lens.
  //
  // This is strictly cheaper than deferring the held-back lenses to round two, and
  // it costs no guarantee, because convergence still requires a complete sweep of
  // every pool lens. The seeded state is provisional in exactly the way an earned
  // retirement is: no lens certifies text it never read.
  if (startSet) {
    for (const l of POOL) {
      if (!startSet.has(l.key)) retired.add(l.key);
    }
  }

  function applyRetirement(lenses, lensResults, survivors, round, note) {
    const retired = LOOP.retired;
    const out = [];
    const back = [];
    lenses.forEach((l, i) => {
      if (!lensResults[i]) return;
      if (survivors.has(l.key)) {
        if (retired.delete(l.key)) back.push(l.key);
      } else if (!retired.has(l.key)) {
        retired.add(l.key);
        out.push(l.key);
      }
    });
    if (out.length > 0) {
      log(
        "Round " +
          round +
          ": retiring " +
          out.join(", ") +
          " (" +
          note +
          "; re-runs only in the sweep)",
      );
    }
    if (back.length > 0) {
      log(
        "Round " +
          round +
          ": reactivating " +
          back.join(", ") +
          " (a finding of its own survived verification)",
      );
    }
  }

  // Redesign as an entry mode. The caller names the areas, so the loop does not
  // have to discover the churn first: a human who already knows which mechanism is
  // wrong should not have to pay six rounds for the detector to agree.
  if (!entryRedesignDone && (mode === "redesign" || (Array.isArray(input.focusAreas) && input.focusAreas.length))) {
    entryRedesignDone = true;
    // focusAreas takes either a bare slug or {area, reason}. A caller who already
    // knows which mechanism is wrong usually knows why, and the per-area agents are
    // briefed from that reason. On a run that has not yet classified any findings
    // the reason is the only evidence they get, so a bare slug leaves them starting
    // cold against a document the loop has not measured.
    const named = (input.focusAreas || []).map((a) => {
      const isObj = a && typeof a === "object";
      return {
        area: String(isObj ? a.area : a)
          .toLowerCase()
          .trim(),
        findings: 0,
        designDefects: 0,
        selfInflicted: 0,
        reason:
          (isObj && a.reason) ||
          "named by the caller as an area to redesign before review begins",
      };
    });
    if (!named.length) {
      log("Redesign mode with no focusAreas; nothing to redesign, entering review");
    } else if (redesignsRun >= redesignsAllowed) {
      // Same budget as the introspection path spends, for the same reason: a
      // redesign is the most expensive move the run has.
      log(
        "The caller named " + named.map((a) => a.area).join(", ") + " to redesign but the budget of " +
          redesignsAllowed + " is spent; entering review without one",
      );
    } else {
      await runRedesign(named, 0, "requested by the caller");
    }
  }

  // A sweep that found nothing, was incomplete only because a lens failed, and
  // follows a sweep the same lens also failed, is a livelock rather than a
  // retry. No lens edits anything and no fixer runs on such a round, so the next
  // sweep puts the whole pool over byte-identical text and gets the same answer.
  // Measured on one run: review:security failed from round 3 on, and rounds 3
  // through 8 each paid a full 14-lens sweep, 84 lens agents, to re-learn what
  // round 4 had already established.
  const STALLED_LENS_ROUNDS = 2;
  function sweepStalled(rnd) {
    const stalled = [...lensFailStreak.entries()]
      .filter(([, n]) => n >= STALLED_LENS_ROUNDS)
      .map(([k]) => k);
    // One failed sweep is always retried: the second sweep is what tells a
    // transient outage apart from a lens that is not coming back.
    if (stalled.length === 0 || sweeps < 2) return false;
    log(
      "Round " + rnd + ": " + stalled.join(", ") + " failed " + STALLED_LENS_ROUNDS +
        " rounds running and every other lens is clean; stopping rather than re-sweeping " +
        "unchanged text. Restore the lens and resume, or set excludeLenses to review without " +
        "it and accept that convergence then certifies nothing about its domain",
    );
    LOOP.stalledLenses = stalled;
    reviewersFailed = true;
    return true;
  }

  // ---- Closing a round ---------------------------------------------------
  //
  // One agent, one command. It merges this round's log shards into the review
  // log and deletes them, counts the ledger for the compaction trigger,
  // compares the proposal's file hashes against the ones it recorded a round
  // ago and reports anything that moved outside its owner's allowance, takes
  // the snapshot the NEXT round's lenses diff against, counts the hunks this
  // round changed, and reads the caller's mid-run overrides.
  //
  // The state of the tree at the end of round N is the state at the start of
  // round N+1, so one call does both halves. The logic lives in
  // .claude/tools/cp-round-boundary.sh rather than in this prompt: it is in
  // version control, it is testable without an agent, and a failure is an exit
  // code rather than an instruction an agent skipped at the end of a long
  // prompt.
  //
  // It runs on EVERY path out of a round, including the ones that found
  // nothing. A round that returns early still wrote log shards and still owes
  // the next round a snapshot, and skipping it there orphaned both.
  async function closeRound(rnd, complete) {
    // The state a fresh relaunch needs to continue this loop rather than
    // restart it. Deliberately small: the round, what has retired, how many
    // sweeps have run, and the arguments in force, which is what a later launch
    // compares against to notice that an anchored one changed.
    const stateJson = JSON.stringify({
      loop: LOOP.name,
      round: rnd,
      sweeps,
      retired: [...retired],
      converged,
      fixedTitles: fixedTitles.length,
      args: Object.fromEntries(
        Object.keys(ARG_CLASS)
          .filter((k) => input[k] !== undefined && ARG_CLASS[k] === "anchored")
          .map((k) => [k, typeof input[k] === "object" ? "(set)" : input[k]]),
      ),
    });
    const raw = await robustAgent(
      "Run exactly these two commands and reply with the stdout of the SECOND and nothing else:\n\n" +
        "mkdir -p " + repo + "/scratchpad/cp-state/" + runTag + " && cat > " +
        repo + "/scratchpad/cp-state/" + runTag + "/" + "state-" + LOOP.name + ".json" +
        " <<'LENNYSTATE'\n" + stateJson + "\nLENNYSTATE\n\n" +
        "bash " + repo + "/.claude/tools/cp-round-boundary.sh" +
        " --dir '" + P.dir + "'" +
        " --tag '" + runTag + "'" +
        " --loop '" + LOOP.name + "'" +
        " --round " + rnd +
        " --repo '" + repo + "'" +
        " --compact-at " + compactAtLines +
        " --standing-target " + standingContextTarget +
        " --standing-trigger " + standingContextTrigger +
        " --compact-growth " + compactGrowthLines +
        // Whether a compaction pass actually RAN since the previous boundary.
        // Only the caller knows: the script can see that it ASKED for one, and a
        // run killed between the request and the pass would otherwise be judged
        // as a pass that failed, raising the target past the current size and
        // permanently excusing the section that needed compacting.
        " --compacted " + (compactionRan ? "1" : "0") +
        "\n\nThe second prints one line of JSON. Reply with that line verbatim. If either exits " +
        "non-zero, reply with the single word FAILED followed by its stderr. Do nothing else: do not " +
        "read, summarise, or edit any other file.",
      { label: "r" + rnd + ":round-boundary", model: "haiku", phase: LOOP.name + " R" + rnd + ": fix" },
    );
    let boundary = null;
    try {
      const m = String(raw || "").match(/\{[\s\S]*\}/);
      if (m) boundary = JSON.parse(m[0]);
    } catch (e) {
      boundary = null;
    }
    if (!boundary) {
      // The bookkeeping is not optional: without it the log is unmerged, the
      // audit did not run, and the next round has no snapshot to diff against.
      // A round that could not close is not a round that can certify.
      log(
        "Round " + rnd + ": the round-boundary script did not complete; round INCONCLUSIVE " +
          "(the log is unmerged and the next round has no snapshot)",
      );
      return false;
    }
    const h = history[history.length - 1];
    // Scoped to the loop as well as the round: `history` spans the spec and
    // non-spec loops, and a round that closes before pushing its own entry
    // would otherwise stamp the previous loop's last entry whenever the two
    // round numbers happen to match.
    if (h && h.round === rnd && h.loop === LOOP.name) {
      h.sectionsChanged = boundary.hunks || 0;
      if ((boundary.changedFiles || []).length) h.filesChanged = boundary.changedFiles;
    }
    // A fixer that edited nothing must not have its findings entered in the
    // run-wide "already fixed, do not re-litigate" list every later lens of
    // BOTH loops is given. One fixer answering "no edit was needed" used to
    // suppress its findings permanently, and the diff proving it had changed
    // nothing was sitting one field away.
    // `hunksKnown` false means there was no previous snapshot to diff against --
    // the first round of a loop -- so zero hunks is the absence of a BASELINE,
    // not the absence of change. Reading the two alike withdrew every genuine
    // fix from round 1 of both loops on a measured run and reported nothing
    // fixed, which is the same under-reporting this guard was added to end.
    if (roundFixedTitles.length && boundary.hunksKnown && (boundary.hunks || 0) === 0) {
      for (const t of roundFixedTitles) {
        const at = fixedTitles.lastIndexOf(t);
        if (at >= 0) fixedTitles.splice(at, 1);
      }
      if (h && h.round === rnd && h.loop === LOOP.name) {
        h.fixClaimUnsupported = roundFixedTitles.slice();
        h.complete = false;
      }
      log(
        "Round " + rnd + ": the round claimed " + roundFixedTitles.length +
          " finding(s) fixed but the tree did not change; the claim is withdrawn and they " +
          "stay open for a later round",
      );
      // closeRound reports completeness through its return value; `complete` is
      // its parameter and the caller ANDs the result into its own flag.
      complete = false;
    }
    roundFixedTitles = [];
    lastRoundSnap = boundary.snapshot || lastRoundSnap;
    if (boundary.overrides && Object.keys(boundary.overrides).length) {
      applyOverrides(boundary.overrides, rnd);
    }
    if (boundary.targetRaisedNow) {
      // The pass could not reach the target, so the target moved. Logged rather
      // than silent: a run that keeps raising is accumulating unresolved state
      // faster than it resolves it, and that is what circling looks like here.
      log(
        "Round " + rnd + ": compaction could not reach its target; raised to " +
          boundary.standingTarget + " (raise " + boundary.targetRaises + ")",
      );
    }
    if (boundary.targetRaises !== undefined) standingRaises = boundary.targetRaises;
    compactionRan = false;
    if (boundary.compactionDue) {
      await compactLog(rnd, boundary.standingLines, boundary.standingTarget);
      compactionRan = true;
    }
    return complete;
  }

  while (round < maxRounds && !converged) {
    round++;
    const roundStartSnap = await snapshot("r" + round + "-start");
    // One pool, one rule. A lens runs unless it has retired, and when every
    // lens has retired the whole pool runs again as a sweep.
    //
    // A round may still hold a single lens, when the rest have retired and one
    // was reactivated by a fix. That is retirement working: the lens has a
    // specific thing to re-read. It is not the case the rotation used to
    // create, where a lens ran alone only because it had never been given a
    // chance to run at all.
    const active = POOL.filter((l) => !retired.has(l.key));
    const isSweep = active.length === 0;
    const lenses = isSweep ? POOL : active;
    if (isSweep) sweeps++;

    log(
      "Round " +
        round +
        (isSweep
          ? ": FULL SWEEP " +
            sweeps +
            " over all " +
            lenses.length +
            " lenses (every lens had retired; a clean sweep converges)"
          : ": launching " +
            lenses.length +
            " reviewers (" +
            retired.size +
            "/" +
            POOL.length +
            " lenses retired)"),
    );

    // Barrier: the dedup step needs every reviewer's findings at once.
    const lensResults = await parallel(
      lenses.map(
        (l) => () =>
          robustAgent(reviewPrompt(l, round, fixedTitles, rejected, lastRoundSnap), {
            label: "r" + round + ":review:" + l.key,
            phase: LOOP.name + " R" + round + ": review",
            schema: REVIEW_FINDINGS,
          }),
      ),
    );
    const failedLenses = lensResults.filter((r) => !r).length;
    const results = lensResults.filter(Boolean);
    lenses.forEach((l, i) => {
      if (lensResults[i]) lensFailStreak.delete(l.key);
      else lensFailStreak.set(l.key, (lensFailStreak.get(l.key) || 0) + 1);
    });

    // Retire every lens that genuinely ran and found nothing; reactivate every lens
    // that found something. On a normal round the reactivation arm is a no-op (an
    // active lens is not in the set). On a sweep it is the mechanism that puts a
    // lens back to work after it finds a defect in text it had previously cleared.
    // A failed lens (r is null) is left exactly as it was, so a transient API
    // failure can neither retire a lens nor resurrect one.
    // Stamp every finding with the lens that produced it. Retirement is decided by
    // which findings SURVIVE verification, and the dedup step merges findings across
    // lenses, so this association must be recorded here, by the script, before any
    // model has a chance to lose it.
    lenses.forEach((l, i) => {
      findingsOf(lensResults[i]).forEach((f) => {
        f.lens = l.key;
      });
    });

    if (results.length === 0) {
      log("Round " + round + ": every reviewer failed; stopping");
      reviewersFailed = true;
      // The round wrote its log shards before its reviewers died, and it still
      // owes the next launch a state file and the next round a snapshot, so it
      // closes here rather than at the loop tail it is about to jump over.
      await closeRound(round, false);
      break;
    }
    // A round may certify "clean" (advance the convergence streak) ONLY when every
    // lens and every verifier actually ran. If any lens failed after robustAgent's
    // retries, the round is INCONCLUSIVE: a partial reviewer set finding nothing is
    // not evidence of convergence. Counting it would reproduce the 529-driven
    // false-convergence bug. verifyComplete (below) extends the same guard to the
    // two-skeptic verification of each finding.
    let roundComplete = failedLenses === 0;
    if (failedLenses > 0) {
      log(
        "Round " +
          round +
          ": " +
          failedLenses +
          "/" +
          lenses.length +
          " lenses failed after retries; round INCONCLUSIVE (will not count toward convergence)",
      );
    }
    const raw = results.flatMap((r) => findingsOf(r));
    log("Round " + round + ": " + raw.length + " raw findings");

    if (raw.length === 0) {
      // A round that found nothing IS the current answer to "what is this loop
      // still finding". Leaving the previous round's titles standing reported
      // findings that had since been fixed as the reason the run stopped.
      LOOP.lastFindings = [];
      // Nobody found anything, so nobody has a survivor: every lens that genuinely
      // ran retires.
      applyRetirement(lenses, lensResults, new Set(), round, "found nothing");
      history.push({
        loop: LOOP.name,
        round,
        sweep: isSweep,
        lenses: lenses.map((l) => l.key),
        raw: 0,
        deduped: 0,
        confirmed: 0,
        retiredAfter: [...retired],
      });
      // The round closes BEFORE it is allowed to certify. A round whose
      // bookkeeping did not complete left its log unmerged and the next round
      // without a snapshot, and a round in that state cannot be the one that
      // ends the loop.
      roundComplete = (await closeRound(round, roundComplete)) && roundComplete;
      history[history.length - 1].complete = roundComplete;
      if (isSweep && roundComplete) {
        converged = true;
        log("Round " + round + ": full sweep found nothing; CONVERGED");
      } else if (isSweep) {
        log(
          "Round " +
            round +
            ": sweep found nothing but was incomplete; NOT converging (the next sweep re-runs " +
            "every lens, including the ones that failed)",
        );
        if (sweepStalled(round)) break;
      }
      continue;
    }

    let deduped = raw;
    if (raw.length > 1) {
      const d = await robustAgent(dedupPrompt(raw), {
        label: "r" + round + ":dedup",
        phase: LOOP.name + " R" + round + ": review",
        schema: DEDUP_FINDINGS,
      });
      const dd = findingsOf(d);
      if (dd.length > 0) deduped = dd;
    }
    log(
      "Round " +
        round +
        ": " +
        deduped.length +
        " findings after dedup; verifying",
    );

    // Sequential rather than parallel, and short-circuiting on the first
    // refusal. Materiality goes first because it is the cheap one -- it assumes
    // the evidence is true and reads only the proposal -- and because it is
    // instructed to default to refuted, so it kills the largest share for the
    // least cost. Evidence verification opens every cited file and is the
    // expensive one, and a finding materiality has already refused never
    // reaches it.
    //
    // Findings are still verified in parallel with each other; it is the two
    // skeptics on ONE finding that are now ordered.
    const verifySteps = {
      material: (f) =>
        robustAgent(materialityPrompt(f), {
          label: "r" + round + ":verify-material",
          phase: LOOP.name + " R" + round + ": verify",
          schema: VERDICT,
        }),
      evidence: (f) =>
        robustAgent(evidencePrompt(f), {
          label: "r" + round + ":verify-evidence",
          phase: LOOP.name + " R" + round + ": verify",
          schema: VERDICT,
        }),
    };
    const verdicts = await parallel(
      deduped.map((f) => async () => {
        if (!verifySequential) {
          const vs = await parallel([
            () => verifySteps[verifyOrder[0]](f),
            () => verifySteps[verifyOrder[1]](f),
          ]);
          // Same guard as the sequential path below, for the same reason: a
          // verifier that DIED is not a refusal. Without the flag the finding
          // fell through to `rejected` as "refuted by the unknown skeptic
          // because verifier unavailable", and every later lens of both loops
          // was told not to re-report it, so one outage suppressed a real
          // finding for the rest of the run. Measured on this workflow under
          // the harness: with verifySequential false and one dead verifier, the
          // finding was rejected in all four rounds.
          if (!vs[0] || !vs[1]) {
            return { f, vs: vs.filter(Boolean), refutedBy: null, dead: true };
          }
          // Name the skeptic that refused, as the sequential path does: "not
          // material" and "the evidence is wrong" are different signals to a
          // later round's lens. The first refuser wins, matching the order the
          // short circuit would have applied.
          return {
            f,
            vs,
            refutedBy: !vs[0].confirmed
              ? verifyOrder[0]
              : !vs[1].confirmed
                ? verifyOrder[1]
                : null,
          };
        }
        const first = await verifySteps[verifyOrder[0]](f);
        // A verifier that DIED is not a refusal. The finding reaches neither
        // confirmed nor rejected, and the round is marked incomplete, because
        // an outage must not be able to suppress a finding permanently.
        if (!first) return { f, vs: [], refutedBy: null, dead: true };
        if (!first.confirmed) return { f, vs: [first], refutedBy: verifyOrder[0] };
        const second = await verifySteps[verifyOrder[1]](f);
        if (!second) return { f, vs: [first], refutedBy: null, dead: true };
        return {
          f,
          vs: [first, second],
          refutedBy: second.confirmed ? null : verifyOrder[1],
        };
      }),
    );

    const live = verdicts.filter(Boolean);
    // Extend the completeness guard to verification: a verifier that failed after
    // retries leaves a finding with fewer than two verdicts, so it is neither
    // confirmed nor safely dismissed. Such a round cannot certify convergence.
    // A finding reached a terminal verdict when both skeptics confirmed, or
    // when the first refused: under the short circuit a refusal is terminal
    // with one verdict. A finding whose verifier died reached neither.
    const verifyComplete =
      live.length === deduped.length &&
      live.every((v) => !v.dead && (v.vs.length === 2 || v.refutedBy !== null));
    if (!verifyComplete) {
      roundComplete = false;
      verifiersFailed.push("r" + round);
      log(
        "Round " +
          round +
          ": some verifiers failed after retries; round INCONCLUSIVE",
      );
    }
    // A lens is retired when no finding of ITS OWN survived verification. That
    // reads as satisfied when verification never ran, so an outage retired the
    // very lenses that had just found the defects. The lens side already has
    // this guarantee -- a lens that failed its own retries is never retired --
    // and this is its counterpart for the stage that judges the lens's output.
    const verifyRan = verifyComplete;
    // Credit a later finding back to the mechanism it is about, so the strike table
    // the next fixer sees reflects which of its own inventions keep failing.
    const creditStrikes = (fs) => {
      for (const m of introducedMechanisms) {
        if (m.loop !== LOOP.name || m.round >= round) continue;
        const needle = String(m.name).toLowerCase();
        if (needle.length < 4) continue;
        for (const f of fs) {
          const hay = (f.title + " " + f.where + " " + f.claim + " " + f.why_wrong).toLowerCase();
          if (hay.includes(needle)) { m.strikes++; break; }
        }
      }
    };

    const confirmed = live
      .filter((v) => v.vs.length === 2 && v.vs.every((x) => x.confirmed))
      .map((v) => v.f);
    creditStrikes(confirmed);
    // A finding at a site an earlier round already rewrote means that attempt
    // did not hold. Marked BEFORE this round's own attempts are recorded, so a
    // round never rejects itself.
    markSitesRejected(confirmed, round);
    recordFindings(round, confirmed);
    // What the loop was still finding when it stopped. A budget-exhausted stop
    // that names nothing tells the operator only that it stopped.
    LOOP.lastFindings = confirmed.map((f) => f.title);
    live
      .filter((v) => !(v.vs.length === 2 && v.vs.every((x) => x.confirmed)))
      .forEach((v) => {
        // A finding whose verifier DIED is not refuted. It is carried: adding
        // it to `rejected` would suppress it in every later round on the
        // strength of an outage, which is the failure this guard exists for.
        if (v.dead) return;
        rejected.push({
          title: v.f.title,
          // Which skeptic refused it, because "not material" and "the evidence
          // is wrong" are different signals to a later round's lens.
          refutedBy: v.refutedBy || "unknown",
          reason:
            v.vs
              .filter((x) => !x.confirmed)
              .map((x) => x.reason)
              .join(" | ") || "verifier unavailable",
        });
      });
    log(
      "Round " +
        round +
        ": " +
        confirmed.length +
        "/" +
        deduped.length +
        " findings confirmed",
    );

    // Credit each surviving finding back to the lens or lenses that produced it.
    // A finding carries `lens` from the stamping above; a merged finding carries
    // `lenses`, the union the dedup step was asked to preserve.
    //
    // Every name is checked against the lenses that actually ran this round,
    // because a name from outside that set is not attribution to some other
    // reviewer: it is attribution to nobody, and the lens that did find the
    // defect then reads as having produced nothing and retires. Measured on this
    // loop with a dedup agent returning `citation-audit` for `citations`: a round
    // that confirmed two findings retired 12 of the 14 lenses, including the one
    // whose finding had just been confirmed and fixed, and the next round ran 2
    // reviewers. The fallback below did not catch it, because one wrong name
    // among correct ones leaves the survivor set non-empty.
    const known = new Set(lenses.map((l) => l.key));
    const survivors = new Set();
    let unattributed = 0;
    for (const f of confirmed) {
      const tags = (
        Array.isArray(f.lenses) && f.lenses.length > 0
          ? f.lenses
          : f.lens
            ? [f.lens]
            : []
      ).filter((t) => known.has(t));
      if (tags.length === 0) unattributed++;
      tags.forEach((t) => survivors.add(t));
    }
    // Attribution fails when the dedup model drops the tags while merging, and
    // when it returns a name no lens in this round carries. Either way the
    // findings it lost can retire nobody, so fall back to the weaker but safe
    // rule (retire only a lens that reported nothing) and say so. The condition
    // is on the unattributed findings rather than on an empty survivor set, so a
    // near miss triggers it as well as a total loss.
    if (unattributed > 0) {
      log(
        "Round " +
          round +
          ": " +
          unattributed +
          "/" +
          confirmed.length +
          " confirmed finding(s) carry no lens this round produced; falling back to retiring only lenses that reported nothing",
      );
      lenses.forEach((l, i) => {
        if (findingsOf(lensResults[i]).length > 0) survivors.add(l.key);
      });
    }
    if (!verifyRan) {
      log(
        "Round " + round + ": verification did not complete, so no lens is retired on its result",
      );
    }
    applyRetirement(
      lenses,
      lensResults,
      // An incomplete verification is not evidence that a lens found nothing
      // worth keeping. Treating every lens as a survivor leaves the pool intact
      // for a round that can actually judge it.
      verifyRan ? survivors : new Set(lenses.map((l) => l.key)),
      round,
      verifyRan
        ? "no finding of its own survived verification"
        : "verification did not complete this round",
    );

    history.push({
      loop: LOOP.name,
      round,
      sweep: isSweep,
      lenses: lenses.map((l) => l.key),
      raw: raw.length,
      deduped: deduped.length,
      confirmed: confirmed.length,
      confirmedTitles: confirmed.map((f) => f.title),
      complete: roundComplete,
      retiredAfter: [...retired],
    });

    if (confirmed.length === 0) {
      roundComplete = (await closeRound(round, roundComplete)) && roundComplete;
      history[history.length - 1].complete = roundComplete;
      if (isSweep && roundComplete) {
        converged = true;
        log(
          "Round " +
            round +
            ": full sweep produced no confirmed findings; CONVERGED",
        );
      } else if (isSweep) {
        log(
          "Round " +
            round +
            ": sweep incomplete (reviewer, verifier, or bookkeeping failures); NOT converging",
        );
        if (sweepStalled(round)) break;
      }
      continue;
    }

    // Strike table: mechanisms this loop introduced in earlier rounds, with the
    // number of later findings each has caused. The loop already has this
    // information and has never used it, so a fixer repairing a mechanism for the
    // third time has been doing so blind.
    const strikeLines = introducedMechanisms
      .filter((m) => m.loop === LOOP.name && m.strikes > 0)
      .map((m) => "- " + m.name + " (introduced round " + m.round + "): " + m.strikes + " later finding(s)")
      .join("\n");
    const preFixSnap = await snapshot("r" + round + "-prefix");

    // ---- What else each fix would falsify ---------------------------------
    //
    // Runs BEFORE grouping so the planner can group by site overlap: two
    // findings that touch the same passage belong in one group, and splitting
    // them produces two designs for one piece of text. Only confirmed findings
    // are expanded, so a refuted one costs nothing.
    await expandSites(confirmed, round);

    // ---- Plan the split ---------------------------------------------------
    //
    // One fixer for every confirmed finding was the loop's largest source of
    // its own defects: findings that share a root produce edits that
    // contradict each other, and a fixer holding twenty findings at once
    // reads none of them closely. The findings are split into cohesive
    // groups, each group gets a design, and each design gets its own fixer.
    //
    // The ONLY cap is on the number of groups. Group size is deliberately
    // unbounded: forty trivial citation corrections that share a subject
    // belong in one group, where the design stage triages them in a handful
    // of tokens and one fixer applies them consistently, while three deep
    // design findings belong in three groups however few they are. A size cap
    // would split the first case for no reason, would not help the second,
    // and combined with a group cap would silently bound how many findings a
    // round can fix at all.
    let groups = [{ id: "G1", title: "all findings", findings: confirmed.map((_, i) => i), order: 1 }];
    if (confirmed.length > 1) {
      const planned = await robustAgent(fixPlanPrompt(confirmed, round), {
        label: "r" + round + ":fix-plan",
        phase: LOOP.name + " R" + round + ": fix",
        schema: FIX_PLAN,
      });
      // A planner that drops a finding loses it silently, so the partition is
      // checked rather than trusted. Any violation falls back to one group of
      // everything, which is the old behaviour and is safe.
      //
      // The index test is Number.isInteger rather than a typeof check because
      // typeof 1.5 and typeof NaN are both "number". Findings [0, 1.5, 2] over
      // three confirmed findings passed the old guard, logged "3 finding(s)
      // split into 1 group(s): G1(3)", and reached the fixer as
      // confirmed[1.5] === undefined, dropped by the filter(Boolean) below.
      // Two findings were fixed and the third was neither fixed, nor logged,
      // nor rejected, while the round's own record claimed all three.
      const seen = [];
      let ok = !!(planned && Array.isArray(planned.groups) && planned.groups.length > 0);
      if (ok) {
        for (const g of planned.groups) {
          for (const i of g.findings || []) {
            if (!Number.isInteger(i) || i < 0 || i >= confirmed.length || seen.includes(i)) ok = false;
            else seen.push(i);
          }
        }
        if (seen.length !== confirmed.length) ok = false;
      }
      if (ok && planned.groups.length > maxFixGroups) {
        log(
          "Round " + round + ": the planner returned " + planned.groups.length +
            " groups against a cap of " + maxFixGroups + "; merging the tail",
        );
        const head = planned.groups.slice(0, maxFixGroups - 1);
        const tail = planned.groups.slice(maxFixGroups - 1);
        head.push({
          id: "G" + maxFixGroups,
          title: "merged tail",
          rationale: "the planner exceeded the group cap; these were combined",
          findings: tail.flatMap((g) => g.findings || []),
          order: Math.max(0, ...head.map((g) => (Number.isInteger(g.order) ? g.order : 0))) + 1,
        });
        planned.groups = head;
      }
      // `order` is the planner's second statement of the fix sequence; its
      // first is the array itself, and the prompt promises "Fixing happens in
      // the order you give". When the field is well formed the two agree and
      // the sort is a no-op. When it is not, the array order is the statement
      // worth keeping, because the alternative is what was measured here:
      // orders 1, 2, -5 ran the planner's last group FIRST, an absent order
      // became 0 through `a.order || 0` and did the same, and three groups all
      // at order 1 left the sequence to stable-sort array position while the
      // log claimed the planner had ordered them. Destroying the anchors a
      // later group needs is exactly what the field exists to prevent, so a
      // broken one is reported rather than obeyed.
      let orderOk = true;
      const orders = [];
      for (const g of (ok ? planned.groups : [])) {
        if (!Number.isInteger(g.order) || g.order < 1 || orders.includes(g.order)) orderOk = false;
        else orders.push(g.order);
      }
      if (ok) {
        groups = orderOk
          ? planned.groups.slice().sort((a, b) => a.order - b.order)
          : planned.groups.slice();
        if (!orderOk) {
          log(
            "Round " + round + ": the fix planner's group order is not a distinct 1-based " +
              "sequence; fixing the groups in the order they were returned",
          );
        }
        log(
          "Round " + round + ": " + confirmed.length + " finding(s) split into " +
            groups.length + " group(s): " + groups.map((g) => g.id + "(" + (g.findings || []).length + ")").join(" "),
        );
      } else {
        log(
          "Round " + round + ": the fix planner did not return a clean partition of the findings; " +
            "falling back to one group of all " + confirmed.length,
        );
      }
    }

    // ---- Design each group, then fix it -----------------------------------
    //
    // Design runs in parallel across groups (nothing is being edited yet) and
    // fixing runs sequentially (the groups edit the same files, and concurrent
    // edits to one markdown file lose writes). A design that dies leaves its
    // group to the old fixer brief, which is safe, and marks the group so the
    // introspection pass can see a run whose design stage keeps failing.
    const designs = await parallel(
      groups.map((g) => () =>
        robustAgent(fixDesignPrompt(g, confirmed, round), {
          label: "r" + round + ":fix-design:" + g.id,
          phase: LOOP.name + " R" + round + ": fix",
          schema: FIX_DESIGN,
        }),
      ),
    );
    // A design result carrying no designs is not a design. Both branches below
    // and the fixer's own design block are gated on the object being truthy, so
    // `{"designs": []}` -- which the schema permits, having no minimum -- took
    // the design path: an observed run handed a fixer "THE DESIGN FOR THIS
    // GROUP ... APPLY IT. Your scope for design decisions is narrow here" above
    // an empty array, and logged nothing. Emptying it to null here puts it on
    // the path the null case already took, so it is logged, the fixer is told
    // to design the group itself, and a round whose designs all came back empty
    // does not spend a reconciliation agent on nothing.
    for (let i = 0; i < designs.length; i++) {
      if (!(designs[i] && (designs[i].designs || []).length)) designs[i] = null;
    }
    const designless = groups.filter((_, i) => !designs[i]).map((g) => g.id);
    if (designless.length) {
      log(
        "Round " + round + ": no design returned for " + designless.join(", ") +
          "; those groups are fixed from the findings alone",
      );
      history[history.length - 1].designless = designless;
    }

    // Reconcile the parallel designs before any of them is applied. Skipped for
    // a single group, where there is nothing to reconcile.
    if (groups.length > 1 && designs.some(Boolean)) {
      const rec = await robustAgent(
        "Reconcile the designs for one round's fix groups against each other, before any of them is " +
          "applied.\n\n" +
          READ_ONLY +
          "\n\nPROPOSAL: " + P.dir + ". Loop: " + LOOP.name + ". Round " + round + ".\n\n" +
          "WHY YOU ARE HERE. Each group's design was produced in parallel by an agent that could not see " +
          "the others. They were designed against the same unchanged text, so two of them can plan " +
          "incompatible edits to it and neither knows. The fixers run one after another, so the second " +
          "would apply a design written against text the first has already rewritten.\n\n" +
          "FIND GENUINE CONFLICTS ONLY. Two designs that touch the same SECTION are not in conflict; two " +
          "that touch the same STATEMENT with different intent are. So are these:\n" +
          "  one design deletes or renames something another's design anchors on;\n" +
          "  two designs state the same rule, predicate, or identifier differently;\n" +
          "  one design's cascades name a section another design rewrites;\n" +
          "  two designs each add a mechanism, and one mechanism would serve both.\n\n" +
          "RESOLVE, do not just report. For each conflict, say which design survives and why, or how the " +
          "two merge, and return the REVISED designs for the groups you changed. Omit a group you left " +
          "alone: returning one unchanged in `revised` is noise the fixer has to reconcile against its " +
          "original.\n" +
          "  A group you do return carries an entry for EVERY finding in it, with `findingTitle` copied " +
          "exactly from the design you were given, because an entry is matched to the design it replaces " +
          "by that title. A finding you omit keeps the design it already had.\n" +
          "  Use the group ids exactly as they appear below. A `groupId` naming no group in this round " +
          "is dropped, and the resolution you wrote for it is lost.\n\n" +
          "PREFER THE SMALLER RESULT. Where two designs each add something and one addition would serve " +
          "both, merge them and say so; that is the most valuable outcome here, because two mechanisms " +
          "doing nearly the same thing is the hair this loop exists to prevent.\n\n" +
          "If the groups must be fixed in a different order for a design to still apply, say so in " +
          "orderNote.\n\n" +
          "THE GROUPS AND THEIR DESIGNS:\n" +
          JSON.stringify(
            groups.map((g, i) => ({ id: g.id, title: g.title, sharedSubject: g.sharedSubject, design: designs[i] })),
            null,
            2,
          ) +
          promptFor("fix-design-reconcile"),
        {
          schema: DESIGN_RECONCILE,
          label: "r" + round + ":fix-design-reconcile",
          phase: LOOP.name + " R" + round + ": fix",
        },
      );
      if (rec) {
        // A revision used to REPLACE the group's whole designs array, so a
        // reconciler that returned only the design it had changed deleted the
        // design of every other finding in that group. Reproduced on a
        // two-finding group: the second finding's design vanished, its fixer
        // was still told "APPLY IT ... you are applying a design rather than
        // inventing one" over a design covering the other finding, and the
        // in-scope sites that design had adjudicated dropped out of the
        // post-fix review checklist. Merging by findingTitle makes a partial
        // array revise what it names and leave the rest standing.
        const unknownGroups = [];
        let applied = 0;
        for (const r of rec.revised || []) {
          const gi = groups.findIndex((g) => g.id === r.groupId);
          if (gi < 0) {
            unknownGroups.push(String(r.groupId));
            continue;
          }
          // An empty `designs` here is the same empty mandate arriving by a second
          // door: it would replace a design the fixer needs with nothing while
          // still reading as one. A revision that revises nothing is ignored.
          if (!(r.designs || []).length) continue;
          const merged = ((designs[gi] || {}).designs || []).slice();
          for (const one of r.designs) {
            const at = merged.findIndex((d) => d && d.findingTitle === one.findingTitle);
            if (at >= 0) merged[at] = one;
            else merged.push(one);
          }
          designs[gi] = { ...(designs[gi] || {}), designs: merged };
          applied++;
        }
        // An unknown group id was dropped in silence while the log counted it
        // as revised, so a resolution the reconciler wrote reached no fixer
        // and nothing recorded that the conflict it settled is still open.
        if (unknownGroups.length) {
          log(
            "Round " + round + ": the design reconciliation revised " + unknownGroups.length +
              " group(s) this round does not have (" + unknownGroups.join(", ") +
              "); those revisions were dropped and their conflicts stand unresolved",
          );
          history[history.length - 1].designRevisionsDropped = unknownGroups;
        }
        if ((rec.conflicts || []).length) {
          log(
            "Round " + round + ": the design reconciliation found " + rec.conflicts.length +
              " conflict(s) between groups and applied " + applied + " revised design(s)",
          );
          history[history.length - 1].designConflicts = rec.conflicts;
        }
        if (rec.orderNote) {
          history[history.length - 1].designOrderNote = rec.orderNote;
          log("Round " + round + ": reconciliation note on group order — " + String(rec.orderNote).slice(0, 160));
        }
      } else {
        log("Round " + round + ": the design reconciliation did not return; applying the designs as written");
      }
    }

    const fixSummaries = [];
    const roundMechanisms = [];
    const escalatedAll = [];
    const designRejected = [];
    for (let gi = 0; gi < groups.length; gi++) {
      const g = groups[gi];
      const picked = (g.findings || []).map((i) => confirmed[i]).filter(Boolean);
      if (picked.length === 0) continue;
      const out = await robustAgent(
        fixPrompt(picked, round, strikeLines || null, g, designs[gi], fixSummaries),
        {
          label: "r" + round + ":fix:" + g.id,
          phase: LOOP.name + " R" + round + ": fix",
          schema: FIX_RESULT,
        },
      );
      if (!out) {
        // The group's confirmed findings are still in the text, and no later
        // stage re-opens them: nothing carries them forward and the lens that
        // raised them may retire on the next round it reports nothing. So the
        // round is incomplete and the loop may not certify convergence.
        log(
          "Round " + round + ": the fixer for " + g.id + " did not return; its " +
            picked.length + " confirmed finding(s) were never edited",
        );
        fixersFailed.push("r" + round + ":" + g.id);
        roundComplete = false;
        // Written here rather than after the group loop because the entry for
        // this round was pushed before the fix stage ran and still carries the
        // value roundComplete had then.
        const h = history[history.length - 1];
        h.complete = false;
        h.fixersFailed = (h.fixersFailed || []).concat(g.id);
        continue;
      }
      fixSummaries.push(g.id + ": " + (out.summary || ""));
      // The findings this group actually closed. Credited per group rather than
      // per round because a group whose fixer died `continue`s above with its
      // findings untouched. Before this the only push into fixedTitles was the
      // post-fix reviewer's own complaints, so one measured run reported 0
      // findings fixed after ten were, and every later lens was handed a
      // "do not re-litigate" list naming nothing that had been fixed.
      picked.forEach((f) => {
        fixedTitles.push(f.title);
        roundFixedTitles.push(f.title);
      });
      for (const m of out.newMechanisms || []) {
        roundMechanisms.push(m);
        // The strike table was READ in six places and written in none, so every
        // consumer of it -- creditStrikes, the fixer's strike lines, the churn
        // detector's mechanism arm, and the mechanism block in every
        // introspection and judge prompt -- had always been inert.
        // Loop-scoped for the same reason siteHistory is: `round` restarts at 1
        // in each loop and this array is module-level, so an unscoped entry
        // from spec round 7 starts accruing strikes at non-spec round 8, from
        // findings that have nothing to do with it, under a prompt header
        // saying "MECHANISMS THIS LOOP'S OWN FIXER INVENTED".
        if (m && m.name) {
          introducedMechanisms.push({
            name: String(m.name),
            loop: LOOP.name,
            round,
            why: m.why || "",
            strikes: 0,
          });
        }
      }
      for (const e of out.escalated || []) escalatedAll.push(e);
      for (const d of out.designRejected || []) designRejected.push(g.id + ": " + d);
    }
    const fixOut = {
      summary: fixSummaries.join("\n\n"),
      newMechanisms: roundMechanisms,
      escalated: escalatedAll,
    };
    const fixSummary = fixOut.summary;
    if (designRejected.length) {
      // A fixer that silently substitutes its own design is the failure this
      // whole stage exists to remove, so the substitution is surfaced.
      log(
        "Round " + round + ": " + designRejected.length +
          " design(s) the fixer judged wrong and departed from",
      );
      history[history.length - 1].designRejected = designRejected;
    }
    history[history.length - 1].groups = groups.map((g) => ({
      id: g.id,
      title: g.title,
      size: (g.findings || []).length,
      risk: g.risk,
    }));

    // The sites the designs actually adopted, which is what the post-fix review
    // checks the fixer against. A site the design ruled out is not checked: the
    // fixer was told not to touch it.
    const inScopeSites = [];
    let inScopeOutOfBounds = 0;
    for (const d of designs || []) {
      for (const one of (d && d.designs) || []) {
        for (const sd of one.siteDispositions || []) {
          if (sd.disposition !== "in-scope") continue;
          // A design may adjudicate a TREE site `in-scope`, and the fixer's HARD
          // CONSTRAINT forbids editing it. Passing it on would tell the post-fix
          // reviewer that an unedited site is a CONFIRMED drift finding against
          // a file nothing in this loop may write, which is the same false
          // finding a misclassified path produces. The remedy for such a site is
          // an edit the PROPOSAL is missing, which the tree list already carries.
          if (!isProposalSite(sd.file)) {
            inScopeOutOfBounds++;
            continue;
          }
          inScopeSites.push({ finding: one.findingTitle, file: sd.file, line: sd.line, quote: sd.quote, why: sd.why });
        }
      }
    }
    if (inScopeSites.length) {
      log("Round " + round + ": " + inScopeSites.length + " related site(s) adopted into this round's fixes");
    }
    if (inScopeOutOfBounds) {
      log(
        "Round " + round + ": " + inScopeOutOfBounds +
          " site(s) adjudicated in-scope lie outside " + P.root + " and are not the fixer's to edit; " +
          "they are not checked as drift",
      );
    }
    history[history.length - 1].sitesAdopted = inScopeSites.length;
    // This round's attempts, so a later round designing at the same location is
    // shown what was tried here and how it fared.
    recordSiteAttempts(confirmed, round, designs);
    const repeated = confirmed.filter((f) => siteHistoryFor(f, round).length >= 2);
    if (repeated.length) {
      log(
        "Round " + round + ": " + repeated.length + " location(s) now rewritten three or more times (" +
          repeated.map((f) => f.where).join("; ") + ")",
      );
      history[history.length - 1].repeatSites = repeated.map((f) => f.where);
    }

    // Narrow post-fix review of the fixer's own edits, then at most ONE follow-up
    // fix. The cap is deliberate: this is a correction pass on fresh text, not a
    // second convergence loop, and an unbounded review-fix cycle here would hide a
    // genuinely contested edit inside a round instead of surfacing it to the next
    // round's lenses and, ultimately, to the sweep.
    const postFix = await robustAgent(
      postFixPrompt(confirmed, fixSummary, round, roundMechanisms, preFixSnap, inScopeSites),
      {
        label: "r" + round + ":post-fix-review",
        phase: LOOP.name + " R" + round + ": fix",
        schema: FINDINGS,
      },
    );
    const postFixFindings = findingsOf(postFix);
    if (!postFix) {
      log("Round " + round + ": post-fix review unavailable after retries");
      history[history.length - 1].postFixReview = "unavailable";
    } else if (postFixFindings.length === 0) {
      log("Round " + round + ": post-fix review found no defect in the fixer's work");
      history[history.length - 1].postFixReview = "clean";
    } else {
      log(
        "Round " +
          round +
          ": post-fix review found " +
          postFixFindings.length +
          " defect(s) in the fixer's own edits; correcting",
      );
      const followUp = await robustAgent(
        followUpFixPrompt(postFixFindings, round),
        { label: "r" + round + ":follow-up-fix", phase: LOOP.name + " R" + round + ": fix" },
      );
      // Only a follow-up that returned corrected anything, so only then does the
      // defect count as fixed. Recording it unconditionally told the next
      // round's lenses that the current text reflects a correction a dead fixer
      // never made.
      if (followUp) postFixFindings.forEach((f) => fixedTitles.push(f.title));
      // Recorded in history either way, so a run where the fixer repeatedly
      // needed correction is visible whether or not the correction landed.
      history[history.length - 1].postFixReview = postFixFindings.map(
        (f) => f.title,
      );
      history[history.length - 1].followUpFixSummary =
        followUp || "follow-up fixer unavailable";
    }

    // Churn test, after the round's fixes have landed. Running it here rather than
    // before the fixes means the decision is taken on the text the next round will
    // actually read.
    // The counters wake the introspection agent; they no longer act on their own.
    // A counter cannot tell a churning mechanism from a large area being drained,
    // and cannot see over-specification at all, so its output is a reason to look
    // rather than a decision. The agent also runs on a cadence, because a runaway
    // between counter trips would otherwise go unexamined.
    const churn = churningAreas(round);
    if (churn.length) history[history.length - 1].churnDetected = churn.map((c) => c.area);
    const dueByCadence = round - lastIntrospectRound >= introspectEvery;
    const dueBySweep = isSweep && confirmed.length > 0;
    if (churn.length || dueByCadence || dueBySweep) {
      const why = churn.length
        ? "a churn counter tripped on " + churn.map((c) => c.area).join(", ")
        : dueBySweep
          ? "a full sweep confirmed findings, which is when the loop learns most about itself"
          : introspectEvery + " rounds since the last introspection";
      const pass = await introspect(round, why, churn, dueByCadence);

      // EVERY verdict goes to a panel now, including `healthy`. A wrong healthy
      // is the most expensive verdict in the loop -- it spends every remaining
      // round -- and nothing was checking it. A gated pass is the exception: it
      // made no argument, so there is nothing to falsify.
      let verdict = pass;
      if (pass && !pass.gated) {
        const panel = await judgePanel(round, pass, await growthSince(lastGrowthSnap), churn);
        history[history.length - 1].panel = {
          proposed: pass.verdict,
          decision: panel.decision,
          upheld: panel.upheld,
          undirected: !!panel.undirected,
          votes: panel.votes.map((v) => ({
            falsified: v.falsified,
            howConclusive: v.howConclusive,
            reasoning: String(v.reasoning || "").slice(0, 400),
          })),
        };
        if (!panel.upheld) {
          // Falsified. Recorded against the pass so the next one sees that it
          // reached this verdict on evidence like today's and was overturned,
          // and must answer that rather than restate itself.
          overruledStops.push({
            round,
            proposed: pass.verdict,
            decidedInstead: panel.decision,
            panelReasoning: panel.votes
              .filter((v) => v.falsified)
              .map((v) => v.howConclusive + ": " + v.reasoning),
          });
          verdict = { ...pass, verdict: panel.decision };
          if (panel.undirected) {
            // The panel refuted the pass and named nothing to do instead, so the
            // loop continues on a verdict no judge endorsed. The design's claim
            // that a wrong continue self-corrects holds only if the next pass
            // actually runs, and on the cadence alone it is introspectEvery
            // rounds away (measured: with introspectEvery 2 the passes fell on
            // rounds 2 and 4, so an undirected refutation at round 2 went
            // unexamined for two more rounds of fixing).
            lastIntrospectRound = round - introspectEvery;
          }
        }
      }

      if (verdict && verdict.verdict === "redesign" && redesignsRun < redesignsAllowed) {
        const named = (verdict.areas || []).map((a) => {
          const hit = churn.find((c) => c.area === String(a).toLowerCase().trim());
          return (
            hit || {
              area: String(a).toLowerCase().trim(),
              findings: 0,
              designDefects: 0,
              selfInflicted: 0,
              reason: verdict.reasoning || "named by the introspection pass",
            }
          );
        });
        if (named.length) {
          const did = await runRedesign(named, round, verdict.reasoning || why);
          if (did) {
            // The document in front of the lenses is materially different, so no
            // lens may stay retired on the strength of having read the old one.
            retired.clear();
            history[history.length - 1].redesignApplied = true;
          }
        }
      } else if (verdict && verdict.verdict === "redesign") {
        log(
          "Round " + round + ": introspection asked for a redesign but the budget of " +
            redesignsAllowed + " is spent; recording instead",
        );
      }

      if (verdict && verdict.verdict === "prune" && (verdict.sections || []).length) {
        // Over-specification is a defect in its own right: it is where two sections
        // drift apart, and detail an implementor does not need costs more to keep
        // true than it is worth. The cure is deletion with a stated constraint,
        // which is what the blanks convention exists for.
        //
        // A section already pruned is dropped rather than re-commissioned, and the
        // whole verdict is bounded by a budget, for the reason the redesign branch
        // above is: measured with introspectEvery 1, a pass naming "## 3. Design"
        // reached the same verdict in four consecutive rounds, and the loop paid
        // for four deletions of one section and four full 13-lens rounds behind
        // them, reaching no sweep and converging on nothing.
        const key = (s) => String(s).toLowerCase().trim();
        const fresh = (verdict.sections || []).filter((s) => !prunedSections.has(key(s)));
        if (fresh.length === 0) {
          log(
            "Round " + round + ": the prune named only section(s) this run has already pruned (" +
              (verdict.sections || []).join("; ").slice(0, 120) + "); not pruning again",
          );
          history[history.length - 1].pruneRepeated = verdict.sections;
        } else if (prunesRun >= prunesAllowed) {
          log(
            "Round " + round + ": introspection asked for a prune but the budget of " +
              prunesAllowed + " is spent; recording instead",
          );
          history[history.length - 1].pruneDeclined = fresh;
        } else {
          prunesRun++;
          await robustAgent(
            "Prune over-specified sections of a change proposal.\n\n" +
              "HARD CONSTRAINT: the only file you may edit is " + path +
              ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
              "An introspection pass judged these sections to have grown past their value:\n" +
              JSON.stringify(fresh, null, 2) +
              "\n\nIts reasoning: " + (verdict.reasoning || "") +
              "\n\nDelete the detail it names and replace each deletion with the blanks convention: " +
              FORMAT_BLANKS +
              "\nDelete nothing the convention bars from delegation, and nothing another section depends on: " +
              "check before each deletion whether any other part of the proposal cites the text you are removing, " +
              "and if it does, either keep it or update the citing section in the same edit. Then reconcile the " +
              "implementation checklist, the files-touched section, and the testing section with what is left.\n\n" +
              'Append a bullet to the "Resolved in adversarial review" section recording what was pruned and why. ' +
              "Follow " + repo + "/.claude/rules/doc-style.md.",
            { label: "prune:r" + round, phase: LOOP.name + " R" + round + ": prune" },
          );
          for (const s of fresh) prunedSections.add(key(s));
          pruneHistory.push({
            tag: prunesRun,
            loop: LOOP.name,
            round,
            sections: fresh,
            why: verdict.reasoning || "",
          });
          history[history.length - 1].pruned = fresh;
          // Pruned text is text the lenses have not read in its new form, and the
          // prune agent is told to update every section that cited it, which reaches
          // the checklist, the files-touched list, and the testing section. No lens
          // may certify that on the strength of a pre-prune read. The budget above is
          // what makes this bounded: before it existed, a pass that reached the same
          // verdict every round cleared the retirement set every round, and the loop
          // could never drain toward a sweep.
          retired.clear();
          log("Round " + round + ": pruned " + fresh.length + " section(s)");
        }
      }

      if (verdict && (verdict.verdict === "halt" || verdict.verdict === "reframe")) {
        // The panel above has already had its chance to falsify this and could
        // not, so the stop stands.
        stoppedByIntrospection = {
          round,
          loop: LOOP.name,
          verdict: verdict.verdict,
          proposedBy: pass.verdict,
          question: verdict.questionForHuman || verdict.reasoning,
          reasoning: verdict.reasoning,
          caseHealthy: verdict.caseHealthy,
          caseUnhealthy: verdict.caseUnhealthy,
          // What the next run should be. A stop that says only to stop leaves a
          // human the work of deciding what to do next, which is work the pass
          // is best placed to do: it has just read the whole run.
          nextSteps: verdict.nextSteps || null,
          panel: (history[history.length - 1].panel || {}).votes || [],
        };
        log(
          "Round " + round + ": stopping with " + verdict.verdict +
            (verdict.nextSteps
              ? " — next steps are " + (verdict.nextSteps.confidence || "unstated") + ": " +
                String(verdict.nextSteps.summary || "").slice(0, 160)
              : " — the pass proposed no next steps"),
        );
      }
    }

    roundComplete = (await closeRound(round, roundComplete)) && roundComplete;
    // The stop breaks HERE rather than at the verdict, so the halting round
    // closes through the same statement every other round does and the two
    // cannot drift apart again. Measured: a halt in round 2 ran no
    // round-boundary call at all, leaving its shards unmerged and the next
    // launch without a state file or a snapshot.
    if (stoppedByIntrospection) break;
  }

  LOOP.round = round;
  if (fixersFailed.length) {
    log(
      "The " + cfg.name + " loop lost the fixer for " + fixersFailed.join(", ") +
        "; those confirmed findings were never edited, so the loop does not certify convergence",
    );
  }
  LOOP.converged =
    converged &&
    !reviewersFailed &&
    fixersFailed.length === 0 &&
    verifiersFailed.length === 0 &&
    !stoppedByIntrospection;
  LOOP.sweeps = sweeps;
  LOOP.reviewersFailed = reviewersFailed;
  LOOP.verifiersFailed = verifiersFailed;
  if (verifiersFailed.length) {
    log(
      "The " + cfg.name + " loop could not verify in round(s) " + verifiersFailed.join(", ") +
        ", so it does not certify convergence over findings nothing judged",
    );
  }
  LOOP.fixersFailed = fixersFailed;
  log(
    "The " + cfg.name + " loop " +
      (LOOP.converged ? "converged" : "did NOT converge") +
      " after " + round + " round(s) and " + sweeps + " sweep(s)",
  );
  return LOOP;
}

// ---- Run the two loops ---------------------------------------------------
//
// Spec first. A finding in the spec loop must have a fix that lands in the
// staged spec edits; one whose only remedy is elsewhere is out of scope there
// and belongs to the non-spec loop, which reads both change files together.
//
// The spec loop is SKIPPED when the proposal stages no spec edits, which is a
// common and valid shape. A cheap probe decides that rather than the script,
// because the script cannot read a file.

// The problem statement is editable, and bounded. Two different acts wear the
// same shape, and only one of them belongs to a fixer:
//
//   CORRECTING THE RECORD -- a false citation, a drifted line number, an
//   evidence claim the tree refutes, a premise the review has since knocked
//   down. This is required. A smoke run produced ten findings whose location
//   was the problem statement, one of them ONLY there, and a fixer that could
//   not touch it left the summary corrected and the problem statement still
//   asserting the thing that was refuted. The fix created the drift.
//
//   CHANGING THE QUESTION -- restating what the problem IS, widening or
//   narrowing its scope, abandoning the framing. That is a `reframe`, it goes
//   through the introspection pass and its panel, and a fixer must not do it
//   in the course of closing a finding.
const PROBLEM_STATEMENT_RULE =
  "  " + P.problem + " — CORRECT THE RECORD here, and only that. A false citation, a line number that has " +
  "drifted, an evidence claim the tree refutes, or a premise this review has knocked down is a defect in " +
  "the problem statement and you fix it there, in the same edit as the section that restates it: leaving " +
  "the two disagreeing is worse than leaving both wrong. You may NOT change what the problem IS, widen or " +
  "narrow its scope, or abandon its framing. That is a reframe, it is the introspection pass's decision " +
  "and not yours, and if a finding seems to need one, say so in your summary and close what you can.\n";

// The spec loop repairs what its own edits falsify in the non-spec staging, and
// nothing else there.
//
// A run measured before this existed left non-spec-changes.md untouched for four
// and a half hours carrying claims that superseded spec decisions had already
// made false, while the summary's watch-out section grew into a nine-hundred-word
// errata list PROMISING those corrections. That list was not a fixer
// misbehaving. It was a correct-but-unwritable correction finding the only
// writable surface it had. The remedy is to let the correction land where it
// belongs, bounded tightly enough that the spec loop does not begin authoring
// the other lane's staging.
//
// This is the same rule the problem statement already carries, and the same one
// that makes the summary editable here: repair the consequence of your own edit
// in the same edit, and do not re-scope. The checklist is deliberately NOT
// included; its drift stays with the handoff.
const NON_SPEC_CONSEQUENCE_RULE =
  "  " + P.nonSpec + " — REPAIR ONLY WHAT YOUR OWN EDIT FALSIFIED, and only where the file ALREADY HAS " +
  "CONTENT beyond its headings. When a spec edit you just made leaves a statement there contradicting the " +
  "spec staging — a deliverable id you removed that it still references, a predicate you changed that it " +
  "still states the old way, an identifier you renamed — correct it in the SAME edit. Leaving the two " +
  "disagreeing is worse than leaving both wrong.\n" +
  "    THE TRIGGER IS ALWAYS A SPEC FINDING. Never go looking for problems in that file. The only thing " +
  "that opens it is an edit of yours having falsified something already written there.\n" +
  "    YOU MAY NOT AUTHOR. Do not add a staged code, schema, chart, docs or test change because your spec " +
  "edit implies one is needed, however plainly it does. That is the next loop's work, and content written " +
  "under a spec lens would reach sign-off having never been reviewed under a non-spec one. Do not correct " +
  "something wrong there on its own terms that your edit neither caused nor repaired: that is a finding " +
  "for the loop that follows. Do not improve wording, restructure, or fill a gap.\n" +
  "    WHEN THE FILE IS EMPTY there is nothing to repair, whatever your edit implies about the work it " +
  "will need. Name that consequence in your summary and leave the file alone.\n";

const SPEC_EDITABLE =
  "You may edit ONLY these files:\n" +
  "  " + P.spec + " — the staged spec edits, which is what this loop converges\n" +
  PROBLEM_STATEMENT_RULE +
  "  " + P.summary + " — because its deliverable index resolves the SPEC ids this loop adds and removes, " +
  "and a loop that may not touch it leaves its own edits mis-indexed until the next one. THE INDEX, AND " +
  "STATEMENTS YOUR OWN EDITS FALSIFY, AND NOTHING ELSE. Do not accumulate corrections owed to other " +
  "files as prose here: a measured run grew a nine-hundred-word errata list in this file's watch-out " +
  "section, promising fixes no lane owned, because this was the only surface it could write. It no " +
  "longer is. Repair what the rule below lets you repair, and record the rest as a `DEFERRED` line in " +
  "your log shard, where the pass between the loops will close it.\n" +
  NON_SPEC_CONSEQUENCE_RULE +
  "  " + P.log + " — your log shard\n" +
  "Every other file in the proposal, including the implementation checklist, and every file outside it, " +
  "is out of bounds.";

const NONSPEC_EDITABLE =
  "You may edit ONLY these files:\n" +
  "  " + P.nonSpec + " — the staged code, schema, chart, migration, docs and test changes\n" +
  PROBLEM_STATEMENT_RULE +
  "  " + P.checklist + " — the implementation sequence\n" +
  "  " + P.summary + " — so it stays true as the staging changes\n" +
  "  " + P.log + " — your log shard\n" +
  (lockSpecChanges
    ? "  " + P.spec + " is LOCKED for this run. A finding whose only remedy is a spec edit is closed by " +
      "recording an open decision in " + P.nonSpec + " with the constraint any answer must satisfy, which " +
      "is a complete fix rather than a deferral."
    : "  " + P.spec + " — permitted, but PREFER any resolution that does not touch it. The staged spec " +
      "edits converged in an earlier loop and every edit here reopens that. When you do edit it, say so " +
      "plainly in your summary so the round records it.");

const SPEC_SCOPE_NOTE =
  "\n\nSCOPE OF THIS LOOP. You are reviewing the STAGED SPEC EDITS in " + P.spec + ". Read whatever else " +
  "you need — the problem statement, the non-spec staging, the checklist — but report only findings whose " +
  "fix lands in the staged spec edits. A finding whose only remedy is a code, test, or docs change is out " +
  "of scope here and the loop that follows this one owns it; raising it now costs two verifiers and " +
  "cannot be closed.\n" +
  "The implementation checklist and the summary's deliverable index are reconciled between the two loops, " +
  "against a settled spec staging. Drift in them is expected here and is NOT a finding.\n" +
  "One thing the FIXER may do that you may not file: where a spec edit it makes falsifies a statement " +
  "already written in the non-spec staging, it corrects that statement as part of completing the spec " +
  "fix. That is the consequence of an edit rather than a finding of its own, so report the spec defect " +
  "and let the fixer propagate it. Do not file the non-spec statement as a separate finding: it costs " +
  "two verifiers and closes nothing this loop is converging.";

const NONSPEC_SCOPE_NOTE =
  "\n\nSCOPE OF THIS LOOP. Read the staged spec edits in " + P.spec + " and the staged non-spec changes " +
  "in " + P.nonSpec + " AS ONE DOCUMENT, together with the checklist and the summary. A non-spec change " +
  "that contradicts a staged spec edit is a finding, and only a reviewer holding both can see it, which is " +
  "why this loop reads both.\n" +
  (lockSpecChanges
    ? "The staged spec edits are LOCKED: they converged in an earlier loop and are not editable here. A " +
      "finding whose only remedy is a spec edit is still worth reporting — it is closed by recording an " +
      "open decision rather than by editing."
    : "The staged spec edits converged in an earlier loop. Prefer a finding whose fix lands in the non-spec " +
      "staging; report one that needs a spec edit when it is genuinely the only remedy.");

// The probe's answer is a field rather than a word in prose. Measured against
// the substring gate this replaces: a reply of "NO - the proposal stages
// nothing under spec/, so the answer is not YES." matched /YES/i and ran the
// whole spec loop, and any non-string return stringified to "[object Object]",
// matched nothing, and skipped the spec review entirely on one log line while
// the run still returned status "reviewed".
const SPEC_PROBE = {
  type: "object",
  required: ["stagesSpecChanges", "why"],
  properties: {
    stagesSpecChanges: {
      type: "boolean",
      description:
        "true when the proposal names any spec file, spec section, or SPEC-n deliverable as something it changes, even when that text is not written yet",
    },
    why: {
      type: "string",
      description: "the spec target the answer rests on, or what the staging carries instead",
    },
  },
};

let specLoop = null;
let specReviewSkipped = null;
let nonSpecLoop = null;

const specProbe = await robustAgent(
  "Report whether a proposal INTENDS any change to a file under spec/. Do not edit anything.\n\n" +
    "Read " + roleRef(P, "spec", "Proposed spec changes") + ", and " +
    roleRef(P, "summary", "## Summary") + " if the first is thin.\n\n" +
    "The question is INTENT, not completeness. Answer YES when the proposal names a spec file, a spec " +
    "section, or a SPEC-n deliverable as something it changes — even when the text is not written yet, is " +
    "marked as an indicative target, or says the implementor fills it in. A proposal whose spec staging is " +
    "still a placeholder needs the spec review MORE than one whose staging is finished, because writing " +
    "that text is the work the review does.\n\n" +
    "Answer NO only when the proposal changes nothing under spec/ at all: the staging carries only its " +
    "headings AND nothing anywhere names a spec target.\n\n" +
    "Set stagesSpecChanges, and name in why the spec target the answer rests on or what the staging " +
    "carries instead.",
  { schema: SPEC_PROBE, label: "probe:spec-changes", model: "haiku", phase: "Review" },
);
// An unreadable answer runs the loop. The spec loop is the expensive half, but
// skipping it certifies nothing about the staged spec edits while the run still
// returns "reviewed", so doubt resolves toward reviewing. `specProbe` is null
// when the probe died after all of robustAgent's retries, which is the case the
// old `|| "YES"` default covered and this keeps.
const specProbeRead = !!specProbe && typeof specProbe.stagesSpecChanges === "boolean";
const hasSpecChanges = !specProbeRead || specProbe.stagesSpecChanges;

if (!specProbeRead) {
  log("probe:spec-changes returned no readable answer; the spec review loop runs anyway");
}
if (!hasSpecChanges) {
  specReviewSkipped = { reason: "no-spec-changes", why: String(specProbe.why || "") };
  log("The proposal stages no spec edits; skipping the spec review loop: " + specReviewSkipped.why);
} else if (input.skipSpecReview) {
  specReviewSkipped = { reason: "skipSpecReview" };
  log("skipSpecReview is set; the spec staging is NOT reviewed by this run");
} else {
  specLoop = await runReviewLoop({
    name: "spec",
    // The spec loop drops test-coverage: the tests a change needs are staged in
    // the non-spec half, so spec convergence certifies nothing about them.
    pool: POOL.filter((l) => l.key !== "test-coverage"),
    maxRounds: maxSpecReviewRounds,
    editable: SPEC_EDITABLE,
    scopeNote: SPEC_SCOPE_NOTE,
    target: P.spec,
  });
}

// Between the loops: the checklist has not been written against the spec
// staging. This is a reconciliation, not a review round.
//
// It runs WHETHER OR NOT the spec loop converged. A loop that exhausted its
// budget has MORE unreconciled consequences than one that converged, not fewer,
// so gating this on convergence meant the one run that most needed it was the
// one that did not get it: the measured run finished with a deliverable index
// and a checklist describing a spec staging that had moved out from under them,
// and nothing had ever reconciled the two.
if (specLoop && !stoppedByIntrospection) {
  await robustAgent(
    "Reconcile a proposal's deliverable index and implementation checklist against the staged spec " +
      "edits.\n\n" +
      "HARD CONSTRAINT: the only files you may edit are " + P.summary + ", " + P.checklist + " and " +
      P.nonSpec + ", and " + P.log + " to record what you closed. Change nothing else, and change nothing " +
      "in them beyond what this pass names.\n\n" +
      (specLoop.converged
        ? "The spec staging in " + P.spec + " has converged. It was reviewed for several rounds and " +
          "deliverables were added, removed, and renumbered along the way, so the index and the checklist " +
          "are behind it.\n\n"
        : "The spec staging in " + P.spec + " did NOT converge: the review loop ran out of budget with " +
          "findings still open. Reconcile against the staging AS IT NOW STANDS. This is worth doing " +
          "precisely because the staging is unsettled — the index and the checklist are further behind it " +
          "than they would be after a converged loop, and leaving them describing a staging that moved is " +
          "how a reader is misled about what the proposal now stages. Do not try to guess where the open " +
          "findings will land; reconcile to the current text and no further.\n\n") +
      "Do four things.\n" +
      "1. Rebuild `## Deliverable index` in " + P.summary + " from what " + P.spec + " and " + P.nonSpec +
      " now stage. Every staged deliverable appears exactly once with the file it lands in and one line.\n" +
      "2. Write the checklist's SPEC-lane steps against the current SPEC ids, as a leading block, one lane " +
      "per step, in the order the spec edits must be applied.\n" +
      "3. Reconcile the existing non-spec steps' `Depends on:` against those step ids.\n\n" +
      "4. DISCHARGE THE DEFERRED CORRECTIONS. The spec loop derives corrections whose remedy lands in " +
      "files it may not edit, and records each as a `DEFERRED [file]:` line in " + P.log + ". Grep that " +
      "log for `DEFERRED` and take every one that nothing has closed.\n" +
      "   CLOSE the ones that are repairs: a statement already written in " + P.nonSpec + " or in the " +
      "checklist that the spec staging has made false. Make the edit in the file the entry names, PROVIDED " +
      "that file is one of the four you may edit; an entry naming any other file is carried forward like " +
      "the ones below rather than acted on. Then " +
      "append a `CORRECTS [id]` line to " + P.log + " naming the entry you closed and what you did.\n" +
      "   CARRY FORWARD the ones that are not. A correction that would require AUTHORING a staged code, " +
      "schema, chart, docs or test change that does not exist yet is not yours to make, however plainly " +
      "the spec staging implies it: writing it here would put content into the proposal that no non-spec " +
      "lens has ever read. Record each as one `OPEN` line in " + P.log + " stating what remains and which " +
      "file it lands in, so the next loop's first round reads it.\n" +
      "   These are corrections the spec loop DERIVED and could not apply. On a measured run they " +
      "accumulated for four and a half hours as an errata list promising fixes that no lane owned, so an " +
      "entry left unclosed here is one left unclosed for good.\n\n" +
      "Steps 1 through 3 are not a review round: do not reopen a decision, do not edit a staged change, " +
      "and do not improve any wording. Step 4 is the one place this pass changes what the proposal says, " +
      "and only to apply a correction the spec loop already derived. " + FORMAT_CHECKLIST +
      promptFor("handoff") +
      "\nFollow " + repo + "/.claude/rules/doc-style.md.",
    { label: "spec-nonspec-handoff", phase: "Review" },
  );
  log(
    "Reconciled the deliverable index and the checklist against the " +
      (specLoop.converged ? "settled" : "UNSETTLED") + " spec staging",
  );
}

// The non-spec loop does not start on a spec staging that is still moving.
//
// It was ungated, and the cost was measured: the spec loop exhausted its budget
// without converging, and the non-spec loop then spent six rounds and half the
// run's tokens reviewing a checklist and a deliverable index against staged spec
// edits that were still open. Raising the spec budget and resuming is the
// operator's call; spending the second budget on unsettled staging is not a
// decision the run should make silently.
const specBlocked =
  specLoop && !specLoop.converged && !input.allowNonSpecOnUnconvergedSpec && !stoppedByIntrospection;
if (specBlocked) {
  log(
    "The spec loop did NOT converge after " + specLoop.round + " of " + specLoop.maxRounds +
      " round(s); the non-spec review is NOT run. Raise maxSpecReviewRounds and resume, or set " +
      "allowNonSpecOnUnconvergedSpec.",
  );
} else if (!stoppedByIntrospection && !input.skipNonSpecReview) {
  nonSpecLoop = await runReviewLoop({
    name: "non-spec",
    pool: POOL,
    maxRounds: maxNonSpecReviewRounds,
    editable: NONSPEC_EDITABLE,
    scopeNote: NONSPEC_SCOPE_NOTE,
    target: P.nonSpec,
  });
} else if (input.skipNonSpecReview) {
  log("skipNonSpecReview is set; the non-spec staging is NOT reviewed by this run");
}

// The run converged when every loop it ran converged. A skipped loop certifies
// nothing about its half, which the result records.
const ranLoops = [specLoop, nonSpecLoop].filter(Boolean);
const converged =
  ranLoops.length > 0 &&
  ranLoops.every((l) => l.converged) &&
  !stoppedByIntrospection;
const round = ranLoops.reduce((n, l) => n + l.round, 0);
const reviewersFailed = ranLoops.some((l) => l.reviewersFailed);

// One verification pass over the implementation checklist and the Summary, after
// convergence and before the proposal is marked verified. Both are maintained as
// the proposal changes rather than written at the end, so this confirms the
// maintenance held rather than producing them from scratch.
if (converged) {
  await robustAgent(
    "Verify one proposal's Summary and implementation checklist against the rest of the document, and " +
      "correct them where they have drifted.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
      "THE CHECKLIST. Every staged deliverable appears in exactly one step. No step names a deliverable the " +
      "proposal does not stage. Every Depends-on names an earlier step that exists. Spec steps precede the code " +
      "steps that consume them, and any step whose lane breaks that order states on its own line why the " +
      "interleave is deliberate. Each step's tier list covers the tiers its deliverables reach. Every box is " +
      "unchecked. Then read the steps as an order of application: if applying them in sequence would hit a " +
      "forward reference that another order would not, resequence.\n\n" +
      "THE SUMMARY. Its top-level changes match what the proposal now stages. Every decision it lists as fixed " +
      "is one the document still takes, and no decision the document treats as settled is missing from it. Its " +
      "watch-outs still describe traps the current design has, rather than ones an earlier revision had.\n\n" +
      "Correct what has drifted, in place. Change nothing else: this is a reconciliation pass, not a review " +
      "round, and the design is settled. Follow " +
      repo +
      "/.claude/rules/doc-style.md.",
    { label: "verify-checklist", phase: "Review" },
  );
  log("Checklist and Summary verified against the converged proposal");
}

// The status file is written on EVERY run, converged or not. Neither review loop
// may edit it -- it is in no loop's editable set -- so before this ran
// unconditionally a non-converged run left the status saying whatever it said
// before the run started. One measured run finished thirteen passes with its
// status still reading "This draft has not been through adversarial review".
//
// The frontmatter goes through proposal-status.mjs, which is the only supported
// writer: the spec-lease hook reads the status through it, so a status set by
// hand-editing prose is a status the hook may not see. The prose body is a
// judgement about what the run established and stays with an agent.
{
  const loopLine = (name, l) =>
    l
      ? name + ": " + l.round + " rounds, " + (l.converged ? "converged" : "DID NOT CONVERGE") +
        ", " + l.sweeps + " full-pool sweep(s)"
      : name + ": not run";
  const history =
    loopLine("spec", specLoop) + "; " + loopLine("non-spec", nonSpecLoop) +
    "; " + fixedTitles.length + " findings fixed";

  if (converged) {
    await robustAgent(
      "Run exactly this command and reply with the single word DONE:\n\n" +
        "node " + repo + "/.claude/tools/proposal-status.mjs " + P.root +
        " --set status=Reviewed --by change-proposal --date " + date + "\n\n" +
        "Do nothing else. Do not read, summarise, or edit anything.",
      { label: "status:set-reviewed", model: "haiku", phase: "Review" },
    );
  }

  await robustAgent(
    "Record what an adversarial review run established, in one proposal's status file.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " + P.status + ". Do NOT touch its YAML frontmatter, " +
      "which another step owns and has already written. Change nothing else, anywhere.\n\n" +
      "WHAT THE RUN DID: " + history + ".\n" +
      "The run " + (converged ? "CONVERGED." : "DID NOT CONVERGE.") + "\n\n" +
      "Do two things, below the frontmatter.\n\n" +
      "1. Write or replace a `## Review history` section stating, in plain declarative sentences, what the " +
      "run above did: the rounds each loop ran, whether each converged, and the findings fixed. When a loop " +
      "did not converge, say so and say that findings it had not closed remain open. Date it " + date + ".\n\n" +
      "2. CORRECT THE REST OF THE FILE against that. This file is not maintained by the review loops, so its " +
      "prose is as old as the day it was written and it routinely contradicts what the run established. A " +
      "sentence saying the draft has not been reviewed, that a decision is open which the review settled, or " +
      "that a reader should run a loop that has now run, is false and you correct or delete it. Do not " +
      "correct it into a claim the run does not support: a run that did not converge has not settled the " +
      "questions it was reviewing.\n\n" +
      "Do not restate the proposal, do not summarise its design, and do not add a finding of your own. " +
      "Follow " + repo + "/.claude/rules/doc-style.md.",
    { label: "status:record-run", phase: "Review" },
  );
  log("Status recorded: " + history);
}

return {
  mode,
  // A run stopped by the spec gate says so, rather than reporting "reviewed"
  // for a proposal whose non-spec staging nothing looked at. A run the
  // introspection pass halted or reframed says so first: it stopped at round 2
  // of 6 with its findings open, and "reviewed" is what a converged run says.
  status: stoppedByIntrospection
    ? "stopped-" + stoppedByIntrospection.verdict
    : mode === "new"
      ? "written"
      : specBlocked
        ? "spec-not-converged"
        : "reviewed",
  specGate: specBlocked
    ? {
        rounds: specLoop.round,
        budget: specLoop.maxRounds,
        sweeps: specLoop.sweeps,
        stillFinding: specLoop.lastFindings,
        resume:
          "Raise maxSpecReviewRounds above " + specLoop.maxRounds +
          " and resume, or set allowNonSpecOnUnconvergedSpec to review the non-spec staging anyway.",
      }
    : undefined,
  path,
  title: draftTitle,
  premises: premiseStats,
  changes:
    mode === "new"
      ? { kept: keptTitles, dropped: droppedChanges.map((d) => d.title) }
      : undefined,
  introspection: {
    passes: introspections,
    stoppedBy: stoppedByIntrospection,
    // The next run, when a stopping verdict proposed one. The skill relaunches
    // automatically on a `halt` whose next steps are clear, and puts the
    // question to a human otherwise.
    nextSteps: (stoppedByIntrospection && stoppedByIntrospection.nextSteps) || null,
    gatedPasses: introspections.filter((i) => i.gated).length,
    overruledStops,
    byArea: Object.fromEntries(
      [...areaLog].map(([a, es]) => [
        a,
        {
          total: es.length,
          design: es.filter((e) => e.kind === "design-defect").length,
          selfInflicted: es.filter((e) => e.introducedBy === "this-run").length,
          rounds: [...new Set(es.map((e) => e.round))],
        },
      ]),
    ),
    mechanisms: introducedMechanisms,
    redesigns: redesignHistory,
    prunes: pruneHistory,
  },
  review: {
    converged,
    reviewersFailed,
    rounds: round,
    sweeps: [specLoop, nonSpecLoop].filter(Boolean).reduce((n, l) => n + l.sweeps, 0),
    loops: [specLoop, nonSpecLoop].filter(Boolean).map((l) => ({
      name: l.name,
      rounds: l.round,
      sweeps: l.sweeps,
      converged: l.converged,
      reviewersFailed: l.reviewersFailed,
      fixersFailed: l.fixersFailed,
      retiredLenses: [...l.retired],
      stalledLenses: l.stalledLenses,
      specTouched: l.specTouched,
    })),
    // A skipped loop certifies nothing about its half of the proposal, so a
    // reader of this result must be able to see which ran.
    specReviewed: !!specLoop,
    // Which of the three reasons the spec loop did not run: the proposal stages
    // nothing under spec/, the caller skipped it, or null when it ran.
    specReviewSkipped,
    nonSpecReviewed: !!nonSpecLoop,
    lockSpecChanges,
    verifyOrder,
    // Echo the caller's lens controls. An excluded lens certifies nothing, so a
    // reader of this result must be able to see what the run did not review.
    excludedLenses: [...excludeSet],
    startLenses: startSet ? [...startSet] : null,
    lensPromptApplied: lensPrompt.length > 0,
    totalFixed: fixedTitles.length,
    fixedTitles,
    rejectedTitles: rejected.map((r) => r.title),
    history,
  },
};
