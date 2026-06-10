#!/usr/bin/env bash
#
# close-build-gaps.sh
#
# Drives `claude -p` in a loop to work BUILD-GAPS.md findings. Each
# iteration invokes a fresh non-interactive Claude session whose prompt
# is the standing batch-processing instructions; Claude picks the next
# batch, does the work, commits, and updates BUILD-GAPS.md. Persistent
# state lives in git history + BUILD-GAPS.md, so the script is safely
# re-runnable.
#
# Two modes (select with --mode; default general):
#
#   general    Close every OPEN finding in BUILD-GAPS.md (the long-
#              standing build loop, rules A-P). A finding that requires a
#              spec change and references an approved proposal is applied
#              with the implement-proposal skill (apply-only) first, then built; everything
#              else is built directly. Stops when all findings are CLOSED.
#
#   proposals  Close ONLY OPEN findings that reference an approved spec
#              proposal, by fully implementing the proposal — apply its
#              spec edits with the implement-proposal skill (apply-only), implement the entire
#              spec change's blast radius in code, write and run tests to
#              green, mark CLOSED. Never opens, re-opens, or generates a
#              proposal. Stops when no OPEN finding references an approved
#              proposal (the NO-QUALIFYING-FINDINGS sentinel). This is the
#              mode the build-gaps-spec-unblock workflow drives.
#
# Proposals are generated, reviewed, and approved OUTSIDE this script
# (the spec-proposal skill plus human sign-off).
# This script never writes or approves proposals.
#
# Stop conditions:
#   - Mode's natural completion (all CLOSED in general; the sentinel in
#     proposals) — success, exit 0.
#   - MAX_ITER reached (default 1000) — exit 1.
#
# Logs land in <cwd>/tmp/ — run the script from the Lenny repo root.
# A single running log (close-build-gaps.log) collects each iteration's
# short summary, demarcated by a header line. The summary log
# (summary.log) keeps one-line per-iteration progress pings. The running
# log is passed to each next Claude session so the loop carries
# continuity even though each `claude -p` is stateless.
#
# Usage (from the Lenny repo root):
#   ./close-build-gaps.sh                      # general mode (default)
#   ./close-build-gaps.sh --mode proposals     # implement approved-proposal findings
#   ./close-build-gaps.sh --proposals          # alias for --mode proposals
#   ./close-build-gaps.sh --max-iter 50
#   ./close-build-gaps.sh --dry-run            # print the selected mode's prompt; do not invoke
#

set -uo pipefail

REPO="$(pwd)"
LOG_DIR="$REPO/tmp"
MAX_ITER=1000
DRY_RUN=0
MODE=general

# Sanity-check: refuse to run outside a Lenny checkout.
if [[ ! -f "$REPO/BUILD-GAPS.md" ]]; then
  echo "error: $REPO/BUILD-GAPS.md not found — run this script from the Lenny repo root." >&2
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) MODE="$2"; shift 2 ;;
    --proposals|--proposals-only) MODE=proposals; shift ;;
    --general) MODE=general; shift ;;
    --max-iter) MAX_ITER="$2"; shift 2 ;;
    --dry-run)  DRY_RUN=1; shift ;;
    -h|--help)
      sed -n '3,53p' "$0"
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

if [[ "$MODE" != "general" && "$MODE" != "proposals" ]]; then
  echo "error: --mode must be 'general' or 'proposals' (got '$MODE')" >&2
  exit 1
fi

mkdir -p "$LOG_DIR"
SUMMARY="$LOG_DIR/summary.log"
RUNNING_LOG="$LOG_DIR/close-build-gaps.log"

# grep -c prints "0" and exits 1 when there are no matches; a bare
# `|| echo 0` would then emit a second "0", yielding the two-line
# string "0\n0" that breaks the `(( open == 0 ))` stop check. Capture
# grep's output and default only when it is empty (missing file).
# count_open counts every OPEN finding. In general mode that is the
# work set and its reaching 0 is the stop condition; in proposals mode
# some OPEN findings are out of scope (only approved-proposal ones
# qualify), so the proposals stop condition is the NO-QUALIFYING-FINDINGS
# sentinel below, and count_open feeds only logging and the all-closed
# fast path.
count_open()   { local n; n=$(grep -c '^### - \[ \] F-.*— OPEN' "$REPO/BUILD-GAPS.md" 2>/dev/null); echo "${n:-0}"; }
count_closed() { local n; n=$(grep -c '— CLOSED'                "$REPO/BUILD-GAPS.md" 2>/dev/null); echo "${n:-0}"; }

