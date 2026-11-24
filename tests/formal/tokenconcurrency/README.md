# Token concurrency model

`TokenConcurrency.tla` is a TLA+ model of the token rules of the Token Service under concurrent requests. Change 0169 (`changes/archive/2026-11-23-0169-token-concurrency-model/`) builds it, and its `change.md` states the properties in plain words and records the results.

The model is a design check. No tier of `lenny-test` runs it, and no Go test reads it (change 0169, decision 6). The change that edits a rule the model describes reruns it. `tests/formal/tokens/` holds the Alloy model of change 0165, in which every operation is one atomic step; this model checks the orderings in which the statements of several requests interleave.

## What the model covers

The model describes each request as a PlusCal process. One label is one atomic step: a statement that reads or writes without locking, a lock step, or a commit. A reviewer checks the atomicity of the model by reading the labels beside the statements of the Go code. Postgres, the deadlock detector, and the event bus are TLA+ operators and processes in the same module (decision 3).

The constant `Variant` selects the rule set:

| Variant | Rules |
| :-- | :-- |
| `Main` | The code on `main` after change 0167 and step 0165.S2. |
| `TF` | The locking design of change 0168 (its decisions 1, 2, 5, and 6 and its sections TF-1 to TF-5), as the specification lane of 0168.S1 states it on branch `change/0168.S1-token-fixes`. |
| `I13Lock` | `TF` with the per-tenant audit advisory lock taken by the cascade and by `RevokeBySubject` before the statement that reads their snapshot, as TOKX-I13 suggests (decision 7). |

### Processes

