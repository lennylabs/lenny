---
proposal: 0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod
title: Name who starts the next session's runtime on a recycled pod
kind: fix
status: Draft
drafted-date: 2026-08-25
drafted-by: 
reviewed-date: 
reviewed-by: 
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Original status block

# Proposal: Name who starts the next session's runtime on a recycled pod

- **Status:** Draft for review.
- **Date:** 2026-08-25
- **Scope:** Closes BUILD-GAPS F-5.2.33 part (b). The specification promises a fresh runtime process for every session on a recycling pod and names no component that creates it under the §4.7.10 sidecar deployment model, which is the default and the model §4.7.10 tells third-party authors to use. This proposal names the creator under each deployment model, records that the sidecar model has none, converts the resulting silent single-session failure into a specified pod retirement at each occupancy-zero boundary, states what the §5.2 whole-pod scrub reaches when the process namespace is not shared, reconciles §4.7.9 step 7 with §4.7.10, and makes the tier-10 and tier-5 cases able to fail. Part (a), the pod-scoped listener teardown, is proposal 0078 and lands first.

This document stages the proposed specification, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the Proposed changes section after sign-off.
