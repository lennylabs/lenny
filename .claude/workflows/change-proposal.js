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
// Default 16 rather than 12: lens retirement made each round much cheaper but not
// fewer, and every sweep spends a round of the budget. A run that is draining
// steadily can otherwise exhaust the budget mid-cycle, one revive short of a clean
// sweep, and be reported as non-converged when it was in fact converging.
const maxRounds = input.maxReviewRounds || 16;

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

const READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file. Cite evidence as file:line.";
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
  "  The lane after the step id is spec, code, schema, migration, test, or docs. Spec steps come first and code steps follow, which is the order the implementation pipeline applies them and the order to prefer. Interleaving a code step before a remaining spec step is allowed where it is genuinely more efficient, and a step that does so states why on its line, so an interleave is a deliberate and reviewable act rather than an accident.\n" +
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

// ---- New mode: validate, draft, challenge, write ----

let path;
let draftTitle = null;
let premiseStats = null;
let keptTitles = [];
let droppedChanges = [];

if (mode === "new") {
  const problem = input.problem;
  const num = input.nextNumber;

  phase("Validate");
  log("Decomposing the problem into testable premises");
  const decomposition = await robustAgent(
    "Decompose a reported spec problem into individually testable premises.\n\n" +
      "Problem:\n" +
      problem +
      "\n\nContext:\n" +
      context +
      "\n\n" +
      READ_ONLY +
      "\n" +
      "List every premise the problem rests on, including implicit ones (assumptions about process lifetimes, ownership, ordering, or who calls what). " +
      "Each premise is one falsifiable statement about what the spec says (spec-claim), what the code does (code-claim), what is missing (gap-claim), or what would go wrong (consequence-claim). " +
      "Mark loadBearing: true when refuting the premise would invalidate or materially redirect the problem. Cap the list at the ten most consequential premises.",
    { schema: PREMISES, label: "decompose" },
  );
  // robustAgent returns null when every retry is exhausted (a hard account
  // "session limit" is not rescued by the model fallback). Return a clean
  // interrupted status rather than dereferencing null, so the run can be
  // resumed after the reset instead of crashing before the proposal is written.
  if (!decomposition) {
    return {
      mode,
      status: "interrupted",
      phase: "decompose",
      reason: "premise decomposition failed after retries (likely session limit)",
    };
  }
  const premises = decomposition.premises.slice(0, 10);
  log(
    premises.length +
      " premises identified; dispatching one skeptic per premise",
  );

  const verdicts = (
    await parallel(
      premises.map(
        (p) => () =>
          robustAgent(
            "Try to REFUTE this premise about the spec or implementation.\n\n" +
              "Premise (" +
              p.kind +
              "): " +
              p.statement +
              "\n\n" +
              "Original problem statement, for context only:\n" +
              problem +
              "\n\n" +
              READ_ONLY +
              "\n" +
              EVIDENCE +
              "\n" +
              "Read the actual spec sections and code the premise is about. Return confirmed only when you found direct supporting evidence, refuted when the evidence contradicts the premise, and revised when the premise is directionally right but wrong in a detail that matters (provide revisedStatement). " +
              "Default to refuted when you cannot find supporting evidence.",
            {
              schema: PREMISE_VERDICT,
              label: "skeptic:" + p.id,
              phase: "Validate",
            },
          ).then((v) => ({ premise: p, ...v })),
      ),
    )
  ).filter(Boolean);

  const refuted = verdicts.filter((v) => v.verdict === "refuted");
  const standing = verdicts.filter((v) => v.verdict !== "refuted");
  premiseStats = { standing: standing.length, refuted: refuted.length };
  log(
    "Premises: " +
      standing.length +
      " standing, " +
      refuted.length +
      " refuted",
  );

  const loadBearing = verdicts.filter((v) => v.premise.loadBearing);
  if (
    loadBearing.length > 0 &&
    loadBearing.every((v) => v.verdict === "refuted")
  ) {
    return {
      mode,
      status: "not-viable",
      reason: "every load-bearing premise was refuted",
      verdicts,
    };
  }

  phase("Draft");
  const dossier = verdicts
    .map(
      (v) =>
        "- [" +
        v.verdict.toUpperCase() +
        "] " +
        (v.revisedStatement || v.premise.statement) +
        "\n  evidence: " +
        v.evidence.join("; ") +
        "\n  notes: " +
        v.notes,
    )
    .join("\n");

  const draft = await robustAgent(
    "Draft a change proposal.\n\n" +
      "Problem:\n" +
      problem +
      "\n\n" +
      "Premise verdicts from independent skeptics (refuted premises are course corrections; the draft must not rest on them):\n" +
      dossier +
      "\n\n" +
      READ_ONLY +
      " Output the draft as structured data only; another agent writes the file.\n" +
      EVIDENCE +
      "\n" +
      "Project principles: " +
      PRINCIPLES +
      "\n" +
      "Read " +
      exemplar +
      " for the level of specificity expected, and read the spec sections each change targets. " +
      "Produce: a title; kind (fix corrects or reconciles existing behavior — spec text, core-product code, or test infrastructure; new adds a capability the spec or implementation lacks); a problem restatement grounded in the confirmed evidence; the review decisions that constrain the design; the change set (each change names its targets — spec files and sections, code packages and files, or test files — the rationale, and a concrete sketch of the staged edit); non-goals; open questions only for decisions that genuinely belong to the human reviewer. " +
      "Set viable: false with whyNotViable when the confirmed evidence shows no change is needed.",
    { schema: DRAFT, label: "draft" },
  );

  // Same guard as the decompose phase: a null draft (retries exhausted, likely a
  // session limit) must not crash on draft.viable — return a resumable status.
  if (!draft) {
    return {
      mode,
      status: "interrupted",
      phase: "draft",
      reason: "draft failed after retries (likely session limit)",
      verdicts,
    };
  }
  if (!draft.viable) {
    return { mode, status: "not-viable", reason: draft.whyNotViable, verdicts };
  }
  draftTitle = draft.title;
  log(
    'Draft "' + draft.title + '" proposes ' + draft.changes.length + " changes",
  );

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
              "Return drop when the change is unnecessary or rests on a false premise, revise with a concrete revision when the need is real but the change is wrong or oversized, and keep only when it survives all five questions.",
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
      dropped: droppedChanges,
      verdicts,
    };
  }

  phase("Write");
  const slug = draft.title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
  path = repo + "/proposals/" + num + "_" + draft.kind + "_" + slug + ".md";

  await robustAgent(
    "Write a change proposal file.\n\n" +
      "HARD CONSTRAINT: the only file you may create or edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/. The proposal stages its changes — spec edits, code changes, and test changes — as fenced markdown blocks or precise change descriptions; it never applies them.\n\n" +
      "Draft (apply the challenge revisions in each sketch verbatim):\n" +
      JSON.stringify({ ...draft, changes: kept }, null, 2) +
      "\n\n" +
      "Dropped alternatives to record in Non-goals with their reasons:\n" +
      JSON.stringify(droppedChanges, null, 2) +
      "\n\n" +
      "Date: " +
      date +
      "\n" +
      "Format. Two unnumbered sections open the proposal, before the numbered ones:\n\n" +
      FORMAT_SUMMARY + "\n" + FORMAT_CHECKLIST + "\n" + FORMAT_BLANKS +
      "\nThen follow the structure of " +
      exemplar +
      ' exactly (read it first): the "# Proposal:" title; Status ("Draft for review."), Date, and Scope bullets; the staging boilerplate paragraph; numbered sections (Problem with file:line citations and any finding IDs the input named; Decisions; design sections; Edge cases and accepted failure modes (every edge case or failure mode the design accepts or defers — not only those it changes — each row naming the observable outcome and the exact spec text and docs/ page that states it, so a deferred mechanism still records its accepted behavior and stages the sentence that documents it; omit only when the change has no accepted or deferred failure mode); Proposed changes with one subsection per target (spec file and section, code package, or test) and an anchor instruction plus a fenced block of the exact text to insert or a precise change description; Non-goals; Testing (list the specific, insightful, relevant new tests to add during implementation — one per behavior the proposal changes, mapped to the tiers the change reaches per .claude/rules/test-coverage.md, each covering the non-happy-path it needs (empty, error, concurrent, boundary, and spec-named-failure) and carrying a // spec: tie, rather than a vague "add tests" note); Findings closed on application; Resolved in adversarial review, initially noting that review rounds populate it; Open decisions for review when the draft has open questions; Files touched on application consistent with the staged changes).\n' +
      "Prose rules: follow " +
      repo +
      "/.claude/rules/doc-style.md (read it first). " +
      "Read the spec sections each staged edit targets so anchors and surrounding text are quoted accurately.",
    { label: "write", phase: "Write" },
  );
  log("Proposal written to " + path);
} else {
  path = input.proposalPath.startsWith("/")
    ? input.proposalPath
    : repo + "/" + input.proposalPath;
}