# ---- General-mode prompt: close every OPEN finding (rules A-P). ----
read -r -d '' PROMPT_GENERAL <<'PROMPT_EOF' || true
You are continuing a long-running effort to close every OPEN finding in BUILD-GAPS.md. Each invocation handles one batch and exits.

WORKING DIRECTORY: the current working directory (the Lenny repo root). All file paths in this prompt are relative to it.

CONTINUITY: the running log of every prior iteration's short summary is at the path in `LENNY_RUNNING_LOG`. Start by reading its tail (`tail -300 "$LENNY_RUNNING_LOG"`) when it exists and is non-empty, so you know which findings were just closed, what was deferred, and what each prior batch reported. Use it to avoid re-attempting work and to extend rather than duplicate prior batches.

RULES (apply to every action this invocation)

A. **Re-read the spec and re-verify before fixing.** Before addressing any gap, re-read the cited spec section and check that the gap is not a false alarm and has not already been addressed by a prior batch. If a finding is already resolved, mark CLOSED with a one-line resolution note pointing at the commit that resolved it and move on.

B. **All code MUST comply with the spec.** Cite the relevant spec section (e.g. `// spec: §X.Y line N`) in code comments and test names. Files under `spec/` may be modified ONLY when both conditions hold: (1) the finding's description references a spec proposal under `proposals/` that has been adversarially verified and whose Status bullet records approval for implementation, and (2) the spec edits are applied with the `implement-proposal` skill in apply-only mode (invoke the Skill tool with skill `implement-proposal` and the argument `<proposal-path> apply-only`) — never by hand-editing `spec/`. The skill applies the proposal's staged spec edits verbatim and verifies exact alignment until clean; the project guard hook blocks direct spec writes unless an approved proposal is pending application. Ordering is mandatory: when a finding requires spec changes, land them FIRST (run `implement-proposal` in apply-only mode, which applies, verifies, and commits the spec edits), and only then implement the code against the updated spec. If a fix would require changing the spec and the finding references no such proposal, stop and report — defer per rule P.

C. **Always write tests for code you modify or create.** See `TESTING.md` for the tier model. Achieve good unit-test coverage first (tier-1), then add higher-level tests where appropriate — component (tier 2), contract (tier 3), integration (tier 4), e2e (tier 5), chaos (tier 8), security (tier 9), all the way up to load tests (tier 7/12) when warranted.

D. **Test scenarios cover all corner cases and eventualities mentioned in the spec.** Happy path is not enough — exercise the empty / error / concurrent / boundary / spec-named-failure paths.

E. **Code and tests reference the relevant spec sections.** Same citation form as rule B; include the spec reference in test function names or `// spec:` comments.

F. **Commit after each batch — or more frequently when appropriate.** Prefer one logical group per commit. Between commits, run `go build ./...` so a disconnect leaves the working tree buildable.

G. **Run regression tests — focus on tiers relevant to the change and dependent packages.** Always include tier-0 (static: `go build`, `go vet`) and tier-1 (unit: `go test ./pkg/...` scoped to the packages you touched). Also add higher tiers to ensure your code changes didn't break anything else.

H. **Best practices.** Modular code, reuse over duplication, sensible package structure, comments only when they explain *why* (not what), small functions, small modules.

I. **Reuse existing packages.** Before creating a new package or module, search the codebase for an existing one that should be reused or extended. Cross-reference with the §4-§17 component layout in the spec.

J. **Mark every fixed finding CLOSED in BUILD-GAPS.md.** Replace `— OPEN` with `— CLOSED` on the heading and add a one or two sentence Resolution note (citing the commit SHA) at the bottom of the finding block. Defer only under the conditions in rule P; when you legitimately defer, replace `— OPEN` with `— DEFERRED` and state the exact spec change or `NEEDS-OPERATOR:` resource required — never a vaguer reason.

