# A formal model of the upload-token keys and consumed state

Status: `In progress`, revision 2, 2026-11-27.

## Problem

Change [0177](../0177-shared-upload-token-keys/change.md) shares the upload-token signing keys among gateway replicas through the Postgres table `platform_signing_keys`. A replica writes the next key a publish lead before it becomes active, each replica reloads the ring on an interval, a replaced key keeps verifying for a verification time (0177 decision 7), a sweep deletes keys past that time, a replica whose last reload is older than the stale bound stops minting and keeps verifying with the ring it holds (0177 decision 4), and the first finalize sets `sessions.upload_token_consumed_at`, which consumes the token on every replica (0177 decision 3). Whether a token works depends on the order of reloads, rotations, sweeps, mints, verifications, and finalizes across replicas, and on the times between them. The build and review of 0177.S1 found one such gap by reasoning ([UPLOAD-I2](../../issues/closed/UPLOAD/UPLOAD-I2-stale-replica-mints-tokens-peers-refuse-before-expiry.md)). The owner asked on 2026-11-26 that upload tokens be verified formally before the implementation of M3 starts, "to make sure there are no gaps".

## Scope

The change builds a formal model of the upload-token key ring of each replica, the shared key table, rotation with the publish lead, the reload and its failures, the stale bound, the sweep, the minting of a token with its lifetime, its verification on any replica, and the consumed mark under concurrent finalizes. Unlike change 0175, the model includes time, as a discrete clock with the configured durations as parameters, because the properties compare the verification time with the token lifetime. It checks these properties: every token a replica mints verifies on every replica that can reload until the token expires; no token verifies after it expires or after its key's verification time; a token is consumed at most once; a replica whose last reload is older than the stale bound mints nothing; and, while Postgres is reachable, every replica has an active key to mint with. It also states what holds for a replica that cannot reload, such as whether it verifies a token signed with a key published after its last reload.

It leaves out:

- The cryptography. A signature is modelled as unforgeable, and envelope encryption under the KMS key is outside the model.
- The SQL of the store. A read or write of the key table or the consumed mark is modelled as an atomic step that may fail.

## Ordering

The change starts after step 0177.S2 lands, so the model reads the design with decisions 7, 8, and 9 of change 0177 built. The owner directed on 2026-11-26 that 0177.S2 be built first. Step 0179.S1 started on 2026-11-27, with 0177.S2 on `main`. It models the design as of decision 11 of change 0177, which steps 0177.S3 and 0177.S4 build on a branch that had not merged when the model was read; "Current state" names the commits.

## Terms

| Term | Meaning |
| :-- | :-- |
| Ring | The set of upload-token signing keys a replica holds in memory, with each key's activation instant and verification time. |
| Model | The TLA+ specification this change adds under `tests/formal/uploadtokens/` (decision 1). |
| Stale bound | The age of a replica's last successful reload beyond which it mints nothing; the overlap window for upload tokens. |
| Healthy replica | A serving replica whose last successful reload is at most one refresh interval old. |
| Counterexample | A trace of the model that breaks one of the properties. |

## High-level design

The model describes each gateway replica as a process that starts, refreshes its ring on a ticker, forces a rotation when an administrator asks, mints tokens, and serves finalize requests, over one shared key table and the session rows. A discrete clock advances one tick at a time, and the configured durations are constants counted in ticks, so the model compares the instants the code compares: a key's activation, retirement, and deletion instants, the age of a replica's last successful reload, and a token's expiry. An environment cuts a replica's link to Postgres or to the KMS and repairs it, restarts a replica, and, in one configuration, deletes key rows by hand.

TLC, the TLA+ model checker, explores every ordering of these steps within each configuration's bounds and either finds no behavior that breaks a property or returns a shortest one that does. The properties are the five of "Scope" written as invariants, with each one split where the code distinguishes cases, plus a statement of what holds for a replica that cannot reload. A property is stated over every token a holder of any key's secret can present, so the issued-token binding of 0177 decision 11 is part of what is checked.

