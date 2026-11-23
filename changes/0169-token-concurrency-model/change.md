# A concurrency model of the token rules

Status: `In progress`, revision 2, 2026-11-23.

## Problem

Change 0165 (a formal model of the token rules, [`changes/0165-token-rules-model/`](../0165-token-rules-model/change.md)) checks the token rules of §13.3 (Credential Flow) with an Alloy model in which every operation is one atomic step, and with generated property tests (step 0165.S2, `tests/component/tokenproperties`) that run one operation at a time. The paragraph **Token rule properties.** that 0165 adds to §13.3 states the properties for "every sequence of operations that complete one at a time". Neither check covers two requests whose transactions interleave, and the Token Service runs on several gateway replicas against one shared `issued_tokens` table.

The orderings matter in practice. Each request of the Token Service runs one or more Postgres transactions of several statements, and the reviews of the token changes found ordering defects by reading the code:

- [TOKX-I13](../../issues/TOKX/TOKX-I13-derivation-committed-during-a-cascade-escapes-it.md): a rotation, a narrowing, or a child mint from a token of a revocation's subtree that commits while the cascade statement runs is outside the statement's snapshot, so the derived token stays live.
- TOKX-I20 (filed on the branch of 0165.S2): a revocation of the caller that commits between the subtree read and the impersonation step does not stop the step from ending sessions.
- TOKX-I22 (filed on the branch of 0165.S2, finding F1 of the property tests): a recursive-revocation request whose caller is an impersonation bearer that its own impersonation step revokes then refuses itself in the cascade.

Change 0168 (token rule fixes from the property tests, on branch `records/0168-token-fixes`) fixes TOKX-I20 and TOKX-I22 with a new locking design: the caller's `FOR SHARE` lock held across the impersonation step, the step's `RETURNING` set passed to the cascade, a start-record table in the administrator's tenant, and one nested transaction per target tenant without the `__all__` sentinel. A design of nested transactions and row locks across tenants is the kind of design in which an ordering defect, such as a wait that Postgres does not detect as a deadlock, is hard to see by reading.

0165 named a TLA+ model of the Token Service transactions over a shared `issued_tokens` table as the candidate for these orderings ("Limits, and alternatives rejected", **No concurrency.**), and its decision 14 held it until 0165.S2 was done. The owner decided on 2026-11-23 to build it now, as this change, and to have it check the locking design of 0168 before the code lane of 0168.S1 is written.

## Scope

The change builds a TLA+ model of the concurrent orderings of the token rules and checks the properties under "Properties" below against two rule sets: the rules on `main` after change 0167 and step 0165.S2, and the locking design of 0168 as its decisions 1, 2, 5, and 6 and its sections TF-1 to TF-5 state it. The orderings are:

- a recursive revocation that commits while an issuance from a token of its subtree runs;
- a rotation racing a child mint that presents the rotated token as `actor_token`, and two rotations of one token;
- write-before-issue across gateway replicas;
- the propagation of a revocation to the replicas' revocation caches;
- concurrent recursive-revocation requests, including requests that revoke each other's caller token;
- an impersonation start racing the revocation of the administrator's bearer;
- a full revoke of a user racing a mint;
- the nested per-tenant transactions of 0168's impersonation step, for deadlock freedom under Postgres row-lock semantics;
- the orderings of F1 (TOKX-I22) and TOKX-I20 under 0168's design.

It leaves out:

- Fixing the rules. Each counterexample is confirmed against the text and the code, written as a scenario, and reported to the owner as an open decision. The fix lands in another change, as 0165's counterexamples landed in 0164 and 0167.
- The properties that do not depend on ordering: scope narrowing, tenant preservation, identity, delegation depth, and lifetime (items 1, 2, 5, 6, 7, and 9 of **Token rule properties.**). The Alloy model and the property tests of 0165 check them.
- The bootstrap admin-credential rotation, whose session-scoped advisory lock and Kubernetes Secret patch §13.3 **Gateway-mediated bootstrap admin-credential rotation ordering.** states, the credential-lease mint of §4.9, and the routes that revoke an administrator's bearer without following the impersonator link, which 0167 decision 13 keeps.
- Time: token expiry, the trailing hour of the rehydration read, the propagation latency target, and rate limits. The model states orderings, and a timing bound is outside what it checks.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token, actor token, caller token | The `subject_token` and `actor_token` of an RFC 8693 exchange, and the `Authorization: Bearer` token that authenticates the request (§13.3 **Client authentication on `/v1/oauth/token`.**). |
| Lineage, subtree | The tree the `parent_jti` column of `issued_tokens` (§12.2) forms within a tenant. The subtree of a token is the token and its lineage descendants. |
| Mint | A request that issues a token: an exchange on `/v1/oauth/token` (a self-rotation, a narrowing, an exchange by another caller, or an `a2a_delegation` child mint), a playground mint, or a platform-admin impersonation start. |
| Issuance transaction | The transaction of **Write-before-issue ordering.** in §13.3: the per-tenant audit advisory lock, the step (1a) read of the source rows, the insert of the issued row, and the insert of its audit row. |
| Cascade | The transaction of `issuedtokenstore.Store.RevokeLineage` that revokes a root and its lineage descendants in the request's tenant. |
| Impersonation step | The part of a recursive-revocation request that revokes the impersonation rows whose impersonator link names a token of the root's subtree, before the cascade (§13.3 **Platform-admin impersonation.** rule 7). |
| Outer transaction, nested transaction | Under 0168's design (its TF-2), the impersonation step's transaction in the request's tenant, and a transaction it opens under a target tenant's context, which commits before the outer one. |
| Start record | Under 0168's design (its decision 6), a row in the administrator's own tenant naming the bearer that started an impersonation and the target tenant. |
| Step revoked set | Under 0168's design (its TF-1), the `jti` values the impersonation step revoked in the request's tenant, which the cascade accepts as a revoked caller. |
| Statement snapshot | Under Postgres's `READ COMMITTED` isolation, the set of rows a statement sees: the rows committed when the statement starts, and the transaction's own writes. |
| Lock wait, application wait | A lock wait is a transaction waiting for a row lock or an advisory lock that another transaction holds; Postgres sees it. An application wait is a transaction whose client waits for another transaction to finish, as an outer transaction waits for its nested transaction; Postgres does not see it. |
| Wait-for graph | The graph whose nodes are open transactions and whose edges are lock waits and application waits. |
| Detected deadlock, undetected wait | A cycle of the wait-for graph made only of lock waits, which Postgres's deadlock detector breaks by aborting one transaction with `40P01`; and a cycle that contains an application wait, which the detector does not see, so its transactions wait until a timeout. |
| Accepted window | An ordering whose outcome the text states and accepts, such as the impersonation started while a request runs that rule 7 says escapes the request. |
| Variant | The rule set the model checks: `Main` for the rules on `main`, `TF` for 0168's design, and `I13Lock` for the resolution TOKX-I13 suggests (decision 7). |
| Counterexample | A behavior, found by TLC within the stated bounds, that ends in a state where an invariant is false, or that violates a liveness property. |

## High-level design

The model describes each request of the Token Service and of the gateway paths that touch `issued_tokens` as a process: a sequence of Postgres statements grouped in transactions, run by a gateway replica. A small description of Postgres sits underneath: which rows each statement sees, which locks it takes and waits for, when a transaction commits or aborts, and how the deadlock detector breaks a cycle. Each replica keeps a revocation cache, fed by an event bus that can lose messages and by a rehydration read on restart.

TLC, the TLA+ model checker, explores every interleaving of the processes' statements within the stated bounds and either finds no behavior that breaks a property or returns one that does, which for an invariant is a shortest one. The properties are the items of **Token rule properties.** that depend on ordering, stated for interleaved requests, plus the deadlock freedom and termination of the request processes.

The model checks two rule sets with the same processes and properties. `Main` is the rules on `main`. `TF` replaces the recursive-revocation request, the impersonation start, the full revoke, and step (1a) with 0168's design. Running both separates a defect 0168 introduces from one it inherits. `Main` must reproduce TOKX-I13, TOKX-I20, and TOKX-I22; a model that does not find them describes the code wrongly, and is corrected before its other results are trusted.

Each counterexample is confirmed against the text and the code, rewritten as a scenario with alice, bob, and carol in the tenants acme and globex, and listed as an open decision. The model keeps the rules as written: a property that fails stays failing until the owner chooses a fix and the change that carries the fix edits the model's process with the rule.

## Requirements

None by identifier. §13.3 is content of the placeholder page TOKX and carries no requirement identifiers yet.

## Properties

Each property is a TLA+ invariant or temporal formula, named in brackets. The item it refines in §13.3 **Token rule properties.** is named where one applies. "Commits" means the transaction's `COMMIT` succeeds.

