// The open-decisions-and-impact-review phase of change-proposal, as a
// subworkflow of its own.
//
// The phase ADJUDICATES AND AUTHORS. It collects the decisions a proposal
// leaves open — the human's, the implementor's, the defects it declares out of
// scope, and what it does to the other proposals on disk — decides which of
// them the workflow can answer, falsifies each disposition, writes the answers
// into the staged change files, and leaves the summary carrying the survivors
// and only those. It files no findings: a review lens emits a defect claim and
// the repair gate admits a resolution only where the resolution happens also to
// be a defect, which is a narrow band chosen by coincidence rather than by the
// decision's importance. That is why this is a phase rather than a lens.
//
// The parent fires it after every review loop and periodically inside the
// non-spec loop, invoking it BY SCRIPT PATH rather than by name: a named
// workflow resolves to a copy the runtime cached, which can be older than the
// file on disk, so a path is read at call time and the child that runs is the
// child that was edited. One invocation is one firing. Cross-firing state
// (the per-item records, the corpus inventory, and the firing counter) is held
// by the parent, handed in as `phaseState`, and returned updated.
//
// MAINTENANCE: the change-proposal skill (.claude/skills/change-proposal)
// documents this subworkflow; keep its description in sync.

export const meta = {
  name: "change-proposal-decisions",
  description:
    "Adjudicate and author a proposal's open decisions and its impacts on other proposals: the open-decisions-and-impact-review phase of change-proposal",
  phases: [
    { title: "Collect", detail: "the human's decisions, the implementor's blanks, the out-of-scope defect declarations, and the impacts on other proposals, each under its own brief" },
    { title: "Falsify", detail: "one agent per item, briefed to refute that item's disposition rather than confirm it" },
    { title: "Apply", detail: "commit the proposal, then one agent per surviving item that needs an edit, sequentially" },
    { title: "Cleanup", detail: "rewrite the summary to the listed sections, in order, relocating what the list does not name" },
    { title: "Verify", detail: "read-only check of factual accuracy and format conformance" },
  ],
};

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || typeof input !== "object") {
  throw new Error(
    "args must be a JSON object or a JSON-encoded object string, received " + typeof args,
  );
}
// Required rather than defaulted, following implement-proposal-build.js: a
// default is one machine's checkout, and a workflow that silently runs against
// the wrong tree is worse than one that stops. There is no sensible default
// proposal either: a firing that adjudicated the wrong proposal would author
// into it.
for (const k of ["proposalPath", "repoRoot"]) {
  if (!input[k]) throw new Error("args." + k + " is required and missing");
}
const repo = input.repoRoot;
// The parent passes this; a workflow script cannot call Date, so anything that
// stamps a date takes it from here.
const date = input.date || "";
// Namespaces anything this firing writes under a run, exactly as it does in the
// parent, so two runs against one proposal do not read each other's.
const runTag =
  (typeof input.runTag === "string" && input.runTag.trim()) ||
  String(input.proposalPath)
    .replace(/^.*\//, "")
    .replace(/\.md$/, "")
    .replace(/[^A-Za-z0-9._-]/g, "-");

// Every site the parent fires this phase from. The label is carried through the
// log lines and the result so an operator reading a run can tell which firing
// wrote what, and it is validated rather than accepted freely because a
// misspelled trigger would otherwise silently name a site that does not exist.
// A caller that names no site gets "unspecified", which names none: picking one
// of the real sites as a default would put a false one in the log.
const TRIGGERS = [
  // Before the spec review rather than after it, when the caller sets
  // decisionsFirst. It was added to the parent without being added here, and the
  // child rejected its own argument before spawning anything: the parent caught
  // the throw, recorded a failed firing, and ran the spec loop as if the phase
  // had simply found nothing. The harness could not catch it because it stubs
  // the subworkflow's RETURN and never executes the child.
  "pre-spec-loop",
  "post-spec-loop",
  "post-non-spec-loop",
  "post-spec-recheck",
  "post-non-spec-recheck",
  "periodic",
  "unspecified",
];
const trigger = input.trigger || "unspecified";
if (!TRIGGERS.includes(trigger)) {
  throw new Error(
    "args.trigger must be one of " + TRIGGERS.join(", ") + '; got "' + trigger + '"',
  );
}

// The cross-firing state the parent holds. Spread rather than rebuilt, so a key
// this frame does not yet know about survives the round trip.
const phaseState =
  input.phaseState && typeof input.phaseState === "object" ? { ...input.phaseState } : {};
// The firing's ordinal within the run. The parent numbers the firings; the
// counter on the phase state is the fallback so a firing is still numbered when
// the caller does not say.
const firing = input.firing || (phaseState.firings || 0) + 1;
phaseState.firings = firing;
if (trigger === "periodic") {
  phaseState.periodicFirings = (phaseState.periodicFirings || 0) + 1;
}
// The periodic firing is the only one whose count is open-ended, so it is the
// only one that takes a count budget. Exhausting it stops the PERIODIC trigger
// for the rest of the run and leaves every post-loop firing running; the gate
// is the parent's, and this reports the stop so it is visible in the log and in
// the result rather than only in the parent's control flow.
const maxPeriodicFirings = input.maxPeriodicFirings || 5;
const periodicBudgetSpent = (phaseState.periodicFirings || 0) >= maxPeriodicFirings;

// The run-wide refuted list the parent accumulates across both loops. An item an
// earlier firing routed to the human may be resolvable once the skeptics have
// refuted the ground it rested on, which is why the list reaches the collectors.
const rejected = Array.isArray(input.rejected) ? input.rejected : [];
// When set, the staged spec edits are closed to this phase and a resolution that
// needs them is recorded for the operator instead. It is the operator's existing
// control over the run and it is honoured here rather than reasoned around.
const lockSpecChanges = !!input.lockSpecChanges;

// ---- Where a proposal's parts live ---------------------------------------
//
// Verbatim from change-proposal.js, so a path resolves the same way in the
// parent and in this phase. A proposal is a directory of role-scoped files, and
// a proposal written before that layout is a single NNNN_kind_slug.md; both
// resolve here so no prompt concatenates a path by hand. The layout is decided
// from the path string rather than by looking, because a workflow script has no
// filesystem access.
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
      logArchive: abs,
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
    logArchive: f("review-log-archive"),
    deviations: f("deviations"),
  };
}

// How a prompt names a role, so one sentence works for both layouts. On a
// folder proposal it is a file; on a legacy one it is a section of the one file,
// and saying so is the difference between an agent reading the right thing and
// an agent reading nothing. Every brief in this phase names its roles through
// this rather than through a path it built itself.
function roleRef(P, role, sectionName) {
  return P.layout === "folder"
    ? P[role]
    : "the `" + sectionName + "` section of " + P.root;
}

const P = proposalFiles(input.proposalPath, repo);
// The one pathspec this phase stages, commits, and diffs under. It is the scope
// the skill's report step already enforces on a run: everything this phase
// writes is inside the proposal, and anything outside it belongs to another
// actor. On a folder proposal it is the directory and on a legacy one it is the
// single file, so a sibling proposal is outside the scope in both layouts.
const PATHSPEC = P.root;

// ---- Model tier -----------------------------------------------------------
//
// Deliberately INDEPENDENT of the session's model and effort, and of the
// parent's own default: the parent passes its base through so the phase runs at
// the tier the run was launched at, and two firings of one proposal stay
// comparable. Hard-coded models stay ABSOLUTE rather than relative to the base.
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

// ---- Argument classification ---------------------------------------------
//
// forward: read where it is used, present in no prompt already issued.
// anchored: baked into prompts the run has issued.
// launch: controls how a run starts.
//
// The workflow lint holds every `input.<name>` this script reads to appearing
// here, so the classification cannot drift away from the code.
const ARG_CLASS = {
  proposalPath: "launch",
  repoRoot: "launch",
  date: "anchored",
  runTag: "anchored",
  firing: "launch",
  trigger: "launch",
  rejected: "anchored",
  phaseState: "forward",
  lockSpecChanges: "forward",
  baseModel: "launch",
  baseEffort: "launch",
  maxPeriodicFirings: "forward",
};

// ---- Agent plumbing -------------------------------------------------------
//
// missingRequired and robustAgent are the parent's, with the parent's rationale:
// a schema'd return missing a top-level required key is treated as a null
// return, and a transient API failure is retried at the base tier twice before
// falling back to sonnet. Both are reproduced here rather than shared because a
// subworkflow is a separate script with no import path to the parent.
function missingRequired(r, schema) {
  if (!r || typeof r !== "object") return [];
  if (!schema || !Array.isArray(schema.required)) return [];
  return schema.required.filter((k) => !Object.prototype.hasOwnProperty.call(r, k));
}

// agent() returns null when the runtime's own retries are exhausted under a
// sustained overload. A dropped agent here is not a quiet zero: a dead collector
// leaves its population unadjudicated rather than empty, and a dead Apply leaves
// its item unapplied, so every caller guards the null and records it.
async function robustAgent(prompt, opts, attempts = 4) {
  // Every agent runs at the configured base unless it names its own model. This
  // is the single point where that is applied: there is exactly one agent() call
  // in this workflow and it is below, so an agent cannot escape the base by
  // being added somewhere new.
  const based = { ...(opts || {}) };
  if (!based.model) based.model = baseModel;
  if (!based.effort) based.effort = baseEffort;
  opts = based;
  const fallbackAt = 3;
  for (let i = 1; i <= attempts; i++) {
    const callOpts = i >= fallbackAt ? { ...opts, model: "sonnet" } : opts;
    const r = await agent(prompt, callOpts);
    if (r !== null && r !== undefined) {
      const missing = missingRequired(r, opts.schema);
      if (missing.length === 0) return r;
      log(
        "  " +
          (opts && opts.label ? opts.label : "agent") +
          ": return is missing required field(s) " +
          missing.join(", ") +
          ", discarding it",
      );
    }
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

// Agents whose call returned nothing after every retry. Reported, because a
// firing that lost an agent adjudicated less than it says it did.
const deadAgents = [];
function recordDead(label) {
  if (!deadAgents.includes(label)) deadAgents.push(label);
}

// ---- The commit -----------------------------------------------------------
//
// Taken before the Apply stage of every firing, unconditionally. It is what
// makes the change detection below decidable: with the proposal committed, the
// working-tree delta under the pathspec is exactly what this firing wrote, and
// a later firing comparing against it can tell a reversal from a rewording.
//
// No other stage in this workflow commits and the skill says not to commit
// unless asked; the operator's instruction is the asking, and the staging scope
// is the proposal alone so nothing else in the tree is swept into it.
const COMMIT_RESULT = {
  type: "object",
  required: ["outcome", "outsideProposal"],
  properties: {
    outcome: {
      type: "string",
      enum: ["committed", "empty", "failed"],
      description:
        '"committed" when the commit was created, "empty" when there was nothing to commit under the pathspec, "failed" when git refused for any other reason',
    },
    sha: {
      type: "string",
      description: "the commit HEAD is at after the attempt, whether or not this firing created it",
    },
    error: {
      type: "string",
      description: "on failed, the git error verbatim; empty otherwise",
    },
    outsideProposal: {
      type: "array",
      items: { type: "string" },
      description:
        "paths git status reports as changed outside the proposal pathspec, left uncommitted",
    },
  },
};

async function commitBaseline() {
  const label = "f" + firing + ":commit";
  const message =
    "change-proposal " + runTag + ": baseline before open-decisions firing " +
    firing +
    " (" +
    trigger +
    ")" +
    (date ? " on " + date : "");
  const res = await robustAgent(
    "Commit the proposal as it stands, so a later diff can tell what the next stage writes. " +
      "Do not edit any file and do not stage anything outside the pathspec named below.\n\n" +
      "Run these commands in order, in " + repo + ":\n" +
      "1. `git add -A -- " + PATHSPEC + "`\n" +
      '2. `git commit -q -m "' + message + '" -- ' + PATHSPEC + "`\n" +
      "3. `git rev-parse HEAD`\n" +
      "4. `git status --porcelain`\n\n" +
      "THE PATHSPEC IS THE WHOLE SCOPE. Stage and commit " + PATHSPEC + " and nothing else. Never run " +
      "`git add` without it, never `git commit -a`, and never `git checkout`, `git reset`, `git stash`, " +
      "or any other command that discards work.\n\n" +
      "REPORT, in outcome:\n" +
      '- "committed" when step 2 created a commit. Put its SHA in sha.\n' +
      '- "empty" when step 2 refused because there was nothing to commit under that pathspec. That is a ' +
      "normal outcome, not a failure: this firing may follow a stage that changed nothing. Put the SHA " +
      "step 3 prints in sha.\n" +
      '- "failed" for anything else git refused, with its message verbatim in error.\n\n' +
      "In outsideProposal, list every path step 4 reports as changed that is NOT under " + PATHSPEC + ". " +
      "Another actor changed those and they are none of this run's business: leave them uncommitted and " +
      "report them. An empty list is the ordinary answer.",
    { label, schema: COMMIT_RESULT, model: "haiku", effort: "high", phase: "Apply" },
  );
  if (!res) {
    recordDead(label);
    return { outcome: "failed", error: "the commit agent returned nothing after retries", outsideProposal: [] };
  }
  return res;
}

// ---- Change detection from the tree ---------------------------------------
//
// What this firing changed is read from git rather than taken from the agents.
// An agent that says it wrote a resolution and wrote nothing is the failure this
// closes, and it is not detectable from the agent's own report by construction.
const TREE_DELTA = {
  type: "object",
  required: ["files", "outsideProposal"],
  properties: {
    files: {
      type: "array",
      items: {
        type: "object",
        required: ["path", "added", "removed"],
        properties: {
          path: { type: "string", description: "repository-relative path" },
          added: { type: "number", description: "lines added, 0 for a file with no line changes" },
          removed: { type: "number" },
          untracked: { type: "boolean", description: "true when the file is new and not yet tracked" },
        },
      },
      description: "every file changed under the proposal pathspec since the baseline commit",
    },
    outsideProposal: {
      type: "array",
      items: { type: "string" },
      description: "paths changed outside the proposal pathspec",
    },
  },
};

// The working-tree delta under the proposal pathspec since the baseline commit.
// `tag` names the point it is taken at, so two deltas in one firing do not
// collide on one agent label.
async function treeDelta(tag) {
  const label = "f" + firing + ":delta:" + tag;
  const res = await robustAgent(
    "Report what has changed in the working tree under one pathspec. Do not edit anything.\n\n" +
      "Run these commands in " + repo + ":\n" +
      "1. `git status --porcelain -- " + PATHSPEC + "`\n" +
      "2. `git diff --numstat -- " + PATHSPEC + "`\n" +
      "3. `git status --porcelain`\n\n" +
      "In files, put one entry per path step 1 or step 2 names, with the added and removed line counts " +
      "step 2 gives it. A path step 1 reports as untracked has no numstat line: count its lines with " +
      "`wc -l`, report them as added with removed 0, and set untracked true. A numstat line reading `-` " +
      "for a binary file is 0 added and 0 removed.\n\n" +
      "In outsideProposal, list every path step 3 reports that step 1 does not, which is everything " +
      "changed outside " + PATHSPEC + ".\n\n" +
      "An empty files array is a valid and meaningful answer: it means nothing under the pathspec has " +
      "changed since the last commit. Report it rather than looking for something to put there.",
    { label, schema: TREE_DELTA, model: "haiku", effort: "high", phase: "Apply" },
  );
  if (!res) {
    recordDead(label);
    return null;
  }
  return {
    files: Array.isArray(res.files) ? res.files : [],
    outsideProposal: Array.isArray(res.outsideProposal) ? res.outsideProposal : [],
  };
}

// What the Apply stage records for one item, decided from the tree rather than
// from the agent's claim. The per-item Apply loop calls it once that item's
// agent has returned and the delta around it has been taken. An Apply that reports a resolution landed while the
// delta taken around it is empty did not land it, and recording that as applied
// would make the phase's report of what it changed a report of what its agents
// said. A dead delta (the agent returned nothing) is not evidence of an empty
// diff, so the item is failed for the missing evidence rather than for the
// missing edit, and the two read differently in the result.
function applyOutcome(item, claim, delta) {
  const base = { id: item.id, disposition: item.disposition, question: item.question };
  if (!claim) {
    return { ...base, status: "failed", reason: "the Apply agent returned nothing after retries", files: [] };
  }
  if (!delta) {
    return {
      ...base,
      status: "failed",
      reason: "no git evidence: the change-detection agent returned nothing after retries",
      files: [],
    };
  }
  if (delta.files.length === 0) {
    return {
      ...base,
      status: "failed",
      reason: "the Apply reported a resolution landed and the diff under the proposal pathspec is empty",
      files: [],
    };
  }
  return { ...base, status: "applied", claim, files: delta.files };
}

// ---- The corpus inventory -------------------------------------------------
//
// Each proposal on disk with its status and the date that status was reached,
// which is what routes an impact: a draft may be invalidated freely, a recently
// reviewed proposal warrants care, and an implemented one is already in the
// tree.
//
// Gathered ONCE per run and carried on the phase state, because it is a fact
// about the other proposals rather than about this one and it does not move
// between firings. The impact ASSESSMENT re-runs at every firing, because what
// this proposal does to each other proposal is a function of the staging as it
// stands at that firing.
const CORPUS = {
  type: "object",
  required: ["proposals"],
  properties: {
    proposals: {
      type: "array",
      items: {
        type: "object",
        required: ["proposal", "status", "date", "dateSource"],
        properties: {
          proposal: { type: "string", description: "the entry's name under proposals/" },
          status: {
            type: "string",
            description: "Draft, Reviewed, Approved, Implemented, Retired, or unknown when the tool refused it",
          },
          date: { type: "string", description: "YYYY-MM-DD, or empty when nothing dates it" },
          dateSource: {
            type: "string",
            enum: ["approved", "reviewed", "drafted", "commit", "none"],
            description:
              'which date the row carries; "commit" is the last-commit fallback for a proposal whose status record has no date of its own',
          },
        },
      },
    },
  },
};

async function corpusInventory() {
  if (Array.isArray(phaseState.corpus) && phaseState.corpus.length > 0) {
    log("Corpus inventory: reusing " + phaseState.corpus.length + " row(s) gathered earlier in this run");
    return phaseState.corpus;
  }
  const label = "f" + firing + ":corpus";
  const res = await robustAgent(
    "Inventory every proposal on disk: its status and the date that status was reached. Do not edit " +
      "anything.\n\n" +
      "Run this in " + repo + ". For each proposal it prints the entry's name, the status tool's JSON, " +
      "and the entry's last commit date:\n\n" +
      "for p in " + repo + "/proposals/[0-9][0-9][0-9][0-9]*; do " +
      'echo "== $(basename "$p")"; ' +
      'node ' + repo + '/.claude/tools/proposal-status.mjs "$p" --json 2>&1 | head -40; ' +
      'echo "-- last commit: $(git -C ' + repo + ' log -1 --format=%ad --date=short -- "$p")"; ' +
      "done\n\n" +
      "For each entry, report one row.\n\n" +
      "THE DATE. `proposal-status.mjs` carries `approved-date`, `reviewed-date`, and `drafted-date` for a " +
      "FOLDER-layout proposal. Take the latest of those the row actually has, and set dateSource to which " +
      "one it was. A LEGACY single-file proposal carries none of them, so fall back to the last commit " +
      "date the command printed for it and set dateSource to \"commit\". Say so rather than presenting a " +
      "commit date as a review date: a commit date is when someone last touched the file, which is not " +
      "when it was reviewed. Nearly every proposal on disk is legacy, so the fallback is the ordinary " +
      "path here rather than an exception.\n\n" +
      "THE STATUS. Take it verbatim from the tool. When the tool refuses an entry (it prints an error and " +
      "no status), report the status as \"unknown\", the date as the last commit date, and dateSource as " +
      "\"commit\"; do not guess a status from the file's prose, and do not drop the row.\n\n" +
      "Report every entry the loop printed, including this proposal's own.",
    { label, schema: CORPUS, model: "haiku", effort: "high", phase: "Collect" },
  );
  if (!res) {
    recordDead(label);
    // An inventory that could not be gathered is not an empty corpus. It is left
    // off the phase state so a later firing gathers it rather than inheriting
    // the hole, and sub-task 4 reads the empty array as "unadjudicated".
    log("Corpus inventory: unavailable after retries; the impact assessment runs without it");
    return [];
  }
  const rows = Array.isArray(res.proposals) ? res.proposals : [];
  phaseState.corpus = rows;
  log("Corpus inventory: " + rows.length + " proposal(s), gathered once for this run");
  return rows;
}

// ---- The cross-firing record ----------------------------------------------
//
// The phase state the parent holds between firings and hands back in. It
// carries the firing counter (set at the top of this file), the corpus
// inventory (gathered once per run, above), and the per-item record built
// here, keyed by the identifier a collector returns. Each record holds the
// item's disposition and, for an applied item, what was written and where.
//
// It travels as an OBJECT through the argument and the return rather than
// through the run-scoped `scratchpad/cp-state/<runTag>/` file the review loop
// uses, because a workflow script has no filesystem access: the file route
// would cost an agent per read and per write on every firing, while the object
// route is directly assertable at the recorded sub-call.
//
// A REVERSAL IS NOT RE-ARGUED. Before this firing adjudicates anything, it
// checks each applied item to see whether the text it wrote is still there,
// reading the tree rather than asking an agent to re-judge the decision. An
// item the review loop reverted or overwrote is marked CONTESTED, recorded
// with both positions, and routed to the human; it is never re-applied and
// never re-adjudicated. That is the whole of the loop-prevention requirement,
// and it is the opposite of reopening an item because its text changed:
// reopening on change is what makes the phase insist, because the loop
// reverses an edit, the phase sees different text, and it argues the point
// again. An item nothing has touched carries its earlier disposition forward
// untouched, which is what keeps a firing over unchanged staging cheap.
//
// Matching a collected item to its record goes through the IDENTIFIER, so an
// item the review loop merely reworded still matches and carries its
// disposition forward. Reversal detection does not go through the identifier:
// the record holds the text and its location, and the check below reads the
// tree.

function records() {
  if (!phaseState.itemRecords || typeof phaseState.itemRecords !== "object") {
    phaseState.itemRecords = {};
  }
  return phaseState.itemRecords;
}

// Whether the text an earlier firing wrote is still in the tree. It is a
// mechanical read, like the commit and the delta above, rather than a judgment:
// the question is whether a string is present, and an agent asked to judge
// whether the decision still holds would re-argue the item, which is what this
// exists to prevent.
const REVERSAL_CHECK = {
  type: "object",
  required: ["items"],
  properties: {
    items: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "state"],
        properties: {
          id: { type: "string", description: "the identifier the item was given, copied back verbatim" },
          state: {
            type: "string",
            enum: ["present", "absent", "undecidable"],
            description:
              '"present" when the recorded text is still in the proposal, "absent" when it is not, "undecidable" when you could not read the file or tell',
          },
          nowCarries: {
            type: "string",
            description:
              "on absent, what the recorded location carries instead, verbatim and short; empty otherwise",
          },
        },
      },
    },
  },
};

