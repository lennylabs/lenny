export const meta = {
  name: "change-proposal",
  description:
    "Validate a problem, draft and write a change proposal — spec edits and/or core-product or test-infra code changes (new mode) — then adversarially review and fix it until two consecutive clean rounds",
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
if (mode !== "new" && mode !== "review") {
  throw new Error('args.mode must be "new" or "review"');
}
if (mode === "new") {
  for (const k of ["problem", "nextNumber"]) {
    if (!input[k])
      throw new Error("args." + k + " is required in new mode and missing");
  }
} else if (!input.proposalPath) {
  throw new Error("args.proposalPath is required in review mode and missing");
}

const repo = input.repoRoot;
const date = input.date;
const exemplar = input.exemplar;
const context = input.context || "none provided";
const maxRounds = input.maxReviewRounds || 12;
const CLEAN_TARGET = 2;

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
        },
      },
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
      "Format: follow the structure of " +
      exemplar +
      ' exactly (read it first): the "# Proposal:" title; Status ("Draft for review."), Date, and Scope bullets; the staging boilerplate paragraph; numbered sections (Problem with file:line citations and any finding IDs the input named; Decisions; design sections; Proposed changes with one subsection per target (spec file and section, code package, or test) and an anchor instruction plus a fenced block of the exact text to insert or a precise change description; Non-goals; Testing (list the specific, insightful, relevant new tests to add during implementation — one per behavior the proposal changes, mapped to the tiers the change reaches per .claude/rules/test-coverage.md, each covering the non-happy-path it needs (empty, error, concurrent, boundary, and spec-named-failure) and carrying a // spec: tie, rather than a vague "add tests" note); Findings closed on application; Resolved in adversarial review, initially noting that review rounds populate it; Open decisions for review when the draft has open questions; Files touched on application consistent with the staged changes).\n' +
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
  "DO NOT report: style or wording, documentation polish, optional improvements, additional nice-to-have tests beyond the coverage the change requires, hypothetical hardening, redundancy, preferences between workable designs, or anything whose absence does not make the applied spec or implementation wrong. If you are unsure whether something meets the bar, do not report it. An empty findings list is a fully acceptable answer and is the expected answer for a converged proposal.\n\n" +
  'The proposal\'s "Resolved in adversarial review" section is a historical record of earlier passes; its descriptions of earlier drafts are not findings. Sections recording deliberately open decisions for the human reviewer are not findings.\n\n' +
  "Every finding MUST carry evidence: exact file paths with line numbers and short quotes for both the proposal's claim and the contradicting source. Read the files to verify line numbers; never cite from memory.";

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
    text: "Lens: client-facing surface integrity. Always run. Identify every externally-consumed contract the proposal adds, changes, or removes, and verify the change is intentional and complete across all of its parallel representations. The client-facing surfaces are the REST API (section 15.1) and its hand-authored OpenAPI document (pkg/gateway/openapi/openapi.json, which the served MCP create_session tool schema and the client SDKs derive from); the MCP and A2A external-protocol surfaces (section 15.2), the lenny/* tool names, and their input schemas; the OpenAI-completions surface; the gateway-to-adapter wire proto (schemas/lenny-adapter.proto) and the JSONL and lifecycle-events schemas (schemas/*.json) with their tier-3 wire-contract tests; the adapter manifest field set (section 4.7) the runtime reads; the runtime and client SDK type files in every language (sdks/runtime/{go,python,typescript}, sdks/client/{go,python,typescript}); the CRD schemas operators apply (charts/lenny/crds and the pkg/embedded/crds copies); client-visible enums, error codes, and event types clients branch on; and the client-facing docs (docs/api/*, docs/client-guide/*, docs/runtime-author-guide/*). A change to one representation that is not mirrored in its parallels is a finding: a REST field missing from the OpenAPI document, the MCP tool schema, an SDK language, or the docs; a wire, proto, or JSONL change that omits a language SDK or its tier-3 contract test; a removed or renamed client-facing field still advertised by the served schema, an SDK, a CRD, or a doc; an enum or error-code value clients consume changed without every emitter and consumer updated. Also enforce the origin rule: a name an external standard defines (the MCP or A2A Task primitive and the protocol vocabulary clients interact with) must not be renamed, while Lenny-defined client surfaces may change; a rename that breaks the standard-aligned surface, or that leaves one client vocabulary half-renamed across representations, is a finding. The platform is pre-deployment with no backward-compatibility shims, so a deliberate, complete breaking change is not itself a finding; an incomplete or inconsistent client-facing change, or an internal surface changed while a parallel client surface still serves the old contract, is.",
  },
  {
    key: "docs-alignment",
    text: "Lens: documentation alignment. Always run. The docs/ tree is downstream of the spec and the implementation: docs follow the spec and the code and are never the source of truth for a spec or core-product decision. Identify every behavior the proposal changes — a spec edit, a code change, a renamed, removed, or added identifier, a changed default, error code, endpoint, flag, metric, alert, or lifecycle step — and verify it is reflected in a staged docs/ edit wherever docs/ currently describes that behavior, and that the staged docs edits leave docs/ internally consistent and consistent with the post-change spec. The docs surfaces are the concept and guide pages (docs/, docs/api/, docs/client-guide/, docs/runtime-author-guide/), the reference pages (docs/reference/, notably docs/reference/metrics.md), and the docs/runbooks/ pages that tests/tier11_docs resolves (alert-to-runbook slug resolution and examples). A docs/ page left describing superseded behavior, an added alert or metric missing its docs/runbooks or docs/reference companion, or a staged docs edit that contradicts the post-change spec, is a finding. Two hard guardrails on this lens: (1) never raise a finding that asks the spec or the implementation to change to match an existing doc; when a doc and the spec disagree the doc is the defect and is reconciled toward the spec, so a finding here is always a missing or wrong docs edit, never a spec or code edit. (2) A doc-described scenario may be cited as a candidate test case only after that doc has been verified against the spec, never as evidence for what the product should do.",
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
    "\n\nWork method: read the proposal fully, then investigate the repository with Grep and targeted Reads to verify or refute its claims under your lens. Report your findings via the structured output (empty array if you find nothing that meets the bar)."
  );
}

