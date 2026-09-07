---
proposal: 0075_fix_derive-message-scope-from-the-address-type
title: Derive message scope from the address type
kind: fix
status: Draft
drafted-date: 2026-08-19
drafted-by: 
reviewed-date: 
reviewed-by: 
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

# Proposal: Derive message scope from the address type

## Status, date, and scope as the original recorded them

- **Status:** Draft for review.
- **Date:** 2026-08-19. Rewritten 2026-09-06 against proposal 0076, which removes the ground this proposal
  originally gave for its central exception, and again once 0076's OD3 was answered. §11 records what the
  rewrites changed.
- **Scope:** Replaces proposal 0073's declared message-scope table with a derivation rule. One request
  message on the gateway-to-adapter contract carried a session identifier the specification classified as
  a guard rather than an address, and that single message is the whole reason 0073 declares the
  classification in a table and gates the table against the proto. The reviewer's answer to proposal
  0076's OD3 reclassifies that message as session-scoped, which leaves the rule without a counterexample,
  so retiring the table turns 32 rows and a gate into a rule the proto can be checked against. Sequences
  after 0073 and after 0076, and changes nothing 0073 states about how the adapter resolves a root.

## Review history

An adversarial review run evaluated the specification changes against the open questions on 2026-09-07. The run executed three rounds against the spec loop and did not converge. Two full-pool sweeps occurred across the three rounds. No findings were fixed. The specification review did not settle the open questions, which remain unresolved. The non-spec loop was not run.
