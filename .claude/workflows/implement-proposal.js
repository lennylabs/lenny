// Implement an approved spec proposal end to end: gate on its approval, then
// run its implementation checklist as one sequence through the
// implement-proposal-build subworkflow and close the findings that reference
// it. This unifies the former spec-apply (land + verify spec) and
// spec-implement (plan + build code + close findings) into one entry point.
//
//   Workflow({ name: "implement-proposal", args: {
//     proposalPath: "proposals/NNNN_*.md",  // required
//     date: "YYYY-MM-DD",                    // required (scripts cannot call Date)
//     repoRoot: "/abs/path",                 // required: the repository root
//     implementCode: true,                   // optional, default true; false = spec-only
//     leaseTtlHours: 24,                     // optional; forwarded to each spec step's lease
//     ...                                    // every other documented argument is forwarded
//                                            // verbatim to the build subworkflow; the
//                                            // implement-proposal skill carries the list and
//                                            // the defaults, which live in that subworkflow
//   }})
//
// There is one ordering and it is the proposal's implementation checklist, so
// this file raises no spec phase of its own: a spec-lane step lands its staged
// edits under a per-step lease inside the subworkflow. With implementCode false
// the run is spec-only: the subworkflow walks the checklist's LEADING spec-lane
// prefix and stops at the first non-spec step, returning "spec-only", or
// "spec-only-incomplete" when a spec step sits behind that stopping step. The
// modifier exists for close-build-gaps.sh, which needs the spec landed before it
// builds the code itself.
//
// Preconditions (the skill checks them before invoking): the proposal
// Status is "Approved", and spec/ is clean in git so the apply verification
// can diff against a clean baseline.
//
// MAINTENANCE: the implement-proposal skill documents this workflow and its
// subworkflow; keep them in sync.