// ---- Bootstrap: give an existing proposal the two sections it predates ----
//
// The writer produces the Summary and the implementation checklist in new mode.
// A proposal written before those existed has neither, and everything downstream
// assumes both: the fixer is told to keep the checklist current, the
// applicability lens to validate it, and the end-of-run pass to reconcile it.
// Without this step each of those improvises separately.
//
// The sections are created ONCE, here, and then maintained and lens-checked like
// any other section for the rest of the run. That is the whole point of doing it
// at round zero rather than at the end: a checklist asserted after convergence is
// a guess at a sequence dressed as a decision, while one created here is
// validated by every round that follows it.
if (mode !== "new") {
  let needsBootstrap = false;
  try {
    const text = require("fs").readFileSync(path, "utf8");
    needsBootstrap =
      !/^## Summary\s*$/m.test(text) ||
      !/^## Implementation checklist\s*$/m.test(text);
  } catch (e) {
    needsBootstrap = false;
  }
  if (needsBootstrap) {
    phase("Bootstrap");
    log("Proposal predates the Summary and checklist sections; creating them");
    await robustAgent(
      "Give an existing change proposal the two sections it was written before, deriving both from what the " +
        "document already says rather than inventing anything new.\n\n" +
        "HARD CONSTRAINT: the only file you may edit is " +
        path +
        ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/. Add the two sections and " +
        "change nothing else: no decision is reopened here, no staged change is edited, and no wording " +
        "elsewhere is improved. This is a structural addition.\n\n" +
        "Read the whole proposal first. Then insert both sections, unnumbered, after the staging boilerplate " +
        "paragraph and before the first numbered section, in this order.\n\n" +
        FORMAT_SUMMARY +
        "\nDerive the Summary from the document. Its top-level changes are the staged deliverables grouped by " +
        "what they accomplish rather than listed one by one. Its fixed decisions are the proposal's own " +
        "Decisions, reduced to the line an implementor needs and stripped of the reasoning. Its watch-outs come " +
        "from the recorded limits, the open questions, the accepted failure modes, and the review history: a " +
        "trap the loop already fell into is exactly what an implementor needs warning about, and the pass log " +
        "is where those are recorded.\n\n" +
        FORMAT_CHECKLIST +
        "\nDerive the checklist from the staged deliverables and the dependencies the document states between " +
        "them. Read the Proposed changes section, the detailed design, and the files-touched section together: " +
        "a deliverable that edits a file another deliverable creates depends on it, and a code deliverable that " +
        "consumes a specification statement depends on the deliverable that states it. Where the document " +
        "already records an application order or a precondition, follow it rather than deriving your own.\n\n" +
        "WHERE THE DOCUMENT DOES NOT SETTLE AN ORDER, say so rather than guessing silently: put the step where " +
        "it seems to belong and note on its line that the order is inferred. The review rounds that follow will " +
        "check it, and a marked inference is something they can check, while a confident guess is not.\n\n" +
        "Follow " +
        repo +
        "/.claude/rules/doc-style.md.",
      { label: "bootstrap-sections", phase: "Bootstrap" },
    );
    log("Summary and implementation checklist created");
  }
}