// The check itself. What it finds is written onto the records, which is where
// every later stage reads it from: a contested item is routed to the human
// whether or not a collector saw it again this firing, because an entry the
// review loop deleted outright is the reversal that leaves nothing to collect.
async function checkReversals() {
  const pending = Object.values(records()).filter(
    (r) => r.applyStatus === "applied" && r.wrote && !r.contested,
  );
  if (pending.length === 0) return [];
  const contestedNow = [];
  const label = "f" + firing + ":reversal-check";
  const res = await robustAgent(
    "Report whether each recorded piece of text is still in the proposal. Do not edit anything and do " +
      "not judge whether it should be there.\n\n" +
      "The proposal is at " + P.root + " in " + repo + ".\n\n" +
      "For each item below, search that directory for the recorded text. Match on the text itself " +
      "rather than on the location: a passage moved to another section of the same file is PRESENT. " +
      "Search for a distinctive sentence from it when the whole passage is long, and read the file the " +
      "location names first.\n\n" +
      "Report present when the text is there, absent when it is not, and undecidable when you could not " +
      "read the file or could not tell. On absent, put what the recorded location carries instead in " +
      "nowCarries, verbatim and short. Undecidable is an honest answer and is treated as such; do not " +
      "report absent because you did not find it quickly.\n\n" +
      JSON.stringify(
        pending.map((r) => ({ id: r.id, where: r.where, text: String(r.wrote).slice(0, 1200) })),
        null,
        1,
      ),
    { label, schema: REVERSAL_CHECK, model: "haiku", effort: "high", phase: "Collect" },
  );
  if (!res) {
    recordDead(label);
    // Not a reversal. A dead check is no evidence either way, and the records
    // stand as they are: the items it covers carry their dispositions forward
    // and are neither re-adjudicated nor re-applied, so nothing loops on it.
    log(
      "Reversal check: unavailable after retries over " + pending.length +
        " applied item(s); their records stand and none is marked contested",
    );
    return [];
  }
  const byId = new Map();
  for (const v of Array.isArray(res.items) ? res.items : []) {
    if (v && typeof v === "object" && v.id) byId.set(String(v.id), v);
  }
  for (const rec of pending) {
    const v = byId.get(rec.id);
    // Only "absent" contests a record. A missing row and an undecidable read
    // are the same answer as the dead agent above.
    if (!v || v.state !== "absent") continue;
    rec.contested = {
      appliedAtFiring: rec.firing,
      contestedAtFiring: firing,
      wrote: rec.wrote,
      where: rec.where,
      nowCarries: String(v.nowCarries || "").slice(0, 400),
    };
    rec.disposition = "human";
    contestedNow.push(rec);
    log(
      "  CONTESTED: " + rec.id + " was applied at firing " + rec.contested.appliedAtFiring +
        " and the text it wrote is gone; it is routed to the human and never re-applied",
    );
  }
  log(
    "Reversal check: " + pending.length + " applied item(s) read from the tree, " + contestedNow.length +
      " contested",
  );
  return contestedNow;
}

// Match each freshly collected item to its record and partition the batch. A
// match keeps the record, which is what the contested suppression and the
// carry-forward rest on. An item that matches nothing is adjudicated afresh,
// and a record nothing matched is recorded as unmatched under its own prior
// identifier so the operator can see it.
// The row an `impact-row` item derives at this firing, normalised for the
// comparison below. It is the recommendation, which sub-task 4's brief fixes as
// the row itself as it should stand under `## Impacts on other proposals`.
function rowText(item) {
  const r = ((item.readings || [])[0] || {}).recommendation || "";
  return String(r).replace(/\s+/g, " ").trim();
}

function matchToRecords(list) {
  const all = records();
  const fresh = [];
  const carried = [];
  const contestedItems = [];
  for (const item of list) {
    const rec = all[item.id];
    if (!rec) {
      fresh.push(item);
      continue;
    }
    rec.lastSeen = firing;
    if (rec.contested) {
      // Never re-applied and never re-adjudicated. The item's readings from
      // this firing are kept, because they are what a human reading the
      // contest sees alongside the position the review loop took.
      item.disposition = "human";
      item.gate = "contested";
      item.survives = false;
      item.contested = rec.contested;
      item.apply = {
        status: "contested",
        reason:
          "applied at firing " + rec.contested.appliedAtFiring +
          " and reversed since; routed to the human rather than re-applied",
      };
      contestedItems.push(item);
      continue;
    }
    if (rec.gate !== "stands" && rec.gate !== "refuted") {
      // A record with no verdict is unmatched work. The gate never reached the
      // item, most often because its falsifier died, so carrying the non-verdict
      // forward would freeze the item unadjudicated for the rest of the run.
      fresh.push(item);
      continue;
    }
    // Sub-task 4's rows are re-derived at every firing, because what this
    // staging does to another proposal is a function of the staging as it
    // stands now, and both this phase and the review loops change it between
    // firings. A row that came back unchanged is untouched and carries forward
    // like anything else. One that came back different is a new claim about
    // another proposal, so it goes through the gate and the write path afresh
    // rather than leaving the earlier firing's row standing.
    if (item.disposition === "impact-row" && rowText(item) !== (rec.rowText || "")) {
      fresh.push(item);
      continue;
    }
    // Carried forward untouched: the disposition, the gate verdict that stood
    // behind it, and what the earlier Apply did with it. The item is not
    // falsified again and, unless that Apply never landed, not applied again.
    item.disposition = rec.disposition;
    item.gate = rec.gate;
    item.falsification = rec.falsification;
    item.survives = rec.gate === "stands";
    item.carried = { fromFiring: rec.firing, applyStatus: rec.applyStatus };
    item.apply = {
      status: rec.applyStatus,
      reason: "carried forward from firing " + rec.firing,
      carriedFrom: rec.firing,
      claim: { wrote: rec.wrote, where: rec.where },
    };
    carried.push(item);
  }
  if (carried.length > 0 || contestedItems.length > 0) {
    log(
      "Records: " + carried.length + " item(s) carried forward untouched, " + contestedItems.length +
        " contested, " + fresh.length + " adjudicated afresh",
    );
  }
  return { fresh, carried, contestedItems };
}

// This firing's outcome, written back onto the record the next one reads. A
// contested record is left as it stands: it is the one record a later firing
// must not overwrite, since overwriting it is re-adjudication.
function recordItems() {
  const all = records();
  for (const item of items) {
    const prior = all[item.id];
    if (prior && prior.contested) {
      prior.lastSeen = firing;
      continue;
    }
    const a = item.apply || {};
    const claim = a.claim || {};
    all[item.id] = {
      id: item.id,
      subTask: item.subTask,
      question: String(item.question || "").slice(0, 300),
      disposition: item.disposition,
      gate: item.gate || "none",
      // Slim on purpose: the record travels through every later firing, and the
      // only part of the verdict a later firing states is how conclusive it was.
      falsification: item.falsification
        ? {
            falsified: !!item.falsification.falsified,
            howConclusive: item.falsification.howConclusive || "",
          }
        : null,
      firing: item.carried ? item.carried.fromFiring : firing,
      applyStatus: a.status || "not-attempted",
      // For an applied item, what was written and where. It is what the
      // reversal check above greps the tree for, and it is the only reason the
      // record holds text at all.
      wrote: String(claim.wrote || "").slice(0, 1200),
      where: Array.isArray(claim.where) ? claim.where : [],
      // Only an impact row needs its text on the record: it is what tells the
      // next firing whether the sweep re-derived the same row or a new one.
      rowText: item.disposition === "impact-row" ? rowText(item) : "",
      contested: null,
      unmatchedAt: (prior && prior.unmatchedAt) || [],
      lastSeen: firing,
    };
  }
  const unmatched = [];
  for (const rec of Object.values(all)) {
    if (rec.lastSeen === firing) continue;
    // A record restored from a resumed run, or written by an older shape of this
    // file, may carry no `unmatchedAt`. Normalising here keeps one malformed
    // record from throwing and nulling the whole firing.
    if (!Array.isArray(rec.unmatchedAt)) rec.unmatchedAt = [];
    if (!rec.unmatchedAt.includes(firing)) rec.unmatchedAt.push(firing);
    unmatched.push({
      id: rec.id,
      disposition: rec.disposition,
      adjudicatedAtFiring: rec.firing,
      applyStatus: rec.applyStatus,
      contested: !!rec.contested,
    });
  }
  if (unmatched.length > 0) {
    log(
      "Records: " + unmatched.length + " prior record(s) matched no item this firing, kept under their " +
        "own identifiers: " + unmatched.map((u) => u.id).join(", "),
    );
  }
  return unmatched;
}

// Every contested record, with BOTH positions on it: the text this phase wrote
// and where, and what that location carries now. `seenThisFiring` says whether a
// collector saw the item again, which is how an operator tells a reversal that
// rewrote the entry from one that deleted it.
function contestedReport() {
  return Object.values(records())
    .filter((r) => r.contested)
    .map((r) => ({
      id: r.id,
      question: r.question,
      appliedAtFiring: r.contested.appliedAtFiring,
      contestedAtFiring: r.contested.contestedAtFiring,
      wrote: String(r.contested.wrote || "").slice(0, 400),
      where: r.contested.where || [],
      nowCarries: r.contested.nowCarries || "",
      seenThisFiring: r.lastSeen === firing,
    }));
}

// ---- What a collector returns ---------------------------------------------
//
// DECISION_ENTRY and OPEN_DECISIONS_FINDINGS come from change-proposal.js,
// where they were the open-decisions lens's return. They move here rather than
// dying with the lens because of the receipt they carry: a schema field is
// validated at the tool-call layer and the model retries on a mismatch, which
// is the only enforcement in this workflow that actually bites.
//
// It exists because prompt text alone did not hold. On a measured run the
// lens's output was produced from five agents' summaries of the evidence rather
// than the evidence, and one follow-up question from a human -- "elaborate on
// this one" -- reversed the recommendation on the spot, with no decision from
// the human and nothing new beyond one document finally read in full.
//
// A schema field can still be back-filled to justify a conclusion already
// reached, which is why each brief states its procedure as an ORDER OF WORK and
// these fields are its receipts. Neither half is sufficient alone.
//
// IT IS DELIBERATELY SMALL, and the field guidance lives in the briefs instead.
// The first version carried 22 descriptions totalling 2.5k characters over
// nested objects, and the API refused every call it was attached to: "output
// schema too large to classify safely". The lens failed 12 times across 3
// rounds, never ran, never retired, and so blocked the sweep for a whole run. A
// schema that cannot be sent enforces nothing, so structure that earns its size
// stays and prose moves to the prompt. groundQuotes and questionsAsked are
// arrays of strings rather than of objects for the same reason; each brief
// fixes their format, and what the schema still enforces is that they are
// PRESENT and non-empty, which is the forcing function that matters.
//
// Two things change in the move. The lens fed one population and this phase
// feeds four, so both enums are reconciled against what sub-tasks 1 through 4
// actually find, and the identifier the per-item record is keyed by is a field
// rather than a convention. And `findings` does not come across: the phase
// files none, which is the whole reason it is a phase rather than a lens.

// Where an item was found. The lens carried one value for a decisions section
// and it read as the summary's, so it splits: an entry an older proposal left
// in a staged change file is a MIGRATION rather than a live summary entry, and
// only a separate home makes the two distinguishable in the counts this phase
// returns. The last two homes are what sub-tasks 3 and 4 collect from.
// The phase collects the summary's decisions section, the out-of-scope defect
// declarations and the cross-proposal impacts, and nothing else. Four homes were
// dropped after a measured firing: `review-log-open` (six of its seven items
// fell, and it dragged in an UNVERIFIED tag and a duplicate), `implementor-blank`
// (the phase does not audit the escape hatch), `staged-open-decisions` and
// `design-item` (both duplicated what sub-tasks 1 and 2 already held, and one
// marker reached two falsifiers under two homes with opposite verdicts).
const HOMES = ["summary-open-decisions", "out-of-scope-defect", "other-proposal"];

// What the phase decided to do with an item. The lens carried the first three;
// sub-task 3's two outcomes and sub-task 4's impact row are their own values
// rather than mappings onto those three, because mapping "the out-of-scope call
// is right" onto `resolve` would import the defect gate's "default to refuted
// when uncertain" and invert sub-task 3's own default that the call stands.
//
// WHAT EACH VALUE MEANS TO THE GATE AND TO APPLY. The falsification brief, the
// verdict an UNCERTAIN falsifier produces, and whether the value requires an
// Apply are the three things sub-task 5 and sub-task 6 read off this table.
//
//   value               falsification brief                       uncertain  Apply
//   ------------------  ----------------------------------------  ---------  -----
//   resolve             show the answer is not derivable from     refuted    yes
//                       the ground the item cites
//   human               show the five-clause GIVE IT TO THE       stands     see below
//                       HUMAN test does not hold for this item
//   implementor         the DELEGATION judge's: would an          stands     no
//                       `IMPLEMENTOR'S CHOICE` marker actually
//                       BOUND the choice here, or would it
//                       delegate without a constraint?
//   out-of-scope-stands show the proposal cannot ship with this   stands     see below
//                       defect left standing in the tree
//   out-of-scope-wrong  show the call was right after all, so     refuted    yes
//                       this proposal need not solve it
//   impact-row          show the stated effect is not what this   refuted    see below
//                       staging does to that proposal
//
// The asymmetry is the two gates the workflow already runs. `resolve` and
// `out-of-scope-wrong` create text nothing else reviews, so they take the
// defect gate's posture, where an uncertain skeptic refutes. `human`,
// `implementor` and `out-of-scope-stands` take the falsification panel's
// posture and stand unless conclusively refuted, which is also each brief's own
// default. `impact-row` asserts something about another proposal's continued
// validity, and an assertion nobody can support is a false claim about that
// proposal rather than a cautious one, so it needs affirmative support too.
//
// The three "see below" values require an Apply unless the summary already
// carries the item as written: an entry already listed under `## Open decisions
// for human to make`, a defect row already under `## Defects in the shipped
// tree that this proposal does not stage`, or an impact row already under
// `## Impacts on other proposals`, each unchanged by this firing. A `human`
// item migrated out of a staged change file, found in a design item, or found
// as an unclosed `OPEN` in the review log is not yet in the summary, so it
// needs one. `implementor` never does: its default is to leave the blank
// exactly as the proposal wrote it, and specifying one is `resolve`.
const DISPOSITIONS = [
  "resolve",
  "implementor",
  "human",
  "out-of-scope-stands",
  "out-of-scope-wrong",
  "impact-row",
];