K. **Potential duplicates.** When a finding's text flags a "Potential duplicate" or "Potential overlap", verify the cross-reference; when you confirm overlap, close the duplicate in the same batch with a "Closed by F-X.Y.Z" resolution note.

L. **No backward-compatibility shims.** The codebase is pre-deployment; change interfaces freely.

M. **Tread carefully — this is a large codebase.** Search broadly (do not restrict yourself to the file/line pointers in the finding text — they may be stale) and read enough surrounding context before each change.

N. **Re-attempt unblocked deferrals.** Before exiting, scan BUILD-GAPS.md for DEFERRED findings whose Resolution notes cite this batch's closed F-IDs as the blocker. Re-attempt any that are now unblocked and mark CLOSED (or re-DEFERRED with a fresh note). Tackle only direct unblocks; do not chase second-order unblocks.

O. **Do not blindly implement proposed fixes.** Before addressing any gap, re-assess the finding's proposed fix against the spec and the current codebase, as the code may have changed significantlysince the finding was reported.

P. **A required spec change with no approved proposal is the ONLY valid reason to defer — close everything else.** The single legitimate defer is a finding whose fix cannot be made without editing a file under `spec/` AND whose description references no approved spec proposal under `proposals/`: a missing, contradictory, or genuinely undefined spec surface with no staged resolution (rule B). Mark only those `— DEFERRED`, and state the exact spec change required. A finding that references an approved proposal is NOT deferrable on spec grounds: apply its staged spec edits with the `implement-proposal` skill in apply-only mode first, then build the finding (rule B). Build and close everything else. Do NOT defer because the work is a "large workstream", a "separate workstream not begun", an "external dependency", or "infrastructure unavailable in this environment" — build the missing prerequisite (controllers, stores, clients, migrations, gateway/adapter wiring, reference runtimes, and the integration/e2e/chaos tests that exercise it) and run it against the infrastructure listed in AVAILABLE INFRASTRUCTURE below. "Needs a Kubernetes cluster / Redis / Postgres / MinIO / envtest" is NOT a blocker here: `kind`, `docker compose`, and envtest are installed and usable — stand them up and use them. There is no "Rule P escape" or "criterion" — those phrases are not part of this rule; do not invent them. The lone exception is a finding that truly requires a resource not present on this host and not substitutable locally (a paid cloud-provider account such as GKE/AKS + managed CloudSQL/Memorystore/GCS/Azure DB, a live external SaaS API such as OpenAI Threads/Runs or the external CrewAI framework, or a gVisor-enabled host RuntimeClass). Do NOT silently defer those: STOP and report each in your summary on a `NEEDS-OPERATOR:` line naming the exact missing resource, and mark the finding `— DEFERRED` with that same `NEEDS-OPERATOR:` reason. Any `— DEFERRED` whose stated reason is neither a spec change nor a `NEEDS-OPERATOR:` escalation is a defect in your batch — fix it by building the finding.

AVAILABLE INFRASTRUCTURE (use it — "no infra" is not a valid defer)

This host has a running Docker daemon plus `kind`, `kubectl`, `helm` (with the `unittest` plugin), and the Go toolchain. Cluster-bound, integration, e2e, and chaos work is therefore runnable locally — bring the infrastructure up and exercise the code against it instead of deferring. Use the repo's own harnesses; do not hand-roll new ones.

