// Implementation subworkflow for implement-proposal. Given an applied spec
// proposal, it identifies the blast radius, plans an ordered build
// sequence, then implements the sequence one step at a time. Each step is
// gated before the sequence advances: an implementer writes the code and
// tests, an independent agent verifies the step's tiers are green, and an
// adversarial review checks the step's diff conforms to the proposal's
// design. A step that does not reach green-and-conformant within
// maxStepAttempts aborts the sequence so a divergent or red step does not
// compound into its dependents. Periodically (every replanEvery steps, or
// after a step that struggled) a read-only critic checks whether the
// remaining plan still matches what landed and, on evidenced drift,
// re-plans the remaining steps forward-only (completed steps immutable).
// After the build, a final verification loop
// fixes and re-runs until the reached tiers are green, changed-line
// coverage meets the 80% floor, and every behavioral fix is pinned by a
// regression test that asserts the corrected outcome (not just line
// coverage); and a final cross-step design-conformance
// review of the cumulative diff catches anything the per-step reviews could
// not see, fixing any divergence before returning.
// It does NOT verify the spec is applied or close findings — the
// implement-proposal parent does that around this call.
//
// Invoked as a sub-step: workflow("implement-proposal-build", { proposalPath,
// date, repoRoot }). Runs only agents (no nested workflow()).
//
// MAINTENANCE: the implement-proposal skill (.claude/skills/implement-proposal)
// documents this subworkflow; keep its description in sync.

