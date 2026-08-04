// apply-0064: a staged applier for the channel-rename proposal.
//
// Forked from implement-proposal because that workflow derives the order of a
// sub-step's operations (authored edits fan out, then mechanical edits run) and
// that derived order is wrong for this proposal. SPEC-1 must run its pass BEFORE
// its three hand corrections, because those corrections delete reserved-phrase
// sites the pass's register still claims, and the pass then aborts on entries no
// site claims. SPEC-3 must reduce, then insert headings, then run the line pass
// over the shifted tree. No field of the plan schema carries that ordering, so
// it is supplied here as a declared stage list instead of being inferred.
//
// What is kept from the parent, because it has proven out: the content rules,
// the deviation record, stop-on-unappliable, per-stage commits, and the
// verify-and-fix loop. What is added: an ordered stage list, and a precheck that
// runs a pass's dry run and refuses to apply when the planned file set does not
// match what the stage declares. Every stop this project hit was a write that
// should not have happened; the precheck is the cheap guard against all of them.
//
// The proposal is NOT modified by this workflow. Ordering lives in the stage
// list, which is an input.
//
//   Workflow({ scriptPath: ".claude/workflows/apply-0064.js", args: {
//     repoRoot: "/abs/path", date: "YYYY-MM-DD",
//     stages: [ ...see STAGE SHAPE below... ],
//     stopAfter: "<stage name>"        // optional; run a prefix and stop
//   }})
//
// STAGE SHAPE. Each stage is { name, ops[], commit }. Each op is one of:
//   { op: "pass", command, expectFiles?, mustWrite?[] }
//       Dry-runs `command`, asserts it exits zero, asserts the planned file
//       count equals expectFiles when given and that every mustWrite path is in
//       the planned set, then applies and confirms the applied file set equals
//       the planned one. expectFiles is omitted when the count is not knowable
//       in advance, and the dry run's own success is then the gate.
//   { op: "author", instruction, files[] }
//       One agent writes the named files. No single-file constraint: an authored
//       edit of this proposal frequently spans several files that must agree.
//   { op: "verify", tiers[] }
//   { op: "review", scope }

export const meta = {
  name: "apply-0064",
  description:
    "Apply the channel-rename proposal as an ordered stage list, running each migration pass once at stage level with a dry-run precheck, and reviewing each stage's own diff",
  phases: [{ title: "Stages", detail: "run each declared stage: ops, verify, review, commit" }],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.repoRoot || !input.date || !Array.isArray(input.stages)) {
  throw new Error("args.repoRoot, args.date and args.stages are required");
}
const repo = input.repoRoot;
const date = input.date;
const stages = input.stages;
const stopAfter = input.stopAfter || "";
const proposal =
  repo + "/proposals/0064_fix_name-the-communication-channels-and-move-them-into-the-spec.md";

const SPEC_RULES =
  "Spec content rules (these take precedence over verbatim application; record every deviation they force):\n" +
  "- The spec never references source code files or implementation paths (pkg/, cmd/, charts/, sdks/, tests/, migrations/, .go or other source files). Rephrase staged text carrying such a reference into behavioral spec language, or drop the reference.\n" +
  "- The spec cross-references other spec content by section number only: §X.Y or a relative markdown link to a section anchor. Replace a line-number cross-reference in staged text with the containing section's number.\n" +
  "- Line numbers in the proposal's ANCHOR INSTRUCTIONS are location hints for you and never become spec content. Locate anchors by the quoted text and section headings; line numbers drift.\n" +
  "- A staged edit that introduces a brand-new section or subsection is appended at the end of its level, after the last existing sibling at that level, and numbered as the next ordinal. Never insert a new section or subsection between existing ones: inserting in the middle forces every following section to be renumbered and breaks existing cross-references. When a staged anchor instruction would place a new section or subsection between existing ones, append it at the end of that level instead, renumber it to the next ordinal, and record the deviation. Editing the body of an existing section in place is unaffected by this rule; it applies only to introducing a new numbered section or subsection.\n" +
  "- Apply staged prose as written otherwise; do not restyle it.\n" +
  "- These rules govern text you author from a staged block. For a mechanical edit you do not author the text: if the script's output violates one of them, that is a defect in the script or its register, so record it as a deviation and stop, rather than hand-correcting the output, which would put the tree and the register out of step.";
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