- **Local Kubernetes cluster** (tier-4 integration, tier-5 e2e: real pods, EndpointSlice, NetworkPolicy, admission webhooks, CRDs, pod lifecycle, drain). Canonical harness: `tests/testinfra/kind` — tests call `kind.SkipUnlessAvailable(t)` then `kind.EnsureCluster(t)` / `kind.InstallLenny(t)`. Stand a cluster up out-of-band with `scripts/setup-cluster.sh --reuse` (cluster `lenny-test`) or `bash tests/testinfra/kind/install.sh` (installs the chart + in-cluster Postgres/Redis/MinIO + the two-pool agent-pod workload as cluster `lenny-e2e`). Run the suite with its build tag: `go test -tags e2e_kind ./tests/tier5_e2e_kind/...`. Reuse the cluster across iterations (bring-up costs ~30s–minutes); set `LENNY_KIND_TEARDOWN=1` only when you want it deleted.
- **Component tier with a real kube-apiserver + etcd (envtest)** (tier-2 controller/reconciler tests). Wrapper: `tests/testinfra/envtest`. Install the assets once via `scripts/setup-dev.sh --include kubernetes`, or directly: `go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest && export KUBEBUILDER_ASSETS="$(setup-envtest use -p path 1.31.0)"`. Then `go test ./tests/tier2_component/...` and the envtest-gated `pkg/...` tests run instead of skipping.
- **Real Redis / Postgres / MinIO / Redis-Sentinel** (durability, fail-open recovery + MAX-rule reconciliation, Sentinel failover, audit-to-Postgres, blob escrow). `make compose` brings up the full local stack from `compose/default.yml` (gateway + echo + Postgres + Redis + MinIO); `make compose-down` tears it down. Point tests at it via `LENNY_POSTGRES_DSN`, `LENNY_REDIS_SENTINEL_ADDRS`, `LENNY_REDIS_SENTINEL_MASTER`, `LENNY_MINIO_ENDPOINT`; the gate is `tests/testinfra/compose`. Run a stray container directly with `docker run` when a single dependency is all you need.
- **Helm chart render / inventory / admission**: `helm unittest charts/lenny` for render assertions, and `helm template` / `helm install` against the kind cluster for live-render and admission-webhook coverage.

YOUR TASK THIS INVOCATION

1. Pick the next 4–8 OPEN findings from BUILD-GAPS.md. Prefer clustering on a shared §X.Y section or on text-flagged duplicates so one fix can close several. Start with:

       grep -n '^### - \[ \] F-.*— OPEN' BUILD-GAPS.md | head -20

   Read the full text of each candidate (the heading plus the bulleted Spec/Evidence/Gap/Suggested-resolution lines) before committing to the batch.

2. Apply the RULES above to each finding in the batch.

3. Output a TIGHT summary (≤200 words; the loop appends it to a running log, so brevity matters). Format:
   - Findings closed: bullet list of IDs with a half-line each.
   - Duplicates also closed (one bullet each).
   - Findings deferred: ID + half-line reason (or "none").
   - Commits: SHAs only.
   - Tests added: count + tier (one line).
   Do NOT restate the rules, the spec, file paths, or per-finding diffs in the summary.

WHEN DONE

Exit cleanly. A driver loop will invoke a fresh session for the next batch. The hook-tracked goal is "all findings in BUILD-GAPS.md are marked as closed"; the loop terminates when `grep -c '^### - \[ \] F-.*— OPEN' BUILD-GAPS.md` returns 0.
PROMPT_EOF

# ---- Proposals-mode prompt: implement approved-proposal findings only. ----
read -r -d '' PROMPT_PROPOSALS <<'PROMPT_EOF' || true
You are implementing approved spec-change proposals. Each invocation closes one batch of OPEN findings in BUILD-GAPS.md that reference an approved spec proposal, then exits. A finding is closed only when the proposal it references is fully implemented across spec, code, and tests.

WORKING DIRECTORY: the current working directory (the Lenny repo root). All file paths in this prompt are relative to it.

CONTINUITY: the running log of every prior iteration's short summary is at the path in `LENNY_RUNNING_LOG`. Start by reading its tail (`tail -300 "$LENNY_RUNNING_LOG"`) when it exists and is non-empty, so you know which findings were just closed and what each prior batch reported. Use it to avoid re-attempting work.

SCOPE — which findings this loop closes

S1. **Only OPEN findings that reference an approved spec proposal.** A finding qualifies only when its body references a proposal file under `proposals/` whose Status bullet begins with "Approved" (approved for implementation; a proposal whose Status begins "Draft" or "Verified" is NOT yet approved, and one that begins "Applied to spec" has already had its spec edits landed — see R2). Find candidates with:

       grep -n '^### - \[ \] F-.*— OPEN' BUILD-GAPS.md | head -40

   then, for each, read its body, extract the referenced `proposals/NNNN_*.md` path, and read that proposal's Status bullet. Skip every OPEN finding that references no proposal or whose proposal is not approved — those are out of scope for this loop; leave them untouched.

