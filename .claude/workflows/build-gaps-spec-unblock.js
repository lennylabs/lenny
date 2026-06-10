export const meta = {
  name: "build-gaps-spec-unblock",
  description:
    "Loop: run close-build-gaps.sh to implement OPEN BUILD-GAPS.md findings that reference an approved spec proposal (spec via spec-apply + full code blast radius + tests), commit and push, until none remain",
  whenToUse:
    "Drain OPEN BUILD-GAPS.md findings backed by an approved spec proposal — apply each proposal's spec edits and full code blast radius, run its tests to green, close the finding, commit and push, repeat",
};

// Usage — every iteration commits and pushes, so run only with explicit intent:
//
//   Workflow({ name: "build-gaps-spec-unblock", args: {
//     date: "YYYY-MM-DD",      // required (logging only; scripts cannot call Date)
//     repoRoot: "/abs/path",   // optional; defaults to this project
//     maxIterations: 5,        // optional; safety cap on the outer loop
//     buildMaxIter: 0          // optional; >0 passes --max-iter to close-build-gaps.sh
//   }})
//
// Proposals are generated, reviewed, and approved OUTSIDE this workflow (the
// spec-proposal and spec-apply skills, signed off by a human). This workflow
// neither writes nor approves proposals, and never opens, re-opens, or creates
// a finding — it only CLOSES findings that already reference an approved
// proposal. The per-finding work lives in close-build-gaps.sh, the single
// engine: it applies the proposal's spec edits with the spec-apply skill,
// implements the entire spec change's blast radius across the codebase, writes
// and runs tests to green, and marks the finding CLOSED. close-build-gaps.sh
// loops internally across batches until no qualifying finding remains and then
// emits the NO-QUALIFYING-FINDINGS sentinel. This workflow runs that script
// (detached, since it can run for hours), commits and pushes its results, and
// re-runs it (a bounded safety net for a crashed or timed-out script run) until
// a run reports the sentinel.

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.date) {
  throw new Error("args.date is required (YYYY-MM-DD; workflow scripts cannot call Date)");
}
const repo = input.repoRoot || "/Users/joan/projects/lenny";
const date = input.date;
const maxIterations = input.maxIterations || 5;
const buildMaxIter = input.buildMaxIter || 0;

const RUN = {
  type: "object",
  required: ["closedCount", "noQualifyingRemain", "committed", "pushed"],
  properties: {
    closedCount: {
      type: "number",
      description: "findings the script marked CLOSED this run",
    },
    closedFindings: { type: "array", items: { type: "string" } },
    noQualifyingRemain: {
      type: "boolean",
      description:
        "the script's final session emitted NO-QUALIFYING-FINDINGS — no OPEN finding references an approved proposal",
    },
    deferredNeedsOperator: {
      type: "array",
      items: { type: "string" },
      description: "findings the script deferred on a NEEDS-OPERATOR resource",
    },
    committed: { type: "boolean" },
    pushed: { type: "boolean" },
    scriptExit: { type: "number" },
    notes: { type: "string" },
  },
};

function runPrompt(iter) {
  const flag =
    " --mode proposals" + (buildMaxIter > 0 ? " --max-iter " + buildMaxIter : "");
  return (
    "Run the approved-proposal implementation loop and land its results. Work in " +
    repo +
    ". Outer iteration " +
    iter +
    " of " +
    maxIterations +
    ".\n\n" +
    "1. Run " +
    repo +
    "/close-build-gaps.sh" +
    flag +
    " to completion. It implements every OPEN BUILD-GAPS.md finding that references an approved spec proposal — applying the proposal's spec edits via the spec-apply skill, implementing the full spec-change blast radius in code, writing and running tests to green, and marking the finding CLOSED — looping internally across batches until no qualifying finding remains. It can run for hours, and harness process-tree timeouts kill ordinary background children, so launch it DETACHED:\n" +
    "     cd " +
    repo +
    " && nohup setsid ./close-build-gaps.sh" +
    flag +
    " > tmp/unblock-driver.log 2>&1 < /dev/null & echo $!\n" +
    "   Then wait for natural exit: the script appends a line containing 'FINAL' to " +
    repo +
    "/tmp/summary.log when it finishes, and the launched PID disappears. Wait efficiently — use the Monitor tool when available, otherwise infrequent bounded checks no more than once every few minutes; never spin. Do not kill the script; wait for natural exit.\n" +
    "2. Read the outcome from the script's own logs; do not re-derive it or edit any file to determine it. Read the tail of " +
    repo +
    "/tmp/summary.log and " +
    repo +
    "/tmp/close-build-gaps.log. closedCount and closedFindings come from the per-batch summaries (the FINAL line's closed count is cumulative across the whole repo, so count this run's newly-CLOSED IDs from the batch summaries instead). noQualifyingRemain is true when the script's last session summary is the single token NO-QUALIFYING-FINDINGS — the script's signal that no OPEN finding references an approved proposal. Collect any NEEDS-OPERATOR lines into deferredNeedsOperator. scriptExit is the script's exit code.\n" +
    "3. The script commits per batch. If any tracked changes remain uncommitted, commit them on the current branch following the repository's commit conventions (read git log --oneline -5 first). Then run git push; on push failure record the error verbatim in notes, set pushed false, and leave the commits local.\n\n" +
    "HARD CONSTRAINT: do NOT edit BUILD-GAPS.md, any file under proposals/, or any spec or code file yourself — close-build-gaps.sh owns all of that, and it alone decides what closes. Never open or re-open a finding. Your job is to run the script, read its result from the logs, commit any stragglers, and push."
  );
}

const iterations = [];
let exhausted = false;
let aborted = null;
let stall = 0;
let iter = 0;

while (iter < maxIterations) {
  iter++;
  phase("Iteration " + iter + ": implement approved-proposal findings");
  const run = await agent(runPrompt(iter), {
    schema: RUN,
    label: "i" + iter + ":run",
  });
  if (!run) {
    aborted = "run agent failed in iteration " + iter;
    break;
  }
  iterations.push({
    iteration: iter,
    closedCount: run.closedCount,
    closedFindings: run.closedFindings || [],
    noQualifyingRemain: run.noQualifyingRemain,
    deferredNeedsOperator: run.deferredNeedsOperator || [],
    pushed: run.pushed,
    scriptExit: run.scriptExit,
  });
  log(
    "Iteration " +
      iter +
      ": closed " +
      run.closedCount +
      " (pushed: " +
      run.pushed +
      ", noQualifyingRemain: " +
      run.noQualifyingRemain +
      ")",
  );

  if (run.noQualifyingRemain) {
    exhausted = true;
    break;
  }

  // Stall guard: a run that closed nothing and did not reach the
  // no-qualifying sentinel made no progress (a crashed script, or a
  // remainder that is all NEEDS-OPERATOR). Re-run once, then stop rather
  // than burn the whole iteration budget spinning.
  if (run.closedCount === 0) {
    stall++;
    if (stall >= 2) {
      aborted =
        "two consecutive runs closed nothing without reaching the no-qualifying sentinel; stopping to avoid a spin (inspect tmp/summary.log — likely a script failure or a NEEDS-OPERATOR-only remainder)";
      break;
    }
  } else {
    stall = 0;
  }
}

return {
  status: aborted ? "aborted" : exhausted ? "exhausted" : "max-iterations",
  abortReason: aborted || undefined,
  iterationsRun: iter,
  iterations,
};
