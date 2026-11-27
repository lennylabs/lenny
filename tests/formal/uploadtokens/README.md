# Upload-token model

`UploadTokens.tla` is a TLA+ model of the upload-token signing keys and the consumed state of §7.1 **`uploadToken` format and security properties**, as change 0177 (`changes/0177-shared-upload-token-keys/`) builds them through its decisions 11 and 13. Change 0179 (`changes/0179-upload-token-model/`) builds the model, and its `change.md` records the results and the counterexamples.

The model is a design check. No tier of `lenny-test` runs it, and no Go test reads it. A change that edits a rule the model describes updates the model and reruns it, as decision 1 of change 0179 states.

## Sources the model was read against

| Source | Branch | Commit |
| :-- | :-- | :-- |
| §7.1, `pkg/signingkeys`, `pkg/uploadtoken`, `cmd/lenny-gateway/token_signing.go`, and the session server, with steps 0177.S1 and 0177.S2 | `main` | `256e036b7` |
| Steps 0177.S3 and 0177.S4 (decision 11 of change 0177: the issued-token binding, the create-and-start consumed mark, a retired key never signs, `Insert` keeps an earlier `retire_after` and `delete_after`, and `Ring.RotateNow`) | `change/0177.S3-S4-audit-tail-and-key-incidents` | `8785d86e9` |
| Step 0177.S4, decision 13 of change 0177: a forced rotation writes nothing while the newest key is a forced rotation's key that is not active yet, and the `forced` column of `platform_signing_keys` (migration 0207) | `change/0177.S3-S4-audit-tail-and-key-incidents` | `677ea0bd6` |

A change that lands a different version of these rules compares it with the table above and edits the model where they differ.

## Plain TLA+

The module is plain TLA+ rather than PlusCal. The model is a timed transition system: the clock advances in one action, `Tick`, whose guard reads every replica (each serving replica has attempted a refresh within the refresh interval, and each stopped replica whose links are up has attempted to start). A guard over all processes is one conjunct in plain TLA+ and has no direct form in PlusCal. Without a translation there is no committed translation to keep current, so `run.sh` has no translator step.

## What the model covers

### Time

`now` is a discrete clock. Every duration is a constant counted in ticks, and the configurations use a tick of 30 seconds, so every default of §7.1 is a whole number of ticks. Several steps can run at one instant, in any order. The comparisons follow the code: a key signs from its `active_from` (at or before now) until its `retire_after` (now before it); a key verifies while its `delete_after` is after now; a token verifies while now is at or before its expiry; a replica signs while now minus its last successful reload is at most the stale bound.

| Constant | Setting | Default | Ticks |
| :-- | :-- | :-- | --: |
| `Refresh` | `--signing-key-refresh-interval` | 30 s | 1 |
| `Lead` | `--signing-key-publish-lead` | 2 min | 4 |
| `Overlap` | `uploadtoken.DefaultOverlapWindow`, also the stale bound (`Options.StaleAfter`) | 5 min | 10 |
| `Lifetime` | `maxCreatedStateTimeoutSeconds`, the token lifetime | 300 s | 10 |
| `RotInt` | `--upload-token-key-rotation-interval` | 24 h | scaled down |

The verification time of a replaced key is `VerifyFor`, `Overlap + Max(Overlap, Lifetime)`, as `signingkeys.UploadTokenVerificationTime` computes it: 20 ticks at the defaults. The configurations scale the rotation interval down to a few minutes so that scheduled rotations happen within the bound, and a forced rotation stands for a rotation at any instant.

### Actions and the code