// ---- Conventions pass (shared, one-shot, outside the error loop) ----

phase("Conventions");
await robustAgent(
  "Check one proposal file against the written conventions and fix only violations.\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "The written rules: section structure and citation formats per the exemplar " +
    exemplar +
    " (read it first), and prose per " +
    repo +
    "/.claude/rules/doc-style.md (read it first). " +
    "Fix structural deviations and doc-style violations (fragments, missing list conjunctions, decorative em-dashes, marketing language). Do not change technical content, citations, or design decisions. If the file already conforms, change nothing and say so.",
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
      "(6) EXECUTION-MODEL INVERSION. The pipeline that applies this proposal lands its spec/ edits FIRST, verifies them, and commits them as their own commit, before any code is written (.claude/skills/implement-proposal/SKILL.md states this as a hard constraint). A proposal whose spec edits depend on code that the same proposal builds therefore cannot be applied at all, in any order: at spec-apply time the code does not exist, and the pipeline will not build it first. Check for this explicitly, because it is invisible when you simulate the proposal's own stated order, where the dependency is satisfied.\n" +
      "    The test: does applying any staged spec edit require running, reading, or consulting something this proposal builds under scripts/, cmd/, pkg/, or tests/? A migration script that performs the rewrite, a register or map that resolves each site's replacement, a generated artifact, a lint whose output selects the edit sites. If so, the spec edits will be hand-applied by an agent with none of it available. Report it, naming the spec sub-step, the code artifact it depends on, and the sub-step that builds that artifact.\n" +
      "    Two signals make it near-certain and are worth grepping for: the proposal says it enumerates no edit sites, or states that completeness is proven by gates rather than by review, while still staging spec edits. Both mean the spec edits have no hand-appliable form by design.\n" +
      "    The resolution is a split (the code lands as its own proposal, first) or an explicit entry criterion, so state the gap and let the author choose. A proposal that already records such a prerequisite is conformant and is NOT a finding. Do not report ordinary sequencing inside one phase, which is class 4; this class is only the spec-before-code boundary the pipeline imposes from outside the document.\n\n" +
      "Method: read the proposal's staged-changes section in full and in order, then open the actual target files to confirm each anchor and each referenced artifact. Build the existence model as you go; a forward reference is only visible if you track what each sub-step creates. That model and class 2's step-1 worklist are the same enumeration read two ways, so build it once: for each created artifact ask both WHEN it exists relative to the edits that reference it (class 1) and WHETHER every property those edits need is stated (class 2). Do not report an edit as unappliable because you would have written it differently, and do not report ordinary implementation judgment (choosing a variable name, formatting a table) as underspecification. The test is whether a competent implementor would be forced to guess at something the proposal was responsible for stating.",
      + "\n(CHECKLIST) THE IMPLEMENTATION CHECKLIST IS THE APPLICATION ORDER, so this lens owns it. Read it against the staged deliverables and report: a staged deliverable that appears in no step; a deliverable named by two steps; a step naming a deliverable the proposal does not stage; a Depends-on that names a later step or a step that does not exist; a step whose lane is code while a spec step it depends on comes after it, unless the step's line states why the interleave is deliberate; and a step whose tier list omits a tier its deliverable plainly reaches. Simulate the checklist as the order of application: if applying the steps in their stated order would hit a forward reference that applying them in another order would not, the checklist is the defect rather than the edit.\n" +
      "A checked box is a finding. The proposal is not implemented, so every box is unchecked until the implementation pipeline ticks it."
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
const POOL_EXTRA = EXTRAS.filter((l) => !excludeSet.has(l.key));
if (POOL_FIXED.length === 0 && POOL_EXTRA.length === 0) {
  throw new Error("args.excludeLenses excludes every lens; nothing would review");
}
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

function reviewPrompt(lens, round, fixedTitles, rejected) {
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
        .map((r) => "- " + r.title + ": refuted because " + r.reason)
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
    history +
    (lensPrompt
      ? "\n\nAdditional instruction from the caller of this run. It adds context or " +
        "focus; it does not lower the finding bar above, and it does not make " +
        "something a finding that the bar excludes:\n" +
        lensPrompt
      : "") +
    "\n\nWork method: read the proposal fully, then investigate the repository with Grep and targeted Reads to verify or refute its claims under your lens. Report your findings via the structured output (empty array if you find nothing that meets the bar)."
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

function fixPrompt(confirmed, round, strikes) {
  return (
    "You are the fixer for round " +
    round +
    " of an iterative convergence loop on the proposal " +
    path +
    ".\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\nApply EXACTLY the confirmed findings below using Edit (or Write for large restructures). Requirements:\n" +
    "- Before each edit, re-verify the relevant spec/code citations yourself with Grep/Read; every claim that remains in the proposal must be accurate and carry file:line evidence. Re-verify every citation in text you touch, including stale line numbers.\n" +
    "- Make the smallest change that corrects each finding. Do not expand scope. Do not change design decisions beyond what the findings require; when a finding forces a design choice, pick the option most consistent with the cited spec precedent and the project principles (" +
    PRINCIPLES +
    "), and record the rationale in the proposal.\n" +
    "- READ EVERY FINDING BEFORE YOU EDIT ANYTHING. Group the findings that touch the same text, the same section, or the same mechanism, and fix each group as one change. Findings that look independent often share a root, and closing them separately produces edits that contradict each other and become findings of their own in a later round.\n" +
    "- INVENTING A MECHANISM IS ALLOWED AND IS SOMETIMES THE ONLY CORRECT FIX, BUT IT IS THE MOST DANGEROUS EDIT YOU CAN MAKE. This loop has measured that a mechanism introduced to close one finding goes on to produce several more over later rounds, because it lands unspecified and nothing reviews it as a design. So when a finding cannot be closed by correcting existing text, and you must add a field, a flag, a report, a compensating action, an RPC, a frame, or an interface change, specify it WHOLE in the same edit, before you write it: the state it reads and EVERY site that sets and clears that state; every caller and every type that satisfies an interface you change; what happens when it does not fire and what observes that; and the test that pins it. Then declare it in newMechanisms with those same four properties filled in. An unspecified mechanism is a defect you are handing to a later round.\n" +
    "- Where a finding genuinely needs a decision rather than an edit, record it in the proposal's open-decisions section with the constraint any solution must satisfy, and list it in escalated. That is a complete fix, not a deferral. Prefer a specified mechanism to an escalation, and an escalation to an unspecified mechanism.\n" +
    "- NEVER WRITE A COUNT of staged edits, sites, statements, rewrites, or files. Name the set, or point at the enumeration that carries it. A count goes stale the moment another fix adds one, and in this loop a stale count becomes a finding, a round, and two verification agents. The documentation rules ban counts for the same reason.\n" +
    "- AFTER YOUR EDITS, reconcile every enumeration and cross-reference that names a section you touched. A fix that corrects one section and leaves another section's list of that section's contents stale is two findings rather than one.\n" +
    "- When a fix changes a trigger predicate or invariant, propagate the exact same predicate to every section that states it (design sections, summary tables, constant comments, proposed spec text, and tests) so no drift is introduced.\n" +
    "- Keep the proposed-changes section (however the proposal titles it) and any files-touched section consistent with your edits.\n" +
    "- KEEP THE IMPLEMENTATION CHECKLIST CURRENT. It is maintained as the proposal changes rather than derived at the end. Any edit that adds, removes, merges, splits, or resequences a staged deliverable changes the checklist in the same edit: add or remove its step, correct the deliverable ids a step names, and correct any Depends-on that the change reorders. Every staged deliverable appears in exactly one step and no step names one that does not exist. Leave every box unchecked.\n" +
    "- KEEP THE SUMMARY TRUE. If a fix changes a top-level change, closes or reopens a decision the Summary lists as fixed, or creates a trap an implementor would fall into, update the Summary in the same edit. It is the one section every implementor agent reads, so a stale line there misleads every one of them.\n" +
    "- You may leave a detail to the implementor rather than specifying it, and doing so is often better than adding text that two sections then have to keep agreeing about. " + FORMAT_BLANKS +
    '- Append a new subsection to the proposal\'s "Resolved in adversarial review" section titled "### Pass <N> (' +
    date +
    ', automated)", where <N> continues the existing pass numbering (read the section to determine it; create the section before the open-decisions section if it does not exist), with one bullet per finding fixed, matching the format of any existing entries.\n' +
    "- Follow the documentation style rules in " +
    repo +
    '/.claude/rules/doc-style.md: complete declarative sentences, no "X, not Y" rhythm, no decorative em-dashes, no marketing language, conjunctions in lists.\n\nConfirmed findings (JSON):\n' +
    JSON.stringify(confirmed, null, 2) +
    "\n\nReturn a short summary listing each finding and the exact edit you made for it."
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
function postFixPrompt(confirmed, fixSummary, round, mechanisms) {
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
    "3. CITATIONS. Is every file:line citation in the newly written text real, and does the cited location say what the new text claims? Open them. A fixer under time pressure invents plausible line numbers.\n\n" +
    "Locating the edits: the fixer's summary below names them. `git diff -- " +
    path +
    "` also shows changed regions, though it spans every uncommitted round rather than this one alone, so treat it as a locator and not as this round's diff.\n\n" +
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
    ". A post-fix review of the previous fixer's edits to " +
    path +
    " found the defects below in that fixer's own work.\n\n" +
    CONTEXT +
    "\n\nHARD CONSTRAINT: the only file you may edit is " +
    path +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "Correct each defect with the smallest edit that fixes it. Re-verify every citation you touch with Grep or Read before writing it. When a defect is drift between a changed statement and its parallels, make every statement agree rather than reverting the original fix. Append your corrections as bullets to the SAME numbered pass subsection the previous fixer created in the proposal's adversarial-review-history section, rather than opening a new pass, because these are corrections to that pass and not a separate round. Follow " +
    repo +
    "/.claude/rules/doc-style.md.\n\nDefects to correct (JSON):\n" +
    JSON.stringify(findings, null, 2) +
    "\n\nReturn a short summary of each edit you made."
  );
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
const churnWindow = input.churnWindow || 6;
const churnMinFindings = input.churnMinFindings || 5;
const churnStrikes = input.churnStrikes || 3;
const redesignsAllowed = input.maxRedesigns === undefined ? 2 : input.maxRedesigns;
let redesignsRun = 0;
// Set when the introspection pass concludes the run should not continue without a
// human decision. It ends the loop rather than the process, so everything already
// fixed is kept and reported.
let stoppedByIntrospection = null;
// Stops the introspection pass proposed and the panel did not uphold. Fed back to
// the pass so it does not re-reach the same verdict on the same evidence.
const overruledStops = [];
// area -> [{round, kind, introducedBy}]
const areaLog = new Map();
const redesignHistory = [];

function recordFindings(rnd, fs) {
  for (const f of fs) {
    const area = (f.area || "unclassified").toLowerCase().trim();
    if (!areaLog.has(area)) areaLog.set(area, []);
    areaLog.get(area).push({
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
    const recent = entries.filter((e) => e.round > rnd - churnWindow);
    if (recent.length < churnMinFindings) continue;
    const deep = recent.filter(
      (e) => e.kind === "design-defect" || e.kind === "contradiction",
    ).length;
    if (deep * 2 < recent.length) continue;
    const prior = entries.filter(
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
    if (m.strikes < churnStrikes) continue;
    if (out.some((o) => o.area === m.name.toLowerCase())) continue;
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
      if (m.name.toLowerCase() === a.area.toLowerCase()) m.strikes = 0;
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
function sectionSizes() {
  try {
    const text = require("fs").readFileSync(path, "utf8").split("\n");
    const out = new Map();
    let cur = "(preamble)";
    let n = 0;
    for (const line of text) {
      const m = /^(#{2,3}) (.+)$/.exec(line);
      if (m) {
        out.set(cur, (out.get(cur) || 0) + n);
        cur = m[2].trim().slice(0, 70);
        n = 0;
      } else n++;
    }
    out.set(cur, (out.get(cur) || 0) + n);
    return out;
  } catch (e) {
    return new Map();
  }
}

function growthSince(before) {
  const now = sectionSizes();
  const rows = [];
  let totalBefore = 0;
  let totalNow = 0;
  for (const [k, v] of now) totalNow += v;
  for (const [k, v] of before) totalBefore += v;
  for (const [k, v] of now) {
    const was = before.get(k) || 0;
    if (v - was <= 0) continue;
    rows.push({
      section: k,
      was,
      now: v,
      added: v - was,
      pct: was ? Math.round((100 * (v - was)) / was) : null,
    });
  }
  rows.sort((a, b) => b.added - a.added);
  return {
    documentWas: totalBefore,
    documentNow: totalNow,
    documentPct: totalBefore
      ? Math.round((100 * (totalNow - totalBefore)) / totalBefore)
      : null,
    grew: rows.slice(0, 8),
  };
}

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
const PANEL_VOTE = {
  type: "object",
  required: ["verdict", "reasoning", "whatWouldChangeMyMind"],
  properties: {
    verdict: {
      type: "string",
      enum: ["healthy", "redesign", "prune", "reframe", "halt"],
    },
    reasoning: { type: "string" },
    whatWouldChangeMyMind: {
      type: "string",
      description:
        "The specific evidence that would move you to the adjacent verdict. A vote nothing could change is a vote that did not examine the evidence.",
    },
  },
};

const DISRUPTION = ["healthy", "prune", "redesign", "reframe", "halt"];

async function reviewStopDecision(rnd, verdict, growth, churn) {
  log(
    "Round " + rnd + ": introspection returned " + verdict.verdict +
      "; putting the decision to a panel before stopping",
  );
  const brief =
    READ_ONLY +
    "\n\nPROPOSAL: " + path + ". Round " + rnd + ".\n\n" +
    "An introspection pass has concluded that this adversarial convergence run should STOP and put a " +
    "question to a human, rather than continue reviewing. You are one of three reviewers of that decision. " +
    "The panel's majority decides; the pass does not decide alone.\n\n" +
    "RATIFYING IS THE FAILURE MODE HERE. You have been handed a conclusion and asked to check it, which is " +
    "the situation in which reviewers agree most and examine least. Reach your own verdict from the evidence " +
    "and let the pass's reasoning inform it rather than set it.\n\n" +
    "THE BURDEN IS ON STOPPING, and the reason is asymmetric cost rather than optimism. A wrong decision to " +
    "continue corrects itself: the next introspection runs within a few rounds, sees more evidence, and can " +
    "stop then. A wrong decision to stop costs a human's attention and the run's momentum, and nothing " +
    "corrects it. So vote to stop only if the evidence convinces you, and prefer the least disruptive verdict " +
    "that answers what the evidence actually shows.\n\n" +
    "YOU MAY DOWNGRADE RATHER THAN VETO. If the pass is right that something is wrong but wrong about how " +
    "serious it is, say so with the verdict that fits: `redesign` when a named mechanism is being repaired a " +
    "facet at a time, `prune` when a section has grown past its value, `healthy` when the run is draining and " +
    "the pass has over-read a rough patch. `reframe` and `halt` both stop the run.\n\n" +
    "THE PASS'S FULL OUTPUT, including the case it made for the run being healthy:\n" +
    JSON.stringify(verdict, null, 2) +
    "\n\nHOW THE DOCUMENT GREW since the previous introspection:\n" +
    JSON.stringify(growth, null, 2) +
    "\n\nCONFIRMED FINDINGS BY AREA, over the whole run, each with its round, kind, and whether it corrected " +
    "text this loop itself wrote:\n" +
    JSON.stringify(Object.fromEntries([...areaLog].map(([a, es]) => [a, es])), null, 2).slice(0, 10000) +
    "\n\nMECHANISMS THIS LOOP'S FIXER INVENTED, and how many later findings each caused:\n" +
    JSON.stringify(introducedMechanisms, null, 2) +
    "\n\nROUND HISTORY:\n" +
    JSON.stringify(
      history.map((h) => ({
        round: h.round,
        sweep: h.sweep,
        confirmed: h.confirmed,
        newMechanisms: h.newMechanisms,
      })),
      null,
      2,
    ).slice(0, 8000) +
    (churn && churn.length ? "\n\nCOUNTERS THAT TRIPPED:\n" + JSON.stringify(churn, null, 2) : "") +
    "\n\nRead the proposal yourself before voting. The evidence above is a summary and the document is the " +
    "subject.";

  const lenses = [
    "You are the TRAJECTORY reviewer. Judge only the direction of travel. Are findings per round falling, and " +
      "are deep defects giving way to shallow ones? A run whose confirmed counts are dropping and whose late " +
      "findings are citations and companion sites is draining, however large it has become. A run whose design " +
      "defects arrive late, or whose counts are flat across several rounds, is not. Say which pattern this run " +
      "shows, with the numbers.",
    "You are the DESIGN reviewer. Ignore the trajectory and judge the document. Read the sections the pass " +
      "names and decide whether the design in front of you is sound, whether a mechanism is described more " +
      "than once in different words, and whether the accumulated fixes still satisfy the proposal's own " +
      "Decisions. A proposal can be converging numerically onto something that should not be built.",
    "You are the COST reviewer. Judge what continuing buys against what it costs. How much has this run spent " +
      "and what has the recent spend produced? What would the next several rounds plausibly find, given what " +
      "the last several found? And what does stopping cost: is the question the pass wants to ask a human " +
      "one that a human can actually answer, or would it come back with the same problem unresolved?",
  ];

  const votes = (
    await parallel(
      lenses.map((l, i) => () =>
        robustAgent(brief + "\n\nYOUR LENS. " + l, {
          label: "stop-review:" + (i + 1) + ":r" + rnd,
          phase: "Round " + rnd + ": introspect",
          schema: PANEL_VOTE,
        }),
      ),
    )
  ).filter(Boolean);

  if (votes.length < 2) {
    log(
      "Round " + rnd + ": only " + votes.length +
        " of 3 stop-decision reviewers returned, which is no quorum; continuing, because a wrong continue " +
        "self-corrects at the next introspection and a wrong stop does not",
    );
    return { decision: "healthy", votes, quorum: false };
  }

  const tally = new Map();
  for (const v of votes) tally.set(v.verdict, (tally.get(v.verdict) || 0) + 1);
  let decision = null;
  for (const [k, n] of tally) if (n > votes.length / 2) decision = k;
  if (!decision) {
    // No majority. Take the least disruptive verdict any reviewer reached, on the
    // same asymmetry: continuing is recoverable and stopping is not.
    decision = [...tally.keys()].sort(
      (a, b) => DISRUPTION.indexOf(a) - DISRUPTION.indexOf(b),
    )[0];
    log(
      "Round " + rnd + ": stop-decision panel split " +
        [...tally].map(([k, n]) => k + "×" + n).join(", ") +
        "; taking the least disruptive, " + decision,
    );
  } else {
    log(
      "Round " + rnd + ": stop-decision panel returned " + decision + " (" +
        [...tally].map(([k, n]) => k + "×" + n).join(", ") + ")",
    );
  }
  return { decision, votes, quorum: true };
}

const introspectEvery = input.introspectEvery || 5;
const introspections = [];
let lastSizes = sectionSizes();
let lastIntrospectRound = 0;

// The agent decides; the counters only wake it. A counter cannot judge whether a
// mechanism is under-designed or a section is over-specified, and an agent that
// only ran on a fixed cadence would miss a runaway between its turns. Together:
// the counter cannot miss, the agent can judge.
async function introspect(rnd, reason, churn) {
  const growth = growthSince(lastSizes);
  lastSizes = sectionSizes();
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
      JSON.stringify(introducedMechanisms, null, 2) +
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

const fixedTitles = [];
const rejected = [];
const history = [];
let round = 0;
let reviewersFailed = false;

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
const retired = new Set();
let converged = false;
let sweeps = 0;

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
  for (const l of POOL_FIXED.concat(POOL_EXTRA)) {
    if (!startSet.has(l.key)) retired.add(l.key);
  }
}

// applyRetirement closes out a round. A lens retires when NONE of its findings
// survived verification, which covers two cases that cost the same and mean the
// same thing for the loop: the lens that found nothing, and the lens whose every
// finding two independent skeptics refuted. A lens that reliably produces findings
// the verifiers reject is not earning the tokens it costs, and retiring it is safe
// because the sweep re-runs every lens over the final text before anything is
// certified.
//
// survivors is the set of lens keys credited with at least one confirmed finding.
// A lens with a survivor is (re)activated, which on a sweep is what puts a lens
// back to work after it finds a real defect in text it had previously cleared.
//
// A lens whose agent FAILED is left exactly as it was: a dropped lens contributes
// no findings and is indistinguishable from a satisfied one, so retiring on
// failure would let an outage retire the pool and certify a proposal.
function applyRetirement(lenses, lensResults, survivors, round, note) {
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
if (mode === "redesign" || (Array.isArray(input.focusAreas) && input.focusAreas.length)) {
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
  if (named.length) {
    await runRedesign(named, 0, "requested by the caller");
  } else {
    log("Redesign mode with no focusAreas; nothing to redesign, entering review");
  }
}

while (round < maxRounds && !converged) {
  round++;
  const activeFixed = POOL_FIXED.filter((l) => !retired.has(l.key));
  const activeExtras = POOL_EXTRA.filter((l) => !retired.has(l.key));
  const isSweep = activeFixed.length === 0 && activeExtras.length === 0;

  let lenses;
  if (isSweep) {
    lenses = POOL_FIXED.concat(POOL_EXTRA);
    sweeps++;
  } else if (activeFixed.length === 0) {
    // The fixed lenses are satisfied and only extras remain. Run every remaining
    // extra in one round rather than rotating one per round, so the sweep is
    // reached immediately instead of after one round per surviving extra.
    lenses = activeExtras;
  } else if (activeExtras.length === 0) {
    lenses = activeFixed;
  } else {
    lenses = activeFixed.concat([
      activeExtras[(round - 1) % activeExtras.length],
    ]);
  }

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
          (POOL_FIXED.length + POOL_EXTRA.length) +
          " lenses retired)"),
  );

  // Barrier: the dedup step needs every reviewer's findings at once.
  const lensResults = await parallel(
    lenses.map(
      (l) => () =>
        robustAgent(reviewPrompt(l, round, fixedTitles, rejected), {
          label: "r" + round + ":review:" + l.key,
          phase: "Round " + round + ": review",
          schema: REVIEW_FINDINGS,
        }),
    ),
  );
  const failedLenses = lensResults.filter((r) => !r).length;
  const results = lensResults.filter(Boolean);

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
    const r = lensResults[i];
    if (r)
      r.findings.forEach((f) => {
        f.lens = l.key;
      });
  });

  if (results.length === 0) {
    log("Round " + round + ": every reviewer failed; stopping");
    reviewersFailed = true;
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
  const raw = results.flatMap((r) => r.findings);
  log("Round " + round + ": " + raw.length + " raw findings");

  if (raw.length === 0) {
    // Nobody found anything, so nobody has a survivor: every lens that genuinely
    // ran retires.
    applyRetirement(lenses, lensResults, new Set(), round, "found nothing");
    if (isSweep && roundComplete) {
      converged = true;
      log("Round " + round + ": full sweep found nothing; CONVERGED");
    } else if (isSweep) {
      log(
        "Round " +
          round +
          ": sweep found nothing but was incomplete; NOT converging (the failed lenses stay active and re-run)",
      );
    }
    history.push({
      round,
      sweep: isSweep,
      lenses: lenses.map((l) => l.key),
      raw: 0,
      deduped: 0,
      confirmed: 0,
      complete: roundComplete,
      retiredAfter: [...retired],
    });
    continue;
  }

  let deduped = raw;
  if (raw.length > 1) {
    const d = await robustAgent(dedupPrompt(raw), {
      label: "r" + round + ":dedup",
      phase: "Round " + round + ": review",
      schema: DEDUP_FINDINGS,
    });
    if (d && d.findings.length > 0) deduped = d.findings;
  }
  log(
    "Round " +
      round +
      ": " +
      deduped.length +
      " findings after dedup; verifying",
  );

  const verdicts = await parallel(
    deduped.map(
      (f) => () =>
        parallel([
          () =>
            robustAgent(evidencePrompt(f), {
              label: "r" + round + ":verify-evidence",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
          () =>
            robustAgent(materialityPrompt(f), {
              label: "r" + round + ":verify-material",
              phase: "Round " + round + ": verify",
              schema: VERDICT,
            }),
        ]).then((vs) => ({ f, vs: vs.filter(Boolean) })),
    ),
  );

  const live = verdicts.filter(Boolean);
  // Extend the completeness guard to verification: a verifier that failed after
  // retries leaves a finding with fewer than two verdicts, so it is neither
  // confirmed nor safely dismissed. Such a round cannot certify convergence.
  const verifyComplete =
    live.length === deduped.length && live.every((v) => v.vs.length === 2);
  if (!verifyComplete) {
    roundComplete = false;
    log(
      "Round " +
        round +
        ": some verifiers failed after retries; round INCONCLUSIVE",
    );
  }
  // Credit a later finding back to the mechanism it is about, so the strike table
  // the next fixer sees reflects which of its own inventions keep failing.
  const creditStrikes = (fs) => {
    for (const m of introducedMechanisms) {
      if (m.round >= round) continue;
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
  recordFindings(round, confirmed);
  live
    .filter((v) => !(v.vs.length === 2 && v.vs.every((x) => x.confirmed)))
    .forEach((v) => {
      rejected.push({
        title: v.f.title,
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
  const survivors = new Set();
  for (const f of confirmed) {
    const tags =
      Array.isArray(f.lenses) && f.lenses.length > 0
        ? f.lenses
        : f.lens
          ? [f.lens]
          : [];
    tags.forEach((t) => survivors.add(t));
  }
  // Attribution can only fail one way: the dedup model dropped the tags while
  // merging. Retiring on an empty survivor set would then retire every lens on a
  // round that actually confirmed defects, so fall back to the weaker but safe
  // rule (retire only a lens that reported nothing) and say so.
  if (confirmed.length > 0 && survivors.size === 0) {
    log(
      "Round " +
        round +
        ": dedup dropped the lens attribution; falling back to retiring only lenses that reported nothing",
    );
    lenses.forEach((l, i) => {
      const r = lensResults[i];
      if (r && r.findings.length > 0) survivors.add(l.key);
    });
  }
  applyRetirement(
    lenses,
    lensResults,
    survivors,
    round,
    "no finding of its own survived verification",
  );

  history.push({
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
          ": sweep incomplete (reviewer or verifier failures); NOT converging",
      );
    }
    continue;
  }

  // Strike table: mechanisms this loop introduced in earlier rounds, with the
  // number of later findings each has caused. The loop already has this
  // information and has never used it, so a fixer repairing a mechanism for the
  // third time has been doing so blind.
  const strikeLines = introducedMechanisms
    .filter((m) => m.strikes > 0)
    .map((m) => "- " + m.name + " (introduced round " + m.round + "): " + m.strikes + " later finding(s)")
    .join("\n");
  const fixOut = await robustAgent(
    fixPrompt(confirmed, round, strikeLines || null),
    { label: "r" + round + ":fix", phase: "Round " + round + ": fix", schema: FIX_RESULT },
  );
  const fixSummary = fixOut && fixOut.summary ? fixOut.summary : fixOut;
  const roundMechanisms = (fixOut && fixOut.newMechanisms) || [];
  roundMechanisms.forEach((m) =>
    introducedMechanisms.push({ name: m.name, round, strikes: 0 }),
  );
  if (roundMechanisms.length) {
    log(
      "Round " + round + ": fixer introduced " + roundMechanisms.length +
      " new mechanism(s): " + roundMechanisms.map((m) => m.name).join(", "),
    );
  }
  if (fixOut && fixOut.escalated && fixOut.escalated.length) {
    log("Round " + round + ": " + fixOut.escalated.length + " finding(s) closed by escalation");
    history[history.length - 1].escalated = fixOut.escalated;
  }
  history[history.length - 1].newMechanisms = roundMechanisms.map((m) => m.name);
  confirmed.forEach((f) => fixedTitles.push(f.title));
  history[history.length - 1].fixSummary = fixSummary || "fixer unavailable";

  // Narrow post-fix review of the fixer's own edits, then at most ONE follow-up
  // fix. The cap is deliberate: this is a correction pass on fresh text, not a
  // second convergence loop, and an unbounded review-fix cycle here would hide a
  // genuinely contested edit inside a round instead of surfacing it to the next
  // round's lenses and, ultimately, to the sweep.
  const postFix = await robustAgent(
    postFixPrompt(confirmed, fixSummary, round, roundMechanisms),
    {
      label: "r" + round + ":post-fix-review",
      phase: "Round " + round + ": fix",
      schema: FINDINGS,
    },
  );
  if (!postFix) {
    log("Round " + round + ": post-fix review unavailable after retries");
    history[history.length - 1].postFixReview = "unavailable";
  } else if (postFix.findings.length === 0) {
    log("Round " + round + ": post-fix review found no defect in the fixer's work");
    history[history.length - 1].postFixReview = "clean";
  } else {
    log(
      "Round " +
        round +
        ": post-fix review found " +
        postFix.findings.length +
        " defect(s) in the fixer's own edits; correcting",
    );
    const followUp = await robustAgent(
      followUpFixPrompt(postFix.findings, round),
      { label: "r" + round + ":follow-up-fix", phase: "Round " + round + ": fix" },
    );
    // Recorded in fixedTitles so later rounds do not re-litigate them, and in
    // history so a run where the fixer repeatedly needed correction is visible.
    postFix.findings.forEach((f) => fixedTitles.push(f.title));
    history[history.length - 1].postFixReview = postFix.findings.map(
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
    const verdict = await introspect(round, why, churn);

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
      await robustAgent(
        "Prune over-specified sections of a change proposal.\n\n" +
          "HARD CONSTRAINT: the only file you may edit is " + path +
          ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
          "An introspection pass judged these sections to have grown past their value:\n" +
          JSON.stringify(verdict.sections, null, 2) +
          "\n\nIts reasoning: " + (verdict.reasoning || "") +
          "\n\nDelete the detail it names and replace each deletion with the blanks convention: " +
          FORMAT_BLANKS +
          "\nDelete nothing the convention bars from delegation, and nothing another section depends on: " +
          "check before each deletion whether any other part of the proposal cites the text you are removing, " +
          "and if it does, either keep it or update the citing section in the same edit. Then reconcile the " +
          "implementation checklist, the files-touched section, and the testing section with what is left.\n\n" +
          'Append a bullet to the "Resolved in adversarial review" section recording what was pruned and why. ' +
          "Follow " + repo + "/.claude/rules/doc-style.md.",
        { label: "prune:r" + round, phase: "Round " + round + ": prune" },
      );
      history[history.length - 1].pruned = verdict.sections;
      // Pruned text is text the lenses have not read in its new form.
      retired.clear();
      log("Round " + round + ": pruned " + verdict.sections.length + " section(s)");
    }

    if (verdict && (verdict.verdict === "halt" || verdict.verdict === "reframe")) {
      // The pass observes; a panel decides. It may uphold the stop, downgrade it
      // to a redesign or a prune, or find the run healthy.
      const panel = await reviewStopDecision(
        round,
        verdict,
        growthSince(lastSizes),
        churn,
      );
      history[history.length - 1].stopDecision = {
        proposed: verdict.verdict,
        decision: panel.decision,
        quorum: panel.quorum,
        votes: panel.votes.map((v) => ({ verdict: v.verdict, reasoning: v.reasoning })),
      };

      if (panel.decision === "halt" || panel.decision === "reframe") {
        stoppedByIntrospection = {
          round,
          verdict: panel.decision,
          proposedBy: verdict.verdict,
          question: verdict.questionForHuman || verdict.reasoning,
          reasoning: verdict.reasoning,
          caseHealthy: verdict.caseHealthy,
          caseUnhealthy: verdict.caseUnhealthy,
          panel: panel.votes,
        };
        break;
      }

      // Overruled. Record it against the pass so the next introspection sees that
      // it called a stop and was not upheld, together with why. Without that the
      // pass would re-reach the same verdict on the same evidence every time it
      // ran, and the panel would re-litigate it every time.
      overruledStops.push({
        round,
        proposed: verdict.verdict,
        decidedInstead: panel.decision,
        panelReasoning: panel.votes.map((v) => v.verdict + ": " + v.reasoning),
      });
      log(
        "Round " + round + ": the panel overruled a " + verdict.verdict + " with " +
          panel.decision + "; the run continues",
      );

      // Carry out the downgrade the panel chose, rather than dropping it.
      if (panel.decision === "redesign" && redesignsRun < redesignsAllowed) {
        const named = (verdict.areas || []).length
          ? verdict.areas.map((a) => ({
              area: String(a).toLowerCase().trim(),
              findings: 0,
              designDefects: 0,
              selfInflicted: 0,
              reason: "downgraded from " + verdict.verdict + " by the stop-decision panel",
            }))
          : churn;
        if (named && named.length) {
          const did = await runRedesign(named, round, "panel downgrade from " + verdict.verdict);
          if (did) {
            retired.clear();
            history[history.length - 1].redesignApplied = true;
          }
        }
      }
    }
  }
}

converged = converged && !reviewersFailed && !stoppedByIntrospection;

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

if (converged) {
  await robustAgent(
    "Update one proposal's Status bullet to record verification.\n\n" +
      "HARD CONSTRAINT: the only file you may edit is " +
      path +
      ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
      'Read the proposal\'s header bullets. Replace the Status bullet\'s leading state (for example "Draft for review.") with: "Verified (' +
      date +
      "). Converged after " +
      round +
      " adversarial review rounds (" +
      fixedTitles.length +
      ' findings fixed); awaiting sign-off." Preserve any later clauses of the bullet that remain true (for example a pointer to the pass-history section), drop clauses the new state supersedes, and follow ' +
      repo +
      "/.claude/rules/doc-style.md.",
    { label: "mark-verified", phase: "Review" },
  );
  log("Proposal marked Verified");
}

return {
  mode,
  status: mode === "new" ? "written" : "reviewed",
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
  },
  review: {
    converged,
    reviewersFailed,
    rounds: round,
    sweeps,
    retiredLenses: [...retired],
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