export const meta = {
  name: "implement-proposal",
  description:
    "Apply an approved proposal's spec edits and verify them, then optionally implement the spec change in code and close the findings that reference it",
  phases: [
    { title: "Plan", detail: "read the proposal, gate on approval, extract staged spec edits and findings" },
    { title: "implement-proposal-build", detail: "run the checklist as one sequence: spec steps land staged edits, code steps build with tests" },
    { title: "Close findings", detail: "mark associated BUILD-GAPS.md findings CLOSED" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath || !input.date) {
  throw new Error("args.proposalPath and args.date are required");
}
// Required rather than defaulted: a default is one machine's checkout, and a
// workflow that silently runs against the wrong tree is worse than one that
// stops. change-proposal.js requires it for the same reason.
if (!input.repoRoot) throw new Error("args.repoRoot is required and missing");
const repo = input.repoRoot;
const date = input.date;
const implementCode = input.implementCode !== false; // default true
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const relProposal = input.proposalPath.startsWith("/")
  ? input.proposalPath.replace(repo + "/", "")
  : input.proposalPath;
// Where this proposal's parts live. Folder layout or legacy single file; every
// prompt below names a role through this rather than concatenating a path.
const P = proposalFiles(input.proposalPath, repo);

// No lease is opened here. The lease is per spec step inside
// implement-proposal-build.js, scoped to exactly that step's files and released
// in a finally, which is what keeps spec/ locked for most of a run. The copy
// that used to live here was dead: openLease() had no caller, so leaseOpen was
// never true and every releaseLease() returned at its first line.

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


// The model and reasoning effort every agent runs at, unless it hard-codes its
// own. Deliberately INDEPENDENT of the session's model and effort: a run that
// silently changed tier because the operator had switched their own model would
// produce results nobody could compare against an earlier run. Hard-coded
// models stay ABSOLUTE rather than relative to the base, so the tiering is only
// coherent while the base sits at or above sonnet.
const MODELS = ["opus", "sonnet", "haiku", "fable"];
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const baseModel = input.baseModel || "opus";
const baseEffort = input.baseEffort || "medium";
if (!MODELS.includes(baseModel)) {
  throw new Error('args.baseModel must be one of ' + MODELS.join(", ") + '; got "' + baseModel + '"');
}
if (!EFFORTS.includes(baseEffort)) {
  throw new Error('args.baseEffort must be one of ' + EFFORTS.join(", ") + '; got "' + baseEffort + '"');
}
// Applied at the single point every agent call in this script passes through,
// so an agent added later cannot escape the base by being written somewhere new.
function based(o) {
  const b = { ...(o || {}) };
  if (!b.model) b.model = baseModel;
  if (!b.effort) b.effort = baseEffort;
  return b;
}
log("Base tier: " + baseModel + " at " + baseEffort + " effort" +
  (input.baseModel || input.baseEffort ? " (caller-set)" : " (default)") +
  ". Agents that name their own model keep it.");

// ---- Argument classification ---------------------------------------------
//
// forward: read where it is used, present in no prompt already issued.
// anchored: baked into prompts the run has issued.
// launch: controls how a run starts.
// The workflow lint holds every `input.<name>` this script reads to appearing
// here, so the classification cannot drift away from the code.
const ARG_CLASS = {
  baseModel: "launch",
  baseEffort: "launch",
  proposalPath: "launch",
  repoRoot: "launch",
  date: "anchored",
  implementCode: "launch",
  leaseTtlHours: "forward",
  specReviewFocus: "anchored",
  acceptedDivergences: "anchored",
  plan: "anchored",
  reverifyDoneSteps: "launch",
  // Forwarded to implement-proposal-build.js and read only there. The classes
  // mirror that file's own registry so one argument does not carry two.
  skipBuild: "launch",
  maxPlanRounds: "forward",
  maxStepAttempts: "forward",
  maxDeadAttempts: "forward",
  maxVerifyRounds: "forward",
  maxReviewRounds: "forward",
  maxReplans: "forward",
  replanEvery: "forward",
  replanStruggleAttempts: "forward",
  coverageFloor: "forward",
  introspectEvery: "forward",
  minUnproductiveRounds: "forward",
  maxPhaseOscillations: "forward",
  maxFinalGateFailures: "forward",
  expensiveTierSeconds: "forward",
};

const PLAN = {
  type: "object",
  required: ["approved", "alreadyApplied", "statusLine", "specEdits", "nonSpecStaged", "findingIds"],
  properties: {
    approved: { type: "boolean", description: 'The status tool printed exactly "Approved"' },
    alreadyApplied: {
      type: "boolean",
      description:
        "The staged spec edits are already present in spec/, read from the tree rather than from the status. " +
        'Reported only; it does not affect the approval gate, and there is no "Applied to spec" status.',
    },
    statusLine: { type: "string" },
    specEdits: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "targetFile", "subsection", "summary", "method"],
        properties: {
          id: { type: "string" },
          targetFile: { type: "string", description: "Path under spec/, relative to the repo root" },
          subsection: { type: "string", description: "The proposal subsection heading that stages this edit" },
          summary: { type: "string" },
          method: {
            type: "string",
            enum: ["authored", "mechanical"],
            description:
              "authored: the proposal stages the literal text to write, and an agent applies it. " +
              "mechanical: the proposal stages a script run over a register and enumerates no edit " +
              "sites, so the script applies it and an agent must not reproduce its effect by hand.",
          },
          command: {
            type: "string",
            description:
              "For method mechanical only: the exact command the proposal states, including its " +
              "dry-run form when it has one. Empty for authored edits.",
          },
        },
      },
    },
    nonSpecStaged: {
      type: "array",
      items: {
        type: "object",
        required: ["subsection", "target", "summary"],
        properties: {
          subsection: { type: "string" },
          target: { type: "string" },
          summary: { type: "string" },
        },
      },
    },
    findingIds: {
      type: "array",
      items: { type: "string" },
      description: "OPEN findings in BUILD-GAPS.md whose body references this proposal",
    },
  },
};

const CLOSE = {
  type: "object",
  required: ["closed", "committed"],
  properties: {
    closed: { type: "array", items: { type: "string" } },
    committed: { type: "boolean" },
    notes: { type: "string" },
  },
};

// ---- Plan: read the proposal, gate, extract staged edits and findings ----