S2. **If no OPEN finding references an approved proposal, output exactly `NO-QUALIFYING-FINDINGS` as the entire summary and exit without any change.** The driver loop stops when it sees this sentinel.

S3. **This loop only closes findings.** Never open, re-open, create, or re-DEFER a finding (the sole DEFER exception is the narrow NEEDS-OPERATOR case below). Never generate, edit, approve, or resolve open decisions in a proposal — proposals arrive pre-generated, reviewed, and approved with every decision already resolved. Your job is to implement them as written; the proposal text is authoritative wherever it and a finding differ.

PER-FINDING RECIPE — apply to every finding in the batch, in this order. Cluster findings that share one proposal and implement them together so the proposal is applied once.

R1. **Read the referenced proposal in full** — every section (Problem, Decisions, Detailed design, Proposed spec changes, CRD/RBAC changes, Observability, Testing, Files touched). The proposal, not the finding, defines the complete change. The finding is one entry point into a larger change.

R2. **Apply the spec changes first, with the implement-proposal skill in apply-only mode.** Invoke the Skill tool with skill `implement-proposal` and the argument `<proposal-path> apply-only`. It applies the proposal's staged spec edits to `spec/`, verifies exact alignment, commits them, and flips the proposal Status to "Applied to spec". Never hand-edit `spec/` (the guard hook blocks direct writes unless an approved proposal is pending). If the proposal's Status already reads "Applied to spec" (a sibling finding in this or a prior batch already landed it), skip the apply step and proceed to R3. If the apply-only run reports blockers (unappliable edits, drifted anchors), STOP and report on a `NEEDS-OPERATOR:` line — do not hand-patch.

R3. **Implement the ENTIRE spec change across the codebase — the full blast radius.** Do NOT limit yourself to the literal finding. Implement everything the proposal specifies: every new field, RPC, condition, metric, controller behavior, gate, schema, and wiring change in its Detailed design / CRD-RBAC / Observability sections. Search the codebase broadly for every existing call site, builder, store, controller, reconciler, schema, and chart template the spec change touches, and update all of them. The change is complete only when the proposal is fully reflected in the code, not merely when the one symptom the finding names is gone.

R4. **Create or modify all relevant tests.** Cover the proposal's Testing section and the corner / error / concurrent / boundary / spec-named-failure paths across the appropriate tiers (unit → component → integration → e2e, plus security/chaos where the change warrants). Reference the spec section in test names or `// spec:` comments (form: `// spec: §X.Y`).

R5. **Run the relevant tests and make them pass.** Always tier-0 (`go build ./...`, `go vet`) and tier-1 (`go test` on the packages you touched and their dependents). Run the higher tiers the change touches against the AVAILABLE INFRASTRUCTURE below. A finding is NOT closed until its tests pass; when a test fails, fix the code until it is green.

R6. **Mark the finding CLOSED in BUILD-GAPS.md** only after R2-R5 are complete and green: replace `— OPEN` with `— CLOSED` on the heading and add a one or two sentence Resolution note citing the commit SHA(s). When a finding flags a "Potential duplicate" you confirm, close the duplicate in the same batch with a "Closed by F-X.Y.Z" note.

GENERAL RULES

A. **Re-verify before implementing.** Re-read the cited spec section (as updated by R2) and the current code; it may have changed since the finding was filed. If a finding is already fully resolved, mark CLOSED with a note pointing at the resolving commit.
F. **Commit logical groups as you go** — spec edits separately from code (per R2). Run `go build ./...` between commits so a disconnect leaves the tree buildable.
H. **Best practices.** Modular code, reuse over duplication, sensible package structure, comments only when they explain *why*.
I. **Reuse existing packages.** Search for an existing package to reuse or extend before creating a new one; cross-reference the §4-§17 component layout in the spec.
L. **No backward-compatibility shims.** The codebase is pre-deployment; change interfaces freely.
M. **Tread carefully — large codebase.** The file/line pointers in findings may be stale; search broadly and read enough surrounding context before each change.

DEFER — narrow

