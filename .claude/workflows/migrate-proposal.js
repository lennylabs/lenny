// Migrate ONE legacy single-file proposal into the folder layout.
//
// There is no batch migration and no script to run over proposals/. A proposal
// migrates the first time a workflow touches it, in the same commit as the work
// that triggered it, and nothing else moves. Both change-proposal and
// implement-proposal invoke this at startup when their resolver reports a
// legacy layout, so the logic lives once rather than being described twice in
// prose across two skills.
//
// Three reasons the migration is lazy rather than a big bang. The split needs
// judgement about which design paragraphs are spec-facing and which are not, so
// it was always agent work rather than a script. Migrating a proposal nobody is
// working on buys nothing and risks a bad split landing unreviewed. And a lazy
// migration is reviewed by whoever asked for the work that triggered it.
//
//   Workflow({ scriptPath: ".claude/workflows/migrate-proposal.js", args: {
//     proposalPath: "proposals/NNNN_kind_slug.md",  // required
//     repoRoot: "/abs/path",                        // required
//     date: "YYYY-MM-DD",                           // required
//     commit: true,                                 // optional, default true
//   }})
//
// Returns { status, dir, ... }, where dir is repo-relative on every exit. status is one of:
//   migrated        the split landed and the legacy file is gone
//   already         the directory exists and the legacy file does not
//   refused-implemented   an Implemented or Retired proposal is not split
//   unreadable-status     the status tool reported no known state, so nothing is split
//   incomplete-split      a role file the split claimed is not on disk
//   status-not-written    the status tool refused to write the split's frontmatter
//   lost-content    the partition check found content in neither output
//   unresolved-references   an inbound reference could not be retargeted
//
// MAINTENANCE: §2.4 of .claude/proposal-pipeline-rework-plan.md is the design.