const DECISION_ENTRY = {
  type: "object",
  required: [
    "id", "decision", "home", "deliverable", "marker", "groundQuotes", "questionsAsked",
    "caseFor", "caseAgainst", "whatWouldFlipIt", "counterfactual", "cascades", "disposition",
    "recommendation", "whatIsStaged", "confidence", "summaryAction",
  ],
  properties: {
    id: { type: "string" },
    decision: { type: "string" },
    home: { type: "string", enum: HOMES },
    deliverable: { type: "string" },
    marker: { type: "string" },
    groundQuotes: { type: "array", minItems: 1, items: { type: "string" } },
    questionsAsked: { type: "array", minItems: 1, items: { type: "string" } },
    caseFor: { type: "string" },
    caseAgainst: { type: "string" },
    whatWouldFlipIt: { type: "string" },
    counterfactual: { type: "string" },
    cascades: { type: "array", items: { type: "string" } },
    disposition: { type: "string", enum: DISPOSITIONS },
    answer: { type: "string" },
    recommendation: { type: "string" },
    // What the proposal STAGES on this question today, in its own words, or the
    // empty string when it stages nothing either way. A proposal that already
    // stages an answer has half-decided the question, and a recommendation
    // pointing the other way means the staged text and the reviewer's brief
    // disagree about what is being built.
    whatIsStaged: { type: "string" },
    // How sure the reading is of its recommendation. It gates how opinionated
    // the phase is allowed to be: see CONFIDENCE_RULE.
    confidence: { type: "string", enum: ["high", "moderate", "low"] },
    summaryAction: { type: "string", enum: ["added", "updated", "withdrawn", "unchanged", "not-applicable"] },
    affectsProposals: { type: "array", items: { type: "string" } },
    changesWithChoice: { type: "boolean" },
  },
};

const OPEN_DECISIONS_FINDINGS = {
  type: "object",
  required: ["coverage", "decisions"],
  properties: {
    // The lens's coverage self-report, kept for the same reason it was written:
    // a collector that must name what it swept walks the sections, and one that
    // must name what it could not verify surfaces a blind spot instead of
    // returning a quiet empty list. Here it also carries the incomplete answer,
    // which is what tells an unadjudicated population from an empty one.
    coverage: { type: "string" },
    decisions: { type: "array", items: DECISION_ENTRY },
  },
};

// ---- The four collectors --------------------------------------------------
//
// Sub-tasks 1 through 4. Each collects a different population and asks a
// different question, and each carries its own default where it states one.
// They are four briefs rather than one merged inventory: one brief over four
// populations asks its question of all of them, and the question that fits the
// human's decisions is the wrong one to ask of a bounded implementor blank.

const COLLECTOR_READ_ONLY =
  "You are a read-only investigator. Do not create, edit, or delete any file, including the review log. " +
  "This phase gates every disposition before anything is written and then writes it in a later stage, so " +
  "an agent that edits before the Apply stage has written past the gate. Cite evidence as file:line.";

const EVIDENCE =
  "Verify every claim directly against spec/, schemas/, pkg/, cmd/, charts/, and git history in " + repo +
  ". Spec files are large; use Grep and targeted Read offsets, never read a whole spec file. Treat the " +
  "problem statement itself and any progress-tracking or audit prose elsewhere in the repository as leads " +
  "to verify rather than as evidence.";

// Read from the review log rather than passed in, following the block every
// agent in the parent gets. The write half of that block does not come with it:
// this phase writes the log directly from its own stages, because a shard is
// merged at a round boundary and a firing runs after its loop's last one.
const STANDING_CONTEXT =
  "THE REVIEW LOG carries what earlier agents on this proposal learned. Read the `## Standing context` " +
  "section of " + P.log + " BEFORE you start: it is curated, it is short, and it " +
  "is where a trap someone already fell into is recorded. Write nothing to it.";

// THE PHASE WRITES THE REVIEW LOG DIRECTLY, which is why the block above is
// only the read half of the parent's. The merge that turns a shard into log
// text runs inside `closeRound`, a round-boundary operation, so a shard a
// post-loop firing wrote would be merged by the NEXT loop's first boundary, or
// never when no loop follows it, and a periodic firing's shard would wait a
// full round. A phase whose whole output is text in the proposal cannot have
// its record of that text arrive a round late, so every stage of it that writes
// edits the file itself and splices its block where the log's structure
// requires. This is the rule those stages are given.
const LOG_WRITE_RULE =
  "WHERE IN THE LOG YOUR BLOCK GOES. " + P.log + " carries `## Standing context`, `## Ledger`, and " +
  "`## Retired`, in that order, and this phase writes it DIRECTLY rather than through a log shard. " +
  "Splice your block at the END of `## Ledger`, immediately before `## Retired`: appending to the end of " +
  "the file buries it inside `## Retired`, which is where entries go once they are done being read. Head " +
  "it `### [f" + firing + ".<stage>]` and use the log's own tags, one per line, so the compaction pass " +
  "can act on it rather than paraphrase it: `DECISION:`, `WATCHOUT:`, `FACT:`, `OPEN:`, and " +
  "`DEFERRED [file]:` for a correction you derived and may not land. Leave `## Standing context` alone, " +
  "because the round-boundary pass curates it, and preserve every other line of the file as it stands.";

const FILE_MAP =
  "THE PROPOSAL. Summary: " + roleRef(P, "summary", "## Summary") + ". Staged spec changes: " +
  roleRef(P, "spec", "Proposed spec changes") + ". Staged non-spec changes: " +
  roleRef(P, "nonSpec", "Non-spec changes") + ". Problem statement: " +
  roleRef(P, "problem", "Problem statement") + ". Implementation checklist: " +
  roleRef(P, "checklist", "Implementation checklist") + ". Review log: " +
  roleRef(P, "log", "Review log") + ".";

// The operator's control over the run, honoured rather than reasoned around. A
// collector that does not know the staged spec edits are closed will recommend
// resolutions the Apply stage cannot land.
const LOCK_NOTE = lockSpecChanges
  ? "THE STAGED SPEC CHANGES ARE LOCKED for this run by the operator. A resolution that would require " +
    "editing them cannot be applied: say so in `recommendation`, and it is recorded for the operator " +
    "instead of staged."
  : "";

// Stated to every collector, because a section's preamble is inherited by
// entries written long after it and no single sub-task owns the section it sits
// on. Correcting it is this phase's own work: the Apply and cleanup stages hold
// the write path, so a collector reports the discrepancy rather than fixing it.
const PREAMBLE_RULE =
  "A PREAMBLE THAT ASSERTS A PROVENANCE ITS LATER ENTRIES DO NOT HAVE IS A FALSE CLAIM ABOUT THE SECTION. " +
  "A sentence saying every entry below it was derived and validated some number of times is written once, " +
  "for the entries that existed then, and inherited by every entry a fixer or a lens added afterwards with " +
  "nothing adversarial applied to it. Check any preamble on the section you are reading against the entries " +
  "actually under it, and state in `coverage` what it claims, what the entries support, and the correction " +
  "it needs.";

const INCOMPLETE_ANSWER =
  "AN INCOMPLETE SWEEP IS AN ANSWER. If you could not reach part of your population, say so in `coverage` " +
  "and name what you did not reach. A population you did not adjudicate is a different answer from a " +
  "population with nothing in it, and this phase records the two differently. An empty `decisions` array is " +
  "correct and expected when there is genuinely nothing of your kind in the proposal; it is a defect only " +
  "when it means you stopped early.";

// The identifier rule. It is the same rule for every collector, because the
// per-item record that carries a disposition across firings is keyed by it, and
// sub-task 1's three readings join on it.
const IDENTIFIER_RULE =
  "THE IDENTIFIER IS THE ENTRY'S, NEVER YOURS. An entry under `## Open decisions for human to make` carries " +
  "a stable identifier stamped when it was first written. Where your item is one of those, return that " +
  "identifier verbatim in `id`, with `deliverable` and `marker` empty. Do not invent, normalise, renumber, or improve one: this phase joins independent " +
  "readings and successive firings on that string, and an identifier you chose joins to nothing. Stamping " +
  "an entry that carries none is a later stage's work.\n" +
  "  An item with no stamp -- a design item, an unclosed `OPEN` in the review log, an entry in a staged " +
  "change file, an `IMPLEMENTOR'S CHOICE:` marker, an out-of-scope declaration, another proposal -- returns " +
  "`id` empty, the deliverable id it belongs to in `deliverable` (or the other proposal's id for an impact " +
  "row), and in `marker` the verbatim line you found it at. Those two are its key, and the marker text as " +
  "first recorded is held across firings, so return the line as it stands rather than a paraphrase of it.";

const ENTRY_FIELDS =
  "WHAT EACH FIELD OF `decisions` HOLDS, since the schema states only the shape. `decision`: the question, " +
  "stated so a person could answer it in one sitting without reading the proposal. `home`: where you found " +
  "it, from the enum. `groundQuotes`: one entry per load-bearing sentence, written as `file:line — \"the " +
  "sentence, verbatim\"`, from sources you opened in this pass. A summary of a source is not the source, " +
  "and a citation travels between agents without its context. `questionsAsked`: one entry per question, " +
  "written as `Q: ... / A: ... (file you opened)`, and an answer you could not find says so plainly. " +
  "`caseFor` and `caseAgainst`: the case for and against your disposition, each at its best, written " +
  "before you chose. `whatWouldFlipIt`: the one fact whose discovery reverses you. `counterfactual`: " +
  "whether disposing of it differently changes anything downstream. `cascades`: one entry per SETTLED " +
  "decision an answer here would falsify, as `the settled decision (where recorded) — falsified by which " +
  "answer`, empty when none. `recommendation`: what you recommend and why, in prose. `answer`: for a " +
  "`resolve` only, the answer itself in one short line, stated as the ground states it rather than in " +
  "your own framing, because independent readings of one item are compared on this line. `summaryAction`: " +
  "what the summary's own entry for this item needs, from the enum. `affectsProposals` and " +
  "`changesWithChoice`: sub-task 4's fields, described in its brief.";

// The frame every collector's prompt is built on, so the four differ in their
// population and their question and in nothing else.
// What the proposal already stages is evidence about the answer, and the two must
// not disagree. A proposal that stages an answer has half-decided the question:
// either that is the right answer, in which case it is the recommendation, or it
// is wrong, in which case the staged text is a defect this phase names rather
// than a recommendation it contradicts in silence.
// The staged change files say what an implementor builds. A decision the human
// has not answered is not something to build, so it has no business being cited
// there: a reader of the staged text should never have to go and find out how a
// question was settled before they can read what is being asked of them.
const NO_DECISION_REFS_RULE =
  "NEVER REFERENCE AN OPEN DECISION IN A STAGED CHANGE FILE. The staged spec changes and the staged " +
  "non-spec changes state what an implementor builds, and nothing else. Do not cite an open decision by " +
  "its identifier there, do not write that a deliverable is conditional on one, and do not carry a " +
  "pointer to the summary's decisions section. That holds for a decision the phase resolves as much as " +
  "for one left to the human: when a decision is answered, the staged text states the ANSWER as a " +
  "requirement, in its own terms, with no trace of the question. When a decision is NOT answered, the " +
  "staged files say nothing about it at all and the question lives in the summary alone.";

const STAGED_ALIGNMENT_RULE =
  "WHAT THE PROPOSAL ALREADY STAGES IS PART OF THE ANSWER. For every decision, read what the staged " +
  "spec and non-spec changes build TODAY on that question and put it in `whatIsStaged`, quoting the " +
  "staged sentence, or the empty string when they stage nothing either way.\n" +
  "IF THE PROPOSAL STAGES AN ANSWER, THAT IS YOUR RECOMMENDATION unless you can say why the staged text " +
  "is wrong. A recommendation pointing away from what the proposal builds leaves the reviewer holding a " +
  "brief that contradicts the deliverable, and whichever they pick something has to be rewritten that " +
  "nobody has reviewed. When the staged answer IS wrong, say so in `recommendation` in those words, name " +
  "the staged sentence, and treat the mismatch as the finding rather than the preference.";

// How opinionated the phase is allowed to be. The operator's instruction: a
// recommendation the reading is sure of is a decision the workflow can take.
const CONFIDENCE_RULE =
  "HOW SURE YOU ARE DECIDES WHO ANSWERS. Set `confidence` for every item.\n" +
  "  - `high`: the ground settles it and a reviewer reading the same files would reach the same answer. " +
  "RESOLVE IT. Do not route a decision to the human that you are sure of; a question whose answer you " +
  "would defend costs a human's attention and buys nothing.\n" +
  "  - `moderate`: the ground points one way and something is left open. RESOLVE IT ONLY IF the proposal " +
  "already stages that same answer, because the staging is then the second reading that makes it sure. " +
  "Otherwise it is the human's.\n" +
  "  - `low`: the ground does not settle it. The human's.\n" +
  "SCOPE, SPECIFICALLY. A decision about whether something belongs in this proposal, where the proposal " +
  "ALREADY STAGES the scope in question, is closed: the answer is that it stays in. Resolve it and say " +
  "that the staged deliverable is the answer. Scope is only the human's where the proposal does not yet " +
  "build the thing being questioned.";

function briefFrame(head, body) {
  return (
    "You are one collector inside the open-decisions-and-impact-review phase of a change proposal's " +
    "review. This is firing " + firing + " (" + trigger + ") on " + P.stem + ". " + head + "\n\n" +
    COLLECTOR_READ_ONLY + "\n\n" +
    FILE_MAP + "\n\n" +
    EVIDENCE + "\n\n" +
    STANDING_CONTEXT + "\n\n" +
    (LOCK_NOTE ? LOCK_NOTE + "\n\n" : "") +
    body + "\n\n" +
    IDENTIFIER_RULE + "\n\n" +
    PREAMBLE_RULE + "\n\n" +
    ENTRY_FIELDS + "\n\n" +
    INCOMPLETE_ANSWER
  );
}

// The run's refuted findings, rendered the way the review loop renders them for
// a lens. A decision an earlier firing left with the human may be resolvable
// once the skeptics have refuted the ground it rested on, which is why the list
// reaches the collector that adjudicates the human's decisions.
function refutedBlock() {
  if (rejected.length === 0) return "";
  return (
    "ALREADY EXAMINED AND REFUTED IN THIS RUN. Do not rest a resolution on any of these, and do not " +
    "re-argue one:\n" +
    rejected
      .map(
        (r) =>
          "- " + r.title + ": refuted by the " + (r.refutedBy || "unknown") + " skeptic because " +
          r.reason,
      )
      .join("\n")
  );
}