phase("Plan");
const plan = await agent(
  "Read a proposal and extract its staged changes and the findings that reference it.\n\n" +
    "The staged SPEC changes are in " + roleRef(P, "spec", "Proposed spec changes") + ".\n" +
    "The staged NON-SPEC changes are in " + roleRef(P, "nonSpec", "Proposed changes") + ".\n" +
    "The implementation sequence is in " + roleRef(P, "checklist", "## Implementation checklist") + ".\n" +
    "The status is read with `node " + repo + "/.claude/tools/proposal-status.mjs " + P.root + " --field status`," +
    " which is the ONLY supported way to read it: do not parse a Status bullet yourself.\n\n" +
    ' Read them in full and extract the staged changes and the findings that reference this proposal.\n\nYou are a read-only investigator; do not edit any file. Work in ' +
    repo +
    '.\n\nReturn:\n- approved: true when the status tool prints exactly "Approved".\n- alreadyApplied: true when the staged spec edits are ALREADY PRESENT in spec/. Check a sample of them rather than inferring it from the status: spec application is recorded per deliverable in the implementation checklist now, not as a status value, so an Approved proposal may have some, none, or all of its spec edits landed. This is reported, not a gate: only the status decides whether the run proceeds.\n- statusLine: what the status tool printed.\n- specEdits: one entry per staged change whose target file is under spec/, from the "Proposed spec changes" section: id (the subsection number, e.g. "7.1"), targetFile (the spec/ path), subsection (the heading), summary. A subsection targeting multiple spec files becomes one entry per file. Classify each entry\'s method. Use "mechanical" when the proposal stages the edit as a run of a script, pass, or generator over a register or map rather than as literal text to write, which a proposal signals by enumerating no edit sites, by naming a command, or by stating that completeness is proven by a gate rather than by review; put the exact command in command, including its dry-run form when the proposal states one. Use "authored" when the proposal stages the literal text together with an anchor for it. When one subsection stages both, split it into one mechanical entry and one authored entry. Defaulting to "authored" for an edit the proposal means a script to make is a defect: it sets an agent guessing at sites the proposal deliberately does not list.\n- nonSpecStaged: one entry per staged change whose target is outside spec/ (code, charts, docs, schemas). These are implemented in the code phase or reported, never hand-applied here.\n- findingIds: every OPEN finding in BUILD-GAPS.md whose body references this proposal by path or number (the file is large; grep for the proposal number and filename). Empty array if none.',
  based({ schema: PLAN, label: "plan", phase: "Plan" }),
);

// agent() returns null when the subagent never ran: the account hit a usage
// limit, or the call died on a terminal API error after its own retries. The
// dereference below then crashed the run with a bare TypeError ("Cannot read
// properties of null (reading 'approved')") and no result, which reads as a
// workflow defect rather than as the account condition it is. Reproduced with
// the plan agent stubbed to null. Every other agent result in this pipeline is
// guarded the same way.
if (!plan) {
  return {
    status: "aborted",
    abortReason:
      "the plan agent returned no result (it was skipped or errored); the proposal was never read, so nothing was implemented",
    findingsReferencing: [],
  };
}

// Approval gates on approval alone. `alreadyApplied` is a fact about the tree,
// so ORing it in let a Draft proposal whose spec edits an interrupted earlier
// run had already landed pass as approved: a stubbed run with
// {approved:false, alreadyApplied:true, statusLine:"Draft - do not implement"}
// entered implement-proposal-build and returned "implemented-not-green". The
// spec-lease hook refuses a non-Approved proposal (spec-lease.mjs), so this is
// the only gate the code lane has.
if (!plan.approved) {
  return {
    status: "not-approved",
    statusLine: plan.statusLine,
    reason:
      "the proposal is not approved for implementation (Status: " +
      plan.statusLine +
      "). Approve it before implementing.",
  };
}

const files = [...new Set(plan.specEdits.map((e) => e.targetFile))];

// ---- One execution sequence ----------------------------------------------
//
// Spec used to be a PHASE here: every staged edit landed, verified and
// committed before implement-proposal-build was invoked at all. That made the
// proposal's implementation checklist carry a `spec` lane the executor ignored,
// so one proposal had two orderings and two progress records that could
// disagree -- and the alignment-repair branch existed only to reconcile them
// after they had.
//
// There is one sequence now, and it is the checklist. A spec-lane step applies
// the deliverables its line names under a lease scoped to their files, verifies
// and commits, and ticks the same box a code step ticks. Progress is
// per-deliverable, in one place, and strictly more informative than the
// "Applied to spec" state it replaces -- which is why `.status.md` carries the
// four states and no fifth.
//
// The safety the phase provided is not lost, it is restated more precisely: a
// step may only depend on spec deliverables whose steps are already ticked.
// That is checkable from the checklist's own Depends-on lines and is sharper
// than a phase boundary, which said nothing about WHICH spec statement a code
// step was written against.
//
// This phase now only reports what the plan found, and the sequence runs below.