function dedupPrompt(findings) {
  return (
    "You merge duplicate review findings. Below is a JSON array of findings from several independent reviewers examining the same proposal. Merge entries that describe the same root error (even if phrased differently or found at different citation points): keep one entry per root error, choose the clearest title, and combine the strongest evidence. Do not drop distinct errors. Do not add new findings. Do not judge validity. Return the merged list.\n\nFindings:\n" +
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

function fixPrompt(confirmed, round) {
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
    "- When a fix changes a trigger predicate or invariant, propagate the exact same predicate to every section that states it (design sections, summary tables, constant comments, proposed spec text, and tests) so no drift is introduced.\n" +
    "- Keep the proposed-changes section (however the proposal titles it) and any files-touched section consistent with your edits.\n" +
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
let cleanStreak = 0;
let round = 0;
let reviewersFailed = false;

while (round < maxRounds && cleanStreak < CLEAN_TARGET) {
  round++;
  const lenses = LENSES.concat([EXTRAS[(round - 1) % EXTRAS.length]]);
  log(
    "Round " +
      round +
      ": launching " +
      lenses.length +
      " reviewers (clean streak " +
      cleanStreak +
      "/" +
      CLEAN_TARGET +
      ")",
  );

  // Barrier: the dedup step needs every reviewer's findings at once.
  const lensResults = await parallel(
    lenses.map(
      (l) => () =>
        robustAgent(reviewPrompt(l, round, fixedTitles, rejected), {
          label: "r" + round + ":review:" + l.key,
          phase: "Round " + round + ": review",
          schema: FINDINGS,
        }),
    ),
  );
  const failedLenses = lensResults.filter((r) => !r).length;
  const results = lensResults.filter(Boolean);

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
    if (roundComplete) cleanStreak++;
    else
      log(
        "Round " +
          round +
          ": zero findings but round incomplete; NOT counting as clean",
      );
    history.push({
      round,
      raw: 0,
      deduped: 0,
      confirmed: 0,
      complete: roundComplete,
    });
    continue;
  }

  let deduped = raw;
  if (raw.length > 1) {
    const d = await robustAgent(dedupPrompt(raw), {
      label: "r" + round + ":dedup",
      phase: "Round " + round + ": review",
      schema: FINDINGS,
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
  const confirmed = live
    .filter((v) => v.vs.length === 2 && v.vs.every((x) => x.confirmed))
    .map((v) => v.f);
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
  history.push({
    round,
    raw: raw.length,
    deduped: deduped.length,
    confirmed: confirmed.length,
    confirmedTitles: confirmed.map((f) => f.title),
    complete: roundComplete,
  });

  if (confirmed.length === 0) {
    if (roundComplete) cleanStreak++;
    else
      log(
        "Round " +
          round +
          ": zero confirmed findings but round incomplete (reviewer/verifier failures); NOT counting as clean",
      );
    continue;
  }
  cleanStreak = 0;

  const fixSummary = await robustAgent(fixPrompt(confirmed, round), {
    label: "r" + round + ":fix",
    phase: "Round " + round + ": fix",
  });
  confirmed.forEach((f) => fixedTitles.push(f.title));
  history[history.length - 1].fixSummary = fixSummary || "fixer unavailable";
}

const converged = cleanStreak >= CLEAN_TARGET && !reviewersFailed;
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
  review: {
    converged,
    reviewersFailed,
    rounds: round,
    cleanStreak,
    totalFixed: fixedTitles.length,
    fixedTitles,
    rejectedTitles: rejected.map((r) => r.title),
    history,
  },
};