// Sub-task 1. Three independent agents over this one brief, because it is the
// widest judgment in the phase and the one where a single reading is least
// trustworthy. The five-clause test and the negative test below are the tree's
// only statement of when a decision is genuinely the human's, and they catch the
// failure mode unique to authoring: a resolution that should have gone to the
// human produces text that is consistent, correctly cited, implementable, and
// passes every reviewer by construction.
function humanDecisionsBrief() {
  return briefFrame(
    "Your population is EVERY DECISION THIS PROPOSAL LEAVES TO A HUMAN. For each one, decide whether it is " +
      "really the human's or whether this workflow can answer it satisfactorily.",
    "WHERE THEY LIVE. ONE HOME: the `## Open decisions for human to make` section of the summary. " +
      "`home`: summary-open-decisions. That section is the proposal's statement of what it is asking a " +
      "human for, and it is the only place this sub-task reads decisions from.\n" +
      "DO NOT SWEEP ANYWHERE ELSE FOR THEM. Not the review log's unclosed `OPEN` entries, not a " +
      "`## Open decisions for review` section in a staged change file, not detailed-design prose still " +
      "phrased as a choice, and not an `IMPLEMENTOR'S CHOICE:` marker. Each of those was collected once " +
      "and each cost more than it returned: the review log yielded one live question against six that " +
      "fell, and the other three duplicated items another sub-task already held, so one marker reached " +
      "two falsifiers under two homes and came back with opposite verdicts.\n" +
      "A decision in the section that IS settled elsewhere is open in the sense that matters, " +
      "because the proposal disagrees with itself about whether it is decided; resolving it means deleting " +
      "the open statement and citing the settled one.\n\n" +
      "A SETTLED DECISION IS NOT YOURS. Do not re-open one, do not re-argue it, and do not report that it " +
      "was decided wrongly, however tempting the argument. That covers the proposal's settled-decisions " +
      "section, a non-goal recorded with its reason, and a historical pass record of what an earlier round " +
      "resolved. A settled decision reopened costs a round, and the argument for reopening it always reads " +
      "well because nobody wrote down the counter-argument once it was closed. THE ONE EXCEPTION IS " +
      "CASCADE: when an answer available to an OPEN decision would FALSIFY a settled one, say so in that " +
      "open decision's `cascades` field, naming the settled decision and which answer falsifies it. It " +
      "never becomes a decision of its own, because nobody is asking whether the settled decision was " +
      "right; what is in question is the open one, and the cascade is part of what the answer costs.\n\n" +
      "PROCEDURE. Work in this order, and do not let a determination form before step 4.\n" +
      "  1. INVENTORY. List every decision the summary's section leaves open. Judge none of them yet.\n" +
      "  2. ELABORATE, one decision at a time. State what is actually being decided, what the proposal " +
      "says about it, and what the primary sources say. Open them and quote the load-bearing sentence.\n" +
      "  3. INTERROGATE. Write the questions a skeptical reviewer would ask about the GROUND rather than " +
      "about the choice: 'what does that document actually argue', never 'which option is better'. At " +
      "least one must be a question whose answer would KILL the answer you are drifting toward. Then " +
      "answer each from a file you open in this pass, quoting what you find. A question you cannot answer " +
      "is a result rather than a gap: it is either the fact that would reverse the decision, or the reason " +
      "the decision is genuinely the human's.\n" +
      "  4. DETERMINE. Only now apply the test below. Your `decisions` array is the receipt for each step, " +
      "and a receipt written after the fact is the failure this procedure exists to prevent.\n\n" +
      "THE TEST, applied to each decision in this order.\n" +
      "  RESOLVE IT when the answer is derivable: the tree, the spec, a landed proposal, or the proposal's " +
      "own staged text fixes it and you can cite where. This is the outcome to reach for. A decision parked " +
      "for a human that the repository already answers costs a review cycle and teaches the reviewer that " +
      "the list is noise. Set `disposition` to `resolve`, put the answer in `answer` with its citation in " +
      "`groundQuotes`, and state in `recommendation` what the proposal must say and where it goes: this " +
      "phase stages the answer into the proposal, and the entry then leaves the summary.\n" +
      "  LEAVE IT TO THE IMPLEMENTOR when the choice is local, reversible, and has no consequence in " +
      "another section. `disposition`: `implementor`.\n" +
      "  GIVE IT TO THE HUMAN only when one of these holds: it trades two goods the proposal cannot rank, " +
      "such as scope, review burden, or release timing; it changes what the proposal IS, by widening or " +
      "narrowing the deliverable; it commits the platform to a contract no evidence in the tree settles; " +
      "it decides another proposal's fate or is decided by one; or it accepts a named residual cost. " +
      "`disposition`: `human`.\n\n" +
      "THE NEGATIVE TEST. A decision belongs to the human only if a person could answer it in one sitting " +
      "without reading the whole proposal. One that fails this is not ready to delegate: restate it in " +
      "`recommendation` until it passes, or resolve it outright. A question that comes back unanswered has " +
      "cost a human's attention and bought nothing.\n\n" +
      "WHERE THE CHOICE IS GENUINELY A DESIGN CHOICE rather than a factual disagreement, do not pick " +
      "silently: record it as an open decision with both options, their consequences, and your default.\n" +
      "DO NOT INVENT A RECOMMENDATION THE LOOP DID NOT DERIVE. An entry the review loop left without one " +
      "says so, and a recommendation you supply here is one you ground in this pass, quoting what you " +
      "opened for it.\n\n" +
      "THE SUMMARY ENDS UP CARRYING THE SURVIVORS AND ONLY THE SURVIVORS. An answerable decision is " +
      "answered and its answer staged into the proposal; a resolved or withdrawn decision leaves that " +
      "file rather than being kept in place. Set `summaryAction` to what this item's summary entry needs.\n" +
      "AN ENTRY THE SUMMARY ALREADY CARRIES IS READ AGAINST THE STAGING AS IT NOW STANDS, because both " +
      "loops have edited the proposal since it was written, and an entry that has fallen out of agreement " +
      "with the proposal is what this sweep exists to catch.\n" +
      "  RESOLVED SINCE. A later round answered it, here or anywhere else in the proposal. Name where it " +
      "was answered in `recommendation` and set `summaryAction` to `withdrawn`: it leaves the section " +
      "rather than being kept in place, because the section carries the live decisions and only those.\n" +
      "  STALE GROUND. Its citation, the text it quotes, or the ground its recommendation rests on has " +
      "changed under it. Rewrite it against the current text and set `summaryAction` to `updated`.\n" +
      "  MIS-STATED. It asks a question this proposal does not actually face, because the staging never " +
      "posed it or a later round posed a different one. Restate it against what the proposal now decides " +
      "and set `summaryAction` to `updated`, or resolve it and set `summaryAction` to `withdrawn`.\n" +
      "  ORPHANED. Its subject is a deliverable this proposal no longer stages. Nobody is being asked " +
      "anything, so it leaves the section: set `summaryAction` to `withdrawn` and name in " +
      "`recommendation` the deliverable that went away and where it went.\n\n" +
      "THREE AGENTS READ THIS SAME POPULATION INDEPENDENTLY and the script joins your answers. You do not " +
      "see the other two and you are not deciding the outcome: an item reaches `resolve` only when all " +
      "three of you resolve it to the same answer, and it is the human's otherwise. So return YOUR reading, " +
      "with the ground you actually opened, rather than the reading you expect to be agreed with." +
      (refutedBlock() ? "\n\n" +
      CONFIDENCE_RULE + "\n\n" +
      STAGED_ALIGNMENT_RULE +  refutedBlock() : ""),
  );
}

// Sub-task 2. One agent. The default is to leave a blank alone, and the two
// protections below are the tree's only statement of what a blank is for.

// Sub-task 3. One agent. Its default is the opposite of the defect gate's, and
// that is why its outcomes are their own dispositions rather than `resolve`:
// the call standing is the answer this brief starts from.
function outOfScopeDefectsBrief() {
  return briefFrame(
    "Your population is EVERY DEFECT THIS PROPOSAL EXPLICITLY CALLS OUT AS OUT OF SCOPE. For each one, " +
      "decide whether that is the right call.",
    "WHERE THEY LIVE. Anywhere the proposal names a defect in the shipped tree and says it is not staging " +
      "a fix: the problem statement, a scope or non-goals section, a staged change file's prose, or an " +
      "aside inside a deliverable. `home`: out-of-scope-defect.\n\n" +
      "THE DEFAULT IS THAT THE CALL IS RIGHT. A declaration is a scope decision the proposal already took, " +
      "with its reason, and disagreeing with the scope is not the same as showing the call wrong. Set " +
      "`disposition` to `out-of-scope-stands` unless you can show otherwise, and put in `recommendation` " +
      "the row the defect needs under `## Defects in the shipped tree that this proposal does not stage`: " +
      "what is wrong, where it is, and what the proposal does about it, which is nothing.\n\n" +
      "THE CALL IS WRONG only where leaving the defect standing makes THIS proposal's own staged changes " +
      "wrong, unimplementable, or false about the tree they land in. Then set `disposition` to " +
      "`out-of-scope-wrong` and state in `recommendation` the solution this proposal must specify, in " +
      "enough detail that the stage that edits the proposal can write it: the change, the file it lands " +
      "in, and the deliverable it joins. A wrong call is an authoring act, so it needs affirmative " +
      "support: an uncertain case leaves the call standing.\n\n" +
      "A defect the proposal does not mention at all is not your population. You are reading the " +
      "declarations the proposal made, rather than sweeping the tree for defects it missed.",
  );
}

// Sub-task 4. One agent, holding the run-scoped corpus inventory rather than
// gathering one. The assessment re-runs at every firing, because what this
// proposal does to each other proposal is a function of the staging as it
// stands now, and this phase and the review loops both change that between
// firings.
function otherProposalsBrief(corpusRows) {
  const inventory =
    corpusRows.length > 0
      ? corpusRows
          .map(
            (r) =>
              "- " + r.proposal + " — status " + (r.status || "unknown") + " — " +
              (r.date ? r.date : "no date") + " (" + (r.dateSource || "none") + ")",
          )
          .join("\n")
      : "(the inventory could not be gathered on this run; read the statuses yourself with " +
        "`.claude/tools/proposal-status.mjs <proposal> --json` and say in `coverage` that you did)";
  return briefFrame(
    "Your population is EVERY OTHER PROPOSAL ON DISK THIS ONE BEARS ON. No reviewer in this run reads " +
      "`proposals/`, so this sweep is the only place the effect is established.",
    "THE CORPUS, with each proposal's status and the date that status was reached. A row marked `commit` " +
      "carries a LAST COMMIT date, which is when someone last touched the file rather than when it was " +
      "reviewed; say so wherever you rest on one, because nearly every proposal on disk is a legacy " +
      "single-file one and the fallback is the ordinary path here.\n" +
      inventory + "\n\n" +
      "THIS FIRING RE-DERIVES THE WHOLE SECTION. What this proposal does to another is a function of what " +
      "is staged in the two change files right now, and both this phase and the review loops change that " +
      "between firings. Read the current staging and assess against it; an earlier firing's rows are not " +
      "evidence.\n\n" +
      "FIRST ASK WHETHER IT IS EVEN A DECISION. When a staged change bears on another proposal, ask " +
      "whether choosing differently would change that effect. If every available answer affects it " +
      "identically, the effect is already settled by a deliverable nobody is questioning, and your output " +
      "is a row rather than a question for a human. Set `changesWithChoice` to record which case you are " +
      "in.\n\n" +
      "THEN THE STATUS AND RECENCY DECIDE HOW THE ROW READS. An `Implemented` proposal is already in the " +
      "tree and is not affected. A `Draft` may be invalidated freely: record it and move on. A `Reviewed` " +
      "or `Approved` proposal last reviewed within fourteen days warrants care, because convergence and " +
      "human attention were recently spent on it and this change spends them again: say in the row that " +
      "proceeding is the human's call and state the question. An older one is recorded with the note that " +
      "it may have drifted regardless.\n\n" +
      "NAMING THE EFFECT IS NOT OPTIONAL. Recording it as a rebase for whichever lands second is not " +
      "enough: say which of the other proposal's deliverables lose their subject and which survive.\n\n" +
      "YOUR WRITE PATH. One entry per affected proposal, `home`: other-proposal, `disposition`: " +
      "`impact-row`, `deliverable`: the other proposal's id, and `marker`: the line in it you are reading " +
      "the effect against. `recommendation` is the row itself, as it should stand under " +
      "`## Impacts on other proposals` in the summary, which is the ONLY place this proposal asserts " +
      "anything about another proposal's continued validity. `affectsProposals` carries one entry per " +
      "proposal the row touches, as `id (status, date, where the date came from) — effect — changes with " +
      "the choice: yes|no`. Skip this proposal's own entry in the corpus, and skip a proposal nothing in " +
      "the staging touches: a row asserting no effect is noise the human pays for.",
  );
}

// ---- Collecting -----------------------------------------------------------

// Populations no agent adjudicated, because its collector returned nothing after
// every retry. Reported, because a firing that lost a collector is silent about
// that population rather than clear of it.
const unadjudicated = [];

function normalizeKeyPart(t) {
  return String(t || "").toLowerCase().replace(/\s+/g, " ").trim().slice(0, 160);
}

// The comparison an answer joins on. Case and whitespace fold, because three
// agents quoting one ground sentence space it differently. Nothing else does,
// and the string is not truncated: the key normalizer's 160-character bound is
// right for a key and wrong here, where two answers agreeing for a paragraph
// and diverging at its end differ substantively and must reach the human. The
// test stays exact equality. A token-overlap threshold reads an answer and its
// negation as one, which is the silent pick this join exists to prevent.
function normalizeAnswer(t) {
  return String(t || "").toLowerCase().replace(/\s+/g, " ").trim();
}

// The tokens of a marker line, used only to recognise a line the review loop
// reworded as the line an earlier firing already keyed. Punctuation is dropped
// so a repunctuated line carries the same tokens.
function markerTokens(t) {
  return normalizeKeyPart(t).replace(/[^a-z0-9]+/g, " ").split(" ").filter(Boolean);
}

// The key an earlier firing gave this marker, when this line is that line
// reworded. The key is pinned at first sight, so an unstamped item the loop
// rephrased still matches its record: it carries its disposition forward, and
// a CONTESTED record still suppresses it. Without this the phase re-adjudicates
// and re-applies an edit the loop reversed, which is the loop the phase state
// exists to prevent. Only a marker first seen at an EARLIER firing is a
// candidate, so two distinct items collected in one firing never merge.
function pinnedKeyFor(deliverable, marker) {
  const want = new Set(markerTokens(marker));
  if (want.size < 3) return "";
  let best = "";
  let bestScore = 0;
  for (const [key, rec] of Object.entries(phaseState.markers || {})) {
    if (!rec || !(rec.firstSeen < firing)) continue;
    if ((normalizeKeyPart(rec.deliverable) || "unscoped") !== deliverable) continue;
    const have = new Set(markerTokens(rec.marker));
    if (have.size < 3) continue;
    let shared = 0;
    for (const w of want) if (have.has(w)) shared++;
    const score = shared / Math.min(want.size, have.size);
    if (score > bestScore) {
      bestScore = score;
      best = key;
    }
  }
  // Deliberately tight. A reword adds or drops a few words of the same line,
  // so it scores at or near 1; two different markers under one deliverable
  // ("the retry jitter" against "the retry budget") score 0.83 and stay apart.
  return bestScore >= 0.9 ? best : "";
}

// The key an item joins on, across the three readings of sub-task 1 and across
// firings. A stamped entry keys on its stamp, which is the point of stamping:
// three agents join on a string none of them chose. An unstamped item keys on
// its deliverable plus the marker text recorded at first sight, held here on the
// phase state, because `lockSpecChanges` can forbid writing a stamp into the
// staged spec changes at all and an implementor blank or an out-of-scope
// declaration therefore never gets one.
function keyFor(entry) {
  const stamped = String(entry.id || "").trim();
  if (stamped) return "id:" + stamped;
  const deliverable = normalizeKeyPart(entry.deliverable) || "unscoped";
  const marker = normalizeKeyPart(entry.marker || entry.decision);
  const pinned = pinnedKeyFor(deliverable, marker);
  if (pinned) {
    const rec = phaseState.markers[pinned];
    if (!rec.reworded) rec.reworded = [];
    if (marker && marker !== normalizeKeyPart(rec.marker) && !rec.reworded.includes(marker)) {
      rec.reworded.push(marker);
      log('  a reworded marker line under ' + (rec.deliverable || "unscoped") + ' matched the key it was first given: "' + rec.marker + '"');
    }
    return pinned;
  }
  const key = "marker:" + deliverable + ":" + marker;
  if (!phaseState.markers) phaseState.markers = {};
  if (!phaseState.markers[key]) {
    phaseState.markers[key] = {
      deliverable: entry.deliverable || "",
      marker: entry.marker || entry.decision || "",
      firstSeen: firing,
    };
  }
  return key;
}

// One reading of one item, kept whole. The gate reads the disposition, and an
// operator reading the result needs the ground the reading rested on.
function readingOf(agentIndex, entry) {
  return {
    agent: agentIndex,
    disposition: entry.disposition,
    recommendation: entry.recommendation || "",
    answer: entry.answer || "",
    entry,
  };
}

// Sub-task 1's join, which is SCRIPT-SIDE on purpose: three agents returning
// dispositions is redundancy only if nothing lets one of them decide the
// outcome. The inventory is the union of the three collections, and the
// disposition is `human` unless all three returned `resolve`, so a three-way
// split is `human` by construction and needs no clause of its own. A dead
// adjudicator leaves fewer than three readings, which cannot be unanimous, so
// the item stands as the human's for the same reason.
async function collectHumanDecisions() {
  const label = (n) => "f" + firing + ":human-decisions:" + n;
  const prompt = humanDecisionsBrief();
  const returns = await parallel(
    [1, 2, 3].map(
      (n) => () =>
        robustAgent(prompt, { label: label(n), schema: OPEN_DECISIONS_FINDINGS, phase: "Collect" }),
    ),
  );
  const byKey = new Map();
  let liveAgents = 0;
  returns.forEach((r, i) => {
    if (!r) {
      recordDead(label(i + 1));
      return;
    }
    liveAgents++;
    for (const entry of Array.isArray(r.decisions) ? r.decisions : []) {
      if (!entry || typeof entry !== "object") continue;
      const key = keyFor(entry);
      if (!byKey.has(key)) byKey.set(key, []);
      const readings = byKey.get(key);
      // One reading per agent per item. An agent that listed the same item
      // twice must not supply two thirds of a unanimity.
      if (readings.some((x) => x.agent === i + 1)) continue;
      readings.push(readingOf(i + 1, entry));
    }
  });
  if (liveAgents === 0) {
    unadjudicated.push("human-decisions");
    log("Sub-task 1 (decisions left to the human): all three adjudicators returned nothing; the population is UNADJUDICATED");
    return [];
  }
  if (liveAgents < 3) {
    log(
      "Sub-task 1: " + liveAgents + "/3 adjudicators returned; no item can be unanimous on fewer than " +
        "three readings, so each stands as the human's",
    );
  }
  const out = [];
  for (const [key, readings] of byKey) {
    const first = readings[0].entry;
    const unanimousResolve = readings.length === 3 && readings.every((x) => x.disposition === "resolve");
    // The answers are compared rather than the recommendations, because a
    // recommendation is prose three independent writers never phrase alike and
    // the answer is the one line each was told to state as the ground states
    // it. Three agents that resolved an item to different answers have not
    // agreed on anything, so the item is the human's, with all three recorded
    // as alternatives rather than one of them picked silently.
    const answers = readings.map((x) => normalizeAnswer(x.answer));
    const agreed = unanimousResolve && answers.every((a) => a && a === answers[0]);
    // Three readings that agree the decision is the IMPLEMENTOR'S take it off
    // the human's list without this phase answering it. Sub-task 2 used to be
    // the only route to that disposition, and deleting it left the DELEGATION
    // falsifier brief unreachable; a decision the summary carries that is really
    // a bounded build choice is the case it exists for.
    const unanimousImplementor =
      readings.length === 3 && readings.every((x) => x.disposition === "implementor");
    const disposition = agreed ? "resolve" : unanimousImplementor ? "implementor" : "human";
    let agreement = "split";
    if (agreed) agreement = "unanimous-resolve";
    else if (unanimousImplementor) agreement = "unanimous-implementor";
    else if (unanimousResolve) agreement = "divergent-resolve";
    else if (readings.length < 3) agreement = "incomplete-readings";
    out.push({
      id: key,
      subTask: "human-decisions",
      home: first.home,
      question: first.decision,
      disposition,
      agreement,
      readings,
      // Recorded only where three agents agreed to resolve and disagreed about
      // the answer: that is the case where something was on the table to pick
      // between, and picking is what this phase does not do.
      alternatives:
        agreement === "divergent-resolve" ? readings.map((x) => x.answer || x.recommendation) : [],
    });
  }
  log(
    "Sub-task 1 (decisions left to the human): " + out.length + " item(s) from " + liveAgents +
      " reading(s), " + out.filter((i) => i.disposition === "resolve").length + " unanimously resolvable",
  );
  return out;
}

