export const meta = {
  name: "build-gaps-spec-unblock",
  description:
    "Loop: run close-build-gaps.sh, then turn the first spec-gated DEFERRED finding in BUILD-GAPS.md into an approved spec proposal and re-open the finding, until no spec-gated DEFERRED finding remains",
  whenToUse:
    "Drain spec-modification-gated DEFERRED findings from BUILD-GAPS.md autonomously (build, propose, resolve decisions, approve, re-open, commit, push, repeat)",
};

// Usage — every iteration commits and pushes, so run only with explicit intent:
//
//   Workflow({ name: "build-gaps-spec-unblock", args: {
//     date: "YYYY-MM-DD",        // required; workflow scripts cannot call Date
//     repoRoot: "/abs/path",     // optional; defaults to this project
//     maxIterations: 10,         // optional; safety cap on loop iterations
//     maxReviewRounds: 8,        // optional; forwarded to the spec-proposal child
//     buildMaxIter: 0            // optional; >0 passes --max-iter to close-build-gaps.sh
//   }})
//
// Each iteration:
//   1. Runs close-build-gaps.sh to completion, then commits and pushes its results.
//   2. Selects the first DEFERRED finding in BUILD-GAPS.md whose own defer reason
//      requires a spec/ modification, skipping findings already carrying a
//      pipeline marker or a proposals/ reference (loop-termination guarantee:
//      every processed finding gains one or the other).
//   3. Generates a proposal for it via the spec-proposal named workflow
//      (.claude/workflows/spec-proposal.js, the canonical script of the
//      spec-proposal skill).
//   4. On a converged proposal: resolves its open decisions (pros and cons per
//      option; selects the simplest option that solves the problem and aligns
//      with spec precedent), marks the proposal approved, updates and re-opens
//      the finding with an explicit apply-the-spec-changes instruction, commits,
//      and pushes. On a not-viable or non-converged outcome: annotates the
//      finding so it is skipped on later iterations, commits, and pushes.
// The loop exits when no qualifying DEFERRED finding remains, or at the
// iteration cap.

let input = args;
if (typeof input === "string") {
  input = JSON.parse(input);
}
if (!input || !input.date) {
  throw new Error("args.date is required (YYYY-MM-DD; workflow scripts cannot call Date)");
}
const repo = input.repoRoot || "/Users/joan/projects/lenny";
const date = input.date;
const maxIterations = input.maxIterations || 10;
const maxReviewRounds = input.maxReviewRounds || 8;
const buildMaxIter = input.buildMaxIter || 0;

const CLOSE = {
  type: "object",
  required: ["summary", "committed", "pushed"],
  properties: {
    summary: { type: "string" },
    exitCode: { type: "number" },
    committed: { type: "boolean" },
    pushed: { type: "boolean" },
    notes: { type: "string" },
  },
};

const SELECT = {
  type: "object",
  required: ["found"],
  properties: {
    found: { type: "boolean" },
    findingId: { type: "string" },
    heading: { type: "string", description: "The finding's heading line, verbatim" },
    problem: {
      type: "string",
      description: "One-to-three-paragraph problem dossier for the proposal pipeline",
    },
    context: {
      type: "string",
      description: "Citation leads: spec file:line, code file:line, finding IDs, defer-reason quotes",
    },
    relatedFindingIds: {
      type: "array",
      items: { type: "string" },
      description: "Other DEFERRED findings blocked transitively on the selected finding's spec gap",
    },
    nextNumber: { type: "string" },
    exemplar: { type: "string" },
  },
};

const DECISIONS = {
  type: "object",
  required: ["decisions"],
  properties: {
    decisions: {
      type: "array",
      items: {
        type: "object",
        required: ["decision", "options", "selected", "rationale"],
        properties: {
          decision: { type: "string" },
          options: {
            type: "array",
            items: {
              type: "object",
              required: ["label", "pros", "cons"],
              properties: {
                label: { type: "string" },
                pros: { type: "array", items: { type: "string" } },
                cons: { type: "array", items: { type: "string" } },
              },
            },
          },
          selected: { type: "string" },
          rationale: { type: "string" },
        },
      },
    },
  },
};

const UPDATE = {
  type: "object",
  required: ["reopened", "committed", "pushed"],
  properties: {
    reopened: { type: "array", items: { type: "string" } },
    committed: { type: "boolean" },
    pushed: { type: "boolean" },
    commitMessage: { type: "string" },
    notes: { type: "string" },
  },
};

