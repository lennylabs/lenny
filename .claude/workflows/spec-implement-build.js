// Implementation subworkflow for spec-implement. Given an applied spec
// proposal, it identifies the blast radius, plans an ordered build
// sequence, implements the sequence one step at a time (creating and
// running tests along the way), and runs a final verification pass.
// It does NOT verify the spec is applied or close findings — the
// spec-implement parent does that around this call.
//
// Invoked as a sub-step: workflow("spec-implement-build", { proposalPath,
// date, repoRoot, maxTier }). Runs only agents (no nested workflow()).
//
// MAINTENANCE: the spec-implement skill (.claude/skills/spec-implement)
// documents this subworkflow; keep its description in sync.

export const meta = {
  name: "spec-implement-build",
  description:
    "Plan the blast radius and build sequence of an applied spec proposal, then implement it one step at a time with tests, then verify",
  phases: [
    { title: "Plan", detail: "blast radius + ordered build sequence, completeness-checked" },
    { title: "Build", detail: "implement each step in order, with tests run to green" },
    { title: "Verify", detail: "run the reached tiers across the whole change" },
  ],
};

let input = args;
if (typeof input === "string") input = JSON.parse(input);
if (!input || !input.proposalPath || !input.date) {
  throw new Error("args.proposalPath and args.date are required");
}
const repo = input.repoRoot || "/Users/joan/projects/lenny";
const date = input.date;
const proposal = input.proposalPath.startsWith("/")
  ? input.proposalPath
  : repo + "/" + input.proposalPath;
const maxPlanRounds = input.maxPlanRounds || 2;

const RULES =
  "Follow the project rules in " +
  repo +
  "/.claude/rules/code-best-practices.md (small single-purpose functions, reuse over duplication, wrapped errors, injected dependencies, context propagation, fail-closed security, `// spec:` citations, no backward-compat shims) and " +
  repo +
  "/.claude/rules/test-coverage.md (tests across every tier the change reaches, not unit alone; tier 0 and tier 1 always plus each higher tier the change touches; 80% changed-line coverage; the `// spec:` and `// diagnosis:` test conventions). Do not hand-edit files under spec/ — the spec is already applied and the guard hook blocks direct writes.";

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
      items: {
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
        },
      },
    },
    risks: { type: "array", items: { type: "string" } },
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

// ---- Plan: blast radius + ordered build sequence, completeness-checked ----

phase("Plan");
log("Planning the blast radius and build sequence for " + proposal);
let plan = await agent(
  "Plan the code implementation of an applied spec proposal.\n\n" +
    "You are a read-only planner; do not edit any file. Work in " +
    repo +
    ".\n\n" +
    "Proposal: " +
    proposal +
    ". The proposal's spec edits are ALREADY applied to spec/. Read the proposal in full — every section, especially Detailed design, CRD and RBAC changes, Observability, Proposed spec changes (now landed in spec/), Testing, and Files touched. The finding(s) that reference this proposal are entry points; the proposal defines the complete change.\n\n" +
    "Then map the full blast radius: grep spec/, pkg/, cmd/, charts/, schemas/, and migrations/ for every existing surface the change touches (call sites, builders, stores, controllers, reconcilers, CRD types, proto/JSONL schemas, chart templates, alert rules) and every new surface it adds. " +
    "Produce blastRadius (one entry per surface) and an ORDERED build sequence of steps where each step is independently implementable once its dependencies are done. For each step give the work, the target files or packages, dependsOn (earlier step ids), the test tiers it must create and run (per " +
    repo +
    "/.claude/rules/test-coverage.md), and the spec sections it implements. Sequence so foundational changes (CRD fields, schemas, shared types) come before the code that consumes them, and tests for each step land within that step.",
  { schema: PLAN, label: "plan", phase: "Plan" },
);