// Sub-tasks 2, 3 and 4: one agent over one population, each with its own default.
// `allowed` is the set of dispositions the brief may return and `fallback` is
// that brief's own default, so a disposition from another sub-task's vocabulary
// is read as a brief the agent stepped outside rather than as a new outcome.
async function collectSingle(cfg) {
  const label = "f" + firing + ":" + cfg.key;
  const r = await robustAgent(cfg.prompt, {
    label,
    schema: OPEN_DECISIONS_FINDINGS,
    phase: "Collect",
  });
  if (!r) {
    recordDead(label);
    unadjudicated.push(cfg.key);
    log(cfg.title + ": the collector returned nothing after retries; the population is UNADJUDICATED rather than empty");
    return [];
  }
  const out = [];
  for (const entry of Array.isArray(r.decisions) ? r.decisions : []) {
    if (!entry || typeof entry !== "object") continue;
    let disposition = entry.disposition;
    if (!cfg.allowed.includes(disposition)) {
      log(
        "  " + label + ': disposition "' + disposition + '" is not one this brief may return; ' +
          "recorded as " + cfg.fallback + ", which is its default",
      );
      disposition = cfg.fallback;
    }
    out.push({
      id: keyFor(entry),
      subTask: cfg.key,
      home: entry.home,
      question: entry.decision,
      disposition,
      agreement: "single-reading",
      readings: [readingOf(1, entry)],
      alternatives: [],
    });
  }
  log(cfg.title + ": " + out.length + " item(s)");
  return out;
}

// ---- The gate: one falsifier per item -------------------------------------
//
// Sub-task 5. One falsifier per item, in parallel, each holding ONLY its own
// item and briefed to REFUTE that item's disposition rather than confirm it. A
// single agent reading a dozen dispositions gives each one a fraction of its
// attention and returns a verdict shaped by the batch; a falsifier holding one
// item can open every file that item touches.
//
// The redundancy that a majority-of-three over the batch used to supply moved
// upstream instead: sub-task 1 runs three independent adjudicators over the
// human decisions, which is the widest judgment in the phase, so a disposition
// reaches its falsifier having already been derived more than once.

// Mirrors FALSIFICATION in change-proposal.js, which is what the review loop's
// own falsification panel returns, so a reader of one recognises the other.
const ITEM_FALSIFICATION = {
  type: "object",
  required: ["falsified", "howConclusive", "theDispositionIAttacked", "reasoning"],
  properties: {
    falsified: { type: "boolean", description: "true when you showed this disposition does not hold" },
    howConclusive: {
      type: "string",
      enum: ["conclusive", "partial", "none"],
      description:
        "conclusive: you have evidence the disposition is wrong, not merely unproven. partial: you have doubt you can articulate but cannot settle. none: you could not falsify it.",
    },
    theDispositionIAttacked: {
      type: "string",
      description: "the disposition and the case behind it, in your words, so a reader can see you attacked the real one",
    },
    reasoning: { type: "string" },
    evidence: { type: "array", items: { type: "string" }, description: "file:line you personally opened" },
    fallbackDisposition: {
      type: "string",
      enum: DISPOSITIONS,
      description:
        "the disposition the evidence actually supports, when you falsified this one. It may not be the one you attacked. A falsification naming nothing is set aside, and so is one naming something: this phase invents no substitute disposition. Leave it empty when you did not falsify.",
    },
  },
};

// One brief per disposition, keyed by disposition the way PANELS in
// change-proposal.js keys its panels by verdict rather than running one brief
// everywhere. A judge asked the question its item's disposition calls for
// examines that item; one asked a question aimed at a different disposition
// ratifies whatever it is handed. The `implementor` brief is the DELEGATION
// judge from that file, which already asks exactly this question of a blank.
//
// `needsSupport` is the posture, and the two postures are the two gates this
// workflow already runs. A disposition that CREATES text nothing else reviews
// takes the defect gate's, where the materiality skeptic is told to default to
// refuted when uncertain, so it needs affirmative support and an uncertain
// verdict refutes it. The rest take the falsification panel's, standing unless
// conclusively refuted, which is also each collector brief's own default. The
// column is the one in the disposition table above.
const FALSIFIERS = {
  resolve: {
    needsSupport: true,
    brief:
      "You are the GROUND judge. The phase proposes to ANSWER this decision and stage the answer into " +
      "the proposal, which is text nothing else in this run reviews. Show the answer is not derivable " +
      "from the ground the item cites: open every source it quotes, check the sentence says what the " +
      "reading says it says, check the answer follows from it rather than from the reader's preference, " +
      "and check nothing elsewhere in the proposal or the tree settles it differently. An answer that is " +
      "merely plausible has not been derived.",
  },
  human: {
    needsSupport: false,
    brief:
      "You are the HUMAN-QUESTION judge. The phase proposes to leave this to a person. Show the " +
      "five-clause test does not hold for it: that it trades no two goods the proposal cannot rank, does " +
      "not change what the proposal IS by widening or narrowing the deliverable, commits the platform to " +
      "no contract the tree leaves unsettled, neither decides another proposal's fate nor turns on one, " +
      "and accepts no named residual cost. The common refutation is that the repository already answers " +
      "it, so look for that answer before concluding there is none: a decision parked for a human that " +
      "the tree settles costs a review cycle and teaches the reviewer that the list is noise.",
  },
  implementor: {
    needsSupport: false,
    brief:
      "You are the DELEGATION judge. Would an IMPLEMENTOR'S CHOICE marker actually BOUND the choice " +
      "here, or would it delegate without a constraint? A blank with no constraint is a licence, and the " +
      "convention forbids it for a wire contract, a fail-closed predicate, an ordering another step " +
      "depends on, or anything a test must assert. A bounded blank is the format working as intended, " +
      "and preferring a more detailed proposal is not a refutation.",
  },
  "out-of-scope-stands": {
    needsSupport: false,
    brief:
      "You are the RESIDUAL judge. The phase proposes to leave this defect standing in the tree and " +
      "record it in the summary. Show the proposal cannot ship that way: that leaving the defect makes " +
      "this proposal's own staged changes wrong, unimplementable, or false about the tree they land in. " +
      "Disagreeing with the scope the proposal chose is not that showing, and the declaration is a scope " +
      "decision the proposal already took with its reason.",
  },
  "out-of-scope-wrong": {
    needsSupport: true,
    brief:
      "You are the SCOPE judge. The phase proposes to specify a solution to a defect this proposal " +
      "declared out of scope, which widens the deliverable and creates text nothing else in this run " +
      "reviews. Show the call was right after all: that the staged changes are correct, implementable, " +
      "and true about the tree with this defect left standing, so this proposal need not solve it.",
  },
  "impact-row": {
    needsSupport: true,
    brief:
      "You are the EFFECT judge. The row asserts what this proposal's staging does to another proposal, " +
      "and the summary is the only place this proposal says anything about another proposal's continued " +
      "validity, so a row nobody can support is a false claim about that proposal rather than a cautious " +
      "one. Show the stated effect is not what the current staging does to it: read the staged text and " +
      "the other proposal rather than the row's account of either, and check the status and the date the " +
      "row rests on.",
  },
};

// Stated to the falsifier in the words the gate reads its verdict with, so it
// is not guessing what `partial` costs.
function postureNote(needsSupport) {
  if (needsSupport) {
    return (
      "THIS DISPOSITION NEEDS AFFIRMATIVE SUPPORT AND IS SET ASIDE UNLESS YOU CONFIRM IT. It creates " +
      "text nothing else in this run reviews, so this gate defaults to refuted when uncertain: `partial` " +
      "sets the item aside exactly as `conclusive` does. Report `none` when you looked for the " +
      "refutation and did not find it, which is a confirmation rather than an absence of work."
    );
  }
  return (
    "THIS DISPOSITION STANDS UNLESS YOU FALSIFY IT CONCLUSIVELY. So `partial` is an honest and common " +
    "answer: it means you have doubt you can articulate and cannot settle, and it leaves the disposition " +
    "standing. Do not inflate it to `conclusive` to be heard, and do not deflate a real refutation to " +
    "`partial` to avoid disturbing the phase."
  );
}

// What the falsifier is handed: one item, whole, with every reading behind it.
// Sliced for the same reason the parent slices its evidence blocks -- three
// readings of a long decision run to thousands of characters -- and the brief
// tells the falsifier the documents are the subject rather than this summary
// of them.
function itemBlock(item) {
  return JSON.stringify(
    {
      id: item.id,
      subTask: item.subTask,
      home: item.home,
      question: item.question,
      disposition: item.disposition,
      agreement: item.agreement,
      alternatives: item.alternatives,
      readings: item.readings,
    },
    null,
    2,
  ).slice(0, 12000);
}

function falsifyPrompt(item, spec) {
  return (
    "You are the falsifier for ONE item in the open-decisions-and-impact-review phase of a change " +
    "proposal's review. This is firing " + firing + " (" + trigger + ") on " + P.stem + ". The phase has " +
    "disposed of this item as `" + item.disposition + "`. YOUR JOB IS TO FALSIFY THAT, not to vote on " +
    "it. Reach for the evidence that would show the disposition wrong, and report honestly whether you " +
    "found it.\n\n" +
    COLLECTOR_READ_ONLY + "\n\n" +
    FILE_MAP + "\n\n" +
    EVIDENCE + "\n\n" +
    STANDING_CONTEXT + "\n\n" +
    "YOUR LENS. " + spec.brief + "\n\n" +
    postureNote(spec.needsSupport) + "\n\n" +
    "RATIFYING IS THE OTHER FAILURE. You have been handed a conclusion and asked to check it, which is " +
    "the situation in which reviewers agree most and examine least. Attack the case the phase actually " +
    "made, which is why you must restate it in your own words in `theDispositionIAttacked` before you " +
    "attack it.\n\n" +
    "YOU HOLD THIS ITEM AND NOTHING ELSE, which is the point of the arrangement: open every file it " +
    "touches rather than resting on the readings' account of them.\n\n" +
    "WHAT A REFUTATION COSTS. A refuted item is SET ASIDE: it is not applied, no substitute disposition " +
    "is invented for it, and the proposal keeps the text it has. A refuted `resolve` leaves the decision " +
    "open and listed for the human. A refuted `human` names no replacement, so its entry stays listed " +
    "with your refutation recorded against it, which is what lets a later firing see that the ground was " +
    "contested without re-arguing it. A refuted `implementor` leaves the blank as the proposal wrote " +
    "it.\n\n" +
    "IF YOU FALSIFY IT, NAME WHAT THE EVIDENCE DOES SUPPORT in `fallbackDisposition`, from `resolve`, " +
    "`human`, `implementor`, `out-of-scope-stands`, `out-of-scope-wrong`, and `impact-row`. This phase " +
    "acts on no substitute and orders these values in no way, so a falsification naming nothing is set " +
    "aside exactly as one naming something is. Name it for the record a later firing reads, and leave it " +
    "empty when you did not falsify.\n\n" +
    "THE ITEM, with every reading behind it, including the case each made against its own " +
    "disposition:\n" + itemBlock(item)
  );
}

// The verdict, read under the item's own posture. This is the whole of the
// gate: a falsifier reports what it found and the script decides what that
// means for the item.
function gateVerdict(needsSupport, v) {
  if (needsSupport) {
    // Affirmative support or nothing. A falsifier that refuted the disposition
    // and one that could not settle its own doubt both leave the item without
    // the support it needs, which is the defect gate's posture.
    return !v.falsified && v.howConclusive === "none" ? "stands" : "refuted";
  }
  return v.falsified && v.howConclusive === "conclusive" ? "refuted" : "stands";
}

// One item's gate. The verdict is recorded on the item whichever way it goes,
// because a refutation is what lets a later firing see that the ground was
// contested, and an unadjudicated item is a different answer from a refuted
// one.
async function falsifyItem(item, index) {
  const spec = FALSIFIERS[item.disposition];
  const label = "f" + firing + ":falsify:" + index;
  if (!spec) {
    // The collectors clamp every disposition to their own brief's vocabulary,
    // so this is unreachable through them. It is guarded anyway because the
    // alternative to a guard here is an item that reaches Apply having passed
    // no gate at all.
    item.gate = "unadjudicated";
    item.survives = false;
    item.falsification = null;
    log("  " + label + ': no falsification brief for disposition "' + item.disposition + '"; the item is UNADJUDICATED');
    return;
  }
  const v = await robustAgent(falsifyPrompt(item, spec), {
    label,
    schema: ITEM_FALSIFICATION,
    phase: "Falsify",
  });
  if (!v) {
    // A falsifier that DIED is not a refutation and is not a confirmation
    // either, following the review loop's own verification guard: the item
    // reaches neither, and the rest of the items proceed.
    recordDead(label);
    item.gate = "unadjudicated";
    item.survives = false;
    item.falsification = null;
    log("  " + label + ": the falsifier returned nothing after retries; " + item.id + " is UNADJUDICATED and is not applied");
    return;
  }
  item.gate = gateVerdict(spec.needsSupport, v);
  item.survives = item.gate === "stands";
  item.falsification = v;
  if (item.gate === "refuted") {
    log(
      "  " + label + ": " + item.disposition + " REFUTED (" + (v.howConclusive || "unstated") + ")" +
        (spec.needsSupport && !v.falsified ? ", on an uncertain verdict under a posture that needs support" : "") +
        "; the item is set aside" +
        (v.fallbackDisposition ? ", and the evidence was said to support " + v.fallbackDisposition : ""),
    );
  }
}

// The gate, over every item this firing collected. One falsifier per item, in
// parallel, and the tally is SCRIPT-SIDE: each falsifier returns a verdict for
// its own item and this decides what reaches Apply. If the Apply stage decided
// what survived, the gate would not be a gate.
async function falsifyAll(list) {
  if (list.length === 0) {
    log("Nothing to falsify: this firing collected no items");
    return [];
  }
  log("Falsifying " + list.length + " item(s), one agent each, each briefed to refute its own item's disposition");
  await parallel(list.map((item, i) => () => falsifyItem(item, i)));
  const survived = list.filter((item) => item.survives);
  const setAside = list.filter((item) => item.gate === "refuted");
  const unexamined = list.filter((item) => item.gate === "unadjudicated");
  log(
    "Gate: " + survived.length + " stood, " + setAside.length + " set aside, " + unexamined.length +
      " unadjudicated" +
      (unexamined.length > 0 ? " (a dead falsifier leaves its own item unadjudicated and the rest proceed)" : ""),
  );
  return survived;
}

// ---- The write path: one Apply per surviving item, sequentially ------------
//
// Sub-task 6. One agent per item that survived the gate carrying a disposition
// that requires an edit, each holding ONE resolution and writing it.
//
// SEQUENTIAL rather than parallel, for the reason the parent's fix stage
// records: the agents edit the same markdown files, and concurrent edits to one
// file lose writes, which is why the fixer serialises too. Each agent after the
// first is told what the earlier ones in this firing actually did, following
// the same rule the fixer follows, because an agent that cannot see the edits
// already landed will duplicate or contradict them.
//
// The fan-out pays twice. Each item's edit is its own commit-sized diff, which
// is what detecting a reversal per item rather than per firing rests on. And a
// dead Apply takes one item with it rather than the whole batch.

// The parent's PROBLEM_STATEMENT_RULE, reproduced here for the reason
// robustAgent is: a subworkflow is a separate script with no import path to the
// parent. Both review loops already grant this correction-only right and the
// phase inherits it unchanged, so one rule is stated in two files. Its closing
// clause is the one adaptation: the parent's fixer reports through a summary
// field over a finding, and this phase's Apply reports through `note` over an
// item.
const PROBLEM_STATEMENT_RULE =
  "  " + P.problem + " — CORRECT THE RECORD here, and only that. A false citation, a line number that has " +
  "drifted, an evidence claim the tree refutes, or a premise this review has knocked down is a defect in " +
  "the problem statement and you fix it there, in the same edit as the section that restates it: leaving " +
  "the two disagreeing is worse than leaving both wrong. You may NOT change what the problem IS, widen or " +
  "narrow its scope, or abandon its framing. That is a reframe, it is the introspection pass's decision " +
  "and not yours, and if this item seems to need one, say so in `note` and write what you can.\n";

// The phase's own editable set. No existing constant's grant matches it:
// SPEC_EDITABLE omits the staged non-spec changes, and NONSPEC_EDITABLE also
// grants the implementation checklist and is written as a fixer brief. The
// phase's set is the two staged change files, the summary, and the review log,
// plus the correction-only right above.
//
// The review log is named as a FILE this phase edits directly rather than as a
// log shard: a shard is merged at a round boundary, and a firing runs after its
// loop's last one, so a shard it wrote would wait for the next loop's first
// boundary or never be merged at all.
//
// A THUNK, like the parent's four, so it reads lockSpecChanges when the prompt
// is built rather than when the file loads. The parent's own copy of that flag
// is assignable and the operator's override writes it, so a constant that
// snapshots it at load states a grant the run is no longer running under. The
// phase has no fixer prompt, so this is consumed in the per-item Apply prompt.
const PHASE_EDITABLE = () =>
  "You may edit ONLY these files:\n" +
  "  " + P.nonSpec + " — the staged code, schema, chart, migration, docs and test changes, where a " +
  "resolution to a non-spec decision is staged\n" +
  (lockSpecChanges
    ? ""
    : "  " + P.spec + " — the staged spec edits, where a resolution to a spec decision is staged\n") +
  PROBLEM_STATEMENT_RULE +
  "  " + P.summary + " — the three sections this phase owns: `## Open decisions for human to make`, " +
  "`## Defects in the shipped tree that this proposal does not stage`, and `## Impacts on other " +
  "proposals`. Correct a statement elsewhere in the file that your own edit falsifies, and leave " +
  "`## Deliverable index` exactly as it stands: the reconciliation pass between the loops owns it.\n" +
  "  " + P.log + " — the review log itself, where you record what you wrote. This phase writes it " +
  "directly rather than through a shard, because a shard is merged at a round boundary and this firing " +
  "runs after its loop's last one.\n" +
  (lockSpecChanges
    ? "  " + P.spec + " is LOCKED for this run by the operator. A resolution that needs a staged spec " +
      "edit is NOT applied: report `blocked`, state in `note` the edit you would have made and the " +
      "ground for it, and it is recorded for the operator instead of staged.\n"
    : "") +
  "Every other file in the proposal, including the implementation checklist, and every file outside it, " +
  "is out of bounds.";