| Action | Code |
| :-- | :-- |
| `Start` | `signingkeys.Open` (`pkg/signingkeys/ring.go`), called by `openUploadTokenKeyRing` in `cmd/lenny-gateway/token_signing.go`: list the keys, write the first key backdated by the publish lead when the purpose has none, reload, and when no key is active, `rotateIfDue` and reload again. The process stops when a step fails or when no key is active or awaiting activation. |
| `RefreshRing` | `Ring.Refresh`, run by `Ring.Run` on its ticker: `rotateIfDue` over the replica's snapshot, `Store.DeleteExpired`, and `reload`. A failed part is logged and the next part runs. |
| `RotateOn` (inside `Start` and `RefreshRing`) | `Ring.rotateIfDue`: a first key backdated by the lead when a reloaded ring is empty; the next slot one lead ahead when the newest key carries a retirement instant (0177.S4); nothing when the newest key is not active or not yet due; otherwise the next slot one lead ahead. |
| `ForcedRotate` | `Ring.RotateNow` (0177.S4), behind `POST /v1/admin/upload-token-keys/rotate` (`pkg/gateway/api/admin/upload_token_keys.go`): reload; when the newest key is a forced key that is not active yet (`PendingForced`), write nothing, which the route answers with `409 INVALID_STATE_TRANSITION` (decision 13); otherwise write the slot after the newest key one lead ahead, marked forced, and reload. |
| `WriteOn` (inside every key write) | `Ring.writeKey` and `Store.Insert` (`pkg/signingkeys/store.go`, `pkg/signingkeys/pgstore/pgstore.go`): seal the secret, insert at the slot with its `forced` mark unless the slot is taken, and in the same transaction set the previous slot's `retire_after` to the new key's `active_from` and its `delete_after` to that plus the verification time, keeping an earlier value (0177.S4). |
| `ReloadOKFor` (inside every reload) | `Ring.reload`: a failed `List`, or a failed unwrap of a key the snapshot does not hold, leaves the snapshot and the reload instant unchanged. |
| `SigningKeyOK`, `SigningKey`, `VerKeys` | `Ring.SigningKey` and `Ring.VerificationKeys`, through `uploadtoken.NewSharedKeys` (`pkg/uploadtoken/shared.go`). |
| `Mint` | `Issuer.IssueDetailed` (`pkg/uploadtoken/uploadtoken.go`) in `mintUploadToken` (`pkg/gateway/core/session/sessionserver/create.go`), and the row insert that records the token's digest and expiry. For a session of `StartSessions`, `mintClaimStartPersist` and `consumeAtStart` (`create_and_start.go`, 0177.S4) record the consumed mark in the creating write. |
| `Accepts` | `verifyUploadTokenValue` (`pkg/gateway/core/session/sessionserver/upload_seam.go`, 0177.S4): `Verifier.Verify` (the HMAC under a held key whose `delete_after` has not passed, the session id, the expiry), the row read, the digest equal to the row's, and no consumed mark. Every upload and every finalize runs it. |
| `FinStart`, `FinReady` | `handleFinalize` (`finalize_handler.go`): the token check with the `created` to `finalizing` compare-and-swap in `store.Update`, then the `finalizing` to `ready` write whose `transitionReady` sets `upload_token_consumed_at`. |
| `Terminate` | A terminate, a `DELETE`, the created-state watchdog, or the finalizing watchdog, each of which ends a session in `created` or `finalizing`. |
| `Cut`, `Heal` | A replica's link to Postgres (`"pg"`: every read and write fails) or to the KMS (`"kms"`: a key write and an unwrap of a key the replica does not hold fail; rows are read). |
| `Restart` | A process crash: the ring is lost, the replica's in-flight finalize requests end, and the process starts again at the same instant. |
| `DeleteRow` | An operator deleting a row of `platform_signing_keys` by hand, which `docs/operator-guide/security.md` says not to do. Only `r2_rowdelete.cfg` allows it. |

### Abstraction choices

- **Tokens.** A token is identified by its session, its key, and its expiry. The HMAC is deterministic, so two tokens with the same three values are the same string with the same digest. `NoAcceptAfterKeyDeletion` tries, for each session, a token signed with every key written so far and with the issued expiry, the current instant, and an expiry past the bound, which covers what a holder of a key's secret can present.
- **Atomic steps.** `Start`, `RefreshRing`, and `ForcedRotate` each run as one step. `refreshMu` serializes a replica's own refresh and forced rotation, and every effect that another replica's step has between their statements is also reached by ordering that step first: a slot another writer takes between `RotateNow`'s reload and its write sends the code to the next slot, which the model reaches by running that writer first. A mint, a finalize step, and a cut are single steps, and a finalize's two steps can be instants apart.
- **The refresh schedule.** A replica may attempt a refresh at any instant and must attempt one at least every `Refresh` ticks. This includes the exact ticker of `Ring.Run` and every earlier schedule.