function closeGapsPrompt(iter) {
  return (
    "Run the repository's build-gap closure script and land its results. Work in " +
    repo +
    ". Iteration " +
    iter +
    " of an autonomous pipeline.\n\n" +
    "1. Run " +
    repo +
    "/close-build-gaps.sh" +
    (buildMaxIter > 0 ? " --max-iter " + buildMaxIter : "") +
    " to completion. It can run far longer than any single foreground Bash call, and harness process-tree timeouts can kill ordinary background children, so launch it DETACHED: `cd " +
    repo +
    " && nohup setsid ./close-build-gaps.sh" +
    (buildMaxIter > 0 ? " --max-iter " + buildMaxIter : "") +
    " > tmp/close-build-gaps-driver.log 2>&1 < /dev/null & echo $!`. Then wait for it to finish: it appends a line containing 'FINAL' to " +
    repo +
    "/tmp/summary.log on exit, and the launched PID disappears. Wait efficiently — use the Monitor tool when available, or infrequent bounded checks (no more than one check every few minutes; never spin). Do not kill the script; wait for natural exit. Summarize what it did from the tail of " +
    repo +
    "/tmp/summary.log and " +
    repo +
    "/tmp/close-build-gaps.log, and record the FINAL line's closed/open counts in notes.\n" +
    "2. After it exits, check git status. The script may commit on its own; if uncommitted changes to tracked files remain, commit them on the current branch with a message following the repository's recent commit conventions (read git log --oneline -5 first). Leave unrelated untracked scratch files alone.\n" +
    "3. Run git push. If the push fails, record the error verbatim in notes, set pushed false, and leave the commits local.\n" +
    "A non-zero exit code from the script is recorded, not fatal; the pipeline continues either way."
  );
}

function selectPrompt(iter) {
  return (
    "Select the first spec-gated DEFERRED finding in " +
    repo +
    "/BUILD-GAPS.md. The file is very large; use Grep for heading lines and targeted Read offsets, never read the whole file. Iteration " +
    iter +
    ".\n\nMethod:\n" +
    "1. Grep for heading lines containing DEFERRED (findings render as '### - [-] <ID> — <title> [Severity] — DEFERRED'; tolerate stale checkbox variants). Process candidates in file order.\n" +
    "2. For each candidate, read its body (from the heading to the next '### ' heading) and qualify it:\n" +
    "   - QUALIFIES when its own defer reason requires a modification to spec/: markers include 'spec change required', a rule-P or rule-B deferral naming a spec reconciliation, a section-vs-section spec contradiction, an undefined spec surface, or 'a spec/ edit a hook forbids'.\n" +
    "   - SKIP findings deferred on infrastructure, environment, or cluster availability rather than on spec content.\n" +
    "   - SKIP findings whose body already references a path under proposals/ or contains the marker 'automated gap pipeline' (already processed by this pipeline).\n" +
    "   - SKIP findings whose body says they are blocked transitively on ANOTHER finding's spec gap; when you select a root finding, collect such dependents' IDs into relatedFindingIds instead.\n" +
    "3. Select the FIRST qualifying finding in file order. Return found: false only when no qualifying finding exists.\n" +
    "4. List " +
    repo +
    "/proposals/ and compute nextNumber (the highest NNNN_ numeric prefix among numbered files, plus one, zero-padded to four digits; ignore unnumbered files) and exemplar (the repo-relative path of the highest-numbered proposal).\n\n" +
    "For the selected finding, write the problem dossier the proposal pipeline will receive: one to three paragraphs in complete sentences stating what the spec requires, why it cannot be satisfied as written (the contradiction or gap), and what the finding needs, grounded in the finding body's own citations. Put every citation lead (spec file:line, code file:line, finding IDs, defer-reason quotes) into context. The pipeline re-verifies everything, so leads need not be re-proven here."
  );
}

function decisionsPrompt(proposalPath) {
  return (
    "Finalize the decisions and status of a converged spec proposal.\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    proposalPath +
    ". Never modify anything under spec/, docs/, pkg/, charts/, or schemas/.\n\n" +
    "1. Read the proposal fully. Locate any open-decisions section ('Open decisions for review' or similar).\n" +
    "2. For each open decision: identify the options, including any the section implies without enumerating; investigate how the spec handles similar cases (Grep spec/ for the closest precedents and read them); write out each option's pros and cons; select the option that is simplest while BOTH solving the stated problem AND aligning with how the spec handles similar cases. When simplicity and precedent-alignment conflict, precedent-alignment wins.\n" +
    "3. Update the proposal: convert the open-decisions section into a resolved-decisions record stating the selection, the date (" +
    date +
    "), the options with their pros and cons, and the rationale; propagate each selection into every section that depends on it (design sections, staged spec edits, summary tables, constant comments, tests, and the files-touched section) using exactly the same predicate and wording everywhere so no drift is introduced; follow " +
    repo +
    "/.claude/rules/doc-style.md.\n" +
    '4. Update the Status bullet\'s leading state to: "Approved for implementation as written (' +
    date +
    '; automated gap pipeline)." Preserve the verified-convergence clause and any other clauses that remain true.\n' +
    "If the proposal has no open decisions, perform only step 4 and return an empty decisions array.\n" +
    "Return every decision with its options, pros, cons, selection, and rationale."
  );
}