const PRECHECK = {
  type: "object",
  required: ["exitZero", "plannedFiles", "output"],
  properties: {
    exitZero: { type: "boolean" },
    plannedFiles: { type: "array", items: { type: "string" } },
    output: { type: "string", description: "The dry run's first lines, verbatim" },
  },
};

const APPLIED = {
  type: "object",
  required: ["ok", "writtenFiles", "notes"],
  properties: {
    ok: { type: "boolean" },
    writtenFiles: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
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
        required: ["title", "file", "why", "fix"],
        properties: {
          title: { type: "string" },
          file: { type: "string" },
          why: { type: "string" },
          fix: { type: "string" },
        },
      },
    },
  },
};

const CTX =
  "Repository: " + repo + ". The proposal being applied is " + proposal +
  ", and it is NOT to be edited by you under any circumstance: it is approved, and the ordering this " +
  "run follows is supplied by the caller rather than by the proposal. Read it for what each sub-step " +
  "stages.\n";

function log2(s) { log("    " + s); }

// robustAgent retries a transient API failure. agent() returns null when the
// runtime's own retries are exhausted, and a null from an APPLY op is the worst
// case this workflow has: the command may have run and written the tree while
// the agent died before reporting, so the run stops with the tree in a state
// nobody has inspected. That happened once, on the complementary name-pass run,
// and the tree turned out correct. Retrying first makes the null far rarer, and
// every op that follows re-derives state from the tree rather than from the
// agent's word, so a retry after a successful-but-unreported run reports the
// already-applied state instead of applying twice.
async function robustAgent(prompt, opts, attempts = 3) {
  for (let i = 1; i <= attempts; i++) {
    const r = await agent(prompt, opts);
    if (r !== null && r !== undefined) return r;
    if (i < attempts) {
      log2((opts && opts.label ? opts.label : "agent") + ": transient failure, retry " + i + "/" + (attempts - 1));
    }
  }
  return null;
}

// ---------------------------------------------------------------------------

const history = [];

