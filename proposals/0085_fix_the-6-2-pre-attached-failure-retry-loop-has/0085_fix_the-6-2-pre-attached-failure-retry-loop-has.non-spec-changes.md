# Non-spec changes: The §6.2 pre-attached retry loop has no stop condition for a terminal session

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.


## Design (implementation-facing)

No production behavior changes. The tree already makes one attempt at every persisted-row seam, which is what the amended §6.2 **Scope:** bullet (SPEC-1) requires there. The non-spec work has three parts:

- CODE-1 corrects the code comments and test annotations that SPEC-1 and SPEC-2 make false. No code token changes.
- TEST-1 pins the single attempt at `POST /v1/sessions/{id}/finalize`.
- RECORDS-1 records in BUILD-GAPS that the retry SPEC-1 keeps on `POST /v1/sessions/start` is unbuilt, and retargets TEST-GAPS T-6.2.10 to it.

Locate every site below by symbol and quoted text. The ordering against proposal 0082's code steps is in the summary's **Watch out for.**

## Staged code changes

### CODE-1 · comment and annotation sweep

Targets: `pkg/gateway/podlifecycle/podsession/binder.go`, `pkg/gateway/sessionserver/start.go`, `pkg/gateway/sessionserver/podclaimerror_internal_test.go`, and `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`. Rewrap each edited comment block with `gofumpt` conventions and change nothing else.

**(a) `Binder.Bind` stays unchanged.** Leave the `Bind` doc comment ("returned so the gateway can retry on a fresh pod") and the inline comment after `Launch` inside `Bind` ("Surface the error so the caller retries on a fresh pod.") as they are. Also leave the `Resume` doc comment and the `ErrNoConcurrentSlot` comment in `binder.go` as they are.

**(b) `Binder.Launch` doc comment** (`binder.go`). Replace

```go
// adapter connection. A launch failure reclaims the pod (and any lease
// assigned at Prepare) via failPhase and is returned so the gateway retries
// on a fresh pod. spec: §4.4 (proposal), §6.1, §5.1.
```

with

```go
// adapter connection. A launch failure reclaims the pod (and any lease
// assigned at Prepare) via failPhase and is returned to the caller. Whether
// the caller re-claims a fresh pod is the caller's policy: the §6.2
// pre-attached retry applies only inside the §7.1 atomic creation unit, and
// §7.3 governs a resume rebuild. spec: §4.4 (proposal), §6.1, §5.1, §6.2
// (pre-attached retry Scope), §7.3.
```

Do not write "only the §7.1 atomic creation unit re-claims a fresh pod". `Launch` is also reached through `Bind` on the resume-rebuild path, where §7.3 re-claims.

**(c) `writeSetupCommandError` doc comment** (`start.go`). Replace

```go
// as Unknown, and Internal/Aborted/ResourceExhausted) is a transient
// setup-window transport failure that §6.2 recovers on a fresh pod, so it
// keeps the retryable 503 fallback + Retry-After. The same
```

with

```go
// as Unknown, and Internal/Aborted/ResourceExhausted) is a transient
// setup-window transport failure, so it keeps the retryable 503 fallback +
// Retry-After for a client retry. Under the §6.2 pre-attached retry Scope,
// only the POST /v1/sessions/start atomic unit also retries it internally on
// a fresh pod (unbuilt: BUILD-GAPS F-6.2.27). The same
```

and replace the tag line `// §6.2 (transient setup failure retried on a fresh pod).` at the end of the same comment with `// §6.2 (pre-attached retry Scope).`

**(d) Tags on the `/resume` helpers and tests.** §7.3 governs `/resume` recovery and is already cited at each site, so the §6.2 clause is removed:

| Site | Replace | With |
|:--|:--|:--|
| `start.go`, `holdOrFailOnResumeError` doc comment | `// spec: §7.3, §6.2 (transient` / `// setup failure retried on a fresh pod). F-7.3.23.` | `// spec: §7.3. F-7.3.23.` |
| `start.go`, `isTransientPodClaimError` doc comment | `// §7.3 (awaiting_client_action holding state for a retryable resume failure),` / `// §6.2 (transient setup failure retried on a fresh pod).` | `// §7.3 (awaiting_client_action holding state for a retryable resume failure).` |
| `podclaimerror_internal_test.go`, the annotation above `TestIsTransientPodClaimErrorSetupCommand_spec_7_3` | `// failure), §6.2 (transient setup failure retried on a fresh pod) — the` | `// failure) — the` |
| `resume_setup_demotion_internal_test.go`, the annotation above `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` | `// failure), §15.1 (SETUP_COMMAND_FAILED), §6.2 (transient setup failure` / `// retried on a fresh pod), §7.2.` | `// failure), §15.1 (SETUP_COMMAND_FAILED), §7.2.` |