| Process | Labels | Code (`Main`) | Change under `TF` |
| :-- | :-- | :-- | :-- |
| Exchange: a self-rotation, a narrowing, an exchange by another caller, or a child mint | `XBegin` to `XCommit` | `RecordWithAudit`, `RecordWithRotationAudit`, `lockAndRecheck`, `recheckSources`, and `revokePrevious` in `pkg/storage/issuedtokenstore`; `exchangeSources` and `derivedLineage` in `pkg/tokenservice/tokenservice.go` | `XUser` and `XUserLock` read the user rows of the subject, the actor, and the caller under a share lock after step (1a) (TF-4). |
| Playground mint | `XBegin` to `XCommit` | `RecordWithSources`, called by `recordBearer` in `pkg/gateway/playground/token.go`, with the subject as the only source | None. TF-4 applies to `/v1/oauth/token`, and the playground mint is outside it. |
| Recursive-revocation request, impersonation step | `MSub` to `MImpCommit` (`Main`), `TOuter` to `TOuterCommit` (`TF`) | `revokeImpersonationsFrom`: `LineageSubtree` (the caller's row `FOR SHARE`, then the subtree read), then `RevokeImpersonationsByImpersonator` under `__all__` | One outer transaction in the request's tenant: the caller's row and user row under share locks, the subtree, the start records, and the update of the request's own tenant's impersonation rows; one nested transaction per other target tenant (TF-2, TF-5). |
| Recursive-revocation request, cascade | `Casc` to `CascCommit` | `RevokeLineage`: `checkCallerLive`, `ensureRoot`, and `cascadeFromRoot`, retried up to `cascadeDeadlockAttempts` (3) times on `40P01` | `CascCaller` accepts a caller in the step revoked set (TF-1); `CascUser` reads the caller's user row (TF-4); `CascAdv` takes the advisory lock under `I13Lock`. |
| Impersonation start | `IChk` to `IOuterCommit` | `checkCaller` in `pkg/credential/impersonation/service.go` (an unlocked read), then `RecordImpersonation`: the administrator's user row `FOR SHARE`, and a nested transaction in the target tenant that reads the target's user row `FOR SHARE` and inserts the bearer's row | The outer transaction also writes the start record before the nested one opens (TF-5). |
| Impersonation end | `EBegin` to `ECommit` | `EndImpersonation` | None. |
| Full revoke | `FLevel` to `FActCommit` | The invalidation level's `UPDATE users`, committed first; then step 5, `RevokeBySubject` and `RevokeImpersonationsByActor` (`userTokenRevoker` in `cmd/lenny-gateway/user_revocation.go`) | The walk also starts from the child rows whose `act_sub` is the user (TF-3); `FSubAdv` takes the advisory lock under `I13Lock`. |
| Deadlock detector and failures | `Det` | Postgres | None. |
| Event bus and replicas | `Bus` | `revocationPublisher` in `cmd/lenny-token-service/server.go`, the `Cache` of `pkg/credential/revocation`, and the 30-second rehydration in `cmd/lenny-gateway/billing_security_workers.go` | None. |

A request starts only when its client holds every token it presents: a token of the initial state, or a token an earlier request in the behavior was issued. A request whose tokens span tenants is refused, as tenant-scope enforcement refuses it, and the model does not check the role a revocation request needs, because each scenario presents only callers that the authorization accepts.

### Postgres

- **Statements.** A statement that reads without locking is one step and sees the committed rows and its own transaction's writes. An `UPDATE` takes its snapshot in one step and then handles its rows one per step: it takes the row's update lock, waiting while another transaction holds a conflicting lock, then re-reads the row's latest committed version and skips it when it no longer matches. It never reaches a row inserted after its snapshot. The rows are handled in any order. This is `READ COMMITTED`.
- **Locks.** Share locks (`FOR SHARE`) are compatible with each other; an update lock conflicts with both modes. A transaction's own locks never block it, so a transaction that holds a share lock on a row takes the update lock when no other transaction holds one. The advisory lock of §11.7 item 3 is an update lock on the tenant's name. Locks are released at commit or abort.
- **Commit and abort.** A commit makes the transaction's writes visible at once and publishes one event per revoked `jti` and replica. Once per behavior (`MaxAborts`), the detector process either fails the transaction a request is running, which stands for a failed statement, including a failed `COMMIT`, or aborts every open transaction, which stands for a primary failover. The request sees the failure at its next step and rolls back what it still holds open. The loss of an idle outer transaction's connection while its nested transaction runs is left out.
- **Deadlock detector.** When the lock waits form a cycle, the detector aborts one transaction of the cycle with `40P01`. The wait of an outer transaction for its nested transaction is an application wait, which the detector does not see.
- **Tenant contexts.** The model records the context of each transaction, and `StepWithoutAll` reads it. Each statement names its tenant in its condition, as the queries do, so row-level security changes no result.

### Abstraction choices

- **Tokens.** A token is its `issued_tokens` row: tenant, `sub`, `parent_jti`, `act_sub`, the impersonator link, and its revocation. Every token has a row. Scopes, audiences, claims, and expiry are the Alloy model's. The orderings of TOKX-I19, which needs an `actor_token` without a row, are outside this model.
- **Time.** The model has no clock. Expiry, the trailing hour of the rehydration read, the propagation latency target, the 5-second `statement_timeout` under which `auditstore.AcquireLockInTx` takes the advisory lock (`audit.lock.acquireTimeoutMs`), and timeouts are left out, so a wait that only a timeout ends is reported as a wait that does not end.
- **Audit rows other than `token.exchanged`.** The rejection audit row, `token.revoked`, `admin.impersonation_started`, and `cross_tenant_read` are written outside the transactions the model describes, and are left out.
- **Revocation cache.** A replica's cache holds `jti` values. A rehydration is a read of every committed revoked row, then a swap that keeps the events delivered during the read, as `Cache.Rehydrate` does. It runs whenever the cache lacks a committed revocation, which stands for the 30-second ticker. A restart empties the cache, and the replica validates nothing until its first rehydration.

## Differences between the code and the text

- **G1. No fallback read on validation.** §13.3 **Authoritative durability for revocation.** says that correctness survives a lost event "because every validation can fall back to `issued_tokens.revoked_at`", and **Token rotation and revocation — no grace period.** says a replica reads `revoked_at` from Postgres on a cache miss for a recently issued token. The gateway's auth middleware (`pkg/auth/authmw/auth.go`) reads only the in-memory cache and its staleness gate, which refuses every validation once the last successful rehydration is older than 90 seconds (`defaultMaxStaleness`), and makes no read of `issued_tokens`. A replica therefore accepts a revoked token until the event reaches it or its next rehydration, at most 30 seconds later while Postgres is reachable. The model describes the code.
- **G2. No timeout on a row-lock wait.** The default `connectQuery` (`charts/lenny/values.yaml`) sets no `statement_timeout` or `lock_timeout`, and the Token Service's HTTP server sets no handler timeout (`cmd/lenny-token-service/surfaces.go`), so a row-lock wait that the deadlock detector does not break lasts until the request's context is cancelled. `auditstore.AcquireLockInTx` bounds only the advisory-lock acquisition.
- **G3. The rule 7 window and the unlocked caller check.** Rule 7 of **Platform-admin impersonation.** says that "an impersonation started from a token of the subtree after the impersonation step and before the cascade commits" escapes the request. `checkCaller` reads the caller's bearer without a lock, so an impersonation whose caller check runs before the cascade commits and whose record commits after it is also linked to a revoked bearer and stays live. The model reads "started" as the caller check, which places that ordering in the window.

## Configurations

Each configuration races the operations of one ordering of the Scope of change 0169. `free` lets each process choose any operation.

| Configuration | Processes | Ordering |
| :-- | :-- | :-- |
| `cascade_rotation.cfg` | 2 | The worked example of change 0169: alice revokes L with C while bob rotates L. |
| `cascade_issuance.cfg` | 3 | A cascade on L while bob rotates L, and a third request narrows L, mints a playground bearer from L, or mints a child with the rotated token as `actor_token`. |
| `rotation.cfg` | 3 | Two rotations of L and a child mint with L as `actor_token`, with one abort. |
| `cache.cfg` | 2 | A cascade and a rotation, with two replicas, one lost event, and one restart. |
| `mutual.cfg` | 3 | Two requests that revoke each other's caller token, and a self-revocation or a rotation of one caller. |
| `impersonation.cfg` | 3 | alice revokes her bearer A with D while impersonations start from A in globex and in acme, or A rotates. |
| `fullrevoke.cfg` | 3 | A full revoke of alice while bob narrows K, a child of alice's A, or mints a child with A as `actor_token`, and alice rotates A, mints a playground bearer from A, or starts an impersonation from A. |
| `nested_step.cfg` | 2 | Requests in acme and globex whose callers are impersonation bearers started from tokens of each other's subtrees. |
| `nested_start.cfg` | 3 | Impersonations from acme into globex and from globex into acme, and a full revoke of a user they read. |
| `f1_i20.cfg` | 2 | A request whose caller I is an impersonation bearer that the lineage of its root B started, and a revocation of I. |
| `free.cfg` | 2 | Each process chooses any operation over three initial tokens and the two the processes mint. |

The bounds are those of decision 5: 2 tenants, 2 users per tenant, at most 3 request processes, at most 6 tokens, 1 abort per behavior, and 3 cascade attempts. `nested_step` and `f1_i20` let a client retry a request once after a `500`. `cache` has 2 replicas.

## Running it

The runner needs Docker and the `tla2tools.jar` of TLC `v1.7.4` (decision 4), from `https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar`. It checks the jar's SHA-256 against `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88` and stops when it differs, checks that the committed PlusCal translation is current, and runs TLC in an `eclipse-temurin:21-jdk` container with no network, the model directory and the jar mounted read-only.

```sh
tests/formal/tokenconcurrency/run.sh <path-to-tla2tools.jar> <output-dir> <configuration> <variant> [property ...]
```

With no property named, TLC checks every invariant and property the configuration lists; with properties named, it checks only those. TLC stops at the first violation, so a run that finds one is repeated without the violated property. `TLC_WORKERS` sets the worker count (default 2), `TLC_NODEADLOCK=1` turns off TLC's deadlock check, and `TLC_DETACH=1` starts the container detached. The output directory receives the effective configuration, TLC's log, and a receipt with the TLC release, the jar's SHA-256, and the checks.

After an edit to the PlusCal algorithm, run the translator on the module and commit the result:

```sh
java -cp tla2tools.jar pcal.trans -nocfg TokenConcurrency.tla
```

## Properties

Each property of change 0169 ("Properties" in its `change.md`) is one or more invariants or temporal formulas of the module. TLC also checks that no reachable state is a deadlock, a state in which a request has not finished and no process can take a step. That check and `RequestsTerminate` together state the termination of C5 and C8. `C10` applies to `TF` only, and decision 7 names the properties checked under `I13Lock`: C1, C5, C7, and C8.

| Property | Invariant or formula | What it states |
| :-- | :-- | :-- |
| C1 | `CascadeComplete` | Once a cascade commits, every committed row of its root's subtree is revoked, then and in every later state. |
| C2 | `NoStaleSource`, `RotateOnce` | No mint commits from a source its step (1a) read saw revoked; a token is the replaced token of at most one committed rotation. Both hold by the construction of step (1a) under the advisory lock. |
| C3 | `IssuedOnlyAfterCommit`, `RowIffAudit`, `AdvisoryLockExclusive` | A token reaches a client only after its row commits, an exchange's row and its `token.exchanged` row commit together or not at all, and one transaction at a time holds a tenant's advisory lock. |
| C4 | `CacheOnlyCommitted`, `AcceptRevokedOnlyInFlight`, `RevocationReachesEveryReplica` | A cache holds only committed revocations; a running replica lacks a committed revocation only while its event is in flight or lost and no rehydration has read it; every committed revocation reaches every replica's cache, under weak fairness. |
| C5 | `AcceptedCallerLive`, `MutualRevokeOneWins`, `RequestsTerminate` | When a cascade commits, its caller is live or was revoked by the same request; two requests do not each revoke the other's caller; every request finishes. |
| C6 | `ImpersonationEndedOrInWindow` | When a cascade commits, every live impersonation row linked to a token of its subtree is in the accepted window of rule 7: its caller check ran before the cascade committed, and its row (`Main`) or its start record (`TF`) committed after the step's read. `ImpersonationEndedOrInWindowExceptFailedStart` is the same property without a row whose impersonation start ended in an error after its nested transaction committed. |
| C7 | `FullRevokeComplete` | Once step 5 of a full revoke of a user commits, no live row of the user's tenant carries the user's `sub` or `act_sub`, none descends from a row that carries the user's `sub`, and no live impersonation row names the user as administrator. |
| C8 | `NoUndetectedWait`, `NoSelfWait`, `RequestsTerminate` | The wait-for graph holds no cycle through an outer transaction's wait for its nested one, and no transaction waits for a lock its own outer or nested transaction holds. |
| C9 | `CallerAcceptedOnlyFromOwnStep`, `NoSessionEndedByRevokedCaller`, `F1RootRevoked`, `RetryCompletesStep` | A revoked caller is accepted only when the same request revoked it; no transaction of a step that ends a session commits while another request's revocation of the caller is committed; a request whose own step revokes its caller revokes its root; a failed step leaves no revocation by the request in its tenant. |
| C10 | `StepWithoutAll`, `NestedWritesOnlyNamedRows` | No transaction of a recursive-revocation request runs under `__all__`, and a nested transaction writes only impersonation rows linked to the request's tenant and subtree. |

## Results

Change 0169 ran every configuration on 2026-11-23 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run, and the bounds above. Every run finished; the largest explored 473,163 distinct states in about two minutes. `change.md` of change 0169, "Model results", records each configuration's run and the open decision each counterexample raised.

| Property | `Main` | `TF` | `I13Lock` |
| :-- | :-- | :-- | :-- |
| C1 | Violated: TOKX-I13 | Violated: TOKX-I13 | Holds |
| C2 | Holds | Holds | Not checked |
| C3 | Holds | Holds | Not checked |
| C4 | Holds | Holds | Not checked |
| C5 | Violated: `MutualRevokeOneWins`, TOKX-I20 | Violated: `RequestsTerminate`, the wait of C8 | Violated: `RequestsTerminate`, the wait of C8 |
| C6 | Holds | Violated: a bearer row without a start record | Not checked |
| C7 | Violated: TOKX-I13 in `RevokeBySubject` | Violated: TOKX-I13 in `RevokeBySubject` | Holds |
| C8 | Holds | Violated: an undetected wait in the impersonation step | Violated: the same wait |
| C9 | Violated: TOKX-I20 and TOKX-I22 | Holds | Not checked |
| C10 | Not checked | Holds | Not checked |

`Main` reproduces the three ordering defects the reviews found by reading the code: TOKX-I13 in `cascade_rotation`, the worked example of change 0169, TOKX-I22 (F1) as `F1RootRevoked` in `f1_i20`, and TOKX-I20 as `NoSessionEndedByRevokedCaller` in `f1_i20` and `nested_step`.