// What one Apply reports. Deliberately small, for the reason the entry schema
// above is: the field guidance is in the prompt and what the schema enforces is
// that the three answers are present. `wrote` and `where` are the record a later
// firing compares the tree against to tell a reversal from a rewording, so they
// are required rather than optional.
const APPLY_RESULT = {
  type: "object",
  required: ["outcome", "wrote", "where"],
  properties: {
    outcome: {
      type: "string",
      enum: ["edited", "already-correct", "blocked"],
      description:
        '"edited" when you wrote the resolution into the proposal, "already-correct" when the proposal already carries it exactly as this item states it, "blocked" when writing it needs a file this phase may not edit',
    },
    wrote: {
      type: "string",
      description:
        "the text you wrote, verbatim, so a later firing can tell whether it is still there. Empty when you edited nothing.",
    },
    where: {
      type: "array",
      items: { type: "string" },
      description: "one entry per edit, as `file — the section or anchor you wrote it at`",
    },
    note: {
      type: "string",
      description:
        "on already-correct, the text that already carries it with its file:line; on blocked, the edit you could not make and why",
    },
  },
};

// What each disposition tells its agent to write, keyed the way the falsifiers
// are, because the edit a resolution needs and the edit a human decision needs
// have nothing in common beyond the file they land in. `implementor` has no
// entry: its default is to leave the blank exactly as the proposal wrote it, and
// specifying one is `resolve`.
const APPLIERS = {
  resolve: {
    brief:
      "You are staging an ANSWER. Write it into the staged changes where the decision lives: a decision " +
      "about the spec staging lands in the staged spec edits and one about the code, schema, chart, " +
      "migration, docs or test staging lands in the staged non-spec changes, beside the text it concerns " +
      "and in the form that file states its other staged changes in. Cite the ground as file:line, and " +
      "name the authority the answer rests on so a reader can tell a derived answer from an asserted one. " +
      "Then REMOVE the item's entry from `## Open decisions for human to make` in the summary: a resolved " +
      "decision leaves that list, and no `### Retired` or equivalent block replaces it. Where the item " +
      "was found in a staged change file's `## Open decisions for review` section, delete its entry " +
      "there as well, and delete that section once it is empty: a resolved entry leaves both files. " +
      "Write the answer and nothing that widens the deliverable beyond it.\n\n" + NO_DECISION_REFS_RULE,
  },
  human: {
    brief:
      "The decision stays the human's, and your job is to make it answerable. Ensure `## Open decisions " +
      "for human to make` in the summary carries exactly one entry for it, stating the question so a " +
      "person can answer it without reading the proposal, the recommendation, the ground it rests on, the " +
      "alternatives and why each lost, what deciding otherwise costs, and a confidence. Where the item " +
      "was found in a staged change file's `## Open decisions for review` section, MIGRATE it: write the " +
      "entry in the summary, delete it there, and delete that section once it is empty, because the " +
      "summary's section is the one home for these now. Do not stage an answer: the phase gated this item " +
      "as the human's.\n\n" + NO_DECISION_REFS_RULE,
  },
  "out-of-scope-stands": {
    brief:
      "The proposal's call is right and the defect stays in the tree. Record it in `## Defects in the " +
      "shipped tree that this proposal does not stage` in the summary, as one entry naming the defect, " +
      "where it is as file:line, and why this proposal does not stage a fix. That section holds no " +
      "decisions and stages no work, so do not open a decision about the defect and do not stage a fix " +
      "for it.",
  },
  "out-of-scope-wrong": {
    brief:
      "The call was wrong and this proposal must solve the defect. Specify the solution in the staged " +
      "changes for the lane it lands in, WHOLE: the change, every site it touches, and the test that pins " +
      "it, because nothing else in this run designs it. Then correct or remove the declaration that said " +
      "the proposal would not solve it, and remove its entry from `## Defects in the shipped tree that " +
      "this proposal does not stage` where one is there, so the proposal does not both stage the fix and " +
      "say it does not.",
  },
  "impact-row": {
    brief:
      "Write the row in `## Impacts on other proposals` in the summary: the other proposal, its status " +
      "and the date that status rests on, what this proposal's current staging does to it, and what that " +
      "leaves its author to do. That section is the ONLY place this proposal asserts anything about " +
      "another proposal's continued validity, so a statement of the same kind sitting elsewhere in the " +
      "summary moves here, and a row this firing's staging has made false is corrected rather than left " +
      "beside the new one. Edit no file belonging to the other proposal.",
  },
};

// The stamp. FORMAT_SUMMARY requires every entry under the human's section to
// carry a stable identifier, and the collectors are told never to invent one,
// because the phase joins three readings and successive firings on that string.
// Stamping an entry that carries none is this stage's work, which is why the
// rule sits in the write path rather than in a collector.
const IDENTIFIER_STAMP =
  "THE IDENTIFIER. Every entry under `## Open decisions for human to make` carries a stable identifier. " +
  "An entry that already has one keeps it verbatim, including through a rewrite of the entry: successive " +
  "firings join on that string and an entry that loses it is adjudicated afresh. An entry you write for " +
  "the first time is stamped now, in the form the section already uses, or as `OD-1`, `OD-2` and so on " +
  "where no entry carries one. Never reuse an identifier a withdrawn entry held.";

// The checklist is out of bounds here for the reason it is out of bounds for the
// spec fixer, and the same escape applies: a deliverable this phase adds or
// removes is recorded for the pass that owns the checklist rather than edited
// into it.
const CHECKLIST_DEFERRAL =
  "THE IMPLEMENTATION CHECKLIST IS NOT YOURS. Where your edit adds, removes, merges, splits or " +
  "resequences a staged deliverable, record it as a `DEFERRED [" + P.checklist + "]` line in " + P.log +
  " naming the step that is now wrong and what is true instead. Do not edit the checklist.";

// What the earlier Applies in THIS firing actually did. It costs no agent, the
// firing already has it, and it is the one thing this agent's item could not
// carry: the item was collected and gated before any edit landed.
function earlierAppliesBlock(earlier) {
  if (earlier.length === 0) return "";
  return (
    "\n\nWHAT THE EARLIER APPLIES IN THIS FIRING ALREADY DID. They ran before you, against the same " +
    "files, and their edits are in the text you are about to read. Check the current text rather than " +
    "your item's account of it, and where an earlier one has already written what your item calls for, " +
    "say so and do not write it twice.\n" +
    earlier.map((line, i) => i + 1 + ". " + line).join("\n")
  );
}

// The gate's own verdict, handed to the writer. A `partial` doubt that left the
// disposition standing is exactly what the text should be written carefully
// around, and it is lost if only the disposition travels.
function gateBlock(item) {
  const v = item.falsification;
  if (!v) return "";
  return (
    "\n\nTHE GATE'S VERDICT ON THIS ITEM. A falsifier attacked this disposition and it stood. What it " +
    "found is not a reason to write something else, and an articulated doubt is a reason to write " +
    "carefully:\n" +
    JSON.stringify(
      {
        howConclusive: v.howConclusive,
        theDispositionIAttacked: v.theDispositionIAttacked,
        reasoning: v.reasoning,
      },
      null,
      2,
    ).slice(0, 4000)
  );
}

// The human item whose entry LEAVES the section. The `human` brief's default is
// to ensure an entry is there, which would write back the entry this phase is
// about to report as withdrawn, so the one case where the entry goes is stated
// here rather than left to the agent to infer.
function withdrawalNote(item) {
  if (!isWithdrawal(item)) return "";
  return (
    " THIS ITEM'S ENTRY LEAVES THE SECTION: every reading of it reports the summary's entry as withdrawn, " +
    "so nobody is being asked anything any more. REMOVE the entry from `## Open decisions for human to " +
    "make` rather than ensuring one is there, delete it from a staged change file's `## Open decisions " +
    "for review` section as well and delete that section once it is empty, and record in " + P.log +
    " what made it moot. No `### Retired` or equivalent block replaces it."
  );
}

function applyPrompt(item, spec, earlier) {
  return (
    "You are the Apply agent for ONE item in the open-decisions-and-impact-review phase of a change " +
    "proposal's review. This is firing " + firing + " (" + trigger + ") on " + P.stem + ". The item's " +
    "disposition is `" + item.disposition + "` and it has been through the phase's gate. YOUR JOB IS TO " +
    "WRITE IT INTO THE PROPOSAL.\n\n" +
    "HARD CONSTRAINT. " + PHASE_EDITABLE() + "\n" +
    "Never modify anything under spec/, docs/, pkg/, charts/, or schemas/: this proposal STAGES its " +
    "changes and never applies them.\n\n" +
    FILE_MAP + "\n\n" +
    EVIDENCE + "\n\n" +
    STANDING_CONTEXT + "\n\n" +
    "WHAT YOU WRITE FOR THIS ITEM. " + spec.brief + withdrawalNote(item) + "\n\n" +
    IDENTIFIER_STAMP + "\n\n" +
    CHECKLIST_DEFERRAL + "\n\n" +
    "YOU HOLD ONE ITEM. Make the smallest edit that lands it, re-verify every citation you write or " +
    "touch against the tree, and reconcile any statement elsewhere in the files you may edit that your " +
    "own edit falsifies. Adjudicate nothing else: another agent holds each of the other items, and an " +
    "edit you make on its behalf lands ungated.\n\n" +
    "THE DISPOSITION IS NOT YOURS TO REOPEN. Three readings and a falsifier reached it before you, and " +
    "an Apply that writes a different answer writes text nothing in this run has reviewed. Where the " +
    "proposal already carries this item exactly as stated, report `already-correct` and edit nothing. " +
    "Where writing it needs a file your HARD CONSTRAINT puts out of bounds, report `blocked` with the " +
    "edit you would have made. Both are honest answers; substituting your own resolution is not.\n\n" +
    "RECORD WHAT YOU WROTE IN THE REVIEW LOG, never as a pass history inside a change file: a history " +
    "appended to the staged changes stages nothing and is read in full by every lens every round. One " +
    "line under this firing naming the item, what you wrote, and where.\n\n" +
    LOG_WRITE_RULE + "\n\n" +
    "GIT IS THE EVIDENCE. What this firing changed is read from the diff under the proposal directory " +
    "rather than from this report, and an `edited` outcome whose diff is empty fails this item. Report " +
    "what you actually wrote.\n\n" +
    "THE ITEM, with every reading behind it:\n" + itemBlock(item) +
    gateBlock(item) +
    earlierAppliesBlock(earlier) +
    "\n\nFollow " + repo + "/.claude/rules/doc-style.md."
  );
}

// Whether the summary already carries an item whose disposition leaves it
// there. `unchanged` is the only value that says so: `not-applicable` says the
// summary holds no entry for this item, which is the opposite. An item every
// reading reports as already carried needs no edit, because writing one would
// be a rewrite of text this phase gated as correct.
function summaryAlreadyCarriesIt(item) {
  const actions = (item.readings || []).map((r) => (r.entry && r.entry.summaryAction) || "");
  return actions.length > 0 && actions.every((a) => a === "unchanged");
}

// Which surviving items need an edit, read off the disposition table above.
// `resolve` and `out-of-scope-wrong` always author. `implementor` never does.
// The other three need one unless the summary already carries the item as
// written.
function needsApply(item) {
  // A carried-forward item was disposed of and gated by an earlier firing, so
  // the only reason to author for it now is that the earlier Apply never landed.
  if (item.carried) {
    return item.carried.applyStatus === "failed" || item.carried.applyStatus === "not-attempted";
  }
  if (item.disposition === "implementor") return false;
  if (item.disposition === "resolve" || item.disposition === "out-of-scope-wrong") return true;
  // A `human` item found anywhere but the summary is not in the summary yet,
  // whatever its readings said the summary entry needs: the migration out of a
  // staged change file, the design item and the unclosed `OPEN` all need the
  // entry written. `out-of-scope-stands` and `impact-row` take no such clause,
  // because their homes are fixed by their briefs and a home test would force
  // an Apply on every firing for a row already present and unchanged.
  if (item.disposition === "human" && item.home !== "summary-open-decisions") return true;
  return !summaryAlreadyCarriesIt(item);
}

function whyNoEdit(item) {
  if (item.carried) {
    return (
      "carried forward from firing " + item.carried.fromFiring + ", where the Apply stage recorded it as " +
      item.carried.applyStatus
    );
  }
  if (item.disposition === "implementor") {
    return "an implementor blank that stands needs no edit: the default is to leave it as the proposal wrote it";
  }
  return "the summary already carries this item as written";
}

// One item's own diff, derived from the cumulative reading taken after the
// previous Apply and the one taken after this one. Each reading is the working
// tree against the baseline commit, so a file this Apply touched either appears
// for the first time or changes its line counts.
//
// An edit that leaves a file's added and removed counts exactly as the previous
// Apply left them is invisible to this, an exact restoration included. That is
// the cumulative reading's own blind spot, and it is why the outcome is stated
// as "the diff is empty" rather than as "you wrote nothing".
function deltaSince(before, now) {
  const prior = new Map();
  for (const f of before.files || []) prior.set(f.path, f);
  const files = [];
  for (const f of now.files || []) {
    const b = prior.get(f.path);
    if (!b || b.added !== f.added || b.removed !== f.removed) files.push(f);
  }
  return { files };
}

// The write path over the items the gate passed. Sequential, one agent each, and
// the record of what each wrote is built from the tree rather than from the
// agent. The three outcome arrays are the firing's, declared below with the rest
// of what it returns.
async function applyAll(list) {
  const queue = [];
  for (const item of list) {
    if (needsApply(item)) {
      queue.push(item);
      continue;
    }
    // A carried-forward or contested item already carries an earlier firing's
    // record, and that record is what the report is built from.
    if (!item.apply) item.apply = { status: "no-edit-needed", reason: whyNoEdit(item) };
  }
  if (queue.length === 0) {
    log("Nothing to apply: no surviving item needs an edit");
    return;
  }
  log(
    "Applying " + queue.length + " of " + list.length + " surviving item(s), one agent each, " +
      "SEQUENTIALLY: they edit the same markdown files and concurrent edits to one file lose writes",
  );
  // The tree as the previous Apply left it. The baseline commit ran first, so
  // the tree under the pathspec starts clean and the first reading is this
  // firing's first edit.
  let before = { files: [] };
  const earlier = [];
  for (let i = 0; i < queue.length; i++) {
    const item = queue[i];
    const label = "f" + firing + ":apply:" + i;
    const spec = APPLIERS[item.disposition];
    if (!spec) {
      // Unreachable through the gate, which passes only a disposition that has
      // a falsification brief, and guarded anyway: the alternative is an item
      // recorded as applied that no agent ever wrote.
      item.apply = { status: "failed", reason: 'no Apply brief for disposition "' + item.disposition + '"' };
      failedItems.push({ id: item.id, disposition: item.disposition, question: item.question, ...item.apply });
      log("  " + label + ': no Apply brief for disposition "' + item.disposition + '"; ' + item.id + " is NOT applied");
      continue;
    }
    const res = await robustAgent(applyPrompt(item, spec, earlier), {
      label,
      schema: APPLY_RESULT,
      phase: "Apply",
    });
    if (!res) recordDead(label);
    // Taken after EVERY agent that ran, including one that died or claims it
    // wrote nothing: an agent can edit and then fail to return, and a reading
    // skipped here would credit that edit to the next item.
    const now = await treeDelta("apply:" + i);
    const own = now ? deltaSince(before, now) : null;
    // A dead detection agent leaves the cumulative reading where it was, so the
    // next item's own diff may carry this one's edits. That is recorded against
    // this item as missing evidence rather than silently absorbed.
    if (now) before = now;
    if (!res) {
      const rec = applyOutcome(item, null, own);
      item.apply = rec;
      failedItems.push(rec);
      earlier.push(item.id + " (" + item.disposition + "): the Apply agent died; nothing was recorded for it");
      log("  " + label + ": the Apply agent returned nothing after retries; " + item.id + " is NOT applied and the rest proceed");
      continue;
    }
    if (res.outcome === "blocked") {
      const rec = {
        id: item.id,
        disposition: item.disposition,
        question: item.question,
        status: "recorded",
        reason: res.note || "the edit this resolution needs is out of bounds for this phase",
        wrote: res.wrote || "",
      };
      item.apply = rec;
      recordedForOperator.push(rec);
      earlier.push(item.id + " (" + item.disposition + "): blocked, nothing written — " + rec.reason);
      log("  " + label + ": " + item.id + " could not be written and is RECORDED for the operator: " + rec.reason);
      continue;
    }
    if (res.outcome === "already-correct") {
      const rec = {
        id: item.id,
        disposition: item.disposition,
        question: item.question,
        status: "unchanged",
        reason: res.note || "the proposal already carries this item as stated",
      };
      item.apply = rec;
      if (own && own.files.length > 0) {
        // Reported rather than reclassified: the tree says the agent edited and
        // the agent says it did not, and which one is right is not decidable
        // from here.
        rec.files = own.files;
        log(
          "  " + label + ": " + item.id + " reported already-correct while the diff shows " +
            own.files.map((f) => f.path).join(", ") + " changed",
        );
      }
      earlier.push(item.id + " (" + item.disposition + "): already carried by the proposal, no edit made");
      continue;
    }
    const rec = applyOutcome(item, res, own);
    item.apply = rec;
    if (rec.status === "applied") {
      applied.push(rec);
      log(
        "  " + label + ": " + item.id + " applied, " + rec.files.length + " file(s) changed (" +
          (res.where || []).join("; ").slice(0, 160) + ")",
      );
      earlier.push(
        item.id + " (" + item.disposition + "): wrote at " + (res.where || []).join("; ") + " — " +
          String(res.wrote || "").slice(0, 400),
      );
      continue;
    }
    failedItems.push(rec);
    earlier.push(item.id + " (" + item.disposition + "): claimed an edit that the diff does not carry");
    log("  " + label + ": " + item.id + " is NOT applied: " + rec.reason);
  }
}