The `/` in a cell separates two consecutive comment lines.

**(e) `TestWritePodClaimErrorSetupCommandFailed_spec_7_3` annotation** (`podclaimerror_internal_test.go`). `writePodClaimError` serves every route, so the tag becomes route-neutral. Replace `// §6.2 (transient setup failure retried on a fresh pod) — a deterministic` with `// §6.2 (pre-attached retry Scope) — a deterministic`, and replace the closing line `// retryable 503 fallback + Retry-After so §6.2 recovers it on a fresh pod.` with `// retryable 503 fallback + Retry-After for a client retry.`

## Staged schema, chart, and migration changes

None.

## Staged docs changes

### RECORDS-1 · BUILD-GAPS.md and TEST-GAPS.md

**(a) New BUILD-GAPS finding.** Insert the entry below in `BUILD-GAPS.md` after the `F-6.2.26` entry and before the `---` rule that precedes the §6.2 `### Cross-cutting summary`. The finding stays OPEN when 0085 lands.

```markdown
### - [ ] F-6.2.27 — The §6.2 pre-attached retry is unbuilt on the `POST /v1/sessions/start` atomic creation unit [Low] — OPEN

- **Spec:** §6.2 Pod State Machine, **Pre-attached failure retry policy:** **Scope:** as amended by proposal 0085. Within the §7.1 atomic creation unit of `POST /v1/sessions/start`, a pre-attached failure in the claim, prepare/setup, or launch steps is retried up to 2 times on a fresh pod with 500ms and 1s backoff before the row is persisted.
- **Impl:** `mintClaimStartPersist` (`pkg/gateway/sessionserver/start.go`) runs `claimAtCreate` and `startOnPod` once and rolls back on the first error through `rollbackClaim` (when `createClaimNeedsRollback` holds) or `rollbackBinding`. `runWithQueue` re-enters acquisition only on the pool-exhaustion sentinels, only on a `queue` pool, and holds no pod between attempts. `POST /v1/sessions` is not part of this gap. Its only pre-persist work is the step-3 pre-check and the step-4 claim, and §7.1 and §7.2 require a claim failure there to surface immediately as a retryable 503 `SESSION_CREATION_FAILED`, or to wait in the §4.6.1 queue on a `queue` pool.
- **Gap:** Build the loop in mintClaimStartPersist around claimAtCreate and startOnPod, ahead of the coordination-lease acquire and `store.Create`. Each failed attempt releases its pod and any credential lease it minted before the next attempt claims. A failure the policy's **Non-retryable failures:** bullet names, and the deterministic `FailedPrecondition` setup exit that surfaces as `SETUP_COMMAND_FAILED`, returns on the first attempt. Pool exhaustion at the claim keeps its §7.1 and §4.6.1 handling and is not retried by this loop. Exhaustion returns the last attempt's envelope, and success keeps the existing `201` response. A slot failure on a concurrent-workspace pool follows the §5.2 **Slot retry policy** and is outside this finding. Tier 1 covers the attempt count, the backoff, and the non-retryable exit; TEST-GAPS T-6.2.10 covers the integration test.
```

**(b) Retarget TEST-GAPS T-6.2.10.** Keep the entry's heading line. Replace its five bullets with:

```markdown
- **Spec/Doc:** §6.2 Pod State Machine, **Pre-attached failure retry policy:** ("Max retries: 2 (3 total attempts including the original)", "Backoff: Exponential — 500ms, 1s between retries"). As amended by proposal 0085, the **Scope:** bullet confines the retry to requests that have persisted no session row, and a request against a persisted row, `POST /v1/sessions/{id}/finalize` included, makes one attempt. Proposal 0007 (eager claim at create) added a pool-exhaustion-at-create path on `POST /v1/sessions` that fails fast with `SESSION_CREATION_FAILED`; that claim exhaustion is outside the retry and stays covered by the fail-fast tests below.
- **Existing tests:** `tests/tier5_e2e_kind/eager_claim_e2e_test.go::TestEagerClaimPoolExhaustionAtCreate` asserts `503 SESSION_CREATION_FAILED` when `POST /v1/sessions` hits an exhausted pool, and `tests/tier7a_load_local/scenarios/create_pool_exhaustion` load-tests the same fail-fast ordering under concurrency. No test injects a transient failure into an attempt of `POST /v1/sessions/start` and asserts the invisible retry-and-recover loop.
- **Gap:** The invisible-retry half is untested: fail the first one or two claim, setup, or launch attempts of `POST /v1/sessions/start`, then assert `201` with the session `running`, no client-visible `resume_pending`, and no session row or SandboxClaim left by the failed attempts.
- **Dependencies:** Blocked on BUILD-GAPS F-6.2.27, which builds the loop. Also requires a fault-injecting adapter or warm pool that fails the first N attempts, plus the `--agent-namespace`-wired gateway. A pre-attached fault-injection stub does not exist; building it is part of this finding.
- **Suggested test:** Add `tests/tier4_integration/pre_attached_retry_test.go` that injects setup failures into the first attempts of `POST /v1/sessions/start` and asserts the eventual `running` with no client-visible `resume_pending`, using `tests/testinfra/sessiondriver` and a fault-injection stub.
```

## Testing

### TEST-1 · a failed finalize claims no replacement pod

Target: `pkg/gateway/sessionserver/start_pod_test.go`, `runFinalizeMaterializationFailureCase` and `TestFinalizeMaterializationFailureIsRetryableSessionCreationFailed_spec_6_2`. Tier 2 (the fixture is envtest-backed). It pins the persisted-row clause of the SPEC-1 **Scope:** text at finalize.

1. Add a helper type in the same file. `sync/atomic` is already imported.

   ```go
   // claimCreateCounter wraps the client the binder claims through and counts
   // every SandboxClaim Create attempt, including one that fails with
   // AlreadyExists, so a test can tell a single claim from a re-claim whose
   // outcome leaves no trace in the cluster.
   type claimCreateCounter struct {
   	client.Client
   	creates atomic.Int64
   }

   func (c *claimCreateCounter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
   	if _, ok := obj.(*lennyv1.SandboxClaim); ok {
   		c.creates.Add(1)
   	}
   	return c.Client.Create(ctx, obj, opts...)
   }
   ```

2. In `runFinalizeMaterializationFailureCase`, build `counter := &claimCreateCounter{Client: cluster}` and pass `counter` to `podBindBinder`. Keep every read through `cluster`.
3. Do not seed a second sandbox, and keep the hard-coded `claim-sbx-1`.
4. After `/create`, assert `counter.creates.Load() == 1`. After the failed `/finalize`, keep the existing assertions and add `counter.creates.Load() == 1`, with a failure message stating that `/finalize` claimed a replacement pod.
5. Append `§6.2 (Pod State Machine, Pre-attached failure retry policy Scope: a request against a persisted row claims no replacement pod)` to the test's `// spec:` annotation. Append to its `// diagnosis:` comment: "A SandboxClaim create count other than one after the failed finalize means /finalize claimed a replacement pod, which the §6.2 Scope bullet forbids for a request against a persisted row."

The count after `/finalize` is the discriminating assertion. The existing "claim-sbx-1 is gone" assertion also passes on a regression that re-claims, fails again, and reclaims. The counter increments before it delegates, so a re-claim attempt is counted even when `Create` returns AlreadyExists; `sbx-1` stays `idle` in envtest because no WarmPoolController runs there, so a regressing re-claim targets it again.

Run tier 0, then tier 2 on `pkg/gateway/sessionserver`, and reap envtest orphans with `pkill -f kubebuilder-envtest` afterwards.

CODE-1 changes comments only and needs no new test. Tier 0 and tier 1 on `pkg/gateway/sessionserver` and `pkg/gateway/podlifecycle/podsession` confirm it compiles and the existing tests hold.

## Edge cases and accepted failure modes

- **A terminal writer ends the session during the single finalize attempt.** Aborting the in-flight attempt stays out of scope. 0082's guarded exit writes govern the finalize response.

## Files touched on application (non-spec)

- `pkg/gateway/podlifecycle/podsession/binder.go` (CODE-1)
- `pkg/gateway/sessionserver/start.go` (CODE-1)
- `pkg/gateway/sessionserver/podclaimerror_internal_test.go` (CODE-1)
- `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go` (CODE-1)
- `pkg/gateway/sessionserver/start_pod_test.go` (TEST-1)
- `BUILD-GAPS.md` (RECORDS-1)
- `TEST-GAPS.md` (RECORDS-1)