### Left out

- **Cryptography.** A signature cannot be forged without the key's secret, and envelope encryption is a step that succeeds or fails.
- **The SQL.** Statements are atomic steps; the isolation level and row locks are not modelled. `Insert` runs in one transaction, and the finalize compare-and-swap is atomic, as `store.Update` makes it.
- **Clocks.** Every replica and Postgres read one clock. Clock skew, which the backdated first key and the `LEAST` of `DeleteExpired` address, is outside the model.
- **Uploads.** An upload runs the same `Accepts` check as a finalize; the upload channel, the staged content, and its close at finalize are outside the model.
- **A missing session row.** `verifyUploadTokenValue` admits a token for a session id that has no row and leaves the refusal to the caller's lookup (UPLOAD-I12 on the 0177.S3-S4 branch); the model's sessions have a row from their creation on.
- **Mid-session uploads**, which use the caller's bearer, and the idempotent replay of a create, which returns the token of the original mint.
- **Bounds.** Each configuration bounds the key writes (`MaxKeys`), the link cuts (`MaxFaults`), the restarts, the forced rotations, the row deletions, the sessions, and the instants (`MaxTime`). A key write past `MaxKeys` fails, as a write that Postgres refuses does.

## Properties

TLC checks the invariants and the action properties each configuration lists, and that no reachable state before the last instant is a deadlock.

