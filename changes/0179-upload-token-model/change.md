# A formal model of the upload-token keys and consumed state

Status: `Draft`, revision 1, 2026-11-26.

## Problem

Change [0177](../0177-shared-upload-token-keys/change.md) shares the upload-token signing keys among gateway replicas through the Postgres table `platform_signing_keys`. A replica writes the next key a publish lead before it becomes active, each replica reloads the ring on an interval, a replaced key keeps verifying for a verification time (0177 decision 7), a sweep deletes keys past that time, a replica whose last reload is older than the stale bound stops minting and keeps verifying with the ring it holds (0177 decision 4), and the first finalize sets `sessions.upload_token_consumed_at`, which consumes the token on every replica (0177 decision 3). Whether a token works depends on the order of reloads, rotations, sweeps, mints, verifications, and finalizes across replicas, and on the times between them. The build and review of 0177.S1 found one such gap by reasoning ([UPLOAD-I2](../../issues/closed/UPLOAD/UPLOAD-I2-stale-replica-mints-tokens-peers-refuse-before-expiry.md)). The owner asked on 2026-11-26 that upload tokens be verified formally before the implementation of M3 starts, "to make sure there are no gaps".

## Scope

The change builds a formal model of the upload-token key ring of each replica, the shared key table, rotation with the publish lead, the reload and its failures, the stale bound, the sweep, the minting of a token with its lifetime, its verification on any replica, and the consumed mark under concurrent finalizes. Unlike change 0175, the model includes time, as a discrete clock with the configured durations as parameters, because the properties compare the verification time with the token lifetime. It checks these properties: every token a replica mints verifies on every replica that can reload until the token expires; no token verifies after it expires or after its key's verification time; a token is consumed at most once; a replica whose last reload is older than the stale bound mints nothing; and, while Postgres is reachable, every replica has an active key to mint with. It also states what holds for a replica that cannot reload, such as whether it verifies a token signed with a key published after its last reload.

It leaves out:

- The cryptography. A signature is modelled as unforgeable, and envelope encryption under the KMS key is outside the model.
- The SQL of the store. A read or write of the key table or the consumed mark is modelled as an atomic step that may fail.

## Ordering

The change starts after step 0177.S2 lands, so the model reads the design with decisions 7, 8, and 9 of change 0177 built. The owner directed on 2026-11-26 that 0177.S2 be built first. Neither this change nor its step is started.

## Terms

| Term | Meaning |
| :-- | :-- |
| Ring | The set of upload-token signing keys a replica holds in memory, with each key's activation instant and verification time. |
| Model | The TLA+ specification this change adds under `tests/formal/`, unless decision O1 settles another tool. |
| Counterexample | A trace of the model that breaks one of the properties. |

## High-level design

To be written when the change starts.

## Requirements

None. The change adds a model and tests of the specification and changes no requirement.

## Current state

To be written when the change starts, from change 0177, its digests, and the specification and code on `main` at that time.

## Detailed design

To be written when the change starts.

## Specification and non-specification changes

None to the specification. The model and its runner are non-specification files under `tests/formal/`. A confirmed counterexample becomes an open decision of this change, and its fix lands in change 0177 or another change.

## Documentation

The `formal/` entry of `tests/README.md` names the new model.

## Tests

The model's checks are the tests. Their results are recorded in this file per configuration, as 0169 records its results.