function updatePrompt(sel, proposalPath, relProposal) {
  return (
    "Hand off an approved spec proposal in BUILD-GAPS.md, then commit and push. Work in " +
    repo +
    ".\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    repo +
    "/BUILD-GAPS.md. The proposal at " +
    proposalPath +
    " is already final; commit it alongside, unmodified.\n\n" +
    "1. Find the finding " +
    sel.findingId +
    " (heading: " +
    sel.heading +
    "). Append a dated entry to its body titled '**Unblocked " +
    date +
    " — heading DEFERRED → OPEN; approved proposal staged (automated gap pipeline).**' The entry MUST: reference " +
    relProposal +
    " explicitly; state that the proposal MUST be implemented as written and that none of its recorded decisions are to be re-litigated, the proposal text being authoritative wherever it and this finding differ; explicitly instruct the reader to FIRST apply the proposal's staged spec changes with the spec-apply skill (invoke the Skill tool with skill spec-apply and the proposal path as the argument; never hand-edit spec/) and commit the applied spec edits, and only THEN implement the code, observability, and tests the proposal's detailed-design and testing sections define, per rule B of the build loop; note that the spec edits are pre-authored and reviewed, which satisfies the prior spec-change-required defer reason.\n" +
    "2. Flip the finding's heading checkbox to '[ ]' and its trailing marker from 'DEFERRED' to 'OPEN'.\n" +
    "3. Related findings possibly unblocked by the same proposal: " +
    JSON.stringify(sel.relatedFindingIds || []) +
    ". For each that is still DEFERRED AND that the proposal actually resolves (verify by reading the proposal's findings-closed section and the related finding's body), append a shorter dated entry referencing the proposal and the root finding " +
    sel.findingId +
    ", instructing implementation under the same batch, and flip its heading to OPEN as well. Skip any the proposal does not resolve.\n" +
    "4. Commit BUILD-GAPS.md and the proposal file together on the current branch with a message following the repository's recent commit conventions (for example '" +
    sel.findingId +
    ": stage approved proposal …; re-open …'). Then run git push; on failure record the error verbatim in notes, set pushed false, and leave the commit local.\n" +
    "Return the IDs of every finding you re-opened."
  );
}

function annotatePrompt(sel, outcome, proposalPath) {
  return (
    "Record a non-actionable proposal-pipeline outcome in BUILD-GAPS.md, then commit and push. Work in " +
    repo +
    ".\n\n" +
    "HARD CONSTRAINT: the only file you may edit is " +
    repo +
    "/BUILD-GAPS.md.\n\n" +
    "Find the finding " +
    sel.findingId +
    " (heading: " +
    sel.heading +
    ") and append a dated entry titled '**Re-triaged " +
    date +
    " (automated gap pipeline).**' recording this outcome verbatim, so later pipeline iterations skip the finding:\n" +
    outcome +
    "\n\nKeep the heading DEFERRED. " +
    (proposalPath
      ? "Commit BUILD-GAPS.md together with the draft proposal at " +
        proposalPath +
        " (unmodified) "
      : "Commit BUILD-GAPS.md ") +
    "on the current branch following the repository's commit conventions, then git push; on failure record the error in notes, set pushed false, and leave the commit local.\n" +
    "Return an empty reopened array."
  );
}

const iterations = [];
let exhausted = false;
let aborted = null;
let iter = 0;