for (const stage of stages) {
  phase("Stages");
  log("Stage " + stage.name);
  const record = { stage: stage.name, ops: [] };

  for (const op of stage.ops || []) {
    if (op.op === "pass") {
      // PRECHECK. The dry run writes nothing, so this costs one run and is the
      // only place a wrong file set can be caught before it lands.
      const pre = await robustAgent(
        "Run ONE dry run of a migration pass and report exactly what it plans. Write nothing.\n\n" +
          CTX +
          "\nRun this command verbatim from " + repo + ":\n\n    " + op.command +
          "\n\nThe tool's default is a dry run: it writes nothing unless -apply is passed, and you must " +
          "NOT pass -apply. Report whether it exited zero, the full list of files it says it planned, " +
          "and its first lines verbatim. If it exited non-zero, report exitZero false and put the error " +
          "in output; do not try to fix anything.",
        { schema: PRECHECK, label: "precheck:" + stage.name, phase: "Stages" },
      );
      if (!pre || !pre.exitZero) {
        return {
          status: "precheck-failed",
          stage: stage.name,
          command: op.command,
          output: pre ? pre.output : "(precheck agent returned nothing)",
          reason:
            "the pass's dry run did not exit zero, so nothing was applied. The tree is unchanged at this " +
            "stage. Resolve what the tool reports before re-running.",
          history,
        };
      }
      const planned = pre.plannedFiles || [];
      log2("dry run planned " + planned.length + " file(s)");
      if (typeof op.expectFiles === "number" && planned.length !== op.expectFiles) {
        return {
          status: "precheck-mismatch",
          stage: stage.name,
          command: op.command,
          expected: op.expectFiles,
          planned: planned.length,
          plannedFiles: planned,
          reason:
            "the pass planned a different file set from the one this stage declares, so it was NOT " +
            "applied. Either the tree moved since the stage list was measured, or an earlier stage " +
            "changed what the register resolves. Re-measure before re-running.",
          history,
        };
      }
      for (const must of op.mustWrite || []) {
        if (!planned.some((p) => p === must || p.endsWith("/" + must))) {
          return {
            status: "precheck-mismatch",
            stage: stage.name,
            command: op.command,
            reason: "the pass does not plan " + must + ", which this stage declares it must write.",
            plannedFiles: planned,
            history,
          };
        }
      }

      const applied = await robustAgent(
        "Apply ONE migration pass by running its command, having already confirmed its dry run.\n\n" +
          CTX +
          "\nThe dry run of this command planned " + planned.length + " file(s) and exited zero. Now run " +
          "the apply form, which is the same command with -apply appended:\n\n    " + op.command + " -apply" +
          "\n\nThen run `git status --porcelain` and report every file it changed. Do NOT hand-write, " +
          "hand-reproduce, or hand-correct anything the pass writes: it resolves each site from its " +
          "register and fails closed on a site the register does not carry, and a hand edit substitutes " +
          "a guess for that guarantee. Report ok false with the reason if the command exits non-zero, or " +
          "if the set of files it changed differs from the " + planned.length + " it planned.",
        { schema: APPLIED, label: "apply:" + stage.name, phase: "Stages" },
      );
      if (!applied || !applied.ok) {
        // The command may have run and written the tree before the agent died.
        // Say so rather than implying the tree is untouched: this run's whole
        // value is that a stop is diagnosable, and a stop that misreports the
        // tree's state is worse than no stop at all.
        return {
          status: "apply-failed",
          treeStateUnknown: !applied,
          inspect:
            !applied
              ? "the apply agent returned nothing after retries, so the command may have run and " +
                "written files before it died. Run `git status --porcelain` and compare against the " +
                "planned set before re-running: re-applying a pass over a tree it already rewrote " +
                "aborts on entries no site claims."
              : "",

          stage: stage.name,
          command: op.command,
          notes: applied ? applied.notes : "(apply agent returned nothing)",
          reason: "the pass's apply form failed or wrote a different set than it planned.",
          history,
        };
      }
      log2("applied, " + (applied.writtenFiles || []).length + " file(s) written");
      record.ops.push({ op: "pass", command: op.command, planned: planned.length });
    } else if (op.op === "author") {
      const res = await robustAgent(
        "Apply the authored spec edits this stage names, from an approved proposal.\n\n" +
          CTX +
          "\nStage: " + stage.name + "\nWhat to write: " + op.instruction +
          "\nFiles you may edit: " + (op.files || []).join(", ") +
          "\n\nEdit ONLY those files. Locate each anchor by its quoted text and headings; line numbers in " +
          "the proposal are hints and drift. Apply the staged text as written.\n\n" + SPEC_RULES +
          "\n\nIf an anchor cannot be located with certainty, STOP: record it as unappliable with the " +
          "reason and apply nothing further. Never guess a location, and never skip an edit to continue " +
          "with later ones, because a partially applied file makes every later discrepancy ambiguous.",
        { schema: APPLY_RESULT, label: "author:" + stage.name, phase: "Stages" },
      );
      if (!res || (res.unappliable || []).length > 0) {
        return {
          status: "author-unappliable",
          stage: stage.name,
          unappliable: res ? res.unappliable : [],
          reason: "an authored edit could not be located, so the stage stopped rather than guessing.",
          history,
        };
      }
      if ((res.deviations || []).length > 0) {
        log2((res.deviations || []).length + " rule-forced deviation(s) recorded");
        record.deviations = res.deviations;
      }
      record.ops.push({ op: "author", files: op.files });
    } else if (op.op === "regen") {
      // A pass that rewrites a generated artifact's SOURCE leaves the generated
      // copy stale, and a no-drift gate then fails. The name pass writes comments
      // in schemas/*.proto, whose stubs live in pkg/proto, and the proposal says
      // those are regenerated rather than rewritten. Regeneration is therefore an
      // operation of the stage rather than something to remember afterwards.
      const g = await robustAgent(
        "Regenerate derived artifacts after a pass rewrote their source. Do not hand-edit generated output.\n\n" +
          CTX + "\nRun, from " + repo + ", with PATH=$HOME/bin:$PATH:\n\n    " +
          (op.commands || []).join("\n    ") +
          "\n\nThen run `git status --porcelain` and report every file that changed. Report ok false with " +
          "the command's output if any command exits non-zero. Never edit a generated file by hand: if " +
          "the output is wrong, the generator or its source is wrong.",
        { schema: APPLIED, label: "regen:" + stage.name, phase: "Stages" },
      );
      if (!g || !g.ok) {
        return {
          status: "regen-failed",
          stage: stage.name,
          notes: g ? g.notes : "(regen agent returned nothing)",
          reason: "regeneration of a derived artifact failed; its no-drift gate would fail.",
          history,
        };
      }
      log2("regenerated " + (g.writtenFiles || []).length + " file(s)");
      record.ops.push({ op: "regen", commands: op.commands });
    } else if (op.op === "verify") {
      const v = await robustAgent(
        "Run the named test tiers and report the result. Do not edit any file.\n\n" +
          CTX + "\nTiers: " + (op.tiers || ["static"]).join(", ") +
          "\n\nRun each with `go run ./cmd/lenny-test --tier <name>` from " + repo +
          ", using PATH=$HOME/bin:$PATH. Report ok true only when every named tier passes, and put the " +
          "failing output in notes otherwise.",
        { schema: APPLIED, label: "verify:" + stage.name, phase: "Stages" },
      );
      if (!v || !v.ok) {
        return {
          status: "verify-failed",
          stage: stage.name,
          notes: v ? v.notes : "(verify agent returned nothing)",
          reason: "a tier this stage requires did not pass. The stage's work is in the tree uncommitted.",
          history,
        };
      }
      log2("tiers green");
      record.ops.push({ op: "verify", tiers: op.tiers });
    } else if (op.op === "review") {
      // Adversarial review over THIS stage's own diff, with a bounded fix loop.
      let round = 0;
      let clean = false;
      while (round < 4 && !clean) {
        round++;
        const rev = await robustAgent(
          "Adversarially review one stage's diff against the proposal that staged it.\n\n" +
            CTX + "\nStage: " + stage.name + "\nScope: " + (op.scope || "the whole stage") +
            "\n\nYou are read-only. Run `git diff` to see what this stage changed (the tree is clean " +
            "apart from this stage's work). Verify against the proposal: does the diff do what the " +
            "sub-step stages, and only that? Hunt for a pass whose output contradicts what the proposal " +
            "says it writes, an authored edit that says something the proposal does not, a sentence a " +
            "rewrite left describing the wrong mechanism, a cross-reference that no longer resolves, and " +
            "any file changed that this stage should not touch.\n\n" +
            "Report only real defects with file:line evidence. Style, wording, and preferences between " +
            "workable phrasings are not defects. An empty list is the expected answer for a sound stage.",
          { schema: FINDINGS, label: "review:" + stage.name + ":r" + round, phase: "Stages" },
        );
        const found = (rev && rev.findings) || [];
        if (found.length === 0) { clean = true; break; }
        log2("review round " + round + ": " + found.length + " finding(s)");
        await agent(
          "Fix the defects a review found in one stage's diff.\n\n" + CTX +
            "\nStage: " + stage.name + "\nDefects:\n" + JSON.stringify(found, null, 2) +
            "\n\nMake the smallest edit that corrects each. Do NOT edit the proposal. Do NOT hand-edit " +
            "anything a migration pass wrote: if the defect is in pass output, that is a defect in the " +
            "pass or its register, so report it rather than correcting the tree, because correcting it " +
            "here puts the tree and the register out of step.\n\n" + SPEC_RULES,
          { label: "fix:" + stage.name + ":r" + round, phase: "Stages" },
        );
      }
      record.ops.push({ op: "review", rounds: round, clean });
      if (!clean) {
        return {
          status: "review-unresolved",
          stage: stage.name,
          reason: "the stage's review did not come back clean within its round budget.",
          history,
        };
      }
    }
  }

  if (stage.commit) {
    await agent(
      "Commit the work of one stage.\n\n" + CTX +
        "\nStage: " + stage.name + "\nWhat it did: " + (stage.commit.what || stage.name) +
        "\n\nRun `git status --porcelain` and `git diff --stat`, stage everything this run changed, and " +
        "commit on the current branch. Write the message in the repository's convention (read " +
        "`git log --oneline -5` first), describing the behaviour the tree now has. The message references " +
        "durable sources only: it may name the proposal's file path, and it MUST NOT carry the " +
        "proposal's internal labels, which are its change or section ids, decision ids, review pass " +
        "numbers, or a step that exists only in the proposal. Do not amend an existing commit.",
      { label: "commit:" + stage.name, phase: "Stages" },
    );
    log2("committed");
  }

  history.push(record);
  if (stopAfter && stage.name === stopAfter) {
    return { status: "stopped-after", stage: stage.name, history };
  }
}

return { status: "stages-complete", stagesRun: history.length, history };
