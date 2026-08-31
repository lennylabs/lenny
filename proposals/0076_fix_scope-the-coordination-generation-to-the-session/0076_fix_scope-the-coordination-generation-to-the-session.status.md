---
proposal: 0076_fix_scope-the-coordination-generation-to-the-session
title: Scope the coordination generation to the session
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

## Original header

# Proposal: Scope the coordination generation to the session

- **Status:** Draft for review.
- **Date:** 2026-08-19
- **Scope:** The specification scopes the coordination generation to the session and the adapter stores it
  per pod. On a pod running one session at a time the two are indistinguishable. On a pod running several,
  one session's coordinator handoff fences out another session's legitimate coordinator, rejects its
  drain barrier, and releases its coordinator-loss hold. The defect is masked today by a broken session
  guard that fails closed, and proposal 0073 repairs that guard.

This document stages the proposed specification, schema, and code changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

**This draft has not been through adversarial review.** It states a defect with its evidence and frames the
design question rather than answering it. The hold-state decision in §7 is genuinely open and is the
substance of this change. Run the change-proposal convergence loop on it before sign-off.