const specStaged = plan.specEdits.length;
if (specStaged === 0) {
  log("The proposal stages no spec edits");
} else {
  log(
    specStaged + " staged spec edit(s) across " + files.length +
      " file(s); they land as the checklist's spec-lane steps rather than as a phase",
  );
}

let specStatus = specStaged === 0 ? "no-spec-edits" : "sequenced";

// ---- Implement code (optional) via the build subworkflow ----

// The implement-proposal-build subworkflow IS the implement stage: it runs
// inline and brings its own phase group (Plan, Build, Verify, Review) under a
// "▸ implement-proposal-build" heading, so no redundant parent "Implement"
// phase wraps it.
log(implementCode
  ? "Implementing the spec change via the implement-proposal-build subworkflow"
  : "Spec-only: running the checklist's leading spec-lane prefix via the implement-proposal-build subworkflow");
let build;
try {
  // Invoked by path rather than by name. A named workflow resolves to a copy
  // the runtime cached, which can be older than the file on disk: a run
  // launched six seconds after a commit to this subworkflow used the previous
  // revision, so several fixes appeared to have no effect and were debugged
  // twice. A path is read from disk at call time, so the child that runs is the
  // child that was edited.
  build = await workflow(
    { scriptPath: repo + "/.claude/workflows/implement-proposal-build.js" },
    {
      proposalPath: input.proposalPath,
      date,
      repoRoot: repo,
      reverifyDoneSteps: !!input.reverifyDoneSteps,
      acceptedDivergences: input.acceptedDivergences,
      plan: input.plan,
      specOnly: !implementCode,
      leaseTtlHours: input.leaseTtlHours,
      // Every remaining tuning argument the skill documents is read only in
      // the child, so the parent has to hand each one across or the documented
      // entry point cannot reach it. A run that passed maxStepAttempts: 7 and
      // a spec review focus here reached the child with neither, so the
      // bound had no effect and the focused per-spec-step reviewer could not be
      // switched on at all. Each value is passed verbatim rather than defaulted:
      // the child already defaults every one, and a second default table here is
      // a second thing to drift.
      baseModel: input.baseModel,
      baseEffort: input.baseEffort,
      skipBuild: input.skipBuild,
      specReviewFocus: input.specReviewFocus,
      maxPlanRounds: input.maxPlanRounds,
      maxStepAttempts: input.maxStepAttempts,
      maxDeadAttempts: input.maxDeadAttempts,
      maxReplans: input.maxReplans,
      replanEvery: input.replanEvery,
      replanStruggleAttempts: input.replanStruggleAttempts,
      maxVerifyRounds: input.maxVerifyRounds,
      maxReviewRounds: input.maxReviewRounds,
      coverageFloor: input.coverageFloor,
      introspectEvery: input.introspectEvery,
      minUnproductiveRounds: input.minUnproductiveRounds,
      maxPhaseOscillations: input.maxPhaseOscillations,
      maxFinalGateFailures: input.maxFinalGateFailures,
      expensiveTierSeconds: input.expensiveTierSeconds,
    },
  );
} catch (e) {
  return {
    status: "aborted",
    abortReason: "implement-proposal-build subworkflow failed: " + (e && e.message),
    specStatus,
    findingsReferencing: plan.findingIds,
  };
}

// A subworkflow that RETURNS null did not throw, so the catch above never sees
// it, and every dereference below sat outside the try that exists for exactly
// this case. Reproduced with the subworkflow stubbed to null: "Cannot read
// properties of null (reading 'steps')". A null result is the same condition as
// a throw, so it takes the same exit.
if (!build) {
  return {
    status: "aborted",
    abortReason: "implement-proposal-build subworkflow returned no result",
    specStatus,
    findingsReferencing: plan.findingIds,
  };
}