| Property | Formula | What it states |
| :-- | :-- | :-- |
| U1 | `HealthyReplicaVerifies` | Every issued token verifies (its key is among the replica's verification keys) on every serving replica whose last successful reload is at most one refresh interval old, until the token expires. |
| U1 | `RecentReplicaVerifies` | The same on every serving replica whose last successful reload is less than one publish lead old. A verification miss on such a replica is a token no replica issued. |
| U1 | `ReachableReplicaVerifies` | The same on every serving replica that can read the session row, whatever the age of its key snapshot. This is the literal reading of "every replica that can reload". |
| U2 | `IssuedExpiresBeforeKeyDeletion` | Every issued token expires before its key's `delete_after`. |
| U2 | `NoAcceptAfterKeyDeletion` | No token that a holder of any key's secret can present, with any expiry, is accepted unless it is the issued token, and none is accepted at or after its key's `delete_after`. |
| U3 | `FinalizeOnce` | A session's token finalizes it at most once. |
| U3 | `ConsumedOnlyByFinalizeOrStart`, `ConsumedMarkKept` | The consumed mark is set only by a finalize that reached `ready` or by create-and-start, and once set it is never cleared. |
| U3 | `StartTokenRefused` | The token of a session that create-and-start created is never accepted. |
| U4 | `MintOnlyWithinStaleBound` | A replica mints only when its last successful reload is no older than the stale bound. |
| U4 | `MintNeverAfterHeldRetirement` | A replica never mints with a key past the retirement instant its snapshot holds for it. |
| U4 | `StaleMintBounded` | A replica that does not yet hold a key's retirement instant mints with that key no later than the stale bound minus the publish lead after that instant. |
| U5 | `HealthyReplicaCanMint` | Every serving replica whose last successful reload is at most one refresh interval old has a key to sign with. |
| U5 | `FreshReplicaCanMint` | Every serving replica within the stale bound of its last successful reload holds an active, unretired key. |
| U6 | `StaleReplicaVerifiesHeldKeys` | A replica that cannot reload keeps verifying every unexpired issued token whose key it holds. |
| U7 | `RetirementOnlyEarlier` | A key's `retire_after` and `delete_after` never move later once set. |
| U7 | `OneKeyPerSlot` | At most one key occupies a rotation slot. |
| U8 | `OneForcedPending` | At most one key that a forced rotation wrote is not active yet, so forced rotations add at most one key row per publish lead (decision 13 of change 0177). |

`MintOnlyWithinStaleBound`, `MintNeverAfterHeldRetirement`, `StartTokenRefused`, and the first half of `NoAcceptAfterKeyDeletion` hold by the construction of the guard they restate; they pin the guard to the property it carries.

## Configurations

| Configuration | Replicas | Durations in ticks of 30 s | Instants | Environment and sessions |
| :-- | --: | :-- | --: | :-- |
| `r2_default.cfg` | 2 | refresh 1, lead 4, overlap 10, lifetime 10, rotation 6 | 28 | one session; one cut (Postgres or KMS); one forced rotation |
| `r3_default.cfg` | 3 | as `r2_default` | 14 | as `r2_default` |
| `r2_longtoken.cfg` | 2 | lifetime 20 (10 min), so a replaced key verifies for 30 ticks; others as `r2_default` | 36 | as `r2_default` |
| `r2_shortlead.cfg` | 2 | refresh 2, lead 3, overlap 4, lifetime 6, rotation 4 | 18 | as `r2_default` |
| `r3_shortlead.cfg` | 3 | as `r2_shortlead` | 16 | as `r2_default` |
| `r2_restart.cfg` | 2 | as `r2_default` | 16 | as `r2_default`, and one restart |
| `r2_consumed.cfg` | 2 | as `r2_default` | 6 | s1 by `POST /v1/sessions`, finalized on both replicas; s2 by `POST /v1/sessions/start`; one Postgres cut; one restart |
| `r3_consumed.cfg` | 3 | as `r2_default` | 6 | s1 finalized on three replicas; no cut |
| `r2_rowdelete.cfg` | 2 | as `r2_default` | 16 | up to three key rows deleted by hand; one restart; one forced rotation; no cut |
| `r2_lead_not_longer.cfg` | 2 | lead 1, equal to the refresh; others as `r2_default` | 10 | one forced rotation; no cut |
| `r2_forced.cfg` | 2 | as `r2_default` | 16 | one session; three forced rotations on either replica; 5 key writes; no cut |

Each configuration but `r2_forced` allows 3 key writes. Replicas are symmetric (`SYMMETRY Symmetry`). The horizons of `r2_default`, `r2_longtoken`, and `r2_shortlead` reach the sweep of a replaced key; the others stop before it. One configuration with every environment event and both sessions grows past 45 million states within twelve instants, which is why the concerns are split. `r2_rowdelete` lists no U1 or U5 property: a deleted row's key stops verifying, and a replica with no active key mints nothing, as the operator guide states. `r2_lead_not_longer` sets a publish lead the gateway refuses at start, and its U1 properties are expected to fail.

## Running it

The runner needs the `tla2tools.jar` of TLC `v1.7.4`, from `https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar`, and Java 11 or later. It checks the jar's SHA-256 against `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88` and stops when it differs. It runs TLC on the host's Java: `TLC_JAVA` names the `java` binary, else `$JAVA_HOME/bin/java`, else `java` on the `PATH`. `TLC_DOCKER=1` runs TLC in an `eclipse-temurin:21-jdk` container with no network instead, with the model directory and the jar mounted read-only.

```sh
tests/formal/uploadtokens/run.sh <path-to-tla2tools.jar> <output-dir> <configuration> [property ...]
tests/formal/uploadtokens/run.sh <path-to-tla2tools.jar> <output-dir> all
```

With no property named, TLC checks every invariant and property the configuration lists; with properties named, it checks only those. `all` runs every configuration in turn. TLC stops at the first violation, so a run that finds one is repeated with the other checks named. `TLC_WORKERS` sets the worker count (default 8), `TLC_HEAP` the JVM heap (default 12g), `TLC_NODEADLOCK=1` turns off TLC's deadlock check, and with `TLC_DOCKER=1`, `TLC_DETACH=1` starts each container detached. The output directory receives the effective configuration, TLC's log, and a receipt with the TLC release, the jar's SHA-256, the checks, and the start and finish times.

The model belongs to no gate and is run ad hoc. On the owner's workstation, take the shared lock before a run and stop any run at one hour, as `.claude/rules/working-practices.md` states: create `scratchpad/gate.lock` with `mkdir` (retry until it succeeds), and remove it with `rmdir` afterwards.

## Results

Step 0179.S1 ran every configuration on 2026-11-27 with TLC `v1.7.4` (`tla2tools.jar` SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`), 2 workers per run, and the bounds above. Every run finished. A run that found a violation was repeated with the configuration's other checks until a run explored the whole state space; the states and time are those of that run.

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `r2_default` | 4,177,847 | 1 min 47 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r3_default` | 1,051,305 | 43 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r2_longtoken` | 8,334,751 | 3 min 44 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r2_shortlead` | 5,706,754 | 2 min 35 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r3_shortlead` | 9,514,847 | 7 min 8 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r2_restart` | 2,978,948 | 1 min 24 s | `ReachableReplicaVerifies` (R1, KMS cut) |
| `r2_consumed` | 6,628,834 | 4 min 9 s | `ReachableReplicaVerifies` (R1, Postgres cut) |
| `r3_consumed` | 4,412 | under 1 s | None |
| `r2_rowdelete` | 5,441,633 | 1 min 50 s | None of the listed checks |
| `r2_lead_not_longer` | 5,162 | under 1 s | `HealthyReplicaVerifies`, `ReachableReplicaVerifies` (R2, expected) |

| Property | Result |
| :-- | :-- |
| U1 | `HealthyReplicaVerifies` and `RecentReplicaVerifies` hold wherever the publish lead is longer than the refresh interval. `ReachableReplicaVerifies` fails wherever a link is cut (R1, open decision O3 of change 0179, UPLOAD-I14). |
| U2 | Holds, also under row deletion. |
| U3 | Holds. |
| U4 | Holds, also under row deletion. A replica that has not loaded a key's retirement instant does sign with the key after that instant, within the bound `StaleMintBounded` states (UPLOAD-I15). |
| U5 | Holds wherever no key row is deleted. |
| U6 | `StaleReplicaVerifiesHeldKeys` holds. A replica that cannot reload does not verify a token signed with a key published after its last successful reload (R1). |
| U7 | Holds, also under row deletion. |

### Rerun for decision 13 of change 0177

Step 0177.S4 added the bound on forced rotations (`PendingForced` in `ForcedRotate`), the `forced` mark of each key, the invariant `OneForcedPending`, and `r2_forced.cfg`, and reran every configuration on 2026-11-27 with the same TLC release and jar, 2 workers per run, on the module at `677ea0bd6`. A first run of each configuration with its listed checks found the same violations as before, and `r2_forced`, `r2_rowdelete`, and `r3_consumed` explored the whole state space. Each other configuration was rerun with its listed checks except `ReachableReplicaVerifies`, and `r2_lead_not_longer` also without `HealthyReplicaVerifies`, and each of those runs explored the whole state space and found no error. The `forced` mark separates keys that a scheduled and a forced rotation write at the same slot and instant, so most configurations reach more distinct states than before.

| Configuration | Distinct states | Time | Violated |
| :-- | --: | --: | :-- |
| `r2_default` | 4,708,447 | 2 min 7 s | `ReachableReplicaVerifies` (R1) |
| `r3_default` | 1,137,036 | 49 s | `ReachableReplicaVerifies` (R1) |
| `r2_longtoken` | 9,427,967 | 4 min 19 s | `ReachableReplicaVerifies` (R1) |
| `r2_shortlead` | 7,120,722 | 3 min 18 s | `ReachableReplicaVerifies` (R1) |
| `r3_shortlead` | 11,593,783 | 8 min 45 s | `ReachableReplicaVerifies` (R1) |
| `r2_restart` | 3,335,300 | 1 min 36 s | `ReachableReplicaVerifies` (R1) |
| `r2_consumed` | 6,628,834 | 4 min 17 s | `ReachableReplicaVerifies` (R1) |
| `r3_consumed` | 4,412 | under 1 s | None |
| `r2_rowdelete` | 5,915,353 | 2 min 3 s | None of the listed checks |
| `r2_lead_not_longer` | 5,162 | under 1 s | `HealthyReplicaVerifies`, `ReachableReplicaVerifies` (R2, expected) |
| `r2_forced` | 321,198 | 15 s | None |

U8 (`OneForcedPending`) holds in `r2_forced`, and every other property listed there holds. The results by property above are unchanged. Two scratch checks of `r2_forced` confirmed that the bound is reached: a second forced rotation at the instant of the first writes nothing, and a forced rotation writes after a scheduled key that is not active yet.

Scratch checks of the same module confirmed that the properties are not vacuous: within the bounds, a replica mints with a second key, a stale replica mints after a key's retirement instant, a key's deletion instant passes and the sweep deletes its row, a finalize reaches `ready`, and a second concurrent finalize is refused.