Each counterexample is checked against §7.1 and the code. A modelling error is fixed in the model; a confirmed gap becomes an open decision of this change and an issue, and the model keeps the rule as written until the change that carries the fix edits it.

The model, its configurations, its runner, and its README live under `tests/formal/uploadtokens/`, laid out as `tests/formal/tokenconcurrency/` is, so that a later change to a rule the model describes updates the model and reruns it (decision 1).

## Requirements

None. The change adds a model and tests of the specification and changes no requirement.

## Current state

The model was read against two commits:

- `main` at `256e036b7`, with steps 0177.S1 and 0177.S2 merged: §7.1 **`uploadToken` format and security properties**; `pkg/signingkeys` (`Ring`, `Open`, `Refresh`, `rotateIfDue`, `reload`, `SigningKey`, `VerificationKeys`, `UploadTokenPolicy`, `UploadTokenVerificationTime`, `MemoryStore`) and `pkg/signingkeys/pgstore`; `pkg/uploadtoken` (`Issuer`, `Verifier`, `NewSharedKeys`); `cmd/lenny-gateway/token_signing.go` (`openUploadTokenKeyRing`, the stale bound set to the overlap window); and the session server's create, finalize, and upload paths, as the digests `digests/0177.S1.md` and `digests/0177.S2.md` of change 0177 name them.
- `change/0177.S3-S4-audit-tail-and-key-incidents` at `8785d86e9`, which builds decision 11 of change 0177 and had not merged: §7.1 as that branch edits it (the issued-token binding, the create-and-start consumed mark, a retired key never signs, a key write never moves the replaced key's instants later, and the forced rotation); `Ring.RotateNow` and the retired-key rules in `pkg/signingkeys/ring.go`; `Insert` keeping an earlier `retire_after` and `delete_after` in `pkg/signingkeys/store.go` and `pgstore.go`; the digest comparison in `verifyUploadTokenValue` (`pkg/gateway/core/session/sessionserver/upload_seam.go`); `consumeAtStart` (`create_and_start.go`); and `POST /v1/admin/upload-token-keys/rotate` (`pkg/gateway/api/admin/upload_token_keys.go`).

At those commits the defaults are a refresh interval of 30 seconds (`--signing-key-refresh-interval`), a publish lead of 2 minutes (`--signing-key-publish-lead`), an overlap window and stale bound of 5 minutes (`uploadtoken.DefaultOverlapWindow`, a constant, [UPLOAD-I3](../../issues/UPLOAD/UPLOAD-I3-upload-token-overlap-window-has-no-setting.md)), a token lifetime of 300 seconds (`maxCreatedStateTimeoutSeconds`), a rotation interval of 24 hours, and a verification time of a replaced key of the overlap window plus the longer of the overlap window and the token lifetime, 10 minutes.

## Detailed design

`tests/formal/uploadtokens/README.md` maps each action of the model to the code and states each property in plain words. This section states the choices behind them.

### UT-1 Time and durations

The clock is discrete, and the configurations use a tick of 30 seconds, so every default is a whole number of ticks: refresh 1, publish lead 4, overlap window 10, token lifetime 10, and a verification time of 20. The comparisons follow the code's: a key signs from its `active_from` until its `retire_after`; it verifies while its `delete_after` is after now; a token verifies while now is at or before its expiry; and a replica signs while its last successful reload is at most the stale bound old. The rotation interval is scaled down to a few minutes so that scheduled rotations happen within the bound, and a forced rotation stands for a rotation at any instant. Clock skew between replicas and Postgres is outside the model.

### UT-2 Replicas and the key table

`Start` is `signingkeys.Open`, `RefreshRing` is `Ring.Refresh` (`rotateIfDue`, the sweep, and the reload), and `ForcedRotate` is `Ring.RotateNow`. Each runs as one atomic step against the table, because `refreshMu` serializes a replica's own refresh and forced rotation and every effect of another replica's step between their statements is also reached by ordering that step first. A replica may attempt a refresh at any instant and must attempt one at least every refresh interval, which includes the exact ticker and every slower schedule. A key write fails while the replica's link to the KMS or to Postgres is cut; a reload fails while its link to Postgres is cut, and while its link to the KMS is cut when the table holds a key the replica does not hold, because `reload` unwraps only new keys and fails as a whole on one failure. A stopped replica whose links are up starts again at the same instant.

### UT-3 Tokens and sessions

A token is its session, its key, and its expiry: the HMAC is deterministic, so two tokens with the same three values are one string with one digest. `Mint` is `IssueDetailed` and the row insert that records the token's digest and expiry; for a session created by `POST /v1/sessions/start`, the row also records the consumed mark. `Accepts` is `verifyUploadTokenValue`: the HMAC under a key the replica holds whose `delete_after` has not passed, the expiry, the row read, the digest equal to the row's, and no consumed mark. `FinStart` and `FinReady` are the finalize handler's token check with its `created` to `finalizing` compare-and-swap, and its `finalizing` to `ready` write, which sets the consumed mark. `Terminate` stands for every writer that ends a session in `created` or `finalizing`.

### UT-4 Properties

| Property | Invariants or formulas | Scope item |
| :-- | :-- | :-- |
| U1 | `HealthyReplicaVerifies`, `RecentReplicaVerifies`, `ReachableReplicaVerifies` | Every token a replica mints verifies on every replica that can reload until it expires, stated for a replica whose last successful reload is at most one refresh interval old, less than one publish lead old, and of any age when the replica can read the session row. |
| U2 | `IssuedExpiresBeforeKeyDeletion`, `NoAcceptAfterKeyDeletion` | No token verifies after it expires or after its key's verification time, for any token a holder of any key's secret can present. |
| U3 | `FinalizeOnce`, `ConsumedOnlyByFinalizeOrStart`, `ConsumedMarkKept`, `StartTokenRefused` | A token is consumed at most once. |
| U4 | `MintOnlyWithinStaleBound`, `MintNeverAfterHeldRetirement`, `StaleMintBounded` | A replica whose last reload is older than the stale bound mints nothing, and how long after a key's retirement a stale replica can still mint with it. |
| U5 | `HealthyReplicaCanMint`, `FreshReplicaCanMint` | While Postgres is reachable, every replica has an active key to mint with. |
| U6 | `StaleReplicaVerifiesHeldKeys`, and the counterexample to `ReachableReplicaVerifies` | What holds for a replica that cannot reload. |
| U7 | `RetirementOnlyEarlier`, `OneKeyPerSlot` | The key-row rules of 0177 decision 11: a write never moves a key's instants later, and one key holds a slot. |

### UT-5 Configurations

The configurations split the concerns, because one configuration with every environment event and both sessions grows past 45 million states within twelve instants. Each configuration of the keys has one session, one cut of a replica's link to Postgres or to the KMS (which may heal), and one forced rotation; `r2_restart` adds a restart; the consumed configurations add concurrent finalizes, a create-and-start session, and terminate over a short horizon; `r2_rowdelete` lets an operator delete key rows; and `r2_lead_not_longer` sets a publish lead the gateway refuses at start. The README lists the bounds of each.

### UT-6 The reduced model (step 0179.S2)

Change [0182](../0182-client-chosen-upload-secret/decisions.md) replaces the upload token with a secret the client chooses: the client sends the SHA-256 digest of the secret on the routes that open an upload phase, the session row records the digest, a marker of the upload phase, and an expiry, and the verifier reads the row, compares the digest in constant time, checks the expiry, and refuses a consumed secret. Steps 0182.S1 and 0182.S2 remove every HMAC token, the key ring, its rotation, its stale bound, and its sweep. 0182 decision 4 narrows FV-2 to what remains, and step 0179.S2 reduces the model to these properties, across replicas and concurrent requests:

- A secret is consumed at most once.
- A secret is accepted only for the session whose row records its digest, and only until the row's expiry.
- A session whose row records no upload phase accepts no secret.
- Two live sessions of one user never record one digest.

The step removes the key table, the rings, the reloads, the rotations, the sweep, the link cuts to the KMS, and properties U1, U2, U4, U5, U6, and U7 with their configurations; it keeps U3 and the concurrent finalizes, adds the row-bound acceptance, the marker, and the per-user uniqueness of the digest under concurrent creates, reruns every configuration, and records the results in "Model results" and the README. A secret is modelled as unguessable, as a signature is today. The model reads §7.1 as step 0182.S1 edits it.

## Specification and non-specification changes

None to the specification. The model and its runner are non-specification files under `tests/formal/`. A confirmed counterexample becomes an open decision of this change, and its fix lands in change 0177 or another change.

## Documentation

The `formal/` entry of `tests/README.md` names the new model, and step 0179.S2 updates its description to the reduced model.

## Tests

The model's checks are the tests. Their results are recorded in this file per configuration, as 0169 records its results.

## Model results

Step 0179.S1 ran every configuration of `tests/formal/uploadtokens/` on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run, in the `eclipse-temurin:21-jdk` container the runner starts, against the commits "Current state" names. Every run finished. TLC stops at the first violation, so each run that found one was repeated with the configuration's other checks until a run explored the whole state space.

### Results by property

| Property | Result |
| :-- | :-- |
| U1 Every token verifies on every replica that can reload | `HealthyReplicaVerifies` and `RecentReplicaVerifies` hold in every configuration whose publish lead is longer than the refresh interval and in which no key row is deleted. `ReachableReplicaVerifies` is violated in every configuration with a link cut (R1). |
| U2 No token verifies after it expires or after its key's verification time | Holds, also under row deletion and for every token a holder of any key's secret can present. |
| U3 A token is consumed at most once | Holds. |
| U4 A replica past the stale bound mints nothing | Holds, also under row deletion. A replica that has not loaded a key's retirement instant signs with the key after that instant, at most the stale bound minus the publish lead (3 minutes at the defaults), which `StaleMintBounded` states (G1). |
| U5 While Postgres is reachable, every replica has a key to mint with | Holds wherever no key row is deleted (R3). |
| U6 A replica that cannot reload | It keeps verifying every unexpired token whose key it holds (`StaleReplicaVerifiesHeldKeys` holds), and it does not verify a token signed with a key published after its last successful reload (R1). |
| U7 Key-row rules | Hold, also under row deletion. |

### Runs

The distinct states and time are those of the last run of each configuration, which explored the whole state space.

| Configuration | Bounds | Distinct states | Time | Violated |
| :-- | :-- | --: | --: | :-- |
| `r2_default` | 2 replicas, default durations, 28 instants | 4,177,847 | 1 min 47 s | `ReachableReplicaVerifies` (R1) |
| `r3_default` | 3 replicas, default durations, 14 instants | 1,051,305 | 43 s | `ReachableReplicaVerifies` (R1) |
| `r2_longtoken` | 2 replicas, token lifetime 10 min, 36 instants | 8,334,751 | 3 min 44 s | `ReachableReplicaVerifies` (R1) |
| `r2_shortlead` | 2 replicas, refresh 1 min, lead 1.5 min, overlap 2 min, lifetime 3 min, 18 instants | 5,706,754 | 2 min 35 s | `ReachableReplicaVerifies` (R1) |
| `r3_shortlead` | 3 replicas, durations of `r2_shortlead`, 16 instants | 9,514,847 | 7 min 8 s | `ReachableReplicaVerifies` (R1) |
| `r2_restart` | 2 replicas, default durations, one restart, 16 instants | 2,978,948 | 1 min 24 s | `ReachableReplicaVerifies` (R1) |
| `r2_consumed` | 2 replicas, concurrent finalizes, create-and-start, one Postgres cut, one restart, 6 instants | 6,628,834 | 4 min 9 s | `ReachableReplicaVerifies` (R1) |
| `r3_consumed` | 3 replicas, concurrent finalizes, 6 instants | 4,412 | under 1 s | None |
| `r2_rowdelete` | 2 replicas, up to 3 key rows deleted, one restart, 16 instants | 5,441,633 | 1 min 50 s | None of the listed checks; the U1 and U5 properties fail as R3 states and are not listed |
| `r2_lead_not_longer` | 2 replicas, publish lead equal to the refresh interval, 10 instants | 5,162 | under 1 s | `HealthyReplicaVerifies`, `ReachableReplicaVerifies` (R2) |

Every key configuration also allows one cut of a replica's link to Postgres or to the KMS, which may heal, one forced rotation, and three key writes. Scratch checks of the same module, which are not committed, confirmed that the properties are not vacuous within these bounds: a replica mints with a second key, a stale replica mints after a key's retirement instant, a key's deletion instant passes and the sweep deletes its row, a finalize reaches `ready`, and a second concurrent finalize is refused.

### Counterexamples

- **R1. A replica whose reload fails refuses a valid token (U1, U6, confirmed gap).** In `r2_default`, replica r1's link to the KMS is cut at instant 0, while its link to Postgres works. Replica r2 forces a rotation at instant 0, which writes key 2 active from instant 4 (2 minutes later). Every reload of r1 fails, because `Ring.reload` cannot unwrap key 2 and fails as a whole. At instant 4, r2 mints s1's token with key 2. r1 can read s1's row, but `Verifier.Verify` finds no key for the token and r1 answers `401 UPLOAD_TOKEN_INVALID`, for as long as the KMS link is cut; from instant 10 r1 also stops minting, as decision 4 of change 0177 designs. In `r2_consumed` the same refusal follows a cut of r1's link to Postgres that spans the write and the activation of key 2: the link returns at instant 5 after r1's refresh at 5 failed, and r2 mints with key 2 at 6, before r1's next refresh. The text and the code were checked: §7.1 **Shared signing keys** states that a token minted on one replica verifies on any other, and **Key refresh failure** covers only a replica that cannot reach Postgres, where the row read fails too. Open decision O3, which decision 3 records as moot, and [UPLOAD-I14](../../issues/UPLOAD/UPLOAD-I14-replica-whose-key-reload-fails-refuses-valid-tokens.md).
- **R2. A publish lead not longer than the refresh interval (U1, expected).** In `r2_lead_not_longer`, with a lead equal to the refresh interval, r1 forces a rotation that activates key 2 at the next instant, and r1 mints with key 2 before r2's refresh at that instant has run, so the healthy replica r2 refuses the token. The gateway refuses to start with such a setting (`signingkeys.Open`); the configuration shows why the check exists.
- **R3. A key row deleted by hand (U1, U5, expected).** In `r2_rowdelete`, the operator deletes the row of the key that signed an unexpired token, so every replica stops verifying the token at its next reload, and deleting the only row leaves a forced rotation's key as the only key until it becomes active, so no replica can mint for one publish lead. `docs/operator-guide/security.md`, as step 0177.S4 edits it, states both effects and tells operators to rotate rather than delete rows. Under row deletion U2, U4, U6, and U7 hold. The model does not reach UPLOAD-I13, filed on branch `change/0177.S3-S4-audit-tail-and-key-incidents` (a first key written after every row is deleted skips the publish lead), separately, because the deletion of a signing key's row breaks U1 first.

### Differences between the code and the text

- **G1. A replica that has not loaded a key's retirement instant signs after it.** §7.1 **Rotation with a publish lead**, as step 0177.S4 edits it, states that "a replica never signs with a key after its retirement instant", and the same paragraph states that a replica whose last reload is as old as the stale bound can still sign with the replaced key. The code skips a key only after the retirement instant the replica's snapshot holds. The model shows that the first holds for the loaded instant (`MintNeverAfterHeldRetirement`) and that the second is bounded (`StaleMintBounded`); every such token still expires before its key's deletion instant. The sentence needs a wording fix in 0177.S4's specification edit, which [UPLOAD-I15](../../issues/UPLOAD/UPLOAD-I15-retired-key-sentence-overstates-what-a-stale-replica-does.md) records. It raises no decision.