// The subworkflow stopped at the end of the leading spec prefix, so the
// green/reviewClean fields the "Implementation done" log reports are
// meaningless here: no code was built and no tier was run.
if (!implementCode) {
  log("Spec-only run finished: " + build.status + ", " + ((build.steps || []).length) + " step(s) applied");
  return {
    // PASS THE SUBWORKFLOW'S STATUS THROUGH. Collapsing everything that is not
    // "spec-only-incomplete" into "spec-only" turned every failure the child
    // reports -- a step that would not apply, a leaked lease, a step on the
    // wrong lane -- into the success status. Both the skill and the build loop
    // are told to STOP on those, so a spec step that never landed reported a
    // clean run and the loop went on to build code against spec text that was
    // never applied. The two statuses this mode may legitimately return are
    // named; anything else is the child's own verdict and is not ours to
    // rewrite.
    status:
      build.status === "spec-only" || build.status === "spec-only-incomplete"
        ? build.status
        : build.status || "aborted",
    proposal: relProposal,
    specStatus,
    statusLine: plan.statusLine,
    files,
    steps: build.steps || [],
    commits: build.commits || [],
    stoppedAt: build.stoppedAt,
    specStepsBehind: build.specStepsBehind || [],
    reason: build.reason,
    nonSpecStaged: plan.nonSpecStaged,
    findingsReferencing: plan.findingIds,
    // Carried through rather than dropped: the skill requires these reported
    // whenever they are non-empty, and a spec-only return leaves the build
    // workflow before the block that would otherwise assemble them.
    checklistDeviations: build.checklistDeviations || [],
    skippedSteps: build.skippedSteps || [],
    reverifyRepaired: build.reverifyRepaired || [],
    proposalEdits: build.proposalEdits || null,
    deviations: build.deviations || [],
    gateMisses: build.gateMisses || [],
  };
}

log(
  "Implementation done: " +
    (build.steps ? build.steps.length : 0) +
    " steps, green=" +
    !!build.green +
    ", reviewClean=" +
    !!build.reviewClean +
    (build.status === "step-stuck" ? ", stuck at step " + build.stuckStep : ""),
);

// ---- Close the findings that reference the proposal ----

phase("Close findings");
let close = { closed: [], committed: false };
if (plan.findingIds.length > 0) {
  if (!build.green || !build.reviewClean) {
    log("Implementation is not green or the design-conformance review is not clean; leaving findings OPEN for review");
  } else {
    const closeResult = await agent(
      "Close the BUILD-GAPS.md findings whose implementation just landed, then commit.\n\n" +
        "HARD CONSTRAINT: the only file you may edit is " +
        repo +
        "/BUILD-GAPS.md. Do not edit the proposal, spec/, or code.\n\n" +
        "Proposal implemented and verified green: " +
        relProposal +
        ". Implementing commits: " +
        (build.commits || []).join(", ") +
        ".\nFindings to close: " +
        plan.findingIds.join(", ") +
        ".\n\nFor each finding: re-read it, confirm the landed implementation resolves it (the proposal's spec edits are applied and its code blast radius is implemented and green), flip the heading checkbox to [x] and the trailing marker to CLOSED, and add a one or two sentence Resolution note citing the proposal path and the implementing commit SHA(s). Do not open or re-open any finding. Then commit BUILD-GAPS.md on the current branch following the repository's commit conventions; the commit message and the Resolution note reference durable sources only (the proposal file path, the finding id, the spec section, the commit SHA), never the proposal's internal change/section/decision/pass/step labels. Return the IDs you closed.",
      based({ schema: CLOSE, label: "close-findings", phase: "Close findings" }),
    );
    // A dead closing agent closed nothing. Overwriting the empty record
    // declared above with null broke `close.closed` in the return below, after
    // the whole build had landed and committed, which is the most expensive
    // place in this workflow to lose a result. Keeping the empty record
    // preserves the return's contract, and the log says the findings are still
    // OPEN, which is what the operator has to act on.
    if (closeResult) close = closeResult;
    else log("The finding-closing agent returned no result; the findings are left OPEN");
  }
}

return {
  status:
    build.status === "aborted"
      ? "build-aborted"
      : build.status === "step-stuck"
        ? "build-step-stuck"
        : build.green && build.reviewClean
          ? "implemented"
          : "implemented-not-green",
  proposal: relProposal,
  specStatus,
  blastRadius: build.blastRadius,
  steps: build.steps,
  commits: build.commits,
  green: !!build.green,
  reviewClean: !!build.reviewClean,
  reviewFindings: build.reviewFindings || [],
  stuckStep: build.stuckStep,
  changedLineCoverage: build.changedLineCoverage,
  failures: build.failures || [],
  resumeNote: build.resumeNote,
  findingsReferencing: plan.findingIds,
  findingsClosed: close.closed || [],
};