- **C1 Cascade completeness (items 3 and 8, TOKX-I13).** Once a cascade from a root commits, every committed row of the root's subtree in its tenant is revoked, then and in every later state [`CascadeComplete`]. A mint that commits after the cascade has a revoked source, so it is refused by step (1a), and a mint that committed before the cascade's statement snapshot is in the subtree the statement revokes. The property fails when a mint commits between the two.
- **C2 Fresh sources (item 8, Actor-token freshness under concurrent rotation).** No mint commits when the row of its `subject_token`, `actor_token`, or caller token was revoked by a transaction that committed before the mint's step (1a) read [`NoStaleSource`]. A token is the replaced token of at most one committed rotation [`RotateOnce`].
- **C3 Write-before-issue across replicas (Write-before-issue ordering).** A token reaches a client only after the transaction that inserts its row and its `token.exchanged` row commits, and no state holds one of the two rows without the other [`IssuedOnlyAfterCommit`, `RowIffAudit`]. The issuance transactions of one tenant hold the per-tenant audit advisory lock one at a time, on any replica [`AdvisoryLockExclusive`]. An abort at any statement, which stands for a primary failover, leaves neither row.
- **C4 Replica revocation caches (Token rotation and revocation, Authoritative durability for revocation).** A replica's cache holds only revocations that Postgres has committed [`CacheOnlyCommitted`]. A replica accepts a revoked token only while the revocation's event has not reached it, the replica has not rehydrated since the revocation committed, and the validation took no fallback read [`AcceptRevokedOnlyInFlight`]. Every committed revocation reaches every running replica's cache, under fair delivery and fair rehydration after a lost event [`RevocationReachesEveryReplica`, liveness].
- **C5 Concurrent recursive revocations (items 3 and 4, Recursive-revocation request authorization).** An accepted request's caller was live at the request's share read of the caller row, or, under `TF`, is in the request's step revoked set [`AcceptedCallerLive`]. Two requests that revoke each other's caller token do not both revoke the other's [`MutualRevokeOneWins`]. Every request ends with an acceptance, a refusal, or an error within the retry bound of `RevokeLineage` (`cascadeDeadlockAttempts`) [`RequestsTerminate`, liveness].
- **C6 Impersonation start and the administrator's bearer (items 3 and 10, Platform-admin impersonation rules 1, 4, and 7).** When a recursive-revocation request whose subtree contains the administrator's bearer commits its cascade, every impersonation row whose impersonator link names a token of the subtree is revoked, except a row whose start falls in the accepted window of rule 7 [`ImpersonationEndedOrInWindow`]. The model records which orderings fall in the window under each variant: under `Main`, an impersonation that commits after the impersonation step and before the cascade commits; under `TF`, one whose start record commits after the outer transaction reads the start records.
- **C7 Full revoke and a mint (item 10, §11.4 step 5).** Once step 5 of a full revoke of a user commits, no committed row in the tenant that is live carries the user's `sub`, names the user in `act_sub`, or is in the subtree of a row that carries the user's `sub`, then and in every later state [`FullRevokeComplete`]. Under `TF`, the model checks whether the share lock on the user row of decision 4 covers every derivation, including a mint whose sources carry another user's `sub` and descend from a token of the revoked user, which reads no user row of that user.
- **C8 Nested per-tenant transactions (0168 TF-2 and TF-5, Platform-admin impersonation rule 1 (c)).** The property covers the nested transactions of the impersonation start under both variants and those of 0168's impersonation step under `TF`. The wait-for graph never holds a cycle that contains an application wait [`NoUndetectedWait`]. An outer transaction never waits on a lock its own nested transaction holds, and a nested transaction never waits on a lock its outer transaction holds [`NoSelfWait`]. Every cycle of lock waits is broken by the deadlock detector, and the request whose transaction it aborts retries or ends with an error [`RequestsTerminate`]. One ordering the model is written to settle: two requests in acme and globex whose callers are impersonation bearers started from tokens of each other's subtrees, where each outer transaction holds the share lock on its own caller row and each nested transaction updates the other's caller row.
- **C9 F1 and TOKX-I20 (0168 TF-1 and TF-2).** The cascade accepts a revoked caller only when the caller's `jti` is in the same request's step revoked set [`CallerAcceptedOnlyFromOwnStep`]. No impersonation session is ended by a request whose caller was revoked by another transaction that committed before the request's share read of the caller row [`NoSessionEndedByRevokedCaller`]. A request whose caller is an impersonation bearer that its own step revokes revokes its root [`F1RootRevoked`]. A failed nested transaction leaves the request's tenant without a revocation by that request, and the retry ends every session the first attempt did not [`RetryCompletesStep`].
- **C10 Tenant contexts (0168 decision 5, §4.2, §12.3).** Under `TF`, no transaction of a recursive-revocation request runs under `__all__`, and a nested transaction writes only impersonation rows that a start record in the caller's tenant names [`StepWithoutAll`, `NestedWritesOnlyNamedRows`].