// ---- Sub-task 7: the summary cleanup ---------------------------------------
//
// One agent, run AFTER Apply, because what belongs in each section depends on
// what Apply resolved: a decision the write path answered has left the human's
// section by the time this runs, and one it left with the human is listed
// there. Running it first would clean up around items that were about to move.
//
// It rewrites the summary to exactly the sections below, in order, and nothing
// else. Content the list does not name is RELOCATED to its stated destination
// rather than deleted: a summary in the wild accumulates blocks under its
// decisions section that are not decisions, and each kind belongs somewhere.

// THE LIST, which is the one `FORMAT_SUMMARY` states in change-proposal.js.
// The two are one statement of the same format, so a section added there is
// added here in the same change. It is rendered for the prompt rather than
// derived from the file, because a summary that has lost a section gets it back
// rather than having the loss ratified.
const SUMMARY_SECTIONS =
  "  `# Summary: <title>`\n" +
  "  `## Summary` — a container that carries no prose of its own, holding these labelled parts in this " +
  "order: `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**`\n" +
  "  `## Goals`\n" +
  "  `## Non-goals`\n" +
  "  `## Open decisions for human to make`\n" +
  "  `## Defects in the shipped tree that this proposal does not stage`\n" +
  "  `## Impacts on other proposals`\n" +
  "  `## Deliverable index`\n";

// `**Fixed decisions.**` is the old spelling of the listed part `**Decisions.**`
// and proposals on disk carry it. It is renamed in place: relocating it would
// move the closed decisions out of the summary and deleting it would lose them.
const RENAME_RULE =
  "A PART LABELLED `**Fixed decisions.**` IS RENAMED IN PLACE to `**Decisions.**`, keeping its content " +
  "and its position under `## Summary`. It is a listed part under its new name, so it is neither " +
  "relocated nor deleted. A part labelled `**What is fixed.**` is the same case for " +
  "`**Problem statement.**`.";

// The index is on the list and is not this phase's to maintain. The
// reconciliation pass between the loops rebuilds it against the staged
// deliverables, and it is the only place a deliverable id resolves, so an
// index this pass shortens or reorders breaks every citation of an id.
const INDEX_RULE =
  "`## Deliverable index` IS NOT YOURS TO MAINTAIN. Preserve it exactly as it stands, in LAST position, " +
  "line for line: the reconciliation pass between the review loops rebuilds it against the staged " +
  "deliverables, and it is the only place a deliverable id resolves, so the checklist and both change " +
  "files cite it. Do not add a line, remove a line, reword a line, reorder it, or move it above another " +
  "section. Where the file carries no index at all, leave it absent and say so in `note` rather than " +
  "inventing one.";

// The problem statement part survives and is not rewritten. Correcting a
// falsehood in it is the Apply stage's right under PROBLEM_STATEMENT_RULE;
// restating what the problem IS is a reframe, which belongs to the
// introspection pass.
const PROBLEM_PART_RULE =
  "`**Problem statement.**` SURVIVES AND IS NOT REWRITTEN. It says what the change repairs, without the " +
  "evidence, the citations, or the refuted premises, which stay in " + P.problem + ". You may move it " +
  "into position and rename it from `**What is fixed.**`, and you may not restate what the problem IS, " +
  "widen or narrow its scope, or abandon its framing. That is a reframe, it is the introspection pass's " +
  "decision, and an edit that makes one is refused.";

// A decision this phase resolved or withdrew leaves the section. Parking it
// under a heading inside the very section it was supposed to leave is the
// failure this rule exists to prevent, and it has happened: the retired
// decisions stayed in the summary, where every reader pays for them every round.
const NO_RETIRED_RULE =
  "NO `### Retired` OR EQUIVALENT BLOCK SURVIVES INSIDE `## Open decisions for human to make`. A " +
  "decision this phase resolved or withdrew LEAVES that section: its answer is staged in the change " +
  "files and its record is in the review log. Delete such a block once you have checked that each entry " +
  "under it is either answered in the staged changes or listed above as still open, and report in " +
  "`relocated` what you found in it.";

// Where unlisted content goes. Each kind has a destination rather than a
// deletion, and the first kind has no destination because it is this phase's
// own input: sub-task 1 collected it at the top of this firing and every item
// on it has already left as a resolution, an implementor blank, or a human
// decision.
const RELOCATION_RULE =
  "WHERE UNLISTED CONTENT GOES. Content the list does not name is RELOCATED rather than deleted. Each " +
  "kind below has its own destination, and anything you cannot place is reported rather than dropped.\n" +
  "  - A META-LIST OF STAGED ITEMS with a proposed disposition for each is this phase's own INPUT rather " +
  "than content to relocate. Sub-task 1 collected it at the top of this firing and every item on it has " +
  "left as one of three things: a resolution staged into the change files, an implementor blank left as " +
  "the proposal wrote it, or a decision listed under `## Open decisions for human to make`. Check each " +
  "item against those three, list the ones still the human's as entries of that section, and drop the " +
  "block. An item you cannot account for stays listed as the human's.\n" +
  "  - A BLOCK OF CONFIRMED SHIPPED-TREE DEFECTS is promoted UNCHANGED to `## Defects in the shipped " +
  "tree that this proposal does not stage`. Move the entries verbatim: they were confirmed against the " +
  "tree by an earlier pass and rewording them here re-asserts them on your authority.\n" +
  "  - PROSE ABOUT ANOTHER PROPOSAL merges into `## Impacts on other proposals`, which is the ONLY place " +
  "this proposal asserts anything about another proposal's continued validity. Two places in one file " +
  "asserting it is the drift that section exists to prevent, so merge into the row already there rather " +
  "than adding a second row beside it, and where the two disagree say so in `note`.\n" +
  "  - A BLOCK OF CORRECTIONS THE PROPOSAL OWES TO FILES ITS LOOP COULD NOT EDIT becomes `DEFERRED` " +
  "entries in " + P.log + ", one per correction, each naming the file, the claim that is now false, and " +
  "what is true instead. A measured run grew a nine-hundred-word errata list in this file's watch-out " +
  "part, promising fixes no lane owned, because this was the only surface it could write. It no longer " +
  "is.\n" +
  "  - ANYTHING ELSE goes into the listed section whose subject covers it. Where none does, record it as " +
  "an `OPEN` line in " + P.log + " naming what it was and where it stood, and report it in `relocated`. " +
  "Deleting content is not an option this pass has.";

// The cleanup's grant is narrower than the Apply stage's. Every destination the
// relocation rule names is inside the summary or inside the review log, and an
// agent rewriting a file's section list has no business in the staged changes.
// It does not branch on lockSpecChanges for the same reason: the staged spec
// edits are outside its grant at either setting.
const CLEANUP_EDITABLE =
  "You may edit ONLY these files:\n" +
  "  " + P.summary + " — the whole file, which you are rewriting to the section list below\n" +
  "  " + P.log + " — where a relocated correction becomes a `DEFERRED` entry and anything you could not " +
  "place becomes an `OPEN` one\n" +
  "Every other file is out of bounds, the staged change files, the problem statement and the " +
  "implementation checklist included. Never modify anything under spec/, docs/, pkg/, charts/, or " +
  "schemas/: this proposal STAGES its changes and never applies them.";

// What the cleanup reports. Small, for the reason the entry schema is: the
// field guidance is in the prompt and what the schema enforces is that the
// three answers are present. `sections` is the headings as the file now stands,
// which is what the verify pass checks the list against.
const CLEANUP_RESULT = {
  type: "object",
  required: ["outcome", "sections", "relocated"],
  properties: {
    outcome: {
      type: "string",
      enum: ["rewritten", "already-conforming", "blocked"],
      description:
        '"rewritten" when you changed the file, "already-conforming" when it already carried exactly the listed sections in order, "blocked" when conforming it needs a file this stage may not edit',
    },
    sections: {
      type: "array",
      items: { type: "string" },
      description: "the file's headings as it now stands, in order, one per entry",
    },
    relocated: {
      type: "array",
      items: { type: "string" },
      description: "one entry per block you moved, as `what it was — where it went`",
    },
    note: { type: "string" },
  },
};

// What this firing did to each item, which is what tells the cleanup which
// decisions have left the summary and which are still the human's. It costs no
// agent: the firing already holds it, and an agent asked to re-derive it would
// re-adjudicate the items.
function firingRecapBlock() {
  if (items.length === 0) {
    return "This firing adjudicated no items, so nothing left the summary through it. Clean up the file as it stands.";
  }
  return items
    .map((item) => {
      const st = (item.apply && item.apply.status) || (item.survives ? "not-applied" : item.gate || "unadjudicated");
      const reason = (item.apply && item.apply.reason) || "";
      return (
        "- " + item.id + " (" + item.disposition + ", gate: " + (item.gate || "none") + ", apply: " + st +
        (isWithdrawal(item) ? ", entry withdrawn" : "") + ")" +
        (reason ? " — " + reason : "") + ": " + String(item.question || "").slice(0, 200)
      );
    })
    .join("\n")
    .slice(0, 8000);
}

function cleanupPrompt() {
  return (
    "You are the cleanup agent for the open-decisions-and-impact-review phase of a change proposal's " +
    "review. This is firing " + firing + " (" + trigger + ") on " + P.stem + ". The adjudication, the " +
    "gate and the write path have already run. YOUR JOB IS TO LEAVE " + P.summary + " CARRYING EXACTLY " +
    "THE SECTIONS BELOW, IN THIS ORDER, AND NOTHING ELSE.\n\n" +
    "HARD CONSTRAINT. " + CLEANUP_EDITABLE + "\n\n" +
    FILE_MAP + "\n\n" +
    EVIDENCE + "\n\n" +
    STANDING_CONTEXT + "\n\n" +
    "THE SECTION LIST:\n" + SUMMARY_SECTIONS + "\n" +
    RENAME_RULE + "\n\n" +
    PROBLEM_PART_RULE + "\n\n" +
    INDEX_RULE + "\n\n" +
    NO_RETIRED_RULE + "\n\n" +
    RELOCATION_RULE + "\n\n" +
    "WHAT EACH LISTED SECTION HOLDS. `## Open decisions for human to make` carries the decisions still " +
    "open for the human and only those, each stating the question so it can be answered without reading " +
    "the proposal, the recommendation, the ground, the alternatives and why each lost, what deciding " +
    "otherwise costs, a confidence, and the stable identifier it was stamped with. `## Defects in the " +
    "shipped tree that this proposal does not stage` carries one entry per confirmed defect this " +
    "proposal deliberately leaves unstaged, and holds no decisions. `## Impacts on other proposals` " +
    "carries one row per proposal this change bears on.\n\n" +
    "PRESERVE THE IDENTIFIERS. Every entry under `## Open decisions for human to make` keeps the stable " +
    "identifier it carries, verbatim, through any rewrite of the entry: successive firings join on that " +
    "string, and an entry that loses it is adjudicated afresh next time. Do not renumber, normalise or " +
    "improve one, and do not reuse one a withdrawn entry held.\n\n" +
    "THIS IS A FORMAT PASS, NOT A REVIEW. Do not re-adjudicate an item, do not answer an open decision, " +
    "do not add a decision the phase did not reach, and do not soften or sharpen an entry's " +
    "recommendation. Move text and correct a statement your own move falsifies, and change nothing else.\n\n" +
    "WHAT THIS FIRING DID TO EACH ITEM, which is what says whether an entry belongs in the human's " +
    "section:\n" + firingRecapBlock() + "\n\n" +
    LOG_WRITE_RULE + "\n\n" +
    "Follow " + repo + "/.claude/rules/doc-style.md."
  );
}

// The cleanup runs on every firing, whatever the adjudication found: the
// sections it conforms are a property of the file rather than of this firing's
// items, and a summary that has drifted is drifted whether or not anything was
// resolved this time.
async function runSummaryCleanup() {
  const label = "f" + firing + ":cleanup";
  const r = await robustAgent(cleanupPrompt(), { label, schema: CLEANUP_RESULT, phase: "Cleanup" });
  if (!r) {
    recordDead(label);
    log("  " + label + ": the cleanup agent returned nothing after retries; the summary is NOT conformed by this firing");
    return null;
  }
  log(
    "Summary cleanup: " + r.outcome +
      ((r.relocated || []).length > 0 ? ", " + r.relocated.length + " block(s) relocated" : "") +
      ", " + (r.sections || []).length + " section(s)",
  );
  for (const line of r.relocated || []) log("  relocated: " + String(line).slice(0, 200));
  if (r.outcome === "blocked") log("  cleanup BLOCKED: " + (r.note || "no reason given"));
  return r;
}

// ---- What the firing reports about the decisions ---------------------------
//
// The parent's result object carries the findings it fixed and the ones it
// refuted and nothing about decisions, so a run reports two findings fixed
// while carrying a dozen unclosed decisions. These two lists close that.
//
// Each resolved or withdrawn entry names the question, the disposition, the
// citation, and THE AUTHORITY that resolved or withdrew it. A section carrying
// withdrawn and replaced entries side by side, some citing a validation pass
// and some citing nobody at all, renders them identically, and a reader cannot
// tell a falsification-panel withdrawal from a fixer's. A withdrawal naming no
// authority is refused rather than recorded.

// The authority behind one item, built from the phase's own record rather than
// asked of an agent, because an agent asked to name its authority names itself.
// It is the readings that reached the disposition and the falsifier that
// attacked it, and an item that reached neither has none: a disposition no
// falsifier adjudicated is not something this phase may claim to have settled.
function authorityFor(item) {
  const readings = (item.readings || []).length;
  const v = item.falsification;
  if (readings === 0 || item.gate !== "stands" || !v) return "";
  // A carried-forward item was adjudicated by an earlier firing, and the
  // authority is that firing's rather than this one's.
  const when = item.carried
    ? "firing " + item.carried.fromFiring + ", carried forward to firing " + firing
    : "firing " + firing + " (" + trigger + ")";
  return (
    "the open-decisions-and-impact-review phase, " + when + ": " + readings +
    " independent reading(s), " + (item.agreement || "agreement unstated") + ", attacked by this item's " +
    "own falsifier under the `" + item.disposition + "` brief, which reported `" +
    (v.howConclusive || "unstated") + "`"
  );
}

// The ground the disposition rests on, in the readings' own words. Every
// collector states groundQuotes as `file:line — "the sentence"`, so the
// citation travels with the location rather than as a claim about it.
function citationFor(item) {
  const quotes = [];
  for (const r of item.readings || []) {
    for (const q of (r.entry && r.entry.groundQuotes) || []) {
      const s = String(q).slice(0, 300);
      if (!quotes.includes(s)) quotes.push(s);
    }
  }
  return quotes.slice(0, 3);
}

// What the readings say this item's own summary entry needed. Unanimity or
// nothing: a withdrawal one reading of three called for is not a withdrawal
// this phase took, and the disagreement is what leaves the entry listed.
function summaryActionOf(item) {
  const actions = (item.readings || [])
    .map((r) => (r.entry && r.entry.summaryAction) || "")
    .filter(Boolean);
  if (actions.length === 0) return "";
  return actions.every((a) => a === actions[0]) ? actions[0] : "";
}

// Whether this phase takes the item's entry OUT of the human's section without
// answering it. One statement, read by the write path and by the report, so an
// item the Apply stage writes back is never reported as one that left. A
// reading that resolves an item is told the entry then leaves the summary, so a
// resolution the join downgraded to the human carries `withdrawn` on every one
// of its readings; the entry stays listed, with the readings' answers beside it
// as alternatives.
function isWithdrawal(item) {
  if (item.contested || item.disposition !== "human") return false;
  if ((item.readings || []).some((r) => r.disposition === "resolve")) return false;
  const status = (item.apply && item.apply.status) || "";
  if (status === "failed" || status === "recorded") return false;
  return summaryActionOf(item) === "withdrawn";
}

// Which items closed this firing and which are still the human's, built from
// the items themselves once the Apply and cleanup stages have run. It calls no
// agent: every input is a record this firing already holds.
function buildDecisionPayloads() {
  for (const item of items) {
    const authority = authorityFor(item);
    const base = {
      id: item.id,
      question: item.question,
      home: item.home,
      disposition: item.disposition,
      citation: citationFor(item),
      authority,
    };
    const status = (item.apply && item.apply.status) || "";
    // A resolution's own entry leaves the human's section too, so the two are
    // ordered rather than tested independently: an item this phase ANSWERED is
    // resolved, and an entry that left the section without an answer is
    // withdrawn.
    const resolved =
      item.disposition === "resolve" && (status === "applied" || status === "unchanged");
    // A contested item takes neither path: what happened to its entry is the
    // reversal itself, and reading it as a withdrawal this phase took would
    // credit the phase with the loop's edit.
    const withdrawn = !resolved && isWithdrawal(item);
    if (withdrawn && !authority) {
      // Refused. A withdrawal is the one disposition whose whole record is the
      // absence of an entry, so one that names nobody leaves a reader unable to
      // tell it from an entry a fixer dropped. It is not recorded as closed and
      // the decision stays the human's.
      decisionsLeftToHuman.push({
        ...base,
        reason:
          "a withdrawal naming no authority is refused: the item's disposition was not adjudicated by " +
          "this phase's gate, so nothing here settled it",
        gate: item.gate || "none",
      });
      log("  " + item.id + ": withdrawal REFUSED, it names no authority; the decision stays the human's");
      continue;
    }
    if (resolved || withdrawn) {
      decisionsResolved.push({
        ...base,
        kind: resolved ? "resolved" : "withdrawn",
        where: (item.apply && item.apply.claim && item.apply.claim.where) || [],
        wrote: String((item.apply && item.apply.claim && item.apply.claim.wrote) || "").slice(0, 400),
      });
      continue;
    }
    // Still the human's: a decision this phase disposed of as the human's, and
    // one it meant to answer and did not, which the gate set aside, the Apply
    // stage failed, or `lockSpecChanges` put out of reach. An implementor
    // blank, an out-of-scope declaration and an impact row are not decisions
    // for the human and are reported through the item list instead.
    if (item.disposition !== "human" && item.disposition !== "resolve") continue;
    let reason;
    if (item.contested) {
      reason =
        "CONTESTED: this phase applied it at firing " + item.contested.appliedAtFiring +
        " and the review loop reversed it, so it is the human's and is never re-applied";
    } else if (item.carried) {
      reason =
        "carried forward from firing " + item.carried.fromFiring + " untouched: nothing this firing " +
        "collected changed the item, so it was not re-adjudicated";
    } else if (item.disposition === "human") {
      reason =
        (item.alternatives || []).length > 0
          ? "disposed of as the human's, with the readings' own answers recorded as alternatives"
          : "disposed of as the human's";
    } else if (item.gate === "refuted") {
      reason = "meant to be answered and was not: the gate refuted the resolution";
    } else if (item.gate !== "stands") {
      reason = "meant to be answered and was not: the gate did not adjudicate it";
    } else {
      reason =
        "meant to be answered and was not: " +
        ((item.apply && item.apply.reason) || "no answer was staged");
    }
    decisionsLeftToHuman.push({
      ...base,
      gate: item.gate || "none",
      reason,
      alternatives: item.alternatives || [],
    });
  }
  // A reversal usually removes the entry the item was collected from, so a
  // contested record commonly has no item this firing. It is still the human's
  // and is listed from the record itself, with both positions on it.
  for (const rec of Object.values(records())) {
    if (!rec.contested || rec.lastSeen === firing) continue;
    decisionsLeftToHuman.push({
      id: rec.id,
      question: rec.question,
      disposition: "human",
      citation: rec.where || [],
      authority: "",
      gate: "contested",
      reason:
        "CONTESTED: this phase applied it at firing " + rec.contested.appliedAtFiring +
        " and the review loop reversed it; no item was collected for it this firing",
      alternatives: [],
      contested: rec.contested,
    });
  }
  log(
    "Decisions: " + decisionsResolved.length + " resolved or withdrawn, " + decisionsLeftToHuman.length +
      " left to the human",
  );
}

