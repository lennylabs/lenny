# Persona journey tier

Status: `Decided`, revision 2, 2026-11-16. Programs: [v1](../programs/v1.md), milestone M2.

## Problem

No tier checks that Lenny works as a person would use it: an operator installing and configuring it, a tenant admin setting up a tenant, an end user running sessions, or a runtime author connecting a runtime, each following the documentation. Agent-driven testing of this kind exists in practice, and it is trustworthy when the harness decides each verdict from deterministic checks of the system's state, keeps transcripts, and has a person review every failure ([test infrastructure review](../archive/2026-11-17-0154-merge-gate/evidence/test-infrastructure-review.md), "Question 3").

## Scope

- **In scope.** `tests/journeys/` with a scenario format, a `journeys` tier in `lenny-test` with Kind provisioning, a restricted toolset for the agent, deterministic postconditions that decide the verdict, three trials per journey, transcript and verdict records under `tests/results/journeys/`, a review procedure for failures, three pilot journeys, and a measured cost per journey with a run budget set before more are added. The tier is named in `TESTING.md` and in the release gate.
- **Out of scope.** Journeys for every area, which each area adds at its milestone once the pilot sets the budget.

## High-level design

A journey is a written scenario for one persona: the steps a person would take, in the words of the documentation, and the postconditions that must hold afterwards, checked by the harness against the cluster and the APIs. An agent with a restricted toolset follows the steps; the harness, not the agent, decides pass or fail. Each journey runs three times, and a person reviews every failure from its transcript. The tier runs on a schedule and before releases, outside the merge gate.

## Current state

No journey tier exists. Tier 5 runs end-to-end tests on Kind written as Go tests.

## Specification and non-specification changes

No specification text changes. Non-specification: `tests/journeys/`, `cmd/lenny-test`, and `TESTING.md`.

## Documentation

Contributors: `TESTING.md` describes the journey format and how to add a journey. The pilot journeys test the operator and user documentation they follow, and each failure that traces to a page is an issue on that page.

## Tests

The pilot journeys, three trials each, with their cost recorded.
