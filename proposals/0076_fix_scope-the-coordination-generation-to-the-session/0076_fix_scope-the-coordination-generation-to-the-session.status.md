---
proposal: 0076_fix_scope-the-coordination-generation-to-the-session
title: Scope the coordination generation to the session
kind: fix
status: Reviewed
drafted-date: 2026-08-19
drafted-by: 
reviewed-date: 2026-09-02
reviewed-by: change-proposal
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Original header

# Proposal: Scope the coordination generation to the session

- **Status:** Reviewed.
- **Date:** 2026-08-19
- **Scope:** The specification scopes the coordination generation to the session and the adapter stores it
  per pod. On a pod running one session at a time the two are indistinguishable. On a pod running several,
  one session's coordinator handoff fences out another session's legitimate coordinator, rejects its
  drain barrier, and releases its coordinator-loss hold. The defect is masked today by a broken session
  guard that fails closed, and proposal 0073 repairs that guard.

This document stages the proposed specification, schema, and code changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## Review history

On 2026-09-02, the adversarial review ran the spec convergence loop on this proposal. The loop ran three rounds and did not converge. It fixed one finding and ran no full-pool sweeps. Findings the loop had not closed remain open. The non-spec convergence loop was not run.