// ---- Sub-task 8: the verify pass -------------------------------------------
//
// One agent, read-only, over what the firing left behind. It reports and fixes
// nothing: the phase files no findings and this pass is the operator's evidence
// that what the other stages wrote is true and conforms, which is a different
// job from writing it.
//
// The checks are the failures this phase's own stages can produce. Each is a
// defect a reader of the finished proposal would not see and a later firing
// would inherit.

const VERIFY_RESULT = {
  type: "object",
  required: ["conforms", "sections", "defects"],
  properties: {
    conforms: {
      type: "boolean",
      description: "true only when `defects` is empty",
    },
    sections: {
      type: "array",
      items: { type: "string" },
      description: "the summary's headings as it now stands, in order, one per entry",
    },
    defects: {
      type: "array",
      items: { type: "string" },
      description: "one entry per defect, in the form the prompt fixes",
    },
    note: { type: "string" },
  },
};

// What the phase says it did, handed over to be checked against the files
// rather than trusted. The verify pass is the only stage that reads the claims
// and the tree together.
function claimsBlock(cleanup) {
  return JSON.stringify(
    {
      cleanup: cleanup || "the cleanup agent returned nothing; the summary was not conformed by this firing",
      decisionsResolved,
      decisionsLeftToHuman,
      applied,
      failedItems,
      recordedForOperator,
    },
    null,
    2,
  ).slice(0, 12000);
}

function verifyPrompt(cleanup) {
  return (
    "You are the verify pass for the open-decisions-and-impact-review phase of a change proposal's " +
    "review. This is firing " + firing + " (" + trigger + ") on " + P.stem + ". Every other stage has " +
    "run and written what it was going to write. YOUR JOB IS TO CHECK THE RESULT for factual accuracy " +
    "and format conformance, and to REPORT what is wrong rather than to fix it.\n\n" +
    COLLECTOR_READ_ONLY + "\n\n" +
    FILE_MAP + "\n\n" +
    EVIDENCE + "\n\n" +
    STANDING_CONTEXT + "\n\n" +
    "WHAT THIS FIRING CHANGED. The proposal was committed before the Apply stage, so `git diff -- " +
    PATHSPEC + "` in " + repo + " is exactly what this firing wrote, and `git show HEAD:<path>` is any " +
    "file as it stood before it. Read that diff first: an entry that left a section and an identifier " +
    "that changed are visible there and nowhere else.\n\n" +
    P.summary + " carries these headings, in this order, and nothing else:\n" + SUMMARY_SECTIONS + "\n" +
    "CHECK EVERY ONE OF THESE AND REPORT EACH THAT FAILS:\n" +
    "  1. `## Deliverable index` is present, is LAST, and is unchanged by this firing. A dropped, " +
    "reordered, shortened or reworded index is a defect: this phase does not maintain it, the " +
    "reconciliation pass between the loops does, and it is the only place a deliverable id resolves.\n" +
    "  2. Every entry under `## Open decisions for human to make` carries the stable identifier it " +
    "carried before, verbatim, including through a rewrite of the entry. A dropped, renumbered or " +
    "reused identifier is a defect: successive firings join on that string, and an entry that loses it " +
    "is adjudicated afresh.\n" +
    "  3. No `### Retired` or equivalent block survives inside `## Open decisions for human to make`. A " +
    "resolved or withdrawn decision leaves that section rather than being parked under a heading inside " +
    "the very section it was supposed to leave.\n" +
    "  4. Every decision that left that section names the authority that resolved or withdrew it, in " +
    "the staged changes or in the review log. A withdrawal naming no authority is a defect: a " +
    "falsification-panel withdrawal and a fixer's render identically otherwise, and a reader cannot tell " +
    "which one settled it.\n" +
    "  5. `**Problem statement.**` is present and still says what the change repairs. An edit that " +
    "restates what the problem IS, widens or narrows its scope, or abandons its framing is a defect: " +
    "that is a reframe, and it belongs to the introspection pass rather than to this phase.\n" +
    "  6. A preamble on `## Open decisions for human to make` that asserts a provenance for the entries " +
    "under it holds against those entries. A sentence saying every entry below was derived and validated " +
    "some number of times is written once, for the entries that existed then, and inherited by every " +
    "entry added afterwards; where the entries do not support it, the claim is a defect and the " +
    "correction it needs is what you report.\n" +
    "  7. Every listed heading is present, in the listed order, and no heading the list does not name " +
    "survives. Content the list does not name was relocated rather than deleted: check the diff for text " +
    "that left the file without arriving anywhere.\n\n" +
    "FACTUAL ACCURACY. Every citation this firing wrote resolves to a real file and line and says what " +
    "the text says it says. Every claim it wrote about the tree holds against the tree. Every row under " +
    "`## Impacts on other proposals` names that proposal's actual status. Open what you check: a " +
    "citation travels between agents without its context, which is the failure this pass exists to " +
    "catch.\n\n" +
    "WHAT THE PHASE SAYS IT DID. Check the files against it rather than trusting it, and a claim the " +
    "files do not carry is itself a defect:\n" + claimsBlock(cleanup) + "\n\n" +
    "REPORT, DO NOT FIX. You edit nothing, the review log included. Write each entry of `defects` as " +
    "`<the check number, or `factual`> — what is wrong — file:line — what is true or required instead`, " +
    "so the operator can act on it without re-deriving it. `conforms` is true only when `defects` is " +
    "empty, and an empty `defects` list you did not earn is worse than a long one."
  );
}

async function runVerify(cleanup) {
  const label = "f" + firing + ":verify";
  const r = await robustAgent(verifyPrompt(cleanup), { label, schema: VERIFY_RESULT, phase: "Verify" });
  if (!r) {
    recordDead(label);
    log("  " + label + ": the verify agent returned nothing after retries; this firing's output is UNVERIFIED");
    return null;
  }
  log(
    r.conforms && (r.defects || []).length === 0
      ? "Verify: no defect found in what this firing wrote"
      : "Verify: " + (r.defects || []).length + " defect(s) in what this firing wrote",
  );
  for (const d of r.defects || []) log("  defect: " + String(d).slice(0, 240));
  return r;
}

// ---- The firing -----------------------------------------------------------

// The budgets this firing read from its arguments, carried into the report so
// an exhausted periodic budget is visible in the result object rather than only
// in the parent's control flow.
const budgets = {
  maxPeriodicFirings,
  periodicFirings: phaseState.periodicFirings || 0,
  periodicBudgetSpent,
  lockSpecChanges,
};

log(
  "Open decisions and impact review, firing " + firing + " (" + trigger + ") on " + P.stem +
    (lockSpecChanges ? ", spec staging LOCKED" : "") +
    (rejected.length > 0 ? ", " + rejected.length + " refuted finding(s) in hand" : ""),
);
if (trigger === "periodic" && periodicBudgetSpent) {
  log(
    "This firing spends the last of maxPeriodicFirings (" + maxPeriodicFirings +
      "); the periodic trigger stops for the rest of the run and every post-loop firing continues.",
  );
}

// Each item's disposition, one entry per collected item. The collectors fill it.
const items = [];
// What the Apply stage wrote, and what it could not.
const applied = [];
const failedItems = [];
// Items an Apply could not write because the resolution needs a file this phase
// may not edit, which under `lockSpecChanges` is the staged spec edits. Not a
// failure: the resolution is recorded for the operator rather than staged.
const recordedForOperator = [];
// What the phase resolved or withdrew, and what it leaves for the human, for the
// parent's result object. A run that reports two findings fixed while carrying
// dozens of unclosed decisions is the gap these close.
const decisionsResolved = [];
const decisionsLeftToHuman = [];

// Four collectors, each a separate brief over a different population: the
// decisions left to the human, the blanks left to the implementor, the defects
// declared out of scope, and what this proposal does to the others on disk.
phase("Collect");
// Before anything is adjudicated: each item an earlier firing applied is
// checked against the tree, and one whose text is gone is marked CONTESTED
// rather than argued again.
await checkReversals();
const corpus = await corpusInventory();
// Sequential rather than parallel: sub-task 1 already fans out three agents of
// its own, and the four populations are collected in the order the phase reads
// them so a log of one firing reads as one pass over the proposal.
items.push(...(await collectHumanDecisions()));
items.push(
  ...(await collectSingle({
    key: "out-of-scope-defects",
    title: "Sub-task 3 (out-of-scope defect declarations)",
    prompt: outOfScopeDefectsBrief(),
    allowed: ["out-of-scope-stands", "out-of-scope-wrong"],
    fallback: "out-of-scope-stands",
  })),
);
items.push(
  ...(await collectSingle({
    key: "other-proposals",
    title: "Sub-task 4 (impacts on other proposals)",
    prompt: otherProposalsBrief(corpus),
    allowed: ["impact-row"],
    fallback: "impact-row",
  })),
);
// ---- Dedup, across every sub-task rather than within one ------------------
//
// Sub-task 1's join already dedupes its own three readings of one home. Nothing
// deduped ACROSS sub-tasks, and a measured firing showed what that costs: one
// question reached two falsifiers by two routes and came back with opposite
// verdicts, and two more markers were adjudicated twice under different homes.
// Two items are the same question when their questions normalise alike; the
// first survives and carries the other's homes, so the report still says
// everywhere it was found.
function questionKey(item) {
  return String(item.question || "")
    .toLowerCase()
    .replace(/`[^`]*`/g, " ")
    .replace(/[^a-z0-9]+/g, " ")
    .trim()
    .split(" ")
    .slice(0, 18)
    .join(" ");
}
{
  const seen = new Map();
  const merged = [];
  const dropped = [];
  for (const it of items) {
    const k = questionKey(it);
    // A question too short to normalise into anything is never merged: an empty
    // key would collapse every such item onto one.
    if (!k || k.length < 12) {
      merged.push(it);
      continue;
    }
    const first = seen.get(k);
    if (!first) {
      seen.set(k, it);
      merged.push(it);
      continue;
    }
    if (!first.alsoFoundIn) first.alsoFoundIn = [];
    if (it.home && it.home !== first.home && !first.alsoFoundIn.includes(it.home)) {
      first.alsoFoundIn.push(it.home);
    }
    dropped.push({ id: it.id, home: it.home, kept: first.id, keptHome: first.home });
  }
  if (dropped.length > 0) {
    items.length = 0;
    items.push(...merged);
    log(
      "Deduped " + dropped.length + " item(s) that restate a question another sub-task already holds: " +
        dropped.map((d) => d.id + " (" + d.home + ") -> " + d.kept + " (" + d.keptHome + ")").join("; "),
    );
  }
}

const tally = DISPOSITIONS.map(
  (d) => items.filter((i) => i.disposition === d).length + " " + d,
).filter((t) => !t.startsWith("0 "));
log(
  "Collected " + items.length + " item(s)" +
    (tally.length > 0 ? ": " + tally.join(", ") : "") +
    (unadjudicated.length > 0 ? "; UNADJUDICATED: " + unadjudicated.join(", ") : ""),
);

// Each collected item against its record from the earlier firings: a match
// carries the earlier disposition forward, a match on a contested record routes
// the item to the human, and an item matching nothing is adjudicated afresh.
const matched = matchToRecords(items);
// Built once, before anything can abort, so both exits report the same records:
// the match against the prior firings' records runs ahead of the commit.
const carriedForward = matched.carried.map((item) => ({
  id: item.id,
  disposition: item.disposition,
  fromFiring: item.carried.fromFiring,
  applyStatus: item.carried.applyStatus,
}));

// The phase's gate. One falsifier per item, holding only that item and briefed
// to refute its disposition rather than confirm it. Only the items adjudicated
// afresh reach it: a carried-forward item was gated by the firing that disposed
// of it, and re-gating it is the re-argument this phase does not make.
phase("Falsify");
// What reaches Apply, decided here rather than there. Which of these needs an
// edit is Apply's own question, answered from the disposition table above: a
// surviving `implementor` leaves the blank exactly as the proposal wrote it.
const survivors = [
  ...(await falsifyAll(matched.fresh)),
  ...matched.carried.filter((item) => item.survives),
];

// The write path: the baseline commit, then one agent per surviving item that
// needs an edit, run sequentially because they edit the same files.
phase("Apply");
const commit = await commitBaseline();
if (commit.outcome === "failed") {
  // A firing that cannot take its baseline has no way to tell what it wrote
  // from what was already there, so its Apply stage would author onto an
  // unusable baseline and its report of what it changed would be unfounded.
  // It stops and says why rather than proceeding.
  log("Baseline commit FAILED: " + (commit.error || "no reason given") + "; the firing does not run");
  return {
    status: "aborted",
    abortReason: "the baseline commit failed: " + (commit.error || "no reason given"),
    firing,
    trigger,
    phaseState,
    budgets,
    items,
    applied,
    failedItems,
    recordedForOperator,
    decisionsResolved,
    decisionsLeftToHuman,
    // The cross-firing record as this firing left it. Nothing new is written to
    // it on an abort, so what it carries is the earlier firings' dispositions
    // plus any reversal this firing's check found before it stopped.
    contested: contestedReport(),
    carriedForward,
    corpusSize: corpus.length,
    outsideProposal: commit.outsideProposal || [],
    periodicBudgetSpent,
    unadjudicated,
    deadAgents,
  };
}
log(
  commit.outcome === "empty"
    ? "Baseline: nothing to commit under " + PATHSPEC + "; HEAD is the baseline"
    : "Baseline committed" + (commit.sha ? " at " + commit.sha : ""),
);
if ((commit.outsideProposal || []).length > 0) {
  log(
    "Left uncommitted, outside the proposal and not this run's to touch: " +
      commit.outsideProposal.join(", "),
  );
}

// One agent per surviving item that needs an edit, each holding one resolution
// and writing it, run one after another because they edit the same files.
await applyAll(survivors);

// The summary is rewritten to the listed sections, in order, and anything the
// list does not name is relocated to where it belongs. It runs after Apply
// because what belongs in each section depends on what Apply resolved.
phase("Cleanup");
const cleanup = await runSummaryCleanup();

// What this firing closed and what it leaves for the human, built from its own
// record before the verify pass reads the files against it.
buildDecisionPayloads();

// This firing's outcome onto the per-item records the next firing reads, and
// the prior records nothing matched this time.
const unmatchedRecords = recordItems();

// A read-only pass over the result: factual accuracy and format conformance.
phase("Verify");
const verification = await runVerify(cleanup);

// What the firing changed, read from the tree rather than from the agents. It
// is the delta against the baseline this firing committed above, so it covers
// every stage between the two.
const changed = await treeDelta("firing");

// Everything either reading found outside the proposal. The two are unioned
// rather than the later one winning: a path another actor touched before the
// baseline and reverted afterwards is still a path this run left alone and said
// so about, and the commit's reading is the only record of it.
const outsideProposal = [...(commit.outsideProposal || [])];
for (const f of (changed && changed.outsideProposal) || []) {
  if (!outsideProposal.includes(f)) outsideProposal.push(f);
}

log(
  "Firing " + firing + " done: " + items.length + " item(s) adjudicated, " + survivors.length +
    " through the gate (" + matched.carried.length + " carried forward, " + matched.contestedItems.length +
    " contested), " + applied.length + " applied, " + failedItems.length + " failed" +
    (recordedForOperator.length > 0 ? ", " + recordedForOperator.length + " recorded for the operator" : "") +
    ", " + decisionsResolved.length + " decision(s) resolved or withdrawn, " + decisionsLeftToHuman.length +
    " left to the human, " + (changed ? changed.files.length + " file(s) changed" : "change detection unavailable"),
);

return {
  status: "done",
  firing,
  trigger,
  // The cross-firing state, handed back so the parent can hand it to the next
  // firing: the per-item records, the corpus inventory, and the firing counter.
  phaseState,
  budgets,
  items,
  applied,
  failedItems,
  // Resolutions the phase gated and could not stage, because the file they land
  // in is out of bounds for this run. Recorded for the operator rather than
  // dropped.
  recordedForOperator,
  decisionsResolved,
  decisionsLeftToHuman,
  // What the cross-firing record says about this firing: the items a reversal
  // contested, the items carried forward untouched, and the prior records no
  // collected item matched, each under its own prior identifier.
  contested: contestedReport(),
  carriedForward,
  unmatchedRecords,
  // What the cleanup left the summary carrying, and what the verify pass found
  // in it. Null on either means that stage's agent died, which is a different
  // answer from a clean pass: the firing's output is unconformed or unverified
  // rather than conformant.
  summaryCleanup: cleanup,
  verification,
  corpusSize: corpus.length,
  // Built from the tree rather than from the agents. Null means the change
  // detection itself died, which is a different answer from an empty list.
  changedFiles: changed ? changed.files : null,
  outsideProposal,
  baselineCommit: commit.sha || "",
  periodicBudgetSpent,
  // Populations a dead collector left unadjudicated. Empty is the ordinary
  // answer, and a name in it means this firing is silent about that population
  // rather than clear of it.
  unadjudicated,
  deadAgents,
};