## Current state

The state on `main` at `b8bba2f99`, which includes 0167.S1. Step 0165.S2, on branch `change/0165.S2-token-properties`, adds **Token rule properties.** to §13.3 and the property tests, and changes no Token Service code.

- **Issuance.** `RecordWithAudit`, `RecordWithRotationAudit`, and `RecordWithSources` in `pkg/storage/issuedtokenstore/issuedtokenstore.go` each run one transaction. `lockAndRecheck` in `recheck.go` takes the per-tenant audit advisory lock (`auditstore.AcquireLockInTx`) and then reads the source rows in one plain `SELECT`, with no row lock. A rotation's `revokePrevious` updates the replaced row in the same transaction.
- **Recursive-revocation request.** `revokeImpersonationsFrom` in `pkg/tokenservice/tokenservice.go` calls `LineageSubtree` (one transaction in the request's tenant: the caller's row `FOR SHARE`, then a recursive read of the subtree) and `RevokeImpersonationsByImpersonator` (a second transaction, under `pgtenant.InAllTenants`). `RevokeLineage` then runs the cascade: the caller's row `FOR SHARE` (`checkCallerLive`), `ensureRoot`, and one recursive `UPDATE` statement, retried up to `cascadeDeadlockAttempts` (3) times on `40P01`. None of these transactions takes the advisory lock.
- **Impersonation start.** `RecordImpersonation` in `pkg/storage/issuedtokenstore/impersonation.go` runs a transaction in the caller's tenant that reads the administrator's user row `FOR SHARE`, and inside it a transaction in the target tenant that reads the target's user row `FOR SHARE` and inserts the bearer's row; the inner one commits first. Before it, `checkCaller` in `pkg/credential/impersonation/service.go` reads the caller bearer's row with no lock.
- **Full revoke.** `pkg/gateway/api/admin/users_invalidate.go` commits the invalidation level, then step 5 calls `RevokeBySubject` (one transaction, one recursive `UPDATE` statement, no lock) and, for a `platform-admin` caller, `RevokeImpersonationsByActor` under `__all__`.
- **Propagation.** The Token Service publishes each revoked `jti` on the event bus after the revoking transaction commits (`propagateRevocation`), and the gateway's revocation cache, `pkg/credential/revocation`, is rehydrated from Postgres on startup.
- **Locks on lineage inserts.** `parent_jti` carries no foreign key (`migrations/0001_initial_schema.up.sql`), so inserting a row takes no lock on its parent's row.

The text states some orderings already: the share lock and the deadlock retry in **Recursive-revocation request authorization.**, the re-read of **Actor-token freshness under concurrent rotation.**, the escape window of rule 7 of **Platform-admin impersonation.**, and the commit of the invalidation level before step 1 of §11.4.

§13.3 **Authoritative durability for revocation.** says that correctness survives a lost event "because every validation can fall back to `issued_tokens.revoked_at`", while **Token rotation and revocation — no grace period.** describes the fallback read for "a cache miss for a recently-issued token". Step 0169.S1 reads the cache code in `pkg/credential/revocation` and `pkg/auth/authmw`, models the fallback the code takes, and records the difference between the two paragraphs, and any other difference between the code and the text, as 0165 recorded its gaps G1 to G7.

## Detailed design

### CM-1 Postgres