export const meta = {
  name: "implement-proposal-build",
  description:
    "Plan the blast radius and build sequence of an applied spec proposal, then implement it one step at a time with tests, then verify",
  phases: [
    { title: "Plan", detail: "blast radius + ordered build sequence, completeness-checked" },
    { title: "Build", detail: "implement each step in order; verify its tiers and adversarially review its diff before advancing; periodically re-plan the remaining steps on drift; abort if a step stays red or divergent" },
    { title: "Verify", detail: "run the reached tiers across the whole change, fixing until green and coverage meets the floor" },
    { title: "Review", detail: "final cross-step design-conformance review of the cumulative diff against the proposal, fix divergences" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath) {
  throw new Error("args.proposalPath is required");
}
// Required rather than defaulted: a default is one machine's checkout, and a
// workflow that silently runs against the wrong tree is worse than one that
// stops. change-proposal.js requires it for the same reason.
if (!input.repoRoot) throw new Error("args.repoRoot is required and missing");
const repo = input.repoRoot;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const maxPlanRounds = input.maxPlanRounds || 2;
// A caller-supplied build sequence, which replaces the planning phase outright.
// Planning exists to derive a sequence from the proposal, and once the
// proposal's checklist carries every step with its tiers and dependencies, that
// derivation re-reads a very large document to reproduce what is already
// written. A resumed run pays it again on every relaunch. When the caller
// already knows the sequence, it hands it over and the run starts building.
// The startup tick reconciliation still applies, so a supplied step whose box
// is ticked is skipped exactly as a derived one is.
const suppliedPlan =
  input.plan && Array.isArray(input.plan.steps) && input.plan.steps.length > 0 ? input.plan : null;
const maxStepAttempts = input.maxStepAttempts || 50;
// Consecutive dead agents (see the loop below) that stop the run. Small,
// because the condition it detects is an account or transport failure that a
// retry cannot clear, and every spin costs wall-clock for nothing.
const maxDeadAttempts = input.maxDeadAttempts || 3;
// Re-verify a step whose checklist box is already ticked instead of skipping
// it. Off by default: the tick is the pipeline's own record that the step went
// green and conformant, so re-checking costs two review agents per step and
// earns nothing on a healthy run. Worth turning on when the checklist may be
// optimistic, after an interrupted run or when the proposal changed once some
// steps had landed. The step's code is committed and its tiers passed when it
// landed, so the question worth re-asking is conformance rather than green.
const reverifyDoneSteps = !!input.reverifyDoneSteps;
// Steps that were ticked, failed re-verification, and had to be repaired. A
// step recorded as done that was not is worth surfacing rather than quietly
// fixing.
const reverifyRepaired = [];
// How often the stuck-finding introspection fires inside a step's fix loop.
// Every fifth attempt: a step that converges normally never reaches it, and a
// step that does not has already told us something is wrong.
const introspectEvery = input.introspectEvery || 5;
// Consecutive rounds that must have tried and failed before the judges may
// return "unproductive". That verdict says the loop will not do work that is
// legal and real, which is a claim about a pattern; one or two rounds that
// missed is trial and error, not a pattern. "unresolvable" carries no such
// minimum: whether the proposal contradicts the code is a fact about the two
// documents, and more rounds do not make it truer.
const minUnproductiveRounds = input.minUnproductiveRounds || 5;
// Divergences a human has already seen and accepted. The final review is
// whole-change and re-derives everything, so without this it re-raises a
// settled question every round and can never converge on a change that
// deliberately departs from its proposal. Each entry is quoted to the
// reviewers as already adjudicated.
const acceptedDivergences = (
  Array.isArray(input.acceptedDivergences)
    ? input.acceptedDivergences
    : input.acceptedDivergences
      ? [input.acceptedDivergences]
      : []
)
  .map((d) => String(d).trim())
  .filter(Boolean);
const ACCEPTED_BLOCK =
  acceptedDivergences.length === 0
    ? ""
    : "\n\nALREADY ADJUDICATED, AND NOT YOURS TO REOPEN. A human has reviewed each divergence below and " +
      "accepted it: the landed code is what should ship and the proposal is what is wrong, and correcting " +
      "the proposal is a separate act outside this run. They are recorded for that correction.\n" +
      acceptedDivergences.map((d, i) => i + 1 + ". " + d).join("\n") +
      "\n\nDo not report any of these, do not report a rephrasing or a near neighbour of one, and do not " +
      "treat the code as non-conformant because of one. Reporting an adjudicated divergence cannot advance " +
      "the run: no one may act on it here, so it only prevents the review from converging. Everything else " +
      "in the change is fully in scope and you should be no gentler with it.";
if (acceptedDivergences.length > 0) {
  log("Final review: " + acceptedDivergences.length + " accepted divergence(s) seeded; reviewers will not re-raise them");
}
// Findings judged unresolvable by any change the step may legally make. Each is
// recorded, suppressed from later review rounds of that step, written into the
// step's commit trail, and surfaced at the end of the run.
const stuckFindings = [];
const maxVerifyRounds = input.maxVerifyRounds || 25;
const maxReviewRounds = input.maxReviewRounds || 50;
const coverageFloor = input.coverageFloor || 80;
// Periodic plan-drift re-check during Build: every replanEvery completed
// steps (and after any step that took at least replanStruggleAttempts
// attempts), a read-only critic checks whether the remaining plan still
// matches reality; on evidenced drift, the remaining steps are re-planned
// (forward-only, completed steps immutable), bounded by maxReplans.
const replanEvery = input.replanEvery || 4;
const maxReplans = input.maxReplans || 6;
const replanStruggleAttempts = input.replanStruggleAttempts || 4;
// skipBuild: the change is already implemented and committed on the branch — skip
// the Plan and Build phases and run only the whole-change Verify and Review
// against HEAD. Used to resume a fully-built proposal at the verify/review point
// without re-walking (or re-planning) the build steps. baseRef is still computed
// at the top of the Build phase and the build loop simply iterates zero steps.
const skipBuild = !!input.skipBuild;

// A schema'd agent occasionally completes without calling StructuredOutput
// (returns prose after the nudge); the runtime throws and, uncaught, that one
// transient miss aborts the whole subworkflow. Retry the agent a few times on
// that specific error only; re-throw everything else unchanged. Used for the
// Verify and Review phase calls, where a late abort discards hours of work.
async function agentTry(p, o) {
  for (let i = 0; i < 4; i++) {
    try {
      return await agent(p, o);
    } catch (e) {
      const msg = String((e && e.message) || e);
      if (i < 3 && msg.indexOf("StructuredOutput") !== -1) {
        try {
          log("agent " + ((o && o.label) || "?") + ": StructuredOutput miss; retry " + (i + 1) + "/3");
        } catch (_) {}
        continue;
      }
      throw e;
    }
  }
}

// Before any tier above 1, check the environment. A cluster whose images were
// built from an earlier revision fails in a way that reads exactly like a code
// defect, and one run spent 1400 seconds waiting on pods that had already
// failed 2,741 times because the renderer passed a flag the image did not
// define. The full checklist is in .claude/rules/test-coverage.md.
const PREFLIGHT_NOTE =
  " ENVIRONMENT PREFLIGHT, before any tier above 1. The cluster and its images were built from an earlier " +
  "revision of this tree and drift from it. (a) Count the agent pods by phase " +
  "(`kubectl -n lenny-agents get pods --no-headers | awk '{print $3}' | sort | uniq -c`): a pile of Error " +
  "pods makes every label-selector wait run to its full timeout, so delete them with " +
  "`--field-selector=status.phase=Failed --wait=false` before starting. (b) If a container exits at once " +
  "with status 2, read the first line of its log: `flag provided but not defined` means the image predates " +
  "the pod-spec renderer and must be rebuilt and reloaded, not worked around in code. (c) A warm pool at " +
  "zero ready recreates failing pods indefinitely, so the pile rebuilds itself; read one of its pods' logs " +
  "before running anything. (d) Reap orphaned envtest processes with `pkill -f kubebuilder-envtest`. " +
  "(e) A test that creates cluster-scoped state must sweep it at START, not only in t.Cleanup, which does " +
  "not run when a process is killed; a fixture minting a unique name per run leaks a whole object per " +
  "killed run otherwise. (f) Treat a tier failure above 1 as an environment failure until you have ruled that out, and never " +
  "rebuild landed code to make a broken fixture pass. When the environment is broken and fixing it is out " +
  "of scope here, record the precondition and skip the case with a reason in " +
  "tests/registers/skip-reasons.yaml, then say so in your result; that is a correct outcome, not a failure." +
  " Read .claude/rules/test-coverage.md \"Environment preflight\" for the full checklist.";

const RULES =
  "Follow the project rules in " +
  repo +
  "/.claude/rules/code-best-practices.md (small single-purpose functions, reuse over duplication, wrapped errors, injected dependencies, context propagation, fail-closed security, `// spec:` citations, no backward-compat shims) and " +
  repo +
  "/.claude/rules/test-coverage.md (tests across every tier the change reaches, not unit alone; tier 0 and tier 1 always plus each higher tier the change touches; 80% changed-line coverage; the `// spec:` and `// diagnosis:` test conventions). REGRESSION TEST FOR EVERY BEHAVIORAL FIX: when this step CORRECTS existing behavior — a bug, a fail-open, a wrong status or error code, a missing or wrong authz/scope gate, a cross-tenant leak, an unwired or never-called path, a mislabeled state — it MUST add a test that ASSERTS the corrected behavior and is constructed so it would FAIL against the pre-fix code. Line coverage of the changed lines by a pre-existing happy-path test does NOT satisfy this: the test must exercise the corrected OUTCOME (the new code/status returned, the gate now rejecting, the path now firing), at the tier that owns it (a security fix → tier 9, a reliability/recovery fix → tier 7a/8, a wire/contract fix → tier 3, a state-machine fix → tier 2). Name the test for the behavior and the spec section or finding it pins. This is in addition to the coverage floor, not a substitute for it. REMOVE DEAD CODE: when the proposal eliminates a surface (a mode, field, RPC, frame, metric, enum value, function, struct, or whole file), delete it together with every code path, helper, test, fixture, schema entry, chart template, and identifier that becomes unreferenced as a result; never leave a removed surface compiling-but-dead or two implementations side by side. A removal is part of the change, not a follow-up. Do not hand-edit files under spec/ — the spec is already applied and the guard hook blocks direct writes. DO NOT EDIT THE PROPOSAL EITHER. The proposal is the authority this step is measured against, it is signed off by a human, and a step that rewrites it to match the code it just wrote has inverted the pipeline: it makes the conformance review vacuous and silently amends an approved design. This holds without exception. It is not relaxed by the proposal being wrong, by its design carrying a race, by it contradicting itself, or by it being unbuildable as written: none of those is a reason to edit it, and there is no circumstance in which this step edits that file. When the implementation ends up departing from what the proposal states, for any reason, record the departure in the `deviations` field of your result, naming what the proposal says, what you did instead, and why. Those entries are collected and reported to a human at the end of the run, which is where a proposal defect gets decided. The only edit any agent makes to the proposal file is ticking an implementation-checklist box, and a separate agent does that." +
  "\n\nNO PROPOSAL-INTERNAL IDENTIFIERS IN CODE, COMMENTS, TESTS, OR COMMIT MESSAGES: the proposal's own scaffolding labels name parts of the proposal document, not the shipped system, and mean nothing to someone reading the code later. NEVER write any of them into source, comments (including test-file comments and test names), or a git commit message: its change/section ids (e.g. `S1`, `S-A1`, `C-A2`, `CODE-A`, `CODE-D/D2`, `SPEC-D`), decision ids (e.g. `D1`, `D8`, `D9`, `RES-1`), review pass numbers (e.g. `Pass 7`), or a step/item/section number that exists only in the proposal's own numbering. Reference a DURABLE source instead: cite the spec section with the `// spec: §X.Y` convention, or describe the behavior directly. A numbered step or section the SPEC itself defines (for example a §5.2 scrub `step 7` that the spec numbers) IS a spec reference and may be cited by that number; the proposal's own build-sequence or change-list step (`step 3`, `S4`, `CODE-A`) is not — cite the spec section it implements instead. For a commit message, name the behavior and the spec section; the proposal file path (`proposals/NNNN_...md`) or the BUILD-GAPS finding id may be included for traceability, but never the internal change/decision/pass/step labels. This holds even when `git log -5` shows prior commits that used such labels: match the repository's format and tense, not that leak." +
  PREFLIGHT_NOTE +
  "\n\nPER-STEP VERIFICATION — must TEST the step, avoid the 180s no-progress watchdog, AND stay MEMORY-SAFE on a 16GB host (running heavy tests in the background or concurrently accumulates orphaned processes and OOM-crashes the whole machine, including this build). DO test every step; do NOT skip tests. Follow ALL of these: (1) DEFAULT to scoped FOREGROUND verification — NEVER use `run_in_background` for `go test`/`lenny-test`/envtest/integration/e2e UNLESS the tier is a single long-running command that will exceed the 180s watchdog without emitting output (see rule (6) for that case only); never more than one heavy job alive at once. (2) SCOPE every command to the changed packages, never the whole repo: `go build ./<changed-pkg>/...`, `go vet ./<changed-pkg>/...`, `golangci-lint run ./<changed-pkg>/...`, `go test ./<changed-pkg>/... -count=1 -p 2`. Scoped runs finish well under 180s (no watchdog stall) AND keep memory bounded; do NOT pipe through `| tail`/`| head` (let output stream). (3) For a tier-2 envtest package the step changes, run it FOREGROUND and SCOPED to that one package only (`go test ./<changed-envtest-pkg>/... -count=1 -p 1` with `KUBEBUILDER_ASSETS` set) so only ONE etcd+kube-apiserver pair is alive at a time; immediately after it returns, reap strays with `pkill -f kubebuilder-envtest 2>/dev/null` before continuing. (4) NEVER run `go test -race ./...` over the whole repo — the race detector uses ~10x memory; if you need `-race`, run it scoped to the one changed package only. (5) NEVER run whole-repo `lenny-test --max-tier unit`/`static` per step. A pre-existing whole-repo failure unrelated to this step's changed packages is not this step's failure. (6) LONG-SILENT TIER EXCEPTION: if and only if a single tier command will genuinely run longer than ~150s without emitting any output (a rare case for full component or integration tiers), you may launch it with `run_in_background: true` to a log file. In that case, poll for completion using the READ TOOL (read the log file path returned by the Bash tool) — do NOT use a Bash `tail -f`/`until` loop. A Bash poll loop depends on the task `.output` file remaining on disk; if the 180s watchdog kills the foreground Bash wrapper, the harness deletes that `.output` file and the poll loop hangs forever. The Read tool bypasses the task-output mechanism and reads the log file directly, making it immune to harness cleanup." +
  "\n\nBRANCH SAFETY (critical): you MUST stay on the current feature branch and commit ONLY to it. NEVER run `git checkout <branch-or-commit>`, `git switch`, `git reset --hard`, or `git branch -f` — switching the checkout has caused build commits to land on the wrong branch (a real, damaging failure). To inspect a historical commit or the pre-implementation baseline, use `git diff <SHA>..HEAD`, `git diff <SHA> -- <path>`, or `git show <SHA>:<path>`, which read history WITHOUT changing the working tree. Immediately before every `git commit`, confirm `git rev-parse --abbrev-ref HEAD` prints the feature branch (not `HEAD`/detached, and never `impl/v1-initial` or any base branch); if it does not, `git checkout <feature-branch>` to return before committing.";

// The proposal's Summary is the one section every agent in this phase reads. It
// carries the top-level changes, the decisions that are closed, and the traps, so
// an implementer building step nine orients the same way as one building step one.
// The script cannot read the proposal: the workflow sandbox has no `require` and
// no filesystem access, so the agent reads its own Summary.
const SUMMARY_BLOCK =
  "\n\nTHE PROPOSAL'S SUMMARY. Read the `## Summary` section of " +
  proposal +
  " before anything else. It states the top-level changes, the decisions that are closed and must not be " +
  "reopened, and the traps this change has already fallen into. A proposal written before that section " +
  "existed may not have one; when it is absent, read the Problem and Decisions sections in its place.\n";

const BLANKS_BLOCK =
  "\n\nBLANKS THE PROPOSAL LEAVES TO YOU. A proposal may delegate a detail rather than specify it, marked as " +
  "**IMPLEMENTOR'S CHOICE:** naming what is open and the constraint any answer must satisfy. When you reach " +
  "one: make the choice, satisfy the constraint, and record in your commit message and your result which " +
  "choice you made and how it satisfies the constraint. A marked blank is a delegation, so it is not a defect " +
  "and not a reason to stop. An UNMARKED gap is different: the proposal neither says what to do nor delegates " +
  "it, so report it rather than guessing.\n";

// Everything the per-step agents need beyond their own instructions.
const RULES_FULL = RULES + SUMMARY_BLOCK + BLANKS_BLOCK;

// One build step, shared by the initial plan and the tail re-plan.
const STEP_ITEM = {
  type: "object",
  required: ["id", "title", "work", "targets", "tiers"],
  properties: {
    id: { type: "string", description: "stable short id, e.g. S1" },
    title: { type: "string" },
    work: { type: "string", description: "what to implement in this step" },
    targets: { type: "array", items: { type: "string" }, description: "files or packages" },
    dependsOn: { type: "array", items: { type: "string" } },
    tiers: {
      type: "array",
      items: { type: "string" },
      description: "test tiers this step must create and run (e.g. unit, component, integration)",
    },
    specRefs: { type: "array", items: { type: "string" } },
    checklistStep: {
      type: "string",
      description:
        "the id of the proposal's implementation-checklist step this step carries across, when the proposal has a checklist",
    },
    alreadyDone: {
      type: "boolean",
      description:
        "the step's surface is already present in the tree; keep it in the sequence so dependencies still resolve, but build nothing",
    },
    needsIntent: {
      type: "boolean",
      description:
        "the step cannot be built literally as the checklist words it; the implementer works from the proposal's stated intent, recorded in work",
    },
    blocked: {
      type: "string",
      description:
        "why this step cannot be built at all, when even its intent could not be recovered; steps that do not depend on it still run",
    },
  },
};

const PLAN = {
  type: "object",
  required: ["blastRadius", "steps"],
  properties: {
    blastRadius: {
      type: "array",
      description:
        "every code/chart/schema surface the proposal touches, each as 'path or package — what changes and why'",
      items: { type: "string" },
    },
    steps: {
      type: "array",
      description: "ordered build sequence; earlier steps are prerequisites of later ones",
      items: STEP_ITEM,
    },
    risks: { type: "array", items: { type: "string" } },
    deviations: {
      type: "array",
      items: { type: "string" },
      description:
        "every place the proposal's implementation checklist did not match the tree: a step already done, a step re-sequenced because its prerequisite was elsewhere, a step whose deliverable is missing, or the absence of a checklist entirely. Reported so the proposal can be corrected; an imperfect checklist is expected and is not a reason to stop.",
    },
  },
};

// Plan-drift critic verdict: does the REMAINING plan still match reality?
const DRIFT = {
  type: "object",
  required: ["drift"],
  properties: {
    drift: { type: "boolean" },
    reasons: {
      type: "array",
      description:
        "concrete, evidenced ways the remaining plan no longer matches reality (a touched-but-unplanned surface, an orphaned removal, a now-redundant step, a backwards dependency)",
      items: { type: "string" },
    },
  },
};

// Forward-only re-plan of the remaining steps; completed steps are immutable.
const TAIL = {
  type: "object",
  required: ["steps"],
  properties: {
    steps: {
      type: "array",
      description: "the revised ordered sequence for the REMAINING work only (steps not yet built)",
      items: STEP_ITEM,
    },
    blastRadiusAdditions: {
      type: "array",
      description: "surfaces discovered during the build that the original blast radius missed",
      items: { type: "string" },
    },
    notes: { type: "string" },
  },
};

const CRITIQUE = {
  type: "object",
  required: ["complete", "gaps"],
  properties: {
    complete: { type: "boolean" },
    gaps: {
      type: "array",
      description: "parts of the proposal's blast radius the plan fails to implement, or missing test tiers",
      items: { type: "string" },
    },
  },
};

const STEP = {
  type: "object",
  required: ["implemented", "testsPassed", "tiersRun"],
  properties: {
    implemented: { type: "boolean" },
    filesChanged: { type: "array", items: { type: "string" } },
    testsAddedOrModified: { type: "array", items: { type: "string" } },
    tiersRun: { type: "array", items: { type: "string" } },
    testsPassed: { type: "boolean" },
    commit: { type: "string" },
    deviations: {
      type: "array",
      description:
        "Every departure of the landed code from what the proposal states, one entry each. The proposal is never edited to close one; it is recorded here and reported to a human at the end of the run.",
      items: {
        type: "object",
        required: ["proposalSays", "implementedInstead", "why"],
        properties: {
          proposalSays: { type: "string", description: "What the proposal states, and the section it states it in" },
          implementedInstead: { type: "string", description: "What the code does, with file:line" },
          why: { type: "string" },
        },
      },
    },
    notes: { type: "string" },
  },
};

const VERIFY = {
  type: "object",
  required: ["green", "tiersRun"],
  properties: {
    green: { type: "boolean" },
    tiersRun: { type: "array", items: { type: "string" } },
    changedLineCoverage: { type: "string" },
    failures: { type: "array", items: { type: "string" } },
    notes: { type: "string" },
  },
};

const REVIEW = {
  type: "object",
  required: ["findings"],
  properties: {
    findings: {
      type: "array",
      items: {
        type: "object",
        required: ["title", "where", "divergence", "fix"],
        properties: {
          title: { type: "string" },
          where: { type: "string", description: "file:line and the proposal section it diverges from" },
          divergence: { type: "string", description: "how the landed code diverges from the proposal's design" },
          fix: { type: "string" },
        },
      },
    },
  },
};

// A ticked checklist box is the run's own record that the step landed, written
// by the pipeline after that step went green and conformant. On a resumed or
// re-entered run it is the authority on what is already done: re-running a
// completed step re-does committed work and can undo the later steps built on
// top of it. Read here rather than inferred by the planner from the tree,
// because the planner infers a step is done from the surface being present,
// which is weaker evidence and which it can get wrong in either direction.
// The proposal is read-only to this phase and every agent is told so in the
// strongest terms the prompt can carry. An instruction is not an enforcement
// mechanism, so the run also looks: nothing blocks the edit, nothing reverts
// it, and if one happened it is reported to a human with its diff rather than
// silently kept or silently undone. Deciding what the proposal should have
// said is the human's call, and this report is what that decision is made on.
const PROPOSAL_EDITS = {
  type: "object",
  required: ["edited", "commits"],
  properties: {
    edited: { type: "boolean", description: "Whether any commit in the range touched the proposal file" },
    commits: {
      type: "array",
      description: "One entry per commit that touched the proposal, oldest first. Empty when none did.",
      items: {
        type: "object",
        required: ["sha", "subject", "whatChanged"],
        properties: {
          sha: { type: "string" },
          subject: { type: "string", description: "The commit subject line" },
          whatChanged: {
            type: "string",
            description:
              "What the edit did to the proposal's meaning, read from the diff: which section, what it said before, what it says now. Quote the changed sentences rather than paraphrasing them.",
          },
          statedReason: {
            type: "string",
            description: "The reason the commit message gives, verbatim, or an empty string when it gives none",
          },
        },
      },
    },
  },
};

const TICKED = {
  type: "object",
  required: ["ticked"],
  properties: {
    ticked: {
      type: "array",
      description: "The step id of every checklist line whose box is `[x]`, e.g. S1, S4. Empty when none are ticked or the proposal has no checklist.",
      items: { type: "string" },
    },
  },
};

// One judge's reading of whether a finding is stuck. A finding is STUCK when no
// change this step may legally make can resolve it: the code is right, the
// proposal is wrong, and the proposal is read-only to this phase. Suppressing
// one is a real cost — the next round stops reporting it — so the bar is
// unanimity at high confidence, and a single dissent or a single medium keeps
// the loop grinding instead.
const STUCK_VERDICT = {
  type: "object",
  required: ["verdict", "confidence", "reasoning"],
  properties: {
    verdict: {
      type: "string",
      enum: ["resolvable", "unresolvable", "unproductive"],
      description:
        "resolvable: a legal change closes this and the rounds show the loop converging on it. unresolvable: no legal change closes it, because the landed code is right and the proposal is wrong. unproductive: a legal change exists, but the rounds show this loop is not making it and further rounds would not either.",
    },
    confidence: { type: "string", enum: ["high", "medium", "low"] },
    findingTitle: {
      type: "string",
      description: "the exact title of the finding you judge, copied verbatim from the findings you were given. Empty when the verdict is resolvable.",
    },
    roundsMovedIt: {
      type: "string",
      description: "What the rounds actually did about this finding, named round by round from the commits you inspected: which rounds touched the files the finding names, and which produced motion elsewhere. This is the evidence for your verdict and is required whenever the verdict is not resolvable.",
    },
    whyCodeIsRight: { type: "string", description: "unresolvable only: the evidence that the landed code is correct, with file:line" },
    whyProposalIsWrong: { type: "string", description: "unresolvable only: what the proposal says that the code cannot satisfy, and where" },
    outstandingWork: { type: "string", description: "unproductive only: the concrete work that would actually close the finding, and why the rounds did not do it" },
    reasoning: { type: "string" },
  },
};

const SHA = {
  type: "object",
  required: ["sha"],
  properties: { sha: { type: "string" } },
};

// ---- Plan: blast radius + ordered build sequence, completeness-checked ----

// Only announce a planning phase when one actually runs. A caller-supplied
// sequence skips the planner, and opening a phase named "Plan" anyway makes
// the progress view claim work the run is not doing.
if (suppliedPlan) {
  log(
    "Build sequence supplied by the caller: " + suppliedPlan.steps.length +
      " step(s); the planning and plan-critique phases are skipped",
  );
} else {
  phase("Plan");
  log("Planning the blast radius and build sequence for " + proposal);
}
let plan = skipBuild
  ? { steps: [] }
  : suppliedPlan
    ? suppliedPlan
    : await agentTry(
  "Turn a proposal's implementation checklist into the build sequence for its code phase.\n\n" +
    "You are a read-only planner; do not edit any file. Work in " +
    repo +
    ".\n\nProposal: " +
    proposal +
    ". Its spec edits are ALREADY applied to spec/.\n\n" +
    "READ THE PROPOSAL'S SUMMARY AND ITS IMPLEMENTATION CHECKLIST FIRST. The checklist is an ordered list of " +
    "steps, each naming the staged deliverables it lands, the test tiers it must run, and the earlier steps it " +
    "depends on. It was written and maintained while the proposal was reviewed, so it is the sequence, and you " +
    "are not being asked to invent one. Carry its steps across in order, keeping their ids, their deliverable " +
    "ids, their tiers, and their dependencies.\n\n" +
    "A STEP WHOSE CHECKBOX IS ALREADY `[x]` IS DONE. The pipeline ticks a box only after that step went " +
    "green and design-conformant, so on a resumed run the tick is the record of work that already landed. " +
    "Carry such a step across with alreadyDone set and build nothing for it; re-doing it re-does committed " +
    "work and can undo the steps built on top of it. The run enforces this independently of your answer.\n\n" +
    "THEN CHECK IT AGAINST THE TREE, because a checklist written during review can be stale or wrong in three " +
    "ways, and the run must survive all three rather than stopping at them.\n" +
    "  A step whose surface is ALREADY PRESENT. Grep-confirm before you assume; a proposal can be partially " +
    "implemented from an interrupted run or from work that landed by another route. Mark such a step " +
    "alreadyDone with the evidence, and keep it in the sequence so the dependency graph stays intact.\n" +
    "  A step that DEPENDS ON SOMETHING ABSENT that the checklist says an earlier step provides. The checklist " +
    "is mis-ordered. Re-sequence locally: move the step after whatever genuinely provides its prerequisite, and " +
    "record the move in deviations. Do not stop.\n" +
    "  A step that CANNOT BE BUILT AS WRITTEN, because a deliverable it names does not exist in the proposal, or " +
    "its target is gone. Keep the step, mark it needsIntent, and say what the proposal's own text says the step " +
    "is for; the implementer will work from the intent. Only when the intent cannot be recovered either does the " +
    "step become blocked, and a blocked step does not stop the steps that do not depend on it.\n\n" +
    "A checklist that is imperfect is expected. Your job is to produce a runnable sequence from it and a clear " +
    "record of where it was wrong, so the proposal can be corrected afterwards. Never abandon the checklist and " +
    "derive your own order from scratch: the order encodes review decisions you cannot see.\n\n" +
    "IF THE PROPOSAL HAS NO IMPLEMENTATION CHECKLIST, say so in deviations and derive the sequence yourself, " +
    "the way a planner had to before checklists existed: map the blast radius by grepping spec/, pkg/, cmd/, " +
    "charts/, schemas/, and migrations/ for every surface the change touches and every surface it REMOVES, and " +
    "emit an ordered sequence with an explicit removal step for each eliminated mode, field, RPC, frame, metric, " +
    "enum value, function, or file, including the code, tests, fixtures, and templates orphaned by it.\n\n" +
    "Either way, produce blastRadius (one entry per surface the change touches) and the ordered steps, each with " +
    "its work, target files or packages, dependsOn, test tiers per " +
    repo +
    "/.claude/rules/test-coverage.md, and the spec sections it implements.",
  { schema: PLAN, label: "plan", phase: "Plan" },
);

for (let round = 1; !skipBuild && !suppliedPlan && round < maxPlanRounds; round++) {
  const critique = await agentTry(
    "Adversarially check whether a build plan covers the entire blast radius of an applied spec proposal.\n\n" +
      "You are a read-only critic; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      " (read every section). Plan under review:\n" +
      JSON.stringify(plan, null, 2) +
      "\n\nReport complete=false with specific gaps when the plan omits any part of the proposal's blast radius (a field, RPC, condition, metric, gate, schema, chart template, or call site the proposal specifies), omits a removal step for a surface the proposal eliminates (a removed mode, field, RPC, frame, metric, enum value, function, or file left in the tree, or code, tests, and fixtures orphaned by a removal), sequences a consumer before its prerequisite, or omits a test tier the change reaches per " +
      repo +
      "/.claude/rules/test-coverage.md. Report complete=true with an empty gaps array when the plan fully implements the proposal. Do not invent scope the proposal does not contain.",
    { schema: CRITIQUE, label: "plan-critique:r" + round, phase: "Plan" },
  );
  if (!critique || critique.complete || critique.gaps.length === 0) {
    log("Plan complete after " + round + " critique round(s)");
    break;
  }
  log("Plan revision " + round + ": closing " + critique.gaps.length + " gap(s)");
  plan = await agentTry(
    "Revise a build plan to close the gaps a completeness critic found.\n\n" +
      "You are a read-only planner; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      ". Current plan:\n" +
      JSON.stringify(plan, null, 2) +
      "\n\nGaps to close:\n" +
      critique.gaps.map((g) => "- " + g).join("\n") +
      "\n\nReturn the full revised plan (blast radius + ordered steps), incorporating the gaps and preserving the parts that were correct. Re-sequence if a gap is a prerequisite-ordering problem.",
    { schema: PLAN, label: "plan-revise:r" + round, phase: "Plan" },
  );
}

// Startup reconciliation: a step whose checklist box is already ticked is
// skipped, whatever the plan says about it.
if (!skipBuild && plan.steps.length > 0) {
  const ticks = await agentTry(
    "Read the implementation checklist in " + proposal + " and report which steps are already marked " +
      "complete. Run `grep -nE '^- \\[[ x]\\] \\*\\*S' " + proposal + "` and return the step id of every " +
      "line whose checkbox is `[x]` rather than `[ ]`. The id is the token right after the `**`, such as " +
      "`S1` or `S12`. Return ids only, and do not edit anything.",
    { schema: TICKED, label: "checklist-ticks", phase: "Plan", model: "haiku" },
  );
  const tickedSet = new Set(((ticks && ticks.ticked) || []).map((t) => String(t).trim()));
  if (tickedSet.size > 0) {
    let forced = 0;
    for (const st of plan.steps) {
      if (st.checklistStep && tickedSet.has(String(st.checklistStep).trim()) && !st.alreadyDone) {
        st.alreadyDone = true;
        forced++;
      }
    }
    log(
      "Checklist: " + tickedSet.size + " step(s) already ticked (" +
        Array.from(tickedSet).join(", ") + ")" +
        (forced > 0 ? "; " + forced + " of them were not marked done by the planner and are now skipped" : ""),
    );
  }
}

log("Build sequence: " + plan.steps.length + " steps");

// ---- Build: implement each step in order, gated by per-step verify + review ----
// Sequential: later steps depend on earlier ones and share the working
// tree, so steps must not run concurrently. Each step runs an inner loop —
// implement/fix, then an INDEPENDENT verify of the step's tiers, then an
// adversarial design-conformance review of the step's own diff — and only
// advances once the step is both green and review-clean. Catching a
// divergence at the step that introduced it is cheaper than catching it in
// the final whole-change review, and stops a wrong foundation from
// propagating into its dependents.

// Per-step review lenses, scoped to the single step's diff. Whole-change
// completeness is left to the final Review; here a surface this step adds
// for a later step to consume is explicitly out of scope.
// The memory and stall rules every test-running agent is held to. Extracted so
// the final gate states them identically to the per-attempt verifier rather
// than paraphrasing them into something weaker.
// For an agent that must look and not touch.
const READ_ONLY_NOTE =
  "You are READ-ONLY. Do not edit, create, revert, or delete any file, do not commit, and run no command " +
  "that writes. Inspect the tree and the git history and report a judgement.";

const MEMORY_SAFE_NOTE =
  " MEMORY-SAFE and no-stall: run every command in the FOREGROUND, one at a time, scoped to the changed " +
  "packages. Never use `run_in_background` for tests unless a single tier will exceed the 180s watchdog " +
  "without emitting output, and never run whole-repo `go test -race ./...` or `lenny-test --max-tier unit`: " +
  "orphaned or concurrent envtest etcd and kube-apiserver pairs, and the race detector's memory, OOM this " +
  "16GB host and take the build down with them. Run a tier-2 envtest package foreground and alone with " +
  "`-p 1` and `KUBEBUILDER_ASSETS` set, then reap strays with `pkill -f kubebuilder-envtest` before the " +
  "next one. Let output stream rather than piping it through `tail` or `head`.";

const STEP_REVIEW_LENSES = [
  {
    key: "conformance",
    text: "Lens: design conformance. Check that what this step builds matches the proposal's design for the sections it implements — the right component owning each write, the gates and predicates the design names, field placement on the correct object, the ordering the design requires, and defaults that agree with it. Passing tests do not excuse a divergence. Also flag any proposal-internal scaffolding label that leaked into this step's code, comments, test names, or commit message — a change/section id (`S1`, `CODE-A`, `SPEC-D`), decision id (`D8`, `D9`, `RES-1`), review pass number, or a step/item number that exists only in the proposal — where a `// spec: §X.Y` citation or a plain behavior description belongs; a step or section the spec itself numbers is fine.",
  },
  {
    key: "invariants",
    text: "Lens: invariants and edge cases. Check that this step enforces the invariants, ordering rules, and failure-mode handling the proposal names for its sections (a precondition-fenced write, a fail-closed gate, a one-writer rule, a crash-recovery path), and that each spec-named edge case for this step has a corresponding code path. Report any the code omits or implements incorrectly.",
  },
];

phase("Build");
// Capture the pre-implementation HEAD so Verify (coverage diff) and Review
// (design-conformance diff) measure exactly this run's changes. This SHA is
// embedded literally in every `git diff <baseRef>..HEAD` below, so it must be
// a real ref — fall back to a prose string and every diff is malformed. Retry
// a couple of times, then fail fast rather than proceed with broken diffs.
let baseRef = null;
for (let b = 0; b < 3 && !baseRef; b++) {
  const baseline = await agentTry(
    "Print the current git HEAD commit SHA in " +
      repo +
      " (run `git rev-parse HEAD`). Do not edit anything. Return it as {sha}.",
    { schema: SHA, label: b === 0 ? "baseline" : "baseline:retry" + b, phase: "Build" },
  );
  if (baseline && baseline.sha) baseRef = baseline.sha.trim();
}
if (!baseRef) {
  throw new Error("could not capture the pre-build HEAD SHA; aborting so coverage and review diffs are not run against a malformed ref");
}

const stepResults = [];
let priorContext = "";
let replanCount = 0;
const skippedSteps = [];
// The design-conformance review of one step's work, by every lens. Extracted
// because it runs in two places: inside the attempt loop against the diff that
// attempt just produced, and on its own against a step an earlier run already
// built and ticked. `ref` is the commit the step's work is measured from; for a
// re-verified step that is the run's base, because the step's commits predate
// this run.
// Three judges, three lenses, no sight of each other's verdicts. A finding is
// suppressed only when all three say stuck at high confidence. The failure
// direction matters: a loop wastes time visibly, while a wrongly suppressed
// finding ships a defect silently, so a single dissent keeps the loop running.
const STUCK_LENSES = [
  {
    key: "motion",
    text:
      "LENS: motion against the blocker. For each round, compare what the commit actually changed against " +
      "the files and lines the finding names. Decide how many rounds touched the finding's own subject at " +
      "all. Rounds that produced commits elsewhere while the finding's subject went untouched are motion " +
      "without progress, and a run of them is the signature of a loop that will not close it.",
  },
  {
    key: "remaining-work",
    text:
      "LENS: the size and legality of what remains. State concretely what would close the finding, then " +
      "judge two things about it. Is it legal for this step (code and tests only, never the proposal)? And " +
      "is it work the rounds have been attempting, or work they have been avoiding? A finding that needs a " +
      "substantial new test or a new mechanism, where every round instead reworded prose around it, is one " +
      "the loop is avoiding rather than approaching.",
  },
  {
    key: "forecast",
    text:
      "LENS: forecast. Assume the loop runs five more rounds exactly as it has been running. Say what you " +
      "expect to change. If you can name the specific commit that would close the finding and the rounds " +
      "are visibly converging on it, it is resolvable. If your honest forecast is five more rounds of the " +
      "same, say so, and say whether that is because no legal change exists or because the loop is not " +
      "making the one that does.",
  },
];

// What the fixer and the reviewers are told about a finding already judged
// unresolvable. Scoped to one step: it never silences anything elsewhere, and
// it dies with the step rather than persisting into a later run, because the
// tree a later run sees may differ.
function suppressedNote(step) {
  const mine = stuckFindings.filter((f) => f.step === step.id && f.kind !== "unproductive");
  if (mine.length === 0) return "";
  return (
    "\n\nALREADY JUDGED UNRESOLVABLE, AND NOT YOUR WORK. Every judge agreed that the finding(s) below " +
    "cannot be closed by any change this step may legally make: the " +
    "landed code is correct and the proposal is wrong, and the proposal is read-only to this phase. Each is " +
    "recorded and will be reported to a human at the end of the run. Do not act on them, do not report them " +
    "again, and do not treat leaving them alone as leaving the step unfinished:\n" +
    mine.map((f, i) => i + 1 + ". " + f.title + "\n   why the code is right: " + f.whyCodeIsRight +
      "\n   what the proposal gets wrong: " + f.whyProposalIsWrong).join("\n")
  );
}

// The round-by-round activity for one step: what each attempt claimed, what it
// committed, whether it verified green, and what the review said afterwards.
// This is the judges' primary evidence. It replaces a filter over stepResults,
// whose entries are only pushed when a step ENDS, so during the loop it
// returned the other steps' results and nothing at all about this one. The
// judges were being asked whether a loop was stuck while being shown no round
// of that loop.
// Trailing rounds that produced a commit and still came back with findings.
// Counted from the end, so a round that cleared the findings resets it: the
// loop demonstrably moved, and what came after is a fresh stall rather than a
// continuation of the old one.
function unproductiveRunLength(rounds) {
  let n = 0;
  for (let i = rounds.length - 1; i >= 0; i--) {
    const r = rounds[i];
    if (!r.commit || (r.findingTitles || []).length === 0) break;
    n++;
  }
  return n;
}

function formatRounds(rounds) {
  if (rounds.length === 0) return "(no rounds recorded yet)";
  return rounds
    .map((r) => {
      const lines = [
        "ROUND " + r.attempt + (r.fix ? " (fix)" : " (initial implementation)"),
        "  commit: " + (r.commit || "(none committed)"),
        "  the agent said it changed: " + ((r.filesChanged || []).join(", ") || "(nothing named)"),
        "  tests it added or modified: " + ((r.testsAddedOrModified || []).join(", ") || "(none)"),
        "  independent verify: " + (r.green ? "green" : "NOT green") +
          ((r.failures || []).length ? " — " + r.failures.join("; ") : ""),
        "  review findings after this round: " +
          ((r.findingTitles || []).length ? "" : "(none)"),
      ];
      (r.findingTitles || []).forEach((t, i) => lines.push("    " + (i + 1) + ". " + t));
      return lines.join("\n");
    })
    .join("\n\n");
}

async function judgeStuck(step, findings, rounds, attempt) {
  const runLength = unproductiveRunLength(rounds);
  const brief =
    "You judge whether a build step's fix loop can still close the findings against it.\n\n" +
    READ_ONLY_NOTE +
    "\n\nProposal: " + proposal + " (its spec edits are applied). Step " + step.id + ": " + step.title +
    "\nWork: " + step.work +
    "\nSections it implements: " + ((step.specRefs || []).join(", ") || "(none named)") +
    "\nTargets: " + ((step.targets || []).join(", ") || "(none named)") +
    "\n\nThis step has run " + attempt + " attempts without going clean.\n\n" +
    "DO THIS FIRST, BEFORE FORMING ANY VIEW. Read the round log below in order. For every round that " +
    "names a commit, run `git show --stat <sha>` and, where the stat is not enough, `git show <sha>`. " +
    "Compare what each commit actually changed against the files and lines the outstanding findings name. " +
    "You are establishing one fact: across these rounds, did the loop move the thing the finding is about, " +
    "or did it produce changes elsewhere while that thing stayed as it was. Answer the lens only after you " +
    "have read the commits. A judgment formed from the finding's text alone, without reading what the " +
    "rounds did, is the failure this step exists to avoid.\n\n" +
    "THE THREE VERDICTS.\n" +
    "  resolvable — a legal change closes the finding AND the rounds show the loop converging on it. This " +
    "is the default and the common case. A hard finding, a finding that recurred, or a finding several " +
    "reviewers reported is still resolvable.\n" +
    "  unresolvable — no legal change closes it. The fixer may change code and tests and may NEVER edit " +
    "the proposal, so when the code is right and the proposal is wrong, nothing legal closes the finding. " +
    "The reviewer reports it, the fixer cannot fix it, and the step loops forever.\n" +
    "  unproductive — a legal change exists and the loop is not making it. The reviewer is right, the code " +
    "genuinely falls short, and the work that would close it is real; but round after round produced " +
    "commits that did not touch it. Judge this on the rounds, not on the finding: the test is whether the " +
    "loop is approaching the work or circling it.\n\n" +
    "HOW LONG THIS HAS BEEN STALLED. The rounds below include " + runLength + " consecutive round(s) that " +
    "committed something and still came back with findings. \"unproductive\" requires at least " +
    minUnproductiveRounds + " such rounds, because it is a claim about a pattern rather than about a bad " +
    "round or two: a fix agent groping toward a hard change looks the same from one round as one that will " +
    "never get there, and only a run of them tells the two apart. At " + runLength + " round(s) you may " +
    (runLength >= minUnproductiveRounds
      ? "return it if the evidence supports it."
      : "NOT return it; answer resolvable or unresolvable on what you see.") +
    " This floor does not apply to \"unresolvable\", which is a fact about the code and the proposal " +
    "rather than about the loop.\n\n" +
    "The two non-resolvable verdicts have different consequences, so do not merge them. An unresolvable " +
    "finding is set aside and reported as a defect in the proposal. An unproductive one is NOT set aside: " +
    "the step stops and a human is told what work is outstanding, because the finding is real and setting " +
    "it aside would ship the gap in silence.\n\n" +
    "The findings still outstanding on this step:\n" + JSON.stringify(findings, null, 2) +
    "\n\nTHE ROUND LOG:\n" + formatRounds(rounds) +
    "\n\nCite file:line and commit SHAs for what you assert.";
  return (await parallel(
    STUCK_LENSES.map((l) => () =>
      agentTry(brief + "\n\n" + l.text, {
        schema: STUCK_VERDICT,
        label: "stuck:" + step.id + ":" + l.key + ":a" + attempt,
        phase: "Build",
      }),
    ),
  )).filter(Boolean);
}

async function reviewStep(step, ref, tag) {
  return await parallel(
      STEP_REVIEW_LENSES.map((l) => () =>
        agentTry(
          (ref
            ? "Adversarially review ONE just-implemented build step against the proposal's design.\n\n"
            : "Adversarially review ONE ALREADY-IMPLEMENTED build step against the proposal's design.\n\n") +
            "The proposal is your measuring stick, never your subject. Report only findings whose remedy changes the CODE. Never report one whose remedy edits, reverts, or restores any file under proposals/, even when you are confident the proposal is the thing that is wrong. Nothing filters such a finding out: it is handed to the fixer as written, so a finding that says to change the proposal becomes an instruction to change it. State the divergence against the code instead, and if the code is right and the proposal wrong, say so in the divergence text and propose no code change; a human reads that at the end of the run and decides what the proposal should have said.\n\n" +
            "Read the proposal at " +
            proposal +
            " (its spec edits are applied), focusing on the sections this step implements (" +
            ((step.specRefs || []).join(", ") || "the sections relevant to this step's work") +
            "), " + suppressedNote(step) + " " +
            (ref
              ? "and read ONLY this step's diff: `git diff " + ref + "..HEAD` in " + repo + "."
              : // No diff exists for this step. Its commits landed in an earlier
                // run, so they sit behind this run's base ref and `git diff
                // base..HEAD` would be empty at the top of the loop and would
                // carry other steps' work later in it. Either way it would not
                // show this step, and a reviewer handed nothing reports nothing,
                // which is a clean verdict that means only that it looked at
                // the wrong thing. Read the tree instead.
                "and read the CURRENT STATE OF THE TREE for what this step owns: " +
                ((step.targets || []).join(", ") || "the files and packages this step's work names") +
                ", in " + repo + ". There is no diff to read: this step was implemented and committed by an " +
                "EARLIER run, so its work is already part of the baseline and `git diff` against this run's " +
                "base shows nothing of it. Judge the code as it now stands against what the proposal " +
                "specifies, exactly as if you were seeing it for the first time.") +
            " You are read-only; report findings only.\n\n" +
            "Scope: judge only what THIS step is responsible for (its work: " +
            step.work +
            "). A surface this step ADDS that a LATER step is meant to consume or wire up is NOT a divergence here — do not flag 'unused' or 'never called' for something a later step will use. A finding is a place this step's landed code diverges from the proposal's design for the sections it implements. Not a style preference, not new scope the proposal does not contain, not a coverage gap. Cite file:line and the proposal section. Report an empty findings array when this step conforms.\n\n" +
            l.text,
          {
            schema: REVIEW,
            label: "review:" + step.id + ":" + l.key + ":" + tag,
            phase: "Build",
          },
        ),
      ),
    );

}

for (let i = 0; i < plan.steps.length; i++) {
  const step = plan.steps[i];
  // A step the planner found already present builds nothing. It stays in the
  // sequence so later steps' dependencies still resolve, and it is reported so
  // the proposal's checklist can be corrected.
  if (step.alreadyDone && !reverifyDoneSteps) {
    log("Step " + step.id + ": already present in the tree; nothing to build");
    skippedSteps.push({ id: step.id, why: "already present" });
    stepResults.push({ step: step.id, skipped: "already present" });
    continue;
  }
  // A blocked step does not stop the ones that do not depend on it. Only when
  // nothing remains runnable does the sequence end, which the tail check below
  // reports.
  if (step.blocked) {
    log("Step " + step.id + ": BLOCKED — " + step.blocked);
    skippedSteps.push({ id: step.id, why: step.blocked });
    stepResults.push({ step: step.id, blocked: step.blocked });
    continue;
  }
  const blockedDeps = (step.dependsOn || []).filter((d) =>
    stepResults.some((r) => r.step === d && r.blocked),
  );
  if (blockedDeps.length) {
    const why = "depends on blocked step(s) " + blockedDeps.join(", ");
    log("Step " + step.id + ": skipped, " + why);
    skippedSteps.push({ id: step.id, why });
    stepResults.push({ step: step.id, blocked: why });
    continue;
  }
  const stepHeader =
    "Step " +
    step.id +
    " (" +
    (i + 1) +
    " of " +
    plan.steps.length +
    "): " +
    step.title +
    "\nWork: " +
    step.work +
    "\nTargets: " +
    (step.targets || []).join(", ") +
    "\nTest tiers to create and run: " +
    (step.tiers || []).join(", ") +
    "\nSpec sections: " +
    (step.specRefs || []).join(", ");

  // HEAD at the start of this step, so the per-step verify and review see
  // only this step's commits and not the prior steps'.
  const stepStart = await agentTry(
    "Print the current git HEAD commit SHA in " +
      repo +
      " (`git rev-parse HEAD`). Do not edit anything. Return {sha}.",
    { schema: SHA, label: "build:" + step.id + ":base", phase: "Build" },
  );
  const stepRef = (stepStart && stepStart.sha) || baseRef;

  let res = null; // last implement/fix STEP result
  let stepGreen = false;
  let stepReviewClean = false;
  let stepFindings = [];
  // One entry per attempt, built as the attempt runs: what the agent said it
  // changed, what it committed, how the independent verify came back, and what
  // the review found afterwards. The stuck judges read this.
  const stepRounds = [];
  // Set when the judges agree the loop is not closing a real finding. The step
  // stops rather than ticking, and this carries the outstanding work out.
  let unproductiveStep = null;
  let issues = ""; // carried failures or divergences for the next attempt
  let attempt = 0;

  // A step an earlier run ticked is re-verified rather than rebuilt: its code
  // is committed and its tiers passed when it landed, so what is worth asking
  // again is whether it still matches the proposal. Measured from the run's
  // base, because the step's own commits predate this run. A clean result skips
  // the step as before; findings fall through into the loop below, which fixes,
  // re-runs the tiers, and re-reviews until the step is green and conformant —
  // the same bar a fresh step is held to.
  if (step.alreadyDone) {
    log("Step " + step.id + ": already ticked; re-verifying conformance and invariants");
    const rv = (await reviewStep(step, null, "reverify")).filter(Boolean);
    const rvRan = rv.length === STEP_REVIEW_LENSES.length;
    const rvFindings = rv.flatMap((r) => r.findings);
    if (rvRan && rvFindings.length === 0) {
      log("Step " + step.id + ": re-verified clean; moving on");
      skippedSteps.push({ id: step.id, why: "already present, re-verified clean" });
      stepResults.push({ step: step.id, skipped: "already present", reverified: true });
      continue;
    }
    log(
      "Step " + step.id + ": re-verify found " + rvFindings.length + " finding(s)" +
        (rvRan ? "" : " (a reviewer did not return, so conformance is unconfirmed)") + "; repairing",
    );
    stepFindings = rvFindings;
    issues = rvFindings.length > 0
      ? "This step was implemented by an earlier run and its checklist box is ticked, but a re-verification " +
        "found it no longer matches the proposal's design. Fix the code to match the proposal for this step " +
        "(do not change scope, do not touch spec/, do not edit the proposal). Its tests already exist, so " +
        "strengthen them where a finding has a runtime effect the current tests pass over, and keep every " +
        "test green:\n" + JSON.stringify(rvFindings, null, 2)
      : "This step was implemented by an earlier run, but a re-verification reviewer did not return, so its " +
        "conformance is unconfirmed. Re-check that this step's code matches the proposal's design for its " +
        "sections; fix any divergence and keep the tests green.";
    reverifyRepaired.push({ id: step.id, checklistStep: step.checklistStep, findings: rvFindings });
  }
  // A dead agent is not a failed attempt. agent() returns null when the
  // subagent never ran: the account hit a usage limit, or the call died on a
  // terminal API error after its own retries. That says nothing about the code
  // this step is writing, so consuming the step's attempt budget for it is
  // wrong twice over. It spins against a condition no retry can clear (one run
  // burned thirty attempts in a tight loop against a weekly limit that had
  // hours left on it), and it exhausts the budget a genuinely difficult step
  // needs, so the step aborts for the wrong reason and the log blames the
  // implementer. Dead attempts are counted separately and abort the run
  // quickly, because the operator's move is to wait or re-authenticate rather
  // than to let the loop keep trying.
  let deadAttempts = 0;
  // Whether the tree as it now stands has had a full tier pass. Every attempt
  // commits, so any attempt invalidates it. A step is never marked done on a
  // false value: when the lenses come back clean and this is false, the full
  // set runs as the final gate. That is what makes the scoped runs below safe —
  // they are early feedback, and the guarantee comes from the last full pass
  // running against the exact tree that gets marked done.
  let fullVerifyCurrent = false;
  // A re-verified step (reverifyDoneSteps) enters the loop with findings
  // already carried, so its first attempt is a fix rather than an initial
  // implementation and takes the scoped path like any other fix.
  const firstAttemptWasFix = !!issues;
  // Inner loop: implement/fix → verify → review, until green-and-conformant
  // or the attempt cap. Each iteration is one fix attempt.
  while (attempt < maxStepAttempts && !(stepGreen && stepReviewClean)) {
    attempt++;
    res = await agentTry(
      attempt === 1 && !issues
        ? "Implement one step of a build sequence for an applied spec proposal.\n\n" +
            "HARD CONSTRAINT: implement only this step. Do not start later steps. Do not edit spec/. Work in " +
            repo +
            ".\n\n" +
            "Proposal (authoritative for the change): " +
            proposal +
            ". Its spec edits are already in spec/; read the relevant sections.\n\n" +
            stepHeader +
            priorContext +
            "\n\nImplement the code for this step, create or modify its tests across the listed tiers (and any other tier this step reaches per the test-coverage rule), and RUN them: tier 0 (`go build ./...`, `go vet`, lint) and tier 1 always, plus each listed higher tier (bring infrastructure up with `lenny-test infra up` when a tier needs it). If this step CORRECTS existing behavior, add a regression test that asserts the corrected outcome and would fail against the pre-fix code (see RULES below) — not merely a test that line-covers the changed lines. Fix the code until the tests pass. Then commit this step on the current branch with a message in the repository's convention (read `git log --oneline -5`). " +
            RULES_FULL +
            "\n\nReturn whether you implemented it, the files changed, the tests added or modified, the tiers you ran, whether they passed, and the commit SHA. If a tier genuinely cannot run here (a cloud-only resource), say so in notes and set testsPassed from the tiers that can run."
        : "Continue one build step of an applied spec proposal that is not yet green-and-conformant.\n\n" +
            "HARD CONSTRAINT: work only on this step. Do not start later steps. Do not edit spec/. Work in " +
            repo +
            ".\n\n" +
            "Proposal (authoritative): " +
            proposal +
            " (spec edits already in spec/).\n\n" +
            stepHeader +
            "\n\nThe prior attempt left this step not done. Address this:\n" +
            issues +
            "\n\nFix the code (add or correct tests where the issue is a missing or wrong test; change the code " +
            "to match the proposal's design where the issue is a divergence), then commit on the current " +
            "branch.\n\nTESTS FOR THIS ATTEMPT ARE SCOPED TO THE FIX. Run tier 0 and tier 1 for the packages " +
            "this fix touches, plus only those of the step's tiers (" +
            ((step.tiers || []).join(", ") || "none beyond tier 0/1") +
            ") whose subject this fix actually changes. Skip a tier this fix cannot affect and name the ones " +
            "you skipped: a comment, a rename, or a doc line does not need an envtest or an integration tier " +
            "re-run, and those cost tens of minutes each. Err toward running one tier too many rather than " +
            "skipping one that mattered. This is not the final gate — the whole set runs again once the " +
            "design review comes back clean, so a tier skipped here is re-run before the step is marked " +
            "done. " +
            RULES_FULL +
            "\n\nReturn the step result with testsPassed reflecting the tiers that can run here.",
      { schema: STEP, label: "build:" + step.id + (attempt > 1 ? ":fix" + attempt : ""), phase: "Build" },
    );

    // A skipped or dead implementer (agent() === null) committed nothing. An
    // empty diff would otherwise pass the independent verify (`--changed`
    // selects nothing) and the review (no diff to find fault with) and
    // masquerade as a done step — then the success log would deref res.commit
    // on null. Treat it as a failed attempt and retry; if it persists, the
    // step aborts cleanly below.
    if (!res) {
      stepGreen = false;
      stepReviewClean = false;
      stepFindings = [];
      issues =
        "The implementer agent returned no result (it was skipped or errored). Re-implement this step from the proposal and its tests across the listed tiers, run them to green, then commit.";
      deadAttempts++;
      attempt--; // the agent never ran; do not spend the step's budget on it
      log(
        "Step " + step.id + ": implementer returned no result (" + deadAttempts +
          "/" + maxDeadAttempts + " dead attempt(s); attempt " + (attempt + 1) +
          "/" + maxStepAttempts + " not consumed)",
      );
      if (deadAttempts >= maxDeadAttempts) {
        log(
          "Step " + step.id + ": " + deadAttempts + " consecutive dead agent(s). " +
            "The subagents are not running, which no further retry fixes: the account is " +
            "rate-limited, out of credit, or the session needs re-authenticating. Stopping so " +
            "the run can be resumed once that clears; the completed steps are committed.",
        );
        break;
      }
      continue;
    }
    // The agent ran, so a later dead call is a fresh incident rather than a
    // continuation of this one.
    deadAttempts = 0;

    // Full tiers on the first implementation of a step; scoped on a fix, whose
    // blast radius is usually far narrower than the step's. The final gate
    // below restores the full set, so nothing is marked done on a scoped pass.
    const fullPass = attempt === 1 && !firstAttemptWasFix;
    // Independent verify: a different agent re-runs the step's tiers and
    // gates green. The implementer's self-report is advisory.
    const sv = await agentTry(
      "Independently verify ONE just-implemented build step of an applied spec proposal. You did not write this code.\n\n" +
        "Work in " +
        repo +
        ". Do not edit code; only run tests and report. Proposal: " +
        proposal +
        ".\n\n" +
        stepHeader +
        "\n\nThis step's changes are everything since " +
        stepRef +
        " (`git diff " +
        stepRef +
        "..HEAD`). Run SCOPED tier 0 (`go build ./<changed-pkg>/...`, `go vet ./<changed-pkg>/...`, `golangci-lint run ./<changed-pkg>/...`) and tier 1 (`go test ./<changed-pkg>/... -count=1`) for the changed packages, plus " +
        (fullPass
          ? "each higher tier this step must run: " +
            ((step.tiers || []).join(", ") || "(none beyond tier 0/1)")
          : "ONLY those of the step's higher tiers whose subject the LAST FIX changed, out of: " +
            ((step.tiers || []).join(", ") || "(none beyond tier 0/1)") +
            ". Read `git diff HEAD~1..HEAD` first to see what that fix was. Skip a tier it cannot affect and " +
            "list every tier you skipped, and why, in your notes: a comment, a rename, or a doc line does not " +
            "need an envtest or an integration tier re-run and those cost tens of minutes each. Err toward " +
            "running one tier too many rather than skipping one that mattered. This is not the final gate — " +
            "the whole set runs again once the design review is clean, before the step is marked done"
        ) +
        ". " + PREFLIGHT_NOTE + " MEMORY-SAFE + no-stall: DEFAULT to running every command in the FOREGROUND, one at a time, SCOPED to the changed packages — NEVER use `run_in_background` for tests unless a single long-running tier will exceed the 180s watchdog without output (see below). NEVER run whole-repo `go test -race ./...` or `lenny-test --max-tier unit` (orphaned/concurrent envtest etcd+kube-apiserver and the race detector OOM-crash this 16GB host). Run the scoped tier 0/1 commands above directly (no `| tail`; they finish in seconds, streaming so no watchdog stall). For a tier-2 envtest package this step changes, run it FOREGROUND scoped to that one package (`go test ./<changed-envtest-pkg>/... -count=1 -p 1` with `KUBEBUILDER_ASSETS` set) so only one etcd+apiserver is alive, then reap strays (`pkill -f kubebuilder-envtest 2>/dev/null`) before the next. Use `-p 2` (or `-p 1` for envtest/integration) to cap memory. LONG-SILENT TIER EXCEPTION: if and only if a single tier will genuinely exceed ~150s without any output, you may run it with `run_in_background: true` to a log file path — but then poll by reading that log file with the Read tool (not with a Bash `tail`/`until` loop; a Bash poll loop can hang forever if the harness deletes its `.output` file after the 180s watchdog fires). Set green=true only if every tier that can run here passes (skip only a tier that genuinely needs a cloud-only resource, noting it); a pre-existing whole-repo failure unrelated to this step's changed packages is not this step's failure. List any real failures precisely so they can be fixed. Coverage is checked once over the whole change at the end, not here. BRANCH SAFETY: never `git checkout` a branch or commit to compare against a baseline — use `git diff <SHA>..HEAD` or `git show <SHA>:<path>` (you only run tests and report; do not change the checkout or the current branch).",
      { schema: VERIFY, label: "verify:" + step.id + ":r" + attempt, phase: "Build" },
    );
    stepGreen = !!(sv && sv.green);
    // This attempt committed, so whatever full pass preceded it is stale. A
    // green full pass on THIS tree is the only thing that sets it true again.
    fullVerifyCurrent = fullPass && stepGreen;
    if (!stepGreen) {
      stepReviewClean = false;
      stepFindings = [];
      issues =
        "This step's tests are not green. Failures:\n" +
        (((sv && sv.failures) || []).map((f) => "- " + f).join("\n") || "- (verify reported not green without listing failures)") +
        ((sv && sv.notes) ? "\nVerifier notes: " + sv.notes : "");
      log("Step " + step.id + " attempt " + attempt + "/" + maxStepAttempts + ": tests not green");
      continue;
    }

    // Adversarial design-conformance review of THIS step's diff only.
    const reviewResults = await reviewStep(step, stepRef, "r" + attempt);
    // Fail closed: a reviewer that died (null) is not evidence of conformance.
    // Only declare the step review-clean when every reviewer ran and none
    // found a divergence.
    const liveReviews = reviewResults.filter(Boolean);
    const allReviewersRan = liveReviews.length === STEP_REVIEW_LENSES.length;
    stepFindings = liveReviews.flatMap((r) => r.findings);
    stepReviewClean = stepFindings.length === 0 && allReviewersRan;
    stepRounds.push({
      attempt,
      fix: attempt > 1 || firstAttemptWasFix,
      commit: res.commit || "",
      filesChanged: res.filesChanged || [],
      testsAddedOrModified: res.testsAddedOrModified || [],
      green: !!(sv && sv.green),
      failures: (sv && sv.failures) || [],
      findingTitles: stepFindings.map((f) => f.title).filter(Boolean),
    });
    log(
      "Step " +
        step.id +
        " attempt " +
        attempt +
        ": green, " +
        stepFindings.length +
        " design-conformance finding(s)" +
        (allReviewersRan ? "" : " (a reviewer did not return; not treated as clean)"),
    );
    // FINAL GATE. The lenses are clean, but if the tree reached this state
    // through scoped runs then no full tier pass has covered it. Run the whole
    // set now, against the exact tree about to be marked done. This is the
    // guarantee the scoped fix rounds borrow against: green here, or the
    // failures become the next attempt's work.
    if (stepReviewClean && !fullVerifyCurrent) {
      log("Step " + step.id + ": review clean after scoped runs; full tier pass as the final gate");
      const fg = await agentTry(
        "Run the FULL test set for one build step of an applied spec proposal and report whether it is green. " +
          "You did not write this code. Work in " +
          repo +
          ". Do not edit anything; only run tests and report.\n\n" +
          stepHeader +
          "\n\nThe step's design review is clean and its code is committed. Earlier rounds ran only the tiers " +
          "each fix could affect, so no single run has covered the tree as it now stands. Run tier 0 and " +
          "tier 1 for every package this step changed (`git diff " +
          stepRef +
          "..HEAD`), and every higher tier this step must run: " +
          ((step.tiers || []).join(", ") || "(none beyond tier 0/1)") +
          ". Skip none of them except a tier that genuinely needs a cloud-only resource, which you name. " +
          MEMORY_SAFE_NOTE + PREFLIGHT_NOTE +
          "\n\nReport green, the tiers you ran, the changed-line coverage if you measured it, and every failure.",
        { schema: VERIFY, label: "verify:" + step.id + ":final", phase: "Build" },
      );
      if (fg && fg.green) {
        fullVerifyCurrent = true;
        log("Step " + step.id + ": full tier pass green");
      } else {
        stepGreen = false;
        stepReviewClean = false;
        issues =
          "The design review is clean, but the full tier pass run against the finished tree is not green. " +
          "Earlier rounds ran only the tiers each fix could affect, so these failures are in tiers those " +
          "rounds skipped. Fix them and keep the design conformant:\n" +
          (((fg && fg.failures) || []).map((f) => "- " + f).join("\n") ||
            "- (the final verifier reported not green without listing failures)") +
          ((fg && fg.notes) ? "\nVerifier notes: " + fg.notes : "");
        log("Step " + step.id + ": full tier pass FAILED after a clean review; returning to the fix loop");
        continue;
      }
    }
    // Every introspectEvery attempts, ask whether this loop can still close its
    // findings. The judges read the round log and the commits before answering,
    // because the question is about the loop's behaviour rather than the
    // finding's merits, and a judge reasoning from the finding's text alone
    // reaches the wrong answer confidently.
    //
    // The bar is every judge agreeing on the same non-resolvable verdict at
    // medium or better. It was unanimous-at-high across three lenses that asked
    // three different questions, which made the narrowest lens the decider:
    // only what all three could see was ever detectable, and the two lenses
    // that judged correctness could not see a loop failing to do work that was
    // legal and real. These lenses read the same evidence and differ in how
    // they weigh it, so agreement between them means something.
    if (!stepReviewClean && stepFindings.length > 0 && attempt % introspectEvery === 0) {
      log("Step " + step.id + ": " + attempt + " attempts without converging; asking whether the loop can still close its findings");
      const runLength = unproductiveRunLength(stepRounds);
      const votes = await judgeStuck(step, stepFindings, stepRounds, attempt);
      const enough = (v) => v.confidence === "high" || v.confidence === "medium";
      const all = (verdict) =>
        votes.length === STUCK_LENSES.length &&
        votes.every((v) => v.verdict === verdict && enough(v));
      const summary = votes.map((v) => v.verdict + "/" + v.confidence).join(", ");
      const titles = votes.map((v) => (v.findingTitle || "").trim()).filter(Boolean);
      const title = titles[0] || (stepFindings[0] && stepFindings[0].title) || "";
      const pick = (k) => votes.map((v) => v[k]).filter(Boolean)[0] || "";

      if (all("unresolvable")) {
        // No legal change closes it: the proposal is the defect. Set it aside
        // so the loop can finish the rest of the step, and report it.
        stuckFindings.push({
          step: step.id,
          checklistStep: step.checklistStep,
          attempt,
          kind: "unresolvable",
          title,
          whyCodeIsRight: pick("whyCodeIsRight"),
          whyProposalIsWrong: pick("whyProposalIsWrong"),
          roundsMovedIt: pick("roundsMovedIt"),
          judges: votes.map((v) => ({ confidence: v.confidence, reasoning: v.reasoning })),
        });
        log(
          "Step " + step.id + ": the judges agree no legal change closes \"" + title.slice(0, 110) +
            "\" (" + summary + "). The code is right and the proposal is wrong. Recorded for a human and " +
            "set aside for the rest of this step.",
        );
      } else if (all("unproductive") && runLength < minUnproductiveRounds) {
        // The judges agreed, but the loop has not been given enough rounds for
        // that verdict to mean what it claims. Enforced here as well as in the
        // brief: a judge that returns it anyway must not be able to stop a step
        // the fixer has barely started on.
        log(
          "Step " + step.id + ": the judges called the loop unproductive, but only " + runLength +
            " consecutive round(s) have tried and failed and " + minUnproductiveRounds +
            " are required; the loop continues",
        );
      } else if (all("unproductive")) {
        // A legal change exists and this loop is not making it. Setting the
        // finding aside would tick the step over a real gap, so the step stops
        // here and the outstanding work goes to a human instead.
        const rec = {
          step: step.id,
          checklistStep: step.checklistStep,
          attempt,
          kind: "unproductive",
          title,
          consecutiveFailedRounds: runLength,
          outstandingWork: pick("outstandingWork"),
          roundsMovedIt: pick("roundsMovedIt"),
          judges: votes.map((v) => ({ confidence: v.confidence, reasoning: v.reasoning })),
        };
        stuckFindings.push(rec);
        unproductiveStep = rec;
        log(
          "Step " + step.id + ": the judges agree the loop is not closing \"" + title.slice(0, 110) +
            "\" (" + summary + "). The finding is real and the work is legal, so it is NOT set aside. " +
            "Stopping the step and reporting the outstanding work.",
        );
        break;
      } else {
        log("Step " + step.id + ": no agreed verdict (" + summary + "); the loop continues");
      }
    }

    if (!stepReviewClean) {
      issues =
        stepFindings.length > 0
          ? "This step builds and its tests pass, but the design-conformance review found divergences from the proposal. Fix the code to match the proposal's design for this step (do not change scope, do not touch spec/). THE PROPOSAL FILE IS OUT OF SCOPE FOR EVERY FINDING BELOW. The findings are reproduced verbatim, and one may name the proposal as the thing to change, or ask for an edit to it to be reverted or restored. Disregard that part of any such finding: it is outside what this step does, the current content of the proposal stands as written whatever its history, and a human has already decided to keep it and review it separately. Do not edit, revert, or restore that file, and do not treat leaving it alone as leaving the finding unaddressed. Take from each finding only what it says about the CODE; where a finding says nothing about the code, note that you left it alone and move on. Each divergence is a defect the step's tests passed over, so it is also a test-coverage gap: for every finding with a runtime effect, ALSO add or strengthen an automated test that asserts the corrected behavior and would FAIL against the pre-fix code, at the tier that owns it, so this class of issue cannot recur. Keep all tests green:\n" +
            JSON.stringify(stepFindings, null, 2) + suppressedNote(step)
          : "This step builds and its tests pass, but a design-conformance reviewer did not return, so conformance is unconfirmed. Re-check that this step's code matches the proposal's design for its sections; fix any divergence and keep the tests green.";
    }
  }

  // Tick the proposal's checklist box for this step once it is green and
  // conformant. The box is the resumption record: a later run reads it to find
  // where to continue, which is what the pipeline previously had no way to know.
  if (stepGreen && stepReviewClean && step.checklistStep) {
    await agentTry(
      "In " + proposal + ", find the implementation-checklist line for step " + step.checklistStep +
        ". It begins `- [ ] **" + step.checklistStep + "`. Change that line's `- [ ]` to `- [x]` and change " +
        "NOTHING else in the file: no wording, no other checkbox, no other line, and no file other than this " +
        "one. If there is no such line, or its box is already `[x]`, change nothing. Reply DONE either way.",
      { label: "tick:" + step.checklistStep, model: "haiku" },
    );
    log("Checklist: marked " + step.checklistStep + " complete");
    await recordStuckForStep(step);
  }

  // Whether the step ticked or aborted, a finding the judges could not send
  // back to the loop is written into the commit trail. Tying it to the tick alone
  // would lose it exactly when a step ends badly, which is when it matters most.
  async function recordStuckForStep(step) {
    const mine = stuckFindings.filter((f) => f.step === step.id && !f.recorded);
    if (mine.length === 0) return;
    for (const f of mine) f.recorded = true;
    {
      await agentTry(
        "Record, in the git history, a finding this build step could not resolve. Work in " + repo + ".\n\n" +
          "Stage nothing and write no file. Make an EMPTY commit on the current branch with " +
          "`git commit --allow-empty` whose message records the following, in the repository's commit style: " +
          "what each finding below records. A finding marked kind \"unresolvable\" was judged by every " +
          "reviewer to be closable by no change the step could legally make, because the landed code is " +
          "correct and the proposal is wrong; say that it is left for human review rather than fixed. A " +
          "finding marked kind \"unproductive\" is real and its remedy is legal, but the rounds did not " +
          "make it; say what work is outstanding, and do not describe it as resolved or as a defect in the " +
          "proposal. Say whether the step ticked. Do not editorialise beyond what is given.\n\n" +
          JSON.stringify(mine, null, 2) +
          "\n\nUse a subject line under 72 characters naming the step and the subject of the finding. " +
          "Reply with the commit sha.",
        { label: "record-stuck:" + step.checklistStep, phase: "Build" },
      );
      log("Step " + step.id + ": recorded " + mine.length + " unclosed finding(s) in the commit trail");
    }
  }

  stepResults.push({
    step: step.id,
    title: step.title,
    attempts: attempt,
    stepGreen,
    reviewClean: stepReviewClean,
    findings: stepReviewClean ? [] : stepFindings,
    ...(res || { implemented: false, testsPassed: false, tiersRun: [], notes: "agent failed" }),
  });
  // Abort the sequence if the step did not reach green-and-conformant within
  // maxStepAttempts: its dependents would build on a broken or divergent
  // foundation. Stop here; the spec and the completed steps are already
  // committed for inspection and resume.
  if (!(stepGreen && stepReviewClean)) {
    await recordStuckForStep(step);
    const remaining = plan.steps.length - i - 1;
    // Name the real cause. A run stopped because its subagents were not
    // running is resumable as soon as that clears, while a genuinely stuck
    // step needs someone to read the findings; reporting the first as the
    // second sends the operator to the wrong place.
    const reason = unproductiveStep
      ? "the judges agreed the loop was not closing a real finding: " + unproductiveStep.title
      : deadAttempts >= maxDeadAttempts
      ? "subagents are not running (rate limit, exhausted credit, or expired auth), so the step was never attempted on its merits"
      : !stepGreen
        ? "tests not green"
        : "design-conformance divergences outstanding";
    log(
      "Step " +
        step.id +
        " stuck (" +
        reason +
        ") after " +
        attempt +
        " attempt(s); aborting the build sequence (" +
        remaining +
        " dependent step(s) not attempted)",
    );
    return {
      status: "step-stuck",
      stuckStep: step.id,
      // Recorded even here: a step that aborts is the case where a suppressed
      // proposal defect is most likely to be the reason it could not finish.
      stuckFindings,
      blastRadius: plan.blastRadius,
      steps: stepResults,
      commits: stepResults.map((s) => s.commit).filter(Boolean),
      green: false,
      reviewClean: false,
      reviewFindings: stepReviewClean ? [] : stepFindings,
      failures: [
        "build aborted at step " +
          step.id +
          " (" +
          step.title +
          ") after " +
          attempt +
          " attempts: " +
          reason +
          "; " +
          remaining +
          " dependent step(s) not attempted",
      ],
      resumeNote:
        "The spec is applied and committed; build steps before " +
        step.id +
        " are committed green and review-clean. " +
        (!stepGreen
          ? "Step " + step.id + "'s tests did not reach green. "
          : "Step " + step.id + " is green but diverges from the proposal's design (see reviewFindings). ") +
        "Fix step " +
        step.id +
        " by hand or re-run implement-proposal, which re-plans against the current tree.",
    };
  }
  log(
    "Step " +
      step.id +
      " done in " +
      attempt +
      " attempt(s) (commit " +
      ((res && res.commit) || "?") +
      "), green + review-clean.",
  );
  // Carry a short tail of prior-step outcomes so each implementer knows
  // what already landed without re-deriving it.
  priorContext =
    "\n\nAlready completed in this sequence:\n" +
    stepResults
      .map((s) => "- " + s.step + ": " + s.title + (s.commit ? " (" + s.commit + ")" : ""))
      .join("\n");

  // Periodic plan-drift check, forward-only. The plan was a prediction made
  // before any code existed; as steps land, reality drifts (an unplanned
  // surface gets touched, a removal orphans more than foreseen, a later step
  // becomes redundant or mis-sequenced). The per-step review judges completed
  // work, not the remaining plan — so here, every replanEvery completed steps
  // (and after a step that struggled), a read-only critic checks whether the
  // remaining plan still holds against what actually landed. On evidenced
  // drift, the remaining steps are re-planned. Completed steps are immutable:
  // only indices after i ever change.
  const completed = i + 1;
  const hasRemaining = i < plan.steps.length - 1;
  const triggerReplan = (completed % replanEvery === 0) || attempt >= replanStruggleAttempts;
  if (hasRemaining && replanCount < maxReplans && triggerReplan) {
    const remainingSteps = plan.steps.slice(i + 1);
    const drift = await agentTry(
      "Check whether the REMAINING build plan still matches reality after part of a build sequence has landed.\n\n" +
        "You are a read-only critic; do not edit any file. Work in " +
        repo +
        ".\n\nProposal (authoritative, spec edits already applied): " +
        proposal +
        ". Read the sections still to be built.\n\nCompleted so far (immutable, already committed):\n" +
        stepResults.map((s) => "- " + s.step + ": " + s.title).join("\n") +
        "\n\nWhat actually landed: `git diff " +
        baseRef +
        "..HEAD` in the repo.\n\nRemaining planned steps (not yet built):\n" +
        JSON.stringify(remainingSteps, null, 2) +
        "\n\nReport drift=true ONLY with concrete, evidenced reasons that the remaining plan no longer correctly or completely implements the rest of the proposal given what landed: a surface the proposal requires that the completed work touched but no remaining step covers; a removal the completed work began that orphaned code no remaining step deletes; a remaining step now redundant because an earlier step already satisfied it; a remaining step whose prerequisites changed so it must be re-sequenced; or a file the completed work changed that forces a different approach in a remaining step. Be conservative: default to drift=false. Do NOT report design-conformance nits (handled per step), style, or scope the proposal does not contain. Return drift=false with an empty reasons array when the remaining plan still holds.",
      { schema: DRIFT, label: "replan-check:after-" + step.id, phase: "Build" },
    );
    if (drift && drift.drift && (drift.reasons || []).length > 0) {
      log(
        "Plan drift after step " +
          step.id +
          " (" +
          drift.reasons.length +
          " reason(s)); re-planning the remaining " +
          remainingSteps.length +
          " step(s) [re-plan " +
          (replanCount + 1) +
          "/" +
          maxReplans +
          "]",
      );
      const tail = await agentTry(
        "Re-plan the REMAINING steps of a build sequence for an applied spec proposal, given what has already landed.\n\n" +
          "You are a read-only planner; do not edit any file. Work in " +
          repo +
          ".\n\nProposal (authoritative, spec edits applied): " +
          proposal +
          ".\n\nCompleted steps are IMMUTABLE — already committed; do not include them, re-order them, or plan to redo them:\n" +
          stepResults
            .map((s) => "- " + s.step + ": " + s.title + (s.commit ? " (" + s.commit + ")" : ""))
            .join("\n") +
          "\n\nWhat actually landed: `git diff " +
          baseRef +
          "..HEAD`.\n\nCurrent remaining plan:\n" +
          JSON.stringify(remainingSteps, null, 2) +
          "\n\nDrift the critic found:\n" +
          drift.reasons.map((r) => "- " + r).join("\n") +
          "\n\nReturn the revised ORDERED sequence for the REMAINING work only. Preserve the id, title, and content of any remaining step that is still valid and unchanged so the logs stay coherent; add steps for newly discovered work, drop steps already satisfied by the completed work, and re-sequence where a prerequisite emerged. Each step keeps the same fields (id, title, work, targets, dependsOn, tiers, specRefs); give new steps fresh ids that do not collide with completed or surviving ids. Cover the rest of the proposal completely, including any removal the completed work left orphaned. In blastRadiusAdditions, list any surface the original plan missed that you discovered. Do not re-plan or duplicate completed work.",
        { schema: TAIL, label: "replan:after-" + step.id, phase: "Build" },
      );
      // Count the re-plan attempt regardless of its outcome: a re-plan that
      // keeps returning empty must still be bounded by maxReplans, otherwise
      // the drift+replan agent pair re-runs on every cadence hit unbounded.
      replanCount++;
      if (tail && tail.steps && tail.steps.length > 0) {
        // Reassign any new-tail id that collides with a completed step id, so
        // stepResults and the drift/replan prompts do not key two different
        // steps under the same id. dependsOn is informational here (steps run
        // in array order), but remap it too for coherence.
        const completedIds = new Set(stepResults.map((s) => s.step));
        const idRemap = {};
        for (const s of tail.steps) {
          if (completedIds.has(s.id)) {
            idRemap[s.id] = s.id + "-r" + replanCount;
            s.id = idRemap[s.id];
          }
        }
        for (const s of tail.steps) {
          if (Array.isArray(s.dependsOn)) s.dependsOn = s.dependsOn.map((d) => idRemap[d] || d);
        }
        // Splice the new tail in place of the old remaining steps. Only
        // indices after i change; the for-loop picks up the new steps as
        // plan.steps.length updates.
        plan.steps.splice(i + 1, plan.steps.length - (i + 1), ...tail.steps);
        if (tail.blastRadiusAdditions && tail.blastRadiusAdditions.length > 0) {
          plan.blastRadius = plan.blastRadius.concat(tail.blastRadiusAdditions);
        }
        log("Re-planned tail: " + tail.steps.length + " remaining step(s) now queued");
      } else {
        log("Re-plan returned no steps; keeping the existing tail");
      }
    }
  }
}

// ---- Verify: run the reached tiers across the whole change ----

// Clean-checkout compile guard. The Verify and recheck agents run tests
// against the WORKING TREE, so a valid fix left uncommitted reads as green even
// though HEAD does not contain it — which has shipped a non-compiling branch
// tip (verification passing over code that was never committed). Before
// trusting green, assert the tree carries no uncommitted tracked source change
// and that HEAD compiles from that clean state. Returns {clean, compiles,
// committed, details}; the caller ANDs clean && compiles into green.
const GUARD = {
  type: "object",
  required: ["clean", "compiles"],
  properties: {
    clean: { type: "boolean", description: "git status --porcelain lists no tracked source change (every change committed)" },
    compiles: { type: "boolean", description: "`go build ./...` exits 0 on the committed tree" },
    committed: { type: "string", description: "SHA of a commit made to clean the tree, or empty if nothing needed committing" },
    details: { type: "string", description: "what was uncommitted and what was done, or the build error if it does not compile" },
  },
};

const GUARD_PROMPT =
  "CLEAN-CHECKOUT COMPILE GUARD for the implementation in " +
  repo +
  ".\n\nThe verification runs tests against the WORKING TREE, so a valid fix left UNCOMMITTED reads as green even though HEAD does not contain it (this has shipped a non-compiling branch tip). Enforce that green reflects COMMITTED code:\n" +
  "1. Run `git status --porcelain`. A tracked modified/added SOURCE file (Go, chart, schema, migration, proto) is part of this change and MUST be committed: first confirm the whole tree builds with `go build ./...` and the affected packages' tests pass, then stage and commit those files on the current feature branch with a descriptive message. Do NOT commit build outputs, coverage files, logs, or unrelated scratch artifacts — leave git-ignored or clearly-external untracked files in place; they do not block clean.\n" +
  "2. After the tree carries no uncommitted tracked source change, run `go build ./...` and confirm it exits 0 (HEAD compiles from the committed state).\n" +
  "Set clean=true only when `git status --porcelain` lists no tracked source change, and compiles=true only when `go build ./...` exits 0. If a tracked source file cannot be committed because it does not build or breaks tests, set clean=false and explain in details rather than committing a broken tree. " +
  "MEMORY-SAFE: run `go build ./...` (compilation only, not under the race detector) and any scoped package tests in the FOREGROUND; never run a whole-repo `go test -race ./...`. " +
  "BRANCH SAFETY: confirm `git rev-parse --abbrev-ref HEAD` prints the feature branch before any commit; never checkout/switch/reset/branch -f.";

// Runs the guard and returns its verdict (or null if the agent died).
async function runCompileGuard(label, phaseName) {
  return await agentTry(GUARD_PROMPT, { schema: GUARD, label: "compile-guard:" + label, phase: phaseName });
}

phase("Verify");
const tierSet = Array.from(new Set(plan.steps.flatMap((s) => s.tiers || [])));
let verify = await agentTry(
  "Verify the completed implementation of an applied spec proposal builds and its tests pass across every tier the change reached.\n\n" +
    "Work in " +
    repo +
    ". Proposal: " +
    proposal +
    ". The implementation just landed across these commits: " +
    stepResults.map((s) => s.commit).filter(Boolean).join(", ") +
    ".\n\nRun tier 0 and tier 1 for the changed packages, plus each higher tier the change reached: " +
    (tierSet.join(", ") || "as determined from the diff") +
    ". MEMORY-SAFE on this 16GB host: DEFAULT to running each tier in the FOREGROUND, one at a time, SCOPED to the changed packages (`lenny-test --changed --max-tier <tier>` selects only changed packages; add go-test parallelism `-p 1`/`-p 2`). NEVER use `run_in_background` for tests unless a single long-running tier will exceed the 180s watchdog without emitting output (see below). NEVER run a whole-repo `go test -race ./...` — orphaned/concurrent envtest etcd+kube-apiserver and the race detector OOM-crash the machine. Run tier-2 envtest packages one at a time and reap strays (`pkill -f kubebuilder-envtest 2>/dev/null`) between them. Stream output (no `| tail`); scoped runs stay under the 180s watchdog. LONG-SILENT TIER EXCEPTION: if and only if a single tier command will genuinely exceed ~150s without any output, launch it with `run_in_background: true` to a log file path — then poll by reading that log file with the Read tool (not with a Bash `tail`/`until` loop; a Bash poll loop hangs forever when the harness deletes its `.output` file after the 180s watchdog fires). RUN TIER-0 STATIC CHECKS DIRECTLY: do not rely solely on `lenny-test --tier static` to report all tier-0 failures at once — run `go build ./...`, `go vet ./...`, `golangci-lint run ./...`, and `bash scripts/lint-migrations.sh` as separate foreground commands so each failure is visible independently rather than short-circuiting the others. A `lint-migrations` failure on a migration number with no test reference is only a failure for the THIS change if the migration was ADDED on this branch; a pre-existing uncovered migration (present on the base ref too) is pre-existing debt, not this change's failure — note it but do not mark green=false for it alone. Bring infrastructure up as needed. Run `lenny-test coverage --diff " +
    baseRef +
    "` and report the changed-line coverage. COVERAGE GATE: green=true requires BOTH that every reached tier passes AND that changed-line coverage is at least " +
    coverageFloor +
    "%; if coverage is below " +
    coverageFloor +
    "%, set green=false and add a failure entry naming the under-covered new or changed files and lines so the fix loop adds tests for them (per the test-coverage rule, the floor is on new code; a pure behavior-preserving refactor is exempt — note it instead of failing). REGRESSION-TEST GATE (in addition to the coverage gate): green=true also requires that every behavioral FIX in this change is pinned by a test that asserts the CORRECTED outcome — not merely line-covered by a pre-existing happy-path test. Inspect the diff against the base ref: for each changed hunk that alters a runtime decision (a returned error/status code, an authz/scope/admission gate or branch, a cross-tenant scoping check, a state transition, a retry/recovery or fail-open/fail-closed path, a wire field), confirm the diff also adds or changes a test that exercises the corrected outcome and would fail against the pre-fix code, at the tier that owns it (security → tier 9, reliability/recovery → tier 7a/8, wire/contract → tier 3, state-machine → tier 2). A fix that landed with no such regression test — even at 100% line coverage — is a failure: set green=false and add an entry naming the fix (file:line), the corrected behavior, and the missing regression test so the fix loop writes it. A pure-additive new feature (no corrected pre-existing behavior) is covered by the coverage gate alone; a behavior-preserving refactor is exempt. Run a DEAD-CODE SWEEP: grep the whole tree for every identifier, mode value, field, RPC, frame, metric name, and enum value the proposal removes, and confirm none survives as a live reference; the tier-0 `unused` linter catches unused package-level symbols, but also check for orphaned exported symbols, whole unreferenced files, stale test fixtures, and dangling schema or chart entries. Treat a surviving removed surface or an orphaned caller as a failure (list it precisely so the fix loop deletes it). List any failures precisely.",
  { schema: VERIFY, label: "verify", phase: "Verify" },
);

// Iterate fix-and-re-run until every reached tier is green, bounded by
// maxVerifyRounds. The loop stops early when a round reports green, or when
// a non-green result lists no actionable failures (nothing to fix).
let vround = 0;
while (
  verify &&
  !verify.green &&
  verify.failures &&
  verify.failures.length > 0 &&
  vround < maxVerifyRounds
) {
  vround++;
  log("Verify round " + vround + "/" + maxVerifyRounds + ": fixing " + verify.failures.length + " failure(s)");
  await agentTry(
    "Fix the test failures from the verification of an applied spec proposal's implementation.\n\n" +
      "Work in " +
      repo +
      ". Proposal: " +
      proposal +
      ". Failures:\n" +
      verify.failures.map((f) => "- " + f).join("\n") +
      "\n\nFix the code (not the spec) so every reached tier passes; add or correct tests where the failure is a missing or wrong test. Commit the fixes and keep the change minimal. " +
      RULES,
    { label: "verify-fix:r" + vround, phase: "Verify" },
  );
  verify = await agentTry(
    "Re-run the reached tiers for the implementation in " +
      repo +
      " and report whether everything is now green. Tiers: " +
      (tierSet.join(", ") || "from the diff") +
      ". MEMORY-SAFE: run each tier in the FOREGROUND, scoped to the changed packages, one at a time (no `run_in_background` for tests; no whole-repo `go test -race ./...`); reap stray envtest processes (`pkill -f kubebuilder-envtest 2>/dev/null`) between tier-2 packages; pass `-p 1`/`-p 2` to cap memory; stream output (no `| tail`). Bring infrastructure up as needed. Apply the same coverage gate: green=true requires every reached tier to pass AND changed-line coverage (via `lenny-test coverage --diff " +
      baseRef +
      "`) at least " +
      coverageFloor +
      "%, with a behavior-preserving refactor exempt. Report green, the tiers run, the changed-line coverage, and any remaining failures precisely.",
    { schema: VERIFY, label: "verify-rerun:r" + vround, phase: "Verify" },
  );
}

let green = !!(verify && verify.green);
if (!green) {
  log(
    vround >= maxVerifyRounds
      ? "Verify cap (" + maxVerifyRounds + " rounds) reached; still not green"
      : "Verify not green with no actionable failures listed; stopping",
  );
} else {
  // Verify reported green against the working tree; confirm that green
  // reflects COMMITTED code and that HEAD compiles from a clean tree.
  const guard = await runCompileGuard("postverify", "Verify");
  if (!guard || !guard.clean || !guard.compiles) {
    green = false;
    log(
      "Clean-checkout compile guard FAILED: " +
        (guard
          ? "clean=" + guard.clean + ", compiles=" + guard.compiles + (guard.details ? " — " + guard.details : "")
          : "guard agent returned no result"),
    );
  } else if (guard.committed) {
    log("Clean-checkout compile guard committed outstanding work: " + guard.committed);
  }
}

// ---- Review: final cross-step design-conformance review of the cumulative diff ----
// Each step was already reviewed against its own diff during Build. This
// final pass reads the WHOLE change against baseRef to catch what a
// step-scoped review cannot: cross-step interactions, a surface one step
// added and another was meant to consume but does not, and whole-change
// completeness. Three lenses report divergences; a fix round applies them
// and the loop re-reviews until clean (or the cap). Skipped when the build
// is not green (no point reviewing a red tree). Findings here are design
// conformance, not new scope.

const REVIEW_LENSES = [
  {
    key: "conformance",
    text: "Lens: design conformance. Read the proposal's Decisions, Detailed design, and CRD/RBAC/Observability sections, then read the cumulative diff. Report where the landed code does something other than what the design specifies: a different component owning a write, a missing or wrong gate, a predicate that does not match the design, a field on the wrong object, an ordering that violates the design, or a default that contradicts it. Passing tests do not excuse a divergence. Also report any proposal-internal scaffolding label that leaked into the shipped code, comments, test names, or a commit message in the diff — a change/section id (`S1`, `CODE-A`, `SPEC-D`), decision id (`D8`, `D9`, `RES-1`), review pass number, or a step/item number that exists only in the proposal — where a `// spec: §X.Y` citation or a plain behavior description belongs; a step or section the spec itself numbers is a valid spec reference.",
  },
  {
    key: "invariants",
    text: "Lens: named invariants and edge cases. List the invariants, races, ordering rules, and failure-mode handling the proposal explicitly calls out (for example a precondition-fenced write, a fail-closed gate, a crash-recovery path, a one-writer rule), and verify the code implements each. Report any invariant the code does not enforce or enforces incorrectly, and any spec-named edge case with no corresponding code path.",
  },
  {
    key: "completeness",
    text: "Lens: blast-radius completeness. Cross-check the proposal's Files-touched and CRD/schema/chart sections against the diff: every code, schema, chart, migration, and metric change the proposal specifies is present, and every surface it removes is gone (no orphaned caller or compiling-but-dead path). Report any specified change missing from the diff or any removal left incomplete.",
  },
];

// A design-conformance review finding is, by construction, a defect the
// automated tests passed over — the build was green when the review ran. So a
// review that fixes only the code leaves the same class of issue able to fall
// through next time. The review-fix agents below therefore MUST add a
// regression test that would fail against the pre-fix code for every finding
// with a runtime effect, the reviewer names the tier that test belongs at, and
// verify-postreview gates on those tests existing. The same rule applies to the
// per-step review (issues message in the Build loop) and is enforced there too.
const REVIEW_RULES =
  "Read the proposal at " +
  proposal +
  " (its spec edits are applied) and the cumulative implementation diff (`git diff " +
  baseRef +
  "..HEAD`) in " +
  repo +
  ". You are read-only; report findings only. A finding is a place the landed code diverges from the proposal's design — not a style preference, not new scope the proposal does not contain, and not a bare coverage percentage (the coverage floor is handled separately). Cite file:line and the proposal section. For each finding, also name the tier at which an automated regression test should assert the corrected behavior (security → 9, reliability/recovery → 7a/8, wire/contract → 3, state-machine → 2, pure logic → 1): you are catching what the automated tests passed over, so the fix must close that test gap, not only the code. Report an empty array when the code conforms.";

let reviewClean = false;
let reviewRound = 0;
let lastReviewFindings = [];
let reviewFixApplied = false;
if (green) {
  phase("Review");
  while (reviewRound < maxReviewRounds && !reviewClean) {
    reviewRound++;
    const reviewResults = await parallel(
      REVIEW_LENSES.map((l) => () =>
        agentTry(REVIEW_RULES + ACCEPTED_BLOCK + "\n\n" + l.text, {
          schema: REVIEW,
          label: "review:" + l.key + ":r" + reviewRound,
          phase: "Review",
        }),
      ),
    );
    // Fail closed: a reviewer that died (null) is not evidence of conformance.
    // Only declare clean when every lens ran and none found a divergence.
    const liveReviews = reviewResults.filter(Boolean);
    const allReviewersRan = liveReviews.length === REVIEW_LENSES.length;
    const findings = liveReviews.flatMap((r) => r.findings);
    lastReviewFindings = findings;
    log(
      "Review round " +
        reviewRound +
        ": " +
        findings.length +
        " design-conformance finding(s)" +
        (allReviewersRan ? "" : " (a reviewer did not return; not treated as clean)"),
    );
    if (findings.length === 0) {
      if (allReviewersRan) {
        reviewClean = true;
        break;
      }
      // No findings, but a reviewer did not run — re-review rather than
      // concluding clean. Nothing to fix, so skip the fix agent.
      continue;
    }
    reviewFixApplied = true;
    await agentTry(
      "Fix design-conformance divergences between the landed implementation and the proposal at " +
        proposal +
        ".\n\nWork in " +
        repo +
        ". The proposal's spec edits are applied; do not edit spec/. Apply each finding so the code matches the proposal's design. EACH FINDING IS A COVERAGE GAP BY DEFINITION: the automated tests passed, yet this review caught the defect — so fixing the code alone lets the same class of issue fall through next time. Therefore, for EVERY finding with a runtime effect (a wrong value, a broken invariant, a mis-wired or missing path, a wrong status/error code/label/metric, a fail-open), you MUST also add or strengthen an automated test that asserts the corrected behavior and is CONSTRUCTED SO IT FAILS against the pre-fix code and PASSES after your fix, at the tier that owns it (security → 9, reliability/recovery → 7a/8, wire/contract → 3, state-machine → 2, pure logic → 1). Line-covering the changed lines does not satisfy this — the assertion must target the corrected outcome, and you should confirm the would-have-caught property (e.g. by checking the test fails at the pre-fix revision). A finding whose code is fixed with no such regression test is NOT done. Run tier 0 and tier 1 (plus the higher tiers the change reaches) to green, and commit each fix together with its regression test. " +
        RULES +
        "\n\nFindings to fix:\n" +
        JSON.stringify(findings, null, 2),
      { label: "review-fix:r" + reviewRound, phase: "Review" },
    );
  }
  if (!reviewClean) {
    log(
      reviewRound >= maxReviewRounds
        ? "Review cap (" + maxReviewRounds + " rounds) reached with divergences outstanding"
        : "Review stopped with divergences outstanding",
    );
  }
} else {
  log("Build not green; skipping design-conformance review");
}

// A review fix can perturb tests; re-confirm green once whenever the review
// applied any fix, so the returned green reflects the post-fix tree. Gating on
// reviewFixApplied (not on outstanding findings) is the point: a review that
// found and fixed a divergence then converged clean still changed the tree.
let finalGreen = green;
if (green && reviewFixApplied) {
  const recheck = await agentTry(
    "Re-run the reached tiers for the implementation in " +
      repo +
      " after the design-conformance fixes and report whether everything is still green. Tiers: " +
      (tierSet.join(", ") || "from the diff") +
      ". MEMORY-SAFE: run each tier in the FOREGROUND, scoped to the changed packages, one at a time (no `run_in_background` for tests; no whole-repo `go test -race ./...`; reap stray envtest etcd/kube-apiserver between packages; `-p 1`/`-p 2` to cap memory; stream output, no `| tail`). Apply the coverage gate (changed-line coverage at least " +
      coverageFloor +
      "% via `lenny-test coverage --diff " +
      baseRef +
      "`, refactors exempt). REGRESSION-TEST GATE FOR REVIEW FIXES: the design-conformance review just caught defects the automated tests had passed over. Inspect the commits made during the review rounds (`git log` since the review began) and confirm each code fix with a runtime effect is pinned by a test that asserts the corrected outcome and would fail against the pre-fix code — not merely line-covered by a pre-existing happy-path test. If any review-driven fix landed with no such regression test, set green=false and name the fix (file:line), the corrected behavior, and the missing test so the gap is closed before this returns. Report green, the tiers run, the coverage, and any remaining failures.",
    { schema: VERIFY, label: "verify-postreview", phase: "Review" },
  );
  finalGreen = !!(recheck && recheck.green);
  if (recheck) verify = recheck;
  // The review-fix agents commit their own fixes, but a fix left uncommitted
  // in the working tree would pass the recheck while leaving HEAD red. Re-run
  // the clean-checkout compile guard so finalGreen reflects committed code.
  if (finalGreen) {
    const guard = await runCompileGuard("postreview", "Review");
    if (!guard || !guard.clean || !guard.compiles) {
      finalGreen = false;
      log(
        "Clean-checkout compile guard FAILED after review fixes: " +
          (guard
            ? "clean=" + guard.clean + ", compiles=" + guard.compiles + (guard.details ? " — " + guard.details : "")
            : "guard agent returned no result"),
      );
    } else if (guard.committed) {
      log("Clean-checkout compile guard committed outstanding review work: " + guard.committed);
    }
  }
}

// Did the proposal change during the build, and if so what did the change say?
// Read from git rather than from any agent's self-report, because an agent that
// edited the file against instruction is not the source to ask about it.
const proposalEditReport = await agentTry(
  "Report whether one file changed during a range of commits, and if so what the changes said. This is a " +
    "read-only audit: do not edit, revert, or restore anything, and run no command that writes.\n\n" +
    "Repository: " + repo + "\nFile: " + proposal + "\nRange: " + baseRef + "..HEAD\n\n" +
    "Run `git log --oneline " + baseRef + "..HEAD -- " + proposal + "` to list the commits that touched it. " +
    "For each, read its diff for that file with `git show <sha> -- " + proposal + "` and its full message with " +
    "`git show -s --format=%B <sha>`. Report what each edit did to the document's meaning, naming the section " +
    "and quoting the sentences it replaced and the sentences it wrote, and give the reason the commit message " +
    "states, verbatim. Do not judge whether the edit was right; a human decides that from your report. When no " +
    "commit touched the file, report edited=false with an empty list, which is the expected result.",
  { schema: PROPOSAL_EDITS, label: "proposal-edit-audit", phase: "Review" },
);
if (proposalEditReport && proposalEditReport.edited) {
  log(
    "THE PROPOSAL WAS EDITED DURING THE BUILD, which the step rules forbid: " +
      ((proposalEditReport.commits || []).length) +
      " commit(s). Kept and reported rather than reverted; see proposalEdits in the result.",
  );
}

return {
  status: finalGreen && reviewClean ? "implemented" : "implemented-not-green",
  blastRadius: plan.blastRadius,
  steps: stepResults,
  // Where the proposal's implementation checklist did not match the tree. An
  // imperfect checklist is expected and does not stop the run, so this is the
  // record that lets the proposal be corrected afterwards.
  checklistDeviations: plan.deviations || [],
  // Where the landed code departs from what the proposal states. The build
  // never edits the proposal to close one, so this is the only place a
  // proposal defect surfaces, and it is addressed to a human.
  // Steps that were ticked but failed re-verification and had to be repaired.
  reverifyRepaired,
  // Findings three judges agreed no legal change could close: the code is right
  // and the proposal is wrong. Suppressed for the step, recorded in its commit
  // trail, and surfaced here because each is a proposal defect for a human.
  stuckFindings,
  proposalDeviations: stepResults.flatMap((r) =>
    (r.deviations || []).map((d) => ({ step: r.step, title: r.title, ...d })),
  ),
  // Edits the build made to the proposal despite being forbidden to. Recorded
  // rather than prevented or reverted; see PROPOSAL_EDITS.
  proposalEdits: proposalEditReport,
  skippedSteps,
  commits: stepResults.map((s) => s.commit).filter(Boolean),
  green: finalGreen,
  reviewClean,
  reviewRounds: reviewRound,
  reviewFindings: reviewClean ? [] : lastReviewFindings,
  verifyRounds: vround,
  changedLineCoverage: verify ? verify.changedLineCoverage : undefined,
  failures: verify ? verify.failures || [] : ["final verification did not run"],
  resumeNote:
    finalGreen && reviewClean
      ? undefined
      : "Spec and code commits are on the branch. " +
        (!finalGreen ? "Tests/coverage are not green. " : "") +
        (!reviewClean ? "Design-conformance review found unresolved divergences (see reviewFindings). " : "") +
        "Re-run implement-proposal to continue, or resolve by hand.",
};