for (let round = 1; round < maxPlanRounds; round++) {
  const critique = await agent(
    "Adversarially check whether a build plan covers the entire blast radius of an applied spec proposal.\n\n" +
      "You are a read-only critic; do not edit any file. Work in " +
      repo +
      ".\n\nProposal: " +
      proposal +
      " (read every section). Plan under review:\n" +
      JSON.stringify(plan, null, 2) +
      "\n\nReport complete=false with specific gaps when the plan omits any part of the proposal's blast radius (a field, RPC, condition, metric, gate, schema, chart template, or call site the proposal specifies), sequences a consumer before its prerequisite, or omits a test tier the change reaches per " +
      repo +
      "/.claude/rules/test-coverage.md. Report complete=true with an empty gaps array when the plan fully implements the proposal. Do not invent scope the proposal does not contain.",
    { schema: CRITIQUE, label: "plan-critique:r" + round, phase: "Plan" },
  );
  if (!critique || critique.complete || critique.gaps.length === 0) {
    log("Plan complete after " + round + " critique round(s)");
    break;
  }
  log("Plan revision " + round + ": closing " + critique.gaps.length + " gap(s)");
  plan = await agent(
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

log("Build sequence: " + plan.steps.length + " steps");

// ---- Build: implement each step in order, with tests run to green ----
// Sequential: later steps depend on earlier ones and share the working
// tree, so steps must not run concurrently.

phase("Build");
const stepResults = [];
let priorContext = "";
for (let i = 0; i < plan.steps.length; i++) {
  const step = plan.steps[i];
  const res = await agent(
    "Implement one step of a build sequence for an applied spec proposal.\n\n" +
      "HARD CONSTRAINT: implement only this step. Do not start later steps. Work in " +
      repo +
      ".\n\n" +
      "Proposal (authoritative for the change): " +
      proposal +
      ". Its spec edits are already in spec/; read the relevant sections.\n\n" +
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
      (step.specRefs || []).join(", ") +
      priorContext +
      "\n\nImplement the code for this step, create or modify its tests across the listed tiers (and any other tier this step reaches per the test-coverage rule), and RUN them: tier 0 (`go build ./...`, `go vet`, lint) and tier 1 always, plus each listed higher tier (bring infrastructure up with `lenny-test infra up` when a tier needs it). Fix the code until the tests pass. Then commit this step on the current branch with a message in the repository's convention (read `git log --oneline -5`). " +
      RULES +
      "\n\nReturn whether you implemented it, the files changed, the tests added or modified, the tiers you ran, whether they passed, and the commit SHA. If a tier genuinely cannot run here (a cloud-only resource), say so in notes and run the rest.",
    { schema: STEP, label: "build:" + step.id, phase: "Build" },
  );
  stepResults.push({ step: step.id, title: step.title, ...(res || { implemented: false, testsPassed: false, tiersRun: [], notes: "agent failed" }) });
  const tail = res
    ? "Step " + step.id + " done (commit " + (res.commit || "?") + ", tests " + (res.testsPassed ? "green" : "RED") + ")."
    : "Step " + step.id + " agent failed.";
  log(tail);
  // Carry a short tail of prior-step outcomes so each implementer knows
  // what already landed without re-deriving it.
  priorContext =
    "\n\nAlready completed in this sequence:\n" +
    stepResults
      .map((s) => "- " + s.step + ": " + s.title + (s.commit ? " (" + s.commit + ")" : "") + (s.testsPassed ? "" : " [tests not green]"))
      .join("\n");
}

// ---- Verify: run the reached tiers across the whole change ----

phase("Verify");
const tierSet = Array.from(new Set(plan.steps.flatMap((s) => s.tiers || [])));
let verify = await agent(
  "Verify the completed implementation of an applied spec proposal builds and its tests pass across every tier the change reached.\n\n" +
    "Work in " +
    repo +
    ". Proposal: " +
    proposal +
    ". The implementation just landed across these commits: " +
    stepResults.map((s) => s.commit).filter(Boolean).join(", ") +
    ".\n\nRun tier 0 and tier 1 for the changed packages, plus each higher tier the change reached: " +
    (tierSet.join(", ") || "as determined from the diff") +
    ". Use `lenny-test --changed --max-tier <tier>` and bring infrastructure up as needed. Report green=true only when every reached tier passes. Also run `lenny-test coverage --diff` against the pre-implementation commit and report the changed-line coverage. List any failures precisely.",
  { schema: VERIFY, label: "verify", phase: "Verify" },
);

if (verify && !verify.green && verify.failures && verify.failures.length > 0) {
  log("Verification found failures; one fix pass");
  await agent(
    "Fix the test failures from the final verification of an applied spec proposal's implementation.\n\n" +
      "Work in " +
      repo +
      ". Proposal: " +
      proposal +
      ". Failures:\n" +
      verify.failures.map((f) => "- " + f).join("\n") +
      "\n\nFix the code (not the spec) until every reached tier is green, commit the fixes, and keep the change minimal. " +
      RULES,
    { label: "verify-fix", phase: "Verify" },
  );
  verify = await agent(
    "Re-run the reached tiers for the implementation in " +
      repo +
      " and report whether everything is now green. Tiers: " +
      (tierSet.join(", ") || "from the diff") +
      ". Use `lenny-test --changed --max-tier <tier>`. Report green, the tiers run, the changed-line coverage, and any remaining failures.",
    { schema: VERIFY, label: "verify-rerun", phase: "Verify" },
  );
}

return {
  status: "implemented",
  blastRadius: plan.blastRadius,
  steps: stepResults,
  commits: stepResults.map((s) => s.commit).filter(Boolean),
  green: !!(verify && verify.green),
  changedLineCoverage: verify ? verify.changedLineCoverage : undefined,
  failures: verify ? verify.failures || [] : ["final verification did not run"],
};