- **Statements.** A transaction is a sequence of statements of one process. A statement that reads or locks no row is one atomic step. A statement that locks or updates rows takes its statement snapshot in a first step and then handles the rows its snapshot selects one row per step, so another transaction can commit between the snapshot and a row. On each row it takes the row's lock, waiting while another transaction holds a conflicting one, then re-reads the row's latest committed version and skips the row when it no longer matches the statement's condition. It never sees a row inserted after its snapshot. This is the behavior of Postgres's `READ COMMITTED` isolation.
- **Row locks.** Two modes: share, taken by `SELECT ... FOR SHARE`, and update, taken by `UPDATE` and `SELECT ... FOR UPDATE`. Share locks are compatible with each other, and an update lock conflicts with both modes. Postgres's `FOR NO KEY UPDATE` and `FOR KEY SHARE` modes are folded into these two, because no statement of the token code takes `FOR KEY SHARE` and no foreign key makes Postgres take it. Locks are held until the transaction ends.
- **Advisory lock.** The per-tenant audit lock of §11.7 item 3 is an exclusive lock on the tenant, taken by `pg_advisory_xact_lock` and held until the transaction ends.
- **Commit and abort.** A commit is atomic and releases every lock. An abort can happen at any statement, up to a bound, and stands for a statement failure or a primary failover; it discards the transaction's writes.
- **Deadlock detector.** When the lock waits form a cycle, an action of the model aborts one transaction of the cycle with `40P01`. Application waits are invisible to it. Postgres sets no lock timeout in the model unless S1 finds one in the connection configuration.
- **Tenant contexts.** Each transaction runs under one tenant's context or under `__all__`. A statement under a tenant's context sees only that tenant's rows, as row-level security makes it.

### CM-2 Request processes

Each request is a process on one replica, with the statements of the code listed under "Current state" for `Main`, and those of 0168's TF-1 to TF-5 for `TF`:

- **Exchange.** The issuance transaction with its sources; a rotation adds the update of the replaced row. Under `TF`, step (1a) also reads the user row of each source under a share lock.
- **Child mint.** The exchange with an `actor_token`.
- **Playground mint.** `RecordWithSources`, which records the bearer with its subject as parent.
- **Recursive-revocation request.** Under `Main`: the subtree transaction, the `__all__` transaction, and the cascade with its retries. Under `TF`: the outer transaction (the caller's row and user row under share locks, the subtree, the start records, and the update of the request's own tenant's impersonation rows), one nested transaction per other target tenant, then the cascade with the step revoked set.
- **Impersonation start.** The unlocked caller read, then the two transactions of rule 1 (c); under `TF`, the first transaction also inserts the start record before the second opens.
- **Impersonation end.** The conditional update of rule 3.
- **Full revoke.** The update of the invalidation level, then step 5; under `TF`, the walk also starts from the child rows whose `act_sub` is the user.
- **Validation.** A replica validates a token by its cache and, where the code does, by a fallback read.

Clients choose which tokens a request presents from the tokens they hold, so a token reaches another user as 0165's `leak` lets it.

### CM-3 Replicas and the event bus

Each replica holds a cache of revoked `jti` values. A committed revocation puts one event per replica on the bus. The bus delivers an event, or loses it, in any order. A replica can restart, which empties its cache and rehydrates it with one read of the committed revoked rows. Fairness conditions state that a running replica eventually receives each event that is not lost, and eventually rehydrates after a loss, as the reconnect path does; S1 confirms the reconnect path in the code.

### CM-4 Variants and configurations

One module holds both rule sets, selected by a constant, as the Alloy model selects the rotation lineage with `Config.rot`. Each configuration file fixes the variant, the bounds, the operations each process may run, and the properties TLC checks. A directed configuration names the operations that race in one ordering of "Scope", such as one cascade process and one rotation process, which keeps the state space small; a free configuration lets every process choose any operation, at a smaller bound. Decision 5 settles the bounds.

### CM-5 Running TLC

The model lives in `tests/formal/tokenconcurrency/`: the module, its configuration files, a runner `run.sh`, and a README that maps each process to the code and each property to its rule, as `tests/formal/tokens/README.md` does. The runner mirrors `tests/formal/tokens/run.sh`: it takes the path of `tla2tools.jar` and an output directory, and runs the PlusCal translator, as decision 3 chooses PlusCal, and TLC in an `eclipse-temurin:21-jdk` container with `--network none`, the model and the jar mounted read-only, and the output directory writable. Unlike 0165's runner, it compares the jar's SHA-256 with the pinned value before it runs, and stops when they differ. It records, per configuration, the TLC version, the jar's SHA-256, the bounds, the number of distinct states, the run time, and the result.

The jar is pinned by release and SHA-256 under decision 4. The candidates, checked on 2026-11-23 against `https://github.com/tlaplus/tlaplus/releases`:

| Release | Published | Marked | `tla2tools.jar` size | SHA-256 | Published checksum |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `v1.7.4` | 2024-08-05 | latest release | 2,274,532 bytes | `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88` | The release notes list SHA-1 `bee4a54f3ee3d4afc347c3240ec2d9e93b075104`, which the downloaded jar matches. The release publishes no SHA-256, so the SHA-256 is computed from the download. |
| `v1.8.0` | 2026-11-19 | pre-release | 4,510,915 bytes | `fdffef247f563ef6edbfd44958d2a82db79e7564e07cb673262d896353fa6713` | GitHub's asset digest for the jar is this SHA-256, which the downloaded jar matches. |

The download URL of each is `https://github.com/tlaplus/tlaplus/releases/download/<release>/tla2tools.jar`.

## Worked example

TOKX-I13 under `Main`, which the model must reproduce. alice in acme holds a token L and a second token C, and bob holds a copy of L. alice sends a recursive-revocation request with C as caller and L as `subject_token`, and bob at the same time rotates L on another replica.

| Step | Replica 1 (alice's revocation) | Replica 2 (bob's rotation) |
| :-- | :-- | :-- |
| 1 | The subtree transaction reads C's row under a share lock and reads the subtree {L}, then commits. | |
| 2 | The impersonation step revokes nothing and commits. | |
| 3 | The cascade reads C's row under a share lock. Its `UPDATE` statement takes its snapshot, which holds L. | |
| 4 | | The issuance transaction takes acme's advisory lock and reads L's row, which is live. |
| 5 | | The transaction inserts its audit row and L', with `parent_jti` = L, and stamps L revoked, which takes the update lock on L. |
| 6 | The `UPDATE` reaches L and waits for its lock. | |
| 7 | | The transaction commits and returns L' to bob. |
| 8 | The `UPDATE` re-reads L, finds it revoked, and skips it. L' is outside its snapshot. The cascade commits and answers `200`. | |

L' stays live after the cascade commits, so `CascadeComplete` fails. When the cascade reaches L before step 5 instead, it stamps L revoked under the update lock, the rotation's update of L waits, re-reads L after the cascade commits, and fails with `subject_token_revoked`, as `revokePrevious` refuses a replaced token that is already revoked; `tests/component/issuedtokencascade/rotation_lockfree_revoke_test.go` covers that ordering. Under `I13Lock` the cascade takes acme's advisory lock before its statement, so step 4 waits until the cascade commits, and step (1a) then refuses L.

## Limits, and alternatives rejected

- **Bounded exploration.** TLC checks every behavior within the bounds and proves nothing beyond them. A counterexample is a complete behavior and needs no larger bound to be confirmed.
- **Abstraction of Postgres.** The model describes the behavior of `READ COMMITTED`, row locks, advisory locks, and the deadlock detector that the token code relies on. It does not describe the lock manager's queue order, lock upgrades from share to update, or predicate locks, which no statement of the token code relies on. An ordering that depends on them is outside what the model shows.
- **No time.** Expiry, the propagation latency target (`TokenRevocationPropagationLag`), and timeouts are not modelled, so a wait that ends only by a timeout is reported as an undetected wait.
- **No scopes or claims.** Tokens carry their tenant, `sub`, lineage columns, impersonator link, and revocation state. The rules about scopes and claims are the Alloy model's.
- **Alternatives rejected.** Extending the Alloy model of 0165 with interleaved statements was set aside: a request is a sequence of several statements, so a race between two requests needs traces many times longer than the six operations Alloy checks at its base scope, and two of 0165's checks at eight operations did not finish. Apalache, the symbolic TLA+ model checker, needs type annotations and has weaker support for liveness, which C4 and C5 need. Tests against Postgres, such as the `load_local` tests 0168 lists, check the code on the orderings a test drives, and the model checks the design on every ordering within its bounds; the change that fixes a counterexample writes the test that replays it.

## Specification and non-specification changes

- `tests/formal/tokenconcurrency/`: the model, its configuration files, the runner `run.sh`, and a README that maps each process to its code and each property to its rule.
- `tests/README.md`: the `formal/` entry names the new model.
- No specification text, under decision 9.

## Documentation

No reader-facing behavior changes: TOKX. The model changes no behavior. The change that carries a fix for a counterexample states its documentation.

## Tests

No tier of `lenny-test` runs the model under decision 6; it is a design check run by hand with `tests/formal/tokenconcurrency/run.sh`, as 0165's model is. Each counterexample is written as a scenario that a `load_local` test can replay, and the change that fixes it writes that test.
