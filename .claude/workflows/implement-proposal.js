// Implement an approved spec proposal end to end: apply its staged spec
// edits to spec/ and verify them, then (optionally) implement the spec
// change in code and close the findings that reference it. This unifies
// the former spec-apply (land + verify spec) and spec-implement (plan +
// build code + close findings) into one entry point.
//
//   Workflow({ name: "implement-proposal", args: {
//     proposalPath: "proposals/NNNN_*.md",  // required
//     date: "YYYY-MM-DD",                    // required (scripts cannot call Date)
//     repoRoot: "/abs/path",                 // required: the repository root
//     implementCode: true,                   // optional, default true; false = land + verify spec only
//     maxApplyRounds: 5,                      // optional; spec apply-verify-fix rounds
//   }})
//
// Spec always comes first: the staged spec edits are applied and verified
// before any code, one sub-step at a time. Each sub-step is applied, verified
// to clean, and committed before the next begins, so a defect is attributable
// to the sub-step that introduced it and a bad sub-step is revertable without
// discarding the ones that already verified clean. An edit whose anchor cannot
// be located stops the run rather than being skipped, because a partially
// applied file makes every other discrepancy ambiguous. With implementCode false the run stops after the spec
// is landed and committed (the former spec-apply behavior). The code phase
// is the implement-proposal-build subworkflow (blast radius + ordered
// build sequence + step-by-step implementation with tests).
//
// Preconditions (the skill checks them before invoking): the proposal
// Status is "Approved" (or already "Applied to spec" for a re-run), and
// spec/ is clean in git so the apply verification can diff against a clean
// baseline.
//
// MAINTENANCE: the implement-proposal skill documents this workflow and its
// subworkflow; keep them in sync.