The only valid defer is (a) a finding that truly requires a host resource not present here and not substitutable locally (a paid cloud account such as GKE/AKS + managed CloudSQL/Memorystore/GCS/Azure DB, a live external SaaS API, or a gVisor-enabled host RuntimeClass), or (b) an implement-proposal apply-only run that reports unappliable blockers (R2). Report each on a `NEEDS-OPERATOR:` line and mark the finding `— DEFERRED` with that reason. Never defer a finding for spec reasons — its proposal is approved, so apply it. Everything else is buildable here; build it.

AVAILABLE INFRASTRUCTURE (use it — "no infra" is not a valid defer)

This host has a running Docker daemon plus `kind`, `kubectl`, `helm` (with the `unittest` plugin), and the Go toolchain. Cluster-bound, integration, e2e, and chaos work is therefore runnable locally — bring the infrastructure up and exercise the code against it instead of deferring. Use the repo's own harnesses; do not hand-roll new ones.

- **Local Kubernetes cluster** (tier-4 integration, tier-5 e2e: real pods, EndpointSlice, NetworkPolicy, admission webhooks, CRDs, pod lifecycle, drain). Canonical harness: `tests/testinfra/kind` — tests call `kind.SkipUnlessAvailable(t)` then `kind.EnsureCluster(t)` / `kind.InstallLenny(t)`. Stand a cluster up out-of-band with `scripts/setup-cluster.sh --reuse` (cluster `lenny-test`) or `bash tests/testinfra/kind/install.sh` (installs the chart + in-cluster Postgres/Redis/MinIO + the two-pool agent-pod workload as cluster `lenny-e2e`). Run the suite with its build tag: `go test -tags e2e_kind ./tests/tier5_e2e_kind/...`. Reuse the cluster across iterations (bring-up costs ~30s–minutes); set `LENNY_KIND_TEARDOWN=1` only when you want it deleted.
- **Component tier with a real kube-apiserver + etcd (envtest)** (tier-2 controller/reconciler tests). Wrapper: `tests/testinfra/envtest`. Install the assets once via `scripts/setup-dev.sh --include kubernetes`, or directly: `go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest && export KUBEBUILDER_ASSETS="$(setup-envtest use -p path 1.31.0)"`. Then `go test ./tests/tier2_component/...` and the envtest-gated `pkg/...` tests run instead of skipping.
- **Real Redis / Postgres / MinIO / Redis-Sentinel** (durability, fail-open recovery + MAX-rule reconciliation, Sentinel failover, audit-to-Postgres, blob escrow). `make compose` brings up the full local stack from `compose/default.yml` (gateway + echo + Postgres + Redis + MinIO); `make compose-down` tears it down. Point tests at it via `LENNY_POSTGRES_DSN`, `LENNY_REDIS_SENTINEL_ADDRS`, `LENNY_REDIS_SENTINEL_MASTER`, `LENNY_MINIO_ENDPOINT`; the gate is `tests/testinfra/compose`. Run a stray container directly with `docker run` when a single dependency is all you need.
- **Helm chart render / inventory / admission**: `helm unittest charts/lenny` for render assertions, and `helm template` / `helm install` against the kind cluster for live-render and admission-webhook coverage.

YOUR TASK THIS INVOCATION

1. Apply SCOPE to pick the batch (4-8 findings, clustering those that share one proposal). If none qualify, emit `NO-QUALIFYING-FINDINGS` and exit.
2. Apply the PER-FINDING RECIPE to each.
3. Output a TIGHT summary (≤200 words; the loop appends it to a running log, so brevity matters). Format:
   - Findings closed: bullet list of IDs with a half-line each.
   - Duplicates also closed (one bullet each).
   - Deferred: ID + NEEDS-OPERATOR reason (or "none").
   - Commits: SHAs only.
   - Tests added/modified: count + tier (one line).
   When nothing qualified, the summary is the single token `NO-QUALIFYING-FINDINGS` and nothing else. Do NOT restate the rules, the spec, file paths, or per-finding diffs.

WHEN DONE

Exit cleanly. A driver loop invokes a fresh session for the next batch until no OPEN finding references an approved proposal.
PROMPT_EOF

