# Spec changes: The §6.2 pre-attached retry loop has no stop condition for a terminal session

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The §6.2 **Pre-attached failure retry policy:** re-claims a fresh pod and replays the setup sequence only while the client request has persisted no session row. The **Scope:** bullet of that policy is the single normative home of this rule (SPEC-1). The rule has two parts:

1. **Pre-persist requests retry.** The §7.1 creation of `POST /v1/sessions` and `POST /v1/sessions/start`, including the MCP and adapter paths that run the same session-creation service, persists the row only after its work succeeds. The policy's retry budget, backoff, and exhaustion outcome apply there unchanged.
2. **Persisted-row requests make one attempt.** A request that runs pre-attached work against a session row persisted before that work starts takes no retry under this policy. The gateway makes one attempt, claims no replacement pod, and returns that attempt's failure as the exhaustion outcome. The Scope text names `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/{id}/start`, `POST /v1/sessions/{id}/resume`, and delegated-child materialization.

Because no retry runs while a terminal writer can reach the row, the policy needs no stop condition for a session that terminate, DELETE, or a pre-running watchdog ends mid-setup.

The phrase "under this policy" limits the rule to §6.2. The §5.2 **Slot retry policy** keeps its own scope and is not edited. §7.1 **Atomicity of session creation (steps 2–8).** and §7.2 **Pre-attached vs. post-attached failure visibility.** already defer to the §6.2 policy, so they are not edited either.

Three other sites state a fresh-pod recovery or a replacement claim on their own authority. SPEC-2 and SPEC-3 delete those clauses rather than restating the Scope rule, so each site defers to the policy that governs the request:

- The §15.1 `SETUP_COMMAND_FAILED` row keeps its fallback-code statement. Each fallback code's own row already states the client retry (SPEC-2).
- The §4.7 `ConfigureWorkspace` row and §29.2 step 23 keep "the pod transitions to `failed`". Inside the §7.1 creation unit the §6.2 policy governs the session-level recovery. At `POST /v1/sessions/{id}/start` the §7.2 `starting` edges and §7.3 govern it (SPEC-3).

## Edge cases and accepted failure modes

- **A terminal writer ends a persisted-row session while its single attempt runs.** Rule 2 of the design gives the request no second attempt, so no replacement pod is claimed for the ended session. The response of an overtaken finalize call is the §15.1 finalize row's statement (0082 SPEC-1). Aborting the in-flight attempt is out of scope.
- **A runtime-launch failure at `POST /v1/sessions/{id}/start` after the session entered `starting`.** The §7.2 `starting → resume_pending` and `starting → failed` edges and §7.3 govern it, as the §6.2 **Client visibility:** "sole exception" sentence states. The SPEC-1 Scope text governs only the §6.2 policy and does not override those edges.
- **A slot failure on a concurrent-workspace pool, on any route.** The §5.2 **Slot retry policy** governs it.
- **`POST /v1/sessions/{id}/resume`.** §7.3 governs recovery, through the `RESUME_FAILED` client retry and the §6.2 `resuming` failure transitions.
- **`POST /v1/sessions` claim failure.** §7.1 **Atomicity of session creation (steps 2–8).** requires a pool-exhaustion or claim failure at step 4 to roll back and return the retryable `503 SESSION_CREATION_FAILED`. SPEC-1 does not change that requirement.

## Staged edits

### SPEC-1 · spec/06_warm-pod-model.md § 6.2 Pod State Machine

SPEC-1 makes two edits to the paragraph beginning **Pre-attached failure retry policy:**.

**Edit (a), lead sentence.** Replace the paragraph's first sentence

```markdown
Failures in any state before `attached` trigger automatic retry by the gateway.
```

with

```markdown
Failures in any state before `attached` trigger automatic retry by the gateway within the scope that the **Scope:** bullet below states.
```

Leave the rest of the lead paragraph, including "The pod is marked `failed` and released back to the pool (or terminated if unhealthy).", unchanged.

**Edit (b), Scope bullet.** Anchor: the **Scope:** bullet of the **Pre-attached failure retry policy:** paragraph, which currently reads "- **Scope:** Retries apply per client request, not per pod. Each retry claims a fresh pod." Append the text below to the end of that bullet, on the same line, separated by one space.

```markdown
The retry applies only while the request has persisted no session row: the [§7.1](07_session-lifecycle.md#71-normal-flow) creation of `POST /v1/sessions` and `POST /v1/sessions/start`, including the MCP and adapter paths that run the same session-creation service ([§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract)), which persists the row only after the claim, prepare, and launch succeed. A request that runs pre-attached work against a session row persisted before that work starts takes no retry under this policy. Such requests include `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/{id}/start`, `POST /v1/sessions/{id}/resume` (whose recovery [§7.3](07_session-lifecycle.md#73-retry-and-resume) governs), and delegated-child materialization ([§8.2](08_recursive-delegation.md#82-delegation-mechanism)). The gateway makes one attempt, claims no replacement pod, and returns that attempt's failure as the exhaustion outcome.
```

Keep the phrase "under this policy". Leave the **Max retries:**, **Backoff:**, **Non-retryable failures:**, **Exhaustion:**, and **Client visibility:** bullets unchanged. Before landing, confirm that the `#1521-restmcp-consistency-contract`, `#71-normal-flow`, `#73-retry-and-resume`, and `#82-delegation-mechanism` anchors resolve.

### SPEC-2 · spec/15_external-api-surface.md § 15.1 REST API

Anchor: the error-code table row `SETUP_COMMAND_FAILED`, Description cell. Replace

```markdown
it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback and is recovered with a fresh pod per [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).
```

with

```markdown
it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback.
```

Leave the rest of the row unchanged. The row stays one physical line. Do not name any endpoint where the gateway retries internally.

### SPEC-3 · spec/04_system-components.md § 4.7 Runtime Adapter and spec/29_communication-scenarios.md § 29.2 Session start

SPEC-3 makes one edit in each file.

**Edit (a), spec/04_system-components.md § 4.7 Runtime Adapter.** Anchor: the RPC table row `ConfigureWorkspace`. Replace

```markdown
If `DemoteSDK` also fails, the pod transitions to `failed` and a replacement is claimed.
```

with

```markdown
If `DemoteSDK` also fails, the pod transitions to `failed`.
```

The row stays one physical line.

**Edit (b), spec/29_communication-scenarios.md § 29.2 Session start.** Anchor: step 23. Replace

```markdown
    materialization, and when `DemoteSDK` also fails the pod transitions to `failed` and a replacement is
    claimed ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)). Steps 24 through 29 are the pod-warm startup
```

with

```markdown
    materialization, and when `DemoteSDK` also fails the pod transitions to `failed`
    ([§7.1](07_session-lifecycle.md#71-normal-flow),
    [§4.7](04_system-components.md#47-runtime-adapter)). Steps 24 through 29 are the pod-warm startup
```

After both SPEC-3 edits land, `grep -n "a replacement is claimed" spec/` returns only the §6.2 sentence describing the `resume_pending` crash re-dispatch. After every SPEC step, run the citation resolver and the naming lint (`scripts/specshift/name`) over the edited files.

## Spec files touched

- `spec/06_warm-pod-model.md` (SPEC-1)
- `spec/15_external-api-surface.md` (SPEC-2)
- `spec/04_system-components.md` (SPEC-3)
- `spec/29_communication-scenarios.md` (SPEC-3)
