# A formal model of the audit chain and redaction receipts

Status: `Draft`, revision 1, 2026-11-26.

## Problem

The audit log is a hash chain per tenant (§11.7): each row records the hash of the row before it, and a chain verifier and a periodic scan check the links. A GDPR erasure (§12.8 step 14) rewrites the payload of the rows that name the erased subject and writes a redaction receipt that records the row's hash before (`original_hash`) and after (`new_hash`), signed inside the KMS under `platform:audit-signing` (change [0177](../0177-shared-upload-token-keys/change.md), decision 5). The verifiers accept a redacted row whose receipt verifies against a trusted key (0177 decision 9) and whose content matches the signed `new_hash` (0177 decision 8). The guarantees depend on who can write which rows and columns, on concurrent appends and redactions, and on key rotations across a retention period of years. The review of 0177.S1 found a gap by reasoning ([AUDIT-I5](../../issues/closed/AUDIT/AUDIT-I5-receipt-check-ignores-the-signed-new-hash.md)). The owner asked on 2026-11-26 that the audit mechanisms be verified formally before the implementation of M3 starts, "to make sure there are no gaps".

## Scope

The change builds a formal model of the audit chain of a tenant, appends from several replicas, redactions with their receipts, rotations of the receipt signing key and the trusted key set, the chain verifier, and the periodic scan, against an adversary with each of the database roles the specification names: the application role, the role holding the `lenny_erasure` grant, and a database administrator. It checks these properties: concurrent appends keep one linear chain; a rewrite, deletion, insertion, or reordering of a row is detected unless it is a redaction with a valid receipt; a redacted row whose content differs from its receipt's `new_hash` is detected; a receipt signed by a key outside the trusted set is refused; every receipt signed by a trusted key verifies across rotations for the retention period; and the model states which tampering each role can do undetected, such as deleting the newest rows of a chain.

It leaves out:

- The cryptography. A hash is modelled as collision-free and a signature as unforgeable.
- The content of the audit events and the OCSF mapping, which the model treats as opaque payloads.

## Ordering

The change starts after step 0177.S2 lands, so the model reads the design with decisions 7, 8, and 9 of change 0177 built. The owner directed on 2026-11-26 that 0177.S2 be built first. Neither this change nor its step is started.

## Terms

| Term | Meaning |
| :-- | :-- |
| Receipt | The record of a redaction that §12.8 step 14 defines, with `original_hash`, `new_hash`, the signing key, and the signature. |
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