export const meta = {
  name: "migrate-proposal",
  description: "Split one legacy single-file proposal into the folder layout, verify nothing was lost, and retarget its inbound references",
  phases: [
    { title: "Assess", detail: "decide whether this proposal migrates at all" },
    { title: "Split", detail: "partition the document into the role-scoped files" },
    { title: "Verify", detail: "run the partition check and retarget inbound references" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath) throw new Error("args.proposalPath is required");
if (!input.repoRoot) throw new Error("args.repoRoot is required and missing");
if (!input.date) throw new Error("args.date is required and missing");

const repo = input.repoRoot;
const date = input.date;
const doCommit = input.commit !== false;
const rel = input.proposalPath.startsWith("/")
  ? input.proposalPath.replace(repo + "/", "")
  : input.proposalPath;

// The stem is the file name without .md, and it is also the directory name and
// the prefix of every file inside it.
const stem = rel.replace(/^proposals\//, "").replace(/\.md$/, "");
// `dir` is absolute because every use of it below goes into an agent prompt, and
// `relDir` is what a result carries: change-proposal.js prefixes the returned dir
// with the repo root, so an absolute one there produced /repo//repo/proposals/<stem>
// and cp-round-boundary.sh exited 1 "no such proposal directory" on every round.
const relDir = "proposals/" + stem;
const dir = repo + "/" + relDir;
const legacy = repo + "/" + rel;
const F = (role) => dir + "/" + stem + "." + role + ".md";

const ROLES = [
  "problem-statement",
  "summary",
  "status",
  "implementation-checklist",
  "spec-changes",
  "non-spec-changes",
  "review-log",
  "deviations",
];

// The states proposal-status.mjs writes. Kept in step with STATES in
// .claude/tools/proposal-status.mjs, which refuses anything else.
const STATES = ["Draft", "Reviewed", "Approved", "Implemented"];

// ---- Argument classification ---------------------------------------------
//
// forward: read where it is used. launch: controls how a run starts.
const ARG_CLASS = {
  proposalPath: "launch",
  repoRoot: "launch",
  date: "anchored",
  commit: "launch",
  maxRepairs: "forward",
};

const ASSESS = {
  type: "object",
  required: ["state", "status"],
  properties: {
    state: {
      type: "string",
      enum: ["legacy", "already-migrated", "partial", "absent"],
      description:
        "legacy: the single file exists and the directory does not. already-migrated: the directory exists and the single file does not. partial: BOTH exist, which means a previous run died mid-split and this one resumes it. absent: neither.",
    },
    status: {
      type: "string",
      description:
        "what the status tool printed for this proposal. One of the four states when the tool succeeded, and the tool's error line as printed when it did not; the error is never mapped onto a state.",
    },
    title: { type: "string" },
    kind: { type: "string", description: "new or fix, from the file stem" },
  },
};

const PRESENT = {
  type: "object",
  required: ["present", "missing"],
  properties: {
    present: { type: "array", items: { type: "string" }, description: "the roles the command printed PRESENT for" },
    missing: { type: "array", items: { type: "string" }, description: "the roles the command printed MISSING for" },
  },
};

const WROTE = {
  type: "object",
  required: ["ok", "printed"],
  properties: {
    ok: { type: "boolean", description: "true only when the command exited 0" },
    printed: { type: "string", description: "what the command printed, verbatim" },
  },
};

const SPLIT = {
  type: "object",
  required: ["written", "notes"],
  properties: {
    written: { type: "array", items: { type: "string" }, description: "the role names actually written" },
    notes: { type: "string", description: "anything the document did not supply, and what was put in its place" },
  },
};

const LOST = {
  type: "object",
  required: ["ok", "lost"],
  properties: {
    ok: { type: "boolean" },
    lost: { type: "array", items: { type: "string" }, description: "the reported lines, verbatim from the checker" },
  },
};

const REFS = {
  type: "object",
  required: ["sites", "unresolved"],
  properties: {
    sites: {
      type: "array",
      description: "one entry per inbound reference retargeted",
      items: {
        type: "object",
        required: ["file", "was", "now", "why"],
        properties: {
          file: { type: "string" },
          was: { type: "string" },
          now: { type: "string" },
          why: { type: "string", description: "why that target rather than the directory or another file" },
        },
      },
    },
    unresolved: {
      type: "array",
      items: { type: "string" },
      description: "a reference this pass could not retarget with confidence; any entry stops the migration",
    },
  },
};

// ---- Assess --------------------------------------------------------------

phase("Assess");

const assess = await agent(
  "Report the migration state of one proposal. Do not edit or create anything.\n\n" +
    "Work in " + repo + ".\n" +
    "Legacy file: " + legacy + "\nDirectory: " + dir + "\n\n" +
    "Run exactly these and read their output:\n" +
    "  test -f '" + legacy + "' && echo FILE_YES || echo FILE_NO\n" +
    "  test -d '" + dir + "' && echo DIR_YES || echo DIR_NO\n" +
    "  node " + repo + "/.claude/tools/proposal-status.mjs '" +
    legacy + "' --field status 2>&1 || node " + repo +
    "/.claude/tools/proposal-status.mjs '" + dir + "' --field status 2>&1\n\n" +
    "Map the two existence answers onto state: file yes and directory no is legacy; " +
    "file no and directory yes is already-migrated; both yes is partial; both no is absent. " +
    "Report the status the tool printed verbatim. If the tool printed an error rather than a state, " +
    "report that error line as the status; do not map it onto a state and do not invent one. " +
    "Read the first heading of whichever of the two " +
    "exists for the title, and take kind from the stem (" + stem + "): the segment after the number.",
  { schema: ASSESS, label: "assess", phase: "Assess" },
);

if (!assess) {
  return { status: "assess-failed", reason: "the assessment agent did not return", dir: relDir };
}

if (assess.state === "absent") {
  return { status: "absent", reason: "neither " + legacy + " nor " + dir + " exists", dir: relDir };
}
if (assess.state === "already-migrated") {
  log("Already migrated: " + dir);
  return { status: "already", dir: relDir, stem };
}

// A landed proposal is a historical record and splitting it is an edit
// (spec-driven-development.md). Under lazy migration this needs no flag and no
// policy: nothing ever triggers an implemented proposal's migration, because
// nothing converges or implements it again. This guard is here for the case
// where a caller asks anyway.
if (assess.status === "Implemented" || assess.status === "Retired") {
  log("Refusing to migrate a " + assess.status + " proposal: it is a historical record");
  return {
    status: "refused-implemented",
    proposalStatus: assess.status,
    reason:
      "a proposal that is " + assess.status + " is not edited, and splitting it into eight files is an edit. " +
      "It stays in the legacy layout and every consumer reads it through the resolver.",
    legacy: rel,
  };
}

// The assessment reports what the status tool PRINTED, and the command above
// folds the tool's stderr into that: measured on this tree, a legacy proposal
// with no Status bullet prints "proposal-status: legacy proposal has no Status
// bullet: <file>", an unrecognised spelling prints "proposal-status:
// unrecognised legacy status: Parked pending discussion (2026-01-01).", and the
// `||` fallback prints "proposal-status: no such proposal: <dir>" because the
// directory does not exist yet. None of the three is Implemented or Retired, so
// the guard above passes them, and the split prompt below writes the string into
// `status:` in the frontmatter. readStatus then rejects the file and spec-lease
// answers "cannot read the status of <proposal>; refusing" to every spec/ write
// naming it, so the migration would produce a proposal no step can implement.
if (!STATES.includes(assess.status)) {
  log("Refusing to migrate: the status tool reported no known state");
  return {
    status: "unreadable-status",
    proposalStatus: assess.status,
    reason:
      "the status tool reported " + JSON.stringify(assess.status) + ", which is none of " +
      STATES.join(", ") + ". The split writes that value into the frontmatter and every later reader " +
      "rejects what it cannot parse, so the proposal is not split. Correct the Status bullet in " + rel +
      " first.",
    legacy: rel,
    dir: relDir,
  };
}

log(
  (assess.state === "partial" ? "Resuming an interrupted split of " : "Migrating ") +
    rel + " (" + assess.status + ")",
);

// ---- Split ---------------------------------------------------------------

phase("Split");

const splitPrompt =
  "Split one legacy single-file change proposal into the folder layout. This is a RELOCATION, not a rewrite.\n\n" +
  "Work in " + repo + ". Source: " + legacy + ". Destination directory: " + dir + " (create it).\n\n" +
  (assess.state === "partial"
    ? "A PREVIOUS RUN DIED PART-WAY THROUGH THIS SPLIT. Both the legacy file and the directory exist. " +
      "Read what is already in the directory first and write only what is missing or obviously truncated. " +
      "Do not duplicate content that already landed.\n\n"
    : "") +
  "THE HARD RULE: every non-blank line of the source must end up in at least one destination file, " +
  "character for character. You may add headings. You may not reword, summarise, merge, drop, or " +
  "'improve' a single line of content. A mechanical check runs immediately after you and reports every " +
  "line that appears in none of the outputs, so a rewrite is not a judgement call that might pass, it is " +
  "a failure that will be caught.\n\n" +
  "Write these files, all prefixed " + stem + ".:\n\n" +
  "  .problem-statement.md — the Problem section and its evidence. Head it `# Problem: <title>`.\n" +
  "  .summary.md — the existing `## Summary` section if the document has one. Head it `# Summary: <title>`. " +
  "If it has none, write the heading and put the Decisions and Non-goals sections here, since those are " +
  "what an implementor needs and they belong to the summary in the new layout.\n" +
  "  .status.md — frontmatter only, plus the original Status bullet verbatim in the body so no line is " +
  "lost. The frontmatter is FLAT, exactly these keys:\n" +
  "```\n---\nproposal: " + stem + "\ntitle: <the proposal title>\nkind: " + (assess.kind || "fix") +
  "\nstatus: " + assess.status + "\ndrafted-date: \ndrafted-by: \nreviewed-date: \nreviewed-by: \n" +
  "approved-date: \napproved-by: \nimplemented-date: \nimplemented-by: \n---\n```\n" +
  "  Fill any date you can read out of the original Status bullet; leave the rest empty. `status` must be " +
  "exactly " + assess.status + ".\n" +
  "  .implementation-checklist.md — the `## Implementation checklist` section if there is one. If there " +
  "is not, write the heading and a single line saying the proposal predates checklists, and nothing else: " +
  "deriving a sequence is not this step's job.\n" +
  "  .spec-changes.md — every staged change whose target is under spec/, with its anchor instructions and " +
  "fenced blocks intact, plus the design prose that is about what the SPECIFICATION must say.\n" +
  "  .non-spec-changes.md — everything else that is staged: code, schemas, charts, migrations, docs, and " +
  "the Testing section. Plus the design prose that is about the implementation.\n" +
  "  .review-log.md — head it `# Review log — " + stem + "`, then `## Standing context` (empty for now), " +
  "and `## Ledger` (empty), and put the entire `Resolved in adversarial review` history " +
  "under Retired. That history is what the log's Retired section is for.\n" +
  "  .deviations.md — head it `# Deviations — " + stem + "` and a one-line note that the implementor owns " +
  "this file and it is empty until an implementation records one. It has no source lines to carry.\n\n" +
  "WHERE A SECTION IS AMBIGUOUS between spec-changes and non-spec-changes, ask which file the change " +
  "lands in: a change to spec/ prose is spec, a change to pkg/ or tests/ or docs/ is not. Design prose " +
  "that motivates both goes to spec-changes, because the specification is what the code is derived from.\n\n" +
  "Do NOT delete the legacy file. A later step does that once the check passes.\n" +
  "Follow " + repo + "/.claude/rules/doc-style.md for any heading or connective sentence you author.";

const split = await agent(splitPrompt, { schema: SPLIT, label: "split", phase: "Split" });
if (!split) return { status: "split-failed", reason: "the split agent did not return", dir: relDir };
log("Split reported " + (split.written || []).length + " file(s)");

// `written` is the split agent's own account of what it did, and nothing
// compared it against ROLES: a split that wrote four files still returned eight
// paths from the return below, four of them absent. The partition checker does
// not cover it either, because it globs whatever .md files are in the directory
// and only asks whether every SOURCE line landed somewhere, and .deviations.md
// and .review-log.md carry no source lines by construction. Ask the filesystem.
const present = await agent(
  "Report which files of a proposal split exist. Do not edit or create anything.\n\n" +
    "Run exactly this and read its output:\n" +
    "  for r in " + ROLES.join(" ") + "; do test -f '" + dir + "/" + stem +
    ".'$r'.md' && echo \"PRESENT $r\" || echo \"MISSING $r\"; done\n\n" +
    "Report each role under the answer the command gave for it, and report nothing it did not print.",
  { schema: PRESENT, label: "confirm-split", phase: "Split" },
);
if (!present) return { status: "confirm-failed", reason: "the split confirmation agent did not return", dir: relDir };
const missingRoles = ROLES.filter((r) => !(present.present || []).includes(r));
if (missingRoles.length > 0) {
  return {
    status: "incomplete-split",
    reason:
      "the split did not produce every role file, so the migration stopped rather than reporting files " +
      "that are not on disk. The legacy file is untouched and the directory is in the working tree for " +
      "inspection; nothing was committed.",
    missing: missingRoles,
    written: present.present || [],
    dir: relDir,
  };
}

// The status goes through the tool, which is the only supported writer
// (spec-driven-development.md, and this tool's own header): the spec-lease hook
// reads the status through it, so a status set by writing prose is a status the
// hook may not agree with. The split wrote that frontmatter by hand from a
// prompt, so this normalises it and proves it parses, since --set exits non-zero
// on absent or malformed frontmatter and on any value outside the four states.
// No --by and no --date: writeStatus stamps the date of the state it sets, and
// the date this migration ran is not the date the proposal was drafted.
const wrote = await agent(
  "Run exactly this command and report its exit status and its output. Change nothing else.\n\n" +
    "node " + repo + "/.claude/tools/proposal-status.mjs '" + dir + "' --set status=" + assess.status + "\n\n" +
    "Set ok from the exit status: 0 is ok and anything else is not. Put what it printed in printed, verbatim.",
  { schema: WROTE, label: "status:write", model: "haiku", phase: "Split" },
);
if (!wrote || !wrote.ok) {
  return {
    status: "status-not-written",
    reason:
      "the status tool refused to write " + assess.status + " into the split status file, so its " +
      "frontmatter is absent or malformed. The legacy file is untouched and nothing was committed. The " +
      "tool printed: " + ((wrote && wrote.printed) || "nothing"),
    dir: relDir,
  };
}

// ---- Verify --------------------------------------------------------------

phase("Verify");

// The split is agent judgement. That the split lost nothing is not, and the
// check stays in a script so "migrated" cannot come to mean "an agent said it
// was fine". Up to three repair attempts, because a check that names the exact
// missing lines is repairable, and one that is still failing on the fourth
// pass is not going to pass on the fifth.
const maxRepairs = input.maxRepairs || 3;
let lost = null;
for (let attempt = 0; attempt <= maxRepairs; attempt++) {
  const res = await agent(
    "Run exactly this command and report its result. Do not edit anything.\n\n" +
      "node " + repo + "/.claude/tools/check-proposal-split.mjs '" + legacy + "' '" + dir + "'\n\n" +
      "It exits 0 when every content line of the source is present across the split files, and 1 when " +
      "some are not, printing each with its source line number. Set ok from the exit status and put every " +
      "reported line in lost.",
    { schema: LOST, label: "check-split:a" + attempt, phase: "Verify" },
  );
  if (!res) return { status: "check-failed", reason: "the partition checker did not return", dir: relDir };
  lost = res;
  if (res.ok) {
    log("Partition check clean" + (attempt > 0 ? " after " + attempt + " repair(s)" : ""));
    break;
  }
  if (attempt === maxRepairs) break;
  log("Partition check found " + res.lost.length + " lost line(s); repairing (" + (attempt + 1) + "/" + maxRepairs + ")");
  await agent(
    "Repair a proposal split that lost content. Work in " + repo + ".\n\n" +
      "Source: " + legacy + ". Destination: " + dir + ".\n\n" +
      "The partition checker reports that these lines of the source appear in NONE of the split files:\n" +
      res.lost.map((l) => "  " + l).join("\n") +
      "\n\nPut each one back, in the destination file whose role it belongs to, in its original position " +
      "relative to its neighbours. Copy the line from the source character for character; do not retype it " +
      "and do not reword it. Change nothing else, and do not delete anything already present.",
    { label: "repair-split:a" + attempt, phase: "Verify" },
  );
}

if (!lost || !lost.ok) {
  return {
    status: "lost-content",
    reason:
      "the split lost content the repair passes could not restore. The legacy file is untouched and the " +
      "directory is in the working tree for inspection; nothing was committed.",
    lost: (lost && lost.lost) || [],
    dir: relDir,
  };
}

// Inbound references. A big-bang migration would have fixed every one in a
// single commit; a lazy one means a reference can break at any time, one
// proposal at a time, so the migration that breaks it fixes it.
const refs = await agent(
  "Retarget every inbound reference to a proposal that has just been split into a directory.\n\n" +
    "Work in " + repo + ". The proposal was " + rel + " and is now the directory proposals/" + stem + "/.\n\n" +
    "FIND the references first, before changing anything:\n" +
    "  git grep -n '" + stem + "' -- . ':!proposals/" + stem + "' ':!.git'\n\n" +
    "Then retarget each one, and the target depends on what the referring site does with it:\n" +
    "  A finding, queue row, or prose mention that points AT the proposal takes the directory, " +
    "proposals/" + stem + "/.\n" +
    "  A test or script that READS the file's content takes the specific role file whose content it " +
    "needs. One that reads staged spec text takes .spec-changes.md; one that reads the checklist takes " +
    ".implementation-checklist.md. Read the surrounding code to decide, and say why in your result.\n" +
    "  A path-PREFIX match (anything keyed on 'proposals/' rather than on this stem) needs no change, " +
    "because a directory under proposals/ still satisfies it. Do not touch those.\n\n" +
    "IF YOU CANNOT DECIDE what a reference should point at, do not guess: leave it and list it in " +
    "unresolved. Any unresolved entry stops the migration, which is the correct outcome, because a " +
    "half-retargeted tree is worse than an unmigrated one.\n\n" +
    "Do not edit anything under proposals/" + stem + "/ and do not delete the legacy file.",
  { schema: REFS, label: "retarget-refs", phase: "Verify" },
);

if (!refs) return { status: "refs-failed", reason: "the reference retargeting agent did not return", dir: relDir };
if ((refs.unresolved || []).length > 0) {
  return {
    status: "unresolved-references",
    reason:
      "an inbound reference could not be retargeted with confidence, so the migration stopped rather than " +
      "landing a half-retargeted tree. The legacy file is still in place.",
    unresolved: refs.unresolved,
    retargeted: refs.sites || [],
    dir: relDir,
  };
}
log("Retargeted " + (refs.sites || []).length + " inbound reference(s)");

// ---- Land ----------------------------------------------------------------

await agent(
  "Run exactly this command and reply with the single word DONE:\n\n" +
    "rm -f '" + legacy + "'\n\n" +
    "Do nothing else. Do not read, summarise, or edit any file.",
  { label: "drop-legacy", model: "haiku", phase: "Verify" },
);

if (doCommit) {
  await agent(
    "Commit a completed proposal migration on the current branch. Work in " + repo + ".\n\n" +
      "Stage and commit exactly: the new directory proposals/" + stem + "/, the deletion of " + rel + ", " +
      "and the files whose references were retargeted (" +
      ((refs.sites || []).map((s) => s.file).join(", ") || "none") + "). Stage nothing else.\n\n" +
      "Write the message in the repository's convention (read `git log --oneline -5` first). Say that the " +
      "proposal moved to the folder layout, that the split was checked to have lost no content, and which " +
      "references were retargeted. The message references durable sources only: it may name the proposal " +
      "path, and it must not carry the proposal's internal change, decision, pass, or step labels. " +
      "Date: " + date + ".",
    { label: "commit-migration", phase: "Verify" },
  );
}

log("Migrated " + rel + " to " + dir);
return {
  status: "migrated",
  dir: relDir,
  stem,
  proposalStatus: assess.status,
  written: present.present,
  retargeted: refs.sites || [],
  // Every role was confirmed on disk above, so this list is a report rather than a claim.
  files: ROLES.map((r) => "proposals/" + stem + "/" + stem + "." + r + ".md"),
};