export const meta = {
  name: "implement-proposal",
  description:
    "Apply an approved proposal's spec edits and verify them, then optionally implement the spec change in code and close the findings that reference it",
  phases: [
    { title: "Plan", detail: "read the proposal, gate on approval, extract staged spec edits and findings" },
    { title: "Apply spec", detail: "land the staged spec edits and verify exact alignment until clean" },
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
const maxApplyRounds = input.maxApplyRounds || 5;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const relProposal = input.proposalPath.startsWith("/")
  ? input.proposalPath.replace(repo + "/", "")
  : input.proposalPath;
// Where this proposal's parts live. Folder layout or legacy single file; every
// prompt below names a role through this rather than concatenating a path.
const P = proposalFiles(input.proposalPath, repo);

// ---- The spec/ write lease -----------------------------------------------
//
// spec/ is read-only unless a lease is open (.claude/tools/spec-lease.mjs and
// the PreToolUse hook in settings.json). The lease names THIS proposal and the
// files it is allowed to write, and it expires. Nothing in this phase can edit
// spec/ without it, which is the point: an agent that wanders into spec/ is
// refused rather than trusted.
//
// A workflow script has no filesystem access, so opening and releasing are
// agent calls. They are DEDICATED agents given one exact command, not an
// instruction appended to an agent that has other work: the lease is the
// security boundary here, its failure is not benign, and an agent with a large
// task in front of it is not a reliable executor of a small instruction at the
// end of its prompt.
const leaseTtlHours = input.leaseTtlHours || 24;
let leaseOpen = false;

async function openLease(step, allowFiles) {
  const allow = (allowFiles || []).join(",");
  const out = await agent(
    "Run exactly this command and reply with its stdout and nothing else:\n\n" +
      "node " + repo + "/.claude/tools/spec-lease.mjs open '" + P.root + "'" +
      " --step '" + (step || "apply") + "'" +
      " --ttl-hours " + leaseTtlHours +
      (allow ? " --allow '" + allow + "'" : "") +
      "\n\nDo nothing else. Do not read, summarise, or edit any file.",
    { label: "lease-open:" + (step || "apply"), model: "haiku", phase: "Apply spec" },
  );
  leaseOpen = true;
  return out;
}

// Called on EVERY return path and at the end of every spec step, whether it
// succeeded or failed. A release that does not happen leaves spec/ writable,
// so this is never folded into an agent that has other work to do.
async function releaseLease(step) {
  if (!leaseOpen) return;
  leaseOpen = false;
  await agent(
    "Run exactly this command and reply with its stdout and nothing else:\n\n" +
      "node " + repo + "/.claude/tools/spec-lease.mjs release" +
      (step ? " --step '" + step + "'" : "") +
      "\n\nDo nothing else. Do not read, summarise, or edit any file.",
    { label: "lease-release:" + (step || "apply"), model: "haiku", phase: "Apply spec" },
  );
}

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

const SPEC_RULES =
  "Spec content rules (these take precedence over verbatim application; record every deviation they force):\n" +
  "- The spec never references source code files or implementation paths (pkg/, cmd/, charts/, sdks/, tests/, migrations/, .go or other source files). Rephrase staged text carrying such a reference into behavioral spec language, or drop the reference.\n" +
  "- The spec cross-references other spec content by section number only: §X.Y or a relative markdown link to a section anchor. Replace a line-number cross-reference in staged text with the containing section's number.\n" +
  "- Line numbers in the proposal's ANCHOR INSTRUCTIONS are location hints for you and never become spec content. Locate anchors by the quoted text and section headings; line numbers drift.\n" +
  "- A staged edit that introduces a brand-new section or subsection is appended at the end of its level, after the last existing sibling at that level, and numbered as the next ordinal. Never insert a new section or subsection between existing ones: inserting in the middle forces every following section to be renumbered and breaks existing cross-references. When a staged anchor instruction would place a new section or subsection between existing ones, append it at the end of that level instead, renumber it to the next ordinal, and record the deviation. Editing the body of an existing section in place is unaffected by this rule; it applies only to introducing a new numbered section or subsection.\n" +
  "- Apply staged prose as written otherwise; do not restyle it.\n" +
  "- These rules govern text you author from a staged block. For a mechanical edit you do not author the text: if the script's output violates one of them, that is a defect in the script or its register, so record it as a deviation and stop, rather than hand-correcting the output, which would put the tree and the register out of step.";

// The proposal's Summary orients the spec-apply agents the same way it orients the
// build agents: what changes, which decisions are closed, and the traps. An
// applier that knows a decision is closed does not relitigate it in a sub-step.
// The script cannot read the proposal: the workflow sandbox has no `require` and
// no filesystem access, so the agent reads its own Summary.
const SUMMARY_BLOCK =
  "\n\nTHE PROPOSAL'S SUMMARY. Read " +
  roleRef(P, "summary", "## Summary") +
  " before you start. It states the top-level changes, the decisions that are closed and must not be " +
  "reopened, and the traps this change has already fallen into. A proposal written before that section " +
  "existed may not have one; when it is absent, read the Problem and Decisions sections in its place.\n";
const BLANKS_BLOCK =
  "\n\nA proposal may delegate a detail with an explicit **IMPLEMENTOR'S CHOICE:** marker naming what is open " +
  "and the constraint any answer must satisfy. That is a delegation rather than an unappliable edit: make the " +
  "choice, satisfy the constraint, and record it in your result. An UNMARKED gap in a staged edit is still " +
  "unappliable and still stops the sub-step.\n";

const PLAN = {
  type: "object",
  required: ["approved", "alreadyApplied", "statusLine", "specEdits", "nonSpecStaged", "findingIds"],
  properties: {
    approved: { type: "boolean", description: 'Status bullet begins "Approved"' },
    alreadyApplied: { type: "boolean", description: 'Status bullet begins "Applied to spec"' },
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

const APPLY_RESULT = {
  type: "object",
  required: ["applied", "unappliable", "deviations"],
  properties: {
    applied: { type: "array", items: { type: "string" } },
    unappliable: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "reason"],
        properties: { id: { type: "string" }, reason: { type: "string" } },
      },
    },
    deviations: {
      type: "array",
      items: {
        type: "object",
        required: ["id", "rule", "original", "replacement"],
        properties: {
          id: { type: "string" },
          rule: { type: "string" },
          original: { type: "string" },
          replacement: { type: "string" },
        },
      },
    },
  },
};

const DISCREPANCIES = {
  type: "object",
  required: ["discrepancies"],
  properties: {
    discrepancies: {
      type: "array",
      items: {
        type: "object",
        required: ["title", "file", "where", "expected", "observed", "fix"],
        properties: {
          title: { type: "string" },
          file: { type: "string" },
          where: { type: "string" },
          expected: { type: "string", description: "What the proposal stages, quoted exactly" },
          observed: { type: "string", description: "What the spec now says, quoted exactly" },
          fix: { type: "string" },
        },
      },
    },
  },
};

const ALIGNMENT = {
  type: "object",
  required: ["aligned", "missing"],
  properties: {
    aligned: { type: "boolean", description: "every staged spec edit is present at its anchor in spec/" },
    missing: { type: "array", items: { type: "string" } },
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
    '.\n\nReturn:\n- approved: true when the status tool prints exactly "Approved".\n- alreadyApplied: true when the staged spec edits are ALREADY PRESENT in spec/. Check a sample of them rather than inferring it from the status: spec application is recorded per deliverable in the implementation checklist now, not as a status value, so an Approved proposal may have some, none, or all of its spec edits landed.\n- statusLine: what the status tool printed.\n- specEdits: one entry per staged change whose target file is under spec/, from the "Proposed spec changes" section: id (the subsection number, e.g. "7.1"), targetFile (the spec/ path), subsection (the heading), summary. A subsection targeting multiple spec files becomes one entry per file. Classify each entry\'s method. Use "mechanical" when the proposal stages the edit as a run of a script, pass, or generator over a register or map rather than as literal text to write, which a proposal signals by enumerating no edit sites, by naming a command, or by stating that completeness is proven by a gate rather than by review; put the exact command in command, including its dry-run form when the proposal states one. Use "authored" when the proposal stages the literal text together with an anchor for it. When one subsection stages both, split it into one mechanical entry and one authored entry. Defaulting to "authored" for an edit the proposal means a script to make is a defect: it sets an agent guessing at sites the proposal deliberately does not list.\n- nonSpecStaged: one entry per staged change whose target is outside spec/ (code, charts, docs, schemas). These are implemented in the code phase or reported, never hand-applied here.\n- findingIds: every OPEN finding in BUILD-GAPS.md whose body references this proposal by path or number (the file is large; grep for the proposal number and filename). Empty array if none.',
  { schema: PLAN, label: "plan", phase: "Plan" },
);

if (!plan.approved && !plan.alreadyApplied) {
  await releaseLease();
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

// ---- Apply spec (or, on a re-run, confirm it is already aligned) ----

// How many times a drifted spec is repaired before the run gives up and asks
// for a human. Small: the verifier names what is missing, so a repair that
// does not close the gap is not going to close it on the fifth try either.
// Areas the caller already suspects the spec review will find something in.
// Each round gains one reviewer that hunts these specifically, running beside
// the normal per-file verifiers rather than replacing them. Its findings are
// concatenated onto theirs with no dedup and no merge step: two phrasings of
// one defect reaching the fixer is cheap, and any step that can drop a finding
// on this path is not worth the fidelity it costs.
const specReviewFocus = (
  Array.isArray(input.specReviewFocus)
    ? input.specReviewFocus
    : input.specReviewFocus
      ? [input.specReviewFocus]
      : []
)
  .map((a) => String(a).trim())
  .filter(Boolean);
if (specReviewFocus.length > 0) {
  log("Spec review focus: " + specReviewFocus.length + " area(s) get a dedicated reviewer each round");
}
const FOCUS_BLOCK =
  specReviewFocus.length === 0
    ? ""
    : "\n\nAREAS TO CONCENTRATE ON. The caller has reason to believe the spec drifted from the proposal in " +
      "these places specifically:\n" +
      specReviewFocus.map((a, i) => i + 1 + ". " + a).join("\n") +
      "\n\nGo after each one directly and exhaustively: find every site it covers, including sites outside " +
      "the anchors the proposal names, and check each against what the proposal stages. These areas are where " +
      "to look hardest, not the limit of what to report; a discrepancy you find elsewhere is still a " +
      "discrepancy. Reporting nothing for an area is a valid answer when the area is genuinely clean.";

const maxAlignRepairs = input.maxAlignRepairs || 3;
let specStatus = "applied"; // applied | applied-with-blockers | not-clean | aligned | no-spec-edits
let unappliable = [];
let deviations = [];
let appliedIds = new Set();
let applyHistory = [];

if (plan.specEdits.length === 0) {
  specStatus = "no-spec-edits";
  log("Proposal stages no spec edits; nothing to land");
} else if (plan.alreadyApplied) {
  // Idempotent re-run: the spec was already landed and committed, so a
  // diff-based check would be empty. Confirm by presence instead.
  phase("Apply spec");
  await openLease("verify-aligned", files);
  log("Status is Applied to spec; verifying the staged edits are present");
  // The presence check, plus a focused reviewer when the caller named areas.
  // Both return the same shape, and their `missing` lists are concatenated
  // verbatim: no dedup, so a site both of them name is landed once by the
  // repair agent and reported twice, which is the harmless direction.
  const checkAligned = async (tag) => {
    const base =
      "Confirm an already-applied proposal's staged spec edits are present in spec/.\n\n" +
      "You are a read-only verifier; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      ". For each staged edit in its 'Proposed spec changes' section, read the named spec/ file and confirm the staged block is present at its anchor. Set aligned true only when every staged edit is present; list any missing ones.";
    const runs = [() => agent(base, { schema: ALIGNMENT, label: "verify-aligned" + tag, phase: "Apply spec" })];
    if (specReviewFocus.length > 0) {
      runs.push(() =>
        agent(base + FOCUS_BLOCK, {
          schema: ALIGNMENT,
          label: "verify-aligned:focus" + tag,
          phase: "Apply spec",
        }),
      );
    }
    const out = (await parallel(runs)).filter(Boolean);
    if (out.length === 0) return null;
    const missing = out.flatMap((r) => r.missing || []);
    // Fail closed: a reviewer that did not return is not evidence of alignment.
    return { aligned: out.length === runs.length && missing.length === 0, missing };
  };
  let align = await checkAligned("");
  // Drift is repaired, not reported. A run that stops here leaves the tree in
  // the worst available state: the status says Applied to spec, most of the
  // edits are in, and the few that are missing are named precisely enough to
  // land. Refusing to land them means the next run re-derives the same list and
  // refuses again, so the only way forward is a hand edit, which is exactly the
  // unreviewed spec change the pipeline exists to prevent. The verifier names
  // each missing edit with its anchor, so the repair is targeted rather than a
  // re-application of the whole proposal, and it re-verifies afterwards: a
  // repair that does not close the gap is still a refusal, just an informed one.
  let alignRepairs = 0;
  while ((!align || !align.aligned) && alignRepairs < maxAlignRepairs) {
    alignRepairs++;
    const missing = (align && align.missing) || [];
    log(
      "Spec drifted from the proposal: " + missing.length +
        " staged edit(s) missing. Landing them (repair " + alignRepairs + "/" + maxAlignRepairs + ")",
    );
    const repair = await agent(
      "Land the staged spec edits of an approved proposal that a previous run left unapplied. Work in " +
        repo +
        ".\n\nProposal: " +
        proposal +
        "\n\nThe rest of this proposal is already applied. A presence check just found ONLY these staged " +
        "edits missing from spec/, each named with the file and anchor the proposal stages it at:\n\n" +
        missing.map((m, i) => i + 1 + ". " + m).join("\n") +
        "\n\nApply exactly these and nothing else. Read the proposal's staged block for each one and write " +
        "what it stages, at the anchor it names. Locate every anchor by its quoted text and its heading " +
        "rather than by a line number, because the surrounding file has changed since the proposal was " +
        "written and the numbers have drifted. Where the same rename has already landed at other sites, " +
        "match how those sites read: a straggler is spelled the way its siblings are, not in a new way.\n\n" +
        "Do not re-apply an edit that is already present, do not touch a spec/ site the list does not name, " +
        "and do not edit the proposal. Commit on the current branch when you are done.\n\n" +
        SPEC_RULES + SUMMARY_BLOCK + BLANKS_BLOCK +
        "\n\nReport what you applied, anything you could not apply and why, and any deviation you had to " +
        "take from the staged text to satisfy the rules above.",
      { schema: APPLY_RESULT, label: "apply-missing:r" + alignRepairs, phase: "Apply spec" },
    );
    if (repair) {
      appliedIds = new Set([...appliedIds, ...(repair.applied || [])]);
      unappliable = unappliable.concat(repair.unappliable || []);
      deviations = deviations.concat(repair.deviations || []);
      applyHistory.push({ repair: alignRepairs, ...repair });
    }
    align = await checkAligned(":r" + alignRepairs);
  }
  if (!align || !align.aligned) {
    await releaseLease();
    return {
      status: "not-aligned",
      statusLine: plan.statusLine,
      reason:
        "the proposal reads Applied to spec and " + alignRepairs +
        " repair pass(es) did not close the gap. Still missing: " +
        ((align && align.missing) || []).join("; ") +
        ". This needs a human: the staged text and the spec have diverged in a way the applier cannot resolve.",
      unappliable,
      deviations,
    };
  }
  if (alignRepairs > 0) log("Spec drift repaired in " + alignRepairs + " pass(es); the staged edits are all present");
  specStatus = "aligned";
} else {
  // Fresh apply: the proposal is Approved and spec/ is a clean baseline.
  phase("Apply spec");
  await openLease("apply", files);
  log(
    plan.specEdits.length +
      " staged spec edits across " +
      files.length +
      " files; applying",
  );

  // Apply sub-step by sub-step rather than file by file across the whole proposal.
  // A large proposal stages its edits as an ordered sequence of sub-steps, each with
  // its own exit criteria and its own gates that go green at its exit, and later
  // sub-steps consume what earlier ones produce. Applying every file at once
  // discards that order: a defect introduced by the first sub-step surfaces only
  // after the last one has been applied on top of it, and the verification loop then
  // sees one undifferentiated tree in which it cannot tell which sub-step is wrong.
  // Per sub-step the tree is verified and committed before the next begins, so a
  // defect is attributable to the sub-step that introduced it and a bad sub-step is
  // revertable without discarding the ones that already verified clean.
  const substeps = [];
  for (const e of plan.specEdits) {
    if (!substeps.includes(e.subsection)) substeps.push(e.subsection);
  }
  log(
    plan.specEdits.length +
      " staged spec edits across " +
      files.length +
      " files in " +
      substeps.length +
      " sub-step(s); applying one sub-step at a time",
  );

  // Declared outside the loop because the post-loop specStatus line reads it.
  // `let` is block-scoped, so declaring it per sub-step left the read after the
  // loop referencing an undeclared binding and every run threw a ReferenceError.
  // Its value after the loop is the last sub-step's, which is the one that
  // decides the phase: an earlier sub-step that did not converge returns from
  // inside the loop and never reaches the read.
  let clean = false;

  for (const ss of substeps) {
    const ssEdits = plan.specEdits.filter((e) => e.subsection === ss);
    // An authored edit writes one file and several can run at once. A mechanical
    // edit is a script run whose file set is decided by its register, not by the
    // caller, so it belongs to the SUB-STEP rather than to any one file: running
    // it inside a per-file agent asks that agent to write files it is forbidden
    // to touch, and runs it concurrently with the sibling agents still editing
    // those same files, so the script would rewrite them from a mid-flight
    // state. Authored edits therefore fan out first and settle, and the
    // mechanical edits run afterwards, one at a time, at sub-step level.
    const ssAuthored = ssEdits.filter((e) => e.method !== "mechanical");
    const ssMechanical = ssEdits.filter((e) => e.method === "mechanical");
    const ssFiles = [...new Set(ssEdits.map((e) => e.targetFile))];
    const ssAuthoredFiles = [...new Set(ssAuthored.map((e) => e.targetFile))];
    log(
      "Sub-step " + ss + ": " + ssAuthored.length + " authored edit(s) across " +
        ssAuthoredFiles.length + " file(s), then " + ssMechanical.length + " mechanical",
    );
    const applyResults = (
      await parallel(
        ssAuthoredFiles.map((f) => () => {
          const edits = ssAuthored.filter((e) => e.targetFile === f);
          return agent(
            "Apply staged spec edits from an approved proposal to one spec file.\n\n" +
              "HARD CONSTRAINT: the only file you may edit is " +
              repo +
              "/" +
              f +
              ". Never modify the proposal or any other file.\n\n" +
              "Proposal: " +
              proposal +
              " (read the whole 'Proposed spec changes' section first for context).\n" +
              "Edits to apply to this file, in order:\n" +
              JSON.stringify(edits, null, 2) +
              "\n\nEdits carry a method and are handled differently.\n" +
              "AUTHORED edits: read the proposal subsection, locate the anchor in the target file by its quoted text and section heading, and apply the staged text exactly as written (fenced blocks verbatim; replacement instructions replace exactly the text they name).\n" +
              "MECHANICAL edits: the proposal stages a script run rather than text, and deliberately enumerates no edit sites. Run the command the edit names. Do NOT hand-apply, hand-reproduce, or hand-correct what the script would write: the script resolves each site from a register and fails closed on a site the register does not carry, and hand-editing substitutes a guess for that guarantee, which is the failure this branch exists to prevent. Before applying, run the command's dry-run form when it has one, read its output, and confirm it touches only files this sub-step targets; then apply, and confirm the applied diff for this file matches what the dry run predicted. If the script exits non-zero, or the applied diff does not match the dry run, or the command is absent from the tree, record the edit as unappliable with that reason and STOP; never fall back to editing by hand.\n" +
              SPEC_RULES + SUMMARY_BLOCK + BLANKS_BLOCK +
              "\n\nIf an anchor cannot be located with certainty, STOP: record that edit as unappliable with the reason, apply NOTHING FURTHER in this file, and return what you applied up to that point. Never guess a location, and never skip an edit in order to continue with the ones after it. Skipping leaves a file in which a later discrepancy cannot be told apart from an edit that never ran, and that is the state the verification loop cannot converge out of; a clean stop at the first unappliable edit is diagnosable, a partially applied file is not. Return the applied edit ids, the unappliable edits, and every rule-forced deviation.",
            { schema: APPLY_RESULT, label: "apply:" + ss + ":" + f.split("/").pop(), phase: "Apply spec" },
          );
        }),
      )
    ).filter(Boolean);

    // The authored fan-out has settled. Now run this sub-step's mechanical edits
    // sequentially, each as its own agent at sub-step level, permitted to write
    // whatever its script's register decides rather than one named file.
    const mechResults = [];
    for (const me of ssMechanical) {
      const r = await agent(
        "Apply ONE mechanical spec edit of an approved proposal by running the script it stages.\n\n" +
          "Work in " + repo + ". Proposal: " + proposal + ".\n" +
          "Sub-step: " + ss + "\nEdit: " + JSON.stringify(me, null, 2) +
          "\n\nThis edit is MECHANICAL: the proposal stages a script run over a register and " +
          "deliberately enumerates no edit sites, so the script decides which files change. Every " +
          "authored edit of this sub-step has already been applied and the tree has settled, so you " +
          "are the only writer now.\n\n" +
          "You MAY write any file the script writes; you may NOT hand-write, hand-reproduce, or " +
          "hand-correct what it would write. The script resolves each site from its register and " +
          "fails closed on a site the register does not carry, and a hand edit substitutes a guess " +
          "for that guarantee.\n\n" +
          "Procedure. Read the sub-step's Change paragraph for the command lines it states. Run the " +
          "dry-run form first, read its file list, and confirm it is the set the proposal says this " +
          "run writes. Then run the apply form. Then confirm the applied diff matches what the dry " +
          "run predicted. If the script exits non-zero, if the applied diff does not match the dry " +
          "run, or if the command is absent from the tree, record the edit as unappliable with that " +
          "reason and STOP; never fall back to editing by hand. An empty diff where the proposal " +
          "says the run writes files is a failure, not a pass.\n\n" + SPEC_RULES,
        { schema: APPLY_RESULT, label: "apply:" + ss + ":mech:" + me.id, phase: "Apply spec" },
      );
      if (r) mechResults.push(r);
    }
    const allResults = applyResults.concat(mechResults);

    unappliable = allResults.flatMap((r) => r.unappliable);
    deviations = deviations.concat(allResults.flatMap((r) => r.deviations));
    for (const id of allResults.flatMap((r) => r.applied)) appliedIds.add(id);
    if (deviations.length > 0) log(deviations.length + " rule-forced deviations recorded");

    // Stop on unappliable rather than verifying a partial tree. An edit that could
    // not be located means the proposal and the tree disagree about something the
    // proposal was responsible for stating, and no number of verify-and-fix rounds
    // resolves that: the fixer can edit spec/ but not the proposal, so the same
    // discrepancy returns every round. Worse, a partially applied tree makes every
    // OTHER discrepancy ambiguous, because a reviewer cannot tell "the applied text
    // is wrong" from "that edit never ran", which is what made both earlier
    // applications of a large proposal oscillate instead of converge. Returning here
    // leaves the applied prefix in the working tree for inspection and names exactly
    // what blocked, which is the actionable report.
    if (unappliable.length > 0) {
      log(
        unappliable.length +
          " edit(s) unappliable; stopping before verification rather than verifying a partial tree",
      );
      await releaseLease();
      return {
        status: "spec-unappliable",
        reason:
          "an edit could not be located with certainty, so application stopped at that edit rather than skipping it. " +
          "The proposal must state the missing anchor, title, or value before it can be applied. The partially applied " +
          "edits are in the working tree for inspection; revert spec/ before re-running.",
        unappliable,
        applied: [...appliedIds],
        deviations,
      };
    }

    const verifiableEdits = ssEdits.filter((e) => appliedIds.has(e.id));
    const DEVIATION_NOTE =
      deviations.length > 0
        ? "\n\nRecorded rule-forced deviations (EXPECTED differences from the staged text; do not report them as discrepancies):\n" +
          JSON.stringify(deviations, null, 2)
        : "";

    const verifyFilePrompt = (f, edits, round) =>
      "You verify that applied spec edits align exactly with the proposal that staged them. Round " +
      round +
      ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      ". Target file: " +
      f +
      ". Edits expected in this file:\n" +
      JSON.stringify(edits, null, 2) +
      "\n\nMethod: read each proposal subsection; read the current target file; run `git diff -- " +
      f +
      "` to see exactly what changed against the clean baseline. Verify all of:\n" +
      "1. Every staged block appears at its anchored location, character-exact (modulo the recorded deviations below).\n" +
    "   This check applies to AUTHORED edits only. A MECHANICAL edit stages no block and no anchor, because the proposal stages it as a script run over a register and enumerates no edit sites. For one of those, verify instead that the command ran, that the diff contains only sites the edit's register carries, and that the gate the sub-step names as its exit criterion is green. A mechanical edit whose diff is empty is a failure rather than a pass, since the pass either did not run or matched nothing.\n" +
      "2. Text the proposal replaces or removes is gone, and nothing it keeps was altered.\n" +
      "3. The diff for this file contains nothing beyond the staged edits: no stray edits, no duplicate insertions, no truncated surroundings.\n" +
      "4. Every cross-reference the applied text adds resolves: a §X.Y number names an existing section, and a relative markdown link's anchor exists in its target file.\n" +
      "5. No added line references source code files or implementation paths, and no added cross-reference uses line numbers (flag cross-references only; incidental prose containing the word 'line' is fine).\n" +
      DEVIATION_NOTE +
      "\n\nReport each discrepancy with exact expected and observed quotes and a concrete fix. An empty list means the file aligns.";

    const sweepPrompt = (round) =>
      "You are a mechanical rules sweep over the applied spec diff. Round " +
      round +
      ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
      repo +
      ".\n\nRun `git diff -- spec/`. Inspect the added lines (lines starting with '+') for the first two checks below, and compare added against removed lines (lines starting with '-') for the renumbering check. Flag as a discrepancy:\n" +
      "- any reference to source code files or implementation paths: pkg/, cmd/, charts/, sdks/, tests/, migrations/, or a source file extension such as .go (added lines only);\n" +
      "- any cross-reference by line number ('line 123', 'lines 45-48') to spec or any other file. Cross-references only; incidental prose is fine (added lines only);\n" +
      "- any sign that a new section or subsection was inserted between existing ones instead of appended at the end of its level: an existing heading whose number changed (the diff removes a heading at one number and adds the same titled heading at a higher number), or a new heading inserted ahead of an existing sibling so the following siblings are renumbered. Renumbering an existing section breaks its cross-references; a new section or subsection belongs at the end of its level. Quote the renumbered headings, name the file, and give the fix (append the new section or subsection at the end of its level and restore the original numbering of the rest).\n" +
      "Pre-existing text that the diff leaves unchanged is out of scope. Quote each offending line exactly, name its file, and give the rule-conformant replacement." +
      DEVIATION_NOTE;

    const fixPrompt = (f, found, round) =>
      "You fix verified discrepancies between applied spec edits and the proposal that staged them. Round " +
      round +
      ".\n\nHARD CONSTRAINT: the only file you may edit is " +
      repo +
      "/" +
      f +
      ". Never modify the proposal or any other file.\n\nProposal: " +
      proposal +
      ".\n" +
      SPEC_RULES + SUMMARY_BLOCK + BLANKS_BLOCK +
      "\n\nDiscrepancies to fix (the expected text is authoritative except where a content rule forces a deviation, which you record in your reply):\n" +
      JSON.stringify(found, null, 2) +
      "\n\nMake the smallest edits that resolve each discrepancy. Return a short summary of each fix.";

    let round = 0;
    clean = false;
    while (round < maxApplyRounds && !clean) {
      round++;
      log("  " + ss + " verification round " + round);
      const checks = ssFiles.map((f) => () =>
        agent(verifyFilePrompt(f, verifiableEdits.filter((e) => e.targetFile === f), round), {
          schema: DISCREPANCIES,
          label: "verify:" + ss + ":" + f.split("/").pop() + ":r" + round,
          phase: "Apply spec",
        }),
      );
      checks.push(() =>
        agent(sweepPrompt(round), { schema: DISCREPANCIES, label: "verify:" + ss + ":rules-sweep:r" + round, phase: "Apply spec" }),
      );
      if (specReviewFocus.length > 0) {
        // Its own prompt rather than a per-file verifier's: this reviewer is
        // scoped to the areas the caller named, across every file the sub-step
        // touched, so binding it to one file would hide exactly the drift it
        // was added to find.
        checks.push(() =>
          agent(
            "You verify that applied spec edits align exactly with the proposal that staged them, " +
              "concentrating on areas the caller has singled out. Round " +
              round +
              ".\n\nYou are a read-only verifier; do not edit any file. Work in " +
              repo +
              ".\n\nProposal: " +
              proposal +
              ". Files this sub-step touched: " +
              ssFiles.join(", ") +
              ". Edits staged for them:\n" +
              JSON.stringify(verifiableEdits, null, 2) +
              "\n\nMethod: read the proposal's staged text for each area below, then read the current spec " +
              "files and `git diff -- spec/` to see what actually changed. Report a discrepancy wherever the " +
              "spec does not say what the proposal stages. Quote the staged text as `expected` and the " +
              "current text as `observed`, both exactly, and name the file and location." +
              FOCUS_BLOCK,
            { schema: DISCREPANCIES, label: "verify:" + ss + ":focus:r" + round, phase: "Apply spec" },
          ),
        );
      }
      const results = (await parallel(checks)).filter(Boolean);
      if (results.length === 0) {
        applyHistory.push({ substep: ss, round, discrepancies: -1, note: "verifiers failed" });
        break;
      }
      const found = results.flatMap((r) => r.discrepancies);
      applyHistory.push({ substep: ss, round, discrepancies: found.length, titles: found.map((d) => d.title) });
      log("  " + ss + " round " + round + ": " + found.length + " discrepancies");
      if (found.length === 0) {
        clean = true;
        break;
      }
      const fixFiles = [...new Set(found.map((d) => d.file))];
      await parallel(
        fixFiles.map((f) => () =>
          agent(fixPrompt(f, found.filter((d) => d.file === f), round), {
            label: "fix:" + ss + ":" + f.split("/").pop() + ":r" + round,
            phase: "Apply spec",
          }),
        ),
      );
    }
    if (!clean) {
      specStatus = "not-clean";
      await releaseLease();
      return {
        status: "spec-not-clean",
        reason:
          "sub-step " +
          ss +
          " did not converge within " +
          maxApplyRounds +
          " verification rounds. Sub-steps before it are committed; its own edits are in the working " +
          "tree for inspection. Revert spec/ to the last sub-step commit before re-running.",
        substep: ss,
        applyHistory,
        unappliable,
      };
    }

    // Commit this sub-step before the next one starts, so the next sub-step is
    // applied against a clean baseline and its diff is its own.
    await agent(
      "Commit the spec edits just applied for one sub-step of an approved proposal.\n\n" +
        "HARD CONSTRAINT: commit only files under spec/. Do not edit any file, do not amend an " +
        "existing commit, and do not touch the proposal.\n\n" +
        "Proposal: " +
        proposal +
        "\nSub-step just applied and verified: " +
        ss +
        "\n\nRun `git status --porcelain -- spec/` and `git diff --stat -- spec/` to see what changed, " +
        "stage those files, and commit on the current branch with a message in the repository's " +
        "convention (read `git log --oneline -5` first) describing what the spec now says. The message " +
        "references durable sources only: it may name the proposal file path, and it MUST NOT carry the " +
        "proposal's internal scaffolding labels, which are its change or section ids, decision ids, " +
        "review pass numbers, or a step number that exists only in the proposal, even if git log shows " +
        "prior commits that used them. Describe the behavior the spec now states instead. If nothing " +
        "under spec/ changed, commit nothing and say so.",
      { label: "commit-spec:" + ss, phase: "Apply spec" },
    );
    log("Sub-step " + ss + " verified and committed");
  }


  specStatus = clean ? (unappliable.length > 0 ? "applied-with-blockers" : "applied") : "not-clean";

  if (specStatus === "not-clean") {
    await releaseLease();
    return {
      status: "spec-not-clean",
      reason: "the spec apply verification did not converge within " + maxApplyRounds + " rounds; the staged edits are partially applied in the working tree for inspection.",
      applyHistory,
      unappliable,
    };
  }
  if (specStatus === "applied-with-blockers") {
    await releaseLease();
    return {
      status: "spec-applied-with-blockers",
      reason: "some staged spec edits could not be located (drifted anchors); resolve them before implementing code.",
      unappliable,
      appliedEdits: [...appliedIds],
      applyHistory,
    };
  }

  // Clean apply: record the status on the proposal and commit the spec edits.
  await agent(
    "Record application on a proposal's Status bullet and commit that change.\n\n" +
      "Work in " +
      repo +
      ". Edit only " +
      proposal +
      " (the Status bullet). Do not edit code, spec/, or any other file.\n\n" +
      "The spec edits are ALREADY COMMITTED: each sub-step was applied, verified, and committed on its " +
      "own as it landed, so `git status --porcelain -- spec/` is expected to be empty here and there is " +
      "nothing under spec/ left to stage. This commit records only that the proposal has been applied.\n\n" +
      '1. In ' +
      proposal +
      ', replace the Status bullet\'s leading state (for example "Approved for implementation as written (...).") with: "Applied to spec (' +
      date +
      ')." Preserve later clauses that remain true; follow ' +
      repo +
      "/.claude/rules/doc-style.md.\n" +
      "2. Commit " +
      relProposal +
      " on the current branch, message in the repository's convention (read `git log --oneline -5`), e.g. " +
      "'proposals: record " +
      relProposal +
      " as applied to spec'. The commit message references durable sources only: it may name the proposal " +
      "file path or a BUILD-GAPS finding id for traceability, but must NOT carry the proposal's internal " +
      "scaffolding labels, which are its change or section ids, decision ids, review pass numbers, or a " +
      "step that exists only in the proposal, even if `git log` shows prior commits that used them.",
    { label: "mark-and-commit-spec", phase: "Apply spec" },
  );
  log("Spec applied and committed per sub-step; status recorded");
}

// The spec phase is done, so spec/ locks again before any code is written.
// Released here rather than at the end of the run: under today's structure the
// build phase has no business in spec/, and a lease left open across it would
// be exactly the standing grant the old hook gave.
await releaseLease();

// ---- Implement code (optional) via the build subworkflow ----

if (!implementCode) {
  await releaseLease();
  return {
    status: "spec-only",
    specStatus,
    statusLine: plan.statusLine,
    files,
    appliedEdits: [...appliedIds],
    deviations,
    nonSpecStaged: plan.nonSpecStaged,
    findingsReferencing: plan.findingIds,
    applyHistory,
  };
}

// The implement-proposal-build subworkflow IS the implement stage: it runs
// inline and brings its own phase group (Plan, Build, Verify, Review) under a
// "▸ implement-proposal-build" heading, so no redundant parent "Implement"
// phase wraps it.
log("Implementing the spec change via the implement-proposal-build subworkflow");
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
    },
  );
} catch (e) {
  await releaseLease();
  return {
    status: "aborted",
    abortReason: "implement-proposal-build subworkflow failed: " + (e && e.message),
    specStatus,
    findingsReferencing: plan.findingIds,
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
    close = await agent(
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
      { schema: CLOSE, label: "close-findings", phase: "Close findings" },
    );
  }
}

await releaseLease();
return {
  status:
    build.status === "step-stuck"
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