while (iter < maxIterations) {
  iter++;
  const record = { iteration: iter };

  phase("Iteration " + iter + ": close build gaps");
  const gaps = await agent(closeGapsPrompt(iter), {
    schema: CLOSE,
    label: "i" + iter + ":close-gaps",
  });
  if (!gaps) {
    aborted = "close-gaps agent failed in iteration " + iter;
    iterations.push(record);
    break;
  }
  record.closeGaps = gaps;
  log("Iteration " + iter + ": close-build-gaps done (pushed: " + gaps.pushed + ")");

  phase("Iteration " + iter + ": select finding");
  const sel = await agent(selectPrompt(iter), {
    schema: SELECT,
    label: "i" + iter + ":select",
  });
  if (!sel) {
    aborted = "selection agent failed in iteration " + iter;
    iterations.push(record);
    break;
  }
  if (!sel.found) {
    exhausted = true;
    iterations.push(record);
    log("Iteration " + iter + ": no spec-gated DEFERRED finding remains; exiting loop");
    break;
  }
  const missing = ["findingId", "heading", "problem", "nextNumber", "exemplar"].filter(
    (k) => !sel[k],
  );
  if (missing.length > 0) {
    aborted =
      "selection agent returned found=true without " +
      missing.join(", ") +
      " in iteration " +
      iter;
    iterations.push(record);
    break;
  }
  record.finding = sel.findingId;
  record.relatedFindingIds = sel.relatedFindingIds || [];
  log("Iteration " + iter + ": selected " + sel.findingId);

  phase("Iteration " + iter + ": generate proposal (" + sel.findingId + ")");
  let gen;
  try {
    gen = await workflow("spec-proposal", {
      mode: "new",
      problem: sel.problem,
      context: sel.context,
      date,
      nextNumber: sel.nextNumber,
      exemplar: sel.exemplar,
      repoRoot: repo,
      maxReviewRounds,
    });
  } catch (e) {
    aborted =
      "spec-proposal child failed in iteration " + iter + ": " + (e && e.message);
    iterations.push(record);
    break;
  }
  record.proposalStatus = gen && gen.status;

  if (!gen || gen.status === "not-viable" || gen.status === "no-change-needed") {
    const outcome =
      "The spec-proposal pipeline did not produce a proposal. Status: " +
      (gen ? gen.status : "unknown") +
      ". Reason: " +
      ((gen && gen.reason) || "see pipeline output") +
      ". The finding's premises require re-examination before another attempt.";
    const note = await agent(annotatePrompt(sel, outcome, null), {
      schema: UPDATE,
      label: "i" + iter + ":annotate",
    });
    record.annotated = !!note;
    iterations.push(record);
    log("Iteration " + iter + ": " + sel.findingId + " not viable; annotated and continuing");
    continue;
  }

  record.proposalPath = gen.path;
  const relProposal = gen.path.startsWith(repo + "/")
    ? gen.path.slice(repo.length + 1)
    : gen.path;

  if (!gen.review || !gen.review.converged) {
    const outcome =
      "A draft proposal was written to " +
      relProposal +
      " but the adversarial review loop did not converge after " +
      (gen.review ? gen.review.rounds : "?") +
      " rounds. The draft requires human review before it can be approved; do not implement it as written.";
    const note = await agent(annotatePrompt(sel, outcome, gen.path), {
      schema: UPDATE,
      label: "i" + iter + ":annotate",
    });
    record.annotated = !!note;
    iterations.push(record);
    log("Iteration " + iter + ": proposal for " + sel.findingId + " did not converge; annotated and continuing");
    continue;
  }

  phase("Iteration " + iter + ": resolve decisions");
  const dec = await agent(decisionsPrompt(gen.path), {
    schema: DECISIONS,
    label: "i" + iter + ":decisions",
  });
  if (!dec) {
    aborted =
      "decision-resolution agent failed in iteration " +
      iter +
      "; proposal " +
      relProposal +
      " is converged but its status and the finding are not finalized";
    iterations.push(record);
    break;
  }
  record.decisions = dec.decisions;
  log(
    "Iteration " +
      iter +
      ": " +
      dec.decisions.length +
      " open decisions resolved; proposal approved",
  );

  phase("Iteration " + iter + ": update finding");
  const upd = await agent(updatePrompt(sel, gen.path, relProposal), {
    schema: UPDATE,
    label: "i" + iter + ":update-finding",
  });
  if (!upd) {
    aborted =
      "finding-update agent failed in iteration " +
      iter +
      "; proposal " +
      relProposal +
      " is approved but " +
      sel.findingId +
      " was not re-opened";
    iterations.push(record);
    break;
  }
  record.reopened = upd.reopened;
  record.pushed = upd.pushed;
  iterations.push(record);
  log(
    "Iteration " +
      iter +
      ": re-opened " +
      upd.reopened.join(", ") +
      " (pushed: " +
      upd.pushed +
      ")",
  );
}

return {
  status: aborted ? "aborted" : exhausted ? "exhausted" : "max-iterations",
  abortReason: aborted || undefined,
  iterationsRun: iter,
  iterations,
};