# Select the prompt and the resume prompt for the chosen mode.
if [[ "$MODE" == "proposals" ]]; then
  PROMPT="$PROMPT_PROPOSALS"
  RESUME_PROMPT='Resume the prior batch. Re-read BUILD-GAPS.md and the proposal(s) you were implementing, finish any in-flight work cleanly (apply remaining spec via the implement-proposal skill (apply-only), finish the code blast radius, get tests green, commit, mark CLOSED, brief summary), and exit. Keep the summary ≤200 words.'
else
  PROMPT="$PROMPT_GENERAL"
  RESUME_PROMPT='Resume the prior batch. Re-read BUILD-GAPS.md to see the current state, finish any in-flight work cleanly (commit + mark CLOSED + brief summary), and exit. Keep the summary ≤200 words.'
fi

if (( DRY_RUN )); then
  echo "=== DRY RUN — mode=$MODE — prompt that would be sent ==="
  printf '%s\n' "$PROMPT"
  echo "==========================================="
  exit 0
fi

# run_claude_capture invokes `claude -p --output-format json`,
# captures both the session_id (for resume on failure) and the
# textual result. Outputs land in the script-scope globals
# CLAUDE_SESSION, CLAUDE_RESULT, CLAUDE_RC so the caller can branch
# on rc and resume by id.
CLAUDE_SESSION=""
CLAUDE_RESULT=""
CLAUDE_RC=0

run_claude_capture() {
  local prompt="$1"
  local resume_id="$2"
  local raw
  raw=$(mktemp)
  local cmd=(claude -p
        --permission-mode auto
        --add-dir "$REPO"
        --model claude-opus-4-8
        --effort xhigh
        --output-format json)
  if [[ -n "$resume_id" ]]; then
    cmd+=(--resume "$resume_id")
  fi
  cmd+=("$prompt")

  LENNY_RUNNING_LOG="$RUNNING_LOG" "${cmd[@]}" >"$raw" 2>&1
  CLAUDE_RC=$?

  # Parse the JSON envelope (success path) or pass the raw text
  # through (on parse failure — usually a CLI-level error).
  CLAUDE_SESSION=$(python3 - "$raw" <<'PY' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    print(d.get("session_id", ""))
except Exception:
    pass
PY
)
  CLAUDE_RESULT=$(python3 - "$raw" <<'PY' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    print(d.get("result") or d.get("error") or "")
except Exception:
    with open(sys.argv[1]) as f:
        print(f.read())
PY
)
  if [[ -z "$CLAUDE_RESULT" ]]; then
    CLAUDE_RESULT=$(cat "$raw")
  fi
  rm -f "$raw"
}

# run_iter executes one batch, appends Claude's short summary to
# the running log, and emits exactly one progress line to the
# summary log. On non-zero exit it retries up to MAX_RETRIES by
# resuming the same session.
# Returns 0 if there is more work to do, 2 if the mode's stop condition
# is met (general: all findings CLOSED; proposals: no qualifying finding
# remains — the NO-QUALIFYING-FINDINGS sentinel, all findings closed, or
# a clean session that closed nothing).
MAX_RETRIES=3

