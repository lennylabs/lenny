# Store conformance suites and router contracts

Status: `Draft`, revision 1, 2026-11-16. Programs: [v1](../programs/v1.md), area milestones.

## Problem

A sample of 80 unit tests found 77.5 percent testing logic or in-process behavior, so the unit tests stay. Two weak spots stand out. 41 store packages have a memory and a Postgres implementation, and their unit tests round-trip the memory one only, so the two can diverge with no test failing. The CLI clients are tested against hand-written servers, so a change to the production gateway or ops routers can break a client with no test failing ([test infrastructure review](../archive/2026-11-17-0154-merge-gate/evidence/test-infrastructure-review.md), "Question 2").

## Scope

- **In scope.** One conformance suite per store interface that has both implementations, run against the memory implementation in the unit tier and against Postgres in the component tier. Contract tests that run `pkg/ctlcli` and `pkg/embedded/localcli` against the production gateway and ops routers. The round-trip and canned-server unit tests the suites replace, deleted.
- **Out of scope.** Stores with one implementation, and clients outside the repository.

## High-level design

Each suite is a function that takes a constructor for the store and runs the interface's behaviors, including its error, boundary, and concurrent paths, so both implementations run the same cases. The suites land with the area that owns each store, so an area's milestone exit includes its stores' suites.

## Current state

41 packages under `pkg/` implement one store interface in memory and in Postgres.

## Specification and non-specification changes

No specification text changes. Non-specification: a conformance package per store interface, the contract tests, and the deleted tests.

## Documentation

Contributors: `TESTING.md` describes the conformance suites and when a new store needs one.

## Tests

The suites themselves, run in the unit and component tiers.