run_iter() {
  local iter="$1"
  local before_closed before_open after_closed after_open delta
  before_closed=$(count_closed)
  before_open=$(count_open)

  if (( before_open == 0 )); then
    printf '[%s] iter=%d open=0 — all findings CLOSED\n' \
      "$(date -Iseconds)" "$iter" | tee -a "$SUMMARY"
    return 2
  fi

  cd "$REPO" || { echo "cd $REPO failed"; return 0; }

  printf '\n========== iter=%d  mode=%s  %s  closed=%s  open=%s ==========\n' \
    "$iter" "$MODE" "$(date -Iseconds)" "$before_closed" "$before_open" \
    >>"$RUNNING_LOG"

  # First attempt: fresh session.
  run_claude_capture "$PROMPT" ""
  printf '%s\n' "$CLAUDE_RESULT" >>"$RUNNING_LOG"

  # Retries: resume the captured session id when we have one;
  # otherwise restart fresh.
  local retries=0
  while (( CLAUDE_RC != 0 )) && (( retries < MAX_RETRIES )); do
    retries=$(( retries + 1 ))
    if [[ -n "$CLAUDE_SESSION" ]]; then
      printf '[retry %d/%d resuming session=%s]\n' \
        "$retries" "$MAX_RETRIES" "$CLAUDE_SESSION" >>"$RUNNING_LOG"
      run_claude_capture "$RESUME_PROMPT" "$CLAUDE_SESSION"
    else
      printf '[retry %d/%d no session captured — fresh start]\n' \
        "$retries" "$MAX_RETRIES" >>"$RUNNING_LOG"
      run_claude_capture "$PROMPT" ""
    fi
    printf '%s\n' "$CLAUDE_RESULT" >>"$RUNNING_LOG"
  done

  after_closed=$(count_closed)
  after_open=$(count_open)
  delta=$(( after_closed - before_closed ))

  # Mode-specific completion. General mode terminates only when every
  # OPEN finding is CLOSED (caught by the before_open==0 check at the top
  # of the next iteration). Proposals mode terminates when the session
  # reports no qualifying finding remains: the affirmative
  # NO-QUALIFYING-FINDINGS sentinel is primary, and a clean run (rc==0)
  # that closed nothing is the backstop, since a well-behaved session
  # either closes at least one finding or emits the sentinel.
  local done=0
  if [[ "$MODE" == "proposals" ]]; then
    if printf '%s' "$CLAUDE_RESULT" | grep -q 'NO-QUALIFYING-FINDINGS'; then
      done=1
    elif (( CLAUDE_RC == 0 && delta == 0 )); then
      done=1
    fi
  fi

  printf '[%s] iter=%d mode=%s delta=+%d closed=%s open=%s rc=%d retries=%d done=%d\n' \
    "$(date -Iseconds)" "$iter" "$MODE" "$delta" "$after_closed" "$after_open" \
    "$CLAUDE_RC" "$retries" "$done" \
    | tee -a "$SUMMARY"

  if (( done )); then
    return 2
  fi
  return 0
}

# Keep the machine awake for the duration of the run. On macOS, an
# unattended Mac drops into Deep Idle and runs Power Nap maintenance
# sleep cycles (8-17 min each) even on AC with `pmset sleep 0`. Those
# sleep windows suspend the network stack and tear down the long-lived
# HTTPS connection to the API, which surfaces as "socket connection was
# closed unexpectedly" and burns whole iterations on retries. `caffeinate
# -dimsu` holds display, system, and disk awake; `-w $$` binds the
# assertion to this script's PID, so it releases when the loop exits.
CAFFEINATE_PID=""
if command -v caffeinate >/dev/null 2>&1; then
  caffeinate -dimsu -w "$$" &
  CAFFEINATE_PID=$!
  trap '[[ -n "$CAFFEINATE_PID" ]] && kill "$CAFFEINATE_PID" 2>/dev/null' EXIT
  printf '[%s] caffeinate holding display+system awake (pid %s, bound to driver pid %s)\n' \
    "$(date -Iseconds)" "$CAFFEINATE_PID" "$$" | tee -a "$SUMMARY"
fi

iter=0
completed=0
while (( iter < MAX_ITER )); do
  iter=$(( iter + 1 ))
  run_iter "$iter"
  rc=$?
  if (( rc == 2 )); then
    completed=1
    break
  fi
done

final_closed=$(count_closed)
final_open=$(count_open)
printf '[%s] FINAL mode=%s iter=%d closed=%s open=%s completed=%d\n' \
  "$(date -Iseconds)" "$MODE" "$iter" "$final_closed" "$final_open" "$completed" \
  | tee -a "$SUMMARY"

# Exit 0 on the mode's natural completion. General mode: every finding
# CLOSED. Proposals mode: no qualifying finding remained (the loop
# stopped on the sentinel/all-closed). Otherwise the loop hit MAX_ITER
# with work possibly remaining — exit 1. In proposals mode, OPEN findings
# that reference no approved proposal are out of scope and do not affect
# the exit code.
if [[ "$MODE" == "proposals" ]]; then
  if (( completed )); then exit 0; fi
  exit 1
else
  if (( final_open == 0 )); then exit 0; fi
  exit 1
fi
